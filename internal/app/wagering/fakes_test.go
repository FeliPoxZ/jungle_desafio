package wagering_test

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ledger"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
)

// Adapters em memória do contrato `ports`, para os testes de unidade.
//
// # Por que eles têm semântica de transação de verdade
//
// O Unit of Work em memória trabalha sobre uma CÓPIA do estado e só troca o
// estado oficial quando o bloco termina sem erro. Sem isso, "falha no meio não
// deixa resíduo" seria uma afirmação vazia: o fake não teria como desfazer nada,
// e o teste passaria mesmo se o serviço gravasse o saldo antes de descobrir que
// não devia.
//
// O que os fakes NÃO provam — e por isso é testado contra o PostgreSQL de
// verdade: lock de linha, concorrência entre processos, constraints, triggers e
// as uni­cidades parciais. Aqui se prova a REGRA; lá se prova a GARANTIA.

// estadoMemoria é o "banco" do teste, com os mesmos agregados do domínio.
type estadoMemoria struct {
	wallets map[ids.WalletID]*wallet.Wallet
	txns    []*wagerdomain.WagerTransaction
	ledger  []ledger.Entry
	outbox  []ports.OutboxEvent
	inbox   []ports.InboxMessage
}

func novoEstado() *estadoMemoria {
	return &estadoMemoria{wallets: map[ids.WalletID]*wallet.Wallet{}}
}

func (e *estadoMemoria) clone() *estadoMemoria {
	copia := &estadoMemoria{
		wallets: make(map[ids.WalletID]*wallet.Wallet, len(e.wallets)),
		txns:    make([]*wagerdomain.WagerTransaction, 0, len(e.txns)),
		ledger:  append([]ledger.Entry(nil), e.ledger...),
		outbox:  append([]ports.OutboxEvent(nil), e.outbox...),
		inbox:   append([]ports.InboxMessage(nil), e.inbox...),
	}
	for id, w := range e.wallets {
		copia.wallets[id] = clonarCarteira(w)
	}
	for _, t := range e.txns {
		copia.txns = append(copia.txns, clonarTransacao(t))
	}
	return copia
}

// clonarCarteira e clonarTransacao usam os reidratadores do domínio: é o mesmo
// caminho que o repositório real usa para transformar linha em agregado, então o
// fake não inventa uma cópia que o domínio não reconhece.
func clonarCarteira(w *wallet.Wallet) *wallet.Wallet {
	return wallet.Rehydrate(w.ID(), w.PlayerID(), w.Balance(), w.Version(), w.CreatedAt(), w.UpdatedAt())
}

func clonarTransacao(t *wagerdomain.WagerTransaction) *wagerdomain.WagerTransaction {
	var saldo *money.Money
	if t.HasResultBalance() {
		v := t.ResultBalance()
		saldo = &v
	}
	return wagerdomain.Rehydrate(
		t.ID(), t.WalletID(), t.PlayerID(), t.Kind(), t.Status(), t.Amount(),
		t.GameID(), t.RoundID(), t.IdempotencyKey(), t.PayloadHash(),
		t.ProviderID(), t.ExternalTransactionID(), t.ReferenceExternalTransactionID(),
		t.ReferenceTransactionID(), t.CorrelationID(), t.FailureCode(), saldo,
		t.ReferenceResolutionAttempt(), t.NextResolutionAttemptAt(),
		t.CreatedAt(), t.UpdatedAt(),
	)
}

// ---------------------------------------------------------------------------
// Unit of Work
// ---------------------------------------------------------------------------

type uowMemoria struct {
	estado *estadoMemoria

	// falhas injetadas, para provar rollback e retentativa.
	falhaTransitoriaEm      int // número da chamada de Run que falha (0 = nunca)
	falharSempreTransitorio bool
	falharSempreConflito    bool
	chamadas                int
	falhaOutbox             error

	// esconderChave faz a consulta por chave de idempotência responder "não
	// encontrado" nas próximas N chamadas.
	//
	// Simula a janela REAL entre as duas leituras da etapa 1: outra instância
	// comita a operação depois de a primeira consulta passar e antes da segunda.
	esconderChave int
}

