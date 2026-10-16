<!--
================================================================================
ARCHITECTURE.md — DOCUMENTO EM CONSTRUÇÃO
================================================================================
Este arquivo é atualizado a cada fase. Cada seção traz um marcador de estado:

  [PRONTO]       implementado, testado e validado contra o banco/container real
  [PARCIAL]      parte implementada, o resto vem em fase indicada
  [PLANEJADO]    desenhado, ainda não escrito (a fase indica quando entra)
  [DECISÃO]      escolha tomada, com motivo — não muda sem motivo novo

Ao concluir uma fase, troque os marcadores dela e acrescente as decisões que
surgiram. O objetivo final é que este documento responda, sozinho, "por que o
sistema é assim".
================================================================================
-->

# ARCHITECTURE.md — Jungle Desafio

Serviço em Go que processa operações financeiras de provedores de jogos
(`BET`, `WIN`, `LOSS`, `REFUND`, `ROLLBACK`) com garantias de dinheiro:
precisão monetária, idempotência persistente, ledger append-only, concorrência
sem perda de atualização, entrega de eventos sem perda e recuperação de falhas.

**Estado atual:** Fases 0–5 concluídas (domínio + schema + persistência). O
caminho de escrita ponta a ponta (HTTP/SQS) entra nas Fases 6–9.

**Índice de estado:**

| Área | Estado |
| --- | --- |
| Organização de pacotes e direção de dependência | `[PRONTO]` |
| Dinheiro (`money`) | `[PRONTO]` |
| Carteira e ledger | `[PRONTO]` |
| Transação de aposta (máquina de estados) | `[PRONTO]` |
| Schema, constraints e imutabilidade | `[PRONTO]` |
| Transações e concorrência (Unit of Work) | `[PRONTO]` |
| Idempotência | `[PRONTO]` — persistência + decisão de replay no caso de uso |
| Caso de uso da aplicação | `[PARCIAL]` — `Process` pronto (Fase 6a); abertura de carteira, leituras e reconciliação na 6b/6c |
| HTTP + autenticação | `[PLANEJADO]` — Fase 7 |
| Consumo do SQS (inbox) | `[PLANEJADO]` — Fase 8 |
| Outbox publisher e worker de referências | `[PLANEJADO]` — Fase 9 |

---

## 1. Organização do código e direção das dependências `[PRONTO]`

```
cmd/api            entrada do serviço (só composição e sinal)
cmd/migrate        entrada das migrations (up/down/version)
migrations         SQL versionado, embutido com go:embed
internal/
  domain/          REGRA DE NEGÓCIO — sem I/O, sem driver, sem HTTP
    errors/        erro de domínio compartilhado (Code estável + mensagem)
    ids/           WalletID, PlayerID, TransactionID
    money/         valor monetário (int64 em unidades mínimas + moeda)
    ledger/        lançamento contábil append-only
    wallet/        carteira: saldo, versão, movimentação
    wagering/      transação de aposta: tipo, estado, eventos
  app/             APLICAÇÃO
    ports/         contratos (interfaces + sentinelas de erro + Cursor + Clock)
    wagering/      caso de uso: domínio + repositórios + outbox na MESMA transação
  infra/           ADAPTADORES
    postgres/      pgx: pool, unit of work, repositórios
  httpapi/         adaptador HTTP: roteador, middlewares, respostas
  platform/        utilitários sem regra de negócio
    canonicaljson/ JSON determinístico (hash de payload)
    clock/         relógio do processo, em UTC
    idgen/         geração de identificador (UUID v7)
  config/          configuração validada na inicialização
  observability/   log estruturado com correlação, métricas Prometheus
test/integration/  testes do caminho completo (camadas reais, banco do compose)
deploy/            compose auxiliares: init do Postgres, filas, realm do Keycloak
docs/              documentação e evidências
```

### A direção das dependências

```
        ┌──────────────┐
        │   domain     │   não depende de ninguém
        └──────▲───────┘
               │
        ┌──────┴───────┐        ┌──────────────┐
        │  app/ports   │◄───────┤    infra     │
        └──────▲───────┘        └──────────────┘
               │                       ▲
        ┌──────┴───────┐               │
        │     app      │───────────────┘
        └──────────────┘        (só na composição/Fx)
```

**`app/ports` existe para a aplicação não importar o banco.** Sem ele, o serviço
precisaria de `infra/postgres` só para nomear o tipo dos repositórios, e trocar
PostgreSQL por outra coisa obrigaria a mexer na regra de negócio.

**Os sentinelas de erro são os MESMOS objetos nos dois lados.** `postgres.ErrConflict`
é um alias de `ports.ErrConflict`. Se fossem dois jogos distintos,
`ports.IsConflict(erroDoRepositorio)` devolveria `false` — o serviço trataria
conflito de idempotência como falha desconhecida, sem erro de compilação e sem
teste vermelho. Há teste de regressão que passa um `PgError` de verdade pela
classificação e confere que o contrato o reconhece.

**Asserções de interface resolvidas em compilação** (`postgres/contracts_test.go`):
mudar a assinatura de um método do repositório quebra o build ali, e não no meio
de outra fase.

### Por que o domínio tem cinco pacotes

| Pacote | Por que é separado |
| --- | --- |
| `errors` | Os três domínios tinham cópia própria de `Error`/`Is`. Três implementações de `errors.Is` podem divergir; uma só, não |
| `ids` | `ledger` precisa de `WalletID` e `wallet` precisa de `ledger.Entry`. Sem um pacote comum, seria ciclo `wallet ⇄ ledger` |
| `money` | É o alicerce: `ledger` e `wallet` dependem dele, e ele não depende de ninguém |
| `ledger` | O lançamento é **outro agregado**. Ver §3 |
| `wallet` | A carteira guarda o saldo atual e é o que o lock protege |

