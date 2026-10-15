# QUESTOES.md — Fixação e preparação para a entrevista

> Responda **com suas palavras**, sem olhar o `GUIA_FASE_0.md`. Depois confira.
> Se errar, não pule: releia a seção indicada. O objetivo não é acertar, é **entender**.
>
> Este arquivo é nosso termômetro. Quando o tempo apertar, é aqui que eu confiro se
> você está pronto para defender o projeto.

**Legenda:** 🟢 fácil · 🟡 média · 🔴 de entrevista técnica (a que separa candidatos)

---

## BLOCO 1 — Conceitos de Go

### 1.1. 🟢 `context.Context`
1. Cite as **duas** coisas que um `context.Context` carrega.
2. Por que `cancel()` **sempre** precisa de `defer`?
3. Por que `Context` nunca deve ser guardado dentro de uma struct?

### 1.2. 🟢 Ponteiro vs valor
4. Qual a diferença entre `func (w Wallet) Debit()` e `func (w *Wallet) Debit()`?
5. Por que `Money` (value object) deve trafegar **por valor** e `Wallet` por **ponteiro**?
6. Que bug o teste do Fx encontrou exatamente por causa disso? (ver `config.Load`)

### 1.3. 🟡 Erros
7. Qual a diferença entre `fmt.Errorf("...: %v", err)` e `fmt.Errorf("...: %w", err)`?
8. O que `errors.Is` faz que `==` não faz?
9. O que `errors.As` faz e para que usamos isso no `WriteError`?
10. Por que `panic` não pode representar uma rejeição de negócio?

### 1.4. 🟡 Interfaces
11. Por que `HealthChecker` está declarada em `httpapi` e **não** no pacote que a implementa?
12. Interface pequena é melhor que interface grande. Por quê?
13. O que é satisfazer uma interface **implicitamente** em Go?

### 1.5. 🟡 Contexto com chave tipada
14. Por que usar `type contextKey string` em vez de `"correlationId"` direto?
15. O que `v, ok := ctx.Value(k).(string)` previne?

### 1.6. 🔴 `slog`
16. Por que o `contextHandler` precisa implementar `WithAttrs` e `WithGroup`?
    (Dica: o que acontece com um logger derivado de `logger.With(...)`?)
17. No callback de `Record.Attrs`, o que significa devolver `true`? E `false`?

---

## BLOCO 2 — Configuração

### 2.1. 🟢
18. Por que `Validate()` acumula problemas em um slice em vez de retornar no primeiro?
19. Qual a diferença entre **validar** e **normalizar**? Dê o exemplo do `OIDC_ISSUER`.
20. Por que `env.ParseWithOptions(&cfg, ...)` precisa do `&`?

### 2.2. 🔴
21. **Por que recusamos `SQS_VISIBILITY_TIMEOUT < 5s` no boot?** Explique a consequência
    concreta que isso evita (cite o item do enunciado que seria violado).
22. Por que `DB_MIGRATIONS_ON_START` é `false` por padrão?
23. Por que `DB_STATEMENT_TIMEOUT` e `DB_LOCK_TIMEOUT` são necessários **no banco** e não
    só no contexto do Go?
24. Por que `OIDC_ISSUER` e `OIDC_DISCOVERY_URL` podem apontar para lugares diferentes?

---

## BLOCO 3 — HTTP

### 3.1. 🟢
25. Por que `/health/live` **não** verifica o Postgres?
26. O que aconteceria se ele verificasse e o Postgres caísse por 30 segundos?
27. O que o `/health/ready` deve devolver se o Postgres está OK e o SQS caiu? Por quê?

### 3.2. 🟡
28. Por que precisamos de `statusRecorder`? Que informação o `http.ResponseWriter` não expõe?
29. O que o método `Unwrap()` do `statusRecorder` viabiliza?
30. Por que logamos `r.URL.Path` e não `r.URL.String()`?

