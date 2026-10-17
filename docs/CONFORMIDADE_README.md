# CONFORMIDADE COM O ENUNCIADO (`README.md`)

> **O `README.md` da raiz é o ENUNCIADO e a cabeça de tudo neste projeto.** Ele é o
> objetivo e a fonte de verdade: onde este documento e o enunciado discordarem,
> **vale o enunciado**, e a dúvida se resolve relendo o `README.md` — não o plano,
> não o handoff, não este arquivo.
>
> **Como ler os marcadores:**
>
> | | |
> | --- | --- |
> | ✅ | atendido, com prova (teste, arquivo ou saída de comando) |
> | 🟡 | parcial — o que falta está dito, com a fase |
> | ⬜ | pendente, com a fase que entrega |
> | ⚠️ | risco ou decisão aberta que precisa de resposta |
>
> **Estado na última verificação:** Fases 0 a **8** concluídas. A Fase 8 entregou o
> consumidor de SQS com inbox na mesma transação do tratamento, retry por
> visibilidade, DLQ explícita, encerramento seguro e readiness do SQS — provado
> contra o **LocalStack real**. Schema na versão **4**
> (`000004_inbox_attempts`). `go test -race` verde em 17 pacotes de unidade e nos
> 3 de integração.

---

## Resumo executivo

| Área do enunciado | Estado | Onde está |
| --- | --- | --- |
| Dinheiro (`Money`) | ✅ | `internal/domain/money` — 574 casos no domínio |
| Carteira + ledger | ✅ | `internal/domain/{wallet,ledger}` + schema com trigger |
| Transação de aposta (máquina de estados) | ✅ | `internal/domain/wagering` |
| Schema, constraints, imutabilidade | ✅ | `migrations/000001..000003` + `deploy/postgres/verify_constraints.sql` |
| Persistência + transação + lock | ✅ | `internal/infra/postgres` |
| Caso de uso (as 5 operações + referências) | ✅ | `internal/app/wagering` |
| Abertura de carteira (`OPENING`) | ✅ | `internal/app/wagering/opening.go` |
| Leituras + reconciliação | ✅ | `internal/app/wagering/{query,reconcile}.go` |
| Outbox (gravação + ordem) | ✅ | `internal/app/wagering/events.go` + `ClaimBatch` |
| **HTTP (contratos §9)** | ✅ **Fase 7** | `internal/httpapi` — 7 rotas + health + métricas |
| **Autenticação (OIDC)** | ✅ **Fase 7** — era **eliminatório** | `internal/infra/auth` (validação real contra o Keycloak) |
| **Consumidor SQS + inbox** | ✅ **Fase 8** | `internal/consumer` + `internal/app/inbox`, com integração contra o LocalStack |
| **Publisher da outbox + workers** | ⬜ **Fase 9** | `ClaimBatch`/lease/`MarkRetry` prontos; a fila de eventos e a DLQ dela estão provisionadas |
| Prova com 3 instâncias | ⬜ **Fase 10** | |
| Métricas de negócio alimentadas | 🟡 **Fase 11** | 4 das 12 já emitidas **pela borda HTTP** (Fase 7); o resto é 8–11 |
| README da solução + `.env.example` | 🟡 **Fase 12** | `.env.example` existe; ver ⚠️ 3 |

### Os cinco pontos de atenção que valem mais que o resto

1. **⚠️ Autenticação era ELIMINATÓRIO (README §14) e agora está feita.** Todos os
   endpoints de negócio exigem token validado contra o Keycloak real, o
   `providerId` vem do token, e há prova de **nenhum efeito financeiro** em acesso
   negado (contagem de tabelas antes/depois). O risco aberto deixou de ser "não
   existe autenticação" e passou a ser o de qualquer validação local: token
   revogado antes do `exp` não é detectado (sem introspecção — limitação
   registrada no `ARCHITECTURE` §16).
2. **✅ P1 resolvida:** a flag `-health-check` existe, é testada **subindo o
   binário real** e `docker compose up --build` deixa de subir o `api` como
   `unhealthy`.
3. **⚠️ Onde vive o "README da solução"?** O §15 pede que o `README.md` explique
   pré-requisitos, variáveis, filas, migrations, execução, exemplos de chamadas e
   testes. O `README.md` da raiz **é o enunciado** e não pode ser editado. Decisão a
   tomar com o usuário na Fase 12: (a) um `README_SOLUCAO.md` na raiz, (b) um
   `docs/README_SOLUCAO.md` referenciado no topo do `PROXIMO_PASSO`, ou (c) uma
   seção nova no topo do `README.md` preservando o texto do enunciado.
4. **⚠️ "pelo menos três processos independentes" (README §8 e §13.4)** e
   **"carteiras distintas simultaneamente" (§13.3)** ainda não têm prova. As
   garantias existem (lock por carteira + constraints), mas a demonstração é da
   Fase 10.
5. **⚠️ Testes com o IdP:** resolvido na direção certa — a prova principal usa o
   **Keycloak do container** com tokens de `client_credentials` (⚠️ 5 atendido), e
   o servidor OIDC de teste assinando de verdade cobre só o que o Keycloak não
   produz facilmente (assinatura de outra chave, `exp` no passado, `alg` errado,
   `aud` errado, IdP fora do ar).

---

## 1. Objetivo — 🟡

> "oferecer uma API HTTP e um consumidor de mensagens que movimentem carteiras...
> Demonstre que o resultado financeiro continua correto com várias instâncias em
> execução e falhas entre as etapas do processamento."

