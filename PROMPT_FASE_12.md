# PROMPT DE ABERTURA — FASE 12 (documentação, os dois documentos finais e entrega)

> Cole este arquivo como **primeira mensagem** de uma nova conversa no workspace
> `E:\jungleg`. Ele é autossuficiente. O handoff completo continua sendo o
> `PROXIMO_PASSO.md`; o enunciado continua sendo o `README.md` da raiz.

---

## 0) REGRA ABSOLUTA — LEIA ISTO ANTES DE QUALQUER COISA

O **`README.md` da raiz é a cabeça de tudo**: é o enunciado do desafio, é a fonte de
verdade e **não pode ser editado** (nem uma vírgula). Sempre que houver dúvida — de
regra de negócio, de contrato, de status, de escopo, de nomenclatura — a resposta está
no `README.md`, e ele vale **sobre** qualquer plano, handoff, documento interno ou
opinião sua. Se `PLANO.md`, `ARCHITECTURE.md`, `docs/CONFORMIDADE_README.md` ou este
prompt discordarem do `README.md`, **vale o `README.md`**, e a divergência deve ser
registrada.

**Esta fase é diferente das outras: o "código" dela são DOCUMENTOS.** A régua de
validação muda de "os testes passam" para "cada comando documentado foi executado,
cada contrato documentado existe no código, e nenhuma frase contradiz o `README.md`".
Isso está detalhado no §6 (critérios de aceite): trate documentação com o mesmo rigor
que código.

**Antes de escrever uma linha, leia nesta ordem:**

1. `README.md` (raiz) — **§15 (entrega: é O capítulo desta fase)**, **§9 (contratos
   HTTP e exemplos)**, **§2 (autenticação e identidades de teste)**, **§14 (critérios,
   eliminatórios e os 5 pontos de "Documentação")**, **§12 (observabilidade)**,
   **§16–§17 (não existem: o enunciado termina no §15)**;
2. `PROXIMO_PASSO.md` — handoff: estado, método, **§3.5 e §3.6 (o compromisso dos dois
   documentos)**, pendências e checklist da Fase 12;
3. `ARCHITECTURE.md` — o documento **do avaliador** (denso, em terceira pessoa). É de
   onde você vai **traduzir** para os dois documentos finais, e **não** copiar;
4. `docs/CONFORMIDADE_README.md` — o que está atendido, as **23 interpretações
   registradas** e a lista do que ainda não existe;
5. `docs/evidencias/FASE_6.md` a `FASE_11.md` — **as seções finais "MATÉRIA-PRIMA PARA
   OS DOIS DOCUMENTOS"** são o insumo direto desta fase;
6. `PLANO.md` §3 — o checklist da FASE 12.

---

## 0b) OS DOIS DOCUMENTOS FINAIS — A RAZÃO DE SER DESTA FASE

> **Compromisso registrado** em `PROXIMO_PASSO.md` §3.5 e §3.6 e no `PLANO.md` §3.
> Exigência do usuário datada de 2026-10-16: os dois documentos são **entrega
> obrigatória**, explicam **do básico ao avançado**, **não presumem vocabulário**, e
> esta fase **não termina** sem eles.

### Por que são dois, e não um

Um único documento falharia nos dois usos. Quem vai **manter** o código precisa de um
texto **didático, exaustivo e em ordem de leitura**. Quem vai **defender o projeto numa
entrevista** precisa de um texto **afiado, em ordem de pergunta, com admissão de
limitação**. Misturar os dois produz um documento que ensina devagar e não treina
rápido. São públicos diferentes, tempos diferentes e formatos diferentes.

### `docs/SISTEMA_EXPLICADO.md` — para quem vai LER E MANTER o código

**Promessa:** depois de ler, a pessoa sabe **o que** o sistema faz, **por que** é assim
e **o que quebra** se ela mexer.

**Regra de ouro:** na primeira aparição de um termo, o termo é **explicado**. Nada de
"como todos sabem, o outbox garante...". O leitor nunca viu este projeto.

**Ordem obrigatória do documento** (do básico ao avançado):

1. **O problema** — o que é processar apostas de provedores com dinheiro de verdade;
   at-least-once, concorrência e queda no meio como fatos do ambiente.
