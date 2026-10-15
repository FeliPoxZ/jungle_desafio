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

// ParseCurrency converte e valida uma string em Currency.
//
// Aceita apenas o código alfabético ISO 4217: exatamente três letras
// maiúsculas ASCII (A-Z). Exemplos válidos: "BRL", "USD", "EUR".
//
// Rejeitados com ErrInvalidCurrency: vazio, comprimento diferente de 3,
// minúsculas ("brl"), código numérico ("986"), dígitos, símbolos e
// espaços.
//
// Não normaliza (trim, uppercase): a borda do sistema faz isso de forma
// explícita. Aqui a política é rejeitar em vez de adivinhar.
func ParseCurrency(s string) (Currency, error) {
	c := Currency(s)
	if err := c.Validate(); err != nil {
		return "", fmt.Errorf("moeda %q: %w", s, err)
	}
	return c, nil
}

// Validate confere se o Currency é um código ISO 4217 alfabético válido.
//
// Existe separado de ParseCurrency para revalidar valores que chegam já
// tipados, como os lidos do banco na reidratação.
func (c Currency) Validate() error {
	// O código ISO 4217 alfabético é ASCII por definição da norma:
	// comparar byte a byte é correto e não há risco com UTF-8.
	if len(c) != 3 {
		return ErrInvalidCurrency
	}
	for i := 0; i < len(c); i++ {
		if c[i] < 'A' || c[i] > 'Z' {
			return ErrInvalidCurrency
		}
	}
	return nil
}

// String devolve o código da moeda ("BRL").
func (c Currency) String() string { return string(c) }
