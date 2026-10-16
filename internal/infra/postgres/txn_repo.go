package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
)

// TxnRepo persiste transações de aposta.
type TxnRepo interface {
	// Insert grava a transação nova. Devolve ErrConflict quando a chave de
	// idempotência do provedor já existe — que é a idempotência funcionando,
	// não um erro de banco.
	Insert(ctx context.Context, t *wagering.WagerTransaction) error

	// GetByID lê pelo identificador interno, com lock opcional.
	GetByID(ctx context.Context, id wagering.TransactionID) (*wagering.WagerTransaction, error)
	GetByIDForUpdate(ctx context.Context, id wagering.TransactionID) (*wagering.WagerTransaction, error)

	// GetByProviderAndExternalID resolve a operação pelo identificador do
	// provedor.
	GetByProviderAndExternalID(ctx context.Context, providerID, externalID string) (*wagering.WagerTransaction, error)

	// GetByIdempotencyKey resolve pela chave de idempotência.
	GetByIdempotencyKey(ctx context.Context, providerID, key string) (*wagering.WagerTransaction, error)

	// GetOpeningByIdempotencyKey resolve a abertura interna pela chave.
	GetOpeningByIdempotencyKey(ctx context.Context, key string) (*wagering.WagerTransaction, error)

	// Save grava o estado atual da transição (status, failure_code, saldo
	// resultante, referência resolvida e controle de tentativas).
	Save(ctx context.Context, t *wagering.WagerTransaction) error

	// ListPendingReferencesDue devolve as operações em PENDING_REFERENCE cujo
	// próximo instante de tentativa já passou.
	//
	// O índice parcial criado na Fase 4 cobre exatamente esta consulta.
	ListPendingReferencesDue(ctx context.Context, now time.Time, limit int) ([]*wagering.WagerTransaction, error)

	// ListByWallet devolve as operações de uma carteira, mais recentes
	// primeiro, com cursor.
	ListByWallet(ctx context.Context, walletID wallet.WalletID, limit int, cursor *Cursor) ([]*wagering.WagerTransaction, error)
}

// Cursor é a posição de paginação: instante + identificador.
//
// Os dois juntos porque `created_at` pode repetir (duas operações no mesmo
// microssegundo), e o desempate por `id` é o que torna a ordem estável.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

type txnRepo struct {
	q querier
}

// NewTxnRepo cria o repositório sobre o pool ou sobre uma transação.
func NewTxnRepo(q querier) TxnRepo { return &txnRepo{q: q} }

const txnColumns = `
	id, wallet_id, player_id, kind, status, amount_minor, currency,
	game_id, round_id, idempotency_key, payload_hash,
	provider_id, external_transaction_id, reference_external_txn_id, reference_transaction_id,
	failure_code, result_balance_minor,
	reference_resolution_attempt, next_resolution_attempt_at,
	created_at, updated_at`

func (r *txnRepo) Insert(ctx context.Context, t *wagering.WagerTransaction) error {
	const sql = `
		INSERT INTO wager_transactions (
			id, wallet_id, player_id, kind, status, amount_minor, currency,
			game_id, round_id, idempotency_key, payload_hash,
			provider_id, external_transaction_id, reference_external_txn_id,
			reference_resolution_attempt,
			created_at, updated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`

	_, err := r.q.Exec(ctx, sql,
		string(t.ID()),
		string(t.WalletID()),
		string(t.PlayerID()),
		t.Kind().String(),
		t.Status().String(),
		t.Amount().Minor(),
		moedaDB(t.Amount().Currency()),
		t.GameID(),
		t.RoundID(),
		t.IdempotencyKey(),
		string(t.PayloadHash()),
		nuloSeVazio(t.ProviderID()),
		nuloSeVazio(t.ExternalTransactionID()),
		nuloSeVazio(t.ReferenceExternalTransactionID()),
		t.ReferenceResolutionAttempt(),
		t.CreatedAt(),
		t.UpdatedAt(),
	)
	return Classify(err)
}

func (r *txnRepo) GetByID(ctx context.Context, id wagering.TransactionID) (*wagering.WagerTransaction, error) {
	const sql = `SELECT ` + txnColumns + ` FROM wager_transactions WHERE id = $1`
	return r.um(ctx, sql, string(id))
}

