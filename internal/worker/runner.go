package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
)

// NomeDoConjunto é como os laços aparecem no readiness.
const NomeDoConjunto = "workers"

const (
	// reinicioBase e reinicioTeto limitam o intervalo entre reinícios de um laço
	// que morreu.
	//
	// Reiniciar na hora martelaria o recurso que acabou de falhar (banco fora do
	// ar, por exemplo) e encheria o log; o teto evita que a instância fique tanto
	// tempo sem tentar que pareça morta.
	reinicioBase = 2 * time.Second
	reinicioTeto = time.Minute
)

// Laco é um laço supervisionado: nome (para log, métrica e readiness) e um Run
// que só termina quando o contexto é cancelado (ou quando quebra).
type Laco interface {
	Nome() string
	Run(ctx context.Context) error
}

// RunnerOptions reúne as dependências do runner.
type RunnerOptions struct {
	// Lacos são os laços LIGADOS pela configuração. Vazio significa "esta
	// instância não publica e não retoma nada", e o readiness responde UP: não há
	// promessa a cumprir.
	Lacos   []Laco
	Logger  *slog.Logger
	Metrics *observability.Metrics
	Config  *config.Config
}

// Runner é a supervisão dos laços.
//
// # Por que um supervisor, e não um `go` por worker
//
// Um laço que morre por pânico ou por erro não tratado **derruba o processo**
// inteiro — e derrubar a API porque o publicador de eventos teve um problema é o
// pior trade-off possível: quem chama a API não tem culpa, e a operação
// financeira continua chegando. Com supervisão, o laço é reiniciado com backoff,
// o incidente aparece no log e na métrica, e o README §3.5 ("encerramento
// abrupto") não vira indisponibilidade total.
//
// O runner também é quem sabe o ESTADO de cada laço, e é isso que o readiness
// consulta: um `/health/ready` que responde UP com o publicador morto está
// mentindo — o processo deveria estar publicando e não está.
type Runner struct {
	lacos   []Laco
	logger  *slog.Logger
	metrics *observability.Metrics
	grace   time.Duration
	rand    *rand.Rand

	mu      sync.Mutex
	estados map[string]estadoDoLaco
}

// estadoDoLaco é o que o readiness e o log precisam saber de um laço.
type estadoDoLaco struct {
	rodando    bool
	ultimoErro string
}

// NovoRunner monta o runner.
func NovoRunner(opts RunnerOptions) *Runner {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	grace := opts.Config.Workers.ShutdownGrace
	if grace <= 0 {
		grace = 10 * time.Second
	}

	estados := make(map[string]estadoDoLaco, len(opts.Lacos))
	for _, l := range opts.Lacos {
		// Estado inicial "parado, ainda não iniciado": se o readiness for
		// consultado antes do start (não deveria, mas...), ele responde DOWN em
		// vez de fingir que está tudo bem.
		estados[l.Nome()] = estadoDoLaco{rodando: false, ultimoErro: "ainda não iniciado"}
	}

	return &Runner{
		lacos:   opts.Lacos,
		logger:  logger,
		metrics: opts.Metrics,
		grace:   grace,
		rand:    rand.New(rand.NewSource(time.Now().UnixNano())),
		estados: estados,
	}
}

// Name identifica o conjunto no relatório do readiness.
//
// É `Name` (e não `Nome`, como nos laços) porque é o nome que o
// `httpapi.HealthChecker` exige — a interface do readiness é do pacote HTTP, e o
// runner a satisfaz por estrutura, sem importá-la.
func (r *Runner) Name() string { return NomeDoConjunto }

// Lacos devolve os nomes dos laços ligados (para o log de boot e o teste).
func (r *Runner) Lacos() []string {
	nomes := make([]string, 0, len(r.lacos))
	for _, l := range r.lacos {
		nomes = append(nomes, l.Nome())
	}
	return nomes
}

// Grace é o prazo de encerramento dos laços.
func (r *Runner) Grace() time.Duration { return r.grace }

