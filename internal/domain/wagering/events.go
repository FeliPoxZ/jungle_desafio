package wagering

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
)

// Tipos de evento de domínio. O nome do tipo é parte do contrato: quem consome
// a fila decide o que fazer pelo eventType.
const (
	EventTypeProcessed            = "WagerTransactionProcessed"
	EventTypeRejected             = "WagerTransactionRejected"
	EventTypePendingReference     = "WagerTransactionPendingReference"
	EventTypeWalletBalanceChanged = "WalletBalanceChanged"
)

// EventVersion é a versão do envelope do evento.
const EventVersion = 1

// Event é o envelope comum a todo evento de domínio.
//
// Type e Version são definidos pelo construtor do evento, nunca por quem chama:
// assim não existe evento publicado com tipo errado ou versão esquecida.
type Event struct {
	EventID       string    `json:"eventId"`
	EventType     string    `json:"eventType"`
	AggregateID   string    `json:"aggregateId"`
	CorrelationID string    `json:"correlationId"`
	CausationID   string    `json:"causationId,omitempty"`
	OccurredAt    time.Time `json:"occurredAt"`
	Version       int       `json:"version"`
	Data          any       `json:"data"`
}

// NewEvent monta o envelope, serializando o payload para o formato de
// transporte. O erro de serialização interrompe a criação: evento que não
// serializa não pode ser publicado.
func NewEvent(
	eventID string,
	eventType string,
	aggregateID string,
	correlationID string,
	causationID string,
	occurredAt time.Time,
	data any,
) (*Event, error) {
	if eventID == "" {
		return nil, invalid("eventID é obrigatório")
	}
	if eventType == "" {
		return nil, invalid("eventType é obrigatório")
	}
	if aggregateID == "" {
		return nil, invalid("aggregateID é obrigatório")
	}
	if occurredAt.IsZero() {
		return nil, invalid("occurredAt é obrigatório")
	}
	if data == nil {
		return nil, invalid("data do evento é obrigatório")
	}

	// A serialização antecipada falha aqui, e não no publicador, se o payload
	// tiver um valor que o JSON não representa (ex.: canal).
	if _, err := json.Marshal(data); err != nil {
		return nil, invalid("payload do evento %s não serializa: %v", eventType, err)
	}

	return &Event{
		EventID:       eventID,
		EventType:     eventType,
		AggregateID:   aggregateID,
		CorrelationID: correlationID,
		CausationID:   causationID,
		OccurredAt:    occurredAt,
		Version:       EventVersion,
		Data:          data,
	}, nil
}

// ProcessedData é o payload de WagerTransactionProcessed.
//
// LOSS também produz este evento, mesmo sem movimentação: a rodada terminou e o
// provedor precisa saber.
type ProcessedData struct {
	TransactionID string          `json:"transactionId"`
	WalletID      wallet.WalletID `json:"walletId"`
	PlayerID      wallet.PlayerID `json:"playerId"`
	Kind          Kind            `json:"kind"`
	Status        Status          `json:"status"`
	ProviderID    string          `json:"providerId,omitempty"`
	RoundID       string          `json:"roundId"`
	GameID        string          `json:"gameId"`
	Money         money.Money     `json:"money"`
	BalanceAfter  *money.Money    `json:"balanceAfter,omitempty"`
}

// RejectedData é o payload de WagerTransactionRejected.
type RejectedData struct {
	TransactionID string          `json:"transactionId"`
	WalletID      wallet.WalletID `json:"walletId"`
	Kind          Kind            `json:"kind"`
	FailureCode   string          `json:"failureCode"`
	Money         money.Money     `json:"money"`
}

// PendingReferenceData é o payload de WagerTransactionPendingReference.
type PendingReferenceData struct {
	TransactionID          string          `json:"transactionId"`
	WalletID               wallet.WalletID `json:"walletId"`
	Kind                   Kind            `json:"kind"`
	ReferenceExternalTxnID string          `json:"referenceExternalTransactionId"`
	Attempt                int             `json:"attempt"`
	NextAttemptAt          time.Time       `json:"nextAttemptAt"`
}

