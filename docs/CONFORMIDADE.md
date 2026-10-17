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
| Schema, constraints, imutabilidade | ✅ | `migrations/000001..000005` + `deploy/postgres/verify_constraints.sql` |
| Persistência + transação + lock | ✅ | `internal/infra/postgres` |
| Caso de uso (as 5 operações + referências) | ✅ | `internal/app/wagering` |
| Abertura de carteira (`OPENING`) | ✅ | `internal/app/wagering/opening.go` |
| Leituras + reconciliação | ✅ | `internal/app/wagering/{query,reconcile}.go` |
| Outbox (gravação + ordem) | ✅ | `internal/app/wagering/events.go` + `ClaimBatch` |
| **HTTP (contratos §9)** | ✅ **Fase 7** | `internal/httpapi` — 7 rotas + health + métricas |
| **Autenticação (OIDC)** | ✅ **Fase 7** — era **eliminatório** | `internal/infra/auth` (validação real contra o Keycloak) |
| **Consumidor SQS + inbox** | ✅ **Fase 8** | `internal/consumer` + `internal/app/inbox`, com integração contra o LocalStack |
| **Publisher da outbox + workers** | ✅ **Fase 9** | `internal/worker` (publicador, referências, reaper, runner) |
| **Concorrência e recuperação com 3 processos** | ✅ **Fase 10** | `test/integration` (T1–T8 + conferência final + latência) e a prova de SIGTERM no container |
| Métricas de negócio + dashboard/alerta | ✅ **Fase 11** | **12 de 12 emitidas E provadas** em `/metrics`; dashboard e 7 alertas em `deploy/observability/` |
| README da solução + `.env.example` | ✅ **Fase 12** | [`README.md`](README.md) (decisão da ⚠️ 3) + `.env.example` conferido |

### Os cinco pontos de atenção que valem mais que o resto

1. **⚠️ Autenticação era ELIMINATÓRIO (README §14) e agora está feita.** Todos os
   endpoints de negócio exigem token validado contra o Keycloak real, o
   `providerId` vem do token, e há prova de **nenhum efeito financeiro** em acesso
   negado (contagem de tabelas antes/depois). O risco aberto deixou de ser "não
   existe autenticação" e passou a ser o de qualquer validação local: token
   revogado antes do `exp` não é detectado (sem introspecção — limitação
   registrada no `ARCHITECTURE` §16). **Na Fase 10** apareceu o outro lado disso: o
   **container** entregue não autenticava por causa do endereço das chaves
   (defeito 41, corrigido com `OIDC_JWKS_URL`), e isso só foi descoberto porque a
   prova de SIGTERM subiu o container de verdade.
2. **✅ P1 resolvida:** a flag `-health-check` existe, é testada **subindo o
   binário real** e `docker compose up --build` deixa de subir o `api` como
   `unhealthy`.
3. **✅ Onde vive o "README da solução" (decidido na Fase 12):** o §15 pede
   pré-requisitos, variáveis, filas, migrations, execução, exemplos e testes no
   "README da solução"; o `README.md` da raiz **é o enunciado** e não pode ser
   editado. A decisão, tomada com o usuário, foi uma pasta de entrega
   [`docs/`](README.md) com o README da solução e os dois
   documentos finais.
4. **✅ "pelo menos três processos independentes" (README §8 e §13.4) e
   "carteiras distintas simultaneamente" (§13.3) têm prova desde a Fase 10:**
   três processos do binário com pools e memórias próprios (medido em
   `pg_stat_activity`), a corrida 80+80 entre instâncias, 50 envios simultâneos
   entre processos e o paralelismo entre carteiras provado por lock construído.
5. **⚠️ Testes com o IdP:** resolvido na direção certa — a prova principal usa o
   **Keycloak do container** com tokens de `client_credentials` (⚠️ 5 atendido), e
   o servidor OIDC de teste assinando de verdade cobre só o que o Keycloak não
   produz facilmente (assinatura de outra chave, `exp` no passado, `alg` errado,
   `aud` errado, IdP fora do ar).

---

## 1. Objetivo — ✅

> "oferecer uma API HTTP e um consumidor de mensagens que movimentem carteiras...
> Demonstre que o resultado financeiro continua correto com várias instâncias em
> execução e falhas entre as etapas do processamento."

