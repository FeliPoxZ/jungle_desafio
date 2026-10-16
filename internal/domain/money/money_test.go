package money

import (
	"errors"
	"fmt"
	"testing"
)

// CodeOf devolve o Code de um erro de domínio do pacote.
//
// Na Fase 7 este helper se muda para o pacote money (errors.go), porque é ele
// que o handler HTTP usa para traduzir erro de domínio em failureCode.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "NAO_E_ERRO_DE_DOMINIO"
}

// fmtSprint formata com %v, que é o caminho usado pelos logs estruturados.
// Existe como função para o teste deixar claro que está exercitando o
// fmt.Stringer, e não chamando String() direto.
func fmtSprint(v any) string {
	return fmt.Sprint(v)
}

// ---------------------------------------------------------------------------
// Propriedades gerais do erro de domínio
// ---------------------------------------------------------------------------

func TestErrorTexto(t *testing.T) {
	casos := []struct {
		err  error
		want string
	}{
		// Sem prefixo de pacote: o erro de domínio é UM tipo compartilhado por
		// money, wallet, ledger e wagering. Prefixar com o nome do pacote faria
		// a mesma mensagem mudar conforme quem a produziu, e o log passaria a
		// depender do caminho em vez do Code.
		{ErrInvalidCurrency, "INVALID_CURRENCY: código de moeda inválido"},
		{ErrInvalidAmount, "INVALID_AMOUNT: valor monetário inválido"},
		{ErrCurrencyMismatch, "CURRENCY_MISMATCH: moedas incompatíveis na operação"},
	}

	for _, c := range casos {
		if got := c.err.Error(); got != c.want {
			t.Errorf("Error() = %q, esperado %q", got, c.want)
		}
	}
}

func TestErroClassificavelPorErrorsIs(t *testing.T) {
	// Erros vindos de pontos diferentes do parsing devem ser todos
	// classificáveis pelo mesmo sentinela.
	origens := map[string]string{
		"vazio":         "",
		"sinal":         "-25.00",
		"escala":        "25.000",
		"texto":         "abc",
		"cientifica":    "1e2",
		"acima do teto": "9999999999999999.99",
	}

	for nome, amount := range origens {
		t.Run(nome, func(t *testing.T) {
			_, err := Parse(amount, "BRL")
			if !errors.Is(err, ErrInvalidAmount) {
				t.Fatalf("errors.Is falhou para %q: %v", amount, err)
			}
			if errors.Is(err, ErrInvalidCurrency) {
				t.Errorf("erro de valor classificado também como moeda")
			}
			if errors.Is(err, ErrCurrencyMismatch) {
				t.Errorf("erro de valor classificado também como moeda incompatível")
			}
		})
	}
}

func TestErroAtravessaEnvelopamento(t *testing.T) {
	// errors.Is precisa funcionar mesmo depois de vários fmt.Errorf("%w").
	_, err := Parse("abc", "BRL")
	duplo := fmt.Errorf("camada externa: %w", fmt.Errorf("camada interna: %w", err))

	if !errors.Is(duplo, ErrInvalidAmount) {
		t.Errorf("errors.Is não atravessou o envelopamento: %v", duplo)
	}
	if CodeOf(duplo) != CodeInvalidAmount {
		t.Errorf("CodeOf = %q", CodeOf(duplo))
	}
}

func TestSentinelaNaoConfundeCodigos(t *testing.T) {
	// O Is compara por Code: um erro nunca pode casar com o sentinela de
	// outro código.
	if errors.Is(ErrInvalidAmount, ErrInvalidCurrency) {
		t.Error("ErrInvalidAmount casou com ErrInvalidCurrency")
	}
	if errors.Is(ErrInvalidCurrency, ErrInvalidAmount) {
		t.Error("ErrInvalidCurrency casou com ErrInvalidAmount")
	}
	if errors.Is(ErrCurrencyMismatch, ErrInvalidAmount) {
		t.Error("ErrCurrencyMismatch casou com ErrInvalidAmount")
	}
}

