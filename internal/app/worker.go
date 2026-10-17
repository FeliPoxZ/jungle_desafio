package app

import (
	"context"
	"log/slog"
	"time"

	"go.uber.org/fx"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/inbox"
	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	"github.com/FeliPoxZ/jungle_desafio/internal/consumer"
	"github.com/FeliPoxZ/jungle_desafio/internal/httpapi"
	"github.com/FeliPoxZ/jungle_desafio/internal/infra/sqs"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
	"github.com/FeliPoxZ/jungle_desafio/internal/worker"
)

// URLDaFilaDeEntrada e URLDaFilaMorta são tipos PRÓPRIOS, e não `string`.
//
// O Fx casa injeção por tipo EXATO, e `string` é onipresente num grafo com
// configuração. Duas URLs distintas com o mesmo tipo seriam um erro de grafo
// ("já fornecido") — e, pior, se uma delas vencesse, o consumidor poderia apagar
// da fila ERRADA. Tipos distintos fazem o compilador (e o Fx) garantirem que a
// URL da DLQ não chega onde se espera a da fila de entrada.
type URLDaFilaDeEntrada string

// URLDaFilaMorta é a URL da DLQ.
type URLDaFilaMorta string

// URLDaFilaDeEventos é a URL do DESTINO dos eventos publicados pela outbox.
//
// Tipo próprio pelo mesmo motivo das outras duas: `SQS_EVENTS_QUEUE` e
// `SQS_EVENTS_DLQ` são duas URLs, e trocá-las publicaria evento financeiro na
// fila de mensagens mortas — onde ninguém consome.
type URLDaFilaDeEventos string

// workerModule liga o consumo do SQS e os laços da Fase 9 (publicador da outbox,
// worker de referências e reaper).
//
// # Por que um módulo separado
//
// O worker tem CICLO DE VIDA próprio (começa a buscar mensagens no start, para de
// buscar no stop) e depende de peças que o HTTP não usa (cliente SQS, filas). Um
// módulo por responsabilidade também deixa explícito, no grafo, quem depende de
// quê.
//
// O `processador` é a ponte: ele recebe a mensagem crua, traduz para a MESMA
// requisição que o HTTP produz e chama o MESMO caso de uso — com os ganchos que
// registram a inbox dentro da transação da operação (README §6.5).
func workerModule() fx.Option {
	return fx.Options(
		fx.Provide(sqs.NewClient),
		fx.Provide(sqs.NovoConsumidorFila),
		fx.Provide(urlDaFilaDeEntrada),
		fx.Provide(urlDaDLQ),
		fx.Provide(processadorDaInbox),
		fx.Provide(consumerOptions),
		fx.Provide(consumer.New),
		fx.Invoke(runConsumidor),
		// O SQS entra no readiness (fecha a P5): com o broker fora, esta instância
		// não processa nada do caminho assíncrono, e é melhor ela sair da rotação.
		// A dependência é o ADAPTADOR (que implementa `Ping`), e não o cliente
		// cru: é ele quem satisfaz a porta estreita do verificador.
		//
		// `fx.Annotate` + `fx.ResultTags`, e não `fx.Provide(fn, fx.ResultTags(...))`:
		// no Fx 1.24 a anotação é um `Annotation`, aplicada com `Annotate` — passar
		// direto para `Provide` faz o Fx tentar tratá-la como CONSTRUTOR e falhar
		// com "must provide constructor function". Mesma armadilha do defeito 23.
		fx.Provide(fx.Annotate(
			func(adaptador *sqs.ConsumidorFila, url URLDaFilaDeEntrada) httpapi.HealthChecker {
				return consumer.NovoHealthChecker(adaptador, string(url))
			},
			fx.ResultTags(`group:"health_checkers"`),
		)),
	)
}

