# PLANO DE EXECUÇÃO — Desafio Backend Go (Processamento Distribuído de Apostas)

> Documento vivo de trabalho. Fonte da verdade sobre **o que fazer, em que ordem, com quais decisões técnicas e o que já está pronto**.
> O enunciado oficial (`README.md` da raiz) é o contrato. Este arquivo é o plano de ataque.
> **Regra:** nada é considerado "pronto" sem código + teste que prove. Nada entra em `ARCHITECTURE.md` sem estar implementado.

- **Prazo:** 2 dias
- **Meta:** 100/100 (nenhum item eliminatório violado)
- **Repositório:** `github.com/FeliPoxZ/jungle_desafio`
- **Última atualização:** D0 — Fase 0 concluída (commit `1eef5b2`)

---

## 0. STATUS DO AMBIENTE (verificado em máquina)

| Item | Status | Observação |
| --- | --- | --- |
| Go | ✅ `go1.26.5 windows/amd64` | `go.mod` vai declarar `go 1.25` (ver Decisão A1) |
| git | ✅ `2.52.0.windows.1` | |
| Docker / Compose | ❌ **NÃO INSTALADO** | **BLOQUEADOR CRÍTICO** — ver Bloqueador B1 |
| openssl | ✅ msys64 | útil para gerar secrets locais |
| winget | ✅ disponível | caminho para instalar Docker |
| psql | ❌ | não é necessário (usaremos container + `make`) |

### B1 — BLOQUEADOR CRÍTICO: Docker ausente
Docker Compose é **obrigatório** no enunciado (PostgreSQL + Keycloak + LocalStack/MiniStack + app).
Sem Docker **não existe**: teste de integração real (eliminatório se tudo virar mock), validação de constraints/migrations, concorrência com 3 instâncias, DLQ, outbox concorrente.

**Ação imediata (usuário, hoje, em paralelo ao código):**
```powershell
winget install -e --id Docker.DockerDesktop
```
Depois: reiniciar a sessão/PC, abrir o Docker Desktop, `docker version`, `docker compose version`.
Habilitar WSL2 se pedido (hoje o `wsl --status` deu `E_ACCESSDENIED`, provável falta de terminal elevado — testar em PowerShell **como administrador**).

**Plano de contingência se Docker não subir a tempo:**
1. Todo o código + testes unitários (`go test ./...`) não dependem de Docker → seguem 100% normais.
2. Testes de integração com `//go:build integration` e `testcontainers-go`. Documentar no README como rodar.
3. Se Docker só existir no fim: validar manualmente via `docker compose up` e rodar a suíte de integração uma vez.
4. **Nunca** substituir PostgreSQL/SQS/IdP por mocks nos testes de integração (eliminatório).

---

## 1. DECISÕES DE ARQUITETURA (congeladas no D0)

Estas decisões entram como texto no `ARCHITECTURE.md`. Congelar cedo evita refactor no dia 2.

### A1. Versões e dependências
| Uso | Escolha | Motivo |
| --- | --- | --- |
| Go | `go 1.25` no `go.mod` e `golang:1.25-alpine` no Dockerfile | toolchain local é 1.26.5, que compila 1.25; 1.25 é estável e sobe em CI/imagem |
| HTTP router | `github.com/go-chi/chi/v5` | `net/http` puro, leve, middleware idiomático, `chi/httptest` |
| Postgres driver | `github.com/jackc/pgx/v5` + `pgxpool` | exigido como preferencial ("SQL explícito") |
| Migrations | `github.com/golang-migrate/migrate/v4` (embedded `iofs`) | up/down versionado, aplicável como comando e testável |
| SQS | `github.com/aws/aws-sdk-go-v2/service/sqs` (+ `config`, `credentials`) | SDK oficial, funciona com LocalStack |
| OIDC | `github.com/coreos/go-oidc/v3/oidc` + `github.com/golang-jwt/jwt/v5` | discovery + JWKS + verificação de assinatura/iss/aud/exp |
| ULID/UUIDv7 | `github.com/google/uuid` (NewV7) | gera IDs no formato dos exemplos do enunciado |
| Config | `github.com/caarlos0/env/v11` | struct tags, validação simples |
| Métricas | `github.com/prometheus/client_golang` | `/metrics` Prometheus |
| Testes | `testing` (padrão) + `go test -race` + `testcontainers-go` (só integração) | exigido: lib padrão |
| Assertivas | stdlib puro (`t.Fatalf`, `errors.Is`) | sem testify → menos dependência, mais "Go way" |

> Sem GORM, sem ORM. Sem `float64` em lugar nenhum (nem em JSON struct — dinheiro é **string**).

### A2. Dinheiro (Money) — representação
- Value object imutável: `type Money struct { minor int64; currency Currency }` (campos privados).
- **Unidades mínimas `int64`** (centavos). Escala fixa **2**. Moeda ISO 4217 (`BRL`), `type Currency string`.
- Persistência: `BIGINT` para `amount_minor` + `CHAR(3)` para `currency`. Nunca `NUMERIC`/`FLOAT`.
- Limite: `|minor| <= 9_000_000_000_000_000` (~90 trilhões de unidades) — bem abaixo do `int64`, deixa folga para somas. Overflow tratado no parse, soma, subtração e negação → erro de domínio, nunca wrap.
- `NaN`, `Infinity`, notação científica, `+`, espaços, mais de 2 casas, string vazia, negativo em entrada externa → **rejeitados com erro tipado** (sem arredondar).
- `LOSS` = `"0.00"` exato; saldo de carteira nunca negativo; negativos só em diferenças internas.
- JSON externo: `{"amount":"25.00","currency":"BRL"}` via tipo `MoneyDTO` com `MarshalJSON`/`UnmarshalJSON` em string.

### A3. Transação SQL e delimitação
- Uma **Unit of Work** (`internal/infra/postgres/uow.go`): abre `pgx.Tx` e injeta o `pgx.Tx` (via interface `DBTX`) nos repositórios.
- **Toda** operação de negócio acontece em **uma única transação**: `wager_transactions` + `wallets` + `wallet_ledger_entries` + `inbox_messages` + `outbox_events`.
- Interfaces de repositório definidas em `internal/domain` (ou `internal/app/ports`); implementação pgx em `internal/infra/postgres`. O serviço de aplicação é quem abre/fecha a transação — repositório **nunca** comita sozinho.
- Advisory locks **não** serão usados. Locks globais proibidos (item 5.6).

### A4. Concorrência por carteira — estratégia escolhida
**Combinação justificável (e documentada):**
1. **Row lock pessimista** na carteira: `SELECT ... FROM wallets WHERE id=$1 FOR UPDATE` (dentro da transação da operação). Serializa apenas escritores da **mesma** carteira.
2. **Constraint de unicidade** `UNIQUE (wallet_id, transaction_id)` no ledger + `CHECK` de saldo/consistência como **rede de segurança final** (garantia no banco, item 5.3).
3. `UPDATE wallets SET balance_minor=$1, version=version+1 WHERE id=$2` com guarda `AND version=$3` quando útil (detecção de lost update).
- Ordem de aquisição sempre igual (`wallets` antes de `wager_transactions` de referência) → **sem deadlock**.
- Carteiras diferentes não compartilham lock → paralelismo real (item 5.6).
- Teste de prova (item 8): saldo 100.00 + duas BET de 80.00 simultâneas → 1 `PROCESSED`, 1 `REJECTED INSUFFICIENT_FUNDS`, saldo 20.00, **um** débito no ledger.

### A5. Idempotência (persistente)
- **Duas chaves de unicidade**, ambas no banco:
  - `UNIQUE (provider_id, idempotency_key)` → chave enviada pelo cliente.
  - `UNIQUE (provider_id, external_transaction_id)` → identidade financeira da operação (impede reaplicar com outra chave).
- **Hash canônico** do payload de negócio: `sha256` sobre **JSON canônico** (chaves ordenadas, sem espaços, escala normalizada `"25.0"`→`"25.00"`, UTC RFC3339). **Exclui**: `Idempotency-Key`, `messageId`, `occurredAt`, headers, timestamps de transporte.
  - Implementação: encoder próprio que ordena chaves (`internal/platform/canonicaljson`) — determinístico e testável.
  - Mesmo algoritmo para HTTP e SQS (equivalência exigida no item 9). A chave no SQS é `data.idempotencyKey`.
