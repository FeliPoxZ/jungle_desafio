package money

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Este arquivo cobre o contrato externo do Money: a serialização em JSON e a
// representação textual. É o formato que o HTTP e o SQS enxergam, então os
// casos aqui são byte a byte, não "parecido".

// jsonDe serializa e devolve o texto, falhando o teste em caso de erro.
func jsonDe(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal falhou: %v", err)
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// String
// ---------------------------------------------------------------------------

func TestMoneyString(t *testing.T) {
	casos := []struct {
		nome  string
		minor int64
		want  string
	}{
		{"zero", 0, "0.00"},
		{"um centavo", 1, "0.01"},
		{"dez centavos", 10, "0.10"},
		{"cinquenta centavos", 50, "0.50"},
		{"noventa e nove centavos", 99, "0.99"},
		{"um real", 100, "1.00"},
		{"um real e um centavo", 101, "1.01"},
		{"dez reais", 1000, "10.00"},
		{"vinte e cinco reais", 2500, "25.00"},
		{"vinte e cinco e noventa e nove", 2599, "25.99"},
		{"novecentos e setenta e cinco", 97500, "975.00"},
		{"mil reais", 100000, "1000.00"},
		{"mil e um centavo", 100001, "1000.01"},
		{"dez milhoes", 1000000000, "10000000.00"},
		{"teto do limite", maxMinor, "90000000000000.00"},
		{"teto menos um centavo", maxMinor - 1, "89999999999999.99"},
		{"negativo centavo", -1, "-0.01"},
		{"negativo dez centavos", -10, "-0.10"},
		{"negativo dezenove e noventa e nove", -1999, "-19.99"},
		{"negativo um real", -100, "-1.00"},
		{"negativo vinte e cinco", -2500, "-25.00"},
		{"piso do limite", minMinor, "-90000000000000.00"},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got := mustFromMinor(c.minor, "BRL").String()
			if got != c.want {
				t.Errorf("String() = %q, esperado %q", got, c.want)
			}
		})
	}
}

func TestMoneyStringTemSempreDuasCasas(t *testing.T) {
	// A escala é fixa em 2: nenhum valor pode sair com 1 ou 3 casas, e nenhum
	// pode sair sem o ponto.
	valores := []int64{
		0, 1, 9, 10, 99, 100, 101, 999, 1000, 1001,
		-1, -9, -10, -99, -100, -101, -999, -1000,
		maxMinor, maxMinor - 1, minMinor, minMinor + 1,
	}

	for _, v := range valores {
		s := mustFromMinor(v, "BRL").String()

		ponto := strings.IndexByte(s, '.')
		if ponto < 0 {
			t.Errorf("String() de %d = %q, sem separador decimal", v, s)
			continue
		}
		if casas := len(s) - ponto - 1; casas != 2 {
			t.Errorf("String() de %d = %q, com %d casas", v, s, casas)
		}
		if strings.Contains(s, " ") {
			t.Errorf("String() de %d = %q, com espaço", v, s)
		}
		if strings.Contains(s, ",") {
			t.Errorf("String() de %d = %q, com vírgula", v, s)
		}
		// Sinal só quando negativo, e só na frente.
		if strings.Contains(s[1:], "-") {
			t.Errorf("String() de %d = %q, sinal fora do início", v, s)
		}
		if strings.HasPrefix(s, "-") != (v < 0) {
			t.Errorf("String() de %d = %q, sinal incorreto", v, s)
		}
	}
}

func TestMoneyStringVoltaPeloParse(t *testing.T) {
	// String e Parse precisam ser inversos para valores não negativos, que é o
	// que circula no contrato externo.
	valores := []int64{0, 1, 99, 100, 2500, 2599, 97500, maxMinor - 1, maxMinor}
	for _, v := range valores {
		s := mustFromMinor(v, "BRL").String()

		m, err := Parse(s, "BRL")
		if err != nil {
			t.Errorf("Parse(%q) falhou para %d: %v", s, v, err)
			continue
		}
		if m.Minor() != v {
			t.Errorf("round-trip de %d: String = %q, Parse devolveu %d", v, s, m.Minor())
		}
	}
}

