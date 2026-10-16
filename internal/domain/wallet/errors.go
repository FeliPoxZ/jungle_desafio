// Package wallet define a carteira do jogador e o ledger append-only que
// registra cada movimentação.
//
// # Invariantes
//
//   - o saldo nunca é negativo (README §6.2)
//   - toda movimentação acontece na moeda da carteira
//   - a versão começa em 1 e só muda quando o saldo muda
//   - cada mudança de saldo tem o lançamento correspondente no ledger
//   - o ledger é append-only: uma entrada nunca é alterada depois de criada
//
// # Dinheiro
//
// O saldo é um money.Money, que guarda int64 em unidades mínimas. Toda
// aritmética passa pelo money.Sub/Add, que já tratam overflow e moeda
// incompatível. Não há aritmética de saldo escrita à mão neste pacote.
package wallet

import (
	"errors"
	"fmt"
)

// Códigos estáveis de falha. Viram failureCode no contrato HTTP.
const (
	CodeInvalidWallet          = "INVALID_WALLET"
	CodeInsufficientFunds      = "INSUFFICIENT_FUNDS"
	CodeWalletCurrencyMismatch = "WALLET_CURRENCY_MISMATCH"
	CodeInvalidEntry           = "INVALID_LEDGER_ENTRY"
)

// Error é o erro de domínio do pacote wallet.
type Error struct {
	Code    string
	Message string
}

// Erros sentinela. Classifique com errors.Is, nunca comparando a mensagem.
var (
	ErrInvalidWallet          = &Error{Code: CodeInvalidWallet, Message: "carteira inválida"}
	ErrInsufficientFunds      = &Error{Code: CodeInsufficientFunds, Message: "saldo insuficiente"}
	ErrWalletCurrencyMismatch = &Error{Code: CodeWalletCurrencyMismatch, Message: "moeda incompatível com a da carteira"}
	ErrInvalidEntry           = &Error{Code: CodeInvalidEntry, Message: "lançamento de ledger inválido"}
)

// Error devolve a representação textual, com o Code na frente para que logs
// possam ser filtrados sem parsear a mensagem.
func (e *Error) Error() string {
	return fmt.Sprintf("wallet: %s: %s", e.Code, e.Message)
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

// invalid monta um erro de carteira inválida com mensagem formatada.
func invalid(format string, args ...any) error {
	return &Error{Code: CodeInvalidWallet, Message: fmt.Sprintf(format, args...)}
}

// insufficient monta o erro de saldo insuficiente, sempre com os dois valores
// envolvidos: é o que torna a rejeição auditável (README §7).
func insufficient(want, have int64) error {
	return &Error{
		Code:    CodeInsufficientFunds,
		Message: fmt.Sprintf("débito de %d excede o saldo de %d", want, have),
	}
}

// invalidEntry monta um erro de ledger inválido com mensagem formatada.
func invalidEntry(format string, args ...any) error {
	return &Error{Code: CodeInvalidEntry, Message: fmt.Sprintf(format, args...)}
}
