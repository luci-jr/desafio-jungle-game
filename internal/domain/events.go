package domain

import (
	"time"

	"github.com/google/uuid"
)

// EventType enumera os tipos oficiais de eventos de domínio exigidos pelo desafio.
const (
	EventTypeWagerTransactionProcessed        = "WagerTransactionProcessed"
	EventTypeWagerTransactionRejected         = "WagerTransactionRejected"
	EventTypeWalletBalanceChanged             = "WalletBalanceChanged"
	EventTypeWagerTransactionPendingReference = "WagerTransactionPendingReference"
)

// EventEnvelope padroniza o invólucro de publicação para a Transactional Outbox.
type EventEnvelope struct {
	EventID       string      `json:"eventId"`
	EventType     string      `json:"eventType"`
	AggregateID   string      `json:"aggregateId"`
	CorrelationID string      `json:"correlationId"`
	CausationID   string      `json:"causationId,omitempty"`
	OccurredAt    string      `json:"occurredAt"` // UTC RFC 3339
	Version       int         `json:"version"`
	Data          interface{} `json:"data"`
}

// NewEventEnvelope cria um envelope com timestamp UTC em RFC 3339 e ID único.
func NewEventEnvelope(
	eventType string,
	aggregateId string,
	correlationId string,
	causationId string,
	now time.Time,
	data interface{},
) EventEnvelope {
	return EventEnvelope{
		EventID:       uuid.NewString(),
		EventType:     eventType,
		AggregateID:   aggregateId,
		CorrelationID: correlationId,
		CausationID:   causationId,
		OccurredAt:    now.UTC().Format(time.RFC3339Nano),
		Version:       1,
		Data:          data,
	}
}

// PayloadWagerTransactionProcessed representa os dados do evento de sucesso.
type PayloadWagerTransactionProcessed struct {
	TransactionID         string `json:"transactionId"`
	ProviderID            string `json:"providerId"`
	ExternalTransactionID string `json:"externalTransactionId"`
	WalletID              string `json:"walletId"`
	PlayerID              string `json:"playerId"`
	Kind                  string `json:"kind"`
	Amount                string `json:"amount"`
	Currency              string `json:"currency"`
	BalanceAfter          string `json:"balanceAfter"`
}

// PayloadWagerTransactionRejected representa os dados do evento de rejeição de negócio.
type PayloadWagerTransactionRejected struct {
	TransactionID         string `json:"transactionId"`
	ProviderID            string `json:"providerId"`
	ExternalTransactionID string `json:"externalTransactionId"`
	WalletID              string `json:"walletId"`
	PlayerID              string `json:"playerId"`
	Kind                  string `json:"kind"`
	Amount                string `json:"amount"`
	Currency              string `json:"currency"`
	FailureCode           string `json:"failureCode"`
}

// PayloadWalletBalanceChanged representa os dados da mutação efetiva de saldo na carteira.
type PayloadWalletBalanceChanged struct {
	WalletID      string `json:"walletId"`
	TransactionID string `json:"transactionId"`
	Direction     string `json:"direction"`
	Amount        string `json:"amount"`
	Currency      string `json:"currency"`
	BalanceBefore string `json:"balanceBefore"`
	BalanceAfter  string `json:"balanceAfter"`
	WalletVersion int64  `json:"walletVersion"`
}

// PayloadWagerTransactionPendingReference representa a notificação de transação aguardando referência.
type PayloadWagerTransactionPendingReference struct {
	TransactionID                  string `json:"transactionId"`
	ProviderID                     string `json:"providerId"`
	ExternalTransactionID          string `json:"externalTransactionId"`
	WalletID                       string `json:"walletId"`
	ReferenceExternalTransactionID string `json:"referenceExternalTransactionId"`
}
