// Package postgres implementa a persistência com pgx.
//
// # Regra central desta camada
//
// Um erro do driver NÃO é um erro de negócio, mas precisa virar um. Aqui os
// códigos do PostgreSQL são traduzidos para erros tipados que o serviço da
// Fase 6 consegue classificar sem conhecer pgx:
//
//	ErrNotFound      linha não existe
//	ErrConflict      violação de unicidade → idempotência funcionando, ou conflito real
//	ErrCheck         violação de CHECK → invariante do domínio foi violada
//	ErrForeignKey    referência inexistente
//	ErrTransient     falha que pode ser repetida (deadlock, serialização, conexão)
//	ErrPermanent     o resto
package postgres

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// Erros sentinela da camada de persistência.
var (
	// ErrNotFound indica que a linha procurada não existe.
	ErrNotFound = errors.New("postgres: registro não encontrado")

	// ErrConflict indica violação de unicidade. No nosso schema isso NÃO é
	// "erro de banco": é a idempotência e as proteções de duplicidade
	// funcionando (chave de idempotência repetida, reversão duplicada,
	// lançamento duplicado).
	ErrConflict = errors.New("postgres: conflito de unicidade")

	// ErrCheck indica violação de CHECK: uma invariante do domínio foi violada
	// no caminho até o banco.
	ErrCheck = errors.New("postgres: violação de constraint de verificação")

	// ErrForeignKey indica referência a linha inexistente.
	ErrForeignKey = errors.New("postgres: violação de chave estrangeira")

	// ErrTransient indica falha que pode ser repetida com sucesso: deadlock,
	// falha de serialização, conexão fora do ar, recurso esgotado.
	ErrTransient = errors.New("postgres: falha transitória")

	// ErrPermanent indica falha que não se resolve repetindo.
	ErrPermanent = errors.New("postgres: falha permanente")
)

// Códigos do PostgreSQL usados na classificação.
// https://www.postgresql.org/docs/current/errcodes-appendix.html
const (
	// Class 23 — Integrity Constraint Violation
	codeUniqueViolation     = "23505"
	codeCheckViolation      = "23514"
	codeForeignKeyViolation = "23503"
	codeNotNullViolation    = "23502"
	codeRestrictViolation   = "23001"

	// Class 40 — Transaction Rollback
	codeSerializationFailure = "40001"
	codeDeadlockDetected     = "40P01"

	// Class 53/57/08 — recursos e indisponibilidade
	codeTooManyConnections = "53300"
	codeQueryCanceled      = "57014"
	codeAdminShutdown      = "57P01"
	codeCannotConnectNow   = "57P03"
	codeConnectionFailure  = "08006"
	codeSQLClientUnable    = "08001"

	// 55P03 (lock_not_available) é levantado quando o lock_timeout estoura em
	// algo como SELECT ... FOR UPDATE. É o caso MAIS importante de retry deste
	// projeto: significa que outra transação estava mexendo na mesma carteira, e
	// repetir logo em seguida tende a dar certo.
	//
	// Classificar isso como erro permanente faria o serviço rejeitar uma
	// operação de negócio legítima por causa de uma disputa momentânea.
	codeLockNotAvailable = "55P03"
)

// Classify traduz um erro do driver em erro tipado desta camada.
//
// Erros já classificados passam intactos: chamar Classify duas vezes não deve
// embrulhar um ErrConflict em outro ErrConflict.
func Classify(err error) error {
	if err == nil {
		return nil
	}

	// Já é da nossa taxonomia.
	for _, sentinela := range []error{ErrNotFound, ErrConflict, ErrCheck, ErrForeignKey, ErrTransient, ErrPermanent} {
		if errors.Is(err, sentinela) {
			return err
		}
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		// Sem código do servidor: conexão caiu, contexto cancelado, rede.
		return fmt.Errorf("%w: %v", ErrTransient, err)
	}

	switch pgErr.Code {
	case codeUniqueViolation:
		return fmt.Errorf("%w: %s (constraint %s)", ErrConflict, pgErr.Message, pgErr.ConstraintName)
	case codeCheckViolation:
		return fmt.Errorf("%w: %s (constraint %s)", ErrCheck, pgErr.Message, pgErr.ConstraintName)
	case codeForeignKeyViolation:
		return fmt.Errorf("%w: %s (constraint %s)", ErrForeignKey, pgErr.Message, pgErr.ConstraintName)
	case codeNotNullViolation:
		return fmt.Errorf("%w: coluna %s não pode ser nula", ErrCheck, pgErr.ColumnName)
	case codeRestrictViolation:
		// Levantado pelo trigger de imutabilidade do ledger.
		return fmt.Errorf("%w: %s", ErrCheck, pgErr.Message)

	case codeSerializationFailure, codeDeadlockDetected,
		codeTooManyConnections, codeQueryCanceled, codeAdminShutdown,
		codeCannotConnectNow, codeConnectionFailure, codeSQLClientUnable,
		codeLockNotAvailable:
		return fmt.Errorf("%w: %s (%s)", ErrTransient, pgErr.Message, pgErr.Code)
	}

	return fmt.Errorf("%w: %s (%s)", ErrPermanent, pgErr.Message, pgErr.Code)
}

// IsRetryable informa se a operação pode ser repetida com chance de sucesso.
//
// Fica aqui, e não no serviço, porque a decisão depende de detalhe do driver.
// Quem repete é a Fase 6, com backoff — repetir no repositório esconderia a
// semântica de "uma tentativa de transação".
func IsRetryable(err error) bool { return errors.Is(err, ErrTransient) }

// IsConflict informa se o erro foi violação de unicidade.
func IsConflict(err error) bool { return errors.Is(err, ErrConflict) }

// IsNotFound informa se a linha não existe.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
