-- =============================================================================
-- 000001_init — schema inicial do desafio
--
-- Ordem de criação (por causa das chaves estrangeiras):
--   wallets -> wager_transactions -> wallet_ledger_entries
-- O down.sql reverte na ordem inversa.
--
-- Princípios aplicados aqui:
--   * dinheiro é BIGINT em unidades mínimas + CHAR(3) ISO 4217 (nunca NUMERIC/FLOAT);
--   * toda regra que o domínio valida em memória ganha a constraint correspondente,
--     porque a constraint sobrevive a um bug de aplicação;
--   * idempotência é índice único, não SELECT antes de INSERT (que tem corrida);
--   * o ledger é append-only por trigger, não por convenção.
-- =============================================================================

-- -----------------------------------------------------------------------------
-- Funções auxiliares
-- -----------------------------------------------------------------------------

-- Mantém updated_at coerente sem depender do aplicativo lembrar de atualizar.
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Impede alteração e exclusão: é o que torna o ledger uma prova, e não um cache.
CREATE OR REPLACE FUNCTION prevent_ledger_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'wallet_ledger_entries é append-only: % não é permitido', TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

-- -----------------------------------------------------------------------------
-- wallets — o estado atual do saldo
-- -----------------------------------------------------------------------------

CREATE TABLE wallets (
    id            UUID        PRIMARY KEY,
    player_id     TEXT        NOT NULL,
    currency      CHAR(3)     NOT NULL,
    balance_minor BIGINT      NOT NULL DEFAULT 0,
    version       BIGINT      NOT NULL DEFAULT 1,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- O par (player_id, currency) identifica uma única carteira (README §6.2).
    CONSTRAINT wallets_player_currency_key UNIQUE (player_id, currency),

    -- Saldo nunca negativo: a invariante mais importante do domínio financeiro.
    CONSTRAINT wallets_balance_non_negative CHECK (balance_minor >= 0),

    -- Limite do domínio (A2). Fica ~1000x abaixo do teto do BIGINT de propósito,
    -- para as checagens de overflow do domínio poderem ser escritas sem estourar.
    CONSTRAINT wallets_balance_within_limit CHECK (balance_minor <= 9000000000000000),

    -- A versão nasce em 1 e só avança quando o saldo muda (README §6.2).
    CONSTRAINT wallets_version_positive CHECK (version >= 1),

    -- Código alfabético ISO 4217: exatamente três letras maiúsculas.
    CONSTRAINT wallets_currency_iso4217 CHECK (currency ~ '^[A-Z]{3}$'),

    CONSTRAINT wallets_player_id_not_blank CHECK (btrim(player_id) <> '')
);

-- -----------------------------------------------------------------------------
-- wager_transactions — a operação pedida pelo provedor (ou a abertura interna)
-- -----------------------------------------------------------------------------

