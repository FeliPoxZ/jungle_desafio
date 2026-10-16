// Package postgres implementa a persistência com pgx.
//
// # Regra central desta camada
//
// Um erro do driver NÃO é um erro de negócio, mas precisa virar um. Aqui os
// códigos do PostgreSQL são traduzidos para os erros do CONTRATO (`app/ports`),
// que o serviço da Fase 6 classifica sem conhecer pgx.
//
// # Por que os sentinelas são aliases, e não erros novos
//
// Se esta camada declarasse os próprios `ErrConflict`/`ErrNotFound`, existiriam
// dois jogos de sentinelas com o mesmo nome. A aplicação chamaria
// `ports.IsConflict(err)` em um erro produzido aqui — e receberia `false`, porque
// seriam objetos diferentes. O bug seria silencioso: o serviço trataria conflito
// de idempotência como falha desconhecida.
//
// Com alias, existe UM jogo de sentinelas, e `IsConflict`/`IsRetryable`/
// `IsNotFound` do contrato funcionam para qualquer erro produzido aqui.
package postgres

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
)

// Erros do contrato de persistência, expostos aqui por conveniência.
//
// Os nomes são os mesmos de `ports` de propósito: o código desta camada lê
// `ErrConflict` e o serviço lê `ports.ErrConflict`, e é o MESMO objeto.
var (
	// ErrNotFound indica que a linha procurada não existe.
	ErrNotFound = ports.ErrNotFound

	// ErrConflict indica violação de unicidade ou versão desatualizada. No nosso
	// schema isso NÃO é "erro de banco": é a idempotência e as proteções de
	// duplicidade funcionando (chave repetida, reversão duplicada, lançamento
	// duplicado).
	ErrConflict = ports.ErrConflict

	// ErrCheck indica violação de CHECK ou de trigger: uma invariante do domínio
	// foi violada no caminho até o banco.
	ErrCheck = ports.ErrCheck

	// ErrForeignKey indica referência a linha inexistente.
	ErrForeignKey = ports.ErrForeignKey

	// ErrTransient indica falha que pode ser repetida com sucesso: deadlock,
	// falha de serialização, timeout de lock, conexão fora do ar.
	ErrTransient = ports.ErrTransient

	// ErrPermanent indica falha que não se resolve repetindo.
	ErrPermanent = ports.ErrPermanent
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

// As funções de classificação vivem no CONTRATO (`ports`), porque é lá que a
// aplicação as alcança sem importar este pacote.
//
// Não há redefinição aqui: `ports.IsRetryable` e a versão que o serviço usa são a
// mesma função. Duplicar criaria dois critérios que podem divergir com o tempo, e
// o serviço decidiria repetir uma operação que o repositório considera
// permanente.
