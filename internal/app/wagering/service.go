package wagering

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	domainerrors "github.com/FeliPoxZ/jungle_desafio/internal/domain/errors"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ledger"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
	"github.com/FeliPoxZ/jungle_desafio/internal/platform/canonicaljson"
	"github.com/FeliPoxZ/jungle_desafio/internal/platform/idgen"
)

const (
	// tentativasMaximas é o teto de execuções de UMA operação.
	tentativasMaximas = 3

	// esperaEntreTentativas é curta de propósito: as falhas tratadas aqui são
	// disputa momentânea (lock timeout, deadlock) e corrida de idempotência. A
	// espera longa com backoff é do worker da outbox e do worker de referências,
	// que têm tempo; o provedor esperando resposta, não.
	esperaEntreTentativas = 20 * time.Millisecond

	// baseReferenciaPendente é o primeiro intervalo até a próxima tentativa de
	// resolver a referência. O crescimento exponencial e o jitter são do worker
	// de referências (Fase 9); aqui basta gravar um instante futuro, para a linha
	// não ser reivindicada no mesmo instante em que nasceu.
	baseReferenciaPendente = 5 * time.Second

	// tipoDeAgregado é o `aggregate_type` gravado na outbox. O `aggregateId` é a
	// CARTEIRA, porque é ela o `MessageGroupId` da fila FIFO: eventos da mesma
	// carteira precisam ser entregues em ordem.
	tipoDeAgregado = "wallet"
)

// Service é o caso de uso das operações financeiras.
type Service struct {
	uow   ports.UnitOfWork
	clock ports.Clock
	ids   idgen.Generator
}

// New monta o serviço.
//
// As três dependências são contratos: o Unit of Work (quem delimita a transação),
// o relógio (quem diz que horas são, para o teste poder afirmar instantes) e o
// gerador de identificadores (para o teste não comparar UUIDs aleatórios).
func New(uow ports.UnitOfWork, clock ports.Clock, ids idgen.Generator) *Service {
	return &Service{uow: uow, clock: clock, ids: ids}
}

