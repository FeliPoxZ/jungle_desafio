# Observabilidade operacional — dashboard, alertas e runbook

Esta pasta é o **diferencial opcional** do `README.md` §12 (tracing e dashboards
são opcionais) entregue de forma versionada: um dashboard do Grafana, as regras de
alerta do Prometheus e o que fazer quando cada uma dispara.

| Arquivo | O que é |
| --- | --- |
| `dashboard-jungleg.json` | Dashboard do Grafana (13 painéis) — operações por desfecho e por tipo, latência p50/p95/p99, atraso da outbox, publicação por resultado, retries por etapa, DLQ, replays, reentregas, conflitos, tentativas de publicação, divergências, processo/readiness |
| `alertas.yml` | Regras do Prometheus, com limiar justificado no próprio arquivo |
| `prometheus.yml` | Scrape da API (`/metrics`) e do readiness via blackbox_exporter |

## Por que NÃO há um Prometheus no `docker-compose.yml`

Decisão registrada (Fase 11), com o custo e o benefício explícitos:

- o enunciado trata dashboards como **diferencial opcional** (§12) — o
  obrigatório são as métricas, e as 12 existem e são provadas em `/metrics`;
- o `docker-compose.yml` é o artefato que a entrega precisa provar de um
  **checkout limpo** (`docker compose up --build`). Cada serviço novo é mais uma
  imagem para baixar, mais um healthcheck para acertar e mais um modo de o
  "sobe do zero" falhar — sem mudar nenhuma garantia financeira;
- o que se perde sem o serviço é o `docker compose up` já trazer o painel. O que
  não se perde é a **definição**: dashboard e alertas estão versionados e sobem
  com dois `docker run`.

Se a preferência for ter tudo no compose, o custo é um serviço `prometheus` (e,
opcionalmente, `grafana`) apontando para `deploy/observability/`. A configuração
já está pronta para isso — as metas usam o nome `api` da rede do compose.

## Como subir (2 comandos)

```sh
docker compose up -d --build            # API em http://localhost:8081

docker run -d --name jungleg-blackbox --network jungleg_default \
  prom/blackbox-exporter:v0.25.0

docker run -d --name jungleg-prometheus --network jungleg_default \
  -p 9090:9090 \
  -v "$PWD/deploy/observability/prometheus.yml:/etc/prometheus/prometheus.yml:ro" \
  -v "$PWD/deploy/observability/alertas.yml:/etc/prometheus/alertas.yml:ro" \
  prom/prometheus:v2.54.1
```

PowerShell usa a mesma forma com `${PWD}` no lugar de `$PWD`. O Prometheus fica em
`http://localhost:9090` (aba **Status → Targets**: `jungleg` e `jungleg-ready`
precisam estar `UP`).

Grafana (opcional):

```sh
docker run -d --name jungleg-grafana -p 3000:3000 grafana/grafana:11.2.0
```

Em `http://localhost:3000` (admin/admin): **Connections → Data sources →
Prometheus** com URL `http://jungleg-prometheus:9090` (mesma rede), depois
**Dashboards → Import → Upload JSON** e escolha `dashboard-jungleg.json`.

## As consultas que importam

```promql
# Operações por desfecho, 5 min
sum by (status) (rate(jungleg_wager_operations_total[5m]))

# p99 de processamento (HTTP e SQS)
histogram_quantile(0.99, sum by (le) (rate(jungleg_wager_operation_duration_seconds_bucket[5m])))

# Atraso da outbox: idade do evento pendente mais antigo
jungleg_wager_outbox_pending_age_seconds

# Retentativas por etapa (quem está repetindo)
sum by (stage) (rate(jungleg_wager_retries_total[5m]))

# Proporção de transações repetidas por conflito
sum(rate(jungleg_wager_transaction_conflicts_total[10m]))
  / clamp_min(sum(rate(jungleg_wager_operations_total[10m])), 0.001)

# DLQ e divergências na última hora
increase(jungleg_wager_dlq_messages_total[1h])
increase(jungleg_wager_reconciliation_divergences_total[1h])
```

## O que fazer quando cada alerta dispara

### JunglegDown

O processo não responde ao scrape há 2 minutos.

1. `docker compose ps` — o serviço `api` está rodando?
2. `docker compose logs --tail=200 api` — o log diz por que saiu (config inválida,
   porta ocupada, panic)?
3. `curl http://localhost:8081/health/live` — liveness responde sem banco.
4. O `api` depende de `migrate` (migrations aplicadas). Se o `migrate` falhou, a
   API não sobe: `docker compose logs migrate`.

### ReadinessDown

O `/health/ready` responde 503. O **corpo** diz qual dependência caiu
(`{"postgres":"DOWN"}` etc.).

