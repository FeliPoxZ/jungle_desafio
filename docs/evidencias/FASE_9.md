# EVIDÊNCIAS — Fase 9 (publicação da outbox + worker de referências + reaper)

> Saídas reais dos comandos, contra **PostgreSQL 16** (`jungleg-postgres`,
> `healthy`), **Keycloak** (`jungleg-keycloak`, `healthy`) e **LocalStack 4.0.3**
> (`jungleg-localstack`, `healthy`) do `docker-compose`, e contra o **binário
> real** (`bin/api.exe`) com os três laços ligados.
>
> Nada aqui é "deve funcionar": os blocos de saída são cópias do terminal.

## Ambiente

| Item | Valor |
| --- | --- |
| Go | 1.26.5 (declarado no `go.mod`) |
| PostgreSQL | 16-alpine (`jungleg-postgres`, healthy) — bancos `jungleg` e `jungle_test` |
| Keycloak | `jungleg-keycloak` (healthy), realm `jungleg`, tokens por `client_credentials` |
| LocalStack | `localstack/localstack:4.0.3` (healthy) — quatro filas provisionadas |
| Filas | `wager-transactions.fifo`, `wager-transactions-dlq.fifo`, **`wallet-events.fifo`**, **`wallet-events-dlq.fifo`** |
| Migrations | versão **5** (`000005_correlation_id`) nos dois bancos |
| `bin/migrate.exe` | **recompilado** (as migrations são embutidas por `go:embed`) |

```
$ docker exec jungleg-localstack awslocal sqs list-queues
{
    "QueueUrls": [
        "http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/wager-transactions-dlq.fifo",
        "http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/wallet-events-dlq.fifo",
        "http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/wager-transactions.fifo",
        "http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/wallet-events.fifo"
    ]
}
```

`wallet-events.fifo` já vem provisionada pelo `init-queues.sh` desde a Fase 8, com
`FifoQueue=true`, `ContentBasedDeduplication=false`, `VisibilityTimeout=30`,
`ReceiveMessageWaitTimeSeconds=20` e `RedrivePolicy` (`maxReceiveCount=5`) para a
DLQ de eventos. **A Fase 9 não teve de provisionar nada** — teve de publicar nele.

---

## 1. Build, vet e formatação

```
$ go build ./...                              (sem saída)
$ go vet ./...                                (sem saída)
$ go vet -tags=integration ./...              (sem saída)
$ gofmt -l .\internal\ .\cmd\ .\migrations\ .\test\    (sem saída)
```

## 2. Testes

```
$ go test -count=1 -race ./...
ok  	.../internal/app	                      2.969s
ok  	.../internal/app/inbox	                1.394s
ok  	.../internal/app/ports	                1.316s
ok  	.../internal/app/wagering	            1.523s
ok  	.../internal/config	                    1.379s
ok  	.../internal/consumer	                1.989s
ok  	.../internal/domain/ledger	            1.333s
ok  	.../internal/domain/money	            1.556s
ok  	.../internal/domain/wagering	          1.393s
ok  	.../internal/domain/wallet	            1.333s
ok  	.../internal/httpapi	                  2.413s
ok  	.../internal/infra/auth	                3.676s
ok  	.../internal/infra/postgres	            1.324s
ok  	.../internal/observability	            1.546s
ok  	.../internal/platform/canonicaljson	    1.231s
ok  	.../internal/platform/idgen	            1.276s
ok  	.../internal/worker	                    3.413s
```

```
$ go test -count=1 -race -tags=integration -timeout 400s ./...
ok  	.../internal/app	                     11.758s
ok  	.../internal/app/inbox	                1.374s
ok  	.../internal/app/ports	                1.325s
ok  	.../internal/app/wagering	            1.609s
ok  	.../internal/config	                    1.359s
ok  	.../internal/consumer	                2.063s
ok  	.../internal/domain/ledger	            1.421s
ok  	.../internal/domain/money	            1.645s
ok  	.../internal/domain/wagering	          1.293s
ok  	.../internal/domain/wallet	            1.253s
ok  	.../internal/httpapi	                  1.994s
ok  	.../internal/infra/auth	                3.955s
ok  	.../internal/infra/postgres	            6.138s
ok  	.../internal/observability	            1.673s
ok  	.../internal/platform/canonicaljson	    1.303s
ok  	.../internal/platform/idgen	            1.300s
ok  	.../internal/worker	                    3.715s
ok  	.../test/integration	                  59.112s
```

