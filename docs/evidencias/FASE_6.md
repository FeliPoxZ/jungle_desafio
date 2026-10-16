# EVIDÊNCIAS — Fase 6 (caso de uso `wagering.Service`)

> Saídas reais dos comandos, contra PostgreSQL 16 real (`jungleg-postgres`,
> `healthy`) e o banco `jungle_test`. Coletadas em 2026-10-16.
>
> **Estado da fase:** bloco 6a (`Process`) e bloco 6b (`OpenWallet`) concluídos e
> validados. O bloco 6c (leituras + `Reconcile`) é o próximo.

## Ambiente

| Item | Versão/valor |
| --- | --- |
| PostgreSQL | 16-alpine (`jungleg-postgres`, healthy) |
| Driver | `github.com/jackc/pgx/v5 v5.11.0` |
| Pacote do caso de uso | `internal/app/wagering` (`service`, `request`, `payload`, `reference`, `outcome`, `events`, `codes`, `errors`, `opening`) |
| Adições de contrato | `ports.Clock`, `platform/clock`, `GetByReferenceAndKind` em `ports` + `txnRepo` |
| Migrations | `000001_init` (Fase 4) e `000002_opening_sem_jogo_e_rodada` (6b) |
| Testes | `internal/app/wagering/*_test.go` (unitário) e `test/integration/` (integração) |

## 1. Build, vet e formatação

```
$ go build ./...
(vazio)

$ go vet ./...
(vazio)

$ go vet -tags=integration ./...
(vazio)

$ gofmt -l ./internal/ ./cmd/ ./migrations/ ./test/
(vazio)
```

> `gofmt -l .` na raiz lista os arquivos do **cache de módulos** (`.gocache/mod`,
> dentro do workspace e no `.gitignore`): dependência de terceiros não é nossa
> para formatar. Por isso a checagem é escopada nos diretórios do projeto.

## 2. Testes

```
$ go test -count=1 -race ./...
ok  	.../internal/app	2.343s
ok  	.../internal/app/ports	1.301s
ok  	.../internal/app/wagering	1.457s
ok  	.../internal/config	1.333s
ok  	.../internal/domain/ledger	1.318s
ok  	.../internal/domain/money	1.546s
ok  	.../internal/domain/wagering	1.360s
ok  	.../internal/domain/wallet	1.320s
ok  	.../internal/httpapi	1.697s
ok  	.../internal/infra/postgres	1.482s
ok  	.../internal/observability	1.497s
ok  	.../internal/platform/canonicaljson	1.224s
ok  	.../internal/platform/idgen	1.217s

$ TEST_DATABASE_URL=postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable \
    go test -count=1 -race -tags=integration -timeout 300s ./...
ok  	.../internal/infra/postgres	6.404s
ok  	.../test/integration	8.927s
(13 pacotes de unidade + 2 de integração, todos ok)
```

> O tempo de `test/integration` inclui a espera pelo lock do banco de teste (ver
> §6.3): as duas suítes de integração usam o MESMO banco e truncam tabelas.

## 3. Bloco 6a — operações externas (`Process`)

### 3.1. O cenário do README §8, contra o banco real

`TestCenarioDoREADMEDuasApostasDe80Sobre100` — carteira com **100.00**, duas
apostas DIFERENTES de **80.00** disparadas ao mesmo tempo, cada uma em sua
transação:

```
processadas = 1, rejeitadas = 1          (a rejeitada com INSUFFICIENT_FUNDS)
saldo no banco = 20.00                   (100.00 − 80.00)
versão da carteira = 2                   (UMA movimentação)
ledger = [DEBIT 80.00]                   (UM débito, não dois)
eventos = 1 Processed, 1 BalanceChanged, 1 Rejected
```

E os reenvios das duas apostas não alteram nada: saldo continua 20.00 e o ledger
continua com um lançamento.

### 3.2. Idempotência persistente

| Cenário | Teste | Resultado |
| --- | --- | --- |
| Mesma aposta 12× em paralelo | `TestMesmaApostaEmParaleloProcessaUmaVez` | 12 respostas com a MESMA transação, **1** lançamento, versão 2 |
| Replay devolve o saldo do processamento original | `TestReplayDevolveOSaldoDoProcessamentoOriginal` | primeira resposta 975.00; carteira vai a 875.00; replay devolve **975.00** |
| Idempotência sobrevive a processo novo | `TestIdempotenciaSobreviveAProcessoNovo` | serviço e Unit of Work novos devolvem a transação original |
| Mesma chave, conteúdo diferente | `TestMesmaChaveComConteudoDiferenteEConflito` | `ErrIdempotencyConflict` (409) |
| Identidade externa sob outra chave | `TestIdentidadeExternaComOutraChaveEConflito` | `ErrIdempotencyConflict` (409) |

