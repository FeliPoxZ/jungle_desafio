-- =============================================================================
-- 000005_correlation_id (reversão)
--
-- A coluna é metadado de TRANSPORTE: nenhuma regra financeira, nenhuma
-- constraint e nenhum índice dependem dela. Derrubá-la não altera saldo, ledger
-- nem estado de transação — o que se perde é a capacidade de rastrear a operação
-- pelo identificador de correlação original.
--
-- Antes de reverter em um ambiente com histórico, avalie se algum evento já
-- publicado depende desse valor para ser correlacionado.
-- =============================================================================

ALTER TABLE wager_transactions
    DROP COLUMN IF EXISTS correlation_id;
