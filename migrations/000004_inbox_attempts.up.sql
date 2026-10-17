-- =============================================================================
-- 000004_inbox_attempts
--
-- Acrescenta `attempts` à inbox_messages: quantas vezes este consumidor JÁ
-- recebeu a mensagem.
--
-- POR QUE ISSO FALTAVA
--
-- O README §10 diz "verifique seu hash em reentregas" e o PLANO §3 (Fase 8)
-- previa "attempts da inbox incrementa". A tabela da Fase 4 não tinha a coluna:
-- o `ApproximateReceiveCount` do SQS existe, mas ele é do BROKER e se perde
-- quando a mensagem é removida ou vai para a DLQ. Sem o contador durável, ao
-- investigar uma mensagem morta não haveria como saber se ela foi recebida duas
-- ou vinte vezes.
--
-- SemântICA: o contador é do CONSUMIDOR (por consumer_name + message_id), não da
-- fila. Duas instâncias da aplicação recebendo a mesma mensagem incrementam o
-- MESMO registro — é isso que torna o número útil (o
-- `ApproximateReceiveCount` seria o mesmo, mas só enquanto a mensagem vive).
--
-- O incremento é `attempts = attempts + 1` em SQL (ver inbox_repository.go):
-- com o valor vindo do processo, duas instâncias gravariam o mesmo número final
-- em vez de somar as duas entregas.
-- =============================================================================

ALTER TABLE inbox_messages
    ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;

-- Não-negatividade: o contador é uma contagem, e contagem negativa é sinal de
-- bug de escrita, não de dado válido.
ALTER TABLE inbox_messages
    ADD CONSTRAINT inbox_attempts_non_negative CHECK (attempts >= 0);

-- Índice para o diagnóstico: "quais mensagens mais reentregaram?" e "o que ainda
-- não concluiu?" são as duas perguntas de um incidente de fila.
CREATE INDEX inbox_messages_attempts_idx
    ON inbox_messages (attempts DESC)
    WHERE completed_at IS NULL;
