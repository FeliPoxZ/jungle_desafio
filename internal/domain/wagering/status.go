package wagering

// Status é o estado da transação.
type Status string

const (
	// StatusPending é o estado inicial: registro aceito, processamento não concluído.
	StatusPending Status = "PENDING"
	// StatusPendingReference indica dependência de uma referência indisponível.
	StatusPendingReference Status = "PENDING_REFERENCE"
	// StatusProcessed indica conclusão com sucesso. Terminal.
	StatusProcessed Status = "PROCESSED"
	// StatusRejected indica recusa por regra de negócio. Terminal.
	StatusRejected Status = "REJECTED"
	// StatusFailed indica falha permanente de infraestrutura. Terminal.
	StatusFailed Status = "FAILED"
)

// ParseStatus converte a string vinda do banco no estado tipado.
func ParseStatus(s string) (Status, error) {
	st := Status(s)
	if err := st.Validate(); err != nil {
		return "", err
	}
	return st, nil
}

// Validate confere se o estado é um dos previstos.
func (s Status) Validate() error {
	switch s {
	case StatusPending, StatusPendingReference, StatusProcessed, StatusRejected, StatusFailed:
		return nil
	}
	return invalid("estado desconhecido: %q", s)
}

// String devolve o estado como aparece no JSON e no banco.
func (s Status) String() string { return string(s) }

// IsTerminal informa se o estado é final. Estado terminal não aceita transição:
// o resultado já foi decidido e a operação é consultada, não reprocessada.
func (s Status) IsTerminal() bool {
	return s == StatusProcessed || s == StatusRejected || s == StatusFailed
}

// transicoes é a máquina de estados. A chave é o estado atual e o valor são os
// estados alcançáveis a partir dele.
//
// PENDING_REFERENCE é o único estado não terminal que não é o inicial: ele
// existe para a operação que depende de uma referência ainda não recebida, e
// depois volta a ser resolvida por PROCESSED, REJECTED ou FAILED.
var transicoes = map[Status][]Status{
	StatusPending:          {StatusProcessed, StatusRejected, StatusFailed, StatusPendingReference},
	StatusPendingReference: {StatusProcessed, StatusRejected, StatusFailed},
	StatusProcessed:        {},
	StatusRejected:         {},
	StatusFailed:           {},
}

// CanTransition informa se a mudança de estado é permitida pela máquina.
func (s Status) CanTransition(to Status) bool {
	for _, permitido := range transicoes[s] {
		if permitido == to {
			return true
		}
	}
	return false
}

// ValidateTransition devolve o erro específico quando a mudança não é permitida:
// estado terminal tem erro próprio, porque a causa é diferente de uma transição
// apenas fora de ordem.
func (s Status) ValidateTransition(to Status) error {
	if s.IsTerminal() {
		return terminal(s, to)
	}
	if !s.CanTransition(to) {
		return invalidTransition("transição de " + s.String() + " para " + to.String() + " não é permitida")
	}
	return nil
}

// AllowedTransitions devolve os estados alcançáveis, para documentação e teste.
func (s Status) AllowedTransitions() []Status {
	permitidos := transicoes[s]
	copia := make([]Status, len(permitidos))
	copy(copia, permitidos)
	return copia
}
