package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
)

// Routes é o contêiner de rotas exposto aos módulos de funcionalidade.
//
// Por que isso existe: quem conhece as rotas é o módulo que implementa o caso de
// uso (wallets, wagering...). O módulo httpapi só monta a base (middlewares,
// health, métricas) e oferece este contêiner para os outros registrarem o que é
// deles. Isso evita que o roteador cresça como um arquivo gigante central.
type Routes struct {
	Base     chi.Router
	Internal chi.Router
	Provider chi.Router
	Public   chi.Router
}

// Autenticador é a porta usada pelos módulos para proteger as rotas.
// Fica aqui (no consumidor) e é implementada em internal/infra/auth.
//
// Três métodos, e não um só, porque autenticar e autorizar são decisões
// diferentes:
//
//   - `Middleware` valida o token e põe a identidade no contexto (401 ou 503);
//   - `RequireRole` exige uma role da identidade já autenticada (403);
//   - `RequireProvider` exige que a identidade seja de um PROVEDOR — ou seja,
//     que o token carregue `provider_id` (403 sem ele).
//
// A separação é o que faz um token ausente responder 401 e um token sem
// permissão responder 403. Com um método só, os dois casos virariam o mesmo
// código, e o cliente não saberia se deve trocar o token ou pedir acesso.
type Autenticador interface {
	// Middleware exige um token válido e coloca a identidade no contexto.
	Middleware(next http.Handler) http.Handler
	// RequireRole exige que a identidade tenha uma das roles informadas.
	RequireRole(roles ...string) func(http.Handler) http.Handler
	// RequireProvider exige uma identidade de provedor (com provider_id).
	RequireProvider() func(http.Handler) http.Handler
}

// NewRouter monta o roteador com os middlewares e as rotas base.
func NewRouter(
	cfg *config.Config,
	logger *slog.Logger,
	metrics *observability.Metrics,
	healthCheckers []HealthChecker,
) *Routes {
	r := chi.NewRouter()
	// Ordem dos middlewares importa — e a de `AccessLog` × `Recoverer` foi
	// corrigida nesta fase (pendência P2):
	//
	//   RequestID -> todo log abaixo já tem correlação
	//   RealIP    -> resolve o IP real atrás de proxy
	//   AccessLog -> registra status e duração de TUDO, inclusive do que o
	//                Recoverer transformar em 500
	//   Recoverer -> converte panic em 500 sem derrubar o processo
	//   Timeout   -> prazo por requisição
	//   MaxBody   -> limita memória por payload
	//
	// Por que `AccessLog` ANTES (mais externo) que o `Recoverer`: o log de
	// acesso é dono do `statusRecorder`, e o `Recoverer` escreve a resposta de
	// 500 DEPOIS que o handler entrou em pânico. Com a ordem invertida, o
	// `statusRecorder` do log já estaria fora do caminho e o log registraria
	// **200** para uma requisição que respondeu **500** — o log passaria a
	// mentir exatamente no caso em que ele é mais necessário.
	//
	// NOTA sobre `chimw.RequestSize`: ele NÃO é usado aqui. Com limite 0 ele
	// recusa QUALQUER corpo com "request body too large" — o oposto do que se
	// quer —, e o limite de verdade é o `MaxBodyMiddleware`, que vem da
	// configuração (`HTTP_MAX_BODY_BYTES`) e produz um erro claro no `decodeJSON`.
	r.Use(RequestIDMiddleware)
	r.Use(chimw.RealIP)
	r.Use(AccessLogMiddleware(logger))
	r.Use(RecovererMiddleware(logger))
	r.Use(TimeoutMiddleware(cfg.HTTP.RequestTimeout))
	r.Use(MaxBodyMiddleware(cfg.HTTP.MaxBodyBytes))

	// Endpoints públicos (não exigem token).
	r.Get("/health/live", LivenessHandler())
	r.Get("/health/ready", ReadyHandler(healthCheckers, logger))
	r.Method(http.MethodGet, "/metrics", metrics.Handler())

	routes := &Routes{Base: r}

	// Sub-roteadores sem autenticação embutida. O módulo de feature decide:
	//   Public   -> aberto
	//   Internal -> exige role internal
	//   Provider -> exige token de provedor e aplica isolamento por providerId
	routes.Public = chi.NewRouter()
	routes.Internal = chi.NewRouter()
	routes.Provider = chi.NewRouter()

	r.Mount("/", routes.Public)
	r.Mount("/internal/wallets", routes.Internal)
	r.Mount("/wagering", routes.Provider)

	return routes
}

// NewServer cria o servidor HTTP.
//
// Timeouts são obrigatórios em produção: sem eles, uma conexão lenta (slow
// loris) mantém goroutine e socket abertos indefinidamente.
func NewServer(cfg *config.Config, routes *Routes) *http.Server {
	return &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           routes.Base,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		ReadHeaderTimeout: cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
	}
}

// ShutdownTimeout devolve o prazo de encerramento gracioso do servidor.
func ShutdownTimeout(cfg *config.Config) time.Duration {
	return cfg.HTTP.ShutdownTimeout
}
