-- =============================================================================
-- 000003 — ordem de saída dos eventos na outbox
--
-- O publisher reivindica lote com `ORDER BY occurred_at`. Isso tem dois furos:
--
--   1. os DOIS eventos da MESMA transação compartilham o `occurred_at` (o mesmo
--      instante, tomado uma vez por operação). Em empate, o PostgreSQL devolve a
--      ordem que o plano produzir — o `WalletBalanceChanged` poderia ser
--      publicado ANTES do `WagerTransactionProcessed` da mesma operação;
--   2. ordenar por relógio de processo supõe relógio monotônico, e ele não é
--      (NTP, ajuste manual, máquina virtual pausada).
--
-- A coluna `sequence` é a ordem de INSERÇÃO, e é ela que vale como ordem dos
-- efeitos: o lock da carteira (`SELECT ... FOR UPDATE`) serializa as operações da
-- MESMA carteira, e ele é mantido desde antes do INSERT até o commit. Então todos
-- os eventos da operação A são inseridos antes de qualquer evento da operação B
-- daquela carteira — que é exatamente a ordem que o `MessageGroupId = walletId`
-- da fila FIFO exige.
--
-- A sequência é global (IDENTITY da tabela) e não por carteira: a ordem que
-- importa é DENTRO do grupo (a carteira), e a ordem global preserva a ordem de
-- cada grupo. Buracos deixados por rollback são normais — `sequence` não é
-- transacional — e não afetam a ordem, só a contiguidade.
--
-- O `occurred_at` continua na tabela como QUANDO o fato aconteceu; ele deixa de
-- ser o critério de ordenação.
-- =============================================================================

ALTER TABLE outbox_events
    ADD COLUMN sequence BIGINT GENERATED ALWAYS AS IDENTITY;

-- A fila continua sendo lida por disponibilidade (o lease) e agora ordenada por
-- sequência: o índice acompanha as duas colunas.
DROP INDEX outbox_events_pending;

CREATE INDEX outbox_events_pending
    ON outbox_events (available_at, sequence)
    WHERE published_at IS NULL;
