# EVIDÊNCIAS — Fase 10 (concorrência e recuperação com três processos independentes)

> Saídas reais dos comandos, contra **PostgreSQL 16** (`jungleg-postgres`, `healthy`),
> **Keycloak** (`jungleg-keycloak`, `healthy`) e **LocalStack 4.0.3**
> (`jungleg-localstack`, `healthy`) do `docker-compose`, e contra o **binário real**
> rodando como **três processos independentes** (e também como **container Linux** na
> prova de SIGTERM).
>
> Nada aqui é "deve funcionar": os blocos de saída são cópias do terminal.

## Ambiente

| Item | Valor |
| --- | --- |
| Go | 1.26.5 (declarado no `go.mod`) |
| PostgreSQL | 16-alpine (`jungleg-postgres`, healthy) — bancos `jungleg` e `jungle_test`; `max_connections = 200` |
| Keycloak | `jungleg-keycloak` (healthy), realm `jungleg`, tokens por `client_credentials` |
| LocalStack | `localstack/localstack:4.0.3` (healthy) — quatro filas provisionadas |
| Filas | `wager-transactions.fifo`, `wager-transactions-dlq.fifo`, `wallet-events.fifo`, `wallet-events-dlq.fifo` |
| Migrations | versão **5** (`000005_correlation_id`) — **a fase não criou migration** |
| Imagem da API | `jungleg-api:local` (`docker compose build api`) — usada só na prova de SIGTERM |
| Instâncias de teste | processos do binário real em `127.0.0.1:18091`/`18092`/`18093` (faixa livre; o compose publica 8081 e o teste do binário em `internal/app` usa 18099) |

```
$ docker ps --format "{{.Names}}`t{{.Status}}"
jungleg-localstack      Up 2 hours (healthy)
jungleg-keycloak        Up 12 hours (healthy)
jungleg-postgres        Up 12 hours (healthy)

$ docker exec jungleg-localstack awslocal sqs list-queues
  .../wager-transactions-dlq.fifo
  .../wallet-events-dlq.fifo
  .../wager-transactions.fifo
  .../wallet-events.fifo
```

> ⚠️ **O que esta fase NÃO mudou no código de produção:** nenhuma garantia, nenhum
> lock, nenhuma transação. As duas mudanças de produção foram **correções de defeito
> revelado por cenário**, as duas registradas na §15 do `ARCHITECTURE.md`:
> `application_name` vindo do DSN (defeito 40) e o endereço do JWKS configurável
> (defeito 41). Nada mais.

---

## 1. Build, vet e formatação

```
$ go build ./...                                       (sem saída)
$ go vet ./...                                         (sem saída)
$ go vet -tags=integration ./...                       (sem saída)
$ gofmt -l .\internal\ .\cmd\ .\migrations\ .\test\    (sem saída)
```

## 2. Testes

```
$ go test -count=1 -race ./...
ok  	.../internal/app	                      3.164s
ok  	.../internal/app/inbox	                1.420s
ok  	.../internal/app/ports	                1.408s
ok  	.../internal/app/wagering	            1.594s
ok  	.../internal/config	                    1.442s
ok  	.../internal/consumer	                2.080s
ok  	.../internal/domain/ledger	            1.393s
ok  	.../internal/domain/money	            1.631s
ok  	.../internal/domain/wagering	          1.452s
ok  	.../internal/domain/wallet	            1.426s
ok  	.../internal/httpapi	                  1.877s
ok  	.../internal/infra/auth	                4.780s
ok  	.../internal/infra/postgres	            1.348s
ok  	.../internal/observability	            1.582s
ok  	.../internal/platform/canonicaljson	    1.306s
ok  	.../internal/platform/idgen	            1.286s
ok  	.../internal/worker	                    3.449s
```

```
$ go test -count=1 -race -tags=integration -timeout 600s ./...
ok  	.../internal/app	                     13.149s
ok  	.../internal/app/inbox	                1.432s
ok  	.../internal/app/ports	                1.374s
ok  	.../internal/app/wagering	            1.581s
ok  	.../internal/config	                    1.430s
ok  	.../internal/consumer	                2.103s
ok  	.../internal/domain/ledger	            1.392s
ok  	.../internal/domain/money	            1.660s
ok  	.../internal/domain/wagering	          1.317s
ok  	.../internal/domain/wallet	            1.291s
ok  	.../internal/httpapi	                  2.148s
ok  	.../internal/infra/auth	                5.110s
ok  	.../internal/infra/postgres	            6.680s
ok  	.../internal/observability	            1.662s
ok  	.../internal/platform/canonicaljson	    1.335s
ok  	.../internal/platform/idgen	            1.302s
ok  	.../internal/worker	                    3.428s
ok  	.../test/integration	                  114.645s
```

**A suíte inteira (Fases 1–9) continua verde**, em duas rodadas consecutivas.

**Casos novos desta fase: 15 funções de teste.**

| Onde | Funções | O que cobrem |
| --- | ---: | --- |
| `test/integration` (Fase 10) | 11 | três instâncias independentes, T1 (HTTP e SQS), T2, T3, T5, T6, T7, T8, conferência final e latência |
| `internal/infra/postgres` | 2 | `application_name` vem do DSN; sem DSN, o default continua `jungleg-api` (defeito 40) |
| `internal/infra/auth` | 2 | token válido recusado quando o JWKS anunciado é inalcançável; aceito com `OIDC_JWKS_URL` (defeito 41) |

**A infraestrutura de prova** (`test/integration/instancias_test.go`) compila
`cmd/api` **uma vez por execução** (no `TestMain`) e sobe N processos com porta,
`application_name`, pool e log próprios; liga e desliga consumidor, outbox e worker
de referências **por instância**; e encerra tudo no `t.Cleanup`, despejando o log do
processo na falha.

---

## 3. Os oito cenários do README §13, um por um

Comando único desta seção (todas as provas novas; ~47s):

```
$ go test -count=1 -race -tags=integration -timeout 600s -v \
    -run 'TestTresInstancias|TestT1Mesma|TestT2Duas|TestT3Carteiras|TestT5Queda|TestT6Tres|TestT7REFUND|TestT8Reinicio|TestConferenciaFinal|TestLatenciaSob' \
    ./test/integration/
