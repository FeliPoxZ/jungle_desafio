package ports

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// contains é um atalho para deixar as asserções legíveis.
func contains(s, sub string) bool { return strings.Contains(s, sub) }

// mustTime converte um instante RFC3339Nano, falhando o teste se for inválido.
func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("instante inválido %q: %v", s, err)
	}
	return v
}

// encodeRaw codifica em base64url cru, para montar cursores malformados sem
// depender de EncodeCursor.
func encodeRaw(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}
func TestCursorRoundTrip(t *testing.T) {
	original := Cursor{
		CreatedAt: mustTime(t, "2026-10-16T12:00:00.123456789Z"),
		ID:        "22222222-2222-2222-2222-222222222222",
	}

	codificado := EncodeCursor(original)
	if codificado == "" {
		t.Fatal("cursor codificado vazio")
	}
	// base64url não deve ter caractere que quebre query string ou header.
	if contains(codificado, "+") || contains(codificado, "/") || contains(codificado, "=") {
		t.Errorf("cursor não é base64url cru: %q", codificado)
	}

	decodificado, err := DecodeCursor(codificado)
	if err != nil {
		t.Fatalf("DecodeCursor falhou: %v", err)
	}
	if !decodificado.CreatedAt.Equal(original.CreatedAt) {
		t.Errorf("instante: %v != %v", decodificado.CreatedAt, original.CreatedAt)
	}
	if decodificado.ID != original.ID {
		t.Errorf("id: %q != %q", decodificado.ID, original.ID)
	}
}

func TestDecodeCursorInvalido(t *testing.T) {
	casos := []string{
		"",
		"   ",
		"nao-e-base64!!!",
		"YWJj",             // "abc": sem separador e sem instante
		"MjAyNi0xMC0xNg==", // base64 padrão, sem separador
		"bm9tZXx8aWQ=",     // instante inválido com id vazio
	}

	for _, c := range casos {
		t.Run(c, func(t *testing.T) {
			if _, err := DecodeCursor(c); err == nil {
				t.Errorf("DecodeCursor(%q) deveria falhar", c)
			}
		})
	}
}

func TestDecodeCursorSemIdentificador(t *testing.T) {
	// Instante válido mas sem id: a paginação pularia linhas.
	bruto := encodeRaw("2026-10-16T12:00:00Z|")
	if _, err := DecodeCursor(bruto); err == nil {
		t.Error("cursor sem identificador deveria falhar")
	}
}
