package wagering

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ledger"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
)

// OpenWalletRequest é a abertura interna de carteira.
//
// Não vem de provedor e não tem jogo, rodada, referência nem operação externa: é
// o caminho do `POST /wallets`, restrito ao serviço interno (README §2).
type OpenWalletRequest struct {
	PlayerID       ids.PlayerID
	InitialBalance money.Money

	// IdempotencyKey é obrigatória: abrir carteira é a operação mais cara de
	// duplicar, e a chave é o que impede o crédito inicial de acontecer duas
	// vezes.
	IdempotencyKey string

	CorrelationID string
}

// OpenWalletResult é a carteira criada (ou a que já existia, no replay).
type OpenWalletResult struct {
	WalletID             ids.WalletID
	PlayerID             ids.PlayerID
	Balance              money.Money
	Version              int64
	OpeningTransactionID wagerdomain.TransactionID
	IdempotentReplay     bool
}

// OpenWallet cria a carteira e, quando há saldo inicial, a abertura financeira
// correspondente: `OPENING` concluída, lançamento de crédito e dois eventos, no
// MESMO commit da carteira.
//
// Saldo inicial zero é aceito (README §7) e cria só a carteira: zero é a
// ausência de dinheiro entrando, não um crédito de zero — e um lançamento de
// valor zero seria recusado pelo próprio domínio.
func (s *Service) OpenWallet(ctx context.Context, req OpenWalletRequest) (OpenWalletResult, error) {
	req = req.normalizado()

	if err := req.validate(); err != nil {
		return OpenWalletResult{}, err
	}

	var res OpenWalletResult
	err := s.comRetentativa(ctx, func(ctx context.Context) error {
		var e error
		res, e = s.abrirCarteira(ctx, req)
		return e
	})
	if err != nil {
		return OpenWalletResult{}, err
	}
	return res, nil
}

// abrirCarteira executa uma tentativa dentro de UMA transação.
func (s *Service) abrirCarteira(ctx context.Context, req OpenWalletRequest) (OpenWalletResult, error) {
	var res OpenWalletResult

	err := s.uow.Run(ctx, func(ctx context.Context, r ports.Repos) error {
		var e error
		res, e = s.abrir(ctx, r, req)
		return e
	})
	if err != nil {
		return OpenWalletResult{}, err
	}
	return res, nil
}

// abrir é a ordem da abertura dentro da transação.
func (s *Service) abrir(ctx context.Context, r ports.Repos, req OpenWalletRequest) (OpenWalletResult, error) {
	// 1. Idempotência da abertura. A unicidade da OPENING é pela CHAVE sozinha
	// (índice parcial `WHERE kind = 'OPENING'`), e não pelo par
	// (provedor, chave): a abertura não tem provedor.
	existente, err := r.Transactions.GetOpeningByIdempotencyKey(ctx, req.IdempotencyKey)
	switch {
	case err == nil:
		return s.replayDaAbertura(ctx, r, existente, req)
	case !ports.IsNotFound(err):
		return OpenWalletResult{}, err
	}

	// 2. Carteira duplicada: `(playerId, currency)` identifica UMA carteira.
	//
	// A leitura prévia existe para a mensagem ser boa; quem decide a corrida é o
	// índice único no INSERT — e a retentativa transforma essa corrida em
	// conflito legível em vez de erro de banco.
	_, err = r.Wallets.GetByPlayerAndCurrency(ctx, req.PlayerID, req.InitialBalance.Currency())
	switch {
	case err == nil:
		return OpenWalletResult{}, fmt.Errorf("wagering: jogador %s em %s: %w",
			req.PlayerID, req.InitialBalance.Currency(), ErrWalletAlreadyExists)
	case !ports.IsNotFound(err):
		return OpenWalletResult{}, err
	}

	now := s.clock.Now().UTC()

	carteira, err := wallet.NewWallet(ids.WalletID(s.ids.NewID()), req.PlayerID, req.InitialBalance, now)
	if err != nil {
		return OpenWalletResult{}, fmt.Errorf("wagering: criando a carteira: %w", err)
	}
	if err := r.Wallets.Insert(ctx, carteira); err != nil {
		return OpenWalletResult{}, err
	}

	if !req.InitialBalance.IsPositive() {
		return OpenWalletResult{
			WalletID: carteira.ID(),
			PlayerID: carteira.PlayerID(),
			Balance:  carteira.Balance(),
			Version:  carteira.Version(),
		}, nil
	}

	return s.abrirComSaldo(ctx, r, carteira, req, now)
}

