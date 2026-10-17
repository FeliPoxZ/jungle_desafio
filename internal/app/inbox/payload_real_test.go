package inbox

import (
	"testing"
)

// TestDecodificarCorpoRealDoBroker usa o corpo EXATO que o teste de integração
// enviou para a fila — o mesmo JSON, com as chaves em ordem alfabética, porque
// `json.Marshal` de um `map` ordena.
//
// Existe porque o teste de integração falhou com "IDEMPOTENCY_KEY_REQUIRED" para
// um corpo que TEM a chave: a dúvida era se o problema estava na tradução (aqui)
// ou no caminho depois dela.
func TestDecodificarCorpoRealDoBroker(t *testing.T) {
	corpo := `{"data":{"externalTransactionId":"txn-sqs-1","gameId":"fortune-chimp","idempotencyKey":"provider-a:txn-sqs-1","kind":"BET","money":{"amount":"25.00","currency":"BRL"},"playerId":"4bca39e6-20bc-4480-85c2-e1b3640bb54a","providerId":"provider-a","roundId":"round-1","walletId":"01a14755-416d-75f0-9a26-838ee8178e27"},"occurredAt":"2026-09-08T12:00:00.000Z","type":"WagerTransactionRequested"}`

	op, err := decodificar([]byte(corpo), "76075ee0-8d1a-4ee6-8e42-d5786ead0cdc")
	if err != nil {
		t.Fatalf("decodificar devolveu erro: %v", err)
	}

	if op.Request.IdempotencyKey != "provider-a:txn-sqs-1" {
		t.Errorf("IdempotencyKey = %q, quer %q", op.Request.IdempotencyKey, "provider-a:txn-sqs-1")
	}
	if op.Request.Amount.Minor() != 2500 {
		t.Errorf("Amount = %d, quer 2500", op.Request.Amount.Minor())
	}
	if op.Request.Kind.String() != "BET" {
		t.Errorf("Kind = %q, quer BET", op.Request.Kind)
	}
}
