package canonicaljson

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChavesOrdenadasEmTodosOsNiveis(t *testing.T) {
	// O caso que motiva o pacote: mesma informação, ordem de declaração
	// diferente, bytes idênticos.
	a := map[string]any{"z": 1, "a": 2, "m": 3}
	b := map[string]any{"m": 3, "z": 1, "a": 2}

	ba, err := Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := Marshal(b)
	if err != nil {
		t.Fatal(err)
	}

	if string(ba) != string(bb) {
		t.Errorf("mapas com o mesmo conteúdo geraram bytes diferentes:\n  %s\n  %s", ba, bb)
	}
	if string(ba) != `{"a":2,"m":3,"z":1}` {
		t.Errorf("ordem inesperada: %s", ba)
	}
}

func TestAninhamentoProfundo(t *testing.T) {
	v := map[string]any{
		"b": map[string]any{"y": 1, "x": 2},
		"a": []any{
			map[string]any{"n": 1, "m": 2},
			"texto",
		},
	}

	got, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":[{"m":2,"n":1},"texto"],"b":{"x":2,"y":1}}`
	if string(got) != want {
		t.Errorf("Marshal =\n  %s\nesperado\n  %s", got, want)
	}
}

func TestEstruturaIgualProduzMesmoHash(t *testing.T) {
	type payload struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	}
	type payloadTrocado struct {
		Currency string `json:"currency"`
		Amount   string `json:"amount"`
	}

	// Structs com a MESMA tag mas ordem de campo trocada precisam gerar o
	// mesmo hash: é o caso do conteúdo idêntico vindo de caminhos diferentes.
	h1, err := Hash(payload{Amount: "25.00", Currency: "BRL"})
	if err != nil {
		t.Fatal(err)
	}
	h2, err := Hash(payloadTrocado{Amount: "25.00", Currency: "BRL"})
	if err != nil {
		t.Fatal(err)
	}

	if h1 != h2 {
		t.Errorf("mesmo conteúdo gerou hashes diferentes: %s vs %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("hash com %d caracteres, esperado 64 (SHA-256 em hex)", len(h1))
	}
}

func TestConteudoDiferenteProduzHashDiferente(t *testing.T) {
	h1, _ := Hash(map[string]any{"amount": "25.00"})
	h2, _ := Hash(map[string]any{"amount": "26.00"})
	h3, _ := Hash(map[string]any{"amount": "25.00", "x": nil})

	if h1 == h2 {
		t.Error("valores diferentes geraram o mesmo hash")
	}
	if h1 == h3 {
		t.Error("objetos com campos diferentes geraram o mesmo hash")
	}
}

func TestArraysPreservamOrdem(t *testing.T) {
	// Ordem em array é significativa: não pode ser ordenada.
	a, err := Marshal([]any{3, 1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != "[3,1,2]" {
		t.Errorf("array foi reordenado: %s", a)
	}
}

func TestNaoEscapaHTML(t *testing.T) {
	got, err := Marshal(map[string]any{"moeda": "R$ <b>"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), `\u003c`) {
		t.Errorf("HTML foi escapado: %s", got)
	}
	if string(got) != `{"moeda":"R$ <b>"}` {
		t.Errorf("Marshal = %s", got)
	}
}

func TestNumerosGrandesPreservamPrecisao(t *testing.T) {
	// UseNumber impede o caminho float64, que arredondaria um inteiro grande.
	got, err := Marshal(map[string]any{"v": json.Number("99999999999999999999")})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"v":99999999999999999999}` {
		t.Errorf("número grande perdeu precisão: %s", got)
	}
}

func TestEscalaresECompacto(t *testing.T) {
	casos := []struct {
		nome string
		in   any
		want string
	}{
		{"string", "abc", `"abc"`},
		{"numero", 42, `42`},
		{"booleano", true, `true`},
		{"nulo", nil, `null`},
		{"float", 1.5, `1.5`},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got, err := Marshal(c.in)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Errorf("Marshal(%v) = %s, esperado %s", c.in, got, c.want)
			}
		})
	}
}

func TestSemEspacoSupérfluo(t *testing.T) {
	got, err := Marshal(map[string]any{"a": 1, "b": []any{1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), " ") || strings.Contains(string(got), "\n") {
		t.Errorf("saída tem espaço ou quebra de linha: %q", got)
	}
}

func TestPayloadNaoSerializavel(t *testing.T) {
	if _, err := Marshal(make(chan int)); err == nil {
		t.Error("canal deveria falhar na serialização")
	}
	if _, err := Hash(make(chan int)); err == nil {
		t.Error("Hash de canal deveria falhar")
	}
}

func TestVazioEArraysVazios(t *testing.T) {
	casos := []struct {
		nome string
		in   any
		want string
	}{
		{"objeto vazio", map[string]any{}, `{}`},
		{"array vazio", []any{}, `[]`},
		{"objeto com array vazio", map[string]any{"a": []any{}}, `{"a":[]}`},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			got, err := Marshal(c.in)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Errorf("Marshal = %s, esperado %s", got, c.want)
			}
		})
	}
}
