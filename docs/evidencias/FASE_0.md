# EVIDÊNCIAS — Fase 0 validada com infraestrutura real

> Saídas reais de comandos, coletadas em 2026-10-15. Servem como prova para o avaliador
> e como referência de "o que é sucesso" para as próximas fases.

## Ambiente

| Item | Versão/valor |
| --- | --- |
| Go | 1.26.5 (módulo declara `go 1.25.0` + `toolchain go1.26.5`) |
| Docker | 29.8.2 |
| Docker Compose | v5.6.0 |
| Driver de storage | overlayfs |
| Docker Desktop | `C:\Users\PoxZe\AppData\Local\Programs\DockerDesktop` |

## 1. Testes unitários (`go test -race ./...`)

```
?   	github.com/FeliPoxZ/jungle_desafio/cmd/api	[no test files]
ok  	github.com/FeliPoxZ/jungle_desafio/internal/app	2.155s
ok  	github.com/FeliPoxZ/jungle_desafio/internal/config	1.235s
ok  	github.com/FeliPoxZ/jungle_desafio/internal/httpapi	1.999s
ok  	github.com/FeliPoxZ/jungle_desafio/internal/observability	1.616s
```

`go build ./...`, `go vet ./...` e `gofmt -l` limpos.

## 2. Smoke test da API (sem Docker)

Servidor subido em `:8099`:

```
GET /health/live   → {"status":"UP"}
GET /health/ready  → {"dependencies":{},"status":"UP"}
GET /metrics       → jungleg_wager_operations_total, ... (13 métricas + runtime)
GET /nao-existe    → 404
```

Log real, com correlação injetada automaticamente pelo `contextHandler`:

```
msg=http_request status=200 path=/health/live durationMs=0
  correlationId=fcc9bd0d-a93e-42da-b02b-fc226b04cab7
  requestId=fcc9bd0d-a93e-42da-b02b-fc226b04cab7
```

## 3. Containers

```
NAME                 STATUS
jungleg-keycloak     Up (healthy)
jungleg-localstack   Up (healthy)
jungleg-postgres     Up (healthy)
```

### 3.1. PostgreSQL — bancos criados pelo init

```
   datname
-------------
 jungle_test
 jungleg
 keycloak
 postgres
(4 rows)
```

### 3.2. SQS (LocalStack 4.0.3) — 4 filas do desafio

```
wager-transactions-dlq.fifo
wager-transactions.fifo
wallet-events-dlq.fifo
wallet-events.fifo
```

Atributos verificados (saída literal do `get-queue-attributes`):

```
FILA: wager-transactions.fifo
  ReceiveMessageWaitTimeSeconds : 20
  VisibilityTimeout             : 60
  FifoQueue                     : true
  RedrivePolicy                 : {"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:wager-transactions-dlq.fifo","maxReceiveCount":"5"}

FILA: wager-transactions-dlq.fifo
  VisibilityTimeout             : 30
  FifoQueue                     : true

FILA: wallet-events.fifo
  ReceiveMessageWaitTimeSeconds : 20
  VisibilityTimeout             : 30
  FifoQueue                     : true
  RedrivePolicy                 : {"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:wallet-events-dlq.fifo","maxReceiveCount":"5"}

FILA: wallet-events-dlq.fifo
  VisibilityTimeout             : 30
  FifoQueue                     : true
```

### 3.3. Keycloak — realm importado e token real emitido

Discovery (`/realms/jungleg/.well-known/openid-configuration`):

```
issuer                : http://localhost:8080/realms/jungleg
token_endpoint        : http://localhost:8080/realms/jungleg/protocol/openid-connect/token
jwks_uri              : http://localhost:8080/realms/jungleg/protocol/openid-connect/certs
grant_types_supported : ..., client_credentials, ...
```

Token obtido com `client_credentials` (`provider-service`), claims decodificados:

```
iss         : http://localhost:8080/realms/jungleg
aud         : jungleg-api
provider_id : provider-a
roles       : provider
expires_in  : 900
```

⭐ **Prova do isolamento:** o `provider_id` vem **assinado pelo IdP**, não do corpo da requisição.

## 4. Comandos reproduzíveis

```powershell
# Cache do Go (obrigatório nesta máquina)
$env:GOMODCACHE='E:\jungleg\.gocache\mod'
$env:GOCACHE='E:\jungleg\.gocache\build'
$env:GOPATH='E:\jungleg\.gocache\gopath'

# Docker no PATH (instalação por usuário)
$env:Path = "C:\Users\PoxZe\AppData\Local\Programs\DockerDesktop\resources\bin;$env:Path"

# Infraestrutura
docker compose up -d postgres keycloak localstack
docker compose --profile init run --rm localstack-init
docker compose ps

# Testes
go test -race ./...
```

## 5. Defeitos encontrados e corrigidos nesta validação

| # | Defeito | Causa raiz | Correção |
| --- | --- | --- | --- |
| 1 | LocalStack morria no boot: *"License activation failed"* | A tag `stable` passou a apontar para a versão **Pro** (exige `LOCALSTACK_AUTH_TOKEN`) | Versão fixada em `localstack/localstack:4.0.3` (última community estável), com o motivo documentado no compose |
| 2 | `init-queues.sh` falhava com `NonExistentQueue` | O `log()` escrevia em **stdout**, e o `$(...)` do chamador capturava **log + URL** misturados. A URL virava `"[[init-queues] fila já existe: x.fifo\nhttp://...]"` | `log()` agora escreve em **stderr** (`>&2`). Regra: função que devolve valor por stdout não pode logar em stdout |
| 3 | `queue_url()` aceitava a string literal `"None"` como URL válida | O `aws-cli` imprime `"None"` (não vazio) quando o resultado é nulo, e `[ -n "None" ]` é verdadeiro | Normalização para vazio dentro de `queue_url()`, num lugar só, + falha explícita se a URL não puder ser obtida |
