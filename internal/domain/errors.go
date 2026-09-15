package domain

import "errors"

// Erros de Domínio para Validação Monetária
var (
	ErrCurrencyMismatch   = errors.New("currency mismatch: operations require matching currencies")
	ErrInvalidMoneyFormat = errors.New("invalid money format: expected decimal string with exactly two decimal places (e.g. '25.00')")
	ErrMoneyOverflow      = errors.New("money arithmetic overflow: operation exceeds int64 bounds")
	ErrNegativeMoney      = errors.New("negative money amount is not allowed for this operation")
	ErrInvalidCurrency    = errors.New("invalid currency code: expected 3-letter uppercase ISO 4217 code")
	ErrZeroNotAllowed     = errors.New("zero amount is not allowed for this operation")
)

// Erros de Domínio para Carteira e Ledger
var (
	ErrInsufficientFunds    = errors.New("insufficient funds in wallet for operation")
	ErrWalletNotFound       = errors.New("wallet not found")
	ErrDuplicateWallet      = errors.New("wallet already exists for this player and currency")
	ErrNegativeBalance      = errors.New("wallet balance cannot be negative")
	ErrInvalidLedgerBalance = errors.New("invalid ledger balance: balanceAfter must equal balanceBefore +/- amount")
)

// Erros de Domínio para Transações e Idempotência
var (
	ErrInvalidTransactionKind      = errors.New("invalid or unsupported wager transaction kind")
	ErrInvalidStateTransition      = errors.New("invalid transaction state transition")
	ErrTransactionAlreadyTerminal  = errors.New("transaction is already in a terminal state (PROCESSED, REJECTED, or FAILED)")
	ErrReferenceNotFound           = errors.New("reference transaction not found")
	ErrReferenceNotProcessed       = errors.New("reference transaction is not processed")
	ErrReferenceMismatch           = errors.New("reference transaction does not match provider, player, wallet, currency, round or amount")
	ErrReferenceAlreadyReversed    = errors.New("reference transaction has already been reversed")
	ErrIdempotencyConflict         = errors.New("idempotency key reused with different payload")
	ErrDuplicateTransaction        = errors.New("duplicate transaction unique violation")
	ErrInvalidExternalPayload      = errors.New("invalid external payload")
	ErrOpeningNotAllowedExternally = errors.New("OPENING transaction kind is reserved for internal wallet opening")
	ErrInboxPayloadConflict        = errors.New("inbox message was received with a different payload")
)
