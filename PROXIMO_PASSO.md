# PROXIMO_PASSO.md — Handoff para a próxima conversa

> **INSTRUÇÃO PARA A NOVA CONVERSA (leia isto primeiro):**
> Este arquivo é um handoff. Ele diz **onde o projeto está, o que já foi decidido, qual é o
> método de trabalho acordado e exatamente qual é a próxima tarefa pontual**.
>
> Antes de responder qualquer coisa, leia também, nesta ordem:
> 1. `CONTEXTO.md` (raiz) — estado geral, ambiente, decisões congeladas, pendências
> 2. `PLANO.md` §3 — checklist por fase, com a Fase 2 detalhada
> 3. `docs/evidencias/FASE_1.md` — provas da fase recém-concluída
> 4. `docs/INFRAESTRUTURA.md` — o que são Postgres/Keycloak/SQS/LocalStack/Docker
>
> **Não comece a codar sem ler o método de trabalho (seção 3). Ele foi ajustado depois de
> várias correções do usuário e desrespeitá-lo quebra a confiança da sessão.**

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
| Fase 2 (`Wallet` + `LedgerEntry`) | ⬅ **PRÓXIMA** |
| Fases 3–12 | ⬜ Não iniciadas (ver checklist no `PLANO.md` §3) |

### Já funciona e está provado

- **Fase 0:** `go test -race ./...` verde; API sobe (`/health/live` → `{"status":"UP"}`,
  `/health/ready`, `/metrics` com 13 métricas); 3 containers `healthy`
  (`jungleg-postgres`, `jungleg-keycloak`, `jungleg-localstack`); 4 filas SQS com redrive
  `maxReceiveCount=5`; realm do Keycloak importado e **token real emitido** com
  `iss`/`aud`/`provider_id`/`roles` corretos. Evidências: `docs/evidencias/FASE_0.md`
- **Fase 1:** `internal/domain/money` completo — `Currency`, `Money`, `Parse`, `Zero`,
  `Add`, `Sub`, `Neg`, `Cmp`, `Equal`, `Sign`, `IsZero/IsNegative/IsPositive`, `String`,
  `MarshalJSON`/`UnmarshalJSON` e erros de domínio com `Code` estável. **384 casos verdes**
  (65 testes + 319 subtests), cobertura **97,9%** (24 de 27 funções em 100%),
  `go build`/`go vet`/`gofmt` limpos, `go test -race ./...` verde nos 5 pacotes.
  **10 de 10 itens conformes** no README §6.1 + §Encapsulamento. O item eliminatório
  "sem ponto flutuante" virou teste automatizado de AST. Evidências:
  `docs/evidencias/FASE_1.md`

### Histórico de commits (mais recente primeiro)

```
<commit final> feat(money): completa o value object com serializacao JSON e suite de testes  ← FASE 1 (Blocos 3–7)
3172772 bloco 2 monet incompleto                       ← Bloco 2 (mensagem a rever)
b1bd10d docs(handoff): atualiza PROXIMO_PASSO para o Bloco 2 da Fase 1
3a36aec feat(money): adiciona o value object Currency com erro de dominio tipado   ← Bloco 1
7e43206 docs: adiciona PROXIMO_PASSO.md (handoff para continuar na Fase 1)
f1ec2da docs(plano): marca a Fase 0 como concluida e validada com infraestrutura real
1eef5b2 chore(setup): fundacao do projeto com Fx, config validada, HTTP e observabilidade
```

> ✅ **A Fase 1 está commitada e validada. Não revalide nem recommite.** Vá direto para a
> **Fase 2** (seção 6).

---

## 3. ⭐ MÉTODO DE TRABALHO (NÃO NEGOCIÁVEL)

O usuário pediu explicitamente e **várias vezes** corrigiu a IA por ir rápido demais, jogar
configuração/código sem explicar, ou escrever código questionável. O ritmo combinado é:

| Regra | Como aplicar |
| --- | --- |
| **MODO B: linha a linha, juntos** | A IA explica um **bloco pequeno** (~10–25 linhas), **o usuário digita**, e só então vem o próximo bloco. A IA **não escreve o código do núcleo no arquivo** — o usuário escreve |
| **Explicar ANTES de escrever** | Regra, conceito, exigência do enunciado e o "porquê" vêm **antes** do código |
| **Um bloco por vez, com checkpoint** | Nunca entregar vários arquivos ou fases de uma vez. Esperar o usuário dizer "bloco N pronto" |
| **O núcleo é do usuário** | `Money`, `Wallet`, `LedgerEntry`, `WagerTransaction` são escritos por ELE. Infraestrutura (compose, migrations, repos, workers, testes de integração) a IA pode escrever |
| **Perguntar antes de avançar** | Se o usuário não entendeu, PARAR e explicar de novo |
| **Linguagem** | Português do Brasil, tom de mentor sênior, analogias concretas, tabelas comparativas |
| **Aceitar contestação** | ⭐ O usuário **questiona o código** e isso é bom. Quando ele contestar, avaliar com honestidade: se ele estiver certo, **admitir e corrigir**, explicando o motivo do erro. Nunca defender código ruim |
| **Quando usar `write`/`edit`** | Preferir que o usuário digite. Usar `write` apenas quando ele pedir explicitamente. Nunca "corrigir" o arquivo dele em silêncio |
| **Formato da entrega de bloco** | 1) objetivo em uma frase 2) o problema antes do código 3) decisões de design e o porquê (tabela) 4) o código para digitar 5) tabela explicando cada parte 6) checkpoint |

**⭐ Princípios de qualidade que o usuário já cobrou:**

> **1. Antes de escrever qualquer helper, verificar se já existe na biblioteca padrão.**
> Se existir, usar a stdlib. "Evitar um import" **nunca** é justificativa — `errors`,
> `strings`, `time` são gratuitos. Reimplementar `errors.As` à mão foi um erro já corrigido.

> **2. Antes de afirmar que uma guarda é necessária (ou suficiente), provar com teste.**
> Na Fase 1 a IA afirmou duas vezes "uma guarda basta" e as duas estavam erradas. Uma
> varredura exaustiva de 81 combinações achou 10 furos no `Add`. **Varredura > raciocínio**
> quando o assunto é sinal, limite ou overflow.

> **3. Comentário só onde explica decisão.** O usuário enxugou os comentários do Bloco 1 e
> prefere código enxuto. Não encher de doc comment onde o nome da função já diz o que faz.

> **4. Quando um teste falhar, verificar se a régua está certa antes de acusar o código.**
> Na Fase 1 a IA quase acusou o código do usuário três vezes por expectativa errada do
> próprio teste. Conferir a premissa primeiro.

**Alternativa se o relógio apertar:** o usuário autorizou que a IA complete o código sozinha
**se o tempo estiver acabando**, seguindo o padrão já construído. Avisar antes e explicar.

---

## 4. AMBIENTE — DETALHES QUE CAUSAM ERRO SE IGNORADOS

### 4.1. Cache do Go (OBRIGATÓRIO)

