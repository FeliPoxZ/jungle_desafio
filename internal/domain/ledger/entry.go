// Package ledger define o lançamento contábil: o registro append-only de toda
// movimentação de dinheiro.
//
// # Ledger não é a carteira
//
// A carteira guarda o saldo **atual**: é o que responde "quanto tem agora" e o
// que o lock protege. O ledger guarda o **histórico**: é o que responde "por que
// o saldo está assim" e o que permite auditoria e reconciliação.
//
// São duas responsabilidades diferentes, e por isso dois agregados diferentes.
// A carteira pode ser reconstruída a partir do ledger (somando os lançamentos),
// mas o ledger **nunca** pode ser reconstruído a partir da carteira: o saldo
// atual não conta como ele chegou ali.
//
// Detalhamento da distinção em `docs/LEDGER_VS_WALLET.md`.
//
// # Por que cada lançamento guarda DOIS saldos
//
// `balanceBefore` e `balanceAfter` fazem a linha ser auditável sozinha: quem lê
// consegue conferir a aritmética sem depender do saldo atual da carteira, que
// pode ter mudado mil vezes desde então. Um ledger em que a auditoria depende do
// estado de hoje não serve para auditar.
package ledger

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	domainerrors "github.com/FeliPoxZ/jungle_desafio/internal/domain/errors"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
)

// Códigos estáveis de falha do ledger. Viram failureCode no contrato HTTP.
const (
	// CodeInvalidEntry cobre lançamento malformado: valor não positivo, direção
	// inválida, instante ausente, transactionID ausente.
	CodeInvalidEntry = "INVALID_ENTRY"

	// CodeLedgerCurrencyMismatch é o lançamento cuja moeda não bate com a moeda
	// do saldo.
	//
	// É um código PRÓPRIO, e não o CURRENCY_MISMATCH do money, porque a origem do
	// problema é diferente: lá era operar duas moedas entre si, aqui é lançar em
	// moeda diferente da moeda da carteira. Separando, o log diz qual dos dois
	// aconteceu sem precisar interpretar a mensagem.
	CodeLedgerCurrencyMismatch = "LEDGER_CURRENCY_MISMATCH"
)

// Error é o erro de domínio, compartilhado com os outros pacotes.
type Error = domainerrors.Error

// ErrInvalidEntry é o erro sentinela de lançamento malformado.
var ErrInvalidEntry = &Error{Code: CodeInvalidEntry, Message: "lançamento de ledger inválido"}

// invalidEntry monta um erro de lançamento inválido com mensagem formatada.
func invalidEntry(format string, args ...any) error {
	return domainerrors.New(CodeInvalidEntry, format, args...)
}

// Direction é o sentido de uma movimentação no ledger.
type Direction string

const (
	// DirectionCredit soma ao saldo.
	DirectionCredit Direction = "CREDIT"
	// DirectionDebit desconta do saldo.
	DirectionDebit Direction = "DEBIT"
)

// ParseDirection converte a string vinda do banco na direção tipada.
func ParseDirection(s string) (Direction, error) {
	d := Direction(s)
	if err := d.Validate(); err != nil {
		return "", err
	}
	return d, nil
}

// Validate confere se a direção é uma das previstas.
func (d Direction) Validate() error {
	if d != DirectionCredit && d != DirectionDebit {
		return invalidEntry("direção inválida: %q (esperado DEBIT ou CREDIT)", d)
	}
	return nil
}

// String devolve a direção como aparece no banco e no JSON.
func (d Direction) String() string { return string(d) }

// TransactionID identifica a operação que originou o lançamento.
//
// É o elo entre o ledger e a transação de aposta: sem ele, o lançamento fica
// órfão e não há como responder "por que o saldo mudou?".
//
// É um alias de `ids.TransactionID` — o MESMO tipo usado pelo `wagering`. Assim
// não existe conversão entre os dois pacotes, e a passagem do identificador do
// agregado para o lançamento é direta.
type TransactionID = ids.TransactionID

// Entry é uma linha do ledger. **Append-only: não existe alteração nem remoção.**
//
// A imutabilidade não é convenção: o banco tem um trigger que recusa UPDATE e
// DELETE nesta tabela (Fase 4), e este pacote não expõe nenhum método que mude um
// lançamento depois de criado. As duas barreiras juntas significam que um bug de
// aplicação não consegue reescrever a história.
type Entry struct {
	id            string
	walletID      ids.WalletID
	transactionID TransactionID
	direction     Direction
	amount        money.Money
	balanceBefore money.Money
	balanceAfter  money.Money
	createdAt     time.Time
}