// workersModule liga os laços da Fase 9.
//
// Fica separado de `workerModule` porque são dois ciclos de vida independentes: o
// consumo da fila de entrada pode estar desligado enquanto a publicação está
// ligada (e vice-versa), e os construtores são outros. A ORDEM no grafo importa
// para o encerramento: este módulo vem depois de `workerModule` e antes de
// `httpapiModule` em `ModuleOptions`, então o Fx (que executa os OnStop na ordem
// INVERSA do registro) para o servidor primeiro, depois o consumidor, depois os
// laços desta fase, e fecha o pool por último — que é a ordem correta: ninguém
// usa o banco depois de o pool fechar.
func workersModule() fx.Option {
	return fx.Options(
		fx.Provide(urlDaFilaDeEventos),
		// O adaptador concreto e a PORTA que o publicador pede. A conversão fica
		// aqui porque é o único lugar que conhece os dois lados.
		fx.Provide(func(cliente *sqs.Cliente, url URLDaFilaDeEventos) *sqs.PublicadorEventos {
			return sqs.NovoPublicadorEventos(cliente, string(url))
		}),
		fx.Provide(func(p *sqs.PublicadorEventos) worker.Publicador { return p }),
		// O MESMO caso de uso do HTTP e do SQS, atrás da porta do worker. É o que
		// garante que a retomada de uma referência passe pela mesma régua de
		// negócio (e pelo mesmo lock de carteira) da operação original.
		fx.Provide(func(s *appwagering.Service) worker.Retomador { return s }),
		fx.Provide(outboxPublisherOptions),
		fx.Provide(worker.NovoOutboxPublisher),
		fx.Provide(referenceWorkerOptions),
		fx.Provide(worker.NovoReferenceWorker),
		fx.Provide(pendingReaperOptions),
		fx.Provide(worker.NovoPendingReaper),
		fx.Provide(runnerOptions),
		fx.Provide(worker.NovoRunner),
		fx.Invoke(runWorkers),
		// O runner entra no readiness: `UP` com os laços mortos seria falso. Sem
		// nenhum laço ligado ele responde `UP` de propósito (não há promessa a
		// cumprir).
		fx.Provide(fx.Annotate(
			func(r *worker.Runner) httpapi.HealthChecker { return r },
			fx.ResultTags(`group:"health_checkers"`),
		)),
	)
}

// urlDaFilaDeEntrada resolve a URL da fila de entrada.
//
// A URL é derivada do endpoint quando ele existe (LocalStack); sem endpoint
// (`SQS_ENDPOINT` vazio, caso de produção), sobra o NOME da fila — e nesse
// ambiente a resolução tem de vir do `GetQueueUrl` do SDK. Enquanto o caminho de
// produção não é exercitado, o valor derivado cobre o ambiente em que o projeto
// roda; a limitação está registrada no ARCHITECTURE.
func urlDaFilaDeEntrada(cfg *config.Config) URLDaFilaDeEntrada {
	if url := sqs.QueueURL(cfg, cfg.SQS.TransactionsQueue); url != "" {
		return URLDaFilaDeEntrada(url)
	}
	return URLDaFilaDeEntrada(cfg.SQS.TransactionsQueue)
}

// urlDaDLQ resolve a URL da fila morta, pelo mesmo critério.
func urlDaDLQ(cfg *config.Config) URLDaFilaMorta {
	if url := sqs.QueueURL(cfg, cfg.SQS.TransactionsDLQ); url != "" {
		return URLDaFilaMorta(url)
	}
	return URLDaFilaMorta(cfg.SQS.TransactionsDLQ)
}

// urlDaFilaDeEventos resolve a URL do destino da publicação.
//
// Mesmo critério das outras duas: a URL é derivada do endpoint quando ele existe
// (LocalStack); sem endpoint, sobra o NOME, e a resolução teria de vir do
// `GetQueueUrl` do SDK — limitação registrada no ARCHITECTURE §16.
func urlDaFilaDeEventos(cfg *config.Config) URLDaFilaDeEventos {
	if url := sqs.QueueURL(cfg, cfg.SQS.EventsQueue); url != "" {
		return URLDaFilaDeEventos(url)
	}
	return URLDaFilaDeEventos(cfg.SQS.EventsQueue)
}

// processadorDaInbox monta o processador com o MESMO caso de uso do HTTP.
//
// # Por que uma função de composição, e não `inbox.NovoProcessador` direto
//
// Duas razões, e a segunda é a que importa:
//
//  1. o Fx casa por tipo EXATO, e o parâmetro do `NovoProcessador` é a interface
//     `inbox.Aplicador` — o tipo concreto precisa ser convertido onde a composição
//     conhece os dois lados;
//  2. é AQUI que fica explícito, no grafo, que HTTP e SQS compartilham o mesmo
//     `*appwagering.Service`. É essa linha que garante a equivalência exigida no
//     README §10 ("HTTP e SQS devem compartilhar o caso de uso e as garantias de
//     idempotência financeira"): se o worker tivesse o seu próprio serviço, o hash
//     de idempotência e o lock seriam os mesmos, mas a GARANTIA não seria óbvia —
//     e um dia alguém injetaria uma configuração diferente só de um lado.
func processadorDaInbox(servico *appwagering.Service) *inbox.Processador {
	return inbox.NovoProcessador(servico)
}