O **núcleo financeiro** está pronto e provado (domínio + schema + persistência +
caso de uso). A **API HTTP** e o **consumidor** entram nas Fases 7 e 8, e a prova
com várias instâncias na Fase 10. Enquanto isso, a garantia que o enunciado cobra
("não pode gerar movimentação duplicada, saldo negativo ou perda de evento") já
está implementada e testada no nível do caso de uso.

## 2. Autenticação e autorização — ✅ Fase 7 (era eliminatório)

| Exigência | Estado |
| --- | --- |
| OAuth 2.0/OIDC com IdP externo, `client_credentials` | ✅ **Fase 7.** `internal/infra/auth`: discovery + JWKS com cache/rotação, assinatura RS256 conferida com `crypto/rsa`, `iss`/`aud`/`exp`/`nbf` validados. Prova principal com o **Keycloak do container** (`deploy/keycloak/realm.json`), obtendo token por `client_credentials` nos três clients do realm |
| A identidade autenticada determina o `providerId` | ✅ o `providerId` sai do **token**; o corpo só pode confirmá-lo, e divergência responde **403 sem chamar o caso de uso** (teste unitário + integração) |
| Provedores acessam só as próprias transações, inclusive em replay | ✅ no caso de uso e na borda: `:providerId` do path divergente ⇒ 403; operação alheia consultada pela própria rota ⇒ **404** (não 403, que confirmaria a existência); o replay devolve o resultado **daquele** provedor |
| Operações de carteira restritas ao serviço interno | ✅ `POST /wallets`, `GET /wallets*`, `/reconciliation` e a leitura interna por id exigem role `internal`. Teste de integração prova **403 com zero efeito financeiro** (contagem de tabelas antes/depois) |
| Acesso à mensageria controlado por credenciais e políticas do broker | ✅ o acesso é por credenciais do broker (`SQS_ACCESS_KEY_ID`/`SQS_SECRET_ACCESS_KEY`, com as de teste no compose) e as políticas da fila estão declaradas no provisionamento (FIFO, DLQ com `maxReceiveCount`). As validações de DOMÍNIO continuam no consumidor, como o §10 exige |
| Escolha do IdP e modelo de permissões justificados no `ARCHITECTURE.md` | ✅ `ARCHITECTURE.md` §10 (era `[PLANEJADO]`): escolha do Keycloak, roles `internal`/`provider`, `provider_id` do token, duas classes de erro (401 x 503), cache/rotação de chave e as limitações registradas |
| Rejeição de credenciais ausentes, inválidas ou expiradas | ✅ token ausente, malformado, expirado, `nbf` no futuro, emissor errado, audience errada, `kid` inexistente, assinatura de outra chave, `alg: none` e `alg: HS256` — 11 casos, cada um afirmando a causa exata |
| IdP indisponível não pode gerar efeito financeiro | ✅ discovery/JWKS fora ⇒ **503** com `Retry-After`, e teste de integração confere saldo e ledger intactos |

## 3. Ambiente de execução e falhas — 🟡

| Situação que o enunciado exige suportar | Estado |
| --- | --- |
| Recebimento repetido por **HTTP** | ✅ replay por chave/idempotência, e o reenvio simultâneo é repetido e devolve o resultado persistido (provado com 12 disparos) |
| Recebimento repetido por **SQS** | ✅ **Fase 8**: a inbox deduplica por `(consumer_name, messageId)` na MESMA transação do tratamento, com o hash do corpo distinguindo reentrega (mesmo hash) de conflito real (hash diferente). Provado contra o LocalStack: reentrega com o mesmo identificador e a mesma operação enviada duas vezes debitam **uma** vez |
| Reversão que chega antes da transação referenciada | ✅ `PENDING_REFERENCE` persistida + evento + `202`, com `next_resolution_attempt_at` no futuro; a retomada é do worker (Fase 9) |
| Processamento simultâneo de operações da mesma carteira | ✅ lock de linha + versão; provado (cenário do README e 12 disparos) |
| Encerramento abrupto antes ou depois do commit | ✅ antes: rollback (nada fica — provado com a outbox quebrada); depois: o evento fica na outbox e o lease expira (worker é a Fase 9). **No consumo (Fase 8):** a mensagem cujo commit não aconteceu volta para a fila (visibilidade liberada), e a que commitou mas não foi removida é reconhecida pela inbox |
| Publicação repetida de um evento | ✅ `eventId` estável na outbox; republicação com o mesmo id — o publisher é a Fase 9 |
| Indisponibilidade temporária do PostgreSQL | ✅ erros transitórios classificados (`55P03`, deadlocks, conexão) e repetidos até 3 vezes; esgotado ⇒ erro transitório (503 na borda) |
| Indisponibilidade temporária do SQS | 🟡 **consumo** ✅ (o `Receive` que falha registra e espera; falha de tratamento adia por `ChangeMessageVisibility` com backoff exponencial). **Publicação** ⬜ Fase 9 |

## 4. Stack — ✅ (com duas observações)

