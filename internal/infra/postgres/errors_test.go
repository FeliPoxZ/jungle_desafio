package postgres

import (
	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"

	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// contains é um atalho para deixar as asserções legíveis.
func contains(s, sub string) bool { return strings.Contains(s, sub) }
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
	if !ports.IsConflict(err) {
		t.Error("IsConflict = false")
	}
	if ports.IsRetryable(err) {
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
	if ports.IsRetryable(err) {
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
			if !ports.IsRetryable(err) {
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
	if ports.IsRetryable(err) {
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
