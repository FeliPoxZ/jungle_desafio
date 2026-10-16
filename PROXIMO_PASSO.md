# PROXIMO_PASSO.md — Handoff para a próxima conversa

> **INSTRUÇÃO PARA A NOVA CONVERSA (leia isto primeiro):**
> Este arquivo é um handoff. Ele diz **onde o projeto está, o que já foi decidido, qual é o
> método de trabalho acordado e exatamente qual é a próxima tarefa pontual**.
>
> Antes de responder qualquer coisa, leia também, nesta ordem:
> 1. `README.md` (raiz) — **o enunciado é a fonte de verdade**; em caso de dúvida, ele decide
> 2. `ARCHITECTURE.md` (raiz) — arquitetura, decisões e problemas resolvidos
> 3. `CONTEXTO.md` (raiz) — estado geral, ambiente, decisões congeladas, pendências
> 4. `PLANO.md` §3 — checklist por fase
> 5. `docs/evidencias/FASE_1.md` a `FASE_6.md` — provas do que já funciona
> 6. `docs/LEDGER_VS_WALLET.md` — a distinção central do domínio financeiro
>
> **Não comece a codar sem ler as seções 3 e 3.5.**

---

## 1. O QUE É O PROJETO

Desafio técnico (3ª fase de um processo seletivo) para vaga de **desenvolvedor back-end
Go júnior**. O enunciado está em `README.md` (raiz) e **não pode ser editado**.

Resumo: um serviço em Go com **Uber Fx** que processa operações financeiras de provedores
de jogos (`BET`, `WIN`, `LOSS`, `REFUND`, `ROLLBACK`) em ambiente **distribuído**, com
PostgreSQL, Keycloak (OIDC) e AWS SQS (LocalStack local). Exige garantias financeiras
reais: precisão monetária, idempotência persistente, ledger append-only, concorrência sem
lost update, outbox transacional, recuperação de falhas.

**Prazo original:** 2 dias. **Entrega:** código + migrations + docker-compose + README da
solução + `ARCHITECTURE.md`.

**Repositório:** `github.com/FeliPoxZ/jungle_desafio`
**Workspace:** `E:\jungleg`

---

## 2. ONDE O PROJETO ESTÁ AGORA

| Item | Estado |
| --- | --- |
| Fase 0 (fundação + infraestrutura) | ✅ **CONCLUÍDA E VALIDADA** com containers reais |
| Fase 1 (`Money`) | ✅ **CONCLUÍDA E VALIDADA** — 384 casos, 97,9% de cobertura, 10/10 no README |
| Fase 2 (`Wallet` + `LedgerEntry`) | ✅ **CONCLUÍDA E VALIDADA** — 63 casos, 99,0% de cobertura |
| Fase 3 (`WagerTransaction`) | ✅ **CONCLUÍDA E VALIDADA** — 127 casos, **100,0%** de cobertura |
| Fase 4 (schema + migrations) | ✅ **CONCLUÍDA E VALIDADA** — 29 CHECK + 4 FK + 2 UNIQUE, 30 operações inválidas recusadas pelo banco |
| Fase 5 (persistência `pgx` + Unit of Work) | ✅ **CONCLUÍDA E VALIDADA** — 38 testes (25 de integração), `-race` verde, 4 defeitos reais corrigidos |
| Fase 6 (caso de uso `wagering.Service`) | ✅ **CONCLUÍDA E VALIDADA** em 3 blocos: 6a (`Process`), 6b (`OpenWallet`), 6c (leituras + `Reconcile`) — 55 casos unitários, 27 de integração, **6 defeitos reais corrigidos** |
| Fase 7 (HTTP + autenticação) | ⬅ **PRÓXIMA** |
| Fases 8–12 | ⬜ Não iniciadas (ver checklist no `PLANO.md` §3) |

**Domínio (Fases 1–3): 574 casos. Persistência (Fase 5): 38. Caso de uso (Fase 6):
55 unitários + 27 de integração. `go test -race` verde em 13 pacotes de unidade e
nos 2 de integração. Árvore git limpa.**

> ⚠️ Os **dois** pacotes de integração usam o mesmo banco de teste e truncam
> tabelas. Cada `TestMain` toma um lock consultivo (`pg_advisory_lock`) e espera a
> vez, então `go test -tags=integration ./...` funciona **sem** `-p 1`.

