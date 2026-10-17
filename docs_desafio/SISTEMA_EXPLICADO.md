# SISTEMA EXPLICADO — o Jungle Desafio, do zero ao detalhe

Este documento ensina o serviço que está neste repositório para quem vai
**mantê-lo** — não para quem vai defendê-lo numa banca. A diferença importa: aqui
interessa "por que está assim", "o que quebra se eu mexer" e "onde eu provo que
continua certo", e não a narrativa de como o projeto foi construído (essa está em
`docs/evidencias/FASE_0.md` a `FASE_11.md`).

**Como ler os ponteiros.** Toda afirmação técnica relevante aponta para o lugar
onde ela pode ser conferida: um `caminho/arquivo.go:linha` com o símbolo entre
parênteses, ou o nome de um teste (`func Test...`) que a trava. O **nome do
símbolo** é a referência estável; o número da linha é o do estado atual do
repositório e pode andar quando o arquivo mudar. Se os dois discordarem, o símbolo
manda — e o `grep` acha o resto.

**O que este documento não é.** Não é o enunciado (esse é o `README.md` da raiz,
que não se edita), não é o registro de decisões (esse é o `ARCHITECTURE.md`) e não
é o mapa de conformidade item por item (`docs/CONFORMIDADE_README.md`). Ele
**traduz** os três para a linguagem de quem chega hoje.

---

# 1. O problema

## 1.1 O que o serviço faz, em uma frase

Provedores de jogos (cassino, crash game, esporte) mandam operações financeiras de
jogadores — aposta, prêmio, perda, estorno, reversão — e o serviço tem de mover o
dinheiro da carteira do jogador **exatamente uma vez por operação**, registrar por
que o saldo mudou, e avisar o resto do mundo por evento — mesmo com operações
repetidas, simultâneas e com o processo morrendo no meio.

São cinco tipos externos. O comportamento de cada um é a primeira coisa a decorar:

| Tipo | Movimenta? | Referência | Regra central |
| --- | --- | --- | --- |
| `BET` | **débito** | opcional (informativa) | exige valor positivo e saldo suficiente |
| `WIN` | **crédito** | opcional (informativa) | exige valor positivo; pode citar a aposta da rodada |
| `LOSS` | **não** | opcional (informativa) | exige `money.amount` exatamente `"0.00"`; não cria lançamento, não muda a versão da carteira, **mas gera evento** |
| `REFUND` | **crédito** | **obrigatória** | devolve integralmente o valor de uma `BET` processada |
| `ROLLBACK` | **contrário ao original** | **obrigatória** | desfaz integralmente uma `BET`, `WIN` ou `REFUND` processada |

Há um sexto tipo, `OPENING`, que **não vem de fora**: é a abertura interna de
carteira (`POST /wallets`), o único caminho que cria saldo a partir do zero
(`internal/domain/wagering/kind.go` — `KindOpening`, `IsInternal`).

A tabela acima é a mesma do enunciado (§7); o que o código acrescenta é que cada
propriedade é um método do tipo, e não um `if` espalhado:
`kind.go` — `MovesMoney` (`k != KindLoss`), `RequiresReference` (`REFUND` e
`ROLLBACK`), `AllowsZeroAmount` (só `LOSS`), `ValidateAmount`.

## 1.2 O ambiente é hostil por definição, não por acidente

O enunciado (§3) não pede "trate erros". Ele **afirma** seis fatos do ambiente e
exige que a solução continue correta com todos eles acontecendo. A diferença é
filosófica e prática: uma exceção é algo que você evita; um fato do ambiente é
algo que você **projeta para suportar**.

| Fato do ambiente | Por que é fato, e não exceção | Onde o sistema trata | O que aconteceria se ignorasse |
| --- | --- | --- | --- |
| entrega **at-least-once** (a mesma operação chega duas, três, cinquenta vezes) | o broker garante "pelo menos uma", nunca "exatamente uma"; retry e republicação são o mecanismo normal dele | chave de idempotência + hash + índices únicos (`internal/app/wagering/service.go:383` — `replay`); inbox por mensagem (`internal/app/inbox/processor.go:212` — `ganchos`) | débito duplicado: o jogador perde dinheiro duas vezes pela mesma aposta |
| **reversão antes da transação revertida** | a ordem de chegada é do provedor e da rede, não sua | `PENDING_REFERENCE` durável + worker (`internal/app/wagering/outcome.go:42` — `marcarPendente`; `internal/worker/referencias.go:123` — `RetomarVencidas`) | ou a reversão é recusada para sempre (dinheiro preso), ou é aplicada sem referência (dinheiro errado) |
| **duas operações na mesma carteira ao mesmo tempo** | duas instâncias atendem o mesmo jogador; a rede não serializa nada | lock de linha (`internal/infra/postgres/wallet_repository.go:53` — `GetByIDForUpdate`) + versão (`Update`) | *lost update*: as duas leem 100,00, as duas debitam 80,00, o saldo fica 20,00 e saíram 160,00 |
| **queda abrupta antes ou depois do commit** | `SIGKILL`, `docker stop` sem prazo, panic, VM desligada | uma transação por operação (`internal/infra/postgres/uow.go:58` — `Run`); outbox + lease (`internal/worker/publicador.go:216` — `reivindicar`) | estado pela metade (saldo sem lançamento) ou evento confirmado e nunca publicado |
| **publicação repetida de evento** | o publisher republica de propósito quando não sabe se publicou | `eventId` estável no corpo; `MessageDeduplicationId` por tentativa (`internal/infra/sqs/publicador_adapter.go:87`) | o consumidor aplica o mesmo efeito duas vezes |
| **PostgreSQL ou SQS fora do ar por um tempo** | indisponibilidade de rede e de broker é rotina | classificação de erro transitório (`internal/infra/postgres/errors.go:95` — `Classify`), retry da transação inteira (`service.go:143` — `comRetentativa`), retry por visibilidade (`internal/consumer/consumer.go:392` — `adiar`) | erro permanente para uma disputa momentânea: uma aposta legítima é recusada |

Repare no formato da tabela: cada linha tem **um mecanismo** e **uma consequência
em dinheiro**. Esse é o padrão de raciocínio que o resto do documento usa.

## 1.3 Os dois números que resumem o risco

Se você só lembrar de dois cenários, lembre destes dois — eles são os que o
enunciado nomeia e os que qualquer revisor vai pedir:

1. **80 + 80 sobre 100.** Uma carteira com **100,00 BRL** recebe, ao mesmo tempo,
   duas apostas distintas de **80,00**. O resultado correto é: **uma** `PROCESSED`,
   **uma** `REJECTED` com `INSUFFICIENT_FUNDS`, saldo final **20,00**, **um único**
   débito no ledger e versão **2**. Se as duas passarem, o saldo vai a −60,00 (saldo
   negativo por concorrência é eliminatório). Se as duas forem recusadas, o jogador
   não conseguiu apostar o que podia.
   Provas: `TestCenarioDoREADMEDuasApostasDe80Sobre100` (um processo),
   `TestT2DuasApostasDe80Sobre100EmInstanciasDiferentes` (três processos reais).
2. **A mesma aposta 50 vezes.** Cinquenta envios simultâneos da mesma operação
   (mesmo `providerId` + `externalTransactionId`, mesma `Idempotency-Key`) têm de
   produzir **um** débito, **uma** linha de `wager_transactions` e versão **2**. Na
   prática: 1 resposta `201`, 49 respostas `200` com `idempotentReplay: true` e o
   **mesmo** saldo.
   Provas: `TestT1MesmaAposta50VezesEntreTresInstancias` (HTTP, três processos) e
   `TestT1MesmaOperacao50VezesPelaFila` (50 entregas distintas pela fila).

## 1.4 O que "correto" significa neste sistema

"Correto" não é "a resposta foi 200". É um conjunto de invariantes que podem ser
conferidas **depois** do fato, lendo o banco:

- o saldo da carteira é **igual** à soma dos lançamentos do ledger (créditos menos
  débitos, incluindo a abertura) — é o que o endpoint de reconciliação calcula e o
  que a conferência final faz em SQL puro
  (`TestConferenciaFinalCruzandoHTTPESQS`, `TestReconcileFechaOExemploDoREADME`);
- nenhuma carteira tem saldo negativo (CHECK `wallets_balance_non_negative`,
  `migrations/000001_init.up.sql:53`);
- cada operação tem no máximo **um** lançamento (`UNIQUE (wallet_id,
  transaction_id)`, `migrations/000001_init.up.sql:227`);
- cada mensagem tem no máximo **um** tratamento por consumidor
  (`UNIQUE (consumer_name, message_id)`, `migrations/000001_init.up.sql:269`);
- nenhum evento confirmado fica sem publicação e nenhum é publicado antes do
  commit (outbox na mesma transação, `internal/app/wagering/events.go:98` —
  `enfileirar`).

O enunciado (§14) transforma parte disso em **eliminatório**: ausência de
autenticação efetiva, acesso não autorizado, cálculo monetário em ponto flutuante,
saldo negativo por concorrência, movimentação duplicada, idempotência só em
memória, dependência de uma única instância, publicação antes do commit, ausência
de ledger auditável e substituição de PostgreSQL/SQS/IdP por mocks. Cada um desses
tem, no capítulo 5, a linha da tabela que mostra onde é imposto e qual teste o
prova.

| Eliminatório (README §14) | Onde é impossível, por construção |
| --- | --- |
| sem autenticação efetiva | toda rota de negócio é registrada atrás de `auth.Middleware` e, sem autenticador, **nada** é registrado (`internal/httpapi/routes.go:27` — `RegistrarRotas`) |
| acesso não autorizado | `providerId` sai do token, nunca do corpo (`internal/httpapi/handlers.go:69`); leitura alheia responde "não existe" (`internal/app/wagering/query.go`) |
| dinheiro em ponto flutuante | `Money` é `int64` + `Currency`; há teste de AST que reprova `float` no pacote (`internal/domain/money/style_test.go` — `TestSemPontoFlutuanteNoCodigo`) |
| saldo negativo por concorrência | `wallet.apply` recusa débito que ficaria negativo (`internal/domain/wallet/wallet.go:200`) **e** o banco recusa (`wallets_balance_non_negative`) |
| movimentação duplicada | índice único por operação no ledger + idempotência persistente |
| idempotência em memória | índices únicos em `wager_transactions` + decisão de replay lendo o banco |
| dependência de uma instância | todo estado de recuperação é coluna de banco; provado com três processos (`TestTresInstanciasIndependentes`) |
| publicação antes do commit | eventos são **linhas** da outbox na mesma transação; nada publica inline |
| ledger não auditável | trigger que recusa `UPDATE` e `DELETE` (`migrations/000001_init.up.sql:231`) |
| mocks no lugar da infra | os testes de integração sobem PostgreSQL, Keycloak e LocalStack reais |

---

# 2. Vocabulário do zero

Este capítulo define os termos na ordem em que eles aparecem no código. Se você
só ler um capítulo para entender o sistema, leia este.

## 2.1 Dinheiro: unidades mínimas, `int64`, escala fixa

**A representação.** `Money` não guarda "25,00". Ele guarda `2500` (vinte e cinco
reais em centavos) e a moeda `BRL`:

```go
type Money struct {
    minor    int64
    currency Currency
}
```

(`internal/domain/money/money.go:62`.) Os campos são privados de propósito: um
`Money` só nasce por `New`, `Parse`, `Zero` ou por uma operação aritmética, e nunca
muda — toda operação devolve um `Money` novo. Isso é o que faz dele um **value
object** (valor imutável), e é o que permite passá-lo entre camadas sem medo de
alguém "ajustar" o valor no meio do caminho.

| Propriedade | Valor | Onde |
| --- | --- | --- |
| representação interna | `int64` em unidades mínimas | `money.go:63` (`minor`) |
| escala | 2, sempre | `money.go:349` (`parseAmount`) |
| contrato externo | `{"amount":"25.00","currency":"BRL"}` | `money.go:229` (`MarshalJSON`) |
| limite absoluto | `9_000_000_000_000_000` unidades mínimas (R$ 90 trilhões) | `money.go:71` (`maxMinor`) |
| persistência | `BIGINT` (unidades mínimas) + `CHAR(3)` | `migrations/000001_init.up.sql:44` |

**Por que `int64` e não `float64`.** `float64` é binário: ele representa potências
de dois, não décimos. `0.1 + 0.2` em `float64` dá `0.30000000000000004`. Num
sistema que soma lançamentos para conferir saldo, esse resíduo se acumula e a soma
**deixa de fechar** — o sistema passaria a mentir sobre o dinheiro, e a mentira
apareceria meses depois, na auditoria. Com `int64` em centavos a aritmética é
exata por construção: não é "provavelmente não dá erro", é "não existe erro de
representação".

Duas consequências de projeto vêm junto, e você vai encontrá-las no código:

1. **nada de número JSON.** `amount` é **string** no contrato (`moneyDTO`,
   `money.go:205`), porque o `encoding/json` converteria um número em `float64`
   antes de o domínio ver o valor;
2. **nada de normalização silenciosa.** `Parse` recusa `"25"`, `"25.000"`,
   `"25,00"`, `"+25.00"`, `"1e2"`, vazio, `NaN`, infinito e escala diferente de
   dois. Não arredonda, não completa, não "conserta". Se aceitasse, o hash de
   idempotência dependeria da forma de escrita e o mesmo pedido viraria duas
   operações (`TestParseAmountInvalido`, `TestUnmarshalJSONRejeitados`).

**Overflow: por que o limite é menor que o teto do `int64`.** O teto de um `int64`
é ~9,22 × 10^18. O limite do domínio é 9 × 10^15, mil vezes menor, e isso é
deliberado: para detectar que `a + b` estourou **sem** fazer a soma que estoura,
a guarda precisa calcular `maxMinor - b`, e essa conta só é segura se os valores
couberem com folga. As guardas ficam em `Add` (`money.go:419`, dois lados: piso e
teto), `Sub` (`money.go:435`) e `Neg` (`money.go:451`).

Isso não foi acertado de primeira: uma varredura exaustiva de **81 combinações**
achou **10 furos**, todos com dois negativos (defeito 1 do capítulo 8). A lição
está registrada no próprio `ARCHITECTURE.md` §2: em sinal, limite e overflow,
varredura exaustiva vale mais que raciocínio.

**Assimetria proposital.** Um `Money` negativo **serializa** com sinal
(`"-0.10"`), mas **não é aceito na entrada** — negativo só existe em cálculo
interno (a `difference` da reconciliação, por exemplo). Há teste que fixa as duas
metades: `TestMoneyString`, `TestRoundTripJSONNaoValeParaNegativo`.

## 2.2 Carteira: o agregado que guarda o saldo de agora

**Agregado** é a palavra para "um conjunto de dados que só muda por métodos
próprios, mantendo suas invariantes". A carteira é a raiz do agregado financeiro:
identidade, jogador, moeda, saldo, versão e instantes, todos privados
(`internal/domain/wallet/wallet.go:27`).

```go
type Wallet struct {
    id        WalletID
    playerID  PlayerID
    currency  money.Currency
    balance   money.Money
    version   int64
    createdAt time.Time
    updatedAt time.Time
}
```

Três regras que você **não** pode quebrar ao mexer aqui:

1. **`(playerId, currency)` identifica uma única carteira.** É índice único no
   banco (`wallets_player_currency_key`, `migrations/000001_init.up.sql:50`) e é o
   que faz "abrir a mesma carteira duas vezes" virar `409`, não duas carteiras.
2. **Débito nunca deixa saldo negativo** (`wallet.go:200`,
   `ErrInsufficientFunds`), e a movimentação **precisa** ser na moeda da carteira
   (`wallet.go:177`, `CodeWalletCurrencyMismatch`).
3. **A versão nasce em 1 e só avança quando o saldo muda.** Movimentação de valor
   zero devolve um resultado vazio, sem lançamento e sem incremento
   (`wallet.go:187`); é a mesma regra do `LOSS`. Teste:
   `TestVersaoSoAvancaQuandoOSaldoMuda`.

`NewWallet` e `Rehydrate` são construtores **diferentes por motivo**: `NewWallet`
valida tudo (é o caminho de criação); `Rehydrate` reconstrói o que veio do banco
sem reaplicar movimento, sem emitir evento e sem mexer em versão ou instantes
(`TestRehydrateNaoReaplicaNemAltera`). Se a reidratação reaplicasse a
movimentação, ler uma carteira do banco debitaria o jogador de novo.

**A versão e o `Movement`.** `Debit`/`Credit` incrementam a versão **em memória**.
Quem persiste precisa da versão **anterior** para o `UPDATE ... WHERE version = $`.
Ler `carteira.Version()` depois do débito devolve a versão nova, o `UPDATE` casa
com zero linhas e a operação falha **parecendo** conflito de concorrência. A
solução não foi no repositório: o domínio devolve um `Movement` que carrega
`versionBefore` (`wallet.go:127`), e o erro deixou de ser possível de cometer. Era
o defeito 8.

## 2.3 Lançamento de ledger: o histórico, append-only

Um `ledger.Entry` é uma linha que diz **o que aconteceu com o saldo**:
identidade, carteira, transação de origem, direção (`DEBIT`/`CREDIT`), valor,
saldo antes, saldo depois e instante (`internal/domain/ledger/entry.go:110`).

```go
type Entry struct {
    id            string
    walletID      ids.WalletID
    transactionID TransactionID
    direction     Direction
    amount        money.Money
    balanceBefore money.Money
    balanceAfter  money.Money
    createdAt     time.Time
}
```

**Append-only** quer dizer: só se acrescenta, nunca se altera nem se apaga. Neste
projeto isso não é convenção — é uma barreira física no banco: a função
`prevent_ledger_mutation()` levanta exceção e o trigger
`wallet_ledger_entries_append_only` a chama em `UPDATE` e `DELETE`
(`migrations/000001_init.up.sql:29` e `:231`). O repositório também não expõe
método de alteração (`internal/infra/postgres/ledger_repository.go` só tem
`Insert`). As duas barreiras juntas significam que **um bug de aplicação não
reescreve a história** — "corrigir" um lançamento é fazer um lançamento novo
(estorno), não editar o antigo.

**Por que cada lançamento guarda dois saldos.** Para a linha ser auditável
sozinha. Se a auditoria dependesse do saldo atual da carteira, ela mudaria a cada
operação e não serviria para auditar. Com `balanceBefore` e `balanceAfter`, quem
lê uma linha confere a aritmética sem consultar mais nada.

E a aritmética é conferida em **dois lugares**, de propósito:

1. no domínio, `ledger.NewEntry` (`entry.go:128`) recusa `after != before ± amount`
   e recusa valor não positivo — o cliente recebe erro com código estável;
2. no banco, `ledger_balance_consistency` (`migrations/000001_init.up.sql:212`)
   recusa a mesma incoerência — nenhum caminho, nem SQL escrito à mão, grava
   lançamento torto.

A primeira dá mensagem boa; a segunda sobrevive a bug de aplicação.

