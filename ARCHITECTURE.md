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

**Estado atual:** Fases 0–8 concluídas (domínio + schema + persistência + caso de
uso + API HTTP autenticada + consumo por SQS). Os workers de publicação e de
referências entram na Fase 9.

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
| Caso de uso da aplicação | `[PRONTO]` — `Process`, `OpenWallet`, leituras e `Reconcile` (Fase 6) |
| HTTP + autenticação | `[PRONTO]` — Fase 7 (§10) |
| Consumo do SQS (inbox) | `[PRONTO]` — Fase 8 (§11) |
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

**32 operações inválidas foram testadas e todas recusadas pelo banco**, mais um
caso POSITIVO que confirma que o afrouxamento de jogo/rodada vale só para
`OPENING` (`deploy/postgres/verify_constraints.sql`). Eram 30 na Fase 4: a
migration 000002 acrescentou a recusa de operação externa sem jogo/rodada, e a
000003 a recusa de `sequence` escrita à mão.

### [DECISÃO] `OPENING` não pertence a jogo nem a rodada (migration 000002)

O schema inicial exigia `game_id` e `round_id` não vazios para **toda** linha. Isso
contradizia o README §6.3 — "rodada, jogo e referência não se aplicam a essa
origem" — e a consequência não era teórica: abrir carteira exigiria **inventar**
um jogo e uma rodada para caber no contrato, gravando dado falso no histórico
financeiro.

A migration 000002 troca a constraint: as operações **externas** continuam
obrigadas a ter os dois, e `OPENING` não. O domínio acompanha (a exigência virou
condicional a `kind.IsInternal()`, e `OPENING` com jogo ou rodada é recusado).
O que o README pede do schema — "distinguir operações internas e externas" —
passou a valer também para esses dois campos.

