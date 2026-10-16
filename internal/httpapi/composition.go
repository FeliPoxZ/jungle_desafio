package httpapi

import (
	"context"
	"log/slog"
	"time"

	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
)

// WageringService é o contrato que a borda HTTP usa do caso de uso.
//
// Declarado AQUI (no consumidor), e não no caso de uso: é o padrão idiomático em
// Go — quem consome é quem diz do que precisa —, e permite que o teste do
// handler use um dublê pequeno em vez de subir banco.
//
// Ele tem exatamente os métodos que os endpoints precisam. Um método que entre
// aqui sem endpoint que o use é sinal de que a borda está fazendo mais do que
// deveria.
type WageringService interface {
	Process(ctx context.Context, req appwagering.Request) (appwagering.Result, error)
	OpenWallet(ctx context.Context, req appwagering.OpenWalletRequest) (appwagering.OpenWalletResult, error)

	GetTransaction(ctx context.Context, id wagering.TransactionID) (*wagering.WagerTransaction, error)
	GetProviderTransaction(ctx context.Context, providerID string, id wagering.TransactionID) (*wagering.WagerTransaction, error)
	GetByProviderExternalID(ctx context.Context, providerID, externalID string) (*wagering.WagerTransaction, error)

	GetWallet(ctx context.Context, id ids.WalletID) (*wallet.Wallet, error)
	ListLedger(ctx context.Context, walletID ids.WalletID, limit int, cursor string) (appwagering.LedgerPage, error)
	Reconcile(ctx context.Context, walletID ids.WalletID) (appwagering.Reconciliation, error)
}

// Garantia em tempo de compilação: o serviço real satisfaz o contrato da borda.
// Sem esta linha, uma mudança de assinatura no caso de uso apareceria só na
// composição do Fx (em tempo de EXECUÇÃO), e não no build.
var _ WageringService = (*appwagering.Service)(nil)

// Handlers agrupa o que os handlers HTTP precisam.
//
// Recebe INTERFACE (o serviço) e tipos concretos das camadas de apoio (logger,
// métricas, autenticador): o serviço é a peça que o teste substitui; as outras
// já são leves e determinísticas.
type Handlers struct {
	servico WageringService
	routes  *Routes
	auth    Autenticador
	logger  *slog.Logger
	metrics *observability.Metrics

	// relogio permite ao teste medir latência sem dormir. Em produção, time.Now.
	relogio func() time.Time
}

// HandlersDeps agrupa as dependências de `NewHandlers`.
//
// Struct de parâmetros em vez de vários argumentos posicionais: com seis
// argumentos, trocar a ordem de dois tipos compatíveis (dois `string`, por
// exemplo) compila e passa a injetar a coisa errada, em silêncio.
type HandlersDeps struct {
	Service       WageringService
	Routes        *Routes
	Authenticator Autenticador
	Logger        *slog.Logger
	Metrics       *observability.Metrics
	Clock         func() time.Time
}

// NewHandlers monta os handlers da API.
func NewHandlers(deps HandlersDeps) *Handlers {
	relogio := deps.Clock
	if relogio == nil {
		relogio = time.Now
	}
	return &Handlers{
		servico: deps.Service,
		routes:  deps.Routes,
		auth:    deps.Authenticator,
		logger:  deps.Logger,
		metrics: deps.Metrics,
		relogio: relogio,
	}
}