`TransactionID` é **um tipo só**, com alias em `wagering` e `ledger`. Como dois
tipos distintos para o mesmo conceito, o serviço passaria o dia convertendo — e
uma conversão esquecida viraria string vazia, que é exatamente o bug silencioso
que `ledger.NewEntry` recusa.

### O erro de domínio é um tipo só, e isso vale para `wagering` também

`money.Error`, `wallet.Error`, `ledger.Error` e `wagering.Error` são **alias** de
`domainerrors.Error`. Não é economia de código: `domainerrors.CodeOf` atravessa a
cadeia de erros com `errors.As`, e um pacote que redefinisse o struct ficaria
**fora** dessa travessia — o `failureCode` da resposta sairia em branco para todo
erro daquele pacote, sem erro de compilação e sem teste vermelho. Aconteceu com
`wagering` (defeito 13 da §15).

---

## 2. Dinheiro: `int64` em unidades mínimas `[PRONTO]`

### A representação

```go
type Money struct {
    minor    int64     // R$ 25,00 → 2500
    currency Currency  // "BRL"
}
```

| Propriedade | Valor |
| --- | --- |
| Representação interna | `int64` em unidades mínimas (centavos) |
| Escala | 2, sempre |
| Contrato externo | `{"amount":"25.00","currency":"BRL"}` |
| Limite | `\|minor\| ≤ 9_000_000_000_000_000` (R$ 90 trilhões) |

### [DECISÃO] Por que não `float64`

`float64` é binário e **não representa decimais exatamente**: `0.1 + 0.2 != 0.3`.
Num ledger, esse resíduo se acumula e a soma dos lançamentos deixa de fechar com o
saldo — o sistema passaria a mentir sobre o dinheiro. A garantia não é
"provavelmente não dá erro": é aritmética inteira, exata por construção.

O limite é bem menor que o teto do `int64` **de propósito**: as guardas de
overflow precisam ser capazes de somar dois valores no limite sem estourar elas
mesmas. Com o teto real, a checagem estouraria junto com a operação.

### [DECISÃO] Por que o domínio não importa `encoding/json`

O `PayloadEncoder` é injetado. O domínio define o **que** serializar; a escolha de
como (e a garantia de determinismo) fica na plataforma. Isso mantém `money` e
`wagering` sem dependência de formato de transporte — o mesmo agregado serve HTTP,
SQS e teste sem arrastar `encoding/json` para dentro da regra de negócio.

### [DECISÃO] `UnmarshalJSON` estrito

Campo desconhecido e chave repetida são **erro**. Um JSON com `{"amount":"25.00","amount":"99.00"}`
tem duas leituras possíveis dependendo do parser; recusar é a única resposta que
não depende de interpretação.

### Armadilha conhecida: serialização assimétrica para negativos

Valores negativos serializam de forma diferente do que foram lidos. É
consequência de representar sinal e magnitude separadamente. Está **documentado e
travado por teste** — não é bug latente, é comportamento conhecido, e o teste
existe para que uma mudança acidental apareça.

### As guardas de overflow foram provadas, não argumentadas

O `Add` ganhou uma guarda de piso depois que uma **varredura exaustiva de 81
combinações** achou 10 furos — todos envolvendo dois negativos. O `Sub` tinha a
guarda com o sinal invertido e sem o teto. O `Neg` rejeitava quase todo valor
válido.

> **Lição registrada:** em sinal, limite e overflow, varredura exaustiva vale mais
> que raciocínio. "Uma guarda basta" esteve errado todas as vezes em que foi
> afirmado sem prova.

---

## 3. Carteira e ledger são agregados diferentes `[PRONTO]`

| | Carteira | Ledger |
| --- | --- | --- |
| Guarda | o saldo **atual** | o histórico de **cada movimentação** |
| Responde | "quanto tem agora" | "por que o saldo está assim" |
| Muda | a cada operação | nunca (append-only) |
| Protegido por | `SELECT ... FOR UPDATE` | trigger que recusa UPDATE e DELETE |
| Reconstruível a partir do outro? | **sim** (somando os lançamentos) | **não** |
| Volume | uma linha por carteira | uma linha por movimentação |

**Por que não são a mesma coisa:** a carteira responde rápido e é o que o lock
protege; o ledger é a prova auditável. Carregar o histórico inteiro para fazer um
débito de R$ 5 seria inviável. E o ledger **não pode** ser reconstruído da
carteira: o saldo atual não conta como ele chegou ali.

### Cada lançamento guarda dois saldos

`balance_before` e `balance_after` fazem a linha ser **auditável sozinha**. Se a
auditoria dependesse do saldo atual da carteira, ela mudaria a cada operação e não
serviria para auditar.

### A aritmética é conferida em dois lugares

1. no domínio (`ledger.NewEntry` recusa `after != before ± amount`) → o cliente
   recebe erro com `Code` estável;
2. no banco (`CHECK` na tabela) → nenhum caminho, nem SQL escrito à mão, grava
   incoerência.

A duplicação é intencional: a primeira dá mensagem boa, a segunda é a barreira que
sobrevive a bug de aplicação.

### [DECISÃO] `Movement`: por que o débito devolve a versão anterior

`Debit` e `Credit` incrementam a versão **em memória**. Quem persiste precisa da
versão **anterior** para o `UPDATE ... WHERE version = $`, e ler
`carteira.Version()` depois do débito devolve a versão nova — o `UPDATE` casa com
zero linhas e a operação falha, parecendo conflito de concorrência.

```go
mov, _ := carteira.Debit(txnID, valor, agora)
r.Wallets.Update(ctx, id, mov.VersionBefore(), carteira.Balance())
//                        ^^^^^^^^^^^^^^^^^ não tem como errar
```