O núcleo financeiro, a **API HTTP**, o **consumidor** e a prova com **três processos
independentes** estão prontos e provados (Fases 1–10). A garantia que o enunciado
cobra ("não pode gerar movimentação duplicada, saldo negativo ou perda de evento")
é demonstrada no caso de uso, no banco e nos dois caminhos de entrada — HTTP e SQS —
com a conferência final saldo × ledger.

## 2. Autenticação e autorização — ✅ Fase 7 (era eliminatório)

| Exigência | Estado |
| --- | --- |
| OAuth 2.0/OIDC com IdP externo, `client_credentials` | ✅ **Fase 7.** `internal/infra/auth`: discovery + JWKS com cache/rotação, assinatura RS256 conferida com `crypto/rsa`, `iss`/`aud`/`exp`/`nbf` validados. Prova principal com o **Keycloak do container** (`deploy/keycloak/realm.json`), obtendo token por `client_credentials` nos três clients do realm |
| A identidade autenticada determina o `providerId` | ✅ o `providerId` sai do **token**; o corpo só pode confirmá-lo, e divergência responde **403 sem chamar o caso de uso** (teste unitário + integração) |
| Provedores acessam só as próprias transações, inclusive em replay | ✅ no caso de uso e na borda: `:providerId` do path divergente ⇒ 403; operação alheia consultada pela própria rota ⇒ **404** (não 403, que confirmaria a existência); o replay devolve o resultado **daquele** provedor |
| Operações de carteira restritas ao serviço interno | ✅ `POST /wallets`, `GET /wallets*`, `/reconciliation` e a leitura interna por id exigem role `internal`. Teste de integração prova **403 com zero efeito financeiro** (contagem de tabelas antes/depois) |
| Acesso à mensageria controlado por credenciais e políticas do broker | ✅ **credenciais**: o cliente autentica no broker com `SQS_ACCESS_KEY_ID`/`SQS_SECRET_ACCESS_KEY` (estáticas no LocalStack, IAM em produção), e o provisionamento declara FIFO + DLQ + `maxReceiveCount`. ⚠️ **A política de acesso da fila (IAM) não é exercida no LocalStack** — limitação registrada no `ARCHITECTURE.md` §16. As validações de DOMÍNIO continuam no consumidor, como o §10 exige |
| Escolha do IdP e modelo de permissões justificados no `ARCHITECTURE.md` | ✅ `ARCHITECTURE.md` §10 (era `[PLANEJADO]`): escolha do Keycloak, roles `internal`/`provider`, `provider_id` do token, duas classes de erro (401 x 503), cache/rotação de chave e as limitações registradas |
| Rejeição de credenciais ausentes, inválidas ou expiradas | ✅ token ausente, malformado, expirado, `nbf` no futuro, emissor errado, audience errada, `kid` inexistente, assinatura de outra chave, `alg: none` e `alg: HS256` — 11 casos, cada um afirmando a causa exata |
| IdP indisponível não pode gerar efeito financeiro | ✅ discovery/JWKS fora ⇒ **503** com `Retry-After`, e teste de integração confere saldo e ledger intactos |

## 3. Ambiente de execução e falhas — ✅

| Situação que o enunciado exige suportar | Estado |
| --- | --- |
| Recebimento repetido por **HTTP** | ✅ replay por chave/idempotência, e o reenvio simultâneo é repetido e devolve o resultado persistido (provado com 12 disparos) |
| Recebimento repetido por **SQS** | ✅ **Fase 8**: a inbox deduplica por `(consumer_name, messageId)` na MESMA transação do tratamento, com o hash do corpo distinguindo reentrega (mesmo hash) de conflito real (hash diferente). Provado contra o LocalStack: reentrega com o mesmo identificador e a mesma operação enviada duas vezes debitam **uma** vez |
| Reversão que chega antes da transação referenciada | ✅ `PENDING_REFERENCE` persistida + evento + `202`, com `next_resolution_attempt_at` no futuro; o worker de referências retoma com backoff e TTL (Fase 9), inclusive **após reinício** (T8) |
| Processamento simultâneo de operações da mesma carteira | ✅ lock de linha + versão; provado (cenário do README e 12 disparos) |
| Encerramento abrupto antes ou depois do commit | ✅ antes: rollback (nada fica — provado com a outbox quebrada); depois: o evento fica na outbox e o lease vence (T5/T6). **No consumo:** a mensagem cujo commit não aconteceu volta para a fila (visibilidade liberada), e a que commitou mas não foi removida é reconhecida pela inbox |
| Publicação repetida de um evento | ✅ `eventId` estável na outbox; republicação com o mesmo id (provado em `TestQuedaEntrePublicacaoEMarkPublishedRepublicaComOMesmoEvento`) |
| Indisponibilidade temporária do PostgreSQL | ✅ erros transitórios classificados (`55P03`, deadlocks, conexão) e repetidos até 3 vezes; esgotado ⇒ erro transitório (503 na borda) |
| Indisponibilidade temporária do SQS | ✅ **consumo**: o `Receive` que falha registra e espera, e a falha de tratamento adia por `ChangeMessageVisibility` com backoff. **Publicação**: falha vira `retry` com backoff e, esgotado, quarentena — o evento **não** se perde (`TestMetricasDaOutbox`, `TestPublicadorColocaEmQuarentenaAposEsgotarTentativas`) |

