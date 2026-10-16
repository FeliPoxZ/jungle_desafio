# PROXIMO_PASSO.md — Handoff para a próxima conversa

> **INSTRUÇÃO PARA A NOVA CONVERSA (leia isto primeiro):**
> Este arquivo é um handoff. Ele diz **onde o projeto está, o que já foi decidido, qual é o
> método de trabalho acordado e exatamente qual é a próxima tarefa pontual**.
>
> Antes de responder qualquer coisa, leia também, nesta ordem:
> 1. `CONTEXTO.md` (raiz) — estado geral, ambiente, decisões congeladas, pendências
> 2. `PLANO.md` §3 — checklist por fase, com a Fase 4 detalhada
> 3. `docs/evidencias/FASE_1.md`, `FASE_2.md` e `FASE_3.md` — provas do que já funciona
> 4. `docs/LEDGER_VS_WALLET.md` — a distinção central do domínio financeiro
>
> **Não comece a codar sem ler o método de trabalho (seção 3).**

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
| Fase 5 (persistência `pgx` + Unit of Work) | ⬅ **PRÓXIMA** |
| Fases 6–12 | ⬜ Não iniciadas (ver checklist no `PLANO.md` §3) |

**Domínio completo (Fases 1–3): 574 casos verdes, `go test -race ./...` verde nos 8 pacotes.**

### O domínio está completo (Fases 1–3)

| Pacote | O que oferece |
| --- | --- |
| `internal/domain/money` | Value object monetário: `int64` em unidades mínimas, escala 2, sem float, limite `9_000_000_000_000_000`, aritmética com guarda de overflow nos dois sentidos, comparação, JSON |
| `internal/domain/wallet` | Carteira (saldo, versão, instantes) + ledger append-only. `apply` é o único ponto que muda saldo; valor zero não gera lançamento |
| `internal/domain/wagering` | Transação de aposta: 6 tipos, 5 estados com máquina de transições, validação por origem (interna × externa), `failureCode`, eventos de domínio |

**O que o domínio NÃO faz (e é Fase 5/6):** movimentar dinheiro por operação, consultar
idempotência existente, comparar metadados com a referência carregada, publicar evento.

---

## 3. ⭐ MÉTODO DE TRABALHO (NÃO NEGOCIÁVEL)

| Regra | Como aplicar |
| --- | --- |
| **MODO B: linha a linha, juntos** | A IA explica um **bloco pequeno**, o usuário digita, e só então vem o próximo. A IA **não escreve o código do núcleo no arquivo** |
| **Explicar ANTES de escrever** | Regra, conceito, exigência do enunciado e o "porquê" vêm **antes** do código |
| **Um bloco por vez, com checkpoint** | Esperar o usuário dizer "bloco N pronto" |
| **O núcleo é do usuário** | `Money`, `Wallet`, `LedgerEntry`, `WagerTransaction` são escritos por ELE. Infraestrutura (compose, migrations, repos, workers, testes de integração) a IA pode escrever |
| **Aceitar contestação** | ⭐ O usuário questiona e isso é bom. Se ele estiver certo, **admitir e corrigir** |
| **Quando usar `write`/`edit`** | Preferir que o usuário digite. Nunca "corrigir" o arquivo dele em silêncio |
| **Formato da entrega de bloco** | 1) objetivo 2) o problema antes do código 3) decisões e o porquê (tabela) 4) código para digitar 5) tabela explicando cada parte 6) checkpoint |

**⭐ Princípios de qualidade já cobrados:**

> **1. Antes de escrever helper, verificar se já existe na stdlib.** "Evitar um import" nunca
> é justificativa.

> **2. Antes de afirmar que uma guarda é necessária (ou suficiente), provar com teste.**
> Na Fase 1 a IA afirmou duas vezes "uma guarda basta" e as duas estavam erradas — a
> varredura exaustiva de 81 combinações achou 10 furos no `Add`. **Varredura > raciocínio**
> em sinal, limite e overflow.

> **3. Comentário só onde explica decisão.** Código enxuto.

> **4. Quando um teste falhar, verificar se a régua está certa antes de acusar o código.**
> A IA quase acusou o código do usuário três vezes por expectativa errada do próprio teste.