**A correção foi no domínio, não no repositório:** o problema estava na *forma da
API*, que exigia do chamador um conhecimento implícito. Devolvendo a versão no
resultado, o erro deixa de ser possível de cometer.

---

## 4. Transação de aposta: máquina de estados `[PRONTO]`

```
                    ┌──────────────────────────────────────┐
                    │                                      │
  (criação) ──► PENDING ──► PROCESSED  (terminal)          │
                  │  │                                     │
                  │  └──► REJECTED     (terminal)          │
                  │                                        │
                  └──► PENDING_REFERENCE ──► PROCESSED     │
                              │              REJECTED      │
                              └──► FAILED ─────────────────┘
```

As transições são uma **tabela explícita**, não uma sequência de `if`s. Estado
terminal recusa qualquer transição, e a tentativa devolve erro tipado.

### Tipos de operação e o que cada um faz

| Tipo | Move dinheiro | Referência | Regra |
| --- | --- | --- | --- |
| `BET` | débito | opcional | exige saldo |
| `WIN` | crédito | opcional | pode referenciar aposta da mesma rodada |
| `LOSS` | **não** | opcional | valor zero; **não** gera lançamento, **não** incrementa versão, mas **gera evento** `Processed` |
| `REFUND` | crédito | **obrigatória** | devolve o valor integral da `BET` referenciada |
| `ROLLBACK` | inverso do original | **obrigatória** | desfaz `BET`/`WIN`/`REFUND` |
| `OPENING` | crédito | — | interno; crédito inicial da carteira |

### [DECISÃO] `OPENING` exige chave de idempotência

É a operação mais cara de duplicar: creditar a carteira duas vezes na abertura.
A proteção é um **índice único parcial** `(idempotency_key) WHERE kind = 'OPENING'`
— a unicidade vale só para esse tipo, então a mesma chave pode ser usada em
operações normais do provedor.

### `LOSS` é um caso especial bem definido

Não movimenta dinheiro, então: sem lançamento no ledger, sem incremento de versão,
sem evento `WalletBalanceChanged`. Mas **gera** `WagerTransactionProcessed`,
porque o provedor precisa da confirmação. Confundir "não move dinheiro" com "não
aconteceu nada" é o erro que esta regra evita.

---

## 5. Schema, constraints e imutabilidade `[PRONTO]`

Cinco tabelas: `wallets`, `wager_transactions`, `wallet_ledger_entries`,
`inbox_messages`, `outbox_events`.

| Recurso | Quantidade | Para que |
| --- | --- | --- |
| `CHECK` | 29 | invariantes (saldo ≥ 0, aritmética do lançamento, valor por tipo, coerência de estado) |
| Chave estrangeira | 4 | integridade referencial |
| `UNIQUE` | 2 | idempotência e carteira única por jogador |
| Índice único **parcial** | 3 | idempotência por tipo |
| Índice | 17 | leitura por provedor, por carteira, fila da outbox, pendências |
| Trigger | 5 | `updated_at` automático e imutabilidade do ledger |

**30 operações inválidas foram testadas e todas recusadas pelo banco**
(`deploy/postgres/verify_constraints.sql`).

### [DECISÃO] Imutabilidade do ledger por trigger, não por convenção

Um `prevent_ledger_mutation()` recusa `UPDATE` e `DELETE` em
`wallet_ledger_entries`. O repositório **não expõe** método de alteração (primeira
barreira); o trigger recusa SQL escrito à mão (segunda). "Corrigir" um lançamento
se faz com movimentação nova (estorno), não reescrevendo a história.

### [DECISÃO] `CHAR(3)` para a moeda, com cuidado no padding

`CHAR` no PostgreSQL **preenche com espaço**: o driver devolve `"BRL "`, não
`"BRL"`. Os repositórios normalizam na leitura (`TrimSpace`) e há teste que
compara com `money.BRL` — se o padding voltasse, o teste falharia.

---

## 6. Transações e concorrência `[PRONTO]`

### A regra central

**Toda operação financeira acontece em UMA transação:** `wager_transactions`,
`wallets`, `wallet_ledger_entries`, `inbox_messages` e `outbox_events`. Se
qualquer parte falhar, o dinheiro não se move e o evento não é publicado. Não
existe estado intermediário visível.

### [DECISÃO] `READ COMMITTED` + `SELECT ... FOR UPDATE`, e não `SERIALIZABLE`

| Critério | `SERIALIZABLE` | `READ COMMITTED` + `FOR UPDATE` |
| --- | --- | --- |
| Como evita lost update | aborta transações conflitantes (40001) | serializa **só a linha da carteira** |
| Retry necessário | em praticamente todo caminho de escrita | só sob disputa real na mesma carteira |
| Aborta transação sem conflito | pode acontecer | não |
| Granularidade do bloqueio | ampla | uma linha |

O conflito real do domínio é "**duas operações na MESMA carteira**", e o lock de
linha trata exatamente esse caso. Preferimos um lock curto e determinístico a um
retry imprevisível.

### A ordem importa: o lock vem antes de ler o saldo

```
1. SELECT ... FOR UPDATE   ← toma o lock
2. lê o saldo
3. calcula e grava
4. COMMIT
```

Ler primeiro e pedir o lock depois permite que outra transação altere o saldo no
meio. **Provado por teste**: duas goroutines debitando 100 e 200 de uma carteira
com 1000 terminam em **700.00** e versão **3**. Sem o `FOR UPDATE`, o resultado
seria 900 ou 800.

Há também teste que mede o **bloqueio**: a transação B esperou 1,59 s até A soltar
o lock. Se B tivesse entrado antes, teria lido estado desatualizado.

### Duas camadas de proteção (e por que as duas)