O cache global `C:\Users\PoxZe\go` **não é gravável** nesta máquina. Todo comando Go precisa:

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'
$env:GOCACHE='E:\jungleg\.gocache\build'
$env:GOPATH='E:\jungleg\.gocache\gopath'
```

O `Makefile` já faz isso. `/.gocache/` está no `.gitignore`.

### 4.2. Docker (funciona, com duas particularidades)

```powershell
$env:Path = "C:\Users\PoxZe\AppData\Local\Programs\DockerDesktop\resources\bin;$env:Path"
```

| Particularidade | Detalhe |
| --- | --- |
| Sandbox × Docker | O sandbox do harness **não abre o named pipe** do Docker. Comandos Docker exigem `sandbox_permissions: danger-full-access` (com `justification` em português) |
| Docker Desktop | **Não inicia sozinho** (`autoStart: False`). Se o pipe não existir: `Start-Process 'C:\Users\PoxZe\AppData\Local\Programs\DockerDesktop\Docker Desktop.exe'` e aguardar ~5–30s |
| LocalStack | **SÓ** `localstack/localstack:4.0.3`. Tags `stable`/`latest` são **Pro** e morrem com *"License activation failed"* |

### 4.3. Line endings

Scripts shell, SQL e Dockerfile rodam em Linux dentro dos containers. Ao editá-los pelo
Windows, garantir **LF** (não CRLF), senão: `syntax error: unexpected end of file`.
O `.gitattributes` já normaliza.

### 4.4. Outras ferramentas

- Go **1.26.5** (módulo declara `go 1.25.0` + `toolchain go1.26.5`)
- PowerShell **5.1** (não 7! sem `&&`, sem ternário `? :`)
- **`make` NÃO está instalado** — os alvos do Makefile servem de documentação executável
- git 2.52.0
- **`package main` não pode ser importado nem testado** → toda lógica em `internal/`

---

## 5. DECISÕES TÉCNICAS JÁ CONGELADAS

Detalhamento completo em `PLANO.md` §1 (A1–A14). Resumo do que afeta as Fases 1–2:

| ID | Decisão |
| --- | --- |
| **A2** | `Money` = **`int64` em unidades mínimas** (centavos) + `Currency` ISO 4217. Escala fixa **2**. **Nunca `float32`/`float64`** |
| A2 | Contrato externo: `{"amount":"25.00","currency":"BRL"}` — `amount` é **string** |
| A2 | Limite: `\|minor\| <= 9_000_000_000_000_000` com tratamento de overflow no parse, soma, subtração e negação |
| A2 | **Revisado na Fase 1:** `New` (criação) valida moeda **e faixa**; `FromMinor` (reidratação) não valida |
| A2 | **Revisado na Fase 1:** serialização é assimétrica para negativos — `MarshalJSON` emite sinal, `UnmarshalJSON` recusa |
| A2 | **Revisado na Fase 1:** `UnmarshalJSON` rejeita campo desconhecido e chave repetida (o `encoding/json` normal ignoraria os dois) |
| A6 | Erros de domínio: tipo + `errors.Is`/`errors.As`. **`panic` nunca representa rejeição de negócio** |
| A1 | Roteador `chi`, driver `pgx/v5`, migrations `golang-migrate`, SQS `aws-sdk-go-v2`, OIDC `go-oidc`, IDs `google/uuid` (v7), métricas Prometheus, testes com a **lib padrão** |
| A8 | Tabela de `failureCode` estáveis (o `Code` do erro de domínio vira `failureCode` no HTTP) |

---

## 6. 🎯 A TAREFA PONTUAL: FASE 2 (`Wallet` + `LedgerEntry`)

### 6.1. O que a Fase 1 deixou pronto para reusar

O pacote `internal/domain/money` já oferece tudo que a `Wallet` precisa — **use, não
reimplemente**:

| Necessidade da `Wallet` | O que usar do `money` |
| --- | --- |
| Somar/subtrair saldo | `Add(other)` / `Sub(other)` — já tratam overflow e moeda incompatível |
| Comparar saldo com zero | `IsZero()`, `IsNegative()`, `IsPositive()`, `Sign()`, `Cmp()` |
| Criar saldo zero na criação | `Zero(currency)` |
| Moeda da movimentação diferente da carteira | `Add`/`Sub` já devolvem `ErrCurrencyMismatch`; ou comparar `Currency()` |
| Reidratar saldo vindo do banco | `FromMinor(minor, currency)` |
| Erro tipado com `failureCode` | Padrão `Error{Code, Message}` + sentinelas + `Is()` por `Code` |

### 6.2. Regras que o enunciado exige da `Wallet` e do ledger

Transcrevendo o que é verificável, porque são **estas** linhas que os testes têm que travar:

| Fonte | Regra |
| --- | --- |
| §6.2 | A carteira carrega identidade, jogador, moeda, saldo, **versão** e instantes de criação/atualização |
| §6.2 | O par `(playerId, currency)` identifica uma única carteira |
| §6.2 | **Débitos precisam preservar saldo maior ou igual a zero** |
| §6.2 | A moeda de cada movimentação deve coincidir com a da carteira |
| §6.2 | Cada mudança financeira exige o lançamento correspondente no ledger, confirmado junto com o saldo |
| §6.2 | Versão inicial `1`; depois da criação, incrementar **apenas** quando houver mudança de saldo |
| §6.2 | Disputas entre escritores não podem descartar uma atualização confirmada |
| §Encaps. L89 | **Separe criação e reidratação.** A reidratação não pode reaplicar movimentações, transições nem emitir eventos |
| §Ledger | Cada lançamento registra `id`, `walletId`, `transactionId`, direção (`DEBIT`/`CREDIT`), valor, **saldo anterior**, **saldo posterior** e instante de criação |
| §Ledger | Unicidade, não negatividade e imutabilidade do ledger impostas pelo **schema e constraints** (Fase 4) |
| §Tipos | `LOSS` = `"0.00"` exato: não cria ledger nem altera versão |

### 6.3. Blocos previstos (o usuário digita o núcleo)

| Bloco | Conteúdo | Observação |
| --- | --- | --- |
| **1** | Erros do pacote `wallet`: `Error`, `Code` estáveis, sentinelas `ErrInsufficientFunds`, `ErrWalletCurrencyMismatch`, `ErrInvalidWallet` + `Is()` por `Code` | **Segue o padrão já validado no `money`** — reaproveitar a estrutura, não inventar outra |
| **2** | `type Wallet struct` (campos privados: `id`, `playerID`, `currency`, `balance`, `version`, `createdAt`, `updatedAt`) + `NewWallet(...)` (criação, versão `1`) + `RehydrateWallet(...)` (sem reaplicar nada) + acessores | Mesma separação `New` × `Rehydrate` que funcionou no `money` |
| **3** | `Debit(m)`, `Credit(m)` — valida moeda, preserva saldo ≥ 0, incrementa versão **só** quando o saldo muda, atualiza `updatedAt` | `Debit` usa `Sub` + `IsNegative` — **não** fazer aritmética própria |
| **4** | `Direction` (`DEBIT`/`CREDIT`) + `type LedgerEntry struct` + `NewEntry(...)` validando `balanceAfter == before ± money` + `RehydrateEntry(...)` | Ledger é **imutável**: sem setters |
| **5** | Testes seguindo o padrão da Fase 1: table-driven + testes de **propriedade** | Ver §6.4 |

### 6.4. Casos de teste que a suíte da `Wallet` PRECISA cobrir

**Criação:** saldo zero (permitido — README L182); saldo inicial positivo; rejeitar moeda inválida; rejeitar `playerID` vazio; versão inicial exatamente `1`; `createdAt`/`updatedAt` iguais na criação
**Reidratação:** `RehydrateWallet` **não** incrementa versão, **não** altera `updatedAt` e **não** emite evento — exigência explícita do L89, travar com teste
**Débito:** débito normal decrementa; débito que zera o saldo é permitido; débito que deixaria negativo é rejeitado com `ErrInsufficientFunds`; débito com saldo exatamente igual ao valor deixa saldo `0` e versão incrementada
**Crédito:** crédito incrementa saldo e versão; crédito de zero **não** muda versão nem `updatedAt`
**Moeda:** débito/crédito com moeda diferente → `ErrWalletCurrencyMismatch` (e **não** `ErrInsufficientFunds`)
**Versão:** incrementa a cada mudança de saldo; **não** incrementa em operação rejeitada nem em operação de valor zero; nunca regride
**Ledger:** `NewEntry` valida `balanceAfter`; direção fora de `DEBIT`/`CREDIT` → erro; `amount` zero → erro; débito no ledger sempre com `balanceBefore > balanceAfter`, crédito o contrário
**Imutabilidade:** `LedgerEntry` sem setter (garantido por ausência de API)
**Propriedade:** `Credit(x)` seguido de `Debit(x)` volta ao saldo original com versão +2; qualquer sequência de operações aceitas termina com saldo ≥ 0; a soma dos lançamentos do ledger bate com a diferença de saldo (invariante que a Fase 11 vai conferir)

### 6.5. Checkpoint esperado de cada bloco

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'; $env:GOCACHE='E:\jungleg\.gocache\build'; $env:GOPATH='E:\jungleg\.gocache\gopath'
gofmt -l ./internal/domain/
go build ./...
go vet ./...
go test -race ./internal/domain/wallet/
```

