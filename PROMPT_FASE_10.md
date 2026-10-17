# PROMPT DE ABERTURA — FASE 10 (concorrência e recuperação com 3 instâncias: T1–T8)

> Copie o bloco abaixo **inteiro** e envie como primeira mensagem da nova conversa.
> Ele é autossuficiente: traz o estado, o método, o escopo exato e os critérios de
> aceite. O `PROXIMO_PASSO.md` continua sendo o handoff completo — este prompt é a
> porta de entrada.

```text
Você vai continuar o projeto E:\jungleg (desafio técnico de back-end Go júnior:
Uber Fx + PostgreSQL + Keycloak/OIDC + AWS SQS via LocalStack). As Fases 0 a 9 já
estão CONCLUÍDAS, commitadas, validadas e documentadas. Sua tarefa é a FASE 10.

═══════════════════════════════════════════════════════════════════════════════
0) REGRA ABSOLUTA — LEIA ISTO ANTES DE QUALQUER COISA
═══════════════════════════════════════════════════════════════════════════════

O README.md DA RAIZ É A CABEÇA DE TUDO. Ele é o ENUNCIADO do desafio, é a fonte de
verdade e NÃO PODE SER EDITADO (nem uma vírgula). Sempre que houver dúvida — de
regra de negócio, de contrato, de status, de escopo, de nomenclatura — a resposta
está no README.md, e ele vale SOBRE qualquer plano, handoff, documento interno ou
opinião minha (sua, no caso). Se o PLANO.md, o ARCHITECTURE.md, o
docs/CONFORMIDADE_README.md ou este prompt discordarem do README.md, VALE O
README.md, e a divergência deve ser registrada.

ANTES DE ESCREVER UMA LINHA DE CÓDIGO, leia nesta ordem:
  1. README.md (raiz) — o enunciado. Para esta fase: §8 (concorrência, e a frase
     "as garantias devem ser demonstradas com pelo menos três processos
     independentes, cada um com suas próprias conexões e memória"), §5 (garantias
     obrigatórias), §13 (os 8 cenários de "Testes de concorrência e recuperação"),
     §14 (critérios e eliminatórios) e §3 (entrega at-least-once e as falhas que
     precisam ser tratadas)
  2. PROXIMO_PASSO.md — handoff completo: estado, método, ambiente, decisões
     congeladas, pendências, lições das Fases 8 e 9 e o prompt no §9
  3. ARCHITECTURE.md — §6 (transações e concorrência: READ COMMITTED + FOR UPDATE,
     a ordem do lock antes de ler o saldo, as duas camadas de proteção), §7
     (idempotência), §8 (outbox e ordem), §11 (consumo do SQS), §13 (recuperação de
     falhas, linha a linha), §15 (os 39 defeitos já encontrados) e §16 (limitações)
  4. docs/CONFORMIDADE_README.md — §8, §13 e §14: exatamente o que esta fase fecha
  5. docs/evidencias/FASE_9.md — a fase imediatamente anterior: §5 (os defeitos 37,
     38 e 39), §6 (a limitação encontrada no binário) e §7 (as decisões)
  6. PLANO.md §3 — o checklist da FASE 10

═══════════════════════════════════════════════════════════════════════════════
0b) OS DOIS DOCUMENTOS FINAIS — O QUE ESTA CAMINHADA ESTÁ CONSTRUINDO
═══════════════════════════════════════════════════════════════════════════════

Esta fase NÃO termina quando os testes ficam verdes. Ela termina quando você
consegue EXPLICAR o que provou. Existem dois documentos pendentes que são entrega
obrigatória (compromisso registrado em PROXIMO_PASSO §3.5 e em PLANO §3, exigência
do usuário datada de 2026-10-16), e a Fase 10 é a que mais produz matéria-prima
para eles:

  docs/SISTEMA_EXPLICADO.md — para quem vai LER E MANTER o código
    Do básico ao avançado, SEM PRESUMIR VOCABULÁRIO: o que é dinheiro em unidades
    mínimas, o que é carteira, o que é um lançamento de ledger, o que é uma
    transação/lock, o que é idempotência, o que é outbox — e só DEPOIS as ligações
    entre as partes (quem chama quem, onde cada garantia é imposta no banco) e as
    regras de negócio (por que LOSS não movimenta, por que ROLLBACK de REFUND
    debita, por que o replay devolve o saldo original). Inclui o fluxo completo de
    uma requisição (HTTP → commit → publicação), cada componente e por que ele
    existe, as decisões com o motivo, a lista de TODOS os problemas resolvidos
    (sintoma, causa, correção) e os limites conhecidos.

  docs/ENTREVISTA.md — para o USUÁRIO TREINAR
    Perguntas e respostas na lógica de quem entrevista: arquitetura ("por que
    READ COMMITTED + FOR UPDATE e não SERIALIZABLE?", "por que outbox e não
    publicar direto?", "por que o ledger é separado da carteira?"), decisão ("por
    que int64 em unidades mínimas?", "por que o domínio não importa encoding/json?"),
    garantia ("como você impede débito duplicado?", "onde está a proteção contra
    lost update?", "o que acontece se o processo morrer no meio?"), erro e
    aprendizado ("qual foi o bug mais difícil?", "o que faria diferente?"). As
    perguntas DIFÍCEIS entram, com resposta honesta — inclusive as que expõem
    limitação (o que ficou de fora, o que é frágil e por quê).

  ⚠️ O que os dois NÃO são: não são resumo do ARCHITECTURE.md (aquele é para o
  avaliador, denso e em terceira pessoa). Estes dois são para ENSINAR — e um
  documento que só diz "o que" sem "por quê" e "o que quebraria sem isso" não
  cumpre o compromisso.

  ⚠️ Nenhum dos dois pode contrariar o README.md da raiz. Toda interpretação
  adotada (as que estão em docs/CONFORMIDADE_README.md e no ARCHITECTURE §9–§11)
  aparece nos dois, com o motivo e a consequência.

POR QUE ISSO É TAREFA DESTA FASE, E NÃO SÓ DA 12:
  - a regra (c) do método (§1) já manda explicar cada parte em
    DECISÃO | MOTIVO | O QUE QUEBRARIA SEM ELA. Essas tabelas SÃO o rascunho dos
    dois documentos. Se você as escrever "para cumprir tabela", a Fase 12 vira
    arqueologia; se escrever pensando em quem vai ler, a Fase 12 vira montagem;
  - a Fase 10 é o cenário de 20 pontos do §14 (concorrência) e o que mais rende
    história para a entrevista: a corrida das duas apostas de 80.00 sobre 100.00,
    a mesma aposta 50× entre três processos, o consumidor morto entre o commit e o
    delete, a instância nova que assume. Cada cenário provado é uma pergunta de
    entrevista que o usuário vai poder responder COM NÚMERO, não com opinião;
  - todas as explicações que você der nesta conversa precisam ser
    ENTREVISTA-READY: comece pelo porquê, use o vocabulário e explique o
    vocabulário na primeira vez, prefira a consequência concreta ("o segundo
    débito entra e o saldo fica negativo") ao jargão ("haveria uma race
    condition").

ENTREGÁVEL CONCRETO NESTA FASE (além das evidências):
  - docs/evidencias/FASE_10.md ganha uma seção final "MATÉRIA-PRIMA PARA OS DOIS
    DOCUMENTOS" com: os cenários provados na forma de pergunta→resposta curta, as
    decisões em tabela, e a lista do que a Fase 10 acrescentou à compreensão do
    sistema (o que se aprendeu sobre locks, idempotência e recuperação que antes
    era só teoria). É esse material que a Fase 12 vai reorganizar — não reescrever;
  - se, ao explicar um cenário, você perceber que o código NÃO é defensável como
    está (uma decisão frágil, um comportamento difícil de justificar), DIGA ISSO
    nesta fase, registre na §16 do ARCHITECTURE e responda honestamente no material
    da entrevista. Documento que esconde fragilidade não ajuda em entrevista: o
    entrevistador encontra.

═══════════════════════════════════════════════════════════════════════════════
1) MÉTODO DE TRABALHO (o mesmo das Fases 6 a 9 — não mude)
═══════════════════════════════════════════════════════════════════════════════

a) A IA ESCREVE O CÓDIGO. O usuário revisa e contesta. Não peça para ele digitar.

b) UM BLOCO COERENTE POR VEZ. Nunca entregue a fase inteira de uma vez. Cada bloco:
   código/teste → validação real → commit. Depois do commit, o próximo bloco.

c) EXPLIQUE DEPOIS DE ESCREVER, e explique de verdade. Para cada parte que importa,
   no formato de tabela: DECISÃO | MOTIVO | O QUE QUEBRARIA SEM ELA. Sem jargão não
   explicado: o usuário está se preparando para a entrevista e precisa saber
   DEFENDER cada linha. Explicação rasa é entrega incompleta.

d) VALIDE DE VERDADE, com a suíte inteira, e mostre a saída real (não "deve
   funcionar"). Obrigatório em cada bloco:
     go build ./...
     go vet ./...
     go vet -tags=integration ./...
     gofmt -l ./internal/ ./cmd/ ./migrations/ ./test/
     go test -count=1 -race ./...
     go test -count=1 -race -tags=integration -timeout 600s ./...
   (o timeout sobe para 600s nesta fase: os cenários sobem PROCESSOS REAIS)

e) ACEITE CONTESTAÇÃO. Se o usuário apontar um erro e ele estiver certo, ADMITA E
   CORRIJA, sem defensiva e sem minimizar. Isso é o que mais agrega valor aqui.

f) QUANDO UM TESTE FALHAR, ANTES DE ACUSAR O CÓDIGO VERIFIQUE SE A RÉGUA DO TESTE
   ESTÁ CERTA. Só nesta caminhada foram corrigidas asserções erradas das Fases 6, 7,
   8 e 9 (a do FIFO com grupo fixo, a do dedup id repetido, a da carteira sem
   lançamento de abertura, a da ordem de chamadas com `pendingStats`, a do TTL com
   `MaxAttempts=3`, a do readiness "não iniciado"). E quando o sintoma não fizer
   sentido, escreva um teste de UNIDADE que rode o dado REAL que falhou.

g) REGISTRE TODO ACHADO na mensagem de commit E nas evidências da fase, com o
   sintoma, a causa de fundo e a correção. Os defeitos já somam 39 e eles são parte
   do valor da entrega.

h) NÃO QUEBRE O QUE JÁ FUNCIONA. Antes de mudar algo de uma fase anterior, leia o
   código e o teste que o cobre. As Fases 0 a 9 estão validadas: qualquer alteração
   nelas exige rodar a suíte inteira de novo.

i) NESTA FASE, O CÓDIGO DE PRODUÇÃO NÃO DEVE MUDAR — a não ser que um cenário
   REVELE um defeito. A fase é de VERIFICAÇÃO (README §13): se um cenário falhar, a
   correção vem com o teste que a trava, e o achado é registrado como os outros.
   Mudança de produção "por precaução", sem cenário que a exija, é fora de escopo.

═══════════════════════════════════════════════════════════════════════════════
2) ESTADO ATUAL (fim da Fase 9 — tudo abaixo está PRONTO e VALIDADO)
═══════════════════════════════════════════════════════════════════════════════

Domínio (Fases 1–3): Money (int64 em unidades mínimas), Wallet, Ledger append-only,
WagerTransaction com máquina de estados — 574 casos, `wagering` 100% de cobertura.
Persistência (Fase 5): repositórios pgx, Unit of Work, lock por carteira
(SELECT ... FOR UPDATE), migrations.
Caso de uso (Fase 6): Process (5 tipos externos + referências), OpenWallet, leituras,
Reconcile, outbox na MESMA transação, idempotência persistente (payload_hash +
replay do resultado original).
HTTP (Fase 7): 7 endpoints do §9 com OIDC real (Keycloak), isolamento por provedor,
201/200/202/422/409/403/404/503 nos lugares certos.
SQS consumidor (Fase 8): wager-transactions.fifo + DLQ, inbox na MESMA transação
(ganchos ProcessHooks OnBegin/OnCommit), retry por ChangeMessageVisibility com
backoff, DLQ explícita, encerramento seguro, readiness da fila.
Publicação e referências (Fase 9): OutboxPublisher (claim → publicar fora da tx →
marcar, lease, quarentena), ReferenceWorker (Retomar com TTL e backoff),
PendingReaper, Runner com supervisão, readiness `workers` — provados contra
PostgreSQL + LocalStack reais e pelo binário real.

ESQUEMA: versão 5. Tabelas: wallets, wallet_ledger_entries, wager_transactions,
inbox_messages, outbox_events. Índices que SÃO a garantia e você vai encontrar nos
testes desta fase:
  - `wallet_ledger_entries`: UNIQUE (wallet_id, transaction_id) — impede lançamento
    duplicado para a mesma operação;
  - `wager_transactions`: UNIQUE parcial de idempotência por (provider, chave) e por
    (provider, external_id); UNIQUE parcial (reference_transaction_id, kind);
  - `inbox_messages`: UNIQUE (consumer_name, message_id);
  - CHECK de saldo não negativo, CHECKs de valor, triggers de imutabilidade do ledger.

O QUE JÁ EXISTE E DEVE SER REUSADO (não reimplemente):
  - `internal/app/healthcheck_integration_test.go`: o PADRÃO para subir o BINÁRIO
    real em teste (`compilarBinario`, `exec.Command`, `portaDeTeste`,
    `esperarPronto`, `portaDeTeste`). É a base da infraestrutura multi-instância
    desta fase — hoje ele sobe 1 processo; esta fase precisa de N.
  - `test/integration/`: `limpar(t)` (TRUNCATE), `carteiraNova`, `carteiraAberta`
    (OpenWallet pela regra), `pedido`, `comChave/comExterna/comReferencia`,
    `postgresUOW` (Unit of Work NOVO sobre o mesmo pool = outra instância lógica),
    `saldoNoBanco`, `versaoNoBanco`, `lancamentosNoBanco`, `contarTransacoes`,
    `eventosNaOutbox`, `transacaoNoBanco`, e o `TestMain` que segura um
    `pg_advisory_lock` (os pacotes de integração NÃO podem truncar o banco ao mesmo
    tempo — já está resolvido, não mexa).
  - `test/integration/sqs_integration_test.go`: `abrirSQS`, `esvaziarFila`,
    `enviar` (com NONCE no dedup id), `rodarConsumidor`, `mensagensNaFila`,
    `registrarInboxComoProcessada`, `esperarAtende`.
  - `internal/app/wagering/service_test.go` (unitário) e
    `test/integration/wagering_service_test.go`: os cenários de concorrência JÁ
    provados em UM processo (`TestMesmaApostaEmParaleloProcessaUmaVez` com 12
    disparos; a disputa 80+80; HTTP × SQS concorrentes). Nesta fase eles são
    REPETIDOS COM PROCESSOS INDEPENDENTES — não apague nem reescreva os existentes.
  - `test/integration/outbox_worker_integration_test.go`: o T6 (dois publishers) e o
    T7 (REFUND antes da referência) já provados com DUAS instâncias lógicas
    (`postgresUOW`) e LocalStack real.
  - `observability`: as 12 métricas já estão emitidas; `wager_operations_total`,
    `wager_operation_duration_seconds`, `wager_outbox_*`, `wager_reference_*`.
    A latência p50/p95/p99 desta fase é MEDIDA a partir delas ou do tempo de
    resposta do cliente.

O QUE AINDA NÃO EXISTE (é o que você vai construir):
  - nenhum teste com **3 processos independentes** de verdade (hoje o máximo são
    goroutines no mesmo processo ou duas instâncias lógicas via `postgresUOW`);
  - nenhum teste que INTERROMPA um consumidor real (SIGTERM/`docker stop`) entre o
    commit e o delete, nem que reinicie a aplicação e confira idempotência e
    pendências;
  - nenhuma medida de paralelismo entre carteiras (T3) com prova de que o tempo não
    cresce como se houvesse lock global;
  - nenhuma prova de latência sob concorrência (diferencial do §14, opcional);
  - a escala do compose: hoje há UM serviço `api` (porta 8081). Três instâncias
    podem ser três processos locais do binário ou três serviços no compose — a
    escolha e o motivo devem estar registrados.

═══════════════════════════════════════════════════════════════════════════════
3) A TAREFA — FASE 10 (README §8 e §13), dividida em QUATRO BLOCOS
═══════════════════════════════════════════════════════════════════════════════

RELEIA o §8 e o §13 do README antes de começar. O §8 pede, literalmente:
  - "A coordenação deve ocorrer por carteira. Escolha locking pessimista, controle
     otimista com retry limitado, atualização atômica condicionada ou uma
     combinação justificável."
  - "As garantias devem ser demonstradas com pelo menos TRÊS PROCESSOS
     INDEPENDENTES, cada um com suas próprias conexões e memória."
  - "Teste obrigatório: uma carteira com 100.00 BRL recebe, ao mesmo tempo, duas
     apostas distintas de 80.00 BRL. O resultado deve conter uma aposta processada,
     uma rejeição por saldo insuficiente, saldo final de 20.00 BRL e um único
     débito no ledger. Reenvios não podem alterar esse resultado. Carteiras
     diferentes devem continuar sendo processadas em paralelo."

O §13 pede os 8 cenários, e o §13 fecha com: "Ao final, confira o saldo armazenado
contra a soma de créditos menos débitos do ledger. Inclua cenários que cruzem HTTP e
SQS para a mesma operação. Os testes de duplicidade devem exercitar a deduplicação
da APLICAÇÃO, com recebimentos repetidos comprovados. Execute go test -race nos
testes aplicáveis."

  BLOCO 10a — a infraestrutura de prova multi-instância
    - um pacote/arquivo de apoio em test/integration (ou internal/app, onde estiver
      o padrão do binário real) que:
        * compila o binário UMA vez por execução da suíte (go build para um
          diretório temporário);
        * sobe N processos com `HTTP_ADDR` distintos, MESMO `DATABASE_URL`
          (jungle_test) e MESMAS filas do LocalStack, cada um com suas conexões e
          sua memória — isso É o "processo independente" do README §8;
        * espera o `/health/ready` de cada um (o readiness hoje inclui postgres,
          oidc, sqs e workers);
        * encerra todos no `t.Cleanup` (Kill no Windows; sinal nos demais);
        * permite ligar/desligar consumidor e workers por instância
          (`SQS_CONSUME_TRANSACTIONS`, `OUTBOX_ENABLED`, `REFERENCE_ENABLED`) —
          os cenários de número NÃO devem ter o publicador/consumidor rodando de
          fundo, senão a asserção fica imprevisível;
        * usa `LOG_LEVEL=warn` (ou buffer por processo) para o log não inundar a
          saída, mas DESPEJA o log do processo na falha — foi assim que a Fase 8
          encontrou "mensagens=0 em laço".
    - deixar documentado (no próprio código e nas evidências) por que essa via e não
      três containers: o README pede três PROCESSOS independentes, e o processo
      local tem as mesmas propriedades (conexões, memória, pool próprios) sem
      custo de rebuild de imagem; os containers entram na prova de SIGTERM (10c).
    - cuidado com o pool: o default é `DB_MAX_CONNS=20` por instância; 3 instâncias
      são 60 conexões no `jungle_test`. Se o PostgreSQL reclamar, ajuste
      `DB_MAX_CONNS` nas instâncias de teste (e registre).

  BLOCO 10b — T1, T2 e T3 com processos independentes
    - **T1 (README §13.1):** a MESMA aposta 50 vezes em paralelo, distribuída entre
      os 3 processos (HTTP) e também pelas duas portas quando fizer sentido →
      UM débito, UMA linha em wager_transactions, versão da carteira +1, e todos
      os reenvios devolvendo o MESMO resultado (idempotentReplay true depois da
      primeira). O mesmo cenário pela FILA: 50 mensagens com o MESMO corpo e chaves
      diferentes de entrega → a idempotência FINANCEIRA barra as repetições.
    - **T2 (README §8):** a carteira com 100.00 e duas BET de 80.00 SIMULTÂNEAS,
      cada uma chegando por uma instância diferente, disparadas por uma barreira
      (channel/go) para que sejam de fato concorrentes → exatamente UMA
      `PROCESSED`, UMA `REJECTED` com `INSUFFICIENT_FUNDS`, saldo 20.00, **um**
      lançamento de débito, versão 2. Reenviar as duas 10× depois e conferir que
      NADA muda (mesmo resultado, saldo 20.00, um lançamento).
    - **T3 (README §13.3):** C carteiras distintas processando ao mesmo tempo, cada
      operação em uma instância → todas concluem, e o tempo total é compatível com
      PARALELISMO, não com serialização. Prove o paralelismo do jeito honesto e
      estável: meça a duração de N operações na MESMA carteira (serializadas pelo
      lock) contra N operações em carteiras DIFERENTES (paralelas) e compare; ou
      observe no PostgreSQL (`pg_locks`/`pg_stat_activity`) que não há lock global.
      Evite asserção de tempo frágil: prefira uma razão com folga grande e um
      comentário explicando o porquê.
    - estes testes são de INTEGRAÇÃO (`//go:build integration`) e rodam com
      `-race`; nada de `time.Sleep` mágico — use barreira e polling com prazo.

  BLOCO 10c — T5 e T8: interrupção e reinício
    - **T5 (README §13.5):** interromper o consumidor DEPOIS do commit e ANTES do
      delete, e validar a reentrega. Faça em DUAS camadas, e diga qual prova o quê:
        (i) DETERMINÍSTICA, com processo real: reconstruir o estado durável exato
            da interrupção (a operação aplicada + a inbox PROCESSED + a mensagem
            AINDA na fila) e subir uma instância nova que consome → a inbox recusa,
            NADA é debitado de novo, a mensagem é removida e a DLQ fica vazia.
            Explique no comentário por que esse estado é idêntico ao de uma queda
            real entre o commit e o delete;
        (ii) COM SINAL REAL, se possível no ambiente: no CONTAINER (Linux) o
            SIGTERM funciona — `docker stop`/`docker start` no serviço `api` com
            uma mensagem em voo, e a mensagem volta. Se não for automatizável,
            faça como prova roteirizada e documente a saída real nas evidências
            (foi assim que a Fase 9 provou o binário). No Windows o `Kill` é
            terminação dura: registre a limitação (P8 do handoff).
    - **T8 (README §13.8):** reiniciar a aplicação e verificar que idempotência,
      pendências e consistência foram preservadas:
        * enviar uma aposta, derrubar a instância, subir OUTRA e reenviar a mesma
          aposta → replay, sem segundo débito;
        * deixar uma operação em `PENDING_REFERENCE` (REFUND antes da BET), derrubar
          e subir de novo → o worker de referências (instância nova) resolve quando
          a BET chega: a pendência é DURÁVEL, não vive em memória;
        * conferir a reconciliação (saldo armazenado == soma do ledger) depois de
          tudo, com o endpoint `POST /wallets/{id}/reconciliation`.
    - **T6 e T7 repetidos com 3 instâncias reais** (o que a Fase 9 provou com 2
      instâncias lógicas): os três publishers ligados disputando a mesma outbox, e
      o REFUND antes da referência resolvido por uma instância enquanto outra
      continua trabalhando. Se a repetição não acrescentar nada, DIGA ISSO e
      justifique (não invente cenário para ocupar espaço).

  BLOCO 10d — fechamento: conferência final e latência (diferencial)
    - a CONFERÊNCIA FINAL do §13 em todos os cenários: saldo armazenado == soma de
      créditos − débitos do ledger (a reconciliação), inclusive cruzando HTTP e SQS
      na MESMA operação;
    - T4 do PLANO: repetir os cenários relevantes com 3 instâncias (é o que 10b/10c
      fazem — aqui é a rodada consolidada, com a suíte inteira);
    - latência (diferencial do §14, OPCIONAL e só se sobrar tempo): um comando
      reprodutível que dispare N operações concorrentes e reporte p50/p95/p99,
      throughput, erros e conflitos, com o método escrito. Se não fizer, diga que
      ficou de fora e por quê;
    - rodar a suíte INTEIRA (todas as fases) e confirmar que nada quebrou.