| Camada | O que faz | O que cobre |
| --- | --- | --- |
| `SELECT ... FOR UPDATE` | serializa quem chega junto | escritores concorrentes |
| `UPDATE ... WHERE version = $` | recusa gravar sobre estado que mudou | alteração por outro caminho |

Se apenas o lock existisse, uma escrita fora do fluxo passaria. Se apenas a versão
existisse, uma das transações falharia à toa em vez de esperar.

### [DECISÃO] Rollback com `context.WithoutCancel`

Se a operação falhou porque o contexto foi cancelado, um rollback com o **mesmo**
contexto não executaria — e a transação ficaria aberta segurando o lock até o
timeout. O rollback é a última coisa que pode falhar, então ele desconecta do
contexto do chamador.

### [DECISÃO] Repositórios criados por transação

Cada repositório carrega o `querier` da transação corrente, então dois `Run`
simultâneos nunca compartilham conexão. O `querier` é uma interface mínima
(`Exec`/`Query`/`QueryRow`) satisfeita tanto por `*pgxpool.Pool` quanto por
`pgx.Tx` — **o mesmo repositório funciona dentro e fora de transação**, sem
duplicar consulta.

### Classificação de erro do driver

| SQLSTATE | Significado | Classificação |
| --- | --- | --- |
| `23505` | violação de unicidade | `ErrConflict` → **é a idempotência funcionando** |
| `23514` | `CHECK` | `ErrCheck` |
| `23001` | `restrict` (trigger do ledger) | `ErrCheck` |
| `23503` | chave estrangeira | `ErrForeignKey` |
| `23502` | `NOT NULL` | `ErrCheck` |
| `40001`, `40P01` | serialização, deadlock | `ErrTransient` |
| **`55P03`** | **lock timeout** | **`ErrTransient`** |
| `57014`, `57P01`, `57P03`, `53300` | cancelamento, shutdown, conexões | `ErrTransient` |
| `08001`, `08006` | falha de conexão | `ErrTransient` |
| outros | — | `ErrPermanent` |

**`55P03` é o caso mais importante do sistema.** `SELECT ... FOR UPDATE` que
estoura o `lock_timeout` significa "outra transação estava nesta carteira" — o
exemplo mais claro de falha que **deve** ser repetida. Classificá-lo como
permanente faria o serviço **rejeitar uma operação legítima** por causa de uma
disputa momentânea. (Foi classificado errado na primeira versão e corrigido.)

**`23505` não é "erro de banco":** no nosso schema, unicidade violada significa que
a proteção funcionou. O serviço decide entre replay e conflito comparando o hash do
payload.

### [DECISÃO] A unidade de retry é a transação

Repetir uma chamada **dentro** da transação é o erro clássico: a tentativa que
falhou deixa a conexão com a transação aberta segurando o lock, e a repetição
trava. O sintoma aparece longe (um `TRUNCATE` de teste morrendo com deadlock, por
exemplo). **O retry envolve o `Run` inteiro.**

---

## 7. Idempotência `[PRONTO]`

### [PRONTO] Persistência

Três índices únicos, cada um para um tipo de duplicidade:

| Índice | Impede |
| --- | --- |
| `UNIQUE (provider_id, idempotency_key)` | o mesmo pedido do provedor processado duas vezes |
| `UNIQUE (idempotency_key) WHERE kind = 'OPENING'` | **crédito inicial duplicado** |
| `UNIQUE (reference_transaction_id, kind) WHERE ... IS NOT NULL` | reversão duas vezes sobre a mesma operação |
| `UNIQUE (wallet_id, transaction_id)` no ledger | dois lançamentos para a mesma operação |
| `UNIQUE (player_id, currency)` em carteiras | duas carteiras para o mesmo jogador |

### [DECISÃO] Por que `INSERT` + índice único, e não `SELECT` seguido de `INSERT`

O padrão "consultar se existe, e inserir se não existir" tem uma janela entre a
consulta e a inserção. Dois processos podem consultar ao mesmo tempo, ambos
verem "não existe", e ambos inserirem. A janela é curta, mas com dinheiro ela é
suficiente para creditar duas vezes.

Com o índice único, **o banco decide**: quem chegar segundo recebe `23505`, e o
serviço trata isso como o caminho esperado. Não existe janela.

### [PRONTO] Deduplicação de mensagem (inbox)

`InsertIfAbsent` usa `ON CONFLICT DO NOTHING` e devolve `false` **sem erro** quando
a mensagem já existia. Reentrega é o caminho esperado (o SQS garante *at least
once*), não uma falha. O `body_hash` guarda o conteúdo: hash igual é replay, hash
diferente é conflito real.

### [PRONTO] A decisão de replay (Fase 6a)

Ao reenviar o mesmo pedido:

```
mesma chave + mesmo hash   → REPLAY: devolve o resultado persistido, SEM movimentar
mesma chave + hash diferente → CONFLITO (409)
external_transaction_id com outra chave → CONFLITO (409)
```

**O replay devolve o saldo ORIGINAL**, não o saldo de agora — a carteira pode ter
mudado desde então, e o cliente pediu o resultado daquela operação. Zero é saldo
legítimo, então a distinção entre "não calculado" (rejeição, pendência) e "zero"
vem de `HasResultBalance`, e não do valor.

### [DECISÃO] São duas leituras, e a segunda compara a chave

A primeira consulta é pela chave de idempotência; a segunda, pela identidade
financeira `(providerId, externalTransactionId)`. As duas são comandos separados,
e **entre elas outra instância pode comitar a mesma operação**.

Comparar a chave da linha encontrada na segunda consulta não é zelo: sem isso,
esse instante é lido como "registrada sob outra chave" e um pedido **legítimo**
recebe 409. Foi um teste de 12 disparos simultâneos da mesma operação que pegou —
a linha aparecia entre a primeira e a segunda consulta (`docs/evidencias/FASE_6.md`
§6.2). O texto do README sustenta a regra: a operação não pode ser reaplicada
usando **outra** chave.

