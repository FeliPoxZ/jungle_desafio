-- =============================================================================
-- Verificação das constraints do schema inicial.
--
-- Cada bloco TENTA uma operação que deve ser recusada. Se a operação passar,
-- o script levanta exceção e o teste falha. A saída esperada é uma sequência de
-- NOTICE "... RECUSADO (ok)".
--
-- Uso:
--   psql -U jungleg -d jungleg -v ON_ERROR_STOP=1 -f verify_constraints.sql
-- =============================================================================

\set ON_ERROR_STOP on
SET client_min_messages TO NOTICE;

-- -----------------------------------------------------------------------------
-- Dados válidos de apoio (o caminho feliz precisa funcionar)
-- -----------------------------------------------------------------------------

INSERT INTO wallets (id, player_id, currency, balance_minor, version)
VALUES ('11111111-1111-1111-1111-111111111111', 'player-1', 'BRL', 100000, 1);

INSERT INTO wager_transactions (
    id, wallet_id, player_id, kind, status, amount_minor, currency, game_id, round_id,
    idempotency_key, payload_hash, provider_id, external_transaction_id
) VALUES (
    '22222222-2222-2222-2222-222222222222', '11111111-1111-1111-1111-111111111111',
    'player-1', 'BET', 'PENDING', 2500, 'BRL', 'game-1', 'round-1',
    'idem-1', 'hash-1', 'provider-a', 'ext-1'
);

INSERT INTO wallet_ledger_entries (
    id, wallet_id, transaction_id, direction, amount_minor, balance_before_minor, balance_after_minor
) VALUES (
    '33333333-3333-3333-3333-333333333333', '11111111-1111-1111-1111-111111111111',
    '22222222-2222-2222-2222-222222222222', 'DEBIT', 2500, 100000, 97500
);

DO $$ BEGIN RAISE NOTICE 'caminho feliz: wallet + transacao + lancamento inseridos (ok)'; END $$;

-- -----------------------------------------------------------------------------
-- wallets
-- -----------------------------------------------------------------------------

DO $$ BEGIN
    BEGIN
        INSERT INTO wallets (id, player_id, currency, balance_minor)
        VALUES (gen_random_uuid(), 'player-neg', 'BRL', -1);
        RAISE EXCEPTION 'FALHOU: saldo negativo foi aceito';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'wallets.balance >= 0: RECUSADO (ok)';
    END;

    BEGIN
        INSERT INTO wallets (id, player_id, currency, balance_minor)
        VALUES (gen_random_uuid(), 'player-lim', 'BRL', 9000000000000001);
        RAISE EXCEPTION 'FALHOU: saldo acima do limite foi aceito';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'wallets.balance <= 9e15: RECUSADO (ok)';
    END;

    BEGIN
        INSERT INTO wallets (id, player_id, currency, version)
        VALUES (gen_random_uuid(), 'player-ver', 'BRL', 0);
        RAISE EXCEPTION 'FALHOU: version = 0 foi aceita';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'wallets.version >= 1: RECUSADO (ok)';
    END;

    BEGIN
        INSERT INTO wallets (id, player_id, currency) VALUES (gen_random_uuid(), 'player-cur', 'brl');
        RAISE EXCEPTION 'FALHOU: moeda minuscula foi aceita';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'wallets.currency ISO 4217 (minuscula): RECUSADO (ok)';
    END;

    BEGIN
        INSERT INTO wallets (id, player_id, currency) VALUES (gen_random_uuid(), 'player-cur2', 'BR');
        RAISE EXCEPTION 'FALHOU: moeda de 2 letras foi aceita';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'wallets.currency ISO 4217 (2 letras): RECUSADO (ok)';
    END;

    BEGIN
        INSERT INTO wallets (id, player_id, currency) VALUES (gen_random_uuid(), '   ', 'BRL');
        RAISE EXCEPTION 'FALHOU: player_id em branco foi aceito';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'wallets.player_id nao vazio: RECUSADO (ok)';
    END;

    BEGIN
        INSERT INTO wallets (id, player_id, currency)
        VALUES (gen_random_uuid(), 'player-1', 'BRL');
        RAISE EXCEPTION 'FALHOU: (player_id, currency) duplicado foi aceito';
    EXCEPTION WHEN unique_violation THEN
        RAISE NOTICE 'wallets UNIQUE (player_id, currency): RECUSADO (ok)';
    END;
