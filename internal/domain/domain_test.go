package domain_test

import (
	"encoding/json"
	"testing"
	"time"

	"backend-challenge-go/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWallet_NewWallet(t *testing.T) {
	now := time.Now().UTC()

	t.Run("success with initial positive balance generates ledger entry", func(t *testing.T) {
		initial, err := domain.NewMoneyFromDecimal("1000.00", "BRL")
		require.NoError(t, err)

		wallet, entry, err := domain.NewWallet("w1", "p1", "BRL", initial, now)
		require.NoError(t, err)
		require.NotNil(t, wallet)
		require.NotNil(t, entry)

		assert.Equal(t, "w1", wallet.ID())
		assert.Equal(t, "p1", wallet.PlayerID())
		assert.Equal(t, "BRL", wallet.Currency())
		assert.Equal(t, int64(1), wallet.Version())
		assert.True(t, wallet.Balance().Equal(initial))

		assert.Equal(t, domain.DirectionCredit, entry.Direction())
		assert.Equal(t, "0.00", entry.BalanceBefore().AmountString())
		assert.Equal(t, "1000.00", entry.BalanceAfter().AmountString())
	})

	t.Run("success with zero initial balance does not generate ledger entry", func(t *testing.T) {
		zero, err := domain.ZeroMoney("BRL")
		require.NoError(t, err)

		wallet, entry, err := domain.NewWallet("w2", "p2", "BRL", zero, now)
		require.NoError(t, err)
		require.NotNil(t, wallet)
		assert.Nil(t, entry)
		assert.Equal(t, int64(1), wallet.Version())
	})

	t.Run("rejects currency mismatch", func(t *testing.T) {
		initial, err := domain.NewMoneyFromDecimal("100.00", "USD")
		require.NoError(t, err)

		_, _, err = domain.NewWallet("w3", "p3", "BRL", initial, now)
		assert.ErrorIs(t, err, domain.ErrCurrencyMismatch)
	})

	t.Run("rejects negative initial balance", func(t *testing.T) {
		neg, err := domain.NewMoneyFromDecimal("-10.00", "BRL")
		require.NoError(t, err)

		_, _, err = domain.NewWallet("w4", "p4", "BRL", neg, now)
		assert.ErrorIs(t, err, domain.ErrNegativeBalance)
	})
}

func TestWallet_DebitAndCredit(t *testing.T) {
	now := time.Now().UTC()
	initial, err := domain.NewMoneyFromDecimal("100.00", "BRL")
	require.NoError(t, err)

	wallet, _, err := domain.NewWallet("w1", "p1", "BRL", initial, now)
	require.NoError(t, err)

	t.Run("debit success", func(t *testing.T) {
		bet, err := domain.NewMoneyFromDecimal("25.00", "BRL")
		require.NoError(t, err)

		entry, err := wallet.Debit(bet, "tx-1", now)
		require.NoError(t, err)
		require.NotNil(t, entry)

		assert.Equal(t, int64(2), wallet.Version())
		assert.Equal(t, "75.00", wallet.Balance().AmountString())
		assert.Equal(t, "100.00", entry.BalanceBefore().AmountString())
		assert.Equal(t, "75.00", entry.BalanceAfter().AmountString())
		assert.Equal(t, domain.DirectionDebit, entry.Direction())
	})

	t.Run("debit insufficient funds", func(t *testing.T) {
		betBig, err := domain.NewMoneyFromDecimal("80.00", "BRL")
		require.NoError(t, err)

		entry, err := wallet.Debit(betBig, "tx-2", now)
		assert.ErrorIs(t, err, domain.ErrInsufficientFunds)
		assert.Nil(t, entry)
		// Versão e saldo não mudam
		assert.Equal(t, int64(2), wallet.Version())
		assert.Equal(t, "75.00", wallet.Balance().AmountString())
	})

	t.Run("credit success", func(t *testing.T) {
		win, err := domain.NewMoneyFromDecimal("50.00", "BRL")
		require.NoError(t, err)

		entry, err := wallet.Credit(win, "tx-3", now)
		require.NoError(t, err)
		require.NotNil(t, entry)

		assert.Equal(t, int64(3), wallet.Version())
		assert.Equal(t, "125.00", wallet.Balance().AmountString())
		assert.Equal(t, "75.00", entry.BalanceBefore().AmountString())
		assert.Equal(t, "125.00", entry.BalanceAfter().AmountString())
		assert.Equal(t, domain.DirectionCredit, entry.Direction())
	})
}