### [DECISÃO] O conflito de unicidade é retentado, não é erro

Quando o `INSERT` devolve `23505`, quem chegou segundo **repete a transação
inteira**. A segunda passada encontra a linha gravada pela primeira e responde
replay (mesma chave) ou conflito (chave diferente, payload diferente, referência
já revertida). No nosso schema, unicidade violada quer dizer "alguém chegou
primeiro" — não é falha de banco.

### [DECISÃO] Retry esgotado não grava `FAILED`

A transação não commitou, então **não existe linha** para marcar. Esgotadas as
tentativas, o erro transitório sobe (503 na borda) e o cliente pode repetir. O
`FAILED` fica reservado aos caminhos em que a linha já está persistida — o worker
de referências, que retoma uma linha `PENDING_REFERENCE` existente.

---

## 8. Outbox: eventos sem perda `[PARCIAL]`

### [DECISÃO] Por que outbox, e não publicar direto no SQS

O SQS é externo e **não participa do commit** do banco. Publicar dentro da
transação cria duas falhas possíveis:

| Situação | Consequência |
| --- | --- |
| SQS aceita, commit falha | o evento fala de uma mudança que **não aconteceu** |
| commit passa, SQS falha | a mudança acontece e **ninguém é avisado** |

Nenhuma das duas é aceitável com dinheiro.

Com a outbox, o evento é gravado **na mesma transação** que o saldo e o ledger: ou
tudo entra, ou nada entra. Um worker lê a tabela e publica depois. A entrega vira
"pelo menos uma vez", e o consumidor deduplica pelo `eventId`.

### [PRONTO] `ClaimBatch` com `FOR UPDATE SKIP LOCKED` + lease

```sql
UPDATE outbox_events SET available_at = $lease
WHERE event_id IN (SELECT ... WHERE available_at <= now() ... FOR UPDATE SKIP LOCKED)
RETURNING ...
```

- **`SKIP LOCKED`**: dois publishers pegam lotes **diferentes**, sem um bloquear o
  outro e sem reivindicar o mesmo evento. Provado por teste com 2 concorrentes
  (4 eventos distintos, nenhum repetido).
- **lease**: se o processo morrer antes de publicar, o evento volta sozinho quando
  o prazo passar, em vez de ficar preso para sempre.

`MarkRetry` incrementa `attempts` **no banco** (`attempts + 1`), não a partir do
valor lido: duas instâncias falhando ao mesmo tempo contariam a mesma tentativa se
o número viesse do processo.

### [PLANEJADO] Fase 9 — publisher e worker

- publisher: `ClaimBatch` → publica → `MarkPublished` / `MarkRetry` com backoff;
- republicação com o **mesmo `eventId`** (o consumidor deduplica);
- worker de referências: resolve `PENDING_REFERENCE` vencidas, backoff exponencial
  com jitter, TTL → `REJECTED REFERENCE_NOT_FOUND`;
- `PendingStats` alimenta o alerta de outbox parada.

### Envelope do evento `[PRONTO]`

```json
{
  "eventId": "...", "eventType": "WagerTransactionProcessed",
  "aggregateId": "<walletId>", "correlationId": "...", "causationId": "...",
  "occurredAt": "...", "version": 1,
  "data": { ... }
}
```

`aggregateId` é o `walletId` porque é ele o `MessageGroupId` na fila FIFO: eventos
da mesma carteira precisam ser processados em ordem.

---

## 9. Caso de uso da aplicação `[PARCIAL]` — Fase 6a (`Process`)

### O que existe hoje

`internal/app/wagering`: `Process(ctx, Request) (Result, error)` — a operação
externa ponta a ponta, em UMA transação. `OpenWallet`, as consultas e o
`Reconcile` entram nos blocos 6b/6c.

**Rejeição de negócio é desfecho, não erro.** `Result` carrega `Status`,
`FailureCode` e o saldo (quando existe); `error` fica para o que impede a
operação de existir: entrada inválida, conflito de idempotência,
indisponibilidade. A borda decide o status HTTP a partir disso — o caso de uso não
conhece HTTP.

### A ordem implementada dentro da transação

```
1.  normaliza, valida e calcula o hash canônico dos campos de negócio
2.  SELECT por (provider_id, idempotency_key)          ← replay x conflito
    SELECT por (provider_id, external_transaction_id)  ← outra chave x mesma chave
3.  SELECT carteira FOR UPDATE        ← o lock ANTES de qualquer cálculo
4.  cria a transação e grava (INSERT) ← todo desfecho é um estado DA LINHA
5.  valida jogador e moeda da carteira × operação
6.  resolve a referência
      ├─ ausente (reversão) → PENDING_REFERENCE + evento + 202, sem movimentar
      ├─ não processável/divergente → REJECTED + evento
      └─ compatível → valida tipo, valor e reversão anterior
7.  aplica a movimentação (Debit/Credit → Movement)
8.  grava saldo + versão (Update com mov.VersionBefore())
9.  grava o lançamento no ledger
10. transiciona, vincula a referência interna e grava o estado (Save)
11. grava os eventos na outbox
12. COMMIT  ← só depois disso a resposta sai
```

**Duas diferenças em relação ao desenho original do PLANO, ambas deliberadas:**

