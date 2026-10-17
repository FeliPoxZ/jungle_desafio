# PROMPT DE ABERTURA — FASE 11 (observabilidade e polimento)

> Cole este arquivo como **primeira mensagem** de uma nova conversa no workspace
> `E:\jungleg`. Ele é autossuficiente: diz onde o projeto está, o método, os fatos do
> código que importam, os blocos da fase, as armadilhas de ambiente e os critérios de
> aceite. O handoff completo continua sendo o `PROXIMO_PASSO.md`; o enunciado
> continua sendo o `README.md` da raiz.

---

## 0) REGRA ABSOLUTA — LEIA ISTO ANTES DE QUALQUER COISA

O **`README.md` da raiz é a cabeça de tudo**: é o enunciado do desafio, é a fonte de
verdade e **não pode ser editado** (nem uma vírgula). Sempre que houver dúvida — de
regra de negócio, de contrato, de status, de escopo, de nomenclatura — a resposta está
no `README.md`, e ele vale **sobre** qualquer plano, handoff, documento interno ou
opinião sua. Se `PLANO.md`, `ARCHITECTURE.md`, `docs/CONFORMIDADE_README.md` ou este
prompt discordarem do `README.md`, **vale o `README.md`**, e a divergência deve ser
registrada no lugar certo.

**Antes de escrever uma linha de código, leia nesta ordem:**

1. `README.md` (raiz) — para esta fase: **§12 (observabilidade)**, **§15 (entrega)**,
   **§14 (critérios e eliminatórios, inclusive "Observabilidade — 5 pontos" e
   "Documentação — 5 pontos")** e **§4 (stack e ciclo de vida: `fx.Lifecycle`,
   shutdown, liberação de recursos)**;
2. `PROXIMO_PASSO.md` — handoff completo: estado, método, decisões congeladas,
   pendências e o checklist de arranque da Fase 11 (§8);
3. `ARCHITECTURE.md` — **§12** (as 12 métricas e quem emite cada uma), **§16**
   (limitações), **§17** (números e como validar tudo);
4. `docs/CONFORMIDADE_README.md` — **§12** (observabilidade) e **§14**;
5. `docs/evidencias/FASE_10.md` — a fase imediatamente anterior; a **seção final
   ("MATÉRIA-PRIMA PARA OS DOIS DOCUMENTOS")** é o que a Fase 12 vai reorganizar;
6. `PLANO.md` §3 — o checklist da FASE 11.

---

## 0b) OS DOIS DOCUMENTOS FINAIS — O QUE ESTA CAMINHADA ESTÁ CONSTRUINDO

Esta fase **não** é a que escreve os dois documentos finais (essa é a Fase 12), mas é
uma das que mais produz matéria-prima para eles: observabilidade é **como você prova
que o sistema está de pé e o que fazer quando não está** — e é exatamente a história
que um entrevistador quer ouvir quando pergunta "como você descobre que o saldo está
errado em produção?".

Existem **dois** documentos pendentes, **entrega obrigatória** (compromisso registrado
em `PROXIMO_PASSO.md` §3.5 e §3.6 e no `PLANO.md` §3, exigência do usuário datada de
2026-10-16):

### `docs/SISTEMA_EXPLICADO.md` — para quem vai LER E MANTER o código

- **Público:** alguém que abre o repositório sem conhecer o projeto (o avaliador, um
  colega novo, você mesmo em três meses).
- **Pergunta que responde:** *"o que este sistema faz, por que ele é assim, e o que
  quebra se eu mudar?"*
- **Unidade de texto:** componente e fluxo. Começa do zero de vocabulário — o que é
  dinheiro em unidades mínimas, o que é carteira, o que é um lançamento de ledger, o
  que é uma transação e um lock, o que é idempotência, o que é outbox — e **só depois**
  as ligações (quem chama quem) e as regras de negócio (por que `LOSS` não movimenta,
  por que `ROLLBACK` de `REFUND` debita, por que o replay devolve o saldo original).
- **Inclui:** o fluxo completo de uma requisição (HTTP → commit → publicação), cada
  componente e por que existe, as decisões com motivo, a lista de **todos** os
  problemas resolvidos (sintoma, causa, correção) e os limites conhecidos.
