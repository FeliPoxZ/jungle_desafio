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
| Fase 4 (schema + migrations) | ⬅ **PRÓXIMA** |
| Fases 5–12 | ⬜ Não iniciadas (ver checklist no `PLANO.md` §3) |

**Total do domínio: 574 casos verdes, `go test -race ./...` verde nos 7 pacotes.**

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

## 6. 🎯 A TAREFA PONTUAL: FASE 4 (schema, migrations e constraints)

**Esta é a fase em que as regras do domínio passam a ser impostas pelo banco.** Tudo que as
Fases 2 e 3 validam em memória precisa de uma constraint correspondente — é a segunda
barreira, e a única que sobrevive a um bug de aplicação.

### 6.1. Pré-requisito: Docker

A Fase 4 **exige Docker rodando** para validar as migrations de verdade (subir, aplicar,
testar as constraints com `INSERT` inválido, reverter). Se o pipe não existir, subir o
Docker Desktop e aguardar.

### 6.2. Entregáveis

| Artefato | Conteúdo |
| --- | --- |
| `migrations/000001_init.up.sql` | `players` (se necessário), `wallets`, `wager_transactions`, `wallet_ledger_entries`, `inbox_messages`, `outbox_events` |
| `migrations/000001_init.down.sql` | drop na ordem inversa |
| Trigger de imutabilidade | `BEFORE UPDATE OR DELETE` no ledger com `RAISE EXCEPTION` |
| `cmd/migrate` | **não existe ainda** (pendência P6) e o compose o referencia |
| Teste das constraints | `INSERT`/`UPDATE` inválidos devem falhar de verdade |

### 6.3. Constraints obrigatórias (`PLANO.md` §3, Fase 4)

**`wallets`**
- `UNIQUE (player_id, currency)` — o par identifica uma única carteira
- `CHECK (balance_minor >= 0)` — saldo nunca negativo
- `CHECK (version >= 1)`
- `CHECK (balance_minor BETWEEN -9000000000000000 AND 9000000000000000)` — o limite do domínio

**`wallet_ledger_entries`**
- `CHECK (balance_after_minor = balance_before_minor + CASE direction WHEN 'CREDIT' THEN amount_minor ELSE -amount_minor END)` — **a aritmética do ledger no banco**
- `CHECK (amount_minor > 0)` — lançamento de valor zero não existe
- `CHECK (direction IN ('DEBIT','CREDIT'))`
- `UNIQUE (wallet_id, transaction_id)` — um lançamento por operação
- **imutabilidade:** trigger `BEFORE UPDATE OR DELETE ... RAISE EXCEPTION`

**`wager_transactions`**
- `UNIQUE (provider_id, idempotency_key)` — **idempotência persistente**
- `UNIQUE (provider_id, external_transaction_id)`
- `UNIQUE (reference_transaction_id, kind) WHERE reference_transaction_id IS NOT NULL` — impede dupla reversão do mesmo tipo
- `UNIQUE (idempotency_key) WHERE kind = 'OPENING'` — **impede crédito inicial duplicado**
- `CHECK (kind <> 'OPENING' OR provider_id IS NULL)` — `OPENING` é interna
- `CHECK (kind IN (...))` e `CHECK (status IN (...))`
- `CHECK (kind <> 'LOSS' OR amount_minor = 0)` e `CHECK (kind = 'LOSS' OR amount_minor > 0)`
- `CHECK (kind NOT IN ('REFUND','ROLLBACK') OR reference_external_transaction_id IS NOT NULL)`

**`inbox_messages`** — `UNIQUE (consumer_name, message_id)`
**`outbox_events`** — `UNIQUE (event_id)`, `CHECK (attempts >= 0)`

### 6.4. Pontos que o avaliador vai cobrar

1. **A aritmética do ledger no banco.** O `CHECK` que compara `balance_after` com `before ± amount` é o que impede um lançamento incoerente entrar, mesmo com bug na aplicação.
2. **Imutabilidade do ledger é trigger, não convenção.** `UPDATE`/`DELETE` levantam exceção.
3. **Idempotência é índice único, não `SELECT` antes de `INSERT`.** `SELECT` seguido de `INSERT` tem corrida; o índice único não tem.
4. **`OPENING` com `UNIQUE` parcial em `idempotency_key`.** É o que impede dois créditos iniciais para o mesmo jogador.
5. **Índices para as consultas reais:** `(wallet_id, created_at, id)` para paginação de ledger, `(status, next_attempt_at)` para o worker de referências pendentes.
6. **`down.sql` que realmente reverte.** Migration sem rollback testado é migration que não existe.

### 6.5. Checkpoint esperado

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'; $env:GOCACHE='E:\jungleg\.gocache\build'; $env:GOPATH='E:\jungleg\.gocache\gopath'
# 1) docker rodando
docker compose up -d postgres
# 2) aplicar migrations
$env:PGPASSWORD='...'; psql -h localhost -U jungleg -d jungleg -f migrations/000001_init.up.sql
# 3) testar as constraints com INSERT invalido (deve falhar)
# 4) reverter
psql -h localhost -U jungleg -d jungleg -f migrations/000001_init.down.sql
go build ./...
go vet ./...
go test -race ./...
```

Commit ao final:
```
feat(db): adiciona schema inicial com constraints de saldo, ledger imutavel e idempotencia
```

---

## 7. PENDÊNCIAS CONHECIDAS

Do `CONTEXTO.md` §4: P1 (healthcheck do `api` no compose usa flag inexistente), P2 (ordem
`AccessLog` × `Recoverer`), P3 (`net.Listen` em vez de `select`+`time.After`), P4 (path
`/internal/wallets` — o enunciado pede `POST /wallets`), P5 (registrar Postgres/SQS no grupo
`health_checkers`), **P6 (`cmd/migrate` não existe — o compose o referencia: resolver na
Fase 4)**, P7 (`StopTimeout` deve vir da config), P8 (testar SIGTERM real). P9/P10 resolvidas.

Das Fases 1–3: o helper `CodeOf` está **duplicado** em três arquivos de teste
(`money_test.go`, `wallet_test.go`, `wagering_test.go`). Quando a Fase 7 montar o handler,
ele vira função de produção no pacote de erros — é ele que traduz `Code` em `failureCode`.

Da Fase 3: o `PayloadEncoder` está injetado e o JSON canônico **ainda não existe** — precisa
ser criado em `internal/platform/canonicaljson` antes ou durante a Fase 5/6.

---

## 8. CHECKLIST DE ARRANQUE PARA A NOVA CONVERSA

- [ ] Ler `CONTEXTO.md`, `PLANO.md` §3 (Fase 4) e `docs/evidencias/FASE_3.md`
- [ ] Subir o Docker Desktop (a Fase 4 depende dele) e confirmar os 3 containers
- [ ] Criar `migrations/000001_init.up.sql` e `.down.sql` seguindo a lista da seção 6.3
- [ ] **Testar cada constraint com um `INSERT` inválido de verdade**, não só aplicar a migration
- [ ] Testar o `down.sql` revertendo e reaplicando
- [ ] Resolver a pendência P6 (`cmd/migrate`) para o compose ficar coerente
- [ ] Aceitar contestação do usuário: se ele questionar e estiver certo, **admitir e corrigir**
- [ ] Depois de aprovado, commitar: `feat(db): adiciona schema inicial com constraints de saldo, ledger imutavel e idempotencia`

**As Fases 0 a 3 estão commitadas e validadas. Não revalide nem recommite.**