> **5. Cuidado com `Set-Content` no PowerShell 5.1:** grava em ANSI e **corrompe acentos**.
> Usar a ferramenta de edição ou `[System.IO.File]::WriteAllLines` com `UTF8Encoding($false)`.
> Já aconteceu uma vez neste projeto.

> **6. Não confie em contagem de falha tirada de pipeline do PowerShell.** Um
> `Select-String` sem `-Pattern` já produziu um "24 falhas" falso neste projeto. Ler o
> output real do `go test`.

**Alternativa se o relógio apertar:** o usuário autorizou que a IA complete o código sozinha
se o tempo estiver acabando. Avisar antes e explicar. **Foi o que aconteceu nas Fases 2 e 3.**

---

## 4. AMBIENTE — DETALHES QUE CAUSAM ERRO SE IGNORADOS

### 4.1. Cache do Go (OBRIGATÓRIO)

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'
$env:GOCACHE='E:\jungleg\.gocache\build'
$env:GOPATH='E:\jungleg\.gocache\gopath'
```

O cache global `C:\Users\PoxZe\go` **não é gravável**. O `Makefile` já faz isso.

### 4.2. Docker ( **obrigatório a partir da Fase 4** )

```powershell
$env:Path = "C:\Users\PoxZe\AppData\Local\Programs\DockerDesktop\resources\bin;$env:Path"
```

| Particularidade | Detalhe |
| --- | --- |
| Sandbox × Docker | O sandbox do harness **não abre o named pipe**. Comandos Docker exigem `sandbox_permissions: danger-full-access` com `justification` em português |
| Docker Desktop | **Não inicia sozinho**. Se o pipe não existir: `Start-Process 'C:\Users\PoxZe\AppData\Local\Programs\DockerDesktop\Docker Desktop.exe'` e aguardar ~5–30s |
| LocalStack | **SÓ** `localstack/localstack:4.0.3`. Tags `stable`/`latest` são **Pro** e morrem com *"License activation failed"* |
| Containers esperados | `jungleg-postgres`, `jungleg-keycloak`, `jungleg-localstack` — todos `healthy` na Fase 0 |

### 4.3. Line endings

Scripts shell, SQL e Dockerfile rodam em Linux nos containers: garantir **LF**, senão
`syntax error: unexpected end of file`. O `.gitattributes` já normaliza.

### 4.4. Outras ferramentas

- Go **1.26.5** · PowerShell **5.1** (sem `&&`, sem ternário) · git 2.52.0
- **`make` NÃO está instalado** — os alvos do Makefile são documentação executável
- **`package main` não pode ser importado nem testado** → toda lógica em `internal/`

---

## 5. DECISÕES TÉCNICAS JÁ CONGELADAS

Detalhamento completo em `PLANO.md` §1 (A1–A14). O que afeta a Fase 4:

| ID | Decisão |
| --- | --- |
| **A2** | `Money` = `int64` em unidades mínimas + `Currency` ISO 4217. Persistência: **`BIGINT`** para valor + **`CHAR(3)`** para moeda. **Nunca `NUMERIC`/`FLOAT`** para dinheiro no nosso modelo |
| A2 | Limite do domínio: `\|minor\| <= 9_000_000_000_000_000` → **`CHECK`** na coluna |
| A3 | Uma **Unit of Work** (`internal/infra/postgres/uow.go`): toda operação de negócio em **uma** transação: `wager_transactions` + `wallets` + `wallet_ledger_entries` + `inbox_messages` + `outbox_events` |
| A4 | Concorrência por carteira: `SELECT ... FOR UPDATE` + `version` (controle otimista). Documentar em `ARCHITECTURE.md` |
| A5 | Idempotência **persistente**: `UNIQUE (provider_id, idempotency_key)` e comparação de hash de payload |
| A8 | Tabela de `failureCode` estáveis (o `Code` do erro de domínio vira `failureCode` no HTTP) |
| A12 | Eventos: envelope com `eventId`, `eventType`, `aggregateId`, `correlationId`, `causationId?`, `occurredAt`, `version`, `data` |

---

## 6. 🎯 A TAREFA PONTUAL: FASE 5 (persistência `pgx` + Unit of Work)

**Esta é a fase em que o domínio passa a ter memória.** As Fases 2–4 definiram as regras e as
imposeram no banco; agora é preciso gravar e ler de verdade, com a garantia de que carteira,
ledger, transação, inbox e outbox mudam **na mesma transação**.

### 6.1. O que já existe para reusar

| Necessidade | O que usar |
| --- | --- |
| O schema e as constraints | `migrations/000001_init.up.sql` — cobrir TODAS as colunas |
| Aplicar migrations | `cmd/migrate` (`up`/`down`/`version`) |
| Limite do domínio | `money.FromMinor` para reidratar, `New` para criar |
| Reidratar carteira | `wallet.Rehydrate(...)` (não valida de propósito) |
| Reidratar lançamento | `wallet.RehydrateEntry(...)` |
| Reidratar transação | `wagering.Rehydrate(...)` |
| Direção e tipo do banco | `wallet.ParseDirection`, `wagering.ParseKind`, `wagering.ParseStatus` |
| URL do banco | `DATABASE_URL` (já é lida por `internal/config`) |

### 6.2. Entregáveis (`PLANO.md` §3, Fase 5)

| Item | Conteúdo |
| --- | --- |
| `internal/infra/postgres/pool.go` | `pgxpool` com `MaxConns`/timeouts da config + `Ping` para readiness |
| `internal/infra/postgres/uow.go` | `uow.Run(ctx, fn func(ctx, Repos) error)`: `BeginTx`/`Commit`/`Rollback` com `defer` |
| `Internal/infra/postgres/wallet_repo.go` | `Insert`, `GetByID`, `GetByIDForUpdate` (**row lock**), `GetByPlayerAndCurrency`, `UpdateBalance` |
| `internal/infra/postgres/txn_repo.go` | `Insert`, `GetByID`, `GetByProviderAndExternalID`, `GetByIdempotencyKey`, transições, `ListPendingReferencesDue`, `ListByWallet` (cursor) |
| `internal/infra/postgres/ledger_repo.go` | `Insert` (append-only), `ListByWalletCursor`, `SumSignedByWallet` (reconciliação) |
| `internal/infra/postgres/inbox_repo.go` | `InsertIfAbsent`, `Get`, `MarkCompleted`, `MarkFailed` |
| `internal/infra/postgres/outbox_repo.go` | `Insert`, `ClaimBatch` (`FOR UPDATE SKIP LOCKED`), `MarkPublished`, `MarkRetry`, `PendingStats` |
| `internal/infra/postgres/errors.go` | `pgconn.PgError` → `ErrConflict` (23505), `ErrCheck` (23514), `ErrTransient` (40001, 40P01, 57P03, 53300), `ErrPermanent` |
| `internal/platform/canonicaljson` | **pendência da Fase 3**: o `PayloadEncoder` está injetado e ainda não tem implementação canônica |
| Testes de integração | `//go:build integration` com testcontainers, ou contra o Postgres do compose |

