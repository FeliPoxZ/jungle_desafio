package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// HeaderIdempotencyKey é o header obrigatório no envio de operações.
const HeaderIdempotencyKey = "Idempotency-Key"

// Códigos de erro do CONTRATO HTTP (distintos dos failureCode de negócio, que
// vêm da tabela de domínio). O cliente usa estes para decidir o que fazer.
const (
	CodeInvalidRequest     = "INVALID_REQUEST"
	CodeUnauthorized       = "UNAUTHORIZED"
	CodeForbidden          = "FORBIDDEN"
	CodeNotFound           = "NOT_FOUND"
	CodeConflict           = "IDEMPOTENCY_CONFLICT"
	CodeUnprocessable      = "BUSINESS_REJECTED"
	CodePending            = "PENDING"
	CodeServiceUnavailable = "SERVICE_UNAVAILABLE"
	CodeInternal           = "INTERNAL_ERROR"
)

// ErrorBody é o corpo padrão de erro. Todos os erros da API têm este formato,
// o que torna as situações distinguíveis pelo contrato (exigência do item 9).
type ErrorBody struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	FailureCode string `json:"failureCode,omitempty"`
}

type errorResponse struct {
	Error ErrorBody `json:"error"`
}

// APIError é o erro de transporte: carrega o status HTTP, o código do contrato,
// a mensagem pública e, quando aplicável, o failureCode de negócio.
//
// Ao contrário de erros de domínio, este tipo existe apenas na borda HTTP.
type APIError struct {
	Status      int
	Code        string
	Message     string
	FailureCode string
	// Internal é o erro original, usado só para log. NUNCA é serializado.
	Internal error
	// Headers extras (ex.: Retry-After).
	Headers map[string]string
}

func (e *APIError) Error() string {
	if e.Internal != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Internal)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *APIError) Unwrap() error { return e.Internal }

// Novos construtores de erro de borda, um por situação do contrato.

func ErrBadRequest(message string, internal error) *APIError {
	return &APIError{Status: http.StatusBadRequest, Code: CodeInvalidRequest, Message: message, Internal: internal}
}

func ErrFailureBadRequest(failureCode, message string) *APIError {
	return &APIError{Status: http.StatusBadRequest, Code: CodeInvalidRequest, Message: message, FailureCode: failureCode}
}

func ErrUnauthorized(message string, internal error) *APIError {
	return &APIError{Status: http.StatusUnauthorized, Code: CodeUnauthorized, Message: message, Internal: internal}
}

func ErrForbidden(message string) *APIError {
	return &APIError{Status: http.StatusForbidden, Code: CodeForbidden, Message: message}
}

func ErrNotFound(message string) *APIError {
	return &APIError{Status: http.StatusNotFound, Code: CodeNotFound, Message: message}
}

func ErrConflict(message string) *APIError {
	return &APIError{Status: http.StatusConflict, Code: CodeConflict, Message: message}
}

// ErrUnprocessable representa rejeição definitiva por regra de negócio (422),
// sempre com um failureCode estável e documentado.
func ErrUnprocessable(failureCode, message string) *APIError {
	return &APIError{Status: http.StatusUnprocessableEntity, Code: CodeUnprocessable, Message: message, FailureCode: failureCode}
}

// ErrUnavailable representa indisponibilidade transitória (503). O cliente deve
// tentar de novo — por isso o Retry-After.
func ErrUnavailable(message string, retryAfterSeconds int, internal error) *APIError {
	return &APIError{
		Status:   http.StatusServiceUnavailable,
		Code:     CodeServiceUnavailable,
		Message:  message,
		Internal: internal,
		Headers:  map[string]string{"Retry-After": fmt.Sprintf("%d", retryAfterSeconds)},
	}
}

func ErrInternal(internal error) *APIError {
	return &APIError{
		Status:   http.StatusInternalServerError,
		Code:     CodeInternal,
		Message:  "erro interno inesperado",
		Internal: internal,
	}
}

// writeJSON serializa a resposta. Erros de serialização acontecem depois do
// header já enviado, então só podem ser logados.
func writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(body); err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
		// A resposta já começou; não há como mudar o status. Registramos apenas.
		slog.Default().DebugContext(r.Context(), "falha ao serializar resposta JSON",
			slog.String("error", err.Error()))
	}
}

// WriteError escreve o erro usando o mapeamento do contrato.
func WriteError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		apiErr = ErrInternal(err)
	}

	for k, v := range apiErr.Headers {
		w.Header().Set(k, v)
	}

	// Loga somente o que interessa para diagnóstico. Mensagens de erro de driver
	// não vão para o cliente (evita vazar detalhe interno).
	level := slog.LevelWarn
	if apiErr.Status >= http.StatusInternalServerError {
		level = slog.LevelError
	}
	logger.LogAttrs(r.Context(), level, "http_error",
		slog.String("path", r.URL.Path),
		slog.Int("status", apiErr.Status),
		slog.String("code", apiErr.Code),
		slog.String("failureCode", apiErr.FailureCode),
	)

	writeJSON(w, r, apiErr.Status, errorResponse{Error: ErrorBody{
		Code:        apiErr.Code,
		Message:     apiErr.Message,
		FailureCode: apiErr.FailureCode,
	}})
}

// decodeJSON lê o corpo com validação estrita:
//   - rejeita campos desconhecidos (erro de digitação do cliente não passa );
//   - rejeita corpo vazio;
//   - rejeita múltiplos objetos JSON no mesmo corpo;
//   - limita o tamanho (definido pelo MaxBodyMiddleware).
//
// Validação estrita é escolha consciente: em contrato financeiro, aceitar em
// silêncio um campo que o cliente achava que estava sendo aplicado é pior do
// que recusar a requisição.
func decodeJSON(r *http.Request, dst any) error {
	contentType := r.Header.Get("Content-Type")
	if contentType != "" && !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		return fmt.Errorf("Content-Type deve ser application/json")
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	dec.UseNumber()

	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("corpo da requisição é obrigatório")
		}
		return fmt.Errorf("JSON inválido: %w", err)
	}

	// Garante que não há um segundo documento JSON no corpo.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("o corpo deve conter um único objeto JSON")
	}
	return nil
}
