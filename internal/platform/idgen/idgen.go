// Package idgen gera os identificadores do sistema.
//
// # Por que UUID v7, e não v4
//
// UUID v7 tem o instante nos bits mais significativos, então os valores gerados
// em sequência ficam **ordenados no tempo**. Isso importa em dois lugares:
//
//   - índice primário: inserir em ordem crescente de chave mantém as páginas do
//     B-tree densas. Com v4 (aleatório) cada insert toca uma página diferente,
//     o índice incha e a escrita fica mais cara;
//   - depuração: olhar dois IDs e saber qual é mais novo ajuda a reconstruir a
//     ordem dos fatos sem consultar o banco.
//
// A alternativa seria `BIGSERIAL`, que é mais rápido ainda, mas exige ida ao
// banco para saber o identificador — e a aplicação precisa do ID **antes** do
// insert, porque o evento da outbox e o lançamento do ledger já o referenciam.
//
// # Por que passa por uma interface
//
// O serviço recebe um `Generator`, e não chama o pacote direto. Assim os testes
// injetam um gerador determinístico (id-1, id-2, ...) e as asserções ficam
// legíveis, em vez de comparar UUIDs aleatórios.
package idgen

import "github.com/google/uuid"

// Generator produz identificadores novos.
//
// A interface tem um método só de propósito: quem depende dela não ganha
// dependência de tempo, de rede nem de estado global.
type Generator interface {
	// NewID devolve um identificador novo, único.
	NewID() string
}

// UUIDGenerator gera UUID v7.
type UUIDGenerator struct{}

// New devolve o gerador padrão (UUID v7).
func New() Generator { return UUIDGenerator{} }

// NewID devolve um UUID v7 novo.
//
// O erro do uuid.NewV7 é ignorado de propósito: ele só falha se a fonte de
// aleatoriedade do sistema falhar, e nesse caso o processo tem problema maior do
// que um identificador. Devolver erro aqui espalharia `if err != nil` por todo
// caminho de criação sem nada útil para fazer a respeito.
func (UUIDGenerator) NewID() string {
	id, err := uuid.NewV7()
	if err != nil {
		// Fallback: UUID v4 continua sendo único, só perde a ordenação temporal.
		return uuid.NewString()
	}
	return id.String()
}
