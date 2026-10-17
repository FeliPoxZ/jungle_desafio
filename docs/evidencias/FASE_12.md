# EVIDÊNCIAS — Fase 12 (documentação, os dois documentos finais e entrega)

> **Escopo:** o `README.md` §15 (entrega) e §14 (critérios e eliminatórios), com o
> §9 (contratos HTTP) e o §2 (autenticação) como réguas. Esta fase **não** mudou
> comportamento financeiro por iniciativa própria: o código só mudou onde a entrega
> revelou defeito (46 a 50), e cada correção veio com teste.

---

## 1. Ambiente

| Item | Valor |
| --- | --- |
| SO | Windows 11, PowerShell 5.1 |
| Go | `go1.26.5 windows/amd64` |
| Containers | `jungleg-postgres`, `jungleg-keycloak`, `jungleg-localstack` (healthy) + `jungleg-migrate` (job) e `jungleg-api` |
| Banco de teste | `jungle_test` (a suíte trunca) |
| Esquema | versão **5**, aplicada e revertida no ensaio da §5 |
| Prometheus (validação das regras) | `prom/prometheus:v2.54.1` (`promtool`) |

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'
$env:GOCACHE='E:\jungleg\.gocache\build'
$env:GOPATH='E:\jungleg\.gocache\gopath'
$env:TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
$env:OIDC_ISSUER='http://localhost:8080/realms/jungleg'
$env:OIDC_DISCOVERY_URL='http://localhost:8080/realms/jungleg'
$env:OIDC_AUDIENCE='jungleg-api'
$env:SQS_ENDPOINT='http://localhost:4566'
```

> ⚠️ **O `api` do compose precisa estar PARADO durante a integração.** Ele consome a
> mesma `wager-transactions.fifo` e rouba as mensagens dos cenários. Sintoma
> observado na primeira rodada desta fase: `TestT1MesmaOperacao50VezesPelaFila`
> esperando "as 50 entregas serem tratadas" com `receiveCount` maior que 1 no log
> dos processos de teste, e `TestConferenciaFinalCruzandoHTTPESQS` sem receber a
> mensagem cruzada. `docker compose stop api` / `docker compose start api`.

---

## 2. Build, vet e formatação

```
go build ./...                          (sem saída = limpo)
go vet ./...                            (sem saída = limpo)
go vet -tags=integration ./...          (sem saída = limpo)
gofmt -l .\internal\ .\cmd\ .\migrations\ .\test\   (sem saída = limpo)
```

---

## 3. Suíte inteira, duas rodadas consecutivas

Com o `api` do compose **parado** (ver a armadilha na §1). Saída real:

```
=== ESTATICO ===
go build ./...                 (sem saída = limpo)
go vet ./...                   (sem saída = limpo)
go vet -tags=integration ./... (sem saída = limpo)
gofmt -l ...                   (sem saída = limpo)