1. `curl -s http://localhost:8081/health/ready | jq` — leia `postgres`, `oidc`,
   `sqs` e `workers`.
2. `postgres` DOWN → o container do banco está de pé? `docker compose ps postgres`.
3. `oidc` DOWN → Keycloak de pé? `docker compose ps keycloak` e
   `curl http://localhost:8080/realms/jungleg/.well-known/openid-configuration`.
4. `sqs` DOWN → LocalStack e **as filas existem**:
   `docker exec jungleg-localstack awslocal sqs list-queues`.
5. `workers` DOWN → um laço configurado não está rodando. O log traz
   `worker_morreu_reiniciando` com o laço e o erro; `readiness` só fica DOWN
   enquanto ele está morto (o runner reinicia com backoff).

### OutboxAtrasada

Há evento confirmado no banco sem chegar ao destino há mais de 60s.

**O dado financeiro está seguro**: o evento está na tabela `outbox_events`, na
mesma transação do saldo. O que está parado é a publicação.

1. O publisher está ligado? `OUTBOX_ENABLED=true` no ambiente do container.
2. `docker compose logs api | grep outbox_atrasada` — o WARN traz quantos e a
   idade.
3. SQS acessível? `docker exec jungleg-localstack awslocal sqs list-queues`.
4. Há evento em quarentena? Veja o alerta seguinte. A consulta para achar:
   `SELECT event_id, event_type, attempts, last_error, available_at FROM outbox_events WHERE published_at IS NULL ORDER BY occurred_at LIMIT 20;`
5. Resolvido o motivo, o evento volta sozinho quando `available_at` vencer — não é
   preciso "reprocessar à mão" nem apagar nada.

### OutboxEmQuarentena

Um evento teve recusa permanente ou esgotou as tentativas (continua pendente, com
`last_error` e `available_at` no prazo longo).

1. Leia o `last_error` — recusa do destino (corpo grande, caractere inválido) ou
   tentativas esgotadas (SQS fora por tempo demais).
2. Corrigida a causa, o **desquarentenamento é operacional e explícito**:
   `UPDATE outbox_events SET available_at = now() WHERE event_id = '<id>';` — o
   publisher publica na próxima rodada, com o mesmo `eventId` (o consumidor
   deduplica).
3. Não marque como publicado: isso diria que o evento saiu quando ele não saiu.

### Divergencia

O saldo armazenado não casa com a soma do ledger. É incidente **financeiro**.

1. Reproduza a leitura: `POST /wallets/{walletId}/reconciliation` devolve
   `storedBalance`, `calculatedBalance`, `difference` e `checkedEntries`.
2. O log `reconciliacao_divergente` traz os valores em unidades mínimas e o
   `walletId`.
3. A reconciliação **não altera** o saldo (roda em transação somente-leitura). A
   correção é um lançamento novo no ledger (o ledger é append-only), decidida por
   quem entende do incidente — nunca um `UPDATE` em `wallets`.
4. Investigue a origem: houve escrita fora do caso de uso? alguma migração manual?

### DLQ

Uma mensagem foi recusada como permanente (corpo inválido, hash divergente) e
**não** foi aplicada — não houve movimentação.

1. Veja o corpo e o motivo:
   `docker exec jungleg-localstack awslocal sqs receive-message --queue-url http://localhost:4566/000000000000/wager-transactions-dlq.fifo --max-number-of-messages 10`
2. O log do consumidor tem `sqs_mensagem_para_dlq` com o `messageId` e o erro.
3. Se o corpo estiver corrompido, o produtor precisa reenviar; se for um caso de
   negócio que deveria ser rejeição, o produtor deve corrigir o payload. A
   mensagem da DLQ não volta sozinha.

### Conflitos

Mais de 10% das transações estão sendo repetidas por conflito.

Não é corrupção: é o **lock por carteira funcionando**. O custo é latência.

1. Confirme que o saldo está certo: a reconciliação responde `consistent: true`.
2. Olhe a distribuição por carteira: se um cliente concentra operações na mesma
   carteira, o p99 dessa carteira sobe (medido: 579 ms contra 166 ms em carteiras
   distintas).
3. Se a taxa subir com muitas carteiras diferentes, o problema é o banco
   (contenção geral): verifique `pg_stat_activity` e o pool.

## O que este diretório NÃO faz

- Não sobe observabilidade por conta própria (a decisão está acima);
- Não define `alertmanager`: as regras dizem o que dispara e o que fazer, e o
  roteamento é do ambiente de quem opera;
- Não cria métrica nova: a Fase 11 prova as 12 do `README.md` §12 e não muda o
  contrato delas (nome e labels são estáveis; renomear quebraria este painel).
