package app

import (
	"context"
	"log/slog"

	"go.uber.org/fx"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	"github.com/FeliPoxZ/jungle_desafio/internal/httpapi"
	"github.com/FeliPoxZ/jungle_desafio/internal/infra/auth"
	"github.com/FeliPoxZ/jungle_desafio/internal/infra/postgres"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
	"github.com/FeliPoxZ/jungle_desafio/internal/platform/clock"
	"github.com/FeliPoxZ/jungle_desafio/internal/platform/idgen"
)

// --- Módulo: infraestrutura --------------------------------------------------

// infraModule fornece os adaptadores: banco, Unit of Work e verificador OIDC.
//
// Note o que a aplicação recebe: `ports.UnitOfWork`, e não `*postgres.Pool`. É a
// direção de dependência do ARCHITECTURE §1 — o caso de uso não conhece o banco.
func infraModule() fx.Option {
	return fx.Module("infra",
		// `context.Background()` com prazo próprio, e não o contexto do Fx: o
		// `*pgxpool.Pool` é um recurso de VIDA LONGA (ele reconecta sozinho e o
		// seu contexto não é cancelado quando o boot termina), diferente do
		// contexto de start, que é cancelado assim que o boot acaba. Passar o
		// contexto errado aqui derrubaria o pool no primeiro `Start`.
		fx.Provide(func(cfg *config.Config) (*postgres.Pool, error) {
			ctx, cancel := context.WithTimeout(context.Background(), cfg.Database.ConnectTimeout)
			defer cancel()
			return postgres.NewPool(ctx, cfg)
		}),
		fx.Provide(func(pool *postgres.Pool) ports.UnitOfWork {
			return postgres.NewUnitOfWork(pool)
		}),
		fx.Provide(fx.Annotate(novoPostgresHealthChecker,
			fx.ResultTags(`group:"health_checkers"`))),
		fx.Provide(novoVerificadorOIDC),
		fx.Invoke(fecharPoolNoShutdown),
	)
}

// novoVerificadorOIDC traduz a configuração da aplicação na do pacote de auth.
//
// A tradução fica aqui, e não no pacote de auth: lá dentro não interessa de onde
// vieram os valores (variável de ambiente, arquivo, teste), e um pacote de
// infraestrutura que importa `config` deixa de ser reutilizável fora desta
// aplicação.
func novoVerificadorOIDC(cfg *config.Config) *auth.Verifier {
	return auth.New(auth.Config{
		Issuer:       cfg.OIDC.Issuer,
		DiscoveryURL: cfg.OIDC.DiscoveryURL,
		Audience:     cfg.OIDC.Audience,
		JWKSCacheTTL: cfg.OIDC.JWKSCacheTTL,
		ClockSkew:    cfg.OIDC.ClockSkew,
		HTTPTimeout:  cfg.OIDC.HTTPTimeout,
	})
}

// novoPostgresHealthChecker registra o PostgreSQL no grupo de checkers.
//
// Com isto o `/health/ready` passa a dizer "postgres: DOWN" quando o banco cai,
// em vez de responder 200 e deixar o orquestrador mandar tráfego para uma
// instância que não consegue atender nada.
func novoPostgresHealthChecker(pool *postgres.Pool) httpapi.HealthChecker {
	return postgres.NewHealthChecker(pool)
}

// fecharPoolNoShutdown registra o fechamento do pool no ciclo de vida do Fx.
//
// A ordem importa: o Fx executa os hooks OnStop na ordem INVERSA do registro, e
// o hook do servidor HTTP foi registrado antes (módulo httpapi) — então o
// servidor para de aceitar primeiro, e só depois o pool fecha. Fechar o pool
// antes deixaria requisições em andamento sem banco.
func fecharPoolNoShutdown(lc fx.Lifecycle, pool *postgres.Pool, logger *slog.Logger) {
	lc.Append(fx.Hook{
		OnStop: func(context.Context) error {
			logger.Info("postgres_pool_closing")
			pool.Close()
			return nil
		},
	})
}

// --- Módulo: caso de uso -----------------------------------------------------

