package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
)

// ports.OutboxRepository persiste os eventos a publicar, na MESMA transação do domínio.
//
// # Por que a outbox existe
//
// Publicar no SQS dentro da transação do banco não funciona: o SQS é externo e
// pode aceitar a mensagem para depois a transação falhar (evento publicado de
// operação que não existiu), ou a transação comitar e a publicação falhar
// (operação sem evento). Gravar o evento na MESMA transação resolve: ou os dois
// acontecem, ou nenhum. A publicação vira um passo separado, idempotente e
// OutboxEvent é a forma de transporte do evento persistido.
type OutboxEvent struct {
	EventID       string
	AggregateID   string
	AggregateType string
	EventType     string
	Payload       []byte
	OccurredAt    time.Time
	AvailableAt   time.Time
	Attempts      int32
	LastError     string
	PublishedAt   time.Time
	CreatedAt     time.Time
}

type outboxRepo struct {
	q querier
}

// NewOutboxRepo cria o repositório sobre o pool ou sobre uma transação.
func NewOutboxRepo(q querier) ports.OutboxRepository { return &outboxRepo{q: q} }

const outboxColumns = `
	event_id, aggregate_id, aggregate_type, event_type, payload,
	occurred_at, available_at, attempts, last_error, published_at, created_at`

// outboxColumnsComAlias é a mesma lista qualificada pelo alias da tabela.
//
// Necessário no RETURNING do ClaimBatch: o UPDATE tem um FROM (subconsulta) que
// também expõe event_id, e sem o alias o PostgreSQL recusa com
// "column reference event_id is ambiguous" (42702).
const outboxColumnsComAlias = `
	o.event_id, o.aggregate_id, o.aggregate_type, o.event_type, o.payload,
	o.occurred_at, o.available_at, o.attempts, o.last_error, o.published_at, o.created_at`

func (r *outboxRepo) Insert(ctx context.Context, e ports.OutboxEvent) error {
	const sql = `
		INSERT INTO outbox_events (
			event_id, aggregate_id, aggregate_type, event_type, payload,
			occurred_at, available_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`

	occurredAt := e.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	availableAt := e.AvailableAt
	if availableAt.IsZero() {
		availableAt = occurredAt
	}

	_, err := r.q.Exec(ctx, sql,
		e.EventID, e.AggregateID, e.AggregateType, e.EventType, e.Payload,
		occurredAt, availableAt,
	)
	return Classify(err)
}

func (r *outboxRepo) ClaimBatch(ctx context.Context, limit int, lease time.Duration, now time.Time) ([]ports.OutboxEvent, error) {
	if limit <= 0 {
		return nil, nil
	}

	// O UPDATE ... FROM (SELECT ... FOR UPDATE SKIP LOCKED) reserva as linhas e
	// já devolve o conteúdo, em uma única ida ao banco.
	//
	// O lease é aplicado empurrando available_at para o futuro: enquanto o
	// instante não chega, outro publisher não pega o mesmo evento. Se este
	// processo morrer, o evento volta sozinho quando o prazo passar.
	const sql = `
		UPDATE outbox_events AS o
		SET available_at = $3
		FROM (
			SELECT event_id
			FROM outbox_events
			WHERE published_at IS NULL AND available_at <= $1
			ORDER BY occurred_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		) AS lote
		WHERE o.event_id = lote.event_id
		RETURNING ` + outboxColumnsComAlias

	rows, err := r.q.Query(ctx, sql, now, limit, now.Add(lease))
	if err != nil {
		return nil, Classify(err)
	}
	defer rows.Close()

	var saida []ports.OutboxEvent
	for rows.Next() {
		e, err := scanOutbox(rows)
		if err != nil {
			return nil, err
		}
		saida = append(saida, e)
	}
	if err := rows.Err(); err != nil {
		return nil, Classify(err)
	}
	return saida, nil
}

func (r *outboxRepo) MarkPublished(ctx context.Context, eventID string, now time.Time) error {
	const sql = `UPDATE outbox_events SET published_at = $2 WHERE event_id = $1`

	tag, err := r.q.Exec(ctx, sql, eventID, now)
	if err != nil {
		return Classify(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *outboxRepo) MarkRetry(ctx context.Context, eventID string, nextAttemptAt time.Time, lastError string) error {
	// attempts = attempts + 1 no BANCO: o contador não depende de ler antes.
	const sql = `
		UPDATE outbox_events
		SET attempts = attempts + 1,
		    available_at = $2,
		    last_error = $3
		WHERE event_id = $1 AND published_at IS NULL`

	tag, err := r.q.Exec(ctx, sql, eventID, nextAttemptAt, nuloSeVazio(lastError))
	if err != nil {
		return Classify(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *outboxRepo) PendingStats(ctx context.Context, now time.Time) (int, time.Duration, error) {
	const sql = `
		SELECT COUNT(*), COALESCE(MIN(occurred_at), $1)
		FROM outbox_events
		WHERE published_at IS NULL`

	var (
		pendentes  int64
		maisAntigo time.Time
	)
	if err := r.q.QueryRow(ctx, sql, now).Scan(&pendentes, &maisAntigo); err != nil {
		return 0, 0, Classify(err)
	}
	if pendentes == 0 {
		return 0, 0, nil
	}

	idade := now.Sub(maisAntigo)
	if idade < 0 {
		idade = 0
	}
	return int(pendentes), idade, nil
}

// scanOutbox lê uma linha de outbox_events, inclusive a partir de RETURNING.
func scanOutbox(rows pgx.Rows) (ports.OutboxEvent, error) {
	var (
		e           ports.OutboxEvent
		lastError   *string
		publishedAt *time.Time
	)

	err := rows.Scan(
		&e.EventID, &e.AggregateID, &e.AggregateType, &e.EventType, &e.Payload,
		&e.OccurredAt, &e.AvailableAt, &e.Attempts, &lastError, &publishedAt, &e.CreatedAt,
	)
	if err != nil {
		return ports.OutboxEvent{}, Classify(err)
	}

	e.LastError = texto(lastError)
	e.PublishedAt = tempo(publishedAt)
	return e, nil
}

var _ = errors.Is