**Casos novos desta fase: 38 unitários + 8 funções de integração.**

| Onde | Casos | O que cobrem |
| --- | ---: | --- |
| `internal/worker` (publicador) | 8 | ordem publicar→marcar, **ausência** de `MarkPublished` na falha, quarentena por tentativas esgotadas, quarentena por recusa permanente, lote vazio sem chamar o destino, atraso da outbox no `/metrics`, parada no cancelamento, respeito ao batch |
| `internal/worker` (backoff) | 2 | jitter **nunca** acima do teto (defeito 32), crescimento exponencial e estabilização no teto |
| `internal/app/wagering` (`Retomar`) | 10 | resolução da referência, resolução **apesar** do TTL, reagendamento sem movimentar, expiração por tentativas, expiração por idade, guarda "outro worker já agiu", guarda "estado esperado mudou", estado terminal ignorado, transação inexistente, referência incompatível |
| `internal/worker` (referências + reaper) | 7 | retomada do vencido, **ausência** de chamada sem vencidas, uma linha com erro não interrompe o lote, parada no cancelamento, reaper retoma o `PENDING`, reaper sem presas não chama nada, métrica com prefixo `reaper_` |
| `internal/worker` (runner) | 6 | sobe todos os laços, reinicia o que morreu, readiness DOWN com laço morto, readiness UP sem laços, espera o trabalho em voo, grace da config |
| `internal/config` | 4 | quarentena ≥ teto do backoff, defaults válidos com workers, defaults válidos com **tudo** ligado, StopTimeout curto recusado |
| `internal/app` | 1 | grafo do Fx válido com os workers ligados |
| `test/integration` | 8 | ver §4 |

## 3. O caminho completo, pelo BINÁRIO REAL

Subida com os três laços ligados (`SQS_CONSUME_TRANSACTIONS=true`,
`OUTBOX_ENABLED=true`, `REFERENCE_ENABLED=true`, `APP_ENV=production` ⇒ log JSON):

```
{"level":"INFO","msg":"workers_iniciados_em_background","lacos":["outbox-publisher","reference-worker","pending-reaper"],"grace":10000000000}
{"level":"INFO","msg":"application_started","addr":":18098","stopTimeout":50000000000}
{"level":"INFO","msg":"pending_reaper_iniciado","interval":5000000000,"pendingMaxAge":120000000000}
{"level":"INFO","msg":"outbox_publisher_iniciado","pollInterval":500000000,"batchSize":50,"lease":30000000000,"maxAttempts":10}
{"level":"INFO","msg":"reference_worker_iniciado","pollInterval":500000000,"batchSize":25,"maxAttempts":20,"pendingMaxAge":120000000000}
```

**Recuperação de um evento pendente de EXECUÇÃO ANTERIOR** (o banco de
desenvolvimento tinha um `outbox_events` com `published_at IS NULL` de um teste
manual antigo): assim que o publisher subiu, ele o publicou.

```
{"level":"INFO","msg":"outbox_evento_publicado","eventId":"5555...","eventType":"WalletBalanceChanged","attempt":1,"atraso":42837318047800}
```

**Carteira aberta pela API** (`POST /wallets` com token real do Keycloak,
`X-Correlation-Id: prova-fase9-001`):

```
{"id":"01a1479a-ebc5-71d8-8d4c-288c88536fce","playerId":"9633991b-...","balance":{"amount":"1000.00","currency":"BRL"},"version":1}
```

**Os dois eventos da abertura foram publicados** (mesma transação do commit, e
agora na fila):

```
{"level":"INFO","msg":"outbox_evento_publicado","eventId":"01a1479a-ebca-...","eventType":"WagerTransactionProcessed","aggregateId":"01a1479a-ebc5-...","attempt":1}
{"level":"INFO","msg":"outbox_evento_publicado","eventId":"01a1479a-ebcd-...","eventType":"WalletBalanceChanged","aggregateId":"01a1479a-ebc5-...","attempt":1}
```

