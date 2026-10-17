package wagering

import (
	"context"
	"strings"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ledger"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
)

// DesfechoDaRetomada diz o que aconteceu com a transação que o worker retomou.
//
// O desfecho existe para o worker poder LOGAR e MEDIR cada caso separadamente:
// "resolvida" e "pendente" são caminhos distintos, "rejeitada" é o TTL vencendo
// (ou a referência incompatível) e "ignorada" é a disputa — outra instância
// chegou primeiro. Tratar os quatro como "ok" apagaria a informação que o
// operador precisa.
type DesfechoDaRetomada string

const (
	// RetomadaResolvida: a referência estava disponível e a operação concluiu,
	// com movimento, lançamento e eventos.
	RetomadaResolvida DesfechoDaRetomada = "resolvida"

	// RetomadaPendente: a referência ainda não chegou (ou não terminou). A
	// operação continua em PENDING_REFERENCE, com a tentativa incrementada e um
	// novo instante agendado.
	RetomadaPendente DesfechoDaRetomada = "pendente"

	// RetomadaRejeitada: a operação virou REJECTED, com `failureCode` estável.
	// Acontece quando a referência é incompatível, quando ela terminou sem
	// sucesso e quando o TTL/máximo de tentativas vence (REFERENCE_NOT_FOUND).
	RetomadaRejeitada DesfechoDaRetomada = "rejeitada"

	// RetomadaIgnorada: nada foi feito, e isso é o esperado. A linha já está em
	// estado terminal (outra instância concluiu entre o claim e o lock) ou a
	// janela desta tentativa ainda não chegou (outra instância está com ela).
	RetomadaIgnorada DesfechoDaRetomada = "ignorada"
)

// PoliticaDeReferencia é o que o worker decide e o caso de uso aplica.
//
// # Por que os números vêm de fora
//
// O BACKOFF (quanto esperar) é política de laço: depende de configuração e de um
// gerador de jitter, e é testável sem banco. A TRANSIÇÃO (gravar a tentativa e o
// instante na linha) é regra de estado, e acontece aqui dentro, na mesma
// transação do resto. É a mesma divisão do consumidor do SQS, que calcula o
// adiamento e deixa o domínio decidir o desfecho.
type PoliticaDeReferencia struct {
	// MaxAttempts é o teto de tentativas de resolução (0 = sem teto).
	MaxAttempts int

	// PendingMaxAge é o TTL da pendência, medido da CRIAÇÃO da operação
	// (0 = sem TTL).
	PendingMaxAge time.Duration

	// NextAttemptAt é o instante da próxima tentativa, já com backoff e jitter.
	// Vazio significa "use o intervalo padrão".
	NextAttemptAt time.Time
}

// RetomarRequest é o pedido de retomada de UMA transação persistida.
type RetomarRequest struct {
	TransactionID wagerdomain.TransactionID
	Politica      PoliticaDeReferencia
}

// Retomada é o desfecho da retomada, com o resultado da operação quando ela
// concluiu e o motivo quando ela foi registrada como falha permanente.
type Retomada struct {
	Desfecho DesfechoDaRetomada
	Result   Result
	Motivo   string
}

// Retomar conclui uma transação que ficou persistida esperando uma referência
// (PENDING_REFERENCE) ou presa em PENDING.
//
// # Por que este caminho é SEPARADO do `Process`
//
// O `Process` começa pela idempotência e pelo INSERT: ele CRIA a operação. A
// retomada encontra a linha já gravada, com o payload e o hash do pedido
// original, e não pode reescrever nada disso — ela só leva a operação ao
// desfecho. Reconstruir a `Request` das colunas persistidas é o que permite
// reusar `resolverReferencia`, `mover` e os emissores de evento, em vez de
// duplicar a régua de negócio (duas réguas divergem com o tempo).
//
// # As duas guardas de concorrência, e por que as duas
//
//  1. `GetByIDForUpdate` — o lock da LINHA. Dois workers podem receber a mesma
//     transação (o claim do reaper não tem lease, e o do worker de referências
//     pode vencer no meio do processamento). Em `READ COMMITTED` o `FOR UPDATE`
//     espera o lock e devolve a versão MAIS NOVA da linha: quem chega segundo vê
//     o estado terminal que o primeiro gravou e não faz nada. Sem esta releitura,
//     o segundo aplicaria a reversão de novo — dinheiro criado do nada;
//  2. a janela do agendamento — `next_resolution_attempt_at` no futuro significa
//     que outra instância reivindicou a linha (o worker empurra esse instante no
//     claim) ou que esta tentativa já foi agendada. É o que impede o teto de
//     tentativas de ser consumido duas vezes mais rápido.
//
// O lock da carteira continua sendo a coordenação final do DINHEIRO: ele é tomado
// depois das duas guardas e antes de qualquer leitura de saldo.
func (s *Service) Retomar(ctx context.Context, req RetomarRequest) (Retomada, error) {
	if strings.TrimSpace(string(req.TransactionID)) == "" {
		return Retomada{}, invalid(CodeInvalidRequest, "retomada exige o identificador da transação")
	}

	var retomada Retomada
	err := s.comRetentativa(ctx, func(ctx context.Context) error {
		var e error
		retomada, e = s.retomar(ctx, req)
		return e
	})
	if err != nil {
		return Retomada{}, err
	}
	return retomada, nil
}

