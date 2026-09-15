package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"backend-challenge-go/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository gerencia o acesso aos dados e transações no PostgreSQL via pgx/v5.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository cria uma nova instância do repositório.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// BeginTx inicia uma transação com isolamento Read Committed padrão.
func (r *Repository) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return r.pool.Begin(ctx)
}

// =========================================================================
// CARTEIRAS (Wallets)
// =========================================================================

// CreateWallet insere uma nova carteira na transação corrente.
func (r *Repository) CreateWallet(ctx context.Context, tx pgx.Tx, w *domain.Wallet) error {
	query := `
		INSERT INTO wallets (id, player_id, currency, balance, version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := tx.Exec(
		ctx, query,
		w.ID(), w.PlayerID(), w.Currency(), w.Balance().Cents(), w.Version(), w.CreatedAt(), w.UpdatedAt(),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return domain.ErrDuplicateWallet
		}
		return fmt.Errorf("falha ao inserir carteira: %w", err)
	}
	return nil
}

// GetWallet busca uma carteira sem lock (para consultas somente leitura).
func (r *Repository) GetWallet(ctx context.Context, walletId string) (*domain.Wallet, error) {
	query := `
		SELECT id, player_id, currency, balance, version, created_at, updated_at
		FROM wallets WHERE id = $1
	`
	var (
		id        string
		playerId  string
		currency  string
		balance   int64
		version   int64
		createdAt time.Time
		updatedAt time.Time
	)

	err := r.pool.QueryRow(ctx, query, walletId).Scan(
		&id, &playerId, &currency, &balance, &version, &createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrWalletNotFound
		}
		return nil, fmt.Errorf("falha ao buscar carteira: %w", err)
	}

	money, err := domain.NewMoney(balance, currency)
	if err != nil {
		return nil, err
	}

	return domain.RehydrateWallet(id, playerId, currency, money, version, createdAt, updatedAt), nil
}

// GetWalletForUpdate busca a carteira aplicando SELECT ... FOR UPDATE (Lock Exclusivo por Carteira).
// Impede Lost Updates e garante serialização de operações concorrentes sobre a mesma carteira.
func (r *Repository) GetWalletForUpdate(ctx context.Context, tx pgx.Tx, walletId string) (*domain.Wallet, error) {
	query := `
		SELECT id, player_id, currency, balance, version, created_at, updated_at
		FROM wallets WHERE id = $1 FOR UPDATE
	`
	var (
		id        string
		playerId  string
		currency  string
		balance   int64
		version   int64
		createdAt time.Time
		updatedAt time.Time
	)

	err := tx.QueryRow(ctx, query, walletId).Scan(
		&id, &playerId, &currency, &balance, &version, &createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrWalletNotFound
		}
		return nil, fmt.Errorf("falha ao buscar carteira com lock FOR UPDATE: %w", err)
	}

	money, err := domain.NewMoney(balance, currency)
	if err != nil {
		return nil, err
	}

	return domain.RehydrateWallet(id, playerId, currency, money, version, createdAt, updatedAt), nil
}

// UpdateWallet atualiza o saldo e versão da carteira na transação corrente.
func (r *Repository) UpdateWallet(ctx context.Context, tx pgx.Tx, w *domain.Wallet) error {
	query := `
		UPDATE wallets
		SET balance = $1, version = $2, updated_at = $3
		WHERE id = $4
	`
	cmdTag, err := tx.Exec(ctx, query, w.Balance().Cents(), w.Version(), w.UpdatedAt(), w.ID())
	if err != nil {
		return fmt.Errorf("falha ao atualizar carteira: %w", err)
	}
	if cmdTag.RowsAffected() == 0 {
		return domain.ErrWalletNotFound
	}
	return nil
}

// =========================================================================
// LIVRO-RAZÃO (Wallet Ledger Entries)
// =========================================================================

// CreateLedgerEntry insere um lançamento contábil auditável e imutável.
func (r *Repository) CreateLedgerEntry(ctx context.Context, tx pgx.Tx, entry *domain.WalletLedgerEntry) error {
	query := `
		INSERT INTO wallet_ledger_entries (
			id, wallet_id, transaction_id, direction, amount, balance_before, balance_after, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	_, err := tx.Exec(
		ctx, query,
		entry.ID(), entry.WalletID(), entry.TransactionID(), string(entry.Direction()),
		entry.Amount().Cents(), entry.BalanceBefore().Cents(), entry.BalanceAfter().Cents(), entry.CreatedAt(),
	)
	if err != nil {
		return fmt.Errorf("falha ao gravar lançamento do ledger: %w", err)
	}
	return nil
}

// LedgerItemDTO é o registro retornado na consulta paginada do ledger.
type LedgerItemDTO struct {
	ID            string    `json:"id"`
	WalletID      string    `json:"walletId"`
	TransactionID string    `json:"transactionId"`
	Direction     string    `json:"direction"`
	Amount        string    `json:"amount"`
	Currency      string    `json:"currency"`
	BalanceBefore string    `json:"balanceBefore"`
	BalanceAfter  string    `json:"balanceAfter"`
	CreatedAt     time.Time `json:"createdAt"`
}

// GetLedgerEntries consulta lançamentos com paginação estável por cursor (timestamp/id).
func (r *Repository) GetLedgerEntries(ctx context.Context, walletId string, cursor string, limit int) ([]LedgerItemDTO, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var (
		query string
		rows  pgx.Rows
		err   error
	)

	if cursor != "" {
		query = `
			SELECT l.id, l.wallet_id, l.transaction_id, l.direction, l.amount, l.balance_before, l.balance_after, l.created_at, w.currency
			FROM wallet_ledger_entries l
			JOIN wallets w ON w.id = l.wallet_id
			WHERE l.wallet_id = $1
			  AND l.created_at < (SELECT created_at FROM wallet_ledger_entries WHERE id = $2)
			ORDER BY l.created_at DESC, l.id DESC
			LIMIT $3
		`
		rows, err = r.pool.Query(ctx, query, walletId, cursor, limit)
	} else {
		query = `
			SELECT l.id, l.wallet_id, l.transaction_id, l.direction, l.amount, l.balance_before, l.balance_after, l.created_at, w.currency
			FROM wallet_ledger_entries l
			JOIN wallets w ON w.id = l.wallet_id
			WHERE l.wallet_id = $1
			ORDER BY l.created_at DESC, l.id DESC
			LIMIT $2
		`
		rows, err = r.pool.Query(ctx, query, walletId, limit)
	}

	if err != nil {
		return nil, "", fmt.Errorf("falha ao consultar ledger: %w", err)
	}
	defer rows.Close()

	var entries []LedgerItemDTO
	var nextCursor string

	for rows.Next() {
		var (
			id, wId, tId, direction, currency string
			amount, bBefore, bAfter           int64
			createdAt                         time.Time
		)
		if err := rows.Scan(&id, &wId, &tId, &direction, &amount, &bBefore, &bAfter, &createdAt, &currency); err != nil {
			return nil, "", err
		}

		amtM, _ := domain.NewMoney(amount, currency)
		bbM, _ := domain.NewMoney(bBefore, currency)
		baM, _ := domain.NewMoney(bAfter, currency)

		entries = append(entries, LedgerItemDTO{
			ID:            id,
			WalletID:      wId,
			TransactionID: tId,
			Direction:     direction,
			Amount:        amtM.AmountString(),
			Currency:      currency,
			BalanceBefore: bbM.AmountString(),
			BalanceAfter:  baM.AmountString(),
			CreatedAt:     createdAt,
		})
		nextCursor = id
	}

	return entries, nextCursor, nil
}

// CalculateLedgerBalance reconstrói o saldo a partir da soma contábil do ledger (Reconciliação).
func (r *Repository) CalculateLedgerBalance(ctx context.Context, walletId string) (int64, int64, error) {
	query := `
		SELECT
			COALESCE(SUM(CASE WHEN direction = 'CREDIT' THEN amount ELSE -amount END), 0) as calculated_balance,
			COUNT(*) as checked_entries
		FROM wallet_ledger_entries
		WHERE wallet_id = $1
	`
	var calculatedBalance int64
	var checkedEntries int64

	err := r.pool.QueryRow(ctx, query, walletId).Scan(&calculatedBalance, &checkedEntries)
	if err != nil {
		return 0, 0, fmt.Errorf("falha ao calcular reconciliação do ledger: %w", err)
	}
	return calculatedBalance, checkedEntries, nil
}

// =========================================================================
// TRANSAÇÕES DE APOSTAS (Wager Transactions)
// =========================================================================

// CreateTransaction grava uma nova transação financeira na transação corrente.
func (r *Repository) CreateTransaction(ctx context.Context, tx pgx.Tx, t *domain.WagerTransaction) error {
	query := `
		INSERT INTO wager_transactions (
			id, provider_id, external_transaction_id, idempotency_key, payload_hash,
			wallet_id, player_id, round_id, game_id, kind, amount, currency,
			reference_external_transaction_id, resolved_reference_id, status,
			failure_code, balance_after, is_internal, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20
		)
	`
	var balanceAfterCents *int64
	if t.Status() == domain.StatusProcessed && !t.BalanceAfter().IsZero() {
		c := t.BalanceAfter().Cents()
		balanceAfterCents = &c
	}

	var resolvedRef *string
	if t.ResolvedReferenceID() != "" {
		ref := t.ResolvedReferenceID()
		resolvedRef = &ref
	}

	var providerID *string
	if t.ProviderID() != "" {
		p := t.ProviderID()
		providerID = &p
	}

	var extID *string
	if t.ExternalTransactionID() != "" {
		e := t.ExternalTransactionID()
		extID = &e
	}

	var idempotencyKey *string
	if t.IdempotencyKey() != "" {
		k := t.IdempotencyKey()
		idempotencyKey = &k
	}

	var payloadHash *string
	if t.PayloadHash() != "" {
		h := t.PayloadHash()
		payloadHash = &h
	}

	var refExtID *string
	if t.ReferenceExternalTransactionID() != "" {
		r := t.ReferenceExternalTransactionID()
		refExtID = &r
	}

	var failureCode *string
	if t.FailureCode() != "" {
		fc := t.FailureCode()
		failureCode = &fc
	}

	_, err := tx.Exec(
		ctx, query,
		t.ID(), providerID, extID, idempotencyKey, payloadHash,
		t.WalletID(), t.PlayerID(), t.RoundID(), t.GameID(), string(t.Kind()),
		t.Money().Cents(), t.Money().Currency(), refExtID, resolvedRef,
		string(t.Status()), failureCode, balanceAfterCents, t.IsInternal(),
		t.CreatedAt(), t.UpdatedAt(),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.ErrDuplicateTransaction
		}
		return fmt.Errorf("falha ao persistir transação de aposta: %w", err)
	}
	return nil
}