## 2.4 Transação e lock: o que o banco garante e o que **não** garante

**Transação** é uma unidade de tudo-ou-nada: ou todas as escritas entram, ou
nenhuma entra. A Unit of Work do projeto abre uma transação e injeta nela **todos**
os repositórios (`internal/infra/postgres/uow.go:79`), então
`wager_transactions`, `wallets`, `wallet_ledger_entries`, `inbox_messages` e
`outbox_events` vivem no mesmo commit. Não existe estado intermediário visível.

**Lock de linha** é diferente: é um pedido explícito para que a linha seja
reservada. `SELECT ... FROM wallets WHERE id = $1 FOR UPDATE`
(`wallet_repository.go:56`) diz ao PostgreSQL: "se outra transação quiser esta
linha, **faça-a esperar** até eu terminar". A segunda transação não falha nem lê
valor velho — ela **espera**.

O que confunde quase todo mundo é a diferença entre as duas coisas:

| Pergunta | O que o banco garante sozinho | O que ele **não** garante sozinho |
| --- | --- | --- |
| "ou tudo, ou nada" | sim, com `BEGIN`/`COMMIT` | que o seu código não deixe a transação aberta segurando lock |
| "duas transações não se atrapalham" | em `READ COMMITTED`, cada `SELECT` vê o último commit | que duas transações não **leiam o mesmo saldo antigo** e calculem em cima dele |
| "ninguém sobrescreve o meu valor" | nada, por padrão | que o `UPDATE` de B não apague o efeito do `UPDATE` de A (*lost update*) |
| "a ordem das operações é a de chegada" | nada | que a operação que chegou depois seja aplicada depois |
| "o lock é da linha e não da tabela" | sim, `FOR UPDATE` é por linha | que você não escreva um lock global por engano |
| "commitar implica publicar no SQS" | nada | qualquer coisa: o broker é externo ao commit |

Cada linha "não garante" dessa tabela virou uma peça do sistema:

- *lost update* → `FOR UPDATE` **antes** de ler o saldo + `UPDATE ... WHERE
  version = $3` (`wallet_repository.go:99`). Duas camadas, e as duas existem.
  Prova: `TestWalletLostUpdateNaoAcontece` (dois débitos concorrentes de 100 e 200
  sobre 1000 terminam em **700,00** e versão **3**; sem o lock, um deles seria
  perdido e o saldo daria 900 ou 800).
- **a ordem importa.** Ler o saldo e pedir o lock depois permite que outra
  transação altere o saldo no meio. A ordem correta está escrita no caso de uso:
  lock (passo 3), depois o instante (passo 4 implícito), depois o resto
  (`internal/app/wagering/service.go:251` — `GetByIDForUpdate`; `:267` — `now`).
  Há teste que mede o bloqueio: uma transação esperou 1,59 s pela outra
  (`TestWalletGetByIDForUpdateBloqueiaOutraTransacao`).
- **o lock é por carteira, não global.** Prova determinística: com a linha da
  carteira A presa, cinco operações em outras carteiras concluíram em 14–34 ms e a
  de A só terminou 408 ms depois da liberação
  (`TestT3CarteirasDistintasEmParaleloSemLockGlobal`).
- **`READ COMMITTED` + `FOR UPDATE`, e não `SERIALIZABLE`.** `SERIALIZABLE` aborta
  transações conflitantes (erro `40001`) e exigiria retry em praticamente todo
  caminho de escrita; o conflito real do domínio é "duas operações na **mesma**
  carteira", e o lock de linha trata exatamente esse caso. A justificativa completa
  está em `uow.go:38`.
- **timeout de lock não é erro de negócio.** Quando o `lock_timeout` estoura, o
  PostgreSQL devolve `55P03`; isso significa "outra transação estava nesta
  carteira, tente de novo" e está classificado como **transitório**
  (`internal/infra/postgres/errors.go:88` e `:126`). Classificá-lo como permanente
  faria o serviço recusar uma aposta legítima por causa de uma disputa de
  milissegundos (defeito 6).

## 2.5 Idempotência: chave, hash canônico, replay e conflito

**Idempotência** é a propriedade de que repetir a mesma instrução não repete o
efeito. Aqui ela tem duas peças: uma **chave** que identifica o pedido e um
**hash** que identifica o conteúdo.

- **Chave.** Vem do header `Idempotency-Key` (HTTP) ou de `data.idempotencyKey`
  (SQS). É `UNIQUE (provider_id, idempotency_key)` no banco
  (`migrations/000001_init.up.sql:161`). O servidor **nunca** substitui a chave
  recebida por uma calculada — fazer isso faria o reenvio do provedor parecer
  operação nova (`handlers.go:79`).
- **Hash.** É `sha256` de um JSON **canônico** (chaves ordenadas em todos os
  níveis, sem espaços, sem escape de HTML) dos campos de **negócio** —
  `providerId`, `externalTransactionId`, `playerId`, `walletId`, `roundId`,
  `gameId`, `kind`, `money`, `referenceExternalTransactionId`
  (`internal/app/wagering/payload.go:31` — `payloadDeNegocio`). Ficam **de fora** a
  chave de idempotência e os metadados de transporte (correlação, `messageId`,
  timestamps): se a chave entrasse no hash, o mesmo pedido chegando por HTTP e por
  SQS geraria dois hashes, duas operações e dois débitos.
- **Normalização antes do hash.** O hash é calculado sobre o `Money` **já
  convertido em unidades mínimas**, nunca sobre o texto recebido. Formas não
  canônicas (`"25"`, `"25.000"`, `"+25.00"`) são **recusadas** no parsing — não
  arredondadas nem completadas.

Com as duas peças, a decisão de reenvio tem exatamente três desfechos:

| Situação | Decisão | Resposta HTTP |
| --- | --- | --- |
| mesma chave **e** mesmo hash | **replay**: devolve o resultado persistido, **sem movimentar** | `200` com `idempotentReplay: true` |
| mesma chave **e** hash diferente | **conflito**: a chave foi reutilizada com outro conteúdo | `409` |
| `(providerId, externalTransactionId)` já registrado sob **outra** chave | **conflito**: a identidade financeira é o par, não a chave | `409` |

A implementação é `service.go:383` (`replay`) e usa **duas leituras**: a primeira
pela chave, a segunda pela identidade financeira. A segunda compara a chave da
linha encontrada, e isso não é zelo: entre as duas consultas, **outra instância
pode comitar a mesma operação**. Sem comparar, esse instante é lido como
"registrada sob outra chave" e um pedido legítimo recebe `409`. Foi um teste de 12
disparos simultâneos da mesma operação que pegou (defeito 14, teste
`TestLinhaApareceEntreAsDuasLeiturasDeIdempotenciaEUmReplay`).

**Por que `INSERT` + índice único, e não "consultar se existe e inserir".** Entre a
consulta e a inserção existe uma janela: dois processos consultam, os dois veem
"não existe", os dois inserem. A janela é curta, mas com dinheiro é suficiente para
creditar duas vezes. Com o índice único, **o banco decide**: quem chega segundo
recebe `23505`, e o serviço trata isso como caminho esperado — repete a transação
inteira e a segunda passada acha a linha gravada pela primeira
(`TestMesmaApostaEmParaleloProcessaUmaVez`).

**Replay devolve o saldo ORIGINAL.** Se a carteira já teve outras movimentações
depois, o replay ainda responde o saldo do processamento original (é por isso que
`wager_transactions` guarda `result_balance_minor`). O cliente pediu o resultado
**daquela** operação, não um retrato de agora. Zero é saldo legítimo, então a
distinção entre "não calculado" (rejeição, pendência) e "zero" vem de
`HasResultBalance`, e não do valor (`service.go:417` — `resultadoPersistido`).
Prova: `TestReplayDevolveOSaldoDoProcessamentoOriginal`.

## 2.6 Outbox e inbox: as duas tabelas que fazem o broker ficar fora do commit

**Outbox.** O SQS é externo: ele **não participa** do commit do banco. Publicar
dentro da transação cria duas falhas possíveis, e nenhuma é aceitável com
dinheiro:

| Situação | Consequência |
| --- | --- |
| o broker aceita e o commit falha | o evento fala de uma mudança que **não aconteceu** |
| o commit passa e o broker falha | a mudança acontece e **ninguém é avisado** |

A solução é a tabela `outbox_events`: o evento é gravado **na mesma transação** do
saldo e do ledger (`internal/app/wagering/events.go:98` — `enfileirar`). Ou tudo
entra, ou nada entra. Um worker separado lê a tabela e publica **depois** do
commit. A entrega vira "pelo menos uma vez" e o consumidor deduplica pelo
`eventId`.

**Inbox.** O problema espelhado na entrada: a mensagem pode ser reentregue, e
precisamos saber que **aquela** mensagem já foi tratada. `inbox_messages` guarda
`(consumer_name, message_id)` único, o `body_hash`, o instante de recebimento e o
de conclusão (`migrations/000001_init.up.sql:244`).

Repare que a inbox **não** é a garantia financeira: ela deduplica *mensagem*, não
*operação*. Cinquenta entregas distintas com o mesmo conteúdo passaram pela inbox
(50 linhas `PROCESSED`) e produziram **um** débito — quem barrou o segundo débito
foi a chave + hash + índice único (`TestT1MesmaOperacao50VezesPelaFila`).

**A diferença de papéis, em uma linha:** outbox resolve "o evento não se perde";
inbox resolve "a mensagem repetida não é trabalho novo".

## 2.7 Referência pendente (`PENDING_REFERENCE`)

Um `REFUND` ou `ROLLBACK` precisa apontar para a operação que ele desfaz, por
`(providerId, referenceExternalTransactionId)`. E a referência pode **não ter
chegado ainda** — o provedor mandou a reversão antes da aposta.

Não dá para simplesmente recusar (o dinheiro ficaria preso), nem para aplicar sem
referência (não se sabe o que desfazer). O que se faz é aceitar e **persistir a
espera**:

- a linha fica `PENDING_REFERENCE`, com `reference_resolution_attempt` e
  `next_resolution_attempt_at` gravados (`outcome.go:42` — `marcarPendente`);
- um evento `WagerTransactionPendingReference` entra na outbox;
- a resposta HTTP é `202` — "aceitei, ainda vou resolver" —, que **não** é erro;
- o worker de referências varre as linhas vencidas e chama `Retomar`, que é um
  caminho separado do `Process`, mas reusa **todas** as réguas de negócio
  (`internal/app/wagering/retomar.go`; `internal/worker/referencias.go:123`).

Se a referência nunca chegar, o TTL decide: `attempt >= REFERENCE_MAX_ATTEMPTS`
(20 por padrão) **ou** idade `>= REFERENCE_PENDING_MAX_AGE` (2 min por padrão) leva
a `REJECTED` com `REFERENCE_NOT_FOUND` e evento de rejeição. O TTL só decide
quando a referência **não resolve**: se ela chegou, mesmo depois do prazo, a
operação conclui (rejeitar uma reversão correta para respeitar um relógio seria
destruir informação).

## 2.8 Worker e lease

**Worker** aqui é um laço de vida longa, separado do servidor HTTP, que faz
trabalho de fundo: publicar a outbox e resolver referências pendentes. Cada laço é
supervisionado: se ele morre, o `Runner` reinicia com backoff e marca o readiness
como `DOWN` enquanto está parado (`internal/worker/runner.go:162` — `supervisionar`;
`:228` — `CheckReadiness`).

**Lease** é o mecanismo que resolve "o worker morreu no meio". Em vez de um lock
que dura até o fim da transação (e que morre com o processo, deixando a linha
ambígua), o worker **empurra** `available_at` para o futuro ao reivindicar o lote:

```sql
WITH reivindicados AS (
    UPDATE outbox_events AS o SET available_at = $3   -- $3 = agora + lease
    FROM (SELECT event_id FROM outbox_events
          WHERE published_at IS NULL AND available_at <= $1
          ORDER BY sequence LIMIT $2
          FOR UPDATE SKIP LOCKED) AS lote
    WHERE o.event_id = lote.event_id
    RETURNING ...
)
SELECT ... FROM reivindicados ORDER BY sequence
```

(`internal/infra/postgres/outbox_repository.go:85` — `ClaimBatch`.) Traduzindo:

- **`SKIP LOCKED`** faz com que dois publishers peguem lotes **diferentes**, sem um
  bloquear o outro e sem reivindicar o mesmo evento;
- **lease** faz com que, se o processo morrer antes de publicar, o evento volte
  sozinho quando o prazo passar, em vez de ficar preso para sempre;
- **a transação é curta**: ela reivindica e confirma o lease; a publicação acontece
  **fora** dela (`internal/worker/publicador.go:216` e `:231`). Segurar
  `FOR UPDATE` e uma conexão do pool durante um round-trip ao broker esgotaria o
  pool com poucas instâncias.

A mesma ideia aparece na retomada: `ClaimPendingReferencesDue` empurra
`next_resolution_attempt_at` (`internal/infra/postgres/transaction_repository.go:181`).

---

# 3. O fluxo completo de uma requisição

Este capítulo é o "mapa do dia a dia". Leia com o código aberto ao lado: cada
caixa do diagrama tem o arquivo correspondente na tabela que vem depois.

## 3.1 O fluxo HTTP: do `POST` ao evento na fila

Você é o provedor `provider-a` e manda uma aposta de 25,00 na carteira que tem
975,00. O caminho inteiro, incluindo o que acontece **depois** da resposta:

```
   PROVEDOR
      │  POST /wagering/transactions
      │  Authorization: Bearer <token RS256>
      │  Idempotency-Key: provider-a:transaction-123
      │  {"providerId":"provider-a","externalTransactionId":"transaction-123",
      │   "walletId":"0192...","roundId":"round-987","gameId":"fortune-chimp",
      │   "kind":"BET","money":{"amount":"25.00","currency":"BRL"}}
      ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│ internal/httpapi — a BORDA (não decide dinheiro, só traduz)                  │
│                                                                              │
│  RequestID → RealIP → AccessLog → Recoverer → Timeout → MaxBody              │
│      │                                                                       │
│      ├─ auth.Middleware ......... valida RS256/iss/aud/exp e põe Identity    │
│      │                            no contexto  (401 se o token não serve;    │
│      │                                            503 se o IdP está fora)    │
│      ├─ auth.RequireProvider .... exige claim provider_id  (403 sem ele)     │
│      ├─ decodeJSON estrito ...... campo desconhecido = erro                  │
│      ├─ corpo.providerId × token → 403 se divergirem (nem chama o serviço)   │
│      └─ Idempotency-Key vazia → 400 IDEMPOTENCY_KEY_REQUIRED                 │
└───────────────────────────────────┬──────────────────────────────────────────┘
                                    │  appwagering.Request (o MESMO tipo do SQS)
                                    ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│ internal/app/wagering — o CASO DE USO (a regra, uma transação)               │
│                                                                              │
│  Process = ProcessWithHooks(nil)                                             │
│    valida + normaliza + calcula hash canônico do payload de negócio          │
│    comRetentativa (até 3, envolvendo a TRANSAÇÃO INTEIRA)                    │
│      │                                                                       │
│      └─ uow.Run  ─────────────────────── BEGIN ───────────────────────────┐  │
│           1. SELECT por (provider, idempotency_key)      → replay? 409?  │  │
│              SELECT por (provider, external_txn_id)      → outra chave?  │  │
│           2. SELECT carteira FOR UPDATE   ◄── O LOCK, ANTES DE CALCULAR  │  │
│           3. now := relógio (depois do lock: ordem dos efeitos)          │  │
│           4. INSERT wager_transactions (PENDING)                         │  │
│           5. jogador e moeda da carteira × operação? → REJECTED          │  │
│           6. resolve a referência (quando houver)                        │  │
│                ausente      → PENDING_REFERENCE + evento + 202           │  │
│                divergente   → REJECTED + evento                          │  │
│                compatível   → segue                                      │  │
│           7. carteira.Debit/Credit → Movement (com versionBefore)        │  │
│           8. UPDATE wallets ... WHERE version = mov.VersionBefore()      │  │
│           9. INSERT wallet_ledger_entries                                │  │
│          10. Save: MarkProcessed + result_balance + referência interna   │  │
│          11. INSERT outbox_events  (Processed + BalanceChanged)          │  │
│            └───────────────────────── COMMIT ─────────────────────────┘  │
│         (se qualquer passo falhar: ROLLBACK; nada de dinheiro se move)   │
└───────────────────────────────────┬──────────────────────────────────────────┘
                                    │  Result{TransactionID, Status, Balance}
                                    ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│ internal/httpapi — a resposta                                                 │
│   PROCESSED (1ª vez)      → 201  {transactionId,status,balance,replay:false} │
│   PROCESSED (replay)      → 200  {... balance ORIGINAL, replay:true}         │
│   PENDING_REFERENCE       → 202  {..., balance:null, failureCode:null}       │
│   REJECTED                → 422  {..., balance:null, failureCode:"..."}      │
│   erro de entrada/transp. → 400/403/409/404/503 pelo envelope {"error":{}}   │
└───────────────────────────────────┬──────────────────────────────────────────┘
                                    │  a resposta JÁ saiu; a transação JÁ commitou
                                    ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│ internal/worker.OutboxPublisher — o publicador (outro laço, outro momento)   │
│   ClaimBatch (transação CURTA)  → empurra available_at = agora + lease       │
│   Publicar (FORA da transação)  → wallet-events.fifo                         │
│   MarkPublished (WithoutCancel)→ tira dos pendentes                          │
│      └ falhou/estourou/não existe destino → MarkRetry / QUARENTENA           │
└───────────────────────────────────┬──────────────────────────────────────────┘
                                    ▼
                       wallet-events.fifo  (FIFO, MessageGroupId = walletId)
```

A tabela abaixo dá o endereço de cada passo. Se você for depurar um caso real,
esta é a ordem em que vale abrir os arquivos:

| # | Etapa | Onde | O que essa etapa garante |
| --- | --- | --- | --- |
| 1 | middlewares e correlação | `internal/httpapi/router.go` — `NewRouter` | um `correlationId` por requisição, log com status verdadeiro |
| 2 | autenticação | `internal/infra/auth/middleware.go:48` — `Middleware` | identidade verificada por criptografia, `401` × `503` separados |
| 3 | autorização | `internal/infra/auth/middleware.go:119` — `RequireProvider` | só provedor autenticado entra na rota de provedor |
| 4 | provedor do token, não do corpo | `internal/httpapi/handlers.go:69` | isolamento entre provedores (eliminatório) |
| 5 | `Idempotency-Key` obrigatória | `handlers.go:79` | reenvio é reconhecível |
| 6 | validação e hash | `internal/app/wagering/payload.go:44` — `payload` + `service.go:121` — `hashDe` | equivalente HTTP × SQS |
| 7 | retry da transação inteira | `service.go:143` — `comRetentativa` | `55P03`/deadlock/`23505` não viram recusa |
| 8 | replay × conflito | `service.go:383` — `replay` | um débito por operação |
| 9 | lock da carteira | `service.go:251` + `wallet_repository.go:53` | sem *lost update* |
| 10 | instante depois do lock | `service.go:267` | ordem dos eventos da mesma carteira |
| 11 | `INSERT` da transação | `service.go:274` | todo desfecho tem onde ser gravado |
| 12 | referência | `internal/app/wagering/reference.go:46` | `PENDING_REFERENCE`, `REFERENCE_MISMATCH`, `ALREADY_REVERSED` |
| 13 | movimentação + ledger | `service.go:307`, `:314`, `:317` | saldo e lançamento no mesmo commit |
| 14 | estado final + eventos | `service.go:342`, `:345`, `:360` | desfecho auditável + outbox |
| 15 | commit | `uow.go:93` | atomicidade |
| 16 | status HTTP | `internal/httpapi/erros.go:35` — `statusDoResultado` | contrato distinguível |
| 17 | publicação | `internal/worker/publicador.go:190` — `PublicarPendentes` | evento sem perda, depois do commit |

