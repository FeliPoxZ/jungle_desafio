// Package inbox é a porta de entrada por SQS: converte a mensagem do broker na
// MESMA requisição que o HTTP produz e garante a deduplicação durável.
//
// # Por que ele existe separado de `infra/sqs` e de `app/wagering`
//
//   - `infra/sqs` fala o protocolo do broker (receber, apagar, visibility);
//   - `app/wagering` decide a regra financeira e não conhece broker;
//   - este pacote é a TRADUÇÃO e a POLÍTICA de deduplicação: o que fazer com uma
//     mensagem repetida, com o corpo divergente, com o campo inválido e com a
//     rejeição de negócio.
//
// Ele depende de `ports` (contratos), nunca de `infra`: o teste roda com dublês, e
// trocar SQS por outro broker não toca nesta lógica.
package inbox

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/platform/canonicaljson"
)

// ConsumerName identifica ESTE consumidor no registro da inbox.
//
// A unicidade é `(consumer_name, message_id)`, e não `message_id` sozinho: dois
// consumidores diferentes (o da fila de entrada e, no futuro, o de outra fila)
// podem receber mensagens com o mesmo identificador, e cada um precisa do seu
// próprio registro. Sem o nome, o segundo consumidor acharia que já tratou.
const ConsumerName = "wager-transactions"

// Statuses da inbox (espelham o CHECK da tabela).
const (
	statusProcessed = "PROCESSED"
	statusRejected  = "REJECTED"
)

// Erros de classificação da mensagem.
//
// A distinção é o que decide o destino dela: transitório volta para a fila,
// permanente vai para a DLQ. Errar essa classificação é caro nos dois sentidos —
// mandar um erro permanente para retry infinito trava a fila, e mandar um
// transitório para a DLQ perde trabalho que só precisava de nova tentativa.
var (
	// ErrMensagemInvalida é o corpo que não é uma mensagem: JSON malformado,
	// campo obrigatório ausente, tipo desconhecido, `OPENING` vindo de fora.
	// Nenhuma tentativa vai consertar isso.
	ErrMensagemInvalida = errors.New("inbox: mensagem inválida")

	// ErrCorpoDivergente é a mesma identidade de mensagem com conteúdo diferente.
	ErrCorpoDivergente = errors.New("inbox: corpo divergente para a mesma mensagem")

	// ErrOperacaoInvalida é a operação recusada por ENTRADA (nada foi gravado).
	//
	// É separado de `ErrMensagemInvalida` porque a mensagem está bem formada e o
	// problema é o conteúdo de negócio — mas o destino é o mesmo: a mesma
	// mensagem reenviada continuaria inválida, então repetir não resolve.
	ErrOperacaoInvalida = errors.New("inbox: operação recusada por entrada inválida")
)

// Resultado é o que o consumidor precisa para decidir o destino da mensagem.
type Resultado struct {
	// Processada diz que o tratamento durável terminou (commit) e a mensagem
	// pode ser REMOVIDA da fila.
	Processada bool

	// Duplicada diz que a mensagem já havia sido tratada; a mensagem também pode
	// ser removida (nada foi feito desta vez).
	Duplicada bool

	// Transitoria diz que a falha pode ser resolvida repetindo: a mensagem NÃO
	// deve ser removida.
	Transitoria bool

	// Desfecho da operação, quando ela chegou a ser decidida.
	Status      string
	FailureCode string

	// Replay diz que o caso de uso reconheceu a operação como já aplicada pela
	// chave de idempotência (a porta HTTP já a tinha processado, por exemplo).
	Replay bool
}

// Processador liga a mensagem do broker ao caso de uso.
//
// A dependência é a INTERFACE do caso de uso (`Aplicador`), e não o `*Service`:
// o teste do processador não precisa subir banco, e o acoplamento fica explícito
// no que ele realmente usa.
type Processador struct {
	servico Aplicador
}

