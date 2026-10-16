package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// InboxRepo registra as mensagens consumidas, para não processar duas vezes.
type InboxRepo interface {
	// InsertIfAbsent grava a mensagem e informa se ela é nova.
	//
	// O retorno booleano é o coração da deduplicação: `false` significa que a
	// mensagem já foi recebida por este consumidor, e quem chamou decide entre
	// pular (mesmo hash) ou auditar conflito (hash diferente).
	//
	// InsertIfAbsent em vez de Exists + Insert porque o UNIQUE do banco resolve
	// a corrida de graça — duas instâncias recebendo a mesma mensagem ao mesmo
	// tempo não conseguem as duas inserir.
	InsertIfAbsent(ctx context.Context, m InboxMessage) (inseriu bool, err error)

	// Get lê a mensagem registrada.
	Get(ctx context.Context, consumerName, messageID string) (*InboxMessage, error)

	// MarkProcessed marca a conclusão, com o instante.
	MarkProcessed(ctx context.Context, consumerName, messageID string, now time.Time) error

	// MarkFailed marca a falha, guardando o código estável.
	MarkFailed(ctx context.Context, consumerName, messageID, failureCode string, now time.Time) error
}

// InboxMessage é a forma de transporte da mensagem de entrada.
type InboxMessage struct {
	ID           string
	ConsumerName string
	MessageID    string
	BodyHash     string
	Status       string
	FailureCode  string
	ReceivedAt   time.Time
	CompletedAt  time.Time
}

type inboxRepo struct {
	q querier
}

// NewInboxRepo cria o repositório sobre o pool ou sobre uma transação.
func NewInboxRepo(q querier) InboxRepo { return &inboxRepo{q: q} }

const inboxColumns = `id, consumer_name, message_id, body_hash, status, failure_code, received_at, completed_at`

// InsertIfAbsent tenta inserir e traduz a violação de unicidade em resultado
// normal, não em erro: mensagem repetida é o caminho esperado do consumidor.
func (r *inboxRepo) InsertIfAbsent(ctx context.Context, m InboxMessage) (bool, error) {
	// ON CONFLICT DO NOTHING é a forma atômica: o banco decide quem insere,
	// sem janela entre consultar e gravar.
	const sql = `
		INSERT INTO inbox_messages (id, consumer_name, message_id, body_hash, status, received_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (consumer_name, message_id) DO NOTHING`

	status := m.Status
	if status == "" {
		status = "RECEIVED"
	}
	recebidoEm := m.ReceivedAt
	if recebidoEm.IsZero() {
		recebidoEm = time.Now().UTC()
	}

	tag, err := r.q.Exec(ctx, sql, m.ID, m.ConsumerName, m.MessageID, m.BodyHash, status, recebidoEm)
	if err != nil {
		return false, Classify(err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *inboxRepo) Get(ctx context.Context, consumerName, messageID string) (*InboxMessage, error) {
	const sql = `SELECT ` + inboxColumns + `
		FROM inbox_messages
		WHERE consumer_name = $1 AND message_id = $2`

	var (
		m           InboxMessage
		failureCode *string
		completedAt *time.Time
	)

	err := r.q.QueryRow(ctx, sql, consumerName, messageID).Scan(
		&m.ID, &m.ConsumerName, &m.MessageID, &m.BodyHash, &m.Status,
		&failureCode, &m.ReceivedAt, &completedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, Classify(err)
	}

	m.FailureCode = texto(failureCode)
	m.CompletedAt = tempo(completedAt)
	return &m, nil
}

func (r *inboxRepo) MarkProcessed(ctx context.Context, consumerName, messageID string, now time.Time) error {
	const sql = `
		UPDATE inbox_messages
		SET status = 'PROCESSED', completed_at = $3, failure_code = NULL
		WHERE consumer_name = $1 AND message_id = $2`

	return r.marcar(ctx, sql, consumerName, messageID, now)
}

func (r *inboxRepo) MarkFailed(ctx context.Context, consumerName, messageID, failureCode string, now time.Time) error {
	const sql = `
		UPDATE inbox_messages
		SET status = 'FAILED', completed_at = $3, failure_code = $4
		WHERE consumer_name = $1 AND message_id = $2`

	tag, err := r.q.Exec(ctx, sql, consumerName, messageID, now, nuloSeVazio(failureCode))
	if err != nil {
		return Classify(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// marcar executa um UPDATE de conclusão de uma mensagem.
func (r *inboxRepo) marcar(ctx context.Context, sql, consumerName, messageID string, now time.Time) error {
	tag, err := r.q.Exec(ctx, sql, consumerName, messageID, now)
	if err != nil {
		return Classify(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