Duas sutilezas dessa ordem que **não** são estilo:

- **o `INSERT` da transação vem antes de resolver a referência.** Todo desfecho —
  rejeição, espera, conclusão — é um **estado da linha**; sem a linha não há onde
  gravá-lo. O `INSERT` grava `PENDING` e o `Save` grava o definitivo **no mesmo
  commit**, então nenhuma outra transação chega a ver o `PENDING`;
- **o instante é lido depois do lock.** Ele vai para o `occurredAt` dos eventos, e
  os eventos da mesma carteira precisam sair na ordem dos efeitos confirmados. Se
  fosse lido antes do lock, a ordem publicada seria a de **início** das transações,
  e o `balanceAfter` de duas operações da mesma carteira poderia chegar invertido.

## 3.2 O fluxo SQS: a mesma regra, com inbox na mesma transação

Agora a mesma aposta chega pela fila. O que muda é **quem** monta a `Request`,
**qual** é a identidade durável da mensagem e **onde** o registro da inbox entra:

```
   PRODUTOR
      │  SendMessage → wager-transactions.fifo
      │  {"messageId":"msg-123","type":"WagerTransactionRequested",
      │   "occurredAt":"...","data":{... iguais aos do HTTP ...,
      │   "idempotencyKey":"provider-a:transaction-123"}}
      │  MessageGroupId = walletId · MessageDeduplicationId = explícito do produtor
      ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│ internal/consumer — a POLÍTICA de consumo (não conhece o domínio)            │
│   Receive (long polling 20 s) → semáforo SQS_CONSUMER_CONCURRENCY (8)        │
│   por mensagem: context.WithoutCancel → processamento sobrevive ao SIGTERM   │
└───────────────────────────────────┬──────────────────────────────────────────┘
                                    ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│ internal/app/inbox — TRADUÇÃO + DEDUPLICAÇÃO DURÁVEL                         │
│   1. hash do corpo (sha256)                                                  │
│   2. decodifica o envelope com a MESMA régua estrita do HTTP                 │
│      inválido → ErrMensagemInvalida (permanente, ANTES de abrir transação)   │
│   3. envelope.messageId, quando presente, tem de casar com o da entrega      │
│   4. correlationId ausente → usa o MessageId da ENTREGA                      │
│   5. monta a Request e chama ProcessWithHooks  ◄── a MESMA do HTTP           │
└───────────────────────────────────┬──────────────────────────────────────────┘
                                    ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│ uow.Run  ───────────────────────── BEGIN ──────────────────────────────────┐ │
│   OnBegin  (DENTRO da transação, antes de qualquer leitura de domínio)     │ │
│     SELECT inbox por (consumer_name='wager-transactions', message_id)      │ │
│       já PROCESSED/REJECTED + MESMO hash → conta reentrega + ErrIdempotent │ │
│                                            Skip  → commit de nada → replay  │ │
│       MESMO id + hash DIFERENTE          → ErrInboxBodyConflict (DLQ)      │ │
│       não existe                         → INSERT (status RECEIVED)        │ │
│   ... lock, saldo, ledger, transação, outbox (idêntico ao fluxo HTTP) ...  │ │
│   OnCommit (DENTRO da transação, depois do domínio, antes do COMMIT)       │ │
│     REJECTED → inbox REJECTED(failureCode)   ·   resto → inbox PROCESSED   │ │
│   ───────────────────────────────── COMMIT ──────────────────────────────┘ │
└───────────────────────────────────┬──────────────────────────────────────────┘
                                    ▼
                    ┌───────────────────────────────────┐
                    │ destino da mensagem               │
                    ├───────────────────────────────────┤
                    │ PROCESSED / replay      → Delete  │
                    │ REJECTED (negócio)      → Delete  │
                    │ PENDING_REFERENCE       → Delete  │  (estado é durável;
                    │                                    │   worker assume)
                    │ transitório / 23505     → adiar   │  ChangeMessageVisibility
                    │ inválida / corpo div.   → DLQ     │  Send + Delete
                    │ erro desconhecido       → adiar   │
                    └───────────────────────────────────┘
```

| # | Etapa | Onde | Por que está aí, e não em outro lugar |
| --- | --- | --- | --- |
| 1 | long polling + semáforo | `internal/consumer/consumer.go:164` — `Run` | sem semáforo, lotes de 10 × N instâncias estouram o pool |
| 2 | contexto do processamento | `consumer.go:255` — `WithCorrelationID(WithoutCancel(...))` | SIGTERM não pode abortar a transação em andamento |
| 3 | correlação = `MessageId` da entrega | `consumer.go:255`; `inbox/processor.go:174` | o envelope do README §10 não tem campo de correlação |
| 4 | hash do corpo | `inbox/processor.go:156` | distingue reentrega (mesmo hash) de conflito real |
| 5 | decodificação estrita | `inbox/processor.go:161` — `decodificar` | campo desconhecido é erro, igual ao HTTP (`TestDecodificarRejeitaMensagemInvalida`) |
| 6 | identidade durável | `inbox/processor.go:185` — `ganchos(idDaEntrega, …)` | é o broker quem atribui o `MessageId`; o produtor não o conhece antes do envio |
| 7 | inbox dentro da transação | `inbox/processor.go:212` — `ganchos` (`OnBegin`/`OnCommit`) | README §6.5: registro e conclusão compartilham a transação do domínio |
| 8 | classificação do erro | `inbox/processor.go:282` — `classificarErro` | transitório volta, permanente vai para a DLQ |
| 9 | destino da mensagem | `consumer.go:307` — `confirmar`; `:392` — `adiar`; `:428` — `paraDLQ` | remover só depois do commit |
| 10 | backoff | `consumer.go:544` — `backoffSegundos` | exponencial, com teto e jitter **dentro** do teto |
| 11 | encerramento | `consumer.go:464` — `encerrar`; `:498` — `liberarVisibilidade` | para de buscar, espera o que está em voo, devolve o resto |

**Por que os ganchos, e não "grava a inbox, depois processa".** Entre as duas
transações existiria uma janela em que uma queda deixa o débito feito **sem**
registro (ou o registro sem o débito). A única forma de garantir o que o README
§6.5 pede é o registro acontecer **dentro** do `uow.Run` da operação — é o que
`ProcessHooks` faz (`internal/app/wagering/hooks.go:24`). E o HTTP não paga nada
por isso: `Process(ctx, req)` é literalmente
`ProcessWithHooks(ctx, req, nil)` (`service.go:76`).

**A régua de destino, em uma frase.** Remove quando o trabalho durável terminou
(inclusive rejeição de negócio e pendência, que são estados gravados); adia quando
repetir pode resolver (infraestrutura, conflito de unicidade, erro desconhecido);
manda para a DLQ quando repetir **nunca** vai resolver (corpo inválido, corpo
divergente para a mesma mensagem, entrada inválida).

## 3.3 Onde os dois fluxos se encontram

A única diferença de regra entre as portas é o **registro da inbox**. Tudo o mais
— hash, idempotência, lock, ledger, outbox, códigos de falha — é o mesmo código,
no mesmo arquivo. É por isso que existem os testes que enviam a **mesma** operação
pelas duas portas: `TestSQSESHTTPCompartilhamAIdempotencia` (sequencial) e
`TestSQSConcorrenteComHTTPNaoDuplicaDebito` (simultâneo). Se um dia alguém
"otimizar" o caminho SQS com uma implementação paralela, esses dois testes são a
rede que pega.

---

# 4. Cada componente e por que existe

Um pacote que existe "por organização" é dívida: ninguém sabe se pode apagá-lo.
Abaixo, cada pacote tem **o que faz** e **o que aconteceria se não existisse** — a
segunda parte é a que importa quando alguém propõe simplificar.

## 4.1 `internal/domain/money` — o valor monetário

**O que faz.** Define `Currency` (código ISO 4217) e `Money` (valor em unidades
mínimas + moeda), com criação validada (`New`, `Parse`, `Zero`), reidratação sem
validação (`FromMinor`), aritmética com guarda de overflow (`Add`, `Sub`, `Neg`),
comparação por moeda (`Cmp`, `Equal`, `Sign`) e serialização em string
(`MarshalJSON`/`UnmarshalJSON`).

**Sem ele**, cada camada inventaria seu jeito de representar dinheiro: um
`float64` no DTO, um `int` no banco, uma string no log. O erro apareceria como
centavo perdido na conciliação, e não haveria um lugar único para corrigir.

**Detalhe que vale saber:** `money` implementa o próprio contrato de JSON
(`MarshalJSON`/`UnmarshalJSON`), mas o domínio de `wagering` **não** serializa o
payload sozinho — ele recebe um `PayloadEncoder` injetado (`codificarPayload`, em
`internal/app/wagering/payload.go:64`). A escolha de **como** serializar (e a
garantia de determinismo) fica na plataforma, e o agregado serve HTTP, SQS e teste
sem arrastar uma decisão de formato para dentro da regra de negócio
(`ARCHITECTURE.md` §2).

## 4.2 `internal/domain/wallet` — a carteira

**O que faz.** Encapsula saldo, versão e instantes; expõe `NewWallet` (criação com
validação), `Rehydrate` (leitura sem reaplicar nada), `Debit`/`Credit` (que
devolvem um `Movement` com o lançamento **e** a versão anterior) e
`apply` — o **único** ponto do sistema que muda saldo.

**Sem ele**, o saldo viraria um campo público que qualquer handler poderia alterar,
e a invariante "nunca negativo" dependeria de disciplina. Com ele, `Debit` recusa
o débito que ficaria negativo **antes** de qualquer escrita (`wallet.go:200`), e o
banco recusa de novo por CHECK — as duas camadas.

## 4.3 `internal/domain/ledger` — o lançamento contábil

**O que faz.** Define `Entry` (imutável, sem setters), `Direction`, `NewEntry` (que
confere `after = before ± amount` e exige valor positivo) e `RehydrateEntry`.

**Sem ele**, a "prova" do saldo seria o próprio saldo — e saldo não conta como
chegou ali. Sem lançamento, não existe reconciliação, não existe auditoria e não
existe resposta para "por que o saldo mudou?".

## 4.4 `internal/domain/wagering` — a operação e sua máquina de estados

**O que faz.** Define `Kind` (os seis tipos, com as propriedades `MovesMoney`,
`RequiresReference`, `AllowsZeroAmount`, `ValidateAmount`), `Status` (os cinco
estados e a **tabela** de transições), `WagerTransaction` (o agregado, com
`New`/`Rehydrate`, `MarkProcessed`, `MarkRejected`, `MarkFailed`,
`MarkPendingReference`, `ResolveReference`, `ReagendarReferencia`) e os
construtores de evento (`ProcessedEvent`, `RejectedEvent`,
`PendingReferenceEvent`, `NewBalanceChangedEvent`).

**Sem ele**, a regra "estado terminal não transiciona" viraria uma sequência de
`if`s espalhados pelo serviço, e o primeiro `if` esquecido reprocessaria uma
operação concluída. A tabela de transições é explícita
(`status.go:52` — `transicoes`) e a tentativa inválida devolve erro **tipado**:
`ErrTerminalState` (estado final) ou `ErrInvalidTransition` (fora de ordem) — dois
erros diferentes porque as causas são diferentes (`status.go:73`).

## 4.5 `internal/app/ports` — os contratos entre aplicação e infraestrutura

**O que faz.** Declara as interfaces que a aplicação usa (`UnitOfWork`, `Repos`,
repositórios, `Clock`, `ConflictCounter`), as sentinelas de erro
(`ErrNotFound`, `ErrConflict`, `ErrTransient`, `ErrCheck`, `ErrForeignKey`,
`ErrPermanent`), os classificadores (`IsNotFound`, `IsConflict`, `IsRetryable`), o
`Cursor` opaco da paginação e os tipos de mensagem/evento.

**Sem ele**, a aplicação precisaria importar `internal/infra/postgres` só para
nomear o tipo dos repositórios — e trocar de banco obrigaria a mexer na regra de
negócio. Além disso, os testes de unidade do caso de uso não poderiam rodar sem
banco.

**O detalhe que já causou defeito:** as sentinelas são os **mesmos objetos** dos
dois lados (`postgres.ErrConflict` é **alias** de `ports.ErrConflict`,
`internal/infra/postgres/errors.go:42`). Se fossem dois jogos de objetos com o
mesmo nome, `ports.IsConflict(erroDoRepositorio)` devolveria `false` — conflito de
idempotência seria tratado como falha desconhecida, sem erro de compilação e sem
teste vermelho (defeito 11). Há teste de regressão:
`TestSentinelasSaoOsMesmosObjetos`, `TestClassificadoresDoContratoReconhecemErroDoDriver`.

## 4.6 `internal/app/wagering` — o caso de uso

**O que faz.** É o coração. `Process`/`ProcessWithHooks` (operação externa ponta a
ponta), `OpenWallet` (abertura interna), `Retomar` (retomada de pendência), as
leituras (`GetTransaction`, `GetProviderTransaction`, `GetByProviderExternalID`,
`GetWallet`, `ListLedger`) e `Reconcile` — todas em transação, as de leitura em
transação somente-leitura.

**Sem ele**, essa ordem (hash → replay → lock → referência → movimento → ledger →
estado → outbox → commit) estaria no handler HTTP **e** no consumidor, em duas
versões que divergiriam no primeiro ajuste. É o pacote que garante o "mesma regra,
duas portas" do README §10.

**O que ele deliberadamente não faz:** não conhece HTTP (não sabe o que é `201`),
não conhece SQS (não sabe o que é `Delete`) e não conhece Prometheus (conta
conflito por uma interface de um método, `ports.ConflictCounter`, `service.go:162`).

## 4.7 `internal/infra/postgres` — a persistência e a transação

**O que faz.** `NewPool` (pgxpool com `statement_timeout`, `lock_timeout`, UTC e
`application_name`), `uow.Run` (transação de escrita, `READ COMMITTED`) e
`uow.RunReadOnly` (transação de leitura, `REPEATABLE READ` + read-only), os cinco
repositórios (carteira, transação, ledger, inbox, outbox), a classificação de erro
do driver (`Classify`) e o health checker.

**Sem ele**, não haveria onde delimitar "uma transação por operação" — e sem isso
não existe atomicidade entre saldo, ledger e evento. Cada repositório recebe o
`querier` da transação corrente (`internal/infra/postgres/scan.go` e os `New*Repo`),
então **dois `Run` simultâneos nunca compartilham conexão**.

**Uma decisão que parece detalhe e não é:** os repositórios são criados **por
transação** (`uow.go:79`). Um repositório que guardasse o pool compartilhado
poderia executar a query fora da transação — e a atomicidade sumiria em silêncio,
sem erro e sem teste vermelho.

## 4.8 `internal/infra/sqs` — o protocolo do broker

**O que faz.** `Cliente` (Receive, Delete, ChangeVisibility, Send, Ping),
`ConsumidorFila` (adapta o cliente à porta `consumer.Fila`) e
`PublicadorEventos` (publica um `worker.Evento` na fila de eventos, com
`MessageGroupId` = agregado, `MessageDeduplicationId` por tentativa e
`MessageAttributes` de `eventType`/`eventId`).

**Sem ele**, a política de consumo (quando repetir, quando ir para a DLQ) ficaria
misturada com o SDK da AWS — e não daria para testar a política sem LocalStack. A
divisão é: **o adaptador sabe receber e apagar; ele não sabe que falha de banco
merece retry** (`internal/consumer/consumer.go:4`).

## 4.9 `internal/infra/auth` — OIDC sem dependência externa

**O que faz.** Valida JWT RS256 com biblioteca padrão: descobre o `jwks_uri` pelo
discovery, busca e cacheia o JWKS (com TTL e rebusca por `kid` desconhecido),
verifica a assinatura com `crypto/rsa` e confere `iss`, `aud`, `exp`, `nbf`
(`internal/infra/auth/verifier.go`). Expõe `Middleware`, `RequireRole` e
`RequireProvider` (`middleware.go`) e um health checker próprio
(`internal/infra/auth/health.go`).

**Sem ele**, não haveria autenticação — que é item eliminatório — e o
`providerId` teria de vir do corpo, entregando o isolamento entre provedores ao
cliente.

**A decisão mais importante do pacote:** o algoritmo é decidido **pelo serviço**
(constante `RS256`), nunca pelo header do token. Confiar no `alg` é a
vulnerabilidade clássica (`alg: none`, `alg: HS256` assinado com a chave pública,
`jku`/`x5u` apontando para outro host). O `kid` é obrigatório, e o `issuer`
anunciado no discovery tem de bater com o configurado (`ARCHITECTURE.md` §10).

## 4.10 `internal/consumer` — a política de consumo

**O que faz.** O laço de long polling com semáforo, o processamento por goroutine
com `context.WithoutCancel`, a decisão de destino (remover, adiar, DLQ) e o
encerramento com prazo. Fala com o broker pela porta `Fila` e com o domínio pela
porta `Processador`.

**Sem ele**, "tentar de novo" e "desistir" seriam decisões espalhadas. Errar essa
classificação é caro nos dois sentidos: mandar erro permanente para retry infinito
**trava a fila** (numa FIFO, uma mensagem em voo bloqueia o grupo inteiro), e
mandar transitório para a DLQ **perde trabalho** que só precisava de nova
tentativa.

## 4.11 `internal/app/inbox` — a tradução e a deduplicação

**O que faz.** Converte a mensagem do broker na **mesma** `Request` do HTTP
(`decodificar`), calcula o hash do corpo, registra e conclui a inbox **dentro** da
transação da operação (via `ProcessHooks`) e classifica o erro em transitório
versus permanente.

**Sem ele**, o consumidor teria de conhecer o domínio (e passaria a duplicar
regra), ou a inbox ficaria numa transação separada — e a queda entre as duas
deixaria débito sem registro. `ConsumerName` (constante `"wager-transactions"`)
existe porque a unicidade é `(consumer_name, message_id)`: dois consumidores
diferentes podem receber o mesmo identificador de mensagem e cada um precisa do seu
registro (`inbox/processor.go:33`).

## 4.12 `internal/worker` — os laços de fundo