func (r *txnRepo) GetByIDForUpdate(ctx context.Context, id wagering.TransactionID) (*wagering.WagerTransaction, error) {
	const sql = `SELECT ` + txnColumns + ` FROM wager_transactions WHERE id = $1 FOR UPDATE`
	return r.um(ctx, sql, string(id))
}

func (r *txnRepo) GetByProviderAndExternalID(ctx context.Context, providerID, externalID string) (*wagering.WagerTransaction, error) {
	const sql = `SELECT ` + txnColumns + `
		FROM wager_transactions
		WHERE provider_id = $1 AND external_transaction_id = $2`
	return r.um(ctx, sql, providerID, externalID)
}

func (r *txnRepo) GetByIdempotencyKey(ctx context.Context, providerID, key string) (*wagering.WagerTransaction, error) {
	const sql = `SELECT ` + txnColumns + `
		FROM wager_transactions
		WHERE provider_id = $1 AND idempotency_key = $2`
	return r.um(ctx, sql, providerID, key)
}

func (r *txnRepo) GetOpeningByIdempotencyKey(ctx context.Context, key string) (*wagering.WagerTransaction, error) {
	const sql = `SELECT ` + txnColumns + `
		FROM wager_transactions
		WHERE kind = 'OPENING' AND idempotency_key = $1`
	return r.um(ctx, sql, key)
}

func (r *txnRepo) Save(ctx context.Context, t *wagering.WagerTransaction) error {
	const sql = `
		UPDATE wager_transactions
		SET status = $2,
		    failure_code = $3,
		    result_balance_minor = $4,
		    reference_transaction_id = $5,
		    reference_resolution_attempt = $6,
		    next_resolution_attempt_at = $7
		WHERE id = $1`

	tag, err := r.q.Exec(ctx, sql,
		string(t.ID()),
		t.Status().String(),
		nuloSeVazio(t.FailureCode()),
		nuloSeDinheiro(t.ResultBalance(), t.HasResultBalance()),
		nuloSeVazio(string(t.ReferenceTransactionID())),
		t.ReferenceResolutionAttempt(),
		nuloSeTempo(t.NextResolutionAttemptAt()),
	)
	if err != nil {
		return Classify(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *txnRepo) ListPendingReferencesDue(ctx context.Context, now time.Time, limit int) ([]*wagering.WagerTransaction, error) {
	const sql = `SELECT ` + txnColumns + `
		FROM wager_transactions
		WHERE status = 'PENDING_REFERENCE'
		  AND next_resolution_attempt_at IS NOT NULL
		  AND next_resolution_attempt_at <= $1
		ORDER BY next_resolution_attempt_at
		LIMIT $2`

	return r.varios(ctx, sql, now, limit)
}

func (r *txnRepo) ListByWallet(ctx context.Context, walletID wallet.WalletID, limit int, cursor *Cursor) ([]*wagering.WagerTransaction, error) {
	// Ordem estável: created_at e id. O cursor carrega os dois para não pular
	// nem repetir linha quando o instante empata.
	if cursor == nil {
		const sql = `SELECT ` + txnColumns + `
			FROM wager_transactions
			WHERE wallet_id = $1
			ORDER BY created_at DESC, id DESC
			LIMIT $2`
		return r.varios(ctx, sql, string(walletID), limit)
	}

	const sql = `SELECT ` + txnColumns + `
		FROM wager_transactions
		WHERE wallet_id = $1
		  AND (created_at, id) < ($2, $3)
		ORDER BY created_at DESC, id DESC
		LIMIT $4`
	return r.varios(ctx, sql, string(walletID), cursor.CreatedAt, cursor.ID, limit)
}

// um executa a consulta que devolve exatamente uma transação.
func (r *txnRepo) um(ctx context.Context, sql string, args ...any) (*wagering.WagerTransaction, error) {
	var raw txnRow

	err := r.q.QueryRow(ctx, sql, args...).Scan(raw.destinos()...)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, Classify(err)
	}
	return raw.dominio(), nil
}

// varios executa a consulta que devolve zero ou mais transações.
func (r *txnRepo) varios(ctx context.Context, sql string, args ...any) ([]*wagering.WagerTransaction, error) {
	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, Classify(err)
	}
	defer rows.Close()

	var saida []*wagering.WagerTransaction
	for rows.Next() {
		var raw txnRow
		if err := rows.Scan(raw.destinos()...); err != nil {
			return nil, Classify(err)
		}
		saida = append(saida, raw.dominio())
	}
	if err := rows.Err(); err != nil {
		return nil, Classify(err)
	}
	return saida, nil
}