| Exigência | Estado |
| --- | --- |
| Go com versão no `go.mod` e no Dockerfile | ✅ (Fase 0 corrigiu o Dockerfile que não compilava) |
| Go Modules versionados | ✅ `go.mod` + `go.sum` |
| Uber Fx com `fx.Module`/`Provide`/`Invoke` | ✅ `internal/app/{app,modules}.go` — módulos `config`, `infra` (pool + Unit of Work + verificador OIDC + health checker), `usecase` (`appwagering.Service`), `auth` (autenticador + health checker) e `httpapi` (rotas, servidor, handlers) |
| `fx.Lifecycle` com validação, cancelamento e shutdown | ✅ o HTTP (`OnStart` detecta erro de bind, `OnStop` faz `Shutdown` com prazo) e o **fechamento do pool** por `Lifecycle`, na ordem inversa do registro (servidor para antes, dependências fecham depois). **P7 resolvida**: `StopTimeout` vem de `HTTP_STOP_TIMEOUT`, validado contra o `ShutdownTimeout`. Shutdown dos workers entra nas Fases 8–9 |
| HTTP (`net/http` ou roteador) | ✅ `chi` + middlewares (`RequestID`, `RealIP`, `AccessLog`, `Recoverer`, `Timeout`, `MaxBody`) — com a ordem corrigida (P2) |
| IdP OIDC | ✅ Keycloak no compose **e middleware implementado** (`internal/infra/auth`): discovery, JWKS com cache/rotação, RS256, `iss`/`aud`/`exp`/`nbf` |
| PostgreSQL | ✅ |
| SQS com LocalStack | ✅ filas FIFO + DLQ + redrive provisionadas (`deploy/localstack/init-queues.sh`) |
| Docker Compose | ✅ `postgres`, `keycloak`, `localstack`, `localstack-init`, `migrate`, `api` |
| Migrations versionadas com aplicação **e reversão** documentadas | ✅ 3 migrations, `up`/`down`/`version` pelo `cmd/migrate`; a condição da reversão da 000002 está no próprio `.down.sql` |
| `pgx` com SQL explícito, transações/locks/constraints verificáveis | ✅ documentado em `ARCHITECTURE.md` §1, §6 e §7 |
| Biblioteca de banco, mapeamento de `Money` e delimitação da transação documentados | ✅ `ARCHITECTURE.md` §2 (Money), §6 (transação) e §17 (como validar) |
| Domínio independente de Fx, HTTP, SQS e persistência | ✅ verificado com `go list -deps`: nenhum Fx, `net/http`, `chi`, `pgx` ou SDK da AWS. **Nuance honesta:** aparece `database/sql/driver` porque `github.com/google/uuid` implementa `sql.Scanner`/`driver.Valuer` — é o pacote de **interfaces**, não um driver; e o caso de uso (`app/wagering`) não importa `internal/infra` |

## 5. Garantias obrigatórias — ✅ (6 de 8 com prova completa)

| # | Garantia | Estado |
| --- | --- | --- |
| 1 | Dinheiro nunca em `float32`/`float64` | ✅ `int64` em unidades mínimas, `Money` sem aritmética de ponto flutuante, parsing por dígitos, JSON como **string** decimal. Há teste de estilo em `money` que recusa `float`, `math` e `strconv.ParseFloat`. **Nuance:** `float64` aparece em `internal/observability` (duração de log e buckets do Prometheus) — não é caminho monetário. O `PLANO.md` E3 dizia "grep vazio em `internal/`", e o certo é "vazio nos caminhos monetários" |
| 2 | Idempotência persistente, sobrevive a reinício | ✅ índices únicos no banco + decisão de replay no caso de uso; provado com um `Service` e um Unit of Work **novos** devolvendo o resultado original |
| 3 | Invariantes financeiras no banco, independentes de lock e da dedup do SQS | ✅ 29 CHECK, 4 FK, 2 UNIQUE, 3 índices únicos parciais, 5 triggers; 32 operações inválidas testadas e recusadas |
| 4 | Evento só publicado depois do commit | ✅ os eventos entram na outbox **na mesma transação**; nada publica inline (o publisher é a Fase 9) |
| 5 | Ledger append-only | ✅ trigger que recusa `UPDATE`/`DELETE` + repositório sem método de alteração |
| 6 | Carteiras independentes em paralelo; sem lock global | ✅ o lock é **de linha** (`SELECT ... FOR UPDATE` na carteira), não de tabela. 🟡 Falta a prova explícita de "carteiras distintas avançam juntas" (Fase 10) |
| 7 | Sem lost update | ✅ provado: dois débitos concorrentes de 100 e 200 sobre 1000 terminam em **700** e versão 3 |
| 8 | Unicidade, não negatividade e imutabilidade impostas pelo schema | ✅ |

## 6. Modelo de domínio — ✅ (5 subseções)

| Subseção | Estado |
| --- | --- |
| Encapsulamento, construtores com validação, transições explícitas, separação criação/reidratação, erros classificáveis por `errors.Is`, `context` nas operações | ✅ Fases 1–3; `Rehydrate` não reaplica movimento nem transição |
| 6.1 `Money` (string decimal, zero por moeda, soma/sub/negação/comparação/serialização, escala 2, ISO 4217, recusa de `NaN`/`Inf`/notação científica/escala excedente/negativo, sem arredondar em silêncio, overflow tratado, moedas compatíveis) | ✅ 384 casos, 97,9% de cobertura |
| 6.2 `Wallet` (`(playerId, currency)` único, débito sem saldo negativo, moeda da movimentação = da carteira, lançamento na mesma transação, versão 1 inicial e só incrementa com mudança de saldo, sem descartar atualização confirmada) | ✅ 99,0% de cobertura |
| 6.3 `WagerTransaction` (todos os campos persistidos, `PENDING` inicial, transições validadas no domínio, estados terminais, replay consulta o resultado, `OPENING` reservado ao interno, schema distingue interno/externo e impede crédito inicial duplicado) | ✅ 100% de cobertura. O `OPENING` veio de fora é recusado (`OPERATION_NOT_ALLOWED`). O crédito inicial duplicado é impedido por índice único parcial **e** testado |
| 6.4 `WalletLedgerEntry` (campos, imutável, valida `after = before ± amount`, unicidade `(walletId, transactionId)`, `LOSS` e rejeição sem lançamento) | ✅ |
| 6.5 Inbox/outbox (tabelas e comportamento) | ✅ schema e repositórios **e o fluxo da inbox** (Fase 8): registro e conclusão na MESMA transação do tratamento durável, com `body_hash` distinguindo reentrega de conflito e unicidade `(consumer_name, message_id)`. **E a outbox publica** (Fase 9): worker separado, lease, `attempts`, próximo envio e publicação — exatamente as colunas que a tabela do §6.5 pede |

