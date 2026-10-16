# EVIDÊNCIAS — Fase 2 (`Wallet` + `LedgerEntry`) validada

> Saídas reais de comandos, coletadas em 2026-10-15. Puro Go: nenhum container,
> banco ou fila envolvido.

## Ambiente

| Item | Versão/valor |
| --- | --- |
| Go | 1.26.5 |
| Pacote | `internal/domain/wallet` |
| Código | 3 arquivos: `errors.go`, `wallet.go`, `ledger_entry.go` |
| Dependência interna | `internal/domain/money` (sem dependência externa) |

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
ok  	github.com/FeliPoxZ/jungle_desafio/internal/app	2.093s
ok  	github.com/FeliPoxZ/jungle_desafio/internal/config	1.248s
ok  	github.com/FeliPoxZ/jungle_desafio/internal/domain/money	1.389s
ok  	github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet	1.216s
ok  	github.com/FeliPoxZ/jungle_desafio/internal/httpapi	1.705s
ok  	github.com/FeliPoxZ/jungle_desafio/internal/observability	1.483s
```

## 3. Volume e cobertura

```
$ go test -v ./internal/domain/wallet/
testes:   29
subtests: 34
total:    63 casos verdes, 0 falhas

$ go test -cover ./internal/domain/wallet/
ok  .../internal/domain/wallet	0.575s	coverage: 99.0% of statements
```

A única linha não coberta é a guarda do erro de `NewEntry` dentro de `apply`:
inalcançável por construção, porque o `apply` já validou tudo que o `NewEntry`
revalida (moeda, valor positivo, instante e faixa). Documentado no código.

## 4. Conformidade com o enunciado (§6.2 `Wallet` + §Ledger)

| Requisito do README | Onde está provado |
| --- | --- |
| §6.2 — a carteira carrega identidade, jogador, moeda, saldo, versão e instantes | `TestNewWallet`, `TestRehydrateNaoReaplicaNemAltera` |
| §6.2 — débitos preservam saldo ≥ 0 | `TestDebit` (tabela com 4 casos de saldo insuficiente), `TestSaldoNuncaFicaNegativoEmSequenciaDeOperacoes` |
| §6.2 — moeda da movimentação coincide com a da carteira | `TestMovimentacaoEmMoedaDiferenteNaoEhSaldoInsuficiente`, `TestNewEntry` |
| §6.2 — cada mudança de saldo exige o lançamento correspondente | `TestDebitGeraLancamentoCoerente`, `TestCreditGeraLancamentoCoerente`, `TestLedgerAcumulaESaldoBate` |
| §6.2 — versão inicial 1; incrementa só quando o saldo muda | `TestNewWallet`, `TestVersaoSoAvancaQuandoOSaldoMuda`, `TestVersaoNuncaRegride` |
| §6.2 L89 (Encaps.) — criação e reidratação separadas; reidratação não reaplica, não incrementa versão, não emite evento | `TestRehydrateNaoReaplicaNemAltera`, `TestRehydrateEntry` |
| §Ledger — registra id, walletId, transactionId, direção, valor, saldo antes e depois, instante | `Entry` + `TestNewEntry` + `TestRehydrateEntry` |
| §Ledger — ledger append-only | `Entry` não tem setter e o valor é devolvido por cópia |
| §Tipos — `LOSS` = `"0.00"` exato, sem ledger e sem versão | `TestDebit`/`TestCredit` (casos de valor zero), `TestVersaoSoAvancaQuandoOSaldoMuda` |

## 5. Decisões de design registradas

| Decisão | Motivo |
| --- | --- |
| `apply` é o **único** ponto que muda saldo | `Debit` e `Credit` só escolhem a direção. Um caminho de mutação significa uma regra, não duas |
| Moeda é checada **antes** da aritmética | Carteira de 100.00 BRL debitada em 150.00 USD devolve `WALLET_CURRENCY_MISMATCH`, não `INSUFFICIENT_FUNDS`. A ordem define o `failureCode` que o cliente recebe |
| Valor **zero não gera lançamento** e não muda versão nem `updatedAt` | Mesma regra do `LOSS` (README §7): zero significa "sem movimentação financeira". Descoberto durante os testes — a primeira versão tentava criar lançamento de zero |
| Valor negativo é rejeitado na movimentação | O sinal é definido pela **direção**. Aceitar negativo inverteria a operação e seria uma forma de furar o saldo mínimo |
| `NewEntry` recebe `balanceBefore` e **recalcula** `balanceAfter` | O saldo posterior é derivado, nunca informado: assim não existe entrada de ledger incoerente por erro de chamada |
| `NewEntry` valida o que o banco também vai validar (`CHECK`) | O erro nasce no domínio, com `Code` estável para o `failureCode`, antes de virar violação de constraint |
| `TransactionID` é `string` neste pacote | O pacote de carteira não pode depender do pacote de transações (Fase 3). A ligação fica na camada de aplicação |
| `NewWallet`/`Debit`/`Credit` recebem `now time.Time` | Sem estado global e sem clock injetado: testes usam instantes fixos e determinísticos |
| Saldo inicial negativo é rejeitado na criação | O saldo da carteira nunca é negativo (README §6.2). Negativo existe em diferenças internas, não como saldo |

## 6. Casos defensivos (dados corrompidos)

`Rehydrate` não valida de propósito — a validação da leitura é do repositório, apoiada
na `CHECK constraint` da tabela (Fase 4). Ainda assim, o domínio recusa:

| Caso | Teste | Resultado |
| --- | --- | --- |
| Saldo acima do teto do `money` e crédito novo | `TestCreditComSaldoCorrompidoNoLimite` | erro de domínio, estado intacto |
| Saldo abaixo do piso do `money` e débito | `TestDebitComSaldoCorrompidoAbaixoDoPiso` | erro de domínio, versão intacta |
| Direção inválida | `TestApplyComDirecaoInvalida` | `ErrInvalidEntry`, estado intacto |
| Saldo posterior estourando o limite | `TestNewEntryComSaldoCorrompido` | `ErrInvalidEntry` |

## 7. Propriedades verificadas

| Propriedade | Teste |
| --- | --- |
| `Credit(x)` seguido de `Debit(x)` volta ao saldo original, com versão +2 | `TestCreditoSeguidoDeDebitoVoltaAoSaldoOriginal` |
| Nenhuma sequência de operações aceitas deixa saldo negativo | `TestSaldoNuncaFicaNegativoEmSequenciaDeOperacoes` |
| A soma dos lançamentos explica a diferença de saldo | `TestLedgerAcumulaESaldoBate` |
| `balanceAfter - balanceBefore == amount` (crédito) e o inverso (débito) | `TestCreditGeraLancamentoCoerente`, `TestDebitGeraLancamentoCoerente` |
| Falha não deixa estado pela metade (saldo, versão, instante) | `TestFalhaNaoDeixaEstadoPelaMetade` |
| Erro de domínio atravessa `errors.Join` e continua classificável | `TestErroAtravessaEnvelopamento` |
