package observability

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/config"
)

// NewLogger cria o logger estruturado da aplicação (JSON em produção, texto
// legível em ambiente local) e já embute os identificadores de correlação que
// estiverem no contexto.
//
// Por que slog: é a biblioteca padrão (Go 1.21+), não adiciona dependência e
// permite trocar o handler sem tocar no código de negócio.
func NewLogger(cfg *config.Config) *slog.Logger {
	level, err := cfg.SlogLevel()
	if err != nil {
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if cfg.IsLocal() {
		// Em desenvolvimento, texto é mais fácil de ler no terminal.
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	logger := slog.New(&contextHandler{next: handler})
	logger = logger.With(slog.String("service", "jungleg"), slog.String("env", cfg.Env))
	return logger
}

// DiscardLogger devolve um logger que não escreve nada. Usado em testes que não
// se importam com a saída.
func DiscardLogger() *slog.Logger {
	return slog.New(&contextHandler{next: slog.NewJSONHandler(io.Discard, nil)})
}

// NewContextHandlerForTest expõe o handler que injeta os identificadores de
// correlação, para que os testes possam compor o logger com uma saída própria
// (ex.: strings.Builder) e inspecionar o JSON gerado.
//
// Mantido público de propósito: testar log por observação de comportamento é
// melhor do que deixar o handler inacessível e não testá-lo.
func NewContextHandlerForTest(next slog.Handler) slog.Handler {
	return &contextHandler{next: next}
}

// contextHandler injeta automaticamente os identificadores de correlação do
// contexto em TODA linha de log. Isso evita o erro clássico de esquecer de
// adicionar "correlationId" em algum ponto e perder a rastreabilidade.
type contextHandler struct {
	next slog.Handler
}

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *contextHandler) Handle(ctx context.Context, record slog.Record) error {
	if id := CorrelationIDFrom(ctx); id != "" && !recordHasAttr(record, "correlationId") {
		record.AddAttrs(slog.String("correlationId", id))
	}
	if id := RequestIDFrom(ctx); id != "" && !recordHasAttr(record, "requestId") {
		record.AddAttrs(slog.String("requestId", id))
	}
	return h.next.Handle(ctx, record)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{next: h.next.WithAttrs(attrs)}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{next: h.next.WithGroup(name)}
}

func recordHasAttr(record slog.Record, key string) bool {
	found := false
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == key {
			found = true
			return false
		}
		return true
	})
	return found
}

// LogDuration devolve um atributo com a duração em milissegundos, formato mais
// amigável para consulta em ferramentas de log do que time.Duration bruto.
func LogDuration(d time.Duration) slog.Attr {
	return slog.Float64("durationMs", float64(d.Microseconds())/1000.0)
}

// Redact mascara valores sensíveis mantendo o suficiente para correlação manual.
// Usado para nunca logar um segredo inteiro.
func Redact(value string) string {
	if value == "" {
		return ""
	}
	r := []rune(value)
	if len(r) <= 4 {
		return strings.Repeat("*", len(r))
	}
	return string(r[:2]) + strings.Repeat("*", len(r)-4) + string(r[len(r)-2:])
}