**Um ponto que merece destaque:** o enunciado aceita "operações sem dependências podem
ser concluídas de forma síncrona, sem commit intermediário de aceite". Foi o caminho
escolhido: **nunca existe `PENDING` commitado** para operação externa — o `INSERT`
grava `PENDING` e o `Save` grava o estado definitivo na mesma transação. O `PENDING`
durável fica restrito a `PENDING_REFERENCE` (que É durável e retomável).

⚠️ **Consequência para a Fase 9:** como nenhum `PENDING` é confirmado, o
`PendingReaper` é código de **segurança** (README §6.3) cujo gatilho não ocorre por
caminho normal. Ele está implementado e provado por inserção direta, e é o que
assume no dia em que existir aceite assíncrono.

## 7. Operações e referências — ✅ (o worker chegou na Fase 9)

| Exigência | Estado |
| --- | --- |
| `BET` débito com saldo suficiente | ✅ |
| `WIN` crédito, podendo referenciar aposta da mesma rodada | ✅ referência **informativa**: conferida quando resolve, não bloqueia quando não |
| `LOSS` sem movimentação, `amount` = `"0.00"`, sem ledger e sem mudar a versão | ✅ (e produz `WagerTransactionProcessed`, sem `WalletBalanceChanged`) |
| `REFUND` devolve integralmente uma `BET` processada | ✅ valor igual ao referenciado; referência obrigatória |
| `ROLLBACK` desfaz movimento de `BET`/`WIN`/`REFUND` | ✅ direção contrária ao original; `ROLLBACK` de `BET` já estornada ⇒ `ALREADY_REVERSED` |
| Referência resolvida por `(providerId, referenceExternalTransactionId)` | ✅ |
| Concordância em provedor, jogador, carteira, moeda e rodada; valor igual | ✅ `REFERENCE_MISMATCH` |
| Zero aceito no saldo inicial e em `LOSS`; os outros exigem valor > 0 | ✅ validado no domínio e por CHECK no banco |
| Sem duas reversões bem-sucedidas do mesmo tipo | ✅ índice único parcial + leitura prévia (`ALREADY_REVERSED`) |
| Documentar `REFUND` + `ROLLBACK` sobre a mesma aposta | ✅ `ARCHITECTURE.md` §9 e `docs/evidencias/FASE_6.md` §7: `ROLLBACK` da `BET` estornada é recusado; desfazer o estorno é `ROLLBACK` do `REFUND`, que debita |
| Reversão que exigiria mais saldo: código DIFERENTE do de aposta sem saldo | ✅ `ROLLBACK_INSUFFICIENT_FUNDS` × `INSUFFICIENT_FUNDS`, separados pelo tipo |
| `PENDING_REFERENCE` persistida, worker com backoff exponencial **após reinício**, TTL/máx. de tentativas, e comportamento quando a referência existe mas está pendente ou terminou mal | 🟡 persistência, backoff inicial e os dois comportamentos ✅; o **worker** com backoff/TTL é a Fase 9 (`PLANO.md` A7 define 20 tentativas/24h) |
| Toda rejeição com `failureCode` estável e documentado | ✅ 17 códigos na tabela A8 + os dois novos (`WALLET_PLAYER_MISMATCH`, `TRANSACTION_NOT_FOUND`), distinguindo corrigível de definitivo |

## 8. Concorrência — 🟡

| Exigência | Estado |
| --- | --- |
| Coordenação por carteira, com estratégia justificada | ✅ `READ COMMITTED` + `SELECT ... FOR UPDATE` + versão, justificado em `ARCHITECTURE.md` §6 |
| Demonstração com **três processos independentes** | ⬜ Fase 10 (3 containers `api`) |
| Teste obrigatório: 100.00 com duas apostas simultâneas de 80.00 → uma processada, uma rejeitada, saldo 20.00 e **um** débito | ✅ provado contra o banco real (`TestCenarioDoREADMEDuasApostasDe80Sobre100`) |
| Reenvios não alteram o resultado | ✅ provado (os dois reenvios depois da corrida) |
| Carteiras diferentes continuam em paralelo | 🟡 garantido por construção (lock de linha); falta a prova explícita (Fase 10) |

## 9. Contratos HTTP — ✅ Fase 7

