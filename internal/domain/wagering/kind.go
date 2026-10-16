package wagering

// Kind é o tipo da operação.
type Kind string

const (
	// KindOpening é a abertura interna de carteira. Não vem de provedor.
	KindOpening Kind = "OPENING"
	// KindBet debita o valor da carteira.
	KindBet Kind = "BET"
	// KindWin credita o valor na carteira.
	KindWin Kind = "WIN"
	// KindLoss registra a perda da rodada sem movimentar dinheiro.
	KindLoss Kind = "LOSS"
	// KindRefund devolve integralmente o valor de uma BET processada.
	KindRefund Kind = "REFUND"
	// KindRollback desfaz integralmente uma BET, WIN ou REFUND processada.
	KindRollback Kind = "ROLLBACK"
)

// ParseKind converte a string vinda do JSON ou do banco no tipo tipado.
func ParseKind(s string) (Kind, error) {
	k := Kind(s)
	if err := k.Validate(); err != nil {
		return "", err
	}
	return k, nil
}

// Validate confere se o tipo é um dos previstos.
func (k Kind) Validate() error {
	switch k {
	case KindOpening, KindBet, KindWin, KindLoss, KindRefund, KindRollback:
		return nil
	}
	return invalid("tipo de operação desconhecido: %q", k)
}

// String devolve o tipo como aparece no JSON, no banco e nos eventos.
func (k Kind) String() string { return string(k) }

// IsExternal informa se a operação vem de um provedor. Só OPENING é interna.
func (k Kind) IsExternal() bool { return k != KindOpening }

// IsInternal é o inverso de IsExternal.
func (k Kind) IsInternal() bool { return k == KindOpening }

// RequiresReference informa se a operação precisa apontar para a transação
// original. Reversão sem referência não tem como achar o que desfazer nem
// provar que o valor bate.
func (k Kind) RequiresReference() bool { return k == KindRefund || k == KindRollback }

// AllowsZeroAmount informa se o tipo aceita valor zero.
//
// Só LOSS aceita: ele registra a perda da rodada sem movimentar dinheiro. Todos
// os outros exigem valor positivo.
func (k Kind) AllowsZeroAmount() bool { return k == KindLoss }

// MovesMoney informa se o tipo produz movimentação financeira.
func (k Kind) MovesMoney() bool { return k != KindLoss }

// FailureCodeZeroAmount é o failureCode de tipo que não aceita valor zero.
const FailureCodeZeroAmount = "INVALID_AMOUNT"

// ValidateAmount confere se o valor é compatível com o tipo da operação.
func (k Kind) ValidateAmount(amountMinor int64) error {
	if err := k.Validate(); err != nil {
		return err
	}
	if k.AllowsZeroAmount() {
		if amountMinor != 0 {
			return &Error{
				Code:    FailureCodeZeroAmount,
				Message: "LOSS exige valor exatamente 0.00",
			}
		}
		return nil
	}
	if amountMinor <= 0 {
		return &Error{
			Code:    FailureCodeZeroAmount,
			Message: k.String() + " exige valor maior que zero",
		}
	}
	return nil
}