═══════════════════════════════════════════════════════════════════════════════
4) O QUE **NÃO** FAZER NESTA FASE
═══════════════════════════════════════════════════════════════════════════════

  - NÃO editar o README.md da raiz (jamais).
  - NÃO reescrever as garantias que já existem (lock por carteira, idempotência,
    outbox, inbox). Se um cenário falhar, o defeito é corrigido COM o teste que o
    trava, e o argumento vem antes do código.
  - NÃO trocar `READ COMMITTED + FOR UPDATE` por `SERIALIZABLE`, nem introduzir lock
    global: o README §5.6 proíbe lock global e o §8 exige coordenação POR CARTEIRA.
  - NÃO usar mocks para PostgreSQL, Keycloak ou SQS nos cenários desta fase: o
    §13/§14 são explícitos, e é justamente aqui que a infra real é a prova.
  - NÃO criar "sleep" para sincronizar teste: barreira (channel), polling com prazo
    (`esperarAtende`) e `t.Cleanup` — sempre.
  - NÃO usar `Set-Content` para arquivos com acento (PowerShell 5.1 grava ANSI e
    corrompe — aconteceu na Fase 9). Use a ferramenta de edição ou
    [System.IO.File]::WriteAllText com UTF8Encoding($false).
  - NÃO rodar `git commit` com mensagem longa por heredoc: escreva a mensagem num
    ARQUIVO em .git/ e use `git commit -F arquivo`. Texto em ASCII.
  - NÃO começar a Fase 11 (dashboards, goleak, go mod tidy) nem a Fase 12 (os dois
    documentos finais): cada uma tem o seu momento.

