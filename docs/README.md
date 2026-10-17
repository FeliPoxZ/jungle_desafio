# Jungle Desafio — como rodar, operar e testar

> **Este é o guia de execução da entrega** (o "README da solução" do enunciado). O
> enunciado do desafio está no [`../README.md`](../README.md) e **não foi alterado**.
> As decisões técnicas estão no [`ARCHITECTURE.md`](ARCHITECTURE.md); a explicação
> didática do código, em [`../docs_desafio/SISTEMA_EXPLICADO.md`](../docs_desafio/SISTEMA_EXPLICADO.md);
> o treino de entrevista, em [`../docs_desafio/ENTREVISTA.md`](../docs_desafio/ENTREVISTA.md).
>
> **Se você só quer ver rodando:** `docker compose up -d --build` e depois
> `curl http://localhost:8081/health/ready` — o resto está na §4.

---

## 1. O que é o sistema

É um serviço back-end em **Go** (com **Uber Fx**) que processa operações
financeiras de provedores de jogos — `BET`, `WIN`, `LOSS`, `REFUND` e `ROLLBACK` —
sobre carteiras de jogadores. A operação chega por **HTTP** (provedor autenticado
por OIDC) ou por **fila SQS FIFO**, e as duas portas usam **o mesmo caso de uso**,
com as mesmas garantias. Dinheiro é `int64` em unidades mínimas, com moeda ISO
4217: não existe `float` em nenhum caminho monetário.

Cada movimentação acontece dentro de **uma transação SQL**: o saldo da carteira
(com `SELECT ... FOR UPDATE` na linha da carteira), o lançamento no **ledger
append-only**, a transação com a máquina de estados, o registro de **inbox** (quando
veio da fila) e os **eventos da outbox** são confirmados no mesmo `COMMIT`. Se
qualquer passo falhar, nada fica: nem saldo, nem lançamento, nem evento.

A publicação dos eventos é feita por um **worker separado**, que lê a outbox e
publica no SQS *depois* do commit — o evento nunca sai antes de o fato existir no
banco, e nunca se perde por causa de uma queda entre o commit e a publicação. Um
worker de referências retoma operações que esperam uma referência ainda
inexistente (`PENDING_REFERENCE`), e um reaper cobre a linha que ficou presa em
`PENDING` depois de uma interrupção.

### O que ele garante (e onde a prova está)

| Garantia | Prova |
| --- | --- |
| Dinheiro nunca passa por `float` | `internal/domain/money` + teste de AST que varre os caminhos monetários + `verify_constraints.sql` |
| Idempotência persistente (sobrevive a reinício) | índice único parcial + hash canônico; `TestT8ReinicioPreservaIdempotenciaPendenciaEConsistencia` |
| Sem lost update, sem saldo negativo | `FOR UPDATE` + versão + `CHECK (balance_minor >= 0)`; `TestT2DuasApostasDe80Sobre100EmInstanciasDiferentes` |
| Ledger append-only | trigger de imutabilidade no schema; `deploy/postgres/verify_constraints.sql` |
| Evento só depois do commit | outbox na mesma transação; `TestQuedaEntreCommitEPublicacaoOutraInstanciaAssume` |
| Nenhuma operação aplicada duas vezes | inbox por `(consumer_name, messageId)` + chave financeira; `TestT1MesmaOperacao50VezesPelaFila` |
| Três instâncias independentes funcionam | `TestT1…`, `TestT2…`, `TestT3…`, `TestT6…`, `TestT7…` (processos reais) |
| Encerramento seguro | SIGTERM no container com `exit=0` e visibilidade liberada (`EVIDENCIAS.md` §3) |

A lista completa, com a camada que impõe cada garantia, está no
[`SISTEMA_EXPLICADO.md`](../docs_desafio/SISTEMA_EXPLICADO.md) (capítulo 5) e no
[`ARCHITECTURE.md`](ARCHITECTURE.md) §5, §6 e §13.

---

## 2. Pré-requisitos

| Item | Versão / observação |
| --- | --- |
| Go | **1.26** (`go.mod` declara `go 1.26.0` + `toolchain go1.26.5`; o Dockerfile usa `golang:1.26-alpine`) |
| Docker Engine / Docker Desktop | com Compose v2 (`docker compose`) |
| Portas livres | `5432` (PostgreSQL), `8080` (Keycloak), `4566` (LocalStack), **`8081`** (API) |
| `curl` e, opcionalmente, `jq` | para os exemplos autenticados |
| (Windows) PowerShell 5.1 | os comandos abaixo têm a versão equivalente; **não** há `make` |

