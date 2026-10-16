// Package errors define o erro de domínio compartilhado pelos pacotes de
// domínio do projeto.
//
// # Por que isso é um pacote, e não uma cópia por pacote
//
// Cada pacote de domínio precisava do mesmo tipo antes: `money`, `wallet` e
// `wagering` tinham três cópias quase idênticas de `Error`, `Error()` e `Is()`.
// Isso significa três lugares para corrigir a mesma coisa e três formas de
// `errors.Is` se comportar de maneira diferente.
//
// Com o tipo em um lugar só:
//
//   - `errors.Is(err, alvo)` funciona igual em todos os pacotes, porque a
//     comparação é sempre por `Code`;
//   - os erros de pacotes diferentes **não se confundem** por acidente: cada
//     pacote declara os próprios códigos, e o `Is` compara o texto do `Code`;
//   - o handler HTTP da Fase 7 tem uma função só para extrair o `Code`, que é o
//     que vira `failureCode` na resposta.
//
// # O que é estável e o que não é
//
// O `Code` é **contrato público**: ele vira `failureCode` no HTTP e o cliente
// decide o que fazer por ele. A `Message` é para humano ler no log e pode mudar
// a qualquer momento sem quebrar ninguém.
package errors

import (
	"errors"
	"fmt"
)

// Error é o erro de domínio.
//
// Carrega um `Code` estável e uma mensagem legível. A classificação é feita pelo
// `Code`, nunca pela mensagem.
type Error struct {
	Code    string
	Message string
}

// Error devolve a representação textual, com o Code na frente para que logs
// possam ser filtrados sem parsear a mensagem.
func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Is permite comparar erros por Code com errors.Is, mesmo quando o erro foi
// envelopado com fmt.Errorf("...: %w", err) alguns níveis acima.
//
// A comparação é por `Code`, e não por identidade do ponteiro: é isso que faz um
// erro continuar reconhecível depois de atravessar camadas.
func (e *Error) Is(target error) bool {
	var other *Error
	if ok := errors.As(target, &other); ok {
		return e.Code == other.Code
	}
	return false
}

// CodeOf devolve o Code de um erro de domínio, ou uma string vazia se o erro não
// for de domínio.
//
// É o ponto de tradução entre o domínio e a borda: o handler HTTP usa isto para
// preencher o `failureCode` sem conhecer os pacotes de domínio.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// New monta um erro de domínio com Code e mensagem formatada.
//
// Existe para os pacotes de domínio não repetirem a construção literal.
func New(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}