// retomar abre a transação e delega ao miolo.
func (s *Service) retomar(ctx context.Context, req RetomarRequest) (Retomada, error) {
	var retomada Retomada

	err := s.uow.Run(ctx, func(ctx context.Context, r ports.Repos) error {
		tx, err := r.Transactions.GetByIDForUpdate(ctx, req.TransactionID)
		switch {
		case ports.IsNotFound(err):
			// A linha sumiu (limpeza, correção manual). Não é erro: não há o que
			// retomar.
			retomada = Retomada{Desfecho: RetomadaIgnorada}
			return nil
		case err != nil:
			return err
		}

		var e error
		retomada, e = s.retomarTransacao(ctx, r, tx, req.Politica)
		return e
	})
	if err != nil {
		return Retomada{}, err
	}
	return retomada, nil
}

// retomarTransacao é a ordem exata dentro da transação de retomada.
//
// O lock da carteira é tomado o MAIS TARDE possível: uma retomada que só vai
// reagendar (a referência ainda não chegou) não tem por que bloquear a carteira —
// e bloquear por nada é o que transforma um worker em gargalo do serviço.
func (s *Service) retomarTransacao(
	ctx context.Context,
	r ports.Repos,
	tx *wagerdomain.WagerTransaction,
	pol PoliticaDeReferencia,
) (Retomada, error) {
	// 1. Estado terminal: outra instância concluiu. Caminho esperado da disputa.
	if tx.Status().IsTerminal() {
		return Retomada{
			Desfecho: RetomadaIgnorada,
			Result:   Result{TransactionID: tx.ID(), Status: tx.Status()},
		}, nil
	}

	now := s.clock.Now().UTC()

	// 2. A janela do agendamento (o lease do claim).
	if tx.Status() == wagerdomain.StatusPendingReference && tx.NextResolutionAttemptAt().After(now) {
		return Retomada{
			Desfecho: RetomadaIgnorada,
			Result:   Result{TransactionID: tx.ID(), Status: tx.Status()},
		}, nil
	}

	// 3. A requisição reconstruída do que está PERSISTIDO. Nada vem da memória:
	//    o processo que aceitou a operação pode ter morrido há horas.
	requisicao := requisicaoDaTransacao(tx)

	// 4. A referência: resolvida, ainda pendente ou incompatível. É uma leitura
	//    que não move dinheiro, então acontece antes do lock da carteira.
	ref, err := s.resolverReferencia(ctx, r, requisicao)
	if err != nil {
		return Retomada{}, err
	}

	switch ref.desfecho {
	case referenciaPendente:
		return s.manterPendente(ctx, r, tx, requisicao, pol, now)

	case referenciaRejeitada:
		return s.rejeitarRetomada(ctx, r, tx, requisicao, ref.failureCode, now)
	}

	// 5. Daqui para baixo houve desfecho: a operação vai movimentar dinheiro (ou
	//    concluir sem movimentar, no caso de LOSS). O lock da carteira vem antes
	//    de qualquer leitura de saldo.
	carteira, err := r.Wallets.GetByIDForUpdate(ctx, tx.WalletID())
	switch {
	case ports.IsNotFound(err):
		// A carteira não existe: não há onde movimentar. Como a coluna tem FK
		// para `wallets`, isto é anomalia de dados — falha permanente, registrada
		// para auditoria.
		return s.falharRetomada(ctx, r, tx, now, "carteira inexistente")
	case err != nil:
		return Retomada{}, err
	}

	// `mov` é ponteiro porque "não houve movimento" (LOSS) precisa ser distinto
	// de "movimento zerado": um `Movement` zerado teria versão zero e gravaria um
	// UPDATE que não casa com nenhuma linha.
	var mov *wallet.Movement
	if requisicao.Kind.MovesMoney() {
		movimento, err := s.mover(carteira, tx.ID(), requisicao, ref, now)
		if err != nil {
			if failureCode := codigoDaFalhaDeMovimento(requisicao.Kind, err); failureCode != "" {
				return s.rejeitarRetomada(ctx, r, tx, requisicao, failureCode, now)
			}
			return Retomada{}, err
		}
		if err := r.Wallets.Update(ctx, carteira.ID(), movimento.VersionBefore(), carteira.Balance()); err != nil {
			return Retomada{}, err
		}
		if err := r.Ledger.Insert(ctx, movimento.Entry()); err != nil {
			return Retomada{}, err
		}
		mov = &movimento
	}

	saldo := carteira.Balance()
	versao := carteira.Version()
	var entrada *ledger.Entry
	if mov != nil {
		saldo = mov.Entry().BalanceAfter()
		versao = mov.Wallet().Version()
		e := mov.Entry()
		entrada = &e
	}

	if requisicao.Kind.RequiresReference() {
		if ref.referente == nil {
			return Retomada{}, invalid(CodeReferenceMismatch, "retomada de %s sem referência resolvida", requisicao.Kind)
		}
		if err := vincularReferenciaDaRetomada(tx, ref.referente.ID(), now); err != nil {
			return Retomada{}, err
		}
	}

	if err := tx.MarkProcessed(saldo, now); err != nil {
		return Retomada{}, err
	}
	if err := r.Transactions.Save(ctx, tx); err != nil {
		return Retomada{}, err
	}

	// Os eventos entram na MESMA transação do saldo, do ledger e do estado. É o
	// mesmo contrato do caminho HTTP/SQS: ou tudo é confirmado, ou nada é.
	if err := s.emitirProcessado(ctx, r, tx, correlacaoDe(tx), causacaoDe(tx), saldo, entrada, versao, now); err != nil {
		return Retomada{}, err
	}

	return Retomada{
		Desfecho: RetomadaResolvida,
		Result:   Result{TransactionID: tx.ID(), Status: tx.Status(), Balance: &saldo},
	}, nil
}