2. **Vocabulário, do zero** — dinheiro em **unidades mínimas** (por que `int64` e não
   `float`; o que é escala fixa; o que estoura), **carteira** (o agregado e a versão),
   **lançamento de ledger** (por que o histórico é append-only e por que ele é separado
   da carteira), **transação e lock** (o que o banco garante e o que ele NÃO garante
   sozinho), **idempotência** (chave, hash, replay, conflito), **outbox** e **inbox**
   (por que existem, um para não perder evento, o outro para não aplicar duas vezes),
   **pending reference** (por que a operação pode esperar), **worker**, **lease**.
3. **O fluxo completo de uma requisição** — HTTP → autenticação → caso de uso →
   transação (lock, saldo, ledger, transação, outbox no mesmo commit) → resposta →
   publicação pelo worker → fila. O mesmo fluxo pela porta SQS, com a inbox.
4. **Cada componente, e por que ele existe** — domínio (`money`, `wallet`, `ledger`,
   `wagering`), `ports`, `app/wagering`, `infra/postgres`, `infra/sqs`, `infra/auth`,
   `consumer`, `app/inbox`, `worker`, `httpapi`, `app` (Fx), `observability`.
5. **Onde cada garantia é imposta** — uma tabela: garantia → camada (domínio / schema /
   transação / índice único / processo) → arquivo/teste que prova. É o capítulo que
   responde "quem me protege do quê".
6. **As regras de negócio e o porquê** — por que `LOSS` não movimenta, por que
   `ROLLBACK` de `REFUND` debita, por que o replay devolve o saldo **original**, por que
   carteira e ledger são agregados diferentes, por que `REFUND` antes da `BET` fica
   pendente em vez de falhar.
7. **As decisões, com motivo e consequência** — cada linha das tabelas
   `DECISÃO | MOTIVO | O QUE QUEBRARIA SEM ELA` das Fases 6–11 vira **prosa curta**.
8. **Os problemas resolvidos** — os **41 defeitos** com sintoma → causa de fundo →
   correção, em ordem de fase. Não é vaidade: é o capítulo que mostra como o sistema
   ficou confiável (e o que olhar quando algo parecer "impossível").
9. **Os limites conhecidos e o que ficou de fora** — do `ARCHITECTURE.md` §16, em
   linguagem de quem vai manter ("isto é frágil porque...", "isto não existe porque...").

**O que este documento NÃO é:** não é o `ARCHITECTURE.md` reescrito. Aquele é para o
**avaliador**, denso e em terceira pessoa; este é para **ensinar**, em segunda pessoa,
com exemplo.

### `docs/ENTREVISTA.md` — para o USUÁRIO TREINAR

**Promessa:** o usuário abre, lê a pergunta, responde em voz alta em 30–60 segundos, e a
resposta dele é boa. As perguntas difíceis estão lá, com resposta **honesta**.

**Regra de ouro:** nenhuma resposta termina em "porque é boa prática". Toda resposta tem
**motivo**, **consequência concreta** e, quando existe, **número**.

**Blocos de perguntas (todos obrigatórios):**

1. **Arquitetura** — "por que `READ COMMITTED` + `FOR UPDATE` e não `SERIALIZABLE`?",
   "por que outbox e não publicar direto no SQS?", "por que o ledger é separado da
   carteira?", "por que o domínio não importa `encoding/json`?", "por que UUID v7?",
   "por que o mesmo caso de uso para HTTP e SQS?".
2. **Decisão** — "por que `int64` em unidades mínimas?", "por que `FOR UPDATE` vem antes
   de ler o saldo?", "por que a unidade de retry é a transação e não a chamada?", "por
   que a quarentena da outbox em vez de descarte?", "por que `MessageDeduplicationId`
   por tentativa?".
3. **Garantia** — "como você impede débito duplicado?", "onde está a proteção contra
   lost update?", "o que acontece se o processo morrer entre o commit e o delete?",
   "e se o broker entregar a mesma mensagem 50 vezes?", "como você sabe que o saldo
   armazenado está certo?".
4. **Concorrência (a nota de 20 pontos)** — "como você provou que três processos não se
   atrapalham?", "existe lock global?", "carteiras diferentes andam em paralelo? prove",
   "o que acontece com duas apostas de 80 sobre 100?".
5. **Erro e aprendizado** — "qual foi o bug mais difícil?", "qual foi o erro de teste que
   mais te enganou?", "o que você faria diferente?", "o que você **não** entregou?".