| Ajuste | Motivo |
| --- | --- |
| O `INSERT` (4) vem antes da resolução da referência (6) | Todo desfecho — rejeição, espera por referência, conclusão — é um **estado da linha**; sem a linha não há onde gravá-lo. O `INSERT` grava `PENDING` e o `Save` grava o definitivo, no mesmo commit: nenhuma outra transação chega a ver o `PENDING` |
| O instante (`now`) é lido **depois** do lock | Ele vai para o `occurredAt` dos eventos, e o publisher publica por `occurred_at` com `MessageGroupId` = carteira. Lido antes do lock, a ordem publicada seria a de **início** das transações, não a dos efeitos confirmados — e o `balanceAfter` de duas operações da mesma carteira poderia chegar invertido. Depois do lock, os instantes da mesma carteira são monotônicos, porque o lock os serializa |

### A referência interna de uma reversão resolvida na hora

O agregado só aceita `ResolveReference` em `PENDING_REFERENCE`. Então a reversão
cuja referência **já está disponível** passa por `MarkPendingReference` +
`ResolveReference` antes de concluir: é a transição que representa "esta operação
depende de uma referência", verdade mesmo quando ela já chegou. O estado
intermediário não é observável (`INSERT` grava `PENDING`, `UPDATE` grava
`PROCESSED`), e o `next_resolution_attempt_at` que sobra na linha concluída é
inerte — a coluna só é lida para linhas `PENDING_REFERENCE`.

`reference_transaction_id` só é gravado quando a operação **depende** da
referência. Em `BET`, `WIN` e `LOSS` a referência é informativa: gravada ali, o
índice parcial `UNIQUE (reference_transaction_id, kind)` passaria a tratar a
operação como reversão daquele fato, e dois `WIN` apontando para a mesma `BET`
colidiriam.

### Códigos de falha estáveis

| Situação | `failureCode` | Desfecho |
| --- | --- | --- |
| `BET` sem saldo | `INSUFFICIENT_FUNDS` | rejeição gravada (422) |
| `ROLLBACK` que exigiria saldo | `ROLLBACK_INSUFFICIENT_FUNDS` | rejeição gravada (422) |
| reversão já aplicada (mesmo tipo, ou `ROLLBACK` de `BET` já estornada) | `ALREADY_REVERSED` | rejeição gravada (422) |
| referência que existe e terminou rejeitada/falha | `REFERENCE_NOT_PROCESSABLE` | rejeição gravada (422) |
| referência divergente, tipo não reversível ou valor diferente | `REFERENCE_MISMATCH` | rejeição gravada (422) |
| moeda da operação ≠ moeda da carteira | `WALLET_CURRENCY_MISMATCH` | rejeição gravada (422) |
| carteira de outro jogador | `WALLET_PLAYER_MISMATCH` | rejeição gravada (422) |
| reversão sem referência ainda | — | `PENDING_REFERENCE` + evento (202) |
| referência nunca chegou (TTL esgotado, worker da Fase 9) | `REFERENCE_NOT_FOUND` | rejeição gravada (422) |
| mesma chave, hash diferente / identidade externa com outra chave | `IDEMPOTENCY_CONFLICT` | erro (409) |
| carteira inexistente | `WALLET_NOT_FOUND` | erro — a FK impede gravar a linha |
| sem chave, `OPENING` de fora, `LOSS` ≠ 0.00, reversão sem referência, tipo desconhecido | `IDEMPOTENCY_KEY_REQUIRED`, `OPERATION_NOT_ALLOWED`, `INVALID_AMOUNT`, `REFERENCE_REQUIRED`, `INVALID_TRANSACTION` | erro de entrada, **nada gravado** (400) |

`INSUFFICIENT_FUNDS` e `ROLLBACK_INSUFFICIENT_FUNDS` são **códigos diferentes de
propósito**: um é "o jogador não tem saldo para apostar", o outro é "não dá para
desfazer". Quem separa os dois é o **tipo da operação**, porque o domínio devolve
o mesmo sentinela nos dois casos.

A distinção entre erro de entrada (nada gravado, a mesma chave pode ser reenviada
depois de corrigida) e rejeição gravada (definitiva, com `failureCode` no
histórico) é o que o README chama de "distinguir entradas corrigíveis de
resultados definitivos".

### Política de retry

Três tentativas, 20 ms × tentativa, envolvendo **a transação inteira**. Dois
motivos: falha transitória (`55P03`, deadlock, conexão) e conflito de unicidade
(alguém chegou primeiro). Rejeição de negócio nunca é repetida — ela é desfecho.

### As interfaces ficam em `app/ports`

O serviço recebe `ports.UnitOfWork`, `ports.Clock` e `idgen.Generator`, e trabalha
sobre `ports.Repos`. Ele não conhece `infra/postgres`, e o teste de unidade roda
com um Unit of Work em memória — com semântica real de transação (cópia do estado
e troca só no fim sem erro), porque senão "falha no meio não deixa resíduo" seria
uma afirmação vazia.

### O que falta na Fase 6

`OpenWallet` (6b: carteira + `OPENING` + crédito + dois eventos no mesmo commit;
saldo zero não cria `OPENING`), e as leituras + `Reconcile` (6c: `GetTransaction`,
`GetByProviderExternalID`, `GetWallet`, `ListLedger` com cursor, reconciliação em
visão consistente, sem alterar saldo).

---

## 10. HTTP e autenticação `[PLANEJADO]` — Fase 7

- OIDC com Keycloak; `provider_id` vem do token, e o corpo da requisição **não**
  escolhe o provedor;
- `Idempotency-Key` obrigatório;
- `201` nova · `200` replay · `202` pendente · `422` rejeição de negócio ·
  `409` conflito · `403` provedor errado;
- `cmd/api` ganha a flag `-health-check` (usada pelo healthcheck do compose);
- rota `POST /wallets` (hoje o path interno é `/internal/wallets`).

---

## 11. Consumo do SQS `[PLANEJADO]` — Fase 8