- **Critério de pronto:** um leitor que nunca viu o projeto consegue explicar, depois
  de ler, **onde** cada garantia é imposta (domínio, banco ou processo) e **o que
  aconteceria** se ela não existisse.

### `docs/ENTREVISTA.md` — para o USUÁRIO TREINAR

- **Público:** o candidato, treinando em voz alta, com tempo limitado.
- **Pergunta que responde:** *"como eu defendo esta decisão em 60 segundos, com
  número, e como eu admito o que ficou frágil sem parecer que não sei?"*
- **Unidade de texto:** pergunta → resposta curta e afiada, com o motivo e a
  consequência.
- **Inclui:** arquitetura ("por que `READ COMMITTED` + `FOR UPDATE` e não
  `SERIALIZABLE`?", "por que outbox e não publicar direto?", "por que o ledger é
  separado da carteira?"), decisão ("por que `int64` em unidades mínimas?", "por que o
  domínio não importa `encoding/json`?"), garantia ("como você impede débito
  duplicado?", "onde está a proteção contra lost update?", "o que acontece se o
  processo morrer no meio?"), erro e aprendizado ("qual foi o bug mais difícil?", "o
  que faria diferente?") — **inclusive as perguntas difíceis, as que expõem
  limitação**, com resposta honesta.
- **Critério de pronto:** nenhuma resposta termina em "porque é boa prática". Toda
  resposta tem **motivo**, **consequência concreta** e, quando existe, **número**.

### A distinção, em uma tabela (é esta que vale para as duas fases)

| Dimensão | `SISTEMA_EXPLICADO.md` | `ENTREVISTA.md` |
| --- | --- | --- |
| Para quem | quem vai manter | quem vai defender |
| Tempo de leitura | longo, por capítulos | curto, por pergunta |
| Tom | didático, do zero ao avançado | direto, como se estivesse falando |
| Vocabulário | **explicado na primeira aparição** | pressupõe que já foi aprendido |
| Estrutura | por componente e por fluxo | por pergunta e por tipo de pergunta |
| Usa a tabela `DECISÃO \| MOTIVO \| O QUE QUEBRARIA SEM ELA` como | **prosa**: cada decisão vira um parágrafo que ensina | **resposta**: cada decisão vira uma fala de 30–60 s |
| Trata limitação como | capítulo "limites conhecidos" | pergunta difícil com resposta honesta |
| O que NÃO é | resumo do `ARCHITECTURE.md` (aquele é para o avaliador, denso, em terceira pessoa) | lista de jargão decorado |

### O triângulo `DECISÃO | MOTIVO | O QUE QUEBRARIA SEM ELA` — como ele vira os dois documentos

Este é o método desta caminhada desde a Fase 6: cada parte que importa é explicada
**nesta conversa** com esse formato. Ele não é burocracia — é o **rascunho direto** dos
dois documentos. Exemplo real, de duas linhas da Fase 10:

| DECISÃO | MOTIVO | O QUE QUEBRARIA SEM ELA |
| --- | --- | --- |
| `SELECT ... FOR UPDATE` na linha da carteira, antes de ler o saldo | o conflito real é "duas operações na mesma carteira"; o lock de linha serializa só isso | as duas apostas de 80.00 leriam 100.00 ao mesmo tempo e uma escrita sumiria (lost update), ou o cliente levaria erro à toa |

1. **No `SISTEMA_EXPLICADO.md`** isso vira dois parágrafos: o primeiro explica, **sem
   presumir vocabulário**, o que é uma transação e o que é um lock de linha ("o banco
   deixa a segunda transação esperando até a primeira terminar"); o segundo conta a
   consequência e **onde** ela é imposta (no banco, não na aplicação), e aponta o teste
   que prova.
2. **No `ENTREVISTA.md`** isso vira:
   > **"Por que não `SERIALIZABLE`?"** — "Porque o conflito real do domínio é por
   > carteira, e `SERIALIZABLE` me faria repetir transações que nem conflitaram.
   > Prefiro um lock curto e determinístico na linha da carteira. A prova: com as duas
   > apostas de 80.00 sobre 100.00, uma vira `PROCESSED`, a outra `REJECTED` por saldo
   > insuficiente, o saldo fica 20.00 e o ledger tem **um** débito. E carteiras
   > diferentes continuam em paralelo — medi p99 de 166 ms em carteiras distintas
   > contra 579 ms na mesma carteira."
3. **A pergunta difícil que o mesmo dado permite responder:** *"e se o processo morrer
   entre o commit e a resposta?"* — resposta com o T5 e o SIGTERM do container, e
   **admitindo** que a janela foi reproduzida por estado durável, não provocada por
   sinal.

**Sem esse triângulo, a Fase 12 vira arqueologia.** Com ele, vira montagem.

---

## 1) MÉTODO DE TRABALHO (o mesmo das Fases 6 a 10 — não mude)

a) **A IA escreve o código.** O usuário revisa e contesta. Não peça para ele digitar.

b) **UM BLOCO COERENTE POR VEZ.** Nunca entregue a fase inteira de uma vez. Cada bloco:
código/teste → validação real → commit. Depois do commit, o próximo bloco.