6. **As difíceis (com limitação exposta)** — "sua prova de queda foi uma queda de
   verdade?", "a ordem por carteira vale com três publishers?", "sua latência é boa?",
   "por que não há introspecção de token?", "o que acontece se o reaper não conseguir
   concluir uma linha?", "se eu matar o processo com SIGKILL, perde-se dinheiro?".

**Formato de cada pergunta:**

```markdown
### "Por que outbox, e não publicar direto no SQS?"

**Resposta (30–60s):** "Porque o SQS não participa do commit do banco. Se eu publico
dentro da transação, ou o SQS aceita e o commit falha (evento de uma mudança que não
aconteceu), ou o commit passa e o SQS falha (mudança sem evento — e dinheiro não pode
acontecer sem aviso). Com a outbox, o evento entra no MESMO commit e um worker publica
depois. A pior falha vira republicação, e o consumidor deduplica pelo `eventId`."

**Se insistirem:** "o preço é latência: o evento sai no próximo ciclo do worker, não na
mesma requisição. E é entrega 'pelo menos uma vez', então o consumidor precisa ser
idempotente."

**Número/teste que sustenta:** `TestQuedaEntreCommitEPublicacaoOutraInstanciaAssume`,
`TestT6TresPublishersReaisDisputamAMesmaOutbox` (12 eventos → 12 mensagens, 0 duplicados).
```

### A distinção, em uma tabela (vale para escrever os dois)

| Dimensão | `SISTEMA_EXPLICADO.md` | `ENTREVISTA.md` |
| --- | --- | --- |
| Para quem | quem vai manter o código | quem vai defender o projeto |
| Pergunta que responde | "como funciona e por quê?" | "como eu respondo isso em 60 s?" |
| Ordem | do zero ao avançado, por capítulo | por pergunta, do simples ao hostil |
| Vocabulário | **explicado na primeira aparição** | pressupõe aprendido |
| Unidade | componente, fluxo, tabela | pergunta → resposta → número |
| A tabela `DECISÃO \| MOTIVO \| O QUE QUEBRARIA SEM ELA` vira | **prosa** que ensina | **fala** que defende |
| Limitação | capítulo "limites conhecidos" | pergunta difícil com resposta honesta |
| Erro do passado | lista de sintoma→causa→correção | "qual foi o bug mais difícil?" |
| Extensão | longa (é um manual) | média (é um treino) |
| Critério de pronto | o leitor explica onde cada garantia é imposta | nenhuma resposta sem motivo e consequência |
| O que NÃO é | resumo do `ARCHITECTURE.md` | lista de jargão decorado |

### O triângulo `DECISÃO | MOTIVO | O QUE QUEBRARIA SEM ELA` — o rascunho dos dois

É o formato de explicação usado em **toda** esta caminhada desde a Fase 6. Exemplo real
(Fase 10):

| DECISÃO | MOTIVO | O QUE QUEBRARIA SEM ELA |
| --- | --- | --- |
| `SELECT ... FOR UPDATE` na linha da carteira, **antes** de ler o saldo | o conflito real é "duas operações na mesma carteira"; o lock de linha serializa só isso | as duas apostas de 80.00 leriam 100.00 ao mesmo tempo e uma escrita sumiria (lost update) |

- **No `SISTEMA_EXPLICADO.md`:** dois parágrafos — um explica, sem presumir vocabulário,
  o que é transação e lock de linha ("o banco faz a segunda esperar"); o outro conta a
  consequência e **onde** ela é imposta (no banco), com o teste que prova.
- **No `ENTREVISTA.md`:** pergunta "por que não `SERIALIZABLE`?" com a resposta de 40
  segundos terminando nos números (1 `PROCESSED`, 1 `REJECTED`, saldo 20.00, **um**
  débito, p99 166 ms contra 579 ms).
- **A pergunta difícil que o mesmo dado permite responder:** "e se o processo morrer
  entre o commit e a resposta?" — com o T5 e o SIGTERM do container, **admitindo** que a
  janela foi reproduzida por estado durável.

### O que NENHUM dos dois pode fazer

- Contradizer o `README.md` da raiz. **Nunca.**
- Omitir as **interpretações adotadas** (as 23 de `docs/CONFORMIDADE_README.md`): cada
  uma aparece nos dois, com o **motivo** e a **consequência**.
- Esconder fragilidade. Documento que esconde limitação não ajuda em entrevista: o
  entrevistador encontra.
- Inventar número. Todo número vem de teste, de evidência ou de medição registrada.

