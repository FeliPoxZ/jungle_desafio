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
| Fase 5 (persistência `pgx` + Unit of Work) | ✅ **CONCLUÍDA E VALIDADA** — 38 testes (25 de integração), `-race` verde, 4 defeitos reais corrigidos |
| Fase 6 (caso de uso `wagering.Service`) | ⬅ **PRÓXIMA** |
| Fases 7–12 | ⬜ Não iniciadas (ver checklist no `PLANO.md` §3) |

**Domínio (Fases 1–3): 574 casos verdes. Persistência (Fase 5): 38 testes, `go test -race ./...` verde nos 10 pacotes.**

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

> **6. Retry envolve a TRANSAÇÃO INTEIRA, nunca uma chamada por dentro dela.**
> Repetir dentro da transação deixa a tentativa que falhou com a conexão aberta
> segurando o lock, e o sintoma aparece longe (o próximo `TRUNCATE` morre com
> deadlock). A unidade de retry é a transação — vale para os testes e para a
> Fase 6.

> **7. Não confie em contagem de falha tirada de pipeline do PowerShell.** Um
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

## 6. 🎯 A TAREFA PONTUAL: FASE 6 (caso de uso `wagering.Service`)

**Esta é a fase mais importante do desafio** (o plano estima ~4h). É onde o
domínio, o banco e as regras de negócio se encontram: uma requisição do provedor
vira uma transação SQL única que decide, movimenta dinheiro e enfileira eventos.

### 6.1. O que as Fases 1–5 deixaram pronto

| Necessidade do serviço | O que usar |
| --- | --- |
| Valor, parsing e formatação | `money.Parse`, `Money.Add/Sub/Neg/Cmp`, `String`/JSON |
| Validar tipo, valor e origem | `wagering.New` (já valida `LOSS` = 0, reversão exige referência, `OPENING` é interna) |
| Transições de estado | `MarkProcessed`, `MarkRejected`, `MarkFailed`, `MarkPendingReference`, `ResolveReference` |
| Saber se movimenta dinheiro | `kind.MovesMoney()` |
| Aplicar a movimentação | `wallet.Debit`/`Credit` → devolvem `Movement` (com a **versionBefore**) |
| Persistir a movimentação | `r.Wallets.Update(ctx, id, mov.VersionBefore(), carteira.Balance())` + `r.Ledger.Insert(ctx, mov.Entry())` |
| Uma transação para tudo | `uow.Run(ctx, func(ctx, r) error)` |
| Lock da carteira | `r.Wallets.GetByIDForUpdate` |
| Idempotência | `r.Transactions.GetByIdempotencyKey` + `UNIQUE` no banco; `matchesPayload` para separar replay de conflito |
| Hash do payload | `canonicaljson.Hash(payload)` |
| Eventos na outbox | `wagering.ProcessedEvent`/`RejectedEvent`/`PendingReferenceEvent`/`NewBalanceChangedEvent` + `r.Outbox.Insert` |
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

**O lock no passo 3 é o que impede lost update.** Se o `SELECT` da carteira
vier depois do cálculo do saldo, dois escritores leem o mesmo valor e um
sobrescreve o outro.

### 6.3. Regras de negócio que faltam implementar (e onde)

| Regra (README) | O que fazer |
| --- | --- |
| `BET` exige saldo suficiente | `wallet.Debit` já devolve `ErrInsufficientFunds`; traduzir para `failureCode: INSUFFICIENT_FUNDS` e `422` |
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

Onde ficam as **interfaces de repositório** que o serviço consome? Hoje elas estão
em `internal/infra/postgres`, o que faria o pacote de aplicação importar
infraestrutura. Opções:

| Opção | Prós | Contras |
| --- | --- | --- |
| Definir as interfaces em `internal/app/ports` e o `postgres` satisfazê-las | Aplicação não importa infraestrutura; testável com dublê | Mais um pacote e conversão de tipos em alguns pontos |
| Consumir as interfaces de `internal/infra/postgres` direto | Menos código | Acopla aplicação à infra; o `PLANO.md` A3 diz o contrário |