### 3.3. Nada fica gravado pela metade

`TestFalhaNaOutboxDesfazTudoNoBancoReal`: a transação do PostgreSQL é a REAL
(carteira, ledger e `wager_transactions` são os de produção); só o adaptador da
outbox é substituído por um que falha — o último passo do processamento.

```
saldo no banco = 1000.00     (o débito não ficou)
versão = 1
ledger: 0 lançamentos
wager_transactions: 0 linhas
outbox: 0 eventos
```

`TestFalhaNaOutboxNaoDeixaResiduo` prova o mesmo com o Unit of Work em memória, e
`TestFalhaTransitoriaERepetida` mostra que uma falha transitória é **repetida**
(2 execuções) e deixa **um** lançamento.

## 4. Bloco 6b — abertura de carteira (`OpenWallet`)

### 4.1. A armadilha que o README fecha

O README §9 fixa: *"a versão da carteira nessa abertura é `1`"*, e o exemplo de
resposta do `POST /wallets` traz `"balance": 1000.00, "version": 1`.

O caminho intuitivo — criar a carteira zerada e chamar `wallet.Credit` — está
errado **duas vezes**: `wallet.NewWallet` já nasce com o saldo inicial (o crédito
dobraria o saldo) e o `Credit` levaria a versão a `2`.

O que os testes exigem, então:

```
carteira:      saldo 1000.00, versão 1        (não 2000.00, não versão 2)
lançamento:    CREDIT 1000.00, 0.00 → 1000.00 (parte de ZERO, não do saldo)
OPENING:       PROCESSED, result_balance = 1000.00, sem jogo/rodada/provedor
eventos:       Processed + WalletBalanceChanged (walletVersion = 1)
```

E a soma do ledger é exatamente o saldo armazenado — que é o que a reconciliação
do enunciado confere.

### 4.2. O que mais os testes provam

| Cenário | Testes | Resultado |
| --- | --- | --- |
| Saldo inicial zero | `TestOpenWalletComSaldoZeroNaoCriaOpening` (unidade e integração) | carteira criada; **nenhuma** `OPENING`, nenhum lançamento, nenhum evento |
| Carteira duplicada `(jogador, moeda)` | `TestOpenWalletDuplicadaEConflito` | `WALLET_CONFLICT`; uma carteira só no banco |
| Reenvio da mesma abertura (mesma chave e conteúdo) | `TestOpenWalletIdempotenteNaoDuplicaNada` | `IdempotentReplay`, 1 transação, 1 lançamento, 2 eventos |
| Mesma chave com conteúdo diferente | `TestOpenWalletMesmaChaveConteudoDiferenteEConflito` | `IDEMPOTENCY_CONFLICT` |
| Replay devolve a carteira COMO ELA ESTÁ | `TestOpenWalletReplayDevolveACarteiraComoElaEsta` | após uma `BET` de 25.00, o replay devolve 975.00 e versão 2 |
| Falha no último passo (outbox) | `TestOpenWalletComOutboxQuebradaNaoDeixaNemCarteira` | **nem a carteira** fica: tudo entra no mesmo commit |
| Abertura + aposta fecham o exemplo do README | `TestAberturaMaisApostaFechaComOExemploDoREADME` | saldo 975.00 = soma do ledger, 2 lançamentos |

### 4.3. A migration 000002 (constraint x README)

O schema inicial exigia `game_id` e `round_id` não vazios para **toda** linha —
inclusive `OPENING`, que pela definição do README §6.3 não pertence a jogo nem a
rodada. Abrir carteira exigiria **inventar** os dois valores.

A migration 000002 troca a constraint:

```sql
CHECK (
    btrim(player_id) <> '' AND btrim(idempotency_key) <> '' AND btrim(payload_hash) <> ''
    AND (kind = 'OPENING' OR (btrim(game_id) <> '' AND btrim(round_id) <> ''))
)
```

Verificado no banco real, os dois lados:

```
OPENING com jogo/rodada vazios: ACEITO (ok)
BET com jogo vazio: RECUSADO (check constraint)
```

