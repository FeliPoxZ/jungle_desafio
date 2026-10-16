# EVIDÊNCIAS — Fase 3 (`WagerTransaction`) validada

> Saídas reais de comandos, coletadas em 2026-10-16. Puro Go: nenhum container,
> banco ou fila envolvido.

## Ambiente

| Item | Versão/valor |
| --- | --- |
| Go | 1.26.5 |
| Pacote | `internal/domain/wagering` |
| Código | 5 arquivos: `errors.go`, `kind.go`, `status.go`, `transaction.go`, `events.go` |
| Dependências internas | `internal/domain/money`, `internal/domain/wallet` |

## 1. Build, vet e formatação

```
$ go build ./...
(vazio)

$ go vet ./...
(vazio)

$ gofmt -l ./internal/
(vazio)
```

## 2. Testes com detector de corrida (`go test -race ./...`)

```
?   	github.com/FeliPoxZ/jungle_desafio/cmd/api	[no test files]
ok  	github.com/FeliPoxZ/jungle_desafio/internal/app	2.205s
ok  	github.com/FeliPoxZ/jungle_desafio/internal/config	1.263s
ok  	github.com/FeliPoxZ/jungle_desafio/internal/domain/money	1.428s
ok  	github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering	1.246s
ok  	github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet	1.264s
ok  	github.com/FeliPoxZ/jungle_desafio/internal/httpapi	1.713s
ok  	github.com/FeliPoxZ/jungle_desafio/internal/observability	1.511s
```

## 3. Volume e cobertura

```
$ go test -v ./internal/domain/wagering/
testes:   39
subtests: 88
total:    127 casos verdes, 0 falhas

$ go test -cover ./internal/domain/wagering/
ok  .../internal/domain/wagering	0.549s	coverage: 100.0% of statements
```

**100,0% de cobertura** — todas as funções, incluindo os acessores.

## 4. A máquina de estados

`PENDING` é o estado inicial. `PROCESSED`, `REJECTED` e `FAILED` são terminais.

| De | Para | Permitido |
| --- | --- | --- |
| `PENDING` | `PROCESSED` / `REJECTED` / `FAILED` / `PENDING_REFERENCE` | sim |
| `PENDING_REFERENCE` | `PROCESSED` / `REJECTED` / `FAILED` | sim |
| `PENDING_REFERENCE` | `PENDING` | **não** (não há regressão de estado) |
| `PROCESSED` / `REJECTED` / `FAILED` | qualquer | **não** (terminal) |

Verificado pela matriz completa de **25 transições** (`TestMaquinaDeEstados`),
incluindo o estado para ele mesmo.

**Erro específico por causa:** transição a partir de terminal devolve
`ErrTerminalState`; transição inválida entre estados não terminais devolve
`ErrInvalidTransition`. São códigos diferentes porque a causa é diferente — e o
`failureCode` que o cliente recebe precisa distinguir as duas.

## 5. Regras por tipo de operação

| Tipo | `IsExternal` | `RequiresReference` | `AllowsZeroAmount` | `MovesMoney` |
| --- | --- | --- | --- | --- |
| `OPENING` | não | não | não | sim |
| `BET` | sim | não | não | sim |
| `WIN` | sim | não | não | sim |
| `LOSS` | sim | não | **sim** | **não** |
| `REFUND` | sim | **sim** | não | sim |
| `ROLLBACK` | sim | **sim** | não | sim |

Coberto por `TestKindPropriedades`, `TestKindValidateAmount` e
`TestNewRegrasDeOrigem`.

## 6. Regras de origem (interna × externa)

| Regra | Teste | Resultado |
| --- | --- | --- |
| `OPENING` não aceita `providerID` | `TestNewRegrasDeOrigem/OPENING_com_providerID` | `OPERATION_NOT_ALLOWED` |
| `OPENING` não aceita `externalTransactionID` | idem | `OPERATION_NOT_ALLOWED` |
| `OPENING` continua exigindo chave de idempotência | `OPENING_sem_chave` | `INVALID_TRANSACTION` |
| Operação externa exige provedor, ID externo, chave e payload | 4 casos | `INVALID_TRANSACTION` |
| `REFUND`/`ROLLBACK` exigem referência | 2 casos | `REFERENCE_REQUIRED` |
| `LOSS` exige `0.00` exato | `LOSS_com_valor` | `INVALID_AMOUNT` |
| `BET`/`WIN`/`REFUND`/`ROLLBACK` exigem valor > 0 | 4 casos | `INVALID_AMOUNT` |

## 7. Conformidade com o enunciado (§6.3 e §7)

