package postgres

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
)

// TestClassificadoresDoContratoReconhecemErroDoDriver é o teste de regressão da
// armadilha mais perigosa desta fronteira.
//
// # O bug que ele impede
//
// Se esta camada declarasse os PRÓPRIOS sentinelas (`postgres.ErrConflict`), e o
// contrato declarasse os dele (`ports.ErrConflict`), seriam dois objetos
// diferentes com o mesmo nome. A aplicação chamaria:
//
//	ports.IsConflict(err)   // err veio do repositório
//
// e receberia **false**. O serviço trataria conflito de idempotência como falha
// desconhecida — silenciosamente, sem erro de compilação e sem teste vermelho,
// porque cada lado estaria internamente consistente.
//
// Este teste passa um erro REAL produzido pela classificação e confere que as
// funções do CONTRATO o reconhecem. Se alguém voltar a duplicar os sentinelas,
// ele quebra.
func TestClassificadoresDoContratoReconhecemErroDoDriver(t *testing.T) {
	casos := []struct {
		nome       string
		code       string
		classifica func(error) bool
	}{
		{"23505 vira conflito", codeUniqueViolation, ports.IsConflict},
		{"23514 vira violação de regra", codeCheckViolation, ports.IsCheckViolation},
		{"23001 vira violação de regra", codeRestrictViolation, ports.IsCheckViolation},
		{"23503 vira chave estrangeira", codeForeignKeyViolation, ports.IsForeignKeyViolation},
		{"55P03 vira retryável", codeLockNotAvailable, ports.IsRetryable},
		{"40P01 vira retryável", codeDeadlockDetected, ports.IsRetryable},
		{"40001 vira retryável", codeSerializationFailure, ports.IsRetryable},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			// Simula o erro cru do driver, como o pgx devolve.
			cru := &pgconn.PgError{Code: c.code, Message: "falha simulada"}
			classificado := Classify(cru)

			if !c.classifica(classificado) {
				t.Errorf("o classificador do contrato NÃO reconheceu o erro: %v", classificado)
			}

			// E o reconhecimento sobrevive a envelopamento, que é o caso real:
			// o repositório embrulha com contexto antes de devolver.
			envelopado := fmt.Errorf("postgres: gravando carteira: %w", classificado)
			if !c.classifica(envelopado) {
				t.Errorf("o classificador do contrato NÃO reconheceu o erro envelopado: %v", envelopado)
			}
		})
	}
}

// TestSentinelasSaoOsMesmosObjetos confirma a identidade, e não só o
// comportamento.
//
// `errors.Is` funcionaria por acaso se as mensagens coincidissem; aqui a garantia
// é que é literalmente o mesmo valor.
func TestSentinelasSaoOsMesmosObjetos(t *testing.T) {
	pares := []struct {
		nome     string
		postgres error
		porta    error
	}{
		{"ErrNotFound", ErrNotFound, ports.ErrNotFound},
		{"ErrConflict", ErrConflict, ports.ErrConflict},
		{"ErrCheck", ErrCheck, ports.ErrCheck},
		{"ErrForeignKey", ErrForeignKey, ports.ErrForeignKey},
		{"ErrTransient", ErrTransient, ports.ErrTransient},
		{"ErrPermanent", ErrPermanent, ports.ErrPermanent},
	}

	for _, p := range pares {
		if !errors.Is(p.postgres, p.porta) || !errors.Is(p.porta, p.postgres) {
			t.Errorf("%s: postgres e ports apontam para objetos diferentes", p.nome)
		}
	}
}