═══════════════════════════════════════════════════════════════════════════════
5) AMBIENTE (detalhes que causam erro se ignorados)
═══════════════════════════════════════════════════════════════════════════════

Sempre, em todo comando Go:
  $env:GOMODCACHE='E:\jungleg\.gocache\mod'
  $env:GOCACHE='E:\jungleg\.gocache\build'
  $env:GOPATH='E:\jungleg\.gocache\gopath'

Containers: jungleg-postgres, jungleg-keycloak, jungleg-localstack (os três healthy).
Bancos: `jungleg` (aplicação) e `jungle_test` (testes — os testes TRUNCAM tabelas,
então NUNCA aponte para `jungleg`). Esquema na versão 5.
PowerShell 5.1: sem `&&`, sem ternário. `make` NÃO existe.
`bin/migrate.exe` carrega as migrations EMBUTIDAS: só recompile se criar migration
(esta fase não deve criar nenhuma).

Comandos de integração (sem eles os testes são pulados ou falham por ambiente):
  $env:TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
  $env:OIDC_ISSUER='http://localhost:8080/realms/jungleg'
  $env:OIDC_DISCOVERY_URL='http://localhost:8080/realms/jungleg'
  $env:OIDC_AUDIENCE='jungleg-api'
  $env:SQS_ENDPOINT='http://localhost:4566'