Não é necessário instalar PostgreSQL, Keycloak ou LocalStack na máquina: os três
sobem no Compose.

---

## 3. Variáveis de ambiente

A configuração é lida **apenas do ambiente** (nunca de arquivo versionado) por
`internal/config` (`caarlos0/env`), e é **validada no boot**: configuração
inválida impede a aplicação de subir, com todos os problemas listados de uma vez.
O arquivo [`../.env.example`](../.env.example) tem **todos** os nomes com
comentários; copie para `.env` se rodar a aplicação no host.

**Obrigatórias:** `DATABASE_URL` e `OIDC_ISSUER`. As demais têm default — e os
defaults são os valores de desenvolvimento abaixo.

### Aplicação e HTTP

| Variável | Default | Para que serve |
| --- | --- | --- |
| `APP_ENV` | `local` | `local` (log em texto) ou `production` (log JSON). `test` é usado pela suíte |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `DATABASE_URL` | — | DSN do PostgreSQL (obrigatória) |
| `HTTP_ADDR` | `:8080` | endereço de escuta da API |
| `HTTP_READ_TIMEOUT` | `10s` | prazo de leitura da requisição |
| `HTTP_WRITE_TIMEOUT` | `15s` | prazo de escrita da resposta |
| `HTTP_IDLE_TIMEOUT` | `60s` | keep-alive |
| `HTTP_REQUEST_TIMEOUT` | `10s` | prazo total por requisição (middleware) |
| `HTTP_MAX_BODY_BYTES` | `1048576` | teto do corpo (1 MiB) |
| `HTTP_SHUTDOWN_TIMEOUT` | `20s` | drenagem do servidor no encerramento |
| `HTTP_STOP_TIMEOUT` | `50s` | prazo TOTAL do encerramento: **precisa cobrir** `HTTP_SHUTDOWN_TIMEOUT` + `SQS_SHUTDOWN_GRACE` (se ligado) + `WORKERS_SHUTDOWN_GRACE` (se ligado) — a config recusa a soma que não couber |

### PostgreSQL

| Variável | Default | Para que serve |
| --- | --- | --- |
| `DB_MAX_CONNS` | `20` | teto do pool (por instância) |
| `DB_MIN_CONNS` | `2` | conexões mantidas abertas |
| `DB_CONN_MAX_LIFETIME` | `30m` | reciclagem de conexão |
| `DB_CONN_MAX_IDLE_TIME` | `5m` | conexão ociosa |
| `DB_CONNECT_TIMEOUT` | `5s` | prazo de conexão |
| `DB_STATEMENT_TIMEOUT` | `10s` | `statement_timeout` do servidor |
| `DB_LOCK_TIMEOUT` | `5s` | `lock_timeout`; o estouro vira falha **transitória** (retry) |
| `DB_MIGRATIONS_ON_START` | `false` | aplicar migrations no boot. Deixe desligado: quem migra é o job `migrate` |

### OIDC / Keycloak

| Variável | Default | Para que serve |
| --- | --- | --- |
| `OIDC_ISSUER` | — | valor exato do claim `iss` (obrigatória) |
| `OIDC_DISCOVERY_URL` | `""` | de onde buscar o `.well-known` (pode ser o hostname interno) |
| `OIDC_AUDIENCE` | `""` | audience exigida no token (`jungleg-api` neste realm) |
| `OIDC_JWKS_URL` | `""` | sobrescreve o endereço das chaves públicas. Vazio usa o `jwks_uri` do discovery. **Use quando o IdP anuncia `localhost`** (caso do container — defeito 41) |
| `OIDC_JWKS_CACHE_TTL` | `15m` | validade do cache de chaves (`kid` desconhecido força rebusca) |
| `OIDC_CLOCK_SKEW` | `30s` | tolerância de relógio em `exp`/`nbf` |
| `OIDC_HTTP_TIMEOUT` | `5s` | prazo de cada chamada ao IdP |
| `OIDC_TOKEN_URL` | `""` | endpoint de token; **não** é usado pela aplicação (o serviço nunca emite token) — serve a ferramentas e testes |

