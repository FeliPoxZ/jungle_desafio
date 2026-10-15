# PROXIMO_PASSO.md — Handoff para a próxima conversa

> **INSTRUÇÃO PARA A NOVA CONVERSA (leia isto primeiro):**
> Este arquivo é um handoff. Ele diz **onde o projeto está, o que já foi decidido, qual é o
> método de trabalho acordado e exatamente qual é a próxima tarefa pontual**.
>
> Antes de responder qualquer coisa, leia também, nesta ordem:
> 1. `CONTEXTO.md` (raiz) — estado geral, ambiente, decisões congeladas, pendências
> 2. `docs/INFRAESTRUTURA.md` — o que são Postgres/Keycloak/SQS/LocalStack/Docker
> 3. `docs/FX_EXPLICADO.md` — Uber Fx do zero (só se for mexer em composição)
> 4. `PLANO.md` — plano completo (decisões A1–A14, checklist por fase, cronograma)
>
> **Não comece a codar sem ler o método de trabalho (seção 3). Ele foi ajustado depois de
> três correções do usuário e desrespeitá-lo quebra a confiança da sessão.**

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
| Fase 1 (`Money`) | 🔄 **EM ANDAMENTO — Bloco 1 CONCLUÍDO pelo usuário; próximo é o Bloco 2** |
| Fases 2–12 | ⬜ Não iniciadas (ver checklist no `PLANO.md` §3) |

### Já funciona e está provado

- **Testes:** `go test -race ./...` verde em 4 pacotes (`config`, `httpapi`, `observability`, `app`)
- **Build/vet/fmt:** `go build ./...`, `go vet ./...` limpos, `gofmt -l` vazio
- **API sobe:** `/health/live` → `{"status":"UP"}`, `/health/ready`, `/metrics` (13 métricas)
- **3 containers `healthy`:** `jungleg-postgres`, `jungleg-keycloak`, `jungleg-localstack`
- **4 filas SQS criadas** com redrive `maxReceiveCount=5` e visibility timeout corretos
- **Realm do Keycloak importado** e **token real emitido** com:
  ```
  iss         : http://localhost:8080/realms/jungleg
  aud         : jungleg-api
  provider_id : provider-a
  roles       : provider
  ```
- **Bancos criados:** `jungleg`, `jungle_test`, `keycloak`

Evidências completas (saídas reais dos comandos): `docs/evidencias/FASE_0.md`

### Histórico de commits (mais recente primeiro)
```
3a36aec feat(money): adiciona o value object Currency com erro de dominio tipado   ← BLOCO 1
7e43206 docs: adiciona PROXIMO_PASSO.md (handoff para continuar na Fase 1)
f1ec2da docs(plano): marca a Fase 0 como concluida e validada com infraestrutura real
1aaaa27 fix(infra): localstack community fixado e script de filas corrigido
1444707 docs: FX_EXPLICADO.md ensinando Uber Fx do zero
36a101c docs: adiciona INFRAESTRUTURA.md explicando o que sao as pecas antes da configuracao
967ed2c docs: guia didatico da Fase 0, questoes de fixacao e contexto entre sessoes
706d799 docs(plano): registra conclusao da Fase 0, decisões de ambiente e bloqueios
1eef5b2 chore(setup): fundacao do projeto com Fx, config validada, HTTP e observabilidade
```

> ✅ **O Bloco 1 ESTÁ COMMITADO E VALIDADO** (commit `3a36aec`), com working tree limpo:
> `go build`, `go vet` e `gofmt -l` sem apontamentos em `./internal/domain/money/`.
> **Não revalide nem recommite o Bloco 1.** Vá direto para o **Bloco 2** (seção 6.4).

---

## 3. ⭐ MÉTODO DE TRABALHO (NÃO NEGOCIÁVEL)

O usuário pediu explicitamente e **três vezes** corrigiu a IA por ir rápido demais, jogar
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

**⭐ Princípio de qualidade que o usuário já cobrou e que deve ser seguido:**
> Antes de escrever qualquer helper, verificar se **já existe na biblioteca padrão**.
> Se existir, usar a stdlib. "Evitar um import" **nunca** é justificativa — `errors`,
> `strings`, `time` são gratuitos. Reimplementar `errors.As` à mão foi um erro já corrigido.

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

