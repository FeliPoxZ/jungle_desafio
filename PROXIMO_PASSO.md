# PROXIMO_PASSO.md — Handoff para a próxima conversa

> **INSTRUÇÃO PARA A NOVA CONVERSA (leia isto primeiro):**
> Este arquivo é um handoff. Ele diz **onde o projeto está, o que já foi decidido, qual é o
> método de trabalho acordado e exatamente qual é a próxima tarefa pontual**.
>
> Antes de responder qualquer coisa, leia também, nesta ordem:
> 1. `CONTEXTO.md` (raiz) — estado geral, ambiente, decisões congeladas, pendências
> 2. `PLANO.md` §3 — checklist por fase, com a Fase 6 detalhada
> 3. `docs/evidencias/FASE_1.md` a `FASE_5.md` — provas do que já funciona
> 4. `docs/LEDGER_VS_WALLET.md` — a distinção central do domínio financeiro
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
| Fase 6 (caso de uso `wagering.Service`) | ⬅ **PRÓXIMA** |
| Fases 7–12 | ⬜ Não iniciadas (ver checklist no `PLANO.md` §3) |

**Domínio (Fases 1–3): 574 casos verdes. Persistência (Fase 5): 38 testes.
`go test -race ./...` verde nos 10 pacotes.** Árvore git limpa.

### Histórico de commits (mais recente primeiro)

```
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

> ✅ **As Fases 0 a 5 estão commitadas e validadas. Não revalide nem recommite.**

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
internal/app/ports                                          CONTRATOS (interfaces + erros + Cursor)
internal/infra/postgres                                     adaptador do banco
internal/httpapi · internal/platform · internal/config · internal/observability
```

A direção das dependências é `app → ports ← infra`: a aplicação não importa o banco, e o
banco não importa a aplicação. Motivos e histórico em `ARCHITECTURE.md` §1.

⚠️ **Os sentinelas de erro são os MESMOS objetos nos dois lados** (`postgres.ErrConflict` é
alias de `ports.ErrConflict`). Não recrie um jogo de erros na infra: `ports.IsConflict`
devolveria `false` para erro do repositório, **silenciosamente**. Há teste de regressão que
passa um `PgError` de verdade pela classificação.

⚠️ **O ledger é pacote próprio desde a reorganização**: `ledger.Entry`, `ledger.Direction`,
`ledger.NewEntry`. `wallet.Debit` devolve `Movement`, cujo `Entry()` é um `ledger.Entry`.

⚠️ **`TransactionID` é um tipo só** (`ids.TransactionID`), com alias em `wagering` e
`ledger`. Não crie conversão entre os dois: são o mesmo conceito, e uma conversão esquecida
viraria string vazia — exatamente o que `ledger.NewEntry` recusa.

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
| A12 | Eventos com envelope: `eventId`, `eventType`, `aggregateId` (= `walletId`), `correlationId`, `causationId?`, `occurredAt`, `version`, `data` |

---

## 6. 🎯 A TAREFA PONTUAL: FASE 6 (caso de uso `wagering.Service`)

**Esta é a fase mais importante do desafio** (o plano estima ~4h). É onde o domínio, o banco
e as regras de negócio se encontram: uma requisição do provedor vira uma transação SQL única
que decide, movimenta dinheiro e enfileira eventos.

### 6.1. O que as Fases 1–5 deixaram pronto

| Necessidade do serviço | O que usar |
| --- | --- |
| Valor, parsing e formatação | `money.Parse`, `Money.Add/Sub/Neg/Cmp`, `String`/JSON |
| Validar tipo, valor e origem | `wagering.New` (já valida `LOSS` = 0, reversão exige referência, `OPENING` é interna) |
| Transições de estado | `MarkProcessed`, `MarkRejected`, `MarkFailed`, `MarkPendingReference`, `ResolveReference` |
| Saber se movimenta dinheiro | `kind.MovesMoney()` |
| Aplicar a movimentação | `wallet.Debit`/`Credit` → devolvem **`Movement`** (com a `VersionBefore`) |
| Persistir a movimentação | `r.Wallets.Update(ctx, id, mov.VersionBefore(), carteira.Balance())` + `r.Ledger.Insert(ctx, mov.Entry())` |
| Uma transação para tudo | `uow.Run(ctx, func(ctx, r) error)` |
| Lock da carteira | `r.Wallets.GetByIDForUpdate` |
| Idempotência | `r.Transactions.GetByIdempotencyKey` + `UNIQUE` no banco; `MatchesPayload` separa replay de conflito |
| Hash do payload | `canonicaljson.Hash(payload)` |
| Eventos na outbox | `ProcessedEvent`/`RejectedEvent`/`PendingReferenceEvent`/`NewBalanceChangedEvent` + `r.Outbox.Insert` |
| Classificar falha do banco | `postgres.IsConflict`, `IsRetryable`, `IsNotFound` |