- Fluxo: se já existe registro com a chave →
  - hash igual ⇒ **replay**: devolve resultado persistido (`status`, `balance` original, `failureCode`) com `idempotentReplay: true`;
  - hash diferente ⇒ **409 conflito**;
  - chave nova mas `(provider, externalTransactionId)` existente ⇒ **409** (não reaplica).
- Persistência em `wager_transactions.result_balance_minor`/`result_currency` → replay devolve o saldo **do processamento original**, mesmo que a carteira tenha mudado depois.
- Idempotência **nunca** só em memória (eliminatório).

### A6. Máquina de estados (`WagerTransaction`)
`PENDING → PROCESSED | REJECTED | FAILED | PENDING_REFERENCE` e `PENDING_REFERENCE → PROCESSED | REJECTED | FAILED`.
- Terminal (`PROCESSED`, `REJECTED`, `FAILED`) nunca transiciona de novo → erro `ErrTerminalState`.
- Transições validadas no **domínio** (métodos `MarkProcessed`, `MarkRejected`, `MarkFailed`, `WaitReference`), não no SQL.
- Transitória x permanente: errno/classificação do driver + códigos `pgx` (ex.: `40001` serialization, `40P01` deadlock, `57P03` cannot_connect_now, `53300` too_many_connections, timeouts de contexto) ⇒ **retry**; violação de regra de negócio ⇒ **REJECTED**; esgotou tentativas de retry de infra ⇒ **FAILED** (auditável).
- `PENDING` confirmado precisa de retomada durável por outra instância (item 6.3) → **não faremos aceite assíncrono por HTTP** (item 6.3 permite conclusão síncrona). O `PENDING` existe para: (a) referência ainda não resolvida (`PENDING_REFERENCE`), (b) registro criado na mesma transação e concluído logo em seguida (estado intermediário atômico, invisível fora da transação), (c) retomada pelo worker de pendências após crash se houver commit de aceite.
  - **Decisão:** operações sem dependência são **síncronas** (sem commit intermediário de aceite), conforme permitido. `PENDING` durável fica restrito a `PENDING_REFERENCE` + um *reaper* que retoma `PENDING` antigos (defesa em profundidade, satisfaz literalmente a exigência).

### A7. Referências e reversões
- `REFUND`/`ROLLBACK` resolvem a referência por `(provider_id, external_transaction_id)`.
- Precisa concordar em **provedor, jogador, carteira, moeda e rodada** e **valor igual** (sem reversão parcial).
- `REFUND` só referencia `BET` processada. `ROLLBACK` só referencia `BET`/`WIN`/`REFUND` processada.
- Sem referência ainda ⇒ grava `PENDING_REFERENCE`, emite `WagerTransactionPendingReference`, e o **worker de referências** reprocessa com **backoff exponencial** (base 5s, fator 2, jitter, teto 10min), persistido em `next_attempt_at` (sobrevive a restart). Máx. **24h/20 tentativas** → `REJECTED` com `REFERENCE_NOT_FOUND` + evento `WagerTransactionRejected`.
  - Referência existe mas ainda `PENDING`/`PENDING_REFERENCE` ⇒ continua pendente (nova tentativa).
  - Referência `REJECTED`/`FAILED` ⇒ `REJECTED` com `REFERENCE_NOT_PROCESSABLE` (definitivo).
- **Dupla reversão bloqueada por constraint**: `UNIQUE (reference_transaction_id, kind) WHERE reference_transaction_id IS NOT NULL` → REFUND e ROLLBACK convivem, dois REFUND não.
- Coerência REFUND+ROLLBACK sobre a mesma BET: aceito (um de cada), nunca devolvendo o mesmo débito duas vezes. Documentado em `ARCHITECTURE.md`.
- `ROLLBACK` que exigiria débito > saldo ⇒ `REJECTED` com `ROLLBACK_INSUFFICIENT_FUNDS` (**código diferente** de `INSUFFICIENT_FUNDS` da aposta — exigido no item 7).

### A8. Códigos de falha (`failureCode` estáveis)
| Código | Situação | Corrigível? |
| --- | --- | --- |
| `INVALID_REQUEST` | schema/valor inválido (400) | sim |
| `INVALID_MONEY` | formato/valor de dinheiro inválido | sim |
| `CURRENCY_MISMATCH` | moeda ≠ moeda da carteira | sim |
| `IDEMPOTENCY_KEY_REQUIRED` | header/`idempotencyKey` ausente | sim |
| `IDEMPOTENCY_CONFLICT` | mesma chave, payload/hash diferente | não |
| `REFERENCE_REQUIRED` | reversão sem `referenceExternalTransactionId` | sim |
| `REFERENCE_NOT_FOUND` | referência não existe (TTL esgotado) | não |
| `REFERENCE_NOT_PROCESSABLE` | referência existe mas rejeitada/falha | não |
| `REFERENCE_MISMATCH` | referência diverge em provedor/jogador/carteira/moeda/rodada | não |
| `INSUFFICIENT_FUNDS` | `BET` sem saldo | sim (com saldo) |
| `ROLLBACK_INSUFFICIENT_FUNDS` | `ROLLBACK` sem saldo | não |
| `OPERATION_NOT_ALLOWED` | `OPENING` via HTTP/SQS, tipo desconhecido | sim |
| `ALREADY_REVERSED` | referência já revertida para o mesmo tipo | não |
| `WALLET_NOT_FOUND` | carteira inexistente | sim |
| `WALLET_CONFLICT` | abrir carteira duplicada `(playerId,currency)` | não |
| `LOSS_AMOUNT_NOT_ZERO` | `LOSS` com valor ≠ 0.00 | sim |
| `PROCESSING_FAILED` | falha permanente de infra | não |

### A9. HTTP — contrato de respostas
| Situação | Status | Corpo |
| --- | --- | --- |
| Nova operação processada | `201` | `{transactionId,status,balance,idempotentReplay:false}` |
| Replay idempotente | `200` | idem com `idempotentReplay:true` (saldo original) |
| `PENDING_REFERENCE` | `202` | `{transactionId,status:"PENDING_REFERENCE",failureCode:null}` |
| `PENDING` em andamento | `202` | `{transactionId,status:"PENDING"}` |
| Rejeição de negócio | `422` | `{transactionId,status:"REJECTED",failureCode}` |
| Entrada inválida | `400` | `{error:{code,failureCode,message}}` |
| Conflito (chave/hash/carteira) | `409` | `{error:{code,failureCode,message}}` |
| Sem/inválido token | `401` | `{error:{code:"UNAUTHORIZED"}}` |
| Provedor errado / operação interna | `403` | `{error:{code:"FORBIDDEN"}}` |
| Indisponibilidade transitória | `503` + `Retry-After` | `{error:{code:"SERVICE_UNAVAILABLE"}}` |
| Erro inesperado | `500` | `{error:{code:"INTERNAL"}}` |

- `POST /wallets`: `201`; duplicado `409`; `initialBalance` zero ⇒ carteira sem `OPENING`/ledger/eventos.
- Ledger paginado: cursor opaco `base64url(createdAt RFC3339Nano | entryId)` + `ORDER BY created_at, id` estável; `limit` default 50, máx 200; resposta com `nextCursor`.
- `GET /providers/:providerId/...`: `:providerId` **tem** que bater com o claim do token, senão `403`.
- `Idempotency-Key` obrigatório em `POST /wagering/transactions`; **nunca** substituída silenciosamente por uma calculada.

### A10. Autenticação e autorização (Keycloak)
- Realm `jungleg`, dois clients `client_credentials`:
  - `provider-service`: role `provider`; token com claim `provider_id` (protocol mapper).
  - `internal-service`: role `internal`; usado pelo worker SQS/app→app.
- Middleware `auth`: discovery **OIDC** do realm, JWKS com cache e rotação, valida `iss`, `aud`, `exp`, `nbf`, assinatura `RS256`.
- Regras:
  - `POST /wagering/transactions`, `GET /providers/:providerId/...` → role `provider` **ou** `internal`; se `provider`, `providerId` do token = `providerId` do corpo/rota (**isolamento entre provedores**, inclusive em replay e leitura).
  - `POST /wallets` (**abertura interna**), `GET /wallets*`, `POST /wallets/:id/reconciliation` → apenas role `internal` (provedor recebe `403` sem efeito financeiro).
  - Auditoria de `providerId` do token vs. payload em **toda** consulta/replay.