---

## 1) MÉTODO DE TRABALHO (o mesmo das Fases 6 a 11 — não mude)

a) **A IA escreve.** O usuário revisa e contesta. Não peça para ele digitar.

b) **UM BLOCO COERENTE POR VEZ.** Cada bloco: escrever → validar → commit. A Fase 12
tem quatro blocos e cada um é um commit.

c) **EXPLIQUE O QUE ESCREVEU**, no formato `DECISÃO | MOTIVO | O QUE QUEBRARIA SEM ELA`
— inclusive quando o que foi escrito **é** um documento. Explicar a estrutura de um
documento ("por que este capítulo vem antes daquele") é parte da entrega.

d) **VALIDE DE VERDADE.** Nesta fase a validação é dupla:
   - **do código** (nada mudou, mas confirme): `go build ./...`, `go vet ./...`,
     `go vet -tags=integration ./...`, `gofmt -l`, `go test -count=1 -race ./...`,
     `go test -count=1 -race -tags=integration -timeout 600s ./...`;
   - **dos documentos** (§6): cada comando do README da solução **executado**, cada
     variável de ambiente confrontada com `internal/config`, cada rota confrontada com o
     roteador, cada link resolvido, cada afirmação com ponteiro para teste/evidência.

e) **ACEITE CONTESTAÇÃO.** Se o usuário apontar erro e estiver certo, ADMITA E CORRIJA.

f) **RÉGUA ANTES DO CÓDIGO.** Quando algo parecer errado, confira a expectativa antes de
acusar o sistema. Nesta caminhada já foram corrigidas asserções erradas nas Fases 6, 7,
8, 9 e 10; e, na fase de documentos, a régua é "o `README.md` diz isso mesmo?".

g) **REGISTRE TODO ACHADO** na mensagem de commit e nas evidências. Os defeitos somam
**41** (o próximo é o **42**). Se a fase de documentação revelar que um comando não
funciona, que uma variável não existe ou que um contrato está documentado errado, **isso
é defeito** — corrija com a prova e numere.

h) **NÃO QUEBRE O QUE JÁ FUNCIONA.** Alteração em código de produção exige rodar a suíte
inteira. Em princípio, nesta fase o código **não muda**; se mudar, é porque a
documentação revelou um defeito — e aí a regra das fases anteriores vale.

i) **NÃO INVENTE ESCOPO.** Os dois documentos e a entrega do §15 já são muito trabalho.
Nada de feature nova, nada de tracing/OTel (diferencial opcional não pedido), nada de
refatoração cosmética.

---

## 2) ESTADO ATUAL (fim da Fase 11 — tudo pronto e validado)

| Item | Estado |
| --- | --- |
| Fases 0–10 | ✅ concluídas, validadas, commitadas e documentadas |
| Fase 11 (observabilidade e polimento) | ✅ concluída — 12/12 métricas com teste, log do processo provado, dashboard/alerta, `go mod tidy`, composição Fx |
| Suíte | `go test -race` verde em 17 pacotes de unidade e 18 com a tag; integração com PostgreSQL, Keycloak e LocalStack **reais** |
| Domínio | 574 casos, `wagering` 100% de cobertura |
| Esquema | **versão 5**; `bin/migrate.exe` com migrations embutidas (`go:embed`) |
| Defeitos corrigidos | **41** (a lista está em `ARCHITECTURE.md` §15) |
| Interpretações adotadas | **23** (`docs/CONFORMIDADE_README.md`) |
| Evidências | `docs/evidencias/FASE_0.md` … `FASE_11.md` |
| Documentos finais | ⬜ **`docs/SISTEMA_EXPLICADO.md` e `docs/ENTREVISTA.md` — não existem ainda** |
| README da solução | ⬜ não existe (ver a ⚠️ 3 abaixo) |

### ⚠️ A decisão que esta fase precisa tomar com o usuário (⚠️ 3 do handoff)

O `README.md` §15 exige que **"o `README.md` da solução"** explique pré-requisitos,
variáveis de ambiente, inicialização das filas, aplicação e reversão das migrations,
execução da aplicação, exemplos de chamadas, comandos de teste, provisionamento do IdP e
instruções para os fluxos autenticados.

**O problema:** `README.md` na raiz **é o enunciado do desafio** e **não pode ser
editado**. As opções são:

- **(a)** um `README_SOLUCAO.md` na raiz;
- **(b)** um `docs/README_SOLUCAO.md` referenciado no topo do `PROXIMO_PASSO.md` e do
  `ARCHITECTURE.md`;
- **(c)** uma seção nova no topo do `README.md`, preservando o texto do enunciado.

**Pergunte ao usuário antes de escrever**, apresente o impacto de cada opção (a opção
(c) mexe no arquivo do enunciado — é a mais arriscada) e registre a decisão nas
evidências. **Não decida sozinho.**

---

## 3) A TAREFA — FASE 12 (README §15), em QUATRO BLOCOS

### BLOCO 12a — o "README da solução" (a porta de entrada do repositório)

- decidir a ⚠️ 3 **com o usuário** e registrar;
- escrever o documento com, no mínimo:
  1. **o que é o sistema** (3 parágrafos) e o que ele garante;
  2. **pré-requisitos** (Go 1.26, Docker, e o que mais for necessário);
  3. **variáveis de ambiente** — tabela com nome, significado, default e **obrigatória
     ou não**, conferida contra `internal/config/config.go` (nenhuma inventada, nenhuma
     faltando) e apontando o `.env.example`;
  4. **subir o ambiente**: `docker compose up -d postgres keycloak localstack` (e
     `--build` quando for o caso), incluindo **como as filas são criadas** (o
     `deploy/localstack/init-queues.sh` e a checagem `list-queues`);
  5. **migrations**: aplicar (`bin/migrate.exe up`) e **reverter** (`down`), com a
     armadilha do binário com migrations embutidas;
  6. **execução da aplicação** (host e container), com as flags (`-health-check`) e o
     que ler no log;
  7. **fluxo autenticado**: como o realm, os clients e as roles são provisionados
     (`deploy/keycloak/realm.json`), como obter token por `client_credentials`, e
     **exemplos `curl` reais** — cada um executado de verdade, com a saída colada;
  8. **comandos de teste**: unidade, integração (com as variáveis), build tags,
     `-race`, e **como preparar as dependências** (containers, filas, banco de teste);
  9. **múltiplas instâncias e simulações de falha**: como rodar as três instâncias do
     teste, como reproduzir a queda entre commit e delete e o SIGTERM no container
     (apontando `docs/evidencias/FASE_10.md` §6);
  10. **onde está o resto da documentação** (ARCHITECTURE, os dois documentos finais,
      `docs/CONFORMIDADE_README.md`, as evidências).
- **cada comando do documento é executado** na fase, do zero, e a saída vai para a
  evidência. Comando que não roda não entra.

### BLOCO 12b — `docs/SISTEMA_EXPLICADO.md`

- seguir a **ordem obrigatória** do §0b (problema → vocabulário → fluxo → componentes →
  onde cada garantia é imposta → regras de negócio → decisões → problemas resolvidos →
  limites);
- **fonte da matéria-prima:** as seções finais de `docs/evidencias/FASE_6.md` a
  `FASE_11.md`, as tabelas de decisão de cada fase, o `ARCHITECTURE.md` §1–§17 (traduzir,
  não copiar) e os **41 defeitos** do §15;
- **exemplo obrigatório:** um diagrama em texto do fluxo de uma requisição HTTP e outro
  do fluxo por SQS (ASCII mesmo — o repositório não tem gerador de imagem);
- **cada afirmação técnica tem um ponteiro**: arquivo:linha, nome de teste ou seção de
  evidência. Sem ponteiro, é opinião;
- **o capítulo "onde cada garantia é imposta"** é obrigatório e é o mais importante:
  garantia → camada → arquivo/teste. Ex.: "não-negatividade → domínio (`wallet.Debit`) +
  `CHECK` no schema + `INSERT` condicional → `TestBETSemSaldoRejeitaSemTocarNoSaldo` e
  `verify_constraints.sql`".

### BLOCO 12c — `docs/ENTREVISTA.md`

- os **seis blocos** de perguntas do §0b, com o formato pergunta → resposta (30–60 s) →
  "se insistirem" → **número/teste que sustenta**;
- **as perguntas difíceis são obrigatórias** e a resposta é **honesta**: a queda
  reproduzida em vez de provocada, a ordem por carteira dependente do lote, a latência de
  cliente local, o reaper que repete uma linha, a ausência de introspecção de token, o
  `stop_grace_period`, o `LOSS` com referência informativa;
