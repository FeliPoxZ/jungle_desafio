package consumer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math/rand"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/inbox"
	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
	"github.com/FeliPoxZ/jungle_desafio/internal/testutil"
)

// Testes da POLÍTICA de consumo, com um broker dublê.
//
// A pergunta que estes testes respondem não é "o SQS funciona?" (isso é a
// integração com o LocalStack), e sim: dado um resultado, o que o consumidor faz
// com a mensagem?
//
//	processada   -> APAGA (só depois do commit)
//	transitória  -> ADIA (visibility timeout com backoff)
//	permanente   -> DLQ + APAGA
//	desconhecida -> ADIA (a DLQ é o fim da linha; tentar é o lado seguro)
//
// O dublê registra TUDO o que foi pedido ao broker, e é essa lista que as
// asserções leem — inclusive a ausência de chamada, que é tão informativa quanto
// a presença.

// --- dublê do broker ----------------------------------------------------------

type acaoBroker struct {
	tipo     string // "delete" | "visibility" | "send"
	handles  []string
	body     []byte
	grupo    string
	dedupID  string
	segundos int32
}

type brokerFalso struct {
	mu sync.Mutex

	// Roteiro: quantos Receive devolvem algo. Depois disso, o Receive bloqueia
	// até o contexto cancelar (imitando o long polling de uma fila vazia).
	aEntregar [][]Message
	entregues int

	acoes       []acaoBroker
	erroReceive error
	erroSend    error

	// recebido é fechado quando todas as mensagens do roteiro foram entregues —
	// o teste espera por ele em vez de dormir um tempo arbitrário.
	recebido chan struct{}
	umaVez   sync.Once
}

func novoBrokerFalso(lotes ...[]Message) *brokerFalso {
	return &brokerFalso{aEntregar: lotes, recebido: make(chan struct{})}
}

func (b *brokerFalso) Receive(ctx context.Context, _ string, _ int32, _ int32) ([]Message, error) {
	b.mu.Lock()
	if b.erroReceive != nil {
		err := b.erroReceive
		b.mu.Unlock()
		return nil, err
	}
	if b.entregues >= len(b.aEntregar) {
		b.mu.Unlock()
		// Fila vazia: espera o cancelamento, como o long polling faria.
		<-ctx.Done()
		return nil, ctx.Err()
	}
	lote := b.aEntregar[b.entregues]
	b.entregues++
	if b.entregues >= len(b.aEntregar) {
		b.umaVez.Do(func() { close(b.recebido) })
	}
	b.mu.Unlock()
	return lote, nil
}

func (b *brokerFalso) Delete(_ context.Context, _ string, handles []string) error {
	b.registrar(acaoBroker{tipo: "delete", handles: handles})
	return nil
}

func (b *brokerFalso) ChangeVisibility(_ context.Context, _ string, handles []string, segundos int32) error {
	b.registrar(acaoBroker{tipo: "visibility", handles: handles, segundos: segundos})
	return nil
}

func (b *brokerFalso) Send(_ context.Context, _ string, body []byte, grupo, dedupID string) error {
	b.mu.Lock()
	erro := b.erroSend
	b.mu.Unlock()
	if erro != nil {
		return erro
	}
	b.registrar(acaoBroker{tipo: "send", body: body, grupo: grupo, dedupID: dedupID})
	return nil
}

func (b *brokerFalso) registrar(a acaoBroker) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.acoes = append(b.acoes, a)
}

func (b *brokerFalso) acoesRegistradas() []acaoBroker {
	b.mu.Lock()
	defer b.mu.Unlock()
	copia := make([]acaoBroker, len(b.acoes))
	copy(copia, b.acoes)
	return copia
}