// BalanceChangedData é o payload de WalletBalanceChanged.
//
// Carrega os dois saldos e a versão: quem consome consegue conferir a
// aritmética e ordenar por versão sem consultar o banco.
type BalanceChangedData struct {
	WalletID      wallet.WalletID  `json:"walletId"`
	TransactionID string           `json:"transactionId"`
	Direction     wallet.Direction `json:"direction"`
	Money         money.Money      `json:"money"`
	BalanceBefore money.Money      `json:"balanceBefore"`
	BalanceAfter  money.Money      `json:"balanceAfter"`
	WalletVersion int64            `json:"walletVersion"`
}

// ProcessedEvent monta o evento de operação concluída, com o tipo já definido.
func (t *WagerTransaction) ProcessedEvent(
	eventID, correlationID, causationID string,
	balanceAfter *money.Money,
	now time.Time,
) (*Event, error) {
	return NewEvent(eventID, EventTypeProcessed, string(t.walletID), correlationID, causationID, now, ProcessedData{
		TransactionID: string(t.id),
		WalletID:      t.walletID,
		PlayerID:      t.playerID,
		Kind:          t.kind,
		Status:        t.status,
		ProviderID:    t.providerID,
		RoundID:       t.roundID,
		GameID:        t.gameID,
		Money:         t.amount,
		BalanceAfter:  balanceAfter,
	})
}

// RejectedEvent monta o evento de rejeição, com o failureCode já registrado.
func (t *WagerTransaction) RejectedEvent(eventID, correlationID, causationID string, now time.Time) (*Event, error) {
	return NewEvent(eventID, EventTypeRejected, string(t.walletID), correlationID, causationID, now, RejectedData{
		TransactionID: string(t.id),
		WalletID:      t.walletID,
		Kind:          t.kind,
		FailureCode:   t.failureCode,
		Money:         t.amount,
	})
}

// PendingReferenceEvent monta o evento de espera por referência, com a tentativa
// e o próximo instante.
func (t *WagerTransaction) PendingReferenceEvent(eventID, correlationID, causationID string, now time.Time) (*Event, error) {
	return NewEvent(eventID, EventTypePendingReference, string(t.walletID), correlationID, causationID, now, PendingReferenceData{
		TransactionID:          string(t.id),
		WalletID:               t.walletID,
		Kind:                   t.kind,
		ReferenceExternalTxnID: t.referenceExternalTxnID,
		Attempt:                t.referenceResolutionAttempt,
		NextAttemptAt:          t.nextResolutionAttemptAt,
	})
}

// NewBalanceChangedEvent monta o evento de mudança de saldo a partir do que a
// carteira devolveu. LOSS e rejeições não geram este evento.
func NewBalanceChangedEvent(
	eventID, correlationID, causationID string,
	transactionID TransactionID,
	entry wallet.Entry,
	walletVersion int64,
	now time.Time,
) (*Event, error) {
	if entry == (wallet.Entry{}) {
		return nil, invalid("evento de saldo exige um lançamento de ledger")
	}
	if walletVersion < 1 {
		return nil, invalid("versão da carteira precisa ser >= 1, veio %d", walletVersion)
	}

	return NewEvent(eventID, EventTypeWalletBalanceChanged, string(entry.WalletID()), correlationID, causationID, now, BalanceChangedData{
		WalletID:      entry.WalletID(),
		TransactionID: string(transactionID),
		Direction:     entry.Direction(),
		Money:         entry.Amount(),
		BalanceBefore: entry.BalanceBefore(),
		BalanceAfter:  entry.BalanceAfter(),
		WalletVersion: walletVersion,
	})
}

// sumHex devolve o SHA-256 em hexadecimal.
func sumHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
