# ENTREVISTA.md — treino de defesa do desafio

> Documento de treino para a entrevista do desafio **já concluído**. Cada resposta tem
> três partes: a fala de 30–60 s, o detalhe que só aparece se insistirem, e o número ou
> **nome de teste que existe no repositório**.
>
> **Regra deste arquivo:** nada aqui é decorado. Todo número saiu de `docs/evidencias/`
> ou do próprio código, e todo nome de teste existe no repositório (`func Test...`). Quando
> o enunciado admitia duas leituras, está dito qual foi adotada e por quê. Caminhos são
> relativos à raiz do repositório.
>
> **Como treinar:** leia só a pergunta, responda em voz alta, e depois confira. Se a
> resposta não terminou em um número ou um teste, ela ainda não está pronta.

---

## Bloco 1 — Arquitetura

### "Por que `READ COMMITTED` com `SELECT ... FOR UPDATE` e não `SERIALIZABLE`?"

**Resposta (30–60s):** Porque o conflito real deste domínio é "duas operações na MESMA
carteira", e o lock de linha trata exatamente esse caso sem serializar mais nada.
`SERIALIZABLE` detecta conflito abortando a transação (40001) e exigiria retry em quase
todo caminho de escrita, incluindo transações que não conflitam de verdade. Com lock de
linha, o retry só acontece sob disputa real, e o conflito continua sendo resolvido pelo
banco — a prova é a corrida de duas apostas de 80.00 sobre 100.00 entre instâncias
diferentes: uma `PROCESSED`, uma `REJECTED/INSUFFICIENT_FUNDS`, saldo 20.00, um débito.

**Se insistirem:** o preço é que a linha fica presa até o commit, então existe
`lock_timeout` por conexão (`DB_LOCK_TIMEOUT`) e o `55P03` é classificado como
**transitório** e repetido (3 tentativas, 20 ms × tentativa). Classificá-lo como permanente
faria o serviço rejeitar uma operação legítima por uma disputa momentânea — foi o defeito 6
da §15. E `SERIALIZABLE` não substituiria nenhuma constraint: o saldo nunca negativo
continua sendo `CHECK` no schema, não consequência do nível de isolamento.

**Número/teste que sustenta:** `TestT2DuasApostasDe80Sobre100EmInstanciasDiferentes` e
`TestWalletLostUpdateNaoAcontece` (dois débitos de 100 e 200 sobre 1000 terminam em
**700.00**, versão **3**).

### "Por que transactional outbox, e não publicar direto no SQS?"

**Resposta (30–60s):** O SQS é externo e não participa do commit. Publicar direto cria
duas falhas: o broker aceita e o commit falha — o evento fala de uma mudança que não
aconteceu; ou o commit passa e o SQS falha — a mudança acontece e ninguém é avisado. Com
a outbox, o evento entra na mesma transação do saldo, do ledger e da inbox: ou tudo
entra, ou nada entra. A entrega vira "pelo menos uma vez", e a pior falha possível passa
a ser **republicação**, não perda — o mesmo `eventId` sai de novo e o consumidor
deduplica.

**Se insistirem:** o publicador publica **fora** da transação, de propósito: segurar
`FOR UPDATE` e uma conexão do pool durante um round-trip ao broker esgotaria o pool com
poucas instâncias, e o lease (30 s) precisa estar **confirmado** para o evento voltar
sozinho se o processo morrer. A ordem também é a garantia: publicar **antes** de marcar;
marcar antes diria "publiquei" antes de publicar.

**Número/teste que sustenta:** `TestFalhaNaOutboxDesfazTudoNoBancoReal` (saldo volta a
1000.00, **0** lançamentos, **0** eventos),
`TestQuedaEntreCommitEPublicacaoOutraInstanciaAssume`,
`TestQuedaEntrePublicacaoEMarkPublishedRepublicaComOMesmoEvento` (4 mensagens, 2
`eventId`, corpo idêntico) e `TestT6TresPublishersReaisDisputamAMesmaOutbox` (12 eventos
→ 12 mensagens, 0 duplicados, 0 pendentes, 4/4/4).

### "Por que o ledger é um agregado separado da carteira?"

**Resposta (30–60s):** Porque respondem perguntas diferentes com custos diferentes. A
carteira diz "quanto tem agora": uma linha por carteira, mutável, e é ela que o
`FOR UPDATE` protege. O ledger diz "por que chegou nisso": uma linha por movimentação,
imutável, e é a prova auditável. Fazer um débito de R$ 5,00 somando o histórico inteiro
seria inviável; e o ledger **não** é reconstruível da carteira, porque o saldo atual não
conta como ele chegou ali. Cada lançamento guarda `balanceBefore` e `balanceAfter`, então
a linha se explica sozinha.

**Se insistirem:** a aritmética é conferida em dois lugares de propósito — no domínio
(`ledger.NewEntry` recusa `after != before ± amount`, dando mensagem boa ao cliente) e no
`CHECK` da tabela (a barreira que sobrevive a bug de aplicação). E o ledger tem
`UNIQUE (wallet_id, transaction_id)`, então dois lançamentos para a mesma operação são
impossíveis mesmo por SQL escrito à mão.

**Número/teste que sustenta:** `TestReconcileFechaOExemploDoREADME` (975.00/975.00,
diferença 0.00, `checkedEntries` **2**), `TestLedgerImutavelNoBanco` e
`TestConferenciaFinalCruzandoHTTPESQS` (soma em SQL puro **101000** == saldo **101000**
em 4 lançamentos).

### "Por que o domínio não importa `encoding/json`?"

**Resposta (30–60s):** No caminho que decide dinheiro — o hash de idempotência — o
encoder é **injetado** (`PayloadEncoder`, apontando para `platform/canonicaljson`). O
domínio define **o que** é payload de negócio; o determinismo (chaves ordenadas em todos
os níveis) é da plataforma. O motivo é consequência direta: se o mesmo pedido serializar
em duas ordens diferentes, os hashes divergem, o reenvio vira operação nova e o débito
acontece duas vezes.

**Se insistirem:** com honestidade, o domínio não é 100% livre de JSON hoje: `money`
importa `encoding/json` porque implementa o contrato externo (`UnmarshalJSON` estrito,
que recusa campo desconhecido e chave repetida) e `wagering/events.go` serializa o
envelope do evento. O que está isolado é o **hash** — que é o único ponto onde
não-determinismo custa dinheiro. `TestImportsPermitidos` existe justamente para deixar a
lista branca de imports explícita em vez de confiar em memória.

**Número/teste que sustenta:** `TestEstruturaIgualProduzMesmoHash`,
`TestNewPayloadHashDeterministico`, `TestSQSESHTTPCompartilhamAIdempotencia` (a mesma
operação pelas duas portas gera o mesmo hash e **um** débito) e `TestImportsPermitidos`.

### "Por que UUID v7, e não v4 ou `BIGSERIAL`?"