**E estão na fila**, lidas de volta com `awslocal sqs receive-message`:

```
MessageGroupId=01a1479a-ebc5-71d8-8d4c-288c88536fce
MessageDeduplicationId=01a1479a-ebca-7bef-a6c3-a166f708f946-t1-1792202698062479500
MessageAttributes: eventType=WagerTransactionProcessed, eventId=01a1479a-ebca-...
Body: {"data":{"kind":"OPENING",...,"walletId":"01a1479a-ebc5-...","balanceAfter":{"amount":"1000.00",...}},
       "eventId":"01a1479a-ebca-...","version":1,"eventType":"WagerTransactionProcessed",
       "occurredAt":"2026-10-17T02:04:57.6691209Z","aggregateId":"01a1479a-ebc5-...",
       "correlationId":"prova-fase9-001"}

MessageAttributes: eventType=WalletBalanceChanged, eventId=01a1479a-ebcd-...
Body: {"data":{"money":{"amount":"1000.00","currency":"BRL"},"walletId":"01a1479a-ebc5-...",
       "direction":"CREDIT","balanceAfter":{...},"balanceBefore":{"amount":"0.00",...},
       "transactionId":"01a1479a-ebc6-...","walletVersion":1},"eventId":"01a1479a-ebcd-...",
       "version":1,"eventType":"WalletBalanceChanged","occurredAt":"...",
       "aggregateId":"01a1479a-ebc5-...","correlationId":"prova-fase9-001"}
```

O `WalletBalanceChanged` tem os **7 campos** do README §11 (`walletId`,
`transactionId`, `direction`, `money`, `balanceBefore`, `balanceAfter`,
`walletVersion`), e o `correlationId` da requisição HTTP **sobreviveu à coluna
`correlation_id`** (migration 000005).

**Operação pela FILA** (BET de 25.00 em `wager-transactions.fifo`, corpo lido de
arquivo dentro do container — a armadilha de JSON do PowerShell):

```
MessageId=54e56997-f385-42f7-a197-0bd4408487a1

{"level":"INFO","msg":"sqs_mensagem_processada","messageId":"54e56997-...","status":"PROCESSED","replay":false,"receiveCount":1,"durationMs":63.9}
{"level":"INFO","msg":"outbox_evento_publicado","eventId":"01a1479b-5bee-...","eventType":"WagerTransactionProcessed","attempt":1,"atraso":455346500}
{"level":"INFO","msg":"outbox_evento_publicado","eventId":"01a1479b-5bef-...","eventType":"WalletBalanceChanged","attempt":1,"atraso":504703300}
```

**T7 pelo binário: REFUND antes da BET.**

```
{"level":"INFO","msg":"sqs_mensagem_processada","messageId":"377c0076-...","status":"PENDING_REFERENCE","receiveCount":1}   ← REFUND chegou primeiro
{"level":"INFO","msg":"sqs_mensagem_processada","messageId":"327fbb55-...","status":"PROCESSED"}                            ← a BET chegou
{"level":"INFO","msg":"reference_resolvida","transactionId":"01a1479b-c192-7b32-...","kind":"REFUND","desfecho":"resolvida","status":"PROCESSED","attempt":2,"durationMs":7.91}
```

Entre o intake do REFUND (23:05:52) e a resolução (23:05:57) passaram **5,4s** —
exatamente o `baseReferenciaPendente` gravado pelo caso de uso. Ninguém dormiu:
foi o `next_resolution_attempt_at` que venceu.

**Estado final da carteira, do ledger e da reconciliação:**

```
GET /health/ready → {"dependencies":{"oidc":"UP","postgres":"UP","sqs":"UP","workers":"UP"},"status":"UP"}

GET /wallets/{id}  → {"balance":{"amount":"975.00","currency":"BRL"},"version":4}

ledger:
  CREDIT 1000.00 [0.00 -> 1000.00]     ← abertura
  DEBIT    25.00 [1000.00 -> 975.00]   ← BET (1ª, pela fila)
  DEBIT    25.00 [975.00 -> 950.00]    ← BET referenciada (T7)
  CREDIT   25.00 [950.00 -> 975.00]    ← REFUND resolvido pelo worker

POST /wallets/{id}/reconciliation → {"storedBalance":"975.00","calculatedBalance":"975.00",
                                     "difference":"0.00","consistent":true,"checkedEntries":4}
```

