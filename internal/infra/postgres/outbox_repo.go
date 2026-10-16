package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// OutboxRepo persiste os eventos a publicar, na MESMA transação do domínio.
//
// # Por que a outbox existe
//
// Publicar no SQS dentro da transação do banco não funciona: o SQS é externo e
// pode aceitar a mensagem para depois a transação falhar (evento publicado de
// operação que não existiu), ou a transação comitar e a publicação falhar
// (operação sem evento). Gravar o evento na MESMA transação resolve: ou os dois
// acontecem, ou nenhum. A publicação vira um passo separado, idempotente e
// repetível.
type OutboxRepo interface {
	// Insert grava o evento para publicação futura.
	Insert(ctx context.Context, e OutboxEvent) error

	// ClaimBatch reserva um lote de eventos para publicação.
	//
	// Usa FOR UPDATE SKIP LOCKED: duas instâncias pegam lotes diferentes, e
	// nenhuma fica esperando pela linha que a outra travou. É isso que permite
	// rodar vários publishers sem duplicar evento nem serializar o worker.
	//
	// A reserva é limitada por tempo (lease): se o processo morrer no meio, o
	// evento volta a ficar disponível quando o lease expirar, em vez de ficar
	// preso para sempre.
	ClaimBatch(ctx context.Context, limit int, lease time.Duration, now time.Time) ([]OutboxEvent, error)

	// MarkPublished marca o evento como publicado.
	MarkPublished(ctx context.Context, eventID string, now time.Time) error

	// MarkRetry devolve o evento à fila com backoff, contando a tentativa.
	//
	// O incremento de attempts é feito no banco ($ + 1), não calculado em Go:
	// duas instâncias falhando ao mesmo tempo contariam a mesma tentativa se o
	// valor viesse do processo.
	MarkRetry(ctx context.Context, eventID string, nextAttemptAt time.Time, lastError string) error

	// PendingStats devolve quantos eventos estão pendentes e a idade do mais
	// antigo, para o gauge de métrica.
	PendingStats(ctx context.Context, now time.Time) (pendentes int64, maisAntigo time.Duration, err error)
}

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
func NewOutboxRepo(q querier) OutboxRepo { return &outboxRepo{q: q} }

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

func (r *outboxRepo) Insert(ctx context.Context, e OutboxEvent) error {
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

func (r *outboxRepo) ClaimBatch(ctx context.Context, limit int, lease time.Duration, now time.Time) ([]OutboxEvent, error) {
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

	var saida []OutboxEvent
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

func (r *outboxRepo) PendingStats(ctx context.Context, now time.Time) (int64, time.Duration, error) {
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
	return pendentes, idade, nil
}

// scanOutbox lê uma linha de outbox_events, inclusive a partir de RETURNING.
func scanOutbox(rows pgx.Rows) (OutboxEvent, error) {
	var (
		e           OutboxEvent
		lastError   *string
		publishedAt *time.Time
	)

	err := rows.Scan(
		&e.EventID, &e.AggregateID, &e.AggregateType, &e.EventType, &e.Payload,
		&e.OccurredAt, &e.AvailableAt, &e.Attempts, &lastError, &publishedAt, &e.CreatedAt,
	)
	if err != nil {
		return OutboxEvent{}, Classify(err)
	}

	e.LastError = texto(lastError)
	e.PublishedAt = tempo(publishedAt)
	return e, nil
}

var _ = errors.Is