// UpdateTransaction atualiza o status, código de falha e saldo resultante da transação.
func (r *Repository) UpdateTransaction(ctx context.Context, tx pgx.Tx, t *domain.WagerTransaction) error {
	query := `
		UPDATE wager_transactions
		SET status = $1, failure_code = $2, balance_after = $3, resolved_reference_id = $4, updated_at = $5
		WHERE id = $6
	`
	var balanceAfterCents *int64
	if t.Status() == domain.StatusProcessed {
		c := t.BalanceAfter().Cents()
		balanceAfterCents = &c
	}

	var failureCode *string
	if t.FailureCode() != "" {
		fc := t.FailureCode()
		failureCode = &fc
	}

	var resolvedRef *string
	if t.ResolvedReferenceID() != "" {
		ref := t.ResolvedReferenceID()
		resolvedRef = &ref
	}

	_, err := tx.Exec(ctx, query, string(t.Status()), failureCode, balanceAfterCents, resolvedRef, t.UpdatedAt(), t.ID())
	if err != nil {
		return fmt.Errorf("falha ao atualizar transação: %w", err)
	}
	return nil
}

// GetTransactionByID busca uma transação pelo identificador interno UUID.
func (r *Repository) GetTransactionByID(ctx context.Context, id string) (*domain.WagerTransaction, error) {
	query := `
		SELECT id, provider_id, external_transaction_id, idempotency_key, payload_hash,
		       wallet_id, player_id, round_id, game_id, kind, amount, currency,
		       reference_external_transaction_id, resolved_reference_id, status,
		       failure_code, balance_after, is_internal, created_at, updated_at
		FROM wager_transactions WHERE id = $1
	`
	return r.scanTransaction(r.pool.QueryRow(ctx, query, id))
}

