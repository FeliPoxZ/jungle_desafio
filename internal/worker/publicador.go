package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
)

// NomeDoPublicador identifica o laço no log, nas métricas e no readiness.
const NomeDoPublicador = "outbox-publisher"

// intervaloDasEstatisticas é de quanto em quanto tempo o atraso da outbox é
// medido.
//
// `PendingStats` é um `COUNT` mais um `MIN` sobre os pendentes: não é caro, mas
// rodá-lo a cada ciclo (1s por padrão) seria uma consulta por segundo por
// instância só para alimentar um gauge que o operador lê em minutos. O atraso da
// outbox é um sinal de MINUTOS, não de segundos.
const intervaloDasEstatisticas = 15 * time.Second

// Evento é um evento pendente da outbox, no formato que o publicador precisa.
//
// `Payload` é o snapshot IMUTÁVEL gravado na coluna JSONB — o mesmo conjunto de
// bytes que o caso de uso serializou no commit. O publicador NÃO reconstrói o
// evento a partir das colunas: se reconstruísse, uma versão nova do código
// publicaria um payload diferente do que foi confirmado no banco, e a outbox
// deixaria de ser "o evento daquele instante" para virar "a interpretação de
// hoje sobre o passado".
type Evento struct {
	EventID     string
	AggregateID string
	Tipo        string
	Payload     []byte
	OccurredAt  time.Time
	Tentativas  int
}

// Publicador é a porta do destino dos eventos. Implementada por
// `internal/infra/sqs`.
//
// Devolver o erro CRU do broker é de propósito: quem decide o que é definitivo é
// `ErrEventoRecusado`, que o adaptador usa para marcar a recusa permanente. Um
// erro desconhecido é tratado como transitório.
type Publicador interface {
	Publicar(ctx context.Context, e Evento) error
}

// ErrEventoRecusado marca a falha PERMANENTE de publicação: o destino recusou o
// evento, e repetir não vai mudar a resposta.
//
// O adaptador do SQS o devolve embrulhado quando o broker responde
// `InvalidParameterValue`, `InvalidMessageContents` ou `MessageTooLong` — que são
// os casos em que a mensagem em si é o problema (corpo grande demais, caractere
// que o SQS não aceita). Sem essa distinção, um evento malformado gastaria as
// dez tentativas antes de parar.
var ErrEventoRecusado = errors.New("worker: evento recusado pelo destino")

// OutboxPublisherOptions reúne as dependências do publicador.
type OutboxPublisherOptions struct {
	// UOW é o Unit of Work do banco: é por ele que o lote é reivindicado e o
	// desfecho gravado.
	UOW        ports.UnitOfWork
	Publicador Publicador
	Logger     *slog.Logger
	Metrics    *observability.Metrics
	Config     *config.Config
}

// OutboxPublisher publica os eventos pendentes da outbox.
//
// # O laço, em uma frase
//
//	reivindica um lote (transação curta) → publica (fora da transação) →
//	marca como publicado / reagenda com backoff
type OutboxPublisher struct {
	uow        ports.UnitOfWork
	publicador Publicador
	logger     *slog.Logger
	metrics    *observability.Metrics
	cfg        config.OutboxConfig

	// rand é o gerador do jitter. Fica no struct (e não global) para o teste
	// poder fixar a sequência e o resultado ser determinístico.
	rand *rand.Rand

	// estatisticasEm é o instante da última medição do atraso da outbox.
	estatisticasEm time.Time
}