func (u *uowMemoria) Run(ctx context.Context, fn func(context.Context, ports.Repos) error) error {
	u.chamadas++
	if u.falharSempreTransitorio || (u.falhaTransitoriaEm > 0 && u.chamadas == u.falhaTransitoriaEm) {
		return ports.ErrTransient
	}
	if u.falharSempreConflito {
		// Conflito de unicidade como o banco devolve: alguém chegou primeiro.
		return ports.ErrConflict
	}

	rascunho := u.estado.clone()
	if err := fn(ctx, u.repos(rascunho)); err != nil {
		// O rascunho é descartado: nada do que a função gravou sobrevive.
		return err
	}
	*u.estado = *rascunho
	return nil
}

func (u *uowMemoria) RunReadOnly(ctx context.Context, fn func(context.Context, ports.Repos) error) error {
	return fn(ctx, u.repos(u.estado.clone()))
}

func (u *uowMemoria) repos(e *estadoMemoria) ports.Repos {
	return ports.Repos{
		Wallets:      &carteirasMemoria{e: e},
		Transactions: &transacoesMemoria{e: e, esconderChave: &u.esconderChave},
		Ledger:       &ledgerMemoria{e: e},
		Inbox:        &inboxMemoria{e: e},
		Outbox:       &outboxMemoria{e: e, falha: u.falhaOutbox},
	}
}

// ---------------------------------------------------------------------------
// Carteiras
// ---------------------------------------------------------------------------

type carteirasMemoria struct{ e *estadoMemoria }

func (r *carteirasMemoria) Insert(_ context.Context, w *wallet.Wallet) error {
	for _, existente := range r.e.wallets {
		if existente.PlayerID() == w.PlayerID() && existente.Currency() == w.Currency() {
			return ports.ErrConflict
		}
	}
	r.e.wallets[w.ID()] = clonarCarteira(w)
	return nil
}

