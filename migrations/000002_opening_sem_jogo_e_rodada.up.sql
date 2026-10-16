-- =============================================================================
-- 000002 — OPENING não pertence a jogo nem a rodada
--
-- O CHECK original (000001) exigia game_id e round_id não vazios para TODA linha,
-- inclusive OPENING. Isso contradizia o README §6.3: "Provedor, ID externo, chave
-- e hash externos, rodada, jogo e referência não se aplicam a essa origem".
--
-- A consequência não era teórica: abrir carteira exigiria INVENTAR um jogo e uma
-- rodada para caber no contrato, gravando dado falso no histórico financeiro.
--
-- A constraint continua valendo para as operações externas (que são as que têm
-- jogo e rodada), e a distinção interna × externa fica explícita no schema — que
-- é justamente o que o README §6.3 pede dele.
-- =============================================================================

ALTER TABLE wager_transactions
    DROP CONSTRAINT wager_transactions_ids_not_blank;

ALTER TABLE wager_transactions
    ADD CONSTRAINT wager_transactions_ids_not_blank CHECK (
        btrim(player_id) <> ''
        AND btrim(idempotency_key) <> ''
        AND btrim(payload_hash) <> ''
        AND (
            kind = 'OPENING'
            OR (btrim(game_id) <> '' AND btrim(round_id) <> '')
        )
    );
