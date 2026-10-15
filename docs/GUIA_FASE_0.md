# GUIA DA FASE 0 — Entendendo tudo o que foi construído

> **Para quem é este guia:** você, que vai defender este projeto numa entrevista técnica.
> Aqui está **o que existe, onde está, para que serve e por que foi feito assim** — pasta por pasta, arquivo por arquivo, e linha a linha nos arquivos importantes.
>
> **Como ler:** não leia de uma vez. Siga a ordem das seções. Ao final de cada arquivo há um bloco **"Teste-se"** com perguntas. Se você não souber responder, releia a seção antes de seguir.
>
> **Estado atual:** apenas a Fase 0 (fundação). Nenhuma regra financeira foi escrita ainda.

---

## ÍNDICE

1. [O modelo mental: as 4 camadas](#1-o-modelo-mental-as-4-camadas)
2. [O caminho de uma requisição](#2-o-caminho-de-uma-requisição)
3. [A árvore de pastas](#3-a-árvore-de-pastas)
4. [Conceitos de Go que você PRECISA dominar antes](#4-conceitos-de-go-que-você-precisa-dominar-antes)
5. [Arquivos de infraestrutura, um por um](#5-arquivos-de-infraestrutura-um-por-um)
6. [Arquivos de código, um por um](#6-arquivos-de-código-um-por-um)
7. [Os arquivos de teste](#7-os-arquivos-de-teste)
8. [Como rodar tudo (comandos)](#8-como-rodar-tudo-comandos)
9. [Problemas que já resolvemos (e por quê)](#9-problemas-que-já-resolvemos-e-por-quê)
10. [Checklist de estudo](#10-checklist-de-estudo)

---

## 1. O modelo mental: as 4 camadas

Todo projeto Go sério separa o código em camadas, e a regra de ouro é: **as setas apontam para dentro**. A camada de dentro não conhece a de fora.

```
┌──────────────────────────────────────────────────────────────┐
│  BORDA (infraestrutura)                                      │
│  HTTP, SQS, PostgreSQL, Keycloak                             │
│  internal/httpapi, internal/infra, internal/worker           │
│                                                              │
│    ┌──────────────────────────────────────────────────┐      │
│    │  APLICAÇÃO (casos de uso)                        │      │
│    │  "processe uma BET", "abra uma carteira"         │      │
│    │  internal/app                                    │      │
│    │                                                  │      │
│    │    ┌──────────────────────────────────────┐      │      │
│    │    │  DOMÍNIO (regras de negócio)         │      │      │
│    │    │  Money, Wallet, Ledger, Transaction  │      │      │
│    │    │  internal/domain                     │      │      │
│    │    │  ★ Go puro. Zero imports externos. ★ │      │      │
│    │    └──────────────────────────────────────┘      │      │
│    └──────────────────────────────────────────────────┘      │
└──────────────────────────────────────────────────────────────┘
```

**O que isso significa na prática:**

| Camada | Pode importar | NÃO pode importar |
| --- | --- | --- |
| `domain` | só a biblioteca padrão do Go | `pgx`, `chi`, `aws-sdk`, `fx`, `httpapi` |
| `app` (casos de uso) | `domain` + interfaces próprias | `pgx`, `chi`, `aws-sdk` (só via interface) |
| `httpapi`, `infra`, `worker` | tudo | — |
| `app` (o pacote de composição Fx) | tudo (é o único que monta tudo) | — |

**Por que o enunciado exige isso:** o item 4 diz *"O domínio deve permanecer independente de Fx, HTTP, SQS e bibliotecas de persistência"*. A vantagem concreta: se amanhã trocarmos PostgreSQL por outro banco, **nenhuma linha do domínio muda**. E os testes do domínio rodam em milissegundos, sem container.

> ⚠️ **Atenção à confusão de nomes:** existem dois pacotes chamados `app`:
> - `internal/app/` → **camada de aplicação + composição Fx** (esse já existe)
> - `internal/domain/` → **domínio** (ainda não existe, vem nas Fases 1–3)
>
> Na Fase 6 vamos criar `internal/application/` para os casos de uso, separando do `internal/app/` que é a composição Fx. Vou avisar quando acontecer.

---

## 2. O caminho de uma requisição

Essa é a visão que você deve ter na cabeça ao abrir qualquer arquivo. Exemplo: um provedor envia `POST /wagering/transactions` com uma aposta.

```
                        ┌─────────────────────────────────────────┐
                        │ cmd/api/main.go                         │
                        │  main() → app.Run(version, commit)      │
                        └────────────────┬────────────────────────┘
                                         │
                        ┌────────────────▼────────────────────────┐
                        │ internal/app/app.go                     │
                        │  fx.New(ModuleOptions(), ...)           │
                        │  monta o grafo, roda OnStart, espera    │
                        │  SIGTERM, roda OnStop                   │
                        └───────┬─────────────────────┬───────────┘
                                │                     │
              ┌─────────────────▼──────┐   ┌──────────▼──────────────────┐
              │ internal/config        │   │ internal/observability      │
              │  Load() → *Config      │   │  NewLogger(), NewMetrics()  │
              │  (falha o boot se      │   │  (injetados em todos)       │
              │   inválida)            │   └─────────────────────────────┘
              └─────────────────┬──────┘
                                │
                        ┌───────▼─────────────────────────────────┐
                        │ internal/httpapi/router.go              │
                        │  NewRouter(...) → *Routes               │
                        │  devolve 4 sub-roteadores:              │
                        │   Base / Public / Internal / Provider   │
                        └───────┬─────────────────────────────────┘
                                │  NewServer() → *http.Server
                                │  (ListenAndServe no OnStart)
                                │
   ┌────────────────────────────▼──────────────────────────────────────┐
   │  AQUI COMEÇA UMA REQUISIÇÃO REAL                                  │
   │                                                                   │
   │  net/http recebe a conexão TCP                                    │
   │      ↓                                                            │
   │  RequestIDMiddleware     cria requestId, lê/gera correlationId,   │
   │                          coloca os dois no context.Context        │
   │      ↓                                                            │
   │  chi/middleware.RealIP   resolve o IP real atrás de proxy         │
   │      ↓                                                            │
   │  RecovererMiddleware     converte panic de bug em 500             │
   │      ↓                                                            │
   │  AccessLogMiddleware     loga método/rota/status/duração (JSON)   │
   │      ↓                                                            │
   │  TimeoutMiddleware       aplica prazo de HTTP_REQUEST_TIMEOUT     │
   │      ↓                                                            │
   │  MaxBodyMiddleware       limita o corpo a 1 MiB                   │
   │      ↓                                                            │
   │  handler da rota (ainda não existe na Fase 0)                     │
   │      ↓                                                            │
   │  retorna erro tipado → WriteError() → status HTTP correto         │
   └───────────────────────────────────────────────────────────────────┘
```

**Ponto-chave para a entrevista:** o middleware coloca o `correlationId` no `context.Context`. Dali em diante, **qualquer** função que receba esse contexto (repositório, serviço, worker) tem acesso à correlação sem precisar de parâmetro extra. E o `contextHandler` em `observability/logger.go` injeta automaticamente no log.

---

## 3. A árvore de pastas

```
E:\jungleg\
│
├── README.md                    ← enunciado do desafio (deles) — NÃO MEXER
├── PLANO.md                     ← nosso plano/checklist de 2 dias
├── go.mod                       ← identidade do módulo + versões
├── go.sum                       ← hashes de verificação das dependências
├── .gitignore                   ← o que NÃO vai para o git
├── .env.example                 ← modelo de variáveis de ambiente
│
├── Dockerfile                   ← receita da imagem da aplicação
├── docker-compose.yml           ← sobe Postgres + Keycloak + LocalStack + app
├── Makefile                     ← atalhos (make up, make test...)
│
├── cmd/                         ← um diretório por BINÁRIO executável
│   └── api/
│       └── main.go              ← 15 linhas: só chama app.Run()
│
├── deploy/                      ← configuração de INFRAESTRUTURA (não é código Go)
│   ├── postgres/init/
│   │   └── 01-databases.sql     ← cria os bancos na primeira subida
│   ├── keycloak/
│   │   └── realm.json           ← realm, clients, roles e usuários de teste
│   └── localstack/
│       └── init-queues.sh       ← cria as filas FIFO + DLQ no SQS local
│
├── internal/                    ← código privado do projeto
│   ├── config/
│   │   ├── config.go            ← lê e valida o ambiente
│   │   └── config_test.go
│   ├── observability/
│   │   ├── context.go           ← correlationId/requestId no context
│   │   ├── logger.go            ← slog + injeção automática de correlação
│   │   ├── metrics.go           ← métricas Prometheus
│   │   └── observability_test.go
│   ├── httpapi/
│   │   ├── router.go            ← monta rotas e middlewares
│   │   ├── middleware.go        ← correlação, log de acesso, timeout, limite
│   │   ├── response.go          ← erros tipados + JSON estrito
│   │   ├── health.go            ← /health/live e /health/ready
│   │   ├── recover.go           ← panic → 500
│   │   └── router_test.go
│   └── app/
│       ├── app.go               ← composição Fx + ciclo de vida
│       └── app_test.go
│
├── docs/
│   ├── GUIA_FASE_0.md           ← este arquivo
│   └── QUESTOES.md              ← perguntas de fixação
│
└── .gocache/                    ← (NÃO versionado) cache do Go dentro do workspace
    ├── mod/                     ← módulos baixados
    ├── build/                   ← resultados de compilação
    └── gopath/
```

**Por que `internal/`?** Essa pasta tem significado **especial no Go**: qualquer pacote dentro de `internal/` só pode ser importado por código do **mesmo módulo** (ou de um subdiretório do pai de `internal`). Ou seja, é impossível alguém importar suas regras de negócio de fora, ou você importar código privado de outra pessoa. É o compilador te protegendo de acoplamento acidental.

**Por que `cmd/`?** Convenção do ecossistema: cada subpasta de `cmd/` produz **um** binário. `cmd/api` → binário `api`. `cmd/migrate` → binário `migrate` (Fase 4). Assim você nunca mistura código de bootstrap com código de biblioteca.

---

## 4. Conceitos de Go que você PRECISA dominar antes

Se algum destes estiver confuso, pare e me pergunte — eles aparecem em **todo** arquivo daqui pra frente.

### 4.1. `context.Context` — o "passe de ônibus" da requisição

É o primeiro parâmetro de quase toda função que faz I/O. Ele carrega **duas coisas**:

1. **Cancelamento e prazo** — quando o cliente desiste ou o timeout estoura, tudo que depende dele é avisado.
2. **Valores de escopo de requisição** — como o `correlationId`.

```go
ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
defer cancel()   // ← SEMPRE. Sem isso vaza goroutine/timer.
```

**Regras de ouro:**
- `ctx` é **sempre o primeiro parâmetro**, nunca dentro de uma struct.
- `nil` nunca é passado como contexto — use `context.Background()`.
- `context.WithValue` é **só** para dados de transporte (correlationId, requestId). **Nunca** para passar dependências (isso é papel da injeção).

### 4.2. Ponteiro (`*T`) vs valor (`T`)

```go
func (w Wallet) Debit(...)  // recebe CÓPIA. Mexer aqui não afeta o original.
func (w *Wallet) Debit(...) // recebe a referência. Mexer aqui afeta o original.
```

- **Value Object** (imutável, ex.: `Money`) → por **valor**. Copiar é barato e ninguém pode alterar por acidente.
- **Entidade mutável** (ex.: `Wallet`, `Config`) → por **ponteiro**.

Foi exatamente isso que o teste do Fx pegou: `config.Load()` devolvia `Config` (valor) mas `runHTTPServer` pedia `*config.Config` (ponteiro). **São tipos diferentes para o Go** — o grafo não fechava.

### 4.3. Struct tags — metadados no código

```go
Addr string `env:"ADDR" envDefault:":8080"`
```

Aquilo entre crases é uma **struct tag**: uma string lida por **reflexão** em tempo de execução. O Go em si ignora. Quem usa é a biblioteca: aqui, `caarlos0/env` lê a tag `env` e busca a variável `ADDR`. O `envPrefix:"HTTP_"` na struct `HTTPConfig` faz o prefixo se somar: `HTTP_` + `ADDR` = `HTTP_ADDR`.

### 4.4. Interfaces — declaradas por quem USA

```go
// Em internal/httpapi/health.go:
type HealthChecker interface {
    CheckReadiness(ctx context.Context) error
    Name() string
}
```

Repare: **quem define a interface é o consumidor** (o `httpapi`), não o implementador. Isso é idiomático em Go e é o oposto de Java/C#. Vantagem: o `httpapi` não precisa conhecer (nem importar) o pacote de banco de dados. Ele só diz "me dê algo que saiba se checar". Nos testes, um `fakeChecker` de 5 linhas satisfaz a interface.

### 4.5. Uber Fx — injeção de dependência

Fx é uma biblioteca de **injeção de dependência** baseada em reflexão. Três conceitos:

| Conceito | O que faz | Analogia |
| --- | --- | --- |
| `fx.Provide(f)` | Registra um **construtor** | "quando alguém pedir X, chame f" |
| `fx.Invoke(g)` | Executa `g` **uma vez** no boot | "e no começo, faça isso" |
| `fx.Lifecycle` | Ganchos de start/stop | "ao subir, faça A; ao descer, faça B" |

Exemplo real do projeto:

```go
fx.Provide(config.Load)          // alguém pede *Config? chama config.Load()
fx.Provide(observability.NewLogger)  // pede *slog.Logger? chama NewLogger(cfg)
fx.Provide(httpapi.NewServer)    // pede *http.Server? chama NewServer(cfg, routes)
```

O Fx **olha os tipos dos parâmetros** de cada construtor e monta o grafo sozinho. Em `NewServer(cfg *config.Config, routes *httpapi.Routes)`, ele vê que precisa de `*Config` e `*Routes` — e sabe como produzir os dois.

**O erro que você vai cometer:** esquecer um `fx.Provide` e ver `missing type: *alguma.Coisa`. **Sempre rode `fx.ValidateApp`** (temos teste pra isso) antes de subir.

---

## 5. Arquivos de infraestrutura, um por um

### 5.1. `go.mod`

```
module github.com/FeliPoxZ/jungle_desafio

go 1.25.0

toolchain go1.26.5
```

| Linha | Significado |
| --- | --- |
| `module ...` | O **nome de import** do seu projeto. Todos os imports internos começam com isso: `github.com/FeliPoxZ/jungle_desafio/internal/config`. |
| `go 1.25.0` | Versão **mínima** da linguagem que o código exige. Escolhemos 1.25 porque é estável e a imagem `golang:1.25-alpine` existe. |
| `toolchain go1.26.5` | A ferramenta **específica** usada para compilar. Sua máquina tem 1.26.5, então ela é registrada aqui. |

**Por que declarar as duas:** o item 4 do enunciado exige *"declare a versão utilizada em go.mod e no Dockerfile"*. Com o `toolchain`, quem clonar o projeto em uma máquina com Go mais antigo baixa automaticamente o 1.26.5 — build reprodutível.

**Sobre o erro que você viu:** quando rodamos `go mod tidy` antes de escrever o código, o Go **removeu** chi, fx e uuid, porque ninguém os importava ainda. Isso é `go mod tidy` fazendo o trabalho dele: deixa no `go.mod` só o que o código realmente usa. Depois que escrevemos os arquivos, rodamos de novo e eles voltaram.

---

### 5.2. `.gitignore`

```gitignore
/bin/            ← binários compilados
/dist/
*.exe
*.test
coverage.out    ← relatório de cobertura
.env            ← ⚠️ SEGREDOS NUNCA VÃO PARA O GIT
/.gocache/      ← cache do Go dentro do workspace
.idea/ .vscode/ ← configuração de editor (cada um usa o seu)
```

**O ponto mais importante:** `.env` está aqui e `.env.example` **não**. O `.env.example` documenta *quais* variáveis existem (com valores de exemplo). O `.env` tem os valores reais e **nunca** é commitado. Isso é exigência explícita do item 15: *"Inclua .env.example com valores locais de exemplo, sem segredos reais"*.

---

### 5.3. `Dockerfile` — explicado linha a linha

```dockerfile
FROM golang:1.25-alpine AS deps
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
```

| Linha | O que faz | Por que assim |
| --- | --- | --- |
| `AS deps` | Nomeia este estágio | Docker multi-stage: só o último vira imagem final |
| `COPY go.mod go.sum* ./` | Copia **só** os arquivos de dependência | ⭐ **Truque de cache:** o Docker invalida camadas a partir da primeira linha que mudou. Como `go.mod` muda pouco, a camada `go mod download` é reaproveitada em quase todo build. Se copiássemos o código antes, **toda** mudança de código rebaixaria as dependências de novo. |
| `RUN go mod download` | Baixa as dependências | Camada cacheável separada |

```dockerfile
FROM deps AS build
ARG TARGET=api
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
        -o /out/app ./cmd/${TARGET}
```

| Flag | O que faz | Por que |
| --- | --- | --- |
| `CGO_ENABLED=0` | Desliga o C interop | Binário **estático**: roda em imagem sem libc (alpine/distroless) |
| `GOOS=linux` | Compila para Linux | Você está no Windows; a imagem roda Linux |
| `-trimpath` | Remove caminhos absolutos | Build **reproducível**: o binário não vaza `C:\Users\PoxZe\...` |
| `-s -w` | Remove símbolos e debug info | Imagem bem menor |
| `-X main.version=...` | Injeta string em variável | ⭐ Assim `version`/`commit` chegam em `cmd/api/main.go` **sem** recompilar código |
| `${TARGET}` | Escolhe o binário | **Um Dockerfile** serve `api` e `migrate` |

```dockerfile
FROM alpine:3.21 AS final
RUN apk add --no-cache ca-certificates tzdata wget && \
    addgroup -S -g 10001 app && \
    adduser -S -u 10001 -G app -h /app app
WORKDIR /app
COPY --from=build /out/app /app/app
USER 10001:10001
ENTRYPOINT ["/app/app"]
```

| Linha | Por que |
| --- | --- |
| `ca-certificates` | Sem isso, HTTPS para o IdP em produção falha com "certificate signed by unknown authority" |
| `tzdata` | Sem isso, `time.LoadLocation("America/Sao_Paulo")` falha. Usamos UTC, mas é bom ter |
| `wget` | Usado pelo healthcheck do compose |
| `adduser ... 10001` + `USER 10001` | ⭐ **Nunca rode como root.** Se alguém explorar a app, o estrago é limitado |
| `COPY --from=build` | Traz **só** o binário. Toolchain e código-fonte **não** existem na imagem final |
| `ENTRYPOINT` (forma exec) | Sem `sh -c`: o sinal SIGTERM chega **direto** no processo. Com `sh -c`, o shell viraria PID 1 e engoliria o sinal — e o shutdown gracioso nunca rodaria |

> 💡 **Por que isso importa no desafio:** o item 10 exige *"Em SIGTERM, pare de buscar trabalho e conclua o processamento em andamento"*. Se o Dockerfile usasse a forma shell do ENTRYPOINT, o SIGTERM não chegaria ao processo e o item estaria **violado** por causa de uma linha de Dockerfile.

---

### 5.4. `docker-compose.yml` — serviço por serviço

**Cabeçalho:**
```yaml
name: jungleg
```
Nome do projeto: todos os containers viram `jungleg-<serviço>`. Evita conflito com outros projetos na sua máquina.

---

#### Serviço `postgres`

```yaml
postgres:
  image: postgres:16-alpine
  environment:
    POSTGRES_USER: ${POSTGRES_USER:-jungleg}
```
`${VAR:-default}` = "use a variável de ambiente, ou `jungleg` se não existir".

```yaml
  command:
    - postgres
    - -c
    - log_min_duration_statement=500ms
    - -c
    - log_lock_waits=on
    - -c
    - deadlock_timeout=1s
    - -c
    - max_connections=200
```

| Configuração | Para que serve |
| --- | --- |
| `log_min_duration_statement=500ms` | Loga toda query que passar de 500ms. Acha consulta lenta |
| `log_lock_waits=on` | ⭐ Loga quando uma transação **espera por lock**. É o que vai provar o teste das duas apostas de 80.00 |
| `deadlock_timeout=1s` | Quanto o Postgres espera antes de checar deadlock |
| `max_connections=200` | Aguentar várias instâncias da app (o teste exige 3) e o pool de cada uma |

```yaml
  volumes:
    - postgres-data:/var/lib/postgresql/data
    - ./deploy/postgres/init:/docker-entrypoint-initdb.d:ro
```
- `postgres-data` é um **volume nomeado**: os dados sobrevivem a `docker compose down`. (Só `down --volumes` apaga.)
- `:/docker-entrypoint-initdb.d:ro` → scripts nessa pasta rodam **apenas na primeira vez** (quando o volume está vazio). `:ro` = read-only.

```yaml
  healthcheck:
    test: ["CMD-SHELL", "pg_isready -U ${POSTGRES_USER:-jungleg} -d ${POSTGRES_DB:-jungleg}"]
    interval: 5s
    timeout: 5s
    retries: 20
    start_period: 10s
```
⭐ **Healthcheck é o que permite `depends_on: condition: service_healthy`.** Sem ele, o Docker só espera o container *iniciar*, e a app tentaria conectar no Postgres que ainda está subindo → erro de conexão no boot. Com ele, o Docker espera o Postgres **responder**.

---

#### Serviço `keycloak` (o IdP)

```yaml
  command: ["start-dev", "--import-realm", "--health-enabled=true"]
```
| Parte | Significado |
| --- | --- |
| `start-dev` | Modo desenvolvimento: HTTP sem TLS, banco embutido configurável, sem cache de cluster |
| `--import-realm` | Importa `realm.json` da pasta montada. **Provisionamento automático** exigido no item 15 |
| `--health-enabled=true` | Liga o endpoint de health na porta de gerenciamento (9000) |

```yaml
  environment:
    KC_DB: postgres
    KC_DB_URL: jdbc:postgresql://postgres:5432/keycloak
    KC_HOSTNAME: http://localhost:8080
```
| Variável | Por que |
| --- | --- |
| `KC_DB*` | Usa **o mesmo** container Postgres, mas banco separado (`keycloak`). Economiza um container |
| `KC_HOSTNAME` | ⭐ **A variável mais importante aqui.** Ela fixa o `iss` do token em `http://localhost:8080/realms/jungleg`. Sem isso, dependendo de quem chama, o issuer mudaria e a validação do token falharia de forma intermitente |

```yaml
  healthcheck:
    test: ["CMD-SHELL", "exec 3<>/dev/tcp/127.0.0.1/9000 && echo -e 'GET /health/ready ...' >&3 && grep -q 'UP' <&3"]
```
A imagem do Keycloak **não tem** `curl`/`wget`. O truque `exec 3<>/dev/tcp/...` abre um socket TCP usando apenas o bash embutido, escreve um `GET /health/ready` manualmente e procura `UP` na resposta. É verboso, mas é o healthcheck que funciona nessa imagem.

---

#### Serviço `localstack` (SQS local)

```yaml
  environment:
    SERVICES: sqs
    PERSISTENCE: "1"
  volumes:
    - ./deploy/localstack:/etc/localstack/init/ready.d:ro
```
| Item | Por que |
| --- | --- |
| `SERVICES: sqs` | Sobe **só** o SQS (mais rápido, menos memória) |
| `PERSISTENCE: "1"` | As filas sobrevivem ao restart |
| `init/ready.d` | O LocalStack executa os scripts dessa pasta **quando está pronto** — ou seja, `init-queues.sh` roda automaticamente |

---

#### Serviço `localstack-init`

```yaml
  image: amazon/aws-cli:latest
  profiles: ["init", "tools"]
  entrypoint: ["/bin/sh", "/scripts/init-queues.sh"]
```
| Item | Por que |
| --- | --- |
| `amazon/aws-cli` | Container com o CLI da AWS já instalado |
| `profiles` | ⭐ **Não sobe por padrão.** Você roda sob demanda: `docker compose run --rm localstack-init`. Isso evita recriar filas toda vez |
| `entrypoint` explícito | Garante que o script rode (a imagem não tem o script por padrão) |

---

#### Serviço `migrate`

```yaml
  build:
    context: .
    dockerfile: Dockerfile
    args:
      TARGET: migrate
  command: ["up"]
  depends_on:
    postgres:
      condition: service_healthy
  restart: "no"
```
⭐ **Esta é a peça que garante a ordem correta de subida:**
1. Postgres sobe e fica **healthy**
2. `migrate up` roda, aplica o schema e **termina com sucesso**
3. **Só então** a API sobe (por causa do `service_completed_successfully` abaixo)

Sem isso, a API poderia subir antes do schema existir.

---

#### Serviço `api`

```yaml
  environment:
    OIDC_ISSUER: http://localhost:8080/realms/jungleg
    OIDC_DISCOVERY_URL: http://host.docker.internal:8080/realms/jungleg
```
⭐ **Este par de variáveis é o pulo do gato.** Entenda bem, porque isso confunde muita gente:

- O `iss` dentro do token é `http://localhost:8080/...` (fixado pelo `KC_HOSTNAME`). **A validação compara com `OIDC_ISSUER`.** Então ele tem que ser exatamente isso.
- Mas o container da API **não consegue** alcançar `localhost:8080` — dentro do container, `localhost` é o próprio container! Ele precisa de `host.docker.internal` (que aponta para a sua máquina, onde a porta 8080 do Keycloak está publicada).

**Resultado:** `OIDC_ISSUER` = o que está *escrito no token*; `OIDC_DISCOVERY_URL` = *onde buscar as chaves públicas*. Podem ser endereços diferentes, e neste caso **são**.

```yaml
  extra_hosts:
    - "host.docker.internal:host-gateway"
```
No Linux, `host.docker.internal` não existe por padrão; isso força a resolução para o gateway. No Windows/Mac já funciona, mas deixar explícito torna o compose portável.

```yaml
      migrate:
        condition: service_completed_successfully
```
⭐ Espera o serviço `migrate` **terminar com exit 0**. Diferente de `service_healthy`, que espera um container **ficar** saudável.

**Nota:** o `healthcheck` do serviço `api` usa `/app/app -health-check`, uma flag que **ainda não existe** no código. Vou corrigir isso na Fase 7, quando os endpoints estiverem completos. (Anotado como pendência!)

---

### 5.5. `deploy/postgres/init/01-databases.sql`

```sql
SELECT 'CREATE DATABASE jungle_test OWNER jungleg'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'jungle_test')\gexec
```

| Pedaço | Por que |
| --- | --- |
| `SELECT 'CREATE DATABASE ...'` | `CREATE DATABASE` não aceita `IF NOT EXISTS` no Postgres. Então montamos o comando como **string** |
| `WHERE NOT EXISTS (...)` | Só gera a string se o banco ainda não existir |
| `\gexec` | ⭐ Meta-comando do `psql`: *"pegue o resultado da query e execute como SQL"*. É o que transforma a string em comando |

**Os três bancos:**
| Banco | Para que |
| --- | --- |
| `jungleg` | Desenvolvimento |
| `jungle_test` | ⭐ Testes de integração. **Isolado de propósito**: os testes truncam tabelas entre cenários e não podem apagar seus dados de dev |
| `keycloak` | Schema do IdP |

---

### 5.6. `deploy/localstack/init-queues.sh`

```sh
set -eu
```
| Flag | O que faz |
| --- | --- |
| `-e` | Aborta no **primeiro** comando que falhar (sem isso, o script continua e mascara o erro) |
| `-u` | Aborta se usar variável **não definida** (pega erro de digitação) |

```sh
queue_url() {
  aws --endpoint-url "$ENDPOINT" sqs get-queue-url --queue-name "$1" --query 'QueueUrl' --output text 2>/dev/null
}
```
Função que consulta a URL de uma fila. `2>/dev/null` descarta o erro (filas inexistentes são **esperadas** na primeira execução). `--query`/`--output text` fazem o CLI devolver só o valor, sem JSON — é o jeito de usar o `aws-cli` dentro de script.

```sh
ensure_queue() {
  name="$1"; shift          # ← pega o 1º argumento como nome, remove ele da lista
  if url="$(queue_url "$name")" && [ -n "$url" ] && [ "$url" != "None" ]; then
    log "fila já existe: $name"
  else
    log "criando fila: $name"
    aws ... sqs create-queue --queue-name "$name" "$@" >/dev/null   # ← "$@" = atributos extras
  fi
}
```
⭐ Isso é **idempotência no script**: rodar duas vezes não dá erro. `"$@"` repassa os argumentos restantes (os `--attributes`).

**A ordem importa:** as DLQs são criadas **primeiro**, porque a fila principal precisa do ARN delas para a *redrive policy*:

```sh
TXN_DLQ_ARN="$(aws ... get-queue-attributes ... --attribute-names QueueArn ...)"
```

```sh
aws sqs set-queue-attributes --queue-url "$TXN_URL" --attributes "{
  \"RedrivePolicy\": \"{\\\"deadLetterTargetArn\\\":\\\"$TXN_DLQ_ARN\\\",\\\"maxReceiveCount\\\":\\\"$MAX_RECEIVE_COUNT\\\"}\"
}"
```
> 🔍 **Entenda os escapes:** `RedrivePolicy` é um atributo cujo **valor é um JSON em string**. Dentro de uma string JSON, aspas viram `\"`. Estamos dentro de um shell script, então cada `\` precisa de mais um `\`. Resultado: `\\\"` no arquivo = `\"` no shell = `"` no JSON interno.

**As 3 decisões de configuração das filas:**

| Configuração | Valor | Por que |
| --- | --- | --- |
| `ContentBasedDeduplication` | **false** | ⭐ A deduplicação que vale é a **nossa** (inbox + constraint no banco). A do SQS dura só 5 minutos e não sobrevive a reinício — o enunciado exige idempotência **persistente** (item 5.2). Deixar `true` daria uma falsa sensação de segurança |
| `VisibilityTimeout` | 60s (entrada) / 30s (saída) | Tempo que a mensagem fica "invisível" após ser lida. **Precisa ser maior que o processamento**, senão outro consumidor pega a mesma mensagem → movimentação duplicada |
| `RedrivePolicy` | `maxReceiveCount=5` | Após 5 recebimentos sem sucesso, a mensagem vai para a DLQ |

---

### 5.7. `deploy/keycloak/realm.json` — a autenticação explicada

Um "realm" é um **espaço isolado de identidades** no Keycloak. O nosso se chama `jungleg`.

**As duas roles de realm — este é o coração da autorização:**

```json
{ "name": "provider",  "description": "Acessa apenas as PRÓPRIAS transações..." }
{ "name": "internal",  "description": "Serviço interno: único que abre/consulta carteira..." }
```

**Os 4 clients:**

| Client | Tipo | Papel |
| --- | --- | --- |
| `provider-service` | `client_credentials` | Representa o provedor **provider-a**. Tem o claim fixo `provider_id: "provider-a"` |
| `provider-b-service` | `client_credentials` | Representa **provider-b**, para os **testes de isolamento** (B tentando acessar A) |
| `internal-service` | `client_credentials` | Role `internal`. Único que pode `POST /wallets`, ler carteira e reconciliar |
| `jungleg-api` | `bearer-only` | ⭐ **Não é um client utilizável.** Existe só como **audience** |

**Por que existem dois provedores:** o item 2 exige *"Provedores acessam apenas suas próprias transações, inclusive em replays"*. Sem um segundo provedor, você **não consegue provar** o isolamento. Com ele, o teste fica: token de B + `providerId` de A = **403**.

**Por que o claim `provider_id` é fixo no client:**

```json
{
  "protocolMapper": "oidc-hardcoded-claim-mapper",
  "config": {
    "claim.name": "provider_id",
    "claim.value": "provider-a"
  }
}
```
⭐ Isso é **fundamental para a segurança**: quem define qual provedor o token representa é o **IdP**, não o corpo da requisição. Se o `providerId` viesse do JSON, qualquer cliente poderia se declarar outro provedor. Com o claim no token, é criptograficamente garantido.

**A audience (`jungleg-api`):**
```json
{
  "protocolMapper": "oidc-audience-mapper",
  "config": { "included.client.audience": "jungleg-api" }
}
```
⭐ Sem isso, um token emitido para **qualquer outro sistema** que compartilhe o mesmo realm seria aceito pela nossa API. A audience amarra o token ao **público pretendido**. A API valida `aud == "jungleg-api"`.

**O usuário `operator`:** existe para o fluxo `password` (`direct access grants`), útil para depurar manualmente pelo Postman/curl. Os clients de serviço usam `client_credentials`.

---

### 5.8. `Makefile`

```make
GOENV := GOMODCACHE=$(CURDIR)/.gocache/mod GOCACHE=$(CURDIR)/.gocache/build GOPATH=$(CURDIR)/.gocache/gopath
GO    := $(GOENV) go
```
⭐ **A linha mais importante do Makefile.** Nesta máquina, o cache global do Go (`C:\Users\PoxZe\go`) **não é gravável** (permissão do Windows). Essa linha força o cache para dentro do workspace. Sem ela, `go build` falha com "Acesso negado".

```make
ifneq (,$(wildcard ./.env))
include .env
export
endif
```
Se existir `.env`, o Make **carrega e exporta** as variáveis. Assim `make migrate-up` funciona sem você exportar nada na mão.

```make
ifeq ($(OS),Windows_NT)
BIN_EXT := .exe
else
BIN_EXT :=
endif
```
Windows precisa de `.exe` no nome do binário; Linux/Mac não. Isso deixa o Makefile portável.

Os alvos estão agrupados por finalidade — veja a seção 8 para os comandos.

**Aviso honesto:** você **não tem `make` instalado** no Windows. Os alvos servem como **documentação executável** (o avaliador no Linux usa) e eu executo os equivalentes direto por aqui. Se quiser usar `make` localmente: `winget install ezwinports.make` ou use o Git Bash com `make`.

---

### 5.9. `.env.example`

Aqui estão todas as variáveis, com o valor que funciona localmente. Destaques:

| Variável | Por que existe |
| --- | --- |
| `DATABASE_URL` | String de conexão completa. O comentário avisa: `postgres` dentro do Docker, `localhost` rodando local |
| `OIDC_ISSUER` vs `OIDC_DISCOVERY_URL` | Explicado em 5.4 — o par mais confuso do projeto |
| `SQS_VISIBILITY_TIMEOUT=30s` | **Não baixe isso.** A validação recusa valores menores que 5s |
| `DB_MIGRATIONS_ON_START=false` | ⭐ Migrations **não** rodam no boot da API. Com 3 instâncias subindo juntas, todas tentariam migrar ao mesmo tempo. Quem migra é o serviço `migrate` |
| `OUTBOX_ENABLED` / `REFERENCE_ENABLED` | Liga os workers. Ligados no compose, desligados por padrão (testes unitários não precisam deles) |

---

## 6. Arquivos de código, um por um

### 6.1. `cmd/api/main.go` — 15 linhas que só delegam

```go
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if err := app.Run(version, commit); err != nil {
		fmt.Fprintf(os.Stderr, "erro fatal: %v\n", err)
		os.Exit(1)
	}
}
```

| Linha | Explicação |
| --- | --- |
| `version = "dev"` | Valor **padrão**. Em build de verdade, o `-ldflags -X main.version=1.2.3` do Dockerfile sobrescreve isso em tempo de link |
| `os.Exit(1)` | Código de saída ≠ 0 avisa o orquestrador que o processo falhou |

⭐ **Por que `main` é tão vazio:** em Go, `package main` **não pode ser importado** por outros pacotes — logo, **não pode ser testado**. Toda a lógica vai para `internal/app`, que é testável. `main` só faz o "cabo de força".

---

### 6.2. `internal/config/config.go` — linha a linha

#### O bloco `Config` (linhas 25–45)

```go
type Config struct {
	Env         string          `env:"APP_ENV" envDefault:"local"`
	HTTP        HTTPConfig      `envPrefix:"HTTP_"`
	Database    DatabaseConfig  `envPrefix:"DB_"`
	OIDC        OIDCConfig      `envPrefix:"OIDC_"`
	SQS         SQSConfig       `envPrefix:"SQS_"`
	Outbox      OutboxConfig    `envPrefix:"OUTBOX_"`
	Reference   ReferenceConfig `envPrefix:"REFERENCE_"`
	LogLevel    string          `env:"LOG_LEVEL" envDefault:"info"`
	ShutdownTTL time.Duration   `env:"SHUTDOWN_TIMEOUT" envDefault:"25s"`
	DatabaseURL string          `env:"DATABASE_URL"`
}
```

**Por que aninhado em sub-structs em vez de 60 campos soltos?**
1. O construtor de cada componente recebe **só o que precisa**: `NewServer(cfg *Config, ...)` lê `cfg.HTTP`; o pool lê `cfg.Database`. Não existe "passar 60 campos".
2. `envPrefix` evita colisão de nomes: `HTTP_SHUTDOWN_TIMEOUT` e `SHUTDOWN_TIMEOUT` são coisas diferentes, e o prefixo deixa isso explícito.

**Sobre `DatabaseURL` fora do grupo `DB_`:** é a variável que **todo** ecossistema Go usa com esse nome exato (`DATABASE_URL`), então mantê-la sem prefixo facilita a vida de quem já conhece. É uma exceção **justificada em comentário**, não um descuido.

**Sobre o comentário das linhas 39–43:** ele registra uma decisão de *design* e o motivo. Em entrevista, quando perguntarem "por que não usou `required`?", a resposta está no código.

#### `HTTPConfig` (linhas 48–56)

```go
Addr            string        `env:"ADDR" envDefault:":8080"`
ReadTimeout     time.Duration `env:"READ_TIMEOUT" envDefault:"10s"`
WriteTimeout    time.Duration `env:"WRITE_TIMEOUT" envDefault:"15s"`
IdleTimeout     time.Duration `env:"IDLE_TIMEOUT" envDefault:"60s"`
ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"20s"`
RequestTimeout  time.Duration `env:"REQUEST_TIMEOUT" envDefault:"10s"`
MaxBodyBytes    int64         `env:"MAX_BODY_BYTES" envDefault:"1048576"`
```

| Campo | O que protege |
| --- | --- |
| `ReadTimeout` | Quanto tempo para ler a requisição inteira. **Sem isso**: um cliente malicioso manda 1 byte por minuto e segura sua goroutine (ataque slow loris) |
| `WriteTimeout` | Quanto tempo para escrever a resposta |
| `IdleTimeout` | Quanto tempo uma conexão keep-alive fica aberta ociosa |
| `ShutdownTimeout` | Prazo para as requisições em andamento terminarem no shutdown |
| `RequestTimeout` | Prazo **por requisição** (o middleware aplica) |
| `MaxBodyBytes` | 1048576 = 1 MiB. Limite de memória por payload |

#### `DatabaseConfig` (linhas 59–71)

```go
MaxConns, MinConns                            // tamanho do pool
ConnMaxLifetime, ConnMaxIdleTime              // reciclagem de conexão
ConnectTimeout, StatementTimeout, LockTimeout // prazos no servidor
MigrationsOnStart bool
```

| Campo | Por que existe |
| --- | --- |
| `MaxConns=20` | Conexões simultâneas. O Postgres tem limite (`max_connections=200`), então várias instâncias × 20 precisa caber nisso |
| `ConnMaxLifetime=30m` | ⭐ Recicla conexões. Evita usar conexão que o Postgres/load balancer já fechou do outro lado |
| `StatementTimeout=10s` | ⭐ Vai virar `SET statement_timeout` **na sessão**: query travada é **abortada pelo servidor**, não fica pendurada segurando lock |
| `LockTimeout=5s` | ⭐ `SET lock_timeout`: se uma transação espera mais de 5s por um lock, ela falha em vez de ficar presa |
| `MigrationsOnStart=false` | Comentário no código explica: 3 instâncias migrando ao mesmo tempo é corrida |

⭐ **`StatementTimeout` e `LockTimeout` são resposta direta ao item 4 do enunciado**: *"cancelamento, prazos de execução"*. Eles devem ser aplicados na **sessão do banco**, não só no contexto do Go — porque o cancelamento do Go via contexto pode não chegar se a query estiver bloqueada no servidor.

#### `OIDCConfig` (linhas 74–88)

```go
Issuer       string        `env:"ISSUER"`
DiscoveryURL string        `env:"DISCOVERY_URL" envDefault:""`
Audience     string        `env:"AUDIENCE" envDefault:""`
JWKSCacheTTL time.Duration `env:"JWKS_CACHE_TTL" envDefault:"15m"`
ClockSkew    time.Duration `env:"CLOCK_SKEW" envDefault:"30s"`
```

| Campo | Explicação |
| --- | --- |
| `Issuer` | ⭐ Valor **exato** do claim `iss` do token. A comparação é de string — um `/` no fim já quebra a validação |
| `DiscoveryURL` | Onde buscar `/.well-known/openid-configuration`. Pode ser diferente do Issuer (ver 5.4) |
| `Audience` | Deve conter `jungleg-api`. Impede aceitar token emitido para outro público |
| `JWKSCacheTTL` | As chaves públicas (JWKS) ficam em cache. Sem cache, **toda requisição** faria um HTTP no Keycloak |
| `ClockSkew` | Tolerância de 30s em `exp`/`nbf`. Sem isso, um relógio 2s adiantado rejeita tokens válidos |

#### `SQSConfig` (linhas 91–109)

```go
Endpoint, Region, AccessKeyID, SecretAccessKey
TransactionsQueue, TransactionsDLQ, EventsQueue, EventsDLQ
MaxReceiveCount, VisibilityTimeout, WaitTime, BatchSize, ConsumerConcurrency
ProvisionQueuesOnBoot, ConsumeTransactions
```

| Campo | Explicação |
| --- | --- |
| `Endpoint` | ⭐ Aponta para o **LocalStack**. Em produção, seria vazio (o SDK usaria o endpoint real da AWS). É isso que torna o SQS local "de mentira mas real" |
| `WaitTime=20s` | *Long polling*: a requisição fica aberta até 20s esperando mensagem. **Sem isso**, o consumidor martelaria a API a cada 100ms (custo e latência) |
| `BatchSize=10` | Máximo que o SQS permite por `ReceiveMessage` |
| `ConsumerConcurrency=8` | Quantas mensagens processamos **em paralelo**. Cuidado: paralelismo por carteira é permitido; o lock no banco resolve a disputa |
| `ConsumeTransactions` | Liga/desliga o consumidor. Em testes só-HTTP, desliga |

#### `OutboxConfig` e `ReferenceConfig` (linhas 112–132)

```go
Outbox:    Enabled, BatchSize, Lease, PollInterval, MaxAttempts, BaseBackoff, MaxBackoff
Reference: Enabled, PollInterval, BatchSize, MaxAttempts, BaseBackoff, MaxBackoff, ReaperEvery, PendingMaxAge
```

| Campo | Explicação |
| --- | --- |
| `Lease=30s` | ⭐ **Conceito de lease (arrendamento).** Um publisher marca "estou trabalhando neste evento até tal hora". Se ele morrer, o lease expira e **outra instância** assume. É isso que resolve *"recuperação de trabalho abandonado"* (item 11) |
| `BaseBackoff` → `MaxBackoff` | Backoff exponencial: 2s, 4s, 8s... até o teto. Evita martelar um serviço que está caindo |
| `ReaperEvery`, `PendingMaxAge` | O "ceifador": varre transações `PENDING` antigas e retoma. Defesa em profundidade |

#### `Load()` (linhas 140–153)

```go
func Load() (*Config, error) {
	var cfg Config
	if err := env.ParseWithOptions(&cfg, env.Options{RequiredIfNoDef: false}); err != nil {
		return nil, fmt.Errorf("config: lendo variáveis de ambiente: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}
```

| Linha | Explicação |
| --- | --- |
| `var cfg Config` | Declara a struct zerada |
| `env.ParseWithOptions(&cfg, ...)` | ⭐ Passa o **endereço** (`&cfg`): a lib precisa **escrever** dentro da struct. Se passasse `cfg`, ela trabalharia numa cópia e você receberia tudo zerado |
| `%w` | ⭐ **Envelopa** o erro. Isso preserva a cadeia original para `errors.Is`/`errors.As`. Com `%v` você perderia a causa |
| `return &cfg, nil` | Devolve ponteiro — decisão documentada no comentário |

⚠️ **Detalhe importante sobre a ordem:** `Load` só chama `Validate` **depois** de o parsing ter sucesso. Se `DATABASE_URL` for `0` ou uma duração inválida (`"10 bananas"`), o erro vem do parsing; se o valor é sintaticamente válido mas semanticamente errado (`SQS_BATCH_SIZE=99`), quem recusa é `Validate`.

#### `Validate()` (linhas 157–240) — o padrão "acumular problemas"

```go
var problems []string

if strings.TrimSpace(c.DatabaseURL) == "" {
	problems = append(problems, "DATABASE_URL é obrigatória")
}
...
if len(problems) > 0 {
	return fmt.Errorf("config: configuração inválida:\n  - %s", strings.Join(problems, "\n  - "))
}
```

⭐ **O padrão que você deve saber explicar:** em vez de `return err` no primeiro problema (fail-fast), **acumulamos** em um slice e devolvemos tudo junto.

**Por quê:** com fail-fast, o operador corrige uma variável, sobe, descobre a próxima, sobe, descobre a próxima... Com acúmulo, ele conserta **tudo de uma vez**. Isso é experiência de operação (DX).

**Os `TrimSpace`:** `strings.TrimSpace(c.DatabaseURL) == ""` trata `"   "` (só espaços) como vazio. Sem isso, alguém com `DATABASE_URL=" "` passaria na validação e falharia de forma estranha ao conectar.

**A validação por itens de negócio — os 3 casos que você precisa saber justificar:**

```go
if c.SQS.BatchSize < 1 || c.SQS.BatchSize > 10 {
	problems = append(problems, "SQS_BATCH_SIZE deve estar entre 1 e 10 (limite do SQS)")
}
```
É o **limite do próprio SQS**. Aceitar 50 faria a API devolver erro de validação em runtime. Melhor recusar no boot.

```go
if c.SQS.VisibilityTimeout < 5*time.Second {
	problems = append(problems, "SQS_VISIBILITY_TIMEOUT muito baixo: risco de reprocessamento concorrente da mesma mensagem")
}
```
⭐ **Este é o mais importante dos três.** Se o visibility timeout for menor que o tempo de processamento, o SQS **entrega a mesma mensagem para outro consumidor** enquanto o primeiro ainda trabalha → dois processamentos simultâneos → risco de movimentação duplicada (item **eliminatório**). Recusar no boot previne a classe de erro.

```go
for _, q := range []struct{ name, value string }{...} {
	if !strings.HasSuffix(q.value, ".fifo") {
		problems = append(problems, q.name+" deve terminar em .fifo ...")
	}
}
```
⭐ Repare no padrão: um **slice anônimo de struct anônima** para iterar sobre pares (nome, valor). Isso evita escrever 4 `if` quase idênticos. O sufixo `.fifo` importa porque só filas FIFO garantem **ordem por `MessageGroupId`** — e usamos `walletId` como group para que as operações de uma mesma carteira cheguem **em ordem** (essencial para `ROLLBACK` depois de `BET`).

**A normalização (linhas 231–239):**
```go
c.OIDC.Issuer = strings.TrimRight(c.OIDC.Issuer, "/")
if c.OIDC.DiscoveryURL == "" {
	c.OIDC.DiscoveryURL = c.OIDC.Issuer
}
c.OIDC.DiscoveryURL = strings.TrimRight(c.OIDC.DiscoveryURL, "/")
```
⭐ **Normalizar é diferente de validar.** Validar diz "está certo/errado"; normalizar deixa o valor **canônico** para comparações. Aqui, remover a barra final garante que `http://x/realms/jungleg/` (com barra) e o claim `iss` (sem barra) sejam considerados iguais. Sem isso, o erro seria intermitente e difícil de achar.

E se `DiscoveryURL` não vier, ela herda o `Issuer` — um default inteligente para desenvolvimento local.

#### `SlogLevel()` e `IsLocal()` (linhas 243–261)

```go
switch strings.ToLower(strings.TrimSpace(c.LogLevel)) {
case "debug":              return slog.LevelDebug, nil
case "info", "":           return slog.LevelInfo, nil
case "warn", "warning":    return slog.LevelWarn, nil
case "error":              return slog.LevelError, nil
default:
	return slog.LevelInfo, fmt.Errorf("LOG_LEVEL inválido: %q (use debug, info, warn ou error)", c.LogLevel)
}
```
- `ToLower` + `TrimSpace` → aceita `"INFO"`, `" info "`. Configuração humana não deve ser frágil.
- `case "info", "":` → string vazia cai no default `info`. Como `Validate` chama este método, um `LOG_LEVEL` inválido **também** impede o boot.
- A mensagem de erro **diz o que é aceito**. Erro bom ensina.

```go
func (c *Config) IsLocal() bool {
	return strings.EqualFold(c.Env, "local") || strings.EqualFold(c.Env, "test")
}
```
`EqualFold` = comparação **case-insensitive**. Usado pelo logger para escolher texto (legível) vs JSON (produção).

> **Teste-se (config.go):**
> 1. Por que a config é devolvida como ponteiro e não como valor?
> 2. Por que `Validate` acumula erros em vez de retornar no primeiro?
> 3. O que aconteceria de ruim se `SQS_VISIBILITY_TIMEOUT` fosse 1 segundo?
> 4. Qual a diferença entre validar e normalizar? Dê o exemplo do `Issuer`.

---

### 6.3. `internal/observability/context.go` — linha a linha

```go
type contextKey string

const (
	correlationIDKey contextKey = "correlationId"
	requestIDKey     contextKey = "requestId"
)
```

⭐ **Por que um tipo próprio (`contextKey`) e não `string` direto?**
Se você usa `context.WithValue(ctx, "id", x)` com uma string, **qualquer** pacote que também use a chave `"id"` sobrescreve a sua. É uma colisão silenciosa. Com um tipo não-exportado, **nenhum outro pacote consegue** criar uma chave igual. É o padrão recomendado pela documentação oficial do Go.

```go
func WithCorrelationID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, correlationIDKey, id)
}
```
O guarda `if id == ""` evita poluir o contexto com valor inútil — e evita que um `CorrelationIDFrom` devolva `""` que pareça "existe mas está vazio".

```go
func CorrelationIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(correlationIDKey).(string); ok {
		return v
	}
	return ""
}
```
⭐ **A "type assertion" com `ok`:** `ctx.Value` devolve `any` (interface vazia). Sem o `, ok`, um valor nil causaria **panic**. Com o `ok`, o pior caso é devolver string vazia. Sempre use a forma com dois valores ao extrair do contexto.

**Diferença conceitual dos dois IDs:**

| | `correlationId` | `requestId` |
| --- | --- | --- |
| O que identifica | A **operação de negócio**, que pode passar por vários serviços | **Esta** requisição HTTP |
| Vem do cliente? | **Sim**, se ele enviar | **Nunca** — é sempre gerado |
| Sobrevive a | Várias requisições | Uma única requisição |
| Aparece no evento de outbox? | **Sim** | Não |

> **Teste-se:** por que usar um tipo não-exportado como chave de contexto em vez de uma string? O que a forma `v, ok := ...` previne?

---

### 6.4. `internal/observability/logger.go` — linha a linha

#### `NewLogger`

```go
func NewLogger(cfg *config.Config) *slog.Logger {
	level, err := cfg.SlogLevel()
	if err != nil {
		level = slog.LevelInfo  // ← fallback defensivo
	}
	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if cfg.IsLocal() {
		handler = slog.NewTextHandler(os.Stdout, opts)   // local: legível
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)   // produção: JSON
	}

	logger := slog.New(&contextHandler{next: handler})
	logger = logger.With(slog.String("service", "jungleg"), slog.String("env", cfg.Env))
	return logger
}
```

| Linha | Explicação |
| --- | --- |
| fallback `LevelInfo` | Mesmo que `Validate` já tenha recusado LOG_LEVEL inválido, aqui não entramos em panic. Defesa em profundidade |
| `os.Stdout` (não `os.Stderr`) | ⭐ **Convenção de container:** stdout é capturado pelo Docker/orquestrador. Escrever em arquivo dentro do container é anti-padrão |
| `NewTextHandler` local | Texto é mais legível no terminal durante o desenvolvimento |
| `NewJSONHandler` produção | ⭐ JSON é **parseável**: Datadog/Loki/CloudWatch indexam campos. O item 12 exige *"logs JSON"* |
| `logger.With(...)` | Adiciona `service` e `env` em **todas** as linhas, sem repetir em cada chamada |

**A ordem importa:** o `contextHandler` **envolve** o handler de texto/JSON. Ou seja, ele injeta os campos **antes** de a linha ser formatada.

#### `contextHandler` — a peça central

```go
func (h *contextHandler) Handle(ctx context.Context, record slog.Record) error {
	if id := CorrelationIDFrom(ctx); id != "" && !recordHasAttr(record, "correlationId") {
		record.AddAttrs(slog.String("correlationId", id))
	}
	if id := RequestIDFrom(ctx); id != "" && !recordHasAttr(record, "requestId") {
		record.AddAttrs(slog.String("requestId", id))
	}
	return h.next.Handle(ctx, record)
}
```

| Pedaço | Explicação |
| --- | --- |
| `slog.Handler` é uma interface | Implementar `Enabled`, `Handle`, `WithAttrs`, `WithGroup` transforma a struct num handler |
| `... && !recordHasAttr(...)` | ⭐ Se o chamador passou `correlationId` **explicitamente**, respeitamos o valor dele. Sem isso, a chave apareceria **duas vezes** no JSON |
| `Handle` recebe `ctx` | ⭐ **Esta é a razão do método existir:** `slog.InfoContext(ctx, ...)` passa o contexto, e o handler pode extrair os valores. Com `slog.Info(...)` (sem contexto), a correlação **não** seria adicionada |

```go
func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{next: h.next.WithAttrs(attrs)}
}
func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{next: h.next.WithGroup(name)}
}
```
⭐ **Estes dois métodos parecem inúteis, mas são obrigatórios.** O `slog` chama `WithAttrs` quando você faz `logger.With(...)`. Se não devolvêssemos um `*contextHandler`, o logger resultante **perderia** a injeção de correlação. É um bug sutil: funciona no logger raiz e para de funcionar em qualquer logger derivado.

```go
func recordHasAttr(record slog.Record, key string) bool {
	found := false
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == key {
			found = true
			return false   // ← false = PARA de iterar
		}
		return true        // ← true = CONTINUA
	})
	return found
}
```
⚠️ **Contra-intuitivo:** no callback de `Record.Attrs`, devolver `true` **continua** a iteração e `false` **para**. É o oposto de `for` com `break`. Aqui: achou → `false` (para), não achou → `true` (continua).

#### `Redact`

```go
func Redact(value string) string {
	if value == "" { return "" }
	r := []rune(value)
	if len(r) <= 4 { return strings.Repeat("*", len(r)) }
	return string(r[:2]) + strings.Repeat("*", len(r)-4) + string(r[len(r)-2:])
}
```
| Detalhe | Explicação |
| --- | --- |
| `[]rune(value)` | ⭐ Converte para **runes** (pontos de código), não bytes. `secret"` com acento teria contagem errada em bytes. `len(string)` conta bytes; `len([]rune)` conta caracteres |
| `r[:2]` + máscara + `r[len-2:]` | Mantém 2 do início e 2 do fim: dá para conferir *qual* segredo é, sem expor o valor |

Usado para nunca logar um segredo inteiro — exigência do item 12.

> **Teste-se:** por que `WithAttrs` e `WithGroup` não podem simplesmente repassar para o handler de baixo? O que quebraria?

---

### 6.5. `internal/observability/metrics.go`

```go
const (
	namespace = "jungleg"
	subsystem = "wager"
)
```
Toda métrica vira `jungleg_wager_<nome>`. O prefixo evita colisão com métricas de outras aplicações no mesmo Prometheus.

```go
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	...
}
```
⭐ **Registry próprio, não o global.** O `prometheus.DefaultRegisterer` é um singleton global; registrar duas vezes o mesmo nome **causa panic**. Como os testes criam várias instâncias de `Metrics`, usamos um registry dedicado por instância. Isso é o que permite rodar testes em paralelo sem explosão.

`NewGoCollector` traz métricas do runtime (goroutines, GC, memória) e `NewProcessCollector` traz CPU e memória do processo. **Diagnóstico grátis.**

```go
operations: prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: namespace, Subsystem: subsystem,
	Name: "operations_total",
	Help: "Total de operações de aposta processadas, por tipo (kind) e status final.",
}, []string{"kind", "status"}),
```
| Conceito | Explicação |
| --- | --- |
| `Counter` | ⭐ Só **aumenta**. Representa "quantos eventos aconteceram" |
| `CounterVec` | Um contador com **labels**. `WithLabelValues("BET","PROCESSED").Inc()` cria/incrementa a série específica |
| `Help` | Descrição. **Obrigatório** em métrica bem feita: é o que aparece no `/metrics` e no Grafana |

⭐ **REGRA CRÍTICA DE CARDINALIDADE — decore isso para a entrevista:**
> Labels só podem ter **valores de baixa cardinalidade e conhecidos de antemão**.

```go
}, []string{"kind", "status"})   // ✅ 6 kinds × 5 status = 30 séries
// NUNCA: []string{"walletId"}   // ❌ 1 milhão de carteiras = 1 milhão de séries
// NUNCA: []string{"transactionId"}  // ❌ explode a memória do Prometheus
```

Por isso `walletId`, `providerId` e `transactionId` vão para os **logs**, nunca para labels de métrica.

```go
operationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "operation_duration_seconds",
	Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
}, []string{"kind", "status"}),
```
| Conceito | Explicação |
| --- | --- |
| `Histogram` | Distribui observações em **buckets**. Permite calcular p50/p95/p99 |
| `Buckets` | Os limites. Escolha consciente: de **1ms a 2.5s** porque a operação é dominada por uma transação no Postgres |

⭐ **Unidade em segundos é convenção obrigatória do Prometheus.** O sufixo `_seconds` no nome faz parte do contrato, não é estilo.

**Os helpers "nil-safe":**
```go
func (m *Metrics) IncReplay() {
	if m == nil {
		return
	}
	m.replays.Inc()
}
```
⭐ **Por que `if m == nil`?** Em Go, chamar um método com receptor ponteiro em um ponteiro nil é **válido** — mas qualquer acesso ao campo (`m.replays`) causaria panic. O guarda torna o objeto utilizável como "no-op" quando não injetado (ex.: teste unitário de domínio que não se importa com métricas).

**As métricas e o que cada uma responde:**

| Métrica | Pergunta que responde | Item do enunciado |
| --- | --- | --- |
| `operations_total{kind,status}` | Quantas apostas, quantas rejeitadas? | 12 — "resultados por status" |
| `idempotent_replays_total` | Quantos reenvios? | 12 — "duplicatas" |
| `inbox_duplicates_total` | Quantas mensagens SQS duplicadas? | 12 — "duplicatas" |
| `retries_total{stage}` | Onde estamos retentando? | 12 — "retries" |
| `dlq_messages_total` | Quantas foram para a DLQ? | 12 — "DLQ" |
| `transaction_conflicts_total` | Quanto estamos disputando lock? | 12 — "conflitos de concorrência" |
| `outbox_pending_age_seconds` | Qual o atraso da outbox? | 12 — "atraso da outbox" |
| `operation_duration_seconds` | Qual a latência? | 12 — "latência de processamento" |
| `reconciliation_divergences_total` | Alguma carteira divergiu? | 12 — "divergências de reconciliação" |

> **Teste-se:** por que `walletId` não pode ser label de métrica? Por que `NewMetrics` usa um registry próprio em vez do global?

---

### 6.6. `internal/httpapi/router.go`

```go
type Routes struct {
	Base     chi.Router
	Internal chi.Router
	Provider chi.Router
	Public   chi.Router
}
```
⭐ **Por que quatro roteadores?** Porque cada grupo tem uma **política de autorização diferente**:

| Roteador | Endpoints | Autorização |
| --- | --- | --- |
| `Base` | todos (monta os outros) | — |
| `Public` | `/health/*`, `/metrics` | **nenhuma** |
| `Internal` | `/wallets` (abrir, ler, ledger, reconciliar) | role `internal` |
| `Provider` | `/wagering/transactions` | role `provider` + isolamento por `providerId` |

Separar em roteadores evita o erro clássico de esquecer o middleware de auth numa rota nova: você registra no grupo certo, a proteção vem junto.

```go
func NewRouter(cfg *config.Config, logger *slog.Logger, metrics *observability.Metrics, healthCheckers []HealthChecker) *Routes {
```
⭐ **A assinatura é a documentação das dependências.** Lê-se: "para montar as rotas eu preciso de config, logger, métricas e dos verificadores de health". O Fx monta isso automaticamente.

```go
	r.Use(chimw.RequestSize(0))
	r.Use(RequestIDMiddleware)
	r.Use(chimw.RealIP)
	r.Use(RecovererMiddleware(logger))
	r.Use(AccessLogMiddleware(logger))
	r.Use(TimeoutMiddleware(cfg.HTTP.RequestTimeout))
	r.Use(MaxBodyMiddleware(cfg.HTTP.MaxBodyBytes))
```
⚠️ **A ORDEM é semântica, não estética:**

1. `RequestIDMiddleware` **primeiro** → todo log abaixo já tem correlação.
2. `RealIP` → resolve IP atrás de proxy antes de qualquer log.
3. `RecovererMiddleware` → ⭐ precisa ser **antes** do `AccessLog` para que um panic ainda passe pelo log de acesso? **Não!** A ordem em chi é: **primeiro registrado = mais externo**. Então `Recoverer` (3º) envolve `AccessLog` (4º)... e um panic capturado pelo `Recoverer` **pularia** o `AccessLog`. Pense nisso: se quiser logar o acesso mesmo em panic, o `AccessLog` deve vir **antes** do `Recoverer`.
   > 📌 **Pendência anotada:** revisar essa ordem na Fase 7 para garantir que requisições com panic também gerem linha de acesso. Bom exemplo de decisão que parece detalhe e não é.
4. `Timeout` → aplica prazo a tudo abaixo.
5. `MaxBody` → limita memória por requisição.

```go
	r.Get("/health/live", LivenessHandler())
	r.Get("/health/ready", ReadyHandler(healthCheckers, logger))
	r.Method(http.MethodGet, "/metrics", metrics.Handler())
```
Registrados **em `Base`**, ou seja, fora de qualquer autenticação. Exigência do item 9: *"Health checks públicos"*.

```go
	routes.Base.Mount("/internal/wallets", routes.Internal)
	routes.Base.Mount("/wagering", routes.Provider)
```
⭐ `Mount` **delega** um prefixo de caminho para outro roteador. O `chi` já lida com o roteamento interno e reconstrói a URL. É assim que cada módulo de feature ganha seu próprio "território" sem tocar no roteador central.

⚠️ **Detalhe real:** o prefixo `/internal/wallets` fica **visível na URL pública**. Na Fase 7 vou revisitar isso: o enunciado pede `POST /wallets` (sem `/internal`). O "internal" aqui é sobre **autorização**, não sobre o path. Anotado como pendência.

```go
func NewServer(cfg *config.Config, routes *Routes) *http.Server {
	return &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           routes.Base,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		ReadHeaderTimeout: cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
	}
}
```
⭐ `ReadHeaderTimeout` **separado** de `ReadTimeout`: protege especificamente contra um cliente que abre a conexão e **nunca termina de mandar os headers** (ataque lento clássico). É o timeout mais importante de todos e muita gente esquece.

> **Teste-se:** no chi, se eu registro `A` e depois `B`, qual envolve qual? Por que `ReadHeaderTimeout` existe separado?

---

### 6.7. `internal/httpapi/middleware.go`

#### `RequestIDMiddleware`

```go
requestID := strings.TrimSpace(r.Header.Get(HeaderRequestID))
if requestID == "" {
	requestID = uuid.NewString()
}
correlationID := strings.TrimSpace(r.Header.Get(HeaderCorrelationID))
if correlationID == "" {
	correlationID = requestID
}
```
⭐ **A regra de negócio da correlação, em 6 linhas:**
- **requestId:** se o cliente mandou, usa; senão gera. Identifica **esta** requisição.
- **correlationId:** se o cliente mandou, **preserva** — é ele que quer rastrear a operação dele atravessando nossos serviços. Senão, deriva do requestId.

```go
w.Header().Set(HeaderRequestID, requestID)
w.Header().Set(HeaderCorrelationID, correlationID)
next.ServeHTTP(w, r.WithContext(ctx))
```
⭐ **`r.WithContext(ctx)`:** `http.Request` é imutável nesse aspecto — você não altera `r.Context()`, você **cria uma cópia** do request com o novo contexto. Esquecer o `r.WithContext(...)` e usar `next.ServeHTTP(w, r)` é um bug clássico: o contexto novo é descartado e nada abaixo vê a correlação.

Devolver os IDs nos headers de resposta permite ao cliente **logar o mesmo ID** e vocês cruzarem os logs.

#### `statusRecorder` — por que precisa existir

```go
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
```
⭐ **O problema:** a interface `http.ResponseWriter` **não tem** como ler o status depois de escrito. Para logar `status=200` você precisa interceptar a chamada `WriteHeader`.

| Detalhe | Explicação |
| --- | --- |
| Embute `http.ResponseWriter` | Herda todos os métodos, sobrescreve só o que interessa (é "embedding", não herança) |
| `if r.status == 0` | ⭐ Só registra o **primeiro** `WriteHeader`. O Go ignora chamadas subsequentes (com aviso), mas queremos o valor real |
| `Write` faz `if r.status == 0 { r.status = 200 }` | ⭐ Se o handler nunca chamar `WriteHeader` explicitamente, o `net/http` assume **200**. Precisamos replicar essa regra |
| `Unwrap()` | ⭐ Permite que `http.ResponseController` (usado pelo `TimeoutHandler` do chi para responder 504) **alcance** o writer original. Sem ele, o 504 não funcionaria |

#### `AccessLogMiddleware`

```go
logger.LogAttrs(r.Context(), slog.LevelInfo, "http_request",
	slog.String("method", r.Method),
	slog.String("path", r.URL.Path),
	slog.Int("status", status),
	slog.Int("bytes", rec.bytes),
	observability.LogDuration(time.Since(start)),
	slog.String("kind", observability.StageFrom(r.Context())),
	slog.String("idempotencyKey", r.Header.Get(HeaderIdempotencyKey)),
)
```
| Escolha | Por que |
| --- | --- |
| `LogAttrs` em vez de `Info(...)` com `any` | ⭐ **Performance:** `LogAttrs` evita alocação de `[]any` e boxing. Em alta frequência, faz diferença |
| `r.Context()` | Para o `contextHandler` injetar a correlação |
| `r.URL.Path` em vez de `r.URL.String()` | ⭐ **`String()` incluiria a query string**, que pode conter dados sensíveis. `Path` é seguro |
| `idempotencyKey` logado | É um **identificador**, não dado sensível. Permite cruzar o replay com o log |
| `time.Since(start)` | Latência observada pelo servidor |

Note o que **NÃO** é logado: corpo da requisição, headers de autorização, valores monetários. Exigência do item 12: *"Não registre credenciais, dados sensíveis ou payloads financeiros completos"*.

#### `TimeoutMiddleware`

```go
ctx, cancel := context.WithTimeout(r.Context(), timeout)
defer cancel()
next.ServeHTTP(w, r.WithContext(ctx))
```
⭐ `defer cancel()` é **obrigatório**. Sem ele, o timer do contexto continua vivo até estourar, vazando memória e goroutine. O `go vet` reclama disso (`lostcancel`) — e é por isso que rodamos `go vet` no checklist.

#### `MaxBodyMiddleware`

```go
r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
```
⭐ `MaxBytesReader` **substitui** o corpo, de modo que a leitura para no limite e devolve erro. É a defesa contra payload gigante: sem isso, um atacante manda 10 GB de JSON e sua aplicação tenta alocar tudo.

> **Teste-se:** por que precisamos de `statusRecorder`? O que `Unwrap()` viabiliza? Por que logamos `Path` e não `String()`?

---

### 6.8. `internal/httpapi/response.go` — o contrato de erro

```go
type APIError struct {
	Status      int
	Code        string
	Message     string
	FailureCode string
	Internal    error               // ← NUNCA serializado
	Headers     map[string]string   // ← ex.: Retry-After
}
```
⭐ **Separação crucial que você precisa saber explicar:**

| Campo | Vai para o cliente? | Papel |
| --- | --- | --- |
| `Status` | indiretamente (é o código HTTP) | 409, 422, 503... |
| `Code` | ✅ | Código **do contrato HTTP** (`IDEMPOTENCY_CONFLICT`) |
| `Message` | ✅ | Texto **seguro** para humano |
| `FailureCode` | ✅ | Código **de negócio** (`INSUFFICIENT_FUNDS`) |
| `Internal` | ❌ | ⭐ O erro técnico original (`pq: duplicate key...`). Só vai para o **log** |

```go
func (e *APIError) Error() string {
	if e.Internal != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Internal)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *APIError) Unwrap() error { return e.Internal }
```
| Método | Por que |
| --- | --- |
| `Error() string` | ⭐ Torna `*APIError` um `error` (satisfaz a interface). É isso que permite `return ErrConflict(...)` |
| `Unwrap() error` | ⭐ Permite `errors.Is(err, ErrNotFound)` e `errors.As(err, &pgErr)` **atravessarem** o `APIError` e chegarem ao erro original |

⭐ **Dois "codes" diferentes — não confunda:**
```
Code (contrato)     = "BUSINESS_REJECTED"    → o cliente sabe o TIPO da situação
FailureCode (regra) = "INSUFFICIENT_FUNDS"   → o cliente sabe o QUE fazer
```
O enunciado exige `failureCode` **estável e documentado** (item 7) e situações **distinguíveis pelo contrato** (item 9). Essa separação é a resposta.

**Os construtores** (um por situação do contrato):

```go
func ErrUnprocessable(failureCode, message string) *APIError {
	return &APIError{Status: 422, Code: CodeUnprocessable, Message: message, FailureCode: failureCode}
}

func ErrUnavailable(message string, retryAfterSeconds int, internal error) *APIError {
	return &APIError{
		Status: 503, Code: CodeServiceUnavailable, Message: message, Internal: internal,
		Headers: map[string]string{"Retry-After": fmt.Sprintf("%d", retryAfterSeconds)},
	}
}
```
⭐ `Retry-After` no 503 diz ao cliente **quando** tentar de novo. É o que diferencia "estou fora, tente em 2s" de "estou quebrado, desista".

#### `WriteError` — o ponto único de tradução

```go
func WriteError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		apiErr = ErrInternal(err)   // ← erro não mapeado = 500
	}

	for k, v := range apiErr.Headers {
		w.Header().Set(k, v)
	}

	level := slog.LevelWarn
	if apiErr.Status >= http.StatusInternalServerError {
		level = slog.LevelError
	}
	logger.LogAttrs(r.Context(), level, "http_error", ...)

	writeJSON(w, r, apiErr.Status, errorResponse{Error: ErrorBody{...}})
}
```
| Linha | Explicação |
| --- | --- |
| `errors.As(err, &apiErr)` | ⭐ **Percorre a cadeia de erros** procurando um `*APIError`. É por isso que `Unwrap()` existe. Se não achou, é um erro inesperado → 500 |
| `level := Warn` / `Error` se ≥500 | ⭐ **Classificação por severidade:** 4xx é problema do cliente (aviso); 5xx é problema nosso (erro, deve acordar alguém) |
| Corpo **nunca** inclui `err.Error()` | O cliente recebe mensagem genérica; o detalhe fica no log |

#### `decodeJSON` — validação estrita

```go
dec := json.NewDecoder(r.Body)
dec.DisallowUnknownFields()
dec.UseNumber()
```
| Opção | Por que |
| --- | --- |
| `DisallowUnknownFields()` | ⭐ Campo desconhecido = **erro**. Num contrato financeiro, aceitar em silêncio um campo que o cliente achava que estava sendo aplicado é pior que recusar |
| `UseNumber()` | ⭐ Números viram `json.Number` (string) em vez de `float64`. **Isso é parte da garantia de "dinheiro nunca passa por float64"** — mas o padrão real será `amount` como **string**, e ainda assim essa opção evita qualquer conversão implícita |

```go
if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
	return fmt.Errorf("o corpo deve conter um único objeto JSON")
}
```
⭐ **Truque idiomático do Go:** decodificar para `&struct{}{}` (struct vazia) uma **segunda** vez. Se sobrou conteúdo, o erro **não** é `io.EOF` → o cliente mandou dois JSONs colados (`{...}{...}`), o que é ambíguo e deve ser recusado.

> **Teste-se:** qual a diferença entre `Code` e `FailureCode`? Por que `Unwrap()` é essencial no `APIError`? O que `UseNumber()` e `DisallowUnknownFields()` protegem?

---

### 6.9. `internal/httpapi/health.go`

```go
type HealthChecker interface {
	CheckReadiness(ctx context.Context) error
	Name() string
}
```
⭐ Interface de **2 métodos**, minúscula, definida por quem consome. O `httpapi` não sabe que existe Postgres ou SQS — ele só sabe pedir "você está pronto?".

```go
func LivenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, r, http.StatusOK, map[string]any{"status": "UP"})
	}
}
```
⭐ **Liveness NÃO verifica dependências — e isso é deliberado.**

> Se o liveness checasse o Postgres e o Postgres caísse por 30 segundos, o Kubernetes mataria **todas** as instâncias da aplicação (porque elas "não estão vivas"). Aí, quando o Postgres voltasse, **não haveria mais ninguém** para atender. Você transformou uma falha de dependência numa **queda total**. Clássico erro de arquitetura.

**Liveness = "este processo está travado?"** (só reinicia se a resposta for sim)

```go
func ReadyHandler(checkers []HealthChecker, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		dependencies := make(map[string]string, len(checkers))
		ready := true

		for _, checker := range checkers {
			if checker == nil { continue }
			if err := checker.CheckReadiness(ctx); err != nil {
				ready = false
				dependencies[checker.Name()] = "DOWN"
				logger.WarnContext(ctx, "readiness_dependency_down", ...)
				continue
			}
			dependencies[checker.Name()] = "UP"
		}

		status := http.StatusOK
		state := "UP"
		if !ready { status = 503; state = "DOWN" }
		writeJSON(w, r, status, map[string]any{"status": state, "dependencies": dependencies})
	}
}
```
| Detalhe | Explicação |
| --- | --- |
| `WithTimeout(3s)` | ⭐ Sem isso, um Postgres que aceita a conexão mas nunca responde **travaria o healthcheck** para sempre |
| `if checker == nil { continue }` | Defensivo: um checker nil no slice não pode causar panic no endpoint de health (que é justamente o que precisa estar no ar) |
| Não para no primeiro erro | ⭐ Continua checando tudo, para reportar **todas** as dependências. Diagnóstico vale mais que velocidade |
| `dependencies: map` | ⭐ Diz **qual** dependência caiu. "DOWN" genérico não ajuda ninguém a 2h da manhã |

> **Teste-se:** por que o liveness não pode checar o banco? O que o readiness deve fazer se o Postgres cair mas o SQS estiver bem?

---

### 6.10. `internal/httpapi/recover.go`

```go
defer func() {
	rec := recover()
	if rec == nil {
		return
	}
	if rec == http.ErrAbortHandler {
		panic(rec)   // ← repassa de propósito
	}
	logger.LogAttrs(r.Context(), slog.LevelError, "panic_recovered",
		slog.Any("panic", rec),
		slog.String("stack", string(debug.Stack())),
		...)
	WriteError(w, r, logger, ErrInternal(nil))
}()
```
| Linha | Explicação |
| --- | --- |
| `recover()` | ⭐ Só funciona **dentro de um `defer`**. Captura o panic e devolve o valor passado a `panic()` |
| `if rec == http.ErrAbortHandler { panic(rec) }` | ⭐ **Exceção deliberada.** O próprio `net/http` usa esse panic para abortar uma resposta de propósito. Capturá-lo mudaria o comportamento da biblioteca padrão |
| `debug.Stack()` | ⭐ A stack trace **é o valor** de logar um panic. Sem ela, você sabe que teve panic mas não onde |
| `ErrInternal(nil)` | O cliente recebe 500 genérico. **O detalhe do panic nunca vaza** — pode conter caminho de arquivo, query SQL, nome de tabela |

⭐ **A frase que resume (e que responde a exigência do enunciado):**
> *"`panic` não deve representar rejeições de negócio"* — rejeição de negócio (`INSUFFICIENT_FUNDS`) é **erro tipado** → 422. `panic` é **bug** → 500 + stack no log. São categorias diferentes e não podem se misturar.

---

### 6.11. `internal/app/app.go` — a composição

#### Os módulos

```go
func ModuleOptions() fx.Option {
	return fx.Options(
		configModule(),
		httpapiModule(),
	)
}

func configModule() fx.Option {
	return fx.Module("config",
		fx.Provide(config.Load),
		fx.Provide(observability.NewLogger),
		fx.Provide(observability.NewMetrics),
	)
}
```
⭐ **Convenção do `fx.Module(name, ...)`:**
- `fx.Provide(f)` registra **como produzir** um tipo.
- O Fx **infere os tipos** pelos parâmetros e retornos de `f`. Em `NewLogger(cfg *config.Config) *slog.Logger`, ele aprende: "para ter `*slog.Logger`, preciso de `*config.Config`".
- `config.Load` devolve `(*Config, error)` — ⭐ o Fx **trata o segundo retorno como erro** e aborta o boot se não for nil. É assim que "configuração inválida impede a aplicação de subir" de graça.

*Conforme o projeto cresce, novos módulos entram aqui:* `infraModule()`, `usecaseModule()`, `workerModule()`.

#### O grupo de health checkers

```go
type healthCheckers struct {
	fx.In
	Checkers []httpapi.HealthChecker `group:"health_checkers"`
}
```
⭐ **Value groups do Fx — padrão avançado que vale explicar na entrevista:**
- `fx.In` marca a struct como **parâmetros de entrada** agregados.
- `group:"health_checkers"` diz: "me dê **todos** os valores registrados nesse grupo".
- Cada camada faz `fx.Provide(fx.Annotate(NewPostgresChecker, fx.ResultTags(`group:"health_checkers"`)))` e aparece aqui **sem que o httpapi saiba que existe**.
- Resultado: adicionar um novo health check **não exige** alterar o `httpapi`. Princípio aberto/fechado na prática.

#### `runHTTPServer` — ciclo de vida

```go
func runHTTPServer(lc fx.Lifecycle, server *http.Server, cfg *config.Config, logger *slog.Logger) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error { ... },
		OnStop:  func(ctx context.Context) error { ... },
	})
}
```
⭐ `fx.Lifecycle` é o mecanismo do Fx para **start e stop ordenados**. Tudo que abre recurso (servidor, pool, consumidor) se registra aqui.

**OnStart — com detecção de erro de bind:**
```go
errCh := make(chan error, 1)
go func() {
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		errCh <- err
		return
	}
	errCh <- nil
}()

select {
case err := <-errCh:
	if err != nil {
		return fmt.Errorf("httpapi: falha ao subir o servidor em %s: %w", server.Addr, err)
	}
	return nil
case <-time.After(50 * time.Millisecond):
	logger.InfoContext(ctx, "http_server_started", slog.String("addr", server.Addr))
	return nil
}
```
⭐ **Por que essa ginástica com canal e `time.After`?**

`ListenAndServe` **bloqueia** até o servidor parar — chamá-la direto no `OnStart` travaria o boot para sempre. Solução: roda em goroutine.

Mas aí você perde o erro de bind (porta ocupada). Daí o canal com **buffer 1**: a goroutine reporta o erro imediatamente.

E o `time.After(50ms)`: damos uma janela curta para o erro aparecer. Se em 50ms nada falhou, consideramos que o bind deu certo. É um **trade-off honesto** — não é uma garantia absoluta, mas pega o caso real (porta ocupada, permissão negada) sem atrasar o boot.

*Alternativa mais rigorosa:* pré-abrir o socket com `net.Listen` e só então chamar `server.Serve(ln)`. Aí o erro é **síncrono** e não precisa de timeout. **Anotado como melhoria** para a Fase 7.

**OnStop:**
```go
if err := server.Shutdown(ctx); err != nil { ... }
```
⭐ `Shutdown` (não `Close`): para de aceitar **novas** conexões, mas **espera** as requisições em andamento terminarem — até o `ctx` estourar. É exatamente o que o item 4 pede: *"shutdown com interrupção de novas entradas e conclusão ou liberação do trabalho em andamento"*.

**A ordem do encerramento — decore para a entrevista:**
```
1. Para de aceitar HTTP         (OnStop do servidor)
2. Cancela os workers            (OnStop dos workers — Fases 8/9)
3. Fecha PostgreSQL e SQS        (OnStop do pool — Fase 5)
```
Por que o Fx executa nessa ordem: ele roda os `OnStop` na **ordem inversa** do registro. Como o servidor registra **antes** dos workers, ele para **primeiro**. Isso é intencional, não coincidência.

> ⚠️ **Correção necessária:** os hooks de **fechamento de recursos** (pool do Postgres, client SQS) devem ser registrados **no módulo de infraestrutura**, que por sua vez precisa vir **antes** do `httpapiModule()` no `ModuleOptions()`. Assim os recursos fecham **por último**. Anotado para a Fase 5.

#### `Run` — o runner

```go
fxApp := fx.New(
	ModuleOptions(),
	fx.WithLogger(func() fxevent.Logger { return &fxSlogLogger{logger: logger} }),
	fx.StopTimeout(stopTimeout),
)
```
| Opção | Por que |
| --- | --- |
| `fx.WithLogger(...)` | ⭐ Substitui o logger padrão do Fx (texto) pelo nosso JSON, para o boot/shutdown saírem no **mesmo formato** do resto |
| `fx.StopTimeout(25s)` | ⭐ Prazo **máximo** do shutdown. Se os workers travarem, o Fx desiste e o processo morre — senão o `docker stop` viraria um pendurado eterno |

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
```
⭐ **Padrão idiomático:** `signal.NotifyContext` devolve um contexto que é cancelado quando o sinal chega. Muito melhor que instalar um handler manual com canal.

| Sinal | Quem manda |
| --- | --- |
| `os.Interrupt` (SIGINT) | `Ctrl+C` no terminal |
| `syscall.SIGTERM` | `docker stop`, Kubernetes, systemd |

```go
if err := fxApp.Start(startCtx); err != nil { return fmt.Errorf("app: falha no start: %w", err) }
logger.Info("application_started")

<-ctx.Done()          // ← bloqueia aqui até chegar sinal
logger.Info("shutdown_signal_received")

shutdownCtx, cancel := context.WithTimeout(context.Background(), stopTimeout)
defer cancel()
if err := fxApp.Stop(shutdownCtx); err != nil { ... }
```
⭐ Repare: `<-ctx.Done()` **bloqueia** a main. É assim que o processo fica vivo servindo requisições: a goroutine do `ListenAndServe` trabalha e a main espera o sinal.

*Correção que fizemos:* eu tinha escrito `fxApp.Start(ctx)` **duas vezes**. Chamar duas vezes pode causar "lifecycle already started". Ficou **uma** chamada, com o contexto de timeout.

> **Teste-se:** por que `ListenAndServe` roda em goroutine? Por que o `OnStop` do servidor tem que vir antes do fechamento do pool? O que `signal.NotifyContext` faz?

---

## 7. Os arquivos de teste

Go tem uma regra que **você precisa saber**: testes ficam em arquivos `_test.go`, e existem dois tipos:

| Tipo | Pacote | Acessa campos privados? | Quando usar |
| --- | --- | --- | --- |
| **Caixa branca** | `package config` (mesmo pacote) | ✅ sim | Testar detalhe interno |
| **Caixa preta** | `package config_test` | ❌ não | ⭐ Testar **comportamento público** — é o que usamos |

Usamos **caixa preta de propósito**: se um teste só passa acessando campo privado, ele está testando **implementação**, não **contrato**. No dia do refactor, ele quebra sem que o comportamento tenha mudado.

### 7.1. `config_test.go`

```go
func baseEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"DB_MAX_CONNS", "DB_MIN_CONNS", ...} {
		t.Setenv(key, "")
	}
	t.Setenv("DATABASE_URL", "postgres://...")
	t.Setenv("OIDC_ISSUER", "http://localhost:8080/realms/jungleg")
}
```
| Detalhe | Explicação |
| --- | --- |
| `t.Helper()` | ⭐ Marca a função como auxiliar: quando ela falha, o `go test` aponta a linha **de quem chamou**, não a linha interna. Melhora muito o diagnóstico |
| `t.Setenv(k, "")` | ⭐ Zera variáveis que poderiam "vazar" do seu ambiente ou da CI e mascarar o teste. Um teste que passa por acaso é pior que um teste que falha |
| `t.Setenv` | ⭐ **Restaura o valor original** ao fim do teste automaticamente. É por isso que ele **proíbe** `t.Parallel()`: dois testes paralelos mexendo no mesmo ambiente seria corrida |

```go
func TestLoadRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(t *testing.T)
		wantMessage string
	}{ ... }

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseEnv(t)
			tt.mutate(t)
			_, err := config.Load()
			if err == nil { t.Fatalf("Load() deveria falhar, mas devolveu nil") }
			if !strings.Contains(err.Error(), tt.wantMessage) { ... }
		})
	}
}
```
⭐ **Padrão "table-driven test" — o jeito idiomático de testar variações em Go:**

| Elemento | Papel |
| --- | --- |
| `tests := []struct{...}` | A **tabela**: cada linha é um cenário |
| `mutate func(t *testing.T)` | Função que altera o ambiente **daquele** cenário. Deixa o teste declarativo: você vê o cenário e o que ele faz |
| `t.Run(tt.name, ...)` | Subteste nomeado. No output aparece `TestLoadRejectsInvalidConfig/DATABASE_URL_vazia` — você sabe **exatamente** qual quebrou |
| `t.Fatalf` vs `t.Errorf` | `Fatalf` **para** o teste (uso quando seguir não faz sentido); `Errorf` registra e **continua** |

**Por que testar `strings.Contains(err.Error(), msg)` e não comparar strings exatas?** Por que a mensagem pode ganhar contexto (`config: configuração inválida:\n - ...`) sem que o teste quebre. Estamos testando **que o problema foi detectado**, não a formatação.

⚠️ **O erro que estes testes pegaram:** eu tinha marcado `DATABASE_URL` com `required,notEmpty`. O teste falhou com a mensagem em inglês da lib, e — pior — o teste `TestValidateReportsEveryProblem` mostrou que a lib abortava **antes** de reportar os outros problemas. Removi a tag e deixei o `Validate` como autoridade única. **Um teste que eu escrevi para "confirmar" o código, na verdade, corrigiu o design.**

### 7.2. `router_test.go`

| Teste | O que prova |
| --- | --- |
| `TestLivenessAlwaysOK` | `/health/live` responde 200 com `{"status":"UP"}` |
| `TestReadinessReadyWhenAllDependenciesUp` | Com tudo de pé, 200 e cada dependência reportada |
| `TestReadinessReturns503WhenDependencyDown` | ⭐ Com SQS fora, 503 **e** aponta `sqs: DOWN` **e** mantém `postgres: UP` (uma falha não mascara o resto) |
| `TestMetricsEndpointIsPublic` | `/metrics` responde sem token e traz métricas do runtime |
| `TestRequestIDMiddlewarePreservesClientCorrelationID` | ⭐ `correlationId` do cliente é **preservado**; `requestId` é novo |
| `TestRequestIDMiddlewareGeneratesCorrelationID` | Sem header, `correlationId == requestId` |
| `TestTimeoutCancelsHandlerContext` | ⭐ O prazo **realmente cancela** o `ctx` do handler (medido por canal, sem corrida) |
| `TestTimeoutMiddlewareReturns504` | O chi responde 504 quando o handler estoura |
| `TestRecovererMiddlewareConvertsPanicTo500` | ⭐ Panic → 500 **e** o corpo não vaza `"falha proposital"` |
| `TestWriteErrorMapsStatusAndBody` | ⭐ Tabela com os **8** mapeamentos de erro (400/401/403/404/409/422/503/500) + `failureCode` |
| `TestUnavailableSetsRetryAfter` | 503 traz `Retry-After: 3` |

⭐ **Padrão importante: `httptest.NewRecorder()`**
```go
rec := httptest.NewRecorder()
req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
routes.Base.ServeHTTP(rec, req)
// inspeciona rec.Code e rec.Body
```
`httptest` é da **biblioteca padrão**: cria um `ResponseWriter` falso em memória e um `Request` sintético. **Sem servidor, sem porta, sem rede** — o teste roda em microssegundos. É o que o enunciado chama de *"testes com a lib padrão do Go"*.

⭐ **Como testamos o timeout de forma confiável:** o handler **não escreve na resposta** (isso seria corrida). Ele apenas **sinaliza num canal** quando o contexto é cancelado, e o teste espera com `select` + timeout. Sem `sleep` arbitrário, sem flaky test.

### 7.3. `observability_test.go`

| Teste | O que prova |
| --- | --- |
| `TestCorrelationIDRoundTrip` | Ida e volta no contexto; string vazia é ignorada |
| `TestContextHandlerInjectsCorrelationFields` | ⭐ O log **de fato** contém `correlationId` e `requestId`, verificando o **JSON gerado** |
| `TestContextHandlerDoesNotDuplicateExplicitField` | Valor explícito prevalece e aparece **uma vez** (pega o bug de `WithAttrs`) |
| `TestNewLoggerHonorsLevelFromConfig` | `LOG_LEVEL=error` → `Enabled(Info) == false` |
| `TestRedact` | Tabela do mascaramento (inclusive o caso de 2 caracteres) |
| `TestMetricsObserveOperation` | ⭐ Faz `scrape` real e procura as **13** métricas no formato de exposição |
| `TestMetricsNilSafe` | Métrica nil não causa panic |
| `TestMetricsHandlerServesRuntimeMetrics` | `/metrics` em `httptest` |
| `TestStatusLabel` | 200→`2xx_200`, 422→`4xx_422`, 503→`5xx_503` |

⭐ **O detalhe que torna `TestContextHandlerInjectsCorrelationFields` possível:** o teste escreve o log em um `strings.Builder` e **procura no JSON** (`strings.Contains(out, "\"correlationId\":\"corr-abc\"")`). Não verificamos "a função foi chamada" — verificamos **o efeito observável**. Isso é teste de comportamento.

### 7.4. `app_test.go`

```go
func TestValidateAppComposition(t *testing.T) {
	setValidEnv(t)
	if err := app.ValidateApp(); err != nil {
		t.Fatalf("grafo do Fx inválido: %v", err)
	}
}
```
⭐ **É o teste mais valioso por milissegundo investido.** Ele pega:
- construtor pedindo algo que ninguém fornece
- ciclo de dependência
- `fx.Invoke` com assinatura inválida

**Tudo isso sem subir banco, fila ou IdP.** É o teste que substitui "sobe e vê se quebra".

```go
func TestBuildAppFailsWithInvalidConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("OIDC_ISSUER", "")

	application := fx.New(app.ModuleOptions(), fx.NopLogger)
	err := application.Err()
	if err == nil {
		_ = application.Stop(t.Context())
		t.Fatal("a construção da aplicação deveria falhar com configuração inválida")
	}
	...
}
```
⭐ **Diferença conceitual que você precisa saber explicar:**

| | `fx.ValidateApp` | `fx.New(...).Err()` |
| --- | --- | --- |
| O que faz | Valida o **grafo** | **Executa** os construtores |
| Erro de injeção | ✅ detecta | ✅ detecta |
| Erro **dentro** de um construtor (config inválida) | ❌ **não** detecta | ✅ detecta |

Descobrimos isso **na prática**: o teste de config inválida **falhou** com `ValidateApp` porque nenhum construtor é executado. Daí usamos `fx.New(...).Err()`, que constrói o grafo de verdade e expõe o erro acumulado. **Esse é o tipo de detalhe que só se aprende testando** — e é ótimo assunto de entrevista.

⚠️ Note o `application.Stop(t.Context())` no caminho de sucesso: se a construção **não** falhasse, precisamos encerrar o app para não vazar recurso no teste.

---

## 8. Como rodar tudo (comandos)

**⚠️ Prefixo obrigatório neste terminal:** o cache do Go precisa apontar para o workspace. Em vez de decorar, use os alvos do `Makefile` (se tiver `make`) ou rode assim:

```powershell
# Defina uma vez por sessão do PowerShell:
$env:GOMODCACHE='E:\jungleg\.gocache\mod'
$env:GOCACHE='E:\jungleg\.gocache\build'
$env:GOPATH='E:\jungleg\.gocache\gopath'
$env:Path = "C:\Users\PoxZe\AppData\Local\Programs\DockerDesktop\resources\bin;$env:Path"
```

| Objetivo | Comando |
| --- | --- |
| Compilar tudo | `go build ./...` |
| Rodar testes | `go test ./...` |
| Testes com detector de corrida | `go test -race ./...` |
| Análise estática | `go vet ./...` |
| Formatar | `gofmt -w ./cmd ./internal` |
| Ver o que está fora do padrão | `gofmt -l ./cmd ./internal` |
| Teste detalhado de um pacote | `go test -race -v ./internal/config/` |
| Cobertura | `go test -coverpkg=./internal/... -coverprofile=coverage.out ./internal/...` |
| Rodar a app | `go run ./cmd/api` |
| Só o Docker | `docker compose up -d postgres keycloak localstack` |
| Criar as filas | `docker compose run --rm localstack-init` |
| Tudo de uma vez | `docker compose up -d --build` |
| Ver logs | `docker compose logs -f api` |
| Limpar tudo (apaga dados) | `docker compose down --volumes` |

**Variáveis mínimas para rodar a app localmente:**
```powershell
$env:DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungleg?sslmode=disable'
$env:OIDC_ISSUER='http://localhost:8080/realms/jungleg'
go run ./cmd/api
```

---

## 9. Problemas que já resolvemos (e por quê)

Esta seção é **ouro para a entrevista**: mostra que você entende os problemas, não só o código final.

| # | Problema | Causa raiz | Solução |
| --- | --- | --- | --- |
| 1 | Não conseguia listar/criar arquivos no workspace | Permissões NTFS do Windows sem `WRITE_OWNER` na pasta | Reparo de permissão (com backup) |
| 2 | `go get` falhava com "Acesso negado" | O cache global `C:\Users\PoxZe\go` é fora do workspace e não gravável | Cache movido para `.gocache/` dentro do projeto, fixado no `Makefile` |
| 3 | `missing type: *config.Config` no Fx | `Load()` devolvia `Config` (valor), o consumidor pedia `*Config` | `Load()` passou a devolver `*Config` |
| 4 | Validação de config reportava só 1 problema e em inglês | Tag `required` da lib `env` abortava no parsing | Removida a tag; `Validate()` é a **autoridade única** |
| 5 | `go mod tidy` removeu chi/fx/uuid | Rodou antes de os imports existirem | Rodado de novo após escrever o código (o tidy é a fonte da verdade) |
| 6 | `ValidateApp` não pegava config inválida | `ValidateApp` só valida o **grafo**, não executa construtores | Teste com `fx.New(...).Err()` (executa os providers) |
| 7 | Formatação fora do padrão em 3 arquivos | Código escrito à mão antes do `gofmt` | `gofmt -w` (e `gofmt -l` no checklist) |
| 8 | `docker` não reconhecido no PATH | O instalador grava no PATH do **usuário**; o processo atual não relê | Uso o caminho absoluto; o PATH novo vale em terminais abertos depois |
| 9 | `permission denied ... npipe:////./pipe/dockerDesktopLinuxEngine` | O sandbox do harness não abre **named pipe** (limitação documentada do modo confinado) | Escalar acesso para comandos Docker, ou usuário roda no terminal dele |
| 10 | Pasta `jungle_desafio` é um segundo repositório git | Clone vazio do GitHub feito dentro do workspace | **Pendente de decisão** (apagar ou reorganizar) |

---

## 10. Checklist de estudo

Marque só quando você conseguir **explicar em voz alta, sem ler**:

**Conceitos de Go**
- [ ] Por que `context.Context` é o primeiro parâmetro e o que ele carrega
- [ ] Quando usar `*T` e quando usar `T`
- [ ] Como `%w` preserva a cadeia de erros e o que `errors.Is`/`As` fazem
- [ ] Por que a interface é definida pelo **consumidor** (ex.: `HealthChecker`)
- [ ] Por que `recover()` só funciona dentro de `defer`
- [ ] O que `t.Helper()`, `t.Setenv()`, `t.Run()` fazem
- [ ] Por que `internal/` tem significado especial no Go

**Arquitetura**
- [ ] As 4 camadas e a direção das setas (domínio não importa infra)
- [ ] O que `fx.Provide`, `fx.Invoke` e `fx.Lifecycle` fazem
- [ ] O que é um value group do Fx e por que ele existe aqui
- [ ] A ordem correta do shutdown e **por quê**
- [ ] Por que liveness ≠ readiness
- [ ] Por que logs vão para stdout em JSON

**Decisões deste projeto**
- [ ] Por que `ContentBasedDeduplication=false` no SQS
- [ ] Por que `walletId` é o `MessageGroupId`
- [ ] Por que `VisibilityTimeout` baixo é perigoso e por que validamos no boot
- [ ] Por que `provider_id` vem do token e não do corpo da requisição
- [ ] Por que existe um segundo provedor (`provider-b`)
- [ ] Por que a audience `jungleg-api` existe
- [ ] Por que `OIDC_ISSUER` e `OIDC_DISCOVERY_URL` podem ser diferentes
- [ ] Diferença entre `Code` (contrato) e `FailureCode` (negócio)
- [ ] Diferença entre `ValidateApp` e `fx.New().Err()`

**Pendências que eu anotei para as próximas fases**
- [ ] Corrigir a ordem `AccessLog` × `Recoverer` (logar acesso mesmo em panic)
- [ ] Trocar `net.Listen` + `Serve` no lugar do `select` com `time.After(50ms)`
- [ ] Revisar o path `/internal/wallets` (o enunciado pede `POST /wallets`)
- [ ] Corrigir o healthcheck do `api` no compose (flag `-health-check` não existe)
- [ ] Registrar o fechamento do pool/SQS no módulo de infra (fechar **por último**)

---

## Próximo passo

**Fase 1 — `Money`.** É a parte que **você** escreve (combinamos assim). Eu entrego o contrato completo: assinaturas, invariantes, tabela de casos de teste e os "porquês" de cada decisão. Você implementa, eu reviso linha por linha e rodo a suíte.

Antes disso, me responda as perguntas da seção **10** que você **não** souber responder — vamos fechar essas lacunas primeiro.
