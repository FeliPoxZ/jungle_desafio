// Package wagering define a transação de aposta: o que o provedor pediu, em que
// estado está e por que falhou.
//
// # Origem da operação
//
// `OPENING` é operação interna de abertura de carteira e nunca chega por HTTP
// ou SQS. As outras cinco são externas e carregam os metadados do provedor:
// identificador externo, chave de idempotência e hash do payload.
//
// # Máquina de estados
//
// Nasce em `PENDING` e termina em `PROCESSED`, `REJECTED` ou `FAILED`. A
// transição para `PENDING_REFERENCE` registra que a operação depende de uma
// referência que ainda não chegou. Estado terminal não aceita nova transição.
//
// # Papel neste pacote
//
// O domínio decide se a operação é representável e permitida. Ele NÃO movimenta
// dinheiro: quem aplica débito e crédito é a camada de aplicação (Fase 6), com
// a carteira da Fase 2 e o ledger da Fase 2.
package wagering

import (
	"errors"
	"fmt"
)

// Códigos estáveis de falha. Viram failureCode no contrato HTTP.
const (
	CodeInvalidTransaction = "INVALID_TRANSACTION"
	CodeInvalidTransition  = "INVALID_TRANSITION"
	CodeTerminalState      = "TERMINAL_STATE"
	CodeMissingReference   = "REFERENCE_REQUIRED"
	CodeInternalKind       = "OPERATION_NOT_ALLOWED"
	CodeReversed           = "ALREADY_REVERSED"
	CodeProcessingFailed   = "PROCESSING_FAILED"
)

// Error é o erro de domínio do pacote wagering.
type Error struct {
	Code    string
	Message string
}

// Erros sentinela. Classifique com errors.Is, nunca comparando a mensagem.
var (
	ErrInvalidTransaction = &Error{Code: CodeInvalidTransaction, Message: "transação inválida"}
	ErrInvalidTransition  = &Error{Code: CodeInvalidTransition, Message: "transição de estado inválida"}
	ErrTerminalState      = &Error{Code: CodeTerminalState, Message: "estado terminal não aceita transição"}
	ErrMissingReference   = &Error{Code: CodeMissingReference, Message: "referência à transação original é obrigatória"}
	ErrInternalKind       = &Error{Code: CodeInternalKind, Message: "tipo de operação interna não é aceito de fora"}
	ErrAlreadyReversed    = &Error{Code: CodeReversed, Message: "referência já revertida para este tipo"}
	ErrProcessingFailed   = &Error{Code: CodeProcessingFailed, Message: "falha permanente de processamento"}
)

// Error devolve a representação textual, com o Code na frente para que logs
// possam ser filtrados sem parsear a mensagem.
func (e *Error) Error() string {
	return fmt.Sprintf("wagering: %s: %s", e.Code, e.Message)
}

// Is permite comparar erros por Code com errors.Is, mesmo quando o erro foi
// envelopado com fmt.Errorf("...: %w", err) alguns níveis acima.
func (e *Error) Is(target error) bool {
	var other *Error
	if ok := errors.As(target, &other); ok {
		return e.Code == other.Code
	}
	return false
}

// invalid monta um erro de transação inválida com mensagem formatada.
func invalid(format string, args ...any) error {
	return &Error{Code: CodeInvalidTransaction, Message: fmt.Sprintf(format, args...)}
}

// invalidTransition monta um erro de transição inválida.
func invalidTransition(message string) error {
	return &Error{Code: CodeInvalidTransition, Message: message}
}

// terminal monta o erro de tentativa de transição a partir de estado terminal.
func terminal(from, to Status) error {
	return &Error{
		Code:    CodeTerminalState,
		Message: fmt.Sprintf("estado %s é terminal; %s não é permitido", from, to),
	}
}
