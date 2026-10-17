# EVIDÊNCIAS — Fase 11 (observabilidade e polimento)

> **Escopo:** o `README.md` §12 (observabilidade) e §15 (entrega), com o §4
> (composição e ciclo de vida) e o §14 (critérios e eliminatórios) como réguas.
> **O comportamento financeiro NÃO mudou**: nenhuma regra de negócio, lock,
> transação, contrato HTTP, nome de fila ou migration foi tocado. O que mudou foi
> a capacidade de PROVAR o que o sistema faz — e quatro defeitos de observabilidade
> que essa prova encontrou.

---

## Ambiente

| Item | Valor |
| --- | --- |
| SO | Windows 11, PowerShell 5.1 |
| Go | `go1.26.5 windows/amd64` |
| PostgreSQL | container `jungleg-postgres` (healthy), banco de teste `jungle_test`, esquema na versão **5** |
| Keycloak | container `jungleg-keycloak` (healthy), realm `jungleg` |
| LocalStack | container `jungleg-localstack` (healthy), `localstack/localstack:4.0.3`, **as quatro filas provisionadas** |
| Prometheus (validação das regras) | imagem `prom/prometheus:v2.54.1`, `promtool` |

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

Antes de confiar em teste de fila:

```
docker exec jungleg-localstack awslocal sqs list-queues
{
    "QueueUrls": [
        "http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/wager-transactions-dlq.fifo",
        "http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/wallet-events-dlq.fifo",
        "http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/wager-transactions.fifo",
        "http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/wallet-events.fifo"
    ]
}
```

---

## 1. Build, vet e formatação

```
go build ./...                          (sem saída = limpo)
go vet ./...                            (sem saída = limpo)
go vet -tags=integration ./...          (sem saída = limpo)
gofmt -l .\internal\ .\cmd\ .\migrations\ .\test\   (sem saída = limpo)
```

---

## 2. Testes — duas rodadas consecutivas da suíte inteira

Saída real (arquivo completo em `.git/suite-11e.txt` durante a execução):

```
=== RODADA 1 — UNIDADE (go test -count=1 -race ./...) ===
ok  internal/app 3.291s | app/inbox 1.468s | app/ports 1.408s | app/wagering 1.618s
ok  config 1.470s | consumer 2.283s | domain/ledger 1.379s | domain/money 1.614s
ok  domain/wagering 1.425s | domain/wallet 1.379s | httpapi 2.081s | infra/auth 4.018s
ok  infra/postgres 1.358s | observability 1.600s | canonicaljson 1.312s | idgen 1.311s
ok  worker 3.592s
=== RODADA 1 — INTEGRACAO (go test -count=1 -race -tags=integration -timeout 600s ./...) ===
ok  internal/app 12.809s | infra/auth 4.725s | infra/postgres 6.183s | worker 4.001s
ok  test/integration 147.956s
=== RODADA 2 — UNIDADE ===
ok  internal/app 3.289s | consumer 2.313s | observability 1.556s | worker 3.458s | (demais idem)
=== RODADA 2 — INTEGRACAO ===
ok  test/integration 146.810s
```

Resultado: **zero falha** nas quatro execuções. A rodada de integração é a que
sobe PostgreSQL, Keycloak, LocalStack e **processos reais** do binário.

### O que a Fase 11 acrescentou de teste

| Onde | Quantos | O que prova |
| --- | ---: | --- |
| `test/integration/metricas_integration_test.go` | 10 | uma função por métrica (as 12, com `operations_total`+latência no mesmo teste e as três da outbox no mesmo teste), lendo `/metrics` |
| `internal/consumer/consumer_test.go` | 3 | `retries_total{stage}` das etapas `sqs_receive`, `sqs_consume`, `sqs_publish` |
| `internal/worker/metricas_test.go` | 5 | `retries_total{stage}` das etapas `outbox_claim`, `outbox_mark`, `reference_claim`, `reaper_scan`, `worker_<laço>` |
| `internal/app/wagering/service_test.go` | 1 | conflito de unicidade é repetido **e contado** (defeito 42) |
| `internal/app/app_test.go` | 2 | ordem inversa dos hooks e prazo de encerramento |
| `test/integration/ciclo_de_vida_test.go` | 1 | `Start`/`Stop` do grafo REAL, ordem das sondas, pool fechado por último |
| `test/integration/logs_test.go` | 2 | log do **processo** real em JSON (produção) e texto (local) |

---

## 3. Prova por métrica (README §12)