- **nada de resposta decorada**: cada uma tem que ser sustentável por um teste que existe
  no repositório (e o nome do teste vai escrito);
- um bloco final **"o que eu faria diferente"** e outro **"o que não entreguei e por
  quê"** — é o que separa candidato honesto de candidato que decorou.

### BLOCO 12d — entrega final e conformidade

- **`docker compose up --build` do zero**, a partir de um checkout limpo (clone num
  diretório temporário, ou `docker compose down -v` + subir): o §15 pede que "outra
  pessoa reproduza a solução a partir de um checkout limpo". **Teste de verdade**, com a
  saída na evidência (é o teste que mais falha em entrega de desafio);
- `down`/`up` das migrations e o caminho de reversão, executados;
- **`.env.example` completo e conferido** contra `internal/config` (existe chave, não
  existe segredo real, está documentado);
- `ARCHITECTURE.md` fechado: decisões, interpretações, limitações, trabalho não
  concluído, e a §17 com os números finais;
- **conferência de conformidade §1 a §15** do `README.md`, uma linha por requisito, com
  o arquivo/teste/evidência que o comprova — este é o produto final da fase, e vai para
  `docs/evidencias/FASE_12.md`;
- índice de evidências (`docs/evidencias/README.md` ou seção no README da solução)
  apontando as 13 fases;
- `PROXIMO_PASSO.md` e `PLANO.md` fechados.

---

## 4) O QUE **NÃO** FAZER NESTA FASE

- NÃO editar o `README.md` da raiz (jamais) — nem para "melhorar" a formatação.
- NÃO copiar o `ARCHITECTURE.md` para dentro dos dois documentos. Aquele é do avaliador;
  estes são para **ensinar** e **treinar**.
- NÃO escrever documento que só diz "o quê". Sem motivo e sem "o que quebraria", o
  compromisso não está cumprido.
- NÃO esconder limitação e NÃO inventar número.
- NÃO documentar comando que não foi executado, variável que não existe no código, rota
  que não está no roteador, ou comportamento que o teste não prova.
- NÃO adicionar feature, refatorar ou "aproveitar para arrumar" — escopo é documentação
  e entrega.
- NÃO usar `Set-Content` para arquivos com acento (PowerShell 5.1 grava ANSI e corrompe;
  já aconteceu nas Fases 9 e 10). Use a ferramenta de edição ou
  `[System.IO.File]::WriteAllText` com `UTF8Encoding($false)`.
- NÃO rodar `git commit` com mensagem longa por heredoc: arquivo em `.git/` +
  `git commit -F arquivo`. Texto em ASCII.
- NÃO começar outra fase. A 12 é a última.

---

## 5) AMBIENTE (detalhes que causam erro se ignorados)

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'
$env:GOCACHE='E:\jungleg\.gocache\build'
$env:GOPATH='E:\jungleg\.gocache\gopath'