**O que faz.** Quatro peças:

| Peça | Papel |
| --- | --- |
| `OutboxPublisher` | reivindica lote com lease, publica, marca publicado; em falha, `MarkRetry` com backoff; esgotou → **quarentena** |
| `ReferenceWorker` | reivindica pendências vencidas e chama `Service.Retomar` |
| `PendingReaper` | varre `PENDING` antigo e chama o **mesmo** `Retomar` |
| `Runner` | sobe os laços, reinicia o que morreu com backoff, expõe readiness e encerra com prazo |

**Sem ele**, o evento confirmado ficaria para sempre na outbox (o README §3 proíbe
perder evento confirmado) e a reversão que chegou antes ficaria pendente para
sempre (o README §7 exige retomada durável, inclusive após reinício).

## 4.13 `internal/httpapi` — a borda

**O que faz.** Roteador `chi` com a ordem de middlewares
`RequestID → RealIP → AccessLog → Recoverer → Timeout → MaxBody`; as rotas do
enunciado; DTOs de entrada e saída; a tabela desfecho→status
(`erros.go:35`) e erro→status (`erros.go:100`); health e `/metrics`.

**Sem ele**, o contrato HTTP — `201` × `200` × `202` × `422` × `409` × `503` —
seria decidido dentro do caso de uso, e o caso de uso passaria a conhecer HTTP (e
o consumidor SQS herdaria a confusão).

**Dois detalhes que custaram defeito:** o `AccessLog` tem de envolver o
`Recoverer` (senão o log registra `200` para uma requisição que respondeu `500`,
defeito 21); e a rota de negócio **nunca** é registrada sem autenticador — se
`h.auth == nil`, `RegistrarRotas` volta sem montar nada (`routes.go:28`), falhando
**fechado** (404), porque registrar endpoint financeiro sem identidade é
eliminatório.

## 4.14 `internal/app` — a composição Fx

**O que faz.** Monta o grafo (`ModuleOptions`) com os módulos `config`, `infra`,
`usecase`, `auth`, `worker`, `workers` e `httpapi`; registra ciclo de vida
(`runHTTPServer`, `runConsumidor`, `runWorkers`, `fecharPoolNoShutdown`);
`ValidateApp` valida o grafo sem subir nada; `Run` carrega a config, monta o Fx,
espera sinal e encerra com prazo.

**Sem ele**, cada `main` teria de saber a ordem de construção e de destruição — e a
ordem é o que garante que o pool só fecha **depois** dos componentes que o usam
(o Fx executa `OnStop` na ordem inversa do registro; o servidor HTTP é registrado
antes, então para primeiro).

**Três armadilhas do Fx que apareceram aqui e valem para quem mexer:** `Provide`
recebe **construtor**, não valor (`clock.System{}` é valor); grupo é
`fx.Annotate(fn, fx.ResultTags(...))`, e dentro de uma struct `fx.In` o `group` é do
**campo** (um `fx.In` não pode ser anotado com `ParamTags`); e o pool recebe
`context.Background()` com prazo, **não** o contexto de start — que é cancelado no
fim do boot e derrubaria o pool no primeiro `Start` (`internal/app/modules.go:28`).

## 4.15 `internal/observability` — log e métricas

**O que faz.** `NewLogger` (JSON em produção, texto em `APP_ENV=local`, **um**
logger por processo), o handler de contexto que injeta `correlationId`/`requestId`
em toda linha, redação de dado sensível (`Redact`), e as 12 métricas Prometheus
com prefixo `jungleg_` (`internal/observability/metrics.go`).

**Sem ele**, um incidente não teria rastro: você saberia que "algo falhou", não
*qual* transação, *qual* carteira, *qual* provedor. E as métricas de negócio não
existiriam — inclusive a mais importante, `outbox_pending_age_seconds`, que é como
se descobre que um evento confirmado não foi publicado.

**A regra de cardinalidade** está em comentário **e** em teste: label só com valor
de baixa cardinalidade (`kind`, `status`, `stage`, `result`); `walletId` como label
reprova (`TestMetricasSemLabelsDeAltaCardinalidade`).

## 4.16 `internal/config` — a configuração validada

**O que faz.** Carrega tudo por variável de ambiente (com prefixos `HTTP_`,
`DB_`, `OIDC_`, `SQS_`, `OUTBOX_`, `REFERENCE_`, `WORKERS_`), aplica defaults e
roda `Validate` **antes** de abrir qualquer conexão (`config.Load` devolve erro e o
Fx aborta o boot).

**Sem ele**, um serviço financeiro subiria "mais ou menos", com metade dos
parâmetros, e o erro apareceria na primeira operação real.

**A validação que já mordeu:** `HTTP_STOP_TIMEOUT` precisa **cobrir** o
`HTTP_SHUTDOWN_TIMEOUT` mais o prazo do consumidor mais o dos workers; com a ordem
invertida, o encerramento mataria o servidor no meio da drenagem. O default
anterior (25 s) violava a própria regra e, com o consumidor ligado, a aplicação
**não subia** (defeito 33). Hoje o default é 50 s e há testes dos dois lados
(`TestLoadRejeitaStopTimeoutMenorQueShutdown`,
`TestLoadComTudoLigadoUsaDefaultsValidos`).

## 4.17 O que sobrou (e por que também existe)

| Pacote / diretório | Papel | Se não existisse |
| --- | --- | --- |
| `internal/domain/errors` | um único `Error{Code, Message}` com `errors.Is`/`errors.As` e `CodeOf` | `money`, `wallet`, `ledger` e `wagering` teriam cópias que divergem; `CodeOf` deixaria de atravessar a cadeia e o `failureCode` sairia vazio (defeito 13) |
| `internal/domain/ids` | `WalletID`, `PlayerID`, `TransactionID` | `wallet` e `ledger` formariam ciclo de import; `TransactionID` duplicado exigiria conversão em todo lugar (defeito 12) |
| `internal/platform/canonicaljson` | JSON determinístico + `Hash` | o hash de idempotência dependeria da ordem dos campos, e o mesmo pedido viraria duas operações |
| `internal/platform/clock` | relógio em UTC injetável | o teste não conseguiria afirmar instantes; o domínio importaria `time.Now` |
| `internal/platform/idgen` | UUID v7 | o teste compararia UUIDs aleatórios; e o índice primário ficaria menos denso que com v7 |
| `cmd/api`, `cmd/migrate` | entradas do serviço e das migrations | não haveria binário; e as migrations (embutidas com `go:embed`) precisam de um `up`/`down`/`version` |
| `migrations/` | schema versionado (5 migrations) | o banco seria criado à mão e "a verdade" não estaria no repositório |
| `test/integration` | o caminho completo contra PostgreSQL, Keycloak e LocalStack reais | a prova multi-processo (T1–T8) não existiria |
| `deploy/` | init do Postgres, filas do LocalStack, realm do Keycloak, Prometheus/alertas | `docker compose up --build` não entregaria ambiente funcional |

---

# 5. Onde cada garantia é imposta

Este é o capítulo que você vai consultar quando alguém perguntar "e se alguém
escrever direto no banco?" ou "e se o lock falhar?". A estrutura é sempre a mesma:
**garantia → camada → arquivo → teste que prova**. Uma garantia de dinheiro
confiável aparece em mais de uma camada de propósito: a primeira dá mensagem boa, a
última sobrevive a bug de aplicação.

## 5.1 As oito garantias obrigatórias (README §5)

| # | Garantia | Camada(s) | Onde é imposta | Teste que prova |
| --- | --- | --- | --- | --- |
| 1 | dinheiro nunca passa por `float32`/`float64` — nem em parsing, cálculo, serialização ou persistência | domínio + contrato + schema | `internal/domain/money/money.go:63` (`minor int64`); `:205` (`moneyDTO.Amount string`); `:349` (`parseAmount` acumula **dígitos**, nunca converte para float); `migrations/000001_init.up.sql:44` (`balance_minor BIGINT`); `:199` (`amount_minor BIGINT`) | `TestSemPontoFlutuanteNoCodigo` (varre a AST e reprova literal `float`, identificador `float64`, `ParseFloat`, `NaN`, `Inf`), `TestMoneySoUsaInt64`, `TestMarshalJSONNuncaUsaNumero`, `TestUnmarshalJSONRejeitaNumeroAntesDoDominio` |
| 2 | idempotência **persistente**, sobrevive ao reinício de todos os processos | índice único (schema) + decisão de aplicação | `migrations/000001_init.up.sql:161` (`UNIQUE (provider_id, idempotency_key)`); `:166` (`UNIQUE (provider_id, external_transaction_id)`); `:177` (`UNIQUE (idempotency_key) WHERE kind='OPENING'`); `internal/app/wagering/service.go:383` (`replay`) | `TestIdempotenciaSobreviveAProcessoNovo` (serviço e Unit of Work **novos** devolvem o resultado original); `TestT8ReinicioPreservaIdempotenciaPendenciaEConsistencia` (processo novo); `TestMesmaChaveComConteudoDiferenteEConflito` |
| 3 | invariantes financeiras garantidas **no banco**, independentes de lock local e da deduplicação do SQS FIFO | schema | as 29 `CHECK`, 4 `FK` e 2 `UNIQUE` de `migrations/000001_init.up.sql` (`:53`, `:212`, `:227`), mais os índices únicos parciais de idempotência — por provedor+chave (`:161`), por identidade externa (`:166`), por referência+tipo (`:172`) e por chave de `OPENING` (`:177`) — e os 5 triggers (`:231`, `:305`–`:318`) | `TestT1MesmaOperacao50VezesPelaFila` — **50 entregas distintas** (a inbox não pode deduplicar) e **um** débito: quem barrou foi a chave financeira + índice; `TestWalletSaldoNegativoRecusadoPeloBanco`; `TestCommitGravaTudoJunto` |
| 4 | evento externo só é publicado **depois** da confirmação da transação que o originou | transação + processo | `internal/app/wagering/events.go:98` (`enfileirar` grava **dentro** do `uow.Run`); `internal/infra/postgres/outbox_repository.go:85` (`ClaimBatch` só enxerga o que commitou); `internal/worker/publicador.go:190` (`PublicarPendentes`) | `TestFalhaNaOutboxDesfazTudoNoBancoReal` e `TestOpenWalletComOutboxQuebradaNaoDeixaNemCarteira` (o evento falha → **nada** fica); `TestOutboxPublicaNaFilaDeEventos`; `TestPublicadorPublicaAntesDeMarcar` |
| 5 | ledger **append-only** — correção exige lançamento novo | schema + repositório + domínio | `migrations/000001_init.up.sql:29` (`prevent_ledger_mutation`), `:231` (trigger `BEFORE UPDATE OR DELETE`); `internal/domain/ledger/entry.go:110` (`Entry` sem setters); `internal/infra/postgres/ledger_repository.go` (só `Insert`) | `TestLedgerImutavelNoBanco` (UPDATE e DELETE recusados), `TestClassifyImutabilidadeDoLedger` (`23001` → `ErrCheck`), blocos do `deploy/postgres/verify_constraints.sql` |
| 6 | carteiras independentes avançam em paralelo; **lock global é proibido** | transação | `internal/infra/postgres/wallet_repository.go:53` (`GetByIDForUpdate`: `WHERE id = $1 FOR UPDATE`); `internal/app/wagering/service.go:251` | `TestT3CarteirasDistintasEmParaleloSemLockGlobal` (com a linha de A presa, B..F concluem em 14–34 ms e **nenhum** backend espera lock; A conclui 408 ms depois da liberação); `TestWalletGetByIDForUpdateBloqueiaOutraTransacao`; `TestTresInstanciasIndependentes` |
| 7 | atualização de saldo impede *lost update* | transação + versão | `internal/infra/postgres/wallet_repository.go:92` (`UPDATE ... SET balance_minor = $2, version = version + 1 WHERE id = $1 AND version = $3`); `service.go:314` usa `mov.VersionBefore()` | `TestWalletLostUpdateNaoAcontece` (1000 − 100 − 200 = **700,00**, versão **3**); `TestWalletUpdateComVersaoErrada`; `TestT2DuasApostasDe80Sobre100EmInstanciasDiferentes` (20,00 e versão 2) |
| 8 | unicidade, não negatividade e imutabilidade do ledger impostas pelo **schema**, constraints e mecanismos do banco | schema | `wallets_balance_non_negative` (`000001:53`), `wallets_version_positive` (`:60`), `ledger_balance_consistency` (`:212`), `ledger_wallet_transaction_key` (`:227`), `wallets_player_currency_key` (`:50`), `inbox_messages_consumer_message` (`:269`), trigger de imutabilidade (`:231`) | `deploy/postgres/verify_constraints.sql` — **32 operações inválidas** testadas, todas recusadas, **mais um caso positivo** que confirma que o afrouxamento de jogo/rodada vale só para `OPENING`; `TestWalletUnicidadeDePlayerEMoeda`; `TestLedgerUnicoPorTransacao`; `TestReversaoDuplicadaBarradaPeloIndiceDoBanco`; `TestTxnOpeningIdempotencia` |

> **Nuance honesta sobre a garantia 1.** `float64` **existe** em
> `internal/observability` (duração de operação em log e os buckets do histograma
> do Prometheus). Não é caminho monetário: nenhum valor de dinheiro é convertido
> ali. A regra correta é "zero float nos caminhos monetários", e não "zero float em
> `internal/`" — está registrada em `docs/CONFORMIDADE_README.md` §5.

## 5.2 As garantias de recuperação (README §3 e §13)

O enunciado não pede só que o sistema esteja certo em regime; ele pede que
**continue** certo nas falhas. Cada linha abaixo é uma falha, o mecanismo que a
absorve e o teste que a reproduz.

| Falha | Mecanismo | Teste que prova |
| --- | --- | --- |
| queda no meio da transação | `uow.Run` faz `rollback` por `defer`, com `context.WithoutCancel` + 5 s (`internal/infra/postgres/uow.go:71`) | `TestRollbackNaoDeixaResiduo` |
| falha no **último** passo (gravar o evento) | o commit não acontece: saldo, ledger, transação e evento voltam juntos | `TestFalhaNaOutboxDesfazTudoNoBancoReal`, `TestOpenWalletComOutboxQuebradaNaoDeixaNemCarteira` |
| reenvio simultâneo da mesma operação | um `INSERT` ganha; o outro recebe `23505`, **repete a transação inteira** e devolve replay ou conflito (`service.go:143`, `:274`) | `TestMesmaApostaEmParaleloProcessaUmaVez`, `TestConflitoDeUnicidadeERepetidoEContado` |
| disputa de lock na mesma carteira (`55P03`) | classificado **transitório** (`internal/infra/postgres/errors.go:88`) e repetido até 3 vezes; esgotado sobe como indisponibilidade (`503` + `Retry-After`) | `TestClassifyTransitorios`, `TestFalhaTransitoriaERepetida`, `TestRetentativaEsgotadaDevolveErroTransitorio`, `TestFalhaDeInfraestruturaNuncaEh4xx` |
| queda **depois do commit, antes de publicar** | o evento fica pendente na outbox; o lease vence e **outra instância** publica | `TestQuedaEntreCommitEPublicacaoOutraInstanciaAssume` |
| queda **depois de publicar, antes de marcar** | republicação com o **mesmo `eventId`** e o **mesmo corpo**; o consumidor deduplica | `TestQuedaEntrePublicacaoEMarkPublishedRepublicaComOMesmoEvento`, `TestOutboxRepublicacaoPreservaOEvento` |
| dois publishers na mesma outbox | `FOR UPDATE SKIP LOCKED` dá lotes disjuntos (`outbox_repository.go:121`) | `TestDoisPublishersDisputamAMesmaOutbox`, `TestT6TresPublishersReaisDisputamAMesmaOutbox` |
| mensagem reentregue pelo SQS | inbox recusa a segunda por `(consumer_name, message_id)`; o `body_hash` distingue reentrega de conflito | `TestSQSReentregaDaMesmaMensagemNaoReaplica`, `TestProcessarReentregaNaoReaplica` |
| queda **depois do commit, antes do delete** | a mensagem volta; a instância nova a recebe, a inbox a reconhece (`attempts` 1→2) e **nenhum débito novo** acontece | `TestT5QuedaEntreCommitEDeleteReentregaReconhecida` + a prova de SIGTERM no container (`docs/evidencias/FASE_10.md` §6) |
| referência que **nunca** chega | `PENDING_REFERENCE` é durável; `REFERENCE_MAX_ATTEMPTS`/`REFERENCE_PENDING_MAX_AGE` esgotam → `REJECTED` com `REFERENCE_NOT_FOUND` + evento | `TestReferenciaQueNuncaChegaExpiraEmREFERENCENOTFOUND`, `TestRetomadaExpiraEmREFERENCENOTFOUND`, `TestRetomadaExpiraPelaIdadeDaPendencia` |
| **reversão antes da referência** | fica `PENDING_REFERENCE` sem movimentar nada; quando a referência chega, o worker resolve com o ledger correto | `TestREFUNDAntesDaReferenciaResolveDepois`, `TestREFUNDAntesDaReferenciaFicaPendenteEDuravel`, `TestT7REFUNDAntesDaReferenciaResolvidoPorOutraInstancia` |
| reinício da aplicação | tentativas, instantes e pendências são **colunas de banco**; nada vive em memória | `TestT8ReinicioPreservaIdempotenciaPendenciaEConsistencia`, `TestIdempotenciaSobreviveAProcessoNovo` |
| `PENDING` antigo sem desfecho | o reaper varre `PENDING` com `updated_at` velho e chama o mesmo `Retomar` (`internal/worker/referencias.go:326` — `Varrer`) | `TestReaperRetomaPendingAntigo`, `TestReaperRetomaPresaEmPending` |
| `SIGTERM` com trabalho em voo | para de buscar, espera `SQS_SHUTDOWN_GRACE`, libera a visibilidade do que sobrou (`consumer.go:498`) | `TestEncerramentoLiberaVisibilidade`, `TestRunnerEsperaOTrabalhoEmVooNoEncerramento`, prova no container |
| saldo que não fecha com o ledger | reconciliação em transação somente-leitura de visão única; divergência vira log + métrica, **sem alterar o saldo** | `TestReconcileFechaOExemploDoREADME`, `TestReconcileDetectaSaldoAdulteradoESeRecusaAMexer`, `TestTransacaoSomenteLeituraRecusaEscrita` |

## 5.3 O banco como última barreira: o que o schema recusa

O script `deploy/postgres/verify_constraints.sql` é a parte do repositório que
responde "e se a aplicação tiver um bug?". Ele **tenta** 32 operações inválidas e
exige que todas falhem; se alguma passar, o script levanta exceção. Alguns
exemplos, com a constraint que recusa:

