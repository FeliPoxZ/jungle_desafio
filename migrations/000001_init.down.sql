-- =============================================================================
-- 000001_init — rollback
--
-- Reverte a migration inicial na ordem inversa da criação, por causa das
-- chaves estrangeiras. Migrations do golang-migrate rodam dentro de uma
-- transação, então um erro no meio desfaz tudo.
-- =============================================================================

-- Triggers caem junto com as tabelas, mas os nomes explícitos deixam o rollback
-- legível e garantem que nada sobre se a ordem mudar.
DROP TRIGGER IF EXISTS outbox_events_set_updated_at ON outbox_events;
DROP TRIGGER IF EXISTS inbox_messages_set_updated_at ON inbox_messages;
DROP TRIGGER IF EXISTS wager_transactions_set_updated_at ON wager_transactions;
DROP TRIGGER IF EXISTS wallets_set_updated_at ON wallets;
DROP TRIGGER IF EXISTS wallet_ledger_entries_append_only ON wallet_ledger_entries;

DROP INDEX IF EXISTS outbox_events_pending;
DROP TABLE IF EXISTS outbox_events;

DROP INDEX IF EXISTS inbox_messages_consumer_message;
DROP TABLE IF EXISTS inbox_messages;

DROP INDEX IF EXISTS wallet_ledger_entries_wallet_created_at;
DROP TABLE IF EXISTS wallet_ledger_entries;

DROP INDEX IF EXISTS wager_transactions_wallet_created_at;
DROP INDEX IF EXISTS wager_transactions_pending_reference;
DROP INDEX IF EXISTS wager_transactions_opening_idempotency_key;
DROP INDEX IF EXISTS wager_transactions_reference_kind;
DROP INDEX IF EXISTS wager_transactions_provider_external_tx;
DROP INDEX IF EXISTS wager_transactions_provider_idempotency_key;
DROP TABLE IF EXISTS wager_transactions;

DROP TABLE IF EXISTS wallets;

DROP FUNCTION IF EXISTS prevent_ledger_mutation();
DROP FUNCTION IF EXISTS set_updated_at();