// Process executa uma operação externa e devolve o desfecho.
//
// Erro é o que impede a operação de existir: entrada inválida, conflito de
// idempotência, indisponibilidade. Rejeição de negócio NÃO é erro — volta em
// `Result`, porque a operação existe, foi decidida e ficou auditável.
func (s *Service) Process(ctx context.Context, req Request) (Result, error) {
	req = req.normalizado()

	if err := req.validate(); err != nil {
		return Result{}, err
	}

	hash, err := s.hashDe(req)
	if err != nil {
		return Result{}, err
	}

	var res Result
	err = s.comRetentativa(ctx, func(ctx context.Context) error {
		var e error
		res, e = s.processar(ctx, req, hash)
		return e
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

// hashDe calcula o hash canônico do payload de negócio.
func (s *Service) hashDe(req Request) (wagerdomain.Hash, error) {
	bruto, err := canonicaljson.Hash(req.payload())
	if err != nil {
		return "", fmt.Errorf("wagering: calculando o hash do payload: %w", err)
	}
	return wagerdomain.Hash(bruto), nil
}

// comRetentativa repete a TRANSAÇÃO INTEIRA, nunca uma chamada por dentro dela.
//
// Repetir por dentro é o erro clássico: a tentativa que falhou deixa a conexão
// com a transação aberta segurando o lock da carteira, e a repetição trava. O
// sintoma aparece longe — no próximo que precisar daquela carteira.
//
// Dois motivos justificam repetir:
//
//   - falha transitória (lock timeout, deadlock, conexão caindo): repetir é o
//     que o banco espera que se faça;
//   - conflito: no nosso schema, unicidade violada quer dizer que ALGUÉM CHEGOU
//     PRIMEIRO — reenvio simultâneo da mesma chave, ou reversão que outra
//     instância acabou de aplicar. A segunda passada lê o que já está gravado e
//     decide entre replay e conflito, em vez de devolver erro de banco.
func (s *Service) comRetentativa(ctx context.Context, fn func(ctx context.Context) error) error {
	var err error

	for tentativa := 1; tentativa <= tentativasMaximas; tentativa++ {
		err = fn(ctx)
		if err == nil {
			return nil
		}
		if !ports.IsRetryable(err) && !ports.IsConflict(err) {
			return err
		}
		if tentativa == tentativasMaximas {
			return err
		}

		select {
		case <-ctx.Done():
			return err
		case <-time.After(esperaEntreTentativas * time.Duration(tentativa)):
		}
	}
	return err
}

// processar executa uma tentativa dentro de UMA transação.
func (s *Service) processar(ctx context.Context, req Request, hash wagerdomain.Hash) (Result, error) {
	var res Result

	err := s.uow.Run(ctx, func(ctx context.Context, r ports.Repos) error {
		var e error
		res, e = s.executar(ctx, r, req, hash)
		return e
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

// executar é a ordem exata das etapas dentro da transação.
//
// Duas ordens não são negociáveis:
//
//   - o LOCK da carteira (etapa 3) vem antes de qualquer cálculo de saldo. Ler o
//     saldo e só depois pedir o lock deixaria outra transação alterar o saldo no
//     meio, e o resultado seria lost update;
//   - a movimentação (7-8) vem depois de tudo que pode recusar a operação. Não
//     existe caminho que mova dinheiro e depois descubra que não devia.
//
// Uma ordem foi ajustada em relação ao PLANO §3: o INSERT da transação (etapa 6)
// acontece antes de resolver a referência (etapa 5), porque TODO desfecho —
// rejeição, espera por referência, conclusão — é um estado DA LINHA. O INSERT
// grava PENDING e o Save grava o estado definitivo, no mesmo commit: nenhuma
// outra transação chega a ver o PENDING.
func (s *Service) executar(ctx context.Context, r ports.Repos, req Request, hash wagerdomain.Hash) (Result, error) {
	// 1-2. Idempotência antes de qualquer lock: um reenvio não pode nem chegar a
	// disputar a carteira.
	res, err := s.replay(ctx, r, req, hash)
	if err != nil {
		return Result{}, err
	}
	if res != nil {
		return *res, nil
	}

	// 3. O lock da linha da carteira.
	carteira, err := r.Wallets.GetByIDForUpdate(ctx, req.WalletID)
	switch {
	case ports.IsNotFound(err):
		return Result{}, fmt.Errorf("wagering: carteira %s: %w: %w", req.WalletID, ErrWalletNotFound, err)
	case err != nil:
		return Result{}, err
	}

	// O instante é lido DEPOIS do lock, e isso não é detalhe: ele vai para o
	// `occurredAt` dos eventos, e o publisher da outbox publica em ordem de
	// `occurred_at` com `MessageGroupId` = carteira. Lido antes do lock, o
	// instante seria a ordem em que as transações COMEÇARAM, não a ordem em que
	// os efeitos foram confirmados — e dois eventos da mesma carteira poderiam
	// sair invertidos, com o `balanceAfter` de um chegando antes do outro. Depois
	// do lock, os instantes das operações da MESMA carteira são monotônicos,
	// porque o lock as serializa.
	now := s.clock.Now().UTC()

	// 6. A transação é criada e gravada antes do desfecho ser decidido.
	tx, err := s.novaTransacao(req, now)
	if err != nil {
		return Result{}, err
	}
	if err := r.Transactions.Insert(ctx, tx); err != nil {
		// Conflito aqui é o caminho esperado do reenvio simultâneo: quem chegar
		// segundo volta para a retentativa, e a segunda passada acha a linha
		// gravada pela etapa 1-2 e responde replay (ou conflito, se o payload
		// divergir).
		return Result{}, err
	}

	// 4. Carteira × operação: dono e moeda.
	if failureCode := incompatibilidadeDeCarteira(carteira, req); failureCode != "" {
		return s.rejeitar(ctx, r, tx, req, failureCode, now)
	}

	// 5. Referência: resolvida, pendente ou recusada.
	ref, err := s.resolverReferencia(ctx, r, req)
	if err != nil {
		return Result{}, err
	}
	switch ref.desfecho {
	case referenciaPendente:
		return s.marcarPendente(ctx, r, tx, req, now)
	case referenciaRejeitada:
		return s.rejeitar(ctx, r, tx, req, ref.failureCode, now)
	}

	// 7-9. Movimentação no agregado, saldo + versão e lançamento no ledger.
	//
	// O UPDATE recebe `mov.VersionBefore()`: quem grava precisa da versão
	// ANTERIOR, porque o débito já incrementou a versão em memória. Ler
	// `carteira.Version()` aqui devolveria a versão nova, o UPDATE casaria com
	// zero linhas e a operação falharia parecendo conflito de concorrência.
	var mov wallet.Movement
	if req.Kind.MovesMoney() {
		mov, err = s.mover(carteira, tx.ID(), req, ref, now)
		if err != nil {
			if failureCode := codigoDaFalhaDeMovimento(req.Kind, err); failureCode != "" {
				return s.rejeitar(ctx, r, tx, req, failureCode, now)
			}
			return Result{}, err
		}
		if err := r.Wallets.Update(ctx, carteira.ID(), mov.VersionBefore(), carteira.Balance()); err != nil {
			return Result{}, err
		}
		if err := r.Ledger.Insert(ctx, mov.Entry()); err != nil {
			return Result{}, err
		}
	}

	saldo := carteira.Balance()
	if req.Kind.MovesMoney() {
		saldo = mov.Entry().BalanceAfter()
	}

	// A referência interna só é gravada quando a operação DEPENDE dela. Em BET,
	// WIN e LOSS a referência é informativa, e gravá-la em
	// `reference_transaction_id` faria o índice parcial
	// UNIQUE (reference_transaction_id, kind) tratar a operação como reversão
	// daquele fato — dois WIN apontando para a mesma BET passariam a colidir.
	if req.Kind.RequiresReference() {
		if ref.referente == nil {
			return Result{}, fmt.Errorf("wagering: %s exige referência resolvida e ela não veio", req.Kind)
		}
		if err := vincularReferencia(tx, ref.referente.ID(), now); err != nil {
			return Result{}, err
		}
	}

	// 10. Transição e gravação do estado.
	if err := tx.MarkProcessed(saldo, now); err != nil {
		return Result{}, err
	}
	if err := r.Transactions.Save(ctx, tx); err != nil {
		return Result{}, err
	}

	// 11. Eventos na outbox, na MESMA transação.
	if err := s.emitirProcessado(ctx, r, tx, req, saldo, mov, now); err != nil {
		return Result{}, err
	}

	// 12. O commit é do `uow.Run`: só depois dele a resposta sai.
	return Result{TransactionID: tx.ID(), Status: tx.Status(), Balance: &saldo}, nil
}

// replay devolve o resultado já persistido, ou nil quando não há nada a
// reenviar, ou erro quando a chave foi reutilizada com outro conteúdo.
//
// # Por que são DUAS leituras, e por que a segunda compara a chave
//
// A primeira leitura é pela chave de idempotência; a segunda, pela identidade
// financeira (providerId, externalTransactionId), que não pode ser reaplicada
// sob outra chave (README §9).
//
// A comparação de chave na segunda leitura não é zelo: entre as duas consultas,
// outra instância pode COMITAR a mesma operação. Sem comparar, esse instante é
// lido como "registrada sob outra chave" e uma operação legítima vira conflito.
// Foi um teste de 12 disparos simultâneos da MESMA operação que pegou isso: a
// linha aparecia entre a primeira e a segunda consulta, e o pedido correto
// recebia conflito em vez de replay.
func (s *Service) replay(ctx context.Context, r ports.Repos, req Request, hash wagerdomain.Hash) (*Result, error) {
	existente, err := r.Transactions.GetByIdempotencyKey(ctx, req.ProviderID, req.IdempotencyKey)
	if err != nil && !ports.IsNotFound(err) {
		return nil, err
	}

	if existente == nil {
		existente, err = r.Transactions.GetByProviderAndExternalID(ctx, req.ProviderID, req.ExternalTransactionID)
		switch {
		case ports.IsNotFound(err):
			return nil, nil
		case err != nil:
			return nil, err
		case existente.IdempotencyKey() != req.IdempotencyKey:
			return nil, fmt.Errorf("wagering: operação %s/%s já registrada sob a chave %q: %w",
				req.ProviderID, req.ExternalTransactionID, existente.IdempotencyKey(), ErrIdempotencyConflict)
		}
	}

	// Mesma chave: só o conteúdo decide entre replay e conflito.
	if !existente.MatchesPayload(hash) {
		return nil, fmt.Errorf("wagering: chave %q com conteúdo diferente: %w", req.IdempotencyKey, ErrIdempotencyConflict)
	}

	res := resultadoPersistido(existente)
	return &res, nil
}

// resultadoPersistido monta o resultado a partir do que está gravado.
//
// O saldo devolvido é o do PROCESSAMENTO ORIGINAL, não o de agora: o cliente
// pediu o resultado daquela operação, e a carteira pode ter mudado desde então.
// `HasResultBalance` é o que distingue "não calculado" (rejeição, pendência) de
// zero legítimo.
func resultadoPersistido(tx *wagerdomain.WagerTransaction) Result {
	res := Result{
		TransactionID:    tx.ID(),
		Status:           tx.Status(),
		FailureCode:      tx.FailureCode(),
		IdempotentReplay: true,
	}
	if tx.HasResultBalance() {
		saldo := tx.ResultBalance()
		res.Balance = &saldo
	}
	return res
}

// novaTransacao cria o agregado a partir da requisição.
func (s *Service) novaTransacao(req Request, now time.Time) (*wagerdomain.WagerTransaction, error) {
	tx, err := wagerdomain.New(
		wagerdomain.TransactionID(s.ids.NewID()),
		req.WalletID,
		req.PlayerID,
		req.Kind,
		req.Amount,
		req.GameID,
		req.RoundID,
		req.IdempotencyKey,
		req.ProviderID,
		req.ExternalTransactionID,
		req.ReferenceExternalTxnID,
		req.payload(),
		codificarPayload,
		now,
	)
	if err != nil {
		// Inalcançável pelo caminho normal: `validate` já cobriu o formato da
		// requisição e o domínio revalida o resto. Fica como defesa, para uma
		// regra nova do agregado não virar pânico aqui.
		return nil, fmt.Errorf("wagering: criando a transação: %w", err)
	}
	return tx, nil
}

// mover aplica a movimentação no agregado.
func (s *Service) mover(carteira *wallet.Wallet, txnID wagerdomain.TransactionID, req Request, ref desfechoDaReferencia, now time.Time) (wallet.Movement, error) {
	direcao, err := direcaoDoMovimento(req.Kind, ref.referente)
	if err != nil {
		return wallet.Movement{}, err
	}
	if direcao == ledger.DirectionDebit {
		return carteira.Debit(txnID, req.Amount, now)
	}
	return carteira.Credit(txnID, req.Amount, now)
}

// incompatibilidadeDeCarteira devolve o failureCode quando a carteira não serve
// para a operação, ou vazio quando serve.
func incompatibilidadeDeCarteira(carteira *wallet.Wallet, req Request) string {
	if carteira.PlayerID() != req.PlayerID {
		return CodeWalletPlayerMismatch
	}
	if carteira.Currency() != req.Amount.Currency() {
		return wallet.CodeWalletCurrencyMismatch
	}
	return ""
}

// codigoDaFalhaDeMovimento traduz a falha da movimentação em failureCode.
//
// Devolve vazio quando o erro NÃO é de negócio (banco fora, conexão caindo):
// esses continuam sendo erro — e, se forem transitórios, são repetidos.
func codigoDaFalhaDeMovimento(kind wagerdomain.Kind, err error) string {
	if errors.Is(err, wallet.ErrInsufficientFunds) {
		// O mesmo sentinela do domínio vira DOIS códigos, e de propósito: um é
		// "o jogador não tem saldo para apostar", o outro é "não dá para
		// desfazer". Quem separa é o tipo da operação.
		if kind == wagerdomain.KindRollback {
			return CodeRollbackInsufficientFunds
		}
		return wallet.CodeInsufficientFunds
	}
	// Demais erros do domínio já carregam Code estável (moeda incompatível,
	// estouro do limite do Money). O Code vazio diz que não é erro de negócio.
	return domainerrors.CodeOf(err)
}

// vincularReferencia grava a referência interna resolvida.
//
// Passa por MarkPendingReference + ResolveReference porque o agregado só aceita
// resolver referência em PENDING_REFERENCE — e essa é a transição que representa
// "esta operação depende de uma referência", verdade mesmo quando ela já estava
// disponível. O instante da próxima tentativa fica igual ao instante da
// resolução: não há próxima tentativa. O estado intermediário não é observável:
// o INSERT gravou PENDING e o UPDATE grava PROCESSED, no mesmo commit.
func vincularReferencia(tx *wagerdomain.WagerTransaction, referencia wagerdomain.TransactionID, now time.Time) error {
	if err := tx.MarkPendingReference(1, now, now); err != nil {
		return fmt.Errorf("wagering: preparando a referência: %w", err)
	}
	if err := tx.ResolveReference(referencia, 1); err != nil {
		return fmt.Errorf("wagering: vinculando a referência: %w", err)
	}
	return nil
}