**Métricas do §12 no `/metrics` do processo real:**

```
jungleg_wager_outbox_publish_total{result="success"} 10
jungleg_wager_outbox_pending_age_seconds 0
jungleg_wager_outbox_publish_attempts_count 5
jungleg_wager_reference_resolution_total{result="resolvida"} 1
jungleg_wager_operations_total{kind="BET",status="PROCESSED"} 1
```

**Os sete eventos do fluxo assíncrono, em ORDEM dentro do grupo (a carteira):**

```
tipo=WagerTransactionProcessed         grupo=01a1479a eventId=01a1479b correlationId=54e56997-f385
tipo=WalletBalanceChanged              grupo=01a1479a eventId=01a1479b correlationId=54e56997-f385
tipo=WagerTransactionPendingReference  grupo=01a1479a eventId=01a1479b correlationId=377c0076-53fb
tipo=WagerTransactionProcessed         grupo=01a1479a eventId=01a1479b correlationId=327fbb55-3c9d
tipo=WalletBalanceChanged              grupo=01a1479a eventId=01a1479b correlationId=327fbb55-3c9d
tipo=WagerTransactionProcessed         grupo=01a1479a eventId=01a1479b correlationId=377c0076-53fb
tipo=WalletBalanceChanged              grupo=01a1479a eventId=01a1479b correlationId=377c0076-53fb
```

Duas leituras importantes:

1. **A ordem por carteira é a ordem dos fatos**: o `PendingReference` do REFUND
   vem antes do `Processed` da BET referenciada, e o `Processed` do próprio REFUND
   vem por último. Quem entrega nessa ordem é a fila FIFO com
   `MessageGroupId = walletId`.
2. **A correlação sobreviveu à espera**: os dois últimos eventos (emitidos pelo
   worker, **cinco segundos depois** da requisição original) carregam o
   `correlationId` da ENTREGA do REFUND (`377c0076-...`), não um identificador
   inventado na retomada. É o que a migration 000005 comprou.

## 4. Os oito testes de integração (PostgreSQL + LocalStack reais)

```
$ go test -count=1 -tags=integration -timeout 300s -v -run 'TestDoisPublishers|TestQueda|TestREFUNDAntes|TestReferenciaQueNunca|TestReaperRetoma' ./test/integration/
--- PASS: TestDoisPublishersDisputamAMesmaOutbox (1.71s)
--- PASS: TestQuedaEntreCommitEPublicacaoOutraInstanciaAssume (2.84s)
--- PASS: TestQuedaEntrePublicacaoEMarkPublishedRepublicaComOMesmoEvento (4.03s)
--- PASS: TestREFUNDAntesDaReferenciaResolveDepois (0.13s)
--- PASS: TestReferenciaQueNuncaChegaExpiraEmREFERENCENOTFOUND (0.12s)
--- PASS: TestReaperRetomaPendingAntigo (0.11s)
--- PASS: TestOutboxPublicaNaFilaDeEventos
--- PASS: TestOutboxRepublicacaoPreservaOEvento
```

| Teste | O que prova |
| --- | --- |
| `TestOutboxPublicaNaFilaDeEventos` | publica de verdade e **lê de volta**: corpo byte a byte igual ao snapshot da outbox, `MessageGroupId` = carteira, atributos `eventType`/`eventId`, e a outbox sem pendentes |
| `TestDoisPublishersDisputamAMesmaOutbox` | T6: 4 carteiras, batch 2, dois publishers em rodadas concorrentes → 8 eventos, 8 mensagens, nenhum `eventId` repetido, ordem por carteira preservada |
| `TestQuedaEntreCommitEPublicacaoOutraInstanciaAssume` | queda **entre commit e publicação**: a instância A reivindica (lease) e morre; B não pega nada enquanto o lease vale e publica os 2 eventos quando ele vence |
| `TestQuedaEntrePublicacaoEMarkPublishedRepublicaComOMesmoEvento` | queda **entre publicação e `MarkPublished`**: 2 mensagens na fila, evento ainda pendente, lease vence, B republica → 4 mensagens, 2 `eventId`, cada um **duas vezes** e com o **corpo idêntico** |
| `TestREFUNDAntesDaReferenciaResolveDepois` | T7 (1ª metade): REFUND antes da BET → `PENDING_REFERENCE` sem movimentar; a BET chega; o worker resolve → `PROCESSED`, saldo 100.00, ledger `CREDIT 100 / DEBIT 25 / CREDIT 25`, `result_balance` 10000 |
| `TestReferenciaQueNuncaChegaExpiraEmREFERENCENOTFOUND` | T7 (2ª metade): TTL estourado → `REJECTED`/`REFERENCE_NOT_FOUND`, saldo intacto, evento de rejeição com o código no payload |
| `TestReaperRetomaPendingAntigo` | linha presa em `PENDING` (inserção direta) retomada → `PENDING_REFERENCE` **reagendada**, em vez de presa para sempre |
| `TestOutboxRepublicacaoPreservaOEvento` | a republicação (via `published_at = NULL`) gera **outra** mensagem no broker (MessageId distinto) com o **mesmo** `eventId` e o mesmo corpo |