| Exigência | Estado |
| --- | --- |
| `POST /wallets` com `{playerId, initialBalance}` → `{id, playerId, balance, version}` | ✅ **implementado** em `POST /wallets` (P4 resolvida), role `internal`, com **versão 1** na abertura, lançamento partindo de `0.00` e dois eventos no mesmo commit; zero ⇒ sem `OPENING`/ledger/eventos; duplicado ⇒ `409`. Reenvio ⇒ `idempotentReplay: true` |
| `GET /wallets/:id`, `/ledger?cursor&limit`, `/wagering/transactions/:id`, `/providers/:providerId/wagering/transactions/:externalTransactionId` | ✅ os quatro endpoints, com cursor opaco, ordenação estável, `limit` padrão 50 e teto 200 (e `limit` não numérico ⇒ 400), isolamento por provedor e `nextCursor: null` no fim |
| `POST /wagering/transactions` com `Idempotency-Key` obrigatório | ✅ implementado. Chave ausente ⇒ `400 IDEMPOTENCY_KEY_REQUIRED` sem chamar o caso de uso; o servidor **nunca** substitui a chave recebida |
| Hash determinístico dos campos de negócio, com JSON canônico, **excluindo** chave de idempotência e metadados de transporte; equivalência HTTP × SQS documentada | ✅ implementado e documentado (`payload.go`). A **prova** da equivalência HTTP × SQS é a Fase 8 (o `Request` é o mesmo tipo nas duas portas) |
| Replay devolve o saldo do processamento original | ✅ provado na borda: `200` com `idempotentReplay: true` e o saldo de 975.00 mesmo depois de a carteira ter mudado |
| Documentar códigos e corpos para entrada inválida, conflito, rejeição, pendente e indisponibilidade | ✅ implementado **e documentado** (`ARCHITECTURE.md` §10, com as duas tabelas): `201`/`200`/`202`/`422`/`409`/`404`/`400`/`403`/`503`/`500`, com `code` e `failureCode` estáveis |
| Reconciliação: `storedBalance`, `calculatedBalance`, `difference`, `consistent`, `checkedEntries`, visão consistente, sem alterar saldo | ✅ implementado e provado na borda (975.00/975.00/0.00/true/2), com **log de divergência** e métrica `wager_reconciliation_divergences_total` |
| `GET /health/live` e `/health/ready` (readiness de PostgreSQL e SQS) | ✅ **P5 fechada**: PostgreSQL, IdP e **SQS** (a fila específica, por `GetQueueAttributes`) registrados no grupo `health_checkers`. Um readiness que respondesse UP com a fila ausente mandaria tráfego para uma instância que não consome nada |
| `docker compose up --build` sobe o `api` saudável | ✅ P1 resolvida: a flag `-health-check` existe e foi provada **subindo o binário real** |

## 10. Consumidor SQS — ✅ Fase 8

| Exigência | Estado |
| --- | --- |
| Provisionar `wager-transactions.fifo` + DLQ com redrive | ✅ FIFO, `ContentBasedDeduplication=false`, `VisibilityTimeout=60`, `ReceiveMessageWaitTimeSeconds=20`, DLQ com `maxReceiveCount=5`. **Corrigido na Fase 8:** o script não tinha shebang nem bit de execução, e as filas **nunca** eram criadas (o LocalStack registrava `Exec format error` no stage READY) — provado reiniciando o container |
| Corpo da mensagem com envelope + `data` | ✅ `internal/app/inbox` decodifica com a MESMA régua estrita do HTTP (campo desconhecido é erro) e produz o mesmo `Request` do caso de uso |
| HTTP e SQS compartilhando o caso de uso e as garantias | ✅ **provado**: `TestSQSESHTTPCompartilhamAIdempotencia` envia a MESMA operação pelas duas portas e confere **um** débito; `TestSQSConcorrenteComHTTPNaoDuplicaDebito` faz as duas ao mesmo tempo |
| `messageId` como identidade durável + hash em reentregas; remover da fila só depois do commit; rejeição terminal remove; transitório faz retry; DLQ; `SIGTERM` libera visibilidade | ✅ identidade = `MessageId` do broker (o campo do envelope é opcional e conferido); `body_hash` distingue reentrega de conflito; o `Delete` acontece **depois** do commit; rejeição terminal remove; transitório adia por `ChangeMessageVisibility` com backoff; permanente vai para a DLQ; no encerramento o que não deu tempo tem a visibilidade liberada |
| Documentar `MessageGroupId`/`MessageDeduplicationId` e validar a concorrência HTTP × SQS | ✅ `ARCHITECTURE.md` §11 (tabela de identificadores) e os dois testes de concorrência entre portas |

## 11. Publicação com transactional outbox — ✅ Fase 9

| Exigência | Estado |
| --- | --- |
| Estado, saldo, ledger, inbox e eventos confirmados atomicamente | ✅ estado + saldo + ledger + outbox no mesmo commit (Fases 6/8), e a inbox junto na entrada por SQS |
| Worker separado publica os pendentes, com múltiplos publishers, disputa por registros, backoff e recuperação de trabalho abandonado | ✅ **`internal/worker.OutboxPublisher`**: `ClaimBatch` (`FOR UPDATE SKIP LOCKED` + lease) → publicar **fora** da transação → `MarkPublished`; falha → `MarkRetry` com backoff exponencial + teto + jitter **dentro** do teto; teto esgotado ou recusa permanente do destino → **quarentena**. Provado com **dois publishers concorrentes** (`TestDoisPublishersDisputamAMesmaOutbox`) e com lease vencido |
| Recuperação entre commit e publicação, e entre publicação e confirmação; republicação preservando `eventId` | ✅ **as duas provas contra PostgreSQL + LocalStack reais**: `TestQuedaEntreCommitEPublicacaoOutraInstanciaAssume` e `TestQuedaEntrePublicacaoEMarkPublishedRepublicaComOMesmoEvento` (4 mensagens, 2 `eventId`, corpo **idêntico** em cada republicação) |
| **Destino provisionado e contratos de roteamento e consumo documentados** | ✅ `wallet-events.fifo` + `wallet-events-dlq.fifo` provisionadas no LocalStack (FifoQueue, `ContentBasedDeduplication=false`, visibility 30, redrive 5) e o contrato em `ARCHITECTURE.md` §11: `MessageGroupId`/`MessageDeduplicationId`/atributos do lado da publicação e as 7 regras do lado do consumo (dedup durável por `eventId` **além** da janela de 5 min, ordem por `walletVersion`, DLQ sem bloquear o grupo…) |
| Eventos exigidos: `WagerTransactionProcessed` (inclui `LOSS`), `WagerTransactionRejected`, `WalletBalanceChanged`, `WagerTransactionPendingReference` | ✅ os quatro são gravados pelo caso de uso **e agora publicados**; provado lendo de volta da fila (`correlationId`, `aggregateId`, os 7 campos do `WalletBalanceChanged`) |
| Envelope com `eventId`, `eventType`, `aggregateId`, `correlationId`, `causationId?`, `occurredAt`, `version`, `data` | ✅ e o `correlationId` **sobrevive à pendência** (migration 000005): os eventos emitidos pelo worker carregam o da requisição original |
| Tipo e versão definidos pelo construtor; timestamps UTC; valores em string decimal; snapshot imutável | ✅ o corpo publicado é o `payload` JSONB **byte a byte** — conferido no teste de integração |
| Ordem de publicação por carteira | ✅ o lote é lido por `sequence` e o `MessageGroupId` é a carteira; ordem conferida lendo a fila. Limite conhecido: com mais eventos da mesma carteira do que o batch, dois publishers podem dividir o grupo (registrado no `ARCHITECTURE.md` §16) |