// manterPendente reagenda a tentativa, ou rejeita por TTL esgotado.
//
// # A ordem: tentar PRIMEIRO, expirar DEPOIS
//
// O TTL só decide quando a referência NÃO está disponível. Se ela chegou — mesmo
// depois do prazo — a operação conclui: rejeitar uma reversão cuja referência
// existe seria destruir informação correta para respeitar um relógio. O TTL
// existe para o que não dá para resolver, e é assim que ele é aplicado.
func (s *Service) manterPendente(
	ctx context.Context,
	r ports.Repos,
	tx *wagerdomain.WagerTransaction,
	requisicao Request,
	pol PoliticaDeReferencia,
	now time.Time,
) (Retomada, error) {
	if expirouReferencia(tx, pol, now) {
		return s.rejeitarRetomada(ctx, r, tx, requisicao, CodeReferenceNotFound, now)
	}

	tentativa := tx.ReferenceResolutionAttempt() + 1
	proximo := pol.NextAttemptAt
	if proximo.IsZero() {
		proximo = now.Add(baseReferenciaPendente)
	}

	if err := tx.ReagendarReferencia(tentativa, proximo, now); err != nil {
		return Retomada{}, err
	}
	if err := r.Transactions.Save(ctx, tx); err != nil {
		return Retomada{}, err
	}
	if err := s.emitirPendencia(ctx, r, tx, correlacaoDe(tx), causacaoDe(tx), now); err != nil {
		return Retomada{}, err
	}

	return Retomada{
		Desfecho: RetomadaPendente,
		Result:   Result{TransactionID: tx.ID(), Status: tx.Status()},
	}, nil
}

// rejeitarRetomada leva a operação a REJECTED reusando o caminho de rejeição do
// serviço (uma régua só para a recusa, com o evento na mesma transação).
func (s *Service) rejeitarRetomada(
	ctx context.Context,
	r ports.Repos,
	tx *wagerdomain.WagerTransaction,
	requisicao Request,
	failureCode string,
	now time.Time,
) (Retomada, error) {
	// A correlação e a causação dos eventos da retomada são as MESMAS usadas nos
	// eventos de conclusão e de pendência; sem isto, a rejeição sairia com
	// `correlationId` vazio (o valor da linha, que pode não existir) enquanto as
	// outras sairiam correlacionadas.
	requisicao.CorrelationID = correlacaoDe(tx)
	requisicao.CausationID = causacaoDe(tx)

	resultado, err := s.rejeitar(ctx, r, tx, requisicao, failureCode, now)
	if err != nil {
		return Retomada{}, err
	}
	return Retomada{Desfecho: RetomadaRejeitada, Result: resultado}, nil
}