A reversão da migration pressupõe que não exista `OPENING` com jogo/rodada vazios;
se existir, o `ADD CONSTRAINT` falha, e isso é proposital: recusar é melhor do que
apagar ou inventar dado financeiro para a constraint antiga voltar a valer.

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
WITH reivindicados AS (
    UPDATE outbox_events AS o SET available_at = $lease
    FROM (SELECT event_id FROM outbox_events
          WHERE published_at IS NULL AND available_at <= $now
          ORDER BY sequence LIMIT $n FOR UPDATE SKIP LOCKED) AS lote
    WHERE o.event_id = lote.event_id
    RETURNING ...
)
SELECT ... FROM reivindicados ORDER BY sequence
```

- **`SKIP LOCKED`**: dois publishers pegam lotes **diferentes**, sem um bloquear o
  outro e sem reivindicar o mesmo evento. Provado por teste com 2 concorrentes
  (4 eventos distintos, nenhum repetido).
- **lease**: se o processo morrer antes de publicar, o evento volta sozinho quando
  o prazo passar, em vez de ficar preso para sempre.

`MarkRetry` incrementa `attempts` **no banco** (`attempts + 1`), não a partir do
valor lido: duas instâncias falhando ao mesmo tempo contariam a mesma tentativa se
o número viesse do processo.

### [DECISÃO] A fila é lida por `sequence`, não por `occurred_at`

`occurred_at` é **quando o fato aconteceu**; não serve como critério de ordem:

1. os **dois** eventos da mesma operação compartilham o instante (o `now` é lido
   uma vez por operação), e em empate não existe ordem — o
   `WalletBalanceChanged` poderia ser publicado antes do
   `WagerTransactionProcessed` da mesma operação;
2. ordenar por relógio de processo supõe relógio monotônico, e ele não é (NTP,
   ajuste manual, VM pausada).

A coluna `sequence` (`BIGINT GENERATED ALWAYS AS IDENTITY`, migration 000003) é a
**ordem de inserção**, e é ela que vale como ordem dos efeitos: o lock da carteira
é mantido desde antes do `INSERT` até o commit, então todos os eventos da operação
A são inseridos antes de qualquer evento da operação B **da mesma carteira** — que
é exatamente o que o `MessageGroupId = walletId` da fila FIFO exige. A sequência é
global (não por carteira) porque a ordem que importa é dentro do grupo, e a ordem
global preserva a de cada grupo. Buracos deixados por rollback são normais
(`sequence` não é transacional) e não afetam a ordem.

### [DECISÃO] A CTE existe porque `RETURNING` não garante ordem

O `ORDER BY sequence` **dentro** da subconsulta decide **quais** linhas entram no
lote (com o `LIMIT`), mas o `RETURNING` do `UPDATE` não promete a ordem das linhas
que devolve — e `RETURNING ... ORDER BY` nem é aceito pela linguagem. Sem a CTE e
o `SELECT ... ORDER BY sequence` de fora, o lote chegava **embaralhado** e o
publisher publicaria fora de ordem mesmo com a coluna no lugar.

Quem mostrou foi um teste que falhava de vez em quando (`TestOrdemDaOutboxNaoDependeDoRelogio`,
~1 falha em 5 execuções): a primeira versão da correção trocou o `ORDER BY` e achou
que bastava. É o defeito 19 da §15.

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

## 9. Caso de uso da aplicação `[PRONTO]` — Fase 6

`internal/app/wagering`: `Process(ctx, Request)` — a operação externa ponta a
ponta, em UMA transação; `OpenWallet(ctx, OpenWalletRequest)` — a abertura interna
de carteira, no mesmo commit da carteira; e as leituras (`GetTransaction`,
`GetProviderTransaction`, `GetByProviderExternalID`, `GetWallet`, `ListLedger`) e
`Reconcile`, todas em transação **somente-leitura**.

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

### Abertura de carteira (`OpenWallet`)

A carteira e a abertura financeira entram no **mesmo commit**: `wallets`,
`wager_transactions` (a `OPENING`), `wallet_ledger_entries` e `outbox_events`. Uma
falha no evento desfaz a carteira também — provado por teste de integração com a
transação real e só o adaptador da outbox quebrado.

```
1. chave de idempotência → GetOpeningByIdempotencyKey (unicidade é da CHAVE, não do par provedor+chave)
2. (jogador, moeda) já tem carteira? → WALLET_CONFLICT
3. NewWallet com o saldo inicial e versão 1 → INSERT
4. saldo zero? → acabou aqui: sem OPENING, sem ledger, sem eventos
5. OPENING: wagering.New (sem provedor, sem ID externo, sem jogo, sem rodada) → INSERT
6. lançamento de crédito partindo de ZERO → Ledger.Insert
7. MarkProcessed(saldo inicial) + Save
8. dois eventos: Processed + WalletBalanceChanged (walletVersion = 1)
9. COMMIT
```

### [DECISÃO] A abertura NÃO chama `credit` na carteira

`wallet.NewWallet` já nasce com o saldo inicial. Chamar `Credit` faria o saldo
virar o **dobro** e a versão ir para **2** — contrariando o README §9, que fixa a
versão da carteira nessa abertura em `1`.

O que faltava não era movimentar a carteira, era **registrar de onde o saldo
veio**: `ledger.NewEntry(..., CREDIT, inicial, balanceBefore = 0.00, ...)`. A
reconciliação do enunciado (abertura `1000.00` + `BET` `25.00` → `975.00` com
`checkedEntries: 2`) só fecha assim.

### [DECISÃO] O replay da abertura devolve a carteira como ela está

O replay de uma **operação** devolve o resultado daquela operação (um fato
imutável: "aquela aposta debitou 25.00 e deixou 975.00"). O replay da **abertura**
devolve a carteira com o saldo e a versão de AGORA, com `IdempotentReplay: true`.

O motivo é a natureza do recurso: a carteira é mutável, e representá-la com o
saldo de ontem mentiria sobre o recurso que o cliente foi consultar. O que o
`IdempotentReplay` diz é que nada foi criado desta vez.

**Consequência conhecida:** abrir com saldo **zero** não cria `OPENING` (o README
manda não criar), então não há registro de idempotência para essa abertura. O
reenvio da mesma chave cai na unicidade da carteira e recebe `WALLET_CONFLICT` —
que é exatamente a resposta prescrita para carteira duplicada.

### Leituras e reconciliação

| Operação | O que faz |
| --- | --- |
| `GetTransaction(id)` | Leitura do serviço interno, por identificador |
| `GetProviderTransaction(providerID, id)` | Leitura do provedor: operação de OUTRO provedor responde **não encontrado** |
| `GetByProviderExternalID(providerID, externalID)` | O provedor faz parte da consulta — a linha alheia nem é lida |
| `GetWallet(id)` | A carteira |
| `ListLedger(walletID, limit, cursor)` | Página do ledger com cursor opaco (limite padrão 50, teto 200) |
| `Reconcile(walletID)` | Saldo gravado × saldo reconstruído do ledger, sem alterar nada |

### [DECISÃO] Operação de outro provedor responde "não existe", e não "proibido"

Devolver `403` para a operação de outro provedor confirmaria a **existência** dela
— e essa é justamente a informação que o isolamento entre provedores protege
(README §2 e §13). O provedor não pode nem descobrir que a operação do outro
existe. `GetByProviderExternalID` vai além: o provedor é parte da consulta SQL, e
a linha alheia não chega a ser lida.

Quem responde `403` é a **rota**: `GET /providers/:providerId/...` com um
`:providerId` diferente do claim do token é uma pergunta explícita sobre outro
provedor, e aí o contrato do A9 manda `403`.

### [DECISÃO] Identificador que não é UUID nunca chega ao banco

`wallets.id`, `wager_transactions.id` e `wallet_ledger_entries.wallet_id` são
colunas `UUID`. Um texto qualquer numa consulta a essas colunas faz o PostgreSQL
recusar com `22P02`, que a classificação do driver trata como falha **permanente**
— ou seja, **500**.

Um identificador malformado nunca é erro de servidor: ele não pode existir. O caso
de uso confere o formato **antes** de consultar (`ids.go`) e responde "não
encontrado" (nas leituras) ou entrada inválida (no `walletId` de uma requisição).
A checagem fica na aplicação, e não no adaptador do banco: lá o `22P02` também
poderia vir de uma inserção, e transformá-lo em "não encontrado" mascararia
defeito de gravação.

### [DECISÃO] `ListLedger` confere a carteira antes de listar

Sem essa conferência, uma carteira inexistente devolveria uma **página vazia**, e o
cliente não teria como distinguir "não tem lançamento" de "não tem carteira". Custa
uma consulta a mais na mesma transação de leitura.

### [DECISÃO] `Reconcile` roda em transação somente-leitura, e isso não é detalhe

As três leituras (saldo, soma e contagem) acontecem numa **única** transação
`REPEATABLE READ` somente-leitura:

- **uma visão só** — fora dela, uma operação confirmada entre a leitura do saldo e
  a soma do ledger produziria uma divergência que **nunca existiu**, o clássico
  falso positivo que faz alguém procurar dinheiro que está no lugar;
- **somente-leitura** — é a garantia de que a conferência não altera saldo, nem por
  acidente nem por bug. Há teste de integração que prova que uma escrita nesse modo
  é recusada pelo PostgreSQL.

`difference = stored − calculated` e pode ser **negativo** (saldo gravado menor que
o reconstruído); o sinal é informação, não ruído. A resposta, o log e a métrica de
divergência ficam na borda e na Fase 11 — o caso de uso devolve o resultado.

### O que a Fase 6 não fez (e onde entra)

- **métricas de negócio** (`wager_operations_total`, `wager_idempotent_replays_total`,
  divergências de reconciliação): Fase 11, para o caso de uso não nascer acoplado ao
  Prometheus;
- **publicação dos eventos**: é do worker (Fase 9) — aqui os eventos **entram** na
  outbox, na mesma transação;
- **consumo por SQS**: Fase 8 — implementado em `app/inbox` + `internal/consumer`, compartilhando ESTE caso de uso com ganchos transacionais (§11).

---

## 10. HTTP e autenticação `[PRONTO]` — Fase 7

### As rotas do enunciado, e quem pode cada uma

| Rota | Role | Por que essa role |
| --- | --- | --- |
| `POST /wallets` | `internal` | Abrir carteira cria saldo a partir do zero: é dinheiro entrando no sistema, e nenhum provedor tem esse direito (README §2) |
| `GET /wallets/{walletId}` | `internal` | Operação de carteira é restrita ao serviço interno |
| `GET /wallets/{walletId}/ledger` | `internal` | idem |
| `POST /wallets/{walletId}/reconciliation` | `internal` | idem |
| `GET /wagering/transactions/{transactionId}` | `internal` | O `{transactionId}` é o identificador **interno** (UUID nosso). O provedor não o conhece |
| `POST /wagering/transactions` | `provider` | A operação do provedor, com `Idempotency-Key` obrigatório |
| `GET /providers/{providerId}/wagering/transactions/{externalTransactionId}` | `provider` | O provedor consulta pela identidade DELE |
| `GET /health/live`, `/health/ready`, `/metrics` | público | O orquestrador precisa consultá-los sem credencial de negócio |

### [DECISÃO] O `providerId` vem do TOKEN, nunca do corpo

É a regra do README §2 ("a identidade autenticada deve determinar o `providerId`
autorizado") e a razão de o corpo ser apenas **confirmatório**:

```
corpo.providerId preenchido e diferente do claim  → 403 (nem chega ao caso de uso)
corpo.providerId igual ao claim                   → prossegue
corpo.providerId ausente                          → prossegue com o claim
```

Sem isso, qualquer token válido — de qualquer provedor — operaria a carteira de
qualquer outro. É o eliminatório "acesso não autorizado a operações ou
transações" (§14). Há teste de unidade que prova que o caso de uso **não é
chamado** quando o provedor diverge.

### [DECISÃO] Operação de outro provedor responde "não existe", e o 403 é do PATH

As duas checagens existem, e cada uma responde uma pergunta diferente:

| Pergunta | Rota | Resposta |
| --- | --- | --- |
| "me dê a operação do **provider-b**" (explícito no path) | `GET /providers/provider-b/...` com token de `provider-a` | **403** — é uma pergunta explícita sobre outro provedor, e o A9 manda 403 |
| "existe a operação `txn-1`?" (com o MEU token) | `GET /providers/provider-a/wagering/transactions/txn-1`, existindo só para `provider-b` | **404** — devolver 403 confirmaria a **existência** de uma operação alheia |

O `GetByProviderExternalID` vai além: o provedor entra na **consulta SQL**, então
a linha alheia nem é lida. O teste de integração confere que a resposta de 404 não
menciona o outro provedor.

### [DECISÃO] Rejeição de negócio é DESFECHO no corpo; erro de transporte é envelope

```
422  {"transactionId":"...","status":"REJECTED","balance":null,"failureCode":"INSUFFICIENT_FUNDS"}
     ^ rejeição: a operação EXISTE, foi decidida e é auditável