c) **EXPLIQUE DEPOIS DE ESCREVER, e explique de verdade.** Para cada parte que importa,
no formato de tabela: `DECISÃO | MOTIVO | O QUE QUEBRARIA SEM ELA`. Sem jargão não
explicado: o usuário está se preparando para a entrevista e precisa saber DEFENDER cada
linha. Explicação rasa é entrega incompleta.

d) **VALIDE DE VERDADE**, com a suíte inteira, e mostre a saída real (não "deve
funcionar"). Obrigatório em cada bloco:

```
go build ./...
go vet ./...
go vet -tags=integration ./...
gofmt -l ./internal/ ./cmd/ ./migrations/ ./test/
go test -count=1 -race ./...
go test -count=1 -race -tags=integration -timeout 600s ./...
```

e) **ACEITE CONTESTAÇÃO.** Se o usuário apontar um erro e ele estiver certo, ADMITA E
CORRIJA, sem defensiva e sem minimizar.

f) **QUANDO UM TESTE FALHAR, ANTES DE ACUSAR O CÓDIGO VERIFIQUE SE A RÉGUA DO TESTE
ESTÁ CERTA.** Nesta caminhada já foram corrigidas asserções erradas nas Fases 6, 7, 8,
9 e 10 (a do FIFO com grupo fixo, a do dedup id repetido, a da carteira sem lançamento
de abertura, a do TTL com `MaxAttempts=3`, a do readiness "não iniciado", o
`MaxNumberOfMessages=14`, a espera pelos contadores aproximados do SQS, o resíduo de
fila entre testes). Quando o sintoma não fizer sentido, escreva um teste de **unidade**
que rode o dado REAL que falhou.

g) **REGISTRE TODO ACHADO** na mensagem de commit E nas evidências da fase, com o
sintoma, a causa de fundo e a correção. Os defeitos já somam **41** e eles são parte do
valor da entrega.

h) **NÃO QUEBRE O QUE JÁ FUNCIONA.** Antes de mudar algo de uma fase anterior, leia o
código e o teste que o cobre. As Fases 0 a 10 estão validadas: qualquer alteração nelas
exige rodar a suíte inteira de novo.

i) **ESTA FASE É DE OBSERVABILIDADE E POLIMENTO — O COMPORTAMENTO FINANCEIRO NÃO MUDA.**
Nenhuma regra de negócio, nenhum lock, nenhuma transação, nenhum contrato HTTP deve
mudar. Se um teste revelar que uma métrica **mente** (conta errado, falta de emissão em
um caminho), isso é defeito e a correção vem com o teste que a trava. Métrica nova só
se o README §12 pedir — e ele pede **categorias**, que já estão cobertas.

---

## 2) ESTADO ATUAL (fim da Fase 10 — tudo abaixo está PRONTO e VALIDADO)

**Domínio (Fases 1–3):** `Money` (int64 em unidades mínimas), `Wallet`, `Ledger`
append-only, `WagerTransaction` com máquina de estados — 574 casos, `wagering` 100% de
cobertura.

**Persistência (Fase 5):** repositórios pgx, Unit of Work, `SELECT ... FOR UPDATE` por
carteira, migrations (**esquema na versão 5**), classificação de erro do driver
(inclusive `55P03` como transitório).