// abrirComSaldo registra a abertura financeira da carteira que já nasceu com o
// saldo inicial.
func (s *Service) abrirComSaldo(
	ctx context.Context,
	r ports.Repos,
	carteira *wallet.Wallet,
	req OpenWalletRequest,
	now time.Time,
) (OpenWalletResult, error) {
	tx, err := wagerdomain.New(
		wagerdomain.TransactionID(s.ids.NewID()),
		carteira.ID(),
		carteira.PlayerID(),
		wagerdomain.KindOpening,
		req.InitialBalance,
		"", "", // OPENING não pertence a jogo nem a rodada
		req.IdempotencyKey,
		"", "", "", // sem provedor, sem identificador externo, sem referência
		req.payload(),
		codificarPayload,
		now,
	)
	if err != nil {
		return OpenWalletResult{}, fmt.Errorf("wagering: criando a OPENING: %w", err)
	}
	if err := r.Transactions.Insert(ctx, tx); err != nil {
		return OpenWalletResult{}, err
	}

	// O lançamento de abertura parte de ZERO e chega no saldo inicial.
	//
	// # Por que NÃO se chama carteira.Credit aqui
	//
	// `wallet.NewWallet` já nasceu com o saldo inicial. Chamar `Credit` faria o
	// saldo virar o DOBRO e a versão ir para 2 — contrariando o README §9, que
	// fixa a versão da carteira nessa abertura em 1. O que falta não é movimentar
	// a carteira: é REGISTRAR de onde o saldo veio, e é isso que o lançamento faz,
	// com `balanceBefore` zero.
	//
	// `FromMinor(0, ...)` em vez de `money.Zero`: a moeda já foi validada na
	// entrada, então o segundo erro possível do `Zero` seria impossível de
	// acontecer — e um erro impossível é só um caminho a mais para testar.
	zero := money.FromMinor(0, req.InitialBalance.Currency())
	entrada, err := ledger.NewEntry(carteira.ID(), tx.ID(), ledger.DirectionCredit, req.InitialBalance, zero, now)
	if err != nil {
		return OpenWalletResult{}, fmt.Errorf("wagering: lançamento de abertura: %w", err)
	}
	if err := r.Ledger.Insert(ctx, entrada); err != nil {
		return OpenWalletResult{}, err
	}

	if err := tx.MarkProcessed(req.InitialBalance, now); err != nil {
		return OpenWalletResult{}, err
	}
	if err := r.Transactions.Save(ctx, tx); err != nil {
		return OpenWalletResult{}, err
	}

	// Dois eventos, e a versão vai 1: a carteira nasceu com o saldo, e a abertura
	// não a movimentou depois.
	if err := s.emitirProcessado(ctx, r, tx, req.CorrelationID, "", req.InitialBalance, &entrada, carteira.Version(), now); err != nil {
		return OpenWalletResult{}, err
	}

	return OpenWalletResult{
		WalletID:             carteira.ID(),
		PlayerID:             carteira.PlayerID(),
		Balance:              carteira.Balance(),
		Version:              carteira.Version(),
		OpeningTransactionID: tx.ID(),
	}, nil
}

// replayDaAbertura devolve a carteira de uma abertura já feita com a mesma chave.
//
// Diferente do replay de uma operação, aqui o resultado é a carteira COMO ELA
// ESTÁ: a operação é um fato imutável ("aquela aposta debitou 25 e deixou 975"),
// mas a carteira é um recurso mutável, e representa-la com o saldo de ontem
// mentiria sobre o recurso. O que o `IdempotentReplay` diz é que nada foi criado
// desta vez.
func (s *Service) replayDaAbertura(
	ctx context.Context,
	r ports.Repos,
	existente *wagerdomain.WagerTransaction,
	req OpenWalletRequest,
) (OpenWalletResult, error) {
	hash, err := s.hashDe(req.payload())
	if err != nil {
		return OpenWalletResult{}, err
	}
	if !existente.MatchesPayload(hash) {
		return OpenWalletResult{}, fmt.Errorf("wagering: chave %q com conteúdo diferente: %w", req.IdempotencyKey, ErrIdempotencyConflict)
	}

	carteira, err := r.Wallets.GetByID(ctx, existente.WalletID())
	if err != nil {
		// OPENING gravada sem carteira é estado impossível (as duas nascem na
		// mesma transação). Se acontecer, o erro precisa aparecer, e não virar
		// uma resposta com saldo zero.
		return OpenWalletResult{}, fmt.Errorf("wagering: carteira %s da abertura: %w", existente.WalletID(), err)
	}

	return OpenWalletResult{
		WalletID:             carteira.ID(),
		PlayerID:             carteira.PlayerID(),
		Balance:              carteira.Balance(),
		Version:              carteira.Version(),
		OpeningTransactionID: existente.ID(),
		IdempotentReplay:     true,
	}, nil
}

// aberturaDeCarteira é a projeção canônica do pedido de abertura, e é ela que o
// hash de idempotência identifica.
//
// Não inclui o identificador da carteira, e isso é essencial: ele é GERADO a cada
// tentativa, então dois pedidos idênticos teriam hashes diferentes e o reenvio
// legítimo viraria conflito. O que identifica o pedido é a chave; o hash confere
// se o CONTEÚDO é o mesmo.
type aberturaDeCarteira struct {
	PlayerID string      `json:"playerId"`
	Kind     string      `json:"kind"`
	Money    money.Money `json:"money"`
}

// payload é o conteúdo de negócio do pedido de abertura.
func (r OpenWalletRequest) payload() aberturaDeCarteira {
	return aberturaDeCarteira{
		PlayerID: string(r.PlayerID),
		Kind:     wagerdomain.KindOpening.String(),
		Money:    r.InitialBalance,
	}
}

// validate recusa a abertura impossível antes de abrir transação.
func (r OpenWalletRequest) validate() error {
	if strings.TrimSpace(r.IdempotencyKey) == "" {
		return invalid(CodeIdempotencyKeyRequired, "abertura de carteira exige chave de idempotência")
	}
	if strings.TrimSpace(string(r.PlayerID)) == "" {
		return invalid(CodeInvalidRequest, "playerID é obrigatório")
	}
	if err := r.InitialBalance.Currency().Validate(); err != nil {
		return err
	}
	if r.InitialBalance.IsNegative() {
		return invalid(CodeInvalidRequest, "saldo inicial não pode ser negativo")
	}
	return nil
}

// normalizado devolve a requisição com a chave sem espaço nas pontas.
func (r OpenWalletRequest) normalizado() OpenWalletRequest {
	r.IdempotencyKey = strings.TrimSpace(r.IdempotencyKey)
	return r
}
