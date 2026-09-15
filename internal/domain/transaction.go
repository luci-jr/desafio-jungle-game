package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// TransactionKind representa a tipologia da transação financeira.
type TransactionKind string

const (
	KindOpening  TransactionKind = "OPENING"
	KindBet      TransactionKind = "BET"
	KindWin      TransactionKind = "WIN"
	KindLoss     TransactionKind = "LOSS"
	KindRefund   TransactionKind = "REFUND"
	KindRollback TransactionKind = "ROLLBACK"
)

// TransactionStatus representa os estados válidos da máquina de estados de uma transação.
type TransactionStatus string

const (
	StatusPending          TransactionStatus = "PENDING"
	StatusPendingReference TransactionStatus = "PENDING_REFERENCE"
	StatusProcessed        TransactionStatus = "PROCESSED"
	StatusRejected         TransactionStatus = "REJECTED"
	StatusFailed           TransactionStatus = "FAILED"
)

// WagerTransaction é a entidade que representa operações de apostas e reversões financeiras.
type WagerTransaction struct {
	id                             string
	providerId                     string
	externalTransactionId          string
	idempotencyKey                 string
	payloadHash                    string
	walletId                       string
	playerId                       string
	roundId                        string
	gameId                         string
	kind                           TransactionKind
	money                          Money
	referenceExternalTransactionId string
	resolvedReferenceId            string
	status                         TransactionStatus
	failureCode                    string
	balanceAfter                   Money
	isInternal                     bool
	createdAt                      time.Time
	updatedAt                      time.Time
}

// CanonicalPayloadDTO representa a estrutura normalizada para o cálculo de hash SHA-256
// dos campos de negócio, garantindo correspondência canônica entre HTTP e SQS.
type CanonicalPayloadDTO struct {
	ExternalTransactionId          string `json:"externalTransactionId"`
	GameId                         string `json:"gameId"`
	Kind                           string `json:"kind"`
	MoneyAmount                    string `json:"moneyAmount"`
	MoneyCurrency                  string `json:"moneyCurrency"`
	PlayerId                       string `json:"playerId"`
	ProviderId                     string `json:"providerId"`
	ReferenceExternalTransactionId string `json:"referenceExternalTransactionId,omitempty"`
	RoundId                        string `json:"roundId"`
	WalletId                       string `json:"walletId"`
}

// ComputeCanonicalPayloadHash gera o hash SHA-256 determinístico a partir dos campos de negócio.
func ComputeCanonicalPayloadHash(dto CanonicalPayloadDTO) (string, error) {
	bytes, err := json.Marshal(dto)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(bytes)
	return hex.EncodeToString(hash[:]), nil
}

// NewExternalTransaction cria uma transação externa proveniente de HTTP ou SQS.
func NewExternalTransaction(
	id string,
	providerId string,
	externalTransactionId string,
	idempotencyKey string,
	walletId string,
	playerId string,
	roundId string,
	gameId string,
	kind TransactionKind,
	money Money,
	referenceExternalTransactionId string,
	now time.Time,
) (*WagerTransaction, error) {
	if id == "" {
		id = uuid.NewString()
	}

	if kind == KindOpening {
		return nil, ErrOpeningNotAllowedExternally
	}

	// Validações por tipo de transação
	switch kind {
	case KindBet, KindWin:
		if !money.IsPositive() {
			return nil, ErrZeroNotAllowed
		}
	case KindLoss:
		if !money.IsZero() {
			return nil, ErrInvalidMoneyFormat
		}
	case KindRefund, KindRollback:
		if !money.IsPositive() {
			return nil, ErrZeroNotAllowed
		}
		if referenceExternalTransactionId == "" {
			return nil, ErrReferenceNotFound
		}
	default:
		return nil, ErrInvalidTransactionKind
	}

	hash, err := ComputeCanonicalPayloadHash(CanonicalPayloadDTO{
		ExternalTransactionId:          externalTransactionId,
		GameId:                         gameId,
		Kind:                           string(kind),
		MoneyAmount:                    money.AmountString(),
		MoneyCurrency:                  money.Currency(),
		PlayerId:                       playerId,
		ProviderId:                     providerId,
		ReferenceExternalTransactionId: referenceExternalTransactionId,
		RoundId:                        roundId,
		WalletId:                       walletId,
	})
	if err != nil {
		return nil, err
	}

	utcNow := now.UTC()
	return &WagerTransaction{
		id:                             id,
		providerId:                     providerId,
		externalTransactionId:          externalTransactionId,
		idempotencyKey:                 idempotencyKey,
		payloadHash:                    hash,
		walletId:                       walletId,
		playerId:                       playerId,
		roundId:                        roundId,
		gameId:                         gameId,
		kind:                           kind,
		money:                          money,
		referenceExternalTransactionId: referenceExternalTransactionId,
		status:                         StatusPending,
		isInternal:                     false,
		createdAt:                      utcNow,
		updatedAt:                      utcNow,
	}, nil
}

