# EVIDÊNCIAS — o que foi executado e o que saiu

> Este arquivo substitui os 13 relatórios por fase: aqui está a **saída real** dos
> comandos, com os números. Tudo foi executado nesta máquina, com os containers do
> `docker-compose.yml`. O detalhe de cada decisão está no
> [`ARCHITECTURE.md`](ARCHITECTURE.md); o mapa requisito → onde está, no
> [`CONFORMIDADE.md`](CONFORMIDADE.md).

## 1. Ambiente e como validar

| Item | Valor |
| --- | --- |
| SO | Windows 11, PowerShell 5.1 |
| Go | `go1.26.5 windows/amd64` |
| Containers | `jungleg-postgres`, `jungleg-keycloak`, `jungleg-localstack` (healthy) |
| Banco de teste | `jungle_test` (**separado** do de aplicação: a suíte trunca tabelas) |
| Esquema | versão 5 |

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'
$env:GOCACHE='E:\jungleg\.gocache\build'
$env:GOPATH='E:\jungleg\.gocache\gopath'
$env:TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
$env:OIDC_ISSUER='http://localhost:8080/realms/jungleg'
$env:OIDC_DISCOVERY_URL='http://localhost:8080/realms/jungleg'
$env:OIDC_AUDIENCE='jungleg-api'
$env:SQS_ENDPOINT='http://localhost:4566'

go build ./... ; go vet ./... ; go vet -tags=integration ./...
gofmt -l .\internal\ .\cmd\ .\migrations\ .\test\
go test -count=1 -race ./...
go test -count=1 -race -tags=integration -timeout 600s ./...
```

> **Pare o `api` do compose antes da integração** (`docker compose stop api`): ele
> consome a **mesma** `wager-transactions.fifo` e "rouba" as mensagens dos cenários.
> Sintoma: `TestT1MesmaOperacao50VezesPelaFila` esperando "as 50 entregas serem
> tratadas" e `TestConferenciaFinalCruzandoHTTPESQS` sem receber a mensagem cruzada.

## 2. Estático e suíte inteira (duas rodadas)

```
go build ./...                          (sem saída = limpo)
go vet ./...                            (sem saída = limpo)
go vet -tags=integration ./...          (sem saída = limpo)
gofmt -l ...                            (sem saída = limpo)

RODADA 1 — UNIDADE (go test -count=1 -race ./...)
ok  internal/app 3.349s | app/inbox 1.442s | app/ports 1.369s | app/wagering 1.621s
ok  config 1.410s | consumer 2.278s | domain/ledger 1.353s | domain/money 1.633s
ok  domain/wagering 1.420s | domain/wallet 1.397s | httpapi 1.997s | infra/auth 4.807s
ok  infra/postgres 1.397s | observability 1.633s | canonicaljson 1.336s | idgen 1.335s
ok  worker 3.560s

RODADA 1 — INTEGRAÇÃO (go test -count=1 -race -tags=integration -timeout 600s ./...)
ok  internal/app 13.837s | infra/postgres 6.363s | infra/auth 4.653s | worker 4.103s
ok  test/integration 146.987s

RODADA 2 — UNIDADE
ok  internal/app 3.290s | consumer 2.336s | observability 1.557s | worker 3.662s  (demais idem)

RODADA 2 — INTEGRAÇÃO
ok  test/integration 148.760s