**Resposta (30–60s):** Porque o v7 tem o instante nos bits mais significativos: os ids
gerados em sequência ficam ordenados no tempo, o índice primário do B-tree fica denso
(com v4 aleatório cada insert toca uma página diferente) e dá para ordenar fatos sem
consultar o banco. `BIGSERIAL` é mais rápido, mas exige ida ao banco para saber o
identificador — e a aplicação precisa dele **antes** do insert, porque o evento da outbox
e o lançamento do ledger já o referenciam.

**Se insistirem:** o custo é que o id vaza o instante de criação (metadado, não segredo),
e o gerador passa por interface (`idgen.Generator`) para o teste injetar ids
determinísticos em vez de comparar UUIDs aleatórios. Se a entropia do sistema falhar, o
código cai para v4 — continua único, perde a ordenação.

**Número/teste que sustenta:** `TestNewIDEhVersao7`, `TestNewIDCresceNoTempo` e
`TestNewIDGeraUUIDValidoEUnico`.

### "Por que o mesmo caso de uso atende HTTP e SQS?"

**Resposta (30–60s):** Porque duas implementações "parecidas" divergem, e a divergência
mais cara é o hash do payload: sairia diferente entre as portas e a mesma operação seria
debitada duas vezes. O consumidor chama `ProcessWithHooks` — a **mesma** função que o
HTTP usa por dentro — com ganchos que gravam a inbox **dentro** da transação da operação.
O HTTP não paga nada por isso: `Process(ctx, req)` é `ProcessWithHooks(ctx, req, nil)`.
Consequência: a mesma operação enviada pelas duas portas produz **um** débito.

**Se insistirem:** a chave financeira vem do corpo (`data.idempotencyKey`), **não** do
`MessageDeduplicationId` — deduplicação de entrega não é deduplicação de negócio. E o
gancho `OnBegin` roda antes de qualquer leitura de domínio; registrar a inbox depois do
trabalho não protegeria nada, porque a reentrega debitaria e só então descobriria que era
duplicata.

**Número/teste que sustenta:** `TestSQSESHTTPCompartilhamAIdempotencia`,
`TestSQSConcorrenteComHTTPNaoDuplicaDebito` e `TestConferenciaFinalCruzandoHTTPESQS`
(HTTP na api-1 e SQS na api-2 na mesma operação → um único débito).

### "Por que Uber Fx? O que ele dá que um `main` na mão não daria?"

**Resposta (30–60s):** Três coisas concretas. Primeiro, ordem de encerramento automática:
`OnStop` roda na ordem inversa do registro, então o servidor para de aceitar antes de o
pool fechar — fechar o pool primeiro deixaria requisição em andamento sem banco.
Segundo, validação do grafo **sem subir nada** (`fx.ValidateApp`), que reprova composição
errada em teste, com e sem os workers ligados. Terceiro, grupos
(`group:"health_checkers"`), que somam dependências novas ao readiness sem alterar o
`httpapi`. Consequência medida: o encerramento gracioso no container termina com
`exit=0` em 8,09 s, com o pool fechado por último.

**Se insistirem:** custou tempo em três armadilhas reais — `Provide` recebe **construtor**
e não valor; grupo é `fx.Annotate(..., fx.ResultTags)`, não opção de `Provide`, e struct
`fx.In` recebe o `group` no campo; e o pool não pode receber o contexto de boot (ele é
cancelado quando o boot termina e derrubaria o pool no primeiro `Start`). Além disso,
`fx.StopTimeout` **não** governa `App.Stop(ctx)` no Fx 1.24 — quem manda é o contexto do
chamador (defeito 45).

**Número/teste que sustenta:** `TestValidateAppComposition`,
`TestHooksDeParadaNaOrdemInversa`, `TestScopeDoPrazoDeEncerramento` e
`TestCicloDeVidaFxComStartEStopReais`.

---

## Bloco 2 — Decisão

### "Por que `int64` em unidades mínimas, e não `float64` ou decimal?"

**Resposta (30–60s):** `float64` é binário e não representa decimais exatamente
(`0.1 + 0.2 != 0.3`); num ledger esse resíduo acumula e a soma dos lançamentos deixa de
fechar com o saldo — o sistema passaria a mentir sobre dinheiro. `int64` em centavos é
exato por construção e a persistência em `BIGINT` preserva exatamente. O limite é
`|minor| ≤ 9_000_000_000_000_000` (R$ 90 trilhões), ~1000× abaixo do teto do `int64`
**de propósito**: as guardas de overflow precisam conseguir somar dois valores no limite
sem estourar elas mesmas.

**Se insistirem:** decimal de precisão exata também resolveria, mas custa alocação e
dependência, e a escala do contrato é fixa em 2. Overflow é tratado em `New`, `Add`,
`Sub` e `Neg`, e a prova foi **varredura exaustiva de 81 combinações**, que achou 10 furos
envolvendo dois negativos — "uma guarda basta" esteve errado todas as vezes em que foi
afirmado sem prova.

**Número/teste que sustenta:** `TestSemPontoFlutuanteNoCodigo` (recusa `float`, `math` e
`strconv.ParseFloat` no caminho monetário), `TestAddPropriedades` e
`TestCreditEstouraOLimiteSemAlterarEstado`.

### "Por que o `FOR UPDATE` vem antes de ler o saldo?"

**Resposta (30–60s):** Porque ler primeiro e travar depois deixa a janela: outra
transação altera o saldo entre a leitura e o lock, e a decisão de débito/crédito usa saldo
velho. Com o lock antes, quem chega junto **espera** e lê o saldo já atualizado. A prova é
numérica: duas goroutines debitando 100 e 200 de uma carteira com 1000 terminam em
**700.00** e versão **3**; sem o `FOR UPDATE`, o resultado seria 900 ou 800.

**Se insistirem:** o instante (`now`) também é lido depois do lock. Ele vai para o
`occurredAt` dos eventos, e a outbox é lida por `sequence` com `MessageGroupId` = carteira;
lido antes do lock, o instante seria a ordem de **início** das transações, não a dos
efeitos confirmados, e dois eventos da mesma carteira poderiam chegar com o `balanceAfter`
invertido. Depois do lock, os instantes da mesma carteira são monotônicos porque o lock os
serializa.

**Número/teste que sustenta:** `TestWalletLostUpdateNaoAcontece` (700.00, versão 3) e
`TestWalletGetByIDForUpdateBloqueiaOutraTransacao` (A segura o lock por 1,5 s; B esperou
**1,59 s** e só então leu).

### "Por que a unidade de retry é a transação, e não a chamada que falhou?"

**Resposta (30–60s):** Porque repetir uma chamada **dentro** da transação é o erro
clássico: a tentativa que falhou deixa a conexão com a transação aberta segurando o lock,
e a repetição trava. O sintoma aparece longe — um `TRUNCATE` de teste morrendo com
deadlock. Então o retry envolve o `Run` inteiro: 3 tentativas, 20 ms × tentativa, e só
para dois casos — falha transitória (`55P03`, deadlock, conexão) e violação de unicidade
(`23505`, que aqui significa "alguém chegou primeiro", não erro de banco).