Cada linha foi disparada pelo GATILHO REAL e lida no endpoint `/metrics` do
registry que o componente usa (`internal/testutil` interpreta o exposition com
`expfmt`, a mesma biblioteca do Prometheus). "Série exigida" é a asserção exata —
o teste falha se o valor, o nome ou o conjunto de labels mudar.

| # | Métrica | Gatilho real usado | Série exigida (asserção que passou) |
| ---: | --- | --- | --- |
| 1 | `operations_total` | BET 25.00 por HTTP (201) e BET 500.00 sobre saldo 100.00 (422) | `{kind="BET",status="PROCESSED"}` = 1 e `{kind="BET",status="REJECTED"}` = 1 |
| 2 | `operation_duration_seconds` | as mesmas duas requisições | `_count` = 1 em cada; `_sum` > 0; **11 buckets** (10 declarados + `+Inf`), com o `+Inf` igual à contagem |
| 3 | `idempotent_replays_total` | a mesma BET duas vezes (201 e 200) | = 1, com o saldo em 75.00 (um débito só) |
| 4 | `transaction_conflicts_total` | carteira travada por `SELECT ... FOR UPDATE` em outra conexão; a BET bate no `lock_timeout` de 3 s, é repetida 3× e responde 503 | = **2** (as duas repetições; a terceira tentativa sobe com o erro) e saldo intacto |
| 5 | `operations_total` no caminho SQS | mensagem BET pela fila, consumidor real | `{kind="BET",status="PROCESSED"}` = 1 (é o defeito 34, agora travado) |
| 6 | `inbox_duplicates_total` | inbox já `PROCESSED` com o `MessageId` do broker; a mensagem é reentregue | = 1, e saldo inalterado |
| 7 | `dlq_messages_total` | corpo `{"type":"...","data":{}}` (mensagem inválida = permanente) | = 1, e **1** mensagem na DLQ (drenada em seguida) |
| 8 | `outbox_publish_total{result}` | publisher real publicando 1 de 2 eventos no `wallet-events.fifo` | `{result="success"}` = 1 |
| 9 | `outbox_pending_age_seconds` | evento pendente envelhecido em 30 s, lote de 1 (um sai, um fica) | ≥ 25 s |
| 10 | `outbox_publish_attempts` | a mesma publicação | `_count` = 1 |
| 11 | `reconciliation_divergences_total` | saldo armazenado divergido por `UPDATE` e `POST /reconciliation` | = 1, com `consistent=false` |
| 12 | `reference_resolution_total{result}` | REFUND antes da BET → `PENDING_REFERENCE`; BET chega; worker retoma | `{result="resolvida"}` = 1 |

Além das doze: `retries_total{stage}` teve **os oito emissores** exercitados, cada
um pela política de produção do componente, com a porta falhando (broker, banco ou
laço):

```
sqs_receive      broker.Receive devolve erro
sqs_consume      processamento devolve ErrTransient  → adia (visibility)
sqs_publish      envio para a DLQ falha              → não apaga a mensagem
outbox_claim     o ciclo do publicador falha no claim do lote
outbox_mark      a mensagem saiu e a confirmação no banco falhou
reference_claim  o ciclo do worker de referências falha no claim
reaper_scan      a varredura do reaper falha
worker_<laço>    o runner reinicia um laço que morreu (nome do laço no label)
```

### Cardinalidade virou asserção

`TestMetricasSemLabelsDeAltaCardinalidade` raspa o `/metrics` de um servidor com
atividade e reprova se qualquer série de `jungleg_wager_*` tiver:

- label com nome de identificador (`walletId`, `transactionId`, `providerId`,
  `messageId`, `eventId`, `playerId`, `correlationId`);
- **valor** de label com formato de UUID.

A regra estava só em comentário; agora, se alguém a violar, o teste falha.

---

## 4. Prova do LOG DO PROCESSO (não de um logger de teste)

`TestLogsDoProcessoReal` sobe o binário com `APP_ENV=production` e os três laços,
dispara os três caminhos e lê o **stdout do processo**. Linhas reais capturadas
(saída do próprio teste; recortes de uma linha longa):

