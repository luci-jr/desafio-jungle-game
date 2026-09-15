-- =====================================================================
-- Migração 000001: Schema Inicial do Processador Distribuído de Apostas
-- =====================================================================

-- Extensão para UUIDs v4
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- 1. TABELA DE CARTEIRAS (Wallets)
-- Raiz do agregado financeiro. O par (player_id, currency) é estritamente único.
-- O saldo possui constraint CHECK (balance >= 0) para impedir saldo negativo em nível de motor.
CREATE TABLE IF NOT EXISTS wallets (
    id UUID PRIMARY KEY,
    player_id VARCHAR(255) NOT NULL,
    currency VARCHAR(3) NOT NULL,
    balance BIGINT NOT NULL CHECK (balance >= 0),
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_wallets_player_currency UNIQUE (player_id, currency)
);

CREATE INDEX IF NOT EXISTS idx_wallets_player_id ON wallets(player_id);

-- 2. TABELA DE TRANSAÇÕES FINANCEIRAS DE APOSTAS (Wager Transactions)
-- Registra operações externas e internas (OPENING).
-- Garante idempotência via chaves únicas em (provider_id, external_transaction_id) e idempotency_key.
CREATE TABLE IF NOT EXISTS wager_transactions (
    id UUID PRIMARY KEY,
    provider_id VARCHAR(255),
    external_transaction_id VARCHAR(255),
    idempotency_key VARCHAR(255),
    payload_hash VARCHAR(64),
    wallet_id UUID NOT NULL REFERENCES wallets(id) ON DELETE RESTRICT,
    player_id VARCHAR(255) NOT NULL,
    round_id VARCHAR(255),
    game_id VARCHAR(255),
    kind VARCHAR(50) NOT NULL,
    amount BIGINT NOT NULL CHECK (amount >= 0),
    currency VARCHAR(3) NOT NULL,
    reference_external_transaction_id VARCHAR(255),
    resolved_reference_id UUID REFERENCES wager_transactions(id) ON DELETE RESTRICT,
    status VARCHAR(50) NOT NULL,
    failure_code VARCHAR(100),
    balance_after BIGINT,
    is_internal BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_transactions_provider_external UNIQUE (provider_id, external_transaction_id),
    CONSTRAINT uq_transactions_idempotency UNIQUE (idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_transactions_wallet_id ON wager_transactions(wallet_id);
CREATE INDEX IF NOT EXISTS idx_transactions_ref_lookup ON wager_transactions(provider_id, reference_external_transaction_id);
CREATE INDEX IF NOT EXISTS idx_transactions_pending_reference ON wager_transactions(status) WHERE status = 'PENDING_REFERENCE';
CREATE UNIQUE INDEX IF NOT EXISTS uq_successful_reversal_reference
    ON wager_transactions(provider_id, reference_external_transaction_id)
    WHERE status = 'PROCESSED' AND kind IN ('REFUND', 'ROLLBACK');

-- 3. TABELA DE LANÇAMENTOS DO LIVRO-RAZÃO (Wallet Ledger Entries)
-- Estritamente append-only. Imutável.
-- A relação (wallet_id, transaction_id) é estritamente única.
CREATE TABLE IF NOT EXISTS wallet_ledger_entries (
    id UUID PRIMARY KEY,
    wallet_id UUID NOT NULL REFERENCES wallets(id) ON DELETE RESTRICT,
    transaction_id UUID NOT NULL REFERENCES wager_transactions(id) ON DELETE RESTRICT,
    direction VARCHAR(10) NOT NULL CHECK (direction IN ('DEBIT', 'CREDIT')),
    amount BIGINT NOT NULL CHECK (amount > 0),
    balance_before BIGINT NOT NULL CHECK (balance_before >= 0),
    balance_after BIGINT NOT NULL CHECK (balance_after >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_ledger_wallet_transaction UNIQUE (wallet_id, transaction_id)
);

CREATE INDEX IF NOT EXISTS idx_ledger_wallet_created ON wallet_ledger_entries(wallet_id, created_at ASC);

-- TRIGGER DE PROTEÇÃO CONTRA ALTERAÇÃO E EXCLUSÃO DO LEDGER
CREATE OR REPLACE FUNCTION fn_protect_ledger_immutability()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'Operacao proibida: os lancamentos do ledger sao estritamente imutaveis e append-only.';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_protect_ledger_immutability ON wallet_ledger_entries;
CREATE TRIGGER trg_protect_ledger_immutability
BEFORE UPDATE OR DELETE ON wallet_ledger_entries
FOR EACH ROW EXECUTE FUNCTION fn_protect_ledger_immutability();

-- 4. TABELA DE INBOX (Deduplicação de Mensageria SQS)
-- Garante que mensagens SQS repetidas não sejam processadas mais de uma vez.
CREATE TABLE IF NOT EXISTS inbox_messages (
    id UUID PRIMARY KEY,
    consumer_name VARCHAR(100) NOT NULL,
    message_id VARCHAR(255) NOT NULL,
    payload_hash VARCHAR(64) NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_inbox_consumer_message UNIQUE (consumer_name, message_id)
);

-- 5. TABELA DE TRANSACTIONAL OUTBOX (Publicação Resiliente de Eventos)
-- Eventos são salvos na mesma transação SQL da mutação de saldo e ledger.
CREATE TABLE IF NOT EXISTS outbox_events (
    id UUID PRIMARY KEY,
    event_id VARCHAR(255) NOT NULL UNIQUE,
    event_type VARCHAR(100) NOT NULL,
    aggregate_id VARCHAR(255) NOT NULL,
    correlation_id VARCHAR(255),
    causation_id VARCHAR(255),
    payload JSONB NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'PUBLISHED', 'FAILED')),
    retry_count INT NOT NULL DEFAULT 0,
    next_retry_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claim_token VARCHAR(255),
    claim_until TIMESTAMPTZ,
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Atualiza bancos criados pela versão anterior da migration sem exigir perda de dados.
ALTER TABLE outbox_events
    ADD COLUMN IF NOT EXISTS claim_token VARCHAR(255),
    ADD COLUMN IF NOT EXISTS claim_until TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_outbox_pending ON outbox_events(status, next_retry_at ASC) WHERE status = 'PENDING';
CREATE INDEX IF NOT EXISTS idx_outbox_claim ON outbox_events(claim_until) WHERE status = 'PENDING';