```

```
--- PASS: TestTresInstanciasIndependentes (0.53s)
--- PASS: TestT1MesmaAposta50VezesEntreTresInstancias (1.79s)
    concorrencia_test.go:246: T1 (HTTP): 50 disparos entre api-1, api-2, api-3, 1 criada, 49 replays, transação 01a147dc-d72c-7047-949b-245b27e66efe
--- PASS: TestT1MesmaOperacao50VezesPelaFila (8.17s)
    concorrencia_test.go:332: T1 (SQS): 50 entregas distintas entre api-1, api-2, api-3, 1 débito, inbox com 50 PROCESSED
--- PASS: TestT2DuasApostasDe80Sobre100EmInstanciasDiferentes (0.53s)
    concorrencia_test.go:515: T2: 100.00 + duas BET de 80.00 entre api-2 e api-3 -> 1 PROCESSED, 1 REJECTED/INSUFFICIENT_FUNDS, saldo 20.00, 1 débito
--- PASS: TestT3CarteirasDistintasEmParaleloSemLockGlobal (0.98s)
    concorrencia_test.go:702: T3: 5 carteiras distintas concluíram com a carteira 0 PRESA (durações 14ms..34ms); a operação na carteira presa esperou 408ms e só concluiu depois da liberação
--- PASS: TestT5QuedaEntreCommitEDeleteReentregaReconhecida (7.87s)
    recuperacao_test.go:290: T5: queda entre commit e delete (mensagem em voo=1), reentrega reconhecida pela inbox (attempts 1 -> 2 em 2.013s), nenhum débito novo, DLQ vazia
--- PASS: TestT6TresPublishersReaisDisputamAMesmaOutbox (13.31s)
    recuperacao_test.go:538: T6: 12 eventos publicados por 3 publisher(es) reais (api-1=4, api-2=4, api-3=4); 0 duplicados, 0 pendentes; ordem por carteira preservada em 3 de 3 grupos
--- PASS: TestT7REFUNDAntesDaReferenciaResolvidoPorOutraInstancia (6.02s)
    recuperacao_test.go:630: T7: REFUND recebido pela api-1 e BET pela api-3; a pendência foi resolvida pela api-2 (a única com o worker); saldo final 100.00, 3 lançamentos, reconciliação consistente
--- PASS: TestT8ReinicioPreservaIdempotenciaPendenciaEConsistencia (5.61s)
    recuperacao_test.go:400: T8: instância A (pid 27328) morreu; instância B (pid 18656) devolveu replay da BET e resolveu o REFUND pendente; saldo 100.00, 3 lançamentos, reconciliação consistente
--- PASS: TestConferenciaFinalCruzandoHTTPESQS (5.30s)
    conferencia_test.go:191: Conferência final: saldo 101000 == soma do ledger 101000 em 4 lançamentos; HTTP (api-1) e SQS (api-2) na MESMA operação -> um único débito; reconciliação pela api-3 consistente