Depois do último bloco aprovado, commitar:
```
feat(wallet): adiciona carteira com debito/credito e ledger append-only
```

---

## 7. PENDÊNCIAS CONHECIDAS (não são da Fase 2, mas estão anotadas)

Do `CONTEXTO.md` §4: P1 (healthcheck do `api` no compose usa flag inexistente), P2 (ordem
`AccessLog` × `Recoverer`), P3 (`net.Listen` em vez de `select`+`time.After`), P4 (path
`/internal/wallets` — o enunciado pede `POST /wallets`), P5 (registrar Postgres/SQS no grupo
`health_checkers`), P6 (`cmd/migrate` não existe ainda — o compose o referencia),
P7 (`StopTimeout` deve vir da config), P8 (testar SIGTERM real). P9/P10 **resolvidas**.

Da Fase 1: o helper `CodeOf` está em `money_test.go` e **precisa se mudar para o pacote
`money`** quando a Fase 7 montar o handler HTTP — é ele que traduz `Code` em `failureCode`.
Está comentado no próprio arquivo.

---

## 8. CHECKLIST DE ARRANQUE PARA A NOVA CONVERSA

- [ ] Ler `CONTEXTO.md`, `PLANO.md` §3 (Fase 2) e `docs/evidencias/FASE_1.md`
- [ ] Entregar o **Bloco 1 da Fase 2** no formato da seção 3: objetivo → problema → decisões → código → tabela → checkpoint
- [ ] Manter o cadence: **um bloco por vez**, explicar antes, perguntar se entendeu
- [ ] Reusar o que o `money` já resolve (`Add`/`Sub`/`IsNegative`/`Zero`/`FromMinor`) em vez de reimplementar
- [ ] Aceitar contestação do usuário: se ele questionar e estiver certo, **admitir e corrigir**
- [ ] Comentários **curtos**: só o que explica decisão
- [ ] Provar com teste antes de afirmar que uma guarda basta (lição da Fase 1)
- [ ] Depois do último bloco aprovado, commitar: `feat(wallet): adiciona carteira com debito/credito e ledger append-only`

**Não é necessário:** revalidar as Fases 0 e 1, nem rodar Docker (a Fase 2 continua puro Go).