### SQS / LocalStack

| Variável | Default | Para que serve |
| --- | --- | --- |
| `SQS_ENDPOINT` | `http://localhost:4566` | endpoint do broker |
| `SQS_REGION` | `us-east-1` | região |
| `SQS_ACCESS_KEY_ID` | `test` | credencial local (os nomes do SDK, `AWS_ACCESS_KEY_ID`, **não** são lidos pela aplicação) |
| `SQS_SECRET_ACCESS_KEY` | `test` | credencial local |
| `SQS_TRANSACTIONS_QUEUE` | `wager-transactions.fifo` | fila de entrada (precisa terminar em `.fifo`) |
| `SQS_TRANSACTIONS_DLQ` | `wager-transactions-dlq.fifo` | DLQ da entrada |
| `SQS_EVENTS_QUEUE` | `wallet-events.fifo` | destino dos eventos publicados |
| `SQS_EVENTS_DLQ` | `wallet-events-dlq.fifo` | DLQ dos eventos |
| `SQS_MAX_RECEIVE_COUNT` | `5` | limite de recebimentos antes do redrive |
| `SQS_VISIBILITY_TIMEOUT` | `30s` | invisibilidade durante o retry (mínimo aceito: 5s) |
| `SQS_WAIT_TIME` | `20s` | long polling |
| `SQS_BATCH_SIZE` | `10` | máximo de mensagens por `Receive` (1 a 10) |
| `SQS_CONSUMER_CONCURRENCY` | `8` | processamento simultâneo por instância |
| `SQS_BASE_BACKOFF` | `2s` | primeiro intervalo do retry (mínimo 1s) |
| `SQS_MAX_BACKOFF` | `1m` | teto do backoff |
| `SQS_SHUTDOWN_GRACE` | `15s` | prazo para concluir o lote em voo no encerramento |
| `SQS_CONSUME_TRANSACTIONS` | `false` | liga o consumidor da fila de entrada |

### Outbox (publicador de eventos)

| Variável | Default | Para que serve |
| --- | --- | --- |
| `OUTBOX_ENABLED` | `false` | liga o publicador nesta instância |
| `OUTBOX_BATCH_SIZE` | `50` | eventos reivindicados por ciclo |
| `OUTBOX_LEASE` | `30s` | validade da reivindicação (mínimo 5s); vencido, outro assume |
| `OUTBOX_POLL_INTERVAL` | `1s` | intervalo entre ciclos |
| `OUTBOX_MAX_ATTEMPTS` | `10` | teto de tentativas antes da quarentena |
| `OUTBOX_BASE_BACKOFF` | `2s` | primeiro intervalo do retry |
| `OUTBOX_MAX_BACKOFF` | `5m` | teto do backoff |
| `OUTBOX_PARKED_BACKOFF` | `1h` | prazo da quarentena (precisa ser ≥ `OUTBOX_MAX_BACKOFF`) |

### Referências pendentes (`PENDING_REFERENCE`)

| Variável | Default | Para que serve |
| --- | --- | --- |
| `REFERENCE_ENABLED` | `false` | liga o worker de referências + reaper |
| `REFERENCE_POLL_INTERVAL` | `2s` | intervalo entre ciclos |
| `REFERENCE_BATCH_SIZE` | `25` | transações por ciclo |
| `REFERENCE_MAX_ATTEMPTS` | `20` | tentativas antes de `REJECTED/REFERENCE_NOT_FOUND` |
| `REFERENCE_BASE_BACKOFF` | `5s` | primeiro intervalo do backoff exponencial |
| `REFERENCE_MAX_BACKOFF` | `10m` | teto do backoff |
| `REFERENCE_PENDING_MAX_AGE` | `2m` | TTL da pendência (≥ `REFERENCE_BASE_BACKOFF`) |
| `REFERENCE_REAPER_INTERVAL` | `1m` | intervalo da varredura de `PENDING` antigo |

### Laços de trabalho

| Variável | Default | Para que serve |
| --- | --- | --- |
| `WORKERS_SHUTDOWN_GRACE` | `10s` | prazo (do CONJUNTO) para os laços concluírem no encerramento |

> As variáveis `POSTGRES_*` e `KEYCLOAK_*` do `.env.example` são consumidas
> **apenas pelo Compose** (banco e realm), não pela aplicação.

