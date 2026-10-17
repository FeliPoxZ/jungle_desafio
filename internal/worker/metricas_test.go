package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
	"github.com/FeliPoxZ/jungle_desafio/internal/testutil"
)

// Testes da métrica `retries_total{stage}` para as etapas dos LAÇOS.
//
// O consumidor tem os seus em `internal/consumer`. Aqui ficam as quatro etapas
// que pertencem a este pacote:
//
//	outbox_claim     -> o ciclo do publicador falhou ao reivindicar o lote
//	outbox_mark      -> a mensagem saiu, e a confirmação no banco falhou
//	reference_claim  -> o ciclo do worker de referências falhou no claim
//	reaper_scan      -> a varredura do reaper falhou
//	worker_<laço>    -> o runner reiniciou um laço que morreu
//
// Cada teste usa a POLÍTICA de produção (Run/PublicarPendentes) com a porta
// falhando: o que se prova é a EMISSÃO, e ela não depende de broker nem de banco
// de verdade — esses estão nos testes de integração.

// endpointDaMetrica publica o registry no handler de produção.
func endpointDaMetrica(t *testing.T, m *observability.Metrics) string {
	t.Helper()

	servidor := httptest.NewServer(m.Handler())
	t.Cleanup(servidor.Close)
	return servidor.URL
}

// esperarRetry espera a etapa aparecer em `/metrics`.
func esperarRetry(t *testing.T, baseURL, etapa string) {
	t.Helper()

	fim := time.Now().Add(5 * time.Second)
	for time.Now().Before(fim) {
		familias := testutil.DoEndpoint(t, baseURL)
		if valor, ok := familias.Valor("jungleg_wager_retries_total",
			map[string]string{"stage": etapa}); ok && valor >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("retries_total{stage=%q} não apareceu em /metrics", etapa)
}

func TestMetricaRetryDaEtapaOutboxClaim(t *testing.T) {
	metricas := observability.NewMetrics()
	uow := &uowFake{registro: &registrador{}, erro: errors.New("banco fora")}

	cfg := config.OutboxConfig{
		BatchSize:    10,
		Lease:        time.Second,
		PollInterval: 10 * time.Millisecond,
		MaxAttempts:  10,
		BaseBackoff:  time.Second,
		MaxBackoff:   time.Minute,
	}
	publicador := NovoOutboxPublisher(OutboxPublisherOptions{
		UOW:        uow,
		Publicador: &publicadorFake{registro: &registrador{}},
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:    metricas,
		Config:     &config.Config{Outbox: cfg},
	})

	ctx, cancel := context.WithCancel(context.Background())
	parou := make(chan struct{})
	go func() { defer close(parou); _ = publicador.Run(ctx) }()

	esperarRetry(t, endpointDaMetrica(t, metricas), "outbox_claim")

	cancel()
	<-parou
}

func TestMetricaRetryDaEtapaOutboxMark(t *testing.T) {
	metricas := observability.NewMetrics()
	uow := &uowFake{
		registro: &registrador{},
		erroMark: errors.New("confirmação falhou"),
		outbox: []ports.OutboxEvent{
			eventDeTeste("evt-1", "carteira-1", "WagerTransactionProcessed", `{}`, time.Now().UTC()),
		},
	}

	cfg := config.OutboxConfig{
		BatchSize:    10,
		Lease:        time.Second,
		PollInterval: time.Second,
		MaxAttempts:  10,
		BaseBackoff:  time.Second,
		MaxBackoff:   time.Minute,
	}
	publicador := NovoOutboxPublisher(OutboxPublisherOptions{
		UOW:        uow,
		Publicador: &publicadorFake{registro: &registrador{}},
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:    metricas,
		Config:     &config.Config{Outbox: cfg},
	})

	if _, err := publicador.PublicarPendentes(context.Background()); err != nil {
		t.Fatalf("PublicarPendentes: %v", err)
	}

	// A publicação deu certo e a confirmação não: o evento foi para a fila e
	// continua pendente no banco — o próximo lease republica (é o
	// "pelo menos uma vez"). O que este teste prova é a contagem da etapa.
	esperarRetry(t, endpointDaMetrica(t, metricas), "outbox_mark")
}

func TestMetricaRetryDaEtapaReferenceClaim(t *testing.T) {
	metricas := observability.NewMetrics()
	uow := &uowTxFake{registro: &registrador{}, erro: errors.New("banco fora")}

	workerRef := NovoReferenceWorker(ReferenceWorkerOptions{
		UOW:       uow,
		Retomador: &retomadorFake{registro: &registrador{}},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:   metricas,
		Config: &config.Config{Reference: config.ReferenceConfig{
			PollInterval:  10 * time.Millisecond,
			BatchSize:     10,
			MaxAttempts:   10,
			BaseBackoff:   time.Second,
			MaxBackoff:    time.Minute,
			PendingMaxAge: time.Minute,
			ReaperEvery:   time.Minute,
		}},
	})

	ctx, cancel := context.WithCancel(context.Background())
	parou := make(chan struct{})
	go func() { defer close(parou); _ = workerRef.Run(ctx) }()

	esperarRetry(t, endpointDaMetrica(t, metricas), "reference_claim")

	cancel()
	<-parou
}

func TestMetricaRetryDaEtapaReaperScan(t *testing.T) {
	metricas := observability.NewMetrics()
	uow := &uowTxFake{registro: &registrador{}, erro: errors.New("banco fora")}

	reaper := NovoPendingReaper(PendingReaperOptions{
		UOW:       uow,
		Retomador: &retomadorFake{registro: &registrador{}},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:   metricas,
		Config: &config.Config{Reference: config.ReferenceConfig{
			PollInterval:  time.Second,
			BatchSize:     10,
			MaxAttempts:   10,
			BaseBackoff:   time.Second,
			MaxBackoff:    time.Minute,
			ReaperEvery:   10 * time.Millisecond,
			PendingMaxAge: time.Minute,
		}},
	})

	ctx, cancel := context.WithCancel(context.Background())
	parou := make(chan struct{})
	go func() { defer close(parou); _ = reaper.Run(ctx) }()

	esperarRetry(t, endpointDaMetrica(t, metricas), "reaper_scan")

	cancel()
	<-parou
}

// O runner reinicia um laço que morreu e CONTA o reinício com o nome do laço.
//
// É a etapa que o painel usa para responder "alguma réplica está reiniciando
// worker em laço?" — sem ela, um laço morrendo a cada 2s apareceria só no log.
func TestMetricaRetryDoLacoPeloRunner(t *testing.T) {
	metricas := observability.NewMetrics()
	instavel := &lacoFake{nome: "instavel", duracao: time.Millisecond, erro: errors.New("banco fora")}

	runner := NovoRunner(RunnerOptions{
		Lacos:   []Laco{instavel},
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics: metricas,
		Config:  &config.Config{Workers: config.WorkersConfig{ShutdownGrace: 100 * time.Millisecond}},
	})

	ctx, cancel := context.WithCancel(context.Background())
	parou := make(chan struct{})
	go func() { defer close(parou); _ = runner.Run(ctx) }()

	esperarRetry(t, endpointDaMetrica(t, metricas), "worker_instavel")

	cancel()
	<-parou
}