`deploy/postgres/verify_constraints.sql` passou de 30 para **31 operações
recusadas**, mais um caso **positivo** (a abertura sem jogo/rodada) que existe
para o afrouxamento não passar despercebido se algum dia abrir a porteira.

## 5. Defeitos reais encontrados e corrigidos na fase

Nenhum era conhecido antes. Dois são defeitos de código (o segundo só apareceu
por causa de um teste de concorrência), um é de schema, e dois são do harness de
teste — um deles criado pelo próprio bloco 6a.

### 5.1. `wagering.Error` era um tipo próprio, e `CodeOf` devolvia vazio

`money`, `wallet` e `ledger` compartilham `domainerrors.Error` por **alias**, e o
comentário do `money` chegava a afirmar que `wagering.Error` era o mesmo tipo.
Não era: o pacote `wagering` redefinia o struct.

| | |
| --- | --- |
| **Sintoma** | `domainerrors.CodeOf(erroDoWagering)` devolvia `""` — a resposta HTTP sairia com `failureCode` **em branco** para `REFERENCE_REQUIRED`, `OPERATION_NOT_ALLOWED`, `ALREADY_REVERSED`, `TERMINAL_STATE`... |
| **Por que passou despercebido** | Cada pacote testava com a **sua própria cópia** do extrator de Code (`errors.As` com o tipo local), então a suíte ficava verde enquanto a borda perdia o código |
| **Correção** | `type Error = domainerrors.Error` em `wagering`, como nos outros três. O texto do erro passou a ser `"CODE: mensagem"` (sem o prefixo `wagering:`), e o teste de formato foi ajustado |

### 5.2. A corrida entre as duas leituras de idempotência virava conflito falso

| | |
| --- | --- |
| **Sintoma** | 12 disparos simultâneos da MESMA operação: entre 1 e 4 pedidos legítimos recebiam `IDEMPOTENCY_CONFLICT` em vez de replay. Aparecia em ~3 de 4 execuções |
| **Causa** | O passo 1 faz DUAS consultas (por chave e por identidade externa). Entre elas, outra instância pode **comitar** a mesma operação. O código antigo, ao achar a linha na segunda consulta, concluía "registrada sob outra chave" **sem olhar a chave da linha encontrada** |
| **Diagnóstico** | O teste foi instrumentado para consultar o banco no instante do erro: havia **1** linha por chave e **1** por identidade externa — era a mesma linha, com a mesma chave |
| **Correção** | A segunda leitura só é conflito quando a chave da linha encontrada é DIFERENTE da chave pedida; chave igual é a mesma operação, e quem decide entre replay e conflito é o hash do payload (README §9: "não pode ser reaplicada usando **outra** chave") |
| **Trava** | `TestLinhaApareceEntreAsDuasLeiturasDeIdempotenciaEUmReplay`, que esconde a primeira consulta uma vez e exige replay |

### 5.3. Dois pacotes de integração truncando o mesmo banco, em paralelo

| | |
| --- | --- |
| **Sintoma** | `go test -tags=integration ./...` falhava com "registro não encontrado", "violates foreign key constraint" e `40P01 deadlock` no `TRUNCATE` — mas cada pacote passava sozinho |
| **Causa** | Os pacotes de integração rodam em **processos** paralelos, apontam para o MESMO banco e cada teste faz `TRUNCATE` |
| **Correção** | Lock consultivo do PostgreSQL (`pg_advisory_lock`) tomado no `TestMain` dos dois pacotes: quem chega segundo espera a suíte inteira do primeiro |
| **Achado dentro do achado** | Com o `lock_timeout` de 3s que o pool configura em toda conexão, a **espera pelo lock consultivo** morria com `55P03` — o mesmo código que, em produção, é classificado como transitório e repetido. A sessão do harness espera sem prazo (`SET lock_timeout = 0`) |

### 5.4. `game_id` e `round_id` obrigatórios para `OPENING` (§4.3)

| | |
| --- | --- |
| **Sintoma** | Nenhum ainda — o defeito apareceu ao DESENHAR a abertura: o schema e o domínio exigiam jogo e rodada de uma operação que não tem os dois |
| **Consequência se ficasse** | A aplicação gravaria jogo e rodada INVENTADOS em toda abertura de carteira, e o histórico financeiro passaria a mentir sobre esses campos |
| **Correção** | Migration 000002 (constraint condicional a `kind = 'OPENING'`) + exigência condicional no domínio + caso positivo no script de constraints |