// esperarAcao espera até `n` ações terem sido registradas.
func (b *brokerFalso) esperarAcao(t *testing.T, n int, prazo time.Duration) []acaoBroker {
	t.Helper()

	fim := time.Now().Add(prazo)
	for time.Now().Before(fim) {
		if acoes := b.acoesRegistradas(); len(acoes) >= n {
			return acoes
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("esperava %d ação(ões) no broker; foram registradas %d: %+v",
		n, len(b.acoesRegistradas()), b.acoesRegistradas())
	return nil
}

// --- dublê do processador -----------------------------------------------------

type processadorFalso struct {
	resultado inbox.Resultado
	erro      error

	// processados é fechado na primeira mensagem, para o teste não dormir.
	processados chan struct{}
	umaVez      sync.Once

	// bloqueioForcado mantém a mensagem em processamento até ser liberado (usado
	// no teste de encerramento).
	bloqueioForcado chan struct{}
}

func novoProcessadorFalso() *processadorFalso {
	return &processadorFalso{processados: make(chan struct{})}
}

func (p *processadorFalso) Processar(ctx context.Context, _ inbox.Entrada) (inbox.Resultado, error) {
	p.umaVez.Do(func() { close(p.processados) })

	if p.bloqueioForcado != nil {
		select {
		case <-p.bloqueioForcado:
		case <-ctx.Done():
			return inbox.Resultado{}, ctx.Err()
		}
	}
	return p.resultado, p.erro
}

// --- infra de teste -----------------------------------------------------------

// configDeTeste monta a configuração do consumidor com prazos curtos.
func configDeTeste() *config.Config {
	cfg := &config.Config{}
	cfg.SQS.TransactionsQueue = "wager-transactions.fifo"
	cfg.SQS.TransactionsDLQ = "wager-transactions-dlq.fifo"
	cfg.SQS.BatchSize = 10
	cfg.SQS.ConsumerConcurrency = 4
	cfg.SQS.WaitTime = 0 // sem long polling: o dublê controla a espera
	cfg.SQS.BaseBackoff = 2 * time.Second
	cfg.SQS.MaxBackoff = time.Minute
	cfg.SQS.ShutdownGrace = 2 * time.Second
	return cfg
}

// rodar inicia o consumidor e devolve uma função de parada.
func rodar(t *testing.T, b *brokerFalso, p *processadorFalso) (context.CancelFunc, *sync.WaitGroup) {
	t.Helper()

	return rodarCom(t, b, p, observability.NewMetrics())
}

// rodarCom é o `rodar` com um registry ESCOLHIDO pelo teste — é o que permite
// ler a métrica depois em `/metrics`.
func rodarCom(t *testing.T, b *brokerFalso, p *processadorFalso, metricas *observability.Metrics) (context.CancelFunc, *sync.WaitGroup) {
	t.Helper()

	consumidor := New(Options{
		Fila:        b,
		Processador: p,
		Config:      configDeTeste(),
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:     metricas,
	})

	ctx, cancel := context.WithCancel(context.Background())
	var terminou sync.WaitGroup
	terminou.Add(1)

	go func() {
		defer terminou.Done()
		_ = consumidor.Run(ctx)
	}()

	return cancel, &terminou
}

// parar cancela o contexto e espera o laço terminar.
func parar(t *testing.T, cancel context.CancelFunc, terminou *sync.WaitGroup) {
	t.Helper()

	cancel()
	pronto := make(chan struct{})
	go func() { terminou.Wait(); close(pronto) }()

	select {
	case <-pronto:
	case <-time.After(10 * time.Second):
		t.Fatal("o consumidor não encerrou no prazo")
	}
}

// mensagem é uma mensagem de teste.
func mensagem(id, handle string, recebimentos int) Message {
	return Message{
		ID:            id,
		ReceiptHandle: handle,
		ReceiveCount:  recebimentos,
		GroupID:       "carteira-1",
		Body:          []byte(`{"messageId":"` + id + `"}`),
	}
}

// --- casos --------------------------------------------------------------------

// Mensagem processada com commit: o consumidor APAGA, e só depois.
func TestMensagemProcessadaEhRemovida(t *testing.T) {
	b := novoBrokerFalso([]Message{mensagem("msg-1", "rh-1", 1)})
	p := novoProcessadorFalso()
	p.resultado = inbox.Resultado{Processada: true, Status: "PROCESSED"}

	cancel, terminou := rodar(t, b, p)
	defer parar(t, cancel, terminou)

	acoes := b.esperarAcao(t, 1, 5*time.Second)

	if acoes[0].tipo != "delete" {
		t.Fatalf("primeira ação = %q, quer delete (%+v)", acoes[0].tipo, acoes)
	}
	if len(acoes[0].handles) != 1 || acoes[0].handles[0] != "rh-1" {
		t.Errorf("handles = %v, quer [rh-1]", acoes[0].handles)
	}
}

// Replay (mensagem já tratada): também é removida — nada foi feito desta vez, e
// deixá-la na fila só faria o ciclo se repetir.
func TestReplayTambemEhRemovido(t *testing.T) {
	b := novoBrokerFalso([]Message{mensagem("msg-1", "rh-1", 2)})
	p := novoProcessadorFalso()
	p.resultado = inbox.Resultado{Processada: true, Duplicada: true}

	cancel, terminou := rodar(t, b, p)
	defer parar(t, cancel, terminou)

	acoes := b.esperarAcao(t, 1, 5*time.Second)
	if acoes[0].tipo != "delete" {
		t.Fatalf("ação = %q, quer delete", acoes[0].tipo)
	}
}

// Falha TRANSITÓRIA: a mensagem NÃO é apagada, e a visibilidade é adiada.
func TestFalhaTransitoriaAdiaSemApagar(t *testing.T) {
	b := novoBrokerFalso([]Message{mensagem("msg-1", "rh-1", 1)})
	p := novoProcessadorFalso()
	p.resultado = inbox.Resultado{Transitoria: true}

	cancel, terminou := rodar(t, b, p)
	defer parar(t, cancel, terminou)

	acoes := b.esperarAcao(t, 1, 5*time.Second)

	if acoes[0].tipo != "visibility" {
		t.Fatalf("primeira ação = %q, quer visibility", acoes[0].tipo)
	}
	if acoes[0].segundos < 1 {
		t.Errorf("visibility = %d segundos, quer >= 1", acoes[0].segundos)
	}
	asserirSemDelete(t, acoes)
	asserirSemDLQ(t, acoes)
}

// Mensagem INVÁLIDA (nunca vai funcionar): vai para a DLQ e é removida da fila
// principal — sem girar até estourar o maxReceiveCount.
func TestMensagemInvalidaVaiParaDLQ(t *testing.T) {
	b := novoBrokerFalso([]Message{mensagem("msg-lixo", "rh-lixo", 1)})
	p := novoProcessadorFalso()
	p.erro = inbox.ErrMensagemInvalida

	cancel, terminou := rodar(t, b, p)
	defer parar(t, cancel, terminou)

	acoes := b.esperarAcao(t, 2, 5*time.Second)

	// A ordem importa: primeiro ENVIA para a DLQ, depois remove. Ao contrário,
	// uma falha no envio perderia a mensagem.
	if acoes[0].tipo != "send" {
		t.Fatalf("primeira ação = %q, quer send (%+v)", acoes[0].tipo, acoes)
	}
	if acoes[1].tipo != "delete" {
		t.Fatalf("segunda ação = %q, quer delete", acoes[1].tipo)
	}
	if acoes[0].grupo != "carteira-1" {
		t.Errorf("MessageGroupId = %q, quer carteira-1 (preserva a ordem do grupo)", acoes[0].grupo)
	}
	if acoes[0].dedupID == "" {
		t.Error("MessageDeduplicationId vazio: a DLQ é FIFO e o reenvio do mesmo lixo criaria duplicata")
	}
	if !strings.Contains(string(acoes[0].body), "msg-lixo") {
		t.Errorf("corpo enviado à DLQ = %q, quer a mensagem original", acoes[0].body)
	}
}

// Corpo divergente também é PERMANENTE (mesma identidade, conteúdo diferente).
func TestCorpoDivergenteVaiParaDLQ(t *testing.T) {
	b := novoBrokerFalso([]Message{mensagem("msg-1", "rh-1", 1)})
	p := novoProcessadorFalso()
	p.erro = inbox.ErrCorpoDivergente

	cancel, terminou := rodar(t, b, p)
	defer parar(t, cancel, terminou)

	acoes := b.esperarAcao(t, 2, 5*time.Second)
	if acoes[0].tipo != "send" {
		t.Fatalf("primeira ação = %q, quer send", acoes[0].tipo)
	}
}

// Erro DESCONHECIDO: adia, não descarta. A DLQ é o fim da linha, e mandar para lá
// algo que talvez funcionasse na segunda tentativa perde trabalho.
func TestErroDesconhecidoAdia(t *testing.T) {
	b := novoBrokerFalso([]Message{mensagem("msg-1", "rh-1", 1)})
	p := novoProcessadorFalso()
	p.erro = errors.New("erro que ninguém classificou")

	cancel, terminou := rodar(t, b, p)
	defer parar(t, cancel, terminou)

	acoes := b.esperarAcao(t, 1, 5*time.Second)
	if acoes[0].tipo != "visibility" {
		t.Fatalf("ação = %q, quer visibility", acoes[0].tipo)
	}
	asserirSemDLQ(t, acoes)
}

// Erro de CANCELAMENTO não é culpa da mensagem: adia (normalmente é o nosso
// próprio encerramento).
func TestCancelamentoAdia(t *testing.T) {
	b := novoBrokerFalso([]Message{mensagem("msg-1", "rh-1", 1)})
	p := novoProcessadorFalso()
	p.erro = context.Canceled

	cancel, terminou := rodar(t, b, p)
	defer parar(t, cancel, terminou)

	acoes := b.esperarAcao(t, 1, 5*time.Second)
	if acoes[0].tipo != "visibility" {
		t.Fatalf("ação = %q, quer visibility", acoes[0].tipo)
	}
}

// --- backoff ------------------------------------------------------------------

// O backoff cresce, tem teto e nunca é menor que 1 segundo.
func TestBackoffCresceComTeto(t *testing.T) {
	c := &Consumidor{cfg: configDeTeste(), rand: randComSemente()}

	anterior := int32(0)
	for recebimentos := 1; recebimentos <= 12; recebimentos++ {
		segundos := c.backoffSegundos(recebimentos)

		if segundos < 1 {
			t.Fatalf("recebimentos=%d: backoff = %d, quer >= 1", recebimentos, segundos)
		}
		if teto := int32(c.cfg.SQS.MaxBackoff.Seconds()); segundos > teto {
			t.Fatalf("recebimentos=%d: backoff = %d, acima do teto de %d", recebimentos, segundos, teto)
		}
		// O crescimento não é estritamente monótono por causa do jitter, mas os
		// primeiros passos precisam subir.
		if recebimentos <= 4 && segundos <= anterior {
			t.Errorf("recebimentos=%d: backoff = %d, não cresceu em relação a %d", recebimentos, segundos, anterior)
		}
		anterior = segundos
	}
}

// Recebimentos absurdos não estouram o cálculo (o deslocamento é limitado).
func TestBackoffNaoEstoura(t *testing.T) {
	c := &Consumidor{cfg: configDeTeste(), rand: randComSemente()}

	for _, recebimentos := range []int{1, 10, 64, 1000, 1 << 30} {
		segundos := c.backoffSegundos(recebimentos)
		if segundos < 1 || segundos > int32(c.cfg.SQS.MaxBackoff.Seconds()) {
			t.Errorf("recebimentos=%d: backoff = %d, fora da faixa [1, %d]",
				recebimentos, segundos, int32(c.cfg.SQS.MaxBackoff.Seconds()))
		}
	}
}

// --- encerramento -------------------------------------------------------------

// No encerramento, a mensagem que NÃO deu tempo de concluir tem a visibilidade
// LIBERADA (segundos = 0) para reentrega imediata — melhor reprocessar (a
// operação é idempotente) do que esperar o visibility timeout inteiro.
func TestEncerramentoLiberaVisibilidade(t *testing.T) {
	b := novoBrokerFalso([]Message{mensagem("msg-lenta", "rh-lenta", 1)})
	p := novoProcessadorFalso()
	p.bloqueioForcado = make(chan struct{}) // nunca liberado: a mensagem fica em voo

	cfg := configDeTeste()
	cfg.SQS.ShutdownGrace = 300 * time.Millisecond // prazo curto: força o caminho

	consumidor := New(Options{
		Fila: b, Processador: p, Config: cfg,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics: observability.NewMetrics(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	var terminou sync.WaitGroup
	terminou.Add(1)
	go func() { defer terminou.Done(); _ = consumidor.Run(ctx) }()

	// Espera a mensagem começar a ser processada, e então pede o encerramento.
	select {
	case <-p.processados:
	case <-time.After(5 * time.Second):
		t.Fatal("a mensagem não começou a ser processada")
	}
	cancel()

	pronto := make(chan struct{})
	go func() { terminou.Wait(); close(pronto) }()
	select {
	case <-pronto:
	case <-time.After(10 * time.Second):
		t.Fatal("o consumidor não encerrou no prazo")
	}

	acoes := b.acoesRegistradas()
	var liberou bool
	for _, a := range acoes {
		if a.tipo == "visibility" && a.segundos == 0 {
			liberou = true
		}
	}
	if !liberou {
		t.Fatalf("a visibilidade não foi liberada (segundos=0) no encerramento: %+v", acoes)
	}

	// E o que ficou em voo NÃO pode ter sido apagado: o trabalho não terminou.
	for _, a := range acoes {
		if a.tipo == "delete" {
			t.Errorf("mensagem em voo foi apagada sem ter concluído: %+v", acoes)
		}
	}
}

// --- classificação ------------------------------------------------------------

// A tabela de classificação, que decide o destino da mensagem.
func TestClassificar(t *testing.T) {
	casos := []struct {
		nome string
		err  error
		quer tipoDeErro
	}{
		{"mensagem inválida", inbox.ErrMensagemInvalida, permanente},
		{"corpo divergente", inbox.ErrCorpoDivergente, permanente},
		{"operação inválida", inbox.ErrOperacaoInvalida, permanente},
		{"cancelamento", context.Canceled, transitorio},
		{"prazo excedido", context.DeadlineExceeded, transitorio},
		{"erro desconhecido", errors.New("qualquer coisa"), desconhecido},
		{"sentinela com contexto", errors.Join(inbox.ErrMensagemInvalida, errors.New("detalhe")), permanente},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			if got := classificar(caso.err); got != caso.quer {
				t.Errorf("classificar(%v) = %v, quer %v", caso.err, got, caso.quer)
			}
		})
	}
}

// Erro de classificação DESCONHECIDA não é permanente: o destino é retry.
func TestClassificacaoDesconhecidaNaoVaiParaDLQ(t *testing.T) {
	if classificar(errors.New("erro novo que ninguém mapeou")) == permanente {
		t.Error("erro desconhecido foi classificado como permanente; a DLQ é o fim da linha")
	}
}

// --- auxiliares ---------------------------------------------------------------

// randComSemente devolve um gerador determinístico: o jitter do backoff precisa
// ser reproduzível no teste.
func randComSemente() *rand.Rand { return rand.New(rand.NewSource(42)) }

func asserirSemDelete(t *testing.T, acoes []acaoBroker) {
	t.Helper()
	for _, a := range acoes {
		if a.tipo == "delete" {
			t.Errorf("a mensagem foi apagada sem ter sido processada: %+v", acoes)
		}
	}
}

func asserirSemDLQ(t *testing.T, acoes []acaoBroker) {
	t.Helper()
	for _, a := range acoes {
		if a.tipo == "send" {
			t.Errorf("a mensagem foi para a DLQ sem ser permanente: %+v", acoes)
		}
	}
}

// --- métricas de retry por etapa (Fase 11) -----------------------------------
//
// `retries_total{stage}` tem SEIS emissores no consumidor. Até a Fase 11 nenhum
// deles tinha teste: o único teste da métrica chamava o helper direto. Estes
// testes exercitam a POLÍTICA real do consumidor e leem o valor no mesmo handler
// que a aplicação expõe em `/metrics`.

// esperarMetricaNoEndpoint espera a série aparecer em `/metrics`, com prazo.
func esperarMetricaNoEndpoint(t *testing.T, baseURL, nome string, labels map[string]string) {
	t.Helper()

	fim := time.Now().Add(5 * time.Second)
	for time.Now().Before(fim) {
		familias := testutil.DoEndpoint(t, baseURL)
		if valor, ok := familias.Valor(nome, labels); ok && valor >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("a série %s%v não apareceu em /metrics", nome, labels)
}

// O `Receive` falhando é retry da etapa `sqs_receive`: o broker não caiu, a
// busca falhou — e o laço precisa continuar.
func TestMetricaRetryDaEtapaSqsReceive(t *testing.T) {
	b := novoBrokerFalso()
	b.erroReceive = errors.New("broker indisponível")
	p := novoProcessadorFalso()

	metricas := observability.NewMetrics()
	cancel, terminou := rodarCom(t, b, p, metricas)
	defer parar(t, cancel, terminou)

	endpoint := httptest.NewServer(metricas.Handler())
	defer endpoint.Close()

	esperarMetricaNoEndpoint(t, endpoint.URL, "jungleg_wager_retries_total",
		map[string]string{"stage": "sqs_receive"})
}

// Processamento transitório é retry da etapa `sqs_consume`.
func TestMetricaRetryDaEtapaSqsConsume(t *testing.T) {
	b := novoBrokerFalso([]Message{mensagem("msg-1", "rh-1", 1)})
	p := novoProcessadorFalso()
	p.erro = ports.ErrTransient

	metricas := observability.NewMetrics()
	cancel, terminou := rodarCom(t, b, p, metricas)
	defer parar(t, cancel, terminou)

	// A mensagem é ADIADA (não apagada)...
	if acoes := b.esperarAcao(t, 1, 5*time.Second); acoes[0].tipo != "visibility" {
		t.Fatalf("primeira ação = %q, quer visibility", acoes[0].tipo)
	}

	endpoint := httptest.NewServer(metricas.Handler())
	defer endpoint.Close()

	// ...e o retry é contado na etapa do consumo.
	esperarMetricaNoEndpoint(t, endpoint.URL, "jungleg_wager_retries_total",
		map[string]string{"stage": "sqs_consume"})
}

// A DLQ que não aceita a mensagem é retry da etapa `sqs_publish`: a mensagem
// NÃO pode ser apagada da fila de origem.
func TestMetricaRetryDaEtapaSqsPublish(t *testing.T) {
	b := novoBrokerFalso([]Message{mensagem("msg-1", "rh-1", 1)})
	b.erroSend = errors.New("DLQ indisponível")
	p := novoProcessadorFalso()
	p.erro = inbox.ErrMensagemInvalida

	metricas := observability.NewMetrics()
	cancel, terminou := rodarCom(t, b, p, metricas)
	defer parar(t, cancel, terminou)

	select {
	case <-p.processados:
	case <-time.After(5 * time.Second):
		t.Fatal("o consumidor não processou a mensagem")
	}

	endpoint := httptest.NewServer(metricas.Handler())
	defer endpoint.Close()

	esperarMetricaNoEndpoint(t, endpoint.URL, "jungleg_wager_retries_total",
		map[string]string{"stage": "sqs_publish"})
}
