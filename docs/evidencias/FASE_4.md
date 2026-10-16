# EVIDÊNCIAS — Fase 4 (schema, migrations e constraints) validada

> Saídas reais de comandos contra o PostgreSQL 16 real (container `jungleg-postgres`,
> healthy). Coletadas em 2026-10-16.

## Ambiente

| Item | Versão/valor |
| --- | --- |
| PostgreSQL | 16-alpine |
| Migration | `golang-migrate/v4 v4.20.1` com o driver `postgres` e a fonte `iofs` |
| Arquivos | `migrations/000001_init.up.sql`, `000001_init.down.sql` |
| CLI próprio | `cmd/migrate` (subcomandos `up`, `down [n]`, `version`) |
| Verificação | `deploy/postgres/verify_constraints.sql` |

## 1. Inventário do schema

```
$ psql -c "SELECT contype, count(*) ..."
 c | 29    -- CHECK
 f |  4    -- FOREIGN KEY
 p |  6    -- PRIMARY KEY
 u |  2    -- UNIQUE

$ índices: 17
$ triggers: 5
```

| Tabela | Papel |
| --- | --- |
| `wallets` | estado atual do saldo (o par `(player_id, currency)` é a identidade) |
| `wager_transactions` | as operações pedidas pelo provedor e a abertura interna |
| `wallet_ledger_entries` | o histórico append-only |
| `inbox_messages` | deduplicação de mensagem consumida |
| `outbox_events` | publicação confiável, na mesma transação do domínio |

Ordem de criação (por causa das FKs): `wallets` → `wager_transactions` →
`wallet_ledger_entries`. O `down.sql` reverte na ordem inversa.

## 2. Cada constraint testada com uma operação que DEVE ser recusada

Saída real de `psql -v ON_ERROR_STOP=1 -f deploy/postgres/verify_constraints.sql`:

```
caminho feliz: wallet + transacao + lancamento inseridos (ok)
wallets.balance >= 0: RECUSADO (ok)
wallets.balance <= 9e15: RECUSADO (ok)
wallets.version >= 1: RECUSADO (ok)
wallets.currency ISO 4217 (minuscula): RECUSADO (ok)
wallets.currency ISO 4217 (2 letras): RECUSADO (ok)
wallets.player_id nao vazio: RECUSADO (ok)
wallets UNIQUE (player_id, currency): RECUSADO (ok)
wager_transactions.kind: RECUSADO (ok)
wager_transactions.status: RECUSADO (ok)
LOSS exige amount = 0: RECUSADO (ok)
BET/WIN/REFUND/ROLLBACK exigem amount > 0: RECUSADO (ok)
valor negativo: RECUSADO (ok)
BET sem provider_id: RECUSADO (ok)
OPENING nao aceita metadados de provedor: RECUSADO (ok)
REFUND/ROLLBACK exigem referencia: RECUSADO (ok)
UNIQUE (provider_id, idempotency_key): RECUSADO (ok)
UNIQUE (provider_id, external_transaction_id): RECUSADO (ok)
REJECTED exige failure_code: RECUSADO (ok)
ledger balance_after = before +/- amount: RECUSADO (ok)
ledger direction DEBIT/CREDIT: RECUSADO (ok)
ledger amount > 0: RECUSADO (ok)
ledger saldos nao negativos: RECUSADO (ok)
UNIQUE (wallet_id, transaction_id): RECUSADO (ok)
ledger append-only (UPDATE): RECUSADO (ok)
ledger append-only (DELETE): RECUSADO (ok)
inbox UNIQUE (consumer_name, message_id): RECUSADO (ok)
inbox status valido: RECUSADO (ok)
outbox attempts >= 0: RECUSADO (ok)
inbox e outbox: caminho feliz e constraints (ok)
trigger updated_at em wallets: avancou (ok)
=== TODAS AS CONSTRAINTS RECUSARAM O QUE DEVIA SER RECUSADO ===
```