// consumerOptions monta as dependências do consumidor.
//
// Existe como provider (em vez de o `consumer.New` receber os campos soltos)
// porque o Fx injeta ESTRUTURAS por tipo: `consumer.Options` precisa ser fornecida
// por alguém que conheça os tipos dos dois lados — e esse alguém é a composição.
//
// As duas URLs entram com tipos distintos (`URLDaFilaDeEntrada` e
// `URLDaFilaMorta`): se fossem `string`, a segunda providência seria um erro de
// grafo; e pior, se uma vencesse, o consumidor poderia APAGAR da fila errada.
func consumerOptions(
	fila *sqs.ConsumidorFila,
	processador *inbox.Processador,
	logger *slog.Logger,
	metrics *observability.Metrics,
	cfg *config.Config,
	entrada URLDaFilaDeEntrada,
	dlq URLDaFilaMorta,
) consumer.Options {
	return consumer.Options{
		Fila:        fila,
		Processador: processador,
		Logger:      logger,
		Metrics:     metrics,
		Config:      cfg,
		URLDaFila:   string(entrada),
		URLDaDLQ:    string(dlq),
	}
}

// runConsumidor registra o consumidor no ciclo de vida do Fx.
//
// # A ordem de encerramento importa
//
// O hook de parada do servidor HTTP é registrado ANTES deste (o módulo httpapi vem
// primeiro em `ModuleOptions`), e o Fx executa os OnStop na ordem INVERSA do
// registro. Logo: o servidor para de aceitar primeiro, e o consumidor para de
// buscar em seguida — os dois dentro do `StopTimeout`.
//
// O `Run` do consumidor usa um contexto CANCELÁVEL: o cancelamento é o sinal de
// "pare de buscar e conclua o que está em voo". Sem isso, o `Stop` do Fx ficaria
// esperando o consumidor para sempre (ele roda até o contexto morrer).
//
// `SQS_CONSUME_TRANSACTIONS=false` desliga o worker: é o que permite subir só a
// API (testes, desenvolvimento) sem depender de fila nenhuma.
func runConsumidor(lc fx.Lifecycle, consumidor *consumer.Consumidor, cfg *config.Config, logger *slog.Logger) {
	if !cfg.SQS.ConsumeTransactions {
		logger.Info("sqs_consumidor_desligado", slog.String("motivo", "SQS_CONSUME_TRANSACTIONS=false"))
		return
	}

	var (
		cancelar context.CancelFunc
		parou    chan struct{}
	)

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			ctx, cancel := context.WithCancel(context.Background())
			cancelar = cancel
			parou = make(chan struct{})

			go func() {
				defer close(parou)
				if err := consumidor.Run(ctx); err != nil {
					logger.Error("sqs_consumidor_falhou", slog.String("error", err.Error()))
				}
			}()

			logger.Info("sqs_consumidor_iniciado_em_background",
				slog.String("queue", cfg.SQS.TransactionsQueue))
			return nil
		},
		OnStop: func(context.Context) error {
			if cancelar == nil {
				return nil
			}

			// O cancelamento é o pedido de parada; o `Run` cuida de esperar o
			// trabalho em voo e de liberar a visibilidade do que sobrou.
			cancelar()

			prazo := cfg.SQS.ShutdownGrace + 5*time.Second
			select {
			case <-parou:
				logger.Info("sqs_consumidor_parou")
			case <-time.After(prazo):
				// O `Run` deveria ter voltado sozinho; se não voltou, não travamos
				// o shutdown do processo por causa dele.
				logger.Warn("sqs_consumidor_nao_parou_no_prazo", slog.Duration("prazo", prazo))
			}
			return nil
		},
	})
}

// --- Composição dos laços da Fase 9 -----------------------------------------

// outboxPublisherOptions monta as dependências do publicador.
//
// Existe como provider (em vez de o `NovoOutboxPublisher` receber os campos
// soltos) porque o Fx injeta ESTRUTURAS por tipo: `worker.OutboxPublisherOptions`
// precisa ser fornecida por alguém que conheça os dois lados do grafo. Mesma
// solução do `consumerOptions` da Fase 8.
func outboxPublisherOptions(
	uow ports.UnitOfWork,
	publicador worker.Publicador,
	logger *slog.Logger,
	metrics *observability.Metrics,
	cfg *config.Config,
) worker.OutboxPublisherOptions {
	return worker.OutboxPublisherOptions{
		UOW:        uow,
		Publicador: publicador,
		Logger:     logger,
		Metrics:    metrics,
		Config:     cfg,
	}
}