---

## 4. Subir o ambiente

```sh
docker compose up -d --build
```

Sobe cinco serviços: `postgres`, `keycloak` (com o realm importado de
`deploy/keycloak/realm.json`), `localstack`, `migrate` (aplica as migrations e
termina) e `api` (HTTP + consumidor + workers, publicada em **`http://localhost:8081`**).

> **Use `--build`.** As migrations são **embutidas no binário** (`go:embed`), então
> uma imagem antiga pode não conhecer a versão atual do schema e falhar com
> `no migration found for version 5: read down for version 5`. Foi o que aconteceu
> no ensaio de entrega (defeitos 47 e 48 — ver [`EVIDENCIAS.md`](EVIDENCIAS.md) §6).

### As filas do SQS

O LocalStack executa `deploy/localstack/init-queues.sh` no stage `READY` e cria as
**quatro** filas, com redrive e DLQ. Confira (não confie no script sem conferir):

```sh
docker exec jungleg-localstack awslocal sqs list-queues
```

```json
{
    "QueueUrls": [
        "http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/wager-transactions-dlq.fifo",
        "http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/wallet-events-dlq.fifo",
        "http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/wager-transactions.fifo",
        "http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/wallet-events.fifo"
    ]
}
```

Se voltar vazio, o script de init falhou; rode de novo:

```sh
docker compose run --rm localstack-init
```

### Estado e saúde

```sh
docker compose ps
curl -s http://localhost:8081/health/live
curl -s http://localhost:8081/health/ready
```

Saída real (com tudo de pé):

```
{"status":"UP"}
{"dependencies":{"oidc":"UP","postgres":"UP","sqs":"UP","workers":"UP"},"status":"UP"}
```

`/health/ready` verifica **PostgreSQL, IdP, SQS** (ping na fila específica) e
**`workers`** (os laços ligados pela configuração estão rodando). Se um deles
cair, o corpo diz qual — e é isso que o alerta de readiness usa (ver
[`../deploy/observability/`](../deploy/observability/)).

Para derrubar: `docker compose down` (mantém os volumes) ou `docker compose down -v`
(apaga os dados).

---

## 5. Migrations (aplicar e reverter)

Há cinco migrations em `migrations/`, embutidas no binário por `go:embed`. Tanto o
`go run ./cmd/migrate` quanto o job `migrate` do Compose carregam todas.

```sh
# aplicar tudo
DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungleg?sslmode=disable' go run ./cmd/migrate up

# versão atual
DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungleg?sslmode=disable' go run ./cmd/migrate version

# reverter UMA
DATABASE_URL='...' go run ./cmd/migrate down 1

# reverter TUDO
DATABASE_URL='...' go run ./cmd/migrate down
```

Saída real do ensaio (banco descartável `jungle_entrega`):

```
=== migrate up ===        migrations aplicadas  versao=5 dirty=false     exit=0
=== migrate version ===   versão do schema      versao=5 dirty=false     5
=== migrate down 1 ===    migrations revertidas versao=4 dirty=false     exit=0
=== migrate version ===   versão do schema      versao=4 dirty=false     4
=== migrate down ===      migrations revertidas; schema sem versão      exit=0
=== migrate version ===   nenhuma migration aplicada                    exit=1 (nada a fazer)
=== migrate up ===        migrations aplicadas  versao=5 dirty=false     exit=0
=== migrate up ===        schema já está atualizado, nada a aplicar     exit=1 (nada a fazer)
```

Códigos de saída (contrato do `golang-migrate`): **0** aplicou mudança, **1** nada
a fazer, **2** erro. O Compose usa `up --noop-ok`, que trata "nada a fazer" como
sucesso — sem isso, o **segundo** `docker compose up` nunca subiria a API
(defeito 48).

> **Armadilha que já custou tempo três vezes:** as migrations são **embutidas**
> no binário (`go:embed`). Um binário velho responde "schema já está atualizado" e
> não aplica nada — e uma imagem velha do Compose falha com
> `no migration found for version 5`. Com `go run ./cmd/migrate` o binário é sempre
> o do código atual; depois de alterar uma migration **rebuilde a imagem**
> (`docker compose build migrate`).

---

## 6. Executar a aplicação

### Pelo Compose (recomendado)

```sh
docker compose up -d --build
docker compose logs -f api
```