```json
{"time":"2026-10-17T01:09:47.3160104-03:00","level":"INFO","msg":"operacao_concluida","service":"jungleg","env":"production","transactionId":"01a1480d-3428-705b-8bb3-b1a48ab5b8e4","walletId":"01a1480d-3400-788f-9295-ba9e9aa1130c","providerId":"provider-a","status":"PROCESSED","idempotentReplay":false,"correlationId":"018f9d0e-3797-4d7a-8968-e5ace6a3f5c3","requestId":"018f9d0e-3797-4d7a-8968-e5ace6a3f5c3"}

{"time":"2026-10-17T01:09:47.3370057-03:00","level":"INFO","msg":"sqs_mensagem_processada","service":"jungleg","env":"production","messageId":"5d57f085-6aac-4db5-8d0f-43941693986c","transactionId":"01a1480d-343c-7d94-96c4-33055599ef5c","walletId":"01a1480d-3400-788f-9295-ba9e9aa1130c","providerId":"provider-a","status":"PROCESSED","failureCode":"","replay":false,"receiveCount":1,"durationMs":16.821,"correlationId":"5d57f085-6aac-4db5-8d0f-43941693986c"}

{"time":"2026-10-17T01:09:47.5170174-03:00","level":"WARN","msg":"reference_rejeitada","service":"jungleg","env":"production","transactionId":"01a1480d-3470-76c6-8782-3f9f03f56a4b","walletId":"01a1480d-3400-788f-9295-ba9e9aa1130c","providerId":"provider-a","kind":"REFUND","desfecho":"rejeitada","status":"REJECTED","attempt":2,"durationMs":6.581,"failureCode":"REFERENCE_MISMATCH","correlationId":"328c025c-eb3b-4141-ab47-8d2e8933efbe"}
```

> A linha `reference_rejeitada` acima veio de uma execução em que o teste tentou
> reverter 25.00 sobre uma BET de 5.00 — o caso **correto** de
> `REFERENCE_MISMATCH`, que serviu de achado de régua (abaixo). Na execução que
> passa, a retomada é bem-sucedida e o teste exige os mesmos campos em
> **`reference_resolvida`** (`correlationId`, `transactionId`, `walletId`,
> `providerId`).

| Caminho | Linha | Identificadores exigidos pelo teste |
| --- | --- | --- |
| HTTP | `operacao_concluida` | `correlationId`, `requestId`, `transactionId`, `walletId`, `providerId` |
| HTTP (acesso) | `http_request` | `correlationId`, `requestId` |
| SQS | `sqs_mensagem_processada` | `correlationId`, `messageId`, `transactionId`, `walletId`, `providerId` |
| Retomada | `reference_resolvida` | `correlationId`, `transactionId`, `walletId`, `providerId` |

E as **ausências** (todas conferidas em TODAS as linhas do stdout):

```
client_secret | provider-service-secret | Authorization | Bearer | access_token | password
"amount"      | "money"                 | "initialBalance"
"25.00"       | "10.00"                 | "1000.00" | "965.00"
```

O teste também exige que **toda linha** do stdout em produção seja JSON e que o
`/metrics` do processo exponha a operação processada — é a prova de exposição no
processo, não no registry do teste.

`TestLogsDoProcessoEmModoLocal` repete com `APP_ENV=local` e exige o **texto**
(`time=... level=INFO msg=operacao_concluida ...`), sem nenhuma linha JSON. É a
outra metade do defeito 27 (dois formatos no mesmo stdout).

---

## 5. Dashboard e alertas versionados

`deploy/observability/` (ver o `README.md` de lá para o runbook completo):

```
dashboard-jungleg.json   13 painéis do Grafana
  JSON OK - paineis: 13 - uid: jungleg-financeiro

alertas.yml              7 regras (promtool)
  Checking /etc/prometheus/alertas.yml
    SUCCESS: 7 rules found

prometheus.yml           scrape /metrics + /health/ready via blackbox
  SUCCESS: 1 rule files found
  SUCCESS: /etc/prometheus/prometheus.yml is valid prometheus config file syntax
  SUCCESS: 7 rules found
```

| Alerta | Limiar | Por que esse limiar |
| --- | --- | --- |
| `JunglegDown` | `up == 0` por 2m | 8 coletas perdidas (scrape 15 s) já não é oscilação de rede |
| `JunglegReadinessDown` | `probe_success == 0` por 2m | o readiness inclui banco, IdP, SQS e `workers`; menos de 2m dispararia em rollout normal |
| `JunglegOutboxAtrasada` | `pending_age > 60` por 5m | 2× o lease (30 s); um lease vencido sozinho é a RECUPERAÇÃO funcionando |
| `JunglegOutboxEmQuarentena` | `increase(...{result="failed"}[15m]) > 0` | recusa permanente não tem valor aceitável |
| `JunglegDivergenciaDeReconciliacao` | `increase(...[5m]) > 0` | incidente financeiro: o terceiro canal do README §9 |
| `JunglegMensagemNaDLQ` | `increase(...[10m]) > 0` | a DLQ é o fim da linha: ninguém reprocessa sozinho |
| `JunglegConflitosAltos` | conflitos/operações > 10% por 10m | disputa por carteira é esperada; 10% sustentado é contenção |

