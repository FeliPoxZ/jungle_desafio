package httpapi

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
)

// RecovererMiddleware converte panic em HTTP 500.
//
// Exigência do desafio: "panic não deve representar rejeições de negócio". Ou
// seja, rejeição de negócio é erro tipado (422/409), nunca panic. Mas um panic
// de bug (nil pointer, índice inválido) não pode derrubar o processo inteiro e
// deixar as outras requisições sem atendimento — então ele é capturado,
// registrado com stack trace no log e traduzido para 500.
func RecovererMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				// http.ErrAbortHandler é usado pelo próprio net/http para abortar
				// a resposta de propósito; não é um bug e deve propagar.
				if rec == http.ErrAbortHandler {
					panic(rec)
				}

				logger.LogAttrs(r.Context(), slog.LevelError, "panic_recovered",
					slog.Any("panic", rec),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("stack", string(debug.Stack())),
					slog.String("correlationId", observability.CorrelationIDFrom(r.Context())),
				)

				// Só escreve se nada foi enviado ainda.
				WriteError(w, r, logger, ErrInternal(nil))
			}()

			next.ServeHTTP(w, r)
		})
	}
}