| Requisito do README | Onde está provado |
| --- | --- |
| §6.3 L129 — os seis tipos | `TestParseKind`, `TestKindPropriedades` |
| §6.3 L131 — metadados de operação externa (IDs, provedor, chave, hash, carteira, jogador, rodada, jogo, tipo, money, referência, estado, timestamps) | `WagerTransaction` + `TestAcessoresDaTransacao` |
| §6.3 L133 — nasce em `PENDING` | `TestNewRegrasDeOrigem` |
| §6.3 L133 — transições validadas pelo domínio | `TestMaquinaDeEstados` (25 transições) |
| §6.3 L139–141 — estados terminais | `TestEstadoTerminalRecusaQualquerTransicao` |
| §6.3 L143 — terminal não sofre nova transição | idem, com verificação de estado e `updatedAt` intactos |
| §6.3 L147 — `OPENING` via HTTP/SQS é rejeitado | `TestNewRegrasDeOrigem` (`OPERATION_NOT_ALLOWED`) |
| §6.3 L149 — `OPENING` exige identidade interna, carteira, jogador, moeda, valor, estado e timestamps; sem metadados externos | `TestNewRegrasDeOrigem`, `TestNewCamposObrigatorios` |
| §7 L174 — `LOSS` exige `"0.00"`, sem ledger e sem versão | `TestKindValidateAmount`, `TestLOSSProduzEventoProcessedSemSaldo` |
| §7 L178 — `REFUND`/`ROLLBACK` exigem `referenceExternalTransactionId` | `TestNewRegrasDeOrigem` |
| §7 L182 — zero aceito só no saldo inicial e em `LOSS` | `TestKindValidateAmount` (17 casos) |
| §7 L194 — `failureCode` estável e documentado | `MarkRejected` + sentinelas com `Code` + `TestErrorTextoECodigos` |
| A12 — envelope com `eventId`, `eventType`, `aggregateId`, `correlationId`, `causationId?`, `occurredAt`, `version`, `data` | `Event` + `TestEventoSerializaNoFormatoDoContrato` |
| A12 — `WagerTransactionProcessed` inclui `LOSS` | `TestLOSSProduzEventoProcessedSemSaldo` |
| A12 — `LOSS` e rejeição **sem** `WalletBalanceChanged` | `TestNewBalanceChangedEventValidacoes` (entry vazio é erro) |

## 8. Decisões de design registradas

| Decisão | Motivo |
| --- | --- |
| `OPENING` **também** exige chave de idempotência | É o que impede **crédito inicial duplicado**: sem chave, duas aberturas para o mesmo jogador creditariam duas vezes |
| `PayloadEncoder` é **injetado** | O hash precisa ser sempre do mesmo formato. Quem chama fornece JSON canônico (`internal/platform/canonicaljson`), então o domínio não importa `encoding/json` e a comparação deixa de ser frágil à ordem dos campos |
| `transicionar` é o **único** ponto que muda estado | Mesma disciplina do `wallet.apply`: uma regra, um caminho de mutação |
| Estado terminal tem erro **próprio** | `ErrTerminalState` ≠ `ErrInvalidTransition`: são `failureCode` diferentes porque a causa é diferente |
| `MoveMoney()` em `Kind` | É a regra que a **Fase 6** consulta para decidir se cria lançamento no ledger. Fica no domínio, não no serviço |
| `ResolveReference` **não** muda de estado | Resolver a referência e concluir a operação são decisões diferentes; quem decide o estado é o processamento |
| `MarkPendingReference` guarda tentativa e próximo instante | É o que dá **retomada durável** por outra instância depois de interrupção (README §6.3 L145) |
| `version` do envelope é `1`, fixo, do construtor | Versão de schema do evento, não da carteira. Quem chama não pode esquecer nem errar |
| Envelope serializa na construção | Evento que não serializa falha ao ser criado, não no publicador |

## 9. O que este pacote NÃO faz (e por quê)

| Não faz | Onde fica |
| --- | --- |
| Debitar/creditar a carteira | Fase 6 (`app/wagering`), usando `wallet.Debit`/`Credit` |
| Criar lançamento no ledger | Fase 6, usando `wallet.NewEntry` |
| Verificar idempotência (consultar chave existente) | Fase 5 (repositório) + Fase 6 (serviço) |
| Comparar provedor/jogador/carteira/rodada com a referência | Fase 6 — depende de **carregar** a transação referenciada |
| Decidir se a referência está pendente ou não processável | Fase 6, com o repositório |
| Publicar evento | Fase 9 (outbox publisher) |