**Se insistirem:** rejeição de negócio nunca é repetida — é desfecho, não falha. E retry
esgotado **não** grava `FAILED`, porque a transação não commitou e não existe linha para
marcar; o erro transitório sobe (503 na borda) e o cliente pode repetir. `FAILED` fica
reservado ao caminho em que a linha já existe: a retomada de referência.

**Número/teste que sustenta:** `TestFalhaTransitoriaERepetida` (2 execuções, **um**
lançamento), `TestRetentativaEsgotadaDevolveErroTransitorio` e
`TestConflitoDeUnicidadeERepetidoEContado`.

### "Por que quarentena em vez de descarte na outbox?"

**Resposta (30–60s):** Porque descartar apaga um fato financeiro **confirmado** e marcar
como publicado mente. O enunciado não define o que fazer com o evento que esgota as
tentativas; a escolha foi deixá-lo pendente, com `last_error` e `available_at` empurrado
por `OUTBOX_PARKED_BACKOFF`. Nada é apagado e nada é marcado como publicado — e o atraso
da outbox denuncia o evento emparedado, então um operador consegue desquarentená-lo.

**Se insistirem:** o custo é real: a fila ganha um item que nunca anda sozinho, e por isso
o alerta tem de existir — `JunglegOutboxEmQuarentena` dispara com
`increase(...{result="failed"}[15m]) > 0`, e `JunglegOutboxAtrasada` com
`pending_age > 60` por 5 min (2× o lease de 30 s). A alternativa "apagar e logar" perderia
o dado sem ninguém perceber.

**Número/teste que sustenta:** `TestPublicadorColocaEmQuarentenaAposEsgotarTentativas` e
`TestPublicadorColocaEmQuarentenaNaRecusaPermanente`.

### "Por que o `MessageDeduplicationId` é por tentativa e não o `eventId`?"

**Resposta (30–60s):** Porque com o `eventId` o SQS **engoliria a republicação** dentro da
janela de 5 minutos: o evento que voltou do lease seria deduplicado pelo broker e a
mensagem já entregue continuaria sendo a única — o contrário do que o §11 pede
("republicações devem preservar o `eventId`"). Com `<eventId>-t<tentativa>-<nonce>` a
republicação acontece de verdade, e a deduplicação que vale é a do **consumidor**, por
`eventId`, com registro durável e sem janela.

**Se insistirem:** o nonce não é enfeite — cobre o caso em que o número da tentativa não
muda: uma queda entre a publicação e o `MarkPublished` **não** incrementa `attempts`, e o
segundo envio teria o mesmo dedup id e seria engolido. Ou seja: a dedup do broker é
conveniência de 5 minutos; a garantia é nossa.

**Número/teste que sustenta:** `TestOutboxRepublicacaoPreservaOEvento` (MessageId distinto,
mesmo `eventId` e mesmo corpo) e
`TestQuedaEntrePublicacaoEMarkPublishedRepublicaComOMesmoEvento` (4 mensagens, 2
`eventId`, cada um duas vezes).

### "Por que índice único **parcial** na idempotência?"

**Resposta (30–60s):** Porque a unicidade que eu preciso não é global, é por tipo de
operação. `UNIQUE (idempotency_key) WHERE kind = 'OPENING'` impede crédito inicial
duplicado sem proibir que a mesma chave apareça em operação normal do provedor. E
`UNIQUE (reference_transaction_id, kind) WHERE reference_transaction_id IS NOT NULL`
impede a segunda reversão do mesmo tipo sem fazer dois `WIN` que apontam para a mesma
`BET` colidirem — em `BET`/`WIN`/`LOSS` a referência é informativa e não grava
`reference_transaction_id`.

**Se insistirem:** a alternativa seria "consultar se existe e inserir se não existir", e
ela tem janela: dois processos consultam, ambos veem "não existe", ambos inserem. Com o
índice, quem chega segundo recebe `23505` e repete a transação inteira; a segunda passada
lê o gravado e decide entre replay e conflito. Não existe janela.

**Número/teste que sustenta:** `TestTxnOpeningIdempotencia` (23505 no crédito inicial
duplicado), `TestReversaoDuplicadaBarradaPeloIndiceDoBanco` e `TestTxnInsertEIdempotencia`.

---

## Bloco 3 — Garantia

### "Como você impede um débito duplicado?"

**Resposta (30–60s):** Com três camadas independentes. A primeira é o índice único
`(provider_id, idempotency_key)` mais a decisão de replay no caso de uso: mesma chave com
mesmo hash devolve o resultado persistido **sem movimentar**, hash diferente responde
409. A segunda é o índice único do ledger `(wallet_id, transaction_id)`, que sobrevive a
qualquer bug acima. A terceira é a inbox por `(consumer_name, message_id)`, para reentrega
de mensagem. A prova que separa as camadas: 50 entregas **distintas** (50 `MessageId`) com
o mesmo conteúdo produzem 50 linhas de inbox `PROCESSED` e **um** débito — quem barrou 49
foi a idempotência financeira, não a inbox.

**Se insistirem:** o replay devolve o saldo do **processamento original** (75.00), não o de
agora, e zero é saldo legítimo — a distinção entre "não calculado" e "zero" vem de
`HasResultBalance`, nunca do valor. E são duas leituras (chave e identidade externa),
sendo que a segunda compara a chave da linha encontrada: sem isso, uma linha que aparece
entre as duas consultas vira 409 num pedido legítimo.

**Número/teste que sustenta:** `TestT1MesmaAposta50VezesEntreTresInstancias` (1 resposta
201 + 49 respostas 200, um débito, versão 2), `TestT1MesmaOperacao50VezesPelaFila` (50
inbox `PROCESSED`, um débito) e `TestReplayDevolveOSaldoDoProcessamentoOriginal`.

### "Onde exatamente está a proteção contra lost update?"

**Resposta (30–60s):** Em duas camadas que cobrem coisas diferentes. O
`SELECT ... FOR UPDATE` serializa quem chega junto; o
`UPDATE wallets SET ... WHERE id = $1 AND version = $3` recusa gravar sobre estado que
mudou por outro caminho — zero linhas afetadas devolve conflito em vez de sobrescrever. Só
o lock deixaria passar uma escrita fora do fluxo; só a versão faria uma das transações
falhar à toa em vez de esperar. Prova: dois débitos concorrentes de 100 e 200 sobre 1000
terminam em **700.00** com versão **3**.

**Se insistirem:** o `UPDATE` recebe `mov.VersionBefore()`, não `carteira.Version()` — o
débito já incrementou a versão em memória, e ler depois devolveria a versão nova: o
`UPDATE` casaria com zero linhas e a operação falharia parecendo conflito de concorrência
(defeito 8). A correção foi na **forma da API** (o domínio devolve a versão anterior no
`Movement`), não no repositório, para o erro deixar de ser possível de cometer.