⚠️ AS FILAS PRECISAM EXISTIR antes de confiar em qualquer teste verde de SQS:
  docker exec jungleg-localstack awslocal sqs list-queues
Esperado: wager-transactions.fifo, wager-transactions-dlq.fifo, wallet-events.fifo,
wallet-events-dlq.fifo. Vazio = o script de init falhou, e o teste falha POR ISSO.

Armadilhas que já custaram tempo nesta caminhada:
  - `docker exec … awslocal sqs send-message` com JSON montado no PowerShell
    CORROMPE o corpo (e o `sh -c` quebra nas aspas). Envie lendo de um ARQUIVO
    copiado para o container (docker cp + script com BODY=$(cat arquivo)).
  - fila FIFO: `MessageGroupId` repetido com mensagem EM VOO bloqueia as seguintes
    do grupo — o `Receive` devolve zero e parece que o consumidor morreu. Nos
    testes, um grupo por cenário e esvazie a fila entre eles.
  - `MessageDeduplicationId` repetido: o SQS deduplica por 5 MINUTOS e devolve o
    MessageId da mensagem ORIGINAL. Use NONCE por envio.
  - visibilidade: a fila do compose usa 60s (wager-transactions) / 30s
    (wallet-events); no ambiente de TESTE use 2s, senão a suíte fica refém dos
    próprios resíduos.
  - 3 processos × `DB_MAX_CONNS=20` = 60 conexões: se o PostgreSQL recusar, baixe o
    pool nas instâncias de teste e registre.
  - Windows: `os.Interrupt`/`Kill` são terminação dura; a prova de SIGTERM é no
    container (Linux). `taskkill /F /PID` encerra o processo.
  - portas: o compose publica 8081; a suíte usa 18099 (binário único) — escolha uma
    faixa livre e documente (ex.: 18091/18092/18093).
  - o `TestMain` dos pacotes de integração segura um `pg_advisory_lock`: os pacotes
    não truncam o banco ao mesmo tempo. NÃO remova esse lock; se um teste novo
    truncar tabelas fora dele, ele vai falhar de forma intermitente.

