package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
)

// ---------------------------------------------------------------------------
// Dublês
// ---------------------------------------------------------------------------

// registrador guarda a ORDEM das chamadas.
//
// A ordem é o que importa em duas asserções desta suíte: "publicar ANTES de
// marcar" e "não marcar quando a publicação falha". Sem registrar a sequência,
// um teste passaria com a ordem invertida.
type registrador struct {
	mu       sync.Mutex
	chamadas []string
}

func (r *registrador) registrar(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chamadas = append(r.chamadas, s)
}

func (r *registrador) lista() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	saida := make([]string, len(r.chamadas))
	copy(saida, r.chamadas)
	return saida
}

func (r *registrador) contem(s string) bool {
	for _, c := range r.lista() {
		if c == s {
			return true
		}
	}
	return false
}

// uowFake é um Unit of Work em memória com a MESMA semântica de transação que o
// publicador usa: o rascunho só é publicado no fim, sem erro.
type uowFake struct {
	registro *registrador
	outbox   []ports.OutboxEvent
	erro     error
}

func (u *uowFake) Run(ctx context.Context, fn func(context.Context, ports.Repos) error) error {
	if u.erro != nil {
		return u.erro
	}
	rascunho := make([]ports.OutboxEvent, len(u.outbox))
	copy(rascunho, u.outbox)
	if err := fn(ctx, u.repos(rascunho)); err != nil {
		return err
	}
	u.outbox = rascunho
	return nil
}

func (u *uowFake) RunReadOnly(ctx context.Context, fn func(context.Context, ports.Repos) error) error {
	copia := make([]ports.OutboxEvent, len(u.outbox))
	copy(copia, u.outbox)
	return fn(ctx, u.repos(copia))
}

func (u *uowFake) repos(outbox []ports.OutboxEvent) ports.Repos {
	return ports.Repos{Outbox: &outboxFake{u: u, outbox: outbox}}
}

type outboxFake struct {
	u      *uowFake
	outbox []ports.OutboxEvent
}

func (o *outboxFake) Insert(context.Context, ports.OutboxEvent) error { return nil }

func (o *outboxFake) ClaimBatch(_ context.Context, limit int, lease time.Duration, now time.Time) ([]ports.OutboxEvent, error) {
	o.u.registro.registrar("claim")

	var saida []ports.OutboxEvent
	for i := range o.outbox {
		ev := o.outbox[i]
		if !ev.PublishedAt.IsZero() || ev.AvailableAt.After(now) {
			continue
		}
		o.outbox[i].AvailableAt = now.Add(lease)
		saida = append(saida, ev)
		if len(saida) == limit {
			break
		}
	}
	return saida, nil
}

func (o *outboxFake) MarkPublished(_ context.Context, eventID string, now time.Time) error {
	o.u.registro.registrar("markPublished")
	for i := range o.outbox {
		if o.outbox[i].EventID == eventID {
			o.outbox[i].PublishedAt = now
			return nil
		}
	}
	return ports.ErrNotFound
}

func (o *outboxFake) MarkRetry(_ context.Context, eventID string, proximo time.Time, motivo string) error {
	o.u.registro.registrar("markRetry")
	for i := range o.outbox {
		if o.outbox[i].EventID == eventID {
			o.outbox[i].Attempts++
			o.outbox[i].AvailableAt = proximo
			o.outbox[i].LastError = motivo
			return nil
		}
	}
	return ports.ErrNotFound
}

func (o *outboxFake) PendingStats(_ context.Context, now time.Time) (int, time.Duration, error) {
	o.u.registro.registrar("pendingStats")

	var (
		pendentes  int
		maisAntigo time.Time
	)
	for _, ev := range o.outbox {
		if !ev.PublishedAt.IsZero() {
			continue
		}
		pendentes++
		if maisAntigo.IsZero() || ev.OccurredAt.Before(maisAntigo) {
			maisAntigo = ev.OccurredAt
		}
	}
	if pendentes == 0 {
		return 0, 0, nil
	}
	return pendentes, now.Sub(maisAntigo), nil
}

