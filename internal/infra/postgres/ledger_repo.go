package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
)

// LedgerRepo persiste e lê lançamentos do ledger.
//
// # Não existe Update nem Delete — de propósito
//
// O ledger é append-only: a imutabilidade é imposta por trigger no banco
// (Fase 4). Não ter método de alteração aqui é a primeira barreira; se alguém
// precisar "corrigir" um lançamento, a resposta é movimentação nova (estorno),
// não reescrever a história.
type LedgerRepo interface {
	// Insert grava o lançamento. Devolve ErrConflict se a operação já tiver
	// lançamento (UNIQUE wallet_id + transaction_id).
	Insert(ctx context.Context, e wallet.Entry) error

	// GetByTransaction lê o lançamento de uma operação.
	GetByTransaction(ctx context.Context, walletID wallet.WalletID, transactionID string) (*wallet.Entry, error)

	// ListByWallet devolve os lançamentos de uma carteira em ordem cronológica,
	// com cursor.
	ListByWallet(ctx context.Context, walletID wallet.WalletID, limit int, cursor *Cursor) ([]*wallet.Entry, error)

	// SumSignedByWallet devolve a soma dos valores com sinal (crédito positivo,
	// débito negativo) em unidades mínimas.
	//
	// É a base da reconciliação: comparar esta soma com o saldo armazenado da
	// carteira revela divergência. Soma no banco (e não em Go) porque o volume
	// pode ser grande e o BIGINT do PostgreSQL é exato.
	SumSignedByWallet(ctx context.Context, walletID wallet.WalletID) (int64, error)
}

type ledgerRepo struct {
	q querier
}

// NewLedgerRepo cria o repositório sobre o pool ou sobre uma transação.
func NewLedgerRepo(q querier) LedgerRepo { return &ledgerRepo{q: q} }

const ledgerColumns = `
	id, wallet_id, transaction_id, direction, amount_minor,
	balance_before_minor, balance_after_minor, created_at`

func (r *ledgerRepo) Insert(ctx context.Context, e wallet.Entry) error {
	const sql = `
		INSERT INTO wallet_ledger_entries (
			id, wallet_id, transaction_id, direction, amount_minor,
			balance_before_minor, balance_after_minor, created_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`

	_, err := r.q.Exec(ctx, sql,
		e.ID(),
		string(e.WalletID()),
		string(e.TransactionID()),
		e.Direction().String(),
		e.Amount().Minor(),
		e.BalanceBefore().Minor(),
		e.BalanceAfter().Minor(),
		e.CreatedAt(),
	)
	return Classify(err)
}

func (r *ledgerRepo) GetByTransaction(ctx context.Context, walletID wallet.WalletID, transactionID string) (*wallet.Entry, error) {
	const sql = `SELECT ` + ledgerColumns + `
		FROM wallet_ledger_entries
		WHERE wallet_id = $1 AND transaction_id = $2`

	var (
		id          string
		rawWalletID string
		rawTxnID    string
		direction   string
		amountMinor int64
		beforeMinor int64
		afterMinor  int64
		createdAt   time.Time
	)

	err := r.q.QueryRow(ctx, sql, string(walletID), transactionID).Scan(
		&id, &rawWalletID, &rawTxnID, &direction,
		&amountMinor, &beforeMinor, &afterMinor, &createdAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, Classify(err)
	}

	// A moeda não está no ledger (só o valor em unidades mínimas): ela vem da
	// carteira. Consultamos o mínimo necessário para reidratar o Money.
	rawCurrency, err := r.moedaDaCarteira(ctx, walletID)
	if err != nil {
		return nil, err
	}
	currency := moedaGo(rawCurrency)

	entry := wallet.RehydrateEntry(
		id,
		wallet.WalletID(rawWalletID),
		wallet.TransactionID(rawTxnID),
		wallet.Direction(direction),
		dinheiroMinor(amountMinor, currency),
		dinheiroMinor(beforeMinor, currency),
		dinheiroMinor(afterMinor, currency),
		createdAt,
	)
	return entry, nil
}

// moedaDaCarteira lê a moeda da carteira dona do lançamento.
func (r *ledgerRepo) moedaDaCarteira(ctx context.Context, walletID wallet.WalletID) (string, error) {
	var currency string
	err := r.q.QueryRow(ctx, `SELECT currency FROM wallets WHERE id = $1`, string(walletID)).Scan(&currency)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", Classify(err)
	}
	return currency, nil
}

