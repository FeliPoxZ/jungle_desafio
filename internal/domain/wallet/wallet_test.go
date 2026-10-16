package wallet

import (
	"errors"
	"testing"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ledger"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
)

var t0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func brl(t *testing.T, amount string) money.Money {
	t.Helper()
	m, err := money.Parse(amount, "BRL")
	if err != nil {
		t.Fatalf("Parse(%q) falhou: %v", amount, err)
	}
	return m
}

func usd(t *testing.T, amount string) money.Money {
	t.Helper()
	m, err := money.Parse(amount, "USD")
	if err != nil {
		t.Fatalf("Parse(%q) falhou: %v", amount, err)
	}
	return m
}

// novaCarteira cria uma carteira válida para os testes.
func novaCarteira(t *testing.T, saldo string) *Wallet {
	t.Helper()
	w, err := NewWallet("w-1", "p-1", brl(t, saldo), t0)
	if err != nil {
		t.Fatalf("NewWallet falhou: %v", err)
	}
	return w
}

// saldoMinor devolve o saldo em unidades mínimas, para comparação direta.
func saldoMinor(w *Wallet) int64 { return w.Balance().Minor() }

// mesmoMoney compara dois valores da mesma moeda. Falha o teste em vez de
// devolver erro porque nos cenários de teste a moeda já é sabidamente igual.
func mesmoMoney(a, b money.Money) bool {
	eq, err := a.Equal(b)
	if err != nil {
		panic("mesmoMoney com moedas diferentes: " + err.Error())
	}
	return eq
}

// ---------------------------------------------------------------------------
// NewWallet
// ---------------------------------------------------------------------------

