// Package ports declara os contratos que a camada de aplicação consome.
//
// # Por que este pacote existe
//
// Sem ele, o serviço de aplicação (`internal/app`) teria que importar
// `internal/infra/postgres` só para nomear o tipo dos repositórios. Isso inverteria
// a direção da dependência: a regra de negócio passaria a depender do driver do
// banco, e trocar PostgreSQL por outra coisa obrigaria a mexer no serviço.
//
// Com as interfaces aqui:
//
//	app  →  ports  ←  infra/postgres
//
// Quem implementa aponta para o contrato; quem usa também. A aplicação não
// conhece o banco, e o banco não conhece a aplicação.
//
// # Por que as interfaces são pequenas
//
// Cada interface tem só os métodos que a aplicação realmente chama. Uma interface
// gorda ("Repository com 40 métodos") é impossível de dublar em teste e mente
// sobre o que o serviço precisa.
//
// # De onde vêm as regras de erro
//
// Os métodos podem devolver `postgres.ErrConflict`, `ErrNotFound` e `ErrTransient`
// — mas a aplicação **não** importa o pacote de infra para isso: o contrato é
// "devolve erro que satisfaz `IsConflict`/`IsNotFound`/`IsRetryable`", e essas
// funções vivem aqui. Ver `errors.go` deste pacote.
package ports

import (
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ledger"
)

// Cursor é a posição de paginação: instante + identificador.
//
// Os dois juntos porque `created_at` pode repetir (duas operações no mesmo
// microssegundo), e o desempate por `id` é o que torna a ordem estável entre
// páginas.
//
// # Por que o cursor mora no contrato, e não na infraestrutura
//
// Quem monta o cursor é quem devolve a página (o repositório), mas quem o
// interpreta na entrada é a borda HTTP, e quem o repassa é a aplicação. Se ele
// ficasse na infraestrutura, os três precisariam importar o pacote do banco.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

// CursorDeEntries monta o cursor a partir do último lançamento da página.
//
// Devolve nil quando a página está vazia: sem elemento não há posição para
// continuar, e o chamador distingue "acabou" de "continue daqui".
func CursorDeEntries(entries []*ledger.Entry) *Cursor {
	if len(entries) == 0 {
		return nil
	}
	ultimo := entries[len(entries)-1]
	return &Cursor{CreatedAt: ultimo.CreatedAt(), ID: ultimo.ID()}
}
