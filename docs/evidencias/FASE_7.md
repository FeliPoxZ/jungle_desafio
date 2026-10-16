# EVIDÊNCIAS — Fase 7 (HTTP + autenticação OIDC)

> Saídas reais dos comandos, contra **PostgreSQL 16** (`jungleg-postgres`,
> `healthy`) e **Keycloak** (`jungleg-keycloak`, `healthy`) do `docker-compose`,
> no banco `jungle_test`.
>
> **Estado da fase: CONCLUÍDA.** Cinco blocos commitados:
>
> | Commit | Bloco |
> | --- | --- |
> | `ec6c014` | composição Fx (infra, usecase), `StopTimeout` da config, readiness (P5, P7) |
> | `1d43174` | **7a** — `internal/infra/auth`: OIDC com validação real (eliminatório) |
> | `95e85bb` | **7b/7d** — endpoints do §9, DTOs, mapeamento de status, rotas por role (P2, P4) |
> | `343ec51` | **7e** — integração com Keycloak e PostgreSQL reais |
> | `31cf6b5` | **P1** — flag `-health-check` do healthcheck do compose |

## Ambiente

| Item | Versão/valor |
| --- | --- |
| Go | 1.26.5 (PowerShell 5.1, Docker Desktop) |
| PostgreSQL | 16-alpine (`jungleg-postgres`, healthy) |
| Keycloak | `jungleg-keycloak`, healthy — realm `jungleg` |
| IdP | OIDC discovery + JWKS reais; tokens por `client_credentials` |
| Pacotes novos | `internal/infra/auth` (9 arquivos), `internal/httpapi` (+6), `internal/app/healthcheck.go` |
| Contratos | `httpapi.Autenticador` (3 métodos), `httpapi.WageringService`, `auth.Verificador` |

## 1. Verificação do realm ANTES de escrever código

Os três itens que, presumidos, custariam caro depois. Saída real, decodificando o
token que o Keycloak emite:

```
=== provider-service ===
{"exp":1792192203,"iat":1792191303,"jti":"...","iss":"http://localhost:8080/realms/jungleg",
 "aud":"jungleg-api","sub":"...","typ":"Bearer","azp":"provider-service",
 "scope":"jungleg-api","roles":["provider"],"provider_id":"provider-a"}

=== provider-b-service ===
{..., "roles":["provider"], "provider_id":"provider-b"}

=== internal-service ===
{..., "roles":["internal"]}      (SEM provider_id — é o que distingue o interno)
```

Três achados que viraram decisão de código:

1. **o `aud` é `jungleg-api`** (string, não lista) — a checagem de audience pode
   ser exigida sem risco de 401 em massa;
2. **a role vem no claim `roles`**, e **não** em `realm_access.roles` (formato
   padrão do Keycloak), porque o realm usa um mapper de *client role*. Ler o lugar
   errado daria 403 em toda rota interna, sem erro de compilação. O parser aceita
   os três formatos (texto, lista e ausente), e há teste para o formato texto;
3. **o token interno não tem `provider_id`** — logo "é provedor?" é uma pergunta
   respondível (`RequireProvider`), e não uma suposição.

## 2. Build, vet e formatação

```
$ go build ./...
(vazio)

$ go vet ./...
(vazio)

$ go vet -tags=integration ./...
(vazio)

$ gofmt -l ./internal/ ./cmd/ ./migrations/ ./test/
(vazio)
```

## 3. Testes unitários

```
$ go test -count=1 -race ./...
ok  	.../internal/app	3.106s
ok  	.../internal/app/ports	1.368s
ok  	.../internal/app/wagering	1.607s
ok  	.../internal/config	1.472s
ok  	.../internal/domain/ledger	1.456s
ok  	.../internal/domain/money	1.702s
ok  	.../internal/domain/wagering	1.485s
ok  	.../internal/domain/wallet	1.457s
ok  	.../internal/httpapi	2.579s          ← NOVO
ok  	.../internal/infra/auth	4.249s        ← NOVO
ok  	.../internal/infra/postgres	1.483s
ok  	.../internal/observability	1.796s
ok  	.../internal/platform/canonicaljson	1.260s
ok  	.../internal/platform/idgen	1.255s
```

