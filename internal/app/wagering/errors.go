// Package wagering é o caso de uso das operações financeiras: uma requisição do
// provedor vira UMA transação SQL que decide, movimenta dinheiro e enfileira
// eventos.
//
// # Por que ele existe
//
// O domínio (`internal/domain/...`) decide o que é permitido e não conhece
// banco. Os repositórios (`internal/infra/postgres`) sabem gravar e não conhecem
// regra de negócio. Este pacote é o único lugar onde os dois se encontram, e é
// ele que garante a parte que nenhum dos dois pode garantir sozinho: **tudo
// acontece na mesma transação** — idempotência, estado, saldo, ledger e outbox —
// ou nada acontece.
//
// # Por que ele depende de `app/ports` e não do banco
//
// O serviço recebe `ports.UnitOfWork` e trabalha sobre `ports.Repos`. Trocar
// PostgreSQL por outra coisa não obriga a mexer aqui, e o teste de unidade roda
// com um Unit of Work em memória sem subir container.
package wagering

import (
	domainerrors "github.com/FeliPoxZ/jungle_desafio/internal/domain/errors"
)

// Erros sentinela do caso de uso. Classifique com errors.Is, nunca comparando a
// mensagem: o Code é o que vira `failureCode` na borda HTTP.
var (
	// ErrIdempotencyConflict é a chave de idempotência reapresentada com
	// conteúdo diferente, ou a identidade (provedor, transação externa)
	// reapresentada sob outra chave. A resposta é conflito, e a operação
	// original permanece como está.
	ErrIdempotencyConflict = domainerrors.New(CodeIdempotencyConflict,
		"identidade da operação já usada com conteúdo diferente")

	// ErrWalletNotFound é a carteira inexistente. Não vira transação rejeitada:
	// a linha da transação tem chave estrangeira para a carteira, então não há
	// onde gravá-la.
	ErrWalletNotFound = domainerrors.New(CodeWalletNotFound, "carteira não encontrada")
)

// invalid marca requisição que o cliente corrige e reenvia com a mesma chave.
func invalid(code, format string, args ...any) error {
	return domainerrors.New(code, format, args...)
}