- `/health/live`, `/health/ready`, `/metrics` públicos.
- Provisionamento automático: `deploy/keycloak/realm.json` importado com `--import-realm` (realm, clients, secrets de exemplo, roles, usuários de teste). Comando de obtenção de token documentado no README.
- Falha do IdP indisponível ⇒ `503` (não `500`) e **nenhum efeito financeiro**.

### A11. Inbox / Outbox
**Inbox** (`inbox_messages`): `UNIQUE (consumer_name, message_id)`, `body_hash`, `received_at`, `completed_at`, `status`, `failure_code`.
- Fluxo do consumidor **em uma transação**: `INSERT inbox` → (duplicado? compara `body_hash`: igual ⇒ pula; diferente ⇒ rejeita/audita) → processa domínio → grava outbox → `UPDATE inbox.completed_at` → **commit** → **só então** `DeleteMessage`.
- Falha transitória ⇒ não deleta, visibility timeout devolve a mensagem (retry com backoff).
- Rejeição de negócio ⇒ marca `inbox` concluída como rejeitada e **deleta** (terminal).
- Mensagem inválida (JSON inválido, schema, `kind=OPENING`) ⇒ `inbox` reaproveitada + `fail` após N tentativas ⇒ **DLQ** (`wager-transactions-dlq.fifo`), sem efeito financeiro.
- Referência pendente: a mensagem é concluída **após** persistir `PENDING_REFERENCE` (item 6.5) — o worker de referências assume.

**Outbox** (`outbox_events`): `event_id` (estável, PK), `aggregate_id`, `event_type`, `event_version`, `payload` (snapshot imutável), `occurred_at`, `correlation_id`, `causation_id`, `attempts`, `next_attempt_at`, `published_at`, `locked_by`, `locked_until`.
- Publisher: `SELECT ... WHERE published_at IS NULL AND next_attempt_at<=now() AND (locked_until IS NULL OR locked_until<now()) ORDER BY occurred_at FOR UPDATE SKIP LOCKED LIMIT n` → marca lease (`locked_by`,`locked_until`) → **commit** → publica → `UPDATE published_at=now()` (nova transação).
- Recuperação de trabalho abandonado: lease expirado volta a ser elegível (crash entre commit e publicação, e entre publicação e confirmação).
- Backoff exponencial + jitter em `next_attempt_at`; `attempts` incrementa; esgotou ⇒ alerta/métrica (mantém para intervenção, `attempts` alto).
- Republicação preserva `eventId` (SQS FIFO dedup por `MessageDeduplicationId=eventId`, além de sink idempotente).
- **Nunca** publicar antes do commit (eliminatório). Só o publisher toca no SQS de saída.

### A12. Eventos
Envelope: `eventId`, `eventType`, `aggregateId`, `correlationId`, `causationId?`, `occurredAt`, `version`, `data` tipado.
| `eventType` | Gatilho | `MessageGroupId` | Payload `data` |
| --- | --- | --- | --- |
| `WagerTransactionProcessed` | sucesso (inclui `LOSS`) | `walletId` | ids, kind, provider, round, game, money, status, balanceAfter? |
| `WagerTransactionRejected` | rejeição definitiva | `walletId` | ids, kind, failureCode, money |
| `WalletBalanceChanged` | mudança efetiva de saldo | `walletId` | `walletId,transactionId,direction,money,balanceBefore,balanceAfter,walletVersion` |
| `WagerTransactionPendingReference` | espera por referência | `walletId` | ids, kind, referenceExternalTransactionId, attempt, nextAttemptAt |
- `LOSS` e rejeições: **sem** `WalletBalanceChanged`. Tipo/versão definidos pelo construtor do evento.
- Fila de saída provisionada: `wallet-events.fifo` (+ DLQ `wallet-events-dlq.fifo`) — destino documentado no README.
- Filas de entrada: `wager-transactions.fifo` (`MessageGroupId=walletId` para ordenar por carteira; `MessageDeduplicationId=idempotencyKey`), DLQ `wager-transactions-dlq.fifo` com redrive `maxReceiveCount=5`.
- `content-based dedup` **não** é usado (dedup persistente é a nossa).

### A13. Observabilidade
- `log/slog` JSON handler; campos sempre que disponíveis: `correlationId`, `messageId`, `transactionId`, `walletId`, `providerId`, `eventId`, `kind`, `failureCode`, `durationMs`.
- **Nunca** logar token, secret, payload financeiro completo nem `Authorization`.
- Métricas Prometheus: `wager_operations_total{kind,status}`, `wager_idempotent_replays_total`, `wager_inbox_duplicates_total`, `wager_retries_total{stage}`, `wager_dlq_messages_total`, `wager_tx_conflicts_total`, `outbox_pending_age_seconds` (histograma/gauge), `outbox_publish_total{result}`, `wager_processing_duration_seconds{kind,status}`, `wallet_reconciliation_divergences_total`.
- `/health/live` (processo vivo) e `/health/ready` (ping PostgreSQL + `GetQueueAttributes`/`ListQueues` no SQS, com timeout de 2s).

### A14. Fx / ciclo de vida
- Módulos: `config`, `observability`, `postgres`, `sqs`, `auth`, `repository`, `usecase`, `http`, `worker`, `app`.
- `fx.Provide` apenas construtores; `fx.Invoke` para: registrar rotas, subir `http.Server`, iniciar workers, rodar migrations (flag).
- `fx.Lifecycle`:
  - `OnStart`: validar config → pingar dependências → migrations (opcional) → provisionar/iniciar servidor e workers.
  - `OnStop`: **parar de aceitar novas entradas** (`http.Server.Shutdown` com timeout) → cancelar contexto dos workers → aguardar conclusão dentro do prazo (ou liberar visibilidade das mensagens SQS para reentrega segura) → fechar pool SQS/pgx **depois** dos consumidores (`fx.StopTimeout` = 25s).
- `SIGTERM`/`SIGINT` via `fx.Shutdowner` / `signal.NotifyContext`.
- Domínio sem importar Fx, HTTP, SQS ou pgx (verificável por `go list -deps`).

---

## 2. ESTRUTURA DE PASTAS (alvo)

```
jungleg/
├── README.md                     # da solução (substitui/envelopa o do desafio) — ver §6
├── ARCHITECTURE.md               # decisões — ver §6
├── PLANO.md                      # este arquivo
├── .env.example
├── .gitignore
├── .golangci.yml                 # lint opcional (diferencial)
├── Makefile                      # up/down/test/test-race/vet/migrate/lint
├── Dockerfile                    # multi-stage, golang:1.25-alpine
├── docker-compose.yml            # postgres, keycloak, localstack(+init), app, migrator
├── go.mod / go.sum
├── cmd/
│   ├── api/main.go               # fx.New(app.Module) + Shutdowner
│   ├── migrate/main.go           # up/down/version/force
│   └── outbox-publisher/main.go  # (opcional) publisher isolado para provar 2 publishers
├── deploy/
│   ├── localstack/init-queues.sh # cria filas FIFO + DLQ + redrive
│   └── keycloak/realm.json       # realm, clients, roles, usuários, mappers
├── migrations/
│   ├── 000001_init.up.sql / .down.sql
│   └── ...                       # versionadas, com rollback documentado
├── internal/
│   ├── config/                   # env → Config + Validate()
│   ├── domain/
│   │   ├── money/                # money.go + money_test.go, parse_test.go, json_test.go, style_test.go
│   │   ├── wallet/               # wallet.go, ledger_entry.go, repository.go(port), errors.go
│   │   └── wagering/             # transaction.go, kind.go, status.go, failure.go, errors.go
│   ├── app/
│   │   ├── ports/                # interfaces (uow, clock, idgen, publisher, inbox, outbox)
│   │   └── wagering/             # service.go (BET/WIN/LOSS/REFUND/ROLLBACK), opening.go, reconcile.go
│   ├── infra/
│   │   ├── postgres/             # pool.go, uow.go, wallet_repo.go, txn_repo.go, ledger_repo.go,
│   │   │                         # inbox_repo.go, outbox_repo.go, reconcile_repo.go, errors.go
│   │   ├── sqsx/                 # client.go, producer.go, consumer.go, queue_provisioner.go
│   │   └── auth/                 # oidc verifier, jwks cache, claims
│   ├── httpapi/                  # server.go, router.go, middleware/(auth,correlation,logging,recover,
│   │   │                         # metrics,timeout), handlers/(wallets,wagering,health,reconciliation),
│   │   │                         # dto/, problem.go (erros → status)
│   ├── worker/                   # outbox_publisher.go, reference_worker.go, pending_reaper.go,
│   │   │                         # sqs_consumer.go, runner.go (supervisão/erros)
│   ├── observability/            # logger.go, metrics.go, correlation.go
│   └── platform/                 # canonicaljson/, clock/, idgen/, retry/, cursor/
├── test/
│   ├── integration/              # //go:build integration (testcontainers)
│   └── e2e/                      # cenários de 3 instâncias, falhas, HTTP×SQS
└── docs/
    ├── ESTADO.md                 # log de progresso (o que fiz/andando/falta) — atualizado a cada passo
    └── evidencias/               # saídas de go test -race, curls, métricas (prova para o avaliador)
```