**Decisão registrada:** não foi adicionado Prometheus/Grafana ao
`docker-compose.yml`. Motivo: o diferencial é opcional (§12), o compose é o
artefato que a Fase 12 precisa provar de um **checkout limpo**, e dois serviços
novos ampliam a superfície de falha sem mudar garantia nenhuma. A definição está
versionada e sobe com dois `docker run` (documentados no README da pasta).

Nota de honestidade: `/health/ready` é **JSON**, não métrica — para alertar
"workers DOWN" sem criar métrica nova, o `prometheus.yml` usa o
`blackbox_exporter` (`probe_success`). Sem ele, o alerta de outbox atrasada cobre
o mesmo sintoma em minutos.

---

## 6. `go mod tidy`

```
go mod tidy
 go.mod | 60 +++++------------------------------  (7 inserções, 109 remoções com go.sum)
 go.sum | 56 -----------------------------------
```

- **removidos**: `testcontainers-go` e `modules/postgres` e a árvore que eles
  arrastavam (moby/containerd, gopsutil, otel, logrus, testify, dockerd...);
- **promovidos a diretos** (estavam marcados `// indirect` apesar de usados):
  `aws-sdk-go-v2` (+ `credentials`, `service/sqs`, `smithy-go`), `jackc/pgx/v5` e
  `prometheus/client_model` + `prometheus/common` (este último passou a ser usado
  pelo `internal/testutil`);
- `go mod verify` → `all modules verified`;
- `go.mod` ficou com **42 linhas** (era 88).

A suíte inteira foi rodada **depois** do tidy (seção 2) — o `go.sum` mudou, e o
método exige revalidar.

---

## 7. Ciclo de vida Fx (README §4 e §13)

`TestCicloDeVidaFxComStartEStopReais` usa `app.ModuleOptions()` **inteiro**, com
PostgreSQL, Keycloak e LocalStack reais:

```
=== RUN   TestCicloDeVidaFxComStartEStopReais
--- PASS: TestCicloDeVidaFxComStartEStopReais (0.10s)
ok  	test/integration	6.300s
```

O que ele afirma:

1. `Start` sobe o grafo e o `/health/ready` responde **200** com
   `"postgres":"UP"`, `"oidc":"UP"`, `"sqs":"UP"`, `"workers":"UP"`;
2. duas sondas registradas por **último** param **primeiro** (ordem inversa do
   registro) — é a regra em que a ordem de encerramento do projeto se apoia;
3. a sonda de parada dá `Ping` no pool com sucesso: **o pool ainda está aberto
   quando os componentes param**, e está **fechado** depois do `Stop`.

`TestHooksDeParadaNaOrdemInversa` prova a regra do Fx sem infra, e
`TestScopeDoPrazoDeEncerramento` prova o prazo (ver defeito 45).

`goleak` **não** foi adicionado: seria dependência nova para uma asserção que a
prova de SIGTERM do container já dá de forma comportamental (`exit=0`, pool
fechado por último, mensagem liberada — `FASE_10.md` §6).

---

## 8. Os defeitos REAIS encontrados nesta fase

### 8.1. Defeito 42 — a métrica de conflito existia e NUNCA era emitida

**Sintoma.** `jungleg_wager_transaction_conflicts_total` aparecia em `/metrics`
com valor **0 para sempre**. O teste do mecanismo passava (ele chama
`IncConflict` direto), e o `ARCHITECTURE.md` §12 dizia "quem emite: caso de uso" —
mas **nenhum caminho de produção chamava `IncConflict`**:

```
$ grep -rn 'IncConflict' internal/ | grep -v _test.go
internal/observability/metrics.go   (definição e o helper)
```

**Causa.** O ponto onde o sistema SABE que houve conflito é o laço de repetição do
caso de uso (`comRetentativa`), e ele não tinha como contar: o serviço não conhecia
métrica nenhuma (decisão de arquitetura: o caso de uso não importa Prometheus).

**Correção.** Uma interface de um método em `app/ports` (`ConflictCounter`), um
campo opcional no `Service` e o `IncConflict()` na linha em que a transação vai ser
repetida. O Fx liga `observability.Metrics` nela — a aplicação continua sem
conhecer Prometheus.

**Prova.** `TestMetricaConflitoDeTransacao` (integração, com lock real) exige **2**;
`TestConflitoDeUnicidadeERepetidoEContado` e `TestFalhaTransitoriaERepetida`
exigem 2 e 1 no dublê.

### 8.2. Defeito 43 — a mesma métrica tinha dois significados para o mesmo label