END $$;

-- -----------------------------------------------------------------------------
-- wager_transactions
-- -----------------------------------------------------------------------------

DO $$
DECLARE
    w UUID := '11111111-1111-1111-1111-111111111111';
BEGIN
    BEGIN
        INSERT INTO wager_transactions (id, wallet_id, player_id, kind, amount_minor, currency,
            game_id, round_id, idempotency_key, payload_hash, provider_id, external_transaction_id)
        VALUES (gen_random_uuid(), w, 'player-1', 'DEPOSIT', 100, 'BRL', 'g', 'r', 'k-dep', 'h', 'p', 'e');
        RAISE EXCEPTION 'FALHOU: kind desconhecido foi aceito';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'wager_transactions.kind: RECUSADO (ok)';
    END;

    BEGIN
        INSERT INTO wager_transactions (id, wallet_id, player_id, kind, status, amount_minor, currency,
            game_id, round_id, idempotency_key, payload_hash, provider_id, external_transaction_id)
        VALUES (gen_random_uuid(), w, 'player-1', 'BET', 'DONE', 100, 'BRL', 'g', 'r', 'k-st', 'h', 'p', 'e');
        RAISE EXCEPTION 'FALHOU: status desconhecido foi aceito';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'wager_transactions.status: RECUSADO (ok)';
    END;

    BEGIN
        INSERT INTO wager_transactions (id, wallet_id, player_id, kind, amount_minor, currency,
            game_id, round_id, idempotency_key, payload_hash, provider_id, external_transaction_id)
        VALUES (gen_random_uuid(), w, 'player-1', 'LOSS', 1, 'BRL', 'g', 'r', 'k-loss', 'h', 'p', 'e');
        RAISE EXCEPTION 'FALHOU: LOSS com valor != 0 foi aceito';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'LOSS exige amount = 0: RECUSADO (ok)';
    END;

    BEGIN
        INSERT INTO wager_transactions (id, wallet_id, player_id, kind, amount_minor, currency,
            game_id, round_id, idempotency_key, payload_hash, provider_id, external_transaction_id)
        VALUES (gen_random_uuid(), w, 'player-1', 'BET', 0, 'BRL', 'g', 'r', 'k-zero', 'h', 'p', 'e');
        RAISE EXCEPTION 'FALHOU: BET com valor 0 foi aceita';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'BET/WIN/REFUND/ROLLBACK exigem amount > 0: RECUSADO (ok)';
    END;

    BEGIN
        INSERT INTO wager_transactions (id, wallet_id, player_id, kind, amount_minor, currency,
            game_id, round_id, idempotency_key, payload_hash, provider_id, external_transaction_id)
        VALUES (gen_random_uuid(), w, 'player-1', 'BET', -100, 'BRL', 'g', 'r', 'k-neg', 'h', 'p', 'e');
        RAISE EXCEPTION 'FALHOU: valor negativo foi aceito';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'valor negativo: RECUSADO (ok)';
    END;

    BEGIN
        INSERT INTO wager_transactions (id, wallet_id, player_id, kind, amount_minor, currency,
            game_id, round_id, idempotency_key, payload_hash)
        VALUES (gen_random_uuid(), w, 'player-1', 'BET', 100, 'BRL', 'g', 'r', 'k-semprov', 'h');
        RAISE EXCEPTION 'FALHOU: BET sem provider_id foi aceita';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'BET sem provider_id: RECUSADO (ok)';
    END;

    BEGIN
        INSERT INTO wager_transactions (id, wallet_id, player_id, kind, amount_minor, currency,
            game_id, round_id, idempotency_key, payload_hash, provider_id, external_transaction_id)
        VALUES (gen_random_uuid(), w, 'player-1', 'OPENING', 100000, 'BRL', 'g', 'r', 'k-open', 'h', 'p', 'e');
        RAISE EXCEPTION 'FALHOU: OPENING com provider_id foi aceita';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'OPENING nao aceita metadados de provedor: RECUSADO (ok)';
    END;

    BEGIN
        INSERT INTO wager_transactions (id, wallet_id, player_id, kind, amount_minor, currency,
            game_id, round_id, idempotency_key, payload_hash, provider_id, external_transaction_id)
        VALUES (gen_random_uuid(), w, 'player-1', 'REFUND', 100, 'BRL', 'g', 'r', 'k-ref', 'h', 'p', 'e');
        RAISE EXCEPTION 'FALHOU: REFUND sem referencia foi aceito';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'REFUND/ROLLBACK exigem referencia: RECUSADO (ok)';
    END;

    BEGIN
        INSERT INTO wager_transactions (id, wallet_id, player_id, kind, amount_minor, currency,
            game_id, round_id, idempotency_key, payload_hash, provider_id, external_transaction_id)
        VALUES (gen_random_uuid(), w, 'player-1', 'BET', 100, 'BRL', 'g', 'r', 'idem-1', 'h2', 'provider-a', 'ext-2');
        RAISE EXCEPTION 'FALHOU: idempotency_key repetida para o mesmo provedor foi aceita';
    EXCEPTION WHEN unique_violation THEN
        RAISE NOTICE 'UNIQUE (provider_id, idempotency_key): RECUSADO (ok)';
    END;

    BEGIN
        INSERT INTO wager_transactions (id, wallet_id, player_id, kind, amount_minor, currency,
            game_id, round_id, idempotency_key, payload_hash, provider_id, external_transaction_id)
        VALUES (gen_random_uuid(), w, 'player-1', 'BET', 100, 'BRL', 'g', 'r', 'idem-novo', 'h2', 'provider-a', 'ext-1');
        RAISE EXCEPTION 'FALHOU: external_transaction_id repetido foi aceito';
    EXCEPTION WHEN unique_violation THEN
        RAISE NOTICE 'UNIQUE (provider_id, external_transaction_id): RECUSADO (ok)';
    END;

    BEGIN
        INSERT INTO wager_transactions (id, wallet_id, player_id, kind, status, amount_minor, currency,
            game_id, round_id, idempotency_key, payload_hash, provider_id, external_transaction_id)
        VALUES (gen_random_uuid(), w, 'player-1', 'BET', 'REJECTED', 100, 'BRL', 'g', 'r', 'k-rej', 'h', 'p', 'e');
        RAISE EXCEPTION 'FALHOU: REJECTED sem failure_code foi aceito';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'REJECTED exige failure_code: RECUSADO (ok)';
    END;
