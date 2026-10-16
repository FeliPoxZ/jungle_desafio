// Package app é o ponto de composição da aplicação.
//
// Este pacote é o ÚNICO lugar que conhece o grafo de dependências inteiro. O
// domínio não conhece Fx; aqui nós ligamos tudo: configuração, observabilidade,
// infraestrutura (Postgres/SQS/IdP), casos de uso, HTTP e workers.
//
// Organização em fx.Module (cada módulo é adicionado conforme a fase avança):
//
//	config     -> configuração validada + logger + métricas
//	infra      -> pool do Postgres, Unit of Work, verificador OIDC
//	usecase    -> serviços de aplicação (regras financeiras)
//	auth       -> autenticador OIDC (middleware, roles, providerId do token)
//	httpapi    -> servidor HTTP, rotas e ciclo de vida
//	worker     -> consumidor SQS, publisher da outbox, worker de referências
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
)

const (
	// startTimeout limita quanto tempo o boot pode levar.
	startTimeout = 30 * time.Second
)

// ModuleOptions devolve as opções do Fx que compõem a aplicação.
// É uma função (e não uma variável) para permitir a inclusão condicional de
// módulos de worker conforme a configuração.
func ModuleOptions() fx.Option {
	return fx.Options(
		configModule(),
		infraModule(),
		usecaseModule(),
		authModule(),
		httpapiModule(),
	)
}

// --- Módulo: configuração e observabilidade ---------------------------------

func configModule() fx.Option {
	return fx.Module("config",
		// Load devolve (Config, error): se a configuração for inválida, o Fx
		// aborta o boot ANTES de abrir qualquer conexão. Falhar cedo é
		// deliberado: um serviço financeiro não deve subir "mais ou menos".
		fx.Provide(config.Load),
		fx.Provide(observability.NewLogger),
		fx.Provide(observability.NewMetrics),
	)
}

// --- Runner ------------------------------------------------------------------

// runHTTPServer registra o servidor no ciclo de vida do Fx.
//
// Ordem de encerramento (importa!): novas entradas param PRIMEIRO (o servidor
// para de aceitar conexões), depois os workers são cancelados pelo Fx e só
// então as dependências (pool, client SQS) são fechadas. Essa ordem é garantida
// porque o hook de parada do servidor é registrado ANTES dos hooks das
// dependências, e o Fx executa OnStop na ordem inversa do registro.
func runHTTPServer(lc fx.Lifecycle, server *http.Server, cfg *config.Config, logger *slog.Logger) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			// Canal para capturar falha de bind (ex.: porta ocupada). Sem isso,
			// um erro de ListenAndServe apareceria apenas em log e o Fx acharia
			// que a aplicação subiu com sucesso.
			errCh := make(chan error, 1)
			go func() {
				if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					errCh <- err
					return
				}
				errCh <- nil
			}()

			// Dá uma janela curta para detectar erro de bind antes de declarar
			// o start bem-sucedido.
			select {
			case err := <-errCh:
				if err != nil {
					return fmt.Errorf("httpapi: falha ao subir o servidor em %s: %w", server.Addr, err)
				}
				logger.InfoContext(ctx, "http_server_stopped_unexpectedly")
				return nil
			case <-time.After(50 * time.Millisecond):
				logger.InfoContext(ctx, "http_server_started", slog.String("addr", server.Addr))
				return nil
			}
		},
		OnStop: func(ctx context.Context) error {
			logger.InfoContext(ctx, "http_server_stopping", slog.Duration("timeout", cfg.HTTP.ShutdownTimeout))

			// Shutdown para de aceitar novas conexões e aguarda as requisições
			// em andamento terminarem dentro do prazo.
			if err := server.Shutdown(ctx); err != nil {
				logger.ErrorContext(ctx, "http_server_shutdown_error", slog.String("error", err.Error()))
				return err
			}
			logger.InfoContext(ctx, "http_server_stopped")
			return nil
		},
	})
}

// --- Runner ------------------------------------------------------------------