func TestMoneyStringUsaFmtStringer(t *testing.T) {
	// Money implementa fmt.Stringer: %v e %s usam o mesmo formato.
	m := mustFromMinor(97500, "BRL")

	if got := fmtSprint(m); got != "975.00" {
		t.Errorf("%%v = %q, esperado \"975.00\"", got)
	}
	if got := m.String(); got != "975.00" {
		t.Errorf("String() = %q", got)
	}
}

func TestCurrencyString(t *testing.T) {
	if BRL.String() != "BRL" {
		t.Errorf("BRL.String() = %q", BRL.String())
	}
	if Currency("USD").String() != "USD" {
		t.Errorf("USD.String() = %q", Currency("USD").String())
	}
}

// ---------------------------------------------------------------------------
// MarshalJSON
// ---------------------------------------------------------------------------

func TestMarshalJSONFormatoExato(t *testing.T) {
	casos := []struct {
		nome     string
		minor    int64
		moeda    Currency
		wantJSON string
	}{
		{"zero", 0, "BRL", `{"amount":"0.00","currency":"BRL"}`},
		{"um centavo", 1, "BRL", `{"amount":"0.01","currency":"BRL"}`},
		{"vinte e cinco", 2500, "BRL", `{"amount":"25.00","currency":"BRL"}`},
		{"saldo do enunciado", 97500, "BRL", `{"amount":"975.00","currency":"BRL"}`},
		{"mil reais", 100000, "BRL", `{"amount":"1000.00","currency":"BRL"}`},
		{"dolar", 2500, "USD", `{"amount":"25.00","currency":"USD"}`},
		{"euro", 1, "EUR", `{"amount":"0.01","currency":"EUR"}`},
		{"negativo interno", -2500, "BRL", `{"amount":"-25.00","currency":"BRL"}`},
		{"negativo centavo", -1, "BRL", `{"amount":"-0.01","currency":"BRL"}`},
		{"teto", maxMinor, "BRL", `{"amount":"90000000000000.00","currency":"BRL"}`},
		{"piso", minMinor, "BRL", `{"amount":"-90000000000000.00","currency":"BRL"}`},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got := jsonDe(t, mustFromMinor(c.minor, c.moeda))
			if got != c.wantJSON {
				t.Errorf("Marshal = %s\nesperado %s", got, c.wantJSON)
			}
		})
	}
}

func TestMarshalJSONNuncaUsaNumero(t *testing.T) {
	// O amount tem que sair entre aspas. Um número JSON aqui significa que o
	// float entrou no caminho, e o item é eliminatório.
	casos := []int64{0, 1, 2500, 97500, maxMinor, -1, -2500, minMinor}

	for _, v := range casos {
		s := jsonDe(t, mustFromMinor(v, "BRL"))

		if strings.Contains(s, `"amount":0`) || strings.Contains(s, `"amount":-`) {
			t.Errorf("value %d saiu como número JSON: %s", v, s)
		}
		if !strings.Contains(s, `"amount":"`) {
			t.Errorf("value %d não saiu como string: %s", v, s)
		}
	}
}

func TestMarshalJSONEmStructAninhada(t *testing.T) {
	// O caso real do enunciado: money dentro de um corpo maior.
	type corpo struct {
		PlayerID string `json:"playerId"`
		Money    Money  `json:"money"`
	}

	c := corpo{PlayerID: "p-1", Money: mustFromMinor(2500, "BRL")}
	got := jsonDe(t, c)
	want := `{"playerId":"p-1","money":{"amount":"25.00","currency":"BRL"}}`

	if got != want {
		t.Errorf("Marshal = %s\nesperado %s", got, want)
	}
}

// ---------------------------------------------------------------------------
// UnmarshalJSON
// ---------------------------------------------------------------------------

