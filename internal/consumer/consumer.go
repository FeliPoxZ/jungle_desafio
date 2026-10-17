// Package consumer é a política de consumo: quando repetir, quando desistir, o
// que fazer com a mensagem que nunca vai funcionar e como encerrar no SIGTERM.
//
// # Por que a política fica aqui, e não no adaptador do broker
//
// O adaptador (`internal/infra/sqs`) sabe RECEBER e APAGAR. Ele não sabe — e não
// deve saber — que uma falha de banco merece nova tentativa e que um corpo
// ilegível não. Essa distinção é a parte que interessa discutir, e ela precisa ser
// testável sem LocalStack: aqui o broker entra por uma porta (`Fila`), e o teste
// usa um dublê que registra exatamente o que foi pedido.
package consumer

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/inbox"
	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
)

// Intervalos do laço.
const (
	// esperaAposErroDeBroker evita o laço quente quando o `Receive` falha (SQS
	// fora, credencial errada): sem ela, um erro imediato viraria milhares de
	// chamadas por segundo.
	esperaAposErroDeBroker = 2 * time.Second

	// esperaAposLoteVazio só se aplica quando o long polling NÃO está em uso. Com
	// `WaitTimeSeconds > 0` o próprio `Receive` já espera, e dormir mais só
	// atrasaria a próxima entrega.
	esperaAposLoteVazio = 200 * time.Millisecond

	// limiteDoLote é o máximo que o SQS aceita em `ReceiveMessage` e nos lotes.
	limiteDoLote = 10

	// tentativasDeEncerramento é quantas vezes a liberação de visibilidade é
	// repetida no encerramento. O SQS pode recusar por rede; insistir uma vez
	// mais é barato comparado a deixar uma mensagem presa pelo visibility
	// timeout inteiro (60s neste projeto).
	tentativasDeEncerramento = 2
)

// Message é uma mensagem recebida, no formato que o consumidor precisa.
//
// É um tipo PRÓPRIO (e não o do adaptador) para o consumidor não importar o
// adaptador: a conversão acontece na composição.
type Message struct {
	ID            string
	Body          []byte
	ReceiptHandle string
	ReceiveCount  int
	GroupID       string
}

// Fila é a porta do broker. Implementada por `internal/infra/sqs`.
type Fila interface {
	// Receive busca até `max` mensagens, esperando até `waitSeconds` por elas.
	Receive(ctx context.Context, queueURL string, max int32, waitSeconds int32) ([]Message, error)

	// Delete remove mensagens concluídas.
	Delete(ctx context.Context, queueURL string, handles []string) error

	// ChangeVisibility adia a próxima entrega (o retry com backoff).
	ChangeVisibility(ctx context.Context, queueURL string, handles []string, seconds int32) error

	// Send entrega a mensagem em outra fila (a DLQ).
	Send(ctx context.Context, queueURL string, body []byte, groupID, dedupID string) error
}

// Processador é o que decide o destino de uma mensagem.
//
// A interface é declarada aqui (consumidor) e implementada por `app/inbox`.
type Processador interface {
	Processar(ctx context.Context, entrada inbox.Entrada) (inbox.Resultado, error)
}

// Options reúne as dependências do consumidor.
type Options struct {
	Fila        Fila
	Processador Processador
	Logger      *slog.Logger
	Metrics     *observability.Metrics
	Config      *config.Config

	// URLDaFila e URLDaDLQ são as URLs já resolvidas das duas filas.
	//
	// Vêm de fora porque transformar NOME em URL é assunto do adaptador do broker
	// (o formato depende do endpoint): a política de consumo trabalha com
	// endereços prontos. Vazio significa "use o nome da configuração".
	URLDaFila string
	URLDaDLQ  string
}

