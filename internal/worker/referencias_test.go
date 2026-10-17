package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
)

// ---------------------------------------------------------------------------
// Dublês
// ---------------------------------------------------------------------------

// transacaoDeTeste monta uma transação em PENDING_REFERENCE vencida.
func transacaoDeTeste(id string, attempt int, proximo, criadaEm time.Time) *wagerdomain.WagerTransaction {
	return wagerdomain.Rehydrate(
		wagerdomain.TransactionID(id),
		"carteira-1",
		"jogador-1",
		wagerdomain.KindRefund,
		wagerdomain.StatusPendingReference,
		money.FromMinor(2500, money.BRL),
		"game-1", "round-1",
		"key-"+id, wagerdomain.Hash("hash-"+id),
		"provider-a", "ext-"+id, "ext-bet", "",
		"corr-"+id,
		"", nil, attempt, proximo, criadaEm, criadaEm,
	)
}

// txRepoFake implementa só o que o worker usa.
//
// A interface do repositório é embutida NIL: qualquer método que o worker chamar
// sem estar previsto aqui estoura em tempo de execução — o que é a asserção de
// ausência mais forte que existe para "o worker não deveria consultar mais nada".
type txRepoFake struct {
	ports.TransactionRepository
	u *uowTxFake
}

func (r *txRepoFake) ClaimPendingReferencesDue(_ context.Context, limit int, lease time.Duration, now time.Time) ([]*wagerdomain.WagerTransaction, error) {
	r.u.registro.registrar("claim")
	r.u.leaseAplicado = lease
	if r.u.erro != nil {
		return nil, r.u.erro
	}
	var saida []*wagerdomain.WagerTransaction
	for _, tx := range r.u.vencidas {
		saida = append(saida, tx)
		if len(saida) == limit {
			break
		}
	}
	return saida, nil
}

func (r *txRepoFake) ListPendingStale(_ context.Context, now time.Time, maxAge time.Duration, limit int) ([]*wagerdomain.WagerTransaction, error) {
	r.u.registro.registrar("listStale")
	r.u.idadeUsada = maxAge
	if r.u.erro != nil {
		return nil, r.u.erro
	}
	var saida []*wagerdomain.WagerTransaction
	for _, tx := range r.u.presas {
		saida = append(saida, tx)
		if len(saida) == limit {
			break
		}
	}
	return saida, nil
}

type uowTxFake struct {
	registro      *registrador
	vencidas      []*wagerdomain.WagerTransaction
	presas        []*wagerdomain.WagerTransaction
	erro          error
	leaseAplicado time.Duration
	idadeUsada    time.Duration
}

func (u *uowTxFake) Run(ctx context.Context, fn func(context.Context, ports.Repos) error) error {
	if u.erro != nil {
		return u.erro
	}
	return fn(ctx, ports.Repos{Transactions: &txRepoFake{u: u}})
}

func (u *uowTxFake) RunReadOnly(ctx context.Context, fn func(context.Context, ports.Repos) error) error {
	return u.Run(ctx, fn)
}

// retomadorFake registra os pedidos e devolve os desfechos enfileirados.
type retomadorFake struct {
	registro  *registrador
	desfechos []appwagering.Retomada
	erros     []error
	pedidos   []appwagering.RetomarRequest
	indice    int
}

func (r *retomadorFake) Retomar(_ context.Context, req appwagering.RetomarRequest) (appwagering.Retomada, error) {
	r.registro.registrar("retomar")
	r.pedidos = append(r.pedidos, req)

	i := r.indice
	r.indice++

	// A última resposta se repete: os testes com uma linha só não precisam
	// enfileirar nada.
	if len(r.erros) > i && r.erros[i] != nil {
		return appwagering.Retomada{}, r.erros[i]
	}
	if len(r.desfechos) > i {
		return r.desfechos[i], nil
	}
	if len(r.desfechos) > 0 {
		return r.desfechos[len(r.desfechos)-1], nil
	}
	return appwagering.Retomada{
		Desfecho: appwagering.RetomadaResolvida,
		Result:   appwagering.Result{Status: wagerdomain.StatusProcessed},
	}, nil
}

type ambienteReferencias struct {
	worker   *ReferenceWorker
	reaper   *PendingReaper
	uow      *uowTxFake
	retomar  *retomadorFake
	registro *registrador
	metrics  *observability.Metrics
}