### 5.5. `bin/migrate.exe` velho não aplica migration nova

| | |
| --- | --- |
| **Sintoma** | Depois de criar a 000002, `.\bin\migrate.exe up` respondeu "schema já está atualizado, nada a aplicar" e o banco ficou na versão 1 |
| **Causa** | O binário carrega as migrations **embutidas** (`go:embed`): um binário construído antes da migration nova não sabe que ela existe |
| **Correção** | Rebuild (`go build -o bin/migrate.exe ./cmd/migrate`) antes de migrar. Documentado no `ARCHITECTURE.md` §17 |

### 5.6. Armadilha reconfirmada do PowerShell (processo, não código)

Ao adicionar um import com `Set-Content -Encoding UTF8`, os acentos de um arquivo
de teste foram gravados em dupla codificação (`operação` → `operaÃ§Ã£o`). É a
terceira vez que a armadilha aparece; a lição 7 do `PROXIMO_PASSO` §3.4 continua
valendo: **editar texto com a ferramenta de edição**, nunca com `Set-Content`.

## 6. Decisões de design registradas

| Decisão | Motivo |
| --- | --- |
| `GetByReferenceAndKind` no contrato | O índice parcial `UNIQUE (reference_transaction_id, kind)` impede a dupla reversão, mas fala pelo `23505`, que **aborta a transação** e não diz qual unicidade caiu. A consulta prévia dá o código certo (`ALREADY_REVERSED`); o índice continua sendo a barreira final |
| O retry envolve o `Run` inteiro | Repetir por dentro deixaria a conexão da tentativa que falhou segurando o lock da carteira (lição 3 do §3.4) |
| Retry também no **conflito**, não só no transitório | No nosso schema, unicidade violada significa "alguém chegou primeiro": a segunda passada lê o gravado e decide entre replay e conflito, em vez de devolver erro de banco |
| Retry esgotado **não** grava `FAILED` | A transação não commitou, então não existe linha para marcar. Esgotou ⇒ erro transitório (503 na Fase 7). `MarkFailed` fica para onde a linha já existe (worker de referências) |
| O instante (`now`) é lido **depois** do lock | Ele vai para o `occurredAt`, e a outbox publica por `occurred_at` com `MessageGroupId` = carteira. Lido antes do lock, a ordem publicada seria a de **início** das transações, não a dos efeitos confirmados |
| O `INSERT` da transação vem antes de resolver a referência | Todo desfecho (rejeição, pendência, conclusão) é um **estado da linha**: sem a linha não há onde gravá-lo. O `INSERT` grava `PENDING` e o `Save` grava o definitivo, no mesmo commit |
| A abertura **não** chama `carteira.Credit` | `NewWallet` já nasce com o saldo inicial: o crédito dobraria o saldo e levaria a versão a 2, contrariando o README §9. O lançamento de abertura registra a ORIGEM do saldo, partindo de `0.00` |
| `LOSS` grava o saldo observado no `result_balance` | O replay do `LOSS` devolve o saldo daquele instante, como qualquer operação concluída — e o saldo não mudou |
| Referência informativa não grava `reference_transaction_id` | O índice parcial `(reference_transaction_id, kind)` passaria a tratar um `WIN` como reversão da aposta, e dois `WIN` para a mesma `BET` colidiriam |
| `ROLLBACK` de `BET` já estornada ⇒ `ALREADY_REVERSED` | O índice permite `REFUND` e `ROLLBACK` conviverem; creditar de novo o mesmo débito criaria dinheiro. Desfazer o estorno é `ROLLBACK` apontando para o `REFUND`, que **debita** e é aceito |
| O replay da abertura devolve a carteira como ela está | A carteira é recurso mutável: representá-la com o saldo da criação mentiria sobre o recurso. O `IdempotentReplay` é o que diz que nada foi criado |
| `entrada` (e não `Movement`) nos emissores de evento | A abertura gera lançamento sem movimentação no agregado; `Movement` zerado significaria "não houve lançamento" — e a abertura tem lançamento |

## 7. Interpretações adotadas (README §15 pede que sejam explícitas)

1. **Referência em `BET`, `WIN` e `LOSS` é informativa.** É conferida quando
   existe (provedor, jogador, carteira, moeda e rodada) e **não bloqueia** a
   operação quando não resolve: um `WIN` cujo referente ainda não chegou não pode
   atrasar o pagamento. Referente que existe e está `REJECTED`/`FAILED` recusa com
   `REFERENCE_NOT_PROCESSABLE`.