### Histórico de commits (mais recente primeiro)

```
21cf528 feat(app): adiciona leituras do caso de uso e reconciliacao em visao consistente   ← FASE 6c
a397398 feat(app): adiciona abertura de carteira com lancamento de abertura e eventos       ← FASE 6b
0cf1526 docs(handoff): atualiza PROXIMO_PASSO para a Fase 6b (abertura de carteira)
4f0ab9d feat(app): adiciona caso de uso de operacoes com idempotencia e ledger transacional  ← FASE 6a
aaa6c24 docs(arquitetura): cria ARCHITECTURE.md e o prompt de abertura da proxima conversa
2e4c2c0 refactor(arquitetura): separa dominio, contrato e infraestrutura
182df42 docs(estrategia): registra a mudanca de metodo e o compromisso dos dois documentos finais
4a3a2d5 docs(handoff): atualiza PROXIMO_PASSO para a Fase 6 (caso de uso)
756bed2 feat(postgres): adiciona repositorios pgx com unit of work e locking por carteira   ← FASE 5
879810f docs(handoff): atualiza PROXIMO_PASSO para a Fase 5 (pgx + unit of work)
b209f85 feat(db): adiciona schema inicial com constraints de saldo, ledger imutavel e idempotencia   ← FASE 4
bdcf0e1 fix(wagering): valida saldo resultante antes de transicionar e remove parametro morto
04194ca docs(handoff): atualiza PROXIMO_PASSO para a Fase 4
72c3aaf feat(wagering): adiciona transacao de aposta com tipos, estados e eventos   ← FASE 3
11cbb9b docs: adiciona LEDGER_VS_WALLET.md e limpa comentarios do pacote wallet
44e5e46 docs(handoff): atualiza PROXIMO_PASSO para a Fase 3
98fbee6 feat(wallet): adiciona carteira com debito/credito e ledger append-only   ← FASE 2
442a025 docs: registra o hash do commit da Fase 1 no plano e no handoff
b046e6a feat(money): completa o value object com aritmetica, comparacao e serializacao   ← FASE 1
3172772 bloco 2 monet incompleto
3a36aec feat(money): adiciona o value object Currency com erro de dominio tipado
1eef5b2 chore(setup): fundacao do projeto com Fx, config validada, HTTP e observabilidade
```

> ✅ **As Fases 0 a 6 estão commitadas e validadas. Não revalide nem recommite.**

---

## 3. ⭐ MÉTODO DE TRABALHO — ESTRATÉGIA MUDOU A PARTIR DA FASE 6

### 3.1. A MUDANÇA (leia com atenção)

**Até a Fase 5** o ritmo era "MODO B": a IA explicava um bloco pequeno, **o usuário digitava**,
e depois vinha o próximo bloco. Foi assim que o núcleo do domínio (`Money`, `Wallet`,
`WagerTransaction`) foi construído e aprendido.

**A partir da Fase 6 a estratégia é outra:**

> **A IA escreve o código.** O usuário revisa, e a IA explica **as partes importantes**
> depois de escrever. O ritmo continua sendo um bloco coerente por vez, com commit ao final
> de cada um — mas quem digita é a IA.
>
> **Motivo:** o tempo é curto e as Fases 6–12 são de integração (serviço, HTTP, SQS, outbox,
> workers). O valor está em fechar o sistema funcionando, não em digitar cada linha.
>
> ⚠️ **Ainda assim: explicar faz parte da entrega.** Código entregue sem a explicação das
> decisões é entrega incompleta nesta sessão. O usuário está se preparando para uma
> entrevista e precisa saber defender cada parte.

### 3.2. O que continua valendo

| Regra | Como aplicar |
| --- | --- |
| **Explicar o que foi feito** | Depois de escrever, explicar as partes importantes: **decisão**, **motivo** e **o que quebraria sem ela** |
| **Um bloco coerente por vez** | Não entregar 3 fases juntas. Bloco → validação → commit |
| **Aceitar contestação** | ⭐ O usuário questiona e isso é bom. Se ele estiver certo, **admitir e corrigir** |
| **Validar de verdade** | `go build`/`go vet`/`gofmt` limpos, testes verdes, e **integração contra o banco real** quando a fase tocar persistência |
| **Commit ao final de cada bloco** | Com mensagem que conta a história: o que foi feito, o que foi decidido e o que foi encontrado |
| **Atualizar `PLANO.md` e `PROXIMO_PASSO.md`** | A cada fase concluída, com as decisões e evidências |
| **Registrar achados** | Bug encontrado durante o trabalho vai para a mensagem de commit **e** para as evidências |