// Run constrói a aplicação, sobe tudo e bloqueia até receber SIGINT/SIGTERM
// (ou até o Fx pedir encerramento), executando o shutdown gracioso.
func Run(version, commit string) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})).
		With(
			slog.String("service", "jungleg"),
			slog.String("version", version),
			slog.String("commit", commit),
		)

	// A configuração é carregada AQUI, antes do grafo, e não só por dentro dele
	// (P7). Motivo: o prazo de shutdown é uma OPÇÃO do Fx (`fx.StopTimeout`),
	// avaliada na construção do app — não há como um provider do grafo
	// influenciá-la. Lendo a config antes, o prazo vem de `HTTP_STOP_TIMEOUT`
	// (validado em `config.Validate`) em vez de uma constante no código.
	//
	// O `config.Load` continua no grafo: aqui é só a leitura antecipada do mesmo
	// valor, e ele é validado nos dois lugares.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}

	fxApp := fx.New(
		ModuleOptions(),
		// Os eventos internos do Fx (boot/shutdown) saem no mesmo formato JSON
		// do resto da aplicação, em vez do logger de texto padrão.
		fx.WithLogger(func() fxevent.Logger { return &fxSlogLogger{logger: logger} }),
		fx.StopTimeout(cfg.HTTP.StopTimeout),
	)

	// Sinal de cancelamento: SIGINT (Ctrl+C) e SIGTERM (docker stop / k8s).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	startCtx, cancelStart := context.WithTimeout(context.Background(), startTimeout)
	defer cancelStart()

	if err := fxApp.Start(startCtx); err != nil {
		return fmt.Errorf("app: falha no start: %w", err)
	}
	logger.Info("application_started",
		slog.String("addr", cfg.HTTP.Addr),
		slog.String("issuer", cfg.OIDC.Issuer),
		slog.Duration("stopTimeout", cfg.HTTP.StopTimeout),
	)

	// Espera o sinal (ou o Fx pedir para parar).
	<-ctx.Done()
	logger.Info("shutdown_signal_received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.StopTimeout)
	defer cancel()

	if err := fxApp.Stop(shutdownCtx); err != nil {
		logger.Error("shutdown_error", slog.String("error", err.Error()))
		return err
	}

	logger.Info("application_stopped")
	return nil
}

// ValidateApp constrói o grafo sem iniciar nada. Usado no teste de composição
// do Fx: pega erro de injeção (dependência faltando, ciclo, tipo duplicado)
// sem precisar subir Postgres, SQS ou IdP.
func ValidateApp(opts ...fx.Option) error {
	all := append([]fx.Option{ModuleOptions()}, opts...)
	all = append(all, fx.NopLogger)
	return fx.ValidateApp(all...)
}

// fxSlogLogger adapta os eventos internos do Fx para o slog.
type fxSlogLogger struct {
	logger *slog.Logger
}

func (l *fxSlogLogger) LogEvent(event fxevent.Event) {
	switch e := event.(type) {
	case *fxevent.OnStartExecuting:
		l.logger.Debug("fx_onstart", slog.String("fn", e.FunctionName), slog.String("caller", e.CallerName))
	case *fxevent.OnStartExecuted:
		if e.Err != nil {
			l.logger.Error("fx_onstart_failed", slog.String("fn", e.FunctionName), slog.String("error", e.Err.Error()))
		}
	case *fxevent.OnStopExecuting:
		l.logger.Debug("fx_onstop", slog.String("fn", e.FunctionName))
	case *fxevent.OnStopExecuted:
		if e.Err != nil {
			l.logger.Error("fx_onstop_failed", slog.String("fn", e.FunctionName), slog.String("error", e.Err.Error()))
		}
	case *fxevent.Supplied:
		l.logger.Debug("fx_supplied", slog.String("type", e.TypeName))
	case *fxevent.Provided:
		for _, rtype := range e.OutputTypeNames {
			l.logger.Debug("fx_provided", slog.String("type", rtype), slog.String("constructor", e.ConstructorName))
		}
	case *fxevent.Invoking:
		l.logger.Debug("fx_invoking", slog.String("fn", e.FunctionName))
	case *fxevent.Invoked:
		if e.Err != nil {
			l.logger.Error("fx_invoke_failed", slog.String("fn", e.FunctionName), slog.String("error", e.Err.Error()))
		}
	case *fxevent.Started:
		if e.Err != nil {
			l.logger.Error("fx_start_failed", slog.String("error", e.Err.Error()))
		}
	case *fxevent.Stopping:
		l.logger.Info("fx_stopping", slog.String("signal", e.Signal.String()))
	case *fxevent.Stopped:
		if e.Err != nil {
			l.logger.Error("fx_stop_failed", slog.String("error", e.Err.Error()))
		}
	}
}