### O que a Fase 9 acrescentou ao worker de referências (README §7)

| Exigência | Estado |
| --- | --- |
| `PENDING_REFERENCE` retomado com backoff exponencial, inclusive após reinício | ✅ `internal/worker.ReferenceWorker` + `Service.Retomar`; tentativa e instante **persistidos** |
| Máximo de tentativas ou TTL → `REJECTED` com código de referência não encontrada + evento | ✅ `REFERENCE_MAX_ATTEMPTS` / `REFERENCE_PENDING_MAX_AGE` → `REFERENCE_NOT_FOUND` + `WagerTransactionRejected` |
| Explicar o comportamento quando a referência existe mas está pendente ou terminou sem sucesso | ✅ referência não terminal ⇒ continua pendente; terminou em `REJECTED`/`FAILED` ⇒ `REFERENCE_NOT_PROCESSABLE` (`ARCHITECTURE.md` §9) |
| Todo `PENDING` confirmado deve ter retomada durável | ✅ `PendingReaper` (mecanismo provado; hoje o estado é inalcançável por caminho normal — ver `ARCHITECTURE.md` §16) |
| Nenhuma transação `PENDING` presa para sempre | 🟡 o reaper dá desfecho; **exceção conhecida**: uma linha inconsistente que o ledger recusa é repetida indefinidamente em vez de virar `FAILED` (§16) |

## 12. Observabilidade — ✅

| Exigência | Estado |
| --- | --- |
| Logs JSON com `correlationId`, `messageId`, `transactionId`, `walletId`, `providerId`; sem credenciais nem payload financeiro completo | ✅ borda HTTP (Fase 7), consumo (Fase 8) e **laços (Fase 9)**: `outbox_evento_publicado`, `outbox_publicacao_reagendada`, `outbox_evento_em_quarentena`, `outbox_atrasada`, `reference_resolvida`/`_ainda_pendente`/`_rejeitada`/`_ignorada`, `pending_reaper_*`, `worker_morreu_reiniciando`, `workers_*`. Valor monetário **não** entra no log |
| Métricas: resultados por status, duplicatas, retries, DLQ, conflitos, **atraso da outbox**, latência, divergências de reconciliação | ✅ **12 de 12** emitidas. A Fase 9 fechou as 5 que faltavam: `wager_outbox_publish_total{result}`, `wager_outbox_pending_age_seconds` (atraso da outbox via `PendingStats`), `wager_outbox_publish_attempts`, `wager_reference_resolution_total{result}` e os `retries_total{stage}` dos laços |
| Health checks definidos na API | ✅ `/health/live` e `/health/ready` com **PostgreSQL, IdP, SQS e `workers`** — o `workers` responde DOWN quando um laço ligado não está rodando (`{"oidc":"UP","postgres":"UP","sqs":"UP","workers":"UP"}` no binário real) |

## 13. Verificação obrigatória — 🟡

| Bloco | Estado |
| --- | --- |
| **Testes unitários** (parsing e operações de `Money`, escala, limites, entradas inválidas, moedas, invariantes da carteira, transições, regras dos cinco tipos, conflito de payload, política de zero, abertura interna com metadados e eventos) | ✅ 574 casos no domínio + 56 no caso de uso + **49 em `internal/infra/auth`** + **67 em `internal/httpapi`** + **45 da Fase 8** (26 inbox + 19 consumer) + **38 da Fase 9** (23 em `internal/worker`: publicador 10, referências/reaper 7, runner 6; 10 de `Retomar` em `internal/app/wagering`; 4 de config; 1 de composição do Fx) |
| **Testes de integração** com PostgreSQL, IdP e LocalStack em containers reais | 🟡 PostgreSQL ✅ real (migrations, constraints, imutabilidade do ledger, atomicidade, outbox concorrente, retry, lease — 29 casos) + **IdP ✅ real** (30 casos na borda HTTP com token por `client_credentials` + 2 do binário real) + LocalStack ✅ real (9 funções de consumo, **8 funções de publicação e recuperação**) |
| Verificação da composição Fx, início, encerramento e liberação de recursos dos workers | ✅ `TestValidateAppComposition` (grafo inteiro, com e **sem** os workers ligados), `TestBuildAppFailsWithInvalidConfig`, o teste do binário real (encerramento e liberação do pool) e, na Fase 9, **6 casos do runner** (sobe, reinicia o laço morto, readiness DOWN/UP, espera o trabalho em voo, grace da config) + o encerramento do consumidor |
| **Não substituir toda a infraestrutura por mocks** | ✅ os testes de integração usam PostgreSQL, Keycloak e **LocalStack** reais; os dublês existem só nos unitários, e registram chamadas para as asserções incluírem a **ausência** delas |
| Autenticação/autorização | ✅ **Fase 7** (11 formas de token inválido, isolamento por corpo/path/leitura/replay, contagem de tabelas antes/depois) |
| Concorrência e recuperação (8 cenários) | 1 mesma aposta 50× ✅ (12 disparos); 2 disputa 80+80 ✅; 3 carteiras distintas ⬜ Fase 10; 4 três instâncias ⬜ Fase 10; 5 interrupção do consumidor 🟡 (encerramento + reentrega provados; falta o processo novo matando o antigo — Fase 10); **6 dois publishers ✅ Fase 9**; **7 `REFUND`/`ROLLBACK` antes da referência ✅ Fase 9 (as duas metades: resolução posterior e rejeição por expiração)**; 8 reinício preservando idempotência/pendências 🟡 (pendências e tentativas estão no banco e o reaper/worker as retomam; falta o cenário com processo novo — Fase 10) |
| Conferência final: saldo armazenado == soma de créditos − débitos | ✅ provado em teste **e** no binário real (`reconciliation` com 4 lançamentos e diferença 0.00) |
| `go test -race` nos testes aplicáveis | ✅ verde nos dois modos (17 pacotes na unidade, 18 com a tag) |

