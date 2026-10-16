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
> **Estado na última verificação:** Fases 0 a **7** concluídas (blocos 7a–7e:
> autenticação OIDC, endpoints do §9, composição Fx, pendências P1/P2/P4/P5/P7 e
> testes com Keycloak e PostgreSQL reais). Schema na versão **3**. `go test -race`
> verde em 15 pacotes de unidade e nos 3 de integração.

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
| **Consumidor SQS + inbox** | ⬜ **Fase 8** | filas provisionadas; tabela `inbox_messages` existe |
| **Publisher da outbox + workers** | ⬜ **Fase 9** | `ClaimBatch`/lease/`MarkRetry` prontos |
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
| Acesso à mensageria controlado por credenciais e políticas do broker | ⬜ Fase 8 (filas FIFO + DLQ já provisionadas; o consumidor entra lá) |
| Escolha do IdP e modelo de permissões justificados no `ARCHITECTURE.md` | ✅ `ARCHITECTURE.md` §10 (era `[PLANEJADO]`): escolha do Keycloak, roles `internal`/`provider`, `provider_id` do token, duas classes de erro (401 x 503), cache/rotação de chave e as limitações registradas |
| Rejeição de credenciais ausentes, inválidas ou expiradas | ✅ token ausente, malformado, expirado, `nbf` no futuro, emissor errado, audience errada, `kid` inexistente, assinatura de outra chave, `alg: none` e `alg: HS256` — 11 casos, cada um afirmando a causa exata |
| IdP indisponível não pode gerar efeito financeiro | ✅ discovery/JWKS fora ⇒ **503** com `Retry-After`, e teste de integração confere saldo e ledger intactos |

## 3. Ambiente de execução e falhas — 🟡

| Situação que o enunciado exige suportar | Estado |
| --- | --- |
| Recebimento repetido por **HTTP** | ✅ replay por chave/idempotência, e o reenvio simultâneo é repetido e devolve o resultado persistido (provado com 12 disparos) |
| Recebimento repetido por **SQS** | ⬜ Fase 8 (a deduplicação por inbox tem tabela e repositório prontos) |
| Reversão que chega antes da transação referenciada | ✅ `PENDING_REFERENCE` persistida + evento + `202`, com `next_resolution_attempt_at` no futuro; a retomada é do worker (Fase 9) |
| Processamento simultâneo de operações da mesma carteira | ✅ lock de linha + versão; provado (cenário do README e 12 disparos) |
| Encerramento abrupto antes ou depois do commit | ✅ antes: rollback (nada fica — provado com a outbox quebrada); depois: o evento fica na outbox e o lease expira (worker é a Fase 9) |
| Publicação repetida de um evento | ✅ `eventId` estável na outbox; republicação com o mesmo id — o publisher é a Fase 9 |
| Indisponibilidade temporária do PostgreSQL | ✅ erros transitórios classificados (`55P03`, deadlocks, conexão) e repetidos até 3 vezes; esgotado ⇒ erro transitório (503 na borda) |
| Indisponibilidade temporária do SQS | ⬜ Fase 8/9 (o retry com backoff já tem colunas e `MarkRetry`) |

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
| 6.5 Inbox/outbox (tabelas e comportamento) | ✅ schema e repositórios; o **fluxo** da inbox é a Fase 8 |

**Um ponto que merece destaque:** o enunciado aceita "operações sem dependências podem
ser concluídas de forma síncrona, sem commit intermediário de aceite". Foi o caminho
escolhido: **nunca existe `PENDING` commitado** para operação externa — o `INSERT`
grava `PENDING` e o `Save` grava o estado definitivo na mesma transação. O `PENDING`
durável fica restrito a `PENDING_REFERENCE` (que É durável e retomável).

## 7. Operações e referências — ✅ (a parte de worker é a Fase 9)

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
| `GET /health/live` e `/health/ready` (readiness de PostgreSQL e SQS) | ✅ readiness de **PostgreSQL e IdP** registrados no grupo `health_checkers` (P5 parcial: o SQS entra na Fase 8, junto com o cliente) |
| `docker compose up --build` sobe o `api` saudável | ✅ P1 resolvida: a flag `-health-check` existe e foi provada **subindo o binário real** |

