package wagering

import (
	"context"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
)

// rejeitar grava a recusa por regra de negócio.
//
// A recusa é DESFECHO, e não erro: a operação existe, foi decidida e fica
// auditável com o `failureCode` que explica o motivo. O evento também entra na
// mesma transação — o provedor precisa saber que a operação não vai acontecer,
// e "não aconteceu nada" não é resposta.
func (s *Service) rejeitar(
	ctx context.Context,
	r ports.Repos,
	tx *wagerdomain.WagerTransaction,
	req Request,
	failureCode string,
	now time.Time,
) (Result, error) {
	if err := tx.MarkRejected(failureCode, now); err != nil {
		return Result{}, err
	}
	if err := r.Transactions.Save(ctx, tx); err != nil {
		return Result{}, err
	}
	if err := s.emitirRejeicao(ctx, r, tx, req, now); err != nil {
		return Result{}, err
	}
	return Result{TransactionID: tx.ID(), Status: tx.Status(), FailureCode: failureCode}, nil
}

// marcarPendente grava a espera pela referência e agenda a próxima tentativa.
//
// A tentativa e o instante ficam PERSISTIDOS: é isso que permite outra instância
// retomar a operação depois de uma interrupção, em vez de a pendência morrer com
// o processo que a criou.
func (s *Service) marcarPendente(
	ctx context.Context,
	r ports.Repos,
	tx *wagerdomain.WagerTransaction,
	req Request,
	now time.Time,
) (Result, error) {
	if err := tx.MarkPendingReference(1, now.Add(baseReferenciaPendente), now); err != nil {
		return Result{}, err
	}
	if err := r.Transactions.Save(ctx, tx); err != nil {
		return Result{}, err
	}
	if err := s.emitirPendencia(ctx, r, tx, req, now); err != nil {
		return Result{}, err
	}
	return Result{TransactionID: tx.ID(), Status: tx.Status()}, nil
}
