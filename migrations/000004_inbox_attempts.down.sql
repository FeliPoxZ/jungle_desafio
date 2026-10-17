-- =============================================================================
-- 000004_inbox_attempts (reversão)
--
-- Remove o contador de recebimentos e o índice de diagnóstico.
--
-- SEGURO: `attempts` é dado de DIAGNÓSTICO (quantas vezes a mensagem foi
-- recebida), reconstruível a partir do log e sem valor financeiro. Nenhum saldo,
-- nenhum lançamento e nenhuma decisão de idempotência dependem dele — a
-- deduplicação usa `body_hash` + `(consumer_name, message_id)`, que continuam na
-- tabela.
--
-- Se houver mensagens NÃO CONCLUÍDAS (completed_at IS NULL) com attempts > 0 no
-- momento da reversão, elas perdem o histórico — e é por isso que a reversão é
-- uma operação de manutenção planejada, não algo para rodar no meio de um
-- incidente com a fila cheia.
-- =============================================================================

DROP INDEX IF EXISTS inbox_messages_attempts_idx;

ALTER TABLE inbox_messages
    DROP CONSTRAINT IF EXISTS inbox_attempts_non_negative;

ALTER TABLE inbox_messages
    DROP COLUMN IF EXISTS attempts;