// txnRow é a forma crua de uma linha de wager_transactions.
//
// Existe para não repetir 21 variáveis em cada scan e para concentrar a
// conversão banco -> domínio em um lugar só.
type txnRow struct {
	id                     string
	walletID               string
	playerID               string
	kind                   string
	status                 string
	amountMinor            int64
	currency               string
	gameID                 string
	roundID                string
	idempotencyKey         string
	payloadHash            string
	providerID             *string
	externalTransactionID  *string
	referenceExternalTxnID *string
	referenceTransactionID *string
	failureCode            *string
	resultBalanceMinor     *int64
	attempt                int32
	nextAttemptAt          *time.Time
	createdAt              time.Time
	updatedAt              time.Time
}

func (row *txnRow) destinos() []any {
	return []any{
		&row.id, &row.walletID, &row.playerID, &row.kind, &row.status,
		&row.amountMinor, &row.currency, &row.gameID, &row.roundID,
		&row.idempotencyKey, &row.payloadHash,
		&row.providerID, &row.externalTransactionID, &row.referenceExternalTxnID,
		&row.referenceTransactionID,
		&row.failureCode, &row.resultBalanceMinor,
		&row.attempt, &row.nextAttemptAt,
		&row.createdAt, &row.updatedAt,
	}
}

func (row *txnRow) dominio() *wagering.WagerTransaction {
	currency := moedaGo(row.currency)

	return wagering.Rehydrate(
		wagering.TransactionID(row.id),
		wallet.WalletID(row.walletID),
		wallet.PlayerID(row.playerID),
		wagering.Kind(row.kind),
		wagering.Status(row.status),
		dinheiroMinor(row.amountMinor, currency),
		row.gameID,
		row.roundID,
		row.idempotencyKey,
		wagering.Hash(row.payloadHash),
		texto(row.providerID),
		texto(row.externalTransactionID),
		texto(row.referenceExternalTxnID),
		wagering.TransactionID(texto(row.referenceTransactionID)),
		texto(row.failureCode),
		dinheiroMinorPtr(row.resultBalanceMinor, currency),
		int(row.attempt),
		tempo(row.nextAttemptAt),
		row.createdAt,
		row.updatedAt,
	)
}

// ---------------------------------------------------------------------------
// Helpers de nulabilidade
// ---------------------------------------------------------------------------

// nuloSeVazio converte string vazia em NULL: as colunas de metadados de
// provedor são nulas em OPENING, e os índices parciais da Fase 4 dependem disso.
func nuloSeVazio(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nuloSe devolve o valor, ou nil quando o booleano for falso.
func nuloSe(v int64, presente bool) *int64 {
	if !presente {
		return nil
	}
	return &v
}

// nuloSeTempo converte instante zero em NULL.
func nuloSeTempo(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// texto desreferencia um ponteiro de string, devolvendo vazio quando for nil.
func texto(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// tempo desreferencia um ponteiro de tempo, devolvendo zero quando for nil.
func tempo(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// dinheiroMinorPtr reidrata uma coluna de valor anulável.
//
// Devolve ponteiro para preservar a diferença entre "não calculado" (NULL) e
// "zero" (carteira zerada). Achatar os dois em zero faria a operação em
// PENDING parecer uma operação que terminou com saldo zero.
func dinheiroMinorPtr(minor *int64, currency money.Currency) *money.Money {
	if minor == nil {
		return nil
	}
	m := dinheiroMinor(*minor, currency)
	return &m
}

// nuloSeDinheiro converte o saldo resultante em coluna anulável.
func nuloSeDinheiro(m money.Money, presente bool) *int64 {
	if !presente {
		return nil
	}
	minor := m.Minor()
	return &minor
}