**Casos da fase:** 49 em `internal/infra/auth` (30 funções `Test`) e 67 em
`internal/httpapi` (37 funções `Test`), contando subtests.

## 4. Testes de integração (containers REAIS)

```
$ TEST_DATABASE_URL=...jungle_test  OIDC_ISSUER=http://localhost:8080/realms/jungleg \
    go test -count=1 -race -tags=integration -timeout 300s ./...
ok  	.../internal/app	11.845s        ← binário real + healthcheck + SIGTERM
ok  	.../internal/infra/postgres	6.586s
ok  	.../test/integration	10.416s
```

**Casos novos de integração:** 30 em `test/integration` (9 funções `Test`, com os
subtests do contrato de status) e 2 em `internal/app`.

### 4.1. Tokens de verdade, obtidos do Keycloak

O teste **não forja token**: ele faz `POST /protocol/openid-connect/token` com
`client_credentials` nos três clients do realm e usa o que voltou. É o fluxo que o
README §2 recomenda e o que o realm deste projeto provisiona.

### 4.2. O fluxo do §9, ponta a ponta (trecho do teste)

```
POST /wallets                    → 201  {id, playerId, balance 1000.00, version 1}
POST /wagering/transactions BET  → 201  {status PROCESSED, balance 975.00, idempotentReplay false}
POST (mesmo corpo e chave)       → 200  {status PROCESSED, balance 975.00, idempotentReplay true}
GET  /wallets/{id}               → 200
GET  /wallets/{id}/ledger        → 200  entries: 2 (CREDIT 0.00→1000.00, DEBIT 1000.00→975.00)
POST /wallets/{id}/reconciliation→ 200  {stored 975.00, calculated 975.00, difference 0.00,
                                         consistent true, checkedEntries 2}
GET  /providers/provider-a/wagering/transactions/txn-1 → 200 (mesmo transactionId)
GET  /wagering/transactions/{id}  → 200  (rota interna)
```

O replay devolve **o saldo do processamento original**, não o de agora — e o
`transactionId` é o mesmo.

### 4.3. Nenhum efeito financeiro em acesso negado

```
TestHTTPProvedorNaoExecutaOperacaoInterna
  POST /wallets                        → 403
  GET  /wallets/{id}                   → 403
  GET  /wallets/{id}/ledger            → 403
  POST /wallets/{id}/reconciliation    → 403
  GET  /wagering/transactions/{id}     → 403
  contagens NO BANCO antes/depois: wallets 1/1, ledger 1/1, wager_transactions 0/0
```

O mesmo padrão em `TestHTTPInternoNaoEnviaOperacaoDeProvedor` (403 e ledger
intacto) e em `TestHTTPIdPForaDoArResponde503SemEfeito` (503 + `Retry-After`, saldo
e ledger inalterados).

### 4.4. Binário real: o healthcheck do compose e o shutdown

```
$ go test -tags=integration -run TestBinario -v ./internal/app/
=== RUN   TestBinarioRealHealthCheckEShutdown
{"level":"INFO","msg":"application_started","addr":"127.0.0.1:18099",
 "issuer":"http://localhost:8080/realms/jungleg","stopTimeout":15000000000}
--- PASS: TestBinarioRealHealthCheckEShutdown (4.18s)
--- PASS: TestBinarioRealFalhaComConfigInvalida (3.89s)
```

O teste compila `./cmd/api`, sobe o processo apontando para o PostgreSQL e o
Keycloak reais, espera o `/health/ready` responder 200, executa
`api -health-check` (o comando exato do `docker-compose.yml`) e confere que ele
sai com código 0, e por fim encerra o processo pelo sinal de término.

## 5. O teste do autenticador (eliminatório), caso a caso