**Sintoma.** O caminho HTTP emitia

```
jungleg_wager_operations_total{kind="PROCESSED",status="2xx_201"}
```

e o caminho SQS emitia

```
jungleg_wager_operations_total{kind="BET",status="PROCESSED"}
```

**Causa.** `handlers.go` chamava `observarOperacao(res.Status.String(),
StatusLabel(statusHTTP))`: o label `kind` recebia o STATUS e o label `status`
recebia o código HTTP. O caminho SQS (Fase 8) usava a semântica correta.

**Por que ninguém viu.** Nenhum teste lia a métrica em `/metrics`. Um painel
somando por `kind` misturaria "tipo de operação" com "desfecho" sem erro nenhum.

**Correção.** HTTP passou a emitir `kind=<tipo>` e `status=<desfecho do domínio>`,
igual ao SQS; `StatusLabel` foi removido (ficou sem uso). O código HTTP continua no
log de acesso.

**Prova.** `TestMetricaOperacoesPorHTTP` e `TestMetricaOperacoesPelaFila` exigem as
mesmas labels nos dois caminhos.

### 8.3. Defeito 44 — campo de log que sempre mentia

**Sintoma.** Toda linha `http_request` trazia `"kind": ""`.

**Causa.** O middleware lia `observability.StageFrom(ctx)`, e **nenhum** código
chamava `WithStage` — sobra de um desenho abandonado.

**Correção.** Campo removido, e `WithStage`/`StageFrom` também (sem chamador, com
teste próprio que só testava a si mesmo).

### 8.4. Defeito 45 — a documentação do prazo de encerramento mentia

**Sintoma.** O teste que esperava `fx.StopTimeout(100ms)` cortar um `App.Stop`
**não passou**: o `Stop` esperou os 2 s do hook.

**Causa.** Em Fx 1.24, `App.Stop(ctx)` obedece ao **contexto do chamador**
(`withTimeout` seleciona em `ctx.Done()`); a opção `fx.StopTimeout` só é aplicada
por `App.run`, que este projeto **não usa** (o `app.Run` faz o próprio tratamento
de sinal).

**O comportamento estava certo** — o prazo efetivo sempre foi o `shutdownCtx`
criado com `cfg.HTTP.StopTimeout`. O que estava errado era a explicação no código
(que dava a entender que a opção bastava) e, por consequência, o teste.

**Correção.** Comentário corrigido (`app.go`) e teste renomeado para
`TestScopeDoPrazoDeEncerramento`, que prova o que de fato manda: o contexto. Ele
também confere `app.StopTimeout()` — se os dois valores divergirem, o teste avisa.

### 8.5. Achados de régua (o código estava certo — 4 correções antes de acusá-lo)

| Achado | O que parecia | O que era |
| --- | --- | --- |
| Histograma com "11 buckets" | `metrics.go` declara 10 | o Prometheus **acrescenta `+Inf`** à exposição; a asserção passou a exigir 11 |
| `REFERENCE_MISMATCH` no teste de log | "a retomada não resolve" | o teste tentou reverter 25.00 sobre uma BET de 5.00; a régua certa é reverter **o valor referenciado** |
| Instância local recusando conexão | "APP_ENV=local não sobe" | `subirInstancia` **não espera** readiness (só `subirInstancias` espera); faltava `esperarPronta` |
| `msg="operacao_concluida"` não achado em modo local | "o log não saiu" | o helper procurava o formato JSON; em texto é `msg=operacao_concluida` |
| Endpoint `/metrics` do harness sempre zerado | "a métrica não é emitida" | o `apiDeTeste` criava **dois** registries (um para o router, outro para os handlers); em produção o Fx injeta **um**. Corrigido no helper |

> O último é o mais instrutivo: o defeito estava na **régua** do teste, e é
> exatamente o tipo de erro que a fase existe para eliminar — "o endpoint não pode
> ler um registry que ninguém alimenta".

---

## 9. Revisão anti-eliminatórios (README §14)

