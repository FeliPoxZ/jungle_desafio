package ledger

import (
	"errors"
	"testing"
	"time"

	domainerrors "github.com/FeliPoxZ/jungle_desafio/internal/domain/errors"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
)

var t0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// brl monta um Money em BRL a partir de uma string decimal.
func brl(t *testing.T, amount string) money.Money {
	t.Helper()
	m, err := money.Parse(amount, string(money.BRL))
	if err != nil {
		t.Fatalf("brl(%q): %v", amount, err)
	}
	return m
}

// usd monta um Money em USD, para os casos de moeda incompatível.
func usd(t *testing.T, amount string) money.Money {
	t.Helper()
	m, err := money.Parse(amount, "USD")
	if err != nil {
		t.Fatalf("usd(%q): %v", amount, err)
	}
	return m
}

// CodeOf extrai o Code de um erro de domínio.
//
// Agora delega para o pacote compartilhado: era uma função duplicada em três
// arquivos de teste, cada uma com a sua cópia da mesma lógica.
func CodeOf(err error) string { return domainerrors.CodeOf(err) }

// mesmoMoney compara dois valores monetários.
func mesmoMoney(a, b money.Money) bool {
	return a.Currency() == b.Currency() && a.Minor() == b.Minor()
}

func TestNewEntry(t *testing.T) {
	agora := t0
	saldo := brl(t, "1000.00")

	casos := []struct {
		nome      string
		direcao   Direction
		valor     money.Money
		queroEr   bool
		code      string
		queroApos string
	}{
		{"debito", DirectionDebit, brl(t, "25.00"), false, "", "975.00"},
		{"credito", DirectionCredit, brl(t, "25.00"), false, "", "1025.00"},
		{"debito zera", DirectionDebit, brl(t, "1000.00"), false, "", "0.00"},
		{"debito excede", DirectionDebit, brl(t, "1000.01"), true, CodeInvalidEntry, ""},
		{"valor zero", DirectionDebit, brl(t, "0.00"), true, CodeInvalidEntry, ""},
		{"valor negativo", DirectionDebit, money.FromMinor(-100, money.BRL), true, CodeInvalidEntry, ""},
		{"direcao vazia", Direction(""), brl(t, "1.00"), true, CodeInvalidEntry, ""},
		{"direcao invalida", Direction("TRANSFER"), brl(t, "1.00"), true, CodeInvalidEntry, ""},
		{"direcao minuscula", Direction("debit"), brl(t, "1.00"), true, CodeInvalidEntry, ""},
		{"moeda diferente", DirectionDebit, usd(t, "1.00"), true, CodeLedgerCurrencyMismatch, ""},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			entry, err := NewEntry("w-1", "txn-teste", c.direcao, c.valor, saldo, agora)
			if c.queroEr {
				if err == nil {
					t.Fatalf("NewEntry deveria falhar, devolveu %+v", entry)
				}
				if CodeOf(err) != c.code {
					t.Errorf("Code = %q, esperado %q", CodeOf(err), c.code)
				}
				if !entry.IsZero() {
					t.Error("entry deveria ser zero value em caso de erro")
				}
				return
			}

			if err != nil {
				t.Fatalf("NewEntry falhou: %v", err)
			}
			if got := entry.BalanceAfter().String(); got != c.queroApos {
				t.Errorf("balanceAfter = %s, esperado %s", got, c.queroApos)
			}
			if entry.BalanceAfter().IsNegative() {
				t.Errorf("balanceAfter negativo: %s", entry.BalanceAfter())
			}
			if !mesmoMoney(entry.BalanceBefore(), saldo) {
				t.Errorf("balanceBefore = %s", entry.BalanceBefore())
			}
			if entry.ID() == "" {
				t.Error("entry sem id")
			}
		})
	}
}

func TestNewEntryComInstanteZero(t *testing.T) {
	if _, err := NewEntry("w-1", "txn-teste", DirectionDebit, brl(t, "1.00"), brl(t, "10.00"), time.Time{}); err == nil {
		t.Error("instante zero deveria falhar")
	}
}

func TestNewEntryDeMoedaInvalida(t *testing.T) {
	invalido := money.FromMinor(100, money.Currency("xx"))
	if _, err := NewEntry("w-1", "txn-teste", DirectionDebit, invalido, invalido, t0); err == nil {
		t.Error("moeda inválida deveria falhar")
	}
}

func TestRehydrateEntry(t *testing.T) {
	// Reidratação não recalcula nem valida: reconstrói exatamente o que veio.
	entry := RehydrateEntry(
		"entry-1", "w-1", "txn-1", DirectionDebit,
		brl(t, "25.00"), brl(t, "1000.00"), brl(t, "975.00"), t0,
	)

	if entry.ID() != "entry-1" || entry.WalletID() != "w-1" || entry.TransactionID() != "txn-1" {
		t.Errorf("identidade: %q/%q/%q", entry.ID(), entry.WalletID(), entry.TransactionID())
	}
	if entry.Direction() != DirectionDebit {
		t.Errorf("direção = %q", entry.Direction())
	}
	if entry.BalanceBefore().String() != "1000.00" || entry.BalanceAfter().String() != "975.00" {
		t.Errorf("saldos = %s -> %s", entry.BalanceBefore(), entry.BalanceAfter())
	}
	if entry.Amount().String() != "25.00" {
		t.Errorf("valor = %s", entry.Amount())
	}
	if !entry.CreatedAt().Equal(t0) {
		t.Errorf("createdAt = %v", entry.CreatedAt())
	}
}

func TestDirectionValidateEParse(t *testing.T) {
	for _, s := range []string{"DEBIT", "CREDIT"} {
		d, err := ParseDirection(s)
		if err != nil {
			t.Errorf("ParseDirection(%q) falhou: %v", s, err)
		}
		if d.String() != s {
			t.Errorf("String() = %q, esperado %q", d.String(), s)
		}
	}

	for _, s := range []string{"", "debit", "credit", "TRANSFER", "DEBITO", " DEBIT"} {
		if _, err := ParseDirection(s); err == nil {
			t.Errorf("ParseDirection(%q) deveria falhar", s)
		} else if !errors.Is(err, ErrInvalidEntry) {
			t.Errorf("ParseDirection(%q): erro não é ErrInvalidEntry: %v", s, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Erros de domínio
// ---------------------------------------------------------------------------

func TestNewEntryComSaldoCorrompido(t *testing.T) {
	// balanceBefore acima do teto: o Add estoura e NewEntry tem que devolver
	// erro de lançamento inválido, não um valor absurdo.
	corrompido := money.FromMinor(9_000_000_000_000_000, money.BRL)

	if _, err := NewEntry("w-1", "txn-teste", DirectionCredit, brl(t, "1.00"), corrompido, t0); err == nil {
		t.Fatal("saldo posterior além do limite deveria falhar")
	} else if !errors.Is(err, ErrInvalidEntry) {
		t.Errorf("err = %v, esperado ErrInvalidEntry", err)
	}
}