## 5. Os defeitos REAIS encontrados nesta fase

Nenhum destes apareceu em teste unitário: os dois primeiros só apareceram
**rodando a retomada contra o PostgreSQL de verdade**, o que é o mesmo padrão da
Fase 8 (três defeitos que só apareceram no binário).

### 5.1. Defeito 37 — a guarda da "janela do lease" ignorava TODO trabalho

**Sintoma:** `TestREFUNDAntesDaReferenciaResolveDepois` falhou com o REFUND
continuando `PENDING_REFERENCE` **depois** da retomada, e **nenhum erro no log** —
o worker dizia "pendente" (um desfecho de sucesso) e seguia em frente.

**Causa de fundo:** o `Retomar` se recusava a agir quando
`next_resolution_attempt_at` estava no futuro — mas é exatamente esse campo que o
`ClaimPendingReferencesDue` **acaba de empurrar** para o futuro, porque ele é o
LEASE do claim. Ou seja: a guarda comparava o relógio com o lease que ela mesma
tinha aplicado, e a conclusão era sempre "outra instância está com a linha". O
worker reivindicava e descartava, para sempre.

**Correção:** a guarda passou a comparar o que o **claim leu** — o estado e a
`reference_resolution_attempt` —, campos que o claim **não** altera:

```go
if req.EstadoEsperado != "" && tx.Status() != req.EstadoEsperado { ignorada }
if tx.ReferenceResolutionAttempt() != req.TentativaEsperada { ignorada }
```

O efeito colateral bom: a guarda ficou mais forte. Se o lease vencer no meio do
processamento e outro worker reivindicar a mesma linha, o segundo vê a tentativa
já incrementada e **não consome uma segunda tentativa do TTL**.

### 5.2. Defeito 38 — o reaper retomava um `PENDING` e não saía do lugar

**Sintoma:** `TestReaperRetomaPendingAntigo` falhou com
`INVALID_TRANSITION: só é possível reagendar uma referência em PENDING_REFERENCE,
estado atual PENDING`, e a linha continuava em `PENDING`.

**Causa de fundo:** o caminho "continua pendente" chamava
`ReagendarReferencia`, que exige `PENDING_REFERENCE`. Uma linha presa em `PENDING`
(caso do reaper) nunca satisfaz essa condição — e o reaper existe justamente para
ela.

**Correção:** o desfecho pendente agora escolhe a operação pelo estado de
partida: `MarkPendingReference` (transição de verdade, de `PENDING` para
`PENDING_REFERENCE`) ou `ReagendarReferencia` (mesmo estado, relógio andando).

### 5.3. Defeito 39 — `correlationId` morria no fim da requisição

**Sintoma:** nenhum evento da retomada conseguiria carregar a correlação da
requisição original; o envelope sairia com `correlationId` vazio. E o caminho SQS
**nunca** preenchia o campo (desde a Fase 8).

**Causa de fundo:** o `correlationId` era usado só no caminho da resposta. Como a
operação que fica pendente é retomada **muito depois** — por outro processo, que
não tem essa informação —, sem uma coluna o dado simplesmente não existia mais.

