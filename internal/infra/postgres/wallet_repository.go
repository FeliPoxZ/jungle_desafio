package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"

	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
)

type walletRepo struct {
	q querier
}

// NewWalletRepo cria o repositório sobre o pool ou sobre uma transação.
func NewWalletRepo(q querier) ports.WalletRepository { return &walletRepo{q: q} }

const walletColumns = `id, player_id, currency, balance_minor, version, created_at, updated_at`

func (r *walletRepo) Insert(ctx context.Context, w *wallet.Wallet) error {
	const sql = `
		INSERT INTO wallets (id, player_id, currency, balance_minor, version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`

	_, err := r.q.Exec(ctx, sql,
		string(w.ID()),
		string(w.PlayerID()),
		moedaDB(w.Currency()),
		w.Balance().Minor(),
		w.Version(),
		w.CreatedAt(),
		w.UpdatedAt(),
	)
	return Classify(err)
}

func (r *walletRepo) GetByID(ctx context.Context, id ids.WalletID) (*wallet.Wallet, error) {
	const sql = `SELECT ` + walletColumns + ` FROM wallets WHERE id = $1`
	return r.um(ctx, sql, string(id))
}

func (r *walletRepo) GetByPlayerAndCurrency(ctx context.Context, playerID ids.PlayerID, currency money.Currency) (*wallet.Wallet, error) {
	const sql = `SELECT ` + walletColumns + ` FROM wallets WHERE player_id = $1 AND currency = $2`
	return r.um(ctx, sql, string(playerID), moedaDB(currency))
}

func (r *walletRepo) GetByIDForUpdate(ctx context.Context, id ids.WalletID) (*wallet.Wallet, error) {
	// FOR UPDATE trava a linha até o fim da transação. É este lock que
	// serializa duas operações concorrentes sobre a mesma carteira.
	const sql = `SELECT ` + walletColumns + ` FROM wallets WHERE id = $1 FOR UPDATE`
	return r.um(ctx, sql, string(id))
}

// um executa uma consulta que devolve exatamente uma carteira.
func (r *walletRepo) um(ctx context.Context, sql string, args ...any) (*wallet.Wallet, error) {
	var (
		rawID       string
		rawPlayerID string
		rawCurrency string
		balance     int64
		version     int64
		createdAt   time.Time
		updatedAt   time.Time
	)

	err := r.q.QueryRow(ctx, sql, args...).Scan(
		&rawID, &rawPlayerID, &rawCurrency, &balance, &version, &createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, Classify(err)
	}

	return wallet.Rehydrate(
		ids.WalletID(rawID),
		ids.PlayerID(rawPlayerID),
		dinheiroMinor(balance, moedaGo(rawCurrency)),
		version,
		createdAt,
		updatedAt,
	), nil
}

func (r *walletRepo) Update(ctx context.Context, id ids.WalletID, expectedVersion int64, balance money.Money) error {
	// `AND version = $3` é o controle otimista. Se ninguém mexeu na linha, uma
	// linha é afetada; se alguém mexeu, ZERO linhas e devolvemos conflito em
	// vez de sobrescrever.
	const sql = `
		UPDATE wallets
		SET balance_minor = $2, version = version + 1
		WHERE id = $1 AND version = $3`

	tag, err := r.q.Exec(ctx, sql, string(id), balance.Minor(), expectedVersion)
	if err != nil {
		return Classify(err)
	}
	if tag.RowsAffected() == 0 {
		// Ou a carteira não existe, ou a versão mudou por outro caminho.
		//
		// Não é violação de unicidade: é o controle otimista recusando
		// sobrescrever um saldo que mudou depois da leitura. O serviço trata
		// como conflito de concorrência e decide entre retry e erro.
		return ErrConflict
	}
	return nil
}