---

## 3. CHECKLIST MESTRE

Legenda: `[ ]` pendente · `[~]` em andamento · `[x]` feito e **validado por teste** · `[!]` bloqueado

### FASE 0 — Fundação ✅ CONCLUÍDA E VALIDADA COM INFRA REAL
- [x] `git init`, `.gitignore`, `.gitattributes` (força LF em scripts/SQL — CRLF quebra o shell no container)
- [x] `go mod init github.com/FeliPoxZ/jungle_desafio` + dependências + `go mod tidy`
- [x] `internal/config`: `Config` via env com `Validate()` única (falha o boot se inválida)
- [x] `.env.example` completo (sem segredos reais)
- [x] `Makefile` (todos os alvos) — **sem `make` instalado no Windows**; serve de documentação executável e o usuário roda os equivalentes
- [x] `Dockerfile` multi-stage (`CGO_ENABLED=0`, non-root, `TARGET` api/migrate)
- [x] `docker-compose.yml` com `postgres:16`, `keycloak:26.4`, `localstack:4.0.3`, `localstack-init`, `migrate`, `api`
- [x] `deploy/localstack/init-queues.sh`: 4 filas FIFO + 2 DLQs + redrive (`maxReceiveCount=5`) + visibility timeout
- [x] `deploy/keycloak/realm.json`: realm, 4 clients, roles `provider`/`internal`, mapper `provider_id`, audience, usuários de teste
- [x] `internal/observability`: slog JSON com correlação automática + 13 métricas Prometheus
- [x] `internal/httpapi`: middlewares, erros tipados, health, `/metrics`
- [x] `internal/app`: composição Fx + ciclo de vida + shutdown gracioso
- [x] **VALIDADO:** 3 containers `healthy`; 4 filas com atributos corretos; realm importado e **token real emitido** (`iss`/`aud`/`provider_id: provider-a`/`roles: provider`); bancos `jungleg`/`jungle_test`/`keycloak` criados
- [~] `internal/platform/clock` e `idgen` — **adiados para a Fase 2** (só serão usados lá)

**Evidências:** `docs/evidencias/FASE_0.md`

### FASE 1 — Domínio `Money` (D0, ~2h) 💰 núcleo — ✅ CONCLUÍDA E VALIDADA

- [x] `money.Currency`: validação ISO 4217 (3 letras maiúsculas ASCII), `BRL`, `Validate()`, `String()`
- [x] `money.New(minor int64, cur Currency) (Money, error)` — **criação**, valida moeda **e** a faixa `|minor| <= maxMinor` (README §Encapsulamento: "construtores com validação")
- [x] `money.FromMinor(minor int64, cur Currency) Money` — **reidratação**, sem revalidar (separa criação de reidratação, README §Encapsulamento L89). A validação na leitura fica no repositório (Fase 5) apoiada na `CHECK constraint` da coluna (Fase 4)
- [x] `money.Parse(amount, currency string) (Money, error)` — escala fixa 2, rejeita vazio/`NaN`/`Inf`/científica/`+`/`-`/espaços/vírgula/`>2` casas/overflow, **sem passar por ponto flutuante** (dígitos acumulados como dígitos)
- [x] `Zero(cur)`, `Add`, `Sub`, `Neg` (guarda de overflow **nos dois sentidos** em `Add` e `Sub`), `Cmp`, `CmpLess/CmpEqual/CmpGreater`, `Equal`, `Sign`, `IsZero`, `IsNegative`, `IsPositive`, `Minor()`, `Currency()`
- [x] `String()` → `"975.00"` (e sinal para negativo) + `MarshalJSON`/`UnmarshalJSON` no contrato `{"amount":"25.00","currency":"BRL"}`
- [x] Erros de domínio **no próprio `money.go`**: `Error{Code, Message}` + `Is()` por `Code` (com `errors.As`), sentinelas `ErrInvalidCurrency`, `ErrInvalidAmount`, `ErrCurrencyMismatch` e códigos `INVALID_CURRENCY`/`INVALID_AMOUNT`/`CURRENCY_MISMATCH`.
  - **Decisão revista:** os códigos planejados `ErrOverflow` e `ErrScaleExceeded` **não foram criados**. Limite e escala são ambos `INVALID_AMOUNT`, e a distinção fica na mensagem. Motivo: `failureCode` precisa ser estável e pequeno para o cliente distinguir "entrada corrigível × resultado definitivo" (item 13); dois códigos a mais só criariam ruído no contrato. Se a Fase 7 precisar diferenciar, o `Code` é o único ponto a mudar.
  - **Decisão revista:** `currency.go`/`errors.go` separados **não** foram criados. O pacote `money` tem 541 linhas de código e cabe em um arquivo com seções; arquivos por tipo só se o pacote crescer.
- [x] `UnmarshalJSON` estrito quanto à forma, além do valor: rejeita **campo desconhecido** (`DisallowUnknownFields`) e **chave repetida** (o `encoding/json` ficaria com a última em silêncio). Um typo em `amount` viraria cobrança de `R$ 0,00`.
- [x] **Serialização é assimétrica de propósito:** `MarshalJSON` de valor negativo **sai** com sinal (diferença interna, conciliação), mas `UnmarshalJSON` **recusa** sinal (README §6.1: negativo proibido na entrada financeira externa). Travado por teste.
- [x] Testes (384 casos, table-driven + testes de propriedade): parsing feliz, entradas inválidas, escala, limites (`±9_000_000_000_000_000`), overflow em `Add`/`Sub`/`Neg`, moedas incompatíveis, comutatividade, `(a+b)-b == a`, `a-b == a+(-b)`, anti-simetria e transitividade do `Cmp`, involução do `Neg`, round-trip JSON, **nenhum `float` no pacote** (`TestSemPontoFlutuanteNoCodigo` via AST: literais float + identificadores `float64`/`ParseFloat`/`NaN`/`Inf`).
- [x] `go test -race ./internal/domain/money/...` — verde; cobertura **97,9%** (o restante são guardas defensivas comprovadamente inalcançáveis, documentadas no código)
- [x] Conformidade README §6.1 e §Encapsulamento: **10 de 10 itens**

**Evidências:** `docs/evidencias/FASE_1.md`

### FASE 2 — Domínio `Wallet` + `LedgerEntry` (D0, ~2h) — ✅ CONCLUÍDA E VALIDADA

