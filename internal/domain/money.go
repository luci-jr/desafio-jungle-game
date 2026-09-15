package domain

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// Money é um Value Object imutável que representa valores monetários em unidades mínimas (centavos).
// Garante precisão exata com int64, sem jamais utilizar float32 ou float64 em nenhuma etapa.
type Money struct {
	cents    int64
	currency string
}

// NewMoney cria uma instância de Money a partir de centavos e código de moeda ISO 4217.
func NewMoney(cents int64, currency string) (Money, error) {
	currency = strings.TrimSpace(currency)
	if !isValidCurrency(currency) {
		return Money{}, ErrInvalidCurrency
	}
	return Money{cents: cents, currency: currency}, nil
}

// ZeroMoney retorna um Money com valor zero para a moeda especificada.
func ZeroMoney(currency string) (Money, error) {
	return NewMoney(0, currency)
}

// NewMoneyFromDecimal faz o parsing de uma string decimal (ex: "25.00", "0.00", "-15.50")
// sem passar por float32/float64, garantindo escala fixa de 2 casas decimais e checagem de overflow.
func NewMoneyFromDecimal(amountStr string, currency string) (Money, error) {
	currency = strings.TrimSpace(currency)
	if !isValidCurrency(currency) {
		return Money{}, ErrInvalidCurrency
	}

	amountStr = strings.TrimSpace(amountStr)
	if amountStr == "" {
		return Money{}, ErrInvalidMoneyFormat
	}

	// Rejeitar valores com notação científica ou strings especiais
	upper := strings.ToUpper(amountStr)
	if strings.ContainsAny(upper, "ENAI") || upper == "NAN" || upper == "INFINITY" {
		return Money{}, ErrInvalidMoneyFormat
	}

	// Tratamento de sinal
	isNegative := false
	if strings.HasPrefix(amountStr, "-") {
		isNegative = true
		amountStr = amountStr[1:]
	} else if strings.HasPrefix(amountStr, "+") {
		amountStr = amountStr[1:]
	}

	if amountStr == "" {
		return Money{}, ErrInvalidMoneyFormat
	}

	// O contrato exige exatamente 2 casas decimais após o ponto
	parts := strings.Split(amountStr, ".")
	if len(parts) != 2 {
		return Money{}, ErrInvalidMoneyFormat
	}

	intPartStr, fracPartStr := parts[0], parts[1]
	if len(intPartStr) == 0 || len(fracPartStr) != 2 {
		return Money{}, ErrInvalidMoneyFormat
	}

	// Parsing da parte inteira com checagem de overflow em int64
	var intPart int64
	for _, ch := range intPartStr {
		if ch < '0' || ch > '9' {
			return Money{}, ErrInvalidMoneyFormat
		}
		digit := int64(ch - '0')
		if intPart > (math.MaxInt64-digit)/10 {
			return Money{}, ErrMoneyOverflow
		}
		intPart = intPart*10 + digit
	}

	// Multiplicação por 100 com checagem de overflow
	if intPart > math.MaxInt64/100 {
		return Money{}, ErrMoneyOverflow
	}
	cents := intPart * 100

	// Parsing da parte fracionária (2 dígitos)
	if fracPartStr[0] < '0' || fracPartStr[0] > '9' ||
		fracPartStr[1] < '0' || fracPartStr[1] > '9' {
		return Money{}, ErrInvalidMoneyFormat
	}
	fracPart := int64(fracPartStr[0]-'0')*10 + int64(fracPartStr[1]-'0')

	if cents > math.MaxInt64-fracPart {
		return Money{}, ErrMoneyOverflow
	}
	cents += fracPart

	if isNegative {
		cents = -cents
	}

	return Money{cents: cents, currency: currency}, nil
}

// NewPositiveMoneyFromDecimal faz o parsing e valida que o valor monetário é estritamente maior que zero.
func NewPositiveMoneyFromDecimal(amountStr string, currency string) (Money, error) {
	m, err := NewMoneyFromDecimal(amountStr, currency)
	if err != nil {
		return Money{}, err
	}
	if m.cents <= 0 {
		return Money{}, ErrNegativeMoney
	}
	return m, nil
}