| Eliminatório | Evidência nesta fase |
| --- | --- |
| Sem autenticação efetiva nos endpoints de negócio | os testes novos passam por `tokenDoKeycloak` (Keycloak real); `TestLogsDoProcessoReal` usa token interno e token de provedor, e o log mostra `providerId` do token |
| Acesso não autorizado a operações/transações | inalterado (Fases 7/10); os testes novos usam as mesmas rotas protegidas |
| Cálculo monetário em ponto flutuante | inalterado; nenhum caminho novo toca dinheiro (`ConflictCounter` conta, não calcula) |
| Saldo negativo por concorrência | inalterado; o teste de conflito termina com saldo intacto (10000) |
| Movimentação duplicada | `TestMetricaReplayIdempotente` e `TestMetricaDuplicataDaInbox` conferem o saldo **e** a métrica |
| Idempotência restrita à memória | inalterado (banco); o replay continua provado por HTTP e SQS |
| Dependência de uma única instância | inalterado; a Fase 11 não removeu nenhuma prova multi-instância |
| Publicação anterior ao commit | `TestMetricasDaOutbox` publica o que foi commitado e deixa o segundo evento pendente (nada é perdido nem marcado sem sair) |
| Ausência de ledger auditável | inalterado |
| Substituição integral de PostgreSQL/SQS/IdP por mocks | os testes de métrica e de log usam PostgreSQL, Keycloak e LocalStack **reais** e o **binário real**; os dublês de `retries_total` são de unidade e as portas falham de propósito (o DADO é real, a falha é injetada) |

**Observabilidade (5 pontos):** fechada — 12/12 métricas com teste de emissão e
leitura em `/metrics`, log do processo nos dois formatos com os cinco
identificadores e sem segredo/payload, health checks com `workers`, dashboard e
alertas versionados com runbook.

**Documentação (5 pontos):** os documentos finais são a Fase 12; esta fase atualizou
`ARCHITECTURE.md` §12/§15/§16/§17 e deixou a matéria-prima abaixo.

---

## 10. Decisões desta fase

| DECISÃO | MOTIVO | O QUE QUEBRARIA SEM ELA |
| --- | --- | --- |
| Ler o valor em `/metrics` (texto do exposition) em vez de espiar o objeto | é o que o Prometheus vê; pega nome errado, label errado e registro faltando | um teste verde sobre um registry que ninguém expõe (foi o defeito 43) |
| Contar conflito no `comRetentativa`, via `ports.ConflictCounter` | é o único ponto que SABE que a transação foi repetida; a interface mantém o caso de uso longe do Prometheus | a métrica volta a ficar em zero para sempre, ou a aplicação passa a importar Prometheus |
| `kind=<tipo>` e `status=<desfecho>` nos dois caminhos | somar por `kind` num painel precisa significar a mesma coisa | painel mistura tipo com desfecho e a série vira lixo |
| `correlationId` do SQS = `MessageId` da entrega, no contexto | o envelope do §10 não tem correlação e o produtor não tem outro id antes do envio | toda linha do caminho SQS sai sem correlação, enquanto o evento publicado tem |
| `Resultado` da inbox carrega `transactionId`/`walletId`/`providerId` | o consumidor não vê a requisição, e sem isso o log do SQS só tem `messageId` | "a mensagem foi tratada" sem dizer QUAL operação |
| Refletir `tx.CorrelationID()` no contexto da retomada | a resolução acontece em outro processo, minutos depois | a linha que mais importa para investigar sai sem correlação |
| Provar o log pelo stdout do **processo** | foi assim que o defeito 27 passou despercebido | dois formatos no mesmo stdout, ou um logger de teste que esconde o real |
| Não adicionar Prometheus ao compose | diferencial opcional × artefato de entrega que precisa subir limpo | compose maior, mais modos de falha no "sobe do zero", nenhuma garantia nova |
| `blackbox_exporter` para o readiness | `/health/ready` é JSON; a fase não cria métrica | não há como alertar "workers DOWN" sem inventar métrica |
| `go mod tidy` e revalidar tudo | `go.sum` mudou; dependência não usada é ruído na entrega | árvore de dependências de 200 MB para uma suíte que usa o compose |
| Não adicionar `goleak` | a prova comportamental já existe (SIGTERM no container) | dependência nova para uma asserção que pode ser instável |

---

## 11. Limitações conhecidas desta fase

1. **O contador de conflito conta REPETIÇÕES, não erros.** A terceira tentativa que
   falha sobe como erro e não entra no contador. É contrato do teste (`= 2`) e está
   documentado no `ARCHITECTURE.md` §12.
2. **`operations_total` conta ATENDIMENTOS**, inclusive replay. Quem separa replay
   é `idempotent_replays_total`. Um painel que use `operations_total` como "número
   de operações financeiras" vai contar de mais.
3. **`retries_total` tem 8 estágios provados em teste de UNIDADE.** A emissão é
   real (código de produção) e as portas falham de propósito; não há teste de
   integração com falha de banco porque provocá-la exigiria derrubar o banco
   compartilhado.
4. **O readiness não é métrica.** O alerta de "workers DOWN" depende do
   `blackbox_exporter` (ou, na falta dele, do alerta de outbox atrasada).
