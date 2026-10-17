# EVIDÊNCIAS — Fase 8 (consumidor SQS + inbox)

> Saídas reais dos comandos, contra **PostgreSQL 16** (`jungleg-postgres`),
> **LocalStack 4.0.3** (`jungleg-localstack`) e **Keycloak**
> (`jungleg-keycloak`) do `docker-compose`, no banco `jungle_test`.
>
> **Estado da fase: CONCLUÍDA.** Dois blocos commitados:
>
> | Commit | Bloco |
> | --- | --- |
> | `c81e7de` | **8a/8b** — ganchos transacionais, `app/inbox`, `internal/consumer`, `internal/infra/sqs`, migration 000004 |
> | `eaf52a0` | **8e** — integração com LocalStack real + correção dos dois defeitos de identidade |

## Ambiente

| Item | Versão/valor |
| --- | --- |
| LocalStack | `localstack/localstack:4.0.3` (não pode ser `latest`: é Pro e morre com "License activation failed") |
| Filas | `wager-transactions.fifo` + `wager-transactions-dlq.fifo`, com `RedrivePolicy` (`maxReceiveCount=5`) |
| Schema | versão **4** (`000004_inbox_attempts`) |
| AWS SDK | `aws-sdk-go-v2/service/sqs v1.52.4` (primeira dependência nova desde a Fase 0) |

## 1. Build, vet e formatação

```
$ go build ./...
(vazio)

$ go vet ./... ; go vet -tags=integration ./...
(vazio)

$ gofmt -l ./internal/ ./cmd/ ./migrations/ ./test/
(vazio)
```

## 2. Testes

```
$ go test -count=1 -race ./...
ok  	.../internal/app	2.948s
ok  	.../internal/app/inbox	1.402s        ← NOVO
ok  	.../internal/app/ports	1.341s
ok  	.../internal/app/wagering	1.552s
ok  	.../internal/config	1.384s
ok  	.../internal/consumer	2.025s           ← NOVO
ok  	.../internal/domain/ledger	1.369s
ok  	.../internal/domain/money	1.604s
ok  	.../internal/domain/wagering	1.398s
ok  	.../internal/domain/wallet	1.355s
ok  	.../internal/httpapi	1.991s
ok  	.../internal/infra/auth	4.306s
ok  	.../internal/infra/postgres	1.294s
ok  	.../internal/observability	1.507s
ok  	.../internal/platform/canonicaljson	1.255s
ok  	.../internal/platform/idgen	1.267s

$ TEST_DATABASE_URL=... SQS_ENDPOINT=http://localhost:4566 \
    go test -count=1 -race -tags=integration -timeout 300s ./...
ok  	.../internal/app	11.989s
ok  	.../internal/infra/postgres	6.000s
ok  	.../test/integration	46.239s
```

**Casos da fase:** 26 em `internal/app/inbox` (14 funções `Test`) e 19 em
`internal/consumer` (12 funções `Test`), contando subtests — mais **9 funções** de
integração com o LocalStack.

## 3. O caminho completo, contra o LocalStack real

```
--- PASS: TestSQSMensagemProcessaEMovimentaCarteira (4.35s)
        SQS -> inbox -> caso de uso -> PostgreSQL;
        saldo 975.00, DOIS lançamentos (abertura + aposta), inbox PROCESSED,
        mensagem removida da fila
--- PASS: TestSQSReentregaDaMesmaMensagemNaoReaplica (5.26s)
        mesmo identificador do broker, registro já PROCESSED na inbox:
        ZERO débito, mensagem removida
--- PASS: TestSQSMesmaOperacaoDuasVezesNaoDuplicaDebito (5.21s)
        dois MessageId do broker, mesmo conteúdo: UM débito (chave de idempotência)
--- PASS: TestSQSCorpoDivergenteVaiParaDLQ (4.21s)
        mesmo registro, hash de corpo diferente: DLQ + fila principal limpa
--- PASS: TestSQSMensagemInvalidaVaiParaDLQ (4.20s)
        corpo ilegível: DLQ, inbox VAZIA, ledger intacto
--- PASS: TestSQSRejeicaoDeNegocioEhTerminal (5.23s)
        aposta sem saldo: inbox REJECTED/INSUFFICIENT_FUNDS, mensagem removida,
        DLQ limpa
--- PASS: TestSQSESHTTPCompartilhamAIdempotencia (2.21s)
        a MESMA operação por SQS e por HTTP: replay na segunda porta,
        UM débito no banco (hash canônico equivalente)
--- PASS: TestSQSConcorrenteComHTTPNaoDuplicaDebito (3.65s)
        as duas portas AO MESMO TEMPO: um aplica, o outro reconhece
--- PASS: TestSQSReadinessReportaAFila (1.06s)
        readiness UP com a fila acessível, DOWN com fila inexistente,
        e o caminho do Receive através do adaptador
```