// usecaseModule fornece os serviços de aplicação.
//
// As três dependências do `Service` são CONTRATOS: o Unit of Work (quem delimita
// a transação), o relógio (para o teste afirmar instantes) e o gerador de
// identificadores (para o teste não comparar UUIDs aleatórios).
func usecaseModule() fx.Option {
	return fx.Module("usecase",
		// `Provide`, e não `Supply`: o serviço depende das INTERFACES
		// (`ports.Clock`, `idgen.Generator`), e o Fx casa por tipo EXATO. Estes
		// dois providers publicam as duas formas de cada valor — o tipo concreto
		// e o contrato —, e é o único jeito de um valor já pronto entrar no
		// grafo como interface.
		//
		// A alternativa (injetar `clock.System` direto no serviço) acoplaria o
		// caso de uso ao pacote de plataforma e mataria a substituição do
		// relógio no teste.
		fx.Provide(func() clock.System { return clock.System{} }),
		fx.Provide(func() ports.Clock { return clock.System{} }),
		fx.Provide(func() idgen.Generator { return idgen.New() }),
		fx.Provide(appwagering.New),
	)
}

// --- Módulo: autenticação ----------------------------------------------------

// authModule fornece o autenticador OIDC e o registra como dependência
// verificável no readiness.
//
// O IdP entra no readiness porque "conseguir validar um token" é pré-requisito
// para atender QUALQUER rota de negócio: uma instância que não alcança o
// Keycloak responde 503 a tudo que importa, e é melhor ela sair da rotação.
func authModule() fx.Option {
	return fx.Module("auth",
		fx.Provide(auth.NewAutenticador),
		fx.Provide(fx.Annotate(
			func(a *auth.Autenticador) httpapi.HealthChecker { return a },
			fx.ResultTags(`group:"health_checkers"`),
		)),
	)
}

// --- Módulo: HTTP ------------------------------------------------------------

// rotasDeps são as dependências de `httpapi.NewRouter`.
//
// Struct `fx.In` em vez de parâmetros soltos porque um deles vem de GRUPO: um
// parâmetro dentro de um `fx.In` pode declarar `group:"..."` diretamente, o que
// não é possível num parâmetro solto (ali só com `fx.ParamTags`, e o Fx recusa
// anotar um `fx.In`).
type rotasDeps struct {
	fx.In

	Config   *config.Config
	Logger   *slog.Logger
	Metrics  *observability.Metrics
	Checkers []httpapi.HealthChecker `group:"health_checkers"`
}

// httpapiModule fornece as rotas, o servidor, os handlers e o ciclo de vida.
func httpapiModule() fx.Option {
	return fx.Module("httpapi",
		fx.Provide(func(deps rotasDeps) *httpapi.Routes {
			return httpapi.NewRouter(deps.Config, deps.Logger, deps.Metrics, deps.Checkers)
		}),
		fx.Provide(httpapi.NewServer),
		fx.Provide(handlersDeps),
		fx.Provide(httpapi.NewHandlers),
		fx.Invoke(registrarRotas),
		fx.Invoke(runHTTPServer),
	)
}

// handlersDeps monta as dependências dos handlers.
//
// Existe como provider (em vez de o próprio `httpapi.NewHandlers` receber tudo,
// ou de `*appwagering.Service` ser injetado direto) porque o campo `Service` do
// `HandlersDeps` é uma INTERFACE: o Fx casa por tipo EXATO, então o tipo concreto
// precisa ser convertido em algum lugar — e esse lugar é a composição, o único
// que conhece os dois lados do contrato.
func handlersDeps(
	servico *appwagering.Service,
	routes *httpapi.Routes,
	autenticador *auth.Autenticador,
	logger *slog.Logger,
	metrics *observability.Metrics,
) httpapi.HandlersDeps {
	return httpapi.HandlersDeps{
		Service:       servico,
		Routes:        routes,
		Authenticator: autenticador,
		Logger:        logger,
		Metrics:       metrics,
	}
}

// registrarRotas monta as rotas de negócio na inicialização do grafo.
//
// É um `fx.Invoke` porque montar rota é EFEITO, não valor: ninguém consome as
// rotas registradas, elas precisam acontecer.
func registrarRotas(handlers *httpapi.Handlers) { handlers.RegistrarRotas() }
