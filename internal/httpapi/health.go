package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

// HealthChecker é a porta que a camada HTTP usa para verificar dependências no
// readiness. Implementada em internal/infra (Postgres + SQS).
//
// A interface fica aqui (no consumidor), não no implementador: é o padrão
// idiomático em Go e mantém a camada HTTP testável com um fake simples.
type HealthChecker interface {
	// CheckReadiness devolve erro quando alguma dependência não está pronta.
	CheckReadiness(ctx context.Context) error
	// Name identifica a dependência para o diagnóstico.
	Name() string
}

// LivenessHandler responde se o PROCESSO está vivo.
//
// Importante: liveness não verifica dependências. Se verificasse, uma queda
// momentânea do Postgres faria o orquestrador reiniciar a aplicação em loop —
// matando um processo saudável por um problema externo.
func LivenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, r, http.StatusOK, map[string]any{
			"status": "UP",
		})
	}
}

// ReadyHandler responde se a aplicação está pronta para receber tráfego,
// verificando PostgreSQL e SQS.
func ReadyHandler(checkers []HealthChecker, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		dependencies := make(map[string]string, len(checkers))
		ready := true

		for _, checker := range checkers {
			if checker == nil {
				continue
			}
			if err := checker.CheckReadiness(ctx); err != nil {
				ready = false
				dependencies[checker.Name()] = "DOWN"
				logger.WarnContext(ctx, "readiness_dependency_down",
					slog.String("dependency", checker.Name()),
					slog.String("error", err.Error()),
				)
				continue
			}
			dependencies[checker.Name()] = "UP"
		}

		status := http.StatusOK
		state := "UP"
		if !ready {
			status = http.StatusServiceUnavailable
			state = "DOWN"
		}

		writeJSON(w, r, status, map[string]any{
			"status":       state,
			"dependencies": dependencies,
		})
	}
}