## 4. Stack — ✅ (com duas observações)

| Exigência | Estado |
| --- | --- |
| Go com versão no `go.mod` e no Dockerfile | ✅ (Fase 0 corrigiu o Dockerfile que não compilava) |
| Go Modules versionados | ✅ `go.mod` + `go.sum` |
| Uber Fx com `fx.Module`/`Provide`/`Invoke` | ✅ `internal/app/{app,modules}.go` — módulos `config`, `infra` (pool + Unit of Work + verificador OIDC + health checker), `usecase` (`appwagering.Service`), `auth` (autenticador + health checker) e `httpapi` (rotas, servidor, handlers) |
| `fx.Lifecycle` com validação, cancelamento e shutdown | ✅ o HTTP (`OnStart` detecta erro de bind, `OnStop` faz `Shutdown` com prazo), o consumidor, os laços (runner com prazo) e o **fechamento do pool** por `Lifecycle`, na ordem inversa do registro (servidor para antes, dependências fecham depois) — provado com `Start`/`Stop` reais (`TestCicloDeVidaFxComStartEStopReais`). **P7 resolvida**: o prazo vem de `HTTP_STOP_TIMEOUT`, validado contra a soma dos encerramentos |
| HTTP (`net/http` ou roteador) | ✅ `chi` + middlewares (`RequestID`, `RealIP`, `AccessLog`, `Recoverer`, `Timeout`, `MaxBody`) — com a ordem corrigida (P2) |
| IdP OIDC | ✅ Keycloak no compose **e middleware implementado** (`internal/infra/auth`): discovery, JWKS com cache/rotação, RS256, `iss`/`aud`/`exp`/`nbf` |
| PostgreSQL | ✅ |
| SQS com LocalStack | ✅ filas FIFO + DLQ + redrive provisionadas (`deploy/localstack/init-queues.sh`) |
| Docker Compose | ✅ `postgres`, `keycloak`, `localstack`, `localstack-init`, `migrate`, `api` |
| Migrations versionadas com aplicação **e reversão** documentadas | ✅ **5 migrations** (`000001` a `000005`), com `up`/`down`/`version` pelo `cmd/migrate` — aplicadas e **revertidas** no ensaio da Fase 12 (banco descartável), e o caminho documentado no README da solução §5 |
| `pgx` com SQL explícito, transações/locks/constraints verificáveis | ✅ documentado em `ARCHITECTURE.md` §1, §6 e §7 |
| Biblioteca de banco, mapeamento de `Money` e delimitação da transação documentados | ✅ `ARCHITECTURE.md` §2 (Money), §6 (transação) e §17 (como validar) |
| Domínio independente de Fx, HTTP, SQS e persistência | ✅ verificado com `go list -deps`: nenhum Fx, `net/http`, `chi`, `pgx` ou SDK da AWS. **Nuance honesta:** aparece `database/sql/driver` porque `github.com/google/uuid` implementa `sql.Scanner`/`driver.Valuer` — é o pacote de **interfaces**, não um driver; e o caso de uso (`app/wagering`) não importa `internal/infra` |

## 5. Garantias obrigatórias — ✅ (8 de 8 com prova)