400/401/403/404/409/503/500  {"error":{"code":"...","message":"...","failureCode":"..."}}
     ^ o que impede a operação de existir
```

**Por que não usar o envelope em tudo:** a rejeição é um fato do domínio, com
identificador próprio. O cliente precisa do `transactionId` para consultar e
auditar — e o `failureCode` é o que ele usa para decidir o que fazer (§7 do
enunciado). Estado, por outro lado, é o que o envelope carrega. As duas formas
continuam distinguíveis pelo contrato, que é a exigência da §9.

### A tabela desfecho → status

| Desfecho | Status | Corpo |
| --- | --- | --- |
| `PROCESSED`, primeira vez | `201` | desfecho com `balance` |
| `PROCESSED`, replay | `200` | desfecho com o saldo **original** e `idempotentReplay: true` |
| `PENDING_REFERENCE` | `202` | desfecho, `balance: null`, `failureCode: null` |
| `REJECTED` | `422` | desfecho com `failureCode` |
| `FAILED` | `503` | desfecho (falha permanente de infraestrutura registrada) |
| `PENDING` durável ou estado desconhecido | `500` | envelope — a borda não inventa sucesso para um desfecho que não conhece |

### A tabela erro → status

| Erro | Status |
| --- | --- |
| `ports.IsConflict` / `ErrIdempotencyConflict` / `ErrWalletAlreadyExists` | `409` |
| `ports.IsNotFound` / `ErrWalletNotFound` / `ErrTransactionNotFound` | `404` |
| Códigos de entrada (`INVALID_REQUEST`, `IDEMPOTENCY_KEY_REQUIRED`, `INVALID_AMOUNT`, `REFERENCE_REQUIRED`, `INVALID_TRANSACTION`, `CURRENCY_MISMATCH`, cursor inválido) | `400` + `failureCode` |
| `OPERATION_NOT_ALLOWED` (`OPENING` de fora) | `403` |
| `ports.IsRetryable` (transitório esgotado: `55P03`, deadlock, conexão) | `503` + `Retry-After` |
| Qualquer outro | `500`, sem vazar driver nem stack |

**A regra que nunca pode ser quebrada:** falha de **infraestrutura** nunca vira
`4xx`. Se um `55P03` (lock timeout) saísse como `400`, o provedor concluiria que o
pedido dele está errado e **não repetiria** — uma disputa momentânea de lock
viraria uma aposta perdida. Há teste unitário que passa quatro erros de
infraestrutura pela classificação e exige que nenhum vire `4xx`.

### Autenticação: OIDC sem dependência externa

`internal/infra/auth` valida o token com a **biblioteca padrão**:

```
discovery (/.well-known/openid-configuration) ──► jwks_uri ──► JWKS (cache + TTL)
                                                                   │
token ──► 3 partes? ──► base64url? ──► alg == RS256? ──► kid existe?
                                                                   │
                              rsa.VerifyPKCS1v15(SHA-256) ──► iss? aud? exp? nbf?
                                                                   │
                                                              Identity