func TestLedger_MathematicalInvariant(t *testing.T) {
	now := time.Now().UTC()
	bBefore, err := domain.NewMoneyFromDecimal("100.00", "BRL")
	require.NoError(t, err)
	amt, err := domain.NewMoneyFromDecimal("25.00", "BRL")
	require.NoError(t, err)

	t.Run("rejects false debit equation", func(t *testing.T) {
		wrongAfter, err := domain.NewMoneyFromDecimal("80.00", "BRL") // deveria ser 75.00
		require.NoError(t, err)

		_, err = domain.NewLedgerEntry("l1", "w1", "t1", domain.DirectionDebit, amt, bBefore, wrongAfter, now)
		assert.ErrorIs(t, err, domain.ErrInvalidLedgerBalance)
	})

	t.Run("rejects false credit equation", func(t *testing.T) {
		wrongAfter, err := domain.NewMoneyFromDecimal("130.00", "BRL") // deveria ser 125.00
		require.NoError(t, err)

		_, err = domain.NewLedgerEntry("l2", "w1", "t2", domain.DirectionCredit, amt, bBefore, wrongAfter, now)
		assert.ErrorIs(t, err, domain.ErrInvalidLedgerBalance)
	})
}

func TestWagerTransaction_StateMachine(t *testing.T) {
	now := time.Now().UTC()
	betMoney, err := domain.NewMoneyFromDecimal("25.00", "BRL")
	require.NoError(t, err)

	tx, err := domain.NewExternalTransaction(
		"tx-1",
		"provider-a",
		"ext-1",
		"provider-a:ext-1",
		"w1",
		"p1",
		"round-1",
		"game-1",
		domain.KindBet,
		betMoney,
		"",
		now,
	)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusPending, tx.Status())
	assert.NotEmpty(t, tx.PayloadHash())

	t.Run("transition to processed is valid", func(t *testing.T) {
		balanceAfter, err := domain.NewMoneyFromDecimal("975.00", "BRL")
		require.NoError(t, err)

		err = tx.TransitionToProcessed(balanceAfter, now)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusProcessed, tx.Status())
		assert.True(t, tx.IsTerminal())
	})

	t.Run("terminal transaction rejects subsequent transition", func(t *testing.T) {
		err = tx.TransitionToRejected("INSUFFICIENT_FUNDS", now)
		assert.ErrorIs(t, err, domain.ErrTransactionAlreadyTerminal)

		err = tx.TransitionToPendingReference(now)
		assert.ErrorIs(t, err, domain.ErrTransactionAlreadyTerminal)
	})
}

func TestWagerTransaction_CanonicalHashDeterminism(t *testing.T) {
	dto1 := domain.CanonicalPayloadDTO{
		ExternalTransactionId: "ext-100",
		GameId:                "game-a",
		Kind:                  "BET",
		MoneyAmount:           "50.00",
		MoneyCurrency:         "BRL",
		PlayerId:              "player-1",
		ProviderId:            "provider-1",
		RoundId:               "round-99",
		WalletId:              "wallet-1",
	}

	dto2 := dto1 // cópia idêntica

	hash1, err := domain.ComputeCanonicalPayloadHash(dto1)
	require.NoError(t, err)
	hash2, err := domain.ComputeCanonicalPayloadHash(dto2)
	require.NoError(t, err)

	assert.Equal(t, hash1, hash2, "hashes devem ser rigorosamente determinísticos")

	// Modificação no payload altera o hash
	dto2.MoneyAmount = "50.01"
	hash3, err := domain.ComputeCanonicalPayloadHash(dto2)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hash3, "mudança de valor deve produzir hash diferente para detecção de conflito")
}

func TestOutbox_EventEnvelope(t *testing.T) {
	now := time.Now().UTC()
	payload := domain.PayloadWagerTransactionProcessed{
		TransactionID:         "tx-1",
		ProviderID:            "provider-a",
		ExternalTransactionID: "ext-1",
		WalletID:              "w1",
		PlayerID:              "p1",
		Kind:                  "BET",
		Amount:                "25.00",
		Currency:              "BRL",
		BalanceAfter:          "975.00",
	}

	envelope := domain.NewEventEnvelope(
		domain.EventTypeWagerTransactionProcessed,
		"w1",
		"corr-1",
		"caus-1",
		now,
		payload,
	)

	bytes, err := json.Marshal(envelope)
	require.NoError(t, err)
	assert.Contains(t, string(bytes), `"eventType":"WagerTransactionProcessed"`)
	assert.Contains(t, string(bytes), `"aggregateId":"w1"`)
	assert.Contains(t, string(bytes), `"balanceAfter":"975.00"`)
}