--- PASS: TestLatenciaSobConcorrenciaTresInstancias (1.34s)
```

### T0 — Três processos independentes (README §8), antes de qualquer cenário

**O que se esperava:** três processos de verdade, com conexões e memória próprias, e
a queda de um sem efeito nos outros.

**O que se observou:** três PIDs distintos; **três conjuntos de conexões** visíveis em
`pg_stat_activity` por `application_name` (`api-1`, `api-2`, `api-3`); matar a `api-1`
faz as conexões dela desaparecerem do banco **sem** afetar o atendimento das outras
duas, que continuam com os próprios pools.

**Por que isso é uma medida e não uma afirmação:** a fase anterior conseguia no máximo
"duas instâncias lógicas" sobre o **mesmo** pool (`postgresUOW`). Aqui o pool é de cada
processo, e é o banco que diz isso.

### T1 (README §13.1) — a mesma aposta 50 vezes em paralelo

**O que se esperava:** um débito, uma linha em `wager_transactions`, versão 2 e todos
os reenvios devolvendo o MESMO resultado.

**O que se observou (HTTP):** 50 requisições liberadas por barreira e distribuídas em
round-robin entre os três processos → **1 resposta 201 e 49 respostas 200** com
`idempotentReplay: true`; **a mesma transação** e o saldo **975.00** em todas; no banco,
**um** débito, **uma** linha, versão **2**; outbox com exatamente os 4 eventos da
abertura + aposta, **nenhum publicado** (publicador desligado nas três instâncias).

**O que se observou (SQS):** **50 entregas distintas** (50 `MessageId` do broker, 50
dedup ids, 50 grupos) com o mesmo corpo, consumidas por **três consumidores reais** →
**um** débito, versão **2**, e **50 linhas de inbox PROCESSED**.

**Por que a segunda metade é a que o README pede:** a inbox deduplica por
`(consumer_name, message_id)`; com 50 `MessageId` diferentes ela **não** barra nada.
Quem barrou 49 foi a **idempotência financeira** (chave + hash + índice único). É
literalmente o "os testes de duplicidade devem exercitar a deduplicação da APLICAÇÃO,
com recebimentos repetidos comprovados".

### T2 (README §8) — 100.00 BRL e duas apostas de 80.00 simultâneas

**O que se esperava:** uma `PROCESSED`, uma `REJECTED` com `INSUFFICIENT_FUNDS`,
saldo **20.00**, **um** débito, e reenvios que não mudam nada.

**O que se observou:** as duas aposta chegaram ao mesmo tempo por **instâncias
diferentes** (`api-2` e `api-3`, disparadas por barreira) → **1 PROCESSED** (saldo
devolvido 20.00) e **1 REJECTED/`INSUFFICIENT_FUNDS`** (sem saldo na resposta, como o
contrato manda); saldo gravado **2000** (20.00), versão **2**, ledger com
`CREDIT 100.00` + `DEBIT 80.00`.

Reenvios: **10 voltas em cada aposta**, alternando as instâncias → a processada volta
**200 replay** e a rejeitada volta **422 com o mesmo `failureCode`** (a decisão é
terminal); saldo, versão, ledger e o conjunto de transações **não mudam**; eventos
fecham em 2 `Processed` + 2 `BalanceChanged` + 1 `Rejected`.

**O que isso prova sobre a concorrência:** as duas passaram pelo `SELECT ... FOR
UPDATE` da **mesma linha**; a segunda esperou, leu o saldo **já atualizado** e o
domínio recusou. Sem o lock, as duas leriam 100.00 e o `UPDATE ... WHERE version = 1`
faria uma delas falhar à toa — ou, sem a versão, uma escrita sumiria (lost update).

### T3 (README §13.3 + §5.6) — carteiras distintas em paralelo, sem lock global

**O que se esperava:** carteiras diferentes avançando **em paralelo**, e nenhum lock
global.

**O que se observou:** uma transação do **próprio teste** prende a linha da carteira 0
(`SELECT ... FOR UPDATE`); **com o lock preso**, cinco operações em carteiras distintas
**concluíram** (14 ms a 34 ms) e `pg_stat_activity` **não** mostrou nenhum backend de
instância esperando lock; a operação na carteira 0 **ficou bloqueada**, o PostgreSQL
mostrou quem esperava (`wait_event_type = Lock`, `wait_event = transactionid`), e ela
só concluiu **408 ms depois**, quando o teste liberou o lock.

**Como isso evita a asserção de tempo frágil:** a diferença é **construída**, não
medida. Se houvesse lock global (tabela, schema, advisory, ou `SERIALIZABLE` abortando
o lote), as cinco operações nas outras carteiras **não teriam concluído** com a linha
da carteira 0 presa. Os tempos vão para o log, mas não são condição de aprovação.

### T5 (README §13.5) — interrupção entre o commit e a remoção da mensagem

Duas camadas, e cada uma prova uma coisa diferente.

**(i) Determinística, com processo real (automatizada).**

**O que se fez:** o consumidor de **produção** rodou com a fila real, mas com o
`Delete` embrulhado para **não apagar** — é o estado durável exato de uma queda entre o
commit e o delete (a transação commitou, a inbox ficou `PROCESSED` e o `DeleteMessage`
nunca chegou ao broker). Depois o processo que consumiu **saiu**, e o estado foi
conferido: operação aplicada, inbox `PROCESSED`, mensagem **ainda na fila**
(`ApproximateNumberOfMessagesNotVisible` com o delta esperado).

**O que se observou no consumo pela instância NOVA:** a mensagem voltou quando o
`VisibilityTimeout` venceu (2 s no cenário), a instância nova a recebeu, a inbox
**recusou** a reentrega (o contador durável `inbox.attempts` foi de **1 para 2** em
2,013 s), **nenhum débito novo**, a mensagem foi removida e a **DLQ ficou vazia**. A
reconciliação da carteira fecha em 2 lançamentos.

**(ii) Com SIGTERM real, no container Linux.**

O Windows não tem SIGTERM para processo de host (`Kill` é terminação dura), e é por
isso que esta camada é feita no container. Saída real (§6).

### T6 (README §13.6) — três publishers reais na mesma outbox

**O que se esperava:** nenhum evento perdido, nenhum duplicado, disputa de verdade.

**O que se observou:** `OUTBOX_BATCH_SIZE=1` e três processos com o publicador ligado
(a configuração força disputa pelo **mesmo** lote a cada evento; com o lote de 50 o
primeiro que acorda leva tudo e os outros dois viram espectadores) → **12 eventos**
(3 aberturas + 3 apostas) viraram **12 mensagens**, **nenhum `eventId` repetido**,
**0 pendentes**, e a soma das publicações nos logs dos três processos fechou em 12
(`api-1=4, api-2=4, api-3=4`).

**O que esta repetição NÃO acrescenta (e por que não é asserção):** a **ordem** por
carteira. Com lote de 1, a ordem de envio passa a depender de qual publisher termina
primeiro — é a limitação do `ARCHITECTURE.md` §16 ("ordem por carteira depende do
lote"). A ordem foi **observada** preservada em 3 de 3 grupos e vai para o log, mas não
é condição de aprovação: o desenho não a garante nesse arranjo.

### T7 (README §13.7) — reversão antes da referência, resolvida por outra instância

**O que se esperava:** a pendência é durável e quem a resolve é um processo que nunca
viu a requisição.

**O que se observou:** `REFUND` recebido pela **api-1** (202 `PENDING_REFERENCE`, sem
movimentar nada), `BET` recebida pela **api-3** e a pendência resolvida pela **api-2**
— a única instância com `REFERENCE_ENABLED`. A resolução aparece no log da api-2 e
**não** no das outras duas; as duas continuam prontas. Saldo final 100.00, ledger
`CREDIT 100.00 / DEBIT 25.00 / CREDIT 25.00`, versão 3, reconciliação consistente, e o
replay do REFUND devolve o saldo do instante da conclusão (100.00).

**A outra metade do §13.7** (rejeição por expiração do TTL) foi provada na Fase 9
(`TestReferenciaQueNuncaChegaExpiraEmREFERENCENOTFOUND`) contra o mesmo PostgreSQL
real. Repetir não acrescentaria: o mecanismo é o mesmo, o estado é durável e não há
nada de multi-instância na expiração. O que a topologia muda é **quem** resolve, e é o
que este teste isola.

### T8 (README §13.8) — reinício preservando idempotência, pendências e consistência

**O que se esperava:** nada disso pode viver em memória.

**O que se observou:** a instância A abriu a carteira, recebeu o `REFUND` antes da
`BET` (ficou pendente, sem worker) e depois a `BET`; **A morreu**. A instância B — pid
diferente, memória nova — respondeu ao reenvio da `BET` com **200 replay**, a **mesma**
transação e o saldo **75.00** (o do processamento original, não o de agora), **sem
segundo débito**; e, **sozinha**, resolveu a pendência (`reference_resolvida` no log de
B). Fim: saldo 100.00, versão 3, 3 lançamentos, reconciliação **consistente** em 3
lançamentos, respondida pela instância B.

### T4 (PLANO) — a rodada consolidada das três instâncias

É o que T1, T2, T3, T5, T6, T7, T8 e a conferência final fazem: **nove** cenários com
três processos/instâncias reais, na mesma suíte. O T4 do plano não é um teste separado —
é a soma deles, e por isso está marcado como atendido.

### Conferência final do §13 — saldo armazenado == soma do ledger

Feita de **duas formas independentes** no `TestConferenciaFinalCruzandoHTTPESQS`:

1. **em SQL puro**, somando `wallet_ledger_entries` direto da tabela (não passa pelo
   caso de uso nem pelo domínio, então não pode "concordar" por construção);
2. pelo **endpoint** `POST /wallets/{id}/reconciliation`, numa instância que não
   participou da operação cruzada.

Resultado: `saldo 101000 == soma do ledger 101000 em 4 lançamentos`, com versão 4 —
o `LOSS` **não** produz lançamento nem incrementa versão. E a operação que **cruzou as
duas portas** (HTTP na api-1 e SQS na api-2, ao mesmo tempo) produziu **um único
débito**.

---

## 4. Latência sob concorrência (diferencial do §14)

Método: `n = 100` operações por cenário, disparadas por barreira, em round-robin pelas
três instâncias; duração medida **no cliente**; p50/p95/p99 por percentil de amostra
ordenada. Ambiente: os três processos e o cliente na mesma máquina, PostgreSQL e
LocalStack em container.

```
carteiras DIFERENTES: p50=117ms p95=161ms p99=166ms max=166ms total=168ms (597 ops/s)
MESMA carteira     : p50=280ms p95=552ms p99=579ms max=579ms total=580ms (172 ops/s)
```

Leitura honesta do número: **não** é benchmark de produção (cliente local, mesma
máquina, sem rede real). O que ele mostra é (a) a borda responde sob concorrência sem
erro e sem conflito não tratado, e (b) a **diferença de 3,5×** entre carteiras distintas
e a mesma carteira — o custo da serialização **por carteira**. Se o lock fosse global,
a primeira linha pareceria a segunda.

---

## 5. Os defeitos REAIS encontrados nesta fase

### 5.1. Defeito 40 — todas as instâncias indistinguíveis no `pg_stat_activity`

**Sintoma:** a primeira execução do `TestTresInstanciasIndependentes` falhou com
`map[jungleg-api:5]` — as **três** instâncias apareciam com o **mesmo**
`application_name`, e não havia como atribuir uma conexão, um lock ou uma query culpada
ao processo que a produziu.

**Causa de fundo:** `postgres.NewPool` definia `application_name = "jungleg-api"`
**depois** de interpretar o DSN, sobrescrevendo qualquer valor vindo da configuração.
Com **uma** instância isso nunca incomodou; a prova multi-instância do §8 o expôs na
primeira execução.

**Correção:** o DSN vence o default; sem o parâmetro, o nome continua `jungleg-api`
(comportamento das Fases 5–9 preservado). Travado por dois testes de integração em
`internal/infra/postgres`. Efeito colateral útil: é a mesma chave que permite ao T3
medir "quem espera lock" filtrando só as conexões das instâncias.

### 5.2. Defeito 41 — o container entregue **não autenticava**

**Sintoma:** a prova de SIGTERM no container (`docker compose build api` + `docker run`)
mostrou `/health/ready` **UP** (`oidc`, `postgres`, `sqs`, `workers`) e, ao mesmo tempo,
`POST /wallets` com token **válido** respondendo **503**
`{"code":"SERVICE_UNAVAILABLE","message":"não foi possível validar a autenticação no momento"}`.

**Causa de fundo:** o Keycloak anuncia o `jwks_uri` com o nome **público** do IdP
(`http://localhost:8080/...`). De dentro do container, `localhost` é o **próprio
container** — então o discovery (buscado pelo `OIDC_DISCOVERY_URL` alcançável) passava,
o readiness ficava UP, e a busca das **chaves** falhava. O sintoma aponta para o IdP; a
causa está no endereço das chaves. Nunca apareceu antes porque nenhum teste subia o
**container** autenticando: os testes usam o binário no host.