CREATE TABLE wager_transactions (
    id                              UUID        PRIMARY KEY,
    wallet_id                       UUID        NOT NULL REFERENCES wallets (id) ON DELETE RESTRICT,
    player_id                       TEXT        NOT NULL,
    kind                            TEXT        NOT NULL,
    status                          TEXT        NOT NULL DEFAULT 'PENDING',
    amount_minor                    BIGINT      NOT NULL,
    currency                        CHAR(3)     NOT NULL,
    game_id                         TEXT        NOT NULL,
    round_id                        TEXT        NOT NULL,

    -- Idempotência persistente (A5). Presente na origem interna também: é o que
    -- impede crédito inicial duplicado em OPENING.
    idempotency_key                 TEXT        NOT NULL,
    payload_hash                    TEXT        NOT NULL,

    -- Específicos de operação externa: nulos em OPENING.
    provider_id                     TEXT,
    external_transaction_id         TEXT,
    reference_external_txn_id       TEXT,
    reference_transaction_id        UUID        REFERENCES wager_transactions (id) ON DELETE RESTRICT,

    -- Resultado da operação, para auditoria e para o replay devolver o mesmo saldo.
    failure_code                    TEXT,
    result_balance_minor            BIGINT,

    -- Retomada durável do worker de referências (README §7).
    reference_resolution_attempt    INTEGER     NOT NULL DEFAULT 0,
    next_resolution_attempt_at      TIMESTAMPTZ,

    created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT wager_transactions_kind_valid CHECK (
        kind IN ('OPENING', 'BET', 'WIN', 'LOSS', 'REFUND', 'ROLLBACK')
    ),

    CONSTRAINT wager_transactions_status_valid CHECK (
        status IN ('PENDING', 'PENDING_REFERENCE', 'PROCESSED', 'REJECTED', 'FAILED')
    ),

    CONSTRAINT wager_transactions_currency_iso4217 CHECK (currency ~ '^[A-Z]{3}$'),

    -- Política de valor zero (README §7): LOSS exige 0.00 exato e nenhum outro
    -- tipo aceita zero. Nada de valor negativo em entrada financeira externa.
    CONSTRAINT wager_transactions_amount_policy CHECK (
        (kind = 'LOSS' AND amount_minor = 0)
        OR (kind <> 'LOSS' AND amount_minor > 0)
    ),

    CONSTRAINT wager_transactions_amount_within_limit CHECK (
        amount_minor <= 9000000000000000
    ),

    CONSTRAINT wager_transactions_result_balance_range CHECK (
        result_balance_minor IS NULL
        OR (result_balance_minor >= 0 AND result_balance_minor <= 9000000000000000)
    ),

    -- OPENING é operação interna e não carrega metadados de provedor.
    CONSTRAINT wager_transactions_opening_is_internal CHECK (
        kind <> 'OPENING' OR (provider_id IS NULL AND external_transaction_id IS NULL)
    ),

    -- Operação externa precisa dos metadados que a identificam e deduplicam.
    CONSTRAINT wager_transactions_external_has_provider CHECK (
        kind = 'OPENING' OR (provider_id IS NOT NULL AND external_transaction_id IS NOT NULL)
    ),

    -- Reversão sem referência não tem como achar o que desfazer.
    CONSTRAINT wager_transactions_reversal_requires_reference CHECK (
        kind NOT IN ('REFUND', 'ROLLBACK') OR reference_external_txn_id IS NOT NULL
    ),

    CONSTRAINT wager_transactions_attempt_non_negative CHECK (reference_resolution_attempt >= 0),

    CONSTRAINT wager_transactions_ids_not_blank CHECK (
        btrim(player_id) <> '' AND btrim(game_id) <> '' AND btrim(round_id) <> ''
        AND btrim(idempotency_key) <> '' AND btrim(payload_hash) <> ''
    ),

    -- Resultado definitivo precisa dizer por quê, com código estável.
    CONSTRAINT wager_transactions_terminal_has_failure_code CHECK (
        status <> 'REJECTED' OR failure_code IS NOT NULL
    )
);

-- Idempotência persistente: a MESMA chave do provedor não pode gerar duas
-- operações. Índice único, não SELECT antes de INSERT (que teria corrida).
CREATE UNIQUE INDEX wager_transactions_provider_idempotency_key
    ON wager_transactions (provider_id, idempotency_key)
    WHERE provider_id IS NOT NULL;

-- Mesma operação externa não pode ser registrada duas vezes.
CREATE UNIQUE INDEX wager_transactions_provider_external_tx
    ON wager_transactions (provider_id, external_transaction_id)
    WHERE provider_id IS NOT NULL;

-- Impede duas reversões bem-sucedidas do mesmo tipo sobre a mesma referência
-- (README §7): pode haver um REFUND e um ROLLBACK, nunca dois do mesmo tipo.
CREATE UNIQUE INDEX wager_transactions_reference_kind
    ON wager_transactions (reference_transaction_id, kind)
    WHERE reference_transaction_id IS NOT NULL;

-- Impede crédito inicial duplicado: uma OPENING por chave de idempotência.
CREATE UNIQUE INDEX wager_transactions_opening_idempotency_key
    ON wager_transactions (idempotency_key)
    WHERE kind = 'OPENING';

-- O worker de referências pendentes varre por estado + próximo instante.
CREATE INDEX wager_transactions_pending_reference
    ON wager_transactions (next_resolution_attempt_at)
    WHERE status = 'PENDING_REFERENCE';

-- Consulta de operações de uma carteira, mais recentes primeiro.
CREATE INDEX wager_transactions_wallet_created_at
    ON wager_transactions (wallet_id, created_at DESC, id DESC);

-- -----------------------------------------------------------------------------
-- wallet_ledger_entries — o histórico append-only
-- -----------------------------------------------------------------------------