**Número/teste que sustenta:** `TestWalletLostUpdateNaoAcontece` e
`TestWalletUpdateComVersaoErrada` — e a regra que acompanha os dois, provada em
`TestVersaoSoAvancaQuandoOSaldoMuda`: a versão só avança quando o saldo muda.

### "O que acontece se o processo morrer entre o commit e o delete da mensagem?"

**Resposta (30–60s):** A mensagem volta quando a visibilidade vence, a instância nova a
recebe e a inbox **recusa**: `attempts` vai de 1 para 2, nenhum débito novo, a mensagem é
removida e a DLQ fica vazia. No container, com `SIGTERM` de verdade, o processo parou de
buscar trabalho, deu 5 s ao que estava em voo, **liberou a visibilidade** do que não
terminou, parou o HTTP e fechou o pool por último, saindo com `exit=0` em 8,09 s.

**Se insistirem:** honestamente, a camada automatizada **reproduz o estado durável** de uma
queda — transação commitada, inbox `PROCESSED`, mensagem ainda na fila — com o consumidor
real e só o `Delete` embrulhado. O que eu não fiz foi matar o processo na janela de
microssegundos, que não é reproduzível por sinal; o sinal de verdade está provado no
container Linux. O resultado é o mesmo nos dois caminhos.

**Número/teste que sustenta:** `TestT5QuedaEntreCommitEDeleteReentregaReconhecida`
(`attempts` 1→2 em 2,013 s), `TestProcessarReentregaNaoReaplica` e
`TestSQSReentregaDaMesmaMensagemNaoReaplica`.

### "Se o broker entregar a mesma mensagem 50 vezes, o que acontece?"

**Resposta (30–60s):** Depende de qual "mesma" é. Se forem 50 entregas do **mesmo**
`MessageId`, a inbox barra 49 — uma linha, `PROCESSED`. Se forem 50 entregas **distintas**
com o mesmo conteúdo (o caso real de republicação, e o que o teste faz), a inbox não barra
nada por desenho e quem barra é a chave + hash + índice único: 50 `MessageId`, 50 linhas de
inbox `PROCESSED` e **um** débito.

**Se insistirem:** o contador `attempts` é do consumidor e incrementa em SQL
(`attempts = attempts + 1`), não a partir do valor lido — duas instâncias recebendo a mesma
mensagem gravariam o mesmo valor final em vez de somar as entregas. Existe porque o
`ApproximateReceiveCount` do broker se perde quando a mensagem é removida ou vai para a
DLQ: sem ele, investigar uma mensagem morta não diria se foi recebida duas ou vinte vezes.

**Número/teste que sustenta:** `TestT1MesmaOperacao50VezesPelaFila` (50 inbox
`PROCESSED`, um débito) e `TestProcessarReentregaNaoReaplica` (`attempts = 2`).

### "Como você sabe que o saldo armazenado está certo?"

**Resposta (30–60s):** Reconstruindo o saldo pela soma do ledger, incluindo a abertura, e
comparando com o saldo gravado — numa **única** transação `REPEATABLE READ` somente-leitura.
Fora dela, uma operação confirmada entre a leitura do saldo e a soma produziria uma
divergência que nunca existiu (o falso positivo clássico). `difference = stored −
calculated` e pode ser negativa: o sinal é informação. A conferência **não altera** nada, e
isso não é promessa: escrita em modo somente-leitura é recusada pelo PostgreSQL.

**Se insistirem:** a conferência também é feita por fora do código, somando a tabela em SQL
puro, porque uma verificação que usa o mesmo caminho que ela confere é tautologia.
Divergência vai para três lugares: resposta, log `reconciliacao_divergente` (com
`storedMinor`/`calculatedMinor`) e métrica `wager_reconciliation_divergences_total`, que
alimenta o alerta `JunglegDivergenciaDeReconciliacao` em ≤ 5 min.

**Número/teste que sustenta:** `TestReconcileFechaOExemploDoREADME` (975.00/975.00,
diferença 0.00, 2 entradas), `TestReconcileDetectaSaldoAdulteradoESeRecusaAMexer`
(diferença 50.00 e saldo intocado em 15000) e `TestTransacaoSomenteLeituraRecusaEscrita`.

### "Como você impede saldo negativo?"

**Resposta (30–60s):** Em três lugares que não dependem um do outro: no domínio
(`wallet.Debit` devolve `ErrInsufficientFunds` sem alterar estado), no schema (`CHECK
balance_minor >= 0` e o `CHECK` de coerência do lançamento) e na serialização (o débito
acontece depois do lock, lendo o saldo já atualizado). Na corrida de 80+80 sobre 100, isso
termina com uma rejeição e saldo **20.00** — medido entre três processos.

**Se insistirem:** o código de falha separa duas coisas que o domínio devolve igual: um
`INSUFFICIENT_FUNDS` é "o jogador não tem saldo para apostar", o outro
`ROLLBACK_INSUFFICIENT_FUNDS` é "não dá para desfazer". Quem separa é o tipo da operação,
porque o sentinela é o mesmo — e quem consome o erro precisa saber se pode tentar de novo
ou se a operação é definitiva.

**Número/teste que sustenta:** `TestT2DuasApostasDe80Sobre100EmInstanciasDiferentes`,
`TestWalletSaldoNegativoRecusadoPeloBanco` e `TestROLLBACKSemSaldoTemCodigoProprio`.

### "Qual identidade de mensagem você usa: o `messageId` do envelope ou o do broker?"

**Resposta (30–60s):** A do **broker**, com o campo do envelope opcional e conferido
quando presente. O §10 pede "o `messageId` do envelope", e essa é a leitura em que o
enunciado admite duas interpretações: quem atribui esse valor é o broker, no
`SendMessage`, e o produtor não tem como conhecê-lo antes de enviar. Exigir o campo
obrigaria a inventar um segundo identificador para a mesma mensagem — e aí eles podem
divergir. A leitura adotada está registrada como interpretação 12 em
`docs/CONFORMIDADE_README.md`.

**Se insistirem:** a régua final ficou: o campo do envelope, quando presente, **precisa
coincidir** com o da entrega; divergência é mensagem inválida (permanente → DLQ). E isso
não é teoria: a primeira versão exigia a coincidência e o consumo recusou **toda**
mensagem, porque o teste preenchia o envelope com um id inventado (defeito 30).

**Número/teste que sustenta:** `TestProcessarSemMessageIDNoEnvelopeEhAceito`,
`TestProcessarComIdentidadeDivergenteEhInvalida` e `TestDecodificarCorpoRealDoBroker`.

---

## Bloco 4 — Concorrência

### "Como você provou que três processos não se atrapalham?"