A equivalência HTTP × SQS é o teste mais importante da fase para o enunciado: o
README §10 exige que as duas portas compartilhem o caso de uso **e as garantias de
idempotência financeira**. Se as duas montassem a requisição de forma diferente, o
hash canônico divergiria e o débito aconteceria duas vezes — e é isso que o teste
prova que não acontece.

## 4. Defeitos de AMBIENTE encontrados (nenhum era de código)

### 4.1. As filas NUNCA eram criadas: o script não tinha shebang nem bit de execução

`deploy/localstack/init-queues.sh` estava com modo `100644` (não executável) e
**sem linha de shebang**. O LocalStack roda os scripts de `/etc/localstack/init/ready.d`
no estágio READY **executando o arquivo** — e o kernel recusava:

```
$ docker logs jungleg-localstack | grep init-queues
2026-10-15T15:10:25 ERROR ... Error while running script
  Script(path='/etc/localstack/init/ready.d/init-queues.sh', stage=READY, state=ERROR):
  [Errno 8] Exec format error
2026-10-15T22:22:42 ERROR ... (idem)
2026-10-16T13:54:33 ERROR ... (idem)
$ awslocal sqs list-queues
(vazio)
```

O erro estava no log **nas três subidas** e nada quebrava, porque nenhum teste
usava SQS até esta fase — a evidência da Fase 4 dizia "filas provisionadas" com
base no script existir, não em ele ter rodado. Correção: shebang `#!/bin/sh` e
`git update-index --chmod=+x`. Provado reiniciando o container:

```
$ docker restart jungleg-localstack && sleep 20
$ docker logs jungleg-localstack | tail
[init-queues] criando fila: wager-transactions.fifo
[init-queues] configurando wallet-events.fifo
[init-queues] filas provisionadas:
  - .../wager-transactions-dlq.fifo
  - .../wallet-events-dlq.fifo
  - .../wager-transactions.fifo
  - .../wallet-events.fifo
$ awslocal sqs get-queue-attributes --queue-url .../wager-transactions.fifo \
    --attribute-names RedrivePolicy VisibilityTimeout
{"RedrivePolicy":"{\"deadLetterTargetArn\":\"arn:aws:sqs:us-east-1:000000000000:wager-transactions-dlq.fifo\",\"maxReceiveCount\":\"5\"}",
 "VisibilityTimeout":"60"}
```

### 4.2. A inbox não tinha `attempts` (migration 000004)

O PLANO previa "o `attempts` da inbox incrementa", mas a tabela da Fase 4 não
tinha a coluna — e o `ApproximateReceiveCount` do SQS é do **broker**: ele se
perde quando a mensagem é removida ou vai para a DLQ. Sem o contador durável, ao
investigar uma mensagem morta não haveria como saber se ela foi recebida duas ou
vinte vezes.

```
$ docker exec jungleg-postgres psql -U jungleg -d jungleg -c "\d inbox_messages"
 attempts      | integer  | not null | 0
Indexes:
    "inbox_messages_attempts_idx" btree (attempts DESC) WHERE completed_at IS NULL
    "inbox_attempts_non_negative" CHECK (attempts >= 0)
```

O incremento é `attempts = attempts + 1` **em SQL** (mesma decisão do `MarkRetry`
da outbox): com o número vindo do processo, duas instâncias recebendo a mesma
mensagem gravariam o mesmo valor final em vez de somar as duas entregas.

## 5. Dois defeitos REAIS de identidade da mensagem

### 5.1. A identidade durável não pode ser o `messageId` do envelope

**Sintoma:** com o teste preenchendo o `messageId` do envelope (como o corpo de
exemplo do README §10 faz) e conferindo-o contra o identificador da entrega, o
consumo recusou **toda** mensagem:

```
level=INFO msg=sqs_mensagem_classificado status="" 
  erro="inbox: mensagem inválida: messageId do envelope (\"msg-fluxo-1\")
        difere do id da entrega (\"76075ee0-8d1a-4ee6-8e42-d5786ead0cdc\")"
```

**Causa de fundo:** o `MessageId` é atribuído pelo **broker** no `SendMessage` e
devolvido na resposta — o produtor **não tem como conhecê-lo antes de enviar**.
Exigir que o envelope carregue a identidade obrigaria o produtor a inventar um
segundo identificador para a mesma mensagem, e aí existiriam dois, que podem
divergir.