END $$;

-- -----------------------------------------------------------------------------
-- wallet_ledger_entries
-- -----------------------------------------------------------------------------

DO $$
DECLARE
    w UUID := '11111111-1111-1111-1111-111111111111';
    t UUID := '22222222-2222-2222-2222-222222222222';
BEGIN
    BEGIN
        INSERT INTO wallet_ledger_entries (id, wallet_id, transaction_id, direction,
            amount_minor, balance_before_minor, balance_after_minor)
        VALUES (gen_random_uuid(), w, t, 'DEBIT', 2500, 100000, 90000);
        RAISE EXCEPTION 'FALHOU: aritmetica incoerente do ledger foi aceita';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'ledger balance_after = before +/- amount: RECUSADO (ok)';
    END;

    BEGIN
        INSERT INTO wallet_ledger_entries (id, wallet_id, transaction_id, direction,
            amount_minor, balance_before_minor, balance_after_minor)
        VALUES (gen_random_uuid(), w, t, 'TRANSFER', 100, 1000, 1100);
        RAISE EXCEPTION 'FALHOU: direcao invalida foi aceita';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'ledger direction DEBIT/CREDIT: RECUSADO (ok)';
    END;

    BEGIN
        INSERT INTO wallet_ledger_entries (id, wallet_id, transaction_id, direction,
            amount_minor, balance_before_minor, balance_after_minor)
        VALUES (gen_random_uuid(), w, t, 'DEBIT', 0, 1000, 1000);
        RAISE EXCEPTION 'FALHOU: lancamento de valor zero foi aceito';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'ledger amount > 0: RECUSADO (ok)';
    END;

    BEGIN
        INSERT INTO wallet_ledger_entries (id, wallet_id, transaction_id, direction,
            amount_minor, balance_before_minor, balance_after_minor)
        VALUES (gen_random_uuid(), w, t, 'DEBIT', 2000, 1000, 0);
        RAISE EXCEPTION 'FALHOU: debito que zeraria abaixo de zero foi aceito';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'ledger saldos nao negativos: RECUSADO (ok)';
    END;

    BEGIN
        INSERT INTO wallet_ledger_entries (id, wallet_id, transaction_id, direction,
            amount_minor, balance_before_minor, balance_after_minor)
        VALUES (gen_random_uuid(), w, t, 'CREDIT', 100, 1000, 1100);
        RAISE EXCEPTION 'FALHOU: segundo lancamento para a mesma transacao foi aceito';
    EXCEPTION WHEN unique_violation THEN
        RAISE NOTICE 'UNIQUE (wallet_id, transaction_id): RECUSADO (ok)';
    END;

    BEGIN
        UPDATE wallet_ledger_entries SET amount_minor = 1 WHERE id = '33333333-3333-3333-3333-333333333333';
        RAISE EXCEPTION 'FALHOU: UPDATE no ledger foi aceito';
    EXCEPTION WHEN restrict_violation THEN
        RAISE NOTICE 'ledger append-only (UPDATE): RECUSADO (ok)';
    END;

    BEGIN
        DELETE FROM wallet_ledger_entries WHERE id = '33333333-3333-3333-3333-333333333333';
        RAISE EXCEPTION 'FALHOU: DELETE no ledger foi aceito';
    EXCEPTION WHEN restrict_violation THEN
        RAISE NOTICE 'ledger append-only (DELETE): RECUSADO (ok)';
    END;