### 3.3. Formato da entrega de cada bloco

1. **O que foi feito** (em uma frase)
2. **O código** (arquivos e por que estão separados assim)
3. **As decisões importantes** — tabela `decisão | motivo | o que quebraria sem ela`
4. **Os achados** — bug, imprevisto, ajuste de desenho
5. **A validação** — comandos e saída real
6. **O commit**

### 3.4. Princípios de qualidade já cobrados (lições das Fases 1–5)

> **1. Provar com teste antes de afirmar que uma guarda basta.** Na Fase 1 a IA afirmou duas
> vezes "uma guarda basta" e as duas estavam erradas — a varredura exaustiva de 81 combinações
> achou 10 furos no `Add`. **Varredura > raciocínio** em sinal, limite e overflow.

> **2. Quando um teste falhar, verificar se a régua está certa antes de acusar o código.**
> A IA quase acusou o código do usuário três vezes por expectativa errada do próprio teste.

> **3. Retry envolve a TRANSAÇÃO INTEIRA**, nunca uma chamada por dentro dela. Repetir dentro
> da transação deixa a tentativa que falhou com a conexão aberta segurando o lock, e o sintoma
> aparece longe (o próximo `TRUNCATE` morre com deadlock). A unidade de retry é a transação —
> vale para os testes e para a Fase 6.

> **4. Consultar o schema antes de escrever SQL.** Dois "defeitos" da Fase 5 eram testes que
> ignoravam fato do schema: o ledger tem FK para `wager_transactions`, e `id` é `UUID`.

> **5. Antes de escrever helper, verificar se já existe na stdlib.** "Evitar um import" nunca
> é justificativa.

> **6. Comentário só onde explica decisão.** Código enxuto, sem doc comment onde o nome já diz.

> **7. Cuidado com `Set-Content` no PowerShell 5.1:** grava em ANSI e **corrompe acentos**.
> Usar a ferramenta de edição ou `[System.IO.File]::WriteAllLines` com `UTF8Encoding($false)`.
> Confirmar com `$texto.Contains('ç')` depois de escrever. Já aconteceu duas vezes.

> **8. Não confie em contagem de falha tirada de pipeline do PowerShell.** Um `Select-String`
> sem `-Pattern` já produziu um "24 falhas" falso. Ler o output real do `go test`.

> **9. `git commit` com mensagem longa:** usar arquivo de mensagem (`git commit -F arquivo`),
> não heredoc — o `@'...'@` do PowerShell 5.1 quebra com aspas e acentos.

### 3.5. 🎯 COMPROMISSO FINAL: DOIS DOCUMENTOS DE ENTREGA

Quando o sistema estiver **funcionando e fechado** (Fases 6–12 concluídas), a IA deve gerar
**dois arquivos** — e eles fazem parte da entrega, não são opcionais:

#### Documento 1 — Explicação do sistema completo

**Arquivo sugerido:** `docs/SISTEMA_EXPLICADO.md`

Explicar **tudo o que o sistema faz**, para quem vai ler o código ou mantê-lo:

- o fluxo completo de uma requisição, do HTTP ao commit e à publicação do evento;
- cada componente e por que ele existe (domínio, repositórios, UoW, outbox, inbox, workers);
- **as lógicas que foram pensadas e seguidas** — as decisões de arquitetura com o motivo;
- **os problemas que foram resolvidos** — a lista de achados de todas as fases (as guardas
  de overflow, o `Neg` invertido, o `CHAR(3)` com padding, o `55P03` mal classificado, o
  `Movement`, o `newEntryID` que não era UUID...), com o sintoma e a correção;
- os limites conhecidos e o que ficou de fora conscientemente.

#### Documento 2 — Preparação para a entrevista

**Arquivo sugerido:** `docs/ENTREVISTA.md`

**Formato de perguntas e respostas**, na lógica de quem entrevista:

- perguntas de **arquitetura**: "por que `READ COMMITTED` com `FOR UPDATE` e não
  `SERIALIZABLE`?", "por que outbox transacional e não publicar direto no SQS?", "por que o
  ledger é separado da carteira?";
- perguntas de **decisão**: "por que `int64` em unidades mínimas e não `decimal`?", "por que
  o domínio não importa `encoding/json`?";
- perguntas de **garantia**: "como você impede débito duplicado?", "onde está a proteção
  contra lost update?", "o que acontece se o processo morrer no meio?";
- perguntas de **erro e aprendizado**: "qual foi o bug mais difícil?", "o que você faria
  diferente?";
- as respostas devem deixar **arquitetura e lógica bem visíveis**, com o motivo e a
  consequência — não só o "o quê";
- incluir as perguntas difíceis (as que expõem limitação) com resposta honesta.

> ⚠️ **Estes dois documentos são o motivo pelo qual a IA deve explicar cada bloco durante o
> caminho:** eles serão montados a partir dessas explicações. Explicar agora é investir na
> entrega final.

---

## 4. AMBIENTE — DETALHES QUE CAUSAM ERRO SE IGNORADOS

### 4.1. Cache do Go (OBRIGATÓRIO)

O cache global `C:\Users\PoxZe\go` é gravável, mas o projeto usa cache isolado. Todo comando:

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'
$env:GOCACHE='E:\jungleg\.gocache\build'
$env:GOPATH='E:\jungleg\.gocache\gopath'
```

O `Makefile` já faz isso. `/.gocache/` está no `.gitignore`.

### 4.2. Docker (necessário das Fases 4 em diante)

```powershell
$env:Path = "C:\Users\PoxZe\AppData\Local\Programs\DockerDesktop\resources\bin;$env:Path"
```

| Particularidade | Detalhe |
| --- | --- |
| Sandbox × Docker | O sandbox pode bloquear o named pipe. Se bloquear, usar `sandbox_permissions` com `justification` em português |
| Docker Desktop | **Não inicia sozinho**. Se o pipe não existir: `Start-Process 'C:\Users\PoxZe\AppData\Local\Programs\DockerDesktop\Docker Desktop.exe'` e aguardar ~5–30s |
| LocalStack | **SÓ** `localstack/localstack:4.0.3`. Tags `stable`/`latest` são **Pro** e morrem com *"License activation failed"* |
| Containers esperados | `jungleg-postgres`, `jungleg-keycloak`, `jungleg-localstack` — todos `healthy` |
| Bancos | `jungleg` (aplicação), `jungle_test` (testes de integração), `keycloak` |

### 4.3. Banco de teste

Os testes de integração **truncam tabelas**: apontar para `jungleg` apagaria dados de verdade.
Sempre usar o banco separado:

```powershell
docker compose up -d postgres
$env:DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungleg?sslmode=disable'
$env:TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
.\bin\migrate.exe up
go test -count=1 -race ./...
go test -count=1 -race -tags=integration -timeout 300s ./...
```

### 4.4. Line endings e outras ferramentas

- Scripts shell, SQL e Dockerfile rodam em Linux nos containers: garantir **LF** (o
  `.gitattributes` já normaliza).
- Go **1.26.5** · PowerShell **5.1** (sem `&&`, sem ternário) · git 2.52.0
- **`make` NÃO está instalado** — os alvos do Makefile são documentação executável
- **`package main` não pode ser importado nem testado** → toda lógica em `internal/`

---

## 5. DECISÕES TÉCNICAS JÁ CONGELADAS

**Estrutura de pacotes (reorganizada depois da Fase 5 — commit `2e4c2c0`):**

```
internal/domain/{errors,ids,money,ledger,wallet,wagering}   regra de negócio, sem I/O
internal/app/ports                                          CONTRATOS (interfaces + erros + Cursor + Clock)
internal/app/wagering                                       CASO DE USO (Process, OpenWallet, leituras, Reconcile)
internal/infra/postgres                                     adaptador do banco
internal/httpapi · internal/platform · internal/config · internal/observability
test/integration                                            integração do caminho completo
```

A direção das dependências é `app → ports ← infra`: a aplicação não importa o banco, e o
banco não importa a aplicação. Motivos e histórico em `ARCHITECTURE.md` §1. O caso de uso
depende de `ports.UnitOfWork`/`ports.Repos`, `ports.Clock` e `idgen.Generator` — nunca de
`infra/postgres`.

⚠️ **Os sentinelas de erro são os MESMOS objetos nos dois lados** (`postgres.ErrConflict` é
alias de `ports.ErrConflict`). Não recrie um jogo de erros na infra: `ports.IsConflict`
devolveria `false` para erro do repositório, **silenciosamente**. Há teste de regressão que
passa um `PgError` de verdade pela classificação.

⚠️ **`domainerrors.Error` é o tipo de erro dos QUATRO pacotes de domínio**, por alias
(`money`, `wallet`, `ledger`, `wagering`). Não redefina o struct em nenhum deles: um tipo
próprio fica **fora** da travessia do `errors.As` que o `domainerrors.CodeOf` usa, e o
`failureCode` da resposta sai **em branco** para todo erro daquele pacote — foi o defeito 13
da §15 do `ARCHITECTURE.md`, e passou despercebido porque cada pacote testava com a sua
própria cópia do extrator de Code.

⚠️ **O ledger é pacote próprio desde a reorganização**: `ledger.Entry`, `ledger.Direction`,
`ledger.NewEntry`. `wallet.Debit` devolve `Movement`, cujo `Entry()` é um `ledger.Entry`.

⚠️ **`TransactionID` é um tipo só** (`ids.TransactionID`), com alias em `wagering` e
`ledger`. Não crie conversão entre os dois: são o mesmo conceito, e uma conversão esquecida
viraria string vazia — exatamente o que `ledger.NewEntry` recusa.

⚠️ **Identificador que não é UUID não pode chegar ao banco**: `wallets.id`,
`wager_transactions.id` e `wallet_ledger_entries.wallet_id` são colunas `UUID`, e um texto
qualquer numa consulta a elas vira `22P02`, que o driver classifica como falha
**permanente** (500). O caso de uso confere o formato antes de consultar
(`internal/app/wagering/ids.go`) — e os dublês de teste geram UUID de verdade, para não
testarem um caminho que produção não tem.

Detalhamento completo em `PLANO.md` §1 (A1–A14). O que afeta as Fases 6–7:

| ID | Decisão |
| --- | --- |
| **A2** | `Money` = `int64` em unidades mínimas + `Currency` ISO 4217, escala 2, **nunca float**. Limite `9_000_000_000_000_000` |
| A2 | `New` valida moeda **e** faixa; `FromMinor` reidrata sem validar |
| A2 | JSON: `{"amount":"25.00","currency":"BRL"}`; `UnmarshalJSON` é estrito (campo desconhecido e chave repetida são erro) |
| **A3** | Uma **Unit of Work**: toda operação de negócio em **uma** transação (`wager_transactions` + `wallets` + `wallet_ledger_entries` + `inbox_messages` + `outbox_events`) |
| **A4** | Concorrência: **`READ COMMITTED` + `SELECT ... FOR UPDATE`** + versão. Não `SERIALIZABLE` |
| **A5** | Idempotência **persistente**: índice único parcial + comparação de hash canônico |
| **A8** | Tabela de `failureCode` estáveis — o `Code` do erro de domínio vira `failureCode` no HTTP |
| A9 | Contrato HTTP: `201` nova, `200` replay, `202` pendente, `422` rejeição, `409` conflito, `403` provedor errado |
| A9 | Ledger paginado: cursor opaco `base64url(createdAt RFC 3339Nano \| entryId)`, `limit` padrão 50 e **teto 200** |
| A10 | `providerId` vem do TOKEN; operação de outro provedor responde **não encontrado** (nunca 403, que confirmaria a existência dela) |
| A12 | Eventos com envelope: `eventId`, `eventType`, `aggregateId` (= `walletId`), `correlationId`, `causationId?`, `occurredAt`, `version`, `data` |

---

## 6. 🎯 A TAREFA PONTUAL: FASE 7 — HTTP + autenticação

### 6.1. O que a Fase 6 deixou pronto (não reimplementar)

`internal/app/wagering` tem o caso de uso COMPLETO. A Fase 7 é a borda: traduzir
HTTP em `Request`/`OpenWalletRequest` e o desfecho em resposta.

| Necessidade do handler | O que usar |
| --- | --- |
| Operação do provedor | `servico.Process(ctx, appwagering.Request{...})` → `Result` + erro |
| Abertura de carteira | `servico.OpenWallet(ctx, appwagering.OpenWalletRequest{...})` → `OpenWalletResult` |
| Leitura interna | `GetTransaction`, `GetWallet`, `ListLedger` |
| Leitura do provedor | `GetProviderTransaction(providerID, id)` e `GetByProviderExternalID(providerID, externalID)` — **já aplicam o isolamento** (operação de outro provedor = "não encontrado") |
| Reconciliação | `Reconcile(ctx, walletID)` → `Reconciliation` |
| Código de falha | `domainerrors.CodeOf(err)` — é `failureCode` no corpo de erro |
| Idempotência do corpo | `Request.IdempotencyKey` vem do header; o serviço recusa chave vazia com `IDEMPOTENCY_KEY_REQUIRED` |

`app/ports` ganhou `Clock` (implementação em `platform/clock.System`) e os
repositórios ganharam `GetByReferenceAndKind` e `CountByWallet`.

### 6.2. O que JÁ EXISTE em `internal/httpapi` (a Fase 0 deixou pronto)

Não recrie nada disto — **estenda**:

| Já existe | Onde |
| --- | --- |
| Middlewares: `RequestID`, `RealIP`, `Recoverer`, `AccessLog`, `Timeout`, `MaxBody` | `middleware.go`, `recover.go` |
| `APIError` com construtores tipados (`ErrBadRequest`, `ErrFailureBadRequest`, `ErrUnauthorized`, `ErrForbidden`, `ErrNotFound`, `ErrConflict`, `ErrUnprocessable`, `ErrUnavailable` com `Retry-After`, `ErrInternal`) e `WriteError` | `response.go` |
| `decodeJSON` estrito (campo desconhecido, chave repetida, `Content-Type`) | `response.go` |
| `Routes{Public, Internal, Provider}` + interface `Autenticador` | `router.go` |
| `/health/live`, `/health/ready` (com `health_checkers`), `/metrics` | `health.go`, `router.go` |

O que falta: o **`Autenticador` de verdade** (Keycloak/OIDC), os **handlers** com
DTOs, o **mapeamento desfecho→status**, o **módulo Fx do caso de uso** e as
pendências P1/P4/P5.

### 6.3. O mapeamento desfecho → HTTP (é o que o avaliador cobra)

| Situação | Status | Corpo |
| --- | --- | --- |
| Operação nova concluída | `201` | `{transactionId,status,balance,idempotentReplay:false}` |
| Replay idempotente | `200` | idem com `idempotentReplay:true` — **saldo do processamento original** |
| `PENDING_REFERENCE` | `202` | `{transactionId,status:"PENDING_REFERENCE",failureCode:null}` |
| Rejeição de negócio | `422` | `{transactionId,status:"REJECTED",failureCode}` |
| Abertura de carteira | `201` | `{id,playerId,balance,version}` (+ `idempotentReplay` no reenvio) |
| Reconciliação | `200` | `{walletId,storedBalance,calculatedBalance,difference,consistent,checkedEntries}` |
| Ledger paginado | `200` | `{entries:[...],nextCursor}` |

Erros (`WriteError` + `domainerrors.CodeOf`):

| Erro | Status |
| --- | --- |
| `ErrIdempotencyConflict`, `ErrWalletAlreadyExists` | `409` |
| `ErrWalletNotFound`, `ErrTransactionNotFound` | `404` |
| Códigos de entrada inválida (`INVALID_REQUEST`, `IDEMPOTENCY_KEY_REQUIRED`, `INVALID_AMOUNT`, `REFERENCE_REQUIRED`, `INVALID_TRANSACTION`, `INVALID_CURRENCY`, cursor inválido) | `400` |
| `OPERATION_NOT_ALLOWED` (`OPENING` de fora) | `403` |
| `ports.IsRetryable(err)` (transitório esgotado) | `503` + `Retry-After` |
| Qualquer outro | `500` (sem vazar stack nem erro de driver) |

⚠️ **Nunca** mapear `error` de infraestrutura para `4xx`: a diferença entre
"o cliente errou" e "o serviço está indisponível" é contrato (README §9).

### 6.4. Autenticação e isolamento (A10 — é eliminatório)

- OIDC discovery + JWKS com cache e rotação; valida `iss`, `aud`, `exp`, `nbf`, `alg` `RS256`;
- **o `providerId` vem do TOKEN**, nunca do corpo: `POST /wagering/transactions` com
  `providerId` diferente do claim ⇒ `403`; `GET /providers/:providerId/...` idem;
- `POST /wallets`, `GET /wallets*`, `POST /wallets/:id/reconciliation` ⇒ role `internal`
  (provedor recebe `403` **sem efeito financeiro**);
- token ausente/inválido ⇒ `401`;
- IdP indisponível ⇒ `503` e **nenhum efeito financeiro** (o teste tem que conferir o banco);
- os endpoints públicos seguem públicos: `/health/live`, `/health/ready`, `/metrics`.

### 6.5. Pendências que são DESTA fase

| Pendência | O que fazer |
| --- | --- |
| **P1** | `cmd/api` ganhar a flag `-health-check` (o healthcheck do `api` no compose a usa) |
| **P4** | `POST /wallets` de verdade — hoje o sub-roteador interno está montado em `/internal/wallets` |
| **P5** | registrar Postgres (e SQS, na Fase 8) no grupo `health_checkers` do Fx |
| **P2** | conferir a ordem `AccessLog` × `Recoverer` (o log precisa registrar o que o recover converteu) |
| **P7** | `StopTimeout` do Fx vindo da config, não da constante em `app.go` |

### 6.6. Fx

- `fx.Module("infra")`: `postgres.NewPool` + `postgres.NewUnitOfWork` (a aplicação
  recebe `ports.UnitOfWork`);
- `fx.Module("usecase")`: `appwagering.New(uow, clock.System{}, idgen.New())`;
- `fx.Module("auth")`: o `Autenticador` (OIDC);
- `fx.Module("http")`: `httpapi.NewRouter` já existe — as rotas novas entram nele;
- `app.ValidateApp()` já valida o grafo sem subir nada: use no teste de composição.

### 6.7. Testes que a fase PRECISA ter

- `httptest` com **servidor OIDC de teste assinando de verdade** (chave gerada no
  teste, JWKS servido localmente) — sem isso, o teste de autenticação não prova nada;
- token ausente / inválido / expirado / assinatura de outra chave / role errada /
  provedor errado;
- **isolamento**: provedor A não lê nem replica operação do provedor B (inclusive em
  replay), e o corpo não escolhe o provedor;
- **sem efeito financeiro** em acesso negado: afirmar o saldo e o ledger no banco
  depois do `403`/`401`;
- contrato de status: `201`/`200`/`202`/`422`/`409`/`404`/`400`/`503`/`400` de cursor;
- IdP fora ⇒ `503`.

### 6.8. Checkpoint obrigatório

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'; $env:GOCACHE='E:\jungleg\.gocache\build'; $env:GOPATH='E:\jungleg\.gocache\gopath'
docker compose up -d postgres
# migrations EMBUTIDAS no binário: rebuilde depois de criar/alterar migration
go build -o bin\migrate.exe .\cmd\migrate\
$env:DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungleg?sslmode=disable'
.\bin\migrate.exe up
go build ./... ; go vet ./... ; go vet -tags=integration ./... ; gofmt -l .\internal\ .\cmd\ .\migrations\ .\test\
go test -count=1 -race ./...
$env:TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
go test -count=1 -race -tags=integration -timeout 300s ./...
```

