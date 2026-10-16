package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestClassifyViolacaoDeUnicidade(t *testing.T) {
	// O caso que mais importa: no nosso schema, unique NÃO é erro de banco.
	// É a idempotência e as proteções de duplicidade funcionando.
	err := Classify(&pgconn.PgError{
		Code:           codeUniqueViolation,
		Message:        "duplicate key value violates unique constraint",
		ConstraintName: "wager_transactions_provider_idempotency_key",
	})

	if !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, esperado ErrConflict", err)
	}
	if !IsConflict(err) {
		t.Error("IsConflict = false")
	}
	if IsRetryable(err) {
		t.Error("conflito de unicidade não é retryável: repetir daria o mesmo erro")
	}
	if !contains(err.Error(), "wager_transactions_provider_idempotency_key") {
		t.Errorf("mensagem deveria citar a constraint: %v", err)
	}
}

func TestClassifyViolacaoDeCheck(t *testing.T) {
	err := Classify(&pgconn.PgError{
		Code:           codeCheckViolation,
		Message:        "new row violates check constraint",
		ConstraintName: "wallets_balance_non_negative",
	})

	if !errors.Is(err, ErrCheck) {
		t.Errorf("err = %v, esperado ErrCheck", err)
	}
	if IsRetryable(err) {
		t.Error("violação de CHECK não é retryável: os dados são os mesmos")
	}
}

func TestClassifyImutabilidadeDoLedger(t *testing.T) {
	// O trigger da Fase 4 levanta restrict_violation ao tentar UPDATE/DELETE.
	err := Classify(&pgconn.PgError{
		Code:    codeRestrictViolation,
		Message: "wallet_ledger_entries é append-only: UPDATE não é permitido",
	})

	if !errors.Is(err, ErrCheck) {
		t.Errorf("err = %v, esperado ErrCheck", err)
	}
}

func TestClassifyChaveEstrangeira(t *testing.T) {
	err := Classify(&pgconn.PgError{
		Code:           codeForeignKeyViolation,
		ConstraintName: "wallet_ledger_entries_wallet_id_fkey",
	})

	if !errors.Is(err, ErrForeignKey) {
		t.Errorf("err = %v, esperado ErrForeignKey", err)
	}
}

func TestClassifyTransitorios(t *testing.T) {
	// Deadlock, falha de serialização e conexão fora do ar PODEM dar certo se
	// repetidos: é isso que autoriza o retry na Fase 6.
	codigos := []string{
		codeSerializationFailure,
		codeDeadlockDetected,
		codeTooManyConnections,
		codeQueryCanceled,
		codeAdminShutdown,
		codeCannotConnectNow,
		codeConnectionFailure,
		codeSQLClientUnable,
	}

	for _, code := range codigos {
		t.Run(code, func(t *testing.T) {
			err := Classify(&pgconn.PgError{Code: code, Message: "falha"})
			if !errors.Is(err, ErrTransient) {
				t.Errorf("código %s: err = %v, esperado ErrTransient", code, err)
			}
			if !IsRetryable(err) {
				t.Errorf("código %s: deveria ser retryável", code)
			}
		})
	}
}

func TestClassifySemCodigoDoServidor(t *testing.T) {
	// Sem PgError: rede, contexto cancelado. Tratamos como transitório porque
	// repetir pode dar certo — e o serviço decide com backoff.
	err := Classify(errors.New("connection reset by peer"))
	if !errors.Is(err, ErrTransient) {
		t.Errorf("err = %v, esperado ErrTransient", err)
	}

	err = Classify(context.DeadlineExceeded)
	if !errors.Is(err, ErrTransient) {
		t.Errorf("deadline exceeded: err = %v, esperado ErrTransient", err)
	}
}

func TestClassifyPermanente(t *testing.T) {
	err := Classify(&pgconn.PgError{Code: "42601", Message: "syntax error"})
	if !errors.Is(err, ErrPermanent) {
		t.Errorf("err = %v, esperado ErrPermanent", err)
	}
	if IsRetryable(err) {
		t.Error("erro de sintaxe não é retryável")
	}
}

func TestClassifyNaoEmbrulhaDuasVezes(t *testing.T) {
	// Classify precisa ser idempotente: embrulhar um ErrConflict em outro
	// ErrConflict funcionaria no errors.Is, mas poluiria a mensagem a cada
	// camada por onde o erro passa.
	uma := Classify(&pgconn.PgError{Code: codeUniqueViolation, Message: "duplicado"})
	duas := Classify(uma)

	if uma.Error() != duas.Error() {
		t.Errorf("Classify não é idempotente:\n  1x: %v\n  2x: %v", uma, duas)
	}

	for _, sentinela := range []error{ErrNotFound, ErrConflict, ErrCheck, ErrForeignKey, ErrTransient, ErrPermanent} {
		jaClassificado := fmt.Errorf("%w: detalhe", sentinela)
		if got := Classify(jaClassificado); got.Error() != jaClassificado.Error() {
			t.Errorf("sentinela %v foi reembrulhado: %v", sentinela, got)
		}
	}
}

func TestClassifyNil(t *testing.T) {
	if err := Classify(nil); err != nil {
		t.Errorf("Classify(nil) = %v, esperado nil", err)
	}
}

func TestClassifyNaoNuloEmViolacao(t *testing.T) {
	err := Classify(&pgconn.PgError{
		Code:       codeNotNullViolation,
		ColumnName: "payload_hash",
	})
	if !errors.Is(err, ErrCheck) {
		t.Errorf("err = %v, esperado ErrCheck", err)
	}
	if !contains(err.Error(), "payload_hash") {
		t.Errorf("mensagem deveria citar a coluna: %v", err)
	}
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
