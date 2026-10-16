package wallet

import (
	"fmt"
	"strings"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
)

// WalletID identifica uma carteira. O valor é gerado fora do domínio (uuid v7),
// então aqui só se verifica que veio preenchido.
type WalletID string

// PlayerID identifica o jogador dono da carteira.
type PlayerID string

// Wallet é a raiz do agregado financeiro: identidade, dono, moeda, saldo,
// versão e instantes. Os campos são não exportados para que o saldo só mude
// pelos métodos do próprio agregado.
type Wallet struct {
	id        WalletID
	playerID  PlayerID
	currency  money.Currency
	balance   money.Money
	version   int64
	createdAt time.Time
	updatedAt time.Time
}

// NewWallet cria a carteira com saldo inicial e versão 1.
//
// Saldo inicial zero é aceito (README §7): a carteira existe, mas não gera
// lançamento de abertura.
func NewWallet(id WalletID, playerID PlayerID, initial money.Money, now time.Time) (*Wallet, error) {
	if strings.TrimSpace(string(id)) == "" {
		return nil, invalid("id da carteira é obrigatório")
	}
	if strings.TrimSpace(string(playerID)) == "" {
		return nil, invalid("playerID é obrigatório")
	}
	if err := initial.Currency().Validate(); err != nil {
		return nil, invalid("moeda do saldo inicial inválida: %v", err)
	}
	if initial.IsNegative() {
		return nil, invalid("saldo inicial não pode ser negativo")
	}
	if now.IsZero() {
		return nil, invalid("instante de criação é obrigatório")
	}

	return &Wallet{
		id:        id,
		playerID:  playerID,
		currency:  initial.Currency(),
		balance:   initial,
		version:   1,
		createdAt: now,
		updatedAt: now,
	}, nil
}

// Rehydrate é o construtor de leitura: reconstrói a carteira do que está
// persistido, sem reaplicar movimentações, sem emitir eventos e sem mexer na
// versão ou nos instantes.
//
// A validação dos dados lidos é responsabilidade do repositório, apoiada nas
// constraints da tabela.
func Rehydrate(
	id WalletID,
	playerID PlayerID,
	balance money.Money,
	version int64,
	createdAt, updatedAt time.Time,
) *Wallet {
	return &Wallet{
		id:        id,
		playerID:  playerID,
		currency:  balance.Currency(),
		balance:   balance,
		version:   version,
		createdAt: createdAt,
		updatedAt: updatedAt,
	}
}

// ID devolve o identificador da carteira.
func (w *Wallet) ID() WalletID { return w.id }

// PlayerID devolve o dono da carteira.
func (w *Wallet) PlayerID() PlayerID { return w.playerID }

// Currency devolve a moeda da carteira.
func (w *Wallet) Currency() money.Currency { return w.currency }

// Balance devolve o saldo atual.
func (w *Wallet) Balance() money.Money { return w.balance }

// Version devolve a versão atual, usada no controle de concorrência otimista.
func (w *Wallet) Version() int64 { return w.version }

// CreatedAt devolve o instante de criação.
func (w *Wallet) CreatedAt() time.Time { return w.createdAt }

// UpdatedAt devolve o instante da última mudança de saldo.
func (w *Wallet) UpdatedAt() time.Time { return w.updatedAt }

// Debit desconta amount do saldo, exigindo a mesma moeda e preservando
// saldo maior ou igual a zero (README §6.2).
//
// Devolve o lançamento de ledger correspondente, que deve ser persistido na
// mesma transação da carteira.
func (w *Wallet) Debit(amount money.Money, now time.Time) (Entry, error) {
	entry, err := w.apply(DirectionDebit, amount, now)
	if err != nil {
		return Entry{}, err
	}
	return entry, nil
}

// Credit soma amount ao saldo, exigindo a mesma moeda.
func (w *Wallet) Credit(amount money.Money, now time.Time) (Entry, error) {
	return w.apply(DirectionCredit, amount, now)
}

// apply é o único ponto que muda o saldo. Valida moeda e valor, calcula o
// saldo novo pela aritmética do money, recusa débito que ficaria negativo e
// só então altera o estado e a versão.
//
// Movimentação de valor zero devolve entrada vazia: não muda saldo, não muda
// versão e não gera lançamento no ledger (mesma regra do LOSS, README §7).
func (w *Wallet) apply(direction Direction, amount money.Money, now time.Time) (Entry, error) {
	if err := direction.Validate(); err != nil {
		return Entry{}, err
	}
	if amount.Currency() != w.currency {
		return Entry{}, &Error{
			Code: CodeWalletCurrencyMismatch,
			Message: fmt.Sprintf("movimentação em %q em carteira de %q",
				amount.Currency(), w.currency),
		}
	}
	if amount.IsNegative() {
		return Entry{}, invalid("movimentação negativa: o sinal é definido pela direção")
	}
	if amount.IsZero() {
		return Entry{}, nil
	}

	var (
		after money.Money
		err   error
	)
	if direction == DirectionDebit {
		after, err = w.balance.Sub(amount)
		if err != nil {
			return Entry{}, err
		}
		if after.IsNegative() {
			return Entry{}, insufficient(amount.Minor(), w.balance.Minor())
		}
	} else {
		after, err = w.balance.Add(amount)
		if err != nil {
			return Entry{}, err
		}
	}

	entry, err := NewEntry(w.id, direction, amount, w.balance, now)
	if err != nil {
		// Inalcançável hoje: o NewEntry revalida moeda, valor positivo,
		// instante e faixa, e o apply já garantiu os quatro antes daqui.
		// Fica como defesa se o NewEntry ganhar uma validação nova.
		return Entry{}, err
	}

	w.balance = after
	w.version++
	w.updatedAt = now

	return entry, nil
}
