// Package observability concentra logging estruturado, correlação de requisições
// e métricas Prometheus.
//
// Regras do desafio que este pacote atende:
//   - logs em JSON com correlationId, messageId, transactionId, walletId, providerId;
//   - nunca registrar credenciais, dados sensíveis ou payload financeiro completo;
//   - métricas de resultado por status, duplicatas, retries, DLQ, conflitos de
//     concorrência, atraso da outbox, latência e divergências de reconciliação.
package observability

import "context"

type contextKey string

const (
	// correlationIDKey é a chave usada para carregar o correlationId no contexto.
	correlationIDKey contextKey = "correlationId"
	// requestIDKey é a chave do identificador da requisição HTTP.
	requestIDKey contextKey = "requestId"
)

// WithCorrelationID devolve um contexto que carrega o correlationId. Esse valor
// nasce no middleware HTTP (ou no consumidor SQS) e acompanha TODA a execução,
// inclusive o que o worker de outbox grava no evento.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, correlationIDKey, id)
}

// CorrelationIDFrom extrai o correlationId do contexto. Devolve "" quando ausente.
func CorrelationIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(correlationIDKey).(string); ok {
		return v
	}
	return ""
}

// WithRequestID carrega o identificador único da requisição HTTP.
func WithRequestID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFrom extrai o requestId do contexto.
func RequestIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}