### 3.3. 🔴
31. **No chi, se eu registro o middleware A e depois o B, qual envolve qual?**
32. Por que `ReadHeaderTimeout` existe separado de `ReadTimeout`?
33. Explique `DisallowUnknownFields()` e `UseNumber()` no `decodeJSON`.
34. No `WriteError`, por que o corpo da resposta **nunca** inclui `err.Error()`?
35. Qual a diferença entre `Code` e `FailureCode`? Dê um exemplo de cada.

### 3.4. 🔴
36. Por que `ErrUnavailable` devolve `Retry-After`? Que problema de contrato isso resolve?
37. Como o `errors.As` no `WriteError` consegue achar um `*APIError` dentro de um erro
    envelopado três níveis acima?

---

## BLOCO 4 — Uber Fx

38. 🟢 O que `fx.Provide` faz? E `fx.Invoke`?
39. 🟢 Como o Fx sabe que `NewServer` precisa de `*config.Config`?
40. 🟡 O que acontece se um provider devolve `(T, error)` e o erro é diferente de `nil`?
41. 🔴 Qual a diferença entre `fx.ValidateApp` e `fx.New(...).Err()`? Por que isso **importa**
    e como descobrimos na prática?
42. 🔴 Em que ordem o Fx executa os `OnStop`? Por que isso garante que o servidor HTTP para
    **antes** do pool do Postgres fechar?
43. 🟡 O que é um value group (`group:"health_checkers"`) e que princípio de design ele atende?
44. 🔴 Por que `ListenAndServe` roda em goroutine no `OnStart`? E por que existe o
    `select` com `time.After(50ms)`? (Bônus: qual seria a alternativa mais rigorosa?)

---

## BLOCO 5 — Docker e infraestrutura

45. 🟢 Por que o `Dockerfile` copia `go.mod`/`go.sum` **antes** do código?
46. 🟡 O que `CGO_ENABLED=0` garante? Por que isso permite usar a imagem `alpine`?
47. 🔴 **Por que o `ENTRYPOINT` usa a forma exec (`["/app/app"]`) e não `sh -c "/app/app"`?**
    Relacione com o requisito de SIGTERM do item 10 do enunciado.
48. 🟢 Por que o container roda com `USER 10001` e não como root?
49. 🟡 Por que o serviço `migrate` usa `restart: "no"` e a API usa `service_completed_successfully`?
50. 🔴 **Por que `ContentBasedDeduplication=false`?** O que o enunciado exige que a
    deduplicação do SQS **não** consegue entregar?
51. 🔴 Por que o `MessageGroupId` será o `walletId`? Que garantia isso dá?
52. 🟡 Por que o healthcheck do Keycloak usa `/dev/tcp` em vez de `curl`?

---

## BLOCO 6 — Segurança (autenticação/autorização)

53. 🔴 **Por que o claim `provider_id` é fixo no client do Keycloak, e não lido do corpo da
    requisição?** O que um atacante conseguiria se viesse do corpo?
54. 🔴 Por que existe o client `provider-b-service`? Que teste ele torna possível?
55. 🟡 O que é a audience `jungleg-api` e que ataque ela impede?
56. 🟡 Qual a diferença entre as roles `provider` e `internal`? Quem pode abrir carteira?
57. 🔴 Um token de `provider-b` tentando ler uma transação de `provider-a` deve receber
    **401**, **403** ou **404**? Justifique considerando o que o enunciado exige.

---

## BLOCO 7 — Observabilidade

58. 🟢 Qual a diferença entre `requestId` e `correlationId`?
59. 🔴 **Por que `walletId` NUNCA pode ser label de uma métrica do Prometheus?**
60. 🟡 Por que `NewMetrics()` cria um registry próprio em vez de usar o global?
61. 🟡 Por que a duração é registrada em **segundos** e o nome termina em `_seconds`?
62. 🟡 Por que os helpers de métrica checam `if m == nil`?
63. 🔴 Por que escolhemos buckets de histograma entre 1ms e 2.5s?

---

## BLOCO 8 — Perguntas que o avaliador VAI fazer

Estas são as perguntas abertas. Treine responder em **2 minutos cada**, olhando o código.