// Aplicador é o contrato do caso de uso que a inbox usa.
//
// Declarado aqui (no consumidor): é o que o processador precisa, e nada além.
type Aplicador interface {
	ProcessWithHooks(ctx context.Context, req appwagering.Request, hooks *appwagering.ProcessHooks) (appwagering.Result, error)
}

// NovoProcessador monta o processador.
func NovoProcessador(servico Aplicador) *Processador {
	return &Processador{servico: servico}
}

// Entrada é a mensagem crua, como o broker entrega.
type Entrada struct {
	// IDDaEntrega é o identificador INTERNO do broker (o `MessageId` do SQS).
	//
	// Ele muda a cada envio, então NÃO serve como identidade durável: um reenvio
	// pelo produtor cria outro identificador, e a deduplicação por ele deixaria a
	// segunda entrega passar. Ele é usado para log e para o
	// `MessageDeduplicationId` da DLQ.
	IDDaEntrega string

	// Corpo é o corpo JSON, exatamente como chegou.
	Corpo []byte
}

// Processar trata uma mensagem e diz o que fazer com ela.
//
// # A ordem, e por que ela não pode mudar
//
//  1. o envelope é decodificado e VALIDADO — mensagem inválida é PERMANENTE
//     (DLQ), e é decidido ANTES de abrir transação: não há por que registrar na
//     inbox uma mensagem que jamais será aplicada;
//  2. o corpo é HASHEADO: o hash identifica o CONTEÚDO, e é ele que distingue
//     reentrega (mesmo hash) de conflito real (hash diferente);
//  3. o caso de uso roda com os ganchos, que registram a inbox na MESMA
//     transação do saldo, do ledger e dos eventos (README §6.5).
//
// A identidade usada na inbox é o `messageId` DO ENVELOPE (README §10), e não o
// identificador da entrega — ver `operacao.MessageID`.
//
// O que NÃO acontece aqui: remover a mensagem da fila. Quem remove é o
// consumidor, e só depois do commit — é a diferença entre "processei" e "avisei
// que processei".
func (p *Processador) Processar(ctx context.Context, entrada Entrada) (Resultado, error) {
	idDaEntrega := strings.TrimSpace(entrada.IDDaEntrega)

	hashCorpo, err := canonicaljson.Hash(map[string]any{"sha256": digestDoCorpo(entrada.Corpo)})
	if err != nil {
		return Resultado{}, fmt.Errorf("inbox: hash do corpo: %w", err)
	}

	op, err := decodificar(entrada.Corpo, idDaEntrega)
	if err != nil {
		return Resultado{}, err
	}

	// A identidade durável é a do BROKER (`MessageId`), que é quem gera o valor no
	// envio. O `messageId` do envelope, quando vem, já foi conferido contra ela.
	//
	// A chave de idempotência FINANCEIRA, por outro lado, vem do corpo
	// (`data.idempotencyKey`, README §10) — não do `MessageDeduplicationId` do
	// broker nem do identificador da mensagem. O broker deduplica ENTREGA; a chave
	// financeira impede o DÉBITO repetido, e precisa ser a MESMA que o HTTP usou.
	ganchos := p.ganchos(idDaEntrega, hashCorpo)

	res, err := p.servico.ProcessWithHooks(ctx, op.Request, ganchos)
	if err != nil {
		return p.classificarErro(err, res)
	}

	return Resultado{
		Processada:  true,
		Status:      res.Status.String(),
		FailureCode: res.FailureCode,
		Replay:      res.IdempotentReplay,
		Duplicada:   res.IdempotentReplay,
	}, nil
}