**Caso de uso (Fase 6) e HTTP (Fase 7):** `Process`, `OpenWallet`, leituras,
`Reconcile`, outbox na mesma transação, idempotência persistente com replay; 7
endpoints do §9 com OIDC real (Keycloak) e isolamento por provedor.

**SQS (Fase 8):** `wager-transactions.fifo` + DLQ, inbox na mesma transação, retry por
`ChangeMessageVisibility` com backoff, DLQ explícita, encerramento seguro.

**Outbox e workers (Fase 9):** `OutboxPublisher` (claim → publicar fora da tx → marcar,
lease, quarentena), `ReferenceWorker`, `PendingReaper`, `Runner` com supervisão e
readiness `workers`.

**Concorrência e recuperação (Fase 10):** **três processos independentes** provaram
T1–T8 e a conferência final do §13; prova de **SIGTERM real em container Linux**;
`application_name` por instância (defeito 40); `OIDC_JWKS_URL` (defeito 41).

### O que já existe e NÃO precisa ser refeito

- **12 métricas emitidas** (`internal/observability/metrics.go`), conforme
  `ARCHITECTURE.md` §12: `operations_total`, `operation_duration_seconds`,
  `idempotent_replays_total`, `inbox_duplicates_total`, `retries_total`,
  `dlq_messages_total`, `transaction_conflicts_total`, `outbox_publish_total`,
  `outbox_pending_age_seconds`, `outbox_publish_attempts`,
  `reconciliation_divergences_total`, `reference_resolution_total`;
- **log estruturado JSON** com `correlationId`/`requestId` automáticos, redação de
  dado sensível (`observability.Redact`), e **os cinco identificadores do §12**
  circulando nos caminhos de HTTP, SQS, workers e retomada;
- **`/health/live` e `/health/ready`** com PostgreSQL, IdP, SQS (a fila específica) e
  `workers`;
- **13 testes** em `internal/observability` (correlação, request id, handler de
  contexto, logger por nível, redact, `ObserveOperation`, nil-safe, handler de
  métricas, rótulos) e **3** de composição em `internal/app`
  (`TestValidateAppComposition`, `...ComWorkersLigados`, `TestBuildAppFailsWithInvalidConfig`);
- **prova do binário real** com os três laços (`internal/app`) e do container.

### O que a Fase 11 precisa FECHAR (é o que você vai construir)

- **teste mínimo POR MÉTRICA**: hoje há teste do mecanismo (`ObserveOperation`), não há
  teste que dispare cada uma das 12 e a leia em `/metrics`. Uma métrica que ninguém
  observa pode estar emitindo zero para sempre — foi o que aconteceu com
  `wager_operations_total` no caminho SQS (defeito 34);
- **auditoria dos logs**: provar, com saída real, que os cinco identificadores aparecem
  nos caminhos certos e que **nenhum** payload financeiro completo nem credencial entra
  no log (há um defeito histórico de **dois loggers no mesmo processo** — defeito 27 —,
  então a prova precisa ser do processo, não de um logger construído no teste);
- **dashboard/alerta versionado** (diferencial do §14): atraso da outbox, DLQ,
  divergência de reconciliação, workers DOWN;
- **polimento**: `go mod tidy` (o `testcontainers-go` está **indireto** no `go.mod` e
  não é usado), `gofmt`, `go vet`, teste da composição Fx com **start/stop real** e
  liberação de recursos (a `fx.ValidateApp` já existe; falta exercitar o ciclo de vida e
  o `StopTimeout` em teste), `goleak` **opcional**;
- **revisão anti-eliminatórios** do §14 item por item, com evidência.

---

## 3) A TAREFA — FASE 11 (README §12 e §15), em CINCO BLOCOS

### BLOCO 11a — métricas: cada uma tem um teste que a vê