**Resposta (30–60s):** Subindo três **processos do binário** (portas 18091–18093), cada um
com PID, pool e memória próprios, e medindo no banco: `pg_stat_activity` mostra três
conjuntos **disjuntos** de conexões por `application_name` (`api-1`, `api-2`, `api-3`), e
matar uma remove só as conexões dela, sem afetar o atendimento das outras duas. Não é
afirmação: é o banco dizendo quem é quem.

**Se insistirem:** o §8 pede "três processos independentes, cada um com suas próprias
conexões e memória" — um processo do binário tem exatamente isso. A interpretação 20 da
conformidade registra que **não** são três containers: o container entra na prova de
`SIGTERM`, que é o que o processo local no Windows não consegue provar. Isso exigiu o
`application_name` vindo do DSN (defeito 40), que antes era sobrescrito pelo default.

**Número/teste que sustenta:** `TestTresInstanciasIndependentes` e
`TestApplicationNameVemDoDSNQuandoDefinido`.

### "Existe lock global em algum lugar?"

**Resposta (30–60s):** Não. O lock é `SELECT ... FOR UPDATE` na **linha** da carteira, e o
instrumento de medida é o próprio banco: com a carteira A presa, `pg_stat_activity`
filtrado pelas conexões das instâncias mostrou **zero** backends esperando lock enquanto
operações em outras carteiras andavam. A espera apareceu só na linha da carteira presa,
com `wait_event_type = Lock` e `wait_event = transactionid`.

**Se insistirem:** existe um `pg_advisory_lock`, mas ele está no **harness de teste** (o
`TestMain` dos dois pacotes de integração, para dois pacotes não truncarem o mesmo banco em
paralelo) — não no caminho de produção. E `SERIALIZABLE` também não está no caminho: se
estivesse, uma disputa em outra carteira poderia abortar o lote.

**Número/teste que sustenta:** `TestT3CarteirasDistintasEmParaleloSemLockGlobal`.

### "Prove que carteiras diferentes andam em paralelo."

**Resposta (30–60s):** A diferença é **construída**, não cronometrada: o próprio teste prende
a linha da carteira 0 e dispara operações em cinco outras carteiras. Elas concluíram em
**14–34 ms** com a carteira 0 presa; a operação na carteira presa ficou bloqueada e só
concluiu **408 ms** depois da liberação. Se houvesse lock global — de tabela, advisory ou
`SERIALIZABLE` abortando o lote — as cinco não teriam concluído com a linha presa.

**Se insistirem:** os tempos vão para o log, mas **não** são condição de aprovação. Teste
de concorrência que depende de cronômetro fica intermitente, e teste intermitente não
serve de evidência: com operações de milissegundos, razões de tempo se invertem por ruído.

**Número/teste que sustenta:** `TestT3CarteirasDistintasEmParaleloSemLockGlobal`
(durações 14 ms a 34 ms; espera de 408 ms).

### "Duas apostas de 80.00 sobre 100.00, ao mesmo tempo: o que acontece?"

**Resposta (30–60s):** Uma `PROCESSED` com saldo devolvido 20.00, uma
`REJECTED/INSUFFICIENT_FUNDS` sem saldo na resposta, saldo gravado 20.00, versão 2 e **um**
débito. Cada aposta entrou por uma instância diferente (api-2 e api-3), disparadas por
barreira. Depois, 20 reenvios (10 voltas em cada) não mudam nada: a processada volta 200
replay e a rejeitada volta 422 com o mesmo `failureCode`, porque a decisão é terminal.

**Se insistirem:** as duas passaram pelo `FOR UPDATE` da **mesma** linha. A segunda
esperou, leu o saldo já atualizado e o domínio recusou. Sem o lock, as duas leriam 100.00 e
o desfecho dependeria de quem gravasse por último — é exatamente o cenário que o enunciado
usa como teste obrigatório.

**Número/teste que sustenta:** `TestT2DuasApostasDe80Sobre100EmInstanciasDiferentes` (e,
numa transação só, o mesmo cenário em `TestCenarioDoREADMEDuasApostasDe80Sobre100`).

### "Quanto custa a serialização por carteira?"

**Resposta (30–60s):** 100 operações concorrentes, 20 carteiras, três instâncias, cliente
local: em carteiras **distintas** deu p50 117 ms / p95 161 ms / **p99 166 ms** (**597**
ops/s); na **mesma** carteira, p50 280 ms / p95 552 ms / **p99 579 ms** (**172** ops/s) —
3,5× no p99. É o custo de serializar uma linha, e é a diferença que um lock global apagaria:
a primeira coluna passaria a parecer a segunda.

**Se insistirem:** não é número de produção — cliente na mesma máquina dos processos, sem
rede real, e o §14 não pede meta de RPS. O que o número mostra é a **topologia** da
contenção, não a velocidade do serviço. Para virar número de produção faltaria carga
distribuída, rede real e uma meta; teste de carga ficou declarado como diferencial não
entregue.

**Número/teste que sustenta:** `TestLatenciaSobConcorrenciaTresInstancias` (as duas
medições acima estão na mesma execução).

### "Como você garante que as requisições saíram mesmo ao mesmo tempo?"

**Resposta (30–60s):** Com barreira — canal fechado depois que todas as goroutines estão
prontas — e não `go` solto nem `sleep`. `go` solto deixa a primeira terminar antes de a
última começar (o teste vira sequencial) e `sleep` é tempo mágico que quebra em máquina
lenta. Com a barreira, os 50 disparos do T1 e as 2 apostas do T2 largam juntos, em
round-robin pelas três instâncias.

**Se insistirem:** simultaneidade absoluta não é demonstrável; o que se demonstra é que o
resultado não depende da ordem. E há uma prova mais forte que a barreira: a segunda
operação da mesma carteira **não consegue** terminar enquanto a linha está presa — não por
lentidão, mas porque o PostgreSQL a coloca em espera, e é o banco que mostra quem está
esperando.

**Número/teste que sustenta:** `TestT1MesmaAposta50VezesEntreTresInstancias` e
`TestT2DuasApostasDe80Sobre100EmInstanciasDiferentes` (barreira + round-robin).

---

## Bloco 5 — Erro e aprendizado

### "Qual foi o bug mais difícil de achar?"

**Resposta (30–60s):** O defeito 37, na retomada de referência. O `REFUND` continuava
`PENDING_REFERENCE` depois da retomada e **não havia erro no log** — o worker registrava
"pendente", que é um desfecho de sucesso. A guarda comparava o relógio com
`next_resolution_attempt_at`, que é exatamente o campo que o próprio claim acabou de
empurrar para o futuro como **lease**: o worker reivindicava e descartava, para sempre. A
correção foi comparar o estado e a `reference_resolution_attempt` lidos no claim, campos
que o claim não altera.

