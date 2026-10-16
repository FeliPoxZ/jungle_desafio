package auth

import (
	"log/slog"
	"net/http"
)

// Autenticador é a implementação concreta da porta `httpapi.Autenticador`.
//
// # Por que um tipo, e não as funções soltas
//
// As funções `Middleware`, `RequireRole` e `RequireProvider` já existem, mas a
// rota precisa das TRÊS a partir de um único valor injetado pelo Fx. Um tipo que
// carrega o verificador e o logger transforma isso em três métodos — e o Fx
// injeta uma coisa só.
//
// O acoplamento com `httpapi` é por ESTRUTURA (interface implícita em Go), não
// por import: este pacote não importa `httpapi`, e ainda assim o valor satisfaz
// a interface de lá. É o que permite a asserção de compilação no teste da
// composição, sem ciclo entre os pacotes.
type Autenticador struct {
	verificador *Verifier
	logger      *slog.Logger
}

// NewAutenticador monta o autenticador.
//
// Recebe o tipo CONCRETO (`*Verifier`), e não a interface `Verificador`: este é
// o ponto de composição, e o readiness precisa de um método (`CheckReadiness`)
// que não faz parte do contrato de validação de token. O middleware continua
// aceitando a interface, que é o que o teste dele substitui.
func NewAutenticador(v *Verifier, logger *slog.Logger) *Autenticador {
	return &Autenticador{verificador: v, logger: logger}
}

// Middleware valida o token e põe a identidade no contexto.
func (a *Autenticador) Middleware(next http.Handler) http.Handler {
	return Middleware(a.verificador, a.logger)(next)
}

// RequireRole exige uma das roles informadas.
func (a *Autenticador) RequireRole(roles ...string) func(http.Handler) http.Handler {
	return RequireRole(roles...)
}

// RequireProvider exige uma identidade de provedor.
func (a *Autenticador) RequireProvider() func(http.Handler) http.Handler {
	return RequireProvider()
}
