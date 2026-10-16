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
| Fase 2 (`Wallet` + `LedgerEntry`) | ✅ **CONCLUÍDA E VALIDADA** — 63 casos, 99,0% de cobertura |
| Fase 3 (`WagerTransaction`) | ⬅ **PRÓXIMA** |
| Fases 4–12 | ⬜ Não iniciadas (ver checklist no `PLANO.md` §3) |

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
98fbee6 feat(wallet): adiciona carteira com debito/credito e ledger append-only   ← FASE 2
442a025 docs: registra o hash do commit da Fase 1 no plano e no handoff
b046e6a feat(money): completa o value object com aritmetica, comparacao e serializacao  ← FASE 1 (Blocos 3–7)
3172772 bloco 2 monet incompleto                       ← Bloco 2 (mensagem a rever)
b1bd10d docs(handoff): atualiza PROXIMO_PASSO para o Bloco 2 da Fase 1
3a36aec feat(money): adiciona o value object Currency com erro de dominio tipado   ← Bloco 1
7e43206 docs: adiciona PROXIMO_PASSO.md (handoff para continuar na Fase 1)
f1ec2da docs(plano): marca a Fase 0 como concluida e validada com infraestrutura real
1eef5b2 chore(setup): fundacao do projeto com Fx, config validada, HTTP e observabilidade
```

> ✅ **As Fases 1 e 2 estão commitadas e validadas. Não revalide nem recommite.** Vá direto
> para a **Fase 3** (seção 6).

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

## 6. 🎯 A TAREFA PONTUAL: FASE 3 (`WagerTransaction`)

### 6.1. O que as Fases 1 e 2 deixaram pronto para reusar

O pacote `internal/domain/money` e o `internal/domain/wallet` já resolvem quase tudo —
**use, não reimplemente**:

| Necessidade da `WagerTransaction` | O que usar |
| --- | --- |
| Valor da operação | `money.Money` — validar sinal/zero conforme o tipo (ver 6.2) |
| Conferir o `"0.00"` exato do `LOSS` | `money.IsZero()` |
| Valor maior que zero em `BET`/`WIN`/`REFUND`/`ROLLBACK` | `money.IsPositive()` |
| Erro tipado com `failureCode` | **Mesmo padrão** de `money` e `wallet`: `Error{Code, Message}` + sentinelas + `Is()` por `Code` |
| ID da operação no ledger | `wallet.TransactionID` já é `string` e aceita o ID da transação |
| Referência à carteira no lançamento | `wallet.WalletID` |

### 6.2. Regras que o enunciado exige (§6.3 e §7 do README)

Transcrevendo o que é verificável, porque são **estas** linhas que os testes têm que travar:

| Fonte | Regra |
| --- | --- |
| §6.3 | Tipos: `OPENING`, `BET`, `WIN`, `LOSS`, `REFUND`, `ROLLBACK` |
| §6.3 | Operação externa registra: identificadores interno e externo, provedor, chave de idempotência, hash do payload, carteira, jogador, rodada, jogo, tipo, `Money`, referência externa opcional, estado e timestamps |
| §6.3 | Quando aplicável: referência interna resolvida, código de falha e resultado financeiro devolvido ao provedor |
| §6.3 | A transação inicia em `PENDING`; transições para processamento, espera por referência, rejeição e falha permanente são validadas pelo domínio |
| §7 | `BET`, `WIN`, `REFUND` e `ROLLBACK` exigem valor **maior que zero**; `LOSS` exige **exatamente** `"0.00"` |
| §7 | Saldo inicial zero em `OPENING` ⇒ sem `OPENING`, sem ledger e sem eventos financeiros |
| §7 | `OPENING` é operação **interna**: não pode ser aceita vinda do provedor |
| §7 | `REFUND` e `ROLLBACK` exigem referência à transação original, e o valor da reversão tem que ser **igual** ao referenciado (sem reversão parcial) |
| §7 | O valor da reversão precisa concordar em provedor, jogador, carteira, moeda e rodada com a operação referenciada |

### 6.3. Pontos que o avaliador vai cobrar na entrevista

1. **`LOSS` não movimenta dinheiro.** Registra a perda e produz evento, **sem** criar lançamento no ledger e **sem** incrementar versão da carteira. É o caso mais fácil de errar, e é a razão de o `wallet` ter a regra de valor zero.
2. **`OPENING` interna × externa.** O construtor tem que exigir campos **diferentes** por origem: a externa exige provedor, chave de idempotência e hash do payload; a `OPENING` não tem esses metadados e não pode ser aceita de fora.
3. **`failureCode` é separado do `Status`.** `REJECTED` diz o que aconteceu com a operação; `failureCode` diz **por quê**. Precisa ser estável e documentado, para o cliente distinguir entrada corrigível de resultado definitivo (README item 13).
4. **Referência obrigatória na reversão.** Sem `referenceExternalTransactionId`, `REFUND`/`ROLLBACK` não têm como achar a operação original nem provar que o valor bate. E a reversão precisa ser de **valor igual**, não parcial.
5. **Estado terminal não volta.** `PROCESSED`, `REJECTED` e `FAILED` são finais; qualquer transição a partir deles é `ErrTerminalState`.
6. **Idempotência é chave, não comparação.** A chave é `(provider_id, idempotency_key)` e o hash do payload serve para detectar **conflito** (mesma chave, corpo diferente). Isso é Fase 4/6, mas o domínio precisa carregar os dois campos.

### 6.4. Blocos previstos (mesmo ritmo: um bloco por vez)

| Bloco | Conteúdo |
| --- | --- |
| **1** | `errors.go`: `Error`, `Code` estáveis, sentinelas `ErrInvalidTransaction`, `ErrInvalidTransition`, `ErrTerminalState`, `ErrMissingReference`, `ErrInternalKind` + `Is()` por `Code` |
| **2** | `Kind` (`OPENING/BET/WIN/LOSS/REFUND/ROLLBACK`) com `ParseKind`, `Validate`, `String`, `IsExternal()`, `RequiresReference()`, `AllowsZeroAmount()` |
| **3** | `Status` (`PENDING/PENDING_REFERENCE/PROCESSED/REJECTED/FAILED`) com `ParseStatus`, `IsTerminal()` e a **tabela de transições permitidas** |
| **4** | `type WagerTransaction` (campos privados) + `New(...)` validando obrigatórios por origem + `Rehydrate(...)` + acessores |
| **5** | Métodos de transição (`MarkProcessed`, `MarkRejected`, `AwaitReference`, `MarkFailed`) — **únicos** que mudam estado, cada um validando a transição |
| **6** | Eventos de domínio (A12): structs tipadas + envelope com `type` e `version` |
| **7** | Testes: todos os tipos, política de zero por tipo, transições válidas/inválidas, terminal, `OPENING` rejeitado de fora, referência obrigatória |

### 6.5. Casos de teste que a suíte PRECISA cobrir

**Kind:** os 6 tipos aceitos; string vazia/minúscula/desconhecida rejeitada; `IsExternal()` verdadeiro só para os 5 externos; `OPENING` com `IsExternal()` falso
**Política de zero por tipo:** `LOSS` exige `0.00` e rejeita `0.01`; `BET`/`WIN`/`REFUND`/`ROLLBACK` rejeitam `0.00` e aceitam `0.01`
**Origem:** `OPENING` sem provedor/chave/hash é válida; `OPENING` **com** provedor é rejeitada; operação externa sem provedor, sem chave de idempotência ou sem hash é rejeitada
**Referência:** `REFUND` e `ROLLBACK` sem `referenceExternalTransactionId` → `ErrMissingReference`; `BET`/`WIN`/`LOSS` sem referência são válidas
**Estado:** nasce `PENDING`; transições válidas por tipo; transição inválida → `ErrInvalidTransition`; transição a partir de terminal → `ErrTerminalState`
**Reidratação:** `Rehydrate` reconstrói estado e timestamps **sem** reaplicar transição e **sem** emitir evento (README §Encaps. L89)
**Erros:** `errors.Is` por `Code`, erro atravessa `errors.Join`, sentinelas não se confundem, `Error()` com o `Code` na frente

### 6.6. Checkpoint esperado de cada bloco

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'; $env:GOCACHE='E:\jungleg\.gocache\build'; $env:GOPATH='E:\jungleg\.gocache\gopath'
gofmt -l ./internal/domain/
go build ./...
go vet ./...
go test -race ./internal/domain/wagering/
```