func TestUnmarshalJSONValoresValidos(t *testing.T) {
	casos := []struct {
		nome     string
		json     string
		wantMino int64
		wantMoed Currency
	}{
		{"zero", `{"amount":"0.00","currency":"BRL"}`, 0, "BRL"},
		{"um centavo", `{"amount":"0.01","currency":"BRL"}`, 1, "BRL"},
		{"vinte e cinco", `{"amount":"25.00","currency":"BRL"}`, 2500, "BRL"},
		{"mil reais", `{"amount":"1000.00","currency":"BRL"}`, 100000, "BRL"},
		{"dolar", `{"amount":"25.00","currency":"USD"}`, 2500, "USD"},
		{"teto", `{"amount":"90000000000000.00","currency":"BRL"}`, maxMinor, "BRL"},
		{"com espacos em volta", `  {"amount":"25.00","currency":"BRL"}  `, 2500, "BRL"},
		{"com quebra de linha", "{\n  \"amount\": \"25.00\",\n  \"currency\": \"BRL\"\n}", 2500, "BRL"},
		{"ordem trocada", `{"currency":"BRL","amount":"25.00"}`, 2500, "BRL"},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			var m Money
			if err := json.Unmarshal([]byte(c.json), &m); err != nil {
				t.Fatalf("Unmarshal(%s) falhou: %v", c.json, err)
			}
			if m.Minor() != c.wantMino {
				t.Errorf("Minor() = %d, esperado %d", m.Minor(), c.wantMino)
			}
			if m.Currency() != c.wantMoed {
				t.Errorf("Currency() = %q, esperado %q", m.Currency(), c.wantMoed)
			}
		})
	}
}