| # | Garantia | Estado |
| --- | --- | --- |
| 1 | Dinheiro nunca em `float32`/`float64` | ✅ `int64` em unidades mínimas, `Money` sem aritmética de ponto flutuante, parsing por dígitos, JSON como **string** decimal. Há teste de estilo em `money` que recusa `float`, `math` e `strconv.ParseFloat`. **Nuance:** `float64` aparece em `internal/observability` (duração de log e buckets do Prometheus) — não é caminho monetário. O `ARCHITECTURE.md` E3 dizia "grep vazio em `internal/`", e o certo é "vazio nos caminhos monetários" |
| 2 | Idempotência persistente, sobrevive a reinício | ✅ índices únicos no banco + decisão de replay no caso de uso; provado com um `Service` e um Unit of Work **novos** devolvendo o resultado original |
| 3 | Invariantes financeiras no banco, independentes de lock e da dedup do SQS | ✅ 29 CHECK, 4 FK, 2 UNIQUE, 3 índices únicos parciais, 5 triggers; 32 operações inválidas testadas e recusadas |
| 4 | Evento só publicado depois do commit | ✅ os eventos entram na outbox **na mesma transação** e o publisher publica **fora** dela, sempre antes de marcar (Fase 9); provado no caminho de saída (T6: 12 eventos → 12 mensagens) e na queda entre commit e publicação |
| 5 | Ledger append-only | ✅ trigger que recusa `UPDATE`/`DELETE` + repositório sem método de alteração |
| 6 | Carteiras independentes em paralelo; sem lock global | ✅ o lock é **de linha** (`SELECT ... FOR UPDATE` na carteira), não de tabela. Provado na Fase 10 (`TestT3CarteirasDistintasEmParaleloSemLockGlobal`: com a carteira A presa, B..F concluem em 14–34 ms e nenhum backend espera lock; A espera 408 ms) |
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
| `ROLLBACK` desfaz movimento de `BET`/`WIN`/`REFUND` | ✅ direção contrária ao original; `ROLLBACK` de `BET` já estornada ⇒ `ALREADY_REVERSED`; e, desde o defeito 50 da Fase 12, `REFUND` de `BET` já desfeita por `ROLLBACK` também ⇒ `ALREADY_REVERSED` |
| Referência resolvida por `(providerId, referenceExternalTransactionId)` | ✅ |
| Concordância em provedor, jogador, carteira, moeda e rodada; valor igual | ✅ `REFERENCE_MISMATCH` |
| Zero aceito no saldo inicial e em `LOSS`; os outros exigem valor > 0 | ✅ validado no domínio e por CHECK no banco |
| Sem duas reversões bem-sucedidas do mesmo tipo | ✅ índice único parcial + leitura prévia (`ALREADY_REVERSED`) |
| Documentar `REFUND` + `ROLLBACK` sobre a mesma aposta | ✅ `ARCHITECTURE.md` §9 e `EVIDENCIAS.md` §5: a PRIMEIRA reversão do débito vale; a segunda, de qualquer tipo, é recusada com `ALREADY_REVERSED` (guarda simétrica desde o defeito 50); desfazer o estorno é `ROLLBACK` do `REFUND` |, que debita |
| Reversão que exigiria mais saldo: código DIFERENTE do de aposta sem saldo | ✅ `ROLLBACK_INSUFFICIENT_FUNDS` × `INSUFFICIENT_FUNDS`, separados pelo tipo |
| `PENDING_REFERENCE` persistida, worker com backoff exponencial **após reinício**, TTL/máx. de tentativas, e comportamento quando a referência existe mas está pendente ou terminou mal | 🟡 persistência, backoff inicial e os dois comportamentos ✅; o **worker** com backoff/TTL é a Fase 9 (`ARCHITECTURE.md` A7 define 20 tentativas/24h) |
| Toda rejeição com `failureCode` estável e documentado | ✅ 17 códigos na tabela A8 + os dois novos (`WALLET_PLAYER_MISMATCH`, `TRANSACTION_NOT_FOUND`), distinguindo corrigível de definitivo |

## 8. Concorrência — ✅ Fase 10