- **teste de integração** (ou de unidade onde não precisar de infra) que, para cada uma
  das 12 métricas, **dispara o gatilho real** e lê o valor em `/metrics`:
  - operação HTTP processada e rejeitada → `operations_total{kind,status}` e
    `operation_duration_seconds` (buckets);
  - replay de idempotência → `idempotent_replays_total`;
  - reentrega pelo SQS → `inbox_duplicates_total`; retry → `retries_total{stage}`;
  - mensagem envenenada → `dlq_messages_total`;
  - conflito de unicidade → `transaction_conflicts_total`;
  - publicação da outbox → `outbox_publish_total{result}`,
    `outbox_pending_age_seconds`, `outbox_publish_attempts`;
  - reconciliação divergente → `reconciliation_divergences_total`;
  - retomada de referência → `reference_resolution_total{result}`.
- onde a leitura exigir infra real (SQS/LocalStack), use a infra real — nada de dublê
  de broker nos testes de integração;
- **cardinalidade**: conferir que nenhum label é identificador (`walletId`,
  `transactionId`); isso é regra de Prometheus e já está documentado — transforme em
  asserção se for barato;
- **documentação**: `ARCHITECTURE.md` §12 já tem a tabela métrica → quem emite → para
  que serve. Acrescente a coluna "teste que prova" (ou uma nota) e mantenha a tabela
  verdadeira.

### BLOCO 11b — logs: prova do PROCESSO, não de um logger de teste

- **subir o binário real** (o padrão de `internal/app/healthcheck_integration_test.go`:
  `compilarBinario`, `exec.Command`, `portaDeTeste`, `esperarPronto`) e capturar o
  **stdout** do processo em `APP_ENV=production` (JSON) e em `local` (texto);
- provar que uma requisição HTTP autenticada, uma mensagem processada pela fila e uma
  resolução de referência pelo worker produzem linhas com **`correlationId`,
  `messageId`, `transactionId`, `walletId`, `providerId`** onde cada um se aplica;
- provar que **nenhum** segredo (`client_secret`, `Authorization`, token) e **nenhum
  payload financeiro completo** (o valor monetário não entra no log — o defeito 27 e a
  decisão de redação estão no `ARCHITECTURE.md` §12) aparece;
- provar que o log sai **JSON em produção e texto em local** (defeito 27: já houve dois
  loggers no mesmo processo; o teste tem de olhar o stdout do processo, não o objeto).

### BLOCO 11c — dashboard e alerta (diferencial do §14)

- versionar em `deploy/observability/` um **dashboard** (JSON do Grafana, ou o formato
  que você justificar) com: operações por status, latência p50/p95/p99
  (`histogram_quantile` sobre `operation_duration_seconds`), retries por estágio, DLQ,
  atraso da outbox (`outbox_pending_age_seconds`), tentativas de publicação,
  divergências de reconciliação e readiness (`up`/`workers`);
- versionar **regras de alerta** com limiar e justificativa: outbox atrasada acima de
  um lease, mensagens na DLQ > 0, divergência de reconciliação > 0, `workers` DOWN;
- **decisão explícita e registrada**: adicionar um serviço Prometheus ao
  `docker-compose.yml` ou apenas entregar dashboard + consultas documentadas? Se
  acrescentar serviço, atualize `.env.example`/compose e o README de execução; se não,
  diga **por quê** (custo × benefício, e o §14 chama isso de diferencial opcional).
- um `deploy/observability/README.md` curto: como subir, quais consultas, o que cada
  alerta significa e **o que fazer** quando ele dispara.

### BLOCO 11d — polimento e composição

- `go mod tidy` (remove `testcontainers-go` e o que ele arrastar) — **e rode a suíte
  inteira depois**, porque tidy mexe em `go.sum`;
- teste de composição Fx **com ciclo de vida real**: `fx.ValidateApp` (já existe),
  `app.Start`/`app.Stop` com um `fx.Lifecycle` observável, provando que (a) os hooks de
  parada rodam na ordem inversa do registro, (b) o `StopTimeout` é respeitado, (c) o
  pool é fechado **depois** dos componentes que o usam. `goleak` é **opcional** — se
  usar, precisa ser determinístico (nada de `time.Sleep` para "deixar goroutine morrer");
- **revisão anti-eliminatórios** do `README.md` §14: um item por linha, com o arquivo/
  teste que o comprova. Não invente evidência: se algo não tem prova, escreva o teste
  ou registre a lacuna.