## 10. Consumidor SQS — ⬜ Fase 8

| Exigência | Estado |
| --- | --- |
| Provisionar `wager-transactions.fifo` + DLQ com redrive | ✅ `deploy/localstack/init-queues.sh`: FIFO, `ContentBasedDeduplication=false`, `VisibilityTimeout=60`, `ReceiveMessageWaitTimeSeconds=20`, DLQ com `maxReceiveCount` |
| Corpo da mensagem com envelope + `data` | ⬜ Fase 8 (o `Request` do caso de uso já é o mesmo tipo que o HTTP vai produzir) |
| HTTP e SQS compartilhando o caso de uso e as garantias | ✅ por construção (um único `Service`); a ligação é a Fase 8 |
| `messageId` como identidade durável + hash em reentregas; remover da fila só depois do commit; rejeição terminal remove; transitório faz retry; DLQ; `SIGTERM` libera visibilidade | ⬜ Fase 8 (tabela `inbox_messages` + `InsertIfAbsent` com `ON CONFLICT DO NOTHING` já prontos) |
| Documentar `MessageGroupId`/`MessageDeduplicationId` e validar a concorrência HTTP × SQS | ⬜ Fase 8 |

## 11. Publicação com transactional outbox — 🟡 Fase 9

| Exigência | Estado |
| --- | --- |
| Estado, saldo, ledger, inbox e eventos confirmados atomicamente | ✅ (estado+saldo+ledger+outbox provados no mesmo commit; a inbox entra na Fase 8) |
| Worker separado publica os pendentes, com múltiplos publishers, disputa por registros, backoff e recuperação de trabalho abandonado | ⬜ Fase 9 — o mecanismo está pronto e provado: `ClaimBatch` com `FOR UPDATE SKIP LOCKED` + lease, `MarkRetry` incrementando `attempts` **no banco**, `PendingStats` |
| Recuperação entre commit e publicação, e entre publicação e confirmação; republicação preservando `eventId` | ⬜ Fase 9 |
| Eventos exigidos: `WagerTransactionProcessed` (inclui `LOSS`), `WagerTransactionRejected`, `WalletBalanceChanged`, `WagerTransactionPendingReference` | ✅ os quatro são gravados pelo caso de uso, com os payloads do §11 (o de saldo com `walletId`, `transactionId`, `direction`, `money`, `balanceBefore`, `balanceAfter`, `walletVersion`) |
| Envelope com `eventId`, `eventType`, `aggregateId`, `correlationId`, `causationId?`, `occurredAt`, `version`, `data` | ✅ |
| Tipo e versão definidos pelo construtor; timestamps UTC; valores em string decimal; snapshot imutável | ✅ |
| **Ordem de publicação** (implícito no `MessageGroupId = walletId`) | ✅ resolvido no bloco 6d: a fila é lida por `sequence` (IDENTITY), e o lote sai ordenado — inclusive com o relógio invertido |

## 12. Observabilidade — 🟡

| Exigência | Estado |
| --- | --- |
| Logs JSON com `correlationId`, `messageId`, `transactionId`, `walletId`, `providerId`; sem credenciais nem payload financeiro completo | ✅ a borda HTTP já preenche os campos de domínio: `http_request` (status, duração, `idempotencyKey`), `http_error` (`code`, `failureCode`), `operacao_concluida`/`operacao_rejeitada` (`transactionId`, `providerId`, `status`, `failureCode`, `correlationId`), `carteira_aberta`, `ledger_consultado`, `reconciliacao_divergente`, `token_recusado`, `idp_indisponivel`, `provedor_divergente_do_token`. Valor monetário **não** entra no log; a divergência sai em unidades mínimas. `messageId` chega com o consumidor (Fase 8) |
| Métricas: resultados por status, duplicatas, retries, DLQ, conflitos, atraso da outbox, latência, divergências de reconciliação | 🟡 **4 das 12 já são emitidas pela borda HTTP** (`wager_operations_total{kind,status}`, `wager_operation_duration_seconds`, `wager_idempotent_replays_total`, `wager_reconciliation_divergences_total`). Retries, DLQ, conflitos e atraso da outbox entram nas Fases 8–9/11 |
| Health checks definidos na API | ✅ `/health/live` (processo) e `/health/ready` (PostgreSQL + IdP, com o nome de cada dependência no corpo). SQS entra na Fase 8 (**P5** parcial) |