exit 0 nas quatro execuções
```

**17 pacotes** de unidade e **18** de integração (o 18º é `test/integration`).
Cobertura do domínio: `money` 97,9%, `wallet` 99,0%, `wagering` 100,0%.

## 3. Concorrência e recuperação (README §8 e §13) — três processos reais

O pacote `test/integration` compila o binário **uma vez** e sobe **três processos**
com `HTTP_ADDR` próprio (faixa 18091+), `application_name=api-N` e log por processo.
Os números são da execução:

| Cenário | Resultado medido |
| --- | --- |
| **T0** independência | 3 PIDs, 3 conjuntos de conexões em `pg_stat_activity` (`api-1/2/3`); derrubar uma não afeta as outras |
| **T1** mesma aposta 50× (HTTP) | 1× `201` + 49 replay `200`, **uma** transação, **um** débito, versão 2 |
| **T1** mesma aposta 50× (fila) | 50 entregas distintas → **50** linhas de inbox `PROCESSED` e **um** débito (a dedup que barrou foi a financeira) |
| **T2** 100.00 + duas de 80.00 | 1 `PROCESSED`, 1 `REJECTED/INSUFFICIENT_FUNDS`, saldo **20.00**, **um** débito, versão 2 |
| **T3** carteiras distintas | 5 operações em outras carteiras concluíram em **14–34 ms**; a carteira presa esperou **408 ms**; nenhum backend esperando lock nas demais |
| **T5** queda entre commit e `Delete` | mensagem volta, inbox recusa (`attempts` 1→2 em 2,013 s), nenhum débito novo, DLQ vazia |
| **T6** 3 publishers na mesma outbox | **12** eventos → **12** mensagens, 0 duplicadas, 0 pendentes, distribuição `api-1=4, api-2=4, api-3=4` |
| **T7** reversão antes da referência | `REFUND` na api-1, `BET` na api-3, pendência resolvida pela api-2 (a única com o worker) |
| **T8** reinício | replay devolve a **mesma** transação e o saldo **75.00** da época; `PENDING_REFERENCE` resolvida pela instância nova; reconciliação consistente |
| **SIGTERM** (container Linux) | `docker stop -t 60` → **exit=0 em 8,09 s**; `sqs_visibilidade_liberada`; mensagem processada **uma** vez pela instância nova |
| **kill sem prazo** | SIGKILL em ~3,4 s; nada commitado (abortado); a mensagem voltou pelo `VisibilityTimeout` e foi processada **uma** vez |
| **Latência sob concorrência** | p99 **166 ms** (carteiras distintas) × **579 ms** (mesma carteira); 597 × 172 ops/s |
| **Conferência final** | saldo armazenado == soma do ledger por **duas vias** independentes (SQL puro e endpoint): **101000** e **diferença 0.00** |

## 4. Observabilidade (README §12)

**12 de 12 métricas** emitidas e cada uma com teste que dispara o gatilho real e lê
o valor em `/metrics` (não no objeto em memória):

| Métrica | Gatilho do teste |
| --- | --- |
| `operations_total{kind,status}` | BET 201 por HTTP **e** por SQS |
| `operation_duration_seconds{kind,status}` | as mesmas operações (contagem, soma, 11 buckets com `+Inf`) |
| `idempotent_replays_total` | BET repetida (201 → 200) |
| `transaction_conflicts_total` | carteira travada por `FOR UPDATE`: 3 tentativas, 503, **2** conflitos |
| `inbox_duplicates_total` | inbox já `PROCESSED` com o `MessageId` do broker |
| `dlq_messages_total` | corpo inválido → DLQ (`1` mensagem) |
| `outbox_publish_total{result}` | publisher real publicando 1 de 2 eventos |
| `outbox_pending_age_seconds` | evento envelhecido 30 s, lote de 1 |
| `outbox_publish_attempts` | a mesma publicação |
| `reconciliation_divergences_total` | saldo divergido por `UPDATE` + `POST /reconciliation` |
| `reference_resolution_total{result}` | REFUND antes da BET, resolvido pelo worker |
| `retries_total{stage}` | os 8 estágios exercitados (3 no consumidor, 5 nos laços) |

Cardinalidade é **asserção**: nenhum label com nome de identificador e nenhum valor
com formato de UUID. Log do **processo real** (`APP_ENV=production`) com
`correlationId`, `requestId`/`messageId`, `transactionId`, `walletId` e `providerId`
por caminho, **sem** segredo (`client_secret`, `Authorization`, `Bearer`) e **sem**
payload financeiro (`amount`, `money`, valores em texto); em `APP_ENV=local` o log é
texto. Dashboard (13 painéis) e 7 alertas validados por `promtool` em
`deploy/observability/`.

## 5. Entrega: checkout limpo, migrations e fluxo autenticado

**`docker compose up -d --build` de um clone limpo** (volumes novos):

```
git clone → HEAD do repositório
docker compose up -d --build   → 52 s
jungleg-api        Up (healthy)
jungleg-migrate    Exited (0)
readiness          {"dependencies":{"oidc":"UP","postgres":"UP","sqs":"UP","workers":"UP"},"status":"UP"}
POST /wallets      → 201 {"balance":{"amount":"500.00","currency":"BRL"},"version":1}
```

**Migrations (aplicar e reverter)**, em banco descartável:

```
up        → migrations aplicadas versao=5 dirty=false   exit=0
version   → 5
down 1    → migrations revertidas versao=4              exit=0
version   → 4
down      → schema sem versão                           exit=0
version   → nenhuma migration aplicada                  exit=1
up        → versao=5                                    exit=0
up        → "schema já está atualizado"                 exit=1
```

**Fluxo autenticado** (Keycloak real, `client_credentials`, contra o container):

```
health/live  → 200 {"status":"UP"}
health/ready → 200 {"dependencies":{...,"workers":"UP"},"status":"UP"}