```
--- PASS: TestVerifyDeTokenDeProvedor            (provider_id do token vira identidade)
--- PASS: TestVerifyDeTokenInternoSemProvedor    (interno não tem provider_id)
--- PASS: TestVerifyAceitaRolesComoTexto         (mapper pode publicar texto)
--- PASS: TestVerifySemAudienceConfigurada
--- PASS: TestVerifyRejeitaTokensInvalidos       (11 subtests, ver abaixo)
--- PASS: TestVerifyRejeitaTokenMalformado       (8 subtests)
--- PASS: TestVerifyRejeitaTokenSemSubject
--- PASS: TestJWKSECacheado                      (5 verificações = 1 busca de JWKS)
--- PASS: TestRotacaoDeChaveAceitaTokenNovo      (kid novo rebusca sem reiniciar)
--- PASS: TestChaveRemovidaDoJWKSTornaTokenInvalido
--- PASS: TestDiscoveryForaDoArEhIndisponivel    (503, não 401)
--- PASS: TestJWKSForaDoArEhIndisponivel         (503, não 401)
--- PASS: TestDiscoveryComOutroEmissorEhRecusado
--- PASS: TestIdPForaDoArNaoAceitaTokenInvalido
--- PASS: TestCheckReadinessDoIdP
```

Subtestes de `TestVerifyRejeitaTokensInvalidos` — cada um afirma o **sentinela**
(`errors.Is`), que é o que o middleware usa para escolher o status:

| Caso | Causa esperada |
| --- | --- |
| emissor diferente | `ErrEmissorInvalido` |
| audience diferente | `ErrPublicoInvalido` |
| sem audience | `ErrPublicoInvalido` |
| expirado / expirado há 1s fora da tolerância | `ErrTokenExpirado` |
| nbf no futuro | `ErrTokenAindaNaoValido` |
| sem `exp` | `ErrTokenExpirado` |
| `kid` inexistente | `ErrAssinaturaInvalida` |
| assinatura de outra chave | `ErrAssinaturaInvalida` |
| `alg: none` | `ErrAlgoritmoNaoPermitido` |
| `alg: HS256` | `ErrAlgoritmoNaoPermitido` |

O servidor OIDC de teste **assina de verdade**: gera par RSA de 2048 bits no
teste, publica o JWKS e emite o token com `rsa.SignPKCS1v15`. Sem isso, o teste de
autenticação provaria apenas que o middleware chama o verificador.

## 6. Defeitos reais encontrados e corrigidos na fase

### 6.1. `chimw.RequestSize(0)` recusava TODO corpo (dormindo desde a Fase 0)

**Sintoma:** `POST /wallets` respondia `400 {"code":"INVALID_REQUEST","message":
"JSON inválido: http: request body too large"}` — para um corpo de 90 bytes e com
o limite configurado em 1 MiB.

**Causa:** `r.Use(chimw.RequestSize(0))` estava no roteador desde a Fase 0 com o
comentário "no-op explícito". Não é no-op: com limite `0` o middleware recusa
**qualquer** corpo.

**Por que ninguém pegou antes:** nenhum teste ou rota anterior recebia corpo. O
primeiro endpoint com `POST` e corpo expôs o defeito. É a razão de o teste de
integração da borda valer a pena mesmo com o caso de uso já coberto.

**Correção:** o middleware foi removido; o limite real é o `MaxBodyMiddleware`,
que vem de `HTTP_MAX_BODY_BYTES` e produz erro claro no `decodeJSON`.

### 6.2. `AccessLog` por dentro do `Recoverer` (pendência P2) — o log registrava 200 para um 500

**Sintoma:** o `statusRecorder` do log de acesso ficava **fora** do caminho quando
o `Recoverer` escrevia a resposta de 500. O log registrava `200` para uma
requisição que respondeu `500`.

**Por que importa:** o log de acesso passa a mentir exatamente no caso em que ele
é mais necessário — a investigação de um panic em produção. E `StatusLabel` das
métricas herdaria o número errado.