// GetTransactionByExternalID busca a transação pela chave natural de negócio (provider_id, external_transaction_id).
func (r *Repository) GetTransactionByExternalID(ctx context.Context, providerId, externalId string) (*domain.WagerTransaction, error) {
	query := `
		SELECT id, provider_id, external_transaction_id, idempotency_key, payload_hash,
		       wallet_id, player_id, round_id, game_id, kind, amount, currency,
		       reference_external_transaction_id, resolved_reference_id, status,
		       failure_code, balance_after, is_internal, created_at, updated_at
		FROM wager_transactions WHERE provider_id = $1 AND external_transaction_id = $2
	`
	return r.scanTransaction(r.pool.QueryRow(ctx, query, providerId, externalId))
}

// GetTransactionByIdempotencyKey busca uma transação pela chave de idempotência do cabeçalho.
func (r *Repository) GetTransactionByIdempotencyKey(ctx context.Context, idempotencyKey string) (*domain.WagerTransaction, error) {
	query := `
		SELECT id, provider_id, external_transaction_id, idempotency_key, payload_hash,
		       wallet_id, player_id, round_id, game_id, kind, amount, currency,
		       reference_external_transaction_id, resolved_reference_id, status,
		       failure_code, balance_after, is_internal, created_at, updated_at
		FROM wager_transactions WHERE idempotency_key = $1
	`
	return r.scanTransaction(r.pool.QueryRow(ctx, query, idempotencyKey))
}