═══════════════════════════════════════════════════════════════════════════════
6) CRITÉRIOS DE ACEITE (não considere a fase pronta sem TODOS)
═══════════════════════════════════════════════════════════════════════════════

  [ ] build, vet (com e sem a tag), gofmt e `go test -race` LIMPOS (unidade e
      integração), com a saída real colada na resposta e nas evidências
  [ ] TRÊS processos independentes subindo e atendendo (com conexões e memória
      próprias), com encerramento garantido no fim de cada teste
  [ ] T1: a mesma aposta 50× entre os 3 processos → um débito, um lançamento,
      mesmo resultado em todos os reenvios
  [ ] T2: 100.00 + duas BET 80.00 simultâneas (instâncias diferentes) → 1
      PROCESSED, 1 REJECTED/INSUFFICIENT_FUNDS, saldo 20.00, UM débito; reenvios
      não alteram nada
  [ ] T3: carteiras distintas em paralelo, com prova de que o paralelismo existe e
      que não há lock global
  [ ] T4: os cenários relevantes rodados com as 3 instâncias (não só em um processo)
  [ ] T5: interrupção depois do commit e antes do delete → reentrega reconhecida
      pela inbox, sem movimentação duplicada (e o limite da prova, se houver,
      escrito)
  [ ] T6 e T7 repetidos com 3 instâncias reais (ou a justificativa de que a
      repetição não acrescenta, com o argumento)
  [ ] T8: reinício preservando idempotência, pendências e consistência; a
      `PENDING_REFERENCE` resolvida por uma instância NOVA
  [ ] conferência final: saldo armazenado == soma de créditos − débitos do ledger em
      todos os cenários, inclusive cruzando HTTP e SQS
  [ ] nenhuma regressão: a suíte inteira (Fases 1–9) continua verde

