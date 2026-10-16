package wagering

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
)

// emitirProcessado grava o evento de conclusão e, quando houve movimentação, o
// de mudança de saldo.
//
// LOSS entra no primeiro e NÃO entra no segundo: a rodada terminou e o provedor
// precisa da confirmação, mas o saldo não mudou e a versão da carteira não
// avançou. "Não movimenta dinheiro" não é "não aconteceu nada".
func (s *Service) emitirProcessado(
	ctx context.Context,
	r ports.Repos,
	tx *wagerdomain.WagerTransaction,
	req Request,
	saldo money.Money,
	mov wallet.Movement,
	now time.Time,
) error {
	processado, err := tx.ProcessedEvent(s.ids.NewID(), req.CorrelationID, req.CausationID, &saldo, now)
	if err != nil {
		return fmt.Errorf("wagering: evento de conclusão: %w", err)
	}
	if err := s.enfileirar(ctx, r, processado); err != nil {
		return err
	}

	if mov.IsZero() {
		return nil
	}

	mudanca, err := wagerdomain.NewBalanceChangedEvent(
		s.ids.NewID(), req.CorrelationID, req.CausationID,
		tx.ID(), mov.Entry(), mov.Wallet().Version(), now,
	)
	if err != nil {
		return fmt.Errorf("wagering: evento de saldo: %w", err)
	}
	return s.enfileirar(ctx, r, mudanca)
}

// emitirRejeicao grava o evento da recusa definitiva.
func (s *Service) emitirRejeicao(
	ctx context.Context,
	r ports.Repos,
	tx *wagerdomain.WagerTransaction,
	req Request,
	now time.Time,
) error {
	rejeitado, err := tx.RejectedEvent(s.ids.NewID(), req.CorrelationID, req.CausationID, now)
	if err != nil {
		return fmt.Errorf("wagering: evento de rejeição: %w", err)
	}
	return s.enfileirar(ctx, r, rejeitado)
}

// emitirPendencia grava o evento da espera pela referência.
func (s *Service) emitirPendencia(
	ctx context.Context,
	r ports.Repos,
	tx *wagerdomain.WagerTransaction,
	req Request,
	now time.Time,
) error {
	pendente, err := tx.PendingReferenceEvent(s.ids.NewID(), req.CorrelationID, req.CausationID, now)
	if err != nil {
		return fmt.Errorf("wagering: evento de referência pendente: %w", err)
	}
	return s.enfileirar(ctx, r, pendente)
}

// enfileirar grava o evento na outbox, na MESMA transação do saldo e do ledger.
//
// Nada é publicado aqui. O SQS é externo e não participa do commit: publicar
// dentro da transação ou faz o evento falar de uma mudança que não aconteceu, ou
// faz a mudança acontecer sem ninguém ser avisado. Quem publica é o worker da
// Fase 9, lendo esta tabela — depois do commit.
//
// O payload é o envelope INTEIRO serializado, e vira um snapshot imutável na
// coluna JSONB: o evento publicado é o evento de agora, não a interpretação dele
// feita meses depois por uma versão nova do código.
func (s *Service) enfileirar(ctx context.Context, r ports.Repos, ev *wagerdomain.Event) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("wagering: serializando o evento %s: %w", ev.EventType, err)
	}

	err = r.Outbox.Insert(ctx, ports.OutboxEvent{
		EventID:       ev.EventID,
		AggregateID:   ev.AggregateID,
		AggregateType: tipoDeAgregado,
		EventType:     ev.EventType,
		Payload:       payload,
		OccurredAt:    ev.OccurredAt,
		AvailableAt:   ev.OccurredAt,
	})
	if err != nil {
		return fmt.Errorf("wagering: enfileirando o evento %s: %w", ev.EventType, err)
	}
	return nil
}