5. **A idade da outbox é medida a cada 15 s.** Um pico curto entre medições não
   aparece no gauge (aparece no log `outbox_atrasada`).
6. **O dashboard não é exercitado em CI** — é JSON validado e importado à mão; não
   há Grafana no compose (decisão da seção 5).
7. **A latência continua medida com cliente local** (herdada da Fase 10).

---

## 12. Rotina de execução da fase

```powershell
# 0. infraestrutura
docker compose up -d postgres keycloak localstack
docker exec jungleg-localstack awslocal sqs list-queues   # as quatro filas

# 1. estático
go build ./... ; go vet ./... ; go vet -tags=integration ./...
gofmt -l .\internal\ .\cmd\ .\migrations\ .\test\

# 2. unidade
go test -count=1 -race ./...

# 3. integração (banco SEPARADO; Keycloak e LocalStack reais)
go test -count=1 -race -tags=integration -timeout 600s ./...

# 4. só as provas desta fase (~35 s, sem os processos da Fase 10)
go test -count=1 -race -tags=integration -timeout 600s -run 'TestMetrica|TestLogsDoProcesso|TestCicloDeVidaFx' ./test/integration/ -v
go test -count=1 -race -run 'TestMetrica|TestConflito|TestFalhaTransitoria|TestHooks|TestScope' ./internal/...

# 5. observabilidade (opcional, fora do compose)
docker run -d --name jungleg-blackbox --network jungleg_default prom/blackbox-exporter:v0.25.0
docker run -d --name jungleg-prometheus --network jungleg_default -p 9090:9090 `
  -v "${PWD}/deploy/observability/prometheus.yml:/etc/prometheus/prometheus.yml:ro" `
  -v "${PWD}/deploy/observability/alertas.yml:/etc/prometheus/alertas.yml:ro" `
  prom/prometheus:v2.54.1