=== RODADA 1 — UNIDADE (go test -count=1 -race ./...) ===
ok  internal/app 3.630s | app/inbox 1.495s | app/ports 1.412s | app/wagering 1.720s
ok  config 1.565s | consumer 2.545s | domain/ledger 1.429s | domain/money 1.624s
ok  domain/wagering 1.380s | domain/wallet 1.312s | httpapi 2.041s | infra/auth 4.482s
ok  infra/postgres 1.310s | observability 1.873s | canonicaljson 1.278s | idgen 1.264s
ok  worker 4.097s
=== RODADA 1 — INTEGRACAO (go test -count=1 -race -tags=integration -timeout 600s ./...) ===
ok  internal/app 13.253s | infra/postgres 6.524s | infra/auth 4.496s | worker 3.998s
ok  test/integration 146.924s
=== RODADA 2 — UNIDADE ===
ok  internal/app 3.290s | consumer 2.336s | observability 1.557s | worker 3.662s | (demais idem)
=== RODADA 2 — INTEGRACAO ===
ok  internal/app 13.181s | infra/postgres 6.523s | worker 3.994s
ok  test/integration 148.760s
=== FIM (exit 0) ===
```

**Zero falha nas quatro execuções.** A primeira tentativa desta rodada **falhou** por
contaminação de ambiente (o container `api` no ar consumindo a mesma fila) — o
diagnóstico está na §1 e o achado na §8.6. Depois de `docker compose stop api`, as
duas rodadas passaram sem tocar em código de teste.

---

## 4. `docker compose up --build` de um CHECKOUT LIMPO (README §15)

O teste que mais falha em entrega de desafio. Executado de verdade: o stack atual foi
derrubado (`docker compose down`, volumes preservados), o repositório foi clonado
para um diretório temporário e o compose foi subido **do zero**, com volumes novos.

```
=== 1. derrubando o stack atual (volumes preservados) ===
=== 2. clone limpo a partir do repositorio ===
HEAD do clone: 7486837 docs(fase12c): entrega o ENTREVISTA.md e o anexo das 23 interpretacoes
=== 3. docker compose up -d --build ===
build+up em 52s
=== 4. aguardando healthy ===
healthy=True (health=healthy)
jungleg-api      Up 6 seconds (healthy)
jungleg-keycloak Up 27 seconds (healthy)
jungleg-localstack Up 32 seconds (healthy)
jungleg-postgres Up 33 seconds (healthy)
=== 5. health e fluxo autenticado no ambiente limpo ===
ready: {"dependencies":{"oidc":"UP","postgres":"UP","sqs":"UP","workers":"UP"},"status":"UP"}
carteira: HTTP 201 {"id":"01a14829-8601-7e59-b279-ea3c14916e8d","playerId":"19dca2d9-81e2-470a-a08c-2066f7149bec","balance":{"amount":"500.00","currency":"BRL"},"version":1}
=== 6. derrubando o clone e limpando volumes ===
=== 7. restaurando o ambiente original ===
```

O clone tinha os seis serviços do compose, o realm importado, as migrations
aplicadas no volume novo e a carteira criada com token real — a entrega se
reproduz a partir de um checkout limpo.

**O que este ensaio encontrou:** dois defeitos de entrega que nenhuma fase anterior
havia pego, porque nenhuma tinha subido o container e olhado o estado (47 e 48).

---

## 5. Migrations: aplicar e REVERTER pelo caminho documentado

Banco descartável `jungle_entrega` (para não destruir o banco de desenvolvimento),
com o `bin/migrate.exe`:

```
=== migrate up ===        migrations aplicadas  versao=5 dirty=false     exit=0
=== migrate version ===   versão do schema      versao=5 dirty=false     5
=== migrate down 1 ===    migrations revertidas versao=4 dirty=false     exit=0
=== migrate version ===   versão do schema      versao=4 dirty=false     4
=== migrate down ===      migrations revertidas; schema sem versão      exit=0
=== migrate version ===   nenhuma migration aplicada                    exit=1
=== migrate up ===        migrations aplicadas  versao=5 dirty=false     exit=0
=== migrate up ===        schema já está atualizado, nada a aplicar     exit=1
```

Os códigos de saída conferem com o contrato documentado (0 mudou, 1 nada a fazer,
2 erro) — e é justamente o "1 nada a fazer" que quebrava o compose no segundo `up`
(defeito 48, corrigido com `up --noop-ok`).

---

## 6. Fluxo autenticado (README §9 e §2), executado contra o container

Token por `client_credentials` no realm `jungleg` (`internal-service` e
`provider-service`), com o container na porta 8081. Saídas reais:

```
health/live   → 200 {"status":"UP"}
health/ready  → 200 {"dependencies":{"oidc":"UP","postgres":"UP","sqs":"UP","workers":"UP"},"status":"UP"}

POST /wallets (interno)
  → 201 {"id":"01a14824-b7fa-…","playerId":"b543a7e1-…","balance":{"amount":"1000.00","currency":"BRL"},"version":1}

