package domain

import (
	"time"

	"github.com/google/uuid"
)

// LedgerDirection representa o sentido do lançamento no livro-razão financeiro.
type LedgerDirection string

const (
	DirectionDebit  LedgerDirection = "DEBIT"
	DirectionCredit LedgerDirection = "CREDIT"
)

// WalletLedgerEntry é uma entidade imutável que registra um lançamento contábil auditável.
// Garante que balanceAfter = balanceBefore ± amount.
type WalletLedgerEntry struct {
	id            string
	walletId      string
	transactionId string
	direction     LedgerDirection
	amount        Money
	balanceBefore Money
	balanceAfter  Money
	createdAt     time.Time
}

// NewLedgerEntry cria e valida um lançamento imutável do ledger, garantindo a consistência matemática.
func NewLedgerEntry(
	id string,
	walletId string,
	transactionId string,
	direction LedgerDirection,
	amount Money,
	balanceBefore Money,
	balanceAfter Money,
	now time.Time,
) (*WalletLedgerEntry, error) {
	if id == "" {
		id = uuid.NewString()
	}

	if amount.Currency() != balanceBefore.Currency() || amount.Currency() != balanceAfter.Currency() {
		return nil, ErrCurrencyMismatch
	}

	if !amount.IsPositive() {
		return nil, ErrZeroNotAllowed
	}

	if balanceAfter.IsNegative() {
		return nil, ErrNegativeBalance
	}

	// Validação estrita da equação contábil
	switch direction {
	case DirectionCredit:
		expected, err := balanceBefore.Add(amount)
		if err != nil {
			return nil, err
		}
		if !expected.Equal(balanceAfter) {
			return nil, ErrInvalidLedgerBalance
		}
	case DirectionDebit:
		expected, err := balanceBefore.Sub(amount)
		if err != nil {
			return nil, err
		}
		if !expected.Equal(balanceAfter) {
			return nil, ErrInvalidLedgerBalance
		}
	default:
		return nil, ErrInvalidLedgerBalance
	}

	return &WalletLedgerEntry{
		id:            id,
		walletId:      walletId,
		transactionId: transactionId,
		direction:     direction,
		amount:        amount,
		balanceBefore: balanceBefore,
		balanceAfter:  balanceAfter,
		createdAt:     now.UTC(),
	}, nil
}

// RehydrateLedgerEntry reconstrói o lançamento a partir do banco de dados sem executar validações de transição.
func RehydrateLedgerEntry(
	id string,
	walletId string,
	transactionId string,
	direction LedgerDirection,
	amount Money,
	balanceBefore Money,
	balanceAfter Money,
	createdAt time.Time,
) *WalletLedgerEntry {
	return &WalletLedgerEntry{
		id:            id,
		walletId:      walletId,
		transactionId: transactionId,
		direction:     direction,
		amount:        amount,
		balanceBefore: balanceBefore,
		balanceAfter:  balanceAfter,
		createdAt:     createdAt.UTC(),
	}
}

func (l *WalletLedgerEntry) ID() string                 { return l.id }
func (l *WalletLedgerEntry) WalletID() string           { return l.walletId }
func (l *WalletLedgerEntry) TransactionID() string      { return l.transactionId }
func (l *WalletLedgerEntry) Direction() LedgerDirection { return l.direction }
func (l *WalletLedgerEntry) Amount() Money              { return l.amount }
func (l *WalletLedgerEntry) BalanceBefore() Money       { return l.balanceBefore }
func (l *WalletLedgerEntry) BalanceAfter() Money        { return l.balanceAfter }
func (l *WalletLedgerEntry) CreatedAt() time.Time       { return l.createdAt }