- [x] `Wallet`: campos privados (`id`, `playerID`, `currency`, `balance`, `version`, `createdAt`, `updatedAt`) + acessores
- [x] `NewWallet(id, playerID, initial, now)` → version `1`, valida id/playerID/moeda/saldo não negativo/instante; separado de `Rehydrate(...)` (**sem** reaplicar movimentações, sem mexer em versão/instantes e sem emitir evento — README §Encaps. L89)
- [x] `Debit(m, now)`, `Credit(m, now)` → validam moeda da carteira **antes** da aritmética; débito nunca deixa saldo negativo (`ErrInsufficientFunds`); incrementam `version` e `updatedAt` **apenas** quando o saldo muda; devolvem o `Entry` correspondente
- [x] `apply(direction, amount, now)` é o **único** ponto que muda saldo — `Debit`/`Credit` só escolhem a direção
- [x] Movimentação de valor **zero**: não muda saldo, não muda versão e **não gera lançamento** (mesma regra do `LOSS`, README §7). Descoberto durante os testes: a primeira versão tentava criar lançamento de zero
- [x] `Direction` (`DEBIT`/`CREDIT`) com `ParseDirection`/`Validate`/`String`
- [x] `Entry` (ledger append-only, imutável, **sem setters**): `NewEntry` calcula e valida `balanceAfter = before ± amount`, exige valor **positivo** e devolve `ErrInvalidEntry`/`ErrWalletCurrencyMismatch`; `RehydrateEntry` reconstrói sem revalidar
- [x] Erros do pacote (`errors.go`): `Error{Code, Message}` + `Is()` por `Code`, sentinelas `ErrInvalidWallet`, `ErrInsufficientFunds`, `ErrWalletCurrencyMismatch`, `ErrInvalidEntry` com códigos `INVALID_WALLET`, `INSUFFICIENT_FUNDS`, `WALLET_CURRENCY_MISMATCH`, `INVALID_LEDGER_ENTRY` — **mesmo padrão do pacote `money`**
- [x] `(playerID, currency)` único: decisão de schema (constraint `UNIQUE`), verificada na Fase 4
- [x] Testes: **63 casos** (29 testes + 34 subtests), cobertura **99,0%** (a única linha descoberta é guarda inalcançável, documentada no código)
- [x] Propriedades: `Credit(x)+Debit(x)` volta ao saldo original com versão +2; nenhuma sequência aceita deixa saldo negativo; a soma dos lançamentos explica a diferença de saldo; falha não deixa estado pela metade
- [x] Casos defensivos com dados corrompidos via `Rehydrate` (saldo acima do teto, abaixo do piso, direção inválida) — o domínio recusa mesmo sem validar na leitura

**Evidências:** `docs/evidencias/FASE_2.md`

### FASE 3 — Domínio `WagerTransaction` (D0, ~2h) — ✅ CONCLUÍDA E VALIDADA

- [x] `Kind` (`OPENING/BET/WIN/LOSS/REFUND/ROLLBACK`) com `ParseKind`, `Validate`, `String`, `IsExternal`/`IsInternal`, `RequiresReference`, `AllowsZeroAmount`, `MovesMoney`, `ValidateAmount`
- [x] `Status` (`PENDING/PENDING_REFERENCE/PROCESSED/REJECTED/FAILED`) com `ParseStatus`, `Validate`, `IsTerminal`, `CanTransition`, `ValidateTransition`, `AllowedTransitions` — máquina de estados em tabela explícita, com **25 transições verificadas**
- [x] `New(...)` validando obrigatórios por origem: `OPENING` (interna) rejeita provedor e ID externo; operação externa exige provedor, ID externo, chave de idempotência e payload. **`OPENING` também exige chave de idempotência** — é o que impede crédito inicial duplicado
- [x] Transições validadas: `MarkProcessed`, `MarkRejected`, `MarkFailed`, `MarkPendingReference`, `ResolveReference`. `transicionar` é o **único** ponto que muda estado
- [x] Erro específico por causa: estado terminal devolve `ErrTerminalState`; transição fora de ordem devolve `ErrInvalidTransition` (códigos diferentes = `failureCode` diferentes)
- [x] `referenceExternalTransactionId` obrigatório p/ `REFUND`/`ROLLBACK` (`REFERENCE_REQUIRED`); valor `> 0` para BET/WIN/REFUND/ROLLBACK e `== 0` para LOSS
- [x] `failureCode` gravado na rejeição e na falha (`PROCESSING_FAILED` como padrão) + `resultBalance` + referência interna resolvida + tentativa e próximo instante de resolução
- [x] Eventos de domínio (A12): envelope com `eventId`, `eventType`, `aggregateId`, `correlationId`, `causationId?`, `occurredAt`, `version`, `data`; construtores `ProcessedEvent` (inclui **LOSS**), `RejectedEvent`, `PendingReferenceEvent` e `NewBalanceChangedEvent` que **recusa** `Entry` vazio (LOSS e rejeição não geram `WalletBalanceChanged`)
- [x] `PayloadEncoder` injetado em vez de `encoding/json` no domínio: o hash do payload vem de JSON canônico fornecido pela borda
- [x] Testes: **127 casos** (39 testes + 88 subtests), cobertura **100,0%**
- [x] `Rehydrate(...)` reconstrói estado, instantes e metadados **sem** reaplicar transição e **sem** emitir evento

**Evidências:** `docs/evidencias/FASE_3.md`

**O que a Fase 3 deliberadamente não faz:** movimentar dinheiro, criar lançamento, consultar idempotência existente, comparar metadados com a referência carregada e publicar evento. Tudo isso é Fase 5 (repositório) e Fase 6 (serviço) — a Fase 3 garante que a operação é **representável e permitida**, não que ela aconteceu.

### FASE 4 — Schema, migrations e constraints (D1, ~2h30) 🗄️
- [ ] `000001_init.up.sql`: `players`(se necessário), `wallets`, `wager_transactions`, `wallet_ledger_entries`, `inbox_messages`, `outbox_events`
- [ ] `000001_init.down.sql` (drop na ordem inversa)
- [ ] Constraints obrigatórias:
  - [ ] `wallets`: `UNIQUE (player_id, currency)`, `CHECK (balance_minor >= 0)`, `CHECK (version >= 1)`
  - [ ] `wallet_ledger_entries`: `CHECK (balance_after_minor = balance_before_minor + CASE direction WHEN 'CREDIT' THEN amount_minor ELSE -amount_minor END)`, `CHECK (amount_minor > 0)`, `UNIQUE (wallet_id, transaction_id)`, `UNIQUE (id)`, imutabilidade
  - [ ] `wager_transactions`: `UNIQUE (provider_id, idempotency_key)`, `UNIQUE (provider_id, external_transaction_id)`, `UNIQUE (reference_transaction_id, kind) WHERE reference_transaction_id IS NOT NULL`, `UNIQUE (idempotency_key) WHERE kind='OPENING'` (impede crédito inicial duplicado), `CHECK (kind <> 'OPENING' OR provider_id IS NULL)`
  - [ ] `inbox_messages`: `UNIQUE (consumer_name, message_id)`
  - [ ] `outbox_events`: `UNIQUE (event_id)`, `CHECK (attempts >= 0)`
- [ ] Imutabilidade do ledger **no banco**: trigger `BEFORE UPDATE OR DELETE ... RAISE EXCEPTION` (+ `GRANT` restrito, documentado)
- [ ] Índices: `wallets(player_id)`, `wallet_ledger_entries(wallet_id, created_at DESC, id DESC)`, `wager_transactions(provider_id, external_transaction_id)`, `wager_transactions(status, next_attempt_at)`, `outbox_events(published_at, next_attempt_at, occurred_at)`
- [ ] `wager_transactions` distingue origem interna/externa (`origin` gerada/`CHECK`)
- [ ] `cmd/migrate` (up/down/version/force) com `iofs` embed + documentação de reversão
- [ ] **Validar:** container Postgres + `migrate up`/`down`/`up` + testes de constraint que **esperam erro** (saldo negativo, ledger duplicado, update/delete no ledger, chave duplicada)

### FASE 5 — Persistência (pgx) + Unit of Work (D1, ~4h)
- [ ] `postgres.NewPool` (`pgxpool`) com timeouts e `MaxConns`; `Ping` para readiness
- [ ] `uow.Run(ctx, fn func(ctx, Repos) error)` → `BeginTx` (`Serializable`? **Não**: `ReadCommitted` + `FOR UPDATE`, justificar), `Commit`/`Rollback` com `defer`, contexto com timeout
- [ ] `WalletRepo`: `Insert`, `GetByID`, `GetByIDForUpdate` (**row lock**), `GetByPlayerAndCurrency`, `UpdateBalance(id, newBalance, newVersion, expectedVersion?)`
- [ ] `TxnRepo`: `Insert`, `GetByID`, `GetByProviderAndExternalID`, `GetByIdempotencyKey`, `MarkProcessed/MarkRejected/MarkFailed/WaitReference`, `ListPendingReferencesDue(now)`, `ListStalePending`, `ListByWallet` (cursor)
- [ ] `LedgerRepo`: `Insert` (append-only), `ListByWalletCursor`, `SumSignedByWallet` (reconciliação)
- [ ] `InboxRepo`: `InsertIfAbsent`, `Get`, `MarkCompleted`, `MarkFailed`
- [ ] `OutboxRepo`: `Insert`, `ClaimBatch` (`FOR UPDATE SKIP LOCKED` + lease), `MarkPublished`, `MarkRetry`, `PendingStats`
- [ ] Classificação de erros: `errors.go` mapeando `pgx`/`pgconn.PgError` (`23505` unique, `23514` check, `40001`, `40P01`, `57P03`, `53300`) → `ErrConflict`, `ErrTransient`, `ErrPermanent`
- [ ] Testes de integração (testcontainers): cada repo, `SKIP LOCKED` com 2 conexões, lost update, atomicidade (rollback não deixa resíduo)

