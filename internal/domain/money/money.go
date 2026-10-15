// Package money define o value object monetário do domínio.
//
// # Representação
//
// O valor é guardado em UNIDADES MÍNIMAS da moeda (centavos), como int64.
// Exemplo: R$ 25,00 é armazenado como 2500.
//
//	Representação interna : 2500      (int64)
//	Escala                : 2         (sempre 2 casas)
//	Contrato externo      : "25.00"   (string decimal)
//
// # Por que não float64
//
// float64 é binário e não representa decimais exatamente: 0.1 + 0.2 não é 0.3.
// Em um ledger financeiro, esse resíduo se acumula e a soma dos lançamentos
// deixa de fechar com o saldo.

package money

import (
	"errors"
	"fmt"
)

// Currency é um código de moeda ISO 4217.
//
// É um tipo próprio (e não uma string solta) de propósito: assim o compilador
// impede trocar a moeda por qualquer texto em uma chamada de função.
type Currency string

const BRL Currency = "BRL"

const (
	CodeInvalidCurrency = "INVALID_CURRENCY"
)

// Error é o erro de domínio do pacote money.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("money: %s: %s", e.Code, e.Message)
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

// Erros sentinela do pacote. Use errors.Is(err, money.ErrInvalidCurrency).
var (
	ErrInvalidCurrency = &Error{Code: CodeInvalidCurrency, Message: "código de moeda inválido"}
)
