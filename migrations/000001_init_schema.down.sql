-- Reversão da migração inicial
DROP TRIGGER IF EXISTS trg_protect_ledger_immutability ON wallet_ledger_entries;
DROP FUNCTION IF EXISTS fn_protect_ledger_immutability();

DROP TABLE IF EXISTS outbox_events;
DROP TABLE IF EXISTS inbox_messages;
DROP TABLE IF EXISTS wallet_ledger_entries;
DROP TABLE IF EXISTS wager_transactions;
DROP TABLE IF EXISTS wallets;
