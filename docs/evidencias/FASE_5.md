# EVIDÊNCIAS — Fase 5 (persistência `pgx` + Unit of Work) validada

> Saídas reais de comandos contra o PostgreSQL 16 real. Os testes de integração
> rodam no banco `jungle_test` (isolado do de desenvolvimento, porque truncam
> tabelas). Coletadas em 2026-10-16.

## Ambiente

| Item | Versão/valor |
| --- | --- |
| PostgreSQL | 16-alpine (`jungleg-postgres`, healthy) |
| Driver | `github.com/jackc/pgx/v5 v5.11.0` |
| Pacote | `internal/infra/postgres` |
| Arquivos | `errors.go`, `pool.go`, `uow.go`, `convert.go`, `wallet_repo.go`, `txn_repo.go`, `ledger_repo.go`, `inbox_repo.go`, `outbox_repo.go` |
| Apoio | `internal/platform/canonicaljson` (pendência da Fase 3) |

## 1. Build, vet e formatação

```
$ go build ./...
(vazio)

$ go vet ./...
(vazio)

$ gofmt -l ./internal/ ./cmd/
(vazio)
```

## 2. Testes

```
$ go test -count=1 -race ./...
ok  	.../internal/app	2.533s
ok  	.../internal/config	1.383s
ok  	.../internal/domain/money	1.598s
ok  	.../internal/domain/wagering	1.390s
ok  	.../internal/domain/wallet	1.327s
ok  	.../internal/httpapi	1.922s
ok  	.../internal/infra/postgres	1.558s
ok  	.../internal/observability	1.561s
ok  	.../internal/platform/canonicaljson	1.238s

$ TEST_DATABASE_URL=postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable \
    go test -count=1 -race -tags=integration -timeout 300s ./internal/infra/postgres/
ok  	.../internal/infra/postgres	6.200s
```

**38 testes**, sendo 13 de unidade (classificação de erro e cursor) e 25 de
integração contra o banco real.

## 3. As três garantias que a fase existe para provar

### 3.1. Não há lost update

`TestWalletLostUpdateNaoAcontece`: duas goroutines debitam 100 e 200 da mesma
carteira de 1000, ao mesmo tempo, cada uma em sua transação.

```
saldo final = 700.00          (1000 − 100 − 200)
versão final = 3              (1 inicial + 2 débitos)
soma dos lançamentos = -30000 (explica a diferença de saldo)
```

Sem `SELECT ... FOR UPDATE` as duas leriam 1000, calculariam 900 e 800, e a
última a gravar venceria — o saldo final seria 900 ou 800.

### 3.2. O lock realmente bloqueia

`TestWalletGetByIDForUpdateBloqueiaOutraTransacao`: a transação A toma o lock e
o segura por 1,5 s; B tenta o mesmo lock.

```
B levou 1.59s para conseguir o lock   (esperou A soltar)
```

Se B tivesse conseguido antes, teria lido estado desatualizado.

### 3.3. Nada fica gravado pela metade

`TestRollbackNaoDeixaResiduo`: grava débito na carteira, lançamento no ledger e
evento na outbox, e então falha de propósito.

```
saldo = 1000.00   (rollback desfez o débito)
versão = 1
ledger: 0 lançamentos
outbox: 0 eventos
```

`TestCommitGravaTudoJunto` é o espelho: quando dá certo, os três ficam
consistentes (`saldo 750.00`, soma dos lançamentos `-25000`).

## 4. Idempotência e unicidade testadas no banco real

| Cenário | Teste | Resultado |
| --- | --- | --- |
| Reenvio do provedor (mesma chave) | `TestTxnInsertEIdempotencia` | `ErrConflict` |
| Crédito inicial duplicado (`OPENING` com a mesma chave) | `TestTxnOpeningIdempotencia` | `ErrConflict` |
| Dois lançamentos para a mesma operação | `TestLedgerUnicoPorTransacao` | `ErrConflict` |
| Duas carteiras para o mesmo `(player_id, currency)` | `TestWalletUnicidadeDePlayerEMoeda` | `ErrConflict` |
| Saldo negativo gravado por fora do domínio | `TestWalletSaldoNegativoRecusadoPeloBanco` | `ErrCheck` |
| `UPDATE`/`DELETE` no ledger | `TestLedgerImutavelNoBanco` | `ErrCheck` (trigger) |
| Mensagem consumida duas vezes | `TestInboxDeduplicacao` | `inseriu=false`, sem erro |

## 5. `FOR UPDATE SKIP LOCKED` na outbox

`TestOutboxClaimBatchComSkipLocked`: dois publishers reivindicando ao mesmo
tempo, 2 eventos cada, de um total de 5.

```
eventos distintos reivindicados = 4
nenhum evento reivindicado duas vezes
```

`TestOutboxLeaseImpedeRepeticaoImediata`: depois de reivindicar, o evento fica
indisponível pelo prazo do lease — se o processo morrer, ele volta sozinho.

`TestOutboxPublicacaoERetry`: `MarkRetry` incrementa `attempts` **no banco**,
guarda o `last_error` e agenda o próximo envio; depois do backoff o evento
reaparece com `attempts=1`; depois de `MarkPublished` nunca mais é reivindicado.

## 6. Classificação de erro do driver