func TestUnmarshalJSONRejeitados(t *testing.T) {
	casos := []struct {
		nome string
		json string
		// code vazio significa que o erro vem do encoding/json, não do domínio.
		code string
	}{
		// O ponto mais importante: amount como número perde precisão antes de
		// chegar ao domínio, então é erro de contrato.
		{"amount como numero", `{"amount":25.00,"currency":"BRL"}`, ""},
		{"amount como inteiro", `{"amount":25,"currency":"BRL"}`, ""},
		{"amount como numero grande", `{"amount":99999999999999999999,"currency":"BRL"}`, ""},
		{"amount como null", `{"amount":null,"currency":"BRL"}`, ""},
		{"amount como booleano", `{"amount":true,"currency":"BRL"}`, ""},
		{"amount como array", `{"amount":["25.00"],"currency":"BRL"}`, ""},
		{"amount como objeto", `{"amount":{"v":"25.00"},"currency":"BRL"}`, ""},
		{"amount como canal", `{"amount":"\u003c-chan int","currency":"BRL"}`, CodeInvalidAmount},
		{"currency como numero", `{"amount":"25.00","currency":986}`, ""},
		{"objeto inteiro como null", `null`, ""},
		{"objeto inteiro como array", `[{"amount":"25.00"}]`, ""},
		{"objeto inteiro como string", `"25.00"`, ""},
		{"objeto inteiro como numero", `25`, ""},
		{"json vazio", ``, ""},
		{"json so espacos", `   `, ""},
		{"json truncado", `{"amount":"25.00"`, ""},
		{"lixo depois do objeto", `{"amount":"25.00","currency":"BRL"} lixo`, ""},

		// Campos desconhecidos: erro de contrato. Um typo em "amout" não pode
		// virar cobrança de zero.
		{"campo desconhecido", `{"amout":"25.00","currency":"BRL"}`, ""},
		{"campo extra", `{"amount":"25.00","currency":"BRL","extra":1}`, ""},
		{"campo duplicado", `{"amount":"25.00","amount":"99.00","currency":"BRL"}`, ""},

		// Campos ausentes: zero value de Money é valor de domínio inválido.
		{"amount ausente", `{"currency":"BRL"}`, CodeInvalidAmount},
		{"currency ausente", `{"amount":"25.00"}`, CodeInvalidCurrency},
		{"objeto vazio", `{}`, CodeInvalidAmount},
		{"amount vazio", `{"amount":"","currency":"BRL"}`, CodeInvalidAmount},

		// Contrato de valor, herdado do Parse.
		{"amount negativo", `{"amount":"-25.00","currency":"BRL"}`, CodeInvalidAmount},
		{"amount com sinal positivo", `{"amount":"+25.00","currency":"BRL"}`, CodeInvalidAmount},
		{"amount uma casa", `{"amount":"25.0","currency":"BRL"}`, CodeInvalidAmount},
		{"amount tres casas", `{"amount":"25.000","currency":"BRL"}`, CodeInvalidAmount},
		{"amount sem ponto", `{"amount":"2500","currency":"BRL"}`, CodeInvalidAmount},
		{"amount com virgula", `{"amount":"25,00","currency":"BRL"}`, CodeInvalidAmount},
		{"amount com espaco", `{"amount":" 25.00","currency":"BRL"}`, CodeInvalidAmount},
		{"amount em notacao cientifica", `{"amount":"1e2","currency":"BRL"}`, CodeInvalidAmount},
		{"amount acima do limite", `{"amount":"9999999999999999.99","currency":"BRL"}`, CodeInvalidAmount},
		{"amount com texto", `{"amount":"abc","currency":"BRL"}`, CodeInvalidAmount},
		{"currency minuscula", `{"amount":"25.00","currency":"brl"}`, CodeInvalidCurrency},
		{"currency numerica", `{"amount":"25.00","currency":"986"}`, CodeInvalidCurrency},
		{"currency curta", `{"amount":"25.00","currency":"BR"}`, CodeInvalidCurrency},
		{"currency longa", `{"amount":"25.00","currency":"BRLL"}`, CodeInvalidCurrency},
		{"currency com simbolo", `{"amount":"25.00","currency":"R$"}`, CodeInvalidCurrency},
		{"currency vazia", `{"amount":"25.00","currency":""}`, CodeInvalidCurrency},
		{"currency com espaco", `{"amount":"25.00","currency":"B L"}`, CodeInvalidCurrency},
		{"currency com aninhamento", `{"amount":"25.00","currency":"<b>"}`, CodeInvalidCurrency},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			// Receptor com valor: o caso de erro não pode deixá-lo alterado.
			m := mustFromMinor(777, "BRL")

			err := json.Unmarshal([]byte(c.json), &m)
			if err == nil {
				t.Fatalf("Unmarshal(%s) deveria falhar, devolveu %d/%q", c.json, m.Minor(), m.Currency())
			}
			if m.Minor() != 777 || m.Currency() != "BRL" {
				t.Errorf("receptor alterado em caso de erro: %d/%q", m.Minor(), m.Currency())
			}

			if c.code == "" {
				return // erro do encoding/json: já validado pelo não-nil
			}
			if !errors.Is(err, ErrInvalidAmount) && !errors.Is(err, ErrInvalidCurrency) {
				t.Errorf("erro não é de domínio: %v", err)
			}
			if got := CodeOf(err); got != c.code {
				t.Errorf("Code = %q, esperado %q", got, c.code)
			}
		})
	}
}

func TestUnmarshalJSONRejeitaNumeroAntesDoDominio(t *testing.T) {
	// Prova explícita do motivo de amount ser string: um número JSON seria
	// arredondado pelo float64 do encoding/json sem ninguém perceber.
	var m Money
	err := json.Unmarshal([]byte(`{"amount":0.1,"currency":"BRL"}`), &m)
	if err == nil {
		t.Fatalf("número JSON deveria ser rejeitado, veio %d", m.Minor())
	}
	if errors.Is(err, ErrInvalidAmount) {
		t.Errorf("o erro deveria vir do encoding/json (tipo), não do domínio: %v", err)
	}
	if !strings.Contains(err.Error(), "cannot unmarshal") {
		t.Errorf("erro inesperado: %v", err)
	}
}

func TestUnmarshalJSONPorPonteiroNil(t *testing.T) {
	// encoding/json devolve erro próprio para ponteiro nil em UnmarshalJSON.
	var p *Money
	err := json.Unmarshal([]byte(`{"amount":"25.00","currency":"BRL"}`), p)
	if err == nil {
		t.Fatal("esperado erro ao deserializar em ponteiro nil")
	}
}

