package wagering

import (
	"context"
	"fmt"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
)

// Reconciliation é o resultado da conferência: o saldo que está GRAVADO na
// carteira contra a soma de tudo o que o ledger registrou.
//
// `Difference` é `stored - calculated`, e é o número que interessa: zero significa
// que o saldo é explicado pelos lançamentos. Diferente de zero, alguém mexeu no
// saldo sem lançar, ou lançou sem mexer no saldo — e aí a pergunta deixa de ser
// "quanto tem" e passa a ser "por que os dois discordam".
type Reconciliation struct {
	WalletID          ids.WalletID
	StoredBalance     money.Money
	CalculatedBalance money.Money

	// Difference pode ser NEGATIVA: o saldo armazenado é menor que o
	// reconstruído. Money aceita negativo em cálculo interno (a carteira, não).
	Difference money.Money

	Consistent bool

	// CheckedEntries é quantos lançamentos entraram na soma, incluindo a
	// abertura de carteira.
	CheckedEntries int
}

// Reconcile reconstrói o saldo a partir do ledger e compara com o saldo gravado.
//
// # Por que em transação de leitura, e por que isso não é detalhe
//
// As três leituras (saldo, soma e contagem) acontecem numa ÚNICA transação
// `REPEATABLE READ` somente-leitura. Fora dela, uma operação confirmada entre a
// leitura do saldo e a soma do ledger produziria uma divergência que **nunca
// existiu** — o clássico falso positivo de reconciliação, que faz alguém procurar
// dinheiro que está no lugar.
//
// O modo somente-leitura é a garantia de que a conferência NÃO altera saldo
// (README §9): nem por acidente, nem por bug.
func (s *Service) Reconcile(ctx context.Context, walletID ids.WalletID) (Reconciliation, error) {
	if !ehUUID(string(walletID)) {
		return Reconciliation{}, fmt.Errorf("wagering: carteira %q: %w", walletID, ErrWalletNotFound)
	}

	var rec Reconciliation

	err := s.uow.RunReadOnly(ctx, func(ctx context.Context, r ports.Repos) error {
		carteira, err := r.Wallets.GetByID(ctx, walletID)
		if ports.IsNotFound(err) {
			return fmt.Errorf("wagering: carteira %s: %w: %w", walletID, ErrWalletNotFound, err)
		}
		if err != nil {
			return err
		}

		// A soma é feita no banco, em BIGINT: exata, e sem trazer o histórico
		// inteiro para somar em Go.
		soma, err := r.Ledger.SumSignedByWallet(ctx, walletID)
		if err != nil {
			return err
		}
		quantos, err := r.Ledger.CountByWallet(ctx, walletID)
		if err != nil {
			return err
		}

		// A moeda vem da carteira: o ledger guarda só o valor em unidades
		// mínimas, e o Money precisa da moeda para existir.
		moeda := carteira.Currency()
		armazenado := carteira.Balance()
		calculado := money.FromMinor(soma, moeda)

		diferenca, err := armazenado.Sub(calculado)
		if err != nil {
			return fmt.Errorf("wagering: diferença da reconciliação: %w", err)
		}

		rec = Reconciliation{
			WalletID:          carteira.ID(),
			StoredBalance:     armazenado,
			CalculatedBalance: calculado,
			Difference:        diferenca,
			Consistent:        diferenca.IsZero(),
			CheckedEntries:    quantos,
		}
		return nil
	})
	if err != nil {
		return Reconciliation{}, err
	}
	return rec, nil
}