| Exigência | Estado |
| --- | --- |
| Coordenação por carteira, com estratégia justificada | ✅ `READ COMMITTED` + `SELECT ... FOR UPDATE` + versão, justificado em `ARCHITECTURE.md` §6 |
| Demonstração com **três processos independentes**, cada um com suas conexões e memória | ✅ **`TestTresInstanciasIndependentes`**: três processos do binário (PIDs distintos), com **três conjuntos disjuntos de conexões** vistos em `pg_stat_activity` por `application_name`, e a queda de um sem efeito nos outros dois. Todos os cenários do §13 rodam nessa topologia |
| Teste obrigatório: 100.00 com duas apostas simultâneas de 80.00 → uma processada, uma rejeitada, saldo 20.00 e **um** débito | ✅ **`TestT2DuasApostasDe80Sobre100EmInstanciasDiferentes`**: uma aposta por instância (`api-2` e `api-3`), disparadas por barreira → 1 `PROCESSED`, 1 `REJECTED/INSUFFICIENT_FUNDS`, saldo 20.00, **um** débito, versão 2 |
| Reenvios não alteram o resultado | ✅ 10 reenvios de cada aposta, alternando instâncias: a processada volta 200 replay, a rejeitada volta 422 com o mesmo `failureCode`, e saldo, versão, ledger e transações não mudam |
| Carteiras diferentes continuam em paralelo | ✅ **`TestT3CarteirasDistintasEmParaleloSemLockGlobal`**: com a linha da carteira A presa por uma transação do teste, 5 operações em outras carteiras concluem (14–34 ms) e **nenhum** backend de instância aparece esperando lock; a de A fica bloqueada e conclui 408 ms depois da liberação. Custo da serialização medido: p99 166 ms em carteiras distintas × 579 ms na mesma (`TestLatenciaSobConcorrenciaTresInstancias`) |

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

## 12. Observabilidade — ✅ Fase 11 (fechada)

| Exigência | Estado |
| --- | --- |
| Logs JSON com `correlationId`, `messageId`, `transactionId`, `walletId`, `providerId`; sem credenciais nem payload financeiro completo | ✅ **provado no stdout do PROCESSO real** (`TestLogsDoProcessoReal`, binário com `APP_ENV=production`): `operacao_concluida` (correlationId, requestId, transactionId, walletId, providerId), `sqs_mensagem_processada` (correlationId, messageId, transactionId, walletId, providerId) e `reference_resolvida` (correlationId da coluna, transactionId, walletId, providerId). O teste reprova qualquer linha com `client_secret`/`Authorization`/`Bearer`, com campo `amount`/`money`/`initialBalance` ou com valor monetário em texto. `TestLogsDoProcessoEmModoLocal` prova o TEXTO em `APP_ENV=local` (a outra metade do defeito 27) |
| Métricas: resultados por status, duplicatas, retries, DLQ, conflitos, **atraso da outbox**, latência, divergências de reconciliação | ✅ **12 de 12 emitidas E provadas**: cada uma tem teste que dispara o gatilho real (HTTP autenticado, SQS/LocalStack, publisher e worker reais) e lê o valor em `/metrics` (`test/integration/metricas_integration_test.go`, 10 funções). Os 8 estágios de `retries_total{stage}` têm teste de emissão próprio. A regra de cardinalidade virou asserção. **Duas métricas mentiam e foram corrigidas**: `transaction_conflicts_total` não era emitida por ninguém (defeito 42) e `operations_total` tinha `kind`/`status` com significados diferentes no HTTP e no SQS (defeito 43) |
| Health checks definidos na API | ✅ `/health/live` e `/health/ready` com **PostgreSQL, IdP, SQS e `workers`**; o readiness foi provado 200 no grafo real do Fx (`TestCicloDeVidaFxComStartEStopReais`) e no processo real |
| Dashboards e tracing (diferencial opcional) | ✅ dashboards **versionados** (`deploy/observability/`: 13 painéis + 7 regras validadas por `promtool` + runbook). Tracing continua fora, declarado como trabalho não concluído |

## 13. Verificação obrigatória — ✅ Fase 10