Detalhamento completo em `PLANO.md` §1 (A1–A14). Resumo do que afeta a Fase 1:

| ID | Decisão |
| --- | --- |
| **A2** | `Money` = **`int64` em unidades mínimas** (centavos) + `Currency` ISO 4217. Escala fixa **2**. **Nunca `float32`/`float64`** |
| A2 | Contrato externo: `{"amount":"25.00","currency":"BRL"}` — `amount` é **string** |
| A2 | Limite: `|minor| <= 9_000_000_000_000_000` com tratamento de overflow no parse, soma, subtração e negação |
| A2 | Rejeitar: vazio, `NaN`, `Infinity`, notação científica, `+`, espaços, mais de 2 casas, negativo em entrada externa |
| A2 | Moedas incompatíveis em aritmética → erro de domínio |
| A6 | Erros de domínio: tipo + `errors.Is`/`errors.As`. **`panic` nunca representa rejeição de negócio** |
| A1 | Roteador `chi`, driver `pgx/v5`, migrations `golang-migrate`, SQS `aws-sdk-go-v2`, OIDC `go-oidc`, IDs `google/uuid` (v7), métricas Prometheus, testes com a **lib padrão** |
| A8 | Tabela de `failureCode` estáveis (o `Code` do erro de domínio vira `failureCode` no HTTP) |

---

## 6. 🎯 A TAREFA PONTUAL: FASE 1 (`Money`) — ENTREGAR O BLOCO 2

### 6.1. Estado exato: Bloco 1 CONCLUÍDO

**O usuário digitou o Bloco 1, aplicou uma correção sugerida pela IA e o código foi
validado e commitado** (`3a36aec`). Conteúdo atual de `internal\domain\money\money.go`:

```go
// Package money define o value object monetário do domínio.
//
// # Representação
//
// O valor é guardado em UNIDADES MÍNIMAS da moeda (centavos), como int64.
// Exemplo: R$ 25,00 é armazenado como 2500.
//
//	Representação interna : 2500      (int64)
//	Escala                : 2         (sempre 2 casas)
//	Contrato externo      : "25.00"   (string decimal)
//
// # Por que não float64
//
// float64 é binário e não representa decimais exatamente: 0.1 + 0.2 não é 0.3.
// Em um ledger financeiro, esse resíduo se acumula e a soma dos lançamentos
// deixa de fechar com o saldo.

package money

import (
	"errors"
	"fmt"
)

// Currency é um código de moeda ISO 4217.
//
// É um tipo próprio (e não uma string solta) de propósito: assim o compilador
// impede trocar a moeda por qualquer texto em uma chamada de função.
type Currency string

const BRL Currency = "BRL"

const (
	CodeInvalidCurrency = "INVALID_CURRENCY"
)

// Error é o erro de domínio do pacote money.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("money: %s: %s", e.Code, e.Message)
}

// Is permite comparar erros por Code com errors.Is, mesmo quando o erro foi
// envelopado com fmt.Errorf("...: %w", err) alguns níveis acima.
func (e *Error) Is(target error) bool {
	var other *Error
	if ok := errors.As(target, &other); ok {
		return e.Code == other.Code
	}
	return false
}

// Erros sentinela do pacote. Use errors.Is(err, money.ErrInvalidCurrency).
var (
	ErrInvalidCurrency = &Error{Code: CodeInvalidCurrency, Message: "código de moeda inválido"}
)
```

**Decisão de estilo do usuário a respeitar:** ele **enxugou os comentários** do Bloco 1
(removeu os comentários longos sobre `Is`/`errors.As` e o bloco de explicação do `asError`).
Ou seja: **ele prefere comentários mais curtos**. Nos próximos blocos, manter apenas os
comentários que explicam **decisões**, não os que repetem o código. Não encher de doc
comment onde o nome da função já diz o que faz.

### 6.2. ⭐ PRIMEIRA AÇÃO: nada a revalidar — vá para o Bloco 2

O Bloco 1 está validado e commitado. Opcionalmente, ler o arquivo para se familiarizar com
o estilo do usuário (seção 6.1 acima já reproduz o conteúdo).

**Depois de entregar o Bloco 2 e o usuário aprovar, commitar** no padrão do repositório:
```
feat(money): adiciona ParseCurrency com validacao ISO 4217
```