## 14. Critérios de avaliação e eliminatórios — ✅

| Eliminatório | Estado |
| --- | --- |
| Ausência de autenticação efetiva nos endpoints de negócio | ✅ **Fase 7.** Todos os endpoints de negócio exigem token validado contra o Keycloak real; não há rota de negócio registrada sem autenticador |
| Acesso não autorizado a operações/transações | ✅ isolamento no caso de uso **e** na borda (corpo, path, leitura e replay), com prova de que o acesso negado não produz efeito financeiro |
| Cálculo monetário em ponto flutuante | ✅ nenhum nos caminhos monetários |
| Saldo negativo por concorrência | ✅ impedido no domínio **e** por CHECK no banco |
| Movimentação duplicada | ✅ índice único + idempotência persistente, alcançável pelas duas portas — e agora também **sem** duplicar na retomada de referência (lock da linha + estado/tentativa esperados) |
| Idempotência restrita à memória | ✅ persistida no banco; **sobrevive ao reinício** (tentativas e pendências no banco) |
| Dependência de uma única instância para funcionar | ✅ estado todo no PostgreSQL; dois publishers concorrentes provados (Fase 9); a prova com 3 instâncias é a Fase 10 |
| **Publicação anterior ao commit** | ✅ impossível por construção (outbox na mesma transação) **e agora provado no caminho de saída**: o publisher só lê o que já foi commitado |
| Ausência de ledger auditável | ✅ ledger append-only com dois saldos por lançamento |
| Substituição integral de PostgreSQL, SQS e IdP por mocks nos testes | ✅ os três são **reais** nos testes de integração (PostgreSQL, Keycloak e LocalStack); os dublês existem só nos unitários |

**Observabilidade (5 pontos):** 🟡→✅ no que a Fase 9 fecha — log estruturado com
os identificadores de rastreio (inclusive na retomada), 12/12 métricas, health
checks com a dependência `workers`. Tracing/dashboards continuam fora (diferencial
opcional do enunciado).

## 15. Entrega — 🟡 Fase 12

| Exigência | Estado |
| --- | --- |
| Código, migrations, Docker Compose e instruções para reproduzir de um checkout limpo | 🟡 código, migrations e compose ✅; **P1 resolvida**, então `docker compose up --build` sobe o `api` saudável. As instruções consolidadas são a Fase 12 |
| `README.md` da solução com pré-requisitos, variáveis, filas, migrations (aplicar/reverter), execução, exemplos e testes | 🟡 os exemplos autenticados já existem em `docs/evidencias/FASE_7.md` §4; a decisão de **onde** esse arquivo vive é a ⚠️ 3 da Fase 12 |
| `.env.example` sem segredos reais | ✅ completo (HTTP, banco, OIDC, SQS, outbox, workers) — e agora com `OIDC_HTTP_TIMEOUT`, `OIDC_TOKEN_URL` e `HTTP_STOP_TIMEOUT` |
| Provisionamento automático do IdP, identidades de teste e instruções dos fluxos autenticados | 🟡 realm, clients, roles, mapper e usuários de teste ✅; o fluxo `client_credentials` é exercitado pelos testes (com os três clients do realm); as instruções ⬜ Fase 12 |
| `ARCHITECTURE.md` com decisões e limitações | ✅ atualizado até a **Fase 9** (§8 publicação da outbox com lease e quarentena, §11 contrato de roteamento/consumo de `wallet-events.fifo`, §12 observabilidade completa, §13 recuperação, §15 defeitos 37–39, §16 limitações) |
| Comandos documentados: `docker compose up --build`, `go test ./...`, `go test -race ./...`, `go vet ./...` | 🟡 documentados em `ARCHITECTURE.md` §17 e nas evidências; a consolidação é a Fase 12 |
| Documentar como preparar dependências de teste, integração, múltiplas instâncias e simulações de falha; informar as build tags | 🟡 a tag `integration` está documentada (§17 e `FASE_8.md` §10), inclusive as dependências (PostgreSQL, Keycloak e **LocalStack com as filas provisionadas** — e o §17 do ARCHITECTURE manda conferir `list-queues`, porque uma falha silenciosa do script de init faz a suíte parecer verde); múltiplas instâncias é a Fase 10 |
| Código formatado com `gofmt` e dependências reproduzíveis | ✅ `gofmt -l` limpo e `go.sum` versionado. A Fase 8 trouxe a **primeira dependência nova desde a Fase 0** (`aws-sdk-go-v2/service/sqs v1.52.4`, com o núcleo do SDK e o `credentials`); a Fase 7 não trouxe nenhuma. Falta o `go mod tidy` para remover `testcontainers-go`, Fase 12 |

