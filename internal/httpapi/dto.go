package httpapi

import (
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ledger"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
)

// MoneyDTO é o contrato externo do dinheiro: `{"amount":"25.00","currency":"BRL"}`.
//
// O valor é STRING, e não número JSON, por dois motivos que se somam:
//
//   - `"25.00"` preserva as duas casas e o significado; `25.00` como número é
//     interpretado por quem lê (o JavaScript do cliente, por exemplo) como
//     `float64`, e é exatamente aí que a precisão se perde — o README §5.1
//     proíbe que dinheiro passe por float em QUALQUER etapa, inclusive na
//     serialização;
//   - o parsing do domínio (`money.Money`) já recebe string decimal e recusa
//     notação científica, escala excedente e valores não exatos. Reusar esse
//     caminho na borda garante a MESMA régua na entrada e na saída.
//
// `Money` já implementa `MarshalJSON`/`UnmarshalJSON` nesse formato, então este
// tipo é um ALIAS do contrato — não uma cópia com regras próprias que poderiam
// divergir das do domínio.
type MoneyDTO = money.Money

// walletResponse é a resposta da abertura e da leitura de carteira (README §9).
type walletResponse struct {
	ID       string   `json:"id"`
	PlayerID string   `json:"playerId"`
	Balance  MoneyDTO `json:"balance"`
	Version  int64    `json:"version"`
	// IdempotentReplay aparece só no reenvio da abertura: diz que nada foi
	// criado desta vez. Na leitura simples é omitido (o campo não se aplica).
	IdempotentReplay bool `json:"idempotentReplay,omitempty"`
}

// openWalletRequest é o corpo do POST /wallets.
//
// Os nomes dos campos são os do README §9 (`playerId`, `initialBalance`).
// `initialBalance` é PONTEIRO para distinguir "ausente" de "zero": zero é um
// saldo inicial aceito, e um campo ausente é erro de entrada. Com valor
// não-ponteiro, `{"playerId":"x"}` e `{"playerId":"x","initialBalance":{"amount":"0.00",...}}`
// seriam indistinguíveis, e o primeiro passaria como se fosse o segundo.
type openWalletRequest struct {
	PlayerID       string    `json:"playerId"`
	InitialBalance *MoneyDTO `json:"initialBalance"`
}

// ledgerEntryResponse é um lançamento do ledger como o cliente o vê.
type ledgerEntryResponse struct {
	ID            string   `json:"id"`
	TransactionID string   `json:"transactionId"`
	Direction     string   `json:"direction"`
	Amount        MoneyDTO `json:"amount"`
	BalanceBefore MoneyDTO `json:"balanceBefore"`
	BalanceAfter  MoneyDTO `json:"balanceAfter"`
	CreatedAt     string   `json:"createdAt"`
}

// ledgerPageResponse é a página do ledger: os lançamentos e o cursor da próxima.
//
// `nextCursor` é `null` (e não string vazia) quando acabou: string vazia é um
// VALOR, e o cliente precisa distinguir "não há mais páginas" de "o cursor veio
// vazio por engano".
type ledgerPageResponse struct {
	Entries    []ledgerEntryResponse `json:"entries"`
	NextCursor *string               `json:"nextCursor"`
}

// reconciliationResponse é a resposta do POST /wallets/{walletId}/reconciliation.
//
// `difference` é `stored − calculated` e pode ser NEGATIVA: o saldo gravado menor
// que o reconstruído. O sinal é informação, não ruído — ele diz em que direção a
// inconsistência está.
type reconciliationResponse struct {
	WalletID          string   `json:"walletId"`
	StoredBalance     MoneyDTO `json:"storedBalance"`
	CalculatedBalance MoneyDTO `json:"calculatedBalance"`
	Difference        MoneyDTO `json:"difference"`
	Consistent        bool     `json:"consistent"`
	CheckedEntries    int      `json:"checkedEntries"`
}

// wagerResponse é a resposta do POST /wagering/transactions.
//
// `balance` e `failureCode` são `any` de propósito, para saírem como `null` nos
// desfechos em que o dado NÃO EXISTE — e é isso que o contrato exige:
//
//	PROCESSED          → balance preenchido, failureCode null
//	REJECTED           → balance null, failureCode preenchido
//	PENDING_REFERENCE  → ambos null
//
// Omitir o campo (`omitempty`) seria pior: o cliente não distinguiria "não se
// aplica" de "o servidor esqueceu de mandar".
type wagerResponse struct {
	TransactionID    string `json:"transactionId"`
	Status           string `json:"status"`
	Balance          any    `json:"balance"`
	IdempotentReplay bool   `json:"idempotentReplay"`
	FailureCode      any    `json:"failureCode"`
}

// rfc3339Milissegundos é o formato de instante do README §11
// ("2026-09-08T12:00:00.000Z"): UTC, com milissegundos.
const rfc3339Milissegundos = "2006-01-02T15:04:05.000Z07:00"

// novoLancamento monta a resposta de um lançamento do ledger.
func novoLancamento(e *ledger.Entry) ledgerEntryResponse {
	return ledgerEntryResponse{
		ID:            e.ID(),
		TransactionID: string(e.TransactionID()),
		Direction:     e.Direction().String(),
		Amount:        e.Amount(),
		BalanceBefore: e.BalanceBefore(),
		BalanceAfter:  e.BalanceAfter(),
		CreatedAt:     e.CreatedAt().UTC().Format(rfc3339Milissegundos),
	}
}
