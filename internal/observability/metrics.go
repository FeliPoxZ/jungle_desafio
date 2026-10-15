package observability

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// namespace e subsystem compõem o prefixo de todas as métricas
// (ex.: jungleg_wager_operations_total).
const (
	namespace = "jungleg"
	subsystem = "wager"
)

// Metrics agrupa as métricas exigidas pelo desafio. É injetado pelo Fx e usado
// pelos handlers, pelo serviço de aplicação e pelos workers.
//
// Decisão de projeto: labels só com valores de cardinalidade BAIXA e conhecida
// (kind, status, stage, result). Nunca usamos walletId/transactionId/providerId
// como label — isso explodiria a cardinalidade do Prometheus.
type Metrics struct {
	registry *prometheus.Registry

	operations        *prometheus.CounterVec
	operationDuration *prometheus.HistogramVec
	replays           prometheus.Counter
	inboxDuplicates   prometheus.Counter
	retries           *prometheus.CounterVec
	dlqMessages       prometheus.Counter
	txConflicts       prometheus.Counter
	outboxPublished   *prometheus.CounterVec
	outboxPendingAge  prometheus.Gauge
	outboxAttempts    prometheus.Histogram
	reconciliation    prometheus.Counter
	referenceResolved *prometheus.CounterVec
}

// NewMetrics cria e registra todas as métricas em um registry próprio.
//
// Usamos um registry dedicado (e não o global) para que os testes possam criar
// várias instâncias sem panic de "duplicate metrics collector registration".
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	m := &Metrics{
		registry: reg,
		operations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "operations_total",
			Help:      "Total de operações de aposta processadas, por tipo (kind) e status final.",
		}, []string{"kind", "status"}),

		operationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "operation_duration_seconds",
			Help:      "Latência de processamento de uma operação, por tipo e status.",
			// Buckets cobrindo de 1ms a ~2.5s: a maior parte das operações é
			// dominada por uma transação no Postgres.
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
		}, []string{"kind", "status"}),

		replays: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "idempotent_replays_total",
			Help:      "Total de reenvios idempotentes atendidos com o resultado persistido.",
		}),

		inboxDuplicates: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "inbox_duplicates_total",
			Help:      "Total de mensagens SQS reentregues detectadas pela inbox.",
		}),

		retries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "retries_total",
			Help:      "Total de retentativas por etapa (postgres, sqs_consume, sqs_publish, reference).",
		}, []string{"stage"}),

		dlqMessages: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "dlq_messages_total",
			Help:      "Total de mensagens encaminhadas/observadas na dead letter queue.",
		}),

		txConflicts: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "transaction_conflicts_total",
			Help:      "Total de conflitos de concorrência (lock wait, serialization, unique violation) tratados.",
		}),

		outboxPublished: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "outbox_publish_total",
			Help:      "Total de tentativas de publicação da outbox por resultado (success, retry, failed).",
		}, []string{"result"}),

		outboxPendingAge: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "outbox_pending_age_seconds",
			Help:      "Idade do evento pendente mais antigo na outbox (atraso da outbox).",
		}),

		outboxAttempts: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "outbox_publish_attempts",
			Help:      "Distribuição do número de tentativas até publicar um evento.",
			Buckets:   []float64{1, 2, 3, 5, 8, 13, 21},
		}),

		reconciliation: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "reconciliation_divergences_total",
			Help:      "Total de reconciliações que encontraram divergência entre saldo armazenado e ledger.",
		}),

		referenceResolved: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: subsystem,
			Name:      "reference_resolution_total",
			Help:      "Total de tentativas de resolução de referência pendente por resultado.",
		}, []string{"result"}),
	}

	reg.MustRegister(
		m.operations,
		m.operationDuration,
		m.replays,
		m.inboxDuplicates,
		m.retries,
		m.dlqMessages,
		m.txConflicts,
		m.outboxPublished,
		m.outboxPendingAge,
		m.outboxAttempts,
		m.reconciliation,
		m.referenceResolved,
	)

	return m
}

// Registry expõe o registry para testes que precisam inspecionar as métricas.
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

// Handler devolve o handler HTTP do endpoint /metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		Registry: m.registry,
	})
}

// --- Helpers de observação ---------------------------------------------------
//
// Os helpers evitam que o código de negócio conheça o formato dos labels.

// ObserveOperation registra uma operação concluída (contador + latência).
func (m *Metrics) ObserveOperation(kind, status string, d time.Duration) {
	if m == nil {
		return
	}
	m.operations.WithLabelValues(kind, status).Inc()
	m.operationDuration.WithLabelValues(kind, status).Observe(d.Seconds())
}

// IncReplay registra um reenvio idempotente atendido.
func (m *Metrics) IncReplay() {
	if m == nil {
		return
	}
	m.replays.Inc()
}

// IncInboxDuplicate registra uma mensagem SQS duplicada barrada pela inbox.
func (m *Metrics) IncInboxDuplicate() {
	if m == nil {
		return
	}
	m.inboxDuplicates.Inc()
}

// IncRetry registra uma retentativa na etapa informada.
func (m *Metrics) IncRetry(stage string) {
	if m == nil {
		return
	}
	m.retries.WithLabelValues(stage).Inc()
}

// IncDLQ registra uma mensagem que foi para a dead letter queue.
func (m *Metrics) IncDLQ() {
	if m == nil {
		return
	}
	m.dlqMessages.Inc()
}

// IncConflict registra um conflito de concorrência tratado.
func (m *Metrics) IncConflict() {
	if m == nil {
		return
	}
	m.txConflicts.Inc()
}

// ObserveOutboxPublish registra o resultado de uma publicação de evento.
func (m *Metrics) ObserveOutboxPublish(result string, attempts float64) {
	if m == nil {
		return
	}
	m.outboxPublished.WithLabelValues(result).Inc()
	if result == "success" && attempts > 0 {
		m.outboxAttempts.Observe(attempts)
	}
}

// SetOutboxPendingAge registra a idade do pendente mais antigo.
func (m *Metrics) SetOutboxPendingAge(d time.Duration) {
	if m == nil {
		return
	}
	m.outboxPendingAge.Set(d.Seconds())
}

// IncReconciliationDivergence registra uma divergência de reconciliação.
func (m *Metrics) IncReconciliationDivergence() {
	if m == nil {
		return
	}
	m.reconciliation.Inc()
}

// ObserveReferenceResolution registra o resultado de resolver uma referência.
func (m *Metrics) ObserveReferenceResolution(result string) {
	if m == nil {
		return
	}
	m.referenceResolved.WithLabelValues(result).Inc()
}

// StatusLabel converte um código HTTP em label estável para métricas.
func StatusLabel(status int) string {
	switch {
	case status >= 200 && status < 300:
		return "2xx_" + strconv.Itoa(status)
	case status >= 400 && status < 500:
		return "4xx_" + strconv.Itoa(status)
	case status >= 500:
		return "5xx_" + strconv.Itoa(status)
	default:
		return strconv.Itoa(status)
	}
}

// stageContextKey permite que middlewares registrem a etapa de retry no contexto
// (usado apenas para logs).
type stageContextKey struct{}

// WithStage carrega a etapa atual (http, sqs_consume, outbox_publish, reference).
func WithStage(ctx context.Context, stage string) context.Context {
	return context.WithValue(ctx, stageContextKey{}, stage)
}

// StageFrom extrai a etapa atual do contexto.
func StageFrom(ctx context.Context) string {
	if v, ok := ctx.Value(stageContextKey{}).(string); ok {
		return v
	}
	return ""
}