2. **`REFUND`/`ROLLBACK` exigem tipo elegível e valor igual** ao referenciado
   (`REFUND` → `BET`; `ROLLBACK` → `BET`, `WIN` ou `REFUND`). Divergência em
   qualquer um dos dois é `REFERENCE_MISMATCH` — reversão parcial não faz parte do
   desafio.
3. **`WALLET_PLAYER_MISMATCH`** é código novo, além da tabela A8: a carteira é
   identificada por `(playerId, currency)`, e um par divergente é requisição
   incoerente. É rejeição **gravada** (422), não erro de entrada.
4. **Moeda da operação × moeda da carteira** usa `WALLET_CURRENCY_MISMATCH` (o
   código do domínio da carteira), e é rejeição gravada. O `CURRENCY_MISMATCH` do
   A8 fica para a borda, quando o próprio `money` do payload é inválido.
5. **`FAILED` não é produzido por operação síncrona.** Ele existe para falha
   permanente registrada em caminho onde a linha já está gravada.
6. **`Process` devolve rejeição como desfecho, não como `error`.** `error` é o que
   impede a operação de existir: entrada inválida, conflito, indisponibilidade.
7. **Abertura com saldo zero não tem registro de idempotência.** O README manda
   não criar `OPENING` nesse caso, então o reenvio da mesma chave cai na unicidade
   da carteira e recebe `WALLET_CONFLICT` — a resposta prescrita para carteira
   duplicada.
8. **`OPENING` não tem jogo, rodada, provedor nem referência** — e a operação
   interna com qualquer um deles é recusada, no domínio e (para jogo/rodada) no
   schema.

## 8. Limitações conhecidas da fase

| Limitação | Por que ficou assim |
| --- | --- |
| A ordem entre os DOIS eventos da MESMA transação não é garantida | `WagerTransactionProcessed` e `WalletBalanceChanged` compartilham o `occurred_at`, e a outbox não tem coluna de sequência. Não é problema de correção (os dois carregam `transactionId`), e o consumidor precisa ser idempotente de qualquer forma |
| A ordenação por `occurred_at` depende do relógio | Um salto para trás do relógio do processo poderia inverter a ordem de duas operações da mesma carteira. Ordem forte exigiria sequência por carteira (coluna/sequence) — fora do escopo |
| Uma referência que existe mas ainda está `PENDING` é lida sem lock | A decisão pode ficar "pendente" por um instante a mais e ser resolvida na tentativa seguinte do worker. Não há caminho em que isso movimente dinheiro errado |
| O `next_resolution_attempt_at` fica preenchido em operações de reversão já `PROCESSED` | Efeito de usar `MarkPendingReference` + `ResolveReference` (o agregado só resolve referência em `PENDING_REFERENCE`). A coluna só é lida para linhas `PENDING_REFERENCE` |
| A migration 000002 não reverte com aberturas já gravadas | O `ADD CONSTRAINT` antigo falharia com `OPENING` de jogo/rodada vazios — e recusar é melhor do que apagar ou inventar dado financeiro. A condição está documentada no próprio arquivo `.down.sql` |

## 9. Rotina de execução da fase

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'; $env:GOCACHE='E:\jungleg\.gocache\build'; $env:GOPATH='E:\jungleg\.gocache\gopath'
docker compose up -d postgres

# migrations EMBUTIDAS no binário: rebuilde depois de criar/alterar migration
go build -o bin\migrate.exe .\cmd\migrate\
$env:DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungleg?sslmode=disable'
.\bin\migrate.exe up

go build ./... ; go vet ./... ; go vet -tags=integration ./...
gofmt -l .\internal\ .\cmd\ .\migrations\ .\test\
go test -count=1 -race ./...

# integração: banco SEPARADO (os testes truncam tabelas)
$env:TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
go test -count=1 -race -tags=integration -timeout 300s ./...

# as 31 operações que o banco precisa recusar (+ 1 caso positivo)
# O `<` precisa do cmd.exe: o PowerShell 5.1 não redireciona entrada de comando
cmd /c "docker exec -i jungleg-postgres psql -U jungleg -d jungle_test -v ON_ERROR_STOP=1 < deploy\postgres\verify_constraints.sql"
```

Os dois pacotes de integração se serializam sozinhos (lock consultivo), então
**não é preciso** `-p 1`.