**30 verificações, todas aprovadas.** O script está escrito de forma que, se uma
operação indevida passar, ele levanta exceção e o `ON_ERROR_STOP` derruba o
comando — ou seja, é um teste que falha, não um relatório.

## 3. CLI de migration testado em todos os caminhos

```
$ migrate version          (antes)  → "nenhuma migration aplicada"        exit 1
$ migrate up                        → "migrations aplicadas versao=1"     exit 0
$ migrate version          (depois) → "versão do schema versao=1"         exit 0
$ migrate up               (repetido) → "schema já está atualizado"        exit 1
$ migrate down                      → "schema sem versão"                 exit 0
$ migrate down             (repetido) → "nada para reverter"               exit 1
$ migrate up                        → aplica novamente                    exit 0
$ migrate down 1                    → reverte uma migration               exit 0
$ migrate down abc                  → "precisa ser inteiro >= 1"          exit 2
```

Os códigos de saída seguem o contrato do CLI oficial do golang-migrate
(`0` aplicou, `1` nada a fazer, `2` erro), o que permite usar `|| true` no
`make migrate-reset` sem mascarar erro real.

## 4. Rollback de verdade

```
$ psql -f migrations/000001_init.down.sql
DROP INDEX ... DROP TABLE ... DROP FUNCTION
exit code: 0

$ SELECT count(*) FROM information_schema.tables WHERE table_schema='public'
0

$ SELECT proname FROM pg_proc ... WHERE nspname='public'
(vazio)
```

O `down` remove as 5 tabelas, os 17 índices e as 2 funções, sem sobra. E o `up`
reaplica do zero com sucesso (testado em sequência).

## 5. Imagem Docker do serviço de migration

```
$ docker build --target final --build-arg TARGET=migrate -t jungleg-migrate:local .
#16 naming to docker.io/library/jungleg-migrate:local done
jungleg-migrate:local   28.5MB

$ docker run --rm --network jungleg_default \
    -e DATABASE_URL='postgres://jungleg:jungleg@postgres:5432/jungleg?sslmode=disable' \
    jungleg-migrate:local up
time=... level=INFO msg="migrations aplicadas" versao=1 dirty=false
exit: 0

$ (mesmo comando de novo)
time=... level=INFO msg="schema já está atualizado, nada a aplicar"
exit: 1
```

O container aplica as migrations contra o Postgres **pela rede do compose**, que
é exatamente o que o serviço `migrate` do `docker-compose.yml` faz.

## 6. Três defeitos estruturais encontrados e corrigidos

| # | Defeito | Impacto | Correção |
| --- | --- | --- | --- |
| 1 | `Dockerfile` baseado em `golang:1.25-alpine`, mas o `go.mod` exige `go >= 1.26.0` | **Build da imagem falhava** em `go mod download`: *"go.mod requires go >= 1.26.0 (running go 1.25.14; GOTOOLCHAIN=local)"* | imagem passou para `golang:1.26-alpine` |
| 2 | Estágio `build` copiava **apenas** `go.mod`/`go.sum` e rodava `go build` | **Build falhava por ausência do código-fonte** | adicionado `COPY . .` depois do `go mod download`, para preservar o cache de dependências |
| 3 | Não existia `.dockerignore` | O contexto de build incluía **675 MB** de `.gocache`, além de `bin/` e do backup do repositório | `.dockerignore` criado |

Nenhum dos três era conhecido antes desta fase. Os itens 1 e 2 significavam que
`docker compose up --build` (o caminho documentado no README e no Makefile)
**nunca funcionaria**.

## 7. Decisões de design registradas