// GetTransactionByIdempotencyKeyTx busca uma transação pela chave de idempotência dentro de uma transação SQL.
func (r *Repository) GetTransactionByIdempotencyKeyTx(ctx context.Context, tx pgx.Tx, idempotencyKey string) (*domain.WagerTransaction, error) {
	query := `
		SELECT id, provider_id, external_transaction_id, idempotency_key, payload_hash,
		       wallet_id, player_id, round_id, game_id, kind, amount, currency,
		       reference_external_transaction_id, resolved_reference_id, status,
		       failure_code, balance_after, is_internal, created_at, updated_at
		FROM wager_transactions WHERE idempotency_key = $1
	`
	return r.scanTransaction(tx.QueryRow(ctx, query, idempotencyKey))
}

// GetTransactionByExternalIDTx busca uma transação pela chave externa dentro de uma transação SQL.
func (r *Repository) GetTransactionByExternalIDTx(ctx context.Context, tx pgx.Tx, providerId, externalId string) (*domain.WagerTransaction, error) {
	query := `
		SELECT id, provider_id, external_transaction_id, idempotency_key, payload_hash,
		       wallet_id, player_id, round_id, game_id, kind, amount, currency,
		       reference_external_transaction_id, resolved_reference_id, status,
		       failure_code, balance_after, is_internal, created_at, updated_at
		FROM wager_transactions WHERE provider_id = $1 AND external_transaction_id = $2
	`
	return r.scanTransaction(tx.QueryRow(ctx, query, providerId, externalId))
}

// scanTransaction mapeia a linha SQL para a entidade de domínio.
func (r *Repository) scanTransaction(row pgx.Row) (*domain.WagerTransaction, error) {
	var (
		id, roundId, gameId, kind, currency, status string
		providerID, extID, idempKey, payloadHash    *string
		refExtID, resolvedRef, failureCode          *string
		walletID, playerID                          string
		amount                                      int64
		balanceAfter                                *int64
		isInternal                                  bool
		createdAt, updatedAt                        time.Time
	)

	err := row.Scan(
		&id, &providerID, &extID, &idempKey, &payloadHash,
		&walletID, &playerID, &roundId, &gameId, &kind, &amount, &currency,
		&refExtID, &resolvedRef, &status, &failureCode, &balanceAfter, &isInternal,
		&createdAt, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("falha ao escanear transação: %w", err)
	}

	money, _ := domain.NewMoney(amount, currency)
	var baMoney domain.Money
	if balanceAfter != nil {
		baMoney, _ = domain.NewMoney(*balanceAfter, currency)
	}

	pID, eID, ik, ph, reID, rrID, fc := "", "", "", "", "", "", ""
	if providerID != nil {
		pID = *providerID
	}
	if extID != nil {
		eID = *extID
	}
	if idempKey != nil {
		ik = *idempKey
	}
	if payloadHash != nil {
		ph = *payloadHash
	}
	if refExtID != nil {
		reID = *refExtID
	}
	if resolvedRef != nil {
		rrID = *resolvedRef
	}
	if failureCode != nil {
		fc = *failureCode
	}

	return domain.RehydrateTransaction(
		id, pID, eID, ik, ph, walletID, playerID, roundId, gameId,
		domain.TransactionKind(kind), money, reID, rrID,
		domain.TransactionStatus(status), fc, baMoney, isInternal,
		createdAt, updatedAt,
	), nil
}

// =========================================================================
// TRANSACTIONAL OUTBOX
// =========================================================================

