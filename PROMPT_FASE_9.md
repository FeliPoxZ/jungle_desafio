# PROMPT DE ABERTURA — FASE 9 (outbox publisher + workers de referência e reaper)

> Copie o bloco abaixo **inteiro** e envie como primeira mensagem da nova conversa.
> Ele é autossuficiente: traz o estado, o método, o escopo exato e os critérios de
> aceite. O `PROXIMO_PASSO.md` continua sendo o handoff completo — este prompt é a
> porta de entrada.

```text
Você vai continuar o projeto E:\jungleg (desafio técnico de back-end Go júnior:
Uber Fx + PostgreSQL + Keycloak/OIDC + AWS SQS via LocalStack). As Fases 0 a 8 já
estão CONCLUÍDAS, commitadas, validadas e documentadas. Sua tarefa é a FASE 9.

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
  1. README.md (raiz) — o enunciado. Para esta fase: §11 (outbox/publicação),
     §12 (observabilidade), §13 (verificação obrigatória: unitários, integração,
     concorrência e recuperação) e §14 (critérios de avaliação)
  2. PROXIMO_PASSO.md — handoff completo: estado, método, ambiente, decisões
     congeladas, pendências e este mesmo prompt no §9
  3. ARCHITECTURE.md — §8 (outbox e ordem), §11 (consumo do SQS, feita na Fase 8),
     §15 (os 36 defeitos já encontrados) e §16 (limitações conhecidas)
  4. docs/CONFORMIDADE_README.md — §11 é exatamente esta fase: diz o que o README
     pede e o que já existe
  5. docs/evidencias/FASE_8.md — a fase imediatamente anterior: leia o §7 (as
     decisões) e o §6b (os defeitos que só apareceram rodando o binário)
  6. PLANO.md §3 — o checklist da FASE 9

═══════════════════════════════════════════════════════════════════════════════
1) MÉTODO DE TRABALHO (o mesmo das Fases 6, 7 e 8 — não mude)
═══════════════════════════════════════════════════════════════════════════════

a) A IA ESCREVE O CÓDIGO. O usuário revisa e contesta. Não peça para ele digitar.

b) UM BLOCO COERENTE POR VEZ. Nunca entregue a fase inteira de uma vez. Cada bloco:
   código → validação real → commit. Depois do commit, o próximo bloco.

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
     go test -count=1 -race -tags=integration -timeout 300s ./...
   Ao final da fase, suba o BINÁRIO REAL com os workers ligados e prove o fluxo
   ponta a ponta (foi assim que apareceram 3 dos 8 defeitos da Fase 8).

e) ACEITE CONTESTAÇÃO. Se o usuário apontar um erro e ele estiver certo, ADMITA E
   CORRIJA, sem defensiva e sem minimizar. Isso é o que mais agrega valor aqui.

f) QUANDO UM TESTE FALHAR, ANTES DE ACUSAR O CÓDIGO VERIFIQUE SE A RÉGUA DO TESTE
   ESTÁ CERTA. Na Fase 8, três asserções estavam erradas (o FIFO com
   MessageGroupId fixo, o MessageDeduplicationId repetido entre execuções e a
   carteira criada por INSERT direto sem lançamento de abertura). E quando o
   sintoma não fizer sentido, escreva um teste de UNIDADE que rode o dado REAL que
   falhou: foi um teste com o corpo exato entregue pelo broker que localizou o
   defeito 31 (o `return` que abortava o parsing e devolvia requisição vazia).

g) REGISTRE TODO ACHADO na mensagem de commit E nas evidências da fase, com o
   sintoma, a causa de fundo e a correção. Os defeitos já somam 36 e eles são parte
   do valor da entrega.

h) NÃO QUEBRE O QUE JÁ FUNCIONA. Antes de mudar algo de uma fase anterior, leia o
   código e o teste que o cobre. As Fases 0 a 8 estão validadas: qualquer alteração
   nelas exige rodar a suíte inteira de novo.

═══════════════════════════════════════════════════════════════════════════════
2) ESTADO ATUAL (fim da Fase 8 — tudo abaixo está PRONTO e VALIDADO)
═══════════════════════════════════════════════════════════════════════════════

Domínio (Fases 1–3): Money/Wallet/Ledger/WagerTransaction, 574 casos, wagering 100%.
Persistência (Fase 5): repositórios pgx, Unit of Work, lock por carteira, migrations.
Caso de uso (Fase 6): Process (5 tipos + referências), OpenWallet, leituras,
  Reconcile, outbox gravada NA MESMA transação. Idempotência persistente com
  payload_hash, replay devolvendo o resultado original.
HTTP (Fase 7): 7 endpoints do §9 autenticados por OIDC real (Keycloak), isolamento
  por provedor, 403/404/422/503 nos lugares certos.
SQS consumidor (Fase 8): fila wager-transactions.fifo + DLQ, inbox na MESMA
  transação do tratamento (ganchos ProcessHooks OnBegin/OnCommit), retry por
  ChangeMessageVisibility com backoff exponencial, DLQ explícita, encerramento
  seguro no SIGTERM, readiness do SQS. Tudo provado contra o LocalStack real.

ESQUEMA: versão 4 (000004_inbox_attempts). Tabelas relevantes desta fase:
  - outbox_events: event_id (PK), aggregate_id, aggregate_type, event_type,
    payload (JSONB), occurred_at, available_at, attempts, last_error, published_at,
    created_at, sequence BIGSERIAL
  - wager_transactions: status PENDING_REFERENCE, reference_external_txn_id,
    reference_resolution_attempt, next_resolution_attempt_at

O QUE JÁ EXISTE E DEVE SER REUSADO (não reimplemente):
  - ports.OutboxRepository (internal/app/ports/repositories.go):
      Insert, ClaimBatch(ctx, limit, lease, now) com FOR UPDATE SKIP LOCKED e
      ordem por sequence, MarkPublished, MarkRetry(ctx, id, nextAttempt, reason)
      (attempts = attempts + 1 no SQL), PendingStats(ctx, now) → (pendentes, idade)
  - o repositório real: internal/infra/postgres/outbox_repository.go (leia os
    comentários: explicam por que a CTE existe e por que a ordem é `sequence`)
  - ports.TransactionRepository.ListPendingReferencesDue(ctx, now, limit)
    (status = 'PENDING_REFERENCE' AND next_resolution_attempt_at <= now)
  - domínio: MarkPendingReference(attempt, nextAttemptAt, now),
    ResolveReference(referenceID, attempt), MarkProcessed, MarkRejected, MarkFailed,
    e a máquina de estados: PENDING_REFERENCE → {PROCESSED, REJECTED, FAILED}
  - eventos com envelope completo e tipo/versão definidos pelo CONSTRUTOR:
    ProcessedEvent, RejectedEvent, PendingReferenceEvent, NewBalanceChangedEvent
    (internal/domain/wagering/events.go) — eventId, eventType, aggregateId,
    correlationId, causationId, occurredAt (UTC RFC3339), version, data tipado
  - caso de uso: resolverReferencia(ctx, repos, req) DEVOLVE o desfecho
    (informativa | pendente | rejeitada | resolvida + referente); `mover(...)` aplica
    o movimento na carteira; os métodos `emitirProcessado/emitirRejeicao/
    emitirPendencia` e `enfileirar` gravam os eventos na outbox
  - config já pronta: OUTBOX_{ENABLED,BATCH_SIZE,LEASE,POLL_INTERVAL,MAX_ATTEMPTS,
    BASE_BACKOFF,MAX_BACKOFF} e REFERENCE_{ENABLED,POLL_INTERVAL,BATCH_SIZE,
    MAX_ATTEMPTS,BASE_BACKOFF,MAX_BACKOFF,REAPER_INTERVAL,PENDING_MAX_AGE}
  - internal/consumer (Fase 8) é o MODELO para o laço de worker: contexto
    cancelável, semáforo, encerramento com prazo, backoff com teto e jitter
    SUBTRAÍDO dentro do teto, log e métrica em cada desfecho
  - o módulo Fx `workerModule()` em internal/app/worker.go e o grupo
    "health_checkers" (fx.Annotate + fx.ResultTags)
  - o adaptador SQS: internal/infra/sqs/client.go (Receive/Delete/ChangeVisibility/
    Send/Ping) — a Fase 8 já tem client, falta o caminho de PUBLICAÇÃO exposto por
    uma porta

O QUE AINDA NÃO EXISTE (é o que você vai construir):
  - nenhum publicador de outbox: `ClaimBatch` funciona e é provado no repositório,
    mas NINGUÉM publica
  - nenhum worker de referências: `PENDING_REFERENCE` fica pendente para sempre
  - o destino dos eventos (`wallet-events.fifo`) está PROVISIONADO no LocalStack,
    mas nenhum evento jamais foi publicado nele

═══════════════════════════════════════════════════════════════════════════════
3) A TAREFA — FASE 9 (README §11, PLANO.md §3)
═══════════════════════════════════════════════════════════════════════════════

RELEIA o §11 do README antes de começar. Ele pede, literalmente:
  - "Um worker separado publica os registros pendentes da outbox. Ele deve
     suportar múltiplos publishers, disputa por registros, backoff e recuperação
     de trabalho abandonado."
  - "Demonstre recuperação após interrupção entre commit e publicação e entre
     publicação e confirmação na outbox. Eventos pendentes devem ser assumidos por
     outra instância; republicações devem preservar o eventId."
  - "Provisione o destino dos eventos de saída e documente seus contratos de
     roteamento e consumo."
  - os 4 eventos exigidos (Processed, Rejected, WalletBalanceChanged,
     PendingReference) com o envelope obrigatório e o payload de
     WalletBalanceChanged com walletId, transactionId, direction, money,
     balanceBefore, balanceAfter e walletVersion.

Divida a fase em QUATRO BLOCOS, nesta ordem, um commit por bloco:

  BLOCO 9a — publicador da outbox
    - porta nova em internal/worker: `Publicador` (ex.: Publicar(ctx, Evento) error
      onde Evento carrega EventID, AggregateID, Type, Payload, OccurredAt)
    - implementação no adaptador SQS (internal/infra/sqs/publicador_adapter.go),
      sobre o client existente. O SQS NÃO recebe `Payload` cru: o corpo publicado é
      o snapshot IMUTÁVEL do evento (o `payload` JSONB gravado na outbox)
    - `worker.OutboxPublisher`: loop com ClaimBatch → publicar → MarkPublished;
      falha de publicação → MarkRetry com backoff exponencial + teto + jitter
      (mesma régua do consumidor da Fase 8, e o jitter DENTRO do teto);
      maxAttempts esgotado → NÃO republica mais, registra e loga (o evento fica
      visível para diagnóstico via PendingStats/last_error)
    - `MarkPublished` com context.WithoutCancel: a mensagem JÁ está na fila; perder
      a confirmação custaria uma republicação desnecessária. A ordem é
      "publicar ANTES de marcar" (at-least-once + dedup), NUNCA o contrário

  BLOCO 9b — worker de referências
    - `worker.ReferenceWorker`: ListPendingReferencesDue → reconstruir o `Request`
      a partir das colunas PERSISTIDAS da transação (provider_id,
      external_transaction_id, idempotency_key, player_id, wallet_id, round_id,
      game_id, kind, amount, reference_external_txn_id) → `resolverReferencia` →
      ramificar no desfecho:
        * resolvida  → lock da carteira, `mover`, ResolveReference + MarkProcessed,
                       evento Processed + WalletBalanceChanged na MESMA transação
        * pendente   → attempt+1, MarkPendingReference(attempt, nextAttemptAt, now)
                       com backoff exponencial + jitter, e evento
                       PendingReference na mesma transação
        * rejeitada  → MarkRejected(failureCode) + evento Rejected
        * tentativa > REFERENCE_MAX_ATTEMPTS ou idade > REFERENCE_PENDING_MAX_AGE
                     → MarkRejected(REFERENCE_NOT_FOUND) + evento Rejected
                       (é o TTL do README §13.7: "rejeição por expiração")
    - `worker.PendingReaper`: retoma `PENDING` antigos (PENDING_MAX_AGE), que é a
      segurança extra do item 6.3 — a operação que ficou no meio de um crash. Se
      puder concluir pelo desfecho da referência, conclui; se o desfecho não fechar,
      MarkFailed com código de auditoria. NUNCA deixe uma transação `PENDING` presa
      para sempre sem desfecho.
    - cuidado com o `FOR UPDATE SKIP LOCKED` nas duas pontas: dois workers não podem
      processar a MESMA transação ao mesmo tempo; o lock da carteira continua sendo
      a coordenação final

  BLOCO 9c — runner e composição
    - `worker.Runner`: supervisão com errgroup — sobe o publisher, o worker de
      referências e o reaper conforme a config (`OUTBOX_ENABLED`,
      `REFERENCE_ENABLED`), com backoff em falha de laço (um worker que morre não
      pode derrubar a API), log estruturado e shutdown limpo no SIGTERM (mesmo
      padrão do consumidor da Fase 8: para de buscar, conclui o que está em voo
      dentro do prazo e libera o resto)
    - registrar no Fx (`workerModule`) e no grupo `health_checkers`: um readiness
      que responde UP sem os workers é falso se o processo deveria estar publicando
    - validar a config: se um worker está ligado, o `HTTP_STOP_TIMEOUT` precisa
      cobrir o encerramento dele (foi o defeito 33: com os defaults, ligar o
      consumidor fazia a aplicação NÃO SUBIR)
    - `.env.example` e `docker-compose.yml` com as chaves novas

  BLOCO 9d — testes e prova de infra real
    - UNITÁRIOS (internal/worker): a política do publicador e dos workers com
      dublês — incluir a asserção de AUSÊNCIA de chamada (ex.: não marca publicado
      quando a publicação falha; não aplica movimento quando a referência ainda
      não resolve)
    - INTEGRAÇÃO (test/integration, build tag `integration`, contra PostgreSQL e
      LocalStack REAIS):
        * publicar de verdade em `wallet-events.fifo` e LER de volta
        * **crash entre o commit e a publicação** (evento na outbox, published_at
          NULL, processo morre): outra instância assume e publica
        * **crash entre a publicação e o MarkPublished**: a mensagem está na fila e
          o evento continua pendente → o lease expira → republica **com o MESMO
          eventId** (é o requisito literal do README). Prove que o corpo publicado é
          idêntico e que o eventId é o mesmo nas duas publicações
        * **dois publishers disputando a mesma outbox** (T6): nenhuma publicação
          duplicada a rigor, nenhuma perdida — e a ordem por `sequence` preservada
          para o mesmo `aggregateId`
        * **recuperação de lease expirado**: lease curto, publisher "morto" que não
          marca publicado, e o evento volta para outro publisher
        * **REFUND antes da referência ⇒ resolve depois** (T7, primeira metade):
          REFUND chega antes da BET → PENDING_REFERENCE + evento; a BET chega; o
          worker resolve e o REVERSAL acontece com o ledger correto
        * **expiração ⇒ REJECTED REFERENCE_NOT_FOUND** (T7, segunda metade): a
          referência nunca chega e o `REFERENCE_MAX_ATTEMPTS` estoura
        * **o reaper retoma um `PENDING` antigo** e o leva a um desfecho
    - rodar a suíte INTEIRA (todas as fases) e confirmar que nada quebrou

═══════════════════════════════════════════════════════════════════════════════
4) O QUE **NÃO** FAZER NESTA FASE
═══════════════════════════════════════════════════════════════════════════════

  - NÃO editar o README.md da raiz (jamais).
  - NÃO criar um consumidor de `wallet-events.fifo` para deduplicar eventos: nesta
    fase o evento é PUBLICADO. O contrato de consumo (inclusive a deduplicação por
    `eventId` além da janela de 5 minutos do SQS) deve ser DOCUMENTADO em
    ARCHITECTURE §11 como contrato de roteamento/consumo — o README §11 pede
    "documente seus contratos de roteamento e consumo".
  - NÃO começar a Fase 10 (3 instâncias, os 8 cenários de concorrência, T1/T2/T3).
    O que couber naturalmente (dois publishers) fica na Fase 9; o resto é a Fase 10.
  - NÃO trocar o mecanismo de banco que já existe e está provado (ClaimBatch,
    lease, MarkRetry). Se achar que ele está errado, TRAGA O ARGUMENTO com a
    evidência antes de mudar.
  - NÃO usar `Set-Content` para arquivos com acento (PowerShell 5.1 grava ANSI e
    corrompe). Use a ferramenta de edição ou
    [System.IO.File]::WriteAllText com UTF8Encoding($false).
  - NÃO rodar `git commit` com mensagem longa por heredoc: escreva a mensagem num
    ARQUIVO em .git/ e use `git commit -F arquivo`. Texto em ASCII para evitar
    problema de encoding no terminal.

═══════════════════════════════════════════════════════════════════════════════
5) AMBIENTE (detalhes que causam erro se ignorados)
═══════════════════════════════════════════════════════════════════════════════

Sempre, em todo comando Go:
  $env:GOMODCACHE='E:\jungleg\.gocache\mod'
  $env:GOCACHE='E:\jungleg\.gocache\build'
  $env:GOPATH='E:\jungleg\.gocache\gopath'

Containers: jungleg-postgres, jungleg-keycloak, jungleg-localstack (os três healthy).
Bancos: `jungleg` (aplicação) e `jungle_test` ( testes — os testes TRUNCAM tabelas,
então NUNCA aponte para `jungleg`).
PowerShell 5.1: sem `&&`, sem ternário. `make` NÃO existe.
`bin/migrate.exe` carrega as migrations EMBUTIDAS (go:embed): se criar migration
nova, RECOMPILE antes (go build -o bin\migrate.exe .\cmd\migrate\).

Comandos de integração (sem eles os testes são pulados ou falham por ambiente):
  $env:TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
  $env:OIDC_ISSUER='http://localhost:8080/realms/jungleg'
  $env:OIDC_DISCOVERY_URL='http://localhost:8080/realms/jungleg'
  $env:OIDC_AUDIENCE='jungleg-api'
  $env:SQS_ENDPOINT='http://localhost:4566'

⚠️ AS FILAS PRECISAM EXISTIR antes de confiar em qualquer teste verde de SQS:
  docker exec jungleg-localstack awslocal sqs list-queues
Se voltar vazio, o script de init falhou e os testes vão falhar POR ISSO, não por
defeito de código. (Na Fase 8 o script não tinha shebang nem bit de execução e as
filas NUNCA eram criadas — defeito 28, com `Exec format error` no log do container.)
Filas esperadas: wager-transactions.fifo, wager-transactions-dlq.fifo,
wallet-events.fifo, wallet-events-dlq.fifo (as duas últimas são o destino DESTA fase).

Armadilhas que já custaram tempo (estão no §4 do handoff e nas evidências da 8):
  - `docker exec … awslocal sqs send-message` com JSON montado no PowerShell
    CORROMPE o corpo. Envie lendo de um ARQUIVO copiado para o container
    (docker cp + BODY=$(cat arquivo)).
  - fila FIFO: `MessageGroupId` repetido com mensagem EM VOO bloqueia o grupo
    seguinte (o `Receive` devolve zero e parece que o consumidor morreu).
  - `MessageDeduplicationId` repetido: o SQS deduplica por 5 MINUTOS e devolve o
    MessageId da mensagem ORIGINAL. Em teste, use nonce.
  - a visibilidade da fila do compose é 60s; no ambiente de TESTE use 2s, senão a
    suíte fica refém dos próprios resíduos.

═══════════════════════════════════════════════════════════════════════════════
6) CRITÉRIOS DE ACEITE (não considere a fase pronta sem TODOS)
═══════════════════════════════════════════════════════════════════════════════

  [ ] build, vet (com e sem a tag), gofmt e `go test -race` LIMPOS (unidade e
      integração), com a saída real colada na resposta e nas evidências
  [ ] o publicador publica de verdade em wallet-events.fifo (provado lendo de volta)
  [ ] crash entre commit e publicação: outra instância assume e publica
  [ ] crash entre publicação e MarkPublished: republica com o MESMO eventId, e o
      corpo publicado é idêntico
  [ ] dois publishers disputando a mesma outbox: sem publicação perdida e sem
      duplicação a rigor, com a ordem por sequence preservada por aggregateId
  [ ] lease expirado recuperado por outro publisher
  [ ] REFUND antes da referência resolve depois (movimento e ledger corretos)
  [ ] referência que nunca chega expira em REJECTED/REFERENCE_NOT_FOUND + evento
  [ ] `PENDING` antigo retomado pelo reaper, com desfecho
  [ ] readiness e encerramento dos workers cobertos (inclusive a validação de
      config: ligar worker com defaults NÃO pode impedir o boot)
  [ ] métricas do §12 desta fase emitidas: atraso da outbox (PendingStats),
      resultados de publicação, retries, conflitos, idade do pendente mais antigo
  [ ] o BINÁRIO REAL subiu com os workers ligados e o fluxo ponta a ponta foi
      provado (operação → evento na outbox → publicado na fila), não só o teste

═══════════════════════════════════════════════════════════════════════════════
7) ENTREGÁVEIS DE DOCUMENTAÇÃO (parte da entrega, não opcional)
═══════════════════════════════════════════════════════════════════════════════

Ao final da fase, atualizar TODOS:
  1. docs/evidencias/FASE_9.md — NOVO, no modelo da FASE_8.md: ambiente, saída real
     dos comandos, os cenários provados, os defeitos encontrados (sintoma, causa de
     fundo, correção), as decisões, as interpretações adotadas, as limitações e a
     rotina de execução
  2. ARCHITECTURE.md — §11/§12 e uma seção de publicação (sai de [PLANEJADO] para
     [PRONTO]): a decisão "publicar antes de marcar", o lease, a recuperação, o
     contrato de roteamento e consumo de wallet-events.fifo, a tabela de
     MessageGroupId/MessageDeduplicationId também para os eventos; §15 ganha os
     defeitos novos com número; §16 as limitações novas; §17 as contagens
  3. docs/CONFORMIDADE_README.md — §11 passa a atendido com prova; §2 (mensageria),
     §6.5, §12 (métricas), §13/§14 atualizados; registrar as interpretações novas
  4. PROXIMO_PASSO.md — estado, histórico de commits, registro da fase, pendências
     e o checklist da Fase 10
  5. PLANO.md — checklist da FASE 9 fechado, com os defeitos numerados, e os
     próximos 3 passos apontando para a Fase 10
  6. .env.example — chaves novas, coerentes com os prefixos da config

E LEMBRE-SE DO COMPROMISSO FINAL (PROXIMO_PASSO §3.5): no fim das fases, dois
documentos — docs/SISTEMA_EXPLICADO.md (do básico ao avançado, sem presumir
vocabulário) e docs/ENTREVISTA.md (perguntas e respostas, incluindo as difíceis e
as que expõem limitação). Tudo o que você explicar agora é matéria-prima deles, e
o usuário precisa sair desta conversa ENTENDENDO o sistema, não só com ele pronto.

═══════════════════════════════════════════════════════════════════════════════
8) PRIMEIRA RESPOSTA ESPERADA
═══════════════════════════════════════════════════════════════════════════════

Não comece a codar. Na primeira resposta:
  a) confirme que leu o README §11, §12, §13 e §14 e diga, com suas palavras, o que
     esta fase exige e o que NÃO exige;
  b) confirme o entendimento do método (IA escreve, um bloco por vez, explicar
     depois, validar de verdade, commit por bloco);
  c) confirme os QUATRO blocos propostos (9a publicador, 9b worker de referências +
     reaper, 9c runner/composição, 9d testes e prova de infra real) e diga se
     enxerga algum risco ou dependência que a proposta não cobriu;
  d) diga exatamente qual será o primeiro passo concreto do bloco 9a.
```

---

## 9. Histórico deste prompt

- **Fase 8 → 9** (este documento): escrito ao fim da Fase 8, já com as lições dela
  incorporadas (as armadilhas do FIFO, o envio por arquivo, a validação de config
  que impede o boot, e a regra de que o teste de integração diz QUE algo está
  errado enquanto o teste de unidade com o dado real diz ONDE).
- O prompt anterior (Fase 8) foi consumido e a fase está fechada; nada dele segue
  pendente.