### FASE 6 — Caso de uso (aplicação) `wagering.Service` (D1, ~4h) 🧠 coração
- [ ] `Process(ctx, Request) (Result, error)`: **1 transação** para idempotência + domínio + ledger + outbox + inbox
  1. valida `Idempotency-Key`; calcula hash canônico; `SELECT` por chave/externalID
  2. replay (hash igual) → resultado persistido; conflito (hash diferente / externalID com outra chave) → `409`
  3. carrega carteira `FOR UPDATE` (valida player/moeda)
  4. resolve referência (REFUND/ROLLBACK) → ausente: `PENDING_REFERENCE` + evento + `202`; presente: valida concordâncias/valor/dupla reversão
  5. aplica movimentação no agregado (`Debit`/`Credit`), cria `LedgerEntry`, `UPDATE` saldo+versão
  6. `INSERT` transação `PROCESSED`/`REJECTED` + resultado + `failureCode`
  7. `INSERT` outbox (`Processed`/`Rejected`/`WalletBalanceChanged` / `PendingReference`)
  8. commit → só então o chamador pode publicar (publisher lê a outbox; **nunca** publicamos inline)
- [ ] `OpenWallet(ctx, req)` (interno): cria carteira + `OPENING PROCESSED` + crédito + 2 eventos **no mesmo commit**; zero ⇒ sem `OPENING`/ledger/eventos
- [ ] `GetTransaction`, `GetByProviderExternalID` (com checagem de provedor), `GetWallet`, `ListLedger(cursor)`, `Reconcile(walletID)`
- [ ] `Reconcile`: soma do ledger (incluindo `OPENING`) vs. saldo armazenado em **visão consistente** (`REPEATABLE READ`/snapshot único) → `difference = stored - calculated`, `checkedEntries`; divergência ⇒ log + métrica + resposta; **não altera saldo**
- [ ] Política de retry para `ErrTransient` (máx 3 tentativas, backoff curto) → esgotou = `FAILED` auditável
- [ ] Testes unitários com **fakes em memória** (ports): cada tipo de operação, replay, conflito, referência ausente, dupla reversão, rollback sem saldo (`ROLLBACK_INSUFFICIENT_FUNDS`), `LOSS` sem ledger, `OPENING`
- [ ] Testes de integração: mesmo `Process` contra Postgres real; 100% dos caminhos de erro com `failureCode` correto

### FASE 7 — HTTP + Auth (D1 noite → D2 manhã, ~4h)
- [ ] `chi` router + middlewares: `Recoverer`, `correlationID`, `logging` (slog JSON), `metrics`, `timeout`, `auth`
- [ ] `problem.go`: mapeamento erro→status conforme A9 (nunca vazar stack/erro de driver)
- [ ] `POST /wallets` (role `internal`), `GET /wallets/:id`, `GET /wallets/:id/ledger?cursor&limit`, `POST /wallets/:id/reconciliation` (role `internal`)
- [ ] `POST /wagering/transactions` (+ header `Idempotency-Key` obrigatório) e `GET /wagering/transactions/:id`
- [ ] `GET /providers/:providerId/wagering/transactions/:externalTransactionId` com isolamento por token
- [ ] `GET /health/live`, `GET /health/ready`, `GET /metrics`
- [ ] DTOs com validação estrita (`DisallowUnknownFields`, limites de tamanho de corpo, `Content-Type` obrigatório)
- [ ] Auth: OIDC discovery + JWKS cache/rotação; valida `iss/aud/exp/nbf/alg`; roles e `provider_id` do token; `401/403` conforme A9; **IdP fora ⇒ 503 sem efeito financeiro**
- [ ] Testes: httptest com servidor OIDC de teste (assinatura real) — token ausente/inválido/expirado/role errada/provedor errado; isolamento em replay e leitura; nenhum efeito financeiro em acesso negado

### FASE 8 — SQS: consumidor, inbox, retry, DLQ (D2, ~4h)
- [ ] `sqsx.Producer`: `SendMessage` FIFO (`MessageGroupId`, `MessageDeduplicationId`, `MessageAttributes` com `eventType`/`eventId`)
- [ ] `sqsx.QueueProvisioner`: cria filas/DLQ/redrive se ausentes (usado por `localstack-init` e por flag de boot); usado no readiness
- [ ] `sqsx.Consumer`: long polling (`WaitTimeSeconds=20`), batch, prefetch **limitado** (semáforo = `MaxConcurrency`), `VisibilityTimeout` compatível com o processamento
- [ ] Fluxo inbox (A11) compartilhando **o mesmo** `wagering.Service` do HTTP
- [ ] Retry: transitório ⇒ não deleta (backoff por visibility timeout) + `ChangeMessageVisibility` para backoff explícito; `attempts` da inbox incrementa
- [ ] DLQ: `ApproximateReceiveCount > 5` ou erro permanente ⇒ mensagem permanece/`DeleteMessage` conforme política + métrica `wager_dlq_messages_total`
- [ ] `SIGTERM`: para de buscar (`ReceiveMessage` cancelado), conclui o lote em andamento dentro do prazo (`fx.StopTimeout`), senão **libera visibilidade** (`ChangeMessageVisibility 0`) para reentrega segura
- [ ] Testes de integração: reentrega da mesma mensagem (`messageId` igual → inbox dedup), hash divergente em reentrega, rejeição terminal deleta, transitório reentrega, mensagem inválida → DLQ, concorrência HTTP×SQS para a mesma operação

### FASE 9 — Outbox publisher + worker de referências (D2, ~3h)
- [ ] `worker.OutboxPublisher`: loop com `ClaimBatch`/lease/publish/`MarkPublished`/`MarkRetry`; N publishers concorrentes; recuperação de lease expirado; republicação com mesmo `eventId`
- [ ] `worker.ReferenceWorker`: busca `PENDING_REFERENCE` vencidas (`next_attempt_at <= now`, `FOR UPDATE SKIP LOCKED`), tenta resolver, backoff exponencial + jitter, TTL/máx. tentativas → `REJECTED REFERENCE_NOT_FOUND` + evento
- [ ] `worker.PendingReaper`: retoma `PENDING` antigos (segurança extra item 6.3)
- [ ] `worker.Runner`: supervisão com `errgroup`, backoff em falha de loop, logs e métricas; shutdown limpo
- [ ] Testes: 2 publishers disputando a mesma outbox (nenhuma publicação duplicada perdida; disputa provada); crash entre commit e publish; crash entre publish e `MarkPublished` (republica com mesmo `eventId`); REFUND antes da referência ⇒ resolve depois; expiração ⇒ `REJECTED`

### FASE 10 — Concorrência e recuperação (D2, ~3h) 🏁 nota pesada (20 + 15 + 15)
- [ ] **T1** mesma aposta 50× em paralelo → **um** débito, saldo correto, replays devolvem mesmo resultado
- [ ] **T2** saldo 100.00 + duas BET 80.00 simultâneas → 1 `PROCESSED`, 1 `REJECTED/INSUFFICIENT_FUNDS`, saldo 20.00, **1** lançamento; reenvios não alteram
- [ ] **T3** carteiras distintas em paralelo (provar paralelismo: tempo/locks distintos + ausência de lock global)
- [ ] **T4** cenários repetidos com **3 instâncias independentes** (3 containers `api` no compose com portas distintas, conexões/memória próprias)
- [ ] **T5** interromper consumidor **depois do commit e antes do delete** → reentrega ⇒ nenhuma movimentação duplicada
- [ ] **T6** dois publishers disputando a outbox ⇒ recuperação de publicação
- [ ] **T7** `REFUND`/`ROLLBACK` antes da referência ⇒ resolução posterior **e** rejeição por expiração
- [ ] **T8** reiniciar app ⇒ idempotência, pendências e consistência preservadas; interromper após `PENDING` confirmado (se houver) e outra instância retoma
- [ ] Conferência final: saldo armazenado == soma de créditos − débitos do ledger; cenários cruzando HTTP e SQS
- [ ] `go test -race ./...` verde nas suítes aplicáveis