// Cents retorna a quantidade interna em centavos (unidades mínimas).
func (m Money) Cents() int64 {
	return m.cents
}

// Currency retorna o código ISO 4217 da moeda.
func (m Money) Currency() string {
	return m.currency
}

// AmountString formata o valor como string decimal com 2 casas (ex: "25.00", "-5.50").
func (m Money) AmountString() string {
	// O valor absoluto de math.MinInt64 não cabe em um int64 positivo.
	// O caso é retornado diretamente para evitar overflow e resto negativo.
	if m.cents == math.MinInt64 {
		return "-92233720368547758.08"
	}

	absCents := m.cents
	sign := ""
	if absCents < 0 {
		sign = "-"
		absCents = -absCents
	}
	units := absCents / 100
	remainder := absCents % 100
	return fmt.Sprintf("%s%d.%02d", sign, units, remainder)
}

// Add soma dois valores monetários da mesma moeda, protegendo contra overflow.
func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, ErrCurrencyMismatch
	}

	a, b := m.cents, other.cents
	if b > 0 && a > math.MaxInt64-b {
		return Money{}, ErrMoneyOverflow
	}
	if b < 0 && a < math.MinInt64-b {
		return Money{}, ErrMoneyOverflow
	}

	return Money{cents: a + b, currency: m.currency}, nil
}

// Sub subtrai outro valor monetário da mesma moeda, protegendo contra overflow.
func (m Money) Sub(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, ErrCurrencyMismatch
	}

	a, b := m.cents, other.cents
	if b < 0 && a > math.MaxInt64+b {
		return Money{}, ErrMoneyOverflow
	}
	if b > 0 && a < math.MinInt64+b {
		return Money{}, ErrMoneyOverflow
	}

	return Money{cents: a - b, currency: m.currency}, nil
}

// Negate inverte o sinal do valor monetário, protegendo contra overflow em math.MinInt64.
func (m Money) Negate() (Money, error) {
	if m.cents == math.MinInt64 {
		return Money{}, ErrMoneyOverflow
	}
	return Money{cents: -m.cents, currency: m.currency}, nil
}

// Equal verifica se dois valores são idênticos em valor e moeda.
func (m Money) Equal(other Money) bool {
	return m.currency == other.currency && m.cents == other.cents
}

// GreaterThan verifica se o valor é maior que o outro.
func (m Money) GreaterThan(other Money) (bool, error) {
	if m.currency != other.currency {
		return false, ErrCurrencyMismatch
	}
	return m.cents > other.cents, nil
}

// LessThan verifica se o valor é menor que o outro.
func (m Money) LessThan(other Money) (bool, error) {
	if m.currency != other.currency {
		return false, ErrCurrencyMismatch
	}
	return m.cents < other.cents, nil
}

// IsZero retorna verdadeiro se o valor for exatamente 0.00.
func (m Money) IsZero() bool {
	return m.cents == 0
}

// IsPositive retorna verdadeiro se o valor for estritamente maior que zero.
func (m Money) IsPositive() bool {
	return m.cents > 0
}

// IsNegative retorna verdadeiro se o valor for negativo.
func (m Money) IsNegative() bool {
	return m.cents < 0
}

// DTO interno para serialização JSON conforme o contrato da API: {"amount":"25.00","currency":"BRL"}
type moneyJSON struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// MarshalJSON serializa Money no formato exato da API sem conversão para float.
func (m Money) MarshalJSON() ([]byte, error) {
	return json.Marshal(moneyJSON{
		Amount:   m.AmountString(),
		Currency: m.currency,
	})
}

// UnmarshalJSON desserializa Money a partir do JSON {"amount":"25.00","currency":"BRL"}.
func (m *Money) UnmarshalJSON(data []byte) error {
	var dto moneyJSON
	if err := json.Unmarshal(data, &dto); err != nil {
		return err
	}
	parsed, err := NewMoneyFromDecimal(dto.Amount, dto.Currency)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

// Valida se o código de moeda é ISO 4217 (3 letras maiúsculas A-Z)
func isValidCurrency(currency string) bool {
	if len(currency) != 3 {
		return false
	}
	for _, ch := range currency {
		if ch < 'A' || ch > 'Z' {
			return false
		}
	}
	return true
}