Depois do último bloco aprovado, commitar:
```
feat(wagering): adiciona transacao de aposta com tipos, estados e eventos de dominio
```
---

## 7. PENDÊNCIAS CONHECIDAS (não são da Fase 3, mas estão anotadas)

Do `CONTEXTO.md` §4: P1 (healthcheck do `api` no compose usa flag inexistente), P2 (ordem
`AccessLog` × `Recoverer`), P3 (`net.Listen` em vez de `select`+`time.After`), P4 (path
`/internal/wallets` — o enunciado pede `POST /wallets`), P5 (registrar Postgres/SQS no grupo
`health_checkers`), P6 (`cmd/migrate` não existe ainda — o compose o referencia),
P7 (`StopTimeout` deve vir da config), P8 (testar SIGTERM real). P9/P10 **resolvidas**.

Das Fases 1 e 2: o helper `CodeOf` existe **duplicado** em `money_test.go` e
`wallet_test.go`. Quando a Fase 7 montar o handler HTTP, ele precisa virar função de
produção no pacote de erros (é ele que traduz `Code` em `failureCode`). Está comentado nos
dois arquivos.

Da Fase 2: a unicidade de `(playerID, currency)` ainda **não** é imposta — é decisão de
schema, e vai para a `constraint UNIQUE` da Fase 4.

---

## 8. CHECKLIST DE ARRANQUE PARA A NOVA CONVERSA

- [ ] Ler `CONTEXTO.md`, `PLANO.md` §3 (Fase 3) e `docs/evidencias/FASE_2.md`
- [ ] Entregar o **Bloco 1 da Fase 3** no formato da seção 3: objetivo → problema → decisões → código → tabela → checkpoint
- [ ] Manter o cadence: **um bloco por vez**, explicar antes, perguntar se entendeu
- [ ] Reusar o padrão de erro já validado (`Error{Code, Message}` + sentinelas + `Is()` por `Code`) em vez de inventar outro
- [ ] Reusar o `money` para validar valor por tipo (`IsZero`, `IsPositive`) em vez de comparar `Minor()` à mão
- [ ] Aceitar contestação do usuário: se ele questionar e estiver certo, **admitir e corrigir**
- [ ] Comentários **curtos**: só o que explica decisão
- [ ] Provar com teste antes de afirmar que uma guarda basta (lição da Fase 1)
- [ ] Depois do último bloco aprovado, commitar: `feat(wagering): adiciona transacao de aposta com tipos, estados e eventos de dominio`

**Não é necessário:** revalidar as Fases 0, 1 e 2, nem rodar Docker (a Fase 3 continua puro Go).