```

**Por que não uma biblioteca de terceiros:** um token é base64url + JSON +
assinatura RSA, e a biblioteca padrão tem as três coisas. A troca é 1 dependência
(mais as transitivas) por ~300 linhas de regra explícita, testável e sem surpresa.

### [DECISÃO] O algoritmo é decidido AQUI, e não pelo header do token

Confiar no campo `alg` é a vulnerabilidade clássica de JWT, e são três ataques
concretos:

| Ataque | O que ele explora | Nossa defesa |
| --- | --- | --- |
| `alg: "none"` | remover a assinatura e o serviço "validar" | `alg` é comparado com a **constante** `RS256` **antes** de qualquer criptografia |
| `alg: "HS256"` | assinar com HMAC usando a **chave pública** (que é pública) como segredo | idem: HS256 nunca é aceito, então a chave pública nunca é usada como segredo |
| `jku`/`x5u` no header | apontar para chave de outro host | o header tem campos opcionais ignorados; a origem das chaves é o discovery **configurado**, e o cliente HTTP não segue redirecionamento |

Além disso: **`kid` é obrigatório**. Sem ele não há como saber qual chave confere a
assinatura, e "tentar todas" equivaleria a aceitar qualquer chave publicada para
qualquer token. E o `issuer` anunciado no discovery precisa **bater** com o
configurado — um DNS trocado no meio do caminho não pode fazer o serviço aceitar
tokens de terceiro.

### [DECISÃO] Duas classes de erro: token inválido (401) e IdP indisponível (503)

| Situação | Status | Por quê |
| --- | --- | --- |
| token ausente, malformado, expirado, assinatura inválida, `iss`/`aud` errados, `alg` errado | `401` | a credencial não serve; repetir não resolve |
| discovery ou JWKS inacessível, resposta ilegível, nenhuma chave utilizável | `503` + `Retry-After` | o token pode estar **perfeito**; o serviço é que não consegue conferi-lo |

Sem a distinção, uma queda do Keycloak faria o cliente descartar um token válido e
o incidente seria diagnosticado errado. A resposta **não** diz qual foi o
problema: "a chave `kid` X não existe" ou "o `exp` passou 3 minutos" ajudaria quem
está sondando; o detalhe vai para o log, com o `correlationId`.

### [DECISÃO] Cache de JWKS com TTL **e** rebusca por `kid` desconhecido

| Defesa | O que cobre |
| --- | --- |
| TTL (`OIDC_JWKS_CACHE_TTL`, padrão 15m) | chave **rotacionada** passa a ser aceita sem reiniciar o serviço; e o IdP não é consultado a cada requisição |
| rebusca quando o `kid` não está no cache | o intervalo entre a rotação no IdP e o fim do TTL seria uma janela de **401 em massa** para tokens válidos |
| rebusca única quando a assinatura não confere | uma chave republicada com o mesmo `kid` é aceita; um token forjado continua recusado |

O **cache vencido com o IdP fora** serve a chave antiga: preferir a chave
conhecida a derrubar o serviço é a escolha certa, porque a assinatura ainda será
conferida com ela. Já o **readiness** fura o cache de propósito: um readiness que
consulta cache responderia `UP` durante todo o TTL mesmo com o Keycloak caído.

### [DECISÃO] Autorizar é uma decisão diferente de autenticar

A porta `httpapi.Autenticador` tem três métodos, e a separação é o que faz os
status serem os corretos:

| Método | Pergunta | Falha |
| --- | --- | --- |
| `Middleware` | "quem é você?" | `401` (sem token/inválido) ou `503` (IdP fora) |
| `RequireRole` | "você pode esta operação?" | `403` — e `401` se **não houver identidade** (composição errada: falha fechado) |
| `RequireProvider` | "você é um provedor?" | `403` sem `provider_id` |

Com um método só, "token ausente" e "token sem permissão" virariam o mesmo código,
e o cliente não saberia se deve trocar o token ou pedir acesso.

### [DECISÃO] A interface fica no consumidor, e a implementação na infraestrutura

`httpapi` declara `Autenticador` e `WageringService`; `internal/infra/auth`
implementa a primeira e `internal/app/wagering` a segunda. Consequências:

- a camada HTTP **não importa** OIDC, e o pacote de autenticação **não importa**
  `chi` — o acoplamento é por estrutura (interface implícita), não por import;
- o teste do handler roda com um autenticador que devolve a identidade que o teste
  quiser, sem chave, sem JWKS e sem rede;
- `internal/app` é o único lugar que faz a conversão tipo concreto → interface.

### [DECISÃO] Rota de negócio nunca é registrada sem autenticador

`Handlers.RegistrarRotas` devolve sem montar nada quando `auth == nil`. Registrar
um endpoint financeiro sem autenticação é o eliminatório do §14; falhar **fechado**
(404, e não 200) é a única resposta aceitável para uma composição errada.

### [DECISÃO] A chave de idempotência da abertura de carteira

O README §9 define `Idempotency-Key` para `POST /wagering/transactions`, e **não**
para `POST /wallets`. Mas abrir carteira é a operação mais cara de duplicar
(crédito inicial), e o caso de uso exige a chave. A regra adotada:

```
cliente mandou Idempotency-Key?  → usa a dele, COMO VEIO (nunca substitui)
não mandou?                      → deriva "abertura:{playerId}:{moeda}"
```

Duas consequências, as duas desejáveis: reenviar o **mesmo** pedido devolve a
carteira com `idempotentReplay: true` (em vez de um conflito seco), e reenviar o
mesmo jogador com **outro** saldo é conflito de conteúdo — que é a verdade, porque
a carteira já foi aberta com outro valor. Com saldo inicial **zero** não existe
`OPENING` (o README manda), então não há registro de idempotência: o reenvio cai na
unicidade de `(playerId, currency)` e recebe `WALLET_CONFLICT`, que é a resposta
prescrita para carteira duplicada.

### Middlewares: a ordem, e a correção desta fase (P2)

```
RequestID → RealIP → AccessLog → Recoverer → Timeout → MaxBody
```

`AccessLog` vem **antes** (mais externo) que o `Recoverer` de propósito: o log de
acesso é dono do `statusRecorder`, e o `Recoverer` escreve o `500` **depois** que o
handler entrou em pânico. Na ordem antiga, o `statusRecorder` já estava fora do
caminho e o log registrava **200** para uma requisição que respondeu **500** — o
log mentia exatamente no caso em que ele é mais necessário, e a métrica de status
herdava o número errado.

**`chimw.RequestSize` foi removido**: com limite `0` ele recusa **qualquer** corpo
("request body too large"). Não era no-op, e nenhum teste anterior enviava corpo —
o defeito dormia desde a Fase 0. O limite real é o `MaxBodyMiddleware`, que vem de
`HTTP_MAX_BODY_BYTES`.

### Composição Fx desta fase

```
config    → Config validada, slog, métricas
infra     → *postgres.Pool (com Close no Lifecycle), ports.UnitOfWork, *auth.Verifier,
            e o health checker do Postgres no grupo health_checkers