**Recomendação:** `internal/app/ports` com interfaces estreitas (só o que o
serviço usa). O `Repos` do `uow` já é uma struct concreta — o serviço pode
receber uma interface `UnitOfWork` que devolve `ports.Repos`.

### 6.5. Casos de teste que a suíte PRECISA cobrir

**Idempotência:** mesma chave + mesmo hash → replay devolve o **saldo original** (mesmo que a carteira já tenha mudado depois); mesma chave + hash diferente → 409; `external_transaction_id` com chave diferente → 409
**Tipos:** `BET` com saldo e sem saldo; `WIN` credita; `LOSS` não move, não cria ledger e não incrementa versão mas produz evento; `REFUND` devolve; `ROLLBACK` desfaz
**Reversão:** referência inexistente → `PENDING_REFERENCE` + 202; referência rejeitada → `REFERENCE_NOT_PROCESSABLE`; valor diferente → erro; `REFUND` duplicado → `ALREADY_REVERSED`; `ROLLBACK` sem saldo → `ROLLBACK_INSUFFICIENT_FUNDS`
**Atomicidade:** falha em qualquer passo não deixa saldo, ledger nem evento
**Concorrência:** duas `BET` de 80 sobre saldo de 100 (o cenário do README) → uma processa, a outra rejeita por saldo, saldo final **20.00** e **um** débito no ledger
**Abertura:** `POST /wallets` com saldo positivo cria `OPENING` + crédito + 2 eventos; com zero não cria `OPENING`; duplicado → conflito
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

**P6 RESOLVIDA na Fase 4**: `cmd/migrate` existe, funciona e é o que o compose executa.
P9/P10 resolvidas anteriormente.

**Resolvidas na Fase 5:**
- `internal/platform/canonicaljson` criado (era pendência da Fase 3);
- o padding do `CHAR(3)` foi tratado (`moedaGo` faz `TrimSpace`) e testado em
  `TestWalletInsertELeitura`, que compara com `money.BRL` e falharia se viesse `"BRL "`;
- o helper `CodeOf` continua duplicado em três arquivos de teste: quando a Fase 7 montar o
  handler, ele vira função de produção (é ele que traduz `Code` em `failureCode`).

**Novas pendências registradas:**
- **Retry:** a unidade de retry é a **transação inteira**, nunca uma chamada por dentro dela
  (repetir por dentro deixa a conexão aberta segurando o lock e o sintoma aparece como
  deadlock em outro lugar). Vale para os testes e para a Fase 6;
- **`testcontainers-go` foi baixado e não é usado** — os testes de integração apontam para o
  Postgres do compose via `TEST_DATABASE_URL`. Remover com `go mod tidy` na Fase 12;
- **`LOSS` com referência**: o domínio aceita `LOSS` sem referência, mas o README permite
  referência opcional. Decidir na Fase 6 se valida quando enviada.

---

## 8. CHECKLIST DE ARRANQUE PARA A NOVA CONVERSA

- [ ] Ler `CONTEXTO.md`, `PLANO.md` §3 (Fase 6) e `docs/evidencias/FASE_5.md`
- [ ] Confirmar containers de pé (`docker ps`) e o schema aplicado (`bin/migrate.exe version`)
- [ ] **Decidir onde ficam as interfaces de repositório** (seção 6.4): `internal/app/ports` é a recomendação, para a aplicação não importar infraestrutura
- [ ] Implementar `Process(ctx, Request)` na **ordem exata da seção 6.2** — o lock da carteira vem ANTES de qualquer cálculo de saldo
- [ ] Reusar `uow.Run`, `Movement`, `canonicaljson.Hash` e os eventos do `wagering` em vez de reimplementar
- [ ] Cobrir os casos da seção 6.5, em especial o cenário do README: duas `BET` de 80 sobre saldo 100 → **saldo final 20.00 e UM débito no ledger**
- [ ] Testar idempotência de verdade: replay devolve o saldo **original**, mesmo com a carteira já alterada
- [ ] Aceitar contestação do usuário: se ele questionar e estiver certo, **admitir e corrigir**
- [ ] Depois de aprovado, commitar: `feat(app): adiciona caso de uso de operacoes com idempotencia e ledger transacional`

**As Fases 0 a 5 estão commitadas e validadas. Não revalide nem recommite.**