// NewOpeningTransaction cria a transação interna reservada para a abertura de carteira.
func NewOpeningTransaction(
	id string,
	walletId string,
	playerId string,
	money Money,
	now time.Time,
) (*WagerTransaction, error) {
	if id == "" {
		id = uuid.NewString()
	}
	utcNow := now.UTC()
	return &WagerTransaction{
		id:           id,
		walletId:     walletId,
		playerId:     playerId,
		kind:         KindOpening,
		money:        money,
		status:       StatusProcessed,
		balanceAfter: money,
		isInternal:   true,
		createdAt:    utcNow,
		updatedAt:    utcNow,
	}, nil
}

// RehydrateTransaction reconstrói a transação a partir da persistência sem reprocessar validações de entrada.
func RehydrateTransaction(
	id string,
	providerId string,
	externalTransactionId string,
	idempotencyKey string,
	payloadHash string,
	walletId string,
	playerId string,
	roundId string,
	gameId string,
	kind TransactionKind,
	money Money,
	referenceExternalTransactionId string,
	resolvedReferenceId string,
	status TransactionStatus,
	failureCode string,
	balanceAfter Money,
	isInternal bool,
	createdAt time.Time,
	updatedAt time.Time,
) *WagerTransaction {
	return &WagerTransaction{
		id:                             id,
		providerId:                     providerId,
		externalTransactionId:          externalTransactionId,
		idempotencyKey:                 idempotencyKey,
		payloadHash:                    payloadHash,
		walletId:                       walletId,
		playerId:                       playerId,
		roundId:                        roundId,
		gameId:                         gameId,
		kind:                           kind,
		money:                          money,
		referenceExternalTransactionId: referenceExternalTransactionId,
		resolvedReferenceId:            resolvedReferenceId,
		status:                         status,
		failureCode:                    failureCode,
		balanceAfter:                   balanceAfter,
		isInternal:                     isInternal,
		createdAt:                      createdAt.UTC(),
		updatedAt:                      updatedAt.UTC(),
	}
}

// IsTerminal informa se a transação atingiu um estado final imutável.
func (t *WagerTransaction) IsTerminal() bool {
	return t.status == StatusProcessed || t.status == StatusRejected || t.status == StatusFailed
}

// TransitionToProcessed avança a transação para PROCESSED com o saldo resultante da carteira.
func (t *WagerTransaction) TransitionToProcessed(balanceAfter Money, now time.Time) error {
	if t.IsTerminal() {
		return ErrTransactionAlreadyTerminal
	}
	t.status = StatusProcessed
	t.balanceAfter = balanceAfter
	t.updatedAt = now.UTC()
	return nil
}

// TransitionToRejected avança a transação para REJECTED com código de erro de negócio.
func (t *WagerTransaction) TransitionToRejected(failureCode string, now time.Time) error {
	if t.IsTerminal() {
		return ErrTransactionAlreadyTerminal
	}
	t.status = StatusRejected
	t.failureCode = failureCode
	t.updatedAt = now.UTC()
	return nil
}

// TransitionToPendingReference coloca a transação em espera da referência externa.
func (t *WagerTransaction) TransitionToPendingReference(now time.Time) error {
	if t.IsTerminal() {
		return ErrTransactionAlreadyTerminal
	}
	t.status = StatusPendingReference
	t.updatedAt = now.UTC()
	return nil
}

// TransitionToFailed encerra a transação com falha permanente de infraestrutura.
func (t *WagerTransaction) TransitionToFailed(failureCode string, now time.Time) error {
	if t.IsTerminal() {
		return ErrTransactionAlreadyTerminal
	}
	t.status = StatusFailed
	t.failureCode = failureCode
	t.updatedAt = now.UTC()
	return nil
}

// ResolveReference vincula a transação à referência interna resolvida no banco.
func (t *WagerTransaction) ResolveReference(referenceId string) {
	t.resolvedReferenceId = referenceId
}

func (t *WagerTransaction) ID() string                    { return t.id }
func (t *WagerTransaction) ProviderID() string            { return t.providerId }
func (t *WagerTransaction) ExternalTransactionID() string { return t.externalTransactionId }
func (t *WagerTransaction) IdempotencyKey() string        { return t.idempotencyKey }
func (t *WagerTransaction) PayloadHash() string           { return t.payloadHash }
func (t *WagerTransaction) WalletID() string              { return t.walletId }
func (t *WagerTransaction) PlayerID() string              { return t.playerId }
func (t *WagerTransaction) RoundID() string               { return t.roundId }
func (t *WagerTransaction) GameID() string                { return t.gameId }
func (t *WagerTransaction) Kind() TransactionKind         { return t.kind }
func (t *WagerTransaction) Money() Money                  { return t.money }
func (t *WagerTransaction) ReferenceExternalTransactionID() string {
	return t.referenceExternalTransactionId
}
func (t *WagerTransaction) ResolvedReferenceID() string { return t.resolvedReferenceId }
func (t *WagerTransaction) Status() TransactionStatus   { return t.status }
func (t *WagerTransaction) FailureCode() string         { return t.failureCode }
func (t *WagerTransaction) BalanceAfter() Money         { return t.balanceAfter }
func (t *WagerTransaction) IsInternal() bool            { return t.isInternal }
func (t *WagerTransaction) CreatedAt() time.Time        { return t.createdAt }
func (t *WagerTransaction) UpdatedAt() time.Time        { return t.updatedAt }
