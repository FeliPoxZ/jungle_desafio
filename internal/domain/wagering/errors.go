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
	"fmt"

	domainerrors "github.com/FeliPoxZ/jungle_desafio/internal/domain/errors"
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

// Error é o erro de domínio, compartilhado com os outros pacotes de domínio.
//
// É ALIAS, e não redefinição, como já era em `money`, `wallet` e `ledger`. Com
// um tipo próprio, `errors.As` não atravessava daqui até o tipo compartilhado:
// `domainerrors.CodeOf(erroDoWagering)` devolvia string VAZIA, e o `failureCode`
// da resposta sairia em branco para toda recusa deste pacote — sem erro de
// compilação e sem teste vermelho. O defeito estava escondido porque cada pacote
// testava com a sua própria cópia do extrator de Code.
type Error = domainerrors.Error

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
