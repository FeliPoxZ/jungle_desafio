package wagering

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ledger"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
)

// emitirProcessado grava o evento de conclusão e, quando houve lançamento, o de
// mudança de saldo.
//
// LOSS entra no primeiro e NÃO entra no segundo: a rodada terminou e o provedor
// precisa da confirmação, mas o saldo não mudou e a versão da carteira não
// avançou. "Não movimenta dinheiro" não é "não aconteceu nada".
//
// `entrada` nil é o que diz "não houve lançamento". É por isso que a função
// recebe a entrada, e não um `Movement`: a abertura de carteira também gera
// lançamento de crédito — partindo de zero — sem que exista movimentação no
// agregado.
func (s *Service) emitirProcessado(
	ctx context.Context,
	r ports.Repos,
	tx *wagerdomain.WagerTransaction,
	correlacao, causacao string,
	saldo money.Money,
	entrada *ledger.Entry,
	versaoDaCarteira int64,
	now time.Time,
) error {
	processado, err := tx.ProcessedEvent(s.ids.NewID(), correlacao, causacao, &saldo, now)
	if err != nil {
		return fmt.Errorf("wagering: evento de conclusão: %w", err)
	}
	if err := s.enfileirar(ctx, r, processado); err != nil {
		return err
	}

	if entrada == nil {
		return nil
	}

	mudanca, err := wagerdomain.NewBalanceChangedEvent(
		s.ids.NewID(), correlacao, causacao,
		tx.ID(), *entrada, versaoDaCarteira, now,
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
	correlacao, causacao string,
	now time.Time,
) error {
	rejeitado, err := tx.RejectedEvent(s.ids.NewID(), correlacao, causacao, now)
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
	correlacao, causacao string,
	now time.Time,
) error {
	pendente, err := tx.PendingReferenceEvent(s.ids.NewID(), correlacao, causacao, now)
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