// NovoOutboxPublisher monta o publicador.
func NovoOutboxPublisher(opts OutboxPublisherOptions) *OutboxPublisher {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &OutboxPublisher{
		uow:        opts.UOW,
		publicador: opts.Publicador,
		logger:     logger,
		metrics:    opts.Metrics,
		cfg:        opts.Config.Outbox,
		rand:       rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Nome identifica o laço para o runner e o readiness.
func (p *OutboxPublisher) Nome() string { return NomeDoPublicador }

// Run publica até o contexto ser cancelado.
//
// # A ordem, e por que ela não pode ser invertida
//
//  1. ClaimBatch    → o lote é reivindicado e o LEASE é confirmado no banco;
//  2. Publicar      → a mensagem vai para a fila, FORA da transação;
//  3. MarkPublished → o evento sai dos pendentes, com `context.WithoutCancel`.
//
// **Publicar antes de marcar, sempre.** A ordem inversa diria "publiquei" antes
// de publicar: uma queda entre as duas etapas perderia o evento para sempre, e o
// README §5.4 é explícito — evento externo só é publicado depois do commit, mas
// também não pode sumir. Nesta ordem, a pior falha possível é a REPUBLICAÇÃO: o
// evento continua pendente, o lease vence e outra instância (ou esta) publica de
// novo, com o MESMO `eventId` no corpo. A entrega é "pelo menos uma vez", e quem
// deduplica é o consumidor, pelo `eventId`.
//
// # Por que a publicação fica fora da transação
//
// O `ClaimBatch` roda numa transação curta e faz COMMIT antes de qualquer ida à
// rede. Manter a transação aberta durante a publicação seguraria as linhas
// (`FOR UPDATE`) e uma conexão do pool durante um round-trip ao broker — com
// várias instâncias, o pool acabaria antes dos publishers. Além disso, o lease
// precisa estar CONFIRMADO: se o processo morrer no meio da publicação, é o lease
// gravado que faz o evento voltar para outra instância.
//
// O que acontece com o que ficou em voo no encerramento: nada é "liberado" à
// força. O evento permanece reivindicado até o lease vencer e volta para a fila
// de trabalho — a recuperação de trabalho abandonado é a mesma para queda, para
// SIGTERM e para erro.
func (p *OutboxPublisher) Run(ctx context.Context) error {
	p.logger.InfoContext(ctx, "outbox_publisher_iniciado",
		slog.Duration("pollInterval", p.pollInterval()),
		slog.Int("batchSize", int(p.cfg.BatchSize)),
		slog.Duration("lease", p.lease()),
		slog.Int("maxAttempts", int(p.cfg.MaxAttempts)),
	)

	for {
		if ctx.Err() != nil {
			break
		}

		publicados, err := p.PublicarPendentes(ctx)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			// Falha de banco (o SQS fora do ar NÃO cai aqui: ele falha na
			// publicação, evento a evento). O laço continua: o runner não deve
			// reiniciar o worker por um banco momentaneamente indisponível, e a
			// espera evita transformar a indisponibilidade em laço quente.
			p.logger.ErrorContext(ctx, "outbox_publisher_ciclo_falhou", slog.String("error", err.Error()))
			p.metrics.IncRetry("outbox_claim")
			dormir(ctx, p.pollInterval())
			continue
		}

		// Lote vazio: espera o intervalo de polling. Lote cheio: volta na hora,
		// porque provavelmente ainda há trabalho (a outbox é uma fila).
		if publicados == 0 {
			dormir(ctx, p.pollInterval())
		}
	}

	p.logger.Info("outbox_publisher_encerrado")
	return nil
}

// PublicarPendentes reivindica e publica UM lote. Devolve quantos eventos foram
// publicados.
//
// É exportado de propósito: é o ciclo do laço, e é ele que o teste de integração
// chama para publicar uma quantidade determinística de eventos contra o SQS real.
// `Run` é este ciclo em laço, com o intervalo de polling.
func (p *OutboxPublisher) PublicarPendentes(ctx context.Context) (int, error) {
	agora := time.Now().UTC()

	lote, err := p.reivindicar(ctx, agora)
	if err != nil {
		return 0, err
	}

	publicados := 0
	for _, ev := range lote {
		if ctx.Err() != nil {
			// Encerrando: o que sobrou do lote volta pelo lease. Publicar agora
			// seria trabalho que o shutdown não tem prazo para confirmar no
			// banco (o `MarkPublished` ficaria sem tempo hábil).
			break
		}
		if p.publicarUm(ctx, ev) {
			publicados++
		}
	}

	p.estatisticas(ctx, agora)
	return publicados, nil
}

// reivindicar pega um lote de eventos disponíveis, numa transação curta.
func (p *OutboxPublisher) reivindicar(ctx context.Context, agora time.Time) ([]ports.OutboxEvent, error) {
	var lote []ports.OutboxEvent

	err := p.uow.Run(ctx, func(ctx context.Context, r ports.Repos) error {
		var e error
		lote, e = r.Outbox.ClaimBatch(ctx, int(p.cfg.BatchSize), p.lease(), agora)
		return e
	})
	if err != nil {
		return nil, fmt.Errorf("worker: reivindicando o lote da outbox: %w", err)
	}
	return lote, nil
}

// publicarUm publica um evento e registra o desfecho. Devolve true se publicou.
func (p *OutboxPublisher) publicarUm(ctx context.Context, ev ports.OutboxEvent) bool {
	// `context.WithoutCancel`: a publicação é o TRABALHO em voo. Se o SIGTERM
	// chegar agora, abortar a chamada ao broker deixaria a dúvida "a mensagem
	// entrou ou não?" — e as duas respostas são seguras (se entrou, o evento
	// continua pendente e o consumidor deduplica; se não entrou, ele será
	// republicado), mas a dúvida não é grátis. O prazo de encerramento é do
	// runner, que espera o lote e depois desiste.
	ctxPublicacao := context.WithoutCancel(ctx)

	err := p.publicador.Publicar(ctxPublicacao, Evento{
		EventID:     ev.EventID,
		AggregateID: ev.AggregateID,
		Tipo:        ev.EventType,
		Payload:     ev.Payload,
		OccurredAt:  ev.OccurredAt,
		Tentativas:  ev.Attempts,
	})

	if err != nil {
		p.registrarFalha(ctx, ev, err)
		return false
	}

	p.marcarPublicado(ctx, ev)
	return true
}

// marcarPublicado tira o evento da fila de pendentes.
//
// `context.WithoutCancel` aqui tem um motivo diferente do da publicação: a
// mensagem JÁ ESTÁ na fila. Perder esta confirmação por causa de um sinal de
// término custaria uma republicação desnecessária — e a republicação é segura,
// mas não é de graça (a fila FIFO entrega de novo, o consumidor deduplica, o
// operador vê "duplicata" no painel).
//
// Falha aqui NÃO é fatal: o evento continua pendente, o lease vence e ele é
// republicado. O que se perde é a garantia de "uma vez só" — que nunca foi
// prometida: a entrega é "pelo menos uma vez".
func (p *OutboxPublisher) marcarPublicado(ctx context.Context, ev ports.OutboxEvent) {
	inicio := time.Now()

	err := p.uow.Run(context.WithoutCancel(ctx), func(ctx context.Context, r ports.Repos) error {
		return r.Outbox.MarkPublished(ctx, ev.EventID, time.Now().UTC())
	})
	if err != nil {
		p.logger.ErrorContext(ctx, "outbox_mark_published_falhou",
			slog.String("eventId", ev.EventID),
			slog.String("eventType", ev.EventType),
			slog.String("error", err.Error()),
		)
		p.metrics.IncRetry("outbox_mark")
		return
	}

	p.metrics.ObserveOutboxPublish("success", float64(ev.Attempts+1))
	p.logger.InfoContext(ctx, "outbox_evento_publicado",
		slog.String("eventId", ev.EventID),
		slog.String("eventType", ev.EventType),
		slog.String("aggregateId", ev.AggregateID),
		slog.Int("attempt", ev.Attempts+1),
		slog.Duration("atraso", time.Since(ev.OccurredAt)),
		observability.LogDuration(time.Since(inicio)),
	)
}

// registrarFalha reagenda o evento que não pôde ser publicado.
//
// # Três destinos possíveis, e o custo de errar cada um
//
//   - **recusa permanente do destino** (`ErrEventoRecusado`): repetir não muda a
//     resposta. Vai para a quarentena na primeira detecção;
//   - **tentativas esgotadas** (`MaxAttempts`): também vai para a quarentena,
//     com o motivo registrado;
//   - **falha transitória**: backoff exponencial com teto e jitter.
//
// # Por que "quarentena", e não "desistir"
//
// O evento NÃO é apagado nem marcado como publicado: ele continua pendente, com
// `last_error` preenchido e `available_at` empurrado para frente (o prazo longo
// de `OUTBOX_PARKED_BACKOFF`). Assim ele aparece em `PendingStats` — o atraso da
// outbox denuncia que existe trabalho parado — e um operador consegue
// desquarentená-lo mexendo em `available_at`.
//
// A alternativa "não reagenda e deixa `available_at` no passado" seria um laço
// quente: o `ClaimBatch` reivindicaria o mesmo evento a cada ciclo. A outra
// alternativa — marcar como publicado algo que não foi — é a única realmente
// proibida: diria que o evento saiu quando ele não saiu.
//
// ⚠️ Consequência a conhecer: uma indisponibilidade do SQS mais longa que a
// janela de tentativas (por padrão, 10 tentativas até ~5 min de teto) manda
// eventos para a quarentena. Nada é perdido — o evento está no banco — mas a
// entrega para de acontecer até alguém agir. O teto e o número de tentativas são
// configuráveis justamente por isso.
func (p *OutboxPublisher) registrarFalha(ctx context.Context, ev ports.OutboxEvent, causa error) {
	tentativa := ev.Attempts + 1
	permanente := errors.Is(causa, ErrEventoRecusado)
	esgotado := tentativa >= int(p.cfg.MaxAttempts)

	var (
		espera time.Duration
		motivo string
	)
	switch {
	case permanente:
		espera, motivo = p.parkedBackoff(), "recusado pelo destino"
	case esgotado:
		espera, motivo = p.parkedBackoff(), "tentativas esgotadas"
	default:
		espera, motivo = p.backoff(tentativa), "falha na publicacao"
	}

	// `MarkRetry` incrementa `attempts` NO BANCO (`attempts + 1`): duas
	// instâncias falhando ao mesmo tempo somariam duas falhas, em vez de gravar o
	// mesmo número. Mesma decisão do consumidor.
	proximo := time.Now().UTC().Add(espera)
	razao := motivo + ": " + causa.Error()

	err := p.uow.Run(context.WithoutCancel(ctx), func(ctx context.Context, r ports.Repos) error {
		return r.Outbox.MarkRetry(ctx, ev.EventID, proximo, razao)
	})

	resultado := "retry"
	if permanente || esgotado {
		resultado = "failed"
	}
	p.metrics.ObserveOutboxPublish(resultado, 0)

	if err != nil {
		// Se nem o reagendamento passou, o evento continua com o lease antigo e
		// volta sozinho quando ele vencer. Nada foi perdido; o que houve foi uma
		// falha de banco somada à falha de publicação.
		p.logger.ErrorContext(ctx, "outbox_mark_retry_falhou",
			slog.String("eventId", ev.EventID),
			slog.String("error", err.Error()),
		)
		return
	}

	campos := []any{
		slog.String("eventId", ev.EventID),
		slog.String("eventType", ev.EventType),
		slog.Int("attempt", tentativa),
		slog.Duration("proximaEm", espera),
		slog.String("error", causa.Error()),
	}

	if permanente || esgotado {
		p.logger.ErrorContext(ctx, "outbox_evento_em_quarentena", campos...)
		return
	}
	p.logger.WarnContext(ctx, "outbox_publicacao_reagendada", campos...)
}

// estatisticas publica o atraso da outbox e a contagem de pendentes.
//
// É a métrica que responde "a outbox está parada?" (README §12: "atraso da
// outbox") e a idade do pendente mais antigo. Sem ela, um publicador
// silenciosamente parado só apareceria quando alguém reparasse na falta do
// evento — que é o pior jeito de descobrir.
func (p *OutboxPublisher) estatisticas(ctx context.Context, agora time.Time) {
	if !p.estatisticasEm.IsZero() && agora.Sub(p.estatisticasEm) < intervaloDasEstatisticas {
		return
	}
	p.estatisticasEm = agora

	var (
		pendentes int
		idade     time.Duration
	)
	err := p.uow.RunReadOnly(ctx, func(ctx context.Context, r ports.Repos) error {
		var e error
		pendentes, idade, e = r.Outbox.PendingStats(ctx, agora)
		return e
	})
	if err != nil {
		p.logger.ErrorContext(ctx, "outbox_estatisticas_falharam", slog.String("error", err.Error()))
		return
	}

	p.metrics.SetOutboxPendingAge(idade)

	if pendentes == 0 {
		p.logger.DebugContext(ctx, "outbox_sem_pendentes")
		return
	}

	// Um evento pendente há mais tempo que um lease inteiro significa que pelo
	// menos um ciclo completo passou sem publicação confirmada — pode ser retry
	// legítimo (destino fora do ar) ou quarentena. Nos dois casos o operador
	// precisa saber.
	if idade > p.lease() {
		p.logger.WarnContext(ctx, "outbox_atrasada",
			slog.Int("pendentes", pendentes),
			slog.Duration("idadeDoMaisAntigo", idade),
		)
		return
	}
	p.logger.DebugContext(ctx, "outbox_pendentes",
		slog.Int("pendentes", pendentes),
		slog.Duration("idadeDoMaisAntigo", idade),
	)
}

// pollInterval é o intervalo entre ciclos, com o default defensável.
func (p *OutboxPublisher) pollInterval() time.Duration {
	if p.cfg.PollInterval <= 0 {
		return time.Second
	}
	return p.cfg.PollInterval
}

// lease é por quanto tempo o lote reivindicado fica fora do alcance das outras
// instâncias.
func (p *OutboxPublisher) lease() time.Duration {
	if p.cfg.Lease <= 0 {
		return 30 * time.Second
	}
	return p.cfg.Lease
}

// backoff é a espera até a próxima tentativa de publicação.
func (p *OutboxPublisher) backoff(tentativa int) time.Duration {
	return esperaComBackoff(p.rand, tentativa, p.cfg.BaseBackoff, p.cfg.MaxBackoff)
}

// parkedBackoff é o prazo da quarentena: longo o bastante para não girar, curto
// o bastante para o evento poder voltar sozinho quando o problema passar.
func (p *OutboxPublisher) parkedBackoff() time.Duration {
	if p.cfg.ParkedBackoff <= 0 {
		return time.Hour
	}
	return p.cfg.ParkedBackoff
}