| Operação tentada | Quem recusa |
| --- | --- |
| carteira com saldo `-1` | `wallets_balance_non_negative` (`000001:53`) |
| carteira com saldo acima de 9 × 10^15 | `wallets_balance_within_limit` (`000001:57`) |
| carteira com `version = 0` | `wallets_version_positive` (`000001:60`) |
| duas carteiras para o mesmo `(player_id, currency)` | `wallets_player_currency_key` (`000001:50`) |
| moeda `'brl'` ou `'BR'` | `wallets_currency_iso4217` (`000001:63`) |
| lançamento cuja aritmética não fecha (`after ≠ before ± amount`) | `ledger_balance_consistency` (`000001:212`) |
| lançamento com valor `0` | `ledger_amount_positive` (`000001:208`) |
| dois lançamentos para a mesma operação | `ledger_wallet_transaction_key` (`000001:227`) |
| `UPDATE` ou `DELETE` no ledger | trigger `wallet_ledger_entries_append_only` (`000001:231`) |
| `OPENING` com `provider_id` | `wager_transactions_opening_is_internal` (`000001:132`) |
| `OPENING` com jogo ou rodada (migration 000002) | `wager_transactions_external_has_provider` / `ids_not_blank` |
| operação externa **sem** jogo/rodada | `wager_transactions_ids_not_blank` (`000001:148`) |
| `REFUND`/`ROLLBACK` sem referência externa | `wager_transactions_reversal_requires_reference` (`000001:142`) |
| `LOSS` com valor > 0, ou `BET` com valor 0 | `wager_transactions_amount_policy` (`000001:117`) |
| `REJECTED` sem `failure_code` | `wager_transactions_terminal_has_failure_code` (`000001:154`) |
| duas `OPENING` com a mesma chave | índice parcial `wager_transactions_opening_idempotency_key` (`000001:177`) |
| duas reversões do mesmo tipo sobre a mesma referência | índice parcial `wager_transactions_reference_kind` (`000001:172`) |
| mensagem repetida do mesmo consumidor | `inbox_messages_consumer_message` (`000001:269`) |
| `sequence` da outbox escrita à mão | `GENERATED ALWAYS AS IDENTITY` (migration 000003) |

**Como rodar a verificação** (o `<` precisa do `cmd.exe` no PowerShell 5.1):

```powershell
cmd /c "docker exec -i jungleg-postgres psql -U jungleg -d jungleg -v ON_ERROR_STOP=1 < deploy\postgres\verify_constraints.sql"
```

## 5.4 Se você quiser mudar uma dessas garantias, aqui está o custo

| Se você mexer em… | Você também precisa mexer em… | Senão |
| --- | --- | --- |
| escala do dinheiro (`2` → `3`) | `parseAmount`, `String`, `moneyDTO`, todas as constraints de limite, o CHECK de consistência do ledger, o parser de cursor e a reconciliação | o banco recusa o que o domínio aceita (ou pior: aceita valor truncado) |
| limite `maxMinor` | `CHECK` de `wallets`, `wager_transactions` e `wallet_ledger_entries` | uma escrita válida no domínio passa a ser recusada pelo banco, com `23514` |
| nível de isolamento (`READ COMMITTED` → `SERIALIZABLE`) | a política de retry (`comRetentativa`) e os testes de concorrência | retry em quase todo caminho de escrita e abortos de transações **sem** conflito real |
| chave da outbox (`sequence` → `occurred_at`) | a ordem por carteira e o `MessageGroupId` | `WalletBalanceChanged` pode sair antes do `WagerTransactionProcessed` da mesma operação (defeito 19) |
| semântica do `23505` | o retry do caso de uso e o teste de conflito | um reenvio legítimo vira `500` em vez de replay |

---

# 6. As regras de negócio e o porquê

Regra sem motivo é regra que alguém vai "corrigir" na primeira semana. Cada seção
abaixo responde: qual é a regra, onde ela está escrita e o que aconteceria de
errado se ela não existisse.

## 6.1 Por que `LOSS` não movimenta dinheiro — e mesmo assim produz evento

`LOSS` é a informação de que a rodada terminou e o jogador perdeu **o que já tinha
apostado**. O dinheiro saiu na `BET`; a `LOSS` não tem valor a mover. Por isso o
enunciado exige `money.amount` igual a `"0.00"` e o domínio recusa qualquer outro
valor (`wagerdomain.Kind.ValidateAmount`, `kind.go:66`; CHECK
`wager_transactions_amount_policy`, `migrations/000001_init.up.sql:117`).

Consequências, todas verificáveis:

- **não** cria lançamento no ledger (valor zero não é movimentação — a mesma regra
  de `wallet.apply`, `wallet.go:187`);
- **não** incrementa a versão da carteira;
- **não** emite `WalletBalanceChanged`;
- **emite** `WagerTransactionProcessed`, porque o provedor precisa da confirmação
  (`internal/app/wagering/events.go:44` — se `entrada == nil`, só o `Processed` vai
  para a outbox).

A frase que resume: **"não movimenta dinheiro" não é "não aconteceu nada"**.
Confundir as duas coisas deixaria o provedor sem resposta para uma operação que
existiu. Provas: `TestLOSSNaoMovimentaMasProduzEvento` (unidade),
`TestLOSSNaoCriaLedgerNemMudaVersao`, `TestLOSSProduzEventoProcessedSemSaldo`.

## 6.2 Por que o `ROLLBACK` de um `REFUND` **debita**

`ROLLBACK` não tem direção própria: ele faz o **contrário do movimento que desfaz**
(`internal/app/wagering/reference.go:183` — `direcaoDoMovimento`,
`:199` — `direcaoContraria`):

| Tipo referenciado | Movimento original | `ROLLBACK` faz |
| --- | --- | --- |
| `BET` | débito | **crédito** (devolve a aposta) |
| `WIN` | crédito | **débito** (tira o prêmio) |
| `REFUND` | crédito | **débito** (desfaz o estorno) |

Se o `ROLLBACK` de um `REFUND` creditasse, ele **criaria dinheiro**: o estorno já
tinha devolvido o valor da aposta, e um crédito em cima disso pagaria duas vezes o
mesmo débito. O caso concreto está em `TestROLLBACKDesfazBETeDesfazEstorno` e em
`TestROLLBACKDesfazEstornoSemCriarDinheiro`: abertura 100,00 → `BET` 40,00 (saldo
60,00) → `REFUND` 40,00 (saldo 100,00) → `ROLLBACK` do `REFUND` 40,00 (saldo
**60,00**, com três lançamentos e sem dinheiro novo).

## 6.3 Por que o replay devolve o saldo **original**

Existem duas coisas diferentes com nome parecido:

- o **replay de uma operação** devolve o **resultado daquela operação** — um fato
  imutável: "aquela aposta debitou 25,00 e deixou 975,00". Se a carteira já recebeu
  dez outras movimentações, isso não muda o que aquela operação fez;
- o **replay de uma abertura de carteira** devolve a carteira **como ela está
  agora**, com `idempotentReplay: true`. Motivo: a carteira é um **recurso
  mutável**, e representá-la com o saldo da criação mentiria sobre o recurso que o
  cliente veio consultar (`internal/app/wagering/opening.go:214` —
  `replayDaAbertura`).

Para o primeiro caso existir, `wager_transactions` guarda `result_balance_minor` e
o resultado persistido é montado dele (`service.go:417`). E como **zero é saldo
legítimo**, a distinção entre "não calculado" e "zero" não pode vir do valor: vem
de `HasResultBalance` (coluna nula × coluna com 0). Há teste que fixa isso no
banco: `TestTxnSaveResultBalanceZeroNaoVirouNull`.

Prova do comportamento com a carteira já alterada:
`TestReplayDevolveOSaldoDoProcessamentoOriginal`, e a versão multi-instância
`TestT8ReinicioPreservaIdempotenciaPendenciaEConsistencia` (o processo novo devolve
o saldo de **75,00** da época, não o de agora).

## 6.4 Por que carteira e ledger são agregados diferentes

| | Carteira | Ledger |
| --- | --- | --- |
| guarda | o saldo **atual** | o histórico de **cada** movimentação |
| responde | "quanto tem agora" | "por que o saldo está assim" |
| muda | a cada operação | nunca (append-only) |
| protegido por | `SELECT ... FOR UPDATE` | trigger que recusa `UPDATE`/`DELETE` |
| reconstruível a partir do outro? | **sim** (somando os lançamentos) | **não** |
| volume | uma linha por carteira | uma linha por movimentação |

Se fossem a mesma tabela, cada débito de 5,00 exigiria carregar o histórico
inteiro para calcular o saldo — inviável. E o contrário também não funciona: o
saldo atual **não conta como ele chegou ali**, então o ledger nunca pode ser
reconstruído da carteira.

A consequência prática mais útil: **a reconciliação existe porque são dois**. Ela
soma o ledger e compara com a carteira (`internal/app/wagering/reconcile.go:47` —
`Reconcile`); se fossem o mesmo dado, a conferência seria tautologia. A conferência
final da Fase 10 faz isso de duas formas independentes (SQL puro e endpoint) e
fecha em 101000 == 101000 (`TestConferenciaFinalCruzandoHTTPESQS`).

O lugar onde os dois se encontram é o commit: `UPDATE wallets` e `INSERT
wallet_ledger_entries` na **mesma** transação (`service.go:314` e `:317`). Não
existe saldo sem lançamento nem lançamento sem saldo — e é por isso que "saldo
mudou mas o ledger não" é um bug de aplicação, não um estado aceitável.

## 6.5 Por que `REFUND` antes da `BET` fica `PENDING_REFERENCE`

A alternativa seria recusar. Mas recusar uma reversão porque a aposta ainda não
chegou **pune o provedor por uma ordem de chegada que ele não controla**, e o
jogador fica com o dinheiro preso até alguém reenviar. A outra alternativa — aplicar
sem referência — é pior: não se sabe qual valor devolver, qual carteira creditar nem
se a operação já foi revertida.

Então a operação é **aceita e persistida como estado durável**:

```
REFUND chega, referência não existe
  → INSERT wager_transactions (PENDING)          [service.go:274]
  → tx.MarkPendingReference(attempt=1, próximo=agora+5s)
  → Save → UPDATE (PENDING_REFERENCE)            [outcome.go:49]
  → outbox: WagerTransactionPendingReference
  → COMMIT
  → resposta 202
  ... depois ...
BET chega e é processada
  → worker reivindica a pendência vencida        [worker/referencias.go:123]
  → Service.Retomar: reconstrói a Request das colunas persistidas
  → lock da carteira → crédito → ledger → ResolveReference + MarkProcessed
  → outbox: Processed (+ BalanceChanged), na MESMA transação
```

Três detalhes que parecem pequenos e não são:

1. **a tentativa e o instante ficam gravados** (`reference_resolution_attempt`,
   `next_resolution_attempt_at`). É isso que permite **outra instância** retomar
   depois de um reinício — a pendência não morre com o processo que a criou;
2. **`Retomar` é um caminho separado do `Process`**, mas reusa
   `resolverReferencia`, `mover` e os emissores de evento. Nenhuma régua de negócio
   duplicada (`internal/app/wagering/retomar.go`);
3. **a reversão cuja referência já está disponível** passa por
   `MarkPendingReference` + `ResolveReference` antes de concluir
   (`service.go:515` — `vincularReferencia`). Não é desvio: o agregado só aceita
   resolver referência em `PENDING_REFERENCE`, e essa é a transição que representa
   "esta operação depende de uma referência" — verdade mesmo quando ela já chegou.
   O estado intermediário **não é observável** (o `INSERT` gravou `PENDING` e o
   `UPDATE` grava `PROCESSED`, no mesmo commit).

Um contraste importante: em `BET`, `WIN` e `LOSS` a referência é **informativa** —
conferida quando resolve, mas **não bloqueia** a operação (`reference.go:47`).
Sem essa diferença, um `WIN` cuja `BET` ainda não chegou ficaria pendente e o
jogador não receberia por causa de um vínculo que ele não precisa. A régua está
registrada como interpretação 1 em `docs/CONFORMIDADE_README.md` e em
`docs/evidencias/FASE_6.md` §8; o teste é
`TestReferenciaInformativaNaoBloqueiaOProcessamento`.

## 6.6 Por que uma reversão sem saldo tem `failureCode` diferente

O domínio devolve **o mesmo** sentinela nos dois casos (`wallet.ErrInsufficientFunds`:
"não há saldo"). Quem separa é o **tipo da operação**:

```go
if errors.Is(err, wallet.ErrInsufficientFunds) {
    if kind == wagerdomain.KindRollback {
        return CodeRollbackInsufficientFunds   // "não dá para desfazer"
    }
    return wallet.CodeInsufficientFunds        // "o jogador não tem saldo para apostar"
}
```

(`service.go:492` — `codigoDaFalhaDeMovimento`.)

Por que isso importa para quem opera: os dois códigos pedem **ações diferentes**.
`INSUFFICIENT_FUNDS` é "o jogador não tem saldo" — o provedor pode tentar de novo
depois de um depósito, e o suporte explica ao jogador. `ROLLBACK_INSUFFICIENT_FUNDS`
é "não dá para desfazer": o jogador já gastou o que seria estornado (por exemplo,
ganhou 30,00 e apostou 130,00; desfazer o `WIN` exigiria 30,00 que não existem mais,
`TestROLLBACKSemSaldoTemCodigoProprio`). Com um código só, a operação de suporte
perderia a informação de que precisa e o provedor repetiria um pedido que nunca vai
passar.

Note que a rejeição é **gravada**, não descartada: `REJECTED` com o código, no
histórico, com evento `WagerTransactionRejected`. O README §7 pede "rejeitada e
auditável".

## 6.7 Por que a abertura não chama `Credit` — e por que zero não cria nada

`wallet.NewWallet` já nasce com o saldo inicial e versão **1**
(`wallet.go:41`). Chamar `Credit` na abertura faria o saldo virar o **dobro** e a
versão ir para **2** — contrariando o README §9, que fixa a versão da carteira
nessa abertura em 1.

O que faltava não era movimentar a carteira: era **registrar de onde o saldo veio**.
Isso é feito com um lançamento de crédito partindo de `0.00`
(`opening.go:176` — `money.FromMinor(0, ...)`, `:177` — `ledger.NewEntry(...)`).
É o que faz a reconciliação do enunciado fechar: abertura `1000.00` + `BET`
`25.00` → `975.00` com `checkedEntries: 2`
(`TestAberturaMaisApostaFechaComOExemploDoREADME`).

**Zero não cria nada.** Abertura com saldo inicial zero cria **só** a carteira: sem
`OPENING`, sem lançamento, sem eventos (`opening.go:121`; README §9 e §7). O motivo
é que zero é a ausência de dinheiro entrando, e um lançamento de valor zero seria
recusado pelo próprio domínio (`ledger_amount_positive`).

**Consequência conhecida:** como não existe `OPENING` para essa abertura, não há
registro de idempotência dela. O reenvio da mesma chave cai na unicidade de
`(playerId, currency)` e recebe `WALLET_CONFLICT` — que é exatamente a resposta
prescrita para carteira duplicada (`TestOpenWalletComSaldoZeroNaoCriaOpening`,
`TestOpenWalletDuplicadaEConflito`).

## 6.8 Os códigos de falha são contrato: a tabela completa

`failureCode` é a parte da resposta que o cliente usa para decidir o que fazer. Por
isso a lista é fechada, estável e separa **corrigível** de **definitivo**.

| Situação | `failureCode` | Desfecho | Gravado? |
| --- | --- | --- | --- |
| `BET` sem saldo | `INSUFFICIENT_FUNDS` | `422` | sim (`REJECTED`) |
| `ROLLBACK` que exigiria saldo | `ROLLBACK_INSUFFICIENT_FUNDS` | `422` | sim |
| reversão já aplicada (mesmo tipo, ou `ROLLBACK` de `BET` já estornada) | `ALREADY_REVERSED` | `422` | sim |
| referência existe e terminou rejeitada/com falha | `REFERENCE_NOT_PROCESSABLE` | `422` | sim |
| referência divergente, tipo não reversível ou valor diferente | `REFERENCE_MISMATCH` | `422` | sim |
| moeda da operação ≠ moeda da carteira | `WALLET_CURRENCY_MISMATCH` | `422` | sim |
| carteira de outro jogador | `WALLET_PLAYER_MISMATCH` | `422` | sim |
| reversão cuja referência ainda não chegou | — | `PENDING_REFERENCE` / `202` | sim |
| referência que nunca chegou (TTL esgotado, worker) | `REFERENCE_NOT_FOUND` | `422` | sim |
| mesma chave com hash diferente / identidade externa com outra chave | `IDEMPOTENCY_CONFLICT` | `409` | não |
| carteira inexistente | `WALLET_NOT_FOUND` | erro (`404`) | não (a FK impede gravar) |
| sem chave, `OPENING` de fora, `LOSS` ≠ `0.00`, reversão sem referência, tipo desconhecido | `IDEMPOTENCY_KEY_REQUIRED`, `OPERATION_NOT_ALLOWED`, `INVALID_AMOUNT`, `REFERENCE_REQUIRED`, `INVALID_TRANSACTION` | `400`/`403` | **nada gravado** |

A última linha é a distinção que o README §7 chama de "distinguir entradas
corrigíveis de resultados definitivos": no erro de entrada **nada foi gravado**, e
a **mesma chave** pode ser reenviada depois de corrigido o pedido. Na rejeição, o
resultado é definitivo e está no histórico
(`erros.go:116`; `docs/evidencias/FASE_6.md` §8).

Os textos vivem em `internal/app/wagering/codes.go` (os que a aplicação decide) e
nos pacotes de domínio (os que o domínio já nomeia). **Não duplique** um código:
se o domínio já sabe o nome do fato, o serviço usa o dele.

## 6.9 A matriz `REFUND` × `ROLLBACK` sobre a mesma aposta

O que está implementado e **provado por teste**:

| Sequência | Resultado | Por quê |
| --- | --- | --- |
| `BET` → `REFUND` | aceito, crédito do valor integral | `REFUND` devolve a aposta (`TestREFUNDDevolveIntegralmenteABET`) |
| `BET` → `REFUND` → **outro `REFUND`** | `REJECTED` / `ALREADY_REVERSED` | índice parcial `(reference_transaction_id, kind)` + leitura prévia (`TestReversaoDuplicadaEALREADYREVERSED`) |
| `BET` → `REFUND` → `ROLLBACK` **da `BET`** | `REJECTED` / `ALREADY_REVERSED` | creditaria de novo o mesmo débito (`TestROLLBACKDesfazBETeDesfazEstorno`) |
| `BET` → `REFUND` → `ROLLBACK` **do `REFUND`** | aceito, **débito** | desfazer o estorno é tirar o que foi devolvido |

O índice parcial permite que **um** `REFUND` e **um** `ROLLBACK` coexistam sobre a
mesma referência — é isso que torna possível desfazer o estorno. O que não pode
existir são **dois do mesmo tipo**.

> **Ponto a fechar se você mexer nesta matriz.** A checagem cruzada existe em uma
> direção só: ao validar um `ROLLBACK` de `BET`, o serviço também consulta se já há
> um `REFUND` (`reference.go:159`). O inverso — `REFUND` depois de um `ROLLBACK` da
> mesma `BET` — não é consultado por `jaRevertida` (`reference.go:154`) e **não tem
> teste**. Se a regra de negócio exigir que essa ordem também seja recusada, este é
> o ponto a alterar, e o teste a escrever é o par simétrico de
> `TestROLLBACKDesfazBETeDesfazEstorno`.

