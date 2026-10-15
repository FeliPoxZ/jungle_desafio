# ENTENDENDO O FX — do zero, passo a passo

> Este documento explica **só** o `internal/app/app.go` e o Uber Fx.
> Se você leu o `GUIA_FASE_0.md` §6.11 e ficou perdido, **esqueça aquela seção** e leia isto aqui.
> Aqui eu vou do zero: qual problema o Fx resolve, o que cada palavra significa, e o que
> acontece de verdade quando você roda `go run ./cmd/api`.
>
> **Como ler:** leia em ordem. Cada seção depende da anterior. Ao final de cada uma tem um
> bloco **"Confira se entendeu"** — não avance sem responder.

---

## ÍNDICE

- [Parte 0 — O problema que o Fx resolve](#parte-0--o-problema-que-o-fx-resolve)
- [Parte 1 — As 3 palavras do Fx (e só 3)](#parte-1--as-3-palavras-do-fx-e-só-3)
- [Parte 2 — O grafo de dependências do NOSSO projeto (desenhado)](#parte-2--o-grafo-de-dependências-do-nosso-projeto-desenhado)
- [Parte 3 — O que acontece na prática, passo a passo](#parte-3--o-que-acontece-na-prática-passo-a-passo)
- [Parte 4 — `fx.Lifecycle`: ligar e desligar na ordem certa](#parte-4--fxlifecycle-ligar-e-desligar-na-ordem-certa)
- [Parte 5 — Por que `app.go` é separado de `cmd/api/main.go`](#parte-5--por-que-appgo-é-separado-de-cmdapimaingo)
- [Parte 6 — Nível 3: o grupo de health checkers](#parte-6--nível-3-o-grupo-de-health-checkers)
- [Parte 7 — Lendo `app.go` de cima a baixo](#parte-7--lendo-appgo-de-cima-a-baixo)
- [Parte 8 — Os erros que você vai cometer](#parte-8--os-erros-que-você-vai-cometer)
- [Resumo em uma página](#resumo-em-uma-página)

---

## Parte 0 — O problema que o Fx resolve

Antes de falar de Fx, entenda **o problema**. Senão o Fx parece só complicação.

### Sem Fx: tudo na mão, na ordem certa

Nosso sistema tem estas peças. Cada uma **depende** de outras:

| Peça | Precisa de | Para quê |
| --- | --- | --- |
| `Config` | nada (lê o ambiente) | saber a porta, a URL do banco... |
| `Logger` | `Config` | saber o nível de log e se é JSON ou texto |
| `Metrics` | nada | criar os contadores |
| `HealthChecker`s | `Config` (o de banco precisa da URL) | saber o que verificar |
| `Routes` | `Config`, `Logger`, `Metrics`, `HealthChecker`s | montar as rotas |
| `http.Server` | `Config`, `Routes` | servir HTTP |
| *(futuro)* `pgxpool.Pool` | `Config` | conectar no Postgres |
| *(futuro)* repositórios | `Pool` | falar com o banco |
| *(futuro)* serviço de aposta | repositórios, `Logger`, `Metrics` | a regra de negócio |
| *(futuro)* handlers | serviço | atender as rotas |

**Agora imagine montar isso sem Fx.** Você escreveria isto no `main`:

```go
func main() {
    // ⚠️ ISSO É O QUE O FX SUBSTITUI
    cfg, err := config.Load()
    if err != nil { log.Fatal(err) }

    logger := observability.NewLogger(cfg)
    metrics := observability.NewMetrics(cfg)

    pool, err := postgres.NewPool(ctx, cfg)
    if err != nil { log.Fatal(err) }
    defer pool.Close()

    walletRepo := postgres.NewWalletRepo(pool)
    ledgerRepo := postgres.NewLedgerRepo(pool)
    txnRepo    := postgres.NewTxnRepo(pool)
    inboxRepo  := postgres.NewInboxRepo(pool)
    outboxRepo := postgres.NewOutboxRepo(pool)

    uow := postgres.NewUnitOfWork(pool, walletRepo, ledgerRepo, txnRepo, inboxRepo, outboxRepo)

    wagerSvc := application.NewWagerService(uow, logger, metrics)
    walletSvc := application.NewWalletService(uow, logger, metrics)

    checkerPG  := infra.NewPostgresChecker(pool)
    checkerSQS := infra.NewSQSChecker(sqsClient)

    routes := httpapi.NewRouter(cfg, logger, metrics, []httpapi.HealthChecker{checkerPG, checkerSQS})
    routes.RegisterWagering(wagerSvc, authMiddleware)
    routes.RegisterWallets(walletSvc, authMiddleware)

    server := httpapi.NewServer(cfg, routes)

    // ...e agora a ordem de DESLIGAR, na ordem INVERSA:
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()
    go func() {
        if err := server.ListenAndServe(); err != nil { ... }
    }()
    <-ctx.Done()
    server.Shutdown(...)
    wagerSvc.Close()
    pool.Close()
}
```

**Conte os problemas:**

| # | Problema | Consequência |
| --- | --- | --- |
| 1 | **Ordem manual** | Se você puser `NewLogger` antes de `config.Load()`, é `nil pointer`. O compilador **não** avisa |
| 2 | **Um arquivo gigante** | Com 30 peças, o `main` vira 200 linhas de encanamento |
| 3 | **Intestável** | Você não consegue testar "o grafo está correto?" sem **executar** tudo |
| 4 | **Ordem de desligar fácil de errar** | Fechar o pool antes do worker = erro no meio da transação |
| 5 | **Trocar uma peça dói** | Mudou a assinatura de `NewWagerService`? Tem que caçar o `main` e ajustar |

### Com Fx: você declara, ele monta

Agora o mesmo sistema com Fx:

```go
fx.Provide(config.Load)                            // sei fazer *Config
fx.Provide(observability.NewLogger)                // sei fazer *slog.Logger (preciso de *Config)
fx.Provide(observability.NewMetrics)               // sei fazer *Metrics
fx.Provide(postgres.NewPool)                       // sei fazer *pgxpool.Pool (preciso de *Config)
fx.Provide(postgres.NewWalletRepo)                 // sei fazer *WalletRepo (preciso do Pool)
fx.Provide(application.NewWagerService)            // sei fazer *WagerService (preciso dos repos)
fx.Provide(httpapi.NewRouter)                      // sei fazer *Routes
fx.Provide(httpapi.NewServer)                      // sei fazer *http.Server
```

**Cinco problemas resolvidos de uma vez:**

| # | Problema | Como o Fx resolve |
| --- | --- | --- |
| 1 | Ordem manual | Ele lê os **tipos dos parâmetros** e resolve a ordem sozinho |
| 2 | Arquivo gigante | Cada pacote declara **como construir o que é dele** |
| 3 | Intestável | `fx.ValidateApp` valida o grafo em **milissegundos**, sem subir nada |
| 4 | Ordem de desligar | `fx.Lifecycle` garante a ordem inversa do registro |
| 5 | Trocar peça dói | Muda o construtor e pronto — o Fx remonta o encanamento |

⭐ **A ideia central, em uma frase:**
> **Você não escreve "crie X depois Y depois Z". Você escreve "X precisa de W, e eu sei construir X". O Fx descobre a ordem.**

**A analogia das fichas de receita:**

```
SEM FX: você é o cozinheiro e precisa lembrar
        "primeiro bato o ovo, depois a farinha..."

COM FX:  você entrega fichas de receita ao maître:
         "PANQUECA precisa de MASSA"
         "MASSA precisa de FARINHA e OVO"
         "FARINHA: pronta"
         O maître monta a ordem e te entrega a panqueca.
         Você não precisa lembrar de nada.
```

> **Confira se entendeu (Parte 0):**
> 1. Qual problema o Fx resolve que o compilador do Go **não** resolve?
> 2. O que aconteceria num `main` manual se você usasse `logger` antes de criá-lo?
> 3. Por que "você declara, ele monta" é melhor que "você escreve a ordem"?

---

## Parte 1 — As 3 palavras do Fx (e só 3)

Todo o Fx gira em torno de três coisas. Aprenda estas três e 90% do Fx está dominado.

### ① `fx.Provide(constructor)` — "eu sei FABRICAR isso"

Registra um **construtor**. Ele **não é executado agora** — só é anotado.

```go
fx.Provide(observability.NewLogger)
```

Para entender o que o Fx **aprende** com essa linha, olhe a assinatura do construtor:

```go
func NewLogger(cfg *config.Config) *slog.Logger
//             └── o que PRECISA      └── o que FABRICA
```

O Fx lê isso e anota:
> *"Para fabricar `*slog.Logger`, eu preciso de um `*config.Config`."*

**A analogia:** é uma **ficha de receita** guardada numa gaveta. Ninguém cozinhou nada ainda. Você só disse ao maître que existe essa receita.

**Regra importante:** `Provide` **não retorna nada** e **não chama nada**. É só registro.

**A variante com erro:**
```go
func Load() (*Config, error)
```
> *"Para fabricar `*Config`, chamo `Load`. Se o segundo valor não for `nil`, é ERRO e o boot para."*

⭐ O Fx trata o **segundo retorno como erro** automaticamente. É por isso que "configuração inválida impede a aplicação de subir" sai **de graça** — não precisamos de `if err != nil { log.Fatal() }`.

### ② `fx.Invoke(function)` — "e agora EXECUTE isso"

`Provide` só registra fichas. **Nada acontece** até alguém **pedir** um prato. `fx.Invoke` é o "peça agora".

```go
fx.Invoke(runHTTPServer)
```

O Fx olha a assinatura:

```go
func runHTTPServer(lc fx.Lifecycle, server *http.Server, cfg *config.Config, logger *slog.Logger)
//                   └── especial        └── PRECISO disso ──────────────┘
```

E pensa:
> *"Ele precisa de `*http.Server`. Tenho uma ficha para isso? Tenho. Mas para fabricar `*http.Server` preciso de `*Routes`... Tenho ficha? Tenho. E para `*Routes` preciso de `*Config`, `*slog.Logger`, `*Metrics`... tenho todas. **Vou fabricar tudo na ordem certa e então chamar `runHTTPServer`.**"*

⭐ **Diferença crucial:**
| | `fx.Provide` | `fx.Invoke` |
| --- | --- | --- |
| O que faz | Registra uma **ficha** | **Executa** de verdade |
| Quando roda | Nunca (é só anotação) | No boot, uma vez |
| Executa o construtor? | ❌ não | ✅ sim, o que for necessário |

**A analogia:** `Provide` é o caderno de receitas. `Invoke` é o cliente fazendo o pedido — só aí a cozinha liga.

### ③ `fx.Module(nome, ...)` — "uma caixa etiquetada"

Agrupa `Provide`s relacionados e dá um **nome** ao grupo.

```go
func configModule() fx.Option {
    return fx.Module("config",
        fx.Provide(config.Load),
        fx.Provide(observability.NewLogger),
        fx.Provide(observability.NewMetrics),
    )
}
```

**Por que agrupar?**
1. **Organização:** o log de boot mostra `[config] provided *Config` em vez de uma lista solta
2. **Escopo:** módulos podem ter providers privados (visíveis só dentro dele)
3. **Leitura:** quem abre o `app.go` vê a arquitetura em 5 linhas: `config`, `infra`, `usecase`, `httpapi`, `worker`

⭐ **A analogia:** são **gavetas etiquetadas** de fichas de receita. "Gaveta CONFIG", "Gaveta BANCO DE DADOS", "Gaveta HTTP". Não muda o funcionamento — muda a **capacidade de achar as coisas**.

### E o `fx.Options`?

```go
func ModuleOptions() fx.Option {
    return fx.Options(
        configModule(),
        httpapiModule(),
    )
}
```

`fx.Options` é só **uma lista de opções agrupadas**. Ele existe para você poder fazer:

```go
fx.New(
    app.ModuleOptions(),   // ← aqui entra a lista inteira
    fx.WithLogger(...),
    fx.StopTimeout(25*time.Second),
)
```

⭐ Repare: `fx.Options` **devolve** um `fx.Option`. `fx.Module` **também** devolve um `fx.Option`. Então módulos podem ser compostos em listas, e listas podem conter módulos. É **composição**.

### O quadro completo dos 3 + 2 auxiliares

| Comando | O que é | Executa algo? |
| --- | --- | --- |
| `fx.Provide(f)` | Ficha de receita | ❌ Não |
| `fx.Invoke(g)` | Pedido: execute g | ✅ Sim (e o que g precisar) |
| `fx.Module(nome, ...)` | Gaveta etiquetada | ❌ Não (só organiza) |
| `fx.Options(...)` | Lista de opções | ❌ Não (só empacota) |
| `fx.New(opções...)` | Monta a cozinha inteira | ✅ Executa todos os providers |

> **Confira se entendeu (Parte 1):**
> 1. `fx.Provide(config.Load)` executa o `config.Load` na hora? Por quê?
> 2. Qual a diferença entre `fx.Provide` e `fx.Invoke`?
> 3. Se eu só tiver `fx.Provide` e nenhum `fx.Invoke`, o que acontece quando eu rodo a aplicação?

<details>
<summary>Resposta da 3 (pense antes de abrir)</summary>

**Nada é construído e nada roda.** A aplicação sobe, monta o grafo e... fica vazia. Nenhum construtor é chamado, porque ninguém **pediu** nada. É por isso que `fx.Invoke(runHTTPServer)` é essencial: é ele que dispara a construção de toda a cadeia (server → routes → config/logger/metrics).

</details>

---

## Parte 2 — O grafo de dependências do NOSSO projeto (desenhado)

Este é o desenho que faltava. **As setas apontam para quem é PRECISO.**

```
                         ┌─────────────────────┐
                         │   MEIO AMBIENTE     │
                         │  (variáveis de env) │
                         └──────────┬──────────┘
                                    │ lê
                                    ▼
                         ┌─────────────────────┐
                         │  config.Load()      │  ← fabrica *Config
                         │  "sei fazer Config" │
                         └──────────┬──────────┘
                                    │ *Config
              ┌─────────────────────┼─────────────────────┐
              │                     │                     │
              ▼                     ▼                     ▼
   ┌──────────────────┐   ┌──────────────────┐   ┌──────────────────┐
   │ NewLogger(cfg)   │   │ NewMetrics()     │   │ healthCheckers   │
   │ → *slog.Logger   │   │ → *Metrics       │   │ (grupo, ver §6)  │
   └────────┬─────────┘   └────────┬─────────┘   └────────┬─────────┘
            │                      │                      │
            │ *slog.Logger         │ *Metrics             │ []HealthChecker
            └──────────┬───────────┴──────────────────────┘
                       │
                       ▼
            ┌──────────────────────────────────────┐
            │ NewRouter(cfg, logger, metrics,      │  ← fabrica *Routes
            │           checkers)                  │
            │  → *httpapi.Routes                   │
            └──────────────────┬───────────────────┘
                               │ *Routes
                               ▼
            ┌──────────────────────────────────────┐
            │ NewServer(cfg, routes)               │  ← fabrica *http.Server
            │  → *http.Server                      │
            └──────────────────┬───────────────────┘
                               │ *http.Server
                               ▼
            ┌──────────────────────────────────────┐
            │ fx.Invoke(runHTTPServer)             │  ← ⭐ O PEDIDO
            │  recebe: (lc, server, cfg, logger)   │
            │  → registra OnStart e OnStop          │
            └──────────────────────────────────────┘
```

**Leia o desenho assim:** para satisfazer o `fx.Invoke` no final (que pede um `*http.Server`), o Fx **sobe** pela árvore, descobre que precisa de `*Routes`, depois de `*Config`/`*Logger`/`*Metrics`, e então **constrói de cima para baixo**, na ordem correta.

⭐ **A ordem real de execução é o INVERSO da leitura do código:**

```
Código escrito (de cima para baixo):      Execução real (de baixo para cima):
  1. config.Load()                         1. config.Load()          ← raiz primeiro
  2. NewLogger()                           2. NewLogger(cfg)
  3. NewMetrics()                          3. NewMetrics()
  4. NewRouter()                           4. NewRouter(cfg, ...)
  5. NewServer()                           5. NewServer(cfg, routes)
  6. Invoke(runHTTPServer)                 6. runHTTPServer(...)     ← por último
```

Neste caso coincidiu. **Mas se você escrever as linhas em ordem trocada, o Fx resolve do mesmo jeito** — esse é o ponto. Veja:

```go
fx.New(
    fx.Provide(httpapi.NewServer),     // ⚠️ escrito PRIMEIRO...
    fx.Provide(httpapi.NewRouter),     // ⚠️ ...fora de ordem
    fx.Provide(observability.NewLogger),
    fx.Provide(config.Load),           // ...e o Config escrito por ÚLTIMO
    fx.Invoke(runHTTPServer),
)
```
**Funciona igual.** O Fx não lê de cima para baixo; ele monta o grafo pelos **tipos**. É exatamente essa a vantagem.

> **Confira se entendeu (Parte 2):**
> 1. Se eu escrever os `Provide` em ordem totalmente embaralhada, funciona? Por quê?
> 2. Quantos `fx.Invoke` nós temos hoje? Quantos `fx.Provide`?
> 3. No desenho, quem depende de `*slog.Logger`?

---

## Parte 3 — O que acontece na prática, passo a passo

Agora a parte que faz "clicar". Você roda:

```powershell
go run ./cmd/api
```

### Passo 1 — `main()` chama `app.Run`

```go
// cmd/api/main.go
func main() {
    if err := app.Run(version, commit); err != nil {   // ← entra aqui
        fmt.Fprintf(os.Stderr, "erro fatal: %v\n", err)
        os.Exit(1)
    }
}
```

`main` é burro de propósito: 4 linhas. Não sabe nada de Fx.

### Passo 2 — `app.Run` cria o logger e chama `fx.New`

```go
func Run(version, commit string) error {
    logger := slog.New(slog.NewJSONHandler(os.Stdout, ...)).
        With(slog.String("service", "jungleg"), ...)

    fxApp := fx.New(                          // ⭐ AQUI TUDO ACONTECE
        ModuleOptions(),
        fx.WithLogger(func() fxevent.Logger { return &fxSlogLogger{logger: logger} }),
        fx.StopTimeout(stopTimeout),
    )
```

`fx.New(...)` faz **quatro coisas**, em ordem:

| # | O que faz | Resultado se der errado |
| --- | --- | --- |
| ① | Coleta todos os `Provide` (recursivamente, entrando nos módulos) | — |
| ② | Resolve o grafo: quem depende de quem | `missing type: *config.Config` |
| ③ | **Executa** os construtores necessários, na ordem correta | `config: configuração inválida: - DATABASE_URL é obrigatória` |
| ④ | Coleta os `fx.Invoke` para rodar no start | — |

⭐ **Repare no passo ③:** `fx.New` **já executa os construtores**. Isso significa que `config.Load()` roda **aqui**, ainda dentro de `fx.New`. Se a config estiver inválida, o erro aparece neste ponto — antes de qualquer conexão de rede.

**Como você recebe esse erro:** `fx.New` **não** devolve erro (não tem retorno). O erro fica acumulado no app, e você o lê com `fxApp.Err()`. É por isso que o teste faz:

```go
application := fx.New(app.ModuleOptions(), fx.NopLogger)
err := application.Err()      // ← é AQUI que o erro de config aparece
```

⚠️ **A pegadinha que descobrimos na prática:** `fx.ValidateApp` (a função que só valida o grafo) **não executa os construtores**. Então ela detecta "falta o provider de `*Routes`", mas **não** detecta "a config está inválida". São ferramentas diferentes:

| Ferramenta | O que valida | Detecta config inválida? |
| --- | --- | --- |
| `fx.ValidateApp(...)` | Só o **grafo** | ❌ Não |
| `fx.New(...).Err()` | Grafo **+ execução** dos providers | ✅ Sim |

### Passo 3 — o Fx executa os construtores em ordem

```
▶ config.Load()
   lê APP_ENV, DATABASE_URL, HTTP_ADDR... do ambiente
   valida: tudo certo?
   → devolve *Config ✅

▶ observability.NewLogger(cfg)
   escolhe JSON ou texto conforme cfg.IsLocal()
   → devolve *slog.Logger ✅

▶ observability.NewMetrics()
   cria 13 métricas em um registry novo
   → devolve *Metrics ✅

▶ httpapi.NewRouter(cfg, logger, metrics, checkers)
   monta chi.Router, registra middlewares
   registra /health/live, /health/ready, /metrics
   → devolve *httpapi.Routes ✅

▶ httpapi.NewServer(cfg, routes)
   cria http.Server com Addr, Handler, timeouts
   → devolve *http.Server ✅
```

⭐ **Observação:** o `*http.Server` é criado, mas **não está escutando** ainda. Criar a struct `http.Server` não abre socket nenhum. Quem abre a porta é o `ListenAndServe()` — e isso só acontece no `OnStart`.

### Passo 4 — o Fx executa os `Invoke`s: `runHTTPServer`

```go
func runHTTPServer(lc fx.Lifecycle, server *http.Server, cfg *config.Config, logger *slog.Logger) {
    lc.Append(fx.Hook{           // ⭐ REGISTRA, não executa
        OnStart: func(ctx context.Context) error { ... },
        OnStop:  func(ctx context.Context) error { ... },
    })
}
```

⚠️ **Detalhe importantíssimo que confunde:** este `Invoke` **não sobe o servidor**. Ele só **registra dois ganchos** (`OnStart` e `OnStop`) no ciclo de vida. O servidor sobe no passo 5.

**Por que separar?** Porque registrar e executar são momentos diferentes:
- `fx.New` = montagem (registrar)
- `fxApp.Start(ctx)` = **execução** (ligar)

Isso permite que **vários** componentes registrem seus ganchos e todos comecem juntos, coordenados.

### Passo 5 — `fxApp.Start(ctx)` dispara todos os `OnStart`

```go
if err := fxApp.Start(startCtx); err != nil {
    return fmt.Errorf("app: falha no start: %w", err)
}
logger.Info("application_started")
```

Agora sim:
```
▶ runHTTPServer.OnStart
    goroutine: server.ListenAndServe()   ← ⭐ AQUI a porta 8080 é aberta
    espera 50ms para detectar erro de bind
    loga "http_server_started"
```

**O que você vê no terminal:**
```
time=... level=INFO msg=http_server_started addr=:8080
{"msg":"application_started","version":"dev",...}
```

### Passo 6 — o processo fica bloqueado esperando o sinal

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()
...
<-ctx.Done()          // ⭐ BLOQUEIA AQUI
logger.Info("shutdown_signal_received")
```

⭐ `<-ctx.Done()` é um **recebimento de canal** que não vai devolver nada até o contexto ser cancelado. Isso **trava** a `main` — que é exatamente o que queremos. A `main` fica viva; quem trabalha são as goroutines (o `ListenAndServe` e, no futuro, os workers).

**Sem esse `<-ctx.Done()` o programa terminaria na hora.** É o "segure o processo vivo".

### Passo 7 — você aperta Ctrl+C

```
▶ signal.NotifyContext cancela o ctx
▶ <-ctx.Done() destrava
▶ loga "shutdown_signal_received"
▶ fxApp.Stop(shutdownCtx)
     executa TODOS os OnStop, na ordem inversa do registro
▶ loga "application_stopped"
▶ main retorna → processo termina
```

### O diagrama do ciclo de vida completo

```
   go run ./cmd/api
         │
         ▼
   main() ──► app.Run()
                 │
                 ├─ cria logger do boot
                 ├─ fx.New(...)  ◄── ① coleta Provides
                 │                     ② resolve o grafo
                 │                     ③ EXECUTA os construtores ← config.Load roda aqui
                 │                     (erro? aparece em fxApp.Err())
                 │
                 ├─ fxApp.Start(ctx)  ◄── ④ executa os OnStart
                 │                          └─ server.ListenAndServe() ⭐ PORTA ABERTA
                 │
                 ├─ <-ctx.Done()      ◄── BLOQUEIA esperando sinal
                 │
                 │      ... aplicação atende requisições ...
                 │
                 ├─ fxApp.Stop(ctx)   ◄── ⑤ executa os OnStop (ordem inversa)
                 │                          └─ server.Shutdown() para de aceitar,
                 │                             espera as requisições em andamento
                 ▼
             processo termina
```

> **Confira se entendeu (Parte 3):**
> 1. Em que momento exato o `config.Load()` é executado: no `fx.New`, no `Start` ou no `Invoke`?
> 2. O `fx.Invoke(runHTTPServer)` sobe o servidor? Se não, o que ele faz?
> 3. Por que `<-ctx.Done()` é necessário?
> 4. Se eu trocar `fx.New()` por `fx.ValidateApp()`, o `config.Load()` roda?

---

## Parte 4 — `fx.Lifecycle`: ligar e desligar na ordem certa

### O conceito

Algumas peças precisam de **ação** para começar e **ação** para terminar:

| Peça | Para começar | Para terminar |
| --- | --- | --- |
| Servidor HTTP | `ListenAndServe` (abre porta) | `Shutdown` (fecha porta) |
| Consumidor SQS | laço de `ReceiveMessage` | parar o laço com o contexto |
| Publisher da outbox | laço de polling | parar o laço |
| Pool do Postgres | `pgxpool.New` (abre conexões) | `pool.Close()` |

`fx.Lifecycle` é o mecanismo que **organiza** essas ações.

```go
lc.Append(fx.Hook{
    OnStart: func(ctx context.Context) error { ... },   // ao subir
    OnStop:  func(ctx context.Context) error { ... },   // ao descer
})
```

### A regra da ordem inversa — e por que ela existe

⭐ **Regra do Fx: os `OnStop` rodam na ORDEM INVERSA do registro.**

**A analogia:** você está vestindo uma roupa. Veste **meia, depois sapato**. Para tirar, você tira **sapato, depois meia**. Sempre o inverso.

| Momento | Ordem |
| --- | --- |
| **Subir** | Registrado 1º → 2º → 3º |
| **Descer** | Registrado 3º → 2º → 1º |

### Como isso se aplica ao nosso projeto

```go
// app.go — ModuleOptions() (estado FINAL alvo)
return fx.Options(
    configModule(),      // 1º registrado → para por ÚLTIMO
    infraModule(),       // 2º registrado → para em 2º  (fecha pool/SQS)
    httpapiModule(),     // 3º registrado → para em 1º  (fecha o servidor HTTP) ⭐
    // workerModule(),   // futuro: registrado DEPOIS do httpapi
)
```

**Ordem de desligamento resultante:**

```
1. workerModule.OnStop     → para de buscar trabalho (workers)
2. httpapiModule.OnStop    → para de aceitar HTTP, espera requisições em andamento
3. infraModule.OnStop      → fecha pool do Postgres e client SQS
```

⭐ **Por que essa ordem é obrigatória (item 4 do enunciado: "fechamento das dependências após a finalização dos componentes que as utilizam"):**

Imagina a ordem errada:
```
❌ infraModule.OnStop      → fecha o pool do Postgres
❌ workerModule.OnStop     → o worker tenta COMMITAR uma transação em andamento
                             → "pool is closed" 💥 erro no meio da transação
```

```
✅ workerModule.OnStop     → termina o trabalho em andamento
✅ httpapiModule.OnStop    → para de aceitar novas requisições
✅ infraModule.OnStop      → agora sim, fecha o pool (ninguém mais usa)
```

**A analogia do prédio:** para fechar o prédio à noite, você **primeiro** manda as pessoas saírem (workers/HTTP) e **depois** desliga a energia (banco). Desligar a energia antes deixa gente presa no elevador.

### O `fx.StopTimeout` — o desistômetro

```go
fx.New(
    ModuleOptions(),
    fx.StopTimeout(25 * time.Second),   // ⭐ prazo MÁXIMO do shutdown
)
```

⭐ **Por que existe:** se um worker travar e nunca terminar o `OnStop`, o processo ficaria pendurado para sempre e o `docker stop` esperaria até o timeout do Docker (que então manda SIGKILL — morte abrupta, pior cenário).

Com `StopTimeout(25s)`: o Fx espera até 25s, desiste e retorna. O processo morre de forma **controlada**, dentro do prazo que o orquestrador dá.

> **Detalhe de alinhamento (pendência P7 do `CONTEXTO.md`):** o `SHUTDOWN_TIMEOUT` da configuração e o `fx.StopTimeout` precisam estar coerentes. Hoje o `stopTimeout` está fixo em 25s no código; o certo é ler da config. Anotado para a Fase 8.

> **Confira se entendeu (Parte 4):**
> 1. Por que o `OnStop` do servidor HTTP precisa rodar **antes** do `OnStop` do pool do Postgres?
> 2. Se eu registrar o `infraModule` **depois** do `httpapiModule`, o que acontece no shutdown?
> 3. Para que serve o `fx.StopTimeout`?

---

## Parte 5 — Por que `app.go` é separado de `cmd/api/main.go`

Você tem dois arquivos que parecem fazer a mesma coisa. **Não fazem.**

| Arquivo | Papel | Analogia |
| --- | --- | --- |
| `cmd/api/main.go` | **Liga a tomada.** 4 linhas: pega version/commit e chama `app.Run` | A chave de ignição do carro |
| `internal/app/app.go` | **Monta o motor.** Sabe todas as peças e como ligá-las | O motor completo |

**A razão técnica — e você precisa saber isto:**

⭐ **Em Go, `package main` NÃO PODE SER IMPORTADO por outro pacote.**

```go
import "github.com/FeliPoxZ/jungle_desafio/cmd/api"   // ❌ ISSO NÃO COMPILA
```

**Duas consequências gigantes:**

| Consequência | Explicação |
| --- | --- |
| **Não dá para testar `main`** | Teste é um pacote `_test` que **importa** o pacote. Se não pode importar, não pode testar |
| **Não dá para reusar** | Se quiser um segundo binário (`cmd/worker`), não poderia reusar a lógica |

**Solução:** toda a lógica de composição vai para `internal/app` (que **é** importável e **é** testável), e o `main` só faz o "cabo de força".

⭐ **Prova concreta:** nosso `internal/app/app_test.go` faz exatamente isso:
```go
application := fx.New(app.ModuleOptions(), fx.NopLogger)   // ⭐ IMPORTO app e testo
err := application.Err()
```
**Isso seria impossível** se a composição estivesse dentro do `main`.

**Bônus:** no futuro, `cmd/worker/main.go` (só workers, sem HTTP) pode reusar `app.ModuleOptions()` com opções diferentes. Um motor, várias ignições.

> **Confira se entendeu (Parte 5):**
> 1. Por que `package main` não pode ser importado?
> 2. O que **quebraria** nos testes se a composição Fx estivesse dentro do `main`?

---

## Parte 6 — Nível 3: o grupo de health checkers

⚠️ **Aviso:** esta seção é o recurso **mais avançado** do Fx que usamos. Se você entendeu as Partes 0–5, pode pular esta e voltar depois. Ela não é pré-requisito para nada.

### O problema que ele resolve

O `NewRouter` precisa de uma **lista** de verificadores de health (Postgres, SQS). Como o Fx entrega isso?

**Opção ingênua (e por que é ruim):**

```go
// ⚠️ ACOPLAMENTO
func NewRouter(cfg *Config, logger *Logger, metrics *Metrics,
               pgPool *pgxpool.Pool, sqsClient *sqs.Client) *Routes {
```
Problemas:
1. O `httpapi` agora precisa **importar** `pgxpool` e `aws-sdk`. Ele não deveria saber que essas coisas existem
2. Se amanhã eu adicionar um verificador de Redis, mudo a **assinatura** do `NewRouter`
3. **Sem lista**: se eu quiser 5 verificadores, viram 5 parâmetros

### A solução: "grupo" (uma lista que se alimenta sozinha)

**Passo 1 — o consumidor declara que quer o grupo:**

```go
type healthCheckers struct {
    fx.In
    Checkers []httpapi.HealthChecker `group:"health_checkers"`
}
```

Leia assim: *"`fx.In` = esta struct é uma lista de PARÂMETROS que eu quero. Especificamente: me dê **todos** os valores registrados no grupo `health_checkers`, como um slice."*

**Passo 2 — cada fornecedor se registra no grupo:**

```go
// em internal/infra/postgres (Fase 5)
fx.Provide(fx.Annotate(
    NewPostgresChecker,
    fx.ResultTags(`group:"health_checkers"`),   // ⭐ "meu resultado entra no grupo"
))
```

**Passo 3 — o consumidor recebe a lista pronta:**

```go
fx.Provide(func(cfg *config.Config, logger *slog.Logger, metrics *observability.Metrics,
                checkers healthCheckers) *httpapi.Routes {     // ⭐ recebe a struct
    return httpapi.NewRouter(cfg, logger, metrics, checkers.Checkers)
})
```

### A analogia da lista de e-mails

```
❌ SEM GRUPO: você precisa saber os nomes de todos
   "manda o convite para João, Maria, Pedro, Ana..."
   → novo convidado = você edita a lista

✅ COM GRUPO: você manda para a lista "time@empresa.com"
   → novo membro entra na lista, você NÃO muda nada no convite
```

⭐ **O princípio de design que isso atende** (chama-se **aberto/fechado**):
> *"Aberto para extensão, fechado para modificação."*

Adicionar um novo health check (Redis, Kafka, o que for) **não exige tocar** no `httpapi`. Você só registra o novo provider no grupo, em outro módulo.

### E o `fx.Annotate`?

`fx.Annotate` **embrulha** um construtor para adicionar metadados sem alterar o construtor original:

```go
fx.Annotate(
    NewPostgresChecker,                        // a função original
    fx.ResultTags(`group:"health_checkers"`),  // metadado: o retorno vai pro grupo
)
```

Ele existe porque **struct tags não podem ser aplicadas a valores de retorno de função**. Então o Fx oferece `Annotate` como alternativa programática.

> **Confira se entendeu (Parte 6):**
> 1. Por que `NewRouter` não deve receber o `*pgxpool.Pool` diretamente?
> 2. O que `fx.In` significa numa struct?
> 3. Se eu criar um `RedisChecker` amanhã, preciso mudar o `httpapi`?

---

## Parte 7 — Lendo `app.go` de cima a baixo

Agora que você entendeu tudo, vai ler o arquivo real **traduzido linha por linha**.

### Cabeçalho e constantes

```go
const (
    startTimeout = 30 * time.Second
    stopTimeout  = 25 * time.Second
)
```
| Constante | O que limita |
| --- | --- |
| `startTimeout` | Quanto o boot pode levar. Se o Postgres demorar mais que 30s para responder, o boot falha com erro claro em vez de pendurar |
| `stopTimeout` | Prazo total do shutdown. Precisa caber no prazo que o Docker/K8s dá antes de matar |

### `ModuleOptions` — a lista de módulos

```go
func ModuleOptions() fx.Option {
    return fx.Options(
        configModule(),
        httpapiModule(),
    )
}
```
**Tradução:** *"as gavetas de fichas deste projeto são: config e httpapi (por enquanto)."*

**Por que uma função e não uma variável?** Porque no futuro vamos incluir módulos **condicionalmente** conforme a configuração:
```go
opts := []fx.Option{configModule(), infraModule(), httpapiModule()}
if cfg.Outbox.Enabled {
    opts = append(opts, workerModule())
}
return fx.Options(opts...)
```
Uma `var` não permite isso.

### `configModule` — a gaveta de configuração

```go
func configModule() fx.Option {
    return fx.Module("config",
        fx.Provide(config.Load),
        fx.Provide(observability.NewLogger),
        fx.Provide(observability.NewMetrics),
    )
}
```
**Tradução de cada ficha:**
| Ficha | O Fx aprende |
| --- | --- |
| `config.Load` | *"posso fabricar `*Config` (e posso FALHAR)"* |
| `observability.NewLogger` | *"posso fabricar `*slog.Logger`, se tiver `*Config`"* |
| `observability.NewMetrics` | *"posso fabricar `*Metrics`, não preciso de nada"* |

### `healthCheckers` — o grupo (ver Parte 6)

```go
type healthCheckers struct {
    fx.In
    Checkers []httpapi.HealthChecker `group:"health_checkers"`
}
```
**Tradução:** *"quero receber a coleção de todos os health checkers registrados."*

⚠️ **Pendência (P5 do `CONTEXTO.md`):** hoje **ninguém** registra nada nesse grupo, então o slice chega **vazio**. É por isso que o `/health/ready` responde `{"dependencies":{},"status":"UP"}` — não há nada para checar. Na Fase 5, o Postgres se registra; na Fase 8, o SQS.

### `httpapiModule` — a gaveta HTTP

```go
func httpapiModule() fx.Option {
    return fx.Module("httpapi",
        fx.Provide(func(cfg *config.Config, logger *slog.Logger, metrics *observability.Metrics,
            checkers healthCheckers) *httpapi.Routes {
            return httpapi.NewRouter(cfg, logger, metrics, checkers.Checkers)
        }),
        fx.Provide(httpapi.NewServer),
        fx.Invoke(runHTTPServer),
    )
}
```

⭐ **Repare:** o `NewRouter` foi embrulhado numa **função anônima** em vez de ser passado direto. **Por quê?**

Porque a assinatura real é:
```go
func NewRouter(cfg, logger, metrics, checkers []HealthChecker) *Routes
```
Mas o Fx precisa receber a struct `healthCheckers` (com o `fx.In` e o `group`) para saber buscar no grupo. Então a função anônima faz a **tradução**:

```
Fx dá:        healthCheckers{Checkers: [...]}     ← struct com tag de grupo
Nós passamos: checkers.Checkers                   ← o slice
```

⭐ **Este é um padrão comum em Fx:** quando a assinatura do construtor não bate com o que o Fx precisa, você cria um adaptador. É uma "camada de tradução" de 3 linhas.

```go
        fx.Provide(httpapi.NewServer),
```
Assinatura: `func(cfg *config.Config, routes *Routes) *http.Server`. O Fx já tem os dois.

```go
        fx.Invoke(runHTTPServer),
```
⭐ **Este é o único `Invoke` do projeto.** É ele que dispara a construção da cadeia inteira. Sem ele, o Fx não construiria nada.

### `runHTTPServer` — registrar os ganchos

```go
func runHTTPServer(lc fx.Lifecycle, server *http.Server, cfg *config.Config, logger *slog.Logger) {
```
**Tradução:** *"me dê o ciclo de vida, o servidor, a config e o logger."* O `lc` é especial: o Fx o fornece porque é a interface para registrar ganchos.

```go
    lc.Append(fx.Hook{
        OnStart: func(ctx context.Context) error {
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
                logger.InfoContext(ctx, "http_server_stopped_unexpectedly")
                return nil
            case <-time.After(50 * time.Millisecond):
                logger.InfoContext(ctx, "http_server_started", slog.String("addr", server.Addr))
                return nil
            }
        },
```

**Por que tanta cerimônia? Duas razões:**

**Razão 1 — `ListenAndServe` BLOQUEIA.** Ele só retorna quando o servidor para. Se chamássemos direto no `OnStart`, o `OnStart` nunca retornaria e o boot travaria para sempre. Por isso roda em **goroutine**.

**Razão 2 — mas aí perco o erro de bind.** Se a porta 8080 estiver ocupada, o `ListenAndServe` falha... dentro da goroutine, e o `OnStart` já teria retornado "sucesso". O Fx acharia que subiu, mas a porta está fechada. **Bug silencioso.**

**A solução:** o canal `errCh` com buffer 1 permite que a goroutine **reporte** o erro de volta. E o `select` espera uma das duas coisas:
| Caso | Significa |
| --- | --- |
| `err := <-errCh` | O `ListenAndServe` retornou: ou erro de bind, ou o servidor parou |
| `<-time.After(50ms)` | 50ms se passaram e nada falhou: **consideramos que o bind deu certo** |

⚠️ **O `50ms` é um trade-off honesto, não uma garantia.** Ele pega o caso real (porta ocupada) sem atrasar o boot. A alternativa mais rigorosa:
```go
ln, err := net.Listen("tcp", cfg.HTTP.Addr)   // erro SÍNCRONO, imediato
if err != nil { return err }
go server.Serve(ln)                            // Serve não abre a porta; ela já está aberta
```
Assim não precisa de timeout nem de canal. **Anotado como pendência P3** para a Fase 7.

```go
        OnStop: func(ctx context.Context) error {
            logger.InfoContext(ctx, "http_server_stopping", slog.Duration("timeout", cfg.HTTP.ShutdownTimeout))
            if err := server.Shutdown(ctx); err != nil {
                logger.ErrorContext(ctx, "http_server_shutdown_error", slog.String("error", err.Error()))
                return err
            }
            logger.InfoContext(ctx, "http_server_stopped")
            return nil
        },
    })
```

⭐ **`Shutdown` × `Close`:**
| Método | Comportamento |
| --- | --- |
| `Close()` | Mata tudo na hora. Requisições em andamento **são abortadas** |
| `Shutdown(ctx)` | ⭐ Para de aceitar novas; **espera** as em andamento terminarem, até o prazo do ctx |

O enunciado exige (item 4): *"shutdown com interrupção de novas entradas e conclusão ou liberação do trabalho em andamento"*. `Close()` violaria isso — uma aposta no meio seria abortada sem resposta.

### `Run` — o runner

```go
func Run(version, commit string) error {
    logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})).
        With(slog.String("service", "jungleg"), slog.String("version", version), slog.String("commit", commit))

    fxApp := fx.New(
        ModuleOptions(),
        fx.WithLogger(func() fxevent.Logger { return &fxSlogLogger{logger: logger} }),
        fx.StopTimeout(stopTimeout),
    )
```

⚠️ **Repare:** este logger é **criado aqui**, fora do Fx. É um logger **de bootstrap** — existe antes de o grafo ser montado, para poder reportar erros de montagem. Depois, o logger "de verdade" (com nível vindo da config) será criado pelo `Provide`.

| Opção do `fx.New` | Por quê |
| --- | --- |
| `ModuleOptions()` | Todas as fichas da aplicação |
| `fx.WithLogger(...)` | Substitui o logger de texto padrão do Fx pelo nosso, para o boot sair em JSON |
| `fx.StopTimeout(stopTimeout)` | Prazo máximo do shutdown |

```go
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()
```
| Sinal | Quem manda |
| --- | --- |
| `os.Interrupt` (SIGINT) | `Ctrl+C` |
| `syscall.SIGTERM` | `docker stop`, Kubernetes, systemd |

`signal.NotifyContext` devolve um `ctx` que é cancelado quando o sinal chega. **Um padrão muito mais limpo** que instalar um handler manual com canal.

```go
    startCtx, cancelStart := context.WithTimeout(context.Background(), startTimeout)
    defer cancelStart()

    if err := fxApp.Start(startCtx); err != nil {
        return fmt.Errorf("app: falha no start: %w", err)
    }
    logger.Info("application_started")

    <-ctx.Done()
    logger.Info("shutdown_signal_received")

    shutdownCtx, cancel := context.WithTimeout(context.Background(), stopTimeout)
    defer cancel()

    if err := fxApp.Stop(shutdownCtx); err != nil {
        logger.Error("shutdown_error", slog.String("error", err.Error()))
        return err
    }

    logger.Info("application_stopped")
    return nil
}
```

**A sequência, sem mistério:**
| Linha | O que faz |
| --- | --- |
| `WithTimeout(30s)` + `Start` | Sobe tudo com prazo. Se algum `OnStart` falhar, o erro volta aqui |
| `logger.Info("application_started")` | Marca o momento em que a app está pronta |
| `<-ctx.Done()` | ⭐ **Bloqueia** até chegar Ctrl+C ou SIGTERM |
| `WithTimeout(25s)` + `Stop` | Desliga tudo, na ordem inversa, com prazo |
| `logger.Info("application_stopped")` | Marca o encerramento limpo |
| `return nil` | `main` recebe nil → exit code 0 |

⚠️ **Correção que fizemos:** eu tinha escrito `fxApp.Start(ctx)` **duas vezes** (uma com `ctx`, outra com `startCtx`). Chamar `Start` duas vezes é erro ("lifecycle already started"). Ficou **uma** chamada.

### `ValidateApp` e `fxSlogLogger`

```go
func ValidateApp(opts ...fx.Option) error {
    all := append([]fx.Option{ModuleOptions()}, opts...)
    all = append(all, fx.NopLogger)
    return fx.ValidateApp(all...)
}
```
Embrulho fino para os testes: junta os módulos, silencia o log e valida o grafo. Existe para o teste não precisar conhecer os detalhes de composição.

```go
type fxSlogLogger struct{ logger *slog.Logger }

func (l *fxSlogLogger) LogEvent(event fxevent.Event) {
    switch e := event.(type) {
    case *fxevent.OnStartExecuting:
        l.logger.Debug("fx_onstart", ...)
    case *fxevent.OnStartExecuted:
        if e.Err != nil { l.logger.Error("fx_onstart_failed", ...) }
    ...
    }
}
```

⭐ **O que é isso:** o `fxevent.Logger` é uma **interface do Fx** com um único método `LogEvent(event)`. O Fx chama esse método para cada evento interno (algo sendo fornecido, invocado, startando, parando).

**Por que implementar:** sem isso, o Fx usaria o logger de **texto** dele e o boot sairia num formato diferente do resto. Com isso, todo o boot sai em **JSON** igual ao resto da aplicação — o que importa para o item 12 (*"logs JSON"*) e para você conseguir ler o boot no Loki/CloudWatch.

⭐ **Repare no `switch e := event.(type)`:** um **type switch**. Cada `case` trata um tipo concreto de evento. É o jeito idiomático de lidar com "interface com muitos tipos possíveis".

> **Confira se entendeu (Parte 7):**
> 1. Por que o `NewRouter` está embrulhado numa função anônima em vez de passado direto?
> 2. Por que `ListenAndServe` roda em goroutine?
> 3. Qual a diferença entre `Shutdown` e `Close`?

---

## Parte 8 — Os erros que você vai cometer

Estes são os erros reais de quem está aprendendo Fx. Eu já cometi dois deles neste projeto.

### ❌ Erro 1 — "missing type: *alguma.Coisa"

```
missing dependencies for function "runHTTPServer":
    missing type: *config.Config
```

**O que significa:** alguém **pede** um tipo que ninguém **fornece**. É como pedir uma receita que não está na gaveta.

**Como resolver:**
1. O tipo está sendo pedido como **ponteiro** `*Config` e o provider devolve **valor** `Config`? ⭐ São tipos **diferentes** para o Go. **Foi exatamente o nosso bug.**
2. Faltou o `fx.Provide`?
3. O provider está num módulo que não foi incluído no `fx.Options`?

**Como detectar cedo:** rode `fx.ValidateApp` (temos teste pra isso). Ele pega isso em **milissegundos**.

### ❌ Erro 2 — "cycle detected in dependency graph"

```
cycle detected in dependency graph:
    *A -> *B -> *A
```

**O que significa:** A precisa de B, e B precisa de A. Impossível construir qualquer um dos dois primeiro.

**Como resolver:** quase sempre é sinal de **design errado**. Redesenhe: um dos dois deveria **receber a dependência como parâmetro de método**, não no construtor.

### ❌ Erro 3 — chamar `Start` duas vezes

```
lifecycle already started
```

**O que significa:** você chamou `fxApp.Start(...)` mais de uma vez.

**Como resolver:** uma chamada só. Se precisar garantir que terminou, guarde o erro da primeira.

### ❌ Erro 4 — esquecer o `fx.Invoke`

**Sintoma:** a aplicação sobe e **nada acontece**. Nenhum log, nenhuma porta aberta. O processo simplesmente... fica parado.

**Por que:** `Provide` só registra fichas. Sem um `Invoke` pedindo algo, **nenhum construtor é executado**.

**Como resolver:** adicione um `fx.Invoke` que peça o que você quer que exista.

### ❌ Erro 5 — `fx.ValidateApp` não pega erro de construtor

**Sintoma:** o teste de validação passa, mas a aplicação **não sobe**.

**Por que:** `ValidateApp` só valida o **grafo**, não **executa** os construtores. Config inválida só aparece na execução.

**Como resolver:** teste com `fx.New(...).Err()`. **Foi exatamente o que descobrimos** — está no `app_test.go` e na §9 do `GUIA_FASE_0.md`.

### ❌ Erro 6 — registrar o `OnStop` na ordem errada

**Sintoma:**
```
pool is closed
```
no meio de um processamento durante o shutdown.

**Por que:** o `OnStop` roda na ordem **inversa** do registro. Se o pool foi registrado **depois**, ele fecha **primeiro** — e os workers ainda estão trabalhando.

**Como resolver:** registre na ordem "de fora para dentro": primeiro o que **usa** (HTTP, workers), depois o que **é usado** (pool, cliente SQS).

> **Confira se entendeu (Parte 8):**
> - Qual desses 6 erros **nós já cometemos** neste projeto? (Dica: são 3)

---

## Resumo em uma página

Se você esquecer tudo, guarde isto:

### As 3 palavras

| Comando | Significa | Executa? |
| --- | --- | --- |
| `fx.Provide(f)` | "sei fabricar isso" (ficha de receita) | ❌ |
| `fx.Invoke(g)` | "execute isso agora" (pedido) | ✅ |
| `fx.Module(nome, ...)` | "gaveta etiquetada" | ❌ |

### O ciclo de vida em 6 passos

```
1. main()          → chama app.Run()
2. fx.New(...)     → ① coleta Provides  ② resolve grafo
                     ③ EXECUTA construtores  (config.Load roda AQUI)
4. fxApp.Start()   → ④ executa os OnStart  (porta abre AQUI)
5. <-ctx.Done()    → bloqueia esperando Ctrl+C / SIGTERM
6. fxApp.Stop()    → ⑤ executa os OnStop, em ordem INVERSA
```

### As 3 regras de ouro

1. **`Provide` não executa nada.** Só `Invoke` (ou `Start`) dispara a construção.
2. **`OnStop` roda na ordem inversa do registro.** Registre "quem usa" antes de "quem é usado".
3. **Use o tipo certo:** `Config` ≠ `*Config`. O Fx não converte tipos.

### O que já está pendente neste arquivo (para as próximas fases)

| Pendência | Fase |
| --- | --- |
| P3 — trocar `select`+`time.After(50ms)` por `net.Listen`+`Serve` | 7 |
| P5 — registrar Postgres e SQS no grupo `health_checkers` | 5 e 8 |
| P7 — ler `StopTimeout` da configuração, não fixo em 25s | 8 |
| Adicionar `infraModule()`, `usecaseModule()`, `workerModule()` | 5, 6, 8, 9 |
| Inclusão condicional do `workerModule()` conforme a config | 8 |

---

## Próximo passo

Volte ao `GUIA_FASE_0.md` §6.11 com este documento ao lado — vai fazer sentido agora.

**Me diga qual destas partes ainda está nebulosa:**
1. **Parte 0** — o problema que o Fx resolve (sem Fx vs com Fx)
2. **Parte 1** — as 3 palavras (`Provide`/`Invoke`/`Module`)
3. **Parte 3** — o passo a passo do que acontece na execução
4. **Parte 4** — `fx.Lifecycle` e a ordem de shutdown
5. **Parte 6** — o grupo de health checkers (o recurso avançado)

Eu aprofundo só naquela, com mais exemplos. Ou, se estiver tudo claro, seguimos para a **Fase 1 (`Money`)** — onde o código é **seu**, escrito com calma, e o Fx não aparece em nenhum lugar (o domínio é Go puro).
