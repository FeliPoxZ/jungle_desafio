package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"

	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
)

type txnRepo struct {
	q querier
}

// NewTxnRepo cria o repositório sobre o pool ou sobre uma transação.
func NewTxnRepo(q querier) ports.TransactionRepository { return &txnRepo{q: q} }

const txnColumns = `
	id, wallet_id, player_id, kind, status, amount_minor, currency,
	game_id, round_id, idempotency_key, payload_hash,
	provider_id, external_transaction_id, reference_external_txn_id, reference_transaction_id,
	correlation_id,
	failure_code, result_balance_minor,
	reference_resolution_attempt, next_resolution_attempt_at,
	created_at, updated_at`

// txnColumnsComAlias é a mesma lista qualificada pelo alias `t`, para o
// `RETURNING` do claim.
//
// O alias é necessário pelo mesmo motivo do `ClaimBatch` da outbox: o `UPDATE`
// tem um `FROM` (a subconsulta do lote) que também expõe `id`, e sem qualificar o
// PostgreSQL recusa com "column reference id is ambiguous" (42702) — o defeito 10
// da §15 do ARCHITECTURE.
const txnColumnsComAlias = `
	t.id, t.wallet_id, t.player_id, t.kind, t.status, t.amount_minor, t.currency,
	t.game_id, t.round_id, t.idempotency_key, t.payload_hash,
	t.provider_id, t.external_transaction_id, t.reference_external_txn_id, t.reference_transaction_id,
	t.correlation_id,
	t.failure_code, t.result_balance_minor,
	t.reference_resolution_attempt, t.next_resolution_attempt_at,
	t.created_at, t.updated_at`

func (r *txnRepo) Insert(ctx context.Context, t *wagering.WagerTransaction) error {
	const sql = `
		INSERT INTO wager_transactions (
			id, wallet_id, player_id, kind, status, amount_minor, currency,
			game_id, round_id, idempotency_key, payload_hash,
			provider_id, external_transaction_id, reference_external_txn_id,
			correlation_id,
			reference_resolution_attempt,
			created_at, updated_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`

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
		nuloSeVazio(t.CorrelationID()),
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

func (r *txnRepo) GetByReferenceAndKind(ctx context.Context, referenceID wagering.TransactionID, kind wagering.Kind) (*wagering.WagerTransaction, error) {
	const sql = `SELECT ` + txnColumns + `
		FROM wager_transactions
		WHERE reference_transaction_id = $1 AND kind = $2`

	return r.um(ctx, sql, string(referenceID), kind.String())
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

// ClaimPendingReferencesDue reivindica o lote do worker de referências.
//
// # A ordem, e por que ela não é `next_resolution_attempt_at`
//
// A subconsulta ordena por `next_resolution_attempt_at` para decidir QUAIS linhas
// entram no lote (as mais vencidas primeiro, com o `LIMIT`). Mas o `RETURNING` do
// `UPDATE` devolve as linhas DEPOIS da alteração — e a alteração é exatamente
// empurrar `next_resolution_attempt_at` para o lease. Ou seja: o valor que
// ordenava acabou de virar o mesmo para todo o lote, e usá-lo no `ORDER BY` de
// fora não ordenaria nada (é a mesma armadilha do defeito 19, com outra causa: lá
// era o `RETURNING` que não prometia ordem, aqui é a coluna de ordem que foi
// sobrescrita).
//
// A ordem de fora é `created_at, id`: `created_at` é IMUTÁVEL, e o `id` (UUID v7)
// desempata. A ordem do lote não é uma garantia de negócio — cada linha é
// independente —, mas um lote previsível é o que permite ao teste afirmar o que
// foi processado primeiro, e o reaper não deve depender de sorte.
func (r *txnRepo) ClaimPendingReferencesDue(ctx context.Context, limit int, leaseFor time.Duration, now time.Time) ([]*wagering.WagerTransaction, error) {
	if limit <= 0 {
		return nil, nil
	}

	const sql = `
		WITH reivindicadas AS (
			UPDATE wager_transactions AS t
			SET next_resolution_attempt_at = $3
			FROM (
				SELECT id
				FROM wager_transactions
				WHERE status = 'PENDING_REFERENCE'
				  AND next_resolution_attempt_at IS NOT NULL
				  AND next_resolution_attempt_at <= $1
				ORDER BY next_resolution_attempt_at
				LIMIT $2
				FOR UPDATE SKIP LOCKED
			) AS lote
			WHERE t.id = lote.id
			RETURNING ` + txnColumnsComAlias + `
		)
		SELECT ` + txnColumns + `
		FROM reivindicadas
		ORDER BY created_at, id`

	return r.varios(ctx, sql, now, limit, now.Add(leaseFor))
}

// ListPendingStale devolve as transações presas em PENDING — a fila do reaper.
//
// O critério é `updated_at`, e não `created_at`: o que interessa é "esta linha não
// é tocada há mais de PENDING_MAX_AGE". Uma linha PENDING é tocada quando o
// processamento a leva para outro estado, então `updated_at` velho é exatamente
// "ninguém concluiu isto".
func (r *txnRepo) ListPendingStale(ctx context.Context, now time.Time, maxAge time.Duration, limit int) ([]*wagering.WagerTransaction, error) {
	if limit <= 0 {
		return nil, nil
	}

	const sql = `SELECT ` + txnColumns + `
		FROM wager_transactions
		WHERE status = 'PENDING'
		  AND updated_at <= $1
		ORDER BY updated_at, id
		LIMIT $2`

	return r.varios(ctx, sql, now.Add(-maxAge), limit)
}

func (r *txnRepo) ListByWallet(ctx context.Context, walletID ids.WalletID, limit int, cursor *ports.Cursor) ([]*wagering.WagerTransaction, error) {
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
	correlationID          *string
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
		&row.correlationID,
		&row.failureCode, &row.resultBalanceMinor,
		&row.attempt, &row.nextAttemptAt,
		&row.createdAt, &row.updatedAt,
	}
}

func (row *txnRow) dominio() *wagering.WagerTransaction {
	currency := moedaGo(row.currency)

	return wagering.Rehydrate(
		wagering.TransactionID(row.id),
		ids.WalletID(row.walletID),
		ids.PlayerID(row.playerID),
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
		texto(row.correlationID),
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
