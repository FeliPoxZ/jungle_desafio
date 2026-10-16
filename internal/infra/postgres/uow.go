package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// UnitOfWork delimita a transação de uma operação de negócio.
//
// Toda operação financeira acontece em UMA transação: transação de aposta,
// carteira, lançamento no ledger, inbox e outbox. Se qualquer parte falhar, o
// dinheiro não se move e o evento não é publicado — não existe estado
// intermediário visível.
type UnitOfWork interface {
	Run(ctx context.Context, fn func(ctx context.Context, r Repos) error) error
	RunReadOnly(ctx context.Context, fn func(ctx context.Context, r Repos) error) error
}

// Repos agrupa os repositórios que participam da mesma transação.
//
// Os repositórios são criados POR TRANSAÇÃO, e não injetados como singleton:
// cada um carrega o `querier` da transação corrente, então dois `Run`
// simultâneos nunca compartilham conexão.
type Repos struct {
	Wallets      WalletRepo
	Transactions TxnRepo
	Ledger       LedgerRepo
	Inbox        InboxRepo
	Outbox       OutboxRepo
}

// Escopo transacional.

type uow struct {
	pool *Pool
}

// NewUnitOfWork cria a Unit of Work sobre o pool.
func NewUnitOfWork(pool *Pool) UnitOfWork { return &uow{pool: pool} }

// Run abre a transação, monta os repositórios nela e executa fn.
//
// # Nível de isolamento: READ COMMITTED + SELECT ... FOR UPDATE
//
// A alternativa seria SERIALIZABLE, que detecta conflitos automaticamente e
// aborta com erro 40001. Optamos por READ COMMITTED com lock explícito de linha
// porque:
//
//   - o conflito real do domínio é "duas operações na MESMA carteira", e o
//     FOR UPDATE serializa exatamente esse caso, sem serializar o banco inteiro;
//   - SERIALIZABLE exigiria retry em praticamente todo caminho de escrita, com
//     backoff, e ainda poderia abortar transações que não conflitam de verdade.
//
// Ou seja: preferimos pagar um lock de linha curto e determinístico em vez de
// um retry imprevisível. Isso está documentado no ARCHITECTURE.md.
//
// # Rollback
//
// O rollback é garantido por defer com contexto próprio: se a fn falhou porque
// o contexto foi cancelado, um Rollback com o MESMO contexto não executaria. O
// rollback é a última coisa que pode falhar, então ele desconecta do contexto do
// chamador.
func (u *uow) Run(ctx context.Context, fn func(ctx context.Context, r Repos) error) error {
	tx, err := u.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return Classify(fmt.Errorf("postgres: abrindo transação: %w", err))
	}

	// Se fn entra em pânico, a transação precisa ser descartada mesmo assim.
	commit := false
	defer func() {
		if commit {
			return
		}
		// Contexto desligado do chamador: rollback com contexto cancelado não roda.
		ctxRollback, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := tx.Rollback(ctxRollback); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			// Não há o que fazer além de não mascarar o erro original.
			_ = err
		}
	}()

	repos := Repos{
		Wallets:      NewWalletRepo(tx),
		Transactions: NewTxnRepo(tx),
		Ledger:       NewLedgerRepo(tx),
		Inbox:        NewInboxRepo(tx),
		Outbox:       NewOutboxRepo(tx),
	}

	if err := fn(ctx, repos); err != nil {
		// O erro volta como veio: pode ser erro de domínio (que o serviço
		// classifica) ou erro de banco (que já foi classificado aqui).
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return Classify(fmt.Errorf("postgres: commit: %w", err))
	}
	commit = true
	return nil
}

// RunReadOnly executa uma consulta sem abrir transação de escrita.
//
// Usado por leituras que precisam de consistência entre mais de uma query
// (reconciliação), onde ler em momentos diferentes daria um resultado que nunca
// existiu.
func (u *uow) RunReadOnly(ctx context.Context, fn func(ctx context.Context, r Repos) error) error {
	tx, err := u.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return Classify(fmt.Errorf("postgres: abrindo transação de leitura: %w", err))
	}

	commit := false
	defer func() {
		if commit {
			return
		}
		ctxRollback, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(ctxRollback)
	}()

	repos := Repos{
		Wallets:      NewWalletRepo(tx),
		Transactions: NewTxnRepo(tx),
		Ledger:       NewLedgerRepo(tx),
		Inbox:        NewInboxRepo(tx),
		Outbox:       NewOutboxRepo(tx),
	}

	if err := fn(ctx, repos); err != nil {
		return err
	}

	// Transação somente-leitura: commit ou rollback é indiferente, mas o commit
	// libera a transação de forma explícita.
	if err := tx.Commit(ctx); err != nil {
		return Classify(fmt.Errorf("postgres: commit de leitura: %w", err))
	}
	commit = true
	return nil
}