| Código PostgreSQL | Significado | Classificação |
| --- | --- | --- |
| `23505` | unicidade | `ErrConflict` — no nosso schema é a **idempotência funcionando** |
| `23514` | CHECK | `ErrCheck` |
| `23001` | restrict (trigger do ledger) | `ErrCheck` |
| `23503` | chave estrangeira | `ErrForeignKey` |
| `23502` | not null | `ErrCheck` |
| `40001`, `40P01` | serialização, deadlock | `ErrTransient` |
| **`55P03`** | **lock timeout** | **`ErrTransient`** |
| `57014`, `57P01`, `57P03`, `53300` | cancelamento, shutdown, conexões | `ErrTransient` |
| `08001`, `08006` | falha de conexão | `ErrTransient` |
| outros | — | `ErrPermanent` |

`TestClassifyNaoEmbrulhaDuasVezes` garante que classificar duas vezes não polui a
mensagem, e `TestClassifyTransitorios` cobre os 9 códigos retryáveis.

**`55P03` é o caso mais importante desta fase**: `SELECT ... FOR UPDATE` que
estoura o `lock_timeout` significa que outra transação estava na mesma carteira.
Tratar como erro permanente faria o serviço **rejeitar uma operação legítima**
por causa de uma disputa momentânea.

## 7. Decisões de design registradas

| Decisão | Motivo |
| --- | --- |
| `READ COMMITTED` + `SELECT ... FOR UPDATE`, não `SERIALIZABLE` | O conflito real é "duas operações na mesma carteira", e o `FOR UPDATE` serializa exatamente esse caso sem serializar o banco. `SERIALIZABLE` exigiria retry em praticamente todo caminho de escrita e abortaria transações que não conflitam |
| Repositórios criados **por transação** | Cada um carrega o `querier` da transação corrente; dois `Run` simultâneos nunca compartilham conexão |
| `querier` é interface mínima (`Exec`/`Query`/`QueryRow`) | Faz o MESMO repositório funcionar sobre `*pgxpool.Pool` e sobre `pgx.Tx`, sem duplicar consulta |
| `23505` vira `ErrConflict`, não erro de banco | No nosso schema o único é a idempotência e as proteções de duplicidade funcionando. O serviço decide entre replay e conflito comparando o hash |
| `MarkRetry` incrementa `attempts` **no banco** (`attempts + 1`) | Duas instâncias falhando ao mesmo tempo contariam a mesma tentativa se o valor viesse do processo |
| `ClaimBatch` aplica **lease** empurrando `available_at` | Se o publisher morrer no meio, o evento volta sozinho quando o prazo passar, em vez de ficar preso |
| Rollback com `context.WithoutCancel` | Se a `fn` falhou por contexto cancelado, um rollback com o mesmo contexto não executaria — e a transação ficaria aberta segurando lock |
| `SumSignedByWallet` soma no banco | Exato (`BIGINT`) e sem trazer milhares de linhas para somar em Go |
| Cursor `base64url(createdAt | entryId)` opaco | O cliente não monta nem interpreta o valor; a ordenação pode mudar sem quebrar o contrato |

## 8. Quatro defeitos reais encontrados e corrigidos

Nenhum era conhecido antes desta fase.

| # | Defeito | Impacto | Correção |
| --- | --- | --- | --- |
| 1 | `55P03` (lock timeout) classificado como **`ErrPermanent`** | O serviço rejeitaria uma operação de negócio legítima por disputa momentânea de lock, em vez de repetir | passou para `ErrTransient` com teste dedicado |
| 2 | `wallet.Debit` não recebia o `transactionID` | O lançamento do ledger ficava **órfão**: sem saber qual operação o causou, não há como responder "por que o saldo mudou?" | `Debit`/`Credit` passaram a exigir `TransactionID`, e `NewEntry` valida que não é vazio |
| 3 | `wallet.Debit` devolvia só o `Entry`, mas incrementa a **versão em memória** | Quem persistia usava `carteira.Version()` (já incrementada), o `UPDATE ... WHERE version = $` casava com zero linhas e a operação falhava por "conflito". O erro é sutil e aparecia como conflito de unicidade | criado o tipo `Movement`, que carrega o `Entry` **e** a `versionBefore` — o erro deixou de ser possível |
| 4 | `newEntryID` gerava string composta (`walletID|txnID|...`) | A coluna `wallet_ledger_entries.id` é `UUID`: o banco recusava com `22P02 invalid input syntax for type uuid`. **Nenhum lançamento conseguiria ser gravado** | passou a gerar `uuid.NewString()` |

O defeito 3 é o mais didático: o erro não estava no repositório, estava na
**forma da API do domínio**. Repositório nenhum conseguiria usar corretamente uma
API que exige adivinhar qual versão passar. A correção foi no domínio.

## 9. Rotina de execução

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'; $env:GOCACHE='E:\jungleg\.gocache\build'; $env:GOPATH='E:\jungleg\.gocache\gopath'
docker compose up -d postgres
$env:DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungleg?sslmode=disable'

# unitários (sem banco) — os de integração são pulados sem TEST_DATABASE_URL
go test -race ./...

# integração (banco separado, porque os testes truncam tabelas)
$env:TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
go test -count=1 -race -tags=integration -timeout 300s ./internal/infra/postgres/
```

**Dependência nova:** `github.com/jackc/pgx/v5` (driver) e
`github.com/golang-migrate/migrate/v4` (já usada pelo `cmd/migrate`). O
`testcontainers-go` foi baixado mas **não é usado**: os testes de integração
apontam para o Postgres do compose via `TEST_DATABASE_URL`, o que é mais simples
e não exige Docker-in-Docker.
