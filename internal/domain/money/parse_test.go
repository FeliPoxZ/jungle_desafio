package money

import (
	"errors"
	"fmt"
	"testing"
)

// Esta é a suíte do pacote money. Ela é table-driven de propósito: cada caso
// é uma linha, e adicionar um caso novo não exige escrever código novo.
//
// Convenções usadas aqui:
//   - valores em unidades mínimas são escritos por extenso (2500 = R$ 25,00);
//   - Parse devolve ErrInvalidAmount e ParseCurrency devolve ErrInvalidCurrency,
//     então os casos de moeda são separados dos casos de valor;
//   - a mensagem do erro nunca é comparada texto a texto, só o Code.

// mustParse é o atalho para os casos em que o valor é sabidamente válido.
// Se falhar, o defeito está no caso de teste, não no código sob teste.
func mustParse(t *testing.T, amount, currency string) Money {
	t.Helper()
	m, err := Parse(amount, currency)
	if err != nil {
		t.Fatalf("Parse(%q, %q) falhou: %v", amount, currency, err)
	}
	return m
}

// mustFromMinor constrói um Money de teste. Usa FromMinor porque alguns casos
// precisam de valores fora da faixa aceita por New.
func mustFromMinor(minor int64, currency Currency) Money {
	return FromMinor(minor, currency)
}

// ---------------------------------------------------------------------------
// ParseCurrency / Currency.Validate
// ---------------------------------------------------------------------------

func TestParseCurrency(t *testing.T) {
	validos := []struct {
		nome  string
		valor string
	}{
		{"real", "BRL"},
		{"dolar", "USD"},
		{"euro", "EUR"},
		{"tres letras quaisquer", "XYZ"},
		{"limite A", "AAA"},
		{"limite Z", "ZZZ"},
	}

	for _, c := range validos {
		t.Run("valido/"+c.nome, func(t *testing.T) {
			got, err := ParseCurrency(c.valor)
			if err != nil {
				t.Fatalf("ParseCurrency(%q) devolveu erro: %v", c.valor, err)
			}
			if string(got) != c.valor {
				t.Errorf("ParseCurrency(%q) = %q", c.valor, got)
			}
			if got.String() != c.valor {
				t.Errorf("String() = %q, esperado %q", got.String(), c.valor)
			}
			if err := got.Validate(); err != nil {
				t.Errorf("Validate() de valor válido devolveu %v", err)
			}
		})
	}

	invalidos := []struct {
		nome  string
		valor string
	}{
		{"vazio", ""},
		{"um espaco", " "},
		{"duas letras", "BR"},
		{"quatro letras", "BRLL"},
		{"minusculas", "brl"},
		{"misto", "Brl"},
		{"codigo numerico ISO", "986"},
		{"digito no meio", "B1L"},
		{"com espaco no meio", "B L"},
		{"com espaco nas bordas", " BRL"},
		{"com espaco no fim", "BRL "},
		{"simbolo de moeda", "R$"},
		{"acentuado", "ÇRL"},
		{"emoji", "🇧🇷"},
		{"duas moedas", "BRLUSD"},
		{"com ponto", "B.R"},
		{"com hifen", "BR-"},
	}

	for _, c := range invalidos {
		t.Run("invalido/"+c.nome, func(t *testing.T) {
			got, err := ParseCurrency(c.valor)
			if err == nil {
				t.Fatalf("ParseCurrency(%q) deveria falhar, devolveu %q", c.valor, got)
			}
			if !errors.Is(err, ErrInvalidCurrency) {
				t.Errorf("errors.Is(err, ErrInvalidCurrency) = false; err = %v", err)
			}
			if CodeOf(err) != CodeInvalidCurrency {
				t.Errorf("Code = %q, esperado %q", CodeOf(err), CodeInvalidCurrency)
			}
			if got != "" {
				t.Errorf("valor em caso de erro = %q, esperado vazio", got)
			}
		})
	}
}