### 6.3. Pontos que o avaliador vai cobrar

1. **`SELECT ... FOR UPDATE` na carteira antes de calcular o saldo novo.** É o que impede lost update entre duas instâncias. Ler o saldo sem lock e depois gravar é o bug que o enunciado chama de eliminatório ("saldo negativo por concorrência").
2. **Ordem das operações na transação.** Lock da carteira → validar no domínio → gravar ledger → gravar transação → outbox. Se o lock vier depois da validação, dois escritores leem o mesmo saldo.
3. **`ReadCommitted` + `FOR UPDATE`, não `Serializable`.** `Serializable` gera `40001` sob disputa e exigiria retry em todo caminho; o lock explícito de linha resolve o caso real (uma carteira por vez) sem serializar o banco inteiro. Documentar em `ARCHITECTURE.md`.
4. **Erro do `pgx` vira erro tipado do domínio.** `23505` (unique) **não** é "erro de banco": no nosso schema, o unique é a idempotência funcionando. Precisa virar `ErrConflict` para o serviço decidir entre replay e conflito.
5. **`ClaimBatch` com `FOR UPDATE SKIP LOCKED`.** É o que permite várias instâncias publicarem a outbox sem pegarem o mesmo evento.
6. **`SumSignedByWallet` somando `CASE direction`** — a base da reconciliação da Fase 11. Se a soma dos lançamentos não bater com o saldo, o sistema precisa avisar.
7. **Reidratar sem reaplicar.** Os repositórios devolvem entidades via `Rehydrate`/`RehydrateEntry`, nunca chamando `Debit`/`Credit` na leitura.