func novoAmbienteReferencias(t *testing.T, cfg config.ReferenceConfig) *ambienteReferencias {
	t.Helper()

	registro := &registrador{}
	uow := &uowTxFake{registro: registro}
	retomador := &retomadorFake{registro: registro}
	metrics := observability.NewMetrics()

	if cfg.BatchSize == 0 {
		cfg.BatchSize = 25
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = 20
	}
	if cfg.BaseBackoff == 0 {
		cfg.BaseBackoff = 5 * time.Second
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = 10 * time.Minute
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = time.Millisecond
	}
	if cfg.PendingMaxAge == 0 {
		cfg.PendingMaxAge = 2 * time.Minute
	}
	if cfg.ReaperEvery == 0 {
		cfg.ReaperEvery = time.Minute
	}

	configuracoes := &config.Config{Reference: cfg}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	worker := NovoReferenceWorker(ReferenceWorkerOptions{
		UOW: uow, Retomador: retomador, Logger: logger, Metrics: metrics, Config: configuracoes,
	})
	worker.rand = rand.New(rand.NewSource(1))

	reaper := NovoPendingReaper(PendingReaperOptions{
		UOW: uow, Retomador: retomador, Logger: logger, Metrics: metrics, Config: configuracoes,
	})

	return &ambienteReferencias{
		worker: worker, reaper: reaper, uow: uow, retomar: retomador,
		registro: registro, metrics: metrics,
	}
}

func metricasDeTexto(t *testing.T, m *observability.Metrics) string {
	t.Helper()

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rec.Body.String()
}

// ---------------------------------------------------------------------------
// Worker de referências
// ---------------------------------------------------------------------------

// O caminho feliz: a linha vencida é reivindicada e retomada, com a política
// configurada e um instante de reagendamento no futuro.
func TestWorkerDeReferenciasRetomaOVencido(t *testing.T) {
	amb := novoAmbienteReferencias(t, config.ReferenceConfig{MaxAttempts: 7, PendingMaxAge: 3 * time.Minute})
	agora := time.Now().UTC()
	amb.uow.vencidas = []*wagerdomain.WagerTransaction{
		transacaoDeTeste("txn-1", 2, agora.Add(-time.Second), agora.Add(-time.Minute)),
	}

	processadas, err := amb.worker.RetomarVencidas(context.Background())
	if err != nil {
		t.Fatalf("RetomarVencidas: %v", err)
	}
	if processadas != 1 {
		t.Fatalf("processadas = %d, esperado 1", processadas)
	}

	if len(amb.retomar.pedidos) != 1 {
		t.Fatalf("o caso de uso deveria ter sido chamado 1 vez, foi %d", len(amb.retomar.pedidos))
	}
	pedido := amb.retomar.pedidos[0]
	if pedido.TransactionID != "txn-1" {
		t.Errorf("transactionId = %q", pedido.TransactionID)
	}
	if pedido.Politica.MaxAttempts != 7 {
		t.Errorf("MaxAttempts = %d, esperado o da config (7)", pedido.Politica.MaxAttempts)
	}
	if pedido.Politica.PendingMaxAge != 3*time.Minute {
		t.Errorf("PendingMaxAge = %v, esperado o da config", pedido.Politica.PendingMaxAge)
	}
	// Backoff: tentativa 3 (a linha tinha 2), base 5s, teto 10m -> entre 3.75s e
	// 20s. O jitter é subtraído DENTRO do teto.
	espera := time.Until(pedido.Politica.NextAttemptAt)
	if espera < 3*time.Second || espera > 20*time.Second {
		t.Errorf("próxima tentativa em %v, esperado entre 3s e 20s (backoff 2^1 * 5s com jitter)", espera)
	}

	// O lease é a janela em que a linha fica fora do alcance de outro worker; com
	// teto de 10m, ele vale o teto (não pode ser menor que o próprio backoff).
	if amb.uow.leaseAplicado < 30*time.Second {
		t.Errorf("lease = %v, esperado pelo menos 30s", amb.uow.leaseAplicado)
	}

	corpo := metricasDeTexto(t, amb.metrics)
	if !strings.Contains(corpo, `jungleg_wager_reference_resolution_total{result="resolvida"} 1`) {
		t.Errorf("a métrica do desfecho não foi emitida:\n%s", corpo)
	}
}

// AUSÊNCIA de chamada: sem linha vencida, o caso de uso não é chamado.
func TestWorkerDeReferenciasSemVencidasNaoChamaOCasoDeUso(t *testing.T) {
	amb := novoAmbienteReferencias(t, config.ReferenceConfig{})

	processadas, err := amb.worker.RetomarVencidas(context.Background())
	if err != nil {
		t.Fatalf("RetomarVencidas: %v", err)
	}
	if processadas != 0 {
		t.Fatalf("processadas = %d, esperado 0", processadas)
	}
	if amb.registro.contem("retomar") {
		t.Errorf("o caso de uso não deveria ser chamado sem vencidas: %v", amb.registro.lista())
	}
	if !amb.registro.contem("claim") {
		t.Errorf("o claim deveria acontecer mesmo sem vencidas: %v", amb.registro.lista())
	}
}