---

# 7. As decisões, com motivo e consequência

Toda decisão de arquitetura é uma troca. Este capítulo registra **o que se ganhou**
e **o que se pagou** — na ordem em que as decisões foram tomadas. As seções 7.1 são
as decisões congeladas antes da primeira linha de código (`PLANO.md` §1, A1–A14);
as seguintes são as que apareceram durante as fases 6 a 11, cada uma com um
"sem isso" que é a consequência concreta de remover a decisão.

## 7.1 As decisões congeladas (A1–A14 do `PLANO.md`)

**A1 — Versões e dependências.** Fixar stack e versões antes de codificar evita
refactor no meio do caminho: o `go.mod` traz `go 1.26.0` com `toolchain go1.26.5`
e o Dockerfile usa `golang:1.26-alpine` (alinhado depois que a primeira imagem não
compilava, porque o módulo exigia 1.26 e a imagem era 1.25). Ficaram `chi` para o
roteador HTTP, `pgx` com SQL explícito, `golang-migrate` com `iofs`, o SDK v2 da
AWS para SQS, `caarlos0/env` para configuração, `prometheus/client_golang` e
`go.uber.org/fx`. Uma decisão do plano **mudou** na prática: o plano previa
`go-oidc` + `golang-jwt` para OIDC, e a implementação usou a biblioteca padrão
(`crypto/rsa`, `encoding/json`, `encoding/base64`) — três dependências a menos e
~300 linhas de regra explícita e testável. Sem o congelamento, cada fase teria
discutido biblioteca em vez de resolver o problema; com ele, qualquer troca virou
uma decisão registrada e não um deslize.

**A2 — Dinheiro em unidades mínimas.** `int64` em centavos com escala fixa 2,
`BIGINT` na persistência, nunca `NUMERIC` nem `FLOAT`, com limite de 9 × 10^15 e
overflow tratado em parse, soma, subtração e negação. A consequência de tirar isso
é a pior possível: ponto flutuante em ledger **não fecha** a soma, e o sistema passa
a mentir sobre dinheiro de forma irreproduzível. E o limite abaixo do teto do
`int64` não é conservadorismo: é o que permite escrever a guarda de overflow que
**não estoura ao checar** (`ARCHITECTURE.md` §2).

**A3 — Transação SQL e delimitação.** Uma Unit of Work abre a transação e injeta o
`pgx.Tx` em todos os repositórios; o serviço de aplicação é quem abre e fecha, e
repositório **nunca** comita sozinho. Sem isso, "saldo + ledger + evento no mesmo
commit" seria aspiração: bastaria um repositório chamar `Exec` no pool para a
atomicidade desaparecer, sem erro de compilação e sem teste vermelho.

**A4 — Concorrência por carteira.** A combinação escolhida foi: lock pessimista de
linha (`SELECT ... FOR UPDATE`), mais a rede de segurança das constraints, mais
`UPDATE` com guarda de versão. A ordem de aquisição é sempre a mesma (carteira antes
da transação de referência), o que evita deadlock. Sem o lock, duas apostas
simultâneas leem o mesmo saldo e a segunda sobrescreve a primeira (*lost update*);
sem a versão, uma escrita fora do fluxo passaria silenciosamente.

**A5 — Idempotência persistente.** Duas chaves de unicidade no banco — a do
provedor (`provider_id`, `idempotency_key`) e a identidade financeira
(`provider_id`, `external_transaction_id`) — mais hash canônico do payload,
excluindo a chave e os metadados de transporte, e o resultado persistido
(`result_balance_minor`) para o replay devolver o saldo original. Se a idempotência
fosse só em memória, um reinício transformaria todo reenvio em operação nova — e
idempotência em memória é eliminatório no README §14.

**A6 — Máquina de estados.** `PENDING → PROCESSED | REJECTED | FAILED |
PENDING_REFERENCE` e `PENDING_REFERENCE → PROCESSED | REJECTED | FAILED`, com
transição validada **no domínio** (não no SQL) e estado terminal recusando qualquer
transição. A decisão que veio junto foi **não fazer aceite assíncrono**: operação
sem dependência conclui de forma síncrona, sem commit intermediário, e o `PENDING`
durável fica restrito a `PENDING_REFERENCE`. Sem a validação no domínio, a regra
"terminal não transiciona" viraria uma sequência de `if`s que o primeiro `if`
esquecido fura.

**A7 — Referências e reversões.** Referência resolvida por
`(provider_id, external_transaction_id)`, concordância obrigatória em provedor,
jogador, carteira, moeda e rodada, valor **igual** (não há reversão parcial),
`REFUND` só de `BET` e `ROLLBACK` de `BET`/`WIN`/`REFUND`, dupla reversão do mesmo
tipo barrada por índice parcial, e `PENDING_REFERENCE` com worker de backoff
exponencial, TTL de 20 tentativas/24 h e `REFERENCE_NOT_FOUND` ao esgotar. Sem o
índice parcial, duas reversões do mesmo tipo criariam dinheiro; sem o worker, a
pendência morreria com o processo.

**A8 — Códigos de falha estáveis.** Uma lista fechada de códigos, cada um com
significado e "corrigível?" — porque o cliente decide o que fazer por eles. Sem
essa disciplina, o mesmo problema sairia com três grafias diferentes e nenhum
cliente conseguiria automatizar tratamento. Os códigos planejados de overflow e de
escala (`ErrOverflow`, `ErrScaleExceeded`) **não** foram criados: limite e escala
são ambos `INVALID_AMOUNT`, porque dois códigos a mais só criariam ruído no
contrato (decisão revista na Fase 1, registrada no `PLANO.md`).

**A9 — Contrato HTTP de respostas.** `201` para operação nova, `200` para replay,
`202` para pendência, `422` para rejeição, `400`/`403`/`409`/`503` para o que
impede a operação de existir, com `Idempotency-Key` obrigatório e **nunca**
substituído. Sem a separação `201` × `200`, um cliente que conta operações novas
contaria a mesma duas vezes; sem a separação `202` × `422`, ele reenviaria uma
operação que já está esperando referência.

**A10 — Autenticação e autorização com Keycloak.** Realm com dois clients
`client_credentials`, roles `provider` e `internal`, `provider_id` por protocol
mapper, e a regra de que a identidade autenticada **determina** o provedor. Sem o
`provider_id` vindo do token, qualquer token válido operaria a carteira de qualquer
provedor — eliminatório. A implementação foi além do plano em um ponto e ficou
atrás em outro: `azp` só é conferido quando há múltiplas audiences (regra do OIDC
Core, e o realm emite uma só), e não há introspecção de token (revogação antes do
`exp` não é detectada) — as duas limitações estão no capítulo 9.

**A11 — Inbox e outbox.** Inbox com `UNIQUE (consumer_name, message_id)` e
`body_hash`, registrada e concluída **na mesma transação** do domínio; outbox com
`event_id` estável, payload como snapshot, `attempts`, `available_at` e
`published_at`, e publisher com lease. Sem a inbox na mesma transação, existiria um
instante com débito feito e sem registro; sem a outbox, o evento ou mente ou se
perde.

**A12 — Eventos.** Envelope com `eventId`, `eventType`, `aggregateId`,
`correlationId`, `causationId` opcional, `occurredAt`, `version` e `data` tipado;
quatro tipos, e a decisão de que **tipo e versão são definidos pelo construtor** —
não por quem publica. `MessageGroupId` é a carteira, porque a ordem que importa é a
dos fatos daquela carteira. Sem os construtores, o primeiro `json.Marshal` de um
struct anônimo fixaria um contrato que ninguém revisou.

**A13 — Observabilidade.** `log/slog` com correlação automática, cinco
identificadores obrigatórios no rastro, proibição explícita de logar token, segredo
ou payload financeiro completo, e as métricas de negócio. Sem a proibição escrita,
o primeiro `log.Info(payload)` vazaria dado financeiro; sem as métricas de outbox,
um evento emparedado ficaria invisível.

**A14 — Fx e ciclo de vida.** Módulos por área, `Provide` só com construtores,
`Invoke` para efeitos (registrar rotas, subir servidor, iniciar laços), e a ordem de
encerramento: novas entradas param primeiro, workers drenam, dependências fecham
por último. Sem a ordem, o pool fecharia com requisição em andamento — e o erro
apareceria como `503` intermitente no fim de cada deploy.

## 7.2 Decisões da Fase 6 (o caso de uso)

**Consulta prévia da reversão, além do índice.** O índice parcial
`UNIQUE (reference_transaction_id, kind)` impede a dupla reversão, mas ele fala
pelo `23505`, que **aborta a transação** e não diz qual unicidade caiu. A consulta
`GetByReferenceAndKind` existe para o cliente receber `ALREADY_REVERSED` com
significado; o índice continua sendo a barreira final. Sem a consulta, o provedor
receberia um conflito genérico e o suporte não saberia o que aconteceu.

**O retry envolve o `Run` inteiro.** Repetir uma chamada **dentro** da transação é
o erro clássico: a tentativa que falhou deixa a conexão com a transação aberta
segurando o lock da carteira, e a repetição trava. O sintoma aparece longe — um
`TRUNCATE` de teste morrendo com deadlock. Sem essa decisão, o próprio retry
viraria a causa da indisponibilidade.

**Retry também no conflito, não só no transitório.** No schema deste projeto,
unicidade violada significa "alguém chegou primeiro". Repetir a transação faz a
segunda passada **ler o que já está gravado** e decidir entre replay e conflito, em
vez de devolver erro de banco. Sem isso, dois envios simultâneos da mesma operação
produziriam um `500` para um deles.

**Retry esgotado não grava `FAILED`.** A transação não commitou, então **não existe
linha** para marcar. Esgotadas as tentativas, o erro transitório sobe (`503`) e o
cliente pode repetir; o `FAILED` fica reservado aos caminhos em que a linha já está
persistida — o worker de referências. Sem essa distinção, o sistema inventaria um
registro de auditoria para uma operação que nunca existiu.

**O instante é lido depois do lock.** Ele vai para o `occurredAt` dos eventos, e a
outbox é publicada em ordem de sequência com `MessageGroupId` = carteira. Lido
antes do lock, o instante seria a ordem de **início** das transações, e o
`balanceAfter` de duas operações da mesma carteira poderia chegar invertido. Sem
isso, o consumidor reconstruiria o saldo fora de ordem — e a limitação, que era
teórica, virou teste na Fase 10.

**O `INSERT` da transação vem antes de resolver a referência.** Todo desfecho —
rejeição, pendência, conclusão — é um **estado da linha**; sem a linha não há onde
gravá-lo. O `INSERT` grava `PENDING` e o `Save` grava o definitivo no mesmo commit,
então o estado intermediário não é observável. Sem essa ordem, a rejeição de uma
operação sem saldo não teria onde ser registrada.

**A abertura não chama `Credit`.** `NewWallet` já nasce com o saldo inicial; chamar
`Credit` dobraria o saldo e levaria a versão a 2, contra o README §9. O lançamento
de abertura registra a **origem** do saldo (`balanceBefore = 0.00`). Sem isso, a
reconciliação do enunciado não fecharia e a versão da carteira abriria errada.

**`LOSS` grava o saldo observado em `result_balance`.** O replay de um `LOSS`
devolve o saldo daquele instante, como qualquer operação concluída — e o saldo não
mudou. Sem isso, o replay de um `LOSS` devolveria `balance: null` e o cliente
concluiria que a operação não foi processada.

**Referência informativa não grava `reference_transaction_id`.** Em `BET`, `WIN` e
`LOSS` a referência é conferida quando resolve, mas gravá-la faria o índice parcial
`(reference_transaction_id, kind)` tratar a operação como reversão daquele fato — e
dois `WIN` apontando para a mesma `BET` colidiriam. Sem essa sutileza, um `WIN`
legítimo viraria `409`.

**`ROLLBACK` de `BET` já estornada é recusado.** O índice permite `REFUND` e
`ROLLBACK` conviverem; creditar de novo o mesmo débito criaria dinheiro. Desfazer o
estorno é `ROLLBACK` apontando para o `REFUND`, que **debita** e é aceito. Sem essa
regra, existiriam dois caminhos para devolver o mesmo débito.

**O replay da abertura devolve a carteira como ela está.** A carteira é recurso
mutável: representá-la com o saldo da criação mentiria sobre o recurso. O
`IdempotentReplay` é o que diz que nada foi criado desta vez. Sem essa distinção, o
cliente que reenvia uma abertura receberia um retrato falso do saldo.

**Os emissores de evento recebem `*ledger.Entry`, não `Movement`.** A abertura gera
lançamento **sem** movimentação no agregado; um `Movement` zerado significaria "não
houve lançamento", e a abertura tem lançamento. Sem essa escolha, o evento
`WalletBalanceChanged` da abertura não seria emitido.

**Operação de outro provedor responde "não encontrado".** `403` confirmaria a
**existência** de uma operação alheia, que é justamente a informação que o
isolamento protege. E a consulta por identidade externa já filtra o provedor **no
SQL**, então a linha alheia nem é lida. Sem as duas coisas, o isolamento dependeria
de uma checagem depois da leitura — que é onde se esquece.

**Identificador que não é UUID não chega ao banco.** Essas colunas são `UUID`, e um
texto qualquer faz o PostgreSQL recusar com `22P02`, que a classificação trata como
falha **permanente** — ou seja, `500`. Um identificador malformado não pode ser
erro de servidor: a aplicação confere o formato **antes** de consultar
(`internal/app/wagering/ids.go` — `ehUUID`) e responde "não encontrado" na leitura
ou entrada inválida na requisição. A checagem fica na aplicação, e não no adaptador
do banco: lá o `22P02` também poderia vir de uma inserção, e transformá-lo em "não
encontrado" mascararia defeito de gravação.

**`ListLedger` confere a carteira antes de listar, e a página tem teto.** Página
vazia é ambígua: o cliente não distingue "sem lançamento" de "sem carteira" — custa
uma consulta a mais na mesma transação. E o teto (50 padrão, 200 máximo) impede que
`limit=1000000` transforme paginação em varredura que segura conexão e memória do
serviço.

## 7.3 Decisões da Fase 7 (HTTP e autenticação)

**O `providerId` vem do token.** É a regra do README §2 e a razão de o corpo ser
apenas confirmatório: corpo divergente responde `403` **sem chamar o caso de uso**.
Sem isso, qualquer token válido operaria a carteira de qualquer outro provedor —
eliminatório, e sem nenhum teste vermelho se ninguém olhasse.

**A leitura por `{transactionId}` interno é do serviço interno.** O UUID é
identificador nosso; expô-lo ao provedor permitiria varredura de identificadores. O
provedor consulta pela identidade **dele**. Sem essa separação, o isolamento seria
de fachada.

**O algoritmo é fixo em RS256, decidido pelo serviço, e o `kid` é obrigatório.**
Confiar no `alg` do token é a vulnerabilidade clássica: `none`, `HS256` assinado com
a chave pública, `jku`/`x5u` apontando para outro host. Sem a decisão, a validação
seria "verificar a assinatura" — e assinatura verificada com a chave que o atacante
escolheu não verifica nada.

**Duas classes de erro: token inválido (`401`) e IdP fora (`503`).** Sem a
distinção, uma queda do Keycloak faria o cliente descartar um token **válido** e o
incidente seria diagnosticado errado. E a resposta não diz qual foi o problema —
"o `exp` passou três minutos" ajudaria quem está sondando; o detalhe vai para o log,
com o `correlationId`.

**O readiness do IdP fura o cache de discovery.** Um readiness que consulta cache
responderia `UP` durante todo o TTL mesmo com o Keycloak caído. Sem isso, o
orquestrador manteria na rotação uma instância que devolve `503` em toda rota de
negócio.

**Rejeição de negócio é desfecho no corpo; erro é envelope.** `422` com
`{transactionId, status, balance:null, failureCode}` versus
`{"error":{code,message,failureCode}}` para o que impede a operação de existir. Sem
a separação, o cliente perderia o `transactionId` da operação auditável — e é por
ele que se consulta e se abre suporte.

**A chave da abertura de carteira é derivada quando o cliente não manda uma.** O
README não define `Idempotency-Key` para `POST /wallets`, mas abrir carteira é a
operação mais cara de duplicar. A regra é `abertura:{playerId}:{moeda}`
(`internal/httpapi/handlers_carteira.go:89`), e a chave do cliente, quando existe, é
usada como veio. Sem isso, reenviar a abertura devolveria conflito seco em vez de
replay — ou, pior, exigiria uma chave que o enunciado não pede.

**Rota de negócio nunca é registrada sem autenticador.** Se `auth == nil`,
`RegistrarRotas` volta sem montar nada. Sem isso, uma composição errada exporia
endpoint financeiro sem identidade — e falhar **fechado** (404) é a única resposta
aceitável para composição errada.

**A ordem dos middlewares é o que faz o log não mentir.** `AccessLog` tem de
envolver o `Recoverer`: o log de acesso é dono do `statusRecorder`, e o `Recoverer`
escreve o `500` **depois** do pânico. Na ordem antiga, o log registrava `200` para
uma requisição que respondeu `500` — e a métrica de status herdava o número errado.
Sem a correção, o log mentia exatamente no caso em que ele é mais necessário.

## 7.4 Decisões da Fase 8 (consumo SQS)

**Ganchos transacionais, e não uma transação em volta.** O registro da inbox tem de
compartilhar a transação do domínio (README §6.5); a única forma é ele acontecer
dentro do `uow.Run` da operação. Sem os ganchos, a queda entre duas transações
deixaria débito sem registro ou registro sem débito — e a reentrega duplicaria o
débito.

**`Process` é `ProcessWithHooks(nil)`.** O HTTP não paga nada pelo recurso existir.
Sem essa equivalência trivial, o caminho SQS passaria a ter uma variante própria do
caso de uso — dois lugares para a mesma regra.

**Mensagem inválida é recusada antes de abrir transação.** Não há por que registrar
na inbox o que jamais será aplicado. Sem isso, a inbox encheria de lixo e o
diagnóstico de duplicata ficaria poluído.

**Corpo divergente é permanente.** Duas mensagens com o mesmo identificador e
conteúdo diferente não são reentrega (o broker garante "pelo menos uma vez", não
"conteúdo diferente"): é mensagem forjada ou bug de produtor. Sem essa decisão, o
caso seria tratado como transitório e a mensagem giraria até a DLQ, perdendo tempo
de fila.

**Rejeição de negócio é terminal e remove a mensagem.** O estado já está durável;
reenviar não muda a decisão. Sem isso, toda operação rejeitada voltaria para a fila
para sempre.

**DLQ explícita (`Send` + `Delete`), além do redrive.** Numa fila FIFO, uma
mensagem envenenada que gira até o `maxReceiveCount` **bloqueia o grupo inteiro**
enquanto gira. Sem a DLQ explícita, uma mensagem inválida pararia as operações da
mesma carteira por minutos.

