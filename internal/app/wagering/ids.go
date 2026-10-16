package wagering

import (
	"strings"

	"github.com/google/uuid"
)

// ehUUID informa se a string pode ser identificador de uma coluna `UUID`.
//
// # Por que isto existe
//
// `wallets.id`, `wager_transactions.id` e `wallet_ledger_entries.wallet_id` são
// colunas `UUID`. Passar um texto qualquer para uma consulta nessas colunas faz o
// PostgreSQL recusar com `22P02 invalid input syntax for type uuid` — que a
// classificação do driver trata como falha PERMANENTE, ou seja, **500**.
//
// Um identificador malformado nunca é erro de servidor: ele não pode existir.
// Sem esta guarda, `GET /wallets/abc` responderia 500 em vez de "não encontrado",
// e uma mensagem do SQS com `walletId` inválido derrubaria o processamento com
// erro de infraestrutura em vez de rejeição de negócio.
//
// A checagem é feita na APLICAÇÃO, e não no adaptador do banco: lá o `22P02`
// também pode vir de uma inserção, e transformá-lo em "não encontrado" mascararia
// defeito de gravação.
func ehUUID(s string) bool {
	_, err := uuid.Parse(strings.TrimSpace(s))
	return err == nil
}
