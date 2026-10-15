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
> **Não comece a codar sem ler o método de trabalho (seção 3 abaixo). Ele foi ajustado
> depois de duas correções do usuário e desrespeitá-lo quebra a confiança da sessão.**

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
| Fase 1 (`Money`) | 🔄 **EM ANDAMENTO — Bloco 1 de ~8 pronto para ser digitado** |
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
f1ec2da docs(plano): marca a Fase 0 como concluida e validada com infraestrutura real
1aaaa27 fix(infra): localstack community fixado e script de filas corrigido
1444707 docs: FX_EXPLICADO.md ensinando Uber Fx do zero
36a101c docs: adiciona INFRAESTRUTURA.md explicando o que sao as pecas antes da configuracao
967ed2c docs: guia didatico da Fase 0, questoes de fixacao e contexto entre sessoes
706d799 docs(plano): registra conclusao da Fase 0, decisoes de ambiente e bloqueios
1eef5b2 chore(setup): fundacao do projeto com Fx, config validada, HTTP e observabilidade
```

---

## 3. ⭐ MÉTODO DE TRABALHO (NÃO NEGOCIÁVEL)

O usuário pediu explicitamente e **duas vezes** reclamou que a IA foi rápida demais e
jogou configuração/código sem explicar. O ritmo combinado agora é:

| Regra | Como aplicar |
| --- | --- |
| **MODO B: linha a linha, juntos** | A IA explica um **bloco pequeno** (~10–25 linhas), **o usuário digita**, e só então vem o próximo bloco. A IA **não escreve o código do núcleo no arquivo** — o usuário escreve |
| **Explicar ANTES de escrever** | Regra, conceito, exigência do enunciado e o "porquê" vêm **antes** do código |
| **Um bloco por vez, com checkpoint** | Nunca entregar vários arquivos ou fases de uma vez. Esperar o usuário dizer "bloco N pronto" |
| **O núcleo é do usuário** | `Money`, `Wallet`, `LedgerEntry`, `WagerTransaction` são escritos por ELE. A IA entrega contrato + revisão. Infraestrutura (compose, migrations, repos, workers, testes de integração) a IA pode escrever |
| **Perguntar antes de avançar** | Se o usuário não entendeu, PARAR e explicar de novo. Perguntar "faz sentido?" ao fim de cada bloco |
| **Linguagem** | Português do Brasil, tom de mentor sênior, analogias concretas, tabelas comparativas |
| **Quando usar `write`/`edit`** | Preferir que o usuário digite. Usar `write` apenas quando ele pedir explicitamente ou quando o arquivo pedido for do bloco corrente (e avisar que foi escrito) |

**Alternativa se o relógio apertar:** o usuário autorizou que a IA complete o código
sozinha **se o tempo estiver acabando**, seguindo o padrão já construído. Nesse caso,
avisar antes e explicar o que foi feito.

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

### 4.2. Docker (funciona, mas com duas particularidades)
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

## 6. 🎯 A TAREFA PONTUAL: CONTINUAR A FASE 1 (`Money`) — BLOCO 1

### 6.1. Situação exata

A IA acabou de entregar o **Bloco 1 de ~8** (o tipo `Currency`) e pediu que o usuário:

1. Criasse a pasta e o arquivo:
   ```powershell
   cd E:\jungleg
   New-Item -ItemType Directory -Force -Path internal\domain\money | Out-Null
   New-Item -ItemType File -Force -Path internal\domain\money\money.go | Out-Null
   ```
2. **Digitasse o Bloco 1** em `internal\domain\money\money.go`
3. Respondesse **"bloco 1 pronto"**

**⚠️ O usuário ainda NÃO confirmou que digitou.** A primeira coisa a fazer na nova conversa é
**perguntar se ele digitou o Bloco 1**. Se não digitou, reenviar o Bloco 1 (está reproduzido
na seção 6.3, para não depender do histórico). Se digitou, validar e seguir para o Bloco 2.

### 6.2. Estrutura planejada do `money.go` (~8 blocos)

| Bloco | Conteúdo | Estado |
| --- | --- | --- |
| **1** | Doc do pacote, `type Currency string`, `const BRL`, `type Error`, `Error()`, `Is()`, `asError()`, `ErrInvalidCurrency` | ⏳ entregue, aguardando o usuário digitar |
| **2** | `ParseCurrency(s string) (Currency, error)` — valida ISO 4217 (exatamente 3 letras A–Z) | ⬜ |
| **3** | `type Money struct { minor int64; currency Currency }` + `New(minor, currency)` (reidratação) + acessores `Minor()`, `Currency()` | ⬜ |
| **4** | `Parse(amount, currency string) (Money, error)` — validação estrita de string decimal, com overflow | ⬜ |
| **5** | `Zero(currency)`, `Add`, `Sub`, `Neg` — com guarda de overflow e moeda compatível | ⬜ |
| **6** | `Cmp`, `Equal`, `IsZero`, `IsNegative`, `IsPositive`, `Sign` | ⬜ |
| **7** | `String()` (`"975.00"`) e `MarshalJSON`/`UnmarshalJSON` (formato `{"amount":"25.00","currency":"BRL"}`) | ⬜ |
| **8** | Testes do pacote (`money_test.go`) — tabela de casos: parsing feliz, limites, overflow, escala excedente, moedas incompatíveis, `NaN`/`Inf`, round-trip de serialização | ⬜ |

### 6.3. CONTEÚDO DO BLOCO 1 (reproduzido — para reenviar se o usuário não tiver digitado)

**Explicação a dar antes do código:**

- `Money` precisa carregar a moeda; `Currency` é **tipo próprio** (não `string` solta) para o compilador impedir trocas acidentais.
- Go não tem construtor obrigatório: validar **na fronteira** (parsing), não na "criação".
- Erro tipado com `Code` estável atende ao item 6 do enunciado (`errors.Is`/`errors.As`).

**Código do Bloco 1:**

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
// deixa de fechar com o saldo. O enunciado (item 5.1) proíbe ponto flutuante
// em parsing, cálculo, serialização e persistência — e é item ELIMINATÓRIO.
// Este pacote não importa nada que use float para dinheiro.
package money

import "fmt"

// Currency é um código de moeda ISO 4217.
//
// É um tipo próprio (e não uma string solta) de propósito: assim o compilador
// impede trocar a moeda por qualquer texto em uma chamada de função.
type Currency string

// BRL é o único código usado nos cenários principais do desafio.
const BRL Currency = "BRL"

// Códigos de erro estáveis. O Code é o que vira failureCode no contrato HTTP.
const (
	CodeInvalidCurrency = "INVALID_CURRENCY"
)

// Error é o erro de domínio do pacote money.
//
// Existe para atender ao item 6 do enunciado: erros de domínio precisam ser
// classificáveis por tipo (errors.As) e por valor (errors.Is). O campo Code é
// estável e documentado, enquanto Message é descritivo para humanos.
type Error struct {
	Code    string
	Message string
}

// Error faz *Error satisfazer a interface error.
func (e *Error) Error() string {
	return fmt.Sprintf("money: %s: %s", e.Code, e.Message)
}

// Is permite comparar erros por Code com errors.Is, mesmo quando o erro foi
// envelopado com fmt.Errorf("...: %w", err) alguns níveis acima.
func (e *Error) Is(target error) bool {
	var other *Error
	if ok := asError(target, &other); ok {
		return e.Code == other.Code
	}
	return false
}

// asError é um helper local que evita importar errors apenas para o Is.
func asError(target error, into **Error) bool {
	for target != nil {
		if e, ok := target.(*Error); ok {
			*into = e
			return true
		}
		u, ok := target.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		target = u.Unwrap()
	}
	return false
}

// Erros sentinela do pacote. Use errors.Is(err, money.ErrInvalidCurrency).
var (
	ErrInvalidCurrency = &Error{Code: CodeInvalidCurrency, Message: "código de moeda inválido"}
)
```