// publicadorFake registra o que foi pedido para publicar (e o que falhou).
type publicadorFake struct {
	registro   *registrador
	erro       error
	publicados []Evento
	payloads   [][]byte
}

func (p *publicadorFake) Publicar(_ context.Context, e Evento) error {
	p.registro.registrar("publicar")
	if p.erro != nil {
		return p.erro
	}
	p.publicados = append(p.publicados, e)
	// Cópia do payload: o slice do evento é do chamador, e comparar depois sem
	// copiar poderia esconder uma mutação.
	copia := make([]byte, len(e.Payload))
	copy(copia, e.Payload)
	p.payloads = append(p.payloads, copia)
	return nil
}

// ---------------------------------------------------------------------------
// Ajuda
// ---------------------------------------------------------------------------

func eventDeTeste(id, agregado, tipo, payload string, ocorridoEm time.Time) ports.OutboxEvent {
	return ports.OutboxEvent{
		EventID:     id,
		AggregateID: agregado,
		EventType:   tipo,
		Payload:     []byte(payload),
		OccurredAt:  ocorridoEm,
		AvailableAt: ocorridoEm,
	}
}

type ambiente struct {
	publicador *OutboxPublisher
	uow        *uowFake
	pub        *publicadorFake
	registro   *registrador
	metrics    *observability.Metrics
}

func novoAmbiente(t *testing.T, cfg config.OutboxConfig) *ambiente {
	t.Helper()

	registro := &registrador{}
	uow := &uowFake{registro: registro}
	pub := &publicadorFake{registro: registro}
	metrics := observability.NewMetrics()

	if cfg.BatchSize == 0 {
		cfg.BatchSize = 50
	}
	if cfg.Lease == 0 {
		cfg.Lease = 30 * time.Second
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = 10
	}
	if cfg.BaseBackoff == 0 {
		cfg.BaseBackoff = 2 * time.Second
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = 5 * time.Minute
	}
	if cfg.ParkedBackoff == 0 {
		cfg.ParkedBackoff = time.Hour
	}

	publicador := NovoOutboxPublisher(OutboxPublisherOptions{
		UOW:        uow,
		Publicador: pub,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Metrics:    metrics,
		Config:     &config.Config{Outbox: cfg},
	})
	// Semente fixa: o jitter deixa de ser aleatório e as asserções de prazo
	// passam a ser determinísticas.
	publicador.rand = rand.New(rand.NewSource(1))

	return &ambiente{publicador: publicador, uow: uow, pub: pub, registro: registro, metrics: metrics}
}

func metricasDoTexto(t *testing.T, m *observability.Metrics) string {
	t.Helper()

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rec.Body.String()
}

// ---------------------------------------------------------------------------
// Testes
// ---------------------------------------------------------------------------

// O caminho feliz, com a ORDEM das chamadas: reivindicar, publicar e só então
// marcar.
func TestPublicadorPublicaAntesDeMarcar(t *testing.T) {
	amb := novoAmbiente(t, config.OutboxConfig{})
	amb.uow.outbox = []ports.OutboxEvent{
		eventDeTeste("evt-1", "carteira-1", "WagerTransactionProcessed", `{"eventId":"evt-1"}`, time.Now().UTC()),
	}

	publicados, err := amb.publicador.PublicarPendentes(context.Background())
	if err != nil {
		t.Fatalf("PublicarPendentes: %v", err)
	}
	if publicados != 1 {
		t.Fatalf("publicados = %d, quer 1", publicados)
	}

	// A ordem que importa: reivindicar, PUBLICAR e só então marcar. A medição do
	// atraso (`pendingStats`) acontece no fim do ciclo e não faz parte da
	// publicação — por isso a asserção é sobre a sequência, e não sobre a lista
	// inteira.
	ordem := amb.registro.lista()
	if len(ordem) < 3 || ordem[0] != "claim" || ordem[1] != "publicar" || ordem[2] != "markPublished" {
		t.Errorf("ordem das chamadas = %v, quer começar com [claim publicar markPublished]", ordem)
	}

	if len(amb.uow.outbox) != 1 || amb.uow.outbox[0].PublishedAt.IsZero() {
		t.Errorf("o evento deveria estar marcado como publicado: %+v", amb.uow.outbox)
	}

	// O corpo publicado é o SNAPSHOT da outbox, byte a byte. Reconstruir o
	// evento a partir das colunas publicaria uma interpretação nova do passado.
	if string(amb.pub.payloads[0]) != `{"eventId":"evt-1"}` {
		t.Errorf("payload publicado = %s, quer o snapshot da outbox", amb.pub.payloads[0])
	}
	if amb.pub.publicados[0].Tipo != "WagerTransactionProcessed" {
		t.Errorf("tipo publicado = %q", amb.pub.publicados[0].Tipo)
	}
}