> `gofmt -l .` na raiz lista o cache de módulos (`.gocache/mod`) — formatar
> dependência de terceiros não é nosso. Por isso a checagem é escopada.

Commit esperado ao fim do bloco:
```
feat(http): adiciona endpoints de wagering e carteira com autenticacao OIDC
```

---

## 7. PENDÊNCIAS CONHECIDAS

Do `CONTEXTO.md` §4: P1 (healthcheck do `api` no compose usa `-health-check`, flag que o
binário não implementa — **é da Fase 7**), P2 (ordem `AccessLog` × `Recoverer`), P3
(`net.Listen` em vez de `select`+`time.After`), P4 (path `/internal/wallets` — o enunciado
pede `POST /wallets`), P5 (registrar Postgres/SQS no grupo `health_checkers`), P7
(`StopTimeout` deve vir da config), P8 (testar SIGTERM real).

**Resolvidas:** P6 (`cmd/migrate` existe), `canonicaljson` criado, padding do `CHAR(3)`
tratado com `TrimSpace` e testado, `Movement` resolvendo a versão do UPDATE condicional,
`CodeOf` unificado em `domainerrors`, `wagering.Error` virou alias (defeito 13 da §15 do
`ARCHITECTURE.md`), `LOSS` com referência decidido (informativa — `ARCHITECTURE.md` §9 e
`FASE_6.md` §8).