// Run sobe todos os laços e só volta quando o contexto é cancelado e todos
// terminaram.
func (r *Runner) Run(ctx context.Context) error {
	if len(r.lacos) == 0 {
		r.logger.Info("workers_desligados", slog.String("motivo", "nenhum laço habilitado pela configuração"))
		return nil
	}

	r.logger.Info("workers_iniciados",
		slog.Any("lacos", r.Lacos()),
		slog.Duration("shutdownGrace", r.grace),
	)

	var emVoo sync.WaitGroup
	for _, l := range r.lacos {
		emVoo.Add(1)
		go func(l Laco) {
			defer emVoo.Done()
			r.supervisionar(ctx, l)
		}(l)
	}

	emVoo.Wait()
	r.logger.Info("workers_encerrados")
	return nil
}

// supervisionar mantém um laço rodando, reiniciando-o com backoff quando ele
// quebra.
//
// "Quebrou" é qualquer retorno antes do cancelamento do contexto — inclusive
// `nil`. Um laço que voltou sem que ninguém tenha pedido para parar morreu, e
// deixá-lo morto seria aceitar que o processo pare de publicar em silêncio.
func (r *Runner) supervisionar(ctx context.Context, l Laco) {
	falhas := 0

	for {
		if ctx.Err() != nil {
			r.marcar(l.Nome(), estadoDoLaco{rodando: false, ultimoErro: "encerrado"})
			return
		}

		r.marcar(l.Nome(), estadoDoLaco{rodando: true})

		err := l.Run(ctx)
		if ctx.Err() != nil {
			r.marcar(l.Nome(), estadoDoLaco{rodando: false, ultimoErro: "encerrado"})
			return
		}

		falhas++

		motivo := "terminou sem erro"
		if err != nil {
			motivo = err.Error()
		}
		// Um laço pode devolver `context.Canceled` em vez de nil quando o
		// chamador cancelou no meio de uma operação. Nos dois casos ele parou, e é
		// isso que o readiness precisa ver — só o texto do motivo muda.
		if errors.Is(err, context.Canceled) {
			motivo = "cancelado"
		}

		r.marcar(l.Nome(), estadoDoLaco{rodando: false, ultimoErro: motivo})
		r.metrics.IncRetry("worker_" + l.Nome())

		espera := esperaComBackoff(r.rand, falhas, reinicioBase, reinicioTeto)
		r.logger.Error("worker_morreu_reiniciando",
			slog.String("laco", l.Nome()),
			slog.Int("falhas", falhas),
			slog.String("error", motivo),
			slog.Duration("reiniciaEm", espera),
		)

		dormir(ctx, espera)
	}
}

// marcar atualiza o estado de um laço.
func (r *Runner) marcar(nome string, estado estadoDoLaco) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.estados[nome] = estado
}

// CheckReadiness informa se os laços que DEVERIAM estar rodando estão rodando.
//
// # O que ele NÃO faz
//
// Não verifica o banco nem o broker: isso é do PostgreSQL e do SQS, que já estão
// no readiness com nome próprio. Aqui a pergunta é outra, e mais estreita: "o
// processo está fazendo o que a configuração manda?". Sem esta pergunta, ligar o
// publisher e ter o laço morto produziria um readiness UP e uma outbox parada —
// o pior par possível, porque o orquestrador mantém a instância na rotação
// exatamente enquanto ela não publica.
//
// Durante um REINÍCIO (backoff), o laço está parado e o readiness fica DOWN: é
// honesto e passageiro. Um DOWN breve por instância em reinício é preferível a um
// UP permanente com o trabalho parado.
func (r *Runner) CheckReadiness(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	parados := make([]string, 0, len(r.estados))
	for nome, estado := range r.estados {
		if !estado.rodando {
			parados = append(parados, nome+" ("+estado.ultimoErro+")")
		}
	}
	if len(parados) == 0 {
		return nil
	}
	return fmt.Errorf("laços parados: %s", strings.Join(parados, ", "))
}