// Uma linha que falha não pode impedir a próxima do lote: o problema de uma
// transação não é o problema das outras.
func TestWorkerDeReferenciasErroNumaLinhaNaoInterrompeOLote(t *testing.T) {
	amb := novoAmbienteReferencias(t, config.ReferenceConfig{})
	agora := time.Now().UTC()
	amb.uow.vencidas = []*wagerdomain.WagerTransaction{
		transacaoDeTeste("txn-1", 1, agora.Add(-time.Second), agora.Add(-time.Minute)),
		transacaoDeTeste("txn-2", 1, agora.Add(-time.Second), agora.Add(-time.Minute)),
	}
	amb.retomar.erros = []error{errors.New("banco indisponível"), nil}

	processadas, err := amb.worker.RetomarVencidas(context.Background())
	if err != nil {
		t.Fatalf("RetomarVencidas: %v", err)
	}
	if processadas != 2 {
		t.Fatalf("processadas = %d, esperado 2 (a falha de uma não impede a outra)", processadas)
	}
	if len(amb.retomar.pedidos) != 2 {
		t.Errorf("o caso de uso deveria ter sido chamado para as duas linhas, foi %d", len(amb.retomar.pedidos))
	}
}

// O laço para no cancelamento do contexto.
func TestWorkerDeReferenciasParaNoCancelamento(t *testing.T) {
	amb := novoAmbienteReferencias(t, config.ReferenceConfig{PollInterval: time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	parou := make(chan error, 1)
	go func() { parou <- amb.worker.Run(ctx) }()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-parou:
		if err != nil {
			t.Errorf("Run devolveu erro no cancelamento: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run não parou no cancelamento")
	}
}

// ---------------------------------------------------------------------------
// Reaper
// ---------------------------------------------------------------------------

// A transação presa em PENDING é retomada, com o critério de idade configurado.
func TestReaperRetomaPresaEmPending(t *testing.T) {
	amb := novoAmbienteReferencias(t, config.ReferenceConfig{PendingMaxAge: 2 * time.Minute, BaseBackoff: 5 * time.Second})
	agora := time.Now().UTC()
	amb.uow.presas = []*wagerdomain.WagerTransaction{
		transacaoDeTeste("txn-presa", 0, time.Time{}, agora.Add(-10*time.Minute)),
	}
	amb.retomar.desfechos = []appwagering.Retomada{{
		Desfecho: appwagering.RetomadaPendente,
		Result:   appwagering.Result{Status: wagerdomain.StatusPendingReference},
	}}

	quantas, err := amb.reaper.Varrer(context.Background())
	if err != nil {
		t.Fatalf("Varrer: %v", err)
	}
	if quantas != 1 {
		t.Fatalf("quantas = %d, esperado 1", quantas)
	}

	if amb.uow.idadeUsada != 2*time.Minute {
		t.Errorf("idade usada = %v, esperado o PendingMaxAge da config", amb.uow.idadeUsada)
	}
	if len(amb.retomar.pedidos) != 1 {
		t.Fatalf("o reaper deveria ter retomado 1 linha, retomou %d", len(amb.retomar.pedidos))
	}
	// O reaper NÃO aplica TTL por idade: a linha está em PENDING, e o relógio da
	// referência só começa quando ela vira PENDING_REFERENCE.
	if amb.retomar.pedidos[0].Politica.PendingMaxAge != 0 {
		t.Errorf("PendingMaxAge = %v, esperado 0 no reaper", amb.retomar.pedidos[0].Politica.PendingMaxAge)
	}
}

// AUSÊNCIA de chamada: sem linha presa, nada acontece.
func TestReaperSemPresasNaoChamaOCasoDeUso(t *testing.T) {
	amb := novoAmbienteReferencias(t, config.ReferenceConfig{})

	quantas, err := amb.reaper.Varrer(context.Background())
	if err != nil {
		t.Fatalf("Varrer: %v", err)
	}
	if quantas != 0 {
		t.Fatalf("quantas = %d, esperado 0", quantas)
	}
	if amb.registro.contem("retomar") {
		t.Errorf("o reaper não deveria chamar o caso de uso sem linhas presas: %v", amb.registro.lista())
	}
}

// O reaper também registra o próprio desfecho, com prefixo, para não se
// confundir com o do worker de referências na mesma métrica.
func TestReaperRegistraDesfechoComPrefixo(t *testing.T) {
	amb := novoAmbienteReferencias(t, config.ReferenceConfig{})
	agora := time.Now().UTC()
	amb.uow.presas = []*wagerdomain.WagerTransaction{
		transacaoDeTeste("txn-presa", 0, time.Time{}, agora.Add(-10*time.Minute)),
	}

	if _, err := amb.reaper.Varrer(context.Background()); err != nil {
		t.Fatalf("Varrer: %v", err)
	}

	corpo := metricasDeTexto(t, amb.metrics)
	if !strings.Contains(corpo, `jungleg_wager_reference_resolution_total{result="reaper_resolvida"} 1`) {
		t.Errorf("a métrica do reaper não foi emitida com o prefixo:\n%s", corpo)
	}
}