---

## Divergências e interpretações registradas (para o avaliador)

Nenhuma contraria o enunciado; todas são escolhas onde ele deixou margem. Estão
detalhadas em `docs/evidencias/FASE_6.md` §8, `docs/evidencias/FASE_7.md` §8 e no
`ARCHITECTURE.md` §9, §10 e §11 e `docs/evidencias/FASE_8.md` §8:

1. Referência em `BET`/`WIN`/`LOSS` é **informativa** e não bloqueia a operação.
2. `WALLET_PLAYER_MISMATCH` e `TRANSACTION_NOT_FOUND` são códigos **novos**, além da
   tabela do PLANO — o enunciado exige código estável e documentado, não uma lista
   fechada.
3. Rejeição de negócio é **desfecho** no caso de uso, não `error` (o status HTTP é
   decisão da borda).
4. Operação de outro provedor responde **não encontrado**, e não `403`.
5. O replay da abertura de carteira devolve a carteira **como ela está** (recurso
   mutável); o replay de operação devolve o resultado original (fato imutável).
6. **Nunca existe `PENDING` commitado** para operação externa — o enunciado permite
   conclusão síncrona; o `PENDING` durável é só `PENDING_REFERENCE`.
7. `sequence` global (e não por carteira) na outbox: a ordem que importa é dentro do
   grupo, e a global a preserva.
8. `POST /wallets` **não** tem header de idempotência no enunciado: a chave é
   derivada de `(playerId, moeda)` quando o cliente não manda uma, e a do cliente é
   usada como veio quando existe.
9. Na **rejeição de negócio** (422), o corpo é o do desfecho (com `transactionId` e
   `failureCode`), e não o envelope de erro — a rejeição é um fato auditável, não um
   erro de transporte. As situações continuam distinguíveis pelo contrato.
10. `GET /wagering/transactions/{id}` exige role `internal`, porque o identificador
    é interno; o provedor consulta pela rota dele.
11. O token do serviço interno não é aceito nas rotas de provedor (403): sem
    `provider_id` não existe provedor autorizado.
12. **A identidade durável da mensagem é o `MessageId` do BROKER**, e não o
    `messageId` do envelope: quem atribui esse valor é o broker, no envio, e o
    produtor não tem como conhecê-lo antes de enviar. O campo do envelope é
    **opcional** e, quando presente, é conferido contra o da entrega — divergência
    é mensagem inválida (permanente → DLQ).
13. **A DLQ recebe também o erro permanente de forma EXPLÍCITA** (`Send` +
    `Delete`), além do redrive do broker: numa fila FIFO, uma mensagem envenenada
    que gira até o `maxReceiveCount` **bloqueia o grupo inteiro**.
14. **Erro não classificável é retentável**, não vai direto para a DLQ: a DLQ é o
    fim da linha, e o `maxReceiveCount` já limita as tentativas.
15. **"Não republicar mais" é QUARENTENA, não descarte** (Fase 9): o evento
    continua pendente, com `last_error` e `available_at` no prazo longo
    (`OUTBOX_PARKED_BACKOFF`). Nada é apagado e nada é marcado como publicado. O
    README não define o que fazer com o evento que esgota as tentativas; a escolha
    preserva o dado e deixa o atraso da outbox denunciar.
16. **O `MessageDeduplicationId` do evento é por TENTATIVA, não por `eventId`**
    (Fase 9): com o `eventId`, o broker engoliria a republicação dentro de 5
    minutos e o README §11 deixaria de valer. A deduplicação que vale é a do
    consumidor, por `eventId`, e ela é durável.
17. **Tentar antes de expirar** (Fase 9): se a referência chegou, a operação
    conclui mesmo depois do TTL. A expiração é para o que **não** resolve.
18. **O `FAILED` não emite `WagerTransactionRejected`** (Fase 9): a tabela do §11
    define esse evento como rejeição de **negócio**; dado inconsistente não é
    decisão de negócio e fica registrado no estado, não num evento inventado.
19. **As tentativas de referência incluem a do intake** (Fase 9): a operação que
    entra em `PENDING_REFERENCE` grava `attempt = 1`, então
    `REFERENCE_MAX_ATTEMPTS = 1` significa "só a do intake" e a primeira retomada
    expira. O default (20) e o `REFERENCE_PENDING_MAX_AGE` (2m) tornam o TTL por
    idade o que decide na prática.

## O que ainda NÃO existe (lista direta, sem rodeio)

- **Consumidor de `wallet-events.fifo`**: nesta fase o evento é **publicado**, e o
  contrato de consumo está documentado (`ARCHITECTURE.md` §11). O consumidor (com
  dedup durável por `eventId`) é escopo das Fases 10–11.
- **3 instâncias de verdade** e os cenários T1–T5/T8 da Fase 10.
- **Dashboards e tracing** (diferenciais opcionais do §14).
- **Contador durável de falhas do reaper**: uma linha `PENDING` inconsistente é
  repetida indefinidamente em vez de virar `FAILED` (`ARCHITECTURE.md` §16).
- **O README da solução** (o enunciado ocupa esse nome) — a consolidação é a
  Fase 12.