// CreateOutboxEvent registra um evento no outbox na mesma transação da mutação contábil.
func (r *Repository) CreateOutboxEvent(ctx context.Context, tx pgx.Tx, envelope domain.EventEnvelope) error {
	query := `
		INSERT INTO outbox_events (
			id, event_id, event_type, aggregate_id, correlation_id, causation_id, payload, status, created_at, next_retry_at
		) VALUES (
			uuid_generate_v4(), $1, $2, $3, $4, $5, $6, 'PENDING', NOW(), NOW()
		)
	`
	payloadBytes, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("falha ao serializar payload do outbox: %w", err)
	}

	_, err = tx.Exec(
		ctx, query,
		envelope.EventID, envelope.EventType, envelope.AggregateID,
		envelope.CorrelationID, envelope.CausationID, payloadBytes,
	)
	if err != nil {
		return fmt.Errorf("falha ao gravar evento no outbox: %w", err)
	}
	return nil
}

// OutboxRecordDTO representa um registro pendente na tabela outbox_events.
type OutboxRecordDTO struct {
	ID        string
	EventID   string
	EventType string
	Payload   []byte
}

// FetchPendingOutboxEvents busca eventos pendentes com FOR UPDATE SKIP LOCKED.
// Permite que múltiplas instâncias publiquem eventos em paralelo sem duplicidade nem bloqueio.
func (r *Repository) FetchPendingOutboxEvents(ctx context.Context, limit int) ([]OutboxRecordDTO, error) {
	query := `
		SELECT id, event_id, event_type, payload
		FROM outbox_events
		WHERE status = 'PENDING' AND next_retry_at <= NOW()
		ORDER BY next_retry_at ASC, created_at ASC
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`
	rows, err := r.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("falha ao buscar eventos pendentes do outbox: %w", err)
	}
	defer rows.Close()

	var records []OutboxRecordDTO
	for rows.Next() {
		var rec OutboxRecordDTO
		if err := rows.Scan(&rec.ID, &rec.EventID, &rec.EventType, &rec.Payload); err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, nil
}

// MarkOutboxPublished marca o evento como publicado.
func (r *Repository) MarkOutboxPublished(ctx context.Context, id string) error {
	query := `UPDATE outbox_events SET status = 'PUBLISHED', published_at = NOW() WHERE id = $1`
	_, err := r.pool.Exec(ctx, query, id)
	return err
}

// IncrementOutboxRetry atualiza a tentativa com backoff exponencial.
func (r *Repository) IncrementOutboxRetry(ctx context.Context, id string, nextRetry time.Time) error {
	query := `UPDATE outbox_events SET retry_count = retry_count + 1, next_retry_at = $1 WHERE id = $2`
	_, err := r.pool.Exec(ctx, query, nextRetry, id)
	return err
}

// =========================================================================
// INBOX PATTERN (Deduplicação de Mensagens SQS)
// =========================================================================

// HasInboxMessage verifica se a mensagem SQS já foi processada por este consumidor.
func (r *Repository) HasInboxMessage(ctx context.Context, consumerName, messageId string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM inbox_messages WHERE consumer_name = $1 AND message_id = $2)`
	var exists bool
	err := r.pool.QueryRow(ctx, query, consumerName, messageId).Scan(&exists)
	return exists, err
}

// SaveInboxMessage registra a mensagem na inbox dentro da transação atômica.
func (r *Repository) SaveInboxMessage(ctx context.Context, tx pgx.Tx, consumerName, messageId, payloadHash string) error {
	query := `
		INSERT INTO inbox_messages (id, consumer_name, message_id, payload_hash, processed_at, created_at)
		VALUES (uuid_generate_v4(), $1, $2, $3, NOW(), NOW())
		ON CONFLICT (consumer_name, message_id) DO NOTHING
	`
	_, err := tx.Exec(ctx, query, consumerName, messageId, payloadHash)
	return err
}

// GetPendingReferenceTransactions busca transações que estão aguardando referência para resolução pelo worker.
func (r *Repository) GetPendingReferenceTransactions(ctx context.Context, limit int) ([]*domain.WagerTransaction, error) {
	query := `
		SELECT id, provider_id, external_transaction_id, idempotency_key, payload_hash,
		       wallet_id, player_id, round_id, game_id, kind, amount, currency,
		       reference_external_transaction_id, resolved_reference_id, status,
		       failure_code, balance_after, is_internal, created_at, updated_at
		FROM wager_transactions
		WHERE status = 'PENDING_REFERENCE'
		ORDER BY created_at ASC
		LIMIT $1
	`
	rows, err := r.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*domain.WagerTransaction
	for rows.Next() {
		tx, err := r.scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		if tx != nil {
			list = append(list, tx)
		}
	}
	return list, nil
}