| Bloco | Estado |
| --- | --- |
| **Testes unitários** (parsing e operações de `Money`, escala, limites, entradas inválidas, moedas, invariantes da carteira, transições, regras dos cinco tipos, conflito de payload, política de zero, abertura interna com metadados e eventos) | ✅ 574 casos no domínio + 56 no caso de uso + **51 em `internal/infra/auth`** (+2 da Fase 10) + **67 em `internal/httpapi`** + **45 da Fase 8** (26 inbox + 19 consumer) + **38 da Fase 9** (23 em `internal/worker`: publicador 10, referências/reaper 7, runner 6; 10 de `Retomar` em `internal/app/wagering`; 4 de config; 1 de composição do Fx) |
| **Testes de integração** com PostgreSQL, IdP e LocalStack em containers reais | ✅ PostgreSQL ✅ real (migrations, constraints, imutabilidade do ledger, atomicidade, outbox concorrente, retry, lease — 29 casos) + **IdP ✅ real** (30 casos na borda HTTP com token por `client_credentials` + 2 do binário real) + LocalStack ✅ real (9 funções de consumo, **8 funções de publicação e recuperação**) + **11 funções da Fase 10** com três processos independentes + **2 funções** de `application_name` |
| Verificação da composição Fx, início, encerramento e liberação de recursos dos workers | ✅ `TestValidateAppComposition` (grafo inteiro, com e **sem** os workers ligados), `TestBuildAppFailsWithInvalidConfig`, o teste do binário real (encerramento e liberação do pool), **6 casos do runner** (Fase 9) e a **prova de SIGTERM no container Linux** (`EVIDENCIAS.md` §6): `exit=0` em 8,09 s, `sqs_visibilidade_liberada`, pool fechado por último |
| **Não substituir toda a infraestrutura por mocks** | ✅ os testes de integração usam PostgreSQL, Keycloak e **LocalStack** reais; os dublês existem só nos unitários, e registram chamadas para as asserções incluírem a **ausência** delas. A única substituição na Fase 10 é o **passo `Delete` do consumidor**, embrulhado para reproduzir a queda entre commit e delete — o banco, o domínio e o consumidor são os de produção |
| Autenticação/autorização | ✅ **Fase 7** (11 formas de token inválido, isolamento por corpo/path/leitura/replay, contagem de tabelas antes/depois) e **Fase 10**: o container do compose passa a autenticar de verdade (defeito 41 — `OIDC_JWKS_URL`) |
| Concorrência e recuperação (8 cenários) | ✅ **todos com prova**, e os de concorrência com **três processos independentes**: 1 mesma aposta 50× (`TestT1MesmaAposta50VezesEntreTresInstancias` e `TestT1MesmaOperacao50VezesPelaFila`); 2 disputa 80+80 (`TestT2DuasApostasDe80Sobre100EmInstanciasDiferentes`); 3 carteiras distintas (`TestT3CarteirasDistintasEmParaleloSemLockGlobal`); 4 três instâncias (a rodada consolidada de T1–T3, T5–T8 e da conferência); 5 interrupção entre commit e delete (`TestT5QuedaEntreCommitEDeleteReentregaReconhecida` **e** o SIGTERM no container); 6 publishers disputando a outbox (Fase 9 + `TestT6TresPublishersReaisDisputamAMesmaOutbox`); 7 reversão antes da referência (Fase 9, as duas metades + `TestT7REFUNDAntesDaReferenciaResolvidoPorOutraInstancia`); 8 reinício (`TestT8ReinicioPreservaIdempotenciaPendenciaEConsistencia`, com `PENDING_REFERENCE` resolvida por instância nova) |
| Conferência final: saldo armazenado == soma de créditos − débitos | ✅ **`TestConferenciaFinalCruzandoHTTPESQS`**, de duas formas independentes (SQL puro e o endpoint de reconciliação), com a **mesma operação cruzando HTTP e SQS** e um único débito; e também no binário real (Fase 9) |
| `go test -race` nos testes aplicáveis | ✅ verde nos dois modos (17 pacotes na unidade, 18 com a tag), em duas rodadas consecutivas da suíte inteira |

## 14. Critérios de avaliação e eliminatórios — ✅