### 6.4. O que fazer quando o usuário disser "bloco 1 pronto"

1. **Validar a compilação:**
   ```powershell
   $env:GOMODCACHE='E:\jungleg\.gocache\mod'; $env:GOCACHE='E:\jungleg\.gocache\build'; $env:GOPATH='E:\jungleg\.gocache\gopath'
   cd E:\jungleg
   go build ./internal/domain/money/
   go vet ./internal/domain/money/
   gofmt -l ./internal/domain/money/
   ```
   Se houver erro, explicar a causa **antes** de corrigir. Não corrigir silenciosamente.

2. **Entregar o Bloco 2** (`ParseCurrency`), no mesmo formato do Bloco 1:
   - O problema que resolve
   - As decisões de design e o porquê
   - O código para o usuário digitar
   - Tabela explicando cada parte
   - Pedir "bloco 2 pronto"

3. **Ao chegar no Bloco 8 (testes)**, usar a **biblioteca padrão** (`testing`, table-driven tests,
   `t.Run`, `t.Fatalf`) — o enunciado exige *"testes com `testing` e `go test`, incluindo
   `-race`"*. Nunca introduzir testify ou outro framework de asserção.

### 6.5. Casos de teste que a suíte do `Money` PRECISA cobrir

Derivados diretamente do item 6.1 e do item 13 do enunciado:

**Parsing válido:** `"0.00"`, `"25.00"`, `"1000.00"`, `"9999999999999999.99"` (limite)
**Parsing inválido:** `""`, `" "`, `"25"` (sem casas?), `"25.0"`, `"25.000"` (**escala excedente**),
`"25,00"`, `"NaN"`, `"Inf"`, `"-Inf"`, `"1e2"` (**notação científica**), `"+25.00"`, `" 25.00"`,
`"25.00 "`, `"abc"`, `"--25.00"`, valor acima do limite (**overflow no parse**)
**Sinais:** negativos permitidos em `Money` interno, **rejeitados na entrada financeira externa**
**Aritmética:** soma, subtração, negação, `Cmp`, `Equal`, `IsZero`, `Sign`
**Overflow:** `Add` e `Sub` e `Neg` no limite → erro, nunca wrap silencioso
**Moedas:** `BRL + USD` → `ErrCurrencyMismatch`; `Cmp` entre moedas diferentes → erro
**Serialização:** round-trip `MarshalJSON` → `UnmarshalJSON`; formato exato `{"amount":"25.00","currency":"BRL"}`
**Auditoria anti-float (item 5.1, eliminatório):** nenhum `float32`/`float64` no pacote

---

## 7. PENDÊNCIAS CONHECIDAS (não são da Fase 1, mas estão anotadas)

Do `CONTEXTO.md` §4: P1 (healthcheck do `api` no compose usa flag inexistente), P2 (ordem
`AccessLog` × `Recoverer`), P3 (`net.Listen` em vez de `select`+`time.After`), P4 (path
`/internal/wallets` — o enunciado pede `POST /wallets`), P5 (registrar Postgres/SQS no grupo
`health_checkers`), P6 (`cmd/migrate` não existe ainda — o compose o referencia),
P7 (`StopTimeout` deve vir da config), P8 (testar SIGTERM real), P9/P10 (resolvidas: o clone
vazio foi movido para `_backup_jungle_desafio_repo` e o `.git` residual removido).

---

## 8. CHECKLIST DE ARRANQUE PARA A NOVA CONVERSA

- [ ] Ler `CONTEXTO.md`, `docs/INFRAESTRUTURA.md` e este arquivo
- [ ] Confirmar com o usuário se o **Bloco 1** foi digitado em `internal/domain/money/money.go`
- [ ] Se **não** digitou: reenviar o conteúdo da seção 6.3 e esperar
- [ ] Se **digitou**: validar com `go build` / `go vet` / `gofmt` e reportar o resultado
- [ ] Seguir para o **Bloco 2** (`ParseCurrency`) no formato: problema → decisões → código → explicação → checkpoint
- [ ] Manter o cadence: **um bloco por vez**, explicar antes, perguntar se entendeu
- [ ] Commitar ao fim da Fase 1 com mensagem no padrão do repositório (Conventional Commits em português, corpo explicando o porquê)