## 13. Verificação obrigatória — 🟡

| Bloco | Estado |
| --- | --- |
| **Testes unitários** (parsing e operações de `Money`, escala, limites, entradas inválidas, moedas, invariantes da carteira, transições, regras dos cinco tipos, conflito de payload, política de zero, abertura interna com metadados e eventos) | ✅ 574 casos no domínio + 56 no caso de uso + **49 em `internal/infra/auth`** + **67 em `internal/httpapi`** (tabela desfecho→status, erro→status, handlers com dublês) |
| **Testes de integração** com PostgreSQL, IdP e LocalStack em containers reais | 🟡 PostgreSQL ✅ real (migrations, constraints, imutabilidade do ledger, atomicidade, outbox concorrente, retry, lease — 29 casos) e **IdP ✅ real** (30 casos na borda HTTP obtendo token por `client_credentials` no Keycloak, + 2 casos subindo o **binário real**). LocalStack/inbox/reentrega/DLQ ⬜ Fase 8. Recuperação após reinicialização ⬜ Fase 10 |
| Verificação da composição Fx, início, encerramento e liberação de recursos dos workers | 🟡 `TestValidateAppComposition` valida o grafo **inteiro** agora (infra + usecase + auth + httpapi), `TestBuildAppFailsWithInvalidConfig` prova a falha de boot, e o teste do binário real prova **encerramento gracioso** e liberação do pool. A liberação dos recursos dos **workers** entra com os workers (Fases 8–9) |
| **Não substituir toda a infraestrutura por mocks** | ✅ os testes de integração usam PostgreSQL real, Keycloak real e o Unit of Work de produção; os dublês existem só nos testes **unitários** da borda (autenticador e serviço), onde o objetivo é a tradução do contrato |
| Autenticação/autorização: credenciais ausentes/inválidas/expiradas, isolamento entre provedores inclusive em replay e leitura, restrição das operações internas, nenhum efeito financeiro em acesso negado | ✅ **Fase 7.** 11 formas de token inválido com a causa exata; isolamento por corpo, por path, na leitura e **no replay**; operações internas exigindo role; e **contagem de tabelas antes/depois** provando zero efeito financeiro no 403/401/503 |
| Concorrência e recuperação (8 cenários) | 1 mesma aposta 50× ✅ (feito com 12 disparos); 2 disputa 80+80 ✅; 3 carteiras distintas ⬜; 4 três instâncias ⬜; 5 interrupção do consumidor ⬜; 6 dois publishers ⬜ (o `SKIP LOCKED` já é provado com 2 concorrentes no nível do repositório); 7 `REFUND`/`ROLLBACK` antes da referência ✅ a primeira metade (fica `PENDING_REFERENCE`), a resolução/expiração ⬜ Fase 9; 8 reinício preservando idempotência/pendências ⬜ |
| Conferência final: saldo armazenado == soma de créditos − débitos | ✅ provado em teste, e é exatamente o que a reconciliação faz |
| `go test -race` nos testes aplicáveis | ✅ verde nos dois modos |

## 14. Critérios de avaliação e eliminatórios — 🟡