**Se insistirem:** "nada acontece" é a pior categoria de sintoma, porque não há stack e não
há erro para seguir. O segundo mais caro foi o defeito 31 (`return` que abortava o parsing
e devolvia requisição vazia com `err == nil`): o teste de integração dizia **que** algo
estava errado, e só um teste de unidade rodando o corpo **exato** entregue pelo broker
disse **onde**.

**Número/teste que sustenta:** `TestREFUNDAntesDaReferenciaResolveDepois` (que falhava),
`TestRetomadaIgnoraQuandoOutroWorkerJaAgiu` e `TestDecodificarCorpoRealDoBroker`.

### "Qual erro de teste mais te enganou?"

**Resposta (30–60s):** `TestOrdemDosEventosNaOutbox`: ele passa **3 de 3** vezes com o
código **antigo**, porque em empate de `occurred_at` o PostgreSQL devolve as linhas na
ordem física, que por acaso é a de inserção. Quem discrimina é
`TestOrdemDaOutboxNaoDependeDoRelogio`, que insere dois eventos com ordem de inserção e de
relógio **opostas** e reprova o código antigo. Somando as fases: na Fase 10 foram 4
correções de régua contra 2 defeitos reais de código, e na Fase 7 foram 3 asserções erradas
(envelope de erro na 422, `cursor=%%%` e "mesma identidade externa em dois provedores é
conflito").

**Se insistirem:** a régua errada tem sintoma de defeito, e o custo é depurar código
correto. A regra que passei a seguir: antes de "consertar" o código, provar que o teste
reprova o comportamento **errado** — se ele passa nos dois, ele não prova nada.

**Número/teste que sustenta:** `TestOrdemDosEventosNaOutbox` (passa 3 de 3 com o código
antigo, então não discrimina) e `TestOrdemDaOutboxNaoDependeDoRelogio` (reprova o código
antigo, então é o que sustenta a correção).

### "O que você faria diferente?"

**Resposta (30–60s):** No processo, três coisas: escrever primeiro o teste que
**discrimina** (o caso da ordem da outbox é o exemplo), subir o binário em toda fase em vez
de só quando um cenário exigia, e nunca registrar evidência por existência de arquivo. O
custo de cada uma é do tamanho de um passo de rotina — o teste antes do código, um
`docker run` por fase e um `list-queues` por fase. A lista completa está no bloco final,
com o custo de cada escolha.

**Se insistirem:** nenhuma dessas mudanças alteraria o desenho financeiro. As 4 correções
de régua da Fase 10 não tocaram código de produção, e as duas mudanças de produção daquela
fase foram correções de defeito revelado por cenário (`application_name` e endereço do
JWKS), não redesenho.

**Número/teste que sustenta:** 4 correções de régua × 2 defeitos de código na Fase 10; o
defeito 28 (filas que nunca eram criadas) é o caso-limite de "evidência por existência".

### "Como você descobriu o defeito 41 — o container não autenticava?"

**Resposta (30–60s):** Subindo o container de verdade para a prova de `SIGTERM`. O
`/health/ready` respondia UP com `oidc`, `postgres`, `sqs` e `workers`, e **todo token
válido** respondia 503 "não foi possível validar a autenticação". A causa: o Keycloak
anuncia o `jwks_uri` com o nome **público** do IdP (`http://localhost:8080/...`), e de
dentro do container `localhost` é o próprio container — o discovery (buscado pela URL
alcançável) passava, o readiness ficava UP e a busca das **chaves** falhava. O sintoma
aponta para o IdP caído; a causa estava no endereço das chaves.

**Se insistirem:** nenhum teste pegou porque os testes usam o binário no **host**; nenhum
subia o container autenticando. A correção foi `OIDC_JWKS_URL` opcional, que sobrescreve
**onde** as chaves são buscadas — o `iss` continua sendo conferido contra `OIDC_ISSUER`,
então a chave não afrouxa validação. A prova é de ponta a ponta: no container, `POST
/wallets` respondeu 201.

**Número/teste que sustenta:** `TestTokenValidoRecusadoQuandoOJWKSNaoEAlcancavel` (503, e
não 401) e `TestJWKSURLSobrescreveOAnunciadoPeloDiscovery` (com a chave, token aceito).

### "O que a observabilidade revelou que estava errado?"

**Resposta (30–60s):** Que métrica registrada não é métrica emitida. Duas mentiam: a de
conflito de concorrência existia e nunca saía de zero, porque `IncConflict` só era chamado
pelo teste do mecanismo (defeito 42); e o **mesmo nome** de métrica tinha dois significados
para o mesmo label — o HTTP emitia `kind="PROCESSED"`/`status="2xx_201"` e o SQS emitia
`kind="BET"`/`status="PROCESSED"` (defeito 43), então somar por `kind` num painel misturava
tipo de operação com desfecho. Hoje são 12 de 12 métricas emitidas e provadas, cada uma por
um teste que dispara o gatilho real e lê `/metrics`.

**Se insistirem:** o conserto do 42 foi no lugar certo: o caso de uso conta cada repetição
da transação através de uma porta de um método (`ports.ConflictCounter`), então ele continua
sem conhecer Prometheus. E o teste exige o valor **exato 2** — a terceira tentativa, que
sobe com o erro, não é conflito tratado.

**Número/teste que sustenta:** `TestMetricaConflitoDeTransacao` (exige 2),
`TestMetricaOperacoesPorHTTP` e `TestMetricaOperacoesPelaFila` (mesmo label, mesmo
significado) e `TestMetricasSemLabelsDeAltaCardinalidade`.

### "O que você aprendeu sobre evidência?"

**Resposta (30–60s):** Que evidência é a **saída de um comando que mostra o efeito**, não a
existência de um arquivo. O caso mais claro é o defeito 28: a evidência da fase dizia
"filas provisionadas" porque o script existia, e o log do LocalStack trazia `Exec format
error` nas **três** subidas — o script não tinha shebang nem bit de execução, e nada
quebrou porque nenhum teste usava SQS ainda. O irmão desse é o defeito 45: a documentação do
prazo de encerramento mentia, o comportamento estava certo e o teste que acreditava na
explicação não passava.

**Se insistirem:** a correção é de método, não de código: `list-queues` antes de qualquer
teste de fila, e teste que lê o **stdout do processo** para provar formato de log — foi o que
pegou o defeito 27 (dois formatos de log no mesmo processo, invisível em teste de objeto).

**Número/teste que sustenta:** `TestLogsDoProcessoReal` e `TestLogsDoProcessoEmModoLocal`
(lê o stdout do binário, nos dois formatos) e `TestScopeDoPrazoDeEncerramento` (defeito 45).

---

## Bloco 6 — As difíceis (limitação exposta)

### "Sua prova de queda entre commit e delete foi uma queda de verdade?"

**Resposta (30–60s):** Não no sentido literal, e eu não vou vender como se fosse. Foi o
**estado durável** de uma queda — transação commitada, inbox `PROCESSED`, mensagem ainda na
fila — reproduzido com o consumidor de produção e apenas o `Delete` embrulhado. Matar o
processo na janela de microssegundos não é reproduzível por sinal. O sinal **de verdade**
está provado no container: `exit=0` em 8,09 s, com `sqs_visibilidade_liberada`.

**Se insistirem:** o resultado é o mesmo nos dois caminhos, e é isso que importa para a
garantia: a mensagem volta, a inbox recusa (`attempts` 1→2), nenhum débito novo e a DLQ fica
vazia. O que a camada automatizada não prova é a **latência** da recuperação — que no
caminho sem prazo é o `VisibilityTimeout` inteiro (60 s).

**Número/teste que sustenta:** `TestT5QuedaEntreCommitEDeleteReentregaReconhecida`
(`attempts` 1→2 em 2,013 s) e a prova de `SIGTERM` no container (`docs/evidencias/FASE_10.md`
§6).

### "A ordem por carteira vale com três publishers?"

**Resposta (30–60s):** Com o lote padrão, sim: o claim lê por `sequence` e o
`MessageGroupId` é a carteira. Com `OUTBOX_BATCH_SIZE=1` — que eu usei justamente para
forçar disputa — dois publishers podem pegar eventos consecutivos da **mesma** carteira e
enviá-los em ordem trocada; o desenho **não** garante a ordem nesse arranjo. Na execução
registrada ela foi preservada em 3 de 3 grupos, mas o teste assere a **partição** (12
eventos → 12 mensagens, 0 duplicados, 0 pendentes, 4/4/4), não a ordem.

**Se insistirem:** consertar de verdade custaria serializar publishers por `aggregateId` —
o que reduz o paralelismo que o `SKIP LOCKED` comprou. A mitigação documentada é do lado do
consumidor: ordenar por `walletVersion` (e detectar lacuna por versão que pula), que é o que
o contrato de consumo da §11 exige.

**Número/teste que sustenta:** `TestT6TresPublishersReaisDisputamAMesmaOutbox` e
`TestDoisPublishersDisputamAMesmaOutbox` (8 eventos, 8 mensagens, nenhum `eventId`
repetido).

### "A latência é boa?"

**Resposta (30–60s):** Não sei dizer "boa" sem meta — e o §14 não pede RPS mínimo. O número
medido é p99 **166 ms** em carteiras distintas e **579 ms** na mesma, com cliente local, na
mesma máquina dos três processos. O que ele mostra é que a serialização é **por carteira**
(3,5×), não que o serviço é rápido em produção.

**Se insistirem:** para virar número de produção faltaria carga distribuída, rede real,
meta de SLO e o atraso da outbox sob carga — nada disso está medido. Prefiro dizer "não
sei" com o número na mão a chamar 166 ms de bom sem referência.

**Número/teste que sustenta:** `TestLatenciaSobConcorrenciaTresInstancias` (n = 100; 597
ops/s contra 172 ops/s).

### "Por que não há introspecção de token (RFC 7662)?"

**Resposta (30–60s):** Porque a validação é local — assinatura RS256 mais claims —, que é o
comportamento normal de OIDC/JWT; introspecção custaria uma ida ao IdP por requisição.
A consequência honesta: revogar um token antes do `exp` **não** é detectado, e a janela é o
tempo de vida do token (15 minutos no realm deste projeto — `exp − iat = 900 s`).

**Se insistirem:** o IdP hoje é necessário para buscar chave, com cache de 15 min, rebusca
por `kid` desconhecido e uso da chave antiga quando o cache vence com o IdP fora — ou seja,
já existe dependência do IdP, mas não por requisição. Introspecção trocaria essa dependência
por uma síncrona em todo acesso e um novo ponto de falha; a mitigação atual é TTL curto.

**Número/teste que sustenta:** `TestVerifyRejeitaTokensInvalidos` (11 casos, cada um
afirmando o sentinela) e `TestJWKSECacheado` (5 verificações = 1 busca de JWKS).

### "O que acontece se o reaper não conseguir concluir uma linha?"

**Resposta (30–60s):** Ele **repete indefinidamente**. Não existe contador durável de falhas
para `PENDING` sem referência, e o conflito de ledger é classificado como transitório — então
o desfecho `FAILED` só é dado quando a carteira não existe, e uma linha inconsistente nunca
chega a estado terminal. Isso não é hipótese: foi encontrado no binário real, com uma
`PENDING` de teste manual antigo cuja carteira já tinha o lançamento daquele
`transaction_id`. O reaper tentou, o índice único do ledger recusou (a invariante segurou) e
ele tenta de novo a cada `REFERENCE_REAPER_INTERVAL`.

**Se insistirem:** o custo é log de erro recorrente e uma linha que ninguém vê terminar. O
conserto é um `attempts` próprio do reaper e `FAILED` depois de N — está registrado como
trabalho não entregue no §16, e não como "funciona". Detalhe: hoje o `PENDING` é
inalcançável por caminho normal, porque o caso de uso grava `PENDING` e o estado definitivo no
mesmo commit (interpretação 6); o reaper é a segurança extra para o dia em que houver aceite
assíncrono.

**Número/teste que sustenta:** `TestReaperRetomaPendingAntigo` (prova o mecanismo por
inserção direta) e o achado do §6 de `docs/evidencias/FASE_9.md`.

### "Se eu matar com SIGKILL, perde dinheiro?"

**Resposta (30–60s):** Não. Medido: `docker stop` **sem** `--timeout` matou o processo em
~3,4 s (`exit=137`); o saldo ficou em 100000 com versão **1** — nada commitado, a transação
foi abortada — e a mensagem voltou pelo `VisibilityTimeout` de 60 s (`receiveCount=2`) e foi
processada **uma** vez: 97500, um débito, DLQ vazia. O que se perde é tempo de recuperação,
não dinheiro.

**Se insistirem:** o trabalho em voo é refeito do zero (não havia commit) e uma publicação de
outbox pode ser republicada com o mesmo `eventId`; o custo é o lease de 30 s vencer antes de
outro publisher assumir. Por isso o `docker-compose.yml` define `stop_grace_period: 60s` no
serviço `api`: sem prazo, o encerramento gracioso é abortado e o sistema demora mais para
voltar ao normal — mas continua correto.

**Número/teste que sustenta:** a medição de `exit=137` em ~3,4 s e saldo 100000/versão 1
(`docs/evidencias/FASE_10.md` §6.4) e, para o lado do publisher,
`TestQuedaEntreCommitEPublicacaoOutraInstanciaAssume`.

### "Por que o readiness não é métrica?"

**Resposta (30–60s):** Porque `/health/ready` é JSON, e transformar isso em métrica seria
duplicar contrato só para agradar o alerta. Para saber que "workers DOWN" eu uso o
`blackbox_exporter` (`probe_success` sobre o readiness), configurado em
`deploy/observability/prometheus.yml`. É menos direto, e eu digo isso no runbook: sem o
blackbox, o alerta `JunglegOutboxAtrasada` (`pending_age > 60` por 5 min) cobre o sintoma em
minutos.

**Se insistirem:** o readiness inclui PostgreSQL, IdP, SQS — a **fila específica**, via
`GetQueueAttributes`, e não "o SQS responde" — e `workers`; um readiness que respondesse UP
com a fila ausente mandaria tráfego para uma instância que não consome nada. Já o liveness
**não** verifica dependência: se verificasse, uma queda momentânea do PostgreSQL faria o
orquestrador reiniciar em loop um processo saudável.

**Número/teste que sustenta:** `TestReadinessReturns503WhenDependencyDown`,
`TestRunnerReadinessDownComLacoMorto` e `TestSQSReadinessReportaAFila`.

### "O que você não entregou?"

**Resposta (30–60s):** Cinco coisas que eu nomeio sem rodeio: tracing com OpenTelemetry,
consumidor de `wallet-events.fifo` (o evento é publicado e o contrato de consumo está
documentado, mas quem consome não existe), rate limiting por identidade, partidas dobradas e
um contador durável de falhas do reaper. Nenhuma é eliminatória pelo §14: tracing e partidas
dobradas são diferenciais opcionais, rate limiting não é exigido pelo enunciado, o consumidor
de eventos tem o contrato documentado na §11 (mas não a implementação) e o contador do reaper
está declarado como limitação no §16.

**Se insistirem:** o que eu **não** deixei pendente é o que o §14 chama de eliminatório:
autenticação efetiva, isolamento entre provedores, sem float no caminho monetário, sem saldo
negativo, sem movimentação duplicada, idempotência persistente, sem dependência de uma única
instância e sem publicação antes do commit. A lista completa, com o motivo de cada ausência,
está no bloco final.

**Número/teste que sustenta:** a §16 do `ARCHITECTURE.md` e a seção "O que ainda NÃO existe"
de `docs/CONFORMIDADE_README.md`.

---

## O que eu faria diferente

1. **Teste que discrimina antes da correção.** No caso da ordem da outbox, o teste "de
   ordem" passava 3 de 3 com o código antigo; quem reprovava era outro. Custo de fazer
   certo: provar, antes de tocar no código, que o teste falha com o comportamento errado.
   Custo de não fazer: depurar código correto — ou, pior, acreditar numa correção que não
   corrigiu.
2. **Subir o binário e o container em toda fase, não só quando um cenário exigia.** Os
   defeitos 27 (dois formatos de log), 33 (defaults que impediam a aplicação de subir com o
   consumidor ligado), 34 (métrica ausente no caminho SQS) e 41 (container não autenticava)
   só apareceram assim. Custo: um `docker run` por fase, com o compose entregando as quatro
   filas de verdade desde o início — não a partir da Fase 8.
3. **`application_name` por instância desde a primeira migration de conexão.** Era um
   parâmetro no DSN e virou o **instrumento de medida** da prova multi-instância (defeito
   40). Sem ele, "cada um com suas conexões" seria afirmação, e não daria para dizer quem
   segurava o lock de uma carteira.
4. **Nascer cada métrica com o teste que lê `/metrics`.** As 12 métricas ficaram "emitidas"
   por três fases, e duas mentiam (defeitos 42 e 43). Custo de fazer desde a primeira: um
   teste de integração por métrica — hoje são 10 funções — e a disciplina de disparar o
   gatilho real em vez de testar o helper.
5. **Medir a infraestrutura antes de confiar nela.** A evidência da Fase 4 dizia "filas
   provisionadas" porque o script existia, e o LocalStack registrava `Exec format error` nas
   três subidas (defeito 28). Custo: um `list-queues` por fase.
6. **Persistir o `correlationId` desde a Fase 6.** Ele morria no fim da requisição e o
   caminho SQS nunca o preenchia; só a migration 000005 resolveu (defeito 39). Custo: uma
   coluna e uma migration — barato perto de eventos de retomada sem rastro por três fases.
7. **Não depender de `ChangeMessageVisibility(0)` nos testes.** O LocalStack aceita a
   chamada e **não** antecipa a entrega (medido: continua invisível até o prazo original).
   Custo de evitá-lo: encurtar o `VisibilityTimeout` da fila de teste para 2 s. Custo de
   descobrir no meio: um teste de 67 s que falha por um motivo que não é dele.

## O que eu não entreguei e por quê

Ligado à §16 do `ARCHITECTURE.md` e à seção final de `docs/CONFORMIDADE_README.md`:

- **Tracing com OpenTelemetry.** Diferencial opcional do §14. A correlação é feita por
  `correlationId` em log estruturado (`X-Correlation-Id` no HTTP, `MessageId` no SQS, coluna
  `correlation_id` na retomada); um trace distribuído daria a mesma resposta com mais
  infraestrutura.
- **Consumidor de `wallet-events.fifo`.** O evento é **publicado** e o contrato de consumo
  está documentado na §11 (dedup durável por `eventId`, ordem por `walletVersion`, DLQ sem
  bloquear o grupo), mas quem consome não existe. O §11 pede publicação, e é o que há.
- **Rate limiting por identidade.** Não é exigido pelo enunciado. Com o lock por carteira, o
  custo de uma rajada é contenção de linha — não corrupção de saldo (§16).
- **Partidas dobradas.** Opcional no §6.4. O ledger tem um lançamento por movimentação, com
  direção, valor e os dois saldos; um modelo de duas contas por lançamento mudaria o schema e
  a reconciliação sem mudar nenhuma garantia que o enunciado cobra.
- **Contador durável de falhas do reaper.** Uma linha `PENDING` inconsistente é repetida
  indefinidamente em vez de virar `FAILED` (§16). Encontrado no binário real e registrado.
- **Introspecção de token (RFC 7662).** A validação é local; revogação antes do `exp` não é
  detectada (§16).
- **Encerramento gracioso provado no Windows.** `Stop-Process` é terminação dura; a prova de
  `SIGTERM` é do container Linux (§16).
- **Envio em lote (`SendMessageBatch`) e resolução da URL da fila por `GetQueueUrl`.** Cada
  evento é um `SendMessage` (é o que mantém o `MarkPublished` por evento e o lease preciso),
  e a URL da fila é derivada do `SQS_ENDPOINT` — sem ele, o que sobra é o nome (§16 e §9 de
  `docs/evidencias/FASE_9.md`).
- **Dashboard exercitado em CI.** A definição está versionada e validada (`promtool` e
  inspeção do JSON), mas não há Grafana no compose por decisão registrada: o compose é o
  artefato que precisa subir limpo, e o painel sobe com dois `docker run` documentados (§16).
- **README da solução e documento de sistema.** A decisão de **onde** eles vivem está
  registrada como pendência em `docs/CONFORMIDADE_README.md` (⚠️ 3); o enunciado ocupa o
  nome `README.md` e não pode ser editado.
