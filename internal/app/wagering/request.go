package wagering

import (
	"strings"

	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
)

// Request é uma operação externa, sem os detalhes da porta de entrada.
//
// HTTP e SQS produzem ESTE tipo: o caso de uso é um só, e a diferença entre as
// duas entradas (header `Idempotency-Key` de um lado, `data.idempotencyKey` do
// outro) fica na borda. Sem isso, a garantia de idempotência valeria para uma
// porta e não para a outra.
type Request struct {
	ProviderID             string
	ExternalTransactionID  string
	IdempotencyKey         string
	PlayerID               ids.PlayerID
	WalletID               ids.WalletID
	RoundID                string
	GameID                 string
	Kind                   wagerdomain.Kind
	Amount                 money.Money
	ReferenceExternalTxnID string

	// Rastreabilidade: vão para os eventos e para o log. Não entram no hash de
	// idempotência — são metadados de transporte.
	CorrelationID string
	CausationID   string
}

// Result é o desfecho da operação, pronto para a borda traduzir em resposta.
//
// Rejeição de negócio é desfecho, não erro: a operação existe, foi decidida e
// está gravada. Por isso `FailureCode` mora aqui, e não em um `error`.
type Result struct {
	TransactionID wagerdomain.TransactionID
	Status        wagerdomain.Status

	// Balance é o saldo da carteira depois da operação. É nil quando não há
	// saldo a informar: rejeição e pendência de referência.
	//
	// É ponteiro, e não `money.Money`, porque zero é um saldo legítimo (carteira
	// zerada) e "sem saldo" precisa ser distinguível dele.
	Balance *money.Money

	// FailureCode é o código estável da recusa. Vazio quando não houve recusa.
	FailureCode string

	// IdempotentReplay marca a resposta que devolve o resultado já persistido,
	// sem movimentar nada de novo.
	IdempotentReplay bool
}

// validate recusa o que não chega a ser uma operação.
//
// Nada é gravado neste caminho, e a MESMA chave de idempotência pode ser
// reenviada depois de corrigida: é isso que distingue entrada corrigível de
// resultado definitivo (A8 do PLANO).
func (r Request) validate() error {
	if strings.TrimSpace(r.IdempotencyKey) == "" {
		return invalid(CodeIdempotencyKeyRequired, "operação externa exige chave de idempotência")
	}
	if strings.TrimSpace(r.ProviderID) == "" {
		return invalid(CodeInvalidRequest, "providerID é obrigatório")
	}
	if strings.TrimSpace(r.ExternalTransactionID) == "" {
		return invalid(CodeInvalidRequest, "externalTransactionID é obrigatório")
	}
	if strings.TrimSpace(string(r.PlayerID)) == "" {
		return invalid(CodeInvalidRequest, "playerID é obrigatório")
	}
	if strings.TrimSpace(string(r.WalletID)) == "" {
		return invalid(CodeInvalidRequest, "walletID é obrigatório")
	}
	if strings.TrimSpace(r.GameID) == "" {
		return invalid(CodeInvalidRequest, "gameID é obrigatório")
	}
	if strings.TrimSpace(r.RoundID) == "" {
		return invalid(CodeInvalidRequest, "roundID é obrigatório")
	}
	if err := r.Kind.Validate(); err != nil {
		return err
	}
	if r.Kind.IsInternal() {
		// OPENING só nasce da abertura interna de carteira: aceitá-lo de fora
		// deixaria o provedor creditar carteira a partir de uma requisição.
		return wagerdomain.ErrInternalKind
	}
	if err := r.Amount.Currency().Validate(); err != nil {
		return err
	}
	if err := r.Kind.ValidateAmount(r.Amount.Minor()); err != nil {
		return err
	}
	if r.Kind.RequiresReference() {
		if strings.TrimSpace(r.ReferenceExternalTxnID) == "" {
			return wagerdomain.ErrMissingReference
		}
		if strings.TrimSpace(r.ReferenceExternalTxnID) == strings.TrimSpace(r.ExternalTransactionID) {
			// Autorreferência: a operação apontaria para si mesma. A referência
			// nunca resolveria, e a linha ficaria pendente até o TTL do worker.
			return invalid(CodeInvalidRequest, "a operação não pode referenciar a si mesma")
		}
	}
	return nil
}

// normalizado devolve a requisição com os campos de texto sem espaço nas pontas.
//
// É normalização ANTES do hash, e é isso que faz " txn-123" e "txn-123" serem a
// mesma operação, em vez de duas. Sem ela, o mesmo pedido com um espaço a mais
// geraria hash diferente e a reentrega seria tratada como conflito.
//
// Os identificadores (`WalletID`, `PlayerID`) NÃO são mexidos: são opacos, e
// alterá-los seria inventar outro identificador.
func (r Request) normalizado() Request {
	r.ProviderID = strings.TrimSpace(r.ProviderID)
	r.ExternalTransactionID = strings.TrimSpace(r.ExternalTransactionID)
	r.IdempotencyKey = strings.TrimSpace(r.IdempotencyKey)
	r.RoundID = strings.TrimSpace(r.RoundID)
	r.GameID = strings.TrimSpace(r.GameID)
	r.ReferenceExternalTxnID = strings.TrimSpace(r.ReferenceExternalTxnID)
	return r
}