### 6.4. Cuidados com o schema já criado

| Coluna | Detalhe que morde |
| --- | --- |
| `currency CHAR(3)` | `CHAR` preenche com espaço: o driver pode devolver `"BRL"` mas também pode devolver com padding. **Testar** e, se necessário, `TrimSpace` na leitura |
| `created_at`/`updated_at` | `updated_at` é atualizado por **trigger**: não enviar valor na leitura nem tentar forçar |
| `payload_hash` | é `TEXT` com o SHA-256 em hexadecimal (64 chars) |
| `result_balance_minor` | `NULL` enquanto a operação não terminou — precisa de `*int64` e depois `money.FromMinor` |
| `reference_transaction_id` | auto-relacionamento: ao inserir uma reversão, a transação referenciada já precisa existir |
| Ledger append-only | `UPDATE`/`DELETE` levantam exceção. O repositório **só** pode `INSERT` — não criar método de correção |

### 6.5. Checkpoint esperado

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'; $env:GOCACHE='E:\jungleg\.gocache\build'; $env:GOPATH='E:\jungleg\.gocache\gopath'
docker compose up -d postgres
$env:DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungleg?sslmode=disable'
.\bin\migrate.exe up
go build ./...; go vet ./...; go test -count=1 ./...
# integração (contra o Postgres do compose ou testcontainers)
go test -count=1 -race -tags=integration ./test/...
```

Commit ao final:
```
feat(postgres): adiciona repositorios pgx com unit of work e locking por carteira
```

---

## 7. PENDÊNCIAS CONHECIDAS

Do `CONTEXTO.md` §4: P1 (healthcheck do `api` no compose usa `-health-check`, flag que o
binário não implementa — **é da Fase 7**), P2 (ordem `AccessLog` × `Recoverer`), P3
(`net.Listen` em vez de `select`+`time.After`), P4 (path `/internal/wallets` — o enunciado
pede `POST /wallets`), P5 (registrar Postgres/SQS no grupo `health_checkers`), P7
(`StopTimeout` deve vir da config), P8 (testar SIGTERM real).

**P6 RESOLVIDA na Fase 4**: `cmd/migrate` existe, funciona e é o que o compose executa.
P9/P10 resolvidas anteriormente.

Das Fases 1–3: o helper `CodeOf` está **duplicado** em três arquivos de teste
(`money_test.go`, `wallet_test.go`, `wagering_test.go`). Quando a Fase 7 montar o handler,
ele vira função de produção no pacote de erros — é ele que traduz `Code` em `failureCode`.

Da Fase 3: o `PayloadEncoder` está injetado e o JSON canônico **ainda não existe** — precisa
ser criado em `internal/platform/canonicaljson` na Fase 5.

Da Fase 4: o schema usa `CHAR(3)` para moeda. `CHAR` preenche com espaço no PostgreSQL, então
os repositórios da Fase 5 precisam **testar** se o driver devolve `"BRL"` ou `"BRL"` com
padding, e tratar a leitura em conformidade.

---

## 8. CHECKLIST DE ARRANQUE PARA A NOVA CONVERSA

- [ ] Ler `CONTEXTO.md`, `PLANO.md` §3 (Fase 5) e `docs/evidencias/FASE_4.md`
- [ ] Confirmar que os containers estão de pé (`docker ps`) e o schema aplicado (`bin/migrate.exe version`)
- [ ] Criar `internal/platform/canonicaljson` (pendência da Fase 3) antes dos repositórios
- [ ] Implementar pool + `uow.Run` com `ReadCommitted` + `FOR UPDATE` na carteira
- [ ] Implementar os repositórios cobrindo **todas** as colunas do schema da Fase 4
- [ ] Classificar erro do `pgx` (`23505` → `ErrConflict`) — o unique do nosso schema **é** a idempotência funcionando, não um erro de banco
- [ ] Testar de verdade: `SKIP LOCKED` com duas conexões, lost update, atomicidade (rollback não deixa resíduo)
- [ ] Aceitar contestação do usuário: se ele questionar e estiver certo, **admitir e corrigir**
- [ ] Depois de aprovado, commitar: `feat(postgres): adiciona repositorios pgx com unit of work e locking por carteira`

**As Fases 0 a 4 estão commitadas e validadas. Não revalide nem recommite.**