64. 🔴 *"Mostre como você garante que dinheiro nunca passa por ponto flutuante."*
65. 🔴 *"Como você garante idempotência que sobrevive ao reinício de todos os processos?"*
66. 🔴 *"Duas apostas de 80.00 chegam ao mesmo tempo numa carteira com 100.00. O que acontece?"*
67. 🔴 *"O que impede uma movimentação duplicada se o SQS entregar a mesma mensagem duas vezes?"*
68. 🔴 *"Como você garante que um evento só é publicado DEPOIS do commit?"*
69. 🔴 *"Onde estão as invariantes financeiras garantidas? No código ou no banco? Por quê os dois?"*
70. 🔴 *"Se eu matar o processo no meio de um processamento, o que acontece?"*
71. 🔴 *"Por que o ledger é append-only? Como isso é imposto?"*
72. 🔴 *"Como você prova que não há atualização perdida?"*
73. 🔴 *"Por que não usar lock global? Como você garante paralelismo entre carteiras?"*

---

## GABARITO RÁPIDO (só para conferir depois de responder)

<details>
<summary>Clique para expandir</summary>

1. Cancelamento/prazo e valores de escopo de requisição.
2. Sem `defer cancel()`, o timer do contexto vive até estourar: vaza memória e goroutine (`go vet` acusa `lostcancel`).
3. Contexto é por requisição; guardar em struct mistura escopos e impede cancelamento correto.
4. Valor = cópia (mudanças não afetam o original); ponteiro = referência (afeta).
5. `Money` é imutável — copiar é barato e seguro. `Wallet` tem estado mutável — precisa ser compartilhado.
6. `Load()` devolvia `Config`, `runHTTPServer` pedia `*Config`: tipos diferentes, grafo não fechava.
7. `%v` achata o erro; `%w` envelopa e preserva a cadeia para `errors.Is/As`.
8. `errors.Is` percorre a cadeia comparando com um erro **sentinela** (funciona através de `Unwrap`).
9. `errors.As` extrai um **tipo** específico da cadeia; usamos para achar `*APIError` e decidir o status.
10. Rejeição de negócio é prevista e tratada (422); panic é bug (500). Misturar torna o contrato imprevisível.
11. Interfaces pertencem ao consumidor: o `httpapi` não precisa importar banco de dados.
12. Interface pequena é fácil de satisfazer e de simular em teste.
13. Em Go não existe `implements`: basta ter os métodos com a assinatura certa.
14. Evita colisão silenciosa de chaves entre pacotes diferentes.
15. Panic por type assertion falha quando o valor é `nil`/de outro tipo.
16. Sem eles, o logger derivado perde a injeção de correlação (bug sutil: funciona só no raiz).
17. `true` continua a iteração; `false` para. (Oposto do `break`.)
18. Para o operador corrigir tudo de uma vez, em vez de um problema por boot.
19. Validar diz certo/errado; normalizar deixa canônico. Ex.: remover a barra final do issuer para comparar com o claim `iss`.
20. A lib precisa **escrever** na struct; sem `&` ela escreveria numa cópia.
21. Visibility timeout menor que o processamento faz o SQS reentregar a mesma mensagem a outro consumidor → processamento simultâneo → risco de movimentação duplicada (item 3 e eliminatório do item 14).
22. Com 3 instâncias subindo juntas, todas migrariam ao mesmo tempo (corrida). Quem migra é o job dedicado.
23. O cancelamento do Go pode não chegar se a query estiver bloqueada no servidor; `statement_timeout`/`lock_timeout` são impostos **pelo Postgres**.
24. O `iss` do token é fixado pelo `KC_HOSTNAME` (`localhost:8080`), mas o container não alcança `localhost` — precisa de `host.docker.internal`.
25. Liveness responde "o processo está travado?".
26. O orquestrador mataria todas as instâncias por uma falha externa; quando o banco voltasse, não haveria ninguém no ar.
27. 503, com `postgres: UP` e `sqs: DOWN` — readiness falha se **qualquer** dependência crítica estiver fora, mas reporta todas.
28. O `ResponseWriter` não expõe o status depois de escrito; precisamos interceptar `WriteHeader`.
29. Permite ao `http.ResponseController` (usado pelo `TimeoutHandler` do chi) alcançar o writer original e escrever o 504.
30. `String()` incluiria a query string, que pode conter dado sensível.
31. O **primeiro** registrado é o mais **externo** (envolve os demais).
32. Protege contra cliente que abre a conexão e nunca envia os headers (slow loris).
33. `DisallowUnknownFields` recusa campo não previsto; `UseNumber` evita conversão implícita para `float64`.
34. Evita vazar detalhe interno (nome de tabela, SQL, caminho de arquivo).
35. `Code` = tipo do erro de contrato (`BUSINESS_REJECTED`); `FailureCode` = regra de negócio (`INSUFFICIENT_FUNDS`).
36. Diz ao cliente **quando** tentar de novo, distinguindo "temporário" de "quebrado".
37. Porque `APIError.Unwrap()` devolve o erro interno, e `errors.As` percorre a cadeia.
38. `Provide` registra como construir um tipo; `Invoke` executa algo uma vez no boot.
39. Lê os tipos dos parâmetros do construtor e procura providers compatíveis.
40. O Fx aborta o boot e devolve o erro.
41. `ValidateApp` só valida o grafo (não executa construtores); `fx.New().Err()` executa e expõe erro de provider. Descobrimos porque o teste de config inválida não falhava.
42. Ordem inversa do registro. O servidor registra antes dos workers, então para primeiro; o pool registra por último, então fecha por último.
43. Grupo que agrega todos os valores anotados; atende aberto/fechado — adicionar um health check novo não altera o `httpapi`.
44. `ListenAndServe` bloqueia. O `select` com 50ms captura erro de bind (porta ocupada) sem travar o boot. Alternativa: `net.Listen` + `server.Serve(ln)`, que dá erro síncrono.
45. Aproveita o cache de camadas do Docker: dependências só são rebaixadas quando `go.mod` muda.
46. Binário estático, sem dependência de libc → roda em imagem mínima.
47. A forma exec faz o processo ser PID 1 e receber SIGTERM direto. Com `sh -c`, o shell seria PID 1, engoliria o sinal e o shutdown gracioso nunca rodaria (violando o item 10).
48. Reduz o impacto de uma eventual exploração.
49. `restart: "no"` evita loop infinito de migration com erro de schema; `service_completed_successfully` garante que a API só sobe depois do schema pronto.
50. A dedup do SQS dura 5 minutos e não sobrevive a reinício; o enunciado exige idempotência **persistente** (item 5.2).
51. `walletId` garante **ordem por carteira** em fila FIFO (BET antes de ROLLBACK da mesma carteira).
52. A imagem do Keycloak não tem `curl`/`wget`; `/dev/tcp` é recurso do bash.
53. Se viesse do corpo, qualquer cliente poderia se declarar outro provedor. No token, é garantido pelo IdP.
54. Torna possível provar o isolamento entre provedores (item 2): B acessando A deve receber 403.
55. Amarra o token a um público pretendido; impede aceitar token emitido para outro sistema do mesmo realm.
56. `provider` opera apenas as próprias transações; `internal` é o único que abre/consulta carteira e reconcilia.
57. 403 — autenticado (não é 401) mas sem permissão. **Nota:** depende da decisão de contrato; documentar em `ARCHITECTURE.md`. Alguns preferem 404 para não revelar existência; o enunciado fala em "acesso não autorizado", então 403 é o mais direto.
58. `requestId` identifica a requisição HTTP; `correlationId` acompanha a operação de negócio e atravessa serviços.
59. Cardinalidade: um label por carteira criaria milhões de séries e derrubaria o Prometheus. IDs vão para logs.
60. O registry global é singleton e registrar o mesmo nome duas vezes causa panic; testes criam várias instâncias.
61. Convenção do Prometheus; sem o sufixo, as ferramentas não interpretam a unidade.
62. Permitem usar `*Metrics` nil como no-op (testes de domínio que não se importam com métricas).
63. Porque a operação é dominada por uma transação no Postgres: 1ms a 2.5s cobre a realidade e dá resolução útil no p50/p95/p99.
64-73. Respostas estão nas fases correspondentes; treine com o código aberto.

</details>