usecase   → ports.Clock, idgen.Generator, *appwagering.Service
auth      → *auth.Autenticador (+ health checker do IdP no mesmo grupo)
httpapi   → *Routes, *http.Server, *Handlers, registro das rotas, runHTTPServer
```

Três detalhes que o Fx exige e que custaram tempo:

1. **`fx.Provide` recebe construtor, não valor.** `clock.System{}` é valor; e como
   o caso de uso depende das **interfaces** (`ports.Clock`, `idgen.Generator`) e o
   Fx casa por tipo exato, os providers publicam as duas formas de cada valor;
2. **grupo é `fx.Annotate(..., fx.ResultTags)`**, não opção de `Provide`; e um
   parâmetro que é struct `fx.In` **não** pode ser anotado com `fx.ParamTags` — o
   `group` pertence ao campo;
3. **o pool recebe `context.Background()` com prazo**, e não o contexto de start: o
   `pgxpool` é recurso de vida longa, e o contexto de start é cancelado quando o
   boot termina — passá-lo derrubaria o pool no primeiro `Start`.

### `StopTimeout` vem da configuração (P7)

O prazo de encerramento é uma **opção** do `fx.New` (`fx.StopTimeout`), avaliada na
construção: nenhum provider do grafo consegue influenciá-la. Por isso `Run` lê a
configuração **antes** de montar o grafo, e `HTTP_STOP_TIMEOUT` é validado
(`config.Validate`) junto com a regra de que ele precisa **cobrir**
`HTTP_SHUTDOWN_TIMEOUT` — com a ordem invertida, o Fx mataria o servidor no meio da
drenagem das requisições em andamento.

### Readiness: Postgres e IdP (P5)

```
GET /health/ready → {"status":"UP","dependencies":{"oidc":"UP","postgres":"UP"}}
```

- **Postgres**: `Ping` com prazo próprio de 2s;
- **IdP**: discovery **sem cache**, com prazo de 3s. Sem ele no readiness, uma
  instância que não alcança o Keycloak responderia `200 UP` e continuaria
  recebendo tráfego para então devolver `503` em toda rota de negócio.

Liveness **não** verifica dependência: se verificasse, uma queda momentânea do
Postgres faria o orquestrador reiniciar em loop um processo saudável.

### Observabilidade da borda

**Um logger por processo, e o formato é decisão da configuração:** `APP_ENV=local`
usa `slog.NewTextHandler` (legível no terminal) e qualquer outro ambiente usa
`NewJSONHandler`. Antes da correção do defeito 27 havia **dois loggers** no mesmo
processo — o boot em JSON e o log de acesso em texto —, o que contrariava o
README §12 justamente no log que mais se consulta num incidente.

| Sinal | Onde |
| --- | --- |
| `http_request` (método, path, status, bytes, duração, `idempotencyKey`) | `AccessLog` |
| `http_error` (path, status, `code`, `failureCode`) | `WriteError` |
| `token_recusado` / `idp_indisponivel` | middleware de autenticação |
| `provedor_divergente_do_token` / `consulta_de_outro_provedor` | handlers |
| `operacao_concluida` / `operacao_rejeitada` (`transactionId`, `providerId`, `status`, `failureCode`, `correlationId`) | handler de operação |
| `reconciliacao_divergente` (unidades mínimas em texto) | handler de reconciliação |
| `wager_operations_total{kind,status}`, `wager_operation_duration_seconds`, `wager_idempotent_replays_total`, `wager_reconciliation_divergences_total` | métricas Prometheus |

Labels são de **cardinalidade baixa** (tipo e status): usar `walletId` ou
`transactionId` como label explodiria a série temporal do Prometheus. O valor
monetário **não** entra no log (payload financeiro completo é proibido pelo §12).

---

## 11. Consumo do SQS `[PRONTO]` — Fase 8

### Uma regra de negócio, duas portas de entrada

```
HTTP  ──► internal/httpapi ──┐
                             ├──► app/wagering.Process (MESMA transação, mesmas garantias)
SQS   ──► internal/consumer ─┘        ▲
              │                       │
              └──► app/inbox ─────────┘   (inbox + dedup + tradução)
                        ▲
              internal/infra/sqs  (protocolo do broker)
```

O consumidor chama `ProcessWithHooks` — a **mesma** função que o HTTP usa por
dentro — com ganchos que registram a inbox **dentro da transação** da operação.
Isso é o que o README §10 exige ("HTTP e SQS devem compartilhar o caso de uso e as
garantias de idempotência financeira"): não é "duas implementações parecidas", é
o mesmo código com um gancho a mais.

### [DECISÃO] Ganchos transacionais, e não uma transação em volta

```
1. uow.Run abre a transação
2. OnBegin   → SELECT inbox por (consumer, messageId)
                 já PROCESSED/REJECTED com o MESMO hash → ErrIdempotentSkip (replay)
                 MESMO id com hash DIFERENTE           → conflito (permanente)
                 não existe                            → INSERT (RECEIVED)
3. ...todo o trabalho do domínio (lock, saldo, ledger, eventos)...
4. OnCommit  → UPDATE da inbox (PROCESSED / REJECTED)
5. COMMIT
```

**Por que assim, e não "grava na inbox, depois processa":** entre as duas
transações existe uma janela em que uma queda deixa o débito feito **sem**
registro (ou o registro sem o débito). O README §6.5 pede explicitamente que o
registro da inbox e a conclusão durável **compartilhem a transação SQL** das
alterações de domínio, do ledger e dos eventos — e a única forma de garantir isso
é o registro acontecer dentro do `uow.Run` da operação.

**O `Process` do HTTP não paga nada por isso:** `Process(ctx, req)` é
`ProcessWithHooks(ctx, req, nil)`.

### [DECISÃO] A identidade durável da mensagem é o `MessageId` do broker

O README §10 diz "use o `messageId` do envelope como identidade durável da
mensagem para o consumidor". A implementação adota o `MessageId` **do broker**, com
o campo do envelope **opcional** e conferido contra ele.

| | Envelope (`messageId`) | Broker (`MessageId`) |
| --- | --- | --- |
| Quem atribui | o produtor | o SQS, no `SendMessage` |
| Sobrevive a reenvio? | sim (é o mesmo valor) | **não** (cada envio gera outro) |
| O produtor consegue preencher? | só se INVENTAR um segundo identificador | — |

O produtor **não tem como conhecer** o `MessageId` antes de enviar: ele existe
apenas na resposta do `SendMessage`. Exigir o campo no envelope obrigaria a manter
dois identificadores para a mesma mensagem, e aí eles podem divergir — que é
exatamente o que aconteceu quando a primeira versão exigia a coincidência: o
consumo recusou **toda** mensagem (defeito 28 da §15).

A régua final: **o campo do envelope, quando presente, precisa coincidir** com o
da entrega; divergência é mensagem inválida (permanente → DLQ); e a identidade
registrada na inbox é a da entrega.

### [DECISÃO] O que é transitório e o que é permanente

| Situação | Destino | Por quê |
| --- | --- | --- |
| operação concluída (ou replay) | **remove** | commit feito; a mensagem não tem mais o que fazer |
| rejeição de negócio (`REJECTED`) | **remove** | terminal e auditável: reenviar não muda a decisão (README §10) |
| referência pendente (`PENDING_REFERENCE`) | **remove** | o estado está durável no banco; o worker de referências assume (Fase 9) |
| falha transitória (banco, lock, conexão) | **adia** | repetir resolve |
| conflito de unicidade | **adia** | no nosso schema, `23505` significa "alguém chegou primeiro" — a segunda passada decide |
| mensagem inválida (corpo ilegível, campo ausente, `OPENING` de fora) | **DLQ** | repetir não conserta |
| corpo divergente para o mesmo registro | **DLQ** | não é reentrega: é mensagem forjada ou bug de produtor |
| recusa por ENTRADA (nada foi gravado) | **DLQ** | a mesma mensagem reenviada continuaria inválida |
| erro **desconhecido** | **adia** | a DLQ é o fim da linha; o `maxReceiveCount` é o teto |

A régua é conservadora de propósito no último caso: mandar para a DLQ algo que
talvez funcionasse na segunda tentativa **perde trabalho**, enquanto tentar de
novo custa tempo — e o broker já limita as tentativas.

### [DECISÃO] DLQ explícita, além do redrive

O `RedrivePolicy` da fila cobre "tentou demais". Mas a mensagem que **nunca** vai
funcionar ficaria sendo recebida e devolvida até estourar o `maxReceiveCount` — e
numa fila FIFO isso é pior do que parece: mensagens do mesmo `MessageGroupId` são
entregues **em ordem**, então uma mensagem envenenada **bloqueia o grupo inteiro**
enquanto gira.

O consumidor manda para a DLQ explicitamente (`Send` + `Delete`) na primeira
detecção de erro permanente, com o `MessageGroupId` preservado e um
`MessageDeduplicationId` derivado do identificador da mensagem (a DLQ também é
FIFO). O redrive continua sendo a rede de segurança para o que escapou.

### [DECISÃO] O retry é `ChangeMessageVisibility` com backoff exponencial

Nada é removido da fila num retry: a mensagem fica **invisível** pelo prazo
calculado e volta sozinha.

```
base * 2^(recebimentos-1), limitado a MaxBackoff, com jitter de 0 a 25%
SUBTRAÍDO (nunca acima do teto)
```

- **exponencial**: um SQS fora do ar não deve ser martelado a cada segundo;
- **teto**: sem ele, a décima tentativa adiaria a mensagem por horas e a fila
  pareceria travada;
- **jitter**: sem ele, todas as instâncias que falharam no mesmo instante voltariam
  no mesmo instante.

O jitter é subtraído **dentro** do teto. A primeira versão somava depois do limite
e ultrapassava o `MaxBackoff` — o teste de teto pegou (defeito 29).

### [DECISÃO] `context.WithoutCancel` no processamento, e o encerramento

Quando o SIGTERM chega, o `Run` cancela o contexto de **busca**. Se o
processamento usasse o mesmo contexto, a transação em andamento seria **abortada
no meio** — o oposto do que o encerramento quer. O processamento roda com
`context.WithoutCancel`, e o teto de tempo é imposto pelo encerramento:

```
1. o contexto cancela → o Receive volta
2. o laço SAI antes de processar o próximo lote   ← "pare de buscar trabalho"
3. o trabalho em voo é aguardado por SQS_SHUTDOWN_GRACE
4. o que sobrou tem a visibilidade LIBERADA (ChangeMessageVisibility 0)
   → reentrega imediata, em vez de esperar o visibility timeout inteiro