A aplicação sobe com todos os laços ligados (`SQS_CONSUME_TRANSACTIONS=true`,
`OUTBOX_ENABLED=true`, `REFERENCE_ENABLED=true`) e o log em texto (`APP_ENV=local`).

### No host

```sh
cp .env.example .env      # ajuste DATABASE_URL/OIDC_*/SQS_* para localhost
go build -o bin/api.exe ./cmd/api
./bin/api.exe
```

O binário aceita uma flag de verificação, usada pelo healthcheck do Compose:

```sh
./bin/api.exe -health-check                                    # consulta http://localhost:8080/health/ready
./bin/api.exe -health-check -health-check-url http://127.0.0.1:8081/health/ready
```

Saída: código **0** quando o serviço responde `200`; diferente de zero caso
contrário. É um processo curto que **não** carrega a configuração inteira.

### O que ler no log

| `msg` | Quando aparece | O que diz |
| --- | --- | --- |
| `application_started` | boot | `addr`, `issuer`, `stopTimeout` |
| `operacao_concluida` / `operacao_rejeitada` | operação por HTTP | `transactionId`, `walletId`, `providerId`, `status`, `correlationId` |
| `sqs_mensagem_processada` | operação pela fila | `messageId`, `transactionId`, `walletId`, `providerId`, `correlationId` |
| `reference_resolvida` / `_ainda_pendente` / `_rejeitada` | worker de referências | os mesmos ids + `desfecho` |
| `outbox_evento_publicado` / `outbox_atrasada` | publicador | `eventId`, `eventType`, `aggregateId` (a carteira) |
| `shutdown_signal_received` / `application_stopped` | encerramento | ordem e prazos |

Em `APP_ENV=production` o log é **JSON** (uma linha por evento); em `local` é texto.
O formato é decidido em um lugar só (`internal/observability/logger.go`) — o
defeito 27 foi exatamente dois formatos no mesmo stdout.

---

## 7. Autenticação e fluxo autenticado

### O que o realm provisiona

`deploy/keycloak/realm.json` cria o realm **`jungleg`**, o audience `jungleg-api`
(mapper nos três clients) e três clients `client_credentials`, com as roles:

| Client | Secret | Role | Pode |
| --- | --- | --- | --- |
| `internal-service` | `internal-service-secret` | `internal` | `POST /wallets`, leitura de carteira/ledger, reconciliação, `GET /wagering/transactions/{id}` |
| `provider-service` | `provider-service-secret` | `provider` + `provider_id=provider-a` | `POST /wagering/transactions` e as leituras do próprio provedor |
| `provider-b-service` | `provider-b-service-secret` | `provider` + `provider_id=provider-b` | idem, isolado em `provider-b` |

O **`providerId` vem do token**, nunca do corpo: o corpo pode confirmá-lo, e
divergência responde `403`.

### Obter token (`client_credentials`)

```sh
TOKEN=$(curl -s -X POST http://localhost:8080/realms/jungleg/protocol/openid-connect/token \
  -d grant_type=client_credentials \
  -d client_id=provider-service \
  -d client_secret=provider-service-secret | jq -r .access_token)
```

### Exemplos reais (executados na Fase 12 contra o container)

Abertura de carteira (token **interno**; `initialBalance` pode ser `"0.00"`):

```sh
curl -s -X POST http://localhost:8081/wallets \
  -H "Authorization: Bearer $TOKEN_INTERNO" -H "Content-Type: application/json" \
  -d '{"playerId":"b543a7e1-211d-41ed-bd1e-a86b624325a6","initialBalance":{"amount":"1000.00","currency":"BRL"}}'
```

```
HTTP 201  {"id":"01a14824-b7fa-71aa-9d2a-53f3631aade0","playerId":"b543a7e1-211d-41ed-bd1e-a86b624325a6","balance":{"amount":"1000.00","currency":"BRL"},"version":1}
```

Envio de operação (token de **provedor**; `Idempotency-Key` é obrigatório):

```sh
curl -s -X POST http://localhost:8081/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_PROVEDOR" -H "Idempotency-Key: provider-a:txn-entrega-2" \
  -H "Content-Type: application/json" \
  -d '{"providerId":"provider-a","externalTransactionId":"txn-entrega-2","playerId":"b543a7e1-211d-41ed-bd1e-a86b624325a6","walletId":"01a14824-b7fa-71aa-9d2a-53f3631aade0","roundId":"round-1","gameId":"fortune-chimp","kind":"BET","money":{"amount":"25.00","currency":"BRL"}}'
```

