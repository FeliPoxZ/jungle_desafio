package postgres

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// Auxiliares usados pelos testes deste pacote.

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

// encodeRaw codifica em base64url cru, para montar cursores malformados nos
// testes sem depender de EncodeCursor.
func encodeRaw(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}