- long polling (`WaitTimeSeconds=20`), prefetch limitado por semáforo;
- **inbox** compartilhando o **mesmo** `wagering.Service` do HTTP — uma regra de
  negócio, duas portas de entrada;
- transitório ⇒ não deleta, backoff por visibility;
- permanente ou `ApproximateReceiveCount > 5` ⇒ DLQ;
- `SIGTERM` ⇒ para de buscar, conclui o lote no prazo, senão **libera
  visibilidade** (`ChangeMessageVisibility 0`) para reentrega segura — melhor
  reprocessar (é idempotente) do que perder.

---

## 12. Observabilidade `[PARCIAL]`

`[PRONTO]` log estruturado com `correlationId`/`requestId` automáticos, redação de
dado sensível, e métricas Prometheus (`jungleg_*`).

`[PLANEJADO]` Fase 10: métricas de negócio (operações por tipo/status, latência,
tamanho da outbox, idade do pendente mais antigo, DLQ) e alertas.

---

## 13. Recuperação de falhas `[PARCIAL]`

| Falha | O que acontece | Estado |
| --- | --- | --- |
| Queda no meio da transação | rollback; nada gravado | `[PRONTO]` |
| Falha em qualquer passo (inclusive no último, o evento) | o commit não acontece: saldo, ledger, transação e evento voltam juntos | `[PRONTO]` |
| Reenvio simultâneo da mesma operação | um insere; o outro recebe `23505`, repete a transação inteira e devolve o resultado persistido | `[PRONTO]` |
| Disputa de lock na mesma carteira (`55P03`) | classificado como transitório e repetido (3 tentativas); esgotado, sobe como indisponibilidade | `[PRONTO]` |
| Queda depois do commit, antes de publicar | evento fica na outbox e o lease expira → republica | `[PRONTO]` (mecanismo) / `[PLANEJADO]` (worker) |
| Queda depois de publicar, antes de marcar | republica com o **mesmo `eventId`** → consumidor deduplica | `[PLANEJADO]` |
| Mensagem reentregue pelo SQS | inbox recusa a segunda | `[PRONTO]` (mecanismo) |
| Referência que nunca chega | TTL esgota → `REJECTED REFERENCE_NOT_FOUND` | `[PLANEJADO]` |
| `PENDING` antigo sem desfecho | reaper retoma | `[PLANEJADO]` |

---

## 14. Decisões de plataforma

| Decisão | Motivo |
| --- | --- |
| **UUID v7** para identificadores | O instante nos bits mais significativos mantém o índice primário denso (v4 aleatório faz cada insert tocar uma página diferente) e permite ordenar fatos sem consultar o banco. A alternativa `BIGSERIAL` é mais rápida, mas exige ida ao banco para saber o ID — e a aplicação precisa dele antes do insert, porque o evento e o lançamento já o referenciam |
| **JSON canônico** com chaves ordenadas | O hash do payload precisa ser estável: sem determinismo, o mesmo pedido enviado duas vezes vira operação nova e debita duas vezes. `SetEscapeHTML(false)` e `UseNumber` evitam que a serialização mude o significado |
| **`golang-migrate`** com `go:embed` | O binário carrega as migrations; não depende de arquivo solto no disco do container |
| **Fixtures de integração** apontam para o Postgres do compose | Mais simples que testcontainers e sem Docker-in-Docker. O banco `jungle_test` é separado porque os testes truncam tabelas |
| **`READ COMMITTED`** como padrão do pool | O lock explícito resolve o caso do domínio; ver §6 |

---

## 15. Problemas resolvidos (histórico)

Registro dos defeitos reais encontrados durante o desenvolvimento. Nenhum foi
introduzido de propósito, e todos foram corrigidos com teste que os trava.

| # | Fase | Defeito | Sintoma | Correção |
| --- | --- | --- | --- | --- |
| 1 | 1 | Guarda de overflow do `Add` de um lado só | 10 combinações de dois negativos passavam | guarda de piso; prova por varredura de 81 combinações |
| 2 | 1 | Guarda do `Sub` com sinal invertido, sem teto | subtração válida recusada / estouro possível | sinal corrigido + guarda de teto |
| 3 | 1 | Guarda do `Neg` invertida | quase todo valor válido era rejeitado | condição corrigida; verificado em 131.085 valores |
| 4 | 1 | `New` aceitava `minor` fora da faixa | `Money` inválido podia existir | validação de faixa no `New` |
| 5 | 1 | `UnmarshalJSON` aceitava campo desconhecido e chave repetida | duas leituras possíveis do mesmo JSON | `DisallowUnknownFields` + rejeição de chave duplicada |
| 6 | 5 | **`55P03` (lock timeout) classificado como permanente** | o serviço **rejeitaria operação legítima** por disputa momentânea | passou para transitório |
| 7 | 5 | `Debit` não recebia o `transactionID` | lançamento do ledger **órfão**: impossível responder "por que o saldo mudou?" | `Debit`/`Credit` exigem o ID; `NewEntry` valida |
| 8 | 5 | `Debit` devolvia só o `Entry`, mas incrementa a versão em memória | `UPDATE` casava com zero linhas e falhava parecendo conflito | tipo `Movement` carregando a `versionBefore` |
| 9 | 5 | `newEntryID` gerava string composta; a coluna é `UUID` | **nenhum lançamento conseguiria ser gravado** (`22P02`) | passou a gerar `uuid.NewString()` |
| 10 | 5 | `RETURNING` com coluna ambígua no `ClaimBatch` | `42702 column reference is ambiguous` | colunas qualificadas pelo alias |
| 11 | refactor | Sentinelas de erro duplicados em `ports` e `postgres` | `ports.IsConflict` devolveria `false` para erro do repositório, **silenciosamente** | alias: um jogo só, com teste de regressão |
| 12 | refactor | `wagering.TransactionID` e `ledger.TransactionID` eram tipos distintos | conversão obrigatória em todo o serviço; conversão esquecida viraria string vazia | tipo único em `ids`, com alias |
| 13 | 6a | **`wagering.Error` era um tipo próprio**, e não alias de `domainerrors.Error` | `domainerrors.CodeOf` devolvia `""` para TODO erro de `wagering`: `failureCode` em branco na resposta para `REFERENCE_REQUIRED`, `OPERATION_NOT_ALLOWED`, `ALREADY_REVERSED`... Sem erro de compilação e sem teste vermelho, porque cada pacote testava com a sua própria cópia do extrator de Code | alias, como em `money`/`wallet`/`ledger`; o texto do erro passou a `"CODE: mensagem"` |
| 14 | 6a | **Corrida entre as duas leituras de idempotência** virava conflito falso | 12 disparos simultâneos da MESMA operação: entre 1 e 4 pedidos legítimos recebiam `IDEMPOTENCY_CONFLICT` em vez de replay (3 de 4 execuções) | a segunda leitura só é conflito se a chave da linha encontrada for DIFERENTE da pedida; chave igual decide pelo hash |
| 15 | 6a | **Dois pacotes de integração truncando o mesmo banco em paralelo** | `go test -tags=integration ./...` falhava com "registro não encontrado", FK violada e `40P01` no `TRUNCATE`, enquanto cada pacote passava sozinho | lock consultivo (`pg_advisory_lock`) no `TestMain` dos dois pacotes |
| 16 | 6a | O `lock_timeout` de 3s do pool abortava a **espera pelo lock consultivo** | o segundo pacote de integração morria com `55P03` antes de a outra suíte terminar | a sessão do harness espera sem prazo (`SET lock_timeout = 0`) |