func TestCodeOfErroNaoDominio(t *testing.T) {
	if got := CodeOf(errors.New("erro qualquer")); got != "NAO_E_ERRO_DE_DOMINIO" {
		t.Errorf("CodeOf(erro comum) = %q", got)
	}
	if got := CodeOf(nil); got != "NAO_E_ERRO_DE_DOMINIO" {
		t.Errorf("CodeOf(nil) = %q", got)
	}
}

func TestIsNaoCasaComErroDeOutroTipo(t *testing.T) {
	// Error.Is é o abre-caixas, mas só reconhece *Error do pacote. Um erro
	// comum não pode "casar" com um sentinela de domínio.
	sentinela := ErrInvalidAmount
	if errors.Is(sentinela, errors.New("texto qualquer")) {
		t.Error("sentinela casou com um erro que não é *Error")
	}
	if errors.Is(sentinela, fmt.Errorf("envelopado: %w", errors.New("outro"))) {
		t.Error("sentinela casou através de um erro que não é *Error")
	}

	// E o caminho inverso, que é o que o Is precisa responder em produção:
	// o erro do domínio envelopado por um erro comum.
	envelopado := fmt.Errorf("camada de aplicação: %w", ErrInvalidAmount)
	if !errors.Is(envelopado, ErrInvalidAmount) {
		t.Error("erro de domínio não atravessou um erro comum")
	}
}

func TestIsComparaPorCodigoMesmoComMensagemDiferente(t *testing.T) {
	// O Is compara Code, não ponteiro nem mensagem: dois erros com o mesmo
	// Code são o "mesmo" erro para efeito de classificação.
	outro := &Error{Code: CodeInvalidAmount, Message: "mensagem completamente diferente"}
	if !errors.Is(outro, ErrInvalidAmount) {
		t.Error("erros com o mesmo Code deveriam casar")
	}
	if errors.Is(outro, ErrInvalidCurrency) {
		t.Error("erros com Code diferente não deveriam casar")
	}
}

func TestParseCadaCodigoTemSentinela(t *testing.T) {
	// Trava a ligação Code <-> sentinela, que é o contrato do failureCode.
	_, err := Parse("1.00", "brl")
	if CodeOf(err) != ErrInvalidCurrency.Code {
		t.Errorf("CodeOf = %q, sentinela = %q", CodeOf(err), ErrInvalidCurrency.Code)
	}
	_, err = Parse("-1.00", "BRL")
	if CodeOf(err) != ErrInvalidAmount.Code {
		t.Errorf("CodeOf = %q, sentinela = %q", CodeOf(err), ErrInvalidAmount.Code)
	}
	_, err = mustFromMinor(100, "BRL").Add(mustFromMinor(100, "USD"))
	if CodeOf(err) != ErrCurrencyMismatch.Code {
		t.Errorf("CodeOf = %q, sentinela = %q", CodeOf(err), ErrCurrencyMismatch.Code)
	}
}

// ---------------------------------------------------------------------------
// Add
// ---------------------------------------------------------------------------

func TestAdd(t *testing.T) {
	casos := []struct {
		nome  string
		a     int64
		b     int64
		want  int64
		quero bool // true = deve falhar
	}{
		{"zero mais zero", 0, 0, 0, false},
		{"normal", 2500, 1000, 3500, false},
		{"com zero", 2500, 0, 2500, false},
		{"negativo mais positivo", -1000, 500, -500, false},
		{"positivo mais negativo", 1000, -500, 500, false},
		{"dois negativos dentro da faixa", -1000, -2000, -3000, false},
		{"resultado zero", 500, -500, 0, false},
		{"positivos que fecham no teto", maxMinor / 2, maxMinor - maxMinor/2, maxMinor, false},
		{"teto mais zero", maxMinor, 0, maxMinor, false},
		{"piso mais zero", minMinor, 0, minMinor, false},

		{"teto mais teto estoura", maxMinor, maxMinor, 0, true},
		{"piso mais piso estoura", minMinor, minMinor, 0, true},
		{"teto mais um estoura", maxMinor, 1, 0, true},
		{"piso mais menos um estoura", minMinor, -1, 0, true},
		{"dois positivos grandes estouram", maxMinor / 2 * 2, 2, 0, true},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			a := mustFromMinor(c.a, "BRL")
			b := mustFromMinor(c.b, "BRL")

			got, err := a.Add(b)
			if c.quero {
				if err == nil {
					t.Fatalf("Add(%d, %d) deveria falhar, devolveu %d", c.a, c.b, got.Minor())
				}
				if !errors.Is(err, ErrInvalidAmount) {
					t.Errorf("erro não é ErrInvalidAmount: %v", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("Add(%d, %d) devolveu erro: %v", c.a, c.b, err)
			}
			if got.Minor() != c.want {
				t.Errorf("Add(%d, %d) = %d, esperado %d", c.a, c.b, got.Minor(), c.want)
			}
			if got.Currency() != "BRL" {
				t.Errorf("moeda do resultado = %q", got.Currency())
			}
		})
	}
}