$env:TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
$env:OIDC_ISSUER='http://localhost:8080/realms/jungleg'
$env:OIDC_DISCOVERY_URL='http://localhost:8080/realms/jungleg'
$env:OIDC_AUDIENCE='jungleg-api'
$env:SQS_ENDPOINT='http://localhost:4566'
```

- **Containers:** `jungleg-postgres`, `jungleg-keycloak`, `jungleg-localstack`
  (`healthy`). Bancos `jungleg` (aplicação) e **`jungle_test`** (testes — TRUNCAM).
  Esquema versão 5.
- **Filas:** `docker exec jungleg-localstack awslocal sqs list-queues` → quatro filas.
  Vazio = init falhou.
- **PowerShell 5.1:** sem `&&`, sem ternário; `make` não existe; `package main` não pode
  ser importado.
- **`bin/migrate.exe`** carrega as migrations **embutidas**: se a fase não cria
  migration, não precisa recompilar — mas o **documento** precisa explicar isso (é a
  armadilha que já custou tempo três vezes).
- **Windows × Linux:** `os.Interrupt`/`Kill` são terminação dura; a prova de SIGTERM é no
  container (`OIDC_JWKS_URL` já está no compose — ver defeito 41).
- **Imagem da API:** `docker compose build api` (multi-stage, `TARGET=api`); a rede é
  `jungleg_default`; o Healthcheck do compose usa `-health-check`.
- **Docker Desktop não inicia sozinho**; o named pipe pode exigir permissão — se o sandbox
  bloquear, use a permissão mais estreita possível com justificativa em português.

---

## 6) CRITÉRIOS DE ACEITE (não considere a fase pronta sem TODOS)

**Do código (nada pode ter regredido):**
- [ ] `go build ./...`, `go vet ./...`, `go vet -tags=integration ./...`, `gofmt -l`
      limpos, com saída real
- [ ] `go test -count=1 -race ./...` e `go test -count=1 -race -tags=integration
      -timeout 600s ./...` verdes (Fases 1–11)

**Dos documentos (a régua desta fase):**
- [ ] **todo comando do README da solução executado** nesta fase, com a saída colada na
      evidência; nenhum comando "deve funcionar"
- [ ] **toda variável de ambiente** documentada existe em `internal/config/config.go` (e
      vice-versa: nenhuma variável real ficou de fora do `.env.example`)
- [ ] **toda rota** documentada existe no roteador (`internal/httpapi`), com o status
      correto; toda tabela do §9 do enunciado aparece no documento
- [ ] **todo link** interno entre documentos resolve (caminho relativo correto)
- [ ] **nenhuma frase contradiz o `README.md`** — e as **23 interpretações** aparecem nos
      dois documentos, com motivo e consequência
- [ ] `docs/SISTEMA_EXPLICADO.md` cobre os **9 capítulos** do §0b, com vocabulário
      explicado na primeira aparição e o capítulo "onde cada garantia é imposta"
- [ ] `docs/ENTREVISTA.md` cobre os **seis blocos** de perguntas, com as difíceis e com
      número/teste em cada resposta
- [ ] **`docker compose up --build` a partir de um checkout limpo funciona** (teste real,
      com saída na evidência)
- [ ] migrations **aplicam e revertem** pelo caminho documentado, executado
- [ ] conferência de conformidade **§1 a §15**, uma linha por requisito, com evidência
- [ ] `git diff -- README.md` **vazio**

---

## 7) ENTREGÁVEIS DE DOCUMENTAÇÃO

1. **O "README da solução"** (conforme a ⚠️ 3 decidida com o usuário);
2. **`docs/SISTEMA_EXPLICADO.md`** — novo, do básico ao avançado, para quem mantém;
3. **`docs/ENTREVISTA.md`** — novo, perguntas e respostas, para treinar;
4. `docs/evidencias/FASE_12.md` — novo: ambiente, saída real dos comandos executados,
   cada comando do README da solução com a sua saída, o teste do checkout limpo, os
   achados (numerados a partir de 42), as decisões (inclusive a ⚠️ 3) e a conferência
   §1–§15;
5. `ARCHITECTURE.md` — fechado (§15/§16/§17 e o que a fase acrescentar);
6. `docs/CONFORMIDADE_README.md` — §15 fechado e os eliminatórios revisados **uma última
   vez**;
7. `PROXIMO_PASSO.md` — estado final, histórico de commits, e um parágrafo dizendo
   **como usar os dois documentos** (ler na ordem X, treinar com Y);
8. `PLANO.md` — FASE 12 fechada, com os defeitos numerados e o "próximos 3 passos"
   substituído por "entrega concluída";
9. `.env.example` — completo e conferido.

---

## 8) PRIMEIRA RESPOSTA ESPERADA

Não comece a escrever. Na primeira resposta:

a) confirme que leu o `README.md` §15 (e §9, §2, §14) e diga, com suas palavras, **o que
a entrega exige** — e o que ela **não** exige (nada de feature nova);
b) confirme o entendimento da **régua desta fase**: documento também se valida (comando
executado, variável conferida no código, rota conferida no roteador, link resolvido,
nenhuma contradição com o `README.md`);
c) confirme os quatro blocos (12a README da solução, 12b SISTEMA_EXPLICADO, 12c
ENTREVISTA, 12d entrega e conformidade) e diga se enxerga risco — em especial: **onde
vive o README da solução** (a ⚠️ 3, que precisa da decisão do usuário), **como fará o
teste de checkout limpo** sem destruir o ambiente atual, e **quanto tempo** cada
documento vai consumir;
d) diga exatamente qual será o primeiro passo concreto do bloco 12a;
e) diga, em três linhas, a **distinção detalhada** entre `docs/SISTEMA_EXPLICADO.md` e
`docs/ENTREVISTA.md` — quem lê cada um, o que cada um ensina, e por que o formato
`DECISÃO | MOTIVO | O QUE QUEBRARIA SEM ELA` é o rascunho dos dois.