func (r *ledgerRepo) ListByWallet(ctx context.Context, walletID wallet.WalletID, limit int, cursor *Cursor) ([]*wallet.Entry, error) {
	// JOIN com wallets para trazer a moeda: o ledger guarda só o valor em
	// unidades mínimas, e o Money precisa da moeda para existir.
	base := `SELECT e.id, e.wallet_id, e.transaction_id, e.direction, e.amount_minor,
	                e.balance_before_minor, e.balance_after_minor, e.created_at, w.currency
	         FROM wallet_ledger_entries e
	         JOIN wallets w ON w.id = e.wallet_id
	         WHERE e.wallet_id = $1`

	var (
		sql  string
		args []any
	)
	if cursor == nil {
		sql = base + ` ORDER BY e.created_at, e.id LIMIT $2`
		args = []any{string(walletID), limit}
	} else {
		sql = base + ` AND (e.created_at, e.id) > ($2, $3) ORDER BY e.created_at, e.id LIMIT $4`
		args = []any{string(walletID), cursor.CreatedAt, cursor.ID, limit}
	}

	rows, err := r.q.Query(ctx, sql, args...)
	if err != nil {
		return nil, Classify(err)
	}
	defer rows.Close()

	var saida []*wallet.Entry
	for rows.Next() {
		var (
			id          string
			rawWalletID string
			rawTxnID    string
			direction   string
			amountMinor int64
			beforeMinor int64
			afterMinor  int64
			createdAt   time.Time
			rawCurrency string
		)
		if err := rows.Scan(&id, &rawWalletID, &rawTxnID, &direction,
			&amountMinor, &beforeMinor, &afterMinor, &createdAt, &rawCurrency); err != nil {
			return nil, Classify(err)
		}

		currency := moedaGo(rawCurrency)
		entry := wallet.RehydrateEntry(
			id,
			wallet.WalletID(rawWalletID),
			wallet.TransactionID(rawTxnID),
			wallet.Direction(direction),
			dinheiroMinor(amountMinor, currency),
			dinheiroMinor(beforeMinor, currency),
			dinheiroMinor(afterMinor, currency),
			createdAt,
		)
		saida = append(saida, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, Classify(err)
	}
	return saida, nil
}

func (r *ledgerRepo) SumSignedByWallet(ctx context.Context, walletID wallet.WalletID) (int64, error) {
	// A soma é feita no banco: exata (BIGINT), sem trazer milhares de linhas
	// para somar em Go. COALESCE cobre carteira sem lançamento.
	const sql = `
		SELECT COALESCE(SUM(
			CASE direction WHEN 'CREDIT' THEN amount_minor ELSE -amount_minor END
		), 0)
		FROM wallet_ledger_entries
		WHERE wallet_id = $1`

	var soma int64
	if err := r.q.QueryRow(ctx, sql, string(walletID)).Scan(&soma); err != nil {
		return 0, Classify(err)
	}
	return soma, nil
}

// ---------------------------------------------------------------------------
// Cursor de paginação
// ---------------------------------------------------------------------------

// EncodeCursor serializa a posição de paginação em base64url, no formato
// documentado no plano: base64url(createdAt RFC3339Nano | entryId).
//
// Cursor opaco de propósito: o cliente não deve montar nem interpretar o valor,
// só devolvê-lo na próxima página. Assim a ordenação pode mudar sem quebrar o
// contrato.
func EncodeCursor(c Cursor) string {
	bruto := c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID
	return base64.RawURLEncoding.EncodeToString([]byte(bruto))
}

// DecodeCursor lê o cursor devolvido pelo cliente.
func DecodeCursor(s string) (Cursor, error) {
	if strings.TrimSpace(s) == "" {
		return Cursor{}, fmt.Errorf("postgres: cursor vazio")
	}

	bruto, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, fmt.Errorf("postgres: cursor não é base64url válido: %w", err)
	}

	partes := strings.SplitN(string(bruto), "|", 2)
	if len(partes) != 2 {
		return Cursor{}, fmt.Errorf("postgres: cursor malformado")
	}

	instante, err := time.Parse(time.RFC3339Nano, partes[0])
	if err != nil {
		return Cursor{}, fmt.Errorf("postgres: cursor com instante inválido: %w", err)
	}
	if partes[1] == "" {
		return Cursor{}, fmt.Errorf("postgres: cursor sem identificador")
	}

	return Cursor{CreatedAt: instante, ID: partes[1]}, nil
}

// CursorDeEntries monta o cursor a partir do último lançamento da página.
func CursorDeEntries(entries []*wallet.Entry) *Cursor {
	if len(entries) == 0 {
		return nil
	}
	ultimo := entries[len(entries)-1]
	return &Cursor{CreatedAt: ultimo.CreatedAt(), ID: ultimo.ID()}
}
