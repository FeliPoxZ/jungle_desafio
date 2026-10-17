package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
)

// lacoFake é um laço controlável: bloqueia até o cancelamento, ou morre depois
// de um tempo com o erro configurado.
type lacoFake struct {
	nome      string
	execucoes atomic.Int32
	duracao   time.Duration
	erro      error
}

func (l *lacoFake) Nome() string { return l.nome }

func (l *lacoFake) Run(ctx context.Context) error {
	l.execucoes.Add(1)

	if l.duracao <= 0 {
		<-ctx.Done()
		return nil
	}

	select {
	case <-time.After(l.duracao):
		return l.erro
	case <-ctx.Done():
		return nil
	}
}

func novoRunnerDeTeste(t *testing.T, lacos ...Laco) *Runner {
	t.Helper()

	return NovoRunner(RunnerOptions{
		Lacos:   lacos,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics: observability.NewMetrics(),
		Config:  &config.Config{Workers: config.WorkersConfig{ShutdownGrace: 100 * time.Millisecond}},
	})
}

// esperarPor repete a condição até o prazo, para o teste não depender de um
// `sleep` mágico.
func esperarPor(t *testing.T, descricao string, prazo time.Duration, condicao func() bool) {
	t.Helper()

	limite := time.Now().Add(prazo)
	for time.Now().Before(limite) {
		if condicao() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condição não atendida no prazo: %s", descricao)
}

// O runner sobe todos os laços e o readiness fica UP enquanto eles rodam.
func TestRunnerSobeTodosOsLacos(t *testing.T) {
	primeiro := &lacoFake{nome: "primeiro"}
	segundo := &lacoFake{nome: "segundo"}
	runner := novoRunnerDeTeste(t, primeiro, segundo)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	parou := make(chan struct{})
	go func() { defer close(parou); _ = runner.Run(ctx) }()

	esperarPor(t, "os dois laços rodando", time.Second, func() bool {
		return primeiro.execucoes.Load() == 1 && segundo.execucoes.Load() == 1
	})

	if err := runner.CheckReadiness(context.Background()); err != nil {
		t.Fatalf("readiness deveria estar UP com os laços rodando: %v", err)
	}

	cancel()
	<-parou
}

// Um laço que morre é REINICIADO — a API não cai por causa dele.
func TestRunnerReiniciaLacoQueMorreu(t *testing.T) {
	// O runner usa backoff de pelo menos 2s entre reinícios; o teste só precisa
	// ver a SEGUNDA execução começar.
	instavel := &lacoFake{nome: "instavel", duracao: time.Millisecond, erro: errors.New("banco fora")}
	runner := novoRunnerDeTeste(t, instavel)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = runner.Run(ctx) }()

	esperarPor(t, "o laço ser reiniciado", 3*time.Second, func() bool {
		return instavel.execucoes.Load() >= 2
	})
}

// Enquanto o laço está morto (no intervalo do reinício), o readiness é DOWN.
//
// É a razão de o runner existir no readiness: responder UP com o publicador
// parado esconderia uma outbox que não publica.
func TestRunnerReadinessDownComLacoMorto(t *testing.T) {
	morto := &lacoFake{nome: "morto", duracao: time.Millisecond, erro: errors.New("falhou")}
	runner := novoRunnerDeTeste(t, morto)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = runner.Run(ctx) }()

	// A condição espera o laço ter RODADO e morrido, e não apenas "readiness
	// DOWN": o runner começa com todos os laços em "ainda não iniciado", então
	// DOWN é o estado inicial também.
	esperarPor(t, "o readiness acusar o laço parado por falha", 2*time.Second, func() bool {
		err := runner.CheckReadiness(context.Background())
		return err != nil && strings.Contains(err.Error(), "falhou")
	})

	err := runner.CheckReadiness(context.Background())
	if err == nil {
		t.Fatal("readiness deveria estar DOWN")
	}
	// A mensagem tem de dizer QUAL laço está parado, e por quê.
	if texto := err.Error(); !strings.Contains(texto, "morto") || !strings.Contains(texto, "falhou") {
		t.Errorf("a mensagem deveria identificar o laço e o motivo; veio: %s", texto)
	}
}

// Sem laço nenhum, o readiness responde UP: não há promessa a cumprir.
func TestRunnerSemLacosRespondeUP(t *testing.T) {
	runner := novoRunnerDeTeste(t)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	if err := runner.Run(ctx); err != nil {
		t.Fatalf("Run sem laços: %v", err)
	}
	if err := runner.CheckReadiness(context.Background()); err != nil {
		t.Fatalf("readiness sem laços deveria ser UP: %v", err)
	}
}

// O runner espera o laço em voo terminar depois do cancelamento.
func TestRunnerEsperaOTrabalhoEmVooNoEncerramento(t *testing.T) {
	// Este laço demora para voltar depois do cancelamento: ele simula a
	// transação em andamento.
	lento := &lacoFakeLento{nome: "lento"}
	runner := novoRunnerDeTeste(t, lento)

	ctx, cancel := context.WithCancel(context.Background())

	parou := make(chan struct{})
	go func() { defer close(parou); _ = runner.Run(ctx) }()

	esperarPor(t, "o laço começar", time.Second, func() bool { return lento.comecou.Load() })

	cancel()

	select {
	case <-parou:
		// O `Run` só voltou porque o laço terminou — é o que o encerramento
		// precisa: ninguém sai do processo com transação aberta.
		if !lento.terminou.Load() {
			t.Error("o runner voltou antes de o laço em voo terminar")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("o runner não voltou depois do cancelamento")
	}
}

// lacoFakeLento demora um pouco para respeitar o cancelamento.
type lacoFakeLento struct {
	nome     string
	comecou  atomic.Bool
	terminou atomic.Bool
}

func (l *lacoFakeLento) Nome() string { return l.nome }

func (l *lacoFakeLento) Run(ctx context.Context) error {
	l.comecou.Store(true)
	<-ctx.Done()
	time.Sleep(50 * time.Millisecond)
	l.terminou.Store(true)
	return nil
}

// O prazo de encerramento vem da configuração.
func TestRunnerGraceVemDaConfig(t *testing.T) {
	runner := NovoRunner(RunnerOptions{
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics: observability.NewMetrics(),
		Config:  &config.Config{Workers: config.WorkersConfig{ShutdownGrace: 7 * time.Second}},
	})

	if runner.Grace() != 7*time.Second {
		t.Errorf("Grace = %v, esperado 7s da config", runner.Grace())
	}
}