```
HTTP 201  {"transactionId":"01a14824-b84d-74a8-b7fb-66ff77cf5a49","status":"PROCESSED","balance":{"amount":"975.00","currency":"BRL"},"idempotentReplay":false,"failureCode":null}
```

O **mesmo** pedido (replay):

```
HTTP 200  {"transactionId":"01a14824-b84d-74a8-b7fb-66ff77cf5a49","status":"PROCESSED","balance":{"amount":"975.00","currency":"BRL"},"idempotentReplay":true,"failureCode":null}
```

Mesma chave com **conteúdo diferente**:

```
HTTP 409  {"error":{"code":"IDEMPOTENCY_CONFLICT","message":"a operação já existe com conteúdo diferente"}}
```

Sem o header:

```
HTTP 400  {"error":{"code":"INVALID_REQUEST","message":"o header Idempotency-Key é obrigatório","failureCode":"IDEMPOTENCY_KEY_REQUIRED"}}
```

BET sem saldo (rejeição de negócio é **desfecho**, não erro de transporte):

```
HTTP 422  {"transactionId":"01a14824-b935-7140-b5d5-534c6ff033c2","status":"REJECTED","balance":null,"idempotentReplay":false,"failureCode":"INSUFFICIENT_FUNDS"}
```

Leitura da carteira, ledger, reconciliação e consulta pela identidade externa:

```
GET /wallets/{id}                  → 200 {"id":"…","balance":{"amount":"975.00","currency":"BRL"},"version":2}
GET /wallets/{id}/ledger?limit=50  → 200 {"entries":[CREDIT 1000.00 → 1000.00, DEBIT 25.00 → 975.00],"nextCursor":null}
POST /wallets/{id}/reconciliation  → 200 {"storedBalance":975.00,"calculatedBalance":975.00,"difference":0.00,"consistent":true,"checkedEntries":2}
GET /providers/provider-a/wagering/transactions/txn-entrega-2 → 200 {…,"status":"PROCESSED",…}
```

> O replay devolve o **saldo do processamento original** (`975.00`), não o saldo de
> agora — é o que o README §9 exige. Se a carteira já tivesse recebido outras
> movimentações, o replay continuaria devolvendo `975.00`.

Isolamento: o mesmo `externalTransactionId` consultado por **outro** provedor
responde **404** (não confirma que a operação existe), e sem token a resposta é
**401**:

```
GET /providers/provider-b/wagering/transactions/txn-entrega-2 → 404 {"error":{"code":"NOT_FOUND","message":"recurso não encontrado"}}
GET /wallets/{id} (sem token)                                 → 401
```

### Códigos HTTP do contrato

| Situação | Status |
| --- | --- |
| Operação nova processada / carteira aberta | `201` |
| Replay idempotente (mesma chave e mesmo conteúdo) | `200` |
| Operação pendente de referência (`PENDING_REFERENCE`) | `202` |
| Entrada inválida (JSON, campo, header, cursor, `limit`) | `400` |
| Token ausente/inválido | `401` |
| Token de outro provedor / rota interna | `403` |
| Recurso inexistente ou de outro provedor | `404` |
| Idempotency-Key reutilizada com conteúdo diferente | `409` |
| Rejeição de negócio (saldo, referência, valor) | `422` |
| Dependência indisponível (banco, IdP, SQS) | `503` com `Retry-After` |

O corpo de erro é `{"error":{"code","message","failureCode"}}`; a **rejeição de
negócio** (422) traz o corpo do desfecho (com `transactionId` e `failureCode`),
porque ela é um fato auditável, não um erro de transporte.

---

## 8. Testes

```sh
# unidade (sem infraestrutura; os de integração são pulados)
go test -count=1 -race ./...

# build/vet/formatação
go build ./... ; go vet ./... ; go vet -tags=integration ./...
gofmt -l ./internal/ ./cmd/ ./migrations/ ./test/

# integração — precisa dos containers e das variáveis abaixo
export TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
export OIDC_ISSUER='http://localhost:8080/realms/jungleg'
export OIDC_DISCOVERY_URL='http://localhost:8080/realms/jungleg'
export OIDC_AUDIENCE='jungleg-api'
export SQS_ENDPOINT='http://localhost:4566'
go test -count=1 -race -tags=integration -timeout 600s ./...
```