// AUSÊNCIA de chamada: quando a publicação falha, o evento NÃO pode ser marcado
// como publicado.
//
// Sem esta asserção, um `MarkPublished` fora do `if err != nil` passaria
// desapercebido — e o efeito em produção seria o pior possível: o evento sairia
// da outbox sem nunca ter chegado à fila (perda silenciosa de um fato
// financeiro).
func TestPublicadorNaoMarcaPublicadoQuandoAPublicacaoFalha(t *testing.T) {
	amb := novoAmbiente(t, config.OutboxConfig{})
	amb.pub.erro = errors.New("sqs fora do ar")
	amb.uow.outbox = []ports.OutboxEvent{
		eventDeTeste("evt-1", "carteira-1", "WagerTransactionProcessed", `{}`, time.Now().UTC()),
	}

	publicados, err := amb.publicador.PublicarPendentes(context.Background())
	if err != nil {
		t.Fatalf("PublicarPendentes: %v", err)
	}
	if publicados != 0 {
		t.Fatalf("publicados = %d, quer 0", publicados)
	}

	if amb.registro.contem("markPublished") {
		t.Errorf("MarkPublished foi chamado numa publicação que falhou: %v", amb.registro.lista())
	}
	if !amb.registro.contem("markRetry") {
		t.Fatalf("MarkRetry deveria ter sido chamado: %v", amb.registro.lista())
	}

	ev := amb.uow.outbox[0]
	if ev.PublishedAt != (time.Time{}) {
		t.Errorf("published_at não deveria estar preenchido")
	}
	if ev.Attempts != 1 {
		t.Errorf("attempts = %d, quer 1 (incrementado no banco)", ev.Attempts)
	}
	if !ev.AvailableAt.After(time.Now().UTC()) {
		t.Errorf("available_at deveria estar no futuro (backoff), veio %v", ev.AvailableAt)
	}
	if !strings.Contains(ev.LastError, "sqs fora do ar") {
		t.Errorf("last_error deveria registrar a causa, veio %q", ev.LastError)
	}

	corpo := metricasDoTexto(t, amb.metrics)
	if !strings.Contains(corpo, `jungleg_wager_outbox_publish_total{result="retry"} 1`) {
		t.Errorf("a métrica de retry não foi emitida:\n%s", corpo)
	}
}

// Esgotado o teto de tentativas, o evento vai para a QUARENTENA: continua
// pendente (nada é perdido), com `available_at` no prazo longo.
func TestPublicadorColocaEmQuarentenaAposEsgotarTentativas(t *testing.T) {
	amb := novoAmbiente(t, config.OutboxConfig{MaxAttempts: 3, MaxBackoff: time.Minute, ParkedBackoff: time.Hour})
	amb.pub.erro = errors.New("sqs fora do ar")

	// Duas falhas já registradas: esta é a terceira (e última) tentativa.
	ev := eventDeTeste("evt-1", "carteira-1", "WagerTransactionProcessed", `{}`, time.Now().UTC())
	ev.Attempts = 2
	amb.uow.outbox = []ports.OutboxEvent{ev}

	if _, err := amb.publicador.PublicarPendentes(context.Background()); err != nil {
		t.Fatalf("PublicarPendentes: %v", err)
	}

	depois := amb.uow.outbox[0]
	if depois.PublishedAt != (time.Time{}) {
		t.Errorf("quarentena NÃO é publicação: published_at deveria continuar vazio")
	}
	if depois.Attempts != 3 {
		t.Errorf("attempts = %d, quer 3", depois.Attempts)
	}
	if !strings.Contains(depois.LastError, "tentativas esgotadas") {
		t.Errorf("last_error deveria dizer que as tentativas esgotaram, veio %q", depois.LastError)
	}

	// O prazo da quarentena é o do ParkedBackoff (menos o jitter de até 25%).
	em := time.Until(depois.AvailableAt)
	if em < 30*time.Minute || em > time.Hour {
		t.Errorf("prazo até a próxima tentativa = %v, quer perto de 1h (quarentena)", em)
	}

	corpo := metricasDoTexto(t, amb.metrics)
	if !strings.Contains(corpo, `jungleg_wager_outbox_publish_total{result="failed"} 1`) {
		t.Errorf("a métrica de falha não foi emitida:\n%s", corpo)
	}
}