```

**O `sync.Once` por entrega** é o que impede a mesma mensagem de ser apagada **e**
devolvida: quem decide o destino primeiro vence, o outro não faz nada. Sem ele, o
encerramento poderia liberar a visibilidade de uma mensagem que a goroutine acabou
de remover — e ela reapareceria depois de já ter sido tratada.

### [DECISÃO] `attempts` incrementado no banco (migration 000004)

`attempts = attempts + 1` em SQL, não a partir do valor lido: duas instâncias
recebendo a mesma mensagem ao mesmo tempo gravariam o **mesmo** valor final em vez
de somar as duas entregas. Mesma decisão do `MarkRetry` da outbox.

O contador é do CONSUMIDOR (por `consumer_name` + `message_id`), e existe porque o
`ApproximateReceiveCount` do broker **se perde** quando a mensagem é removida ou
vai para a DLQ: sem ele, investigar uma mensagem morta não diria se ela foi
recebida duas ou vinte vezes.

### Filas, FIFO e os dois identificadores de grupo

| Item | Valor | Por quê |
| --- | --- | --- |
| `MessageGroupId` (envio pelo provedor) | a **carteira** | ordem por carteira; duas operações da mesma carteira não podem ser invertidas |
| `MessageDeduplicationId` (envio) | explícito, do produtor | `ContentBasedDeduplication=false`: a deduplicação que vale é a NOSSA (chave financeira + inbox), não a do broker |
| `MessageGroupId` (DLQ) | preservado | a DLQ também é FIFO |
| `MessageDeduplicationId` (DLQ) | `dlq-<messageId>` | reenvio do mesmo lixo não cria uma segunda entrada |
| `VisibilityTimeout` | 60s (fila) / configurável | o processamento inclui transação no Postgres |
| `WaitTimeSeconds` | 20s | long polling: sem ele, cada chamada vazia volta na hora e o consumidor martela a API |

### Concorrência

O semáforo (`SQS_CONSUMER_CONCURRENCY`, padrão 8) é o teto de trabalho simultâneo:
sem ele, lotes de 10 com N instâncias estourariam o pool de conexões do banco — o
consumidor viraria o gargalo do próprio serviço. A coordenação entre entradas
(HTTP × SQS × instâncias) continua sendo o **lock da carteira** e as constraints,
não o consumidor.

### Readiness do SQS (fecha a P5)

`GET /health/ready` consulta a **fila específica** (`GetQueueAttributes`), e não
"o SQS responde": com a fila ausente — ambiente mal provisionado — o serviço não
consumiria nada, e responder `UP` seria falso.

### Observabilidade do consumo

| Sinal | Quando |
| --- | --- |
| `sqs_mensagem_processada` (messageId, status, failureCode, replay, receiveCount, duração) | commit feito; a mensagem foi removida |
| `sqs_mensagem_adiada` (motivo, receiveCount, backoff) | retry por visibilidade |
| `sqs_mensagem_para_dlq` (messageId, receiveCount, erro) | erro permanente |
| `sqs_mensagem_classificada` (DEBUG) | desfecho da classificação, para diagnóstico de bancada |
| `sqs_receive_falhou`, `sqs_delete_falhou`, `sqs_visibility_falhou`, `sqs_dlq_envio_falhou` | falhas do broker |
| `sqs_encerramento_*` | parada: trabalho concluído, prazo esgotado, visibilidade liberada |
| `wager_inbox_duplicates_total`, `wager_retries_total{stage="sqs_consume"}`, `wager_dlq_messages_total` | métricas Prometheus |

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
| Mensagem reentregue pelo SQS | inbox recusa a segunda (por `consumer_name` + identificador da mensagem), com o hash do corpo distinguindo reentrega de conflito | `[PRONTO]` |
| Referência que nunca chega | a mensagem de ENTRADA é concluída e o `PENDING_REFERENCE` fica durável; o TTL esgota no worker (Fase 9) → `REJECTED REFERENCE_NOT_FOUND` | `[PARCIAL]` |
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
| 17 | 6b | O `CHECK ids_not_blank` exigia `game_id` e `round_id` de **toda** linha, inclusive `OPENING` | abrir carteira obrigaria a INVENTAR jogo e rodada, gravando dado falso no histórico financeiro | migration 000002: a exigência passou a valer só para operação externa; o domínio acompanha |
| 18 | 6c | **Identificador malformado em coluna `UUID` virava erro permanente (500)** | `GET /wallets/abc` responderia 500 em vez de "não encontrado" (`22P02 invalid input syntax for type uuid`), e uma mensagem de SQS com `walletId` inválido derrubaria o processamento como falha de infraestrutura | guarda de formato antes da consulta (`ids.go`), com "não encontrado" na leitura e entrada inválida no `walletId` da requisição |
| 19 | 6d | **`RETURNING` não garante a ordem das linhas que devolve** | a fila era lida por `occurred_at`; ao trocar por `sequence`, o `ORDER BY` da subconsulta passou a decidir só QUAIS linhas entram no lote — o lote chegava embaralhado (1 falha em 5 execuções do teste de ordem) e o publisher publicaria fora de ordem | o `UPDATE` virou uma CTE e o `SELECT` de fora ordena por `sequence` |
| 20 | 7 | **`chimw.RequestSize(0)` no roteador recusava TODO corpo** | `POST /wallets` respondia `400 {"code":"INVALID_REQUEST","message":"JSON inválido: http: request body too large"}` para um corpo de 90 bytes com limite de 1 MiB. Dormia desde a Fase 0 porque **nenhuma rota anterior recebia corpo** | middleware removido; o limite real é o `MaxBodyMiddleware`, vindo de `HTTP_MAX_BODY_BYTES` |
| 21 | 7 | **`AccessLog` por dentro do `Recoverer` (P2)**: o log registrava 200 para um 500 | o `statusRecorder` do log ficava fora do caminho quando o `Recoverer` escrevia a resposta de panic — o log (e a métrica de status) mentia exatamente no caso mais importante | `AccessLog` passou a envolver o `Recoverer` |
| 22 | 7 | `fx.Provide(clock.System{})` — valor, não construtor; e o caso de uso depende das **interfaces** | `must provide constructor function, got {} (type clock.System)`; o Fx casa por tipo exato, então o valor também não satisfaria `ports.Clock` | providers que publicam as duas formas (`clock.System`/`ports.Clock`, `idgen.Generator`) |
| 23 | 7 | Grupo do Fx usado como opção de `Provide` | `must provide constructor function, got {[group:...]} (type fx.resultTagsAnnotation)`, e depois `fx.In structs cannot be annotated with fx.ParamTags` | `fx.Annotate(fn, fx.ResultTags(...))` no provider, e `group` no **campo** de uma struct `fx.In` |
| 24 | 7 | `context.Context` exigido por `postgres.NewPool` não existe no grafo | `missing type: context.Context` | provider que cria `context.Background()` com prazo — **não** o contexto de start, que é cancelado no fim do boot e derrubaria o pool |
| 25 | 7 | `-health-check` não existia, e a primeira versão exigia a configuração inteira | o healthcheck do compose apontava para uma flag inexistente (contêiner `unhealthy`); e um processo curto de verificação não deve depender de `DATABASE_URL`/`OIDC_ISSUER` para dar veredito sobre o **serviço** | modo healthcheck que não carrega a config e consulta o próprio `/health/ready` |
| 26 | 7 | **Três asserções de teste erradas** (régua, não código) | (a) esperava envelope de erro na rejeição 422; (b) usava `cursor=%%%`, que é texto literal válido e devolvia página vazia; (c) tratava a mesma identidade externa em dois provedores como conflito, quando a identidade financeira é o PAR `(providerId, externalTransactionId)` | as três corrigidas, com o motivo escrito no próprio teste |
| 27 | 7 | **Dois loggers no mesmo processo**: o log de acesso saía em TEXTO e o boot em JSON | `app.Run` montava o próprio `slog.NewJSONHandler` enquanto o resto do grafo recebia `observability.NewLogger(cfg)` (texto em `local`). O `/health/live` respondia `200` mas o `http_request` saía fora do formato estruturado que o README §12 exige — e o defeito **não aparecia em teste nenhum**, porque nenhum olhava o stdout do processo | um logger por processo, vindo da config; `local` = texto, produção = JSON (provado nos dois modos) |
| 28 | 8 | **As filas do LocalStack NUNCA eram criadas**: o `init-queues.sh` não tinha shebang nem bit de execução no git | `docker logs jungleg-localstack` trazia, nas TRÊS subidas, `stage=READY, state=ERROR: [Errno 8] Exec format error`, e `list-queues` respondia vazio. Nada quebrava porque nenhum teste usava SQS — a evidência da Fase 4 dizia "filas provisionadas" com base no script **existir**. O LocalStack roda os scripts de `init/ready.d` **executando o arquivo**, não via `sh arquivo` | shebang `#!/bin/sh` + `git update-index --chmod=+x`, provado reiniciando o container (as filas passaram a ser criadas no stage READY) |
| 29 | 8 | **A inbox não tinha `attempts`** (migration 000004) | o PLANO previa "o attempts da inbox incrementa" e a tabela da Fase 4 não tinha a coluna; o `ApproximateReceiveCount` do broker **se perde** quando a mensagem é removida ou vai para a DLQ, então investigar uma mensagem morta não diria se ela foi recebida duas ou vinte vezes | coluna com CHECK de não-negatividade, índice parcial de diagnóstico e `CountAttempt` incrementando **em SQL** |
| 30 | 8 | **A identidade da mensagem no envelope não pode ser a do consumo** | com o `messageId` do envelope exigido e conferido contra o da entrega, o consumo recusou TODA mensagem: o produtor não tem como conhecer o `MessageId` antes do `SendMessage` — ele só existe na resposta do envio | identidade durável = `MessageId` **do broker**; campo do envelope **opcional** e conferido quando presente (divergência ⇒ mensagem inválida) |
| 31 | 8 | **`return` que abortava o parsing** (introduzido na correção do 30) | `return operacao{}, nil` no caminho do envelope sem `messageId` devolvia uma requisição VAZIA com erro `nil`; o sintoma era `IDEMPOTENCY_KEY_REQUIRED` para um corpo que TINHA a chave. Localizado por um teste de unidade que roda o **corpo exato** entregue pelo broker (`payload_real_test.go`): o teste de integração disse **que** algo estava errado, o unitário com o corpo real disse **onde** | o `messageId` do envelope é opcional e o parsing segue |
| 32 | 8 | **Jitter do backoff somado DEPOIS do teto** | o atraso ultrapassava o `MaxBackoff` (62s com teto de 60s), e o limite de `VisibilityTimeout` do SQS voltaria a ser alcançável por acidente | jitter **subtraído dentro** do teto (0 a 25% para baixo) |
| 33 | 8 | **Com os DEFAULTS, ligar o consumidor faz a aplicação NÃO SUBIR** | `HTTP_STOP_TIMEOUT` padrão era 25s e a validação exige cobrir 20s (servidor) + 15s (consumidor): o default **violava a própria regra**, e o operador recebia "configuração inválida" num ambiente correto | default de 40s (e `.env.example` alinhado), com dois testes de config travando os dois lados da regra |
| 34 | 8 | **A métrica de negócio não era emitida no caminho SQS** | `wager_operations_total` não existia para operações vindas da fila: o caminho PRINCIPAL do serviço ficava invisível no painel (a operação aparecia no banco e em lugar nenhum mais) | o consumidor emite a mesma métrica da borda HTTP, com `kind` lido de forma tolerante (`unknown` em vez de perder a contagem) |
| 35 | 8 | **O contador durável de entregas nunca incrementava** | `attempts` ficava em 0 para sempre: o caminho da reentrega — o único que sobrevive quando o broker não é mais consultado — não tocava no contador | inserção grava 1 (normalizado no repositório) e a reentrega faz `attempts + 1` no SQL; `TestProcessarReentregaNaoReaplica` afirma 2 |
| 36 | 8 | **`INSERT` ganhou coluna sem os placeholders** (introduzido na correção do 35) | `mismatched param and argument count` nos testes de integração do repositório | SQL e argumentos alinhados — pego pelos testes de `internal/infra/postgres`, que rodam no mesmo comando |

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
| O dublê em memória não prova lock nem concorrência | Ele tem semântica de transação (cópia do estado, troca só no fim sem erro), mas não tem `FOR UPDATE`. Lock, lost update e disputa entre processos são provados contra o PostgreSQL real |
| `next_resolution_attempt_at` fica preenchido em reversão já `PROCESSED` | Efeito de usar `MarkPendingReference` + `ResolveReference`, que é o único caminho que o agregado oferece para vincular a referência interna. A coluna só é lida para linhas `PENDING_REFERENCE`, então o valor é inerte |
| `ListLedger` faz uma consulta a mais por página | Confere a existência da carteira antes de listar (para não devolver página vazia ambígua). É uma leitura a mais dentro da mesma transação de leitura |
| `Reconcile` não emite métrica nem log de divergência | **Resolvido na Fase 7 na borda**: o handler emite `wager_reconciliation_divergences_total` e o log `reconciliacao_divergente`. O caso de uso continua sem conhecer Prometheus (de propósito) |
| `ARCHITECTURE.md` sem as seções de workers | As seções 10 (HTTP) e 11 (SQS) foram fechadas nas Fases 7 e 8; o publisher da outbox e o worker de referências entram na Fase 9 |
| Sem introspecção de token (RFC 7662) na autenticação | A validação é **local** (assinatura + claims), que é o comportamento normal de OIDC/JWT. Consequência: revogar um token antes do `exp` não é detectado. Introspecção custaria uma ida ao IdP por requisição |
| `azp` só é conferido com múltiplas audiences | Regra do OIDC Core §3.1.3.7. O realm deste projeto emite uma audience só, então não há o que conferir hoje |
| Sem rate limiting por identidade | Não é exigido pelo enunciado; com o lock por carteira, o custo de uma rajada é contenção de linha, não corrupção de saldo |