// ---------------------------------------------------------------------------
// Detecção de chave repetida
//
// rejectDuplicateKeys é chamada direto aqui porque, via UnmarshalJSON, as
// guardas de forma são inalcançáveis: o Decode anterior já rejeitou tudo que
// não seja objeto bem formado. Testar direto é como se prova que a função em
// si está correta.
// ---------------------------------------------------------------------------

func TestRejectDuplicateKeys(t *testing.T) {
	casos := []struct {
		nome    string
		json    string
		queroEr bool
	}{
		{"objeto de um campo", `{"amount":"25.00"}`, false},
		{"objeto de dois campos", `{"amount":"25.00","currency":"BRL"}`, false},
		{"objeto vazio", `{}`, false},
		{"tres campos distintos", `{"amount":"1.00","currency":"BRL","extra":1}`, false},
		{"valor nulo", `{"amount":null}`, false},
		{"valor aninhado", `{"amount":{"a":{"b":1}}}`, false},
		{"valor array", `{"amount":[1,2,3]}`, false},
		{"valor string com chave falsa", `{"amount":"{\"x\":1}"}`, false},

		{"campo repetido", `{"amount":"1.00","amount":"2.00"}`, true},
		{"campo repetido com outros no meio", `{"a":1,"b":2,"a":3}`, true},
		{"campo repetido tres vezes", `{"a":1,"a":2,"a":3}`, true},
		{"repetido logo na abertura", `{"a":1,"a":2}`, true},
		{"repetido no fim", `{"b":1,"c":2,"c":3}`, true},
		{"repetido com valor aninhado", `{"a":{"x":1},"a":{"y":2}}`, true},

		{"nao e objeto", `[]`, true},
		{"e string", `"25.00"`, true},
		{"e numero", `25`, true},
		{"e null", `null`, true},
		{"vazio", ``, true},
		{"truncado", `{"amount":`, true},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			err := rejectDuplicateKeys([]byte(c.json))
			if c.queroEr && err == nil {
				t.Errorf("rejectDuplicateKeys(%s) deveria falhar", c.json)
			}
			if !c.queroEr && err != nil {
				t.Errorf("rejectDuplicateKeys(%s) falhou: %v", c.json, err)
			}
		})
	}
}

func TestUnmarshalJSONRejeitaRepeticaoAninhadaNoObjeto(t *testing.T) {
	// Repetição de campo dentro de um valor aninhado não é do nosso contrato
	// (o objeto aninhado não é um Money), mas o campo de TOPO tem que ser
	// único. Este teste separa os dois casos para a regra ficar explícita.
	var m Money

	// Repetição no topo: erro.
	if err := json.Unmarshal([]byte(`{"amount":"1.00","currency":"BRL","amount":"2.00"}`), &m); err == nil {
		t.Error("repetição no topo deveria falhar")
	}

	// Objeto aninhado em campo desconhecido: já barrado por campo desconhecido.
	if err := json.Unmarshal([]byte(`{"amount":"1.00","currency":"BRL","x":{"y":1,"y":2}}`), &m); err == nil {
		t.Error("campo desconhecido deveria falhar")
	}
}

// ---------------------------------------------------------------------------
// Round-trip
// ---------------------------------------------------------------------------

func TestRoundTripJSON(t *testing.T) {
	// Round-trip vale para os valores que circulam no contrato externo, isto é,
	// não negativos. O caso negativo é assimétrico de propósito e tem teste
	// próprio (TestRoundTripJSONNaoValeParaNegativo).
	type par struct {
		nome  string
		minor int64
		moeda Currency
	}

	casos := []par{
		{"zero", 0, "BRL"},
		{"um centavo", 1, "BRL"},
		{"centavos", 99, "BRL"},
		{"um real", 100, "BRL"},
		{"vinte e cinco", 2500, "BRL"},
		{"saldo do enunciado", 97500, "BRL"},
		{"mil reais", 100000, "BRL"},
		{"teto", maxMinor, "BRL"},
		{"teto menos um", maxMinor - 1, "BRL"},
		{"dolar", 2500, "USD"},
		{"euro", 1234, "EUR"},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			original := mustFromMinor(c.minor, c.moeda)

			b, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}

			var volta Money
			if err := json.Unmarshal(b, &volta); err != nil {
				t.Fatalf("Unmarshal de %s: %v", b, err)
			}

			eq, err := original.Equal(volta)
			if err != nil {
				t.Fatalf("Equal: %v", err)
			}
			if !eq {
				t.Errorf("round-trip mudou o valor: %s -> %d/%q", b, volta.Minor(), volta.Currency())
			}
			if volta.Minor() != c.minor {
				t.Errorf("round-trip: minor %d -> %d", c.minor, volta.Minor())
			}
			if volta.Currency() != c.moeda {
				t.Errorf("round-trip: moeda %q -> %q", c.moeda, volta.Currency())
			}
		})
	}
}