### 6.2. A ordem exata dentro da transação (é isso que o avaliador cobra)

```
1. valida Idempotency-Key e calcula o hash canônico
2. SELECT por (provider_id, idempotency_key)
     ├─ existe e hash igual   → REPLAY: devolve o resultado persistido, sem movimentar
     └─ existe e hash diferente → CONFLITO (409)
3. SELECT carteira FOR UPDATE        ← o lock vem ANTES de qualquer cálculo
4. valida moeda da carteira × moeda da operação
5. resolve a referência (REFUND/ROLLBACK)
     ├─ ausente  → MarkPendingReference + evento + 202, sem movimentar
     └─ presente → valida provedor/jogador/carteira/moeda/rodada e valor igual
6. cria a transação (wagering.New) e grava (INSERT)
7. aplica a movimentação no agregado (Debit/Credit → Movement)
8. grava saldo + versão (Wallets.Update com mov.VersionBefore())
9. grava o lançamento no ledger (Ledger.Insert)
10. transiciona e grava o estado (MarkProcessed/MarkRejected + Transactions.Save)
11. grava os eventos na outbox
12. COMMIT  ← só depois disso a resposta sai
```

**O lock no passo 3 é o que impede lost update.** Se o `SELECT` da carteira vier depois do
cálculo do saldo, dois escritores leem o mesmo valor e um sobrescreve o outro.

### 6.3. Regras de negócio que faltam implementar

| Regra (README) | O que fazer |
| --- | --- |
| `BET` exige saldo suficiente | `wallet.Debit` devolve `ErrInsufficientFunds`; traduzir para `failureCode: INSUFFICIENT_FUNDS` e `422` |
| `WIN` pode referenciar aposta da mesma rodada | referência **opcional**: se enviada, validar rodada/provedor/jogador/carteira/moeda |
| `LOSS` não movimenta | `kind.MovesMoney()` é falso: não chama `Debit`/`Credit`, não cria ledger, não incrementa versão. Ainda produz `WagerTransactionProcessed` |
| `REFUND` devolve integralmente a `BET` | referência obrigatória; valor **igual** ao referenciado; erro diferente de "sem saldo" |
| `ROLLBACK` desfaz `BET`/`WIN`/`REFUND` | movimento contrário ao original; se precisaria debitar mais que o saldo → `ROLLBACK_INSUFFICIENT_FUNDS` (código **diferente** de `BET` sem saldo) |
| Reversão não pode acontecer duas vezes | `UNIQUE (reference_transaction_id, kind)` já barra; traduzir para `ALREADY_REVERSED` |
| Referência existe mas foi rejeitada/falhou | `REFERENCE_NOT_PROCESSABLE` |
| Referência diverge em provedor/jogador/carteira/moeda/rodada | `REFERENCE_MISMATCH` |
| TTL/tentativas de referência pendente | ao esgotar: `MarkRejected` com `REFERENCE_NOT_FOUND` + evento de rejeição |
| `OPENING` interno (abertura de carteira) | caminho separado: cria carteira, `OPENING` em `PROCESSED`, lançamento de crédito, eventos `Processed` + `BalanceChanged`. Saldo zero ⇒ **sem** `OPENING`, sem ledger, sem eventos |

### 6.4. Ponto de arquitetura que precisa de decisão

Onde ficam as **interfaces de repositório** que o serviço consome? Hoje elas estão em
`internal/infra/postgres`, o que faria o pacote de aplicação importar infraestrutura. Opções:

| Opção | Prós | Contras |
| --- | --- | --- |
| Definir as interfaces em `internal/app/ports` e o `postgres` satisfazê-las | Aplicação não importa infraestrutura; testável com dublê | Mais um pacote |
| Consumir as interfaces de `internal/infra/postgres` direto | Menos código | Acopla aplicação à infra; o `PLANO.md` A3 diz o contrário |

**Recomendação:** `internal/app/ports` com interfaces estreitas. O `Repos` do `uow` já é uma
struct concreta — o serviço pode receber uma interface `UnitOfWork` que devolve `ports.Repos`.

### 6.5. Casos de teste que a suíte PRECISA cobrir

