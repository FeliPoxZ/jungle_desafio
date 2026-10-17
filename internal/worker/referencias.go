package worker

import (
	"context"
	"log/slog"
	"math/rand"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
)

// Nomes dos laços desta fase.
const (
	// NomeDoWorkerDeReferencias identifica o laço que resolve PENDING_REFERENCE.
	NomeDoWorkerDeReferencias = "reference-worker"

	// NomeDoReaper identifica o laço que retoma transações presas em PENDING.
	NomeDoReaper = "pending-reaper"
)

// Retomador é o contrato do caso de uso que os dois laços usam.
//
// A interface é declarada AQUI (no consumidor), e não no caso de uso: é o que o
// worker precisa, e nada além. O teste do worker roda com um dublê que registra
// as chamadas — inclusive as que NÃO devem acontecer.
type Retomador interface {
	Retomar(ctx context.Context, req appwagering.RetomarRequest) (appwagering.Retomada, error)
}

// ReferenceWorker resolve as operações que ficaram esperando uma referência.
//
// # O que ele NÃO faz
//
// Ele não decide a regra de negócio: quem diz se a referência serve, se a
// reversão é possível e qual o estado final é o caso de uso (`Service.Retomar`),
// o MESMO que atende HTTP e SQS. Aqui mora só o laço: quando buscar, quanto
// esperar entre as tentativas e o que registrar.
type ReferenceWorker struct {
	uow       ports.UnitOfWork
	retomador Retomador
	logger    *slog.Logger
	metrics   *observability.Metrics
	cfg       config.ReferenceConfig
	rand      *rand.Rand
}

// ReferenceWorkerOptions reúne as dependências do worker.
type ReferenceWorkerOptions struct {
	UOW       ports.UnitOfWork
	Retomador Retomador
	Logger    *slog.Logger
	Metrics   *observability.Metrics
	Config    *config.Config
}

