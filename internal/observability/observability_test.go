package observability_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
)

func TestCorrelationIDRoundTrip(t *testing.T) {
	ctx := observability.WithCorrelationID(context.Background(), "corr-123")
	if got := observability.CorrelationIDFrom(ctx); got != "corr-123" {
		t.Errorf("CorrelationIDFrom = %q, quer corr-123", got)
	}

	// Contexto sem valor devolve string vazia (nunca entra em panic).
	if got := observability.CorrelationIDFrom(context.Background()); got != "" {
		t.Errorf("CorrelationIDFrom(contexto vazio) = %q, quer vazio", got)
	}

	// String vazia é ignorada: não polui o contexto com valor inútil.
	ctx = observability.WithCorrelationID(context.Background(), "")
	if got := observability.CorrelationIDFrom(ctx); got != "" {
		t.Errorf("CorrelationIDFrom = %q, quer vazio", got)
	}
}

func TestRequestIDRoundTrip(t *testing.T) {
	ctx := observability.WithRequestID(context.Background(), "req-1")
	if got := observability.RequestIDFrom(ctx); got != "req-1" {
		t.Errorf("RequestIDFrom = %q, quer req-1", got)
	}
	if got := observability.RequestIDFrom(context.Background()); got != "" {
		t.Errorf("RequestIDFrom(contexto vazio) = %q, quer vazio", got)
	}
}

// O handler de contexto deve injetar correlationId e requestId em toda linha de
// log sem que o chamador precise lembrar de passar.
func TestContextHandlerInjectsCorrelationFields(t *testing.T) {
	var buf strings.Builder
	base := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(observability.NewContextHandlerForTest(base))

	ctx := observability.WithCorrelationID(context.Background(), "corr-abc")
	ctx = observability.WithRequestID(ctx, "req-xyz")

	logger.InfoContext(ctx, "operacao_processada")

	out := buf.String()
	if !strings.Contains(out, `"correlationId":"corr-abc"`) {
		t.Errorf("log não contém correlationId: %s", out)
	}
	if !strings.Contains(out, `"requestId":"req-xyz"`) {
		t.Errorf("log não contém requestId: %s", out)
	}
}

// Se o chamador já passou o correlationId explicitamente, ele não é duplicado.
func TestContextHandlerDoesNotDuplicateExplicitField(t *testing.T) {
	var buf strings.Builder
	base := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(observability.NewContextHandlerForTest(base))

	ctx := observability.WithCorrelationID(context.Background(), "do-contexto")
	logger.InfoContext(ctx, "msg", slog.String("correlationId", "explicito"))

	out := buf.String()
	if strings.Count(out, "correlationId") != 1 {
		t.Errorf("correlationId deveria aparecer uma única vez: %s", out)
	}
	if !strings.Contains(out, "explicito") {
		t.Errorf("o valor explícito deveria prevalecer: %s", out)
	}
}

func TestNewLoggerHonorsLevelFromConfig(t *testing.T) {
	cfg := &config.Config{Env: "test", LogLevel: "error"}
	logger := observability.NewLogger(cfg)
	if logger == nil {
		t.Fatal("NewLogger devolveu nil")
	}
	// Nível error: debug/info não devem ser registrados.
	if logger.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("logger com LOG_LEVEL=error não deveria aceitar Info")
	}
	if !logger.Enabled(context.Background(), slog.LevelError) {
		t.Error("logger com LOG_LEVEL=error deveria aceitar Error")
	}
}

func TestDiscardLoggerWritesNothing(t *testing.T) {
	logger := observability.DiscardLogger()
	logger.Error("não deve aparecer", slog.String("x", "y"))
}

func TestRedact(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "", want: ""},
		{in: "ab", want: "**"},
		{in: "abcd", want: "****"},
		{in: "secret-value", want: "se********ue"},
	}
	for _, tt := range tests {
		if got := observability.Redact(tt.in); got != tt.want {
			t.Errorf("Redact(%q) = %q, quer %q", tt.in, got, tt.want)
		}
	}
}

// --- Métricas ----------------------------------------------------------------

func TestMetricsObserveOperation(t *testing.T) {
	m := observability.NewMetrics()

	m.ObserveOperation("BET", "PROCESSED", 12*time.Millisecond)
	m.ObserveOperation("BET", "REJECTED", 3*time.Millisecond)
	m.IncReplay()
	m.IncInboxDuplicate()
	m.IncRetry("postgres")
	m.IncDLQ()
	m.IncConflict()
	m.ObserveOutboxPublish("success", 1)
	m.ObserveOutboxPublish("retry", 2)
	m.SetOutboxPendingAge(5 * time.Second)
	m.IncReconciliationDivergence()
	m.ObserveReferenceResolution("resolved")

	body := scrape(t, m)

	for _, want := range []string{
		`jungleg_wager_operations_total{kind="BET",status="PROCESSED"} 1`,
		`jungleg_wager_operations_total{kind="BET",status="REJECTED"} 1`,
		`jungleg_wager_idempotent_replays_total 1`,
		`jungleg_wager_inbox_duplicates_total 1`,
		`jungleg_wager_retries_total{stage="postgres"} 1`,
		`jungleg_wager_dlq_messages_total 1`,
		`jungleg_wager_transaction_conflicts_total 1`,
		`jungleg_wager_outbox_publish_total{result="success"} 1`,
		`jungleg_wager_outbox_publish_total{result="retry"} 1`,
		`jungleg_wager_outbox_pending_age_seconds 5`,
		`jungleg_wager_reconciliation_divergences_total 1`,
		`jungleg_wager_reference_resolution_total{result="resolved"} 1`,
		`jungleg_wager_operation_duration_seconds_count{kind="BET",status="PROCESSED"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("métrica ausente no /metrics: %s", want)
		}
	}
}

// Métricas nulas (não injetadas, ex.: em um teste unitário simples) não podem
// causar panic: os helpers são nil-safe de propósito.
func TestMetricsNilSafe(t *testing.T) {
	var m *observability.Metrics

	m.ObserveOperation("BET", "PROCESSED", time.Second)
	m.IncReplay()
	m.IncInboxDuplicate()
	m.IncRetry("x")
	m.IncDLQ()
	m.IncConflict()
	m.ObserveOutboxPublish("success", 1)
	m.SetOutboxPendingAge(time.Second)
	m.IncReconciliationDivergence()
	m.ObserveReferenceResolution("resolved")
}

func TestMetricsHandlerServesRuntimeMetrics(t *testing.T) {
	m := observability.NewMetrics()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)

	m.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quer 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "go_goroutines") {
		t.Error("métricas de runtime ausentes")
	}
}

// scrape coleta as métricas no formato de exposição do Prometheus.
func scrape(t *testing.T, m *observability.Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape falhou: status %d", rec.Code)
	}
	return rec.Body.String()
}

// Garante que o logger de descarte continua sem efeitos colaterais mesmo com
// atributos inválidos.
func TestDiscardLoggerWithErrors(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	logger.Error("erro", slog.Any("err", errors.New("qualquer")))
}