| Eliminatório | Estado |
| --- | --- |
| Ausência de autenticação efetiva nos endpoints de negócio | ✅ **Fase 7.** Todos os endpoints de negócio exigem token validado contra o Keycloak real; não há rota de negócio registrada sem autenticador |
| Acesso não autorizado a operações/transações | ✅ isolamento no caso de uso **e** na borda (corpo, path, leitura e replay), com prova de que o acesso negado não produz efeito financeiro |
| Cálculo monetário em ponto flutuante | ✅ nenhum nos caminhos monetários (com a nuance do §5.1) |
| Saldo negativo por concorrência | ✅ impedido no domínio **e** por CHECK no banco |
| Movimentação duplicada | ✅ impedida por índice único + idempotência persistente, agora alcançável pelas duas portas (HTTP hoje; SQS na Fase 8) |
| Idempotência restrita à memória | ✅ persistida no banco |
| Dependência de uma única instância para funcionar | ✅ o estado está todo no PostgreSQL (locks, índices, constraints) e a validação de token é **local** (não há estado de sessão em memória); a prova com 3 instâncias é a Fase 10 |
| Publicação anterior ao commit | ✅ impossível por construção (outbox na mesma transação) |
| Ausência de ledger auditável | ✅ ledger append-only com dois saldos por lançamento |
| Substituição integral de PostgreSQL, SQS e IdP por mocks nos testes | ✅ PostgreSQL e IdP são **reais** nos testes de integração; os dublês existem só nos testes unitários da borda |

## 15. Entrega — 🟡 Fase 12

| Exigência | Estado |
| --- | --- |
| Código, migrations, Docker Compose e instruções para reproduzir de um checkout limpo | 🟡 código, migrations e compose ✅; **P1 resolvida**, então `docker compose up --build` sobe o `api` saudável. As instruções consolidadas são a Fase 12 |
| `README.md` da solução com pré-requisitos, variáveis, filas, migrations (aplicar/reverter), execução, exemplos e testes | 🟡 os exemplos autenticados já existem em `docs/evidencias/FASE_7.md` §4; a decisão de **onde** esse arquivo vive é a ⚠️ 3 da Fase 12 |
| `.env.example` sem segredos reais | ✅ completo (HTTP, banco, OIDC, SQS, outbox, workers) — e agora com `OIDC_HTTP_TIMEOUT`, `OIDC_TOKEN_URL` e `HTTP_STOP_TIMEOUT` |
| Provisionamento automático do IdP, identidades de teste e instruções dos fluxos autenticados | 🟡 realm, clients, roles, mapper e usuários de teste ✅; o fluxo `client_credentials` é exercitado pelos testes (com os três clients do realm); as instruções ⬜ Fase 12 |
| `ARCHITECTURE.md` com decisões e limitações | ✅ atualizado até a **Fase 7** (§10 fechada: autenticação, autorização, contratos, middlewares, composição, readiness e limitações) |
| Comandos documentados: `docker compose up --build`, `go test ./...`, `go test -race ./...`, `go vet ./...` | 🟡 documentados em `ARCHITECTURE.md` §17 e nas evidências; a consolidação é a Fase 12 |
| Documentar como preparar dependências de teste, integração, múltiplas instâncias e simulações de falha; informar as build tags | 🟡 a tag `integration` está documentada (§17 e `FASE_7.md` §10), inclusive as dependências (PostgreSQL e Keycloak de pé); múltiplas instâncias é a Fase 10 |
| Código formatado com `gofmt` e dependências reproduzíveis | ✅ `gofmt -l` limpo e `go.sum` versionado — **sem dependência nova nesta fase** (a validação OIDC usa a biblioteca padrão). Falta o `go mod tidy` para remover `testcontainers-go`, Fase 12 |

---

## Divergências e interpretações registradas (para o avaliador)

Nenhuma contraria o enunciado; todas são escolhas onde ele deixou margem. Estão
detalhadas em `docs/evidencias/FASE_6.md` §8, `docs/evidencias/FASE_7.md` §8 e no
`ARCHITECTURE.md` §9 e §10:

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

## O que ainda NÃO existe (lista direta, sem rodeio)

- Nenhum consumidor de SQS, nenhuma mensagem sendo processada.
- Nenhum publisher de outbox e nenhum worker (referências, reaper).
- **Parcialmente resolvido na Fase 7:** 4 das 12 métricas já são emitidas (pela
  borda HTTP: operações, latência, replays e divergência de reconciliação); o resto
  é das Fases 8–9 e 11.
- Nenhuma prova com 3 instâncias, nem de carteiras distintas em paralelo.
- O README da solução (o enunciado ocupa esse nome).
