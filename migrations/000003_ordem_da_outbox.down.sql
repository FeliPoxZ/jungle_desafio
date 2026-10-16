-- =============================================================================
-- 000003 — reversão: a outbox volta a ser ordenada por `occurred_at`
--
-- Reverter aqui NÃO perde dado (a sequência é só ordem), mas volta a permitir que
-- os dois eventos da mesma operação saiam em qualquer ordem — e volta a depender
-- do relógio do processo.
-- =============================================================================

DROP INDEX outbox_events_pending;

CREATE INDEX outbox_events_pending
    ON outbox_events (available_at, occurred_at)
    WHERE published_at IS NULL;

ALTER TABLE outbox_events
    DROP COLUMN sequence;
