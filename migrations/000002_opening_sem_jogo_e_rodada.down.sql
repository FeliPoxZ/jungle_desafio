-- =============================================================================
-- 000002 — reversão
--
-- ATENÇÃO: a reversão pressupõe que NÃO exista OPENING com jogo ou rodada vazios.
-- Se existir (toda abertura de carteira criada pela aplicação cria assim), o
-- ADD CONSTRAINT falha — e isso é proposital: recusar é melhor do que apagar ou
-- inventar dado financeiro para a constraint antiga voltar a valer.
--
-- Para reverter com dados presentes, é preciso decidir ANTES o que fazer com
-- essas linhas (o valor de jogo/rodada de uma abertura é uma decisão de negócio,
-- não de migration).
-- =============================================================================

ALTER TABLE wager_transactions
    DROP CONSTRAINT wager_transactions_ids_not_blank;

ALTER TABLE wager_transactions
    ADD CONSTRAINT wager_transactions_ids_not_blank CHECK (
        btrim(player_id) <> '' AND btrim(game_id) <> '' AND btrim(round_id) <> ''
        AND btrim(idempotency_key) <> '' AND btrim(payload_hash) <> ''
    );