// Recusa PERMANENTE do destino (corpo inválido, mensagem grande demais) vai para
// a quarentena na PRIMEIRA detecção: repetir o mesmo corpo daria a mesma
// resposta, e as dez tentativas só atrasariam os outros eventos do lote.
func TestPublicadorColocaEmQuarentenaNaRecusaPermanente(t *testing.T) {
	amb := novoAmbiente(t, config.OutboxConfig{MaxAttempts: 10, ParkedBackoff: 2 * time.Hour})
	amb.pub.erro = fmt.Errorf("corpo inválido: %w", ErrEventoRecusado)
	amb.uow.outbox = []ports.OutboxEvent{
		eventDeTeste("evt-1", "carteira-1", "WalletBalanceChanged", `{}`, time.Now().UTC()),
	}

	if _, err := amb.publicador.PublicarPendentes(context.Background()); err != nil {
		t.Fatalf("PublicarPendentes: %v", err)
	}

	depois := amb.uow.outbox[0]
	if depois.Attempts != 1 {
		t.Errorf("attempts = %d, quer 1 (uma única tentativa)", depois.Attempts)
	}
	if !strings.Contains(depois.LastError, "recusado pelo destino") {
		t.Errorf("last_error deveria dizer que o destino recusou, veio %q", depois.LastError)
	}
	if em := time.Until(depois.AvailableAt); em < time.Hour {
		t.Errorf("a quarentena deveria ser longa, veio %v", em)
	}
}

// Sem pendentes, o publicador não publica nem marca nada — e as estatísticas são
// medidas (é o que alimenta o gauge de atraso da outbox).
func TestPublicadorSemPendentesNaoChamaODestino(t *testing.T) {
	amb := novoAmbiente(t, config.OutboxConfig{})

	publicados, err := amb.publicador.PublicarPendentes(context.Background())
	if err != nil {
		t.Fatalf("PublicarPendentes: %v", err)
	}
	if publicados != 0 {
		t.Fatalf("publicados = %d, quer 0", publicados)
	}
	if amb.registro.contem("publicar") {
		t.Errorf("o destino não deveria ser chamado sem pendentes: %v", amb.registro.lista())
	}
	if amb.registro.contem("markPublished") {
		t.Errorf("nada deveria ser marcado como publicado: %v", amb.registro.lista())
	}
	if !amb.registro.contem("pendingStats") {
		t.Errorf("as estatísticas deveriam ser medidas: %v", amb.registro.lista())
	}
}

// O atraso da outbox chega ao /metrics (README §12: "atraso da outbox").
func TestPublicadorEmiteAtrasoDaOutbox(t *testing.T) {
	amb := novoAmbiente(t, config.OutboxConfig{})
	// Evento NÃO publicável (available_at no futuro) e velho: ele conta como
	// pendente e é o mais antigo.
	ev := eventDeTeste("evt-antigo", "carteira-1", "WagerTransactionProcessed", `{}`, time.Now().UTC().Add(-90*time.Second))
	ev.AvailableAt = time.Now().UTC().Add(time.Hour)
	amb.uow.outbox = []ports.OutboxEvent{ev}

	if _, err := amb.publicador.PublicarPendentes(context.Background()); err != nil {
		t.Fatalf("PublicarPendentes: %v", err)
	}

	corpo := metricasDoTexto(t, amb.metrics)
	if !strings.Contains(corpo, "jungleg_wager_outbox_pending_age_seconds") {
		t.Fatalf("o gauge de atraso da outbox não foi emitido:\n%s", corpo)
	}
	if strings.Contains(corpo, "jungleg_wager_outbox_pending_age_seconds 0") {
		t.Errorf("a idade do pendente deveria ser > 0:\n%s", corpo)
	}
}