**Correção:** `OIDC_JWKS_URL` (opcional) sobrescreve o endereço das chaves; vazio
mantém o `jwks_uri` do discovery. O `iss` continua sendo conferido contra
`OIDC_ISSUER` — a chave ajusta **onde** as chaves são buscadas, não afrouxa o emissor.
`docker-compose.yml` e `.env.example` atualizados. Dois testes novos provam o par: sem
a sobrescrita o token válido é recusado **como indisponibilidade** (503, e não 401);
com ela, aceito.

**Provado de ponta a ponta:** `docker run jungleg-api:local` com a chave → readiness
`{"oidc":"UP","postgres":"UP","sqs":"UP","workers":"UP"}` e
`POST /wallets` → `{"id":"...","balance":{"amount":"1000.00","currency":"BRL"},"version":1}`.

### 5.3. Achados de régua (o código estava certo — 4 correções antes de acusá-lo)

| # | Achado | Correção |
| --- | --- | --- |
| — | `MaxNumberOfMessages=14` no teste do T6 → o SQS devolveu `InvalidParameterValue` (o limite é 10) | leitura em rodadas de 10, com o motivo escrito no teste |
| — | T5 esperava a reentrega em poucos segundos, mas **`ChangeMessageVisibility(0)` não antecipa a entrega no LocalStack** (medido: a mensagem continuou invisível pelos 60s do prazo original) — o teste levava 67s | o `VisibilityTimeout` da FILA vai para 2s no cenário e é restaurado; a mensagem volta **sozinha** quando o prazo vence, que é o que uma queda faz de verdade |
| — | T5 exigia a fila com **exatamente 1** mensagem em voo; um resíduo de teste anterior (2 em voo) fazia falhar | a asserção passou a medir o **delta** em relação ao que já estava em voo |
| — | T1 (SQS) usava `mensagensNaFila`, que **recebe sem apagar** e deixa mensagem em voo — sujando os testes seguintes (`TestSQSMensagemProcessaEMovimentaCarteira` da Fase 8 falhou por isso na primeira rodada da suíte inteira) | o T1 passou a observar contadores e estado durável; a infraestrutura de teste passou a deixar a fila em 2s no ambiente de teste e a **drenar** (inclusive em voo) no `t.Cleanup` |