// falharRetomada registra a anomalia de dados como falha permanente de auditoria.
//
// `FAILED` é o estado do README §6.3 para "falha permanente de infraestrutura
// registrada para auditoria": não é rejeição de negócio (o provedor não errou) e
// não pode ficar pendente para sempre.
//
// NÃO emite `WagerTransactionRejected`: a tabela do README §11 define esse evento
// como "rejeição definitiva por regra de negócio", e um dado inconsistente não é
// uma decisão de negócio. Inventar um evento de falha que o enunciado não prevê
// seria pior do que registrar o estado (que é durável, auditável e consultável
// pela API) e deixar o operador agir.
func (s *Service) falharRetomada(
	ctx context.Context,
	r ports.Repos,
	tx *wagerdomain.WagerTransaction,
	now time.Time,
	motivo string,
) (Retomada, error) {
	if err := tx.MarkFailed("", now); err != nil {
		return Retomada{}, err
	}
	if err := r.Transactions.Save(ctx, tx); err != nil {
		return Retomada{}, err
	}

	return Retomada{
		Desfecho: RetomadaRejeitada,
		Result:   Result{TransactionID: tx.ID(), Status: tx.Status(), FailureCode: tx.FailureCode()},
		Motivo:   motivo,
	}, nil
}

// requisicaoDaTransacao reconstrói a `Request` a partir das colunas persistidas.
//
// NÃO passa por `validate`: a linha já foi validada quando entrou, e revalidar
// aqui recusaria uma operação legítima por causa de uma regra nova — o pedido
// original não pode ser "corrigido" retroativamente. O que a retomada precisa é
// exatamente o que a régua de referência lê.
func requisicaoDaTransacao(tx *wagerdomain.WagerTransaction) Request {
	return Request{
		ProviderID:             tx.ProviderID(),
		ExternalTransactionID:  tx.ExternalTransactionID(),
		IdempotencyKey:         tx.IdempotencyKey(),
		PlayerID:               tx.PlayerID(),
		WalletID:               tx.WalletID(),
		RoundID:                tx.RoundID(),
		GameID:                 tx.GameID(),
		Kind:                   tx.Kind(),
		Amount:                 tx.Amount(),
		ReferenceExternalTxnID: tx.ReferenceExternalTransactionID(),
		CorrelationID:          tx.CorrelationID(),
	}
}

// vincularReferenciaDaRetomada grava a referência interna resolvida.
//
// É diferente de `vincularReferencia` (a do caminho síncrono) porque o estado de
// partida é outro: lá a linha acabou de nascer em PENDING e precisa transitar
// para PENDING_REFERENCE antes de resolver; aqui ela normalmente JÁ está em
// PENDING_REFERENCE (é o caso do worker), e a transição para o mesmo estado não
// existe — de propósito (ver `ReagendarReferencia`). A exceção é a linha presa em
// PENDING, que o reaper retoma: essa precisa da transição primeiro.
func vincularReferenciaDaRetomada(tx *wagerdomain.WagerTransaction, referencia wagerdomain.TransactionID, now time.Time) error {
	tentativa := tx.ReferenceResolutionAttempt()
	if tentativa < 1 {
		tentativa = 1
	}

	if tx.Status() == wagerdomain.StatusPending {
		if err := tx.MarkPendingReference(tentativa, now, now); err != nil {
			return err
		}
	}

	return tx.ResolveReference(referencia, tentativa)
}

// expirouReferencia diz se a pendência já passou do prazo.
func expirouReferencia(tx *wagerdomain.WagerTransaction, pol PoliticaDeReferencia, now time.Time) bool {
	if pol.MaxAttempts > 0 && tx.ReferenceResolutionAttempt() >= pol.MaxAttempts {
		return true
	}
	if pol.PendingMaxAge > 0 && now.Sub(tx.CreatedAt()) >= pol.PendingMaxAge {
		return true
	}
	return false
}

// correlacaoDe devolve o correlationId dos eventos emitidos na retomada.
//
// Quando a linha não tem correlação (gravada antes da migration 000005, ou
// caminho que não propaga), o identificador da própria transação assume: é
// estável, é único e permite reconstruir a história da operação. Publicar o campo
// em branco seria pior — um evento sem correlação nenhuma não é rastreável.
func correlacaoDe(tx *wagerdomain.WagerTransaction) string {
	if id := tx.CorrelationID(); id != "" {
		return id
	}
	return string(tx.ID())
}

// causacaoDe devolve o `causationId` dos eventos da retomada.
//
// A causa é a PRÓPRIA transação pendente: os eventos de conclusão, de nova
// tentativa e de expiração existem porque aquela linha esperou. Isso liga os
// eventos da retomada ao registro que os originou.
func causacaoDe(tx *wagerdomain.WagerTransaction) string { return string(tx.ID()) }
