// Package ids define os identificadores compartilhados entre os agregados do
// domínio.
//
// # Por que um pacote só para identificadores
//
// `WalletID` é usado pelo agregado da carteira, pelo lançamento do ledger e pela
// transação de aposta. Sem um lugar comum, um desses pacotes teria que importar
// o outro só para pegar o tipo — e isso cria ciclo:
//
//	wallet  → ledger (Debit devolve o lançamento)
//	ledger  → wallet (precisa de WalletID)   ← ciclo
//
// Com os identificadores aqui, a dependência vira uma seta só, apontando para
// baixo:
//
//	ids  ←  ledger  ←  wallet  ←  wagering
//
// # Por que tipo próprio e não string
//
// Um `type WalletID string` impede trocar a carteira por qualquer texto em uma
// chamada de função — o compilador recusa. É o mesmo motivo do `money.Currency`.
package ids

// WalletID identifica uma carteira. O valor é gerado fora do domínio (UUID v7).
type WalletID string

// PlayerID identifica o jogador dono da carteira.
type PlayerID string

// TransactionID identifica uma transação de aposta.
//
// Fica aqui porque DOIS pacotes precisam dele com o mesmo significado: o
// `wagering`, que é o dono do conceito, e o `ledger`, que registra qual operação
// causou cada lançamento. Se cada um tivesse o seu, o serviço passaria o dia
// convertendo `wagering.TransactionID` em `ledger.TransactionID` — e uma conversão
// esquecida viraria string vazia, que é exatamente o bug silencioso que o
// `NewEntry` recusa com "transactionID é obrigatório".
type TransactionID string

// String devolve o valor como string, para logs e para o banco.
func (id WalletID) String() string { return string(id) }

// String devolve o valor como string, para logs e para o banco.
func (id PlayerID) String() string { return string(id) }

// String devolve o valor como string, para logs e para o banco.
func (id TransactionID) String() string { return string(id) }