### FASE 11 — Observabilidade e polimento (D2, ~2h)
- [ ] Logs JSON com todos os IDs; nenhum segredo/payload financeiro completo
- [ ] Todas as métricas de A13 expostas e com teste mínimo
- [ ] `go vet ./...`, `gofmt -l .` vazio, `go mod tidy` limpo
- [ ] Teste da composição Fx: `fx.ValidateApp`, start/stop, `fx.StopTimeout`, liberação de recursos dos workers (sem goroutine leak: `goleak` opcional)
- [ ] Revisão anti-eliminatórios (§4 abaixo) item por item, com evidência

### FASE 12 — Documentação e entrega (D2, ~2h)
- [ ] `README.md`: pré-requisitos, variáveis de ambiente, **inicialização das filas**, migrations (aplicar e reverter), execução, exemplos `curl` autenticados, comandos de teste, provisionamento do IdP + identidades de teste, execução de integração/múltiplas instâncias/simulações de falha, build tags
- [ ] `.env.example`
- [ ] `ARCHITECTURE.md`: dinheiro, transações, idempotência (hash/algoritmo/campos/normalizações), locks/concorrência, referências pendentes, reversões (matriz REFUND/ROLLBACK), inbox/outbox, autenticação/autorização, Fx/ciclo de vida/shutdown, máquina de estados + transitório×permanente, HTTP (status/corpos), SQS (MessageGroupId/DeduplicationId/visibility/limites/DLQ), **limitações, interpretações e trabalho não concluído**
- [ ] `docs/evidencias/`: saídas de `go test -race`, curls, `/metrics` (prova para o avaliador)
- [ ] `docker compose up --build` do zero em clone limpo funciona (teste real!)

---

## 4. CHECKLIST DOS ELIMINATÓRIOS (revisar antes de entregar)

| # | Eliminatório | Como provamos |
| --- | --- | --- |
| E1 | Sem autenticação efetiva nos endpoints de negócio | testes com servidor OIDC real (401/403) + middleware cobrindo todos os endpoints de negócio |
| E2 | Acesso não autorizado a operações/transações | teste de isolamento entre provedores (leitura, replay, `:providerId` divergente) |
| E3 | Cálculo monetário em ponto flutuante | `int64` minor units; auditoria `grep -Rn "float32\|float64" internal/` vazia; teste de parse com `NaN`/`Inf` |
| E4 | Saldo negativo por concorrência | `CHECK (balance_minor >= 0)` + `FOR UPDATE` + T2/T1 |
| E5 | Movimentação duplicada | constraints únicas + replay persistido + T1/T2/T5/T7 |
| E6 | Idempotência só em memória | tabelas/constraints + T8 (restart) |
| E7 | Depender de uma única instância | T3/T4 (3 instâncias, workers com lease/SKIP LOCKED) |
| E8 | Publicação antes do commit | outbox transacional + publisher externo; teste de crash entre commit e publish |
| E9 | Ausência de ledger auditável | `wallet_ledger_entries` append-only + trigger + reconciliação |
| E10 | Substituir PostgreSQL/SQS/IdP por mocks nos testes | integração com testcontainers (containers reais) ou compose real |

---

## 5. CRONOGRAMA DE 2 DIAS

### DIA 1
| Janela | Foco | Entregável verificável |
| --- | --- | --- |
| 0h–1h30 | Fase 0 | `go build ./...` OK, compose de infra sobe, filas criadas |
| 1h30–3h30 | Fase 1 | `go test -race ./internal/domain/money/...` verde |
| 3h30–5h30 | Fases 2 e 3 | domínio completo + testes verdes |
| 5h30–8h | Fase 4 | migrations up/down + testes de constraint |
| 8h–12h | Fase 5 | repositórios + UoW + integração (testcontainers) |

### DIA 2
| Janela | Foco | Entregável verificável |
| --- | --- | --- |
| 0h–4h | Fase 6 | `wagering.Service` completo + unit + integração |
| 4h–8h | Fase 7 | API + auth com testes de isolamento |
| 8h–12h | Fase 8 | consumidor SQS + inbox + DLQ |
| 12h–15h | Fase 9 | outbox publisher + reference worker |
| 15h–18h | Fase 10 | T1–T8, `-race` |
| 18h–20h | Fases 11 e 12 | observabilidade + docs + evidências |

> **Regra de ouro:** ao fim de cada janela, o commit tem que deixar `go build ./... && go test ./...` verde. Nunca acumular código quebrado.
> **Corte de escopo se atrasar** (nesta ordem): `LOSS`+`reconciliation` simplificados → tracing/OTel (já é opcional) → load tests (opcional) → `PendingReaper`. **Nunca** cortar: auth, idempotência, ledger, constraints, outbox, T1/T2.

---

## 6. CONVENÇÕES DE CÓDIGO (padrão que vamos seguir)

- `context.Context` como primeiro parâmetro em todo I/O; `defer rollback` idiomático; erros envolvidos com `%w`.
- Erros de domínio: tipos + sentinelas, `errors.Is/As`, **nunca** `panic` para rejeição de negócio.
- Construtores validam; campos privados; zero value inválido ⇒ erro (nunca silencioso).
- Interfaces pequenas e definidas no **consumidor** (`ports`), não no fornecedor.
- Nada de variável global mutável; dependências injetadas por construtor (Fx).
- `Money` sempre por valor (é value object imutável); `*Wallet` quando há mutação.
- Nomes de tabela/coluna em `snake_case`; colunas de dinheiro terminam em `_minor` + `_currency`.
- Comentários de "porquê", não de "o quê". Documentar toda decisão não óbvia também no `ARCHITECTURE.md`.
- Testes: `t.Run` com nomes descritivos em tabela, `t.Parallel()` onde seguro, `-race` na CI, helpers `testdata` para payloads.
- Build tags: unit (padrão) e `//go:build integration` (containers reais) — comandos documentados.

---

## 7. LOG DE PROGRESSO (atualizar a cada passo)

Formato: `[data] [fase] status — o que foi feito / decisão / bloqueio`.

