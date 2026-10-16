package wagering

import (
	"context"
	"fmt"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ledger"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
)

const (
	// limitePadraoLedger é a página do ledger quando o cliente não pede tamanho.
	limitePadraoLedger = 50

	// limiteMaximoLedger é o teto de uma página.
	//
	// Existe para o cliente não transformar a paginação em varredura: sem teto,
	// `limit=1000000` seria uma consulta que segura conexão e memória do serviço.
	// O valor é o mesmo do A9.
	limiteMaximoLedger = 200
)

// LedgerPage é uma página do ledger, com o cursor da próxima.
//
// O cursor é OPACO: o cliente não monta nem interpreta o valor, só devolve na
// chamada seguinte. Assim a ordenação pode mudar sem quebrar o contrato.
type LedgerPage struct {
	Entries    []*ledger.Entry
	NextCursor string
}

// GetTransaction devolve a operação pelo identificador interno.
//
// É leitura do serviço INTERNO (a borda exige a role `internal`): a operação pode
// ser de qualquer provedor, e quem chamou já passou pela autorização.
func (s *Service) GetTransaction(ctx context.Context, id wagerdomain.TransactionID) (*wagerdomain.WagerTransaction, error) {
	if !ehUUID(string(id)) {
		// Identificador que não é UUID não pode existir na coluna: "não
		// encontrado", e não erro de servidor (ver `ids.go`).
		return nil, fmt.Errorf("wagering: transação %q: %w", id, ErrTransactionNotFound)
	}
	return s.lerTransacao(ctx, func(r ports.Repos) (*wagerdomain.WagerTransaction, error) {
		return r.Transactions.GetByID(ctx, id)
	})
}

// GetProviderTransaction devolve a operação para UM provedor.
//
// Quando a operação existe mas é de outro provedor, a resposta é "não existe" — e
// não "proibido". Devolver 403 confirmaria a existência de uma operação alheia, e
// essa é exatamente a informação que o isolamento entre provedores protege
// (README §2 e §13): o provedor não pode nem descobrir que a operação do outro
// existe.
func (s *Service) GetProviderTransaction(ctx context.Context, providerID string, id wagerdomain.TransactionID) (*wagerdomain.WagerTransaction, error) {
	if !ehUUID(string(id)) {
		return nil, fmt.Errorf("wagering: transação %q: %w", id, ErrTransactionNotFound)
	}
	return s.lerTransacao(ctx, func(r ports.Repos) (*wagerdomain.WagerTransaction, error) {
		tx, err := r.Transactions.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}
		if tx.ProviderID() != providerID {
			return nil, ports.ErrNotFound
		}
		return tx, nil
	})
}

// GetByProviderExternalID devolve a operação pela identidade do provedor.
//
// O provedor faz parte da CONSULTA, e não de uma checagem depois dela: uma
// operação de outro provedor simplesmente não é encontrada, o que é o mesmo
// isolamento do método acima sem sequer ler a linha alheia.
func (s *Service) GetByProviderExternalID(ctx context.Context, providerID, externalID string) (*wagerdomain.WagerTransaction, error) {
	return s.lerTransacao(ctx, func(r ports.Repos) (*wagerdomain.WagerTransaction, error) {
		return r.Transactions.GetByProviderAndExternalID(ctx, providerID, externalID)
	})
}

// GetWallet devolve a carteira pelo identificador.
func (s *Service) GetWallet(ctx context.Context, id ids.WalletID) (*wallet.Wallet, error) {
	if !ehUUID(string(id)) {
		return nil, fmt.Errorf("wagering: carteira %q: %w", id, ErrWalletNotFound)
	}

	var carteira *wallet.Wallet

	err := s.uow.RunReadOnly(ctx, func(ctx context.Context, r ports.Repos) error {
		var e error
		carteira, e = r.Wallets.GetByID(ctx, id)
		if ports.IsNotFound(e) {
			return fmt.Errorf("wagering: carteira %s: %w: %w", id, ErrWalletNotFound, e)
		}
		return e
	})
	if err != nil {
		return nil, err
	}
	return carteira, nil
}

// ListLedger devolve uma página do ledger de uma carteira.
//
// O `cursor` recebido é o valor opaco devolvido na página anterior (string vazia
// na primeira). O `NextCursor` só vem preenchido quando a página veio CHEIA: se
// veio menor que o limite, acabou, e devolver um cursor mandaria o cliente
// procurar uma página que não existe.
func (s *Service) ListLedger(ctx context.Context, walletID ids.WalletID, limit int, cursor string) (LedgerPage, error) {
	if !ehUUID(string(walletID)) {
		return LedgerPage{}, fmt.Errorf("wagering: carteira %q: %w", walletID, ErrWalletNotFound)
	}

	limite := normalizarLimite(limit)

	var (
		posicao *ports.Cursor
		pagina  LedgerPage
	)
	if cursor != "" {
		c, err := ports.DecodeCursor(cursor)
		if err != nil {
			// Cursor malformado é entrada inválida do cliente: a borda traduz
			// isto em 400, e não em 500.
			return LedgerPage{}, invalid(CodeInvalidRequest, "cursor inválido: %v", err)
		}
		posicao = &c
	}

	err := s.uow.RunReadOnly(ctx, func(ctx context.Context, r ports.Repos) error {
		// A carteira é conferida ANTES de listar: sem isso, uma carteira que não
		// existe devolveria uma página vazia, e o cliente não teria como
		// distinguir "não tem lançamento" de "não tem carteira".
		if _, e := r.Wallets.GetByID(ctx, walletID); e != nil {
			if ports.IsNotFound(e) {
				return fmt.Errorf("wagering: carteira %s: %w: %w", walletID, ErrWalletNotFound, e)
			}
			return e
		}

		entradas, e := r.Ledger.ListByWallet(ctx, walletID, limite, posicao)
		if e != nil {
			return e
		}

		pagina.Entries = entradas
		if len(entradas) == limite {
			ultima := entradas[len(entradas)-1]
			pagina.NextCursor = ports.EncodeCursor(ports.Cursor{
				CreatedAt: ultima.CreatedAt(),
				ID:        ultima.ID(),
			})
		}
		return nil
	})
	if err != nil {
		return LedgerPage{}, err
	}
	return pagina, nil
}

// lerTransacao executa uma leitura de transação numa visão consistente.
func (s *Service) lerTransacao(
	ctx context.Context,
	buscar func(r ports.Repos) (*wagerdomain.WagerTransaction, error),
) (*wagerdomain.WagerTransaction, error) {
	var tx *wagerdomain.WagerTransaction

	err := s.uow.RunReadOnly(ctx, func(ctx context.Context, r ports.Repos) error {
		var e error
		tx, e = buscar(r)
		if ports.IsNotFound(e) {
			// A transação de outro provedor cai AQUI também, e de propósito: o
			// erro não distingue "não existe" de "não é sua" (ver
			// GetProviderTransaction).
			return fmt.Errorf("wagering: %w: %w", ErrTransactionNotFound, e)
		}
		return e
	})
	if err != nil {
		return nil, err
	}
	return tx, nil
}

// normalizarLimite aplica o padrão e o teto da página.
func normalizarLimite(limit int) int {
	switch {
	case limit <= 0:
		return limitePadraoLedger
	case limit > limiteMaximoLedger:
		return limiteMaximoLedger
	default:
		return limit
	}
}