| Eliminatório | Estado |
| --- | --- |
| Ausência de autenticação efetiva nos endpoints de negócio | ✅ **Fase 7.** Todos os endpoints de negócio exigem token validado contra o Keycloak real; não há rota de negócio registrada sem autenticador. **Reforçado na Fase 10:** o container entregue pelo compose passou a autenticar de verdade (defeito 41 — sem `OIDC_JWKS_URL` todo token válido virava 503 dentro do container) |
| Acesso não autorizado a operações/transações | ✅ isolamento no caso de uso **e** na borda (corpo, path, leitura e replay), com prova de que o acesso negado não produz efeito financeiro |
| Cálculo monetário em ponto flutuante | ✅ nenhum nos caminhos monetários |
| Saldo negativo por concorrência | ✅ impedido no domínio **e** por CHECK no banco; a disputa 80+80 sobre 100.00 foi provada com **três processos** e termina em 20.00 |
| Movimentação duplicada | ✅ índice único + idempotência persistente, alcançável pelas duas portas — e, na Fase 10, provada com 50 envios simultâneos entre três processos (HTTP) e 50 entregas distintas pela fila: **um** débito nos dois caminhos |
| Idempotência restrita à memória | ✅ persistida no banco; **sobrevive ao reinício** — provado com um processo que morre e outro que responde replay (`TestT8...`) |
| Dependência de uma única instância para funcionar | ✅ **fechado na Fase 10**: três processos com pools e memórias próprios executam T1–T8; a queda de um não afeta os outros; publishers e consumidores concorrentes funcionam |
| **Publicação anterior ao commit** | ✅ impossível por construção (outbox na mesma transação) e provado no caminho de saída: o publisher só lê o que já foi commitado; com três publishers reais, 12 eventos → 12 mensagens sem duplicar nem perder |
| Ausência de ledger auditável | ✅ ledger append-only com dois saldos por lançamento; a conferência final da Fase 10 soma o ledger em SQL puro e compara com o saldo armazenado (101000 == 101000) |
| Substituição integral de PostgreSQL, SQS e IdP por mocks nos testes | ✅ os três são **reais** nos testes de integração (PostgreSQL, Keycloak e LocalStack); os dublês existem só nos unitários |

**Observabilidade (5 pontos):** ✅ **fechada na Fase 11** — 12/12 métricas com
teste de emissão e leitura em `/metrics`, log do **processo** real nos dois
formatos com os cinco identificadores e sem segredo/payload financeiro, health
checks com `workers`, dashboard e alertas versionados com runbook. Dois defeitos
reais de observabilidade foram corrigidos (42 e 43) e dois de documentação/código
morto (44 e 45).

**Documentação (5 pontos):** ✅ **fechada na Fase 12** — o
[`README.md`](README.md) é o README da solução (⚠️ 3
decidida com o usuário), com todo comando **executado** e a saída na evidência; os
dois documentos finais estão em [`../docs_desafio/SISTEMA_EXPLICADO.md`](../docs_desafio/SISTEMA_EXPLICADO.md)
e [`../docs_desafio/ENTREVISTA.md`](../docs_desafio/ENTREVISTA.md); o `ARCHITECTURE.md`
está completo (§12 com a tabela métrica → teste, §15 com os 50 defeitos, §16 com as
limitações, §17 com os números finais).

## 15. Entrega — ✅ Fase 12

| Exigência | Estado |
| --- | --- |
| Código, migrations, Docker Compose e instruções para reproduzir de um checkout limpo | ✅ **provado**: `git clone` para diretório temporário + `docker compose up -d --build` = 52 s, `api` `healthy`, `/health/ready` 200 com as quatro dependências e carteira criada em banco novo. Saída em `EVIDENCIAS.md` |
| `README.md` da solução com pré-requisitos, variáveis, filas, migrations (aplicar/reverter), execução, exemplos e testes | ✅ [`README.md`](README.md) — todas as 61 variáveis conferidas contra `internal/config`, migrations aplicadas **e revertidas** no ensaio, curls autenticados com saída real, comandos de teste com build tag e dependências. O `README.md` da raiz continua sendo o enunciado, intocado |
| `.env.example` sem segredos reais | ✅ completo e **conferido por script** contra `internal/config`: as 61 variáveis da aplicação estão lá (as 5 restantes são `POSTGRES_*`/`KEYCLOAK_*`, do compose) e nenhuma credencial real |
| Provisionamento automático do IdP, identidades de teste e instruções dos fluxos autenticados | ✅ `deploy/keycloak/realm.json` importado no boot (realm, 3 clients `client_credentials`, roles, mapper de audience); token e curls autenticados documentados e **executados** (§7 do README da solução) |
| `ARCHITECTURE.md` com decisões e limitações | ✅ atualizado até a **Fase 12** (§12 métrica → teste e log por caminho, §13 recuperação, §15 defeitos 42–50, §16 limitações, §17 números e comandos) |
| Comandos documentados: `docker compose up --build`, `go test ./...`, `go test -race ./...`, `go vet ./...` | ✅ no README da solução (§4, §5, §8) e no `ARCHITECTURE.md` §17 |
| Documentar como preparar dependências de teste, integração, múltiplas instâncias e simulações de falha; informar as build tags | ✅ README da solução §8 e §9: containers, banco de teste separado, filas, Keycloak, tag `integration`, as três instâncias (faixa 18091+), a queda entre commit e delete e o SIGTERM no container — incluindo o aviso de **parar o `api` do compose** antes da integração |
| Código formatado com `gofmt` e dependências reproduzíveis | ✅ `gofmt -l` limpo, `go mod tidy` aplicado (Fase 11), `go.mod` com 42 linhas e `go mod verify` limpo |