POST /wagering/transactions (provedor, Idempotency-Key: provider-a:txn-entrega-2)
  → 201 {"transactionId":"01a14824-b84d-…","status":"PROCESSED","balance":{"amount":"975.00","currency":"BRL"},"idempotentReplay":false,"failureCode":null}

mesmo pedido (replay)
  → 200 {… "idempotentReplay":true …}

mesma chave, corpo DIFERENTE
  → 409 {"error":{"code":"IDEMPOTENCY_CONFLICT","message":"a operação já existe com conteúdo diferente"}}

sem Idempotency-Key
  → 400 {"error":{"code":"INVALID_REQUEST","message":"o header Idempotency-Key é obrigatório","failureCode":"IDEMPOTENCY_KEY_REQUIRED"}}

BET de 5000.00 sobre saldo 975.00
  → 422 {"status":"REJECTED","balance":null,"failureCode":"INSUFFICIENT_FUNDS"}

GET /wallets/{id}                  → 200 {"balance":{"amount":"975.00",…},"version":2}
GET /wallets/{id}/ledger?limit=50  → 200 {"entries":[CREDIT 1000.00, DEBIT 25.00],"nextCursor":null}
POST /wallets/{id}/reconciliation  → 200 {"storedBalance":975.00,"calculatedBalance":975.00,"difference":0.00,"consistent":true,"checkedEntries":2}
GET /providers/provider-a/wagering/transactions/txn-entrega-2 → 200 {…,"status":"PROCESSED",…}

