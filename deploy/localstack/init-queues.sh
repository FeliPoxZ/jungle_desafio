# =============================================================================
# Provisionamento automático das filas SQS no LocalStack.
# Executado pelo serviço "localstack-init" do docker-compose e também pelo
# hook de ready do LocalStack (deploy/localstack/init-queues.sh).
#
# O script é IDEMPOTENTE: cria o que falta e não faz nada se já existir.
# =============================================================================

set -eu

ENDPOINT="${AWS_ENDPOINT_URL:-http://localhost:4566}"
export AWS_DEFAULT_REGION="${AWS_DEFAULT_REGION:-us-east-1}"
export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-test}"
export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-test}"
export AWS_PAGER=""

MAX_RECEIVE_COUNT="${SQS_MAX_RECEIVE_COUNT:-5}"

log() { printf '[init-queues] %s\n' "$*"; }

queue_url() {
  aws --endpoint-url "$ENDPOINT" sqs get-queue-url --queue-name "$1" --query 'QueueUrl' --output text 2>/dev/null
}

ensure_queue() {
  name="$1"
  shift
  if url="$(queue_url "$name")" && [ -n "$url" ] && [ "$url" != "None" ]; then
    log "fila já existe: $name"
  else
    log "criando fila: $name"
    aws --endpoint-url "$ENDPOINT" sqs create-queue --queue-name "$name" "$@" >/dev/null
  fi
  queue_url "$name"
}

# ---------------------------------------------------------------------------
# 1) DLQs primeiro: as filas principais precisam do ARN delas para o redrive.
# ---------------------------------------------------------------------------
TXN_DLQ_URL="$(ensure_queue "wager-transactions-dlq.fifo" \
  --attributes FifoQueue=true,ContentBasedDeduplication=false)"
TXN_DLQ_ARN="$(aws --endpoint-url "$ENDPOINT" sqs get-queue-attributes \
  --queue-url "$TXN_DLQ_URL" --attribute-names QueueArn --query 'Attributes.QueueArn' --output text)"

EVT_DLQ_URL="$(ensure_queue "wallet-events-dlq.fifo" \
  --attributes FifoQueue=true,ContentBasedDeduplication=false)"
EVT_DLQ_ARN="$(aws --endpoint-url "$ENDPOINT" sqs get-queue-attributes \
  --queue-url "$EVT_DLQ_URL" --attribute-names QueueArn --query 'Attributes.QueueArn' --output text)"

# ---------------------------------------------------------------------------
# 2) Fila de entrada (operações vindas dos provedores).
#    - VisibilityTimeout folgado: o processamento inclui transação no Postgres.
#    - Redrive para a DLQ após SQS_MAX_RECEIVE_COUNT recebimentos.
#    - ContentBasedDeduplication DESLIGADO de propósito: a deduplicação que vale
#      é a nossa (inbox + idempotência persistente). O MessageDeduplicationId é
#      sempre enviado explicitamente pelo produtor.
# ---------------------------------------------------------------------------
log "configurando wager-transactions.fifo"
TXN_URL="$(ensure_queue "wager-transactions.fifo" \
  --attributes FifoQueue=true,ContentBasedDeduplication=false,VisibilityTimeout=60,ReceiveMessageWaitTimeSeconds=20)"
aws --endpoint-url "$ENDPOINT" sqs set-queue-attributes --queue-url "$TXN_URL" --attributes "{
  \"RedrivePolicy\": \"{\\\"deadLetterTargetArn\\\":\\\"$TXN_DLQ_ARN\\\",\\\"maxReceiveCount\\\":\\\"$MAX_RECEIVE_COUNT\\\"}\",
  \"VisibilityTimeout\": \"60\",
  \"ReceiveMessageWaitTimeSeconds\": \"20\"
}" >/dev/null

# ---------------------------------------------------------------------------
# 3) Fila de saída (eventos publicados pela outbox).
# ---------------------------------------------------------------------------
log "configurando wallet-events.fifo"
EVT_URL="$(ensure_queue "wallet-events.fifo" \
  --attributes FifoQueue=true,ContentBasedDeduplication=false,VisibilityTimeout=30,ReceiveMessageWaitTimeSeconds=20)"
aws --endpoint-url "$ENDPOINT" sqs set-queue-attributes --queue-url "$EVT_URL" --attributes "{
  \"RedrivePolicy\": \"{\\\"deadLetterTargetArn\\\":\\\"$EVT_DLQ_ARN\\\",\\\"maxReceiveCount\\\":\\\"$MAX_RECEIVE_COUNT\\\"}\",
  \"VisibilityTimeout\": \"30\",
  \"ReceiveMessageWaitTimeSeconds\": \"20\"
}" >/dev/null

# ---------------------------------------------------------------------------
# 4) Resumo (evidência de que o ambiente está pronto).
# ---------------------------------------------------------------------------
log "filas provisionadas:"
aws --endpoint-url "$ENDPOINT" sqs list-queues --query 'QueueUrls' --output text | tr '\t' '\n' | sed 's/^/  - /'