| Decisão | Motivo |
| --- | --- |
| Dinheiro como `BIGINT` + `CHAR(3)`, nunca `NUMERIC`/`FLOAT` | Um único tipo de número em todo o sistema: o domínio, a coluna e o índice falam a mesma unidade (A2) |
| `CHECK (balance_minor <= 9000000000000000)` | O limite do domínio vira barreira no banco; sem ele, um bug de aplicação gravaria valor que o `Money` recusa reidratar |
| Idempotência por **índice único parcial** e não por `SELECT` antes de `INSERT` | `SELECT` seguido de `INSERT` tem corrida; o índice único não tem (A5) |
| `UNIQUE (idempotency_key) WHERE kind = 'OPENING'` | É o que impede **crédito inicial duplicado**, a falha mais cara do desafio |
| `UNIQUE (reference_transaction_id, kind) WHERE reference_transaction_id IS NOT NULL` | Permite um `REFUND` e um `ROLLBACK` sobre a mesma aposta, mas nunca dois do mesmo tipo |
| A aritmética do ledger no `CHECK` | `balance_after = before ± amount` conferido pelo banco é o que impede lançamento incoerente entrar, mesmo com bug na aplicação |
| Imutabilidade do ledger por **trigger** | Convenção não sobrevive a um `UPDATE` de manutenção; `RAISE EXCEPTION` sobrevive |
| `set_updated_at()` como trigger, não como responsabilidade do app | `updated_at` coerente independente de o aplicativo lembrar |
| Migrations embutidas com `//go:embed` num pacote dedicado | O `//go:embed` só enxerga o diretório do próprio pacote; o pacote em `migrations/` resolve isso **e** dispensa copiar a pasta SQL na imagem |
| `CHAR(3)` com `CHECK (currency ~ '^[A-Z]{3}$')` | `CHAR(n)` no PostgreSQL preenche com espaço, então a constraint é o que garante o formato alfabético ISO 4217 |

## 8. Conformidade com o enunciado (§8 e §6.4)

| Requisito | Onde está provado |
| --- | --- |
| §8 — "unicidade, não negatividade e imutabilidade do ledger devem ser impostas pelo schema, pelas constraints e pelos mecanismos de proteção do banco" | `wallets_balance_non_negative`, `ledger_wallet_transaction_key`, trigger `wallet_ledger_entries_append_only` — todos testados |
| §6.4 — "o lançamento é imutável e sua construção deve validar `balanceAfter = balanceBefore ± money`" | `ledger_balance_consistency` (testada com valor incoerente) |
| §6.4 — "imponha no banco a unicidade de `(walletId, transactionId)` e a proteção contra edição ou exclusão" | `ledger_wallet_transaction_key` + trigger |
| §6.4 — "`LOSS` e operações rejeitadas não produzem lançamentos" | `ledger_amount_positive` + `CHECK (direction IN ...)` + o `CHECK` de política de valor em `wager_transactions` |
| §6.2 — "o par `(playerId, currency)` identifica uma única carteira" | `wallets_player_currency_key` |
| §6.2 — "débitos precisam preservar saldo maior ou igual a zero" | `wallets_balance_non_negative` + `ledger_balances_non_negative` |
| §6.3 L149 — "o schema deve distinguir operações internas e externas e impedir crédito inicial duplicado" | `wager_transactions_opening_is_internal`, `wager_transactions_external_has_provider`, `wager_transactions_opening_idempotency_key` |
| §6.5 — inbox com "unicidade de `(consumerName, messageId)`" | `inbox_messages_consumer_message` |
| §6.5 — outbox com "tentativas, próximo envio e publicação; suporte a retry com backoff" | `outbox_attempts_non_negative` + índice parcial `outbox_events_pending` sobre `(available_at, occurred_at)` |
| A2 — "Persistência: `BIGINT` para `amount_minor` + `CHAR(3)` para `currency`. Nunca `NUMERIC`/`FLOAT`" | colunas de todas as tabelas |

## 9. Pendência resolvida

**P6 do `CONTEXTO.md`** (`cmd/migrate` não existia, mas o `docker-compose.yml` o
referenciava): resolvida nesta fase. O serviço `migrate` do compose agora tem
binário, imagem e migrations funcionando.

**P1 do `CONTEXTO.md`** (healthcheck do `api` usa `-health-check`, flag
inexistente no binário): **continua aberto** — é da Fase 7 (HTTP). Detectado de
novo ao revisar o compose, mas fora do escopo desta fase.