func TestCurrencyValidateDireto(t *testing.T) {
	// Validate é usado na reidratação, com o tipo já construído.
	if err := BRL.Validate(); err != nil {
		t.Errorf("BRL.Validate() = %v", err)
	}
	// Um Currency não inicializado precisa ser rejeitado (README: valores
	// de domínio não inicializados devem ser rejeitados).
	var zero Currency
	if err := zero.Validate(); !errors.Is(err, ErrInvalidCurrency) {
		t.Errorf("Currency{}.Validate() = %v, esperado ErrInvalidCurrency", err)
	}
	if err := Currency("xx").Validate(); !errors.Is(err, ErrInvalidCurrency) {
		t.Errorf("Validate de minúsculas = %v", err)
	}
}

// ---------------------------------------------------------------------------
// Parse do amount
// ---------------------------------------------------------------------------

func TestParseAmount(t *testing.T) {
	casos := []struct {
		nome   string
		amount string
		want   int64
	}{
		{"zero", "0.00", 0},
		{"um centavo", "0.01", 1},
		{"dez centavos", "0.10", 10},
		{"meio real", "0.50", 50},
		{"um real", "1.00", 100},
		{"vinte e cinco reais", "25.00", 2500},
		{"vinte e cinco e noventa e nove", "25.99", 2599},
		{"mil reais", "1000.00", 100000},
		{"mil e um centavo", "1000.01", 100001},
		{"dois zeros a esquerda", "007.50", 750},
		{"muitos zeros a esquerda", "00000025.00", 2500},
		{"treze digitos", "1000000000000.00", 100000000000000},
		{"quatorze digitos", "10000000000000.00", 1000000000000000},
		{"quinze digitos no teto", "90000000000000.00", 9000000000000000},
		{"teto exato com centavos", "89999999999999.99", 8999999999999999},
		{"limite pedido pelo enunciado", "9999999999999999.99", 0}, // inválido, tratado abaixo
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			if c.want == 0 && c.amount == "9999999999999999.99" {
				if _, err := Parse(c.amount, "BRL"); !errors.Is(err, ErrInvalidAmount) {
					t.Fatalf("Parse(%q) deveria estourar o limite; err = %v", c.amount, err)
				}
				return
			}

			m, err := Parse(c.amount, "BRL")
			if err != nil {
				t.Fatalf("Parse(%q) devolveu erro: %v", c.amount, err)
			}
			if m.Minor() != c.want {
				t.Errorf("Parse(%q).Minor() = %d, esperado %d", c.amount, m.Minor(), c.want)
			}
			if m.Currency() != BRL {
				t.Errorf("Currency() = %q, esperado BRL", m.Currency())
			}
			if m.IsNegative() {
				t.Errorf("entrada externa nunca deveria ser negativa: %d", m.Minor())
			}
		})
	}
}

