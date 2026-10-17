// Package httpapi implementa a camada HTTP: roteamento, middlewares e handlers.
//
// Esta camada é deliberadamente "burra": ela traduz HTTP para chamadas de caso
// de uso e erros de domínio para status codes. Nenhuma regra de negócio mora
// aqui — inclusive as decisões sobre o que é 409, 422 ou 503 vêm de erros
// tipados do domínio/aplicação.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
)

// HeaderCorrelationID é o header usado para propagar correlação entre serviços.
const HeaderCorrelationID = "X-Correlation-Id"

// HeaderRequestID identifica unicamente a requisição HTTP.
const HeaderRequestID = "X-Request-Id"

// RequestIDMiddleware garante que toda requisição tenha requestId e
// correlationId no contexto e na resposta.
//
// Decisão: se o cliente enviar X-Correlation-Id, ele é PRESERVADO. Isso permite
// rastrear uma operação que atravessa vários serviços (o chamador inicia a
// correlação). O requestId é sempre novo, porque identifica esta requisição.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get(HeaderRequestID))
		if requestID == "" {
			requestID = uuid.NewString()
		}
		correlationID := strings.TrimSpace(r.Header.Get(HeaderCorrelationID))
		if correlationID == "" {
			correlationID = requestID
		}

		ctx := observability.WithRequestID(r.Context(), requestID)
		ctx = observability.WithCorrelationID(ctx, correlationID)

		w.Header().Set(HeaderRequestID, requestID)
		w.Header().Set(HeaderCorrelationID, correlationID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// statusRecorder captura o status e o tamanho da resposta para o log de acesso.
// O http.ResponseWriter padrão não expõe o status depois de escrito.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Unwrap permite que http.ResponseController (usado em timeouts e streaming)
// alcance o writer original.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// AccessLogMiddleware registra uma linha JSON por requisição.
//
// Cuidados exigidos pelo desafio: não logar credenciais nem payload financeiro
// completo. Aqui registramos apenas método, rota, status, duração e o
// idempotencyKey (que é um identificador, não um dado sensível).
func AccessLogMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}

			next.ServeHTTP(rec, r)

			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}

			logger.LogAttrs(r.Context(), slog.LevelInfo, "http_request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Int("bytes", rec.bytes),
				observability.LogDuration(time.Since(start)),
				slog.String("idempotencyKey", r.Header.Get(HeaderIdempotencyKey)),
			)
		})
	}
}

// TimeoutMiddleware aplica um prazo máximo a cada requisição, para que um
// cliente lento não consuma recursos indefinidamente.
func TimeoutMiddleware(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// MaxBodyMiddleware limita o tamanho do corpo aceito, evitando consumo de
// memória por payloads abusivos.
func MaxBodyMiddleware(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}