// NovoReferenceWorker monta o worker.
func NovoReferenceWorker(opts ReferenceWorkerOptions) *ReferenceWorker {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &ReferenceWorker{
		uow:       opts.UOW,
		retomador: opts.Retomador,
		logger:    logger,
		metrics:   opts.Metrics,
		cfg:       opts.Config.Reference,
		rand:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Nome identifica o laço para o runner e o readiness.
func (w *ReferenceWorker) Nome() string { return NomeDoWorkerDeReferencias }

// Run retoma as referências vencidas até o contexto ser cancelado.
//
// O laço busca um lote, processa e volta a buscar. Sem lote, espera o intervalo
// de polling: uma tabela vazia consultada em laço quente gastaria o banco inteiro
// para não fazer nada.
func (w *ReferenceWorker) Run(ctx context.Context) error {
	w.logger.InfoContext(ctx, "reference_worker_iniciado",
		slog.Duration("pollInterval", w.pollInterval()),
		slog.Int("batchSize", int(w.cfg.BatchSize)),
		slog.Int("maxAttempts", int(w.cfg.MaxAttempts)),
		slog.Duration("pendingMaxAge", w.cfg.PendingMaxAge),
	)

	for {
		if ctx.Err() != nil {
			break
		}

		processadas, err := w.RetomarVencidas(ctx)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			w.logger.ErrorContext(ctx, "reference_worker_ciclo_falhou", slog.String("error", err.Error()))
			w.metrics.IncRetry("reference_claim")
			dormir(ctx, w.pollInterval())
			continue
		}

		if processadas == 0 {
			dormir(ctx, w.pollInterval())
		}
	}

	w.logger.Info("reference_worker_encerrado")
	return nil
}

// RetomarVencidas reivindica e retoma UM lote. Devolve quantas transações foram
// processadas.
//
// Exportado de propósito: é o ciclo do laço, e é ele que o teste de integração
// chama para processar uma quantidade determinística de pendências contra o
// PostgreSQL real.
func (w *ReferenceWorker) RetomarVencidas(ctx context.Context) (int, error) {
	agora := time.Now().UTC()

	var lote []*wagerdomain.WagerTransaction
	err := w.uow.Run(ctx, func(ctx context.Context, r ports.Repos) error {
		var e error
		lote, e = r.Transactions.ClaimPendingReferencesDue(ctx, int(w.cfg.BatchSize), w.lease(), agora)
		return e
	})
	if err != nil {
		return 0, err
	}

	processadas := 0
	for _, tx := range lote {
		if ctx.Err() != nil {
			// Encerrando: o que sobrou do lote volta pelo lease (o claim já
			// empurrou o instante da próxima tentativa).
			break
		}
		w.retomarUma(ctx, tx, agora)
		processadas++
	}
	return processadas, nil
}

// retomarUma retoma UMA transação e registra o desfecho.
func (w *ReferenceWorker) retomarUma(ctx context.Context, tx *wagerdomain.WagerTransaction, agora time.Time) {
	inicio := time.Now()

	// A tentativa que ACABOU de ser reivindicada é a próxima da linha. O backoff
	// é calculado sobre o número de falhas até aqui, e não sobre o valor lido do
	// banco depois da gravação: o valor do processo é o que descreve ESTA rodada.
	tentativa := tx.ReferenceResolutionAttempt() + 1
	proximo := agora.Add(w.backoff(tentativa))

	// `context.WithoutCancel`: a retomada é o TRABALHO em voo. Abortar a
	// transação no meio, no SIGTERM, deixaria a operação sem desfecho — o oposto
	// do que o encerramento quer. O prazo é do runner, que espera o lote e depois
	// desiste (a linha volta pelo lease).
	retomada, err := w.retomador.Retomar(context.WithoutCancel(ctx), appwagering.RetomarRequest{
		TransactionID: tx.ID(),
		// O que o CLAIM leu. É a guarda contra dois workers agindo sobre a mesma
		// linha: se a tentativa ou o estado mudaram desde o claim, outro chegou
		// primeiro e o caso de uso ignora o pedido.
		EstadoEsperado:    tx.Status(),
		TentativaEsperada: tx.ReferenceResolutionAttempt(),
		Politica: appwagering.PoliticaDeReferencia{
			MaxAttempts:   int(w.cfg.MaxAttempts),
			PendingMaxAge: w.cfg.PendingMaxAge,
			NextAttemptAt: proximo,
		},
	})

	if err != nil {
		// A linha continua reivindicada: ela volta quando o lease vencer. Não é
		// preciso devolver nada explicitamente — o lease É a recuperação.
		w.logger.ErrorContext(ctx, "reference_retomada_falhou",
			slog.String("transactionId", string(tx.ID())),
			slog.String("walletId", string(tx.WalletID())),
			slog.Int("attempt", tentativa),
			slog.String("error", err.Error()),
		)
		return
	}

	w.metrics.ObserveReferenceResolution(string(retomada.Desfecho))

	campos := []any{
		slog.String("transactionId", string(tx.ID())),
		slog.String("walletId", string(tx.WalletID())),
		slog.String("providerId", tx.ProviderID()),
		slog.String("kind", tx.Kind().String()),
		slog.String("desfecho", string(retomada.Desfecho)),
		slog.String("status", retomada.Result.Status.String()),
		slog.Int("attempt", tentativa),
		observability.LogDuration(time.Since(inicio)),
	}

	switch retomada.Desfecho {
	case appwagering.RetomadaResolvida:
		w.logger.InfoContext(ctx, "reference_resolvida", campos...)
	case appwagering.RetomadaPendente:
		campos = append(campos, slog.Time("proximaEm", proximo))
		w.logger.InfoContext(ctx, "reference_ainda_pendente", campos...)
	case appwagering.RetomadaRejeitada:
		campos = append(campos, slog.String("failureCode", retomada.Result.FailureCode))
		if retomada.Motivo != "" {
			campos = append(campos, slog.String("motivo", retomada.Motivo))
		}
		w.logger.WarnContext(ctx, "reference_rejeitada", campos...)
	default:
		// Disputa: outra instância chegou primeiro. É o caminho esperado de dois
		// workers sobre a mesma linha, e É para acontecer — em DEBUG para não
		// poluir o log de quem roda várias instâncias.
		w.logger.DebugContext(ctx, "reference_ignorada", campos...)
	}
}

// --- Reaper ------------------------------------------------------------------

// PendingReaper retoma as transações que ficaram presas em PENDING.
//
// # O que ele protege, exatamente
//
// O caso de uso grava `PENDING` e o estado definitivo no MESMO commit, então hoje
// nenhuma linha PENDING é confirmada — o reaper é a SEGURANÇA EXTRA do README
// §6.3, e não o caminho normal. Ele existe para o dia em que um aceite
// assíncrono aparecer (a alternativa que o enunciado permite em §6.3: "operações
// sem dependências podem ser concluídas de forma síncrona, sem commit
// intermediário de aceite") ou para uma linha presa por correção manual: em
// qualquer um dos casos, uma operação sem desfecho é um fato financeiro pela
// metade.
//
// Ele NÃO inventa um desfecho: chama o MESMO `Retomar` do worker de referências,
// com a política do worker. Se a operação depende de uma referência que não
// chegou, ela passa a `PENDING_REFERENCE` e o worker de referências assume o TTL;
// se o desfecho não fecha (dado inconsistente), ela vira `FAILED` para auditoria.
type PendingReaper struct {
	uow       ports.UnitOfWork
	retomador Retomador
	logger    *slog.Logger
	metrics   *observability.Metrics
	cfg       config.ReferenceConfig
	rand      *rand.Rand
}

// PendingReaperOptions reúne as dependências do reaper.
type PendingReaperOptions struct {
	UOW       ports.UnitOfWork
	Retomador Retomador
	Logger    *slog.Logger
	Metrics   *observability.Metrics
	Config    *config.Config
}

// NovoPendingReaper monta o reaper.
func NovoPendingReaper(opts PendingReaperOptions) *PendingReaper {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &PendingReaper{
		uow:       opts.UOW,
		retomador: opts.Retomador,
		logger:    logger,
		metrics:   opts.Metrics,
		cfg:       opts.Config.Reference,
		rand:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Nome identifica o laço para o runner e o readiness.
func (r *PendingReaper) Nome() string { return NomeDoReaper }

// Run varre o banco no intervalo configurado até o contexto ser cancelado.
func (r *PendingReaper) Run(ctx context.Context) error {
	intervalo := r.cfg.ReaperEvery
	if intervalo <= 0 {
		intervalo = time.Minute
	}

	r.logger.InfoContext(ctx, "pending_reaper_iniciado",
		slog.Duration("interval", intervalo),
		slog.Duration("pendingMaxAge", r.cfg.PendingMaxAge),
	)

	ticker := time.NewTicker(intervalo)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("pending_reaper_encerrado")
			return nil
		case <-ticker.C:
			if _, err := r.Varrer(ctx); err != nil {
				if ctx.Err() != nil {
					r.logger.Info("pending_reaper_encerrado")
					return nil
				}
				r.logger.ErrorContext(ctx, "pending_reaper_varredura_falhou", slog.String("error", err.Error()))
				r.metrics.IncRetry("reaper_scan")
			}
		}
	}
}

// Varrer retoma as transações presas em PENDING. Devolve quantas foram retomadas.
//
// Exportado de propósito: o teste de integração precisa provar a retomada de uma
// linha PENDING sem esperar o intervalo do ticker.
func (r *PendingReaper) Varrer(ctx context.Context) (int, error) {
	agora := time.Now().UTC()

	var presas []*wagerdomain.WagerTransaction
	err := r.uow.Run(ctx, func(ctx context.Context, rep ports.Repos) error {
		var e error
		presas, e = rep.Transactions.ListPendingStale(ctx, agora, r.cfg.PendingMaxAge, int(r.cfg.BatchSize))
		return e
	})
	if err != nil {
		return 0, err
	}

	for _, tx := range presas {
		if ctx.Err() != nil {
			break
		}
		r.logger.WarnContext(ctx, "pending_reaper_retomando",
			slog.String("transactionId", string(tx.ID())),
			slog.String("walletId", string(tx.WalletID())),
			slog.Duration("idade", agora.Sub(tx.UpdatedAt())),
		)
		r.retomar(ctx, tx, agora)
	}
	return len(presas), nil
}

// retomar retoma UMA transação presa, com a política de referência.
func (r *PendingReaper) retomar(ctx context.Context, tx *wagerdomain.WagerTransaction, agora time.Time) {
	proximo := agora.Add(r.cfg.BaseBackoff)

	// Sem TTL por idade: a linha está em PENDING (não esperando referência), e
	// quem aplica o TTL da referência é o worker de referências, depois que a
	// operação virar PENDING_REFERENCE. Aplicar o TTL aqui rejeitaria por uma
	// espera que ainda nem começou.
	retomada, err := r.retomador.Retomar(context.WithoutCancel(ctx), appwagering.RetomarRequest{
		TransactionID: tx.ID(),
		// A linha continua em PENDING com esta tentativa? Se outro reaper (ou o
		// próprio caso de uso) já agiu, o pedido é ignorado.
		EstadoEsperado:    tx.Status(),
		TentativaEsperada: tx.ReferenceResolutionAttempt(),
		Politica: appwagering.PoliticaDeReferencia{
			MaxAttempts:   int(r.cfg.MaxAttempts),
			PendingMaxAge: 0,
			NextAttemptAt: proximo,
		},
	})
	if err != nil {
		r.logger.ErrorContext(ctx, "pending_reaper_retomada_falhou",
			slog.String("transactionId", string(tx.ID())),
			slog.String("error", err.Error()),
		)
		return
	}

	r.metrics.ObserveReferenceResolution("reaper_" + string(retomada.Desfecho))
	r.logger.InfoContext(ctx, "pending_reaper_desfecho",
		slog.String("transactionId", string(tx.ID())),
		slog.String("desfecho", string(retomada.Desfecho)),
		slog.String("status", retomada.Result.Status.String()),
		slog.String("failureCode", retomada.Result.FailureCode),
		slog.String("motivo", retomada.Motivo),
	)
}

// --- parâmetros --------------------------------------------------------------

func (w *ReferenceWorker) pollInterval() time.Duration {
	if w.cfg.PollInterval <= 0 {
		return 2 * time.Second
	}
	return w.cfg.PollInterval
}

// lease é por quanto tempo a linha reivindicada fica fora do alcance dos outros
// workers.
//
// É derivado do maior intervalo entre tentativas, e não configurado à parte: um
// lease MENOR que o backoff faria a linha voltar antes de a próxima tentativa
// estar agendada — e vários workers passariam a processar a mesma transação em
// rodadas seguidas, cada um tentando resolver uma referência que ainda não
// chegou. O piso de 30s cobre o tempo de uma transação no banco com folga.
func (w *ReferenceWorker) lease() time.Duration {
	piso := 30 * time.Second
	if w.cfg.MaxBackoff > piso {
		return w.cfg.MaxBackoff
	}
	return piso
}

// backoff é a espera até a próxima tentativa de resolver a referência.
//
// `tentativa-1` é o número de falhas até aqui: a PRIMEIRA re-tentativa espera a
// base, a segunda o dobro, e assim por diante até o teto.
func (w *ReferenceWorker) backoff(tentativa int) time.Duration {
	return esperaComBackoff(w.rand, tentativa-1, w.cfg.BaseBackoff, w.cfg.MaxBackoff)
}