### BLOCO 11e — fechamento: suíte inteira e documentação

- suíte inteira (unidade e integração) **duas vezes seguidas**, com a saída real;
- `docs/evidencias/FASE_11.md` no modelo das anteriores: ambiente, comandos e saída
  real, o que foi provado por métrica/log/dashboard, os achados, as decisões em tabela,
  as limitações, a rotina de execução — e a seção final **"MATÉRIA-PRIMA PARA OS DOIS
  DOCUMENTOS"** (é o insumo da Fase 12: perguntas→respostas curtas sobre diagnóstico,
  decisões em tabela, o que a fase acrescentou à compreensão);
- atualizar `ARCHITECTURE.md` (§12 com os testes por métrica e os alertas; §16 se
  aparecer limitação nova; §17 com os números novos), `docs/CONFORMIDADE_README.md`
  (§12 e §15), `PROXIMO_PASSO.md` e `PLANO.md`.

---

## 4) O QUE **NÃO** FAZER NESTA FASE

- NÃO editar o `README.md` da raiz (jamais).
- NÃO mudar regra de negócio, lock, transação, contrato HTTP ou nome de fila.
- NÃO criar métrica nova sem o §12 pedir; NÃO renomear métrica existente (quebra painel
  e alerta de quem já usa).
- NÃO usar dublê de PostgreSQL/SQS/Keycloak nos testes de integração.
- NÃO adicionar dependência sem justificar (o `goleak` é opcional e só entra se o teste
  for determinístico).
- NÃO subir um Prometheus/Grafana no compose "por subir": decida, justifique e registre.
- NÃO usar `time.Sleep` mágico: polling com prazo e barreira.
- NÃO usar `Set-Content` para arquivos com acento (PowerShell 5.1 grava ANSI e corrompe —
  já aconteceu nas Fases 9 e 10). Use a ferramenta de edição ou
  `[System.IO.File]::WriteAllText` com `UTF8Encoding($false)`.
- NÃO rodar `git commit` com mensagem longa por heredoc: escreva a mensagem num ARQUIVO
  em `.git/` e use `git commit -F arquivo`. Texto em ASCII.
- NÃO começar a Fase 12 (os dois documentos finais e a entrega): cada uma tem o seu
  momento.

---

## 5) AMBIENTE (detalhes que causam erro se ignorados)