**Erro desconhecido vai para retry, não para a DLQ.** A DLQ é o fim da linha:
mandar para lá algo que talvez funcionasse na segunda tentativa **perde trabalho**,
enquanto tentar de novo custa tempo — e o broker já limita as tentativas. Sem essa
escolha conservadora, um bug transitório de classificação viraria perda de
mensagens.

**`context.WithoutCancel` no processamento.** Se usasse o contexto do laço, o
`SIGTERM` abortaria a transação em andamento — o oposto do que o encerramento quer.
Sem isso, cada deploy poderia abortar operações no meio.

**No encerramento: parar de buscar, esperar o trabalho em voo, liberar a
visibilidade do que sobrou** (`ChangeMessageVisibility 0`), com um `sync.Once` por
entrega. O `sync.Once` é o que impede a mesma mensagem de ser **apagada e
devolvida**: quem decide o destino primeiro vence. Sem ele, o encerramento poderia
liberar a visibilidade de uma mensagem que a goroutine acabou de remover, e ela
reapareceria depois de já ter sido tratada.

**Backoff exponencial com teto e jitter dentro do teto.** Exponencial porque um SQS
fora do ar não deve ser martelado; teto porque sem ele a décima tentativa adiaria a
mensagem por horas e a fila pareceria travada; jitter porque sem ele todas as
instâncias que falharam no mesmo instante voltariam no mesmo instante. E o jitter é
**subtraído** dentro do teto: a primeira versão somava depois do limite e
ultrapassava o `MaxBackoff` (defeito 32).

**`attempts` incrementado no SQL.** `attempts = attempts + 1` no banco, não a partir
do valor lido: duas instâncias recebendo a mesma mensagem ao mesmo tempo gravariam
o **mesmo** valor final em vez de somar as duas entregas. Sem isso, investigar uma
mensagem morta não diria se ela foi recebida duas ou vinte vezes — porque o
`ApproximateReceiveCount` do broker se perde quando a mensagem é removida.

**SQS no readiness, consultando a fila específica.** Com a fila ausente — ambiente
mal provisionado — o serviço não consumiria nada, e responder `UP` seria falso. Sem
isso, o orquestrador mandaria tráfego para uma instância surda.

## 7.5 Decisões da Fase 9 (outbox publisher e workers)

**Publicar ANTES de marcar.** A ordem inversa diria "publiquei" antes de publicar:
uma queda entre as duas etapas perderia o evento **para sempre**. Nesta ordem, a
pior falha possível é a **republicação**, e o consumidor deduplica pelo `eventId`.
Sem essa ordem, o README §3 ("nenhum evento confirmado se perde") seria violado por
um `SIGKILL` de milissegundos.

**O lote é reivindicado em transação curta e a publicação acontece fora dela.**
Segurar `FOR UPDATE` e uma conexão do pool durante um round-trip ao broker
esgotaria o pool com poucas instâncias; além disso o lease precisa estar
**confirmado** para o evento voltar sozinho. Sem essa separação, três publishers
consumiriam o pool inteiro e a API pararia.

**`MessageDeduplicationId` por TENTATIVA, não por `eventId`.** Se fosse o
`eventId`, o SQS engoliria a republicação dentro da janela de 5 minutos: o evento
que voltou do lease seria deduplicado pelo broker e a mensagem original continuaria
sendo a única — o contrário do que o README §11 pede. E o nonce cobre o caso em que
o número da tentativa não muda (queda entre publicar e marcar). Sem essa decisão, a
republicação seria silenciosamente descartada.

**Quarentena em vez de descarte.** Esgotado o teto (ou na recusa permanente do
destino), o evento continua pendente, com `last_error` e `available_at` empurrado
por `OUTBOX_PARKED_BACKOFF`. Nada é apagado e nada é marcado como publicado. Sem
isso, o evento que "não publica mais" desapareceria sem que ninguém soubesse — com
a quarentena, o atraso da outbox denuncia o evento emparedado.

**`MarkPublished` com `context.WithoutCancel`.** A mensagem já está na fila;
perder a confirmação por causa de um sinal custaria uma republicação desnecessária.
Sem isso, todo encerramento geraria duplicatas no painel do consumidor.

**`ReagendarReferencia` em vez de auto-laço na máquina de estados.** A tentativa e o
instante mudam; o estado, não. Sem esse método, o caminho "continua pendente"
tentaria uma transição que a máquina não permite e falharia — foi o defeito 38.

**Duas guardas na retomada: lock da linha + estado/tentativa esperados.** O lock
sozinho não basta: o segundo worker encontra a linha ainda em `PENDING_REFERENCE` e
consumiria outra tentativa do TTL. E a guarda **não** pode ser o
`next_resolution_attempt_at`, porque o claim acabou de sobrescrevê-lo com o lease —
comparar com ele faz a retomada **ignorar todo o trabalho** (defeito 37).

**O lock da carteira é o último, e só quando há movimento.** Uma retomada que apenas
reagenda não bloqueia carteira. Sem isso, um worker processando pendências
serializaria carteiras que não precisavam de nada.

**TTL só decide quando a referência NÃO resolve.** Se ela chegou, a operação
conclui mesmo depois do prazo: rejeitar uma reversão correta para respeitar um
relógio seria destruir informação. Sem essa regra, uma rede lenta transformaria uma
operação válida em rejeição definitiva.

**O reaper usa o MESMO `Retomar`, sem caminho próprio, e não aplica TTL por idade.**
A linha está em `PENDING`; o relógio da referência só começa quando ela vira
`PENDING_REFERENCE`. Sem o reuso, existiriam duas implementações da retomada — e a
segunda seria a que ninguém testa.

**`FAILED` não emite `WagerTransactionRejected`.** A tabela do README §11 define
esse evento como rejeição de **negócio**, e dado inconsistente não é decisão de
negócio. Sem essa distinção, o consumidor contaria falha de infraestrutura como
rejeição.

**Um prazo de encerramento para o CONJUNTO dos laços, não um por laço.** Os laços
param em paralelo; um prazo por laço exigiria a soma na validação — e o ambiente de
exemplo não subiria (é o defeito 33 com três parcelas em vez de duas). Sem a
decisão, o `.env.example` e o compose violariam a própria regra de configuração.

**A decisão de quais laços ligar fica na composição, não no runner.** O runner
continua testável com laços falsos. Sem isso, o teste do runner precisaria de
Postgres e SQS.

**Readiness olha o runner.** `UP` com o publicador morto seria falso exatamente
enquanto a outbox não publica. Sem isso, a instância ficaria na rotação enquanto o
trabalho de fundo está parado.

## 7.6 Decisões da Fase 10 (prova multi-instância)

**Três processos do binário, e não três containers.** O README §8 pede três
**processos** independentes com conexões e memória próprias — e um processo do
binário tem exatamente isso (PID, pool, memória, descritores). Container acrescenta
isolamento de imagem e rede, que não muda nada na concorrência, e custaria rebuild a
cada ciclo. Sem essa decisão, qualquer cenário de concorrência dependeria de
`docker compose build` (minutos por execução); o container ficou reservado à prova
de **sinal**, que é o que o processo local no Windows não consegue provar.

**`application_name` por instância no DSN.** É o instrumento de **medida** da
independência e da disputa de lock: sem ele, "cada um com suas conexões" seria
afirmação, e não haveria como dizer **quem** espera lock no `pg_stat_activity`. A
descoberta de que o pool sobrescrevia o DSN com `"jungleg-api"` foi o defeito 40 —
invisível com uma instância só, desde a Fase 5.

**Barreira (canal fechado) em vez de `go` solto ou `sleep`.** `go` solto deixa a
primeira terminar antes da última começar, e o teste vira sequencial; `sleep` é
tempo mágico. Sem a barreira, o T1 testaria 50 chamadas em fila e o verde não
significaria simultaneidade.

**`OUTBOX_BATCH_SIZE=1` no teste de disputa.** Com o lote padrão (50), o primeiro a
acordar leva tudo e os outros dois nunca tocam na outbox: o teste seria "um
publisher com dois espectadores". Sem esse ajuste, o `SKIP LOCKED` não seria
exercitado.

**O T3 é provado por lock construído, não por cronômetro.** A diferença passa a ser
do experimento, não da máquina: com operações de milissegundos, razões de tempo se
invertem por ruído. Sem isso, o teste de paralelismo entre carteiras ficaria
intermitente — e teste intermitente não serve de evidência.

**A queda entre commit e delete é reproduzida com o `Delete` embrulhado.** A janela
real dura microssegundos e o Windows não tem `SIGTERM` de host; o que se reproduz é
o **estado durável** (transação commitada + inbox `PROCESSED` + mensagem ainda na
fila), que é idêntico ao da queda. Sem essa camada, o caminho "commit feito,
mensagem não removida" ficaria sem prova automatizada.

**A fila de teste roda com `VisibilityTimeout` de 2 s e é drenada no `t.Cleanup`.**
O resíduo de um cenário reaparecia no seguinte e fazia falhar um teste por motivo
alheio. Sem isso, a suíte ficaria intermitente **sem** haver defeito — e o próximo a
mexer perderia horas no lugar errado.

**A conferência final é feita em SQL puro e também pelo endpoint.** Duas
implementações independentes; se as duas usassem o saldo da carteira como
referência, nenhuma provaria nada. Sem isso, a conferência viraria tautologia.

**A distribuição de publicações entre as réplicas não é asserida.** Ela depende do
escalonamento (observado 4/4/4); a invariante que o teste assere é a **partição**
(12 eventos → 12 mensagens, sem repetição e sem perda). Sem essa renúncia, o teste
seria intermitente por um motivo que não é do sistema.

## 7.7 Decisões da Fase 11 (observabilidade)

**Ler o valor em `/metrics` em vez de espiar o objeto.** É o que o Prometheus vê, e
é o único jeito de pegar nome errado, label errado e registro faltando. Sem isso, o
teste fica verde sobre um registry que ninguém expõe — foi assim que o defeito 43
passou.

**Contar conflito no ponto onde a transação é repetida, via porta de um método.**
O `comRetentativa` é o único lugar que **sabe** que a transação foi descartada, e
`ports.ConflictCounter` mantém o caso de uso longe do Prometheus. Sem isso, ou a
métrica fica em zero para sempre (defeito 42), ou a aplicação passa a importar
Prometheus.

**`kind=<tipo>` e `status=<desfecho>` nos dois caminhos.** A série é uma só e o
painel soma por label; se o HTTP emitisse `kind=PROCESSED, status=2xx_201` e o SQS
emitisse `kind=BET, status=PROCESSED`, somar por `kind` misturaria tipo com
desfecho.

**`correlationId` do SQS = `MessageId` da entrega, no contexto.** O envelope do
README §10 não tem campo de correlação e o produtor não tem outro identificador
antes do envio. Sem isso, toda linha do caminho assíncrono sairia sem rastro,
enquanto o evento publicado sairia com ele.

**O `Resultado` da inbox carrega `transactionId`/`walletId`/`providerId`.** O
consumidor não vê a requisição (ela morre dentro do processador); sem esses campos,
o log do SQS diria "a mensagem foi tratada" sem dizer **qual** operação.

**Refletir `tx.CorrelationID()` no contexto da retomada.** A resolução acontece em
outro processo, minutos depois; sem isso, a linha que mais importa para investigar
sairia sem correlação (defeito 39).

**Provar o log pelo stdout do processo real.** Formato e cobertura só são reais no
stdout do binário: foi assim que o defeito 27 (dois formatos no mesmo stdout)
passou despercebido por uma fase inteira.

**Não adicionar Prometheus ao compose, e versionar dashboard e alertas.**
Diferencial opcional × artefato de entrega que precisa subir limpo: o compose maior
traz mais modos de falha no "sobe do zero" e nenhuma garantia nova. O painel é JSON
validado e as regras passam no `promtool`.

**`blackbox_exporter` para o readiness, em vez de criar métrica de readiness.** O
`/health/ready` é JSON, e a fase não cria métrica nova. Sem o blackbox, o alerta de
"workers DOWN" não existe — e o alerta de outbox atrasada cobre o sintoma em
minutos, menos direto, como está no runbook.

**`go mod tidy` e revalidar tudo depois.** Dependência não usada é ruído na entrega
(`testcontainers-go` estava baixado e não era usado), e `go.sum` mudou: a suíte
inteira foi revalidada **depois** do tidy, não antes.

**Não adicionar `goleak`.** A prova comportamental já existe (SIGTERM no container,
`TestCicloDeVidaFxComStartEStopReais`); a dependência nova compraria uma asserção
que pode ser instável.

---

# 8. Os problemas resolvidos: 45 defeitos reais

Nenhum destes foi introduzido de propósito. Todos foram encontrados durante o
desenvolvimento, corrigidos e **travados por teste** — é por isso que a lista tem
valor: cada linha é um erro que o sistema não comete mais, e o teste que o impede de
voltar está no repositório. A fonte primária é `ARCHITECTURE.md` §15; aqui está a
versão "sintoma → causa de fundo → correção", para você reconhecer o **sintoma** se
ele reaparecer.

Leia a coluna "sintoma" como quem lê um alerta de produção: é o que você veria. A
coluna "causa" é o que estava errado de verdade (quase nunca é onde o sintoma
aparece). A "correção" é o que mudou.

## 8.1 Fases 1 a 6 — domínio, persistência e caso de uso

| # | Fase | Sintoma | Causa de fundo | Correção |
| --- | --- | --- | --- | --- |
| 1 | 1 | 10 combinações de dois valores negativos passavam na soma | guarda de overflow do `Add` existia de um lado só | guarda de piso; prova por varredura exaustiva de 81 combinações |
| 2 | 1 | subtração válida era recusada — e o estouro continuava possível | guarda do `Sub` com sinal invertido e sem teto | sinal corrigido + guarda de teto |
| 3 | 1 | quase todo valor válido era rejeitado na negação | condição do `Neg` invertida | condição corrigida; verificado em 131.085 valores |
| 4 | 1 | um `Money` inválido podia existir | `New` aceitava `minor` fora da faixa | validação de faixa no `New` |
| 5 | 1 | o mesmo JSON tinha duas leituras possíveis | `UnmarshalJSON` aceitava campo desconhecido e chave repetida | `DisallowUnknownFields` + rejeição de chave duplicada |
| 6 | 5 | o serviço **rejeitaria operação legítima** por uma disputa momentânea | `55P03` (lock timeout) classificado como permanente | passou para transitório |
| 7 | 5 | lançamento **órfão**: impossível responder "por que o saldo mudou?" | `Debit` não recebia o `transactionID` | `Debit`/`Credit` exigem o ID; `NewEntry` valida |
| 8 | 5 | `UPDATE` casava com zero linhas e falhava parecendo conflito | `Debit` devolvia só o `Entry`, mas incrementa a versão em memória | tipo `Movement` carregando a `versionBefore` |
| 9 | 5 | **nenhum** lançamento conseguiria ser gravado (`22P02`) | `newEntryID` gerava string composta e a coluna é `UUID` | passou a gerar `uuid.NewString()` |
| 10 | 5 | `42702 column reference is ambiguous` ao reivindicar lote | `RETURNING` com coluna ambígua no `ClaimBatch` | colunas qualificadas pelo alias |
| 11 | refactor | `ports.IsConflict` devolveria `false` para erro do repositório, **em silêncio** | sentinelas de erro duplicados em `ports` e `postgres` | alias: um jogo só, com teste de regressão |
| 12 | refactor | conversão obrigatória em todo o serviço; a esquecida viraria string vazia | `wagering.TransactionID` e `ledger.TransactionID` eram tipos distintos | tipo único em `ids`, com alias |
| 13 | 6a | `failureCode` **em branco** na resposta para todo erro de `wagering` | `wagering.Error` era tipo próprio, e não alias de `domainerrors.Error` | alias, como nos outros pacotes; texto passou a `"CODE: mensagem"` |
| 14 | 6a | 1 a 4 pedidos **legítimos** recebiam `IDEMPOTENCY_CONFLICT` em vez de replay (3 de 4 execuções) | corrida entre as duas leituras de idempotência | a segunda leitura só é conflito se a chave da linha encontrada for **diferente**; chave igual decide pelo hash |
| 15 | 6a | `go test -tags=integration ./...` falhava com "registro não encontrado", FK violada e `40P01` no `TRUNCATE` — cada pacote passava sozinho | dois pacotes de integração truncando o **mesmo** banco em paralelo | lock consultivo (`pg_advisory_lock`) no `TestMain` dos dois pacotes |
| 16 | 6a | o segundo pacote de integração morria com `55P03` antes de a outra suíte terminar | `lock_timeout` de 3 s do pool abortava a espera pelo lock consultivo | a sessão do harness espera sem prazo (`SET lock_timeout = 0`) |
| 17 | 6b | abrir carteira obrigaria a **inventar** jogo e rodada, gravando dado falso no histórico | `CHECK ids_not_blank` exigia `game_id` e `round_id` de toda linha, inclusive `OPENING` | migration 000002: a exigência passou a valer só para operação externa |
| 18 | 6c | `GET /wallets/abc` responderia `500`; mensagem de SQS com `walletId` inválido derrubava o processamento como falha de infraestrutura | identificador malformado em coluna `UUID` vira `22P02`, que a classificação trata como permanente | guarda de formato antes da consulta (`internal/app/wagering/ids.go`), com "não encontrado" na leitura |
| 19 | 6d | o lote chegava **embaralhado** e o publisher publicaria fora de ordem (1 falha em 5 execuções) | `RETURNING` não garante a ordem das linhas que devolve | o `UPDATE` virou CTE e o `SELECT` de fora ordena por `sequence` |

## 8.2 Fases 7 e 8 — borda HTTP e consumo SQS