═══════════════════════════════════════════════════════════════════════════════
7) ENTREGÁVEIS DE DOCUMENTAÇÃO (parte da entrega, não opcional)
═══════════════════════════════════════════════════════════════════════════════

Ao final da fase, atualizar TODOS:
  1. docs/evidencias/FASE_10.md — NOVO, no modelo da FASE_9.md: ambiente, saída real
     dos comandos, os OITO cenários um por um (o que foi feito, o que se esperava, o
     que se observou), os defeitos encontrados (sintoma, causa de fundo, correção),
     as decisões, as interpretações, as limitações e a rotina de execução
  2. ARCHITECTURE.md — §6 e §8 com o que a prova multi-instância confirmou (ou
     mudou), §13 com as linhas de T1–T8 marcadas como PROVADAS, §15 com os defeitos
     novos numerados, §16 com as limitações novas, §17 com as contagens
  3. docs/CONFORMIDADE_README.md — §8 e §13 com os 8 cenários fechados (cada um com
     o nome do teste que o prova), §14 com os eliminatórios revisados
  4. PROXIMO_PASSO.md — estado, histórico de commits, registro da fase, pendências
     e o checklist da Fase 11
  5. PLANO.md — checklist da FASE 10 fechado, com os defeitos numerados, e os
     próximos 3 passos apontando para a Fase 11 e para os dois documentos finais
  6. `.env.example` / `docker-compose.yml` — só se a fase acrescentar chave ou
     serviço (ex.: `api-2`/`api-3`); se não acrescentar, diga isso