// NewEntry cria o lançamento e confere a aritmética: balanceAfter tem que ser
// exatamente balanceBefore mais (crédito) ou menos (débito) o valor.
//
// Essa checagem aqui é redundante com o CHECK do banco (Fase 4), e é de
// propósito: o erro nasce no domínio, com Code estável, **antes** de virar falha
// de constraint. O cliente recebe "saldo posterior inválido" com o código certo,
// e não um erro de banco que ninguém sabe interpretar.
func NewEntry(
	walletID ids.WalletID,
	transactionID TransactionID,
	direction Direction,
	amount money.Money,
	balanceBefore money.Money,
	now time.Time,
) (Entry, error) {
	if err := direction.Validate(); err != nil {
		return Entry{}, err
	}
	if strings.TrimSpace(string(transactionID)) == "" {
		return Entry{}, invalidEntry("transactionID é obrigatório no lançamento")
	}
	if err := amount.Currency().Validate(); err != nil {
		return Entry{}, invalidEntry("moeda do lançamento inválida: %v", err)
	}
	if !amount.IsPositive() {
		return Entry{}, invalidEntry("valor do lançamento precisa ser maior que zero, veio %s", amount)
	}
	if balanceBefore.Currency() != amount.Currency() {
		return Entry{}, &Error{
			Code: CodeLedgerCurrencyMismatch,
			Message: fmt.Sprintf("lançamento em %q com saldo em %q",
				amount.Currency(), balanceBefore.Currency()),
		}
	}
	if now.IsZero() {
		return Entry{}, invalidEntry("instante do lançamento é obrigatório")
	}

	var (
		after money.Money
		err   error
	)
	if direction == DirectionDebit {
		after, err = balanceBefore.Sub(amount)
	} else {
		after, err = balanceBefore.Add(amount)
	}
	if err != nil {
		return Entry{}, invalidEntry("saldo posterior inválido: %v", err)
	}
	if after.IsNegative() {
		return Entry{}, invalidEntry("saldo posterior negativo: %s", after)
	}

	return Entry{
		id:            newEntryID(),
		walletID:      walletID,
		transactionID: transactionID,
		direction:     direction,
		amount:        amount,
		balanceBefore: balanceBefore,
		balanceAfter:  after,
		createdAt:     now,
	}, nil
}

// RehydrateEntry reconstrói o lançamento do que está persistido, sem revalidar
// nem recalcular nada.
//
// Existe separado do NewEntry porque são operações diferentes: criar valida as
// regras, reidratar confia no que já foi validado quando entrou. Misturar os dois
// faria a leitura do banco reaplicar regra de negócio — e uma linha antiga, válida
// na época, poderia ser recusada por uma regra nova.
func RehydrateEntry(
	id string,
	walletID ids.WalletID,
	transactionID TransactionID,
	direction Direction,
	amount money.Money,
	balanceBefore money.Money,
	balanceAfter money.Money,
	createdAt time.Time,
) *Entry {
	return &Entry{
		id:            id,
		walletID:      walletID,
		transactionID: transactionID,
		direction:     direction,
		amount:        amount,
		balanceBefore: balanceBefore,
		balanceAfter:  balanceAfter,
		createdAt:     createdAt,
	}
}

// ID devolve o identificador do lançamento.
func (e Entry) ID() string { return e.id }

// WalletID devolve a carteira do lançamento.
func (e Entry) WalletID() ids.WalletID { return e.walletID }

// TransactionID devolve a operação que originou o lançamento.
func (e Entry) TransactionID() TransactionID { return e.transactionID }

// Direction devolve o sentido da movimentação.
func (e Entry) Direction() Direction { return e.direction }

// Amount devolve o valor movimentado, sempre positivo.
//
// O sinal não está no valor: quem define crédito ou débito é a Direction. Isso
// evita a ambiguidade de "valor negativo de um débito", que é crédito.
func (e Entry) Amount() money.Money { return e.amount }

// BalanceBefore devolve o saldo antes da movimentação.
func (e Entry) BalanceBefore() money.Money { return e.balanceBefore }

// BalanceAfter devolve o saldo depois da movimentação.
func (e Entry) BalanceAfter() money.Money { return e.balanceAfter }

// CreatedAt devolve o instante da movimentação.
func (e Entry) CreatedAt() time.Time { return e.createdAt }

// IsZero informa se o Entry é o valor zero (não construído).
//
// Existe porque comparar com Entry{} de fora do pacote para de compilar no dia
// em que o struct ganhar um campo não comparável (slice, mapa, função). O método
// isola quem chama dessa mudança.
func (e Entry) IsZero() bool {
	return e.id == "" && e.walletID == "" && e.createdAt.IsZero()
}

// SignedMinor devolve o valor em unidades mínimas já com o sinal da direção:
// crédito positivo, débito negativo.
//
// É o que permite reconciliar a carteira: somar os lançamentos de uma carteira
// tem que dar exatamente o saldo atual dela. O sinal fica aqui, e não no Amount,
// para o valor continuar sendo sempre positivo.
func (e Entry) SignedMinor() int64 {
	if e.direction == DirectionDebit {
		return -e.amount.Minor()
	}
	return e.amount.Minor()
}

// newEntryID gera o identificador do lançamento.
//
// É um UUID porque a coluna `wallet_ledger_entries.id` é UUID (Fase 4). Gerar um
// identificador derivado do conteúdo (uuid v5, ou uma string composta) parecia
// elegante, mas quebrava ao gravar: o banco recusa qualquer coisa que não seja
// UUID válido.
//
// O identificador é aleatório, não derivado: a proteção contra lançamento
// duplicado é o UNIQUE (wallet_id, transaction_id), não a chave primária.
func newEntryID() string {
	return uuid.NewString()
}