END $$;

-- -----------------------------------------------------------------------------
-- inbox_messages e outbox_events
-- -----------------------------------------------------------------------------

DO $$ BEGIN
    INSERT INTO inbox_messages (id, consumer_name, message_id, body_hash)
    VALUES ('44444444-4444-4444-4444-444444444444', 'wager-transactions', 'msg-1', 'hash-1');

    BEGIN
        INSERT INTO inbox_messages (id, consumer_name, message_id, body_hash)
        VALUES (gen_random_uuid(), 'wager-transactions', 'msg-1', 'hash-2');
        RAISE EXCEPTION 'FALHOU: mensagem duplicada foi aceita';
    EXCEPTION WHEN unique_violation THEN
        RAISE NOTICE 'inbox UNIQUE (consumer_name, message_id): RECUSADO (ok)';
    END;

    BEGIN
        INSERT INTO inbox_messages (id, consumer_name, message_id, body_hash, status)
        VALUES (gen_random_uuid(), 'c', 'msg-x', 'h', 'UNKNOWN');
        RAISE EXCEPTION 'FALHOU: status de inbox desconhecido foi aceito';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'inbox status valido: RECUSADO (ok)';
    END;

    INSERT INTO outbox_events (event_id, aggregate_id, aggregate_type, event_type, payload)
    VALUES ('55555555-5555-5555-5555-555555555555', '11111111-1111-1111-1111-111111111111',
            'wallet', 'WalletBalanceChanged', '{"walletId":"w-1"}'::jsonb);

    BEGIN
        INSERT INTO outbox_events (event_id, aggregate_id, aggregate_type, event_type, payload, attempts)
        VALUES (gen_random_uuid(), 'a', 'wallet', 'X', '{}'::jsonb, -1);
        RAISE EXCEPTION 'FALHOU: attempts negativo foi aceito';
    EXCEPTION WHEN check_violation THEN
        RAISE NOTICE 'outbox attempts >= 0: RECUSADO (ok)';
    END;

    RAISE NOTICE 'inbox e outbox: caminho feliz e constraints (ok)';
END $$;

-- -----------------------------------------------------------------------------
-- Triggers de updated_at
-- -----------------------------------------------------------------------------

DO $$
DECLARE
    antes TIMESTAMPTZ;
    depois TIMESTAMPTZ;
BEGIN
    SELECT updated_at INTO antes FROM wallets WHERE id = '11111111-1111-1111-1111-111111111111';
    PERFORM pg_sleep(0.02);
    UPDATE wallets SET balance_minor = 97500 WHERE id = '11111111-1111-1111-1111-111111111111';
    SELECT updated_at INTO depois FROM wallets WHERE id = '11111111-1111-1111-1111-111111111111';

    IF depois <= antes THEN
        RAISE EXCEPTION 'FALHOU: updated_at nao avancou no UPDATE (antes=%, depois=%)', antes, depois;
    END IF;
    RAISE NOTICE 'trigger updated_at em wallets: avancou (ok)';
END $$;

DO $$ BEGIN RAISE NOTICE '=== TODAS AS CONSTRAINTS RECUSARAM O QUE DEVIA SER RECUSADO ==='; END $$;