func (r *carteirasMemoria) GetByID(_ context.Context, id ids.WalletID) (*wallet.Wallet, error) {
	w, ok := r.e.wallets[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return clonarCarteira(w), nil
}

func (r *carteirasMemoria) GetByPlayerAndCurrency(_ context.Context, playerID ids.PlayerID, currency money.Currency) (*wallet.Wallet, error) {
	for _, w := range r.e.wallets {
		if w.PlayerID() == playerID && w.Currency() == currency {
			return clonarCarteira(w), nil
		}
	}
	return nil, ports.ErrNotFound
}

// GetByIDForUpdate lê a carteira sem lock: em memória não existe escritor
// concorrente. O lock de verdade é provado contra o PostgreSQL.
func (r *carteirasMemoria) GetByIDForUpdate(ctx context.Context, id ids.WalletID) (*wallet.Wallet, error) {
	return r.GetByID(ctx, id)
}

func (r *carteirasMemoria) Update(_ context.Context, id ids.WalletID, expectedVersion int64, balance money.Money) error {
	w, ok := r.e.wallets[id]
	if !ok || w.Version() != expectedVersion {
		return ports.ErrConflict
	}
	// `version = version + 1` e a checagem de versão: as mesmas duas coisas que
	// o UPDATE condicional faz no banco.
	r.e.wallets[id] = wallet.Rehydrate(id, w.PlayerID(), balance, w.Version()+1, w.CreatedAt(), time.Now().UTC())
	return nil
}

// ---------------------------------------------------------------------------
// Transações
// ---------------------------------------------------------------------------

type transacoesMemoria struct {
	e             *estadoMemoria
	esconderChave *int
}

func (r *transacoesMemoria) Insert(_ context.Context, t *wagerdomain.WagerTransaction) error {
	for _, x := range r.e.txns {
		if x.ProviderID() != "" && x.ProviderID() == t.ProviderID() && x.IdempotencyKey() == t.IdempotencyKey() {
			return ports.ErrConflict
		}
		if x.ProviderID() != "" && x.ProviderID() == t.ProviderID() && x.ExternalTransactionID() == t.ExternalTransactionID() {
			return ports.ErrConflict
		}
		if t.Kind() == wagerdomain.KindOpening && x.Kind() == wagerdomain.KindOpening && x.IdempotencyKey() == t.IdempotencyKey() {
			return ports.ErrConflict
		}
	}
	r.e.txns = append(r.e.txns, clonarTransacao(t))
	return nil
}

func (r *transacoesMemoria) GetByID(_ context.Context, id wagerdomain.TransactionID) (*wagerdomain.WagerTransaction, error) {
	for _, t := range r.e.txns {
		if t.ID() == id {
			return clonarTransacao(t), nil
		}
	}
	return nil, ports.ErrNotFound
}

func (r *transacoesMemoria) GetByIDForUpdate(ctx context.Context, id wagerdomain.TransactionID) (*wagerdomain.WagerTransaction, error) {
	return r.GetByID(ctx, id)
}

func (r *transacoesMemoria) GetByProviderAndExternalID(_ context.Context, providerID, externalID string) (*wagerdomain.WagerTransaction, error) {
	for _, t := range r.e.txns {
		if t.ProviderID() == providerID && t.ExternalTransactionID() == externalID {
			return clonarTransacao(t), nil
		}
	}
	return nil, ports.ErrNotFound
}

func (r *transacoesMemoria) GetByIdempotencyKey(_ context.Context, providerID, key string) (*wagerdomain.WagerTransaction, error) {
	if r.esconderChave != nil && *r.esconderChave > 0 {
		*r.esconderChave--
		return nil, ports.ErrNotFound
	}
	for _, t := range r.e.txns {
		if t.ProviderID() == providerID && t.IdempotencyKey() == key {
			return clonarTransacao(t), nil
		}
	}
	return nil, ports.ErrNotFound
}

func (r *transacoesMemoria) GetOpeningByIdempotencyKey(_ context.Context, key string) (*wagerdomain.WagerTransaction, error) {
	for _, t := range r.e.txns {
		if t.Kind() == wagerdomain.KindOpening && t.IdempotencyKey() == key {
			return clonarTransacao(t), nil
		}
	}
	return nil, ports.ErrNotFound
}

func (r *transacoesMemoria) GetByReferenceAndKind(_ context.Context, referenceID wagerdomain.TransactionID, kind wagerdomain.Kind) (*wagerdomain.WagerTransaction, error) {
	for _, t := range r.e.txns {
		if t.ReferenceTransactionID() == referenceID && t.Kind() == kind {
			return clonarTransacao(t), nil
		}
	}
	return nil, ports.ErrNotFound
}

func (r *transacoesMemoria) Save(_ context.Context, t *wagerdomain.WagerTransaction) error {
	// A unicidade que o banco impõe no UPDATE (índice parcial
	// reference_transaction_id + kind) é conferida AQUI, e não no Insert: o
	// INSERT grava a referência nula, e é o Save que a preenche.
	if t.ReferenceTransactionID() != "" {
		for _, x := range r.e.txns {
			if x.ID() != t.ID() && x.ReferenceTransactionID() == t.ReferenceTransactionID() && x.Kind() == t.Kind() {
				return ports.ErrConflict
			}
		}
	}

	for i, x := range r.e.txns {
		if x.ID() == t.ID() {
			r.e.txns[i] = clonarTransacao(t)
			return nil
		}
	}
	return ports.ErrNotFound
}

func (r *transacoesMemoria) ListPendingReferencesDue(_ context.Context, now time.Time, limit int) ([]*wagerdomain.WagerTransaction, error) {
	var saida []*wagerdomain.WagerTransaction
	for _, t := range r.e.txns {
		if t.Status() == wagerdomain.StatusPendingReference && !t.NextResolutionAttemptAt().After(now) {
			saida = append(saida, clonarTransacao(t))
			if len(saida) == limit {
				break
			}
		}
	}
	return saida, nil
}

func (r *transacoesMemoria) ClaimPendingReferencesDue(_ context.Context, limit int, lease time.Duration, now time.Time) ([]*wagerdomain.WagerTransaction, error) {
	// Espelha o SQL: cada linha vencida é reivindicada (o lease empurra o
	// próximo instante para o futuro) e devolvida.
	var saida []*wagerdomain.WagerTransaction
	for _, t := range r.e.txns {
		if t.Status() != wagerdomain.StatusPendingReference || t.NextResolutionAttemptAt().After(now) {
			continue
		}
		if err := t.ReagendarReferencia(t.ReferenceResolutionAttempt(), now.Add(lease), now); err != nil {
			return nil, err
		}
		saida = append(saida, clonarTransacao(t))
		if len(saida) == limit {
			break
		}
	}
	return saida, nil
}

func (r *transacoesMemoria) ListPendingStale(_ context.Context, now time.Time, maxAge time.Duration, limit int) ([]*wagerdomain.WagerTransaction, error) {
	var saida []*wagerdomain.WagerTransaction
	corte := now.Add(-maxAge)
	for _, t := range r.e.txns {
		if t.Status() == wagerdomain.StatusPending && !t.UpdatedAt().After(corte) {
			saida = append(saida, clonarTransacao(t))
			if len(saida) == limit {
				break
			}
		}
	}
	return saida, nil
}

func (r *transacoesMemoria) ListByWallet(_ context.Context, walletID ids.WalletID, limit int, _ *ports.Cursor) ([]*wagerdomain.WagerTransaction, error) {
	var saida []*wagerdomain.WagerTransaction
	for _, t := range r.e.txns {
		if t.WalletID() == walletID {
			saida = append(saida, clonarTransacao(t))
			if len(saida) == limit {
				break
			}
		}
	}
	return saida, nil
}

// ---------------------------------------------------------------------------
// Ledger
// ---------------------------------------------------------------------------

type ledgerMemoria struct{ e *estadoMemoria }

func (r *ledgerMemoria) Insert(_ context.Context, entry ledger.Entry) error {
	for _, x := range r.e.ledger {
		if x.WalletID() == entry.WalletID() && x.TransactionID() == entry.TransactionID() {
			return ports.ErrConflict
		}
	}
	r.e.ledger = append(r.e.ledger, entry)
	return nil
}

func (r *ledgerMemoria) GetByTransaction(_ context.Context, walletID ids.WalletID, transactionID string) (*ledger.Entry, error) {
	for i, x := range r.e.ledger {
		if x.WalletID() == walletID && string(x.TransactionID()) == transactionID {
			return &r.e.ledger[i], nil
		}
	}
	return nil, ports.ErrNotFound
}

func (r *ledgerMemoria) ListByWallet(_ context.Context, walletID ids.WalletID, limit int, cursor *ports.Cursor) ([]*ledger.Entry, error) {
	// Espelha a consulta real: ordem por (createdAt, id) e cursor EXCLUSIVO. Um
	// fake que ignorasse o cursor faria o teste de paginação passar sem provar
	// nada.
	var daCarteira []ledger.Entry
	for _, x := range r.e.ledger {
		if x.WalletID() == walletID {
			daCarteira = append(daCarteira, x)
		}
	}
	sort.Slice(daCarteira, func(i, j int) bool {
		if !daCarteira[i].CreatedAt().Equal(daCarteira[j].CreatedAt()) {
			return daCarteira[i].CreatedAt().Before(daCarteira[j].CreatedAt())
		}
		return daCarteira[i].ID() < daCarteira[j].ID()
	})

	var saida []*ledger.Entry
	for i := range daCarteira {
		if cursor != nil {
			depois := daCarteira[i].CreatedAt().After(cursor.CreatedAt) ||
				(daCarteira[i].CreatedAt().Equal(cursor.CreatedAt) && daCarteira[i].ID() > cursor.ID)
			if !depois {
				continue
			}
		}
		entrada := daCarteira[i]
		saida = append(saida, &entrada)
		if len(saida) == limit {
			break
		}
	}
	return saida, nil
}

func (r *ledgerMemoria) SumSignedByWallet(_ context.Context, walletID ids.WalletID) (int64, error) {
	var soma int64
	for _, x := range r.e.ledger {
		if x.WalletID() == walletID {
			soma += x.SignedMinor()
		}
	}
	return soma, nil
}

func (r *ledgerMemoria) CountByWallet(_ context.Context, walletID ids.WalletID) (int, error) {
	var quantos int
	for _, x := range r.e.ledger {
		if x.WalletID() == walletID {
			quantos++
		}
	}
	return quantos, nil
}

// ---------------------------------------------------------------------------
// Inbox
// ---------------------------------------------------------------------------

type inboxMemoria struct{ e *estadoMemoria }

func (r *inboxMemoria) InsertIfAbsent(_ context.Context, m ports.InboxMessage) (bool, error) {
	for _, x := range r.e.inbox {
		if x.ConsumerName == m.ConsumerName && x.MessageID == m.MessageID {
			return false, nil
		}
	}
	r.e.inbox = append(r.e.inbox, m)
	return true, nil
}

func (r *inboxMemoria) Get(_ context.Context, consumerName, messageID string) (*ports.InboxMessage, error) {
	for i, x := range r.e.inbox {
		if x.ConsumerName == consumerName && x.MessageID == messageID {
			return &r.e.inbox[i], nil
		}
	}
	return nil, ports.ErrNotFound
}

func (r *inboxMemoria) MarkProcessed(_ context.Context, consumerName, messageID string, now time.Time) error {
	for i, x := range r.e.inbox {
		if x.ConsumerName == consumerName && x.MessageID == messageID {
			r.e.inbox[i].Status = "PROCESSED"
			r.e.inbox[i].CompletedAt = now
			return nil
		}
	}
	return ports.ErrNotFound
}

// MarkRejected separa "recusei" de "falhei": rejeição é terminal e a mensagem
// pode ser removida da fila; falha ainda pode ser retentada.
func (r *inboxMemoria) MarkRejected(_ context.Context, consumerName, messageID, failureCode string, now time.Time) error {
	for i, x := range r.e.inbox {
		if x.ConsumerName == consumerName && x.MessageID == messageID {
			r.e.inbox[i].Status = "REJECTED"
			r.e.inbox[i].FailureCode = failureCode
			r.e.inbox[i].CompletedAt = now
			return nil
		}
	}
	return ports.ErrNotFound
}

// CountAttempt incrementa o recebimento, como o repositório real faz no SQL.
func (r *inboxMemoria) CountAttempt(_ context.Context, consumerName, messageID string, _ time.Time) error {
	for i, x := range r.e.inbox {
		if x.ConsumerName == consumerName && x.MessageID == messageID {
			r.e.inbox[i].Attempts++
			return nil
		}
	}
	return ports.ErrNotFound
}

func (r *inboxMemoria) MarkFailed(_ context.Context, consumerName, messageID, reason string, now time.Time) error {
	for i, x := range r.e.inbox {
		if x.ConsumerName == consumerName && x.MessageID == messageID {
			r.e.inbox[i].Status = "FAILED"
			r.e.inbox[i].FailureCode = reason
			r.e.inbox[i].CompletedAt = now
			return nil
		}
	}
	return ports.ErrNotFound
}

// ---------------------------------------------------------------------------
// Outbox
// ---------------------------------------------------------------------------

type outboxMemoria struct {
	e     *estadoMemoria
	falha error
}

func (r *outboxMemoria) Insert(_ context.Context, event ports.OutboxEvent) error {
	if r.falha != nil {
		return r.falha
	}
	for _, x := range r.e.outbox {
		if x.EventID == event.EventID {
			return ports.ErrConflict
		}
	}
	// A sequência é do BANCO (IDENTITY) no adaptador real; aqui ela é o tamanho
	// da fila, para o dublê manter a mesma propriedade: ordem de inserção.
	event.Sequence = int64(len(r.e.outbox) + 1)
	r.e.outbox = append(r.e.outbox, event)
	return nil
}

func (r *outboxMemoria) ClaimBatch(_ context.Context, limit int, lease time.Duration, now time.Time) ([]ports.OutboxEvent, error) {
	// Espelha o SQL: a fila é lida em ordem de inserção (a `sequence`), cada
	// evento reivindicado recebe o LEASE, e o que não está disponível é pulado.
	var saida []ports.OutboxEvent
	for i := range r.e.outbox {
		x := r.e.outbox[i]
		if !x.PublishedAt.IsZero() || x.AvailableAt.After(now) {
			continue
		}
		r.e.outbox[i].AvailableAt = now.Add(lease)
		saida = append(saida, x)
		if len(saida) == limit {
			break
		}
	}
	return saida, nil
}

func (r *outboxMemoria) MarkPublished(_ context.Context, eventID string, now time.Time) error {
	for i, x := range r.e.outbox {
		if x.EventID == eventID {
			r.e.outbox[i].PublishedAt = now
			return nil
		}
	}
	return ports.ErrNotFound
}

func (r *outboxMemoria) MarkRetry(_ context.Context, eventID string, nextAttemptAt time.Time, reason string) error {
	for i, x := range r.e.outbox {
		if x.EventID == eventID {
			r.e.outbox[i].Attempts++
			r.e.outbox[i].AvailableAt = nextAttemptAt
			r.e.outbox[i].LastError = reason
			return nil
		}
	}
	return ports.ErrNotFound
}

func (r *outboxMemoria) PendingStats(_ context.Context, now time.Time) (int, time.Duration, error) {
	var (
		pendentes  int
		maisAntigo time.Time
	)
	for _, x := range r.e.outbox {
		if !x.PublishedAt.IsZero() {
			continue
		}
		pendentes++
		if maisAntigo.IsZero() || x.OccurredAt.Before(maisAntigo) {
			maisAntigo = x.OccurredAt
		}
	}
	if pendentes == 0 {
		return 0, 0, nil
	}
	return pendentes, now.Sub(maisAntigo), nil
}

// ---------------------------------------------------------------------------
// Relógio e identificadores
// ---------------------------------------------------------------------------

type relogioFixo struct{ agora time.Time }

func novoRelogio() *relogioFixo {
	return &relogioFixo{agora: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)}
}

func (r *relogioFixo) Now() time.Time { return r.agora }

func (r *relogioFixo) avancar(d time.Duration) { r.agora = r.agora.Add(d) }

// idsSequenciais gera identificadores previsíveis ("...-000000000002"): o teste
// compara o que o serviço devolveu com o que está no estado, sem precisar de
// igualdade de UUID aleatório.
//
// O FORMATO precisa ser o de UUID, e não "id-1": o caso de uso recusa
// identificador que não é UUID antes de chegar ao banco (ver `ids.go`), e um
// dublê com identificador impossível testaria um caminho que produção não tem.
type idsSequenciais struct{ n int }

func (g *idsSequenciais) NewID() string {
	g.n++
	return uuidDeTeste(2, g.n)
}

// uuidDeTeste monta um UUID válido e determinístico. O primeiro grupo separa
// carteiras (1) de transações e eventos (2), para nenhum identificador de teste
// se confundir com outro.
func uuidDeTeste(grupo, n int) string {
	return fmt.Sprintf("00000000-0000-7000-8000-%04d%08d", grupo, n)
}