// Consumidor é o laço de consumo da fila de operações.
type Consumidor struct {
	fila        Fila
	processador Processador
	logger      *slog.Logger
	metrics     *observability.Metrics
	cfg         *config.Config

	// urlDaFila e urlDaDLQ são os endereços usados em toda chamada ao broker.
	urlDaFila string
	urlDaDLQ  string

	// rastreio guarda o que está em voo para o encerramento ser pontual: só o que
	// ainda NÃO foi confirmado precisa ser devolvido à fila.
	rastreio *rastreioDeEntrega

	// rand é o gerador do jitter. Fica no struct (e não global) para os testes
	// poderem fixar a sequência e o resultado ser determinístico.
	rand *rand.Rand
}

// New monta o consumidor.
func New(opts Options) *Consumidor {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	urlDaFila := opts.URLDaFila
	if urlDaFila == "" {
		urlDaFila = opts.Config.SQS.TransactionsQueue
	}
	urlDaDLQ := opts.URLDaDLQ
	if urlDaDLQ == "" {
		urlDaDLQ = opts.Config.SQS.TransactionsDLQ
	}

	return &Consumidor{
		fila:        opts.Fila,
		processador: opts.Processador,
		logger:      logger,
		metrics:     opts.Metrics,
		cfg:         opts.Config,
		urlDaFila:   urlDaFila,
		urlDaDLQ:    urlDaDLQ,
		rastreio:    novoRastreioDeEntrega(),
		rand:        rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Run consome até o contexto ser cancelado.
//
// # O encerramento, passo a passo
//
//  1. o contexto cancela → o `Receive` em andamento volta com erro (ou vazio);
//  2. o laço SAI antes de processar o próximo lote: novas mensagens param de ser
//     buscadas, que é o que o README §10 pede ("pare de buscar trabalho");
//  3. o processamento em andamento é aguardado com um teto (`ShutdownGrace`);
//  4. quem não deu tempo tem a VISIBILIDADE LIBERADA (`ChangeVisibility 0`) e
//     volta para a fila imediatamente, em vez de esperar o visibility timeout
//     inteiro. Reprocessar é seguro (a operação é idempotente); perder, não.
//
// O ponto 3 é o que distingue "encerrar" de "abortar": matar as goroutines no meio
// de uma transação deixaria o trabalho sem desfecho, e o desfecho é do banco.
func (c *Consumidor) Run(ctx context.Context) error {
	c.logger.InfoContext(ctx, "sqs_consumidor_iniciado",
		slog.String("queue", c.urlDaFila),
		slog.Int("concurrency", c.cfg.SQS.ConsumerConcurrency),
		slog.Int("batchSize", int(c.cfg.SQS.BatchSize)),
		slog.Duration("shutdownGrace", c.cfg.SQS.ShutdownGrace),
	)

	var emVoo sync.WaitGroup
	// O semáforo é o teto de trabalho simultâneo. Sem ele, `Receive` de 10 em 10
	// com N instâncias abertas estouraria o pool de conexões do banco — o
	// consumidor viraria o gargalo do próprio serviço.
	semaforo := make(chan struct{}, c.cfg.SQS.ConsumerConcurrency)

	for {
		if ctx.Err() != nil {
			break
		}

		mensagens, err := c.fila.Receive(ctx, c.urlDaFila,
			int32(c.cfg.SQS.BatchSize), int32(c.cfg.SQS.WaitTime.Seconds()))
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			c.logger.ErrorContext(ctx, "sqs_receive_falhou", slog.String("error", err.Error()))
			c.metrics.IncRetry("sqs_receive")
			c.dormir(ctx, esperaAposErroDeBroker)
			continue
		}

		if len(mensagens) == 0 {
			c.dormir(ctx, c.esperaSemLote())
			continue
		}

		c.despachar(ctx, mensagens, semaforo, &emVoo)
	}

	// Parou de buscar. Agora conclui (ou devolve) o que está em voo.
	c.encerrar(&emVoo)
	c.logger.InfoContext(context.WithoutCancel(ctx), "sqs_consumidor_encerrado")
	return nil
}

// despachar envia cada mensagem para uma goroutine, respeitando o semáforo.
func (c *Consumidor) despachar(ctx context.Context, mensagens []Message, semaforo chan struct{}, emVoo *sync.WaitGroup) {

	for _, msg := range mensagens {
		select {
		case semaforo <- struct{}{}:
		case <-ctx.Done():
			// Encerrando: o que já foi recebido e não despachado precisa voltar
			// para a fila AGORA — não há goroutine para concluir isto.
			c.devolver(ctx, msg)
			continue
		}

		entrega := c.rastreio.registrar(msg, time.Now())
		emVoo.Add(1)

		go func() {
			defer emVoo.Done()
			defer func() { <-semaforo }()
			defer c.rastreio.concluir(msg.ReceiptHandle)

			c.tratar(ctx, entrega)
		}()

	}
}

// tratar processa UMA mensagem e decide o destino dela.
func (c *Consumidor) tratar(ctx context.Context, entrega *entregaRastreada) {
	msg := entrega.msg
	inicio := time.Now()

	// O processamento usa um contexto PRÓPRIO, desligado do cancelamento do laço.
	//
	// Por que: quando o SIGTERM chega, o `Run` cancela o contexto de busca. Se o
	// processamento usasse o mesmo contexto, a transação em andamento seria
	// ABORTADA no meio — exatamente o que a etapa 3 do encerramento quer evitar.
	// O teto de tempo é imposto pelo `encerrar`, que espera por um prazo e depois
	// libera a visibilidade.
	ctxProcessamento := context.WithoutCancel(ctx)

	resultado, err := c.processador.Processar(ctxProcessamento, inbox.Entrada{
		// O identificador da ENTREGA (do broker) vai junto para o processador
		// poder conferir que ele casa com o `messageId` do envelope. A identidade
		// durável usada na inbox é a do envelope (README §10).
		IDDaEntrega: msg.ID,
		Corpo:       msg.Body,
	})

	// O desfecho da classificação fica em DEBUG: ele é diagnóstico de bancada
	// (útil quando um teste ou um incidente precisa saber POR QUE a mensagem foi
	// para a DLQ), e em volume de produção seria ruído — o resumo por mensagem já
	// sai em INFO nos caminhos de conclusão, adiamento e DLQ.
	c.logger.DebugContext(ctx, "sqs_mensagem_classificada",
		slog.String("messageId", msg.ID),
		slog.Bool("processada", resultado.Processada),
		slog.Bool("duplicada", resultado.Duplicada),
		slog.Bool("transitoria", resultado.Transitoria),
		slog.String("status", resultado.Status),
		slog.String("erro", erroTexto(err)))

	switch {
	case err != nil:
		// Erro de classificação: transitório volta, permanente vai para a DLQ.
		c.tratarErro(ctx, entrega, err, inicio)
		return

	case resultado.Transitoria:
		// Falha classificada como "repita", ou conflito de unicidade (no nosso
		// schema, unicidade violada quer dizer "alguém chegou primeiro").
		c.adiar(ctx, entrega, "transitorio", resultado.Status, inicio)
		return

	case resultado.Processada:
		// Commit feito (ou replay reconhecido): a mensagem pode ser removida.
		//
		// `Transitoria` é conferido ANTES de `Processada` de propósito: um
		// resultado poderia vir com os dois marcados por bug, e a escolha segura
		// é NÃO apagar (reprocessar é inofensivo; perder não é).
		c.confirmar(ctx, entrega, resultado, inicio)
		return

	default:
		// Desfecho que o consumidor não conhece. Não apagar é a escolha segura:
		// a mensagem volta e o próximo ciclo decide de novo.
		c.adiar(ctx, entrega, "resultado_desconhecido", "", inicio)
		return
	}
}

// confirmar remove a mensagem da fila depois do commit.
func (c *Consumidor) confirmar(ctx context.Context, entrega *entregaRastreada, res inbox.Resultado, inicio time.Time) {
	if !entrega.reivindicar() {
		return // já tratada pelo encerramento
	}

	// `context.WithoutCancel`: a remoção precisa acontecer mesmo se o sinal de
	// término chegou — o trabalho JÁ foi confirmado no banco. Não remover aqui
	// deixaria a mensagem voltar e ser reprocessada (inofensivo, mas gastando
	// ciclo).
	if err := c.fila.Delete(context.WithoutCancel(ctx), c.urlDaFila, []string{entrega.msg.ReceiptHandle}); err != nil {
		c.logger.ErrorContext(ctx, "sqs_delete_falhou",
			slog.String("messageId", entrega.msg.ID), slog.String("error", err.Error()))
		// Não é fatal: a mensagem será reentregue e a inbox a reconhece.
	}

	status := res.Status
	if status == "" {
		status = "PROCESSED"
	}
	c.logger.InfoContext(ctx, "sqs_mensagem_processada",
		slog.String("messageId", entrega.msg.ID),
		slog.String("status", status),
		slog.String("failureCode", res.FailureCode),
		slog.Bool("replay", res.Replay),
		slog.Int("receiveCount", entrega.msg.ReceiveCount),
		observability.LogDuration(time.Since(inicio)),
	)
	if res.Replay {
		c.metrics.IncInboxDuplicate()
	}
}

// tratarErro decide entre retry (volta para a fila) e DLQ (não há o que fazer).
func (c *Consumidor) tratarErro(ctx context.Context, entrega *entregaRastreada, err error, inicio time.Time) {
	switch classificacao := classificar(err); classificacao {
	case permanente:
		c.paraDLQ(ctx, entrega, err, inicio)

	case transitorio:
		c.adiar(ctx, entrega, "transitorio", "", inicio)

	default:
		// DESCONHECIDO vai para retry, de propósito: a DLQ é o fim da linha, e
		// mandar para lá algo que talvez funcionasse na segunda tentativa perde
		// trabalho. O limite de recebimentos do broker garante que a tentativa
		// não é infinita — a mensagem chega à DLQ de qualquer forma se o problema
		// for real.
		c.adiar(ctx, entrega, "erro_desconhecido", "", inicio)
	}
}

// adiar aplica backoff exponencial e devolve a mensagem à fila.
//
// O `ChangeMessageVisibility` é o retry: a mensagem NÃO é removida nem
// descartada, apenas fica invisível pelo prazo calculado.
func (c *Consumidor) adiar(ctx context.Context, entrega *entregaRastreada, motivo, status string, inicio time.Time) {
	if !entrega.reivindicar() {
		return
	}

	segundos := c.backoffSegundos(entrega.msg.ReceiveCount)

	if err := c.fila.ChangeVisibility(context.WithoutCancel(ctx), c.urlDaFila,
		[]string{entrega.msg.ReceiptHandle}, segundos); err != nil {
		c.logger.ErrorContext(ctx, "sqs_visibility_falhou",
			slog.String("messageId", entrega.msg.ID), slog.String("error", err.Error()))
	}

	c.logger.WarnContext(ctx, "sqs_mensagem_adiada",
		slog.String("messageId", entrega.msg.ID),
		slog.String("motivo", motivo),
		slog.String("status", status),
		slog.Int("receiveCount", entrega.msg.ReceiveCount),
		slog.Int("backoffSeconds", int(segundos)),
		observability.LogDuration(time.Since(inicio)),
	)
	c.metrics.IncRetry("sqs_consume")
}

// paraDLQ manda a mensagem explicitamente para a fila morta.
//
// # Por que explicitamente, se existe o redrive
//
// A mensagem que NUNCA vai funcionar (corpo ilegível, hash divergente) ficaria
// sendo recebida e devolvida até estourar o `maxReceiveCount`. Numa fila FIFO
// isso é pior do que parece: mensagens do MESMO `MessageGroupId` são entregues em
// ordem, então uma mensagem envenenada bloqueia o grupo inteiro enquanto gira.
//
// O `Send` explícito tira a mensagem do caminho na primeira detecção de erro
// permanente, e o redrive continua sendo a rede de segurança para o que escapou
// (falha transitória que insistiu até o limite).
func (c *Consumidor) paraDLQ(ctx context.Context, entrega *entregaRastreada, err error, inicio time.Time) {
	if !entrega.reivindicar() {
		return
	}

	// `MessageGroupId` preservado e `MessageDeduplicationId` derivado do
	// identificador da mensagem: assim um reenvio do MESMO lixo para a DLQ não
	// cria uma segunda entrada (a DLQ tem deduplicação por 5 minutos).
	dedupID := "dlq-" + entrega.msg.ID
	envioErr := c.fila.Send(context.WithoutCancel(ctx), c.urlDaDLQ,
		entrega.msg.Body, entrega.msg.GroupID, dedupID)
	if envioErr != nil {
		c.logger.ErrorContext(ctx, "sqs_dlq_envio_falhou",
			slog.String("messageId", entrega.msg.ID), slog.String("error", envioErr.Error()))
		// NÃO removemos: se o envio falhou, apagar aqui perderia a mensagem.
		// Ela volta para a fila e o redrive a leva depois.
		c.metrics.IncRetry("sqs_publish")
		return
	}

	if err := c.fila.Delete(context.WithoutCancel(ctx), c.urlDaFila,
		[]string{entrega.msg.ReceiptHandle}); err != nil {
		c.logger.ErrorContext(ctx, "sqs_delete_falhou",
			slog.String("messageId", entrega.msg.ID), slog.String("error", err.Error()))
	}

	c.logger.ErrorContext(ctx, "sqs_mensagem_para_dlq",
		slog.String("messageId", entrega.msg.ID),
		slog.Int("receiveCount", entrega.msg.ReceiveCount),
		slog.String("error", err.Error()),
		observability.LogDuration(time.Since(inicio)),
	)
	c.metrics.IncDLQ()
}

// encerrar espera o trabalho em voo e devolve o que não deu tempo.
func (c *Consumidor) encerrar(emVoo *sync.WaitGroup) {
	prazo := c.cfg.SQS.ShutdownGrace
	if prazo <= 0 {
		prazo = 20 * time.Second
	}

	terminou := make(chan struct{})
	go func() {
		emVoo.Wait()
		close(terminou)
	}()

	select {
	case <-terminou:
		c.logger.Info("sqs_encerramento_concluido_trabalho")
	case <-time.After(prazo):
		// Deu o prazo. O que ficou em voo tem a visibilidade LIBERADA para
		// reentrega segura — melhor reprocessar (a operação é idempotente) do
		// que perder a mensagem para o visibility timeout.
		pendentes := c.rastreio.drenar()
		c.logger.Warn("sqs_encerramento_prazo_esgotado",
			slog.Int("mensagensEmVoo", len(pendentes)),
			slog.Duration("prazo", prazo))

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		for _, entrega := range pendentes {
			c.liberarVisibilidade(ctx, entrega)
		}
	}
}

// liberarVisibilidade devolve a mensagem à fila imediatamente.
func (c *Consumidor) liberarVisibilidade(ctx context.Context, entrega *entregaRastreada) {
	var ultimoErro error

	for tentativa := 1; tentativa <= tentativasDeEncerramento; tentativa++ {
		ultimoErro = c.fila.ChangeVisibility(ctx, c.urlDaFila,
			[]string{entrega.msg.ReceiptHandle}, 0)
		if ultimoErro == nil {
			c.logger.Info("sqs_visibilidade_liberada", slog.String("messageId", entrega.msg.ID))
			return
		}
		time.Sleep(200 * time.Millisecond)
	}

	c.logger.Error("sqs_visibilidade_nao_liberada",
		slog.String("messageId", entrega.msg.ID),
		slog.String("error", ultimoErro.Error()))
}

// devolver devolve uma mensagem recebida mas não despachada.
func (c *Consumidor) devolver(ctx context.Context, msg Message) {
	ctx = context.WithoutCancel(ctx)
	if err := c.fila.ChangeVisibility(ctx, c.urlDaFila, []string{msg.ReceiptHandle}, 0); err != nil {
		c.logger.ErrorContext(ctx, "sqs_devolucao_falhou",
			slog.String("messageId", msg.ID), slog.String("error", err.Error()))
	}
}

// backoffSegundos calcula o adiamento da próxima tentativa.
//
// # Exponencial com teto e jitter
//
//		base * 2^(recebimentos-1), limitado a MaxBackoff, com até 25% de jitter
//
//	  - **exponencial**: um SQS fora do ar não deve ser martelado a cada segundo;
//	  - **teto**: sem ele, a décima tentativa adiaria a mensagem por horas e a
//	    fila pareceria travada;
//	  - **jitter**: sem ele, todas as instâncias que falharam no mesmo instante
//	    voltariam no mesmo instante — o rebanho sincronizado é o que transforma
//	    uma oscilação em sobrecarga.
//
// O jitter é aplicado DENTRO do teto (espalha os valores entre 75% e 100% do
// atraso calculado), e não em cima dele. A primeira versão somava o jitter depois
// do limite e o resultado passava do `MaxBackoff` — o teste de teto pegou. A
// alternativa "MaxBackoff + jitter" também seria errada: aí o teto deixaria de ser
// teto, e o limite de `VisibilityTimeout` do SQS voltaria a ser alcançável por
// acidente.
func (c *Consumidor) backoffSegundos(recebimentos int) int32 {
	if recebimentos < 1 {
		recebimentos = 1
	}

	base := c.cfg.SQS.BaseBackoff
	if base <= 0 {
		base = time.Second
	}
	teto := c.cfg.SQS.MaxBackoff
	if teto <= 0 {
		teto = time.Minute
	}

	// Deslocamento limitado: com recebimentos grande, `1 << n` estoura. O teto já
	// resolve o VALOR, mas o estouro aconteceria ANTES de aplicá-lo.
	deslocamento := recebimentos - 1
	if deslocamento > 20 {
		deslocamento = 20
	}

	atraso := base * time.Duration(int64(1)<<uint(deslocamento))
	if atraso > teto || atraso <= 0 {
		atraso = teto
	}

	// Jitter de 0 a 25% do atraso, SUBTRAÍDO: o resultado nunca passa do teto.
	if atraso > time.Second {
		variacao := time.Duration(c.rand.Int63n(int64(atraso/4) + 1))
		atraso -= variacao
	}
	if atraso < time.Second {
		atraso = time.Second
	}

	segundos := int32(atraso / time.Second)
	if segundos < 1 {
		segundos = 1
	}
	return segundos
}

// esperaSemLote é quanto dormir quando o `Receive` voltou vazio.
func (c *Consumidor) esperaSemLote() time.Duration {
	if c.cfg.SQS.WaitTime > 0 {
		// Long polling ativo: o próprio Receive já esperou. Dormir mais só
		// atrasaria a próxima entrega.
		return 0
	}
	return esperaAposLoteVazio
}

// dormir espera o prazo, mas acorda no cancelamento.
func (c *Consumidor) dormir(ctx context.Context, d time.Duration) {
	if d <= 0 {
		// Sem espera, mas ainda assim cedendo o processador: um laço apertado
		// monopolizaria a CPU.
		select {
		case <-ctx.Done():
		case <-time.After(time.Millisecond):
		}
		return
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// erroTexto devolve o texto do erro, ou vazio: o slog serializa um `error` nil
// como `null`, e o campo fica mais legível como string vazia.
func erroTexto(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// --- classificação do erro ----------------------------------------------------

// tipoDeErro diz para onde a mensagem vai.
type tipoDeErro int

const (
	// desconhecido: não classificável — a escolha é tentar de novo.
	desconhecido tipoDeErro = iota
	// transitorio: repetir resolve.
	transitorio
	// permanente: repetir não resolve.
	permanente
)

// classificar traduz o erro do processador (ou do caso de uso) no destino da
// mensagem.
//
// # A régua, e o custo de errar
//
// A decisão é entre "volta para a fila" e "vai para a DLQ", e errar tem preço nos
// dois sentidos:
//
//   - permanente tratado como transitório → a mensagem gira para sempre, e numa
//     fila FIFO ela BLOQUEIA o grupo inteiro (as mensagens do mesmo
//     `MessageGroupId` são entregues em ordem);
//   - transitório tratado como permanente → trabalho que só precisava de nova
//     tentativa é descartado para a DLQ.
//
// Por isso só o que é INEQUIVOCAMENTE permanente vai direto para a DLQ: mensagem
// malformada, corpo divergente e recusa de entrada (que nada gravou e continuaria
// inválida). Todo o resto tenta de novo, e o `maxReceiveCount` do broker é o teto.
func classificar(err error) tipoDeErro {
	switch {
	case errors.Is(err, inbox.ErrMensagemInvalida),
		errors.Is(err, inbox.ErrCorpoDivergente),
		errors.Is(err, inbox.ErrOperacaoInvalida):
		return permanente

	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		// Cancelamento NÃO é culpa da mensagem: normalmente é o nosso próprio
		// encerramento. Devolver para a fila é o certo.
		return transitorio

	default:
		return desconhecido
	}
}

// --- rastreio do que está em voo ---------------------------------------------

// entregaRastreada é uma mensagem em processamento.
//
// O `sync.Once` é o que impede a mesma entrega de ser apagada E devolvida à fila
// ao mesmo tempo: quem chegar primeiro decide o destino, o outro não faz nada.
// Sem isso, o encerramento poderia liberar a visibilidade de uma mensagem que a
// goroutine acabou de remover — e a mensagem reapareceria depois de já ter sido
// tratada.
type entregaRastreada struct {
	msg       Message
	iniciada  time.Time
	umaVez    sync.Once
	confirmou bool
}

// reivindicar marca a entrega como resolvida. Devolve false se já estava.
func (e *entregaRastreada) reivindicar() bool {
	decidido := false
	e.umaVez.Do(func() {
		e.confirmou = true
		decidido = true
	})
	return decidido
}

// rastreioDeEntrega guarda as entregas em voo.
type rastreioDeEntrega struct {
	mu       sync.Mutex
	entregas map[string]*entregaRastreada
}

func novoRastreioDeEntrega() *rastreioDeEntrega {
	return &rastreioDeEntrega{entregas: map[string]*entregaRastreada{}}
}

func (r *rastreioDeEntrega) registrar(msg Message, agora time.Time) *entregaRastreada {
	entrega := &entregaRastreada{msg: msg, iniciada: agora}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.entregas[msg.ReceiptHandle] = entrega

	return entrega
}

func (r *rastreioDeEntrega) concluir(handle string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entregas, handle)
}

// drenar devolve as entregas que ainda NÃO foram resolvidas.
//
// As resolvidas saem da lista na hora (`concluir`), então o que sobra aqui é
// exatamente o que precisa voltar para a fila.
func (r *rastreioDeEntrega) drenar() []*entregaRastreada {
	r.mu.Lock()
	defer r.mu.Unlock()

	pendentes := make([]*entregaRastreada, 0, len(r.entregas))
	for _, entrega := range r.entregas {
		// Reivindica: se a goroutine dela resolver nesse meio-tempo, ela vê
		// `false` e não tenta apagar/adiar de novo.
		if entrega.reivindicar() {
			pendentes = append(pendentes, entrega)
		}
	}
	r.entregas = map[string]*entregaRastreada{}

	return pendentes
}