**Correção:** migration `000005` (`wager_transactions.correlation_id`), gravada no
`INSERT` e lida na reidratação; a retomada usa a correlação gravada (ou o
`transactionId` quando não houver); e o caminho SQS passou a preencher
`CorrelationID` com o `MessageId` da entrega (o envelope do §10 não tem campo de
correlação, e o produtor não tem outro identificador de rastreio a oferecer).

### 5.4. Achados de processo e de régua

| # | Achado | Correção |
| --- | --- | --- |
| — | **`Set-Content` no PowerShell 5.1 corrompeu os acentos** de um arquivo de teste durante o Bloco 9a (`nÃ£o` no lugar de `não`). É a armadilha do `PROXIMO_PASSO` §4.4, e ela foi pisada mesmo com o aviso escrito | arquivo reescrito com a ferramenta de edição; a regra voltou para a lista de lições |
| — | **Régua errada** no teste de ordem do publicador: exigia a lista EXATA de chamadas, mas o ciclo também mede as estatísticas (`pendingStats`) no fim | a asserção passou a verificar a SEQUÊNCIA `claim → publicar → markPublished` |
| — | **Régua errada** no TTL da retomada: o teste usava `MaxAttempts=3` esperando expirar na primeira re-tentativa. A tentativa do **intake** já conta (`MarkPendingReference(1, ...)`), então quem expira é o teto de 1 | teste corrigido, com o motivo escrito nele |
| — | **Régua errada** no readiness: o teste esperava "erro != nil" para provar o laço morto, mas o runner começa com todos os laços em "ainda não iniciado" — DOWN é o estado inicial | a condição passou a esperar a mensagem de FALHA do laço |
| — | **`docker-compose.yml` sem `stop_grace_period`**: o `docker stop` manda SIGKILL em 10s, enquanto a aplicação orça 50s — o encerramento gracioso seria abortado no ambiente entregue | `stop_grace_period: 60s` no serviço `api` |
| — | **Comentário certo pelo motivo errado** no `workerModule`: dizia que "o módulo httpapi vem primeiro em `ModuleOptions`"; é o contrário (ele é o último e, pela ordem inversa dos `OnStop`, para primeiro) | comentário corrigido |
| — | O `Runner` precisou de `Name()` (inglês) e não `Nome()`: o contrato do readiness é o `httpapi.HealthChecker` | método renomeado, com o motivo documentado |

## 6. Uma limitação encontrada no binário (não é defeito de código)

O banco de **desenvolvimento** tem dados de teste manuais das fases anteriores.
Um deles é uma transação `PENDING` (id `22222222-...`) cuja carteira **já tem** o
lançamento daquele `transaction_id` — resíduo de um teste manual da Fase 5. O
reaper a encontrou e tentou retomá-la:

```
{"level":"WARN","msg":"pending_reaper_retomando","transactionId":"22222222-...","idade":42842306331600}
{"level":"ERROR","msg":"pending_reaper_retomada_falhou","transactionId":"22222222-...",
 "error":"conflito: duplicate key value violates unique constraint \"ledger_wallet_transaction_key\""}
```

Duas leituras, as duas importantes:

1. **A invariante financeira segurou**: o índice único do ledger impediu o
   movimento duplicado. É o README §5.3 ("as invariantes devem ser garantidas no
   banco") funcionando contra um dado inconsistente que o próprio sistema não
   produz.
2. **O reaper não desiste**: ele repete a varredura a cada `REFERENCE_REAPER_INTERVAL`
   e a linha nunca chega a `FAILED`, porque a falha é de **conflito** (classificada
   como transitória) e não há contador durável de tentativas para linhas em
   `PENDING`. Está registrado no `ARCHITECTURE.md` §16 como limitação: o desfecho
   `FAILED` hoje só é dado quando a carteira não existe. Um contador de falhas do
   reaper (ou um `attempts` próprio) é trabalho da Fase 10/11.

## 7. Decisões de design registradas

1. **Publicar ANTES de marcar.** A ordem inversa perderia o evento numa queda
   entre as duas etapas. Nesta ordem a pior falha é a republicação, com o MESMO
   `eventId`, e quem deduplica é o consumidor.
2. **O lote é reivindicado em transação curta e a publicação acontece FORA dela.**
   Segurar as linhas (`FOR UPDATE`) e uma conexão do pool durante a ida de rede
   esgotaria o pool; além disso o lease precisa estar **confirmado** para o evento
   voltar sozinho.
3. **`MessageDeduplicationId` por TENTATIVA, e não por `eventId`.** Se fosse o
   `eventId`, o SQS engoliria a republicação dentro de 5 minutos e o sistema
   dependeria da deduplicação do broker — que **expira**. Com o nonce por
   tentativa, a republicação acontece de verdade e o consumidor deduplica por um
   registro durável.
4. **Quarentena em vez de descarte.** Esgotado o teto (ou recusa permanente do
   destino), o evento continua pendente, com `last_error` e `available_at` no
   prazo longo (`OUTBOX_PARKED_BACKOFF`). Nada é perdido e o atraso da outbox
   denuncia o evento parado.
5. **`MarkPublished` com `context.WithoutCancel`**: a mensagem já está na fila;
   perder a confirmação por causa do SIGTERM custaria republicação.
6. **`ReagendarReferencia` no lugar de um auto-laço na máquina de estados.** A
   tentativa e o instante mudam; o estado, não.
7. **As guardas de concorrência da retomada são duas** (lock da linha + estado/
   tentativa esperados). O lock sozinho não basta: o segundo worker encontra a
   linha ainda em `PENDING_REFERENCE` e consumiria outra tentativa.
8. **O lock da carteira é o último**, e só quando há movimento: uma retomada que
   apenas reagenda não bloqueia carteira.
9. **TTL só decide quando a referência NÃO resolve.** Se ela chegou, a operação
   conclui mesmo depois do prazo — rejeitar uma reversão correta para respeitar um
   relógio seria destruir informação.
10. **O reaper usa o MESMO `Retomar`**, sem caminho próprio, e **não** aplica TTL
    por idade (a linha está em `PENDING`; o relógio da referência só começa quando
    ela vira `PENDING_REFERENCE`).
11. **`FAILED` não emite `WagerTransactionRejected`**: a tabela do README §11
    define esse evento como rejeição de **negócio**, e dado inconsistente não é
    decisão de negócio.
12. **Um prazo de encerramento para o CONJUNTO** (`WORKERS_SHUTDOWN_GRACE`), e não
    um por laço: os laços param em paralelo. Um por laço exigiria a soma na
    validação e o ambiente de exemplo não subiria (defeito 33 com três parcelas).
13. **A decisão de quais laços ligar fica na composição**, não no runner: o runner
    continua testável com laços falsos.
14. **Readiness olha o runner**: `UP` com o publicador morto seria falso
    exatamente enquanto a outbox não publica.

## 8. Interpretações adotadas (o §15 pede que sejam explícitas)

1. **"Tentativas" da referência incluem a do intake.** `MarkPendingReference(1,...)`
   já conta uma tentativa, então `REFERENCE_MAX_ATTEMPTS=1` significa "só a do
   intake" e a primeira retomada expira. O default de 20 e o
   `REFERENCE_PENDING_MAX_AGE` de 2m tornam o TTL por idade o que decide na
   prática.
2. **A idade da pendência é medida da CRIAÇÃO da operação.** Para o caminho normal
   (o REFUND entra e fica pendente no mesmo commit) a diferença é nula; para a
   linha retomada pelo reaper, é conservador (conta o tempo em que ela esteve
   presa em `PENDING`).
3. **O reaper não rejeita por idade.** Ele não aplica TTL porque a linha não está
   esperando referência ainda; quem aplica é o worker, depois da transição.
4. **Evento de operação `FAILED` não existe no contrato.** O estado fica durável e
   consultável; inventar um `WagerTransactionFailed` que o README não prevê seria
   pior do que a ausência.
5. **O corpo publicado é o snapshot JSONB, não uma reconstrução.** O JSONB
   normaliza a ordem das chaves e os espaços — e é isso que sai, byte a byte, em
   toda republicação. Reconstruir publicaria "a interpretação de hoje" sobre o
   passado.
6. **Repetição é possível por desenho.** A entrega é "pelo menos uma vez": o
   consumidor deduplica por `eventId` (registro durável, sem janela de 5 minutos).
   O contrato de consumo está no `ARCHITECTURE.md` §11.
7. **`MessageGroupId` = `aggregateId` = carteira** também para os eventos, e não
   por tipo de evento: a ordem que importa é a dos fatos **daquela carteira**.

## 9. Limitações conhecidas da fase

- **O `PENDING` do reaper é inalcançável por caminho normal** (o caso de uso grava
  `PENDING` e o estado definitivo no mesmo commit). Ele é a segurança extra do
  README §6.3, provada por inserção direta; o dia em que houver aceite assíncrono,
  ele já está lá.
- **O reaper repete indefinidamente uma linha que não consegue concluir**, sem
  transição para `FAILED` (ver §6): falta um contador durável de falhas do reaper.
- **Ordem por carteira com múltiplos publishers é garantida enquanto o lote couber
  no `OUTBOX_BATCH_SIZE`.** Com 50 eventos pendentes da MESMA carteira em uma
  rodada, o lote pode ser dividido entre dois publishers e a ordem de ENVIO fica
  sujeita ao escalonamento do broker. O consumidor pode (e deve) ordenar por
  `walletVersion` do `WalletBalanceChanged`.
- **O encerramento gracioso (SIGTERM) não foi provado no Windows**: o
  `Stop-Process` é terminação dura. A prova de SIGTERM é do ambiente Linux/container
  (pendência P8, herdada da Fase 7).
- **A URL da fila de eventos é derivada do endpoint** (LocalStack). Sem
  `SQS_ENDPOINT`, o que sobra é o NOME — a resolução por `GetQueueUrl` não está
  exercitada (mesma limitação da Fase 8, `ARCHITECTURE.md` §16).
- **Sem `SendMessageBatch`**: cada evento é uma chamada. O lote existe no claim,
  não no envio. É o que mantém o `MarkPublished` por evento e a recuperação
  observável.
- **`correlation_id` é nullable** e linhas anteriores à migration 000005 ficam
  nulas; a retomada cai para o `transactionId` nesse caso.

## 10. Rotina de execução da fase

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'; $env:GOCACHE='E:\jungleg\.gocache\build'; $env:GOPATH='E:\jungleg\.gocache\gopath'

docker compose up -d postgres keycloak localstack
docker ps --format '{{.Names}}\t{{.Status}}'
docker exec jungleg-localstack awslocal sqs list-queues     # as QUATRO filas

# migrations: o binário carrega as EMBUTIDAS — recompilar depois de criar/alterar
go build -o bin\migrate.exe .\cmd\migrate\
$env:DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungleg?sslmode=disable'
.\bin\migrate.exe up

go build ./... ; go vet ./... ; go vet -tags=integration ./...
gofmt -l .\internal\ .\cmd\ .\migrations\ .\test\
go test -count=1 -race ./...

$env:TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
$env:OIDC_ISSUER='http://localhost:8080/realms/jungleg'
$env:OIDC_DISCOVERY_URL='http://localhost:8080/realms/jungleg'
$env:OIDC_AUDIENCE='jungleg-api'
$env:SQS_ENDPOINT='http://localhost:4566'
go test -count=1 -race -tags=integration -timeout 400s ./...

# só a publicação e os laços desta fase
go test -count=1 -tags=integration -timeout 300s -v -run 'TestOutbox|TestDoisPublishers|TestQueda|TestREFUND|TestReferencia|TestReaper' ./test/integration/

# prova pelo binário (a que está no §3)
go build -o bin\api.exe .\cmd\api\
$env:HTTP_ADDR=':18098'; $env:APP_ENV='production'
$env:SQS_CONSUME_TRANSACTIONS='true'; $env:OUTBOX_ENABLED='true'; $env:REFERENCE_ENABLED='true'
.\bin\api.exe
```

> ⚠️ **Armadilha de PowerShell (de novo):** montar o JSON do `send-message` dentro
> do `docker exec` corrompe o corpo (e o PowerShell ainda quebra o `sh -c` com as
> aspas). O envio do §3 foi feito com o corpo em **arquivo** (`docker cp` + script
> com `BODY=$(cat /tmp/bet.json)`). E `Set-Content` com acento corrompe o arquivo:
> use a ferramenta de edição ou `[System.IO.File]::WriteAllText` com
> `UTF8Encoding($false)`.