**Correção:** `AccessLog` passou a **envolver** o `Recoverer` (mais externo), de
modo que o `statusRecorder` veja o status final. A ordem está documentada no
`router.go` com o motivo.

### 6.3. `fx.Provide(clock.System{})` — valor, não construtor (composição)

**Sintoma:** `must provide constructor function, got {} (type clock.System)`.

**Causa:** `clock.System{}` e `idgen.New()` são **valores**, e `Provide` espera
funções. Além disso, o caso de uso depende das **interfaces** (`ports.Clock`,
`idgen.Generator`), e o Fx casa por tipo exato.

**Correção:** providers que publicam as duas formas de cada valor (`clock.System`
e `ports.Clock`; `idgen.Generator`). A alternativa — injetar o tipo concreto no
serviço — acoplaria o caso de uso ao pacote de plataforma e mataria a substituição
do relógio no teste.

### 6.4. Grupo do Fx não é `fx.ResultTags` como argumento de `Provide`

**Sintoma:** `must provide constructor function, got {[group:"health_checkers"]}
(type fx.resultTagsAnnotation)` — e depois `fx.In structs cannot be annotated with
fx.ParamTags or fx.From`.

**Causa:** em `go.uber.org/fx@v1.24.0`, `fx.ResultTags`/`fx.ParamTags` são
`Annotation` (usadas com `fx.Annotate`), não opções de `fx.Provide`. E um
parâmetro que é struct `fx.In` não pode ser anotado: o `group` pertence ao **campo**.

**Correção:** `fx.Annotate(fn, fx.ResultTags(...))` nos providers do grupo, e uma
struct `fx.In` (`rotasDeps`) com `Checkers []httpapi.HealthChecker
\`group:"health_checkers"\``.

### 6.5. O `context.Context` do pool não pode ser o do boot

**Sintoma:** `missing type: context.Context` ao fornecer `postgres.NewPool`.

**Causa:** `NewPool(ctx, cfg)` precisa de contexto, e o Fx não fornece
`context.Context` para construtores de recurso.

**Correção:** um provider que cria `context.Background()` com o prazo de conexão
da config. Deliberadamente **não** o contexto de start do Fx: o `pgxpool` é
recurso de vida longa e o contexto de start é cancelado quando o boot termina —
passá-lo derrubaria o pool no primeiro `Start`.

### 6.6. O `-health-check` exigia a configuração inteira (P1)

**Sintoma:** o healthcheck do compose (`/app/api -health-check`) falhava porque a
flag não existia; e, ao ser implementada carregando a configuração, o processo
curto passaria a depender de `DATABASE_URL`/`OIDC_ISSUER` para dar um veredito que
é sobre o estado do serviço.

**Correção:** o modo healthcheck não carrega a config; ele consulta o próprio
`/health/ready`, cuja URL sai de `HTTP_ADDR`.

### 6.7. Três asserções de teste estavam erradas (régua, não código)

Registrado porque a lição da Fase 1 se repetiu — e desta vez em três formas:

| Asserção | O que o código fazia (certo) | Correção |
| --- | --- | --- |
| rejeição (422) traz envelope de erro | traz o **corpo do desfecho**, com `transactionId` e `failureCode` — a rejeição é desfecho auditável, não erro de transporte | teste passou a aceitar os dois formatos, declarando qual vale em cada caso |
| `cursor=%%%` deveria dar 400 | `%%` é lido como texto literal, então o cursor era "válido" e a página voltou vazia | o valor de teste passou a ser realmente inválido (`###nao-e-cursor###`) |
| mesmo `externalTransactionId` em dois provedores = conflito | são **duas** operações: a identidade financeira é o par `(providerId, externalTransactionId)` | o teste passou a afirmar o isolamento (cada provedor vê o SEU resultado) |

### 6.8. Dois loggers no mesmo processo — o log de acesso saía em TEXTO

**Sintoma (achado ao SUBIR o binário, não em teste):** o mesmo processo emitia
**dois formatos** no stdout:

