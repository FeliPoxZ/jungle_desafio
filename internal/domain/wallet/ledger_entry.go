package wallet

import (
	"fmt"
	"strings"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
)

// Direction é o sentido de uma movimentação no ledger.
type Direction string

const (
	// DirectionCredit soma ao saldo.
	DirectionCredit Direction = "CREDIT"
	// DirectionDebit desconta do saldo.
	DirectionDebit Direction = "DEBIT"
)

// ParseDirection converte a string vinda do banco na direção tipada.
func ParseDirection(s string) (Direction, error) {
	d := Direction(s)
	if err := d.Validate(); err != nil {
		return "", err
	}
	return d, nil
}

// Validate confere se a direção é uma das previstas.
func (d Direction) Validate() error {
	if d != DirectionCredit && d != DirectionDebit {
		return invalidEntry("direção inválida: %q (esperado DEBIT ou CREDIT)", d)
	}
	return nil
}

// String devolve a direção como aparece no banco e no JSON.
func (d Direction) String() string { return string(d) }

// TransactionID identifica a operação que originou o lançamento.
type TransactionID string

// Entry é uma linha do ledger: append-only e imutável.
//
// Guarda os dois saldos (antes e depois) porque o ledger precisa ser auditável
// sozinho: quem lê a linha consegue conferir a aritmética sem depender do saldo
// atual da carteira.
type Entry struct {
	id            string
	walletID      WalletID
	transactionID TransactionID
	direction     Direction
	amount        money.Money
	balanceBefore money.Money
	balanceAfter  money.Money
	createdAt     time.Time
}

// NewEntry cria o lançamento e confere a aritmética: balanceAfter tem que ser
// exatamente balanceBefore mais (crédito) ou menos (débito) o valor.
//
// Essa checagem aqui é redundante com o CHECK do banco na Fase 4, e é de
// propósito: o erro nasce no domínio, com Code estável, antes de virar falha
// de constraint.
func NewEntry(
	walletID WalletID,
	direction Direction,
	amount money.Money,
	balanceBefore money.Money,
	now time.Time,
) (Entry, error) {
	if err := direction.Validate(); err != nil {
		return Entry{}, err
	}
	if err := amount.Currency().Validate(); err != nil {
		return Entry{}, invalidEntry("moeda do lançamento inválida: %v", err)
	}
	if !amount.IsPositive() {
		return Entry{}, invalidEntry("valor do lançamento precisa ser maior que zero, veio %s", amount)
	}
	if balanceBefore.Currency() != amount.Currency() {
		return Entry{}, &Error{
			Code: CodeWalletCurrencyMismatch,
			Message: fmt.Sprintf("lançamento em %q com saldo em %q",
				amount.Currency(), balanceBefore.Currency()),
		}
	}
	if now.IsZero() {
		return Entry{}, invalidEntry("instante do lançamento é obrigatório")
	}

	var (
		after money.Money
		err   error
	)
	if direction == DirectionDebit {
		after, err = balanceBefore.Sub(amount)
	} else {
		after, err = balanceBefore.Add(amount)
	}
	if err != nil {
		return Entry{}, invalidEntry("saldo posterior inválido: %v", err)
	}
	if after.IsNegative() {
		return Entry{}, invalidEntry("saldo posterior negativo: %s", after)
	}

	return Entry{
		id:            newEntryID(walletID, direction, amount, balanceBefore, now),
		walletID:      walletID,
		direction:     direction,
		amount:        amount,
		balanceBefore: balanceBefore,
		balanceAfter:  after,
		createdAt:     now,
	}, nil
}

// RehydrateEntry reconstrói o lançamento do que está persistido, sem revalidar
// nem recalcular nada.
func RehydrateEntry(
	id string,
	walletID WalletID,
	transactionID TransactionID,
	direction Direction,
	amount money.Money,
	balanceBefore money.Money,
	balanceAfter money.Money,
	createdAt time.Time,
) *Entry {
	return &Entry{
		id:            id,
		walletID:      walletID,
		transactionID: transactionID,
		direction:     direction,
		amount:        amount,
		balanceBefore: balanceBefore,
		balanceAfter:  balanceAfter,
		createdAt:     createdAt,
	}
}

// ID devolve o identificador do lançamento (gerado pelo banco na Fase 4; aqui
// é derivado para o objeto já nascer completo).
func (e Entry) ID() string { return e.id }

// WalletID devolve a carteira do lançamento.
func (e Entry) WalletID() WalletID { return e.walletID }

// TransactionID devolve a operação que originou o lançamento.
func (e Entry) TransactionID() TransactionID { return e.transactionID }

// Direction devolve o sentido da movimentação.
func (e Entry) Direction() Direction { return e.direction }

// Amount devolve o valor movimentado, sempre positivo.
func (e Entry) Amount() money.Money { return e.amount }

// BalanceBefore devolve o saldo antes da movimentação.
func (e Entry) BalanceBefore() money.Money { return e.balanceBefore }

// BalanceAfter devolve o saldo depois da movimentação.
func (e Entry) BalanceAfter() money.Money { return e.balanceAfter }

// CreatedAt devolve o instante da movimentação.
func (e Entry) CreatedAt() time.Time { return e.createdAt }

// newEntryID deriva um identificador determinístico do conteúdo do lançamento.
//
// Serve para o domínio produzir um objeto completo antes de existir banco. Na
// Fase 4 a coluna id passa a ser gerada pelo banco (uuid v7) e este valor deixa
// de ser usado como chave primária.
func newEntryID(
	walletID WalletID,
	direction Direction,
	amount money.Money,
	balanceBefore money.Money,
	now time.Time,
) string {
	var b strings.Builder
	b.WriteString(string(walletID))
	b.WriteByte('|')
	b.WriteString(direction.String())
	b.WriteByte('|')
	b.WriteString(amount.String())
	b.WriteByte('|')
	b.WriteString(balanceBefore.String())
	b.WriteByte('|')
	b.WriteString(now.UTC().Format(time.RFC3339Nano))
	return b.String()
}