PowerShell: `$env:TEST_DATABASE_URL='...'` etc.

> ⚠️ **Pare o serviço `api` do compose antes de rodar a integração:**
> `docker compose stop api`. Ele consome a **mesma** fila
> (`wager-transactions.fifo`) e "rouba" as mensagens dos cenários, que então
> esperam por entregas que nunca chegam (sintoma: `TestT1MesmaOperacao50VezesPelaFila`
> esperando "as 50 entregas serem tratadas", com `receiveCount` maior que 1 no log
> dos processos de teste). Foi o que aconteceu na primeira rodada da Fase 12 — é
> contaminação de ambiente, não defeito de código. Depois dos testes, suba de novo
> com `docker compose start api`.

**Dependências da integração**, em ordem:

1. `docker compose up -d postgres keycloak localstack` (os três `healthy`);
2. banco de teste **separado** (`jungle_test`) — os testes **truncam tabelas**;
3. filas criadas (`list-queues` com as quatro);
4. **Keycloak de pé**: os testes da borda HTTP obtêm token real por
   `client_credentials` (sem ele, são pulados com aviso);
5. `-timeout 600s`: a suíte sobe **processos reais** e espera `VisibilityTimeout` e backoff.

A **build tag** é `integration`; sem ela, os arquivos `*_integration_test.go` e
`test/integration` não são compilados — é o que mantém `go test ./...` verde em
máquina sem Docker.

Prova rápida só da Fase 11 (métricas, log do processo e ciclo de vida, ~35 s):

```sh
go test -count=1 -race -tags=integration -timeout 600s \
  -run 'TestMetrica|TestLogsDoProcesso|TestCicloDeVidaFx' ./test/integration/ -v
```

Números da última execução (duas rodadas): unidade verde em **17 pacotes**;
integração verde em **18 pacotes** (o 18º é `test/integration`), com
`test/integration` em ~147 s.

### Smoke test pronto (só `curl.exe`, sem jq)

Há um script que exercita **todos os endpoints** e confere status + corpo:
[`../teste-curl.ps1`](../teste-curl.ps1) — 26 passos (públicos, 201/200/409/400/422,
replay, `PENDING_REFERENCE`, ledger, reconciliação, isolamento por provedor,
401/403/404 e a série de métricas):

```powershell
powershell -ExecutionPolicy Bypass -File .\teste-curl.ps1
```

> **Windows:** use `curl.exe` (no PowerShell 5.1, `curl` é apelido de
> `Invoke-WebRequest`). E o PowerShell 5.1 **remove as aspas** do JSON ao chamar um
> `.exe` nativo: mande o corpo por arquivo (`--data-binary "@corpo.json"`), como o
> script faz — ou use `cmd.exe`, onde `-d "{\"campo\":\"valor\"}"` funciona.

---

## 9. Múltiplas instâncias e simulações de falha

### Três instâncias independentes

O cenário do README §8 roda sozinho, dentro do pacote `test/integration`: o
`TestMain` compila `cmd/api` **uma vez** e cada cenário sobe **três processos**
com `HTTP_ADDR` próprio (faixa **18091+**), `application_name=api-N` no DSN e log
por processo, que é despejado na falha.

```sh
go test -count=1 -race -tags=integration -timeout 600s -run 'TestT' ./test/integration/ -v
```

O que ele prova: 50 envios simultâneos da mesma aposta debitam **uma** vez; duas
apostas de 80.00 sobre 100.00 terminam com 1 `PROCESSED`, 1
`REJECTED/INSUFFICIENT_FUNDS`, saldo 20.00 e **um** débito; carteiras distintas
andam em paralelo (14–34 ms) enquanto a mesma carteira espera o lock (408 ms).

### Queda entre o commit e o `Delete` da mensagem

O estado durável é reproduzido com o consumidor real e só o `Delete` embrulhado:
`TestT5QuedaEntreCommitEDeleteReentregaReconhecida`. A mensagem volta, a inbox a
recusa (`attempts` 1→2) e **não** há débito novo.

### SIGTERM de verdade (container Linux)