func TestParseAmountInvalido(t *testing.T) {
	casos := []struct {
		nome   string
		amount string
	}{
		{"vazio", ""},
		{"um espaco", " "},
		{"so espacos", "   "},
		{"sem separador decimal", "25"},
		{"uma casa decimal", "25.0"},
		{"tres casas decimais", "25.000"},
		{"quatro casas decimais", "25.0000"},
		{"sem parte inteira", ".50"},
		{"sem parte fracionaria", "25."},
		{"so o ponto", "."},
		{"virgula decimal", "25,00"},
		{"separador de milhar", "1,000.00"},
		{"negativo", "-25.00"},
		{"negativo pequeno", "-0.01"},
		{"sinal positivo", "+25.00"},
		{"espaco a esquerda", " 25.00"},
		{"espaco a direita", "25.00 "},
		{"espaco no meio", "2 5.00"},
		{"notacao cientifica", "1e2"},
		{"notacao cientifica maiuscula", "1E2"},
		{"notacao cientifica negativa", "1e-2"},
		{"NaN", "NaN"},
		{"Inf", "Inf"},
		{"Inf negativo", "-Inf"},
		{"Infinity", "Infinity"},
		{"texto", "abc"},
		{"texto com numeros", "25abc.00"},
		{"dois pontos", "25.00.00"},
		{"dois sinais", "--25.00"},
		{"sinal no meio", "2-5.00"},
		{"moeda junto", "R$25.00"},
		{"dezesseis digitos", "9999999999999999.99"},
		{"dezessete digitos", "99999999999999999.99"},
		{"dezenove digitos", "92233720368547758.07"},
		{"teto do int64", "9223372036854775807.00"},
		{"hexadecimal", "0x19.00"},
		{"underscore", "2_5.00"},
		{"barra", "25/00"},
		{"newline", "25.00\n"},
		{"tab", "\t25.00"},
		{"so zero e ponto", "0."},
		{"zeros sem ponto", "0000"},

		// Estes têm 15 dígitos na parte inteira, então passam pelo portão de
		// comprimento e são barrados pelo limite de valor. É a linha do laço
		// que acumula os dígitos e só depois descobre o estouro.
		{"15 digitos acima do limite", "900000000000001.00"},
		{"15 digitos muito acima do limite", "999999999999999.99"},
		{"15 noves com centavos", "999999999999999.00"},
		{"15 digitos com oitocentos", "800000000000000.01"},

		// Não-dígito na parte inteira: barrado dentro do laço, depois do
		// portão de comprimento.
		{"letra dentro da parte inteira", "2a5.00"},
		{"espaco dentro da parte inteira", "2 5.00"},
		{"ponto extra na parte inteira", "2.5.00"},

		// Não-dígito na parte fracionária: precisa ter exatamente 2 caracteres
		// para chegar no laço dos centavos.
		{"letra nas duas casas", "25.ab"},
		{"letra na segunda casa", "25.0a"},
		{"letra na primeira casa", "25.a0"},
		{"letra nas duas, maiuscula", "25.ZZ"},
		{"digito e letra", "25.9a"},
		{"letra e digito", "25.a9"},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			m, err := Parse(c.amount, "BRL")
			if err == nil {
				t.Fatalf("Parse(%q) deveria falhar, devolveu %d", c.amount, m.Minor())
			}
			if !errors.Is(err, ErrInvalidAmount) {
				t.Errorf("errors.Is(err, ErrInvalidAmount) = false; err = %v", err)
			}
			if CodeOf(err) != CodeInvalidAmount {
				t.Errorf("Code = %q", CodeOf(err))
			}
			if m.Minor() != 0 || m.Currency() != "" {
				t.Errorf("em caso de erro o Money deveria ser o zero value, veio %d/%q", m.Minor(), m.Currency())
			}
		})
	}
}

func TestParseMoedaInvalida(t *testing.T) {
	// A moeda é validada antes do valor: com moeda inválida, o Code é de moeda.
	for _, moeda := range []string{"", "brl", "BR", "986", "R$"} {
		t.Run(fmt.Sprintf("moeda=%q", moeda), func(t *testing.T) {
			_, err := Parse("25.00", moeda)
			if !errors.Is(err, ErrInvalidCurrency) {
				t.Fatalf("err = %v, esperado ErrInvalidCurrency", err)
			}
			if CodeOf(err) != CodeInvalidCurrency {
				t.Errorf("Code = %q", CodeOf(err))
			}
		})
	}
}