```
time=... level=INFO msg=http_server_started addr=127.0.0.1:18099      ← texto
{"time":"...","level":"INFO","msg":"application_started",...}          ← JSON
time=... level=INFO msg=http_request path=/health/ready status=200     ← texto
```

**Causa:** `app.Run` montava o **próprio** `slog.NewJSONHandler`, enquanto o resto
do grafo recebia `observability.NewLogger(cfg)` — que em ambiente `local` usa
`NewTextHandler`. Dois loggers, dois formatos, e o `service`/`env` de um lado só.

**Por que importa:** o README §12 pede **log estruturado** com os identificadores
para rastrear a operação. O log de acesso é justamente o que mais se consulta num
incidente — e era o que saía fora do formato. Pior: o defeito só aparece quando
alguém **roda o binário**; nenhum teste olhava o stdout do processo.

**Correção:** um logger por processo, vindo da configuração. `Run` passou a usar
`observability.NewLogger(cfg)` (e a acrescentar `version`/`commit`). Isso também
devolveu o comportamento certo por ambiente: **texto** em `local`, **JSON** em
produção. Provado nos dois modos:

```
APP_ENV=local
time=... level=INFO msg=http_server_started service=jungleg env=local addr=127.0.0.1:18099
time=... level=INFO msg=application_started service=jungleg env=local version=dev ...
time=... level=INFO msg=http_request service=jungleg env=local method=GET path=/health/ready status=200 ...

APP_ENV=production
{"time":"...","level":"INFO","msg":"http_server_started","service":"jungleg","env":"production","addr":"127.0.0.1:18099"}
{"time":"...","level":"INFO","msg":"application_started","service":"jungleg","env":"production","version":"dev",...}
{"time":"...","level":"INFO","msg":"http_request","service":"jungleg","env":"production","method":"GET","path":"/health/live","status":200,...}
```

### 6.9. Log final: o binário real subindo contra Postgres e Keycloak de verdade

```
$ go build -o bin/api.exe ./cmd/api
$ HTTP_ADDR=127.0.0.1:18099 DATABASE_URL=...jungle_test \
  OIDC_ISSUER=http://localhost:8080/realms/jungleg \
  OIDC_DISCOVERY_URL=http://localhost:8080/realms/jungleg OIDC_AUDIENCE=jungleg-api \
  ./bin/api.exe

GET /health/ready → 200 {"dependencies":{"oidc":"UP","postgres":"UP"},"status":"UP"}
GET /health/live  → 200 {"status":"UP"}
GET /metrics      → 200 (famílias `jungleg_wager_*` exportadas)
./bin/api.exe -health-check → exit code 0        ← o comando exato do compose
```

O `oidc: UP` é o readiness do IdP **sem cache** — ou seja, o serviço falou com o
Keycloak naquele instante, e não apenas uma vez no boot.

### 6.10. Armadilha reconfirmada: `AccessLog` e o `statusRecorder` com panic

Ver 6.2 — o mesmo defeito tem duas faces (log e métrica). Registrado

## 7. Decisões de design registradas

1. **O `providerId` vem do TOKEN.** Se viesse do corpo, qualquer token válido
   operaria qualquer provedor. Há teste em que o corpo pede `provider-a` com token
   de `provider-b` e a resposta é 403 **sem chamar o caso de uso**.
2. **Operação de outro provedor ⇒ não encontrado**, e não 403 (devolver 403
   confirmaria a existência). O 403 fica para o `:providerId` do path divergir.
3. **`GET /wagering/transactions/{id}` é do serviço interno.** O `{id}` é o
   identificador INTERNO; expô-lo ao provedor entregaria o isolamento (ele
   poderia varrer UUIDs).
4. **`alg` fixo em RS256**, decidido pelo serviço. `kid` obrigatório. Sem
   dependência externa: `crypto/rsa` + `encoding/json` + `encoding/base64`.
5. **Duas classes de erro:** token inválido (401) e IdP fora (503). Sem a
   distinção, uma queda do Keycloak faria o cliente descartar um token válido.