---

## 16. Limitações conhecidas e o que ficou de fora `[PRONTO]`

Registrar o que **não** está feito é parte da entrega: um documento que só fala do
que funciona não ajuda quem vai manter.

| Limitação | Por que ficou assim |
| --- | --- |
| `Rehydrate` da transação tem ~20 parâmetros | Espelha as colunas e evita depender de ordem de campo. Feio, mas explícito e seguro contra troca de argumentos |
| `Kind` é `string`, então um valor inválido responde `true` a `MovesMoney` | Mitigado por `Validate` rodar antes em todo caminho. Um tipo fechado exigiria enumeração em Go, que não existe |
| Sem foreign key de `outbox_events` para o agregado | A outbox precisa sobreviver à remoção do agregado; ela é a fila, não um detalhe dele |
| `testcontainers-go` baixado e **não usado** | Os testes apontam para o Postgres do compose. Remover com `go mod tidy` na Fase 12 |
| `LOSS` com referência: o serviço confere quando ela resolve | O README permite referência opcional. A régua adotada está registrada em `docs/evidencias/FASE_6.md` §8: referência informativa não bloqueia a operação |
| A ordem entre os DOIS eventos da MESMA transação não é garantida | `WagerTransactionProcessed` e `WalletBalanceChanged` compartilham o `occurred_at`, e a outbox não tem coluna de sequência. Os dois carregam `transactionId`, e o consumidor precisa ser idempotente de qualquer forma |
| A ordenação da outbox por `occurred_at` depende do relógio do processo | Um salto para trás do relógio poderia inverter a ordem de duas operações da mesma carteira. Ordem forte exigiria sequência por carteira (coluna/sequence) — fora do escopo |
| O dublê em memória não prova lock nem concorrência | Ele tem semântica de transação (cópia do estado, troca só no fim sem erro), mas não tem `FOR UPDATE`. Lock, lost update e disputa entre processos são provados contra o PostgreSQL real |
| `next_resolution_attempt_at` fica preenchido em reversão já `PROCESSED` | Efeito de usar `MarkPendingReference` + `ResolveReference`, que é o único caminho que o agregado oferece para vincular a referência interna. A coluna só é lida para linhas `PENDING_REFERENCE`, então o valor é inerte |
| Sem `ARCHITECTURE.md` de HTTP/SQS/workers | As seções 10 e 11 estão `[PLANEJADO]`; entram nas Fases 7–9 |

---

## 17. Como validar tudo `[PRONTO]`

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'
$env:GOCACHE='E:\jungleg\.gocache\build'
$env:GOPATH='E:\jungleg\.gocache\gopath'

docker compose up -d postgres
$env:DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungleg?sslmode=disable'
.\bin\migrate.exe up

go build ./... ; go vet ./... ; gofmt -l .
go test -race ./...

# integração (banco SEPARADO: os testes truncam tabelas)
$env:TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
go test -count=1 -race -tags=integration -timeout 300s ./...
# as 30 operações inválidas que o banco precisa recusar
docker exec -i jungleg-postgres psql -U jungleg -d jungleg < deploy/postgres/verify_constraints.sql
```

**Números atuais:** 574 casos no domínio (Fases 1–3) + **38** testes de
persistência (Fase 5: 13 de unidade, incluindo os 3 de contrato e classificação, e
25 de integração) + 3 de `idgen` + **32 casos** do caso de uso (Fase 6a) + **15
casos** de integração do caminho completo. `go test -race` verde nos **13 pacotes**
de unidade e nos 2 de integração. Cobertura: `money` 97,9%, `wallet` 99,0%,
`wagering` 100,0%.

Os dois pacotes de integração usam o MESMO banco de teste e truncam tabelas.
Como `go test ./...` roda pacotes em processos paralelos, cada `TestMain` toma um
lock consultivo do PostgreSQL (`pg_advisory_lock`) e espera a vez — **não é
preciso `-p 1`**.

**Evidências por fase:** `docs/evidencias/FASE_1.md` a `FASE_6.md`.
