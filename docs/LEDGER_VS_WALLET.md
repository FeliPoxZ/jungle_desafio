# Ledger × Wallet — a distinção que o desafio cobra

> Documento de estudo. Nasceu de uma dúvida real durante a implementação da Fase 2:
> *"qual é a diferença entre ledger e wallet?"*
> Resposta curta: **a `Wallet` diz quanto o jogador tem. O ledger diz como ele chegou lá.**

---

## 1. Os dois, lado a lado

| | `Wallet` (`wallet.go`) | `Entry` — o ledger (`ledger_entry.go`) |
| --- | --- | --- |
| O que guarda | **o presente**: `balance`, `version`, `updatedAt` | **o passado**: cada movimentação que já aconteceu |
| Muda depois de criado? | **Sim** — é o agregado mutável | **Nunca** — append-only, sem setter |
| Quantos por jogador | **1** (o par `playerID` + `currency`) | **N** (um por movimentação) |
| Responde | *"quanto ele tem agora?"* | *"por que ele tem isso?"* |
| Custo de leitura | O(1) — uma linha | O(n) — soma de todas as linhas |
| Se for apagado | o saldo se perde | a história se perde |
| Quem escreve | `Debit` / `Credit` | `NewEntry`, chamado pelo `apply` |

## 2. O mesmo evento visto pelos dois

Um débito de R$ 25,00 numa carteira que tinha R$ 1.000,00.

**A carteira** guarda só o novo estado — ela não faz ideia de que houve um débito:

```go
Wallet{
    id:        "w-1",
    playerID:  "p-1",
    currency:  "BRL",
    balance:   975.00,   // 1000.00 − 25.00
    version:   2,        // era 1
    updatedAt: 12:01,
}
```

Se você olhar só a carteira amanhã, vê `975.00` e **não sabe por quê**.

**O ledger** guarda o evento, com os dois saldos:

```go
Entry{
    walletID:      "w-1",
    transactionID: "txn-abc",
    direction:     DEBIT,
    amount:        25.00,
    balanceBefore: 1000.00,   // para auditoria
    balanceAfter:  975.00,    // para auditoria
    createdAt:     12:01,
}
```

Essa linha **se explica sozinha**: quem abrir a tabela amanhã confere `1000,00 − 25,00 = 975,00`
sem depender do saldo atual da carteira.

## 3. Por que não ter só um dos dois

Essa é a pergunta de acompanhamento. Cada um resolve um problema que o outro não resolve.

| Se existisse só a `Wallet` | Se existisse só o ledger |
| --- | --- |
| Ler o saldo é rápido, mas **não há como provar** que ele está certo | Prova tudo, mas responder *"quanto ele tem?"* exige somar **todas** as linhas do jogador em **toda** requisição |
| Um bug de concorrência some da vista | O `SELECT` de saldo fica cada vez mais lento |
| Não há como auditar um débito contestado | O enunciado pede `GET /wallets` com saldo — lento e frágil |
| O enunciado exige ledger auditável → **reprova** | |

A `Wallet` é o **cache materializado** da soma do ledger. O ledger é a **fonte da verdade**.
Os dois existem porque se quer **velocidade de leitura** e **prova de integridade** ao mesmo tempo.

## 4. O que amarra os dois

### 4.1. São gravados na mesma transação

Se o saldo muda e o lançamento não é gravado, o sistema está **mentindo**. É o enunciado:

> *"Cada mudança financeira exige o lançamento correspondente no ledger, confirmado junto com o saldo."*

Isso é a Unit of Work da Fase 5: `wallets` + `wallet_ledger_entries` + outbox no mesmo commit.

### 4.2. O lançamento carrega os dois saldos

Não é enfeite. É o que permite reconstruir o saldo **sem recalcular** a aritmética, e é o que
a reconciliação do enunciado pede:

> *"Reconstrua o saldo a partir do ledger, incluindo a abertura, e compare os valores.
> `difference` é o saldo armazenado menos o saldo reconstruído."*
> *"A reconciliação não deve alterar o saldo."*

A Fase 11 vai literalmente fazer `soma(créditos) − soma(débitos)` e comparar com
`wallet.balance`. Se divergir, o sistema **avisa** em log e métrica em vez de esconder.

### 4.3. O ledger nunca é alterado

- **No código:** `Entry` não tem setter e é devolvido por cópia.
- **No banco (Fase 4):** trigger `BEFORE UPDATE OR DELETE ... RAISE EXCEPTION`.

**Por que isso é levado a sério:** se o ledger pode ser editado, ele deixa de ser prova.
Corrigir um saldo com `UPDATE` **apaga a evidência do erro**. Em sistema financeiro, correção é
**movimentação nova** (estorno), nunca reescrever a história.

## 5. O detalhe que quase todo candidato erra

**A `Wallet` não pode ser reconstruída somando o ledger a cada leitura** — mas o ledger **é** a
fonte da verdade quando os dois divergem.

E existe um caso em que eles discordam **legitimamente**: uma operação de **valor zero**
(`LOSS`) não gera lançamento. Por isso o enunciado diz que a reconciliação precisa
*"incluir a abertura"*, e não apenas somar linhas.

Foi exatamente essa regra que a Fase 2 implementou:

```go
if amount.IsZero() {
    return Entry{}, nil   // sem saldo, sem versão, sem updatedAt, sem ledger
}
```

## 6. Onde isso aparece no código

| Regra | Onde está | Como é garantido |
| --- | --- | --- |
| Saldo nunca negativo | `wallet.apply` | `Sub` + `IsNegative` → `INSUFFICIENT_FUNDS` antes de tocar no estado |
| Moeda da movimentação = moeda da carteira | `wallet.apply` | checada **antes** da aritmética (define o `failureCode`) |
| Versão só muda quando o saldo muda | `wallet.apply` | `version++` e `updatedAt` só depois de um valor não zero |
| Lançamento coerente | `wallet.NewEntry` | calcula `balanceAfter` em vez de receber |
| Ledger imutável | `wallet.Entry` | sem setters; valor devolvido por cópia |
| Ledger não registra zero | `wallet.apply` | valor zero não gera `Entry` |
| Mesma transação | Fase 5 (Unit of Work) | a definir |

## 7. Resposta pronta para a entrevista

Se perguntarem *"qual a diferença entre carteira e ledger?"*:

> *"A carteira é o estado atual e mutável do saldo do jogador — uma linha por par jogador e
> moeda, com versão para controle de concorrência. O ledger é o histórico append-only: um
> lançamento por movimentação, imutável, registrando direção, valor e os saldos antes e depois.*
>
> *Preciso dos dois porque resolvem problemas diferentes: a carteira me dá leitura O(1) do
> saldo, o ledger me dá a prova de como ele chegou ali. Os dois são gravados na mesma
> transação, e o lançamento guarda os dois saldos exatamente para eu poder reconstruir e
> conferir o saldo sem depender dele — é o que a reconciliação faz.*
>
> *E o ledger nunca é alterado: correção em sistema financeiro é movimentação nova, não
> reescrever a história."*

Se apertarem com *"mas se o saldo da carteira é a soma do ledger, por que não calcula sempre?"*:

> *"Por latência. Com anos de histórico, somar tudo em cada requisição de saldo não escala. A
> carteira é o cache materializado dessa soma, e a reconciliação é o que garante que o cache
> não mentiu."*

E se perguntarem *"e quando os dois divergem legitimamente?"*:

> *"Quando a operação tem valor zero, como o `LOSS`: ele não movimenta dinheiro, então não gera
> lançamento e não muda a versão da carteira. Por isso a reconciliação precisa considerar a
> abertura da carteira, e não só somar as linhas do ledger."*