### 6.3. Estrutura planejada do `money.go` (~8 blocos)

| Bloco | Conteúdo | Estado |
| --- | --- | --- |
| **1** | Doc do pacote, `Currency`, `BRL`, `type Error`, `Error()`, `Is()` (com `errors.As`), `ErrInvalidCurrency` | ✅ **commitado (`3a36aec`)** |
| **2** | `ParseCurrency(s string) (Currency, error)` + `Validate()` + `String()` — ISO 4217 (exatamente 3 letras A–Z) | ⬅ **PRÓXIMO** |
| **3** | `type Money struct { minor int64; currency Currency }` + `New(minor, currency)` (reidratação) + `Minor()`, `Currency()` | ⬜ |
| **4** | `Parse(amount, currency string) (Money, error)` — string decimal estrita, com overflow | ⬜ |
| **5** | `Zero(currency)`, `Add`, `Sub`, `Neg` — com guarda de overflow e moeda compatível | ⬜ |
| **6** | `Cmp`, `Equal`, `IsZero`, `IsNegative`, `IsPositive`, `Sign` | ⬜ |
| **7** | `String()` (`"975.00"`) e `MarshalJSON`/`UnmarshalJSON` (`{"amount":"25.00","currency":"BRL"}`) | ⬜ |
| **8** | Testes (`money_test.go`) — table-driven com a lib padrão | ⬜ |

### 6.4. BLOCO 2 — o que ensinar (a IA deve COMPOR o código na hora, no formato do Bloco 1)

**Formato obrigatório da entrega do bloco** (o usuário já validou este formato no Bloco 1):
1. **Objetivo do bloco** em uma frase
2. **O problema antes do código** — o que quebra se não existir
3. **As decisões de design e o porquê** (tabela)
4. **O código para o usuário digitar** (bloco Go completo)
5. **Tabela explicando cada parte**
6. **Checkpoint:** pedir "bloco 2 pronto" e oferecer explicação se algo não ficou claro

**Conteúdo técnico obrigatório do Bloco 2:**

| Item | Decisão a transmitir |
| --- | --- |
| Assinatura | `func ParseCurrency(s string) (Currency, error)` |
| Validação | ISO 4217 **alfabético**: exatamente **3 letras ASCII maiúsculas** (`A`–`Z`). Isolar em `Validate()` |
| Por que `Validate()` separado | Serve tanto para `ParseCurrency` quanto para reidratar do banco (Fase 5) |
| Por que rejeitar minúsculas | Não normalizar silenciosamente. Aceitar `"brl"` e converter para `"BRL"` criaria dois códigos de hash de idempotência diferentes para a mesma entrada (o enunciado exige documentar normalizações) |
| Por que rejeitar código numérico | ISO 4217 também tem código numérico (`986` = BRL), mas o contrato do desafio usa o **alfabético**. Aceitar os dois criaria ambiguidade |
| Trim de espaços? | **Não** dentro de `ParseCurrency` — rejeitar. Normalização acontece na borda (Fase 7), explicitamente |
| O que validar com `len` vs bytes | Usar iteração sobre bytes é suficiente (ASCII), mas **comentar** que o código é alfabético ASCII por definição da ISO 4217 — assim não há surpresa com UTF-8 |
| Erro | `&Error{Code: CodeInvalidCurrency, Message: fmt.Sprintf("código de moeda inválido: %q (esperado 3 letras maiúsculas A-Z, ex.: \"BRL\")", s)}` |
| `String()` | `func (c Currency) String() string { return string(c) }` — implementa `fmt.Stringer`, melhora logs e `%v` |

**Doc comment sugerida para `ParseCurrency`** (explicar o contrato):
```
// ParseCurrency valida e converte uma string em Currency.
//
// Aceita exclusivamente o código ALFABÉTICO ISO 4217: exatamente três letras
// maiúsculas ASCII (A-Z). Exemplos válidos: "BRL", "USD", "EUR".
//
// Rejeitados de propósito, com ErrInvalidCurrency:
//   - string vazia ou só espaços
//   - comprimento diferente de 3
//   - letras minúsculas ("brl"), para não existirem duas representações do
//     mesmo valor na chave de idempotência
//   - código numérico ISO ("986"), porque o contrato externo usa o alfabético
//   - dígitos, símbolos ou texto misturado ("R$", "BRL1", "B RL")
//
// Não há normalização aqui (trim, uppercase): a borda do sistema normaliza de
// forma explícita e documentada. Este pacote rejeita em vez de adivinhar.
```