mesma consulta pelo provider-b → 404 {"error":{"code":"NOT_FOUND","message":"recurso não encontrado"}}
GET /wallets/{id} sem token    → 401
```

**Defeito 50, reproduzido no mesmo ambiente antes da correção:**

```
BET 25.00        → 201 saldo 75.00
ROLLBACK da BET  → 201 saldo 100.00
REFUND da MESMA BET → 201 saldo 125.00   ← dinheiro criado do nada
```

Depois da correção (`jaRevertida` simétrica), o mesmo `REFUND` responde
`422 REJECTED` com `failureCode=ALREADY_REVERSED` e o saldo continua 100.00 —
provado por `TestREFUNDDeBETJaDesfeitaPorROLLBACKEhRejeitado` (unidade) e
`…NoBanco` (integração). O ledger tinha 4 lançamentos consistentes no caso antigo:
a reconciliação **não** acusaria nada, e é isso que torna o defeito caro.

---

## 7. Conferências automáticas da entrega

| Conferência | Como | Resultado |
| --- | --- | --- |
| **Variáveis de ambiente** | script extrai os 61 nomes de `internal/config` (com o prefixo de cada struct) e compara com o `.env.example` | **0 faltando**; sobram só as 5 do compose (`POSTGRES_*`, `KEYCLOAK_*`). Antes da correção (defeito 49): 5 faltando e 2 com nome errado (`AWS_*`) |
| **Rotas** | `Select-String` nos registros do roteador × endpoints do README §9 | as 7 rotas de negócio + 3 públicas conferem (`POST/GET /wallets`, ledger, reconciliação, envio e leituras de transação, health e `/metrics`) |
| **Links internos** | cada `](caminho)` de `docs_desafio/*.md` resolvido contra o disco | todos resolvem (o link para `docs/evidencias/FASE_12.md` passou a resolver quando este arquivo foi criado) |
| **Nomes de teste citados nos documentos** | script extrai `Test[A-Za-z0-9_]+` do documento e compara com os `func Test…(` do repositório | `SISTEMA_EXPLICADO`: ~120 citações, todas existem; `ENTREVISTA`: 74 citações, todas existem |
| **Compose** | `docker compose config --quiet` | válido, com `SQS_ACCESS_KEY_ID`/`SQS_SECRET_ACCESS_KEY` e o `--noop-ok` no job de migration |

---

## 8. Os achados desta fase

### 8.1. Defeito 46 — duas variáveis de ambiente que nunca eram lidas

`SHUTDOWN_TIMEOUT` (topo do `Config`) e `SQS_PROVISION_QUEUES_ON_BOOT` estavam
declaradas com `env:`/default e **nenhum** código as lia. Configuração que promete
comportamento que não existe é pior que configuração ausente: alguém a liga
esperando efeito. Campos removidos; a suíte e a composição Fx confirmam que nada
dependia delas.

### 8.2. Defeito 47 — o container do compose nunca ficava `healthy`

O healthcheck executava `/app/api -health-check`; o Dockerfile instala o binário em
`/app/app`. O comando falhava sempre com "no such file or directory", e o serviço
ficava `unhealthy` **mesmo pronto**. Corrigido para `/app/app`; o ensaio mostra
`Up (healthy)`.

### 8.3. Defeito 48 — o segundo `docker compose up` nunca subia a API

O job `migrate` devolve **1** quando não há nada a aplicar (contrato do
golang-migrate), e `depends_on: service_completed_successfully` exige **0**. No
primeiro `up` as migrations aplicam (0); em todos os seguintes o job "falha" e a API
não é criada. Corrigido com a flag `up --noop-ok` no `cmd/migrate` (o contrato
continua valendo para uso manual) e `command: ["up","--noop-ok"]` no compose.

### 8.4. Defeito 49 — credenciais do broker com o nome errado

O `.env.example` e o compose traziam `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`,
mas a aplicação monta as credenciais estáticas de `SQSConfig`:
`SQS_ACCESS_KEY_ID`/`SQS_SECRET_ACCESS_KEY`. As documentadas eram **inertes**, e os
defaults `test` escondiam o erro. O mesmo script achou três variáveis reais fora do
exemplo (`SQS_BASE_BACKOFF`, `SQS_MAX_BACKOFF`, `SQS_SHUTDOWN_GRACE`). Os três
lugares corrigidos (`.env.example`, compose, tabela do README).

### 8.5. Defeito 50 — `REFUND` depois de `ROLLBACK` da mesma `BET` criava dinheiro

**O achado mais grave da caminhada, e ele veio da documentação.** Ao escrever o
capítulo da matriz `REFUND` × `ROLLBACK` do `SISTEMA_EXPLICADO.md`, a afirmação "os
dois sentidos são barrados" não se sustentou na leitura do código: a guarda de
coerência (`jaRevertida`) consultava o outro tipo em **uma** direção só. Reproduzido
contra o container: `BET 40 → ROLLBACK 40 → REFUND 40` levava 100.00 a **140.00**,
com o ledger consistente (a reconciliação não acusaria).

Correção: a guarda passou a consultar o outro tipo nos dois sentidos. Testes:
`TestREFUNDDeBETJaDesfeitaPorROLLBACKEhRejeitado` (unidade) e
`TestREFUNDDeBETJaDesfeitaPorROLLBACKEhRejeitadoNoBanco` (integração — saldo 100.00
e **dois** lançamentos). O par simétrico que já existia,
`TestROLLBACKDesfazBETeDesfazEstorno`, foi o modelo.

### 8.6. Achado de ambiente — o container `api` consome a fila dos testes

Ao rodar a suíte com o `api` do compose no ar, `TestT1MesmaOperacao50VezesPelaFila`
e `TestConferenciaFinalCruzandoHTTPESQS` falharam por **timeout**: o container tem
`SQS_CONSUME_TRANSACTIONS=true` e consome a mesma `wager-transactions.fifo`, então
parte das 50 mensagens foi tratada por ele (contra o banco `jungleg`, não o de
teste) e nunca chegou aos processos de teste. Não é defeito de código: é
contaminação de ambiente, e está documentada no README da solução (§8) e no
`ARCHITECTURE.md` §17.

### 8.7. Achados de régua

| Achado | O que parecia | O que era |
| --- | --- | --- |
| `.env.example` com 2 variáveis "sobrando" | variáveis inertes de propósito | nomes errados: a aplicação lê `SQS_*` (defeito 49) |
| `docker compose run --rm migrate up` falhou com `up: not found` | o compose quebrado | `run` **substitui** o `command` do serviço; o caminho certo é `docker compose run --rm migrate` (sem argumento) ou `docker compose run --rm migrate down 1` |
| job `migrate` com `sh -c` e `$codigo` | funcionava | o compose interpolava `$codigo` como variável do host; o `if` virava `[ -gt 1 ]` e "passava" por acidente. Abandonado em favor da flag no binário |

---

## 9. Conferência de conformidade §1–§15 (uma linha por requisito)

| § | Requisito | Evidência |
| ---: | --- | --- |
| 1 | Objetivo: API HTTP + consumidor movimentando carteiras com garantias | `TestHTTPFluxoCompletoComTokenReal`, `TestBETGravaSaldoLedgerEEventos`, `TestSQSProcessaEMovimentaCarteira` |
| 2 | Autenticação/autorização com IdP externo; `providerId` do token; isolamento entre provedores; acesso à fila por credencial | `internal/infra/auth` (49 casos), `TestHTTP…` (30 de integração), `deploy/keycloak/realm.json`, `TestHTTPProvedorDeOutroNaoVeTransacao` |
| 3 | Falhas do ambiente (entrega repetida, reversão antes da referência, concorrência, queda, republicação, indisponibilidade) | `TestT1…`, `TestT5…`, `TestT7…`, `TestQuedaEntreCommitEPublicacaoOutraInstanciaAssume`, `TestMetrica…` |
| 4 | Stack obrigatória (Go, Fx, IdP, PostgreSQL, SQS, Compose, migrations, `-race`) | `go.mod`, `internal/app`, `docker-compose.yml`, `migrations/`, suíte com `-race` |
| 5 | 8 garantias financeiras | `SISTEMA_EXPLICADO.md` cap. 5 (garantia → camada → teste); `ARCHITECTURE.md` §5 e §6 |
| 6 | Modelo de domínio (Money, Wallet, WagerTransaction, Ledger, inbox/outbox) | `internal/domain/*` (574 casos, `wagering` 100%), `ARCHITECTURE.md` §2–§4 |
| 7 | Operações e referências (5 tipos, reversões, TTL, `failureCode`) | `internal/app/wagering` + `SISTEMA_EXPLICADO.md` §6 (matriz e tabela de códigos) |
| 8 | Concorrência: 100.00 + duas de 80.00; três processos; carteiras em paralelo | `TestT2…`, `TestT1…` (3 processos), `TestT3…`; resultados na `FASE_10.md` |
| 9 | Contratos HTTP (7 rotas, status, corpos, paginação por cursor, reconciliação) | `docs_desafio/README.md` §7 e a tabela de status; curls reais na §6 desta evidência |
| 10 | Consumidor SQS (filas FIFO + DLQ, redrive, retry, DLQ, encerramento) | `internal/consumer`, `deploy/localstack/init-queues.sh`, `TestMetricaMensagemParaDLQ`, `FASE_8.md` |
| 11 | Outbox transacional (publicação depois do commit, lease, recuperação, 4 eventos) | `internal/worker`, `TestT6…`, `TestMetricasDaOutbox`, `ARCHITECTURE.md` §8 |
| 12 | Observabilidade (5 ids no log, 12 métricas, health checks; dashboard opcional) | `FASE_11.md` §3 e §4; `deploy/observability/` |
| 13 | Verificação (unidade, integração real, auth, T1–T8, conferência final, `-race`) | `FASE_10.md` §3, `ARCHITECTURE.md` §13, suíte da §3 desta evidência |
| 14 | Critérios e eliminatórios | `FASE_11.md` §9 (um por linha) e a tabela do `CONFORMIDADE_README.md` §14 |
| 15 | Entrega (código, compose, instruções, `.env.example`, IdP, `ARCHITECTURE`, comandos) | `docs_desafio/README.md`, §4 e §5 desta evidência, `.env.example` conferido |

---

## 10. Decisões desta fase

| DECISÃO | MOTIVO | O QUE QUEBRARIA SEM ELA |
| --- | --- | --- |
| A entrega vive em `docs_desafio/` (⚠️ 3, decidida com o usuário) | o `README.md` da raiz é o enunciado e não pode ser editado; a pasta reúne README da solução + os dois documentos finais | ou se edita o enunciado (proibido) ou a entrega fica invisível para quem abre o repositório |
| Todo comando do README da solução foi **executado** | comando que não roda não entra; é o que pegou os defeitos 47 e 48 | um "deve funcionar" que falha na mão do avaliador |
| `--noop-ok` no `up` em vez de mudar o código de saída | o contrato do CLI (1 = nada a fazer) é útil para uso manual e é documentado | scripts e o `compose run migrate down 1` mudariam de comportamento |
| A guarda de coerência REFUND×ROLLBACK é simétrica | o mesmo débito devolvido duas vezes cria dinheiro | o defeito 50: saldo maior que o inicial, com ledger consistente |
| Parar o `api` do compose antes da integração | ele consome a mesma fila e rouba mensagens dos testes | falha intermitente em T1/conferência, atribuída ao código errado |
| Os dois documentos finais seguem a matéria-prima das fases 10 e 11 | a Fase 12 organiza, não reconstrói; o número vem de teste, nunca de opinião | documento que contradiz a evidência |
| `SISTEMA_EXPLICADO` cita símbolo + linha, e avisa que a linha envelhece | referência estável é o símbolo; o número ajuda na primeira leitura | documento que fica errado no primeiro refactor |

---

## 11. Limitações e o que não foi entregue

- **Consumidor de `wallet-events.fifo`** não implementado (o §11 pede a publicação, e
  o contrato de consumo está documentado);
- **tracing/OpenTelemetry** e **partidas dobradas**: diferenciais opcionais;
- **contador durável de falhas do reaper** (linha `PENDING` inconsistente repete);
- **sem prova de SIGTERM no Windows** (a prova é no container Linux);
- **sem teste de carga** com throughput/p99 de produção;
- os números de linha citados no `SISTEMA_EXPLICADO.md` envelhecem (a referência
  estável é o símbolo).

---

## 12. Como usar os dois documentos

1. **Para entender o sistema (ou mantê-lo):** leia `docs_desafio/SISTEMA_EXPLICADO.md`
   na ordem — problema → vocabulário → fluxo → componentes → onde cada garantia é
   imposta → regras → decisões → defeitos → limites. O capítulo 5 é o que responde
   "quem me protege do quê".
2. **Para treinar a entrevista:** abra `docs_desafio/ENTREVISTA.md`, leia só a
   pergunta, responda em voz alta e confira; se a resposta não terminar em número ou
   teste, repita. As perguntas difíceis (bloco 6) e o anexo das 23 interpretações são
   o que evita a resposta decorada.
3. **Para avaliar a entrega:** `docs_desafio/README.md` (execução), `ARCHITECTURE.md`
   (decisões), `docs/CONFORMIDADE_README.md` (requisito → onde está) e esta pasta.

---

## 13. Rotina de execução da entrega

```powershell
# 0. ambiente
docker compose up -d --build          # inclui o job migrate e a api em :8081
docker compose stop api               # antes da integração (ele consome a fila)
docker exec jungleg-localstack awslocal sqs list-queues

# 1. estático
go build ./... ; go vet ./... ; go vet -tags=integration ./...
gofmt -l .\internal\ .\cmd\ .\migrations\ .\test\

# 2. unidade
go test -count=1 -race ./...

# 3. integração
go test -count=1 -race -tags=integration -timeout 600s ./...

# 4. de volta ao ambiente de demonstração
docker compose start api
```