E LEMBRE-SE DO COMPROMISSO FINAL (PROXIMO_PASSO §3.5 e §0b deste prompt): no fim
das fases, dois documentos — docs/SISTEMA_EXPLICADO.md (do básico ao avançado, sem
presumir vocabulário, para quem vai manter o código) e docs/ENTREVISTA.md (perguntas
e respostas, incluindo as difíceis e as que expõem limitação). Os dois são ENTREGA
OBRIGATÓRIA, e a Fase 10 é a maior fonte de matéria-prima deles: concorrência é o
critério de 20 pontos do §14. Por isso, ao fim desta fase:
  - a seção final de docs/evidencias/FASE_10.md ("MATÉRIA-PRIMA PARA OS DOIS
    DOCUMENTOS") tem de existir, com os cenários em pergunta→resposta curta, as
    decisões em tabela e o que a fase acrescentou à compreensão do sistema;
  - tudo o que você explicar nesta conversa precisa estar escrito de forma que a
    Fase 12 apenas ORGANIZE — nunca precise reconstruir o raciocínio.

═══════════════════════════════════════════════════════════════════════════════
8) PRIMEIRA RESPOSTA ESPERADA
═══════════════════════════════════════════════════════════════════════════════

Não comece a codar. Na primeira resposta:
  a) confirme que leu o README §8, §13 e §14 e diga, com suas palavras, o que esta
     fase exige e o que NÃO exige (a fase é de VERIFICAÇÃO: o código de produção não
     deve mudar, a não ser que um cenário revele defeito);
  b) confirme o entendimento do método (IA escreve, um bloco por vez, explicar
     depois, validar de verdade, commit por bloco);
  c) confirme os QUATRO blocos propostos (10a infraestrutura multi-instância, 10b
     T1/T2/T3, 10c T5/T8, 10d fechamento) e diga se enxerga algum risco ou
     dependência que a proposta não cobriu — em especial: como você vai garantir que
     os processos realmente são INDEPENDENTES (conexões e memória), como vai provar
     T3 sem asserção de tempo frágil, e o que será SIMULADO em vez de executado (o
     kill entre commit e delete) com o motivo;
  d) diga exatamente qual será o primeiro passo concreto do bloco 10a;
  e) diga, em três linhas, o que você entendeu sobre os dois documentos finais
     (§0b): por que eles são entrega obrigatória, o que cada um ensina e qual é a
     matéria-prima que esta fase entrega para eles.
```

---

## 9. Histórico deste prompt

- **Fase 9 → 10** (este documento): escrito ao fim da Fase 9, já com as lições dela
  incorporadas — os defeitos 37/38/39 (guarda que ignora todo o trabalho, transição
  errada no reaper, correlação que morre no fim da requisição), a validação de config
  do pior caso, o `stop_grace_period` do compose, e a regra de que teste de
  integração diz QUE algo está errado enquanto o unitário com o dado real diz ONDE.
- **Revisão de 2026-10-17 (pedido do usuário):** acrescentada a seção **§0b**, que
  promove os dois documentos finais (`docs/SISTEMA_EXPLICADO.md` e
  `docs/ENTREVISTA.md`) a compromisso explícito da fase — o que cada um ensina, o
  que eles NÃO são, por que a matéria-prima é produzida AGORA (e não na Fase 12), o
  entregável concreto da fase (§ final de `docs/evidencias/FASE_10.md`) e a
  instrução de explicar tudo em formato "entrevista-ready"; §7 e §8 atualizados para
  cobrar isso também na primeira resposta.
- O prompt da Fase 9 (`PROMPT_FASE_9.md`) foi consumido e a fase está fechada; nada
  dele segue pendente.