A prova está em [`EVIDENCIAS.md`](EVIDENCIAS.md) (T5/T6):
`docker stop` com `stop_grace_period: 60s` leva ao `exit=0` em ~8 s, com
`sqs_visibilidade_liberada` e a mensagem processada **uma** vez pela instância nova.
O cenário manual é:

```sh
docker run -d --name jungle-sigterm --network jungleg_default ... jungleg-api:local   # com os laços ligados
# envie uma mensagem, espere ela entrar em voo e:
docker stop -t 60 jungle-sigterm
docker logs jungle-sigterm | grep -E 'shutdown_signal_received|sqs_visibilidade_liberada|application_stopped'
```

### Falhas de infraestrutura

| Falha | O que acontece |
| --- | --- |
| PostgreSQL fora | `/health/ready` DOWN (`postgres`), requisições de negócio em `503`; nada é gravado |
| Keycloak fora | `/health/ready` DOWN (`oidc`) e **todo** token responde `503` (não 401: o token pode estar perfeito) |
| SQS fora | `/health/ready` DOWN (`sqs`); a publicação da outbox falha, os eventos continuam pendentes e são republicados quando volta |
| Worker morto | `/health/ready` DOWN (`workers`) enquanto o runner reinicia o laço (com backoff); o log traz `worker_morreu_reiniciando` |
| Processo morto no meio | a transação faz rollback (nada pela metade); o trabalho reivindicado volta pelo **lease** |

---

## 10. Observabilidade

- `GET /metrics` (público) expõe as 12 métricas Prometheus `jungleg_wager_*`, sem
  label de alta cardinalidade. Cada uma tem teste que dispara o gatilho e lê o
  valor em `/metrics` (`EVIDENCIAS.md` §4).
- `GET /health/live` e `GET /health/ready` (públicos) — readiness com PostgreSQL,
  IdP, SQS e `workers`.
- Logs JSON com `correlationId`, `messageId`, `transactionId`, `walletId` e
  `providerId`, **sem** credencial e **sem** payload financeiro completo.
- Dashboard (13 painéis), 7 regras de alerta e o runbook do que fazer quando cada
  uma dispara: [`../deploy/observability/`](../deploy/observability/).

---

## 11. Onde está o resto da documentação

| Documento | Para quem | O que tem |
| --- | --- | --- |
| [`README.md`](README.md) (este) | quem vai **rodar** | subir, configurar, migrar, autenticar e testar |
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | avaliador técnico | decisões, concorrência, idempotência, SQS, outbox, os 50 defeitos, limitações |
| [`CONFORMIDADE.md`](CONFORMIDADE.md) | avaliador | requisito do enunciado → onde está atendido, e as 23 interpretações adotadas |
| [`EVIDENCIAS.md`](EVIDENCIAS.md) | avaliador | saída real dos testes, dos curls e do checkout limpo |
| [`../docs_desafio/SISTEMA_EXPLICADO.md`](../docs_desafio/SISTEMA_EXPLICADO.md) | quem vai **manter** o código | do zero de vocabulário ao onde-cada-garantia-é-imposta |
| [`../docs_desafio/ENTREVISTA.md`](../docs_desafio/ENTREVISTA.md) | quem vai **defender** o projeto | pergunta → resposta de 60 s, com número e limitação admitida |

---

## 12. Limitações conhecidas (resumo)

A lista completa está no [`ARCHITECTURE.md`](ARCHITECTURE.md) §16. As que mais
importam para operar:

- **sem consumidor de `wallet-events.fifo`** nesta entrega: o contrato de consumo
  está documentado, mas quem consome os eventos é o sistema do outro lado;
- **sem tracing (OpenTelemetry)** e **sem partidas dobradas** — diferenciais
  opcionais do enunciado;
- **`/health/ready` não é métrica**: alertar "workers DOWN" usa o
  `blackbox_exporter` (configurado em `deploy/observability/`);
- **a ordem por carteira na publicação depende do lote**: com mais eventos
  pendentes da mesma carteira do que `OUTBOX_BATCH_SIZE`, o consumidor deve
  ordenar por `walletVersion`;
- **a latência medida é de cliente local**, em comparação de topologias — não é
  número de produção;
- **sem introspecção de token (RFC 7662)**: a validação é local (assinatura +
  claims), então um token revogado antes do `exp` não é detectado.