6. **Readiness do IdP fura o cache de discovery** — com o cache, ele responderia
   UP durante todo o TTL mesmo com o Keycloak caído.
7. **Rejeição é desfecho no corpo; erro é envelope.** `422` com
   `{transactionId, status, balance:null, failureCode}` versus
   `{"error":{code,message,failureCode}}` para o que impede a operação de existir.
8. **Chave da abertura derivada** (`abertura:{playerId}:{moeda}`) quando o cliente
   não manda `Idempotency-Key` — o README não define esse header para `POST
   /wallets`, e sem chave o caso de uso recusa. A chave do cliente, quando existe,
   é usada como veio.
9. **Rota de negócio nunca é registrada sem autenticador.** `RegistrarRotas`
   devolve sem montar nada se `auth == nil`: falhar fechado.

## 8. Interpretações adotadas (o §15 pede que sejam explícitas)

1. `POST /wallets` **não** tem header de idempotência no enunciado; a chave é
   derivada (ver 7.8) e o reenvio do mesmo pedido devolve a carteira com
   `idempotentReplay: true`.
2. A **rejeição de negócio** responde 422 com o corpo do desfecho (com
   `failureCode`), e não com o envelope de erro. As situações continuam
   distinguíveis pelo contrato — que é o que o README exige — e o cliente ganha o
   `transactionId` da operação auditável.
3. `GET /wagering/transactions/{id}` exige role `internal`, porque o identificador
   é interno. O provedor consulta pela rota dele.
4. O token do serviço interno **não** é aceito nas rotas de provedor (403): sem
   `provider_id` não existe provedor autorizado.
5. Roles `internal` e `provider` são as do realm deste projeto; a checagem é por
   presença de role no claim `roles`.

## 9. Limitações conhecidas da fase

- **A validação de `azp`** só é aplicada quando o token tem **múltiplas**
  audiences (regra do OIDC Core §3.1.3.7). Com uma audience só — o que o realm
  emite hoje — ela não tem o que conferir.
- **Não há introspecção de token (RFC 7662)**: a validação é local (assinatura +
  claims). Revogar um token antes do `exp` não é detectado — é o comportamento
  normal de validação JWT, e está registrado como limitação.
- **Rate limiting** por identidade não existe (não é exigido pelo enunciado).
- As **métricas de negócio** alimentadas pela borda nesta fase são
  `wager_operations_total`, `wager_operation_duration_seconds`,
  `wager_idempotent_replays_total` e `wager_reconciliation_divergences_total`. O
  restante (`retries`, `dlq`, atraso da outbox, idade do pendente) é das Fases 8–11.
- O **SQS continua sem consumidor** (Fase 8): a equivalência HTTP × SQS do hash de
  idempotência é provada quando o consumidor existir.

## 10. Rotina de execução da fase

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'; $env:GOCACHE='E:\jungleg\.gocache\build'; $env:GOPATH='E:\jungleg\.gocache\gopath'

# containers
docker compose up -d postgres keycloak
docker ps --format '{{.Names}}\t{{.Status}}'

# banco de TESTE (os testes truncam tabelas)
$env:TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
go build -o bin\migrate.exe .\cmd\migrate\
$env:DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungleg?sslmode=disable'
.\bin\migrate.exe up

# validacao
go build ./... ; go vet ./... ; go vet -tags=integration ./...
gofmt -l .\internal\ .\cmd\ .\migrations\ .\test\
go test -count=1 -race ./...

# IdP real para a integracao
$env:OIDC_ISSUER='http://localhost:8080/realms/jungleg'
$env:OIDC_DISCOVERY_URL='http://localhost:8080/realms/jungleg'
$env:OIDC_AUDIENCE='jungleg-api'
go test -count=1 -race -tags=integration -timeout 300s ./...

# so a borda HTTP autenticada
go test -count=1 -tags=integration -timeout 300s -v -run TestHTTP ./test/integration/

# o autenticador, caso a caso
go test -count=1 -race -v ./internal/infra/auth/
```