func TestNewWallet(t *testing.T) {
	casos := []struct {
		nome     string
		id       WalletID
		playerID PlayerID
		saldo    string
		moeda    string
		now      time.Time
		queroEr  bool
		code     string
	}{
		{"saldo zero e permitido", "w-1", "p-1", "0.00", "BRL", t0, false, ""},
		{"saldo positivo", "w-1", "p-1", "1000.00", "BRL", t0, false, ""},
		{"um centavo", "w-1", "p-1", "0.01", "BRL", t0, false, ""},
		{"id vazio", "", "p-1", "10.00", "BRL", t0, true, CodeInvalidWallet},
		{"id so espacos", "   ", "p-1", "10.00", "BRL", t0, true, CodeInvalidWallet},
		{"playerID vazio", "w-1", "", "10.00", "BRL", t0, true, CodeInvalidWallet},
		{"playerID so espacos", "w-1", "  ", "10.00", "BRL", t0, true, CodeInvalidWallet},
		{"instante zero", "w-1", "p-1", "10.00", "BRL", time.Time{}, true, CodeInvalidWallet},
		{"moeda invalida no saldo", "w-1", "p-1", "10.00", "xx", t0, true, CodeInvalidWallet},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			var saldo money.Money
			var err error
			if c.moeda == "xx" {
				// FromMinor permite moeda inválida: é o caso de dado corrompido.
				saldo = money.FromMinor(1000, money.Currency("xx"))
			} else {
				saldo, err = money.Parse(c.saldo, c.moeda)
				if err != nil {
					t.Fatalf("Parse do saldo: %v", err)
				}
			}

			w, err := NewWallet(c.id, c.playerID, saldo, c.now)
			if c.queroEr {
				if err == nil {
					t.Fatalf("NewWallet deveria falhar, devolveu %+v", w)
				}
				if CodeOf(err) != c.code {
					t.Errorf("Code = %q, esperado %q", CodeOf(err), c.code)
				}
				if w != nil {
					t.Error("carteira deveria ser nil em caso de erro")
				}
				return
			}

			if err != nil {
				t.Fatalf("NewWallet falhou: %v", err)
			}
			if w.Version() != 1 {
				t.Errorf("Version() = %d, esperado 1", w.Version())
			}
			if !w.CreatedAt().Equal(t0) || !w.UpdatedAt().Equal(t0) {
				t.Errorf("createdAt/updatedAt deveriam ser iguais na criação")
			}
			if !mesmoMoney(w.Balance(), saldo) {
				t.Errorf("saldo = %s, esperado %s", w.Balance(), saldo)
			}
			if w.Currency() != saldo.Currency() {
				t.Errorf("moeda = %q", w.Currency())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Rehydrate
// ---------------------------------------------------------------------------

func TestRehydrateNaoReaplicaNemAltera(t *testing.T) {
	// README §Encapsulamento L89: a reidratação não pode reaplicar movimentações,
	// não pode mexer na versão nem nos instantes, e não emite evento.
	criacao := t0
	atualizacao := t0.Add(3 * time.Hour)
	saldo := brl(t, "975.00")

	w := Rehydrate("w-1", "p-1", saldo, 7, criacao, atualizacao)

	if w.Version() != 7 {
		t.Errorf("Version() = %d, esperado 7 (não pode incrementar)", w.Version())
	}
	if !w.CreatedAt().Equal(criacao) {
		t.Errorf("CreatedAt alterado: %v", w.CreatedAt())
	}
	if !w.UpdatedAt().Equal(atualizacao) {
		t.Errorf("UpdatedAt alterado: %v", w.UpdatedAt())
	}
	if !mesmoMoney(w.Balance(), saldo) || saldoMinor(w) != 97500 {
		t.Errorf("saldo = %s (%d)", w.Balance(), saldoMinor(w))
	}
	if w.Currency() != money.BRL {
		t.Errorf("moeda = %q", w.Currency())
	}
	if w.ID() != "w-1" || w.PlayerID() != "p-1" {
		t.Errorf("identidade alterada: %q/%q", w.ID(), w.PlayerID())
	}
}

// ---------------------------------------------------------------------------
// Debit
// ---------------------------------------------------------------------------

func TestDebit(t *testing.T) {
	casos := []struct {
		nome       string
		saldo      string
		debito     string
		queroSaldo string
		queroEr    bool
		code       string
		queroVerso int64
	}{
		{"debito normal", "1000.00", "25.00", "975.00", false, "", 2},
		{"debito que zera", "100.00", "100.00", "0.00", false, "", 2},
		{"debito de um centavo", "1.00", "0.01", "0.99", false, "", 2},
		{"debito do saldo inteiro com centavos", "25.99", "25.99", "0.00", false, "", 2},
		{"debito de zero nao muda nada", "100.00", "0.00", "100.00", false, "", 1},
		{"saldo zero com debito zero", "0.00", "0.00", "0.00", false, "", 1},
		{"excede o saldo", "100.00", "100.01", "", true, CodeInsufficientFunds, 1},
		{"excede muito", "100.00", "1000.00", "", true, CodeInsufficientFunds, 1},
		{"saldo zero com debito", "0.00", "0.01", "", true, CodeInsufficientFunds, 1},
		{"um centavo a mais", "1.00", "1.01", "", true, CodeInsufficientFunds, 1},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			w := novaCarteira(t, c.saldo)

			entry, err := w.Debit("txn-teste", brl(t, c.debito), t0.Add(time.Minute))
			if c.queroEr {
				if err == nil {
					t.Fatalf("Debit deveria falhar")
				}
				if CodeOf(err) != c.code {
					t.Errorf("Code = %q, esperado %q", CodeOf(err), c.code)
				}
				if !errors.Is(err, ErrInsufficientFunds) {
					t.Errorf("errors.Is(err, ErrInsufficientFunds) = false: %v", err)
				}
				// Estado não pode ter mudado.
				if w.Balance().String() != c.saldo {
					t.Errorf("saldo mudou mesmo com erro: %s", w.Balance())
				}
				if w.Version() != 1 {
					t.Errorf("versão mudou mesmo com erro: %d", w.Version())
				}
				if !entry.Entry().IsZero() {
					t.Error("entry deveria ser zero value em caso de erro")
				}
				return
			}

			if err != nil {
				t.Fatalf("Debit falhou: %v", err)
			}
			if got := w.Balance().String(); got != c.queroSaldo {
				t.Errorf("saldo = %s, esperado %s", got, c.queroSaldo)
			}
			if w.Version() != c.queroVerso {
				t.Errorf("versão = %d, esperado %d", w.Version(), c.queroVerso)
			}
			if w.Balance().IsNegative() {
				t.Errorf("saldo ficou negativo: %s", w.Balance())
			}
			if c.debito == "0.00" && !entry.Entry().IsZero() {
				t.Errorf("débito de zero não deveria gerar lançamento: %+v", entry)
			}
			if c.debito != "0.00" && entry.Entry().IsZero() {
				t.Error("débito com valor deveria gerar lançamento")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Credit
// ---------------------------------------------------------------------------

func TestCredit(t *testing.T) {
	casos := []struct {
		nome       string
		saldo      string
		credito    string
		queroSaldo string
		queroVerso int64
	}{
		{"credito normal", "1000.00", "25.00", "1025.00", 2},
		{"credito em saldo zero", "0.00", "1000.00", "1000.00", 2},
		{"credito de um centavo", "0.00", "0.01", "0.01", 2},
		{"credito de zero nao muda nada", "100.00", "0.00", "100.00", 1},
		{"credito grande", "90000000000000.00", "0.00", "90000000000000.00", 1},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			w := novaCarteira(t, c.saldo)

			entry, err := w.Credit("txn-teste", brl(t, c.credito), t0.Add(time.Minute))
			if err != nil {
				t.Fatalf("Credit falhou: %v", err)
			}
			if got := w.Balance().String(); got != c.queroSaldo {
				t.Errorf("saldo = %s, esperado %s", got, c.queroSaldo)
			}
			if w.Version() != c.queroVerso {
				t.Errorf("versão = %d, esperado %d", w.Version(), c.queroVerso)
			}

			if c.credito == "0.00" {
				// Valor zero não gera lançamento no ledger.
				if !entry.Entry().IsZero() {
					t.Errorf("crédito de zero gerou entry: %+v", entry)
				}
				return
			}
			if entry.Entry().Direction() != ledger.DirectionCredit {
				t.Errorf("direção = %q", entry.Entry().Direction())
			}
			if !mesmoMoney(entry.Entry().Amount(), brl(t, c.credito)) {
				t.Errorf("valor do entry = %s", entry.Entry().Amount())
			}
		})
	}
}

func TestCreditEstouraOLimiteSemAlterarEstado(t *testing.T) {
	// O teto do money é 90000000000000.00. Creditar além disso tem que falhar
	// com erro de domínio e sem tocar no saldo.
	w := novaCarteira(t, "90000000000000.00")
	antes := w.Balance().String()

	if _, err := w.Credit("txn-teste", brl(t, "0.01"), t0); err == nil {
		t.Fatal("crédito acima do limite deveria falhar")
	}
	if w.Balance().String() != antes {
		t.Errorf("saldo mudou: %s -> %s", antes, w.Balance())
	}
	if w.Version() != 1 {
		t.Errorf("versão mudou: %d", w.Version())
	}
}

// ---------------------------------------------------------------------------
// Moeda e valor inválido
// ---------------------------------------------------------------------------

func TestMovimentacaoEmMoedaDiferenteNaoEhSaldoInsuficiente(t *testing.T) {
	// A moeda é checada ANTES da aritmética: uma carteira de 100.00 BRL
	// debitada em 150.00 USD tem que dar mismatch de moeda, não saldo
	// insuficiente. A ordem importa para o failureCode que o cliente recebe.
	w := novaCarteira(t, "100.00")

	_, errDebit := w.Debit("txn-teste", usd(t, "150.00"), t0)
	if !errors.Is(errDebit, ErrWalletCurrencyMismatch) {
		t.Errorf("Debit em USD: err = %v, esperado ErrWalletCurrencyMismatch", errDebit)
	}
	if errors.Is(errDebit, ErrInsufficientFunds) {
		t.Error("não pode ser classificado como saldo insuficiente")
	}

	_, errCredit := w.Credit("txn-teste", usd(t, "1.00"), t0)
	if !errors.Is(errCredit, ErrWalletCurrencyMismatch) {
		t.Errorf("Credit em USD: err = %v", errCredit)
	}

	if !mesmoMoney(w.Balance(), brl(t, "100.00")) || w.Version() != 1 {
		t.Errorf("estado mudou: %s v%d", w.Balance(), w.Version())
	}
}

func TestMovimentacaoNegativaEhRejeitada(t *testing.T) {
	// O sinal é definido pela direção, não pelo valor: um valor negativo
	// inverteria a operação e seria uma forma de burlar o saldo mínimo.
	w := novaCarteira(t, "100.00")
	negativo := money.FromMinor(-2500, money.BRL)

	if _, err := w.Debit("txn-teste", negativo, t0); err == nil {
		t.Error("debito negativo deveria falhar")
	}
	if _, err := w.Credit("txn-teste", negativo, t0); err == nil {
		t.Error("credito negativo deveria falhar")
	}
	if w.Balance().String() != "100.00" || w.Version() != 1 {
		t.Errorf("estado mudou: %s v%d", w.Balance(), w.Version())
	}
}

// ---------------------------------------------------------------------------
// Versão e instantes
// ---------------------------------------------------------------------------

func TestVersaoSoAvancaQuandoOSaldoMuda(t *testing.T) {
	w := novaCarteira(t, "100.00")
	depois := t0.Add(time.Hour)

	// Operações de valor zero não mudam saldo, versão nem updatedAt.
	for i := 0; i < 3; i++ {
		if _, err := w.Credit("txn-teste", brl(t, "0.00"), depois); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Debit("txn-teste", brl(t, "0.00"), depois); err != nil {
			t.Fatal(err)
		}
	}
	if w.Version() != 1 {
		t.Errorf("versão mudou com operações de zero: %d", w.Version())
	}
	if !w.UpdatedAt().Equal(t0) {
		t.Errorf("updatedAt mudou com operações de zero: %v", w.UpdatedAt())
	}

	// Uma operação com valor muda.
	if _, err := w.Debit("txn-teste", brl(t, "1.00"), depois); err != nil {
		t.Fatal(err)
	}
	if w.Version() != 2 {
		t.Errorf("versão = %d, esperado 2", w.Version())
	}
	if !w.UpdatedAt().Equal(depois) {
		t.Errorf("updatedAt = %v, esperado %v", w.UpdatedAt(), depois)
	}
	if !w.CreatedAt().Equal(t0) {
		t.Errorf("createdAt não pode mudar: %v", w.CreatedAt())
	}
}

func TestVersaoNuncaRegride(t *testing.T) {
	w := novaCarteira(t, "1000.00")
	anterior := w.Version()

	for i := 0; i < 10; i++ {
		agora := t0.Add(time.Duration(i) * time.Minute)
		if _, err := w.Credit("txn-teste", brl(t, "1.00"), agora); err != nil {
			t.Fatal(err)
		}
		if w.Version() <= anterior {
			t.Fatalf("versão regrediu ou parou: %d -> %d", anterior, w.Version())
		}
		anterior = w.Version()
	}
}

// ---------------------------------------------------------------------------
// Propriedades
// ---------------------------------------------------------------------------

func TestCreditoSeguidoDeDebitoVoltaAoSaldoOriginal(t *testing.T) {
	w := novaCarteira(t, "100.00")

	valores := []string{"0.01", "1.00", "25.99", "1000.00", "999999.99"}
	for i, v := range valores {
		antes := w.Balance()
		versaoAntes := w.Version()
		agora := t0.Add(time.Duration(i+1) * time.Minute)

		if _, err := w.Credit("txn-teste", brl(t, v), agora); err != nil {
			t.Fatalf("Credit(%s): %v", v, err)
		}
		if _, err := w.Debit("txn-teste", brl(t, v), agora); err != nil {
			t.Fatalf("Debit(%s): %v", v, err)
		}

		if !mesmoMoney(w.Balance(), antes) {
			t.Errorf("após Credit(%s)+Debit(%s): saldo %s, esperado %s", v, v, w.Balance(), antes)
		}
		if w.Version() != versaoAntes+2 {
			t.Errorf("versão = %d, esperado %d", w.Version(), versaoAntes+2)
		}
	}
}

func TestSaldoNuncaFicaNegativoEmSequenciaDeOperacoes(t *testing.T) {
	w := novaCarteira(t, "1000.00")

	// Sequência determinística misturando créditos e débitos, vários deles
	// grandes demais: os que passam não podem deixar saldo negativo, e os que
	// falham não podem mexer no estado.
	operacoes := []struct {
		credito bool
		valor   string
	}{
		{true, "500.00"}, {false, "1200.00"}, {false, "300.00"}, {true, "0.01"},
		{false, "1200.00"}, {false, "1199.99"}, {true, "1.00"}, {false, "500.00"},
		{false, "0.02"}, {true, "1000000.00"}, {false, "1000000.00"}, {false, "0.01"},
	}

	for i, op := range operacoes {
		agora := t0.Add(time.Duration(i) * time.Second)
		if op.credito {
			if _, err := w.Credit("txn-teste", brl(t, op.valor), agora); err != nil {
				t.Fatalf("Credit(%s) falhou: %v", op.valor, err)
			}
		} else {
			_, err := w.Debit("txn-teste", brl(t, op.valor), agora)
			if err != nil && !errors.Is(err, ErrInsufficientFunds) {
				t.Fatalf("Debit(%s) falhou com erro inesperado: %v", op.valor, err)
			}
			if err == nil && w.Balance().IsNegative() {
				t.Fatalf("Debit(%s) aceito deixou saldo negativo: %s", op.valor, w.Balance())
			}
		}
		if w.Balance().IsNegative() {
			t.Fatalf("saldo negativo após %d operações: %s", i+1, w.Balance())
		}
	}
}

// ---------------------------------------------------------------------------
// Ledger
// ---------------------------------------------------------------------------

func TestDebitGeraLancamentoCoerente(t *testing.T) {
	w := novaCarteira(t, "1000.00")
	agora := t0.Add(time.Minute)

	entry, err := w.Debit("txn-teste", brl(t, "25.00"), agora)
	if err != nil {
		t.Fatal(err)
	}

	if entry.Entry().Direction() != ledger.DirectionDebit {
		t.Errorf("direção = %q", entry.Entry().Direction())
	}
	if !mesmoMoney(entry.Entry().Amount(), brl(t, "25.00")) {
		t.Errorf("valor = %s", entry.Entry().Amount())
	}
	if entry.Entry().BalanceBefore().String() != "1000.00" {
		t.Errorf("balanceBefore = %s, esperado 1000.00", entry.Entry().BalanceBefore())
	}
	if entry.Entry().BalanceAfter().String() != "975.00" {
		t.Errorf("balanceAfter = %s, esperado 975.00", entry.Entry().BalanceAfter())
	}
	if entry.Entry().WalletID() != w.ID() {
		t.Errorf("walletID = %q", entry.Entry().WalletID())
	}
	if !entry.Entry().CreatedAt().Equal(agora) {
		t.Errorf("createdAt = %v", entry.Entry().CreatedAt())
	}

	// A aritmética do ledger tem que fechar sozinha.
	dif, err := entry.Entry().BalanceBefore().Sub(entry.Entry().BalanceAfter())
	if err != nil {
		t.Fatal(err)
	}
	if !mesmoMoney(dif, entry.Entry().Amount()) {
		t.Errorf("before-after = %s, esperado %s", dif, entry.Entry().Amount())
	}
}

func TestCreditGeraLancamentoCoerente(t *testing.T) {
	w := novaCarteira(t, "100.00")

	entry, err := w.Credit("txn-teste", brl(t, "50.00"), t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	if entry.Entry().BalanceBefore().String() != "100.00" || entry.Entry().BalanceAfter().String() != "150.00" {
		t.Errorf("saldos = %s -> %s", entry.Entry().BalanceBefore(), entry.Entry().BalanceAfter())
	}
	dif, err := entry.Entry().BalanceAfter().Sub(entry.Entry().BalanceBefore())
	if err != nil {
		t.Fatal(err)
	}
	if !mesmoMoney(dif, entry.Entry().Amount()) {
		t.Errorf("after-before = %s, esperado %s", dif, entry.Entry().Amount())
	}
}

func TestLedgerAcumulaESaldoBate(t *testing.T) {
	// A soma dos lançamentos tem que explicar a diferença de saldo. É a
	// invariante que a reconciliação da Fase 11 vai conferir no banco.
	w := novaCarteira(t, "1000.00")
	agora := t0.Add(time.Minute)

	creditos := int64(0)
	debitos := int64(0)
	operacoes := []struct {
		credito bool
		valor   string
	}{
		{false, "25.00"}, {false, "100.00"}, {true, "50.00"},
		{false, "1.00"}, {true, "0.99"}, {false, "0.01"},
	}

	for i, op := range operacoes {
		agora = agora.Add(time.Duration(i) * time.Second)
		var (
			mov Movement
			err error
		)
		if op.credito {
			mov, err = w.Credit("txn-teste", brl(t, op.valor), agora)
			creditos += mov.Entry().Amount().Minor()
		} else {
			mov, err = w.Debit("txn-teste", brl(t, op.valor), agora)
			debitos += mov.Entry().Amount().Minor()
		}
		if err != nil {
			t.Fatalf("operação %d falhou: %v", i, err)
		}
	}

	esperado := int64(100000) + creditos - debitos
	if saldoMinor(w) != esperado {
		t.Errorf("saldo = %d, esperado %d (1000.00 + créditos - débitos)", saldoMinor(w), esperado)
	}
}

// ---------------------------------------------------------------------------
// NewEntry / RehydrateEntry
// ---------------------------------------------------------------------------
func TestErrorTextoECodigos(t *testing.T) {
	casos := []struct {
		err  error
		want string
	}{
		{ErrInvalidWallet, "INVALID_WALLET: carteira inválida"},
		{ErrInsufficientFunds, "INSUFFICIENT_FUNDS: saldo insuficiente"},
		{ErrWalletCurrencyMismatch, "WALLET_CURRENCY_MISMATCH: moeda incompatível com a da carteira"},
	}

	for _, c := range casos {
		if got := c.err.Error(); got != c.want {
			t.Errorf("Error() = %q, esperado %q", got, c.want)
		}
	}
}

func TestErrosNaoSeConfundem(t *testing.T) {
	if errors.Is(ErrInsufficientFunds, ErrWalletCurrencyMismatch) {
		t.Error("saldo insuficiente casou com moeda incompatível")
	}
	if errors.Is(ErrInsufficientFunds, ErrInvalidWallet) {
		t.Error("entry inválido casou com carteira inválida")
	}
	if errors.Is(ErrInsufficientFunds, ErrInvalidWallet) {
		t.Error("saldo insuficiente casou com carteira inválida")
	}
}

func TestErroAtravessaEnvelopamento(t *testing.T) {
	w := novaCarteira(t, "1.00")
	_, err := w.Debit("txn-teste", brl(t, "2.00"), t0)
	if err == nil {
		t.Fatal("esperado erro")
	}

	envelopado := errors.Join(errors.New("camada de aplicação"), err)
	if !errors.Is(envelopado, ErrInsufficientFunds) {
		t.Errorf("errors.Is não atravessou: %v", envelopado)
	}
	if CodeOf(envelopado) != CodeInsufficientFunds {
		t.Errorf("CodeOf = %q", CodeOf(envelopado))
	}
}

// CodeOf devolve o Code de um erro de domínio do pacote wallet.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "NAO_E_ERRO_DE_DOMINIO"
}

func TestFalhaNaoDeixaEstadoPelaMetade(t *testing.T) {
	// Cada tentativa rejeitada tem que deixar saldo, versão e instante intactos.
	// (bloco original mantido abaixo)
	w := novaCarteira(t, "100.00")
	saldo, versao, atualizado := w.Balance(), w.Version(), w.UpdatedAt()

	tentativas := []func() error{
		func() error { _, err := w.Debit("txn-teste", brl(t, "100.01"), t0.Add(time.Hour)); return err },
		func() error { _, err := w.Debit("txn-teste", usd(t, "1.00"), t0.Add(time.Hour)); return err },
		func() error { _, err := w.Credit("txn-teste", usd(t, "1.00"), t0.Add(time.Hour)); return err },
		func() error {
			_, err := w.Debit("txn-teste", money.FromMinor(-1, money.BRL), t0.Add(time.Hour))
			return err
		},
	}

	for i, tentativa := range tentativas {
		if err := tentativa(); err == nil {
			t.Fatalf("tentativa %d deveria falhar", i)
		}
		if !mesmoMoney(w.Balance(), saldo) {
			t.Errorf("saldo mudou na tentativa %d: %s", i, w.Balance())
		}
		if w.Version() != versao {
			t.Errorf("versão mudou na tentativa %d: %d", i, w.Version())
		}
		if !w.UpdatedAt().Equal(atualizado) {
			t.Errorf("updatedAt mudou na tentativa %d: %v", i, w.UpdatedAt())
		}
	}
}

// ---------------------------------------------------------------------------
// Dados corrompidos: as guardas defensivas do domínio
//
// Estes casos só são alcançáveis se um valor inválido entrar por Rehydrate,
// que não valida de propósito (a validação da leitura é do repositório, apoiada
// na CHECK constraint da tabela). Existem para provar que, mesmo assim, o
// domínio recusa em vez de gravar absurdo.
// ---------------------------------------------------------------------------

func TestNewWalletRecusaSaldoInicialNegativo(t *testing.T) {
	negativo := money.FromMinor(-100, money.BRL)

	if _, err := NewWallet("w-1", "p-1", negativo, t0); err == nil {
		t.Fatal("saldo inicial negativo deveria falhar")
	} else if CodeOf(err) != CodeInvalidWallet {
		t.Errorf("Code = %q", CodeOf(err))
	}
}

func TestCreditComSaldoCorrompidoNoLimite(t *testing.T) {
	// Saldo acima do limite do money só chega aqui por Rehydrate. O crédito
	// tem que devolver erro de domínio e não tocar no estado.
	corrompido := money.FromMinor(9_000_000_000_000_001, money.BRL)
	w := Rehydrate("w-1", "p-1", corrompido, 3, t0, t0)

	if _, err := w.Credit("txn-teste", brl(t, "1.00"), t0.Add(time.Minute)); err == nil {
		t.Fatal("crédito sobre saldo corrompido deveria falhar")
	}
	if saldoMinor(w) != 9_000_000_000_000_001 || w.Version() != 3 {
		t.Errorf("estado mudou: %d v%d", saldoMinor(w), w.Version())
	}
}

func TestDebitComSaldoCorrompidoAbaixoDoPiso(t *testing.T) {
	// Saldo abaixo do piso do money: a subtração estoura antes de a checagem
	// de saldo insuficiente rodar. O domínio recusa de qualquer forma.
	corrompido := money.FromMinor(-9_000_000_000_000_001, money.BRL)
	w := Rehydrate("w-1", "p-1", corrompido, 3, t0, t0)

	if _, err := w.Debit("txn-teste", brl(t, "1.00"), t0); err == nil {
		t.Fatal("débito sobre saldo corrompido deveria falhar")
	}
	if w.Version() != 3 {
		t.Errorf("versão mudou: %d", w.Version())
	}
}

func TestApplyComDirecaoInvalida(t *testing.T) {
	// Chamada direta com direção inválida: a guarda existe mesmo que Debit e
	// Credit já fixem a direção.
	w := novaCarteira(t, "100.00")

	if _, err := w.apply(ledger.Direction("TRANSFER"), "txn-teste", brl(t, "1.00"), t0); err == nil {
		t.Fatal("direção inválida deveria falhar")
	} else if !errors.Is(err, ledger.ErrInvalidEntry) {
		t.Errorf("err = %v, esperado ledger.ErrInvalidEntry", err)
	}
	if w.Balance().String() != "100.00" || w.Version() != 1 {
		t.Errorf("estado mudou: %s v%d", w.Balance(), w.Version())
	}
}
func TestIsNaoCasaComErroDeOutroTipo(t *testing.T) {
	if errors.Is(ErrInsufficientFunds, errors.New("erro qualquer")) {
		t.Error("sentinela casou com erro que não é *Error")
	}
	if !errors.Is(errors.Join(errors.New("contexto"), ErrInsufficientFunds), ErrInsufficientFunds) {
		t.Error("erro de domínio não atravessou o Join")
	}
}