// O laço para quando o contexto é cancelado (é o que o runner usa no SIGTERM).
func TestPublicadorParaNoCancelamento(t *testing.T) {
	amb := novoAmbiente(t, config.OutboxConfig{PollInterval: time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	parou := make(chan error, 1)
	go func() { parou <- amb.publicador.Run(ctx) }()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-parou:
		if err != nil {
			t.Errorf("Run devolveu erro no cancelamento: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run não parou no cancelamento do contexto")
	}
}

// Lote maior que o batch configurado é publicado em partes, e a segunda chamada
// pega o resto (a outbox é uma fila, não um instantâneo).
func TestPublicadorRespeitaOBatch(t *testing.T) {
	amb := novoAmbiente(t, config.OutboxConfig{BatchSize: 2})
	agora := time.Now().UTC()
	amb.uow.outbox = []ports.OutboxEvent{
		eventDeTeste("evt-1", "c1", "A", `{}`, agora),
		eventDeTeste("evt-2", "c1", "B", `{}`, agora),
		eventDeTeste("evt-3", "c2", "C", `{}`, agora),
	}

	publicados, err := amb.publicador.PublicarPendentes(context.Background())
	if err != nil {
		t.Fatalf("PublicarPendentes: %v", err)
	}
	if publicados != 2 {
		t.Fatalf("publicados = %d, quer 2 (o batch manda)", publicados)
	}
	if !amb.uow.outbox[2].PublishedAt.IsZero() {
		t.Errorf("o terceiro evento não deveria ter sido publicado neste ciclo")
	}
}

// ---------------------------------------------------------------------------
// Backoff
// ---------------------------------------------------------------------------

// O jitter NUNCA pode empurrar o prazo acima do teto. É o defeito 32 da §15 do
// ARCHITECTURE, aqui travado para os laços desta fase: somar o jitter depois do
// limite faria o teto deixar de ser teto.
func TestEsperaComBackoffNuncaPassaDoTeto(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	base := 2 * time.Second
	teto := 30 * time.Second

	for tentativa := 1; tentativa <= 40; tentativa++ {
		for i := 0; i < 200; i++ {
			espera := esperaComBackoff(r, tentativa, base, teto)
			if espera > teto {
				t.Fatalf("tentativa %d: espera %v passou do teto %v", tentativa, espera, teto)
			}
			if espera < esperaMinima {
				t.Fatalf("tentativa %d: espera %v abaixo do piso %v", tentativa, espera, esperaMinima)
			}
		}
	}
}

// O crescimento é exponencial até o teto e depois fica na faixa do teto.
func TestEsperaComBackoffCresceEDepoisEstabiliza(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	base := time.Second
	teto := 8 * time.Second

	anterior := time.Duration(0)
	for tentativa := 1; tentativa <= 6; tentativa++ {
		espera := esperaComBackoff(r, tentativa, base, teto)
		if espera > teto {
			t.Fatalf("tentativa %d: %v acima do teto", tentativa, espera)
		}
		if espera < anterior {
			t.Fatalf("tentativa %d: %v menor que a anterior %v", tentativa, espera, anterior)
		}
		anterior = espera
	}

	// Depois de estourar o teto, o valor fica na faixa do teto (75% a 100%).
	for tentativa := 8; tentativa <= 12; tentativa++ {
		espera := esperaComBackoff(r, tentativa, base, teto)
		if espera < teto*3/4 || espera > teto {
			t.Fatalf("tentativa %d: %v fora da faixa do teto %v", tentativa, espera, teto)
		}
	}
}