func TestAddPropriedades(t *testing.T) {
	valores := []int64{minMinor, minMinor + 1, -1000, -1, 0, 1, 1000, maxMinor - 1, maxMinor}

	// Comutatividade e a identidade (a+b)-b == a.
	for _, a := range valores {
		for _, b := range valores {
			ma := mustFromMinor(a, "BRL")
			mb := mustFromMinor(b, "BRL")

			ab, errAB := ma.Add(mb)
			ba, errBA := mb.Add(ma)
			if (errAB == nil) != (errBA == nil) {
				t.Fatalf("Add(%d,%d) e Add(%d,%d) discordam: %v vs %v", a, b, b, a, errAB, errBA)
			}
			if errAB == nil && ab.Minor() != ba.Minor() {
				t.Errorf("Add não é comutativa: %d vs %d", ab.Minor(), ba.Minor())
			}
			if errAB != nil {
				continue
			}

			volta, err := ab.Sub(mb)
			if err != nil {
				t.Errorf("(%d+%d)-%d falhou: %v", a, b, b, err)
				continue
			}
			if volta.Minor() != a {
				t.Errorf("(%d+%d)-%d = %d", a, b, b, volta.Minor())
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Sub
// ---------------------------------------------------------------------------

func TestSub(t *testing.T) {
	casos := []struct {
		nome  string
		a     int64
		b     int64
		want  int64
		quero bool
	}{
		{"normal", 2500, 1000, 1500, false},
		{"resultado zero", 2500, 2500, 0, false},
		{"subtrair zero", 2500, 0, 2500, false},
		{"resultado negativo", 1000, 2500, -1500, false},
		{"subtrair negativo soma", 1000, -500, 1500, false},
		{"negativo menos positivo", -1000, 500, -1500, false},
		{"negativo menos negativo", -1000, -500, -500, false},
		{"teto menos teto", maxMinor, maxMinor, 0, false},
		{"zero menos teto", 0, maxMinor, -maxMinor, false},
		{"zero menos piso cabe no teto", 0, minMinor, maxMinor, false},

		{"teto menos piso estoura", maxMinor, minMinor, 0, true},
		{"piso menos teto estoura", minMinor, maxMinor, 0, true},
		{"piso menos um estoura", minMinor, 1, 0, true},
		{"teto menos menos um estoura", maxMinor, -1, 0, true},
		{"zero menos abaixo do piso estoura", 0, minMinor - 1, 0, true},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			a := mustFromMinor(c.a, "BRL")
			b := mustFromMinor(c.b, "BRL")

			got, err := a.Sub(b)
			if c.quero {
				if err == nil {
					t.Fatalf("Sub(%d, %d) deveria falhar, devolveu %d", c.a, c.b, got.Minor())
				}
				return
			}

			if err != nil {
				t.Fatalf("Sub(%d, %d) devolveu erro: %v", c.a, c.b, err)
			}
			if got.Minor() != c.want {
				t.Errorf("Sub(%d, %d) = %d, esperado %d", c.a, c.b, got.Minor(), c.want)
			}
		})
	}
}

func TestSubEBAddSaoInversas(t *testing.T) {
	valores := []int64{minMinor, minMinor + 1, -999999, -1, 0, 1, 999999, maxMinor - 1, maxMinor}
	for _, a := range valores {
		for _, b := range valores {
			ma, mb := mustFromMinor(a, "BRL"), mustFromMinor(b, "BRL")

			d, err := ma.Sub(mb)
			if err != nil {
				// se a-b não cabe, não há o que voltar
				continue
			}
			volta, err := d.Add(mb)
			if err != nil {
				t.Errorf("(%d-%d)+%d falhou: %v", a, b, b, err)
				continue
			}
			if volta.Minor() != a {
				t.Errorf("(%d-%d)+%d = %d", a, b, b, volta.Minor())
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Neg
// ---------------------------------------------------------------------------

func TestNeg(t *testing.T) {
	casos := []struct {
		nome  string
		in    int64
		want  int64
		quero bool
	}{
		{"zero", 0, 0, false},
		{"positivo", 2500, -2500, false},
		{"negativo", -2500, 2500, false},
		{"um centavo", 1, -1, false},
		{"menos um centavo", -1, 1, false},
		{"teto", maxMinor, -maxMinor, false},
		{"piso vira teto", minMinor, maxMinor, false},
		{"teto menos um", maxMinor - 1, -(maxMinor - 1), false},
		{"piso mais um", minMinor + 1, maxMinor - 1, false},

		{"abaixo do piso estoura", minMinor - 1, 0, true},
		{"piso do int64 estoura", -9_223_372_036_854_775_808, 0, true},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			m := mustFromMinor(c.in, "BRL")
			got, err := m.Neg()

			if c.quero {
				if err == nil {
					t.Fatalf("Neg(%d) deveria falhar, devolveu %d", c.in, got.Minor())
				}
				if !errors.Is(err, ErrInvalidAmount) {
					t.Errorf("erro não é ErrInvalidAmount: %v", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("Neg(%d) devolveu erro: %v", c.in, err)
			}
			if got.Minor() != c.want {
				t.Errorf("Neg(%d) = %d, esperado %d", c.in, got.Minor(), c.want)
			}
			if got.Currency() != m.Currency() {
				t.Errorf("moeda mudou: %q -> %q", m.Currency(), got.Currency())
			}
		})
	}
}

func TestNegEhInvolutiva(t *testing.T) {
	// Neg é sua própria inversa.
	valores := []int64{minMinor, minMinor + 1, -1000, -1, 0, 1, 1000, maxMinor - 1, maxMinor}
	for _, v := range valores {
		m := mustFromMinor(v, "BRL")
		n, err := m.Neg()
		if err != nil {
			t.Fatalf("Neg(%d) falhou: %v", v, err)
		}
		volta, err := n.Neg()
		if err != nil {
			t.Fatalf("Neg(Neg(%d)) falhou: %v", v, err)
		}
		if volta.Minor() != v {
			t.Errorf("Neg(Neg(%d)) = %d", v, volta.Minor())
		}
	}
}

func TestSubEquivaleASomarONegativo(t *testing.T) {
	// a - b deve ser igual a a + (-b).
	valores := []int64{minMinor / 2, -1000, -1, 0, 1, 1000, maxMinor / 2}
	for _, a := range valores {
		for _, b := range valores {
			sub, errSub := mustFromMinor(a, "BRL").Sub(mustFromMinor(b, "BRL"))
			bNeg, errNeg := mustFromMinor(b, "BRL").Neg()
			if errNeg != nil {
				continue
			}
			soma, errSoma := mustFromMinor(a, "BRL").Add(bNeg)
			if (errSub == nil) != (errSoma == nil) {
				t.Fatalf("a-b e a+(-b) discordam em %d,%d: %v vs %v", a, b, errSub, errSoma)
			}
			if errSub == nil && sub.Minor() != soma.Minor() {
				t.Errorf("%d-%d = %d, mas %d+(-%d) = %d", a, b, sub.Minor(), a, b, soma.Minor())
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Moeda incompatível
// ---------------------------------------------------------------------------

func TestOperacoesExigemMesmaMoeda(t *testing.T) {
	brl := mustFromMinor(1000, "BRL")
	usd := mustFromMinor(1000, "USD")

	casos := map[string]func() error{
		"Add":           func() error { _, err := brl.Add(usd); return err },
		"Sub":           func() error { _, err := brl.Sub(usd); return err },
		"Add invertido": func() error { _, err := usd.Add(brl); return err },
		"Sub invertido": func() error { _, err := usd.Sub(brl); return err },
		"Cmp":           func() error { _, err := brl.Cmp(usd); return err },
		"Equal":         func() error { _, err := brl.Equal(usd); return err },
	}

	for nome, fn := range casos {
		t.Run(nome, func(t *testing.T) {
			err := fn()
			if err == nil {
				t.Fatalf("%s deveria falhar com moedas diferentes", nome)
			}
			if !errors.Is(err, ErrCurrencyMismatch) {
				t.Errorf("errors.Is(err, ErrCurrencyMismatch) = false; err = %v", err)
			}
			if CodeOf(err) != CodeCurrencyMismatch {
				t.Errorf("Code = %q", CodeOf(err))
			}
		})
	}
}

func TestMoedaIgualNaoEhMismatch(t *testing.T) {
	// A comparação é por igualdade de Currency: duas moedas iguais (ainda que
	// inválidas) não são "incompatíveis".
	a := mustFromMinor(100, Currency("xx"))
	b := mustFromMinor(200, Currency("xx"))
	if _, err := a.Cmp(b); err != nil {
		t.Errorf("mesma moeda não deveria dar mismatch: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Comparação
// ---------------------------------------------------------------------------

func TestCmp(t *testing.T) {
	casos := []struct {
		nome string
		a    int64
		b    int64
		want int
	}{
		{"igual", 2500, 2500, CmpEqual},
		{"menor", 1000, 2500, CmpLess},
		{"maior", 2500, 1000, CmpGreater},
		{"zero igual zero", 0, 0, CmpEqual},
		{"zero menor que positivo", 0, 1, CmpLess},
		{"zero maior que negativo", 0, -1, CmpGreater},
		{"negativo menor", -2500, -1000, CmpLess},
		{"negativo maior", -1000, -2500, CmpGreater},
		{"teto contra piso", maxMinor, minMinor, CmpGreater},
		{"piso contra teto", minMinor, maxMinor, CmpLess},
		{"teto contra teto", maxMinor, maxMinor, CmpEqual},
		{"piso contra piso", minMinor, minMinor, CmpEqual},
		{"um centavo de diferenca", 2500, 2501, CmpLess},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got, err := mustFromMinor(c.a, "BRL").Cmp(mustFromMinor(c.b, "BRL"))
			if err != nil {
				t.Fatalf("Cmp(%d, %d) devolveu erro: %v", c.a, c.b, err)
			}
			if got != c.want {
				t.Errorf("Cmp(%d, %d) = %d, esperado %d", c.a, c.b, got, c.want)
			}
		})
	}
}

func TestCmpAntiSimetrico(t *testing.T) {
	valores := []int64{minMinor, minMinor + 1, -1000, -1, 0, 1, 1000, maxMinor - 1, maxMinor}
	for _, a := range valores {
		for _, b := range valores {
			ab, errAB := mustFromMinor(a, "BRL").Cmp(mustFromMinor(b, "BRL"))
			ba, errBA := mustFromMinor(b, "BRL").Cmp(mustFromMinor(a, "BRL"))
			if errAB != nil || errBA != nil {
				t.Fatalf("Cmp dentro da mesma moeda não deveria falhar: %v, %v", errAB, errBA)
			}
			if ab != -ba {
				t.Errorf("Cmp(%d,%d) = %d e Cmp(%d,%d) = %d (não são opostos)", a, b, ab, b, a, ba)
			}
			if (ab == CmpEqual) != (a == b) {
				t.Errorf("Cmp(%d,%d) = %d com a != b", a, b, ab)
			}
		}
	}
}

func TestCmpTransitivo(t *testing.T) {
	valores := []int64{minMinor, minMinor + 1, -1000, 0, 1, 1000, maxMinor - 1, maxMinor}
	for _, a := range valores {
		for _, b := range valores {
			for _, c := range valores {
				ab, _ := mustFromMinor(a, "BRL").Cmp(mustFromMinor(b, "BRL"))
				bc, _ := mustFromMinor(b, "BRL").Cmp(mustFromMinor(c, "BRL"))
				ac, _ := mustFromMinor(a, "BRL").Cmp(mustFromMinor(c, "BRL"))

				if ab == CmpLess && bc == CmpLess && ac != CmpLess {
					t.Errorf("transitividade quebrada em (%d, %d, %d): %d, %d, %d", a, b, c, ab, bc, ac)
				}
				if ab == CmpEqual && bc == CmpEqual && ac != CmpEqual {
					t.Errorf("igualdade quebrada em (%d, %d, %d)", a, b, c)
				}
			}
		}
	}
}

func TestCmpResultadosPossiveis(t *testing.T) {
	// Trava o contrato: Cmp só devolve -1, 0 ou 1.
	valores := []int64{minMinor, -1, 0, 1, maxMinor}
	for _, a := range valores {
		for _, b := range valores {
			got, _ := mustFromMinor(a, "BRL").Cmp(mustFromMinor(b, "BRL"))
			switch got {
			case CmpLess, CmpEqual, CmpGreater:
			default:
				t.Fatalf("Cmp(%d, %d) = %d, fora do contrato", a, b, got)
			}
		}
	}
	if CmpLess != -1 || CmpEqual != 0 || CmpGreater != 1 {
		t.Errorf("constantes fora do padrão -1/0/+1")
	}
}

func TestEqual(t *testing.T) {
	casos := []struct {
		nome string
		a    int64
		b    int64
		want bool
	}{
		{"iguais", 2500, 2500, true},
		{"zeros", 0, 0, true},
		{"negativos iguais", -2500, -2500, true},
		{"diferentes", 2500, 2501, false},
		{"sinal diferente", 2500, -2500, false},
		{"teto contra piso", maxMinor, minMinor, false},
		{"teto contra teto", maxMinor, maxMinor, true},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got, err := mustFromMinor(c.a, "BRL").Equal(mustFromMinor(c.b, "BRL"))
			if err != nil {
				t.Fatalf("Equal devolveu erro: %v", err)
			}
			if got != c.want {
				t.Errorf("Equal(%d, %d) = %v, esperado %v", c.a, c.b, got, c.want)
			}
		})
	}
}

func TestEqualConcordaComCmp(t *testing.T) {
	valores := []int64{minMinor, -1000, -1, 0, 1, 1000, maxMinor}
	for _, a := range valores {
		for _, b := range valores {
			eq, errEq := mustFromMinor(a, "BRL").Equal(mustFromMinor(b, "BRL"))
			cmp, errCmp := mustFromMinor(a, "BRL").Cmp(mustFromMinor(b, "BRL"))
			if errEq != nil || errCmp != nil {
				t.Fatal("erros inesperados em mesma moeda")
			}
			if eq != (cmp == CmpEqual) {
				t.Errorf("Equal(%d,%d) = %v mas Cmp = %d", a, b, eq, cmp)
			}
		}
	}
}

func TestEqualComZeroValue(t *testing.T) {
	// Money{} não tem moeda nem valor. Dois zero values são comparáveis.
	var zero Money
	outro := mustFromMinor(0, Currency(""))
	eq, err := zero.Equal(outro)
	if err != nil {
		t.Fatalf("Equal de zero values devolveu erro: %v", err)
	}
	if !eq {
		t.Errorf("dois Money{} de mesmo minor deveriam ser iguais")
	}

	eq, err = zero.Equal(mustFromMinor(1, Currency("")))
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if eq {
		t.Errorf("minor diferente não deveria ser igual")
	}
}

// ---------------------------------------------------------------------------
// Sign / IsZero / IsNegative / IsPositive
// ---------------------------------------------------------------------------

func TestPredicadosDeSinal(t *testing.T) {
	casos := []struct {
		nome         string
		minor        int64
		wantSign     int
		wantZero     bool
		wantNegative bool
		wantPositive bool
	}{
		{"zero", 0, CmpEqual, true, false, false},
		{"um centavo", 1, CmpGreater, false, false, true},
		{"positivo", 2500, CmpGreater, false, false, true},
		{"menos um centavo", -1, CmpLess, false, true, false},
		{"negativo", -2500, CmpLess, false, true, false},
		{"teto", maxMinor, CmpGreater, false, false, true},
		{"piso", minMinor, CmpLess, false, true, false},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			m := mustFromMinor(c.minor, "BRL")
			if got := m.Sign(); got != c.wantSign {
				t.Errorf("Sign() = %d, esperado %d", got, c.wantSign)
			}
			if got := m.IsZero(); got != c.wantZero {
				t.Errorf("IsZero() = %v, esperado %v", got, c.wantZero)
			}
			if got := m.IsNegative(); got != c.wantNegative {
				t.Errorf("IsNegative() = %v, esperado %v", got, c.wantNegative)
			}
			if got := m.IsPositive(); got != c.wantPositive {
				t.Errorf("IsPositive() = %v, esperado %v", got, c.wantPositive)
			}

			// Exatamente um dos três precisa ser verdadeiro.
			soma := 0
			for _, b := range []bool{m.IsZero(), m.IsNegative(), m.IsPositive()} {
				if b {
					soma++
				}
			}
			if soma != 1 {
				t.Errorf("estado de sinal ambíguo: zero=%v negativo=%v positivo=%v",
					m.IsZero(), m.IsNegative(), m.IsPositive())
			}
		})
	}
}

func TestSignConcordaComCmpZero(t *testing.T) {
	zero := mustFromMinor(0, "BRL")
	valores := []int64{minMinor, -1, 0, 1, maxMinor}
	for _, v := range valores {
		m := mustFromMinor(v, "BRL")
		cmp, err := m.Cmp(zero)
		if err != nil {
			t.Fatal(err)
		}
		if m.Sign() != cmp {
			t.Errorf("Sign(%d) = %d, mas Cmp com zero = %d", v, m.Sign(), cmp)
		}
	}
}

func TestPredicadosComZeroValue(t *testing.T) {
	// Money{} não tem moeda. As perguntas sobre o número continuam válidas.
	var zero Money
	if !zero.IsZero() {
		t.Error("Money{}.IsZero() = false")
	}
	if zero.IsNegative() || zero.IsPositive() {
		t.Error("Money{} não é negativo nem positivo")
	}
	if zero.Sign() != CmpEqual {
		t.Errorf("Money{}.Sign() = %d", zero.Sign())
	}
	if zero.Minor() != 0 || zero.Currency() != "" {
		t.Errorf("Money{} = %d/%q", zero.Minor(), zero.Currency())
	}
}

// ---------------------------------------------------------------------------
// Laboratório de erros
//
// Este teste não é uma verificação: ele existe para ver o erro viajando pela
// cadeia e o errors.Is respondendo. Rode com -v para ler a saída.
// ---------------------------------------------------------------------------

func TestLaboratorioErros(t *testing.T) {
	casos := []struct {
		amount   string
		currency string
	}{
		{"25.00", "BRL"},  // válido
		{"25.00", "BRL1"}, // moeda inválida
		{"-25.00", "BRL"}, // sinal
		{"25.000", "BRL"}, // 3 casas
		{"1e2", "BRL"},    // científica
		{"abc", "BRL"},    // texto
	}

	for _, c := range casos {
		m, err := Parse(c.amount, c.currency)
		if err == nil {
			t.Logf("OK    %-8q %-6q -> minor=%d", c.amount, c.currency, m.Minor())
			continue
		}

		// O ponto: a decisão vem do Code, não da mensagem.
		t.Logf("ERRO  %-8q %-6q -> Code=%q | é moeda? %v | é valor? %v",
			c.amount, c.currency,
			CodeOf(err),
			errors.Is(err, ErrInvalidCurrency),
			errors.Is(err, ErrInvalidAmount),
		)
	}
}