**Abertas:**
- **`ARCHITECTURE.md` existe** (criado em `aaa6c24`) e está atualizado até a Fase 6: 17 seções com
  marcadores `[PRONTO]` / `[PARCIAL]` / `[PLANEJADO]`. Faltam preencher as seções 10 (HTTP —
  Fase 7), 11 (SQS — Fase 8) e 12 (observabilidade de negócio — Fase 11);
- `testcontainers-go` foi baixado e **não é usado** (os testes apontam para o Postgres do
  compose via `TEST_DATABASE_URL`). Remover com `go mod tidy` na Fase 12;
- **métricas de negócio** (`wager_operations_total{kind,status}`, `wager_idempotent_replays_total`)
  ficaram para a Fase 11 de propósito, para o caso de uso não nascer acoplado ao Prometheus —
  inclusive o **log e a métrica de divergência da reconciliação**;
- `migrate.exe` sobrando na raiz (além de `bin/migrate.exe`): lixo de build, só limpar;
- **`bin/migrate.exe` carrega as migrations embutidas** (`go:embed`): depois de criar ou alterar
  uma migration, é preciso rebuidá-lo, senão ele responde "schema já está atualizado" e não
  aplica nada (aconteceu na 000002);
- a ordem entre os **dois eventos da mesma transação** não é garantida (mesmo `occurred_at`) e a
  ordenação da outbox depende do relógio — as duas estão registradas como limitação na §16 do
  `ARCHITECTURE.md`, com o motivo de não terem sido resolvidas agora;