CREATE TABLE wallet_ledger_entries (
    id                   UUID        PRIMARY KEY,
    wallet_id            UUID        NOT NULL REFERENCES wallets (id) ON DELETE RESTRICT,
    transaction_id       UUID        NOT NULL REFERENCES wager_transactions (id) ON DELETE RESTRICT,
    direction            TEXT        NOT NULL,
    amount_minor         BIGINT      NOT NULL,
    balance_before_minor BIGINT      NOT NULL,
    balance_after_minor  BIGINT      NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT ledger_direction_valid CHECK (direction IN ('DEBIT', 'CREDIT')),

    -- Lançamento de valor zero não existe: valor zero não movimenta dinheiro
    -- (é a regra do LOSS, que não gera lançamento — README §7).
    CONSTRAINT ledger_amount_positive CHECK (amount_minor > 0),

    -- A aritmética do ledger conferida pelo banco. É esta constraint que impede
    -- um lançamento incoerente entrar, mesmo com bug na aplicação.
    CONSTRAINT ledger_balance_consistency CHECK (
        balance_after_minor = balance_before_minor
            + CASE direction WHEN 'CREDIT' THEN amount_minor ELSE -amount_minor END
    ),

    CONSTRAINT ledger_balances_non_negative CHECK (
        balance_before_minor >= 0 AND balance_after_minor >= 0
    ),

    CONSTRAINT ledger_balances_within_limit CHECK (
        balance_before_minor <= 9000000000000000 AND balance_after_minor <= 9000000000000000
    ),

    -- Uma operação gera no máximo um lançamento: é a proteção contra
    -- movimentação duplicada (README §6.4).
    CONSTRAINT ledger_wallet_transaction_key UNIQUE (wallet_id, transaction_id)
);

-- Imutabilidade no banco: o ledger é prova, não cache.
CREATE TRIGGER wallet_ledger_entries_append_only
    BEFORE UPDATE OR DELETE ON wallet_ledger_entries
    FOR EACH ROW EXECUTE FUNCTION prevent_ledger_mutation();

-- Paginação por cursor: base64url(created_at RFC3339Nano | entryId), com
-- ORDER BY created_at, id (A9).
CREATE INDEX wallet_ledger_entries_wallet_created_at
    ON wallet_ledger_entries (wallet_id, created_at, id);

-- -----------------------------------------------------------------------------
-- inbox_messages — deduplicação de mensagem consumida
-- -----------------------------------------------------------------------------

CREATE TABLE inbox_messages (
    id           UUID        PRIMARY KEY,
    consumer_name TEXT       NOT NULL,
    message_id   TEXT        NOT NULL,
    body_hash    TEXT        NOT NULL,
    status       TEXT        NOT NULL DEFAULT 'RECEIVED',
    failure_code TEXT,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT inbox_status_valid CHECK (
        status IN ('RECEIVED', 'PROCESSED', 'REJECTED', 'FAILED')
    ),

    CONSTRAINT inbox_consumer_not_blank CHECK (btrim(consumer_name) <> ''),
    CONSTRAINT inbox_message_id_not_blank CHECK (btrim(message_id) <> ''),

    -- Conclusão precisa de instante: é o que prova que a mensagem foi tratada.
    CONSTRAINT inbox_completed_has_timestamp CHECK (
        completed_at IS NULL OR status <> 'RECEIVED'
    )
);

-- Unicidade por (consumidor, mensagem): a mesma mensagem não é tratada duas vezes.
CREATE UNIQUE INDEX inbox_messages_consumer_message
    ON inbox_messages (consumer_name, message_id);

-- -----------------------------------------------------------------------------
-- outbox_events — publicação confiável, na mesma transação do domínio
-- -----------------------------------------------------------------------------

CREATE TABLE outbox_events (
    event_id       UUID        PRIMARY KEY,
    aggregate_id   TEXT        NOT NULL,
    aggregate_type TEXT        NOT NULL,
    event_type     TEXT        NOT NULL,
    payload        JSONB       NOT NULL,
    occurred_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    available_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts       INTEGER     NOT NULL DEFAULT 0,
    last_error     TEXT,
    published_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT outbox_attempts_non_negative CHECK (attempts >= 0),
    CONSTRAINT outbox_event_type_not_blank CHECK (btrim(event_type) <> ''),
    CONSTRAINT outbox_aggregate_id_not_blank CHECK (btrim(aggregate_id) <> '')
);

-- O publisher busca o que está pronto para sair, mais antigo primeiro, e só o
-- que ainda não foi publicado.
CREATE INDEX outbox_events_pending
    ON outbox_events (available_at, occurred_at)
    WHERE published_at IS NULL;

-- -----------------------------------------------------------------------------
-- Triggers de updated_at
-- -----------------------------------------------------------------------------

CREATE TRIGGER wallets_set_updated_at
    BEFORE UPDATE ON wallets
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER wager_transactions_set_updated_at
    BEFORE UPDATE ON wager_transactions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER inbox_messages_set_updated_at
    BEFORE UPDATE ON inbox_messages
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER outbox_events_set_updated_at
    BEFORE UPDATE ON outbox_events
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