// referenceWorkerOptions monta as dependências do worker de referências.
func referenceWorkerOptions(
	uow ports.UnitOfWork,
	retomador worker.Retomador,
	logger *slog.Logger,
	metrics *observability.Metrics,
	cfg *config.Config,
) worker.ReferenceWorkerOptions {
	return worker.ReferenceWorkerOptions{
		UOW:       uow,
		Retomador: retomador,
		Logger:    logger,
		Metrics:   metrics,
		Config:    cfg,
	}
}

// pendingReaperOptions monta as dependências do reaper.
func pendingReaperOptions(
	uow ports.UnitOfWork,
	retomador worker.Retomador,
	logger *slog.Logger,
	metrics *observability.Metrics,
	cfg *config.Config,
) worker.PendingReaperOptions {
	return worker.PendingReaperOptions{
		UOW:       uow,
		Retomador: retomador,
		Logger:    logger,
		Metrics:   metrics,
		Config:    cfg,
	}
}

// runnerOptions decide QUAIS laços esta instância roda.
//
// # Por que a decisão fica na composição
//
// O runner não conhece `OUTBOX_ENABLED`/`REFERENCE_ENABLED`: ele recebe a lista
// pronta. Assim a política de "o que está ligado" mora num lugar só (aqui), e o
// runner continua testável com qualquer combinação de laços falsos.
//
// A lista vazia é um estado LEGÍTIMO: numa instância só-HTTP nada é ligado, e o
// runner não promete nada — o readiness dela responde UP sem mentir.
func runnerOptions(
	cfg *config.Config,
	publicador *worker.OutboxPublisher,
	referencias *worker.ReferenceWorker,
	reaper *worker.PendingReaper,
	logger *slog.Logger,
	metrics *observability.Metrics,
) worker.RunnerOptions {
	var lacos []worker.Laco
	if cfg.Outbox.Enabled {
		lacos = append(lacos, publicador)
	}
	if cfg.Reference.Enabled {
		// O reaper anda junto do worker de referências: os dois usam a mesma
		// configuração (REFERENCE_*) e a mesma retomada.
		lacos = append(lacos, referencias, reaper)
	}

	return worker.RunnerOptions{
		Lacos:   lacos,
		Logger:  logger,
		Metrics: metrics,
		Config:  cfg,
	}
}

// runWorkers registra os laços no ciclo de vida do Fx.
//
// # O encerramento, e onde ele difere do consumidor
//
// O consumidor tem trabalho EM VOO que precisa ser confirmado no banco ou
// devolvido à fila. Os laços da Fase 9 também, mas a recuperação deles não
// depende de nada explícito: o lease (`available_at` da outbox e
// `next_resolution_attempt_at` da transação) já devolve o trabalho abandonado
// quando o prazo vence. Por isso o encerramento aqui é "cancele e espere":
//
//  1. o cancelamento faz cada laço PARAR DE BUSCAR (o `Run` sai no começo do
//     ciclo seguinte, e o item em voo termina com `context.WithoutCancel`);
//  2. o que ficou em voo é aguardado por `WORKERS_SHUTDOWN_GRACE`;
//  3. o que não deu tempo volta sozinho pelo lease — sem código especial, e sem
//     risco de publicar duas vezes: republicar preserva o `eventId`, e o
//     consumidor deduplica por ele.
func runWorkers(lc fx.Lifecycle, runner *worker.Runner, cfg *config.Config, logger *slog.Logger) {
	var (
		cancelar context.CancelFunc
		parou    chan struct{}
	)

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			ctx, cancel := context.WithCancel(context.Background())
			cancelar = cancel
			parou = make(chan struct{})

			go func() {
				defer close(parou)
				if err := runner.Run(ctx); err != nil {
					logger.Error("workers_falharam", slog.String("error", err.Error()))
				}
			}()

			logger.Info("workers_iniciados_em_background",
				slog.Any("lacos", runner.Lacos()),
				slog.Duration("grace", runner.Grace()),
			)
			return nil
		},
		OnStop: func(context.Context) error {
			if cancelar == nil {
				return nil
			}

			// Para de buscar trabalho nos laços.
			cancelar()

			prazo := runner.Grace() + 5*time.Second
			select {
			case <-parou:
				logger.Info("workers_pararam")
			case <-time.After(prazo):
				// Não trava o shutdown do processo por causa dos laços: o que
				// ficou volta pelo lease.
				logger.Warn("workers_nao_pararam_no_prazo", slog.Duration("prazo", prazo))
			}
			return nil
		},
	})
}