// ganchos monta o registro da inbox dentro da transação da operação.
//
// `messageID` é o do ENVELOPE: é ele a identidade durável da mensagem para este
// consumidor.
func (p *Processador) ganchos(messageID, hashCorpo string) *appwagering.ProcessHooks {
	return &appwagering.ProcessHooks{
		OnBegin: func(ctx context.Context, r ports.Repos) error {
			// A leitura prévia existe para dar mensagem boa: o BANCO é quem
			// decide a corrida (o INSERT com ON CONFLICT). É o mesmo desenho do
			// resto do projeto — consultar para explicar, índice para decidir.
			existente, err := r.Inbox.Get(ctx, ConsumerName, messageID)
			switch {
			case err == nil:
				if existente.BodyHash != hashCorpo {
					return fmt.Errorf("%w: messageId %s (hash %s × %s)",
						appwagering.ErrInboxBodyConflict, messageID, existente.BodyHash, hashCorpo)
				}
				// Mesma mensagem, já com tratamento durável confirmado: nada a
				// fazer ALÉM de contar a entrega.
				//
				// A contagem é o único efeito desta passagem, e é deliberado: é
				// ela que registra que a mensagem foi entregue mais de uma vez —
				// informação que só existe aqui, porque o
				// `ApproximateReceiveCount` do broker se perde quando a mensagem
				// é removida. O incremento é `attempts + 1` no SQL (duas
				// instâncias somam as duas entregas em vez de gravar o mesmo
				// valor).
				if err := r.Inbox.CountAttempt(ctx, ConsumerName, messageID, agora()); err != nil {
					return fmt.Errorf("inbox: contando a reentrega de %s: %w", messageID, err)
				}
				// O caso de uso traduz isto em replay: transação encerrada sem
				// mudar nada do domínio.
				return appwagering.ErrIdempotentSkip
			case !ports.IsNotFound(err):
				return err
			}

			_, err = r.Inbox.InsertIfAbsent(ctx, ports.InboxMessage{
				ID:           novoID(),
				ConsumerName: ConsumerName,
				MessageID:    messageID,
				BodyHash:     hashCorpo,
				Status:       "RECEIVED",
				ReceivedAt:   agora(),
			})
			if err != nil {
				return fmt.Errorf("inbox: registrando a mensagem %s: %w", messageID, err)
			}
			return nil
		},
		OnCommit: func(ctx context.Context, r ports.Repos, res appwagering.Result) error {
			// A conclusão entra no MESMO commit do saldo, do ledger e dos
			// eventos. Rejeição de negócio também é conclusão: ela é terminal e
			// permite remover a mensagem (README §10).
			if res.Status.String() == "REJECTED" {
				return r.Inbox.MarkRejected(ctx, ConsumerName, messageID, res.FailureCode, agora())
			}
			return r.Inbox.MarkProcessed(ctx, ConsumerName, messageID, agora())
		},
	}
}

// classificarErro decide se a falha é transitória (volta para a fila) ou
// permanente (vai para a DLQ).
//
// # A régua
//
//	transitório  -> falha de infraestrutura: banco fora, lock disputado, conexão.
//	                Repetir resolve, e `ports.IsRetryable` é quem sabe disso.
//	permanente   -> o corpo nunca vai mudar: entrada inválida, hash divergente.
//	                Repetir para sempre é envenenar a fila.
//
// Uma rejeição de NEGÓCIO não chega aqui: ela é desfecho, não erro — o caso de uso
// a devolve em `Result` e a mensagem é removida.
func (p *Processador) classificarErro(err error, res appwagering.Result) (Resultado, error) {
	if errors.Is(err, appwagering.ErrInboxBodyConflict) {
		return Resultado{}, fmt.Errorf("%w: %w", ErrCorpoDivergente, err)
	}

	// Operação recusada por ENTRADA: nada foi gravado, e a mesma mensagem
	// reenviada continuaria inválida.
	if code := codigoDeEntrada(err); code != "" {
		return Resultado{}, fmt.Errorf("%w (%s): %w", ErrOperacaoInvalida, code, err)
	}

	if ports.IsRetryable(err) || ports.IsConflict(err) {
		return Resultado{
			Transitoria: true,
			Status:      res.Status.String(),
			FailureCode: res.FailureCode,
		}, nil
	}

	// Erro que não se sabe classificar: a escolha segura é tentar de novo. A DLQ
	// é o fim da linha; mandar para lá algo que talvez funcionasse perde o
	// trabalho. O limite de recebimentos do broker garante que a tentativa não é
	// infinita.
	return Resultado{Transitoria: true}, nil
}