**Dica de implementação a indicar ao usuário** (sem escrever o código por ele):
- Um laço `for i := 0; i < len(s); i++` com `s[i] < 'A' || s[i] > 'Z'` → erro
- A comparação de bytes é correta **porque** o padrão é ASCII; o comentário deve dizer isso

**Checkpoint do Bloco 2:** pedir para o usuário rodar
```powershell
go build ./internal/domain/money/ && go vet ./internal/domain/money/
```
E depois seguir para o Bloco 3 (`type Money struct`).

### 6.5. Casos de teste que a suíte do `Money` PRECISA cobrir (Bloco 8)

Derivados dos itens 5.1, 6.1 e 13 do enunciado:

**Parsing válido:** `"0.00"`, `"25.00"`, `"1000.00"`, `"9999999999999999.99"` (limite)
**Parsing inválido:** `""`, `" "`, `"25.0"`, `"25.000"` (**escala excedente**), `"25,00"`,
`"NaN"`, `"Inf"`, `"-Inf"`, `"1e2"` (**notação científica**), `"+25.00"`, `" 25.00"`,
`"25.00 "`, `"abc"`, `"--25.00"`, valor acima do limite (**overflow no parse**)
**Currency:** `"BRL"`, `"USD"` válidos; `""`, `"brl"`, `"BR"`, `"BRLL"`, `"B RL"`, `"986"`,
`"R$"`, `"B1L"` inválidos; `errors.Is(err, ErrInvalidCurrency)` verdadeiro
**Sinais:** negativos permitidos em `Money` interno, **rejeitados na entrada financeira externa**
**Aritmética:** soma, subtração, negação, `Cmp`, `Equal`, `IsZero`, `Sign`
**Overflow:** `Add`, `Sub` e `Neg` no limite → erro, nunca wrap silencioso
**Moedas:** `BRL + USD` → erro de moeda incompatível; `Cmp` entre moedas diferentes → erro
**Serialização:** round-trip `MarshalJSON` → `UnmarshalJSON`; formato exato `{"amount":"25.00","currency":"BRL"}`
**Auditoria anti-float (item 5.1, ELIMINATÓRIO):** nenhum `float32`/`float64` no pacote

---

## 7. PENDÊNCIAS CONHECIDAS (não são da Fase 1, mas estão anotadas)

Do `CONTEXTO.md` §4: P1 (healthcheck do `api` no compose usa flag inexistente), P2 (ordem
`AccessLog` × `Recoverer`), P3 (`net.Listen` em vez de `select`+`time.After`), P4 (path
`/internal/wallets` — o enunciado pede `POST /wallets`), P5 (registrar Postgres/SQS no grupo
`health_checkers`), P6 (`cmd/migrate` não existe ainda — o compose o referencia),
P7 (`StopTimeout` deve vir da config), P8 (testar SIGTERM real). P9/P10 **resolvidas**: o
clone vazio do GitHub foi movido para `_backup_jungle_desafio_repo` e o `.git` residual removido.

---

## 8. CHECKLIST DE ARRANQUE PARA A NOVA CONVERSA

- [ ] Ler `CONTEXTO.md`, `docs/INFRAESTRUTURA.md` e este arquivo
- [ ] Entregar o **Bloco 2** (`ParseCurrency`) no formato da seção 6.4:
      objetivo → problema → decisões → código → tabela → checkpoint
- [ ] Manter o cadence: **um bloco por vez**, explicar antes, perguntar se entendeu
- [ ] Aceitar contestação do usuário: se ele questionar o código e estiver certo, **admitir e corrigir**
- [ ] Preferir comentários **curtos** (o usuário enxugou os do Bloco 1): só o que explica decisão
- [ ] Depois do Bloco 2 aprovado, commitar: `feat(money): adiciona ParseCurrency com validacao ISO 4217`
- [ ] Seguir para o **Bloco 3** (`type Money struct` + `New` + acessores)

**Não é necessário:** revalidar o Bloco 1, recommitar, nem rodar Docker (a Fase 1 é puro Go,
sem banco).
