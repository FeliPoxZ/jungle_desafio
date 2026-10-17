-- =============================================================================
-- 000005_correlation_id
--
-- POR QUE ESTA COLUNA EXISTE
--
-- O `correlationId` de uma requisição era usado apenas no caminho da resposta: o
-- caso de uso o recebia da borda, gravava nos eventos emitidos NAQUELE commit e
-- o perdia. A partir da Fase 9 existe uma operação que continua DEPOIS da
-- requisição: a transação que ficou em `PENDING_REFERENCE` e é retomada pelo
-- worker de referências minutos (ou horas) mais tarde. Os eventos que esse worker
-- emite — nova tentativa, conclusão, expiração — precisam do mesmo
-- `correlationId` para que a história inteira da operação seja rastreável
-- (README §12: "identificadores disponíveis para rastrear a operação").
--
-- O worker não tem de onde tirar esse valor: o processo que atendeu a requisição
-- já morreu, e inventar um novo identificador no momento da retomada faria a
-- MESMA operação aparecer com DOIS rastreios diferentes — pior do que não ter.
--
-- É NULLABLE de propósito: a coluna é metadado de transporte, não invariante
-- financeira. Linhas gravadas antes desta migration (e o caminho interno, quando
-- ninguém propaga correlação) ficam NULL, e o worker trata isso escolhendo um
-- valor derivado da própria transação.
-- =============================================================================

ALTER TABLE wager_transactions
    ADD COLUMN IF NOT EXISTS correlation_id TEXT;

COMMENT ON COLUMN wager_transactions.correlation_id IS
    'Identificador de correlação da requisição que originou a operação. Metadado de transporte: não entra no hash de idempotência e não tem valor de negócio.';
