package domain_test

import (
	"encoding/json"
	"math"
	"testing"

	"backend-challenge-go/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMoney_NewMoneyFromDecimal_Success(t *testing.T) {
	tests := []struct {
		input       string
		currency    string
		expectCents int64
		expectStr   string
	}{
		{"25.00", "BRL", 2500, "25.00"},
		{"0.00", "BRL", 0, "0.00"},
		{"0.05", "BRL", 5, "0.05"},
		{"0.50", "BRL", 50, "0.50"},
		{"1000.00", "USD", 100000, "1000.00"},
		{"-25.00", "BRL", -2500, "-25.00"},
		{"-0.05", "BRL", -5, "-0.05"},
		{"+15.75", "BRL", 1575, "15.75"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			m, err := domain.NewMoneyFromDecimal(tt.input, tt.currency)
			require.NoError(t, err)
			assert.Equal(t, tt.expectCents, m.Cents())
			assert.Equal(t, tt.currency, m.Currency())
			assert.Equal(t, tt.expectStr, m.AmountString())
		})
	}
}

func TestMoney_NewMoneyFromDecimal_InvalidFormats(t *testing.T) {
	invalidCases := []string{
		"", "   ", "25", "25.0", "25.000", "25.00.00",
		"abc", "NaN", "Infinity", "1e5", "25.a0", "--25.00", "++25.00",
	}

	for _, input := range invalidCases {
		t.Run("invalid_"+input, func(t *testing.T) {
			_, err := domain.NewMoneyFromDecimal(input, "BRL")
			assert.ErrorIs(t, err, domain.ErrInvalidMoneyFormat)
		})
	}
}

func TestMoney_InvalidCurrency(t *testing.T) {
	invalidCurrencies := []string{"", "B", "BR", "BRLA", "123", "brl", "US1"}
	for _, curr := range invalidCurrencies {
		t.Run("curr_"+curr, func(t *testing.T) {
			_, err := domain.NewMoney(100, curr)
			assert.ErrorIs(t, err, domain.ErrInvalidCurrency)

			_, err = domain.NewMoneyFromDecimal("10.00", curr)
			assert.ErrorIs(t, err, domain.ErrInvalidCurrency)
		})
	}
}

func TestMoney_Arithmetic_Success(t *testing.T) {
	m1, err := domain.NewMoneyFromDecimal("25.00", "BRL")
	require.NoError(t, err)

	m2, err := domain.NewMoneyFromDecimal("75.00", "BRL")
	require.NoError(t, err)

	sum, err := m1.Add(m2)
	require.NoError(t, err)
	assert.Equal(t, int64(10000), sum.Cents())
	assert.Equal(t, "100.00", sum.AmountString())

	diff, err := m2.Sub(m1)
	require.NoError(t, err)
	assert.Equal(t, int64(5000), diff.Cents())
	assert.Equal(t, "50.00", diff.AmountString())

	neg, err := m1.Negate()
	require.NoError(t, err)
	assert.Equal(t, int64(-2500), neg.Cents())
	assert.Equal(t, "-25.00", neg.AmountString())
}

func TestMoney_CurrencyMismatch(t *testing.T) {
	brl, err := domain.NewMoneyFromDecimal("10.00", "BRL")
	require.NoError(t, err)

	usd, err := domain.NewMoneyFromDecimal("10.00", "USD")
	require.NoError(t, err)

	_, err = brl.Add(usd)
	assert.ErrorIs(t, err, domain.ErrCurrencyMismatch)

	_, err = brl.Sub(usd)
	assert.ErrorIs(t, err, domain.ErrCurrencyMismatch)

	_, err = brl.GreaterThan(usd)
	assert.ErrorIs(t, err, domain.ErrCurrencyMismatch)

	_, err = brl.LessThan(usd)
	assert.ErrorIs(t, err, domain.ErrCurrencyMismatch)
}

func TestMoney_Overflow(t *testing.T) {
	maxM, err := domain.NewMoney(math.MaxInt64, "BRL")
	require.NoError(t, err)

	oneCent, err := domain.NewMoney(1, "BRL")
	require.NoError(t, err)

	_, err = maxM.Add(oneCent)
	assert.ErrorIs(t, err, domain.ErrMoneyOverflow)

	minM, err := domain.NewMoney(math.MinInt64, "BRL")
	require.NoError(t, err)

	_, err = minM.Sub(oneCent)
	assert.ErrorIs(t, err, domain.ErrMoneyOverflow)

	_, err = minM.Negate()
	assert.ErrorIs(t, err, domain.ErrMoneyOverflow)
}

// TestMoney_AmountString_MinInt64 garante que o menor int64 seja formatado
// sem overflow durante a conversão para valor absoluto.
func TestMoney_AmountString_MinInt64(t *testing.T) {
	money, err := domain.NewMoney(math.MinInt64, "BRL")
	require.NoError(t, err)

	assert.Equal(t, "-92233720368547758.08", money.AmountString())
}

func TestMoney_JSON_Contract(t *testing.T) {
	m, err := domain.NewMoneyFromDecimal("975.00", "BRL")
	require.NoError(t, err)

	data, err := json.Marshal(m)
	require.NoError(t, err)
	assert.JSONEq(t, `{"amount":"975.00","currency":"BRL"}`, string(data))

	var deserialized domain.Money
	err = json.Unmarshal(data, &deserialized)
	require.NoError(t, err)
	assert.True(t, m.Equal(deserialized))
}