func TestParseParserAmountComMoedaValida(t *testing.T) {
	// Casos que o enunciado usa literalmente.
	m, err := Parse("975.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}
	if m.Minor() != 97500 {
		t.Errorf("Minor() = %d, esperado 97500", m.Minor())
	}
}

// ---------------------------------------------------------------------------
// New / FromMinor / Zero / acessores
// ---------------------------------------------------------------------------

func TestNew(t *testing.T) {
	casos := []struct {
		nome    string
		minor   int64
		moeda   Currency
		wantErr bool
	}{
		{"zero", 0, BRL, false},
		{"positivo", 2500, BRL, false},
		{"negativo interno", -2500, BRL, false},
		{"teto exato", maxMinor, BRL, false},
		{"piso exato", minMinor, BRL, false},
		{"um acima do teto", maxMinor + 1, BRL, true},
		{"um abaixo do piso", minMinor - 1, BRL, true},
		{"muito acima do teto", 9_223_372_036_854_775_807, BRL, true},
		{"muito abaixo do piso", -9_223_372_036_854_775_808, BRL, true},
		{"moeda vazia", 100, "", true},
		{"moeda minuscula", 100, "brl", true},
		{"moeda curta", 100, "BR", true},
		{"moeda numerica", 100, "986", true},
		{"moeda valida e valor invalido", maxMinor + 1, "USD", true},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			m, err := New(c.minor, c.moeda)
			if c.wantErr {
				if err == nil {
					t.Fatalf("New(%d, %q) deveria falhar, devolveu %+v", c.minor, c.moeda, m)
				}
				if m.Minor() != 0 || m.Currency() != "" {
					t.Errorf("em erro, Money deveria ser zero value: %d/%q", m.Minor(), m.Currency())
				}
				if !errors.Is(err, ErrInvalidAmount) && !errors.Is(err, ErrInvalidCurrency) {
					t.Errorf("erro não é de domínio: %v", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("New(%d, %q) devolveu erro: %v", c.minor, c.moeda, err)
			}
			if m.Minor() != c.minor {
				t.Errorf("Minor() = %d, esperado %d", m.Minor(), c.minor)
			}
			if m.Currency() != c.moeda {
				t.Errorf("Currency() = %q, esperado %q", m.Currency(), c.moeda)
			}
		})
	}
}

func TestNewClassificaErro(t *testing.T) {
	_, err := New(maxMinor+1, BRL)
	if !errors.Is(err, ErrInvalidAmount) {
		t.Errorf("valor acima do teto: err = %v, esperado ErrInvalidAmount", err)
	}
	_, err = New(100, "xx")
	if !errors.Is(err, ErrInvalidCurrency) {
		t.Errorf("moeda inválida: err = %v, esperado ErrInvalidCurrency", err)
	}
}

func TestFromMinorNaoValida(t *testing.T) {
	// FromMinor é o caminho de reidratação: confia na origem e não valida.
	// Estes casos existem para travar esse contrato: se alguém acrescentar
	// validação aqui, o teste quebra e a decisão é revista de propósito.
	casos := []struct {
		nome  string
		minor int64
		moeda Currency
	}{
		{"acima da faixa", maxMinor + 1, BRL},
		{"abaixo da faixa", minMinor - 1, BRL},
		{"teto do int64", 9_223_372_036_854_775_807, BRL},
		{"piso do int64", -9_223_372_036_854_775_808, BRL},
		{"moeda nao validada", 100, Currency("xx")},
		{"moeda vazia", 100, Currency("")},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			m := FromMinor(c.minor, c.moeda)
			if m.Minor() != c.minor {
				t.Errorf("Minor() = %d, esperado %d", m.Minor(), c.minor)
			}
			if m.Currency() != c.moeda {
				t.Errorf("Currency() = %q, esperado %q", m.Currency(), c.moeda)
			}
		})
	}
}

func TestZero(t *testing.T) {
	casos := []struct {
		nome    string
		moeda   Currency
		wantErr bool
	}{
		{"BRL", BRL, false},
		{"USD", "USD", false},
		{"vazia", "", true},
		{"minuscula", "brl", true},
		{"numerica", "986", true},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			m, err := Zero(c.moeda)
			if c.wantErr {
				if err == nil {
					t.Fatalf("Zero(%q) deveria falhar", c.moeda)
				}
				return
			}
			if err != nil {
				t.Fatalf("Zero(%q) devolveu erro: %v", c.moeda, err)
			}
			if !m.IsZero() {
				t.Errorf("IsZero() = false")
			}
			if m.Minor() != 0 {
				t.Errorf("Minor() = %d", m.Minor())
			}
			if m.Currency() != c.moeda {
				t.Errorf("Currency() = %q", m.Currency())
			}
			if m.Sign() != CmpEqual {
				t.Errorf("Sign() = %d, esperado CmpEqual", m.Sign())
			}
		})
	}
}

func TestAcessores(t *testing.T) {
	m := mustParse(t, "25.99", "BRL")
	if m.Minor() != 2599 {
		t.Errorf("Minor() = %d", m.Minor())
	}
	if m.Currency() != BRL {
		t.Errorf("Currency() = %q", m.Currency())
	}
	if got := m.Currency().String(); got != "BRL" {
		t.Errorf("Currency().String() = %q", got)
	}

	neg := mustFromMinor(-2599, "BRL")
	if neg.Minor() != -2599 {
		t.Errorf("Minor() de negativo = %d", neg.Minor())
	}
}