func TestRoundTripJSONNaoValeParaNegativo(t *testing.T) {
	// A serialização é assimétrica de propósito: um Money negativo, que só
	// existe em diferenças e cálculos internos, SAI com sinal, mas não volta,
	// porque entrada financeira externa negativa é inválida (README §6.1).
	// Este teste trava essa decisão para ela não virar bug silencioso.
	original := mustFromMinor(-2500, "BRL")

	b, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if got := string(b); got != `{"amount":"-25.00","currency":"BRL"}` {
		t.Errorf("Marshal de negativo = %s", got)
	}

	var volta Money
	err = json.Unmarshal(b, &volta)
	if err == nil {
		t.Fatalf("Unmarshal de valor negativo deveria falhar, devolveu %d", volta.Minor())
	}
	if !errors.Is(err, ErrInvalidAmount) {
		t.Errorf("erro = %v, esperado ErrInvalidAmount", err)
	}
}

func TestRoundTripJSONEmCorpoDoEnunciado(t *testing.T) {
	// Corpo real do enunciado, com money aninhado.
	const corpo = `{
		"playerId": "player-123",
		"currency": "BRL",
		"initialBalance": { "amount": "1000.00", "currency": "BRL" }
	}`

	type requisicao struct {
		PlayerID       string `json:"playerId"`
		Currency       string `json:"currency"`
		InitialBalance Money  `json:"initialBalance"`
	}

	var req requisicao
	if err := json.Unmarshal([]byte(corpo), &req); err != nil {
		t.Fatalf("Unmarshal do corpo: %v", err)
	}
	if req.InitialBalance.Minor() != 100000 {
		t.Errorf("initialBalance = %d, esperado 100000", req.InitialBalance.Minor())
	}

	saida := jsonDe(t, req)
	want := `{"playerId":"player-123","currency":"BRL","initialBalance":{"amount":"1000.00","currency":"BRL"}}`
	if saida != want {
		t.Errorf("reserialização = %s\nesperado %s", saida, want)
	}
}

func TestRoundTripStringNaoAlteraOMoney(t *testing.T) {
	// Value object é imutável: nem String nem Marshal mudam o receptor.
	m := mustFromMinor(2500, "BRL")
	antes := m.Minor()

	_ = m.String()
	_ = jsonDe(t, m)

	if m.Minor() != antes || m.Currency() != "BRL" {
		t.Errorf("o receptor mudou: %d/%q", m.Minor(), m.Currency())
	}
}

func TestUnmarshalNaoCompartilhaEstado(t *testing.T) {
	// Dois Unmarshal no mesmo receptor devem ser independentes.
	var m Money
	if err := json.Unmarshal([]byte(`{"amount":"10.00","currency":"BRL"}`), &m); err != nil {
		t.Fatal(err)
	}
	primeiro := m.Minor()

	if err := json.Unmarshal([]byte(`{"amount":"20.00","currency":"USD"}`), &m); err != nil {
		t.Fatal(err)
	}
	if primeiro != 1000 {
		t.Errorf("primeiro valor = %d, esperado 1000", primeiro)
	}
	if m.Minor() != 2000 || m.Currency() != "USD" {
		t.Errorf("segundo Unmarshal = %d/%q", m.Minor(), m.Currency())
	}
}