```

---

## MATÉRIA-PRIMA PARA OS DOIS DOCUMENTOS

> Como no `FASE_10.md`: aqui está o FATO, com o número. A Fase 12 organiza.

### A. Pergunta → resposta curta (o que esta fase provou)

| Pergunta | Resposta curta, com o número |
| --- | --- |
| Como você sabe que o saldo está errado em produção? | `jungleg_wager_reconciliation_divergences_total` sobe, o log `reconciliacao_divergente` traz `storedMinor`/`calculatedMinor`/`differenceMinor` e `walletId`, e o alerta `JunglegDivergenciaDeReconciliacao` dispara em ≤ 5 min |
| Como você sabe que um evento confirmado não foi publicado? | `jungleg_wager_outbox_pending_age_seconds` > 60 s (2× o lease) dispara `JunglegOutboxAtrasada`; o log `outbox_atrasada` diz quantos e a idade; nada foi perdido — o evento está na tabela |
| Como você sabe que a operação foi processada, e não só aceita? | `operations_total{kind="BET",status="PROCESSED"}` subiu e o log `operacao_concluida` traz `transactionId` + `walletId` com o `status` |
| Como você rastreia UMA operação do começo ao fim? | pelo `correlationId`: no HTTP ele vem do `X-Correlation-Id` (ou é o `requestId`); no SQS é o `MessageId` da entrega; na retomada vem da coluna `correlation_id`. A mesma linha tem `transactionId`, `walletId` e `providerId` |
| As métricas estão certas ou só existem? | 12/12 têm teste que dispara o gatilho real e lê `/metrics`; duas estavam MENTINDO (defeitos 42 e 43) e foram corrigidas |
| Quantas métricas e quais labels? | 12, sempre com valor de cardinalidade baixa (`kind`, `status`, `stage`, `result`). Um `walletId` como label reprova o teste de cardinalidade |
| Como você prova que o log de produção é JSON? | o teste sobe o BINÁRIO com `APP_ENV=production`, lê o stdout e exige que **toda** linha parseie; em `APP_ENV=local` exige texto e nenhuma linha JSON |
| O log vaza dado financeiro? | o teste reprova qualquer linha com `"amount"`, `"money"`, `"initialBalance"` ou valor monetário em texto, e com `client_secret`/`Authorization`/`Bearer` |
| O que acontece com o worker no encerramento? | `Stop` com o grafo real: as sondas param na ordem inversa, o pool responde `Ping` durante o `OnStop` dos componentes e está fechado depois — o `SIGTERM` do container termina com `exit=0` (Fase 10) |
| O prazo de encerramento é respeitado? | sim, mas por quem você imagina: o **contexto** passado a `Stop` (montado de `HTTP_STOP_TIMEOUT`), não a opção `fx.StopTimeout` (defeito 45) |
| Quanto custou em dependências? | `go mod tidy` removeu `testcontainers-go` e a árvore dele; `go.mod` caiu de 88 para 42 linhas, `go.sum` 56 linhas a menos, `go mod verify` limpo |

### B. Decisões em tabela (para quem mantém)

| Decisão | Por que | O que quebraria sem ela |
| --- | --- | --- |
| Um teste por métrica, lendo `/metrics` | mecanismo testado não é emissão: duas métricas ficaram mentindo por fases | o painel mostra zero e o plantonista procura no lugar errado |
| Contador de conflito via porta de um método | o caso de uso sabe da repetição e não pode importar Prometheus | a métrica de conflito volta a ser decorativa |
| `kind`/`status` com o mesmo significado nas duas portas | a série é uma só, e o painel soma por label | "BET" e "PROCESSED" na mesma coluna de `kind` |
| Log provado no stdout do processo | defeito 27: dois formatos no mesmo stdout, invisível em teste de objeto | o container entrega log que não é o que o README pede |
| `correlationId` do SQS = `MessageId` da entrega | o §10 não tem campo de correlação e o produtor não tem outro id antes do envio | o caminho assíncrono fica sem rastro, e o evento publicado com rastro |
| `Resultado` carrega os ids da operação | o consumidor não vê a requisição | "tratei a mensagem" sem dizer qual transação/carteira/provedor |
| Ciclo de vida provado com Start/Stop reais | `fx.ValidateApp` só prova que o grafo FECHA | ordem de encerramento e fechamento do pool ficam no comentário |
| Dashboard/alerta versionados, sem serviço no compose | diferencial opcional × artefato de entrega | ou o compose incha, ou a definição do painel não existe |
| `blackbox` para o readiness | o readiness é JSON, e a fase não cria métrica | "workers DOWN" sem alerta |

### C. O que a Fase 11 acrescentou à compreensão do sistema

1. **Métrica registrada não é métrica emitida.** O helper funcionando e o valor em
   `/metrics` são coisas diferentes; a segunda é a única que o operador vê.
2. **O mesmo nome de métrica pode ter dois contratos.** Um label com significado
   diferente por caminho não quebra compilação nem teste — quebra o painel.
3. **O caminho assíncrono é o que mais precisa de identificador.** No HTTP o
   `requestId` está sempre ali; no SQS ele precisa ser escolhido (o `MessageId` da
   entrega), e na retomada precisa ser persistido (migration 000005).
4. **O log do processo é a prova, não o logger do teste.** Formato e cobertura só
   são reais no stdout do binário.
5. **A opção de framework não é a regra.** `fx.StopTimeout` não governa
   `App.Stop(ctx)`: o prazo é do contexto do chamador. Ler o código do framework
   valeu mais que a documentação dele.
6. **Régua errada continua sendo o modo de falha mais comum** — 4 correções de
   régua nesta fase contra 4 defeitos reais (e um dos defeitos estava na própria
   régua: dois registries no helper).

### D. Perguntas difíceis que a fase permite responder com honestidade

| Pergunta difícil | Resposta honesta |
| --- | --- |
| "Se a métrica de conflito nunca foi emitida, o que mais pode estar mentindo?" | "Nada que eu tenha encontrado: as outras 11 foram disparadas pelo gatilho real e lidas em `/metrics` nesta fase. O que eu não tenho é alerta em cima de todas elas — o runbook cobre as que exigem ação." |
| "Você mexeu no comportamento financeiro para consertar observabilidade?" | "Não. As mudanças são contador, campo de log e contexto. Nenhum lock, transação, regra ou contrato HTTP mudou, e a suíte das fases 1–10 continua verde nas duas rodadas." |
| "O dashboard está no ar?" | "A definição está versionada e validada (`promtool` e o JSON), mas não há Prometheus no compose, por decisão registrada: o diferencial é opcional e o compose é o artefato que precisa subir limpo. Subir o painel custa dois `docker run`, documentados." |
| "Como você alerta 'workers DOWN' se o readiness não é métrica?" | "Pelo `probe_success` do blackbox em cima do `/health/ready`. Sem o blackbox, o alerta de outbox atrasada cobre o mesmo sintoma em minutos — é menos direto e eu digo isso no runbook." |
| "O contador de conflito conta o quê, exatamente?" | "Cada REPETIÇÃO da transação por conflito ou falha transitória. A tentativa que falha pela terceira vez não entra: ela não é conflito tratado, é o erro que sobe. O teste exige 2 justamente para fixar isso." |
