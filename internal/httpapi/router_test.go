package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	"github.com/FeliPoxZ/jungle_desafio/internal/httpapi"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
)

// testRouter monta o roteador real da aplicação com checkers de health falsos.
func testRouter(t *testing.T, checkers ...httpapi.HealthChecker) *httpapi.Routes {
	t.Helper()

	cfg := &config.Config{
		Env: "test",
		HTTP: config.HTTPConfig{
			Addr:           ":0",
			RequestTimeout: 5 * time.Second,
			MaxBodyBytes:   1024,
		},
	}
	return httpapi.NewRouter(cfg, observability.DiscardLogger(), observability.NewMetrics(), checkers)
}

type fakeChecker struct {
	name string
	err  error
}

func (f fakeChecker) Name() string { return f.name }

func (f fakeChecker) CheckReadiness(_ context.Context) error { return f.err }

// --- Health ------------------------------------------------------------------

func TestLivenessAlwaysOK(t *testing.T) {
	routes := testRouter(t)

	rec := httptest.NewRecorder()
	routes.Base.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/live", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quer 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("resposta não é JSON: %v", err)
	}
	if body["status"] != "UP" {
		t.Errorf("status = %v, quer UP", body["status"])
	}
}

// Readiness com todas as dependências de pé: 200 e cada dependência reportada.
func TestReadinessReadyWhenAllDependenciesUp(t *testing.T) {
	routes := testRouter(t,
		fakeChecker{name: "postgres"},
		fakeChecker{name: "sqs"},
	)

	rec := httptest.NewRecorder()
	routes.Base.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quer 200 (body=%s)", rec.Code, rec.Body.String())
	}

	var body struct {
		Status       string            `json:"status"`
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("resposta não é JSON: %v", err)
	}
	if body.Status != "UP" {
		t.Errorf("status = %q, quer UP", body.Status)
	}
	if body.Dependencies["postgres"] != "UP" || body.Dependencies["sqs"] != "UP" {
		t.Errorf("dependências = %v, quer ambas UP", body.Dependencies)
	}
}

// Uma dependência fora do ar deve derrubar a readiness (503) e dizer QUAL caiu.
func TestReadinessReturns503WhenDependencyDown(t *testing.T) {
	routes := testRouter(t,
		fakeChecker{name: "postgres"},
		fakeChecker{name: "sqs", err: errors.New("connection refused")},
	)

	rec := httptest.NewRecorder()
	routes.Base.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, quer 503", rec.Code)
	}

	var body struct {
		Status       string            `json:"status"`
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("resposta não é JSON: %v", err)
	}
	if body.Status != "DOWN" {
		t.Errorf("status = %q, quer DOWN", body.Status)
	}
	if body.Dependencies["sqs"] != "DOWN" {
		t.Errorf("sqs = %q, quer DOWN", body.Dependencies["sqs"])
	}
	if body.Dependencies["postgres"] != "UP" {
		t.Errorf("postgres = %q, quer UP (uma falha não pode mascarar o resto)", body.Dependencies["postgres"])
	}
}

// --- Métricas ----------------------------------------------------------------

func TestMetricsEndpointIsPublic(t *testing.T) {
	routes := testRouter(t)

	rec := httptest.NewRecorder()
	routes.Base.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quer 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "go_goroutines") {
		t.Error("a resposta do /metrics não contém métricas do runtime")
	}
}

// --- Correlação --------------------------------------------------------------

// O correlationId do cliente é PRESERVADO: é o que permite rastrear a operação
// atravessando serviços.
func TestRequestIDMiddlewarePreservesClientCorrelationID(t *testing.T) {
	routes := testRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	req.Header.Set(httpapi.HeaderCorrelationID, "corr-do-cliente")

	rec := httptest.NewRecorder()
	routes.Base.ServeHTTP(rec, req)

	if got := rec.Header().Get(httpapi.HeaderCorrelationID); got != "corr-do-cliente" {
		t.Errorf("correlationId da resposta = %q, quer %q", got, "corr-do-cliente")
	}
	// requestId é sempre novo, mesmo quando o correlationId veio do cliente.
	if rec.Header().Get(httpapi.HeaderRequestID) == "" {
		t.Error("requestId da resposta está vazio")
	}
}

// Sem correlationId do cliente, ele é derivado do requestId (sempre presente).
func TestRequestIDMiddlewareGeneratesCorrelationID(t *testing.T) {
	routes := testRouter(t)

	rec := httptest.NewRecorder()
	routes.Base.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/live", nil))

	requestID := rec.Header().Get(httpapi.HeaderRequestID)
	correlationID := rec.Header().Get(httpapi.HeaderCorrelationID)

	if requestID == "" || correlationID == "" {
		t.Fatalf("headers ausentes: requestId=%q correlationId=%q", requestID, correlationID)
	}
	if requestID != correlationID {
		t.Errorf("correlationId deveria ser igual ao requestId quando o cliente não envia nada: %q != %q", correlationID, requestID)
	}
}

// --- Timeout -----------------------------------------------------------------

// O middleware de timeout precisa efetivamente cancelar o contexto do handler.
func TestTimeoutCancelsHandlerContext(t *testing.T) {
	cfg := &config.Config{
		Env:  "test",
		HTTP: config.HTTPConfig{RequestTimeout: 30 * time.Millisecond, MaxBodyBytes: 1024},
	}
	routes := httpapi.NewRouter(cfg, observability.DiscardLogger(), observability.NewMetrics(), nil)

	cancelled := make(chan struct{})
	routes.Base.Get("/slow", func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-time.After(3 * time.Second):
		}
	})

	rec := httptest.NewRecorder()
	routes.Base.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/slow", nil))

	select {
	case <-cancelled:
		// Comportamento esperado: o contexto foi cancelado pelo prazo.
	case <-time.After(2 * time.Second):
		t.Fatal("o contexto do handler não foi cancelado dentro do prazo")
	}
}