Nenhuma dessas correções tocou código de produção.

---

## 6. A prova de SIGTERM com sinal real (container Linux)

O que só o container prova: **SIGTERM de verdade**. O Windows não tem sinal de
término para processo de host (`Kill` é terminação dura — pendência P8).

### 6.1. Subida com os três laços, pelo container

```
$ docker run -d --name jungleg-fase10 --network jungleg_default -p 18181:8080 \
    -e HTTP_STOP_TIMEOUT=50s -e HTTP_SHUTDOWN_TIMEOUT=20s \
    -e WORKERS_SHUTDOWN_GRACE=10s -e SQS_SHUTDOWN_GRACE=5s \
    -e OIDC_JWKS_URL=http://host.docker.internal:8080/realms/jungleg/protocol/openid-connect/certs \
    -e SQS_CONSUME_TRANSACTIONS=true -e OUTBOX_ENABLED=true -e REFERENCE_ENABLED=true \
    jungleg-api:local

$ curl /health/ready
{"dependencies":{"oidc":"UP","postgres":"UP","sqs":"UP","workers":"UP"},"status":"UP"}

$ POST /wallets (token real de client_credentials)
{"id":"01a147c7-465e-70ab-a248-bbc6dee2f630", ... ,"version":1}
```

Ao subir, o publicador **publicou os eventos pendentes** deixados pela execução
anterior da suíte (`outbox_evento_publicado ... atraso=18.6s`): recuperação de trabalho
abandonado, de graça.

### 6.2. Mensagem EM VOO no momento do SIGTERM

Para garantir a mensagem em voo, o teste segurou o lock da carteira no PostgreSQL e
enviou a mensagem; o consumidor recebeu e ficou **bloqueado**:

```
$ psql ... -c "select application_name, wait_event_type, wait_event from pg_stat_activity where wait_event_type='Lock'"
 jungleg-api      | Lock            | transactionid

$ docker stop --timeout 60 jungleg-fase10      # SIGTERM
docker stop levou 8.09s
$ docker inspect ... --format 'exit={{.State.ExitCode}}'
exit=0

$ docker logs --tail 8 jungleg-fase10
level=WARN  msg=sqs_encerramento_prazo_esgotado mensagensEmVoo=1 prazo=5s
level=INFO  msg=sqs_visibilidade_liberada messageId=ce8dadaf-fdc5-4b2b-a8a7-219e0b08d540
level=INFO  msg=sqs_consumidor_encerrado
level=INFO  msg=http_server_stopping timeout=20s
level=INFO  msg=http_server_stopped
level=INFO  msg=postgres_pool_closing
level=INFO  msg=application_stopped
```

**O que isso prova, linha a linha:** o processo parou de buscar trabalho, deu o prazo
do `SQS_SHUTDOWN_GRACE` ao trabalho em voo, **liberou a visibilidade** da mensagem que
não terminou, parou o servidor, fechou o pool **por último** e saiu com **código 0**
dentro do prazo configurado.

### 6.3. A mensagem volta — e é processada UMA vez

```
$ docker run -d --name jungleg-fase10b ... jungleg-api:local     # instância nova
level=INFO msg=sqs_mensagem_processada messageId=ce8dadaf-... status=PROCESSED receiveCount=3

$ psql ... -c "select balance_minor, version from wallets where id='01a147c9-...'"
 97500 | 2
$ psql ... -c "select direction, amount_minor from wallet_ledger_entries ..."
 CREDIT | 100000
 DEBIT  |   2500
$ psql ... -c "select message_id, status, attempts from inbox_messages where message_id='ce8dadaf-...'"
 ce8dadaf-... | PROCESSED | 1
```

`receiveCount=3` (duas entregas que morreram sem commit + a que concluiu) e **um**
débito. A inbox registra **uma** linha, porque as duas primeiras entregas nunca
chegaram a commitar.

### 6.4. O caminho do "kill sem prazo" (também medido)

Sem `--timeout` explícito, o `docker stop` desta máquina **matou o processo em ~3,4 s**
(`exit=137`), antes de o encerramento gracioso terminar:

```
$ docker stop jungleg-fase10
docker stop levou 3.37s
$ docker inspect ... --format 'exit={{.State.ExitCode}}'
exit=137                       # SIGKILL

$ psql ... -c "select balance_minor, version from wallets ..."
 100000 | 1                    # nada commitado: a transação foi abortada
```

A mensagem voltou **quando o `VisibilityTimeout` da fila venceu** (60 s), foi
redeliverada (`receiveCount=2`) e processada **uma única vez** (saldo 97500, um débito,
DLQ vazia). É a diferença entre os dois caminhos, e ela está registrada como limitação:
**o encerramento gracioso depende de o orquestrador dar prazo**; o `docker-compose.yml`
já traz `stop_grace_period: 60s` para o serviço `api` exatamente por isso.

---

## 7. Decisões de design desta fase

| Decisão | Motivo | O que quebraria sem ela |
| --- | --- | --- |
| **Processos locais do binário**, e não três containers `api` no compose | O README §8 pede três **processos** independentes com conexões e memória próprias — e um processo do binário tem exatamente isso (PID, pool, memória, FDs). Container acrescenta isolamento de imagem/rede, que não muda nada na concorrência, e custaria rebuild a cada ciclo | A suíte passaria a depender de `docker compose build` para qualquer cenário de concorrência (minutos por execução) e o compose ganharia dois serviços que ninguém sobe em produção |
| **`application_name` por instância no DSN** | É o instrumento de MEDIDA da independência e da disputa de lock; sem ele "cada um com suas conexões" seria afirmação | O T0 não teria como provar pool próprio, e o T3 não teria como dizer **quem** espera lock |
| **Barreira (canal fechado) em vez de `go` solto ou `sleep`** | `go` solto deixa a primeira terminar antes da última começar (vira teste sequencial); `sleep` é tempo mágico | O T1/T2 passariam a testar 50 chamadas em fila, e o verde não significaria simultaneidade |
| **`OUTBOX_BATCH_SIZE=1` no T6** | Com o lote padrão (50), o primeiro a acordar leva tudo e os outros dois nunca tocam na outbox | O T6 seria "um publisher com dois espectadores", e a disputa do `SKIP LOCKED` não seria exercitada |
| **T3 por lock construído, não por cronômetro** | A diferença passa a ser do experimento, não da máquina; com operações de milissegundos, razões de tempo se invertem por ruído | O T3 ficaria intermitente, e um teste intermitente não serve de evidência em entrevista nem em revisão |
| **T5 camada (i): `Delete` embrulhado** | A janela real entre commit e delete dura microssegundos e o Windows não tem SIGTERM. O que se reproduz é o **estado durável**, que é idêntico ao da queda | Não haveria prova automatizada do caminho "commit feito, mensagem não removida" — e a reentrega ficaria sem teste |
| **T5 camada (ii): container com `--timeout` explícito** | É a única forma de exercitar **SIGTERM de verdade** neste ambiente | A prova de sinal continuaria sendo só a do Windows (que não existe) |
| **Fila de teste com `VisibilityTimeout=2s` + `drenar` no cleanup** | O resíduo de um cenário reaparecia no seguinte e fazia falhar um teste por motivo alheio | A suíte ficaria intermitente sem que houvesse defeito — e o próximo a mexer perderia horas no lugar errado |
| **Conferência final em SQL puro E pelo endpoint** | Duas implementações independentes; se as duas usassem o saldo da carteira como referência, nenhuma provaria nada | A conferência viraria tautologia |

## 8. Interpretações adotadas (o §15 pede que sejam explícitas)

1. **"Três processos independentes" = três processos do binário** (não três
   containers). O container entra na prova de sinal. Registrado no `PLANO.md` como
   ajuste da redação original ("3 containers `api` no compose"), que era mais restritiva
   do que o enunciado.
2. **T4 do `PLANO.md` não é um teste separado**: é a rodada consolidada (T1–T3, T5–T8 e
   a conferência rodando com três instâncias na mesma suíte).
3. **T1 "50 vezes"** foi provado com o número do enunciado (50). O texto anterior da
   conformidade citava "12 disparos" — que prova o mecanismo em um processo, mas não o
   número pedido. Corrigido no `docs/CONFORMIDADE_README.md` §13.
4. **T6 não assere ordem** quando `OUTBOX_BATCH_SIZE=1`: nesse arranjo o desenho não a
   garante (limitação já registrada no `ARCHITECTURE.md` §16). A partição dos eventos é
   a invariante; a ordem foi observada e reportada.
5. **T7 repetido só na metade da resolução**: a expiração do TTL já foi provada na Fase
   9 com a mesma infraestrutura real e não tem nada de multi-instância.

## 9. Limitações conhecidas desta fase

| Limitação | Por que ficou assim |
| --- | --- |
| A queda entre commit e delete é **reproduzida**, não provocada por sinal | A janela dura microssegundos; o estado durável é idêntico e a camada (ii) cobre o sinal |
| **`ChangeMessageVisibility(0)` não antecipa a entrega no LocalStack** | Medido: a mensagem continua invisível até o prazo original. Em SQS real a chamada funciona; nos testes, encurtar o `VisibilityTimeout` da fila é o caminho fiel |
| **`docker stop` sem `--timeout` mata o processo** (~3,4 s nesta máquina) | O encerramento gracioso depende de o orquestrador dar prazo; o compose já traz `stop_grace_period: 60s`. Está no `ARCHITECTURE.md` §16 |
| A distribuição de publicações entre as réplicas **não é asserida** no T6 | Depende de escalonamento; a invariante (12 eventos → 12 mensagens, sem repetição) é o que se prova. A distribuição observada vai para o log |
| A latência é medida com cliente local, na mesma máquina | Serve para comparar as duas topologias de contenção, não como número de produção |
| Sem prova de SIGTERM no Windows (P8) | Não existe sinal de término de host; a prova é do container |