Sempre, em todo comando Go:

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'
$env:GOCACHE='E:\jungleg\.gocache\build'
$env:GOPATH='E:\jungleg\.gocache\gopath'
```

- **Containers:** `jungleg-postgres`, `jungleg-keycloak`, `jungleg-localstack` (os três
  `healthy`). Bancos: `jungleg` (aplicação) e **`jungle_test`** (testes — os testes
  TRUNCAM tabelas, então NUNCA aponte para `jungleg`). Esquema na **versão 5**.
- **Comandos de integração** (sem eles os testes são pulados):

```powershell
$env:TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
$env:OIDC_ISSUER='http://localhost:8080/realms/jungleg'
$env:OIDC_DISCOVERY_URL='http://localhost:8080/realms/jungleg'
$env:OIDC_AUDIENCE='jungleg-api'
$env:SQS_ENDPOINT='http://localhost:4566'
```

- **As filas precisam existir** antes de confiar em teste verde de SQS:
  `docker exec jungleg-localstack awslocal sqs list-queues` → as quatro filas. Vazio = o
  script de init falhou, e o teste falha **por isso**.
- **PowerShell 5.1:** sem `&&`, sem ternário; `make` NÃO existe; `package main` não pode
  ser importado (toda lógica em `internal/`).
- **`bin/migrate.exe` carrega as migrations embutidas** (`go:embed`): só recompile se
  criar migration (esta fase **não** deve criar nenhuma).
- **Armadilhas que já custaram tempo:** `docker exec … awslocal sqs send-message` com
  JSON montado no PowerShell CORROMPE o corpo (use arquivo + `docker cp`); fila FIFO com
  `MessageGroupId` repetido bloqueia as seguintes; `MessageDeduplicationId` repetido é
  deduplicado por 5 min; os contadores `ApproximateNumberOfMessages*` do SQS levam ~1
  min para recalibrar; o LocalStack **não** antecipa entrega com
  `ChangeMessageVisibility(0)`; `TestMain` dos pacotes de integração segura um
  `pg_advisory_lock` (não remova).
- **Portas:** o compose publica 8081; o teste do binário em `internal/app` usa 18099; a
  suíte de três processos da Fase 10 usa 18091+. Escolha uma faixa livre e documente.
- **Para a prova de log em produção**, subir o binário com `APP_ENV=production
  LOG_LEVEL=info` e capturar stdout; para o container, `docker compose build api` e
  `docker run` na rede `jungleg_default` (o `OIDC_JWKS_URL` já está no compose — ver
  defeito 41).

---

## 6) CRITÉRIOS DE ACEITE (não considere a fase pronta sem TODOS)

- [ ] build, `vet` (com e sem a tag), `gofmt` e `go test -race` LIMPOS (unidade e
      integração), com a saída real colada na resposta e nas evidências
- [ ] **12 de 12 métricas** com teste que dispara o gatilho e lê o valor em `/metrics`
- [ ] logs do **processo real** provando os cinco identificadores do §12, sem segredo e
      sem payload financeiro completo, em JSON (produção) e texto (local)
- [ ] dashboard + regras de alerta versionados, com limiar justificado e um
      `deploy/observability/README.md` dizendo o que fazer quando dispara
- [ ] `go mod tidy` aplicado, suíte inteira verde **depois** dele
- [ ] composição Fx com start/stop real: ordem dos hooks, `StopTimeout` e pool fechado
      por último (e `goleak` só se determinístico)
- [ ] revisão anti-eliminatórios do §14 item por item, com evidência (ou lacuna escrita)
- [ ] nenhuma regressão: a suíte inteira (Fases 1–10) continua verde
- [ ] `README.md` da raiz intocado (`git diff -- README.md` vazio)

---

## 7) ENTREGÁVEIS DE DOCUMENTAÇÃO (parte da entrega, não opcional)

1. `docs/evidencias/FASE_11.md` — NOVO, no modelo de `FASE_10.md`: ambiente, saída real,
   o que foi provado por métrica e por log, os achados, as decisões, as limitações, a
   rotina de execução e a **seção final "MATÉRIA-PRIMA PARA OS DOIS DOCUMENTOS"**;
2. `ARCHITECTURE.md` — §12 (tabela métrica→teste, alertas), §16 (limitações novas, se
   houver), §17 (números);
3. `docs/CONFORMIDADE_README.md` — §12 e §14 (e o que a fase fechar da §15);
4. `PROXIMO_PASSO.md` — estado, commits, registro da fase, pendências e checklist da
   Fase 12;
5. `PLANO.md` — checklist da FASE 11 fechado, com os defeitos numerados (o próximo é o
   **42**) e os próximos passos apontando para a Fase 12;
6. `.env.example` / `docker-compose.yml` — só se a fase acrescentar chave ou serviço; se
   não acrescentar, diga isso.

---

## 8) PRIMEIRA RESPOSTA ESPERADA

Não comece a codar. Na primeira resposta:

a) confirme que leu o `README.md` §§12, 14 e 15 e diga, com suas palavras, o que esta
fase exige e o que **não** exige (é fase de observabilidade e polimento: o
comportamento financeiro não muda);
b) confirme o entendimento do método (IA escreve, um bloco por vez, explicar depois no
formato `DECISÃO | MOTIVO | O QUE QUEBRARIA SEM ELA`, validar de verdade, commit por
bloco);
c) confirme os cinco blocos (11a métricas, 11b logs do processo, 11c dashboard/alerta,
11d polimento/composição, 11e fechamento) e diga se enxerga risco ou dependência que a
proposta não cobriu — em especial: **como vai provar cada métrica sem dublê**, **como
vai provar o log do processo** (e não de um logger construído no teste) e **o que vai
decidir sobre Prometheus no compose**;
d) diga exatamente qual será o primeiro passo concreto do bloco 11a;
e) diga, em três linhas, o que entendeu sobre os dois documentos finais (§0b): por que
são entrega obrigatória, o que cada um ensina e qual é a matéria-prima que **esta** fase
entrega para eles.