POST /wallets 1000.00            → 201 {"id":…,"balance":1000.00,"version":1}
BET 25.00                        → 201 {"status":"PROCESSED","balance":975.00}
mesmo pedido (replay)            → 200 {"idempotentReplay":true,"balance":975.00}
mesma chave, corpo diferente     → 409 {"code":"IDEMPOTENCY_CONFLICT"}
sem Idempotency-Key              → 400 {"failureCode":"IDEMPOTENCY_KEY_REQUIRED"}
BET 5000.00 sem saldo            → 422 {"status":"REJECTED","failureCode":"INSUFFICIENT_FUNDS"}
LOSS 0.00                        → 201 PROCESSED (não movimenta)
REFUND da BET                    → 201 (saldo volta a 1000.00)
ROLLBACK da BET já estornada     → 422 ALREADY_REVERSED
REFUND de referência inexistente → 202 PENDING_REFERENCE
GET ledger?limit=50              → 200 (CREDIT 1000.00, DEBIT 25.00, CREDIT 25.00)
POST reconciliação               → 200 {"storedBalance":1000.00,"calculatedBalance":1000.00,"consistent":true,"checkedEntries":3}
GET por identidade externa (A)   → 200; pelo provider B → 404
GET ledger limit inválido        → 400
sem token                        → 401; token de provedor na rota interna → 403
```

**Defeito 50 (dinheiro criado)** — antes e depois da correção, no container:

```
antes:  BET 40 → ROLLBACK 40 → REFUND 40  ⇒  saldo 100.00 → 60.00 → 100.00 → 140.00 (201)
depois: BET 40 → ROLLBACK 40 → REFUND 40  ⇒  422 ALREADY_REVERSED, saldo 100.00 (versão 3)
```

## 6. Os defeitos encontrados

**51 defeitos** documentados em [`ARCHITECTURE.md`](ARCHITECTURE.md) §15, cada um com
sintoma, causa de fundo e correção. Os mais caros:

| # | Sintoma | Causa |
| ---: | --- | --- |
| 6 | operação legítima recusada | `55P03` classificado como permanente |
| 13 | `failureCode` em branco | `wagering.Error` não era alias do erro de domínio |
| 19 | fila fora de ordem (1 em 5 execuções) | `RETURNING` não garante ordem → CTE com `ORDER BY sequence` |
| 20 | todo corpo recusado | `chimw.RequestSize(0)` |
| 27 | dois formatos de log no mesmo stdout | dois loggers no processo |
| 30/31 | mensagem aceita sem ser processada | `return` no meio do parsing; identidade da mensagem |
| 34 | operações por SQS invisíveis | métrica não emitida no caminho assíncrono |
| 40 | instâncias indistinguíveis no `pg_stat_activity` | pool sobrescrevia o `application_name` do DSN |
| 41 | container não autenticava | Keycloak anuncia `jwks_uri` em `localhost` → `OIDC_JWKS_URL` |
| 42 | métrica de conflito em zero para sempre | nenhum caminho chamava `IncConflict` |
| 43 | mesmo label com dois significados | HTTP: `kind=<status>`; SQS: `kind=BET` |
| 47 | container nunca `healthy` | healthcheck apontava para `/app/api` |
| 48 | segundo `compose up` não subia a API | `migrate` devolve 1 em "nada a fazer" |
| 49 | credenciais do broker inertes | documentado `AWS_*`, código lê `SQS_*` |
| **50** | **REFUND depois de ROLLBACK criava dinheiro** | guarda de reversão assimétrica |
| 51 | a aplicação não subia no host seguindo o guia | o guia mandava copiar o `.env`; a aplicação lê **ambiente** (e a porta 8080 do host é do Keycloak) |

## 7. Conformidade com o enunciado (§1–§15)

| § | Requisito | Evidência |
| ---: | --- | --- |
| 1 | API HTTP + consumidor movimentando carteiras | §3 e §5 acima |
| 2 | OIDC, `providerId` do token, isolamento, credencial do broker | `internal/infra/auth` (49 casos), 30 integrações, §5; policy de fila não exercida no LocalStack (§16) |
| 3 | Falhas: duplicata, reversão antecipada, concorrência, queda, republicação, indisponibilidade | §3 (T1–T8) |
| 4 | Stack: Go, Fx, HTTP, OIDC, Postgres, SQS, Compose, migrations, `-race` | `go.mod`, `internal/app`, `docker-compose.yml`, §2 |
| 5 | 8 garantias financeiras | `../docs_desafio/SISTEMA_EXPLICADO.md` cap. 5 |
| 6 | Domínio | `internal/domain/*` (574 casos) |
| 7 | 5 tipos, referências, TTL, `failureCode` | `internal/app/wagering`, §5 |
| 8 | 100.00 + duas de 80.00; três processos; carteiras em paralelo | §3 (T1, T2, T3) |
| 9 | 7 rotas, status, cursor, reconciliação | §5 e [`README.md`](README.md) §7 |
| 10 | FIFO + DLQ + redrive, retry, DLQ, SIGTERM | §3, §4 e `internal/consumer` |
| 11 | Outbox transacional, 4 eventos, recuperação | §3 (T6) e `internal/worker` |
| 12 | 5 ids no log, 12 métricas, health checks, dashboard | §4 |
| 13 | Unidade, integração real, auth, T1–T8, conferência, `-race` | §2 e §3 |
| 14 | Critérios e eliminatórios | [`CONFORMIDADE.md`](CONFORMIDADE.md) §14 |
| 15 | Entrega reproduzível, instruções, `.env.example`, IdP, ARCHITECTURE | §5, [`README.md`](README.md) e [`ARCHITECTURE.md`](ARCHITECTURE.md) |

## 8. Limitações (o que não está entregue)

- **Sem consumidor de `wallet-events.fifo`**: o evento é publicado e o contrato de
  consumo está documentado (`ARCHITECTURE.md` §11); consumir é do outro lado.
- **Sem tracing (OpenTelemetry)**, **sem partidas dobradas** e **sem teste de carga**:
  diferenciais opcionais do §14.
- **Contador durável de falhas do reaper**: uma linha `PENDING` inconsistente é
  repetida em vez de virar `FAILED`.
- **A política IAM da fila não é exercida no LocalStack**: existem as credenciais do
  broker; a *queue policy* de principal fica para o deploy em AWS.
- **Sem introspecção de token**: token revogado antes do `exp` não é detectado
  (validação local de assinatura e claims).
- **A ordem por carteira na publicação depende do lote**: com mais eventos da mesma
  carteira que `OUTBOX_BATCH_SIZE`, o consumidor deve ordenar por `walletVersion`.