**Decisão:** a identidade durável do consumo é o `MessageId` **do broker**, e o
campo do envelope (`messageId`), quando presente, é **conferido** contra ele —
divergência é mensagem inválida (permanente → DLQ). O campo é opcional. Está
registrado no `ARCHITECTURE.md` §11 e é uma **interpretação** da linha do README
§10 ("use o `messageId` do envelope como identidade durável da mensagem para o
consumidor"): o broker é quem entrega a mensagem **com** esse identificador, e é
ele que sobrevive à reentrega.

### 5.2. O `return` que abortava o parsing (defeito introduzido na correção acima)

**Sintoma:** depois da correção 5.1, o consumo passou a falhar com
`IDEMPOTENCY_KEY_REQUIRED` para um corpo que **tinha** a chave:

```
level=INFO msg=sqs_resultado processada=false status=""
  erro="inbox: operação recusada por entrada inválida (IDEMPOTENCY_KEY_REQUIRED)"
```

E o corpo enviado (log do teste) tinha o campo:

```
{"data":{"externalTransactionId":"txn-sqs-1",...,"idempotencyKey":"provider-a:txn-sqs-1",...},
 "type":"WagerTransactionRequested"}
```

**Causa:** a correção 5.1 devolveu `return operacao{}, nil` no caminho do envelope
sem `messageId` — **abortando o parsing inteiro** e devolvendo uma requisição
VAZIA com erro `nil`. O caso de uso então recusava a operação por falta de chave.

**Como foi localizado:** um teste de unidade que roda o **corpo exato** que o
broker entregou (`internal/app/inbox/payload_real_test.go`):

```
$ go test -v -run TestDecodificarCorpoRealDoBroker ./internal/app/inbox/
    payload_real_test.go:23: IdempotencyKey = "", quer "provider-a:txn-sqs-1"
    payload_real_test.go:26: Amount = 0, quer 2500
    payload_real_test.go:29: Kind = "", quer BET
```

**A lição da fase:** o teste de integração disse **que** algo estava errado; o
teste de unidade com o corpo real disse **onde**. Sem ele, a depuração teria
continuado no lugar errado (fila, grupo, visibilidade) — e foi exatamente o que
aconteceu por várias rodadas.

## 6. Defeitos de TESTE corrigidos no caminho (a régua, não o código)

| Sintoma | O que era, de verdade | Correção |
| --- | --- | --- |
| `Receive` devolvia **zero** com mensagem parada na fila | `MessageGroupId` **fixo**: numa fila FIFO, mensagem em voo **bloqueia** as seguintes do mesmo grupo | um grupo por mensagem |
| idem, em execuções repetidas | `MessageDeduplicationId` repetido entre execuções: o SQS deduplica por 5 minutos e devolvia o `MessageId` da mensagem **original**, já consumida | nonce no dedup id |
| idem, depois de um teste que deixou mensagem em voo | visibilidade de 60s (a da fila do compose) | **no ambiente de TESTE**, 2s — a fila do compose continua com 60s |
| "lançamentos = 1" onde o teste esperava 2 | carteira criada por `INSERT` direto: **sem** `OpenWallet` não existe lançamento de abertura | os testes de SQS passaram a abrir a carteira pela regra de negócio (a régua do README §9: abertura 1000.00 + BET 25.00 → `checkedEntries: 2`) |
| "a condição não foi atendida" sem mais nada | o log do consumidor ia para `io.Discard` | o log vai para um buffer e é **despejado na falha** — foi o que mostrou "mensagens=0" em laço e o erro exato de classificação |

## 6b. Três defeitos que SÓ apareceram ao rodar o binário

O teste de integração exercita o consumidor embutido no processo de teste. Subir o
**binário** com `SQS_CONSUME_TRANSACTIONS=true` e enviar uma mensagem pela fila
revelou três coisas que nenhum teste via.

### 6b.1. Com os DEFAULTS, ligar o consumidor faz a aplicação NÃO SUBIR

```
$ ./bin/api.exe   # com SQS_CONSUME_TRANSACTIONS=true e defaults
erro fatal: app: config: configuração inválida:
  - HTTP_STOP_TIMEOUT deve cobrir HTTP_SHUTDOWN_TIMEOUT + SQS_SHUTDOWN_GRACE quando
    SQS_CONSUME_TRANSACTIONS está ligado
```

O `HTTP_STOP_TIMEOUT` padrão era **25s**, e a regra exige cobrir 20s (servidor) +
15s (consumidor) = 35s. Ou seja: **o default violava a própria regra de validação**,
e o operador que só ligasse o worker recebia "configuração inválida" num ambiente
correto. Corrigido: default de **40s** (e o `.env.example` alinhado), com dois
testes de config travando os dois lados da regra.

### 6b.2. A métrica de negócio não era emitida no caminho SQS

O consumidor não chamava `ObserveOperation`, então `wager_operations_total` **não
existia** para operações vindas da fila — o caminho principal do serviço ficava
invisível no painel (a operação aparecia no banco e em lugar nenhum mais).
Corrigido: o consumidor emite a mesma métrica da borda HTTP, com `kind` extraído do
corpo de forma TOLERANTE (`unknown` quando não é o envelope esperado — a validação
de verdade é do processador; aqui o objetivo é não perder a contagem).

```
$ curl http://127.0.0.1:18099/metrics | grep operations_total
jungleg_wager_operations_total{kind="BET",status="PROCESSED"} 1
jungleg_wager_operation_duration_seconds_count{kind="BET",status="PROCESSED"} 1
```

### 6b.3. O contador durável de entregas nunca incrementava

`attempts` ficava em 0 para sempre: o caminho da reentrega (a mensagem que a inbox
já tem) **não existia** para o contador — e é exatamente nele que a contagem importa,
porque é o único registro que sobrevive quando o broker não é mais consultado.
Corrigido em duas partes: a inserção grava `attempts = 1` (uma linha na inbox
significa PELO MENOS uma entrega, normalizado **no repositório**) e a reentrega faz
`attempts = attempts + 1` no SQL. O teste que discrimina: `TestProcessarReentregaNaoReaplica`
afirma `attempts = 2` depois de duas entregas.

> O mesmo defeito apareceu **de novo**, e a correção o expôs: o `INSERT` ganhou a
> coluna `attempts` sem os placeholders correspondentes (`mismatched param and
> argument count`), pego pelos testes de integração do repositório
> (`TestInboxDeduplicacao` e `TestInboxMarcarConclusao`). É o valor de ter a suíte
> inteira no mesmo comando: a mudança de uma camada quebra a asserção de outra.

## 6c. A prova ponta a ponta pelo BINÁRIO (wallet por HTTP, operação pela FILA)

```
1) carteira aberta pela API: 01a14761-2f6d-7b52-b029-cb6255753915 saldo=1000.00 versao=1
2) mensagem enviada para a fila (MessageId 50e216b8-...)
3) saldo depois do consumo: 97500 (esperado 97500)
4) reconciliacao: 975.00/975.00 dif=0.00 consistent=True entries=2
5) jungleg_wager_operations_total{kind="BET",status="PROCESSED"} 1
6) log: msg=sqs_mensagem_processada messageId=50e216b8-... status=PROCESSED
        failureCode="" replay=false receiveCount=1 durationMs=24.7
```

Leitura: a carteira nasceu pelo caminho de negócio (abertura + lançamento), a
operação chegou **pela fila** (não por HTTP), o consumidor aplicou, o ledger ficou
com DOIS lançamentos (abertura 1000.00 e débito 25.00), a inbox registrou
`PROCESSED`, a reconciliação fechou e a métrica de negócio foi emitida.

> ⚠️ **Armadilha de PowerShell registrada:** montar JSON com aspas dentro de
> `docker exec ... awslocal sqs send-message` **corrompe o corpo** (o primeiro envio
> chegou como `invalid character ''t'' ...` e foi para a DLQ, corretamente
> classificada como permanente). O envio tem de ser feito **lendo de um arquivo**
> copiado para dentro do container: `docker cp` + `BODY=$(cat arquivo)`.

## 7. Decisões de design registradas

1. **Ganchos transacionais** (`ProcessHooks`): `OnBegin` antes de qualquer leitura
   de domínio (grava a inbox), `OnCommit` depois do domínio e antes do commit. Um
   sentinela (`ErrIdempotentSkip`) encerra a transação sem erro e devolve replay.
   É o que permite o registro da inbox entrar na **mesma transação** do saldo,
   ledger e eventos (README §6.5) sem o caso de uso conhecer SQS.
2. **`Process(ctx, req)` é `ProcessWithHooks(ctx, req, nil)`**: o HTTP não paga
   nada pelo recurso existir.
3. **Mensagem inválida é recusada ANTES de abrir transação**: não suja a inbox com
   o que jamais será aplicado.
4. **Corpo divergente é permanente**: não é reentrega (o SQS garante "pelo menos
   uma vez", não "conteúdo diferente").
5. **Rejeição de negócio é terminal**: inbox `REJECTED`, mensagem removida.
6. **DLQ explícita** (`Send` + `Delete`) além do redrive: a mensagem envenenada
   não gira até o `maxReceiveCount` — o que numa fila FIFO **bloqueia o grupo
   inteiro** enquanto gira. O redrive continua como rede de segurança.
7. **Erro desconhecido vai para retry**, não para a DLQ: a DLQ é o fim da linha, e
   mandar para lá algo que talvez funcionasse perde trabalho. O `maxReceiveCount` é
   o teto.
8. **`context.WithoutCancel` no processamento**: se usasse o contexto do laço, o
   SIGTERM abortaria a transação em andamento — o oposto do que o encerramento
   quer.
9. **Encerramento**: para de buscar, aguarda o trabalho em voo (`SQS_SHUTDOWN_GRACE`)
   e, no que sobrar, **libera a visibilidade** (`ChangeMessageVisibility 0`) para
   reentrega imediata. O `sync.Once` por entrega impede que a mesma mensagem seja
   apagada E devolvida.
10. **Backoff exponencial com teto e jitter** aplicado pelo
    `ChangeMessageVisibility`. O jitter é **subtraído dentro do teto**: somá-lo
    depois do limite estourava o `MaxBackoff` (o teste de teto pegou).
11. **`attempts` incrementado no SQL**, não a partir do valor lido.
12. **SQS no readiness** (fecha a P5): o readiness consulta a **fila específica**
    (`GetQueueAttributes`), não "o SQS responde".

## 8. Interpretações adotadas (o §15 pede que sejam explícitas)

1. **Identidade durável da mensagem = `MessageId` do broker.** O README §10 pede o
   `messageId` "do envelope", mas quem atribui esse valor é o broker, no envio. O
   campo do envelope é **opcional** e, quando vem, é conferido contra o da entrega
   (divergência ⇒ mensagem inválida).
2. **DLQ explícita além do redrive** (o README pede "erros permanentes ou
   tentativas esgotadas devem chegar à DLQ": as duas vias chegam lá).
3. **Erro desconhecido é retentável.** O README não define a política para o erro
   que não se sabe classificar; a escolha conservadora é tentar de novo, com o
   `maxReceiveCount` como teto.
4. **A chave de idempotência financeira vem do corpo** (`data.idempotencyKey`), e
   **não** do `MessageDeduplicationId` — que é deduplicação de entrega, não de
   negócio.

## 9. Limitações conhecidas da fase

- **A URL da fila é derivada do endpoint** (`<endpoint>/000000000000/<nome>`) para
  o LocalStack. Em produção (sem `SQS_ENDPOINT`), o valor que sobra é o NOME, e a
  resolução teria de vir do `GetUrl` do SDK — não exercitado, e registrado no
  `ARCHITECTURE.md` §16.
- **Sem `MessageGroupId` por carteira no teste**: cada mensagem de teste usa o seu
  grupo, o que elimina a interferência entre testes mas **não** exercita a ordem
  dentro do grupo. A ordem é garantida pela fila FIFO e pelo `aggregateId =
  walletId` dos eventos (Fase 6d); a prova de ordem por carteira é da Fase 10.
- **Sem DLQ para `wallet-events.fifo`**: a fila de eventos é do publisher (Fase 9).
- **`MaxReceiveCount` do broker continua sendo o teto** para a falha transitória
  que insiste; a DLQ explícita cobre o permanente.

## 10. Rotina de execução da fase

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'; $env:GOCACHE='E:\jungleg\.gocache\build'; $env:GOPATH='E:\jungleg\.gocache\gopath'

docker compose up -d postgres keycloak localstack
docker ps --format '{{.Names}}\t{{.Status}}'

# ⚠️ as filas precisam EXISTIR. Se `list-queues` estiver vazio, o script de init
# falhou: veja o log do LocalStack (era "Exec format error" por falta de shebang)
docker exec jungleg-localstack awslocal sqs list-queues

$env:TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
go build -o bin\migrate.exe .\cmd\migrate\    # migrations EMBUTIDAS: recompilar
$env:DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungleg?sslmode=disable'
.\bin\migrate.exe up

go build ./... ; go vet ./... ; go vet -tags=integration ./...
gofmt -l .\internal\ .\cmd\ .\migrations\ .\test\
go test -count=1 -race ./...

$env:OIDC_ISSUER='http://localhost:8080/realms/jungleg'
$env:OIDC_DISCOVERY_URL='http://localhost:8080/realms/jungleg'
$env:OIDC_AUDIENCE='jungleg-api'
$env:SQS_ENDPOINT='http://localhost:4566'
go test -count=1 -race -tags=integration -timeout 300s ./...

# só o consumo (o que a fase entrega)
go test -count=1 -tags=integration -timeout 300s -v -run TestSQS ./test/integration/
```