---

## Divergências e interpretações registradas (para o avaliador)

Nenhuma contraria o enunciado; todas são escolhas onde ele deixou margem. O detalhe
técnico de cada uma está no `ARCHITECTURE.md` §9, §10 e §11: 

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
20. **"Três processos independentes" são três processos do BINÁRIO**, não três
    containers (Fase 10): o §8 pede "suas próprias conexões e memória", e um
    processo do binário tem PID, pool e memória próprios — provado em
    `pg_stat_activity` por `application_name`. O container entra na prova de
    **SIGTERM**, que é o que o processo local no Windows não consegue provar. O
    `ARCHITECTURE.md` falava em "3 containers `api` no compose"; a redação foi ajustada
    (o compose **não** ganhou `api-2`/`api-3`).
21. **O T4 do `ARCHITECTURE.md` não é um teste separado** (Fase 10): é a rodada
    consolidada — T1–T3, T5–T8 e a conferência final rodando com três processos
    na mesma suíte.
22. **O T6 não assere a ORDEM por carteira quando `OUTBOX_BATCH_SIZE=1`** (Fase
    10): nesse arranjo dois publishers podem enviar eventos consecutivos da mesma
    carteira em ordem trocada — é a limitação do §16, agora observada. A
    invariante asserida é a partição (12 eventos → 12 mensagens, sem repetição nem
    perda); a ordem é reportada. Com o lote padrão, a ordem continua garantida
    pelo `ORDER BY sequence` do claim.
23. **O endereço das chaves públicas é configurável** (Fase 10, defeito 41): o
    padrão continua sendo o `jwks_uri` do discovery (OIDC). `OIDC_JWKS_URL`
    existe porque o Keycloak anuncia o nome PÚBLICO do IdP, que de dentro de um
    container pode não ser alcançável — e o `iss` continua sendo conferido contra
    `OIDC_ISSUER`. A chave ajusta **onde** as chaves são buscadas, não afrouxa a
    validação.

## O que ainda NÃO existe (lista direta, sem rodeio)

- **Consumidor de `wallet-events.fifo`**: o evento é **publicado**, e o contrato de
  consumo está documentado (`ARCHITECTURE.md` §11, com as 7 regras). O consumidor em
  si não foi implementado — decisão de escopo (o README §11 pede a publicação).
- **Tracing com OpenTelemetry** e **partidas dobradas**: diferenciais opcionais do
  §14; não implementados, e o `correlationId` cobre o rastreio por log.
- **Contador durável de falhas do reaper**: uma linha `PENDING` inconsistente é
  repetida indefinidamente em vez de virar `FAILED` (`ARCHITECTURE.md` §16).
- **Prova de SIGTERM no Windows** (P8): não existe sinal de término de host; a
  prova é do container Linux (`EVIDENCIAS.md` §3).
- **Teste de carga com metodologia e throughput**: há medição de latência sob
  concorrência (Fase 10), mas não teste de carga — diferencial opcional, sem meta de
  RPS no enunciado.
- **`README.md` da solução na raiz**: o arquivo da raiz é o enunciado e não pode ser
  editado; a entrega vive em [`docs/`](README.md) (a ⚠️ 3,
  decidida com o usuário na Fase 12), junto com os dois documentos finais.