## 10. Rotina de execução da fase

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'
$env:GOCACHE='E:\jungleg\.gocache\build'
$env:GOPATH='E:\jungleg\.gocache\gopath'

docker compose up -d postgres keycloak localstack
docker exec jungleg-localstack awslocal sqs list-queues   # as quatro filas

$env:TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
$env:OIDC_ISSUER='http://localhost:8080/realms/jungleg'
$env:OIDC_DISCOVERY_URL='http://localhost:8080/realms/jungleg'
$env:OIDC_AUDIENCE='jungleg-api'
$env:SQS_ENDPOINT='http://localhost:4566'

go build ./... ; go vet ./... ; go vet -tags=integration ./...
gofmt -l .\internal\ .\cmd\ .\migrations\ .\test\
go test -count=1 -race ./...
go test -count=1 -race -tags=integration -timeout 600s ./...

# só a Fase 10 (11 funções, ~47s)
go test -count=1 -race -tags=integration -timeout 600s -v -run 'TestTresInstancias|TestT1Mesma|TestT2Duas|TestT3Carteiras|TestT5Queda|TestT6Tres|TestT7REFUND|TestT8Reinicio|TestConferenciaFinal|TestLatenciaSob' ./test/integration/
```

O `-timeout 600s` não é folga decorativa: esta fase sobe processos reais e espera
`VisibilityTimeout`/backoff vencerem.

---

## MATÉRIA-PRIMA PARA OS DOIS DOCUMENTOS

> Esta seção existe para a Fase 12 **organizar**, não reconstruir. Ela é a fonte de
> `docs/SISTEMA_EXPLICADO.md` (ensinar quem vai manter) e `docs/ENTREVISTA.md`
> (treinar a defesa). Cada resposta abaixo é curta de propósito: o texto longo é
> trabalho da Fase 12; aqui está o **fato provado**, com o número.

### A. Pergunta → resposta curta (o que esta fase provou)

| Pergunta | Resposta curta, com o número |
| --- | --- |
| Como você prova que as garantias valem com mais de uma instância? | Três **processos** do binário, cada um com PID, pool e memória próprios; o banco mostra três conjuntos de conexões (`api-1`/`api-2`/`api-3`) e a queda de um não afeta os outros dois |
| A mesma aposta 50× em paralelo debita uma vez? | Sim: 1 resposta 201 + 49 respostas 200 replay, **uma** transação, **um** débito, versão 2 |
| E se as 50 chegarem pela fila em vez do HTTP? | 50 entregas **distintas** → 50 linhas de inbox `PROCESSED` e **um** débito: quem barrou foi a idempotência **financeira**, não a inbox |
| Duas apostas de 80.00 sobre 100.00 ao mesmo tempo? | 1 `PROCESSED`, 1 `REJECTED/INSUFFICIENT_FUNDS`, saldo 20.00, **um** débito, versão 2; 20 reenvios não mudam nada |
| Carteiras diferentes andam em paralelo? | Sim: com a carteira A presa por um lock, 5 operações em outras carteiras concluíram em 14–34 ms e **nenhum** backend esperava lock; a operação em A esperou 408 ms |
| Existe lock global? | Não. `pg_stat_activity` mostrou espera **só** na linha da carteira presa, e nenhuma nas demais |
| O que acontece se o processo morrer depois do commit e antes de apagar a mensagem? | A mensagem volta; a instância nova a recebe e a inbox **recusa** (`attempts` 1→2); nenhum débito novo; DLQ vazia |
| E no SIGTERM de verdade (container)? | `exit=0` em 8,09 s: parou de buscar, deu 5 s ao trabalho em voo, **liberou a visibilidade**, parou o HTTP, fechou o pool por último |
| E se o orquestrador não der prazo (`docker stop` sem timeout)? | SIGKILL em ~3,4 s; nada commitado (abortado), a mensagem voltou no `VisibilityTimeout` e foi processada **uma** vez |
| Três publishers na mesma outbox perdem ou duplicam evento? | 12 eventos → 12 mensagens, 0 duplicados, 0 pendentes, distribuição `api-1=4, api-2=4, api-3=4` |
| Referência que chega depois é resolvida por quem? | Pela instância que tem o worker (api-2), mesmo tendo o `REFUND` chegado na api-1 e a `BET` na api-3 |
| Reiniciar a aplicação perde idempotência ou pendência? | Não: replay devolve a **mesma** transação e o saldo **75.00** da época; a `PENDING_REFERENCE` foi resolvida pela instância nova; reconciliação consistente |
| O saldo bate com o ledger? | Sim, por duas vias independentes: SQL puro (101000) e o endpoint (101000, 4 lançamentos, diferença 0.00) |
| Qual o custo da serialização por carteira? | 100 operações: p99 166 ms em carteiras distintas contra 579 ms na mesma carteira (3,5×); 597 ops/s contra 172 ops/s |

### B. Decisões em tabela (para o documento de quem mantém)

| Decisão | Por que | O que quebraria sem ela |
| --- | --- | --- |
| `READ COMMITTED` + `SELECT ... FOR UPDATE` por carteira | O conflito real é "duas operações na mesma carteira"; o lock de linha trata exatamente isso, sem abortar transação sem conflito | `SERIALIZABLE` faria retry em quase todo caminho de escrita e poderia abortar transações sem conflito |
| A versão (`UPDATE ... WHERE version = $`) como segunda camada | Recusa gravar sobre estado que mudou por outro caminho | Uma escrita fora do fluxo passaria silenciosamente por cima |
| Lock **antes** de ler o saldo | Ler primeiro e travar depois deixa outra transação alterar o saldo no meio | O saldo lido é velho: uma das duas decisões de crédito/débito fica errada |
| Inbox **na mesma transação** do tratamento | Fora dela existe um instante com débito feito e sem registro (ou o contrário) | A reentrega duplicaria o débito, ou o registro diria "tratado" sem tratamento |
| Identidade durável = `MessageId` do broker | O produtor não tem como conhecer o id antes do envio | Exigir o campo no envelope obriga a inventar um segundo id, e eles divergem |
| Retry do consumidor por `ChangeMessageVisibility` com backoff | A mensagem fica invisível e volta sozinha; nada é removido nem descartado | Remover e reenviar perderia a mensagem se o envio falhasse |
| Publicar **antes** de marcar na outbox | Uma queda entre as duas etapas vira republicação (inofensiva) em vez de perda | Marcar antes perderia o evento para sempre numa queda |
| `MessageDeduplicationId` por **tentativa** na publicação | A janela de 5 min do broker engoliria a republicação | O evento republicado seria descartado pelo broker e a mensagem original continuaria a única |
| Lease em vez de "liberar no encerramento" | O lease vence sozinho; não depende de o processo ter tempo de se despedir | Processo morto sem despedida prenderia o evento para sempre |
| Fila de teste com 2 s de visibilidade + `drenar` | O prazo curto faz resíduo voltar logo; o dreno remove inclusive o que está em voo | Um resíduo de 60 s reaparece no teste seguinte e o faz falhar por motivo alheio |

### C. O que a Fase 10 acrescentou à compreensão do sistema

1. **Lock de linha não é "só teoria" quando se vê a espera.** O `wait_event_type =
   Lock`/`transactionid` no `pg_stat_activity` é a prova de que a segunda transação
   **esperou** — e que a espera é da **linha**, não da tabela. É a diferença entre
   "acredito no `FOR UPDATE`" e "vi quem espera, por quê, e que os outros não esperam".
2. **Idempotência tem duas camadas independentes, e a fase mostrou qual barra o quê.**
   50 entregas distintas com o **mesmo** conteúdo provaram que a inbox **não** é a
   garantia financeira: ela deduplica mensagem, não operação. Quem impede o segundo
   débito é a chave + hash + índice único.
3. **Replay é sobre o passado.** O saldo devolvido é o do **processamento original**
   (75.00), não o de agora — e isso ficou óbvio quando o replay veio de um processo que
   nunca tinha visto a operação.
4. **Pendência é dado, não estado de memória.** O `REFUND` esperou, a instância morreu,
   outra resolveu. Se a pendência vivesse em memória, o processo novo não saberia de
   nada.
5. **O encerramento tem prazo e o prazo tem consequência.** Com trabalho em voo, o
   SIGTERM gracioso termina com `exit=0` e devolve a mensagem; sem prazo, o SIGKILL
   aborta a transação e a mensagem só volta pelo `VisibilityTimeout`. O sistema continua
   correto nos dois casos — a diferença é **latência de recuperação**.
6. **A recuperação é medida no relógio errado se você olhar o processo.** `attempts`,
   `status`, `published_at`, `next_resolution_attempt_at` — todo o estado de recuperação
   é **coluna de banco**. Foi o que permitiu reiniciar e continuar.
7. **Régua errada é o modo de falha mais comum de teste de concorrência.** Nesta fase
   foram **quatro** correções de régua (limite de lote do SQS, contadores aproximados,
   visibilidade que o LocalStack não antecipa, resíduo de fila) contra **dois** defeitos
   reais de código. O teste de concorrência erra primeiro na expectativa.
8. **Um cenário pode revelar defeito em lugar que ninguém olhava.** A prova de SIGTERM
   em container não achou problema de concorrência: achou que **o container entregue não
   autenticava** (defeito 41) e que todas as instâncias eram indistinguíveis no banco
   (defeito 40). Os dois estavam lá desde as Fases 5 e 7.

### D. Perguntas difíceis que a fase permite responder com honestidade

| Pergunta difícil | Resposta honesta |
| --- | --- |
| "Sua prova de queda entre commit e delete foi uma queda de verdade?" | "Foi o **estado durável** de uma queda, reproduzido com o consumidor real e o `Delete` embrulhado. E o sinal de verdade está provado no container: `exit=0` com `sqs_visibilidade_liberada`. O que não fiz foi matar o processo na janela de microssegundos — ela não é reproduzível por sinal." |
| "Então com `docker stop` normal você perde a graça do shutdown?" | "Depende do prazo do orquestrador. Sem prazo, o SIGKILL aborta a transação e a mensagem volta pelo `VisibilityTimeout`; o sistema continua sem débito duplicado, só demora mais. O compose define `stop_grace_period: 60s` por isso." |
| "A ordem por carteira vale com três publishers?" | "Com o lote padrão, sim. Com lote de 1 — que eu usei para forçar disputa — a ordem de envio depende de quem termina primeiro, e o desenho não garante. Está registrado como limitação no ARCHITECTURE §16; a invariante que eu provo nesse arranjo é a partição dos eventos." |
| "E a latência, é boa?" | "Não sei dizer 'boa' sem meta. O número medido é p99 166 ms em carteiras distintas e 579 ms na mesma, com cliente local. O que ele mostra é que a serialização é **por carteira**; não é número de produção." |
| "Se o LocalStack não honra `ChangeMessageVisibility(0)`, sua liberação de visibilidade funciona?" | "A chamada é feita e o log a registra; o que o LocalStack não faz é antecipar a entrega. Em SQS real ela antecipa. A entrega não depende disso: a mensagem volta quando o prazo vence, e foi assim que o teste provou o resultado." |