---

## 17. Como validar tudo `[PRONTO]`

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'
$env:GOCACHE='E:\jungleg\.gocache\build'
$env:GOPATH='E:\jungleg\.gocache\gopath'

docker compose up -d postgres keycloak localstack
$env:DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungleg?sslmode=disable'
.\bin\migrate.exe up

go build ./... ; go vet ./... ; go vet -tags=integration ./...
gofmt -l .\internal\ .\cmd\ .\migrations\ .\test\
go test -race ./...

# integração (banco SEPARADO: os testes truncam tabelas; e o IdP REAL)
$env:TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
$env:OIDC_ISSUER='http://localhost:8080/realms/jungleg'
$env:OIDC_DISCOVERY_URL='http://localhost:8080/realms/jungleg'
$env:OIDC_AUDIENCE='jungleg-api'
$env:SQS_ENDPOINT='http://localhost:4566'
# as filas precisam EXISTIR: se `list-queues` estiver vazio, o script de init
# falhou (ver o log do LocalStack) e os testes de consumo vão falhar por isso
docker exec jungleg-localstack awslocal sqs list-queues
go test -count=1 -race -tags=integration -timeout 300s ./...

# as 32 operações inválidas que o banco precisa recusar
cmd /c "docker exec -i jungleg-postgres psql -U jungleg -d jungleg < deploy/postgres/verify_constraints.sql"
```

**Números atuais:** 574 casos no domínio (Fases 1–3) + **38** testes de
persistência (Fase 5) + 3 de `idgen` + **56 casos** do caso de uso (Fase 6) +
**49** em `internal/infra/auth` (Fase 7) + **67** em `internal/httpapi` (Fase 7) +
**26** em `internal/app/inbox` (Fase 8) + **19** em `internal/consumer` (Fase 8) +
**29 casos** de integração do caminho completo (Fase 6) + **30** da borda HTTP
(Fase 7) + **9 funções** de consumo com LocalStack (Fase 8) + 2 de integração do
binário real. `go test -race` verde nos **17 pacotes** de unidade e nos **3** de
integração. Cobertura do domínio: `money` 97,9%, `wallet` 99,0%, `wagering` 100,0%.

**Migrations:** quatro (`000001_init`, `000002_opening_sem_jogo_e_rodada`,
`000003_ordem_da_outbox`, `000004_inbox_attempts`). O binário `bin/migrate.exe` carrega as migrations
**embutidas** (`go:embed`): depois de criar uma migration nova, é preciso
**rebuidá-lo** (`go build -o bin/migrate.exe ./cmd/migrate`) — um binário velho
responde "schema já está atualizado" e não aplica nada.

Os dois pacotes de integração usam o MESMO banco de teste e truncam tabelas.
Como `go test ./...` roda pacotes em processos paralelos, cada `TestMain` toma um
lock consultivo do PostgreSQL (`pg_advisory_lock`) e espera a vez — **não é
preciso `-p 1`**.

**Evidências por fase:** `docs/evidencias/FASE_1.md` a `FASE_7.md`.

**Dependências de teste:** os testes de integração da borda HTTP precisam do
**Keycloak** de pé (o token é obtido por `client_credentials`), e o de
`internal/app` compila e sobe o binário real. Sem `TEST_DATABASE_URL` as suítes de
integração são **puladas** (com aviso), para `go test ./...` continuar verde em
máquina sem Docker; sem Keycloak, os testes da borda HTTP são pulados pelo mesmo
motivo — e o `internal/infra/auth` continua provando a criptografia com o servidor
OIDC de teste que assina de verdade.