| # | Fase | Sintoma | Causa de fundo | Correção |
| --- | --- | --- | --- | --- |
| 20 | 7 | `POST /wallets` respondia `400 {"code":"INVALID_REQUEST","message":"... request body too large"}` para um corpo de 90 bytes com limite de 1 MiB | `chimw.RequestSize(0)` no roteador recusava **todo** corpo; dormia desde a Fase 0 porque nenhuma rota anterior recebia corpo | middleware removido; o limite real é o `MaxBodyMiddleware`, de `HTTP_MAX_BODY_BYTES` |
| 21 | 7 | o log registrava **200** para uma requisição que respondeu **500** | `AccessLog` por dentro do `Recoverer`: o `statusRecorder` ficava fora do caminho quando o `Recoverer` escrevia a resposta | `AccessLog` passou a envolver o `Recoverer` |
| 22 | 7 | `must provide constructor function, got {} (type clock.System)` | `fx.Provide` recebeu **valor**, não construtor; e o caso de uso depende das **interfaces** | providers que publicam as duas formas (`clock.System`/`ports.Clock`, `idgen.Generator`) |
| 23 | 7 | `must provide constructor function, got {[group:...]} (type fx.resultTagsAnnotation)` e depois `fx.In structs cannot be annotated with fx.ParamTags` | grupo do Fx usado como opção de `Provide` | `fx.Annotate(fn, fx.ResultTags(...))` no provider e `group` no **campo** de uma struct `fx.In` |
| 24 | 7 | `missing type: context.Context` | `context.Context` exigido por `postgres.NewPool` não existe no grafo | provider que cria `context.Background()` com prazo — **não** o contexto de start |
| 25 | 7 | contêiner `unhealthy`; e um processo curto de verificação dependia de `DATABASE_URL`/`OIDC_ISSUER` para dar veredito sobre o serviço | a flag `-health-check` não existia | modo healthcheck que não carrega a config e consulta o próprio `/health/ready` |
| 26 | 7 | três asserções de teste erradas (a **régua**, não o código) | (a) esperava envelope de erro na rejeição `422`; (b) usava `cursor=%%%`, que é texto literal válido; (c) tratava a mesma identidade externa em dois provedores como conflito | as três corrigidas, com o motivo escrito no próprio teste |
| 27 | 7 | o `/health/live` respondia `200`, mas o `http_request` saía em **texto** e o boot em **JSON** | `app.Run` montava o próprio `slog.NewJSONHandler` enquanto o resto do grafo recebia o logger da config | um logger por processo, vindo da config (`APP_ENV=local` = texto; outros = JSON) |
| 28 | 8 | as filas do LocalStack **nunca** eram criadas; `list-queues` respondia vazio | `init-queues.sh` sem shebang e sem bit de execução: o LocalStack executa o arquivo e o kernel recusava com `Exec format error` | shebang `#!/bin/sh` + `git update-index --chmod=+x`, provado reiniciando o container |
| 29 | 8 | investigar uma mensagem morta não dizia se ela foi recebida duas ou vinte vezes | a inbox não tinha `attempts`, e o `ApproximateReceiveCount` do broker se perde quando a mensagem é removida | migration 000004 (coluna + CHECK + índice parcial) e `CountAttempt` incrementando **em SQL** |
| 30 | 8 | o consumo recusou **toda** mensagem | o `messageId` do envelope era exigido e conferido contra o da entrega — que o produtor não tem como conhecer antes do `SendMessage` | identidade durável = `MessageId` **do broker**; campo do envelope opcional e conferido quando presente |
| 31 | 8 | `IDEMPOTENCY_KEY_REQUIRED` para um corpo que **tinha** a chave | um `return` que abortava o parsing devolvia requisição vazia com erro `nil` (introduzido na correção do 30) | `messageId` do envelope é opcional e o parsing segue; achado por teste unitário com o **corpo exato** do broker |
| 32 | 8 | o atraso do retry ultrapassava o `MaxBackoff` (62 s com teto de 60 s) | jitter somado **depois** do teto | jitter **subtraído dentro** do teto (0 a 25% para baixo) |
| 33 | 8 | com os **defaults**, ligar o consumidor fazia a aplicação **não subir** | `HTTP_STOP_TIMEOUT` padrão de 25 s violava a própria regra (20 s do servidor + 15 s do consumidor) | default para 50 s (era 40 s na correção), com dois testes travando os dois lados da regra |
| 34 | 8 | `wager_operations_total` não existia para operações vindas da fila: o caminho principal ficava invisível no painel | o caminho SQS não emitia a métrica de negócio | o consumidor emite a mesma métrica da borda, com `kind` tolerante (`unknown` em vez de perder a contagem) |
| 35 | 8 | `attempts` da inbox ficava em `0` para sempre | o caminho da reentrega — o único que sobrevive quando o broker não é mais consultado — não tocava no contador | inserção grava 1 (normalizado no repositório) e a reentrega faz `attempts + 1` no SQL |
| 36 | 8 | `mismatched param and argument count` nos testes de integração do repositório | o `INSERT` ganhou coluna sem os placeholders correspondentes (introduzido na correção do 35) | SQL e argumentos alinhados; pego pelos testes de `internal/infra/postgres` |

## 8.3 Fases 9 a 11 — workers, prova multi-instância e observabilidade

| # | Fase | Sintoma | Causa de fundo | Correção |
| --- | --- | --- | --- | --- |
| 37 | 9 | o `REFUND` continuava `PENDING_REFERENCE` depois da retomada, **sem erro nenhum no log** (o worker registrava "pendente", um desfecho de sucesso) | a guarda da "janela do lease" comparava o relógio com `next_resolution_attempt_at`, que o próprio claim acabou de empurrar para o futuro | a guarda passou a comparar **estado e tentativa lidos no claim** (`reference_resolution_attempt`, que o claim não altera) |
| 38 | 9 | `INVALID_TRANSITION: só é possível reagendar uma referência em PENDING_REFERENCE, estado atual PENDING` | o caminho "continua pendente" chamava `ReagendarReferencia` numa linha que ainda não tinha transitado | o desfecho pendente escolhe pelo estado: `MarkPendingReference` (de `PENDING`) ou `ReagendarReferencia` (mesmo estado) |
| 39 | 9 | os eventos emitidos na retomada sairiam **sem correlação**, e o caminho SQS nunca a preenchia | o `correlationId` morria no fim da requisição; não havia onde persistir | migration `000005` (`correlation_id`), gravada no `INSERT` e lida na reidratação; o SQS usa o `MessageId` da entrega |
| 40 | 10 | todas as instâncias indistinguíveis no `pg_stat_activity` (`map[jungleg-api:5]`): não havia como saber qual processo segurava o lock | o pool sobrescrevia o `application_name` do DSN com `"jungleg-api"` | o DSN vence o default (sem o parâmetro, o nome continua `jungleg-api`), travado por dois testes de integração |
| 41 | 10 | `/health/ready` **UP** com todas as dependências e **todo token válido** respondendo `503` dentro do container | o Keycloak anuncia `jwks_uri` em `http://localhost:8080/...`, que de dentro do container é o próprio container | `OIDC_JWKS_URL` opcional sobrescreve o endereço do JWKS; `iss` continua conferido contra `OIDC_ISSUER` |
| 42 | 11 | `jungleg_wager_transaction_conflicts_total` ficava em **0 para sempre** | `IncConflict` só era chamado pelo teste do mecanismo; nenhum caminho de produção emitia | o caso de uso conta cada repetição em `comRetentativa`, via `ports.ConflictCounter` ligado ao `observability.Metrics` no Fx |
| 43 | 11 | somar por `kind` num painel misturava tipo de operação com desfecho | o HTTP emitia `kind="PROCESSED", status="2xx_201"` e o SQS emitia `kind="BET", status="PROCESSED"` — mesmo nome, dois significados | o HTTP passou a emitir `kind=<tipo>` e `status=<desfecho>`; `StatusLabel` foi removido |
| 44 | 11 | toda linha `http_request` trazia `"kind": ""` | o middleware lia `observability.StageFrom(ctx)` e nenhum código chamava `WithStage` (sobra de um desenho abandonado) | campo removido, junto com `WithStage`/`StageFrom` (sem chamador) |
| 45 | 11 | a documentação do prazo de encerramento mentia — e o teste que acreditava nela não passava | o comentário dava a entender que `fx.StopTimeout` governava o shutdown; em Fx 1.24, `App.Stop(ctx)` obedece ao **contexto do chamador** | comentário corrigido; `TestScopeDoPrazoDeEncerramento` prova que o contexto manda e que `StopTimeout()` carrega o mesmo valor |

**O padrão que se repete nas 45 linhas**, e que vale mais que a lista: em 12 delas o
sintoma apareceu **longe** da causa (log em `app`, métrica em `handlers`, coluna em
`migrations`, identidade de mensagem em `infra/sqs`). Em 9, o defeito estava na
**régua do teste**, não no código. E em 5, o defeito estava **dormente** desde uma
fase anterior porque nenhum teste tocava o caminho: `RequestSize` desde a Fase 0, as
filas do LocalStack desde a Fase 0, `application_name` desde a Fase 5, o JWKS do
container desde a Fase 7, e a métrica de conflito desde a Fase 7. A conclusão
prática: **caminho sem teste não é caminho pronto**, mesmo quando todos os testes
existentes estão verdes.

---

# 9. Os limites conhecidos e o que ficou de fora

Esta seção é a tradução do `ARCHITECTURE.md` §16 para a pergunta que quem mantém
faz: "o que eu **não** posso prometer a quem me pergunta?". Registre isso antes de
alguém prometer por você.

## 9.1 Limitações que você pode encontrar em operação

| Limitação | O que significa no dia a dia | O que fazer / quando importa |
| --- | --- | --- |
| o encerramento gracioso depende do prazo do orquestrador | medido: `docker stop` **sem** `--timeout` matou o processo em ~3,4 s (`exit=137`) com uma mensagem em voo, e a mensagem voltou pelo `VisibilityTimeout` (60 s) em vez de pela liberação explícita. **Nada se perde** — só demora mais | o `docker-compose.yml` define `stop_grace_period: 60s` para o `api` exatamente por isso; em Kubernetes, configure `terminationGracePeriodSeconds` compatível com `HTTP_STOP_TIMEOUT` |
| `ChangeMessageVisibility(0)` não antecipa a entrega no LocalStack | a chamada devolve sucesso e a mensagem continua invisível até o prazo original da fila; em SQS real ela antecipa | nos testes, o `VisibilityTimeout` da fila é encurtado para 2 s em vez de empurrar a mensagem; em produção, o comportamento é o do SQS |
| a queda entre commit e delete é **reproduzida**, não provocada por sinal | a janela dura microssegundos e o Windows não tem `SIGTERM` de host. O que se reproduz é o estado durável (transação commitada + inbox `PROCESSED` + mensagem ainda na fila), com o consumidor real e só o `Delete` embrulhado | a prova de sinal de verdade está no container Linux (`docs/evidencias/FASE_10.md` §6: `exit=0`, `sqs_visibilidade_liberada`) |
| o encerramento dos laços (SIGTERM) não foi provado **no Windows** | `Stop-Process` é terminação dura | a prova de sinal é do ambiente Linux/container; no Windows, não prometa "shutdown gracioso comprovado" |
| o reaper repete indefinidamente uma linha que não consegue concluir | não há contador durável de falhas para `PENDING` sem referência, e conflito de ledger é classificado como transitório; o desfecho `FAILED` só é dado quando a carteira não existe | se aparecer uma linha `PENDING` inconsistente, ela vai girar; intervenha à mão ou implemente o contador antes de confiar no reaper |
| o `PENDING` do reaper é **inalcançável** por caminho normal | o caso de uso grava `PENDING` e o estado definitivo no **mesmo commit**, então nenhuma linha `PENDING` é confirmada | o reaper é segurança extra (README §6.3) e só é provado por inserção direta; no dia em que existir aceite assíncrono, ele já está lá |
| a ordem por carteira depende do tamanho do lote | com mais eventos pendentes da **mesma** carteira do que `OUTBOX_BATCH_SIZE` numa rodada, dois publishers podem dividir a carteira e a ordem de **envio** fica sujeita ao escalonamento. Na prática (lote 50), é preciso uma rajada de 50+ eventos da mesma carteira; com `OUTBOX_BATCH_SIZE=1` isso ficou **observado** (Fase 10) | o consumidor pode (e deve) ordenar por `walletVersion` do `WalletBalanceChanged`; não prometa ordem global, prometa ordem **por carteira** |
| a distribuição de publicações entre réplicas não é asserida | a divisão depende de escalonamento (observado 4/4/4) | o que se promete é a **partição**: 12 eventos → 12 mensagens, sem repetição e sem perda |
| publicação sem `SendMessageBatch` | cada evento é um `SendMessage`; o lote existe no claim, não no envio | é o que mantém o `MarkPublished` por evento e a recuperação observável; se o volume crescer, o custo é de latência do publisher |
| a URL da fila é derivada do endpoint | com `SQS_ENDPOINT` (LocalStack) a URL é `<endpoint>/000000000000/<nome>`; **sem** ele, o que sobra é o **nome**, e a resolução por `GetQueueUrl` não está exercitada | em produção na AWS, ajuste a resolução de URL antes de subir (é o primeiro item da lista de "trabalho não concluído") |
| a latência medida é de cliente local, na mesma máquina dos processos | p99 166 ms em carteiras distintas × 579 ms na mesma carteira: é comparação entre duas topologias de contenção, **não** número de produção | o README §14 não pede meta de RPS; não use esses números como capacidade |
| `operations_total` conta **atendimentos**, inclusive replay | um replay é uma resposta atendida, e a latência dele também é medida | quem conta operação financeira distinta é `operations_total` − `idempotent_replays_total` |
| `transaction_conflicts_total` conta **repetições**, não erros | cada `comRetentativa` que vai tentar de novo incrementa; a tentativa final que sobe com o erro não incrementa | o teste exige o valor exato (2) para fixar o contrato; não leia esse contador como "número de erros" |
| `retries_total{stage}` tem os 8 emissores provados em teste de **unidade** | provocar a falha de banco exigiria derrubar o banco compartilhado da suíte | a emissão é do código de produção e a porta falha de propósito; a exposição em `/metrics` é provada nos testes de integração das demais métricas |
| o readiness **não** é métrica | `/health/ready` é JSON | para alertar "workers DOWN" é preciso o `blackbox_exporter` (`probe_success` sobre o readiness), configurado em `deploy/observability/prometheus.yml`; sem ele, o alerta de outbox atrasada cobre o sintoma em minutos |
| a idade da outbox é medida a cada 15 s | um pico curto entre medições não aparece no gauge | aparece no log `outbox_atrasada` (WARN) do ciclo em que aconteceu |
| o dashboard não é exercitado em CI | é JSON validado e as regras passam no `promtool`, mas não há Grafana no compose | o painel é importado à mão uma vez (decisão registrada na Fase 11) |
| sem introspecção de token (RFC 7662) | a validação é **local** (assinatura + claims), que é o comportamento normal de OIDC/JWT. Revogar um token antes do `exp` **não é detectado** | se a revogação imediata for requisito, é preciso introspecção — ao custo de uma ida ao IdP por requisição |
| `azp` só é conferido com múltiplas audiences | regra do OIDC Core §3.1.3.7; o realm deste projeto emite uma audience só | não há o que conferir hoje; se o realm passar a emitir várias, a regra já está no código |
| sem rate limiting por identidade | não é exigido pelo enunciado; com o lock por carteira, o custo de uma rajada é contenção de linha, não corrupção de saldo | se a proteção for necessária, ela entra na borda — e **não** substitui o lock |

## 9.2 Escolhas de modelagem que você vai achar feias (e são propositais)

| Escolha | Por que ficou assim |
| --- | --- |
| `Rehydrate` da transação tem ~20 parâmetros | espelha as colunas e evita depender de ordem de campo. Feio, mas explícito e seguro contra troca de argumentos |
| `Kind` é `string`, então um valor inválido responde `true` a `MovesMoney` | mitigado por `Validate` rodar **antes** em todo caminho. Um tipo fechado exigiria enumeração em Go, que não existe |
| sem foreign key de `outbox_events` para o agregado | a outbox precisa sobreviver à remoção do agregado; ela é a fila, não um detalhe dele |
| `LOSS` com referência: o serviço confere quando ela resolve | o README permite referência opcional; a régua adotada é que referência informativa **não bloqueia** a operação (`docs/evidencias/FASE_6.md` §8) |
| o dublê em memória não prova lock nem concorrência | ele tem semântica de transação (cópia do estado, troca só no fim sem erro), mas não tem `FOR UPDATE`. Lock, *lost update* e disputa entre processos são provados contra o PostgreSQL real |
| `next_resolution_attempt_at` fica preenchido em reversão já `PROCESSED` | efeito de usar `MarkPendingReference` + `ResolveReference`, que é o único caminho que o agregado oferece para vincular a referência interna. A coluna só é lida para `PENDING_REFERENCE`, então o valor é inerte |
| `ListLedger` faz uma consulta a mais por página | confere a existência da carteira antes de listar, para não devolver página vazia ambígua |
| `correlation_id` é **nullable** | linhas anteriores à migration 000005 (e caminhos que não propagam) ficam nulas; a retomada cai para o `transactionId`. Não é invariante financeira, é metadado de transporte |
| `Reconcile` não emite métrica nem log | é decisão de camada: o handler emite `wager_reconciliation_divergences_total` e o log `reconciliacao_divergente`; o caso de uso continua sem conhecer Prometheus |

## 9.3 O que não existe, em lista direta

- **Consumidor de `wallet-events.fifo`.** O evento é **publicado**, e o contrato de
  consumo está documentado (`ARCHITECTURE.md` §11, com as 7 regras: dedup durável por
  `eventId`, ordem por `walletVersion`, DLQ sem bloquear o grupo, etc.). O consumidor
  em si não está implementado — de propósito, porque o README §11 pede a publicação.
  **Se você for implementá-lo, comece pelas 7 regras**: deduplicar por `eventId` num
  registro durável (a janela de 5 min do broker **expira** e não é garantia) e não
  reordenar entre carteiras.
- **Tracing com OpenTelemetry.** Diferencial opcional do §14; não implementado. O
  `correlationId` cobre o rastreio por log, mas não há span distribuído.
- **Contador durável de falhas do reaper.** Ver a linha correspondente em 9.1: uma
  linha `PENDING` inconsistente é repetida indefinidamente em vez de virar `FAILED`.
- **Carga e capacidade.** Há medição de latência sob concorrência (Fase 10), mas não
  há teste de carga com metodologia, throughput e p99 de produção — o §14 trata isso
  como diferencial opcional e não há meta de RPS.
- **`README_SOLUCAO` na raiz.** O `README.md` da raiz é o **enunciado** e não pode ser
  editado; as instruções de execução estão consolidadas em outro lugar (é a decisão
  editorial registrada na conformidade, ⚠️ 3).

## 9.4 As três coisas que eu verificaria primeiro ao assumir este código

1. **Existe teste para o caminho que você vai mexer?** A lição das 45 linhas do
   capítulo 8 é que defeito dormente mora em caminho sem teste — e cinco defeitos
   ficaram escondidos por fases inteiras exatamente assim.
2. **A mudança toca a ordem de alguma coisa?** A ordem do lock, a ordem dos eventos,
   a ordem de encerramento e a ordem de classificação de erro são as quatro coisas
   que este sistema usa como garantia. Mudar qualquer uma delas muda garantia, não
   só estilo.
3. **O erro que você está vendo é de negócio ou de infraestrutura?** Quase todo
   defeito caro da lista nasceu de confundir os dois — e a consequência é sempre a
   mesma: uma operação legítima recusada, ou um erro permanente tratado como
   transitório até envenenar a fila.

---

*Documento derivado de `README.md` (enunciado, fonte de verdade), `ARCHITECTURE.md`
(§1–§17), `docs/CONFORMIDADE_README.md` (23 interpretações registradas) e
`docs/evidencias/FASE_0.md` a `FASE_11.md`. Quando este texto e o repositório
divergirem, vale o repositório — e o `grep` resolve em segundos.*






