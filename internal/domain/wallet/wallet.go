package wallet

import (
	"fmt"
	"strings"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ledger"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
)

// WalletID e PlayerID vêm do pacote ids, que é compartilhado com o ledger e o
// wagering. São aliases para os nomes continuarem funcionando aqui dentro sem
// duplicar o tipo — se fossem tipos novos, wallet.WalletID e ids.WalletID seriam
// incompatíveis e toda conversão viraria ruído.
type (
	// WalletID identifica uma carteira.
	WalletID = ids.WalletID
	// PlayerID identifica o jogador dono da carteira.
	PlayerID = ids.PlayerID
)

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

// Movement é o resultado de uma movimentação: o lançamento a gravar no ledger e
// a versão que a carteira tinha ANTES da operação.
//
// # Por que a versão vem junto (e por que isso importa)
//
// Debit e Credit já incrementam a versão da carteira em memória. Quem persiste
// precisa da versão ANTERIOR para o UPDATE condicional (`WHERE version = $3`).
// Ler `carteira.Version()` DEPOIS do débito devolve a versão nova, o UPDATE casa
// com zero linhas e a operação falha — um erro fácil de cometer e difícil de
// enxergar.
//
// Devolvendo a versão como parte do resultado, o acoplamento fica explícito e o
// erro deixa de ser possível: quem grava usa o que o domínio informou.
type Movement struct {
	entry         ledger.Entry
	versionBefore int64
	balanceBefore money.Money
	walletAfter   *Wallet
}

// Entry devolve o lançamento a gravar no ledger.
func (m Movement) Entry() ledger.Entry { return m.entry }

// VersionBefore devolve a versão anterior, para o UPDATE condicional.
func (m Movement) VersionBefore() int64 { return m.versionBefore }

// BalanceBefore devolve o saldo antes da movimentação.
func (m Movement) BalanceBefore() money.Money { return m.balanceBefore }

// Wallet devolve a carteira já atualizada, pronta para persistir o saldo novo.
func (m Movement) Wallet() *Wallet { return m.walletAfter }

// IsZero informa se não houve movimentação (valor zero: nada a persistir).
func (m Movement) IsZero() bool { return m.entry.IsZero() }

// Debit desconta amount do saldo, exigindo a mesma moeda e preservando
// saldo maior ou igual a zero (README §6.2).
//
// O transactionID identifica a operação que originou a movimentação e vai para
// o lançamento do ledger: sem ele, o lançamento fica órfão e não há como
// responder "por que o saldo mudou?".
func (w *Wallet) Debit(transactionID ledger.TransactionID, amount money.Money, now time.Time) (Movement, error) {
	return w.apply(ledger.DirectionDebit, transactionID, amount, now)
}

// Credit soma amount ao saldo, exigindo a mesma moeda.
func (w *Wallet) Credit(transactionID ledger.TransactionID, amount money.Money, now time.Time) (Movement, error) {
	return w.apply(ledger.DirectionCredit, transactionID, amount, now)
}

// apply é o único ponto que muda o saldo. Valida moeda e valor, calcula o
// saldo novo pela aritmética do money, recusa débito que ficaria negativo e
// só então altera o estado e a versão.
//
// Movimentação de valor zero devolve resultado vazio: não muda saldo, não muda
// versão e não gera lançamento no ledger (mesma regra do LOSS, README §7).
func (w *Wallet) apply(direction ledger.Direction, transactionID ledger.TransactionID, amount money.Money, now time.Time) (Movement, error) {
	if err := direction.Validate(); err != nil {
		return Movement{}, err
	}
	if strings.TrimSpace(string(transactionID)) == "" {
		return Movement{}, invalid("transactionID é obrigatório na movimentação")
	}
	if amount.Currency() != w.currency {
		return Movement{}, &Error{
			Code: CodeWalletCurrencyMismatch,
			Message: fmt.Sprintf("movimentação em %q em carteira de %q",
				amount.Currency(), w.currency),
		}
	}
	if amount.IsNegative() {
		return Movement{}, invalid("movimentação negativa: o sinal é definido pela direção")
	}
	if amount.IsZero() {
		return Movement{}, nil
	}

	var (
		after money.Money
		err   error
	)
	if direction == ledger.DirectionDebit {
		after, err = w.balance.Sub(amount)
		if err != nil {
			return Movement{}, err
		}
		if after.IsNegative() {
			return Movement{}, insufficient(amount.Minor(), w.balance.Minor())
		}
	} else {
		after, err = w.balance.Add(amount)
		if err != nil {
			return Movement{}, err
		}
	}

	// A versão ANTERIOR é capturada antes da mutação: é ela que o UPDATE
	// condicional precisa para detectar escritor concorrente.
	versionBefore := w.version
	balanceBefore := w.balance

	entry, err := ledger.NewEntry(w.id, transactionID, direction, amount, balanceBefore, now)
	if err != nil {
		// Inalcançável hoje: o NewEntry revalida moeda, valor positivo,
		// transactionID, instante e faixa, e o apply já garantiu os cinco antes
		// daqui. Fica como defesa se o NewEntry ganhar validação nova.
		return Movement{}, err
	}

	w.balance = after
	w.version++
	w.updatedAt = now

	return Movement{
		entry:         entry,
		versionBefore: versionBefore,
		balanceBefore: balanceBefore,
		walletAfter:   w,
	}, nil
}