**Idempotência:** mesma chave + mesmo hash → replay devolve o **saldo original** (mesmo que a
carteira já tenha mudado depois); mesma chave + hash diferente → 409; `external_transaction_id`
com chave diferente → 409
**Tipos:** `BET` com saldo e sem saldo; `WIN` credita; `LOSS` não move, não cria ledger e não
incrementa versão mas produz evento; `REFUND` devolve; `ROLLBACK` desfaz
**Reversão:** referência inexistente → `PENDING_REFERENCE` + 202; rejeitada →
`REFERENCE_NOT_PROCESSABLE`; valor diferente → erro; `REFUND` duplicado → `ALREADY_REVERSED`;
`ROLLBACK` sem saldo → `ROLLBACK_INSUFFICIENT_FUNDS`
**Atomicidade:** falha em qualquer passo não deixa saldo, ledger nem evento
**Concorrência:** duas `BET` de 80 sobre saldo de 100 (o cenário do README) → uma processa, a
outra rejeita por saldo, saldo final **20.00** e **um** débito no ledger
**Abertura:** `POST /wallets` com saldo positivo cria `OPENING` + crédito + 2 eventos; com
zero não cria `OPENING`; duplicado → conflito
**Eventos:** `LOSS` e rejeição **não** geram `WalletBalanceChanged`; `Processed` inclui `LOSS`

### 6.6. Checkpoint esperado

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'; $env:GOCACHE='E:\jungleg\.gocache\build'; $env:GOPATH='E:\jungleg\.gocache\gopath'
docker compose up -d postgres
$env:DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungleg?sslmode=disable'
.\bin\migrate.exe up
go build ./...; go vet ./...; go test -race ./...
$env:TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
go test -count=1 -race -tags=integration -timeout 300s ./...
```

Commit ao final:
```
feat(app): adiciona caso de uso de operacoes com idempotencia e ledger transacional
```

---

## 7. PENDÊNCIAS CONHECIDAS

Do `CONTEXTO.md` §4: P1 (healthcheck do `api` no compose usa `-health-check`, flag que o
binário não implementa — **é da Fase 7**), P2 (ordem `AccessLog` × `Recoverer`), P3
(`net.Listen` em vez de `select`+`time.After`), P4 (path `/internal/wallets` — o enunciado
pede `POST /wallets`), P5 (registrar Postgres/SQS no grupo `health_checkers`), P7
(`StopTimeout` deve vir da config), P8 (testar SIGTERM real).

**Resolvidas:** P6 (`cmd/migrate` existe), `canonicaljson` criado, padding do `CHAR(3)`
tratado com `TrimSpace` e testado, `Movement` resolvendo a versão do UPDATE condicional.

**Abertas:**
- **`ARCHITECTURE.md` FOI CRIADO** (era o maior risco): 17 seções com marcadores de estado
  `[PRONTO]` / `[PARCIAL]` / `[PLANEJADO]`, para ser atualizado a cada fase. As seções 10
  (HTTP), 11 (SQS), 12 (observabilidade) e 13 (recuperação) são as que faltam preencher;
- o helper `CodeOf` **RESOLVIDO**: virou `domainerrors.CodeOf` no pacote compartilhado;
- `testcontainers-go` foi baixado e **não é usado** (os testes apontam para o Postgres do
  compose via `TEST_DATABASE_URL`). Remover com `go mod tidy` na Fase 12;
- `LOSS` com referência opcional: decidir na Fase 6 se valida quando enviada;
- **`ARCHITECTURE.md` ainda não existe** e é entrega obrigatória do enunciado. É o risco mais
  alto do projeto hoje — as decisões estão no `PLANO.md` e nas evidências, mas o avaliador
  procura o arquivo. Fazer **antes** do fim, não deixar para a Fase 12.

---

## 8. CHECKLIST DE ARRANQUE PARA A NOVA CONVERSA

- [ ] Ler `CONTEXTO.md`, `PLANO.md` §3 (Fase 6) e `docs/evidencias/FASE_5.md`
- [ ] Confirmar containers de pé (`docker ps`) e o schema aplicado (`bin/migrate.exe version`)
- [ ] **Confirmar com o usuário que a estratégia mudou** (seção 3.1): a IA escreve o código e explica depois
- [ ] **Decidir onde ficam as interfaces de repositório** (seção 6.4): `internal/app/ports`
- [ ] Implementar `Process(ctx, Request)` na **ordem exata da seção 6.2** — o lock da carteira vem ANTES de qualquer cálculo de saldo
- [ ] Reusar `uow.Run`, `Movement`, `canonicaljson.Hash` e os eventos do `wagering` em vez de reimplementar
- [ ] Cobrir os casos da seção 6.5, em especial o cenário do README: duas `BET` de 80 sobre saldo 100 → **saldo final 20.00 e UM débito no ledger**
- [ ] Testar idempotência de verdade: replay devolve o saldo **original**, mesmo com a carteira já alterada
- [ ] Aceitar contestação do usuário: se ele questionar e estiver certo, **admitir e corrigir**
- [ ] Depois de aprovado, commitar: `feat(app): adiciona caso de uso de operacoes com idempotencia e ledger transacional`
- [ ] Lembrar do **compromisso final** (seção 3.5): `docs/SISTEMA_EXPLICADO.md` e `docs/ENTREVISTA.md`

**As Fases 0 a 5 estão commitadas e validadas. Não revalide nem recommite.**
