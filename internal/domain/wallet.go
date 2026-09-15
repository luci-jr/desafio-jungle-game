package domain

import (
	"time"

	"github.com/google/uuid"
)

// Wallet é a raiz do agregado financeiro (Aggregate Root).
// Controla saldo, versão e mutações garantindo não negatividade.
type Wallet struct {
	id        string
	playerId  string
	currency  string
	balance   Money
	version   int64
	createdAt time.Time
	updatedAt time.Time
}

// NewWallet cria uma nova carteira para um jogador em uma moeda específica.
// Se initialBalance for positivo, gera o lançamento contábil inicial de crédito (OPENING).
// Se for zero, não gera lançamento contábil inicial.
func NewWallet(
	id string,
	playerId string,
	currency string,
	initialBalance Money,
	now time.Time,
) (*Wallet, *WalletLedgerEntry, error) {
	if id == "" {
		id = uuid.NewString()
	}

	if initialBalance.Currency() != currency {
		return nil, nil, ErrCurrencyMismatch
	}

	if initialBalance.IsNegative() {
		return nil, nil, ErrNegativeBalance
	}

	utcNow := now.UTC()
	w := &Wallet{
		id:        id,
		playerId:  playerId,
		currency:  currency,
		balance:   initialBalance,
		version:   1,
		createdAt: utcNow,
		updatedAt: utcNow,
	}

	var entry *WalletLedgerEntry
	if initialBalance.IsPositive() {
		zero, err := ZeroMoney(currency)
		if err != nil {
			return nil, nil, err
		}
		// A abertura inicial de saldo usa o ID da própria carteira ou UUID gerado para a transação interna OPENING
		entry, err = NewLedgerEntry(
			uuid.NewString(),
			id,
			id, // vinculada à transação de abertura
			DirectionCredit,
			initialBalance,
			zero,
			initialBalance,
			utcNow,
		)
		if err != nil {
			return nil, nil, err
		}
	}

	return w, entry, nil
}

// RehydrateWallet reconstrói o agregado Wallet a partir do banco de dados sem executar validações ou eventos.
func RehydrateWallet(
	id string,
	playerId string,
	currency string,
	balance Money,
	version int64,
	createdAt time.Time,
	updatedAt time.Time,
) *Wallet {
	return &Wallet{
		id:        id,
		playerId:  playerId,
		currency:  currency,
		balance:   balance,
		version:   version,
		createdAt: createdAt.UTC(),
		updatedAt: updatedAt.UTC(),
	}
}

// Debit debita um valor da carteira, validando suficiência de saldo e moedas compatíveis.
// Retorna o lançamento contábil correspondente do ledger.
func (w *Wallet) Debit(amount Money, transactionId string, now time.Time) (*WalletLedgerEntry, error) {
	if amount.Currency() != w.currency {
		return nil, ErrCurrencyMismatch
	}

	if !amount.IsPositive() {
		return nil, ErrZeroNotAllowed
	}

	less, err := w.balance.LessThan(amount)
	if err != nil {
		return nil, err
	}
	if less {
		return nil, ErrInsufficientFunds
	}

	balanceBefore := w.balance
	newBalance, err := w.balance.Sub(amount)
	if err != nil {
		return nil, err
	}

	w.balance = newBalance
	w.version++
	w.updatedAt = now.UTC()

	return NewLedgerEntry(
		uuid.NewString(),
		w.id,
		transactionId,
		DirectionDebit,
		amount,
		balanceBefore,
		newBalance,
		w.updatedAt,
	)
}

// Credit credita um valor na carteira e incrementa a versão.
// Retorna o lançamento contábil correspondente do ledger.
func (w *Wallet) Credit(amount Money, transactionId string, now time.Time) (*WalletLedgerEntry, error) {
	if amount.Currency() != w.currency {
		return nil, ErrCurrencyMismatch
	}

	if !amount.IsPositive() {
		return nil, ErrZeroNotAllowed
	}

	balanceBefore := w.balance
	newBalance, err := w.balance.Add(amount)
	if err != nil {
		return nil, err
	}

	w.balance = newBalance
	w.version++
	w.updatedAt = now.UTC()

	return NewLedgerEntry(
		uuid.NewString(),
		w.id,
		transactionId,
		DirectionCredit,
		amount,
		balanceBefore,
		newBalance,
		w.updatedAt,
	)
}

func (w *Wallet) ID() string           { return w.id }
func (w *Wallet) PlayerID() string     { return w.playerId }
func (w *Wallet) Currency() string     { return w.currency }
func (w *Wallet) Balance() Money       { return w.balance }
func (w *Wallet) Version() int64       { return w.version }
func (w *Wallet) CreatedAt() time.Time { return w.createdAt }
func (w *Wallet) UpdatedAt() time.Time { return w.updatedAt }