- **D0** — Fase 0 (parcial) — Workspace destravado (ACL do Windows). Toolchain verificada: Go 1.26.5 ✅, git ✅, **Docker ausente ❌ (B1)**. Plano, decisões A1–A14 e checklist criados neste arquivo.
- **D0** — **Fase 0 CONCLUÍDA** (commit `1eef5b2`) — módulo `github.com/FeliPoxZ/jungle_desafio`; 14 dependências fixadas; `Dockerfile` multi-stage com `TARGET` (api/migrate); `docker-compose.yml` com PostgreSQL 16 + Keycloak 26.4 + LocalStack + migrations + api; `deploy/localstack/init-queues.sh` (4 filas FIFO + DLQ + redrive idempotente); `deploy/keycloak/realm.json` (3 clients + `jungleg-api` como audience, roles `provider`/`internal`, claim `provider_id`, usuários de teste); `Makefile`; `.env.example`; `.gitignore`; `internal/config` com validação única; `internal/observability` (slog com correlação automática + métricas Prometheus); `internal/httpapi` (middlewares, erros tipados, health, /metrics); `internal/app` (composição Fx + ciclo de vida + shutdown gracioso).
  - **Testes verdes:** `config` (11 casos inválidos + defaults), `httpapi` (health/ready/down, correlação, timeout, panic→500, 8 mapeamentos de erro, Retry-After), `observability` (correlação, redact, 13 métricas, nil-safe), `app` (grafo Fx + falha de config).
  - **Smoke test real:** aplicação subiu em `:8099`, respondeu `/health/live` `{"status":"UP"}`, `/health/ready`, `/metrics` (métricas `jungleg_*` expostas) e `404` em rota inexistente; logs com `correlationId`/`requestId` automáticos.
  - **Decisões de ambiente novas:** cache de módulos do Go movido para `.gocache/` DENTRO do workspace (o cache global `C:\Users\PoxZe\go` não é gravável nesta máquina) — o `Makefile` e o `.gitignore` já refletem isso.
  - **Achado a resolver:** existe `E:\jungleg\jungle_desafio\` — um SEGUNDO repositório git (clone vazio do GitHub, só `.gitattributes`). Precisa ser removido ou o workspace precisa ser reorganizado; um repo dentro de outro impede o commit do conteúdo.
- **B1 segue ABERTO** — Docker Desktop ainda não instalado; nada de compose/integração foi validado até agora (todo o código acima foi validado sem Docker).
- **D0** — **Fase 1 CONCLUÍDA** (commits `3a36aec`, `3172772` e `b046e6a`) — value object `Money` completo em `internal/domain/money/money.go` (541 linhas) + suíte em 4 arquivos de teste (2.090 linhas).
  - **Decisão de design revista (A2):** o `New` **valida a faixa**, não só a moeda. O plano dizia "reidratação, permite qualquer sinal" e o README §Encapsulamento L87/L91 pede "construtores com validação" e "valores de domínio inválidos devem ser rejeitados". A proposta original deixava uma porta por onde um `Money` fora da faixa entrava em silêncio. Resolvido separando em `New` (criação validada) e `FromMinor` (reidratação sem validação, conforme L89).
  - **Achados durante a Fase 1** (todos corrigidos e travados por teste): guarda do `Add` com um lado só (10 furos achados por varredura exaustiva de 81 combinações, todos com dois operandos negativos); guarda do `Sub` com sinal invertido; guarda do `Neg` invertida (`m.minor > -maxMinor` rejeitava quase todo valor válido); `gofmt` pendente; comentário do `maxAmountDigits` impreciso.
  - **Decisão de contrato registrada:** `UnmarshalJSON` é estrito quanto à forma — rejeita campo desconhecido e chave repetida. O `encoding/json` normal ignoraria os dois, e um typo em `amount` viraria `R$ 0,00` silencioso.
  - **Decisão de contrato registrada:** serialização **assimétrica** para negativos — `MarshalJSON` emite sinal (diferença interna / conciliação), `UnmarshalJSON` recusa (entrada externa). Documentado no código e travado por teste.
  - **Guardas inalcançáveis documentadas:** a guarda de overflow dentro do laço dos inteiros em `parseAmount` é inalcançável dado o portão de 15 dígitos (provado por exaustão); 3 guardas de forma em `rejectDuplicateKeys` são inalcançáveis a partir de `UnmarshalJSON`. Mantidas como defesa e comentadas no código — é a razão da cobertura estar em 97,9% e não 100%.
  - **Anti-float é teste automatizado:** `TestSemPontoFlutuanteNoCodigo` varre a AST do pacote (literais `FLOAT` + identificadores `float64`/`float32`/`ParseFloat`/`FormatFloat`/`NaN`/`Inf`) e `TestImportsPermitidos` mantém lista fechada de imports do núcleo (`bytes`, `encoding/json`, `errors`, `fmt`, `strings`). O item eliminatório do enunciado deixou de depender de revisão manual.
  - **Conformidade README §6.1 + §Encapsulamento: 10 de 10.** Números: **384 casos verdes** (65 testes, 319 subtests), `go test -race ./...` verde nos 5 pacotes, `go vet` e `gofmt` limpos.
- **D0** — **Fase 2 CONCLUÍDA** — carteira e ledger em `internal/domain/wallet` (`errors.go`, `wallet.go`, `ledger_entry.go`).
  - **Decisão de design:** `apply` é o **único** ponto que muda saldo; `Debit` e `Credit` só escolhem a direção. Uma regra, um caminho de mutação.
  - **Decisão de design:** a moeda é checada **antes** da aritmética, para o cliente receber `WALLET_CURRENCY_MISMATCH` em vez de `INSUFFICIENT_FUNDS`. A ordem define o `failureCode`.
  - **Achado durante os testes:** movimentação de valor zero tentava criar lançamento de ledger, e o `NewEntry` corretamente recusa valor zero. Resolvido no domínio: valor zero **não gera lançamento**, não muda versão e não muda `updatedAt` — mesma regra do `LOSS` (README §7).
  - **Decisão de design:** valor negativo é rejeitado na movimentação; o sinal é definido pela **direção**. Aceitar negativo inverteria a operação e furaria o saldo mínimo.
  - **Decisão de design:** `NewEntry` calcula o `balanceAfter` em vez de recebê-lo, então não existe entrada de ledger incoerente por erro de chamada.
  - **Números: 63 casos verdes** (29 testes, 34 subtests), cobertura **99,0%**, `go test -race ./...` verde nos 6 pacotes.
- **D0** — **Fase 3 CONCLUÍDA** — transação de aposta em `internal/domain/wagering` (`errors.go`, `kind.go`, `status.go`, `transaction.go`, `events.go`).
  - **Decisão de design:** `OPENING` **também** exige chave de idempotência. É o que impede crédito inicial duplicado — sem chave, duas aberturas para o mesmo jogador creditariam duas vezes. O plano previa apenas "identidade interna estável".
  - **Decisão de design:** `PayloadEncoder` é injetado em vez de o domínio importar `encoding/json`. O hash do payload precisa ser sempre do mesmo formato; quem chama fornece JSON canônico (`internal/platform/canonicaljson`, Fase 4/6). Elimina a fragilidade de hash dependente da ordem dos campos.
  - **Decisão de design:** `transicionar` é o **único** ponto que muda estado, mesma disciplina do `wallet.apply`. `ResolveReference` grava a referência **sem** mudar de estado: resolver e concluir são decisões diferentes.
  - **Decisão de design:** estado terminal devolve `ErrTerminalState`, transição fora de ordem devolve `ErrInvalidTransition`. Códigos diferentes porque a causa é diferente — e o `failureCode` do cliente precisa distinguir.
  - **Decisão de design:** `Kind.MovesMoney()` concentra a regra que a Fase 6 vai consultar para decidir se cria lançamento no ledger. A regra fica no domínio, não no serviço.
  - **Achado durante os testes:** a cobertura só chegou a 100% depois de testar os 13 acessores. Acessores sem teste são pontos cegos silenciosos — o repositório da Fase 5 depende deles.
  - **Números: 127 casos verdes** (39 testes, 88 subtests), cobertura **100,0%**, `go test -race ./...` verde nos 7 pacotes.

### Próximos 3 passos imediatos
1. **Nós:** Fase 4 — schema, migrations e constraints (`000001_init.up.sql`/`.down.sql`), onde as regras das Fases 2 e 3 passam a ser impostas pelo banco.
2. **Nós:** Fase 4 — schema e constraints (ledger imutável, `UNIQUE (player_id, currency)`, `CHECK (balance_minor >= 0)`), que é onde as regras da Fase 2 passam a ser impostas pelo banco.
3. **Usuário:** checkpoint de entendimento das Fases 1–2 antes da Fase 4 (a Fase 3 é sequência direta do domínio).

---

## 8. PONTOS DE ATENÇÃO / DÚVIDAS DE INTERPRETAÇÃO (resolver e documentar)

1. **`OPENING` e saldo zero**: zero ⇒ sem `OPENING`, sem ledger, sem eventos financeiros (itens 9 e 7). Carteira ainda é criada.
2. **`LOSS` processa sem ledger** mas produz `WagerTransactionProcessed`; **não** incrementa `version`.
3. **`WIN` com referência de aposta da mesma rodada**: opcional ("pode informar"). Trataremos como referência **opcional** que, se enviada, é validada (mesma rodada/provedor/jogador/carteira/moeda) e persistida.
4. **`REFUND`+`ROLLBACK` na mesma BET**: ambos permitidos (um de cada tipo), nunca dois do mesmo tipo (constraint). Documentar como não há dupla devolução do mesmo débito.
5. **Aceite assíncrono**: **não** implementaremos para operações sem dependência (permitido pelo item 6.3: "podem ser concluídas de forma síncrona, sem commit intermediário de aceite").
6. **Nível de isolamento**: `READ COMMITTED` + `SELECT ... FOR UPDATE` explícito (justificar em `ARCHITECTURE.md`); reconciliação em snapshot consistente.
7. **Moeda**: apenas `BRL` nos cenários principais, mas o tipo carrega moeda e há testes de incompatibilidade (exigido).
8. **Tracing OTel / partidas dobradas / load test**: diferenciais opcionais — só se sobrar tempo (nesta ordem de prioridade).