- o **`wager_transactions` de `OPENING`** tem `game_id` e `round_id` vazios por decisão
  (migration 000002); a reversão dessa migration exige decidir o que fazer com essas linhas — a
  condição está documentada no próprio `.down.sql`.

---

## 8. CHECKLIST DE ARRANQUE PARA A NOVA CONVERSA

- [ ] Ler `README.md` (o enunciado é a fonte de verdade), `ARCHITECTURE.md`, `PLANO.md` §3 (Fase 7) e `docs/evidencias/FASE_6.md`
- [ ] Confirmar containers de pé (`docker ps`) e o schema aplicado (`bin/migrate.exe version` — deve dizer **2**)
- [ ] **Confirmar com o usuário que a estratégia mudou** (seção 3.1): a IA escreve o código e explica depois
- [ ] **Estender** `internal/httpapi` (middlewares, `APIError`/`WriteError`, `Routes`, health e métricas já existem) — não recriar
- [ ] Implementar o `Autenticador` de verdade (OIDC/Keycloak) e o isolamento por `provider_id` do token (seção 6.4) — o `providerId` do CORPO não escolhe o provedor
- [ ] Mapear `Result`/erros para os status da seção 6.3, sem nunca vazar stack nem erro de driver, e sem transformar falha de infraestrutura em `4xx`
- [ ] Ligar o caso de uso no Fx (`fx.Module("usecase")` com `appwagering.New(uow, clock.System{}, idgen.New())`)
- [ ] Resolver P1 (`-health-check`), P4 (`POST /wallets`), P5 (health checkers), P2 (ordem dos middlewares) e P7 (`StopTimeout` da config)
- [ ] Testar com servidor OIDC de teste **assinando de verdade**: token ausente/inválido/expirado, role errada, provedor errado, isolamento em replay e leitura, e **nenhum efeito financeiro** em acesso negado (conferir o banco)
- [ ] Aceitar contestação do usuário: se ele questionar e estiver certo, **admitir e corrigir**
- [ ] Depois de aprovado, commitar: `feat(http): adiciona endpoints de wagering e carteira com autenticacao OIDC`
- [ ] Lembrar do **compromisso final** (seção 3.5): `docs/SISTEMA_EXPLICADO.md` e `docs/ENTREVISTA.md`

**As Fases 0 a 6 estão commitadas e validadas. Não revalide nem recommite.**