// O chi também sabe responder 504 quando o handler estoura o prazo.
func TestTimeoutMiddlewareReturns504(t *testing.T) {
	cfg := &config.Config{
		Env:  "test",
		HTTP: config.HTTPConfig{RequestTimeout: 30 * time.Millisecond, MaxBodyBytes: 1024},
	}
	routes := httpapi.NewRouter(cfg, observability.DiscardLogger(), observability.NewMetrics(), nil)

	// chi/middleware.Timeout responde 504 e cancela o contexto.
	slow := chi.NewRouter()
	slow.Use(chimw.Timeout(30 * time.Millisecond))
	slow.Get("/", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
			w.WriteHeader(http.StatusOK)
		}
	})
	routes.Base.Mount("/slow", slow)

	rec := httptest.NewRecorder()
	routes.Base.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/slow", nil))

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, quer 504", rec.Code)
	}
}

// --- Panic -------------------------------------------------------------------

// Panic de bug vira 500 e o processo continua atendendo. Rejeição de negócio
// NUNCA usa panic (isso é coberto pelos erros tipados).
func TestRecovererMiddlewareConvertsPanicTo500(t *testing.T) {
	routes := testRouter(t)
	routes.Base.Get("/boom", func(http.ResponseWriter, *http.Request) {
		panic("falha proposital de teste")
	})

	rec := httptest.NewRecorder()
	routes.Base.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, quer 500", rec.Code)
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("resposta de erro não é JSON: %v", err)
	}
	if body.Error.Code != httpapi.CodeInternal {
		t.Errorf("code = %q, quer %q", body.Error.Code, httpapi.CodeInternal)
	}
	// O detalhe interno do panic NÃO pode ir para o cliente.
	if strings.Contains(rec.Body.String(), "falha proposital") {
		t.Error("a resposta vazou o detalhe do panic")
	}
}

// --- Erros -------------------------------------------------------------------

func TestWriteErrorMapsStatusAndBody(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantFailure string
	}{
		{
			name:       "requisição inválida",
			err:        httpapi.ErrBadRequest("campo X inválido", nil),
			wantStatus: http.StatusBadRequest,
			wantCode:   httpapi.CodeInvalidRequest,
		},
		{
			name:       "não autenticado",
			err:        httpapi.ErrUnauthorized("token ausente", nil),
			wantStatus: http.StatusUnauthorized,
			wantCode:   httpapi.CodeUnauthorized,
		},
		{
			name:       "proibido",
			err:        httpapi.ErrForbidden("provedor diferente do token"),
			wantStatus: http.StatusForbidden,
			wantCode:   httpapi.CodeForbidden,
		},
		{
			name:       "não encontrado",
			err:        httpapi.ErrNotFound("carteira não encontrada"),
			wantStatus: http.StatusNotFound,
			wantCode:   httpapi.CodeNotFound,
		},
		{
			name:       "conflito de idempotência",
			err:        httpapi.ErrConflict("chave reutilizada com conteúdo diferente"),
			wantStatus: http.StatusConflict,
			wantCode:   httpapi.CodeConflict,
		},
		{
			name:        "rejeição de negócio",
			err:         httpapi.ErrUnprocessable("INSUFFICIENT_FUNDS", "saldo insuficiente"),
			wantStatus:  http.StatusUnprocessableEntity,
			wantCode:    httpapi.CodeUnprocessable,
			wantFailure: "INSUFFICIENT_FUNDS",
		},
		{
			name:       "indisponibilidade transitória",
			err:        httpapi.ErrUnavailable("postgres indisponível", 2, errors.New("connection refused")),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   httpapi.CodeServiceUnavailable,
		},
		{
			name:       "erro não mapeado vira 500 sem vazar detalhe",
			err:        errors.New("detalhe interno do driver"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   httpapi.CodeInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/qualquer", nil)

			httpapi.WriteError(rec, req, observability.DiscardLogger(), tt.err)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, quer %d", rec.Code, tt.wantStatus)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, quer application/json", ct)
			}

			var body struct {
				Error struct {
					Code        string `json:"code"`
					Message     string `json:"message"`
					FailureCode string `json:"failureCode"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("corpo não é JSON: %v (body=%q)", err, rec.Body.String())
			}
			if body.Error.Code != tt.wantCode {
				t.Errorf("code = %q, quer %q", body.Error.Code, tt.wantCode)
			}
			if body.Error.FailureCode != tt.wantFailure {
				t.Errorf("failureCode = %q, quer %q", body.Error.FailureCode, tt.wantFailure)
			}
			if body.Error.Message == "" {
				t.Error("mensagem pública vazia")
			}
			// Nenhum detalhe interno pode aparecer no corpo.
			if strings.Contains(rec.Body.String(), "detalhe interno do driver") {
				t.Error("resposta vazou o erro interno")
			}
		})
	}
}

// 503 precisa dizer ao cliente QUANDO tentar de novo.
func TestUnavailableSetsRetryAfter(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)

	httpapi.WriteError(rec, req, observability.DiscardLogger(), httpapi.ErrUnavailable("indisponível", 3, nil))

	if got := rec.Header().Get("Retry-After"); got != "3" {
		t.Errorf("Retry-After = %q, quer 3", got)
	}
}
