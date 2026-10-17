//go:build integration

package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/httpapi"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
	"github.com/FeliPoxZ/jungle_desafio/internal/platform/clock"
	"github.com/FeliPoxZ/jungle_desafio/internal/platform/idgen"
	"github.com/FeliPoxZ/jungle_desafio/internal/testutil"
	"github.com/FeliPoxZ/jungle_desafio/internal/worker"
)

// Testes das DOZE métricas do README §12 — uma função por métrica.
//
// # Por que estes testes existem (defeito 34 e 42)
//
// Até a Fase 10 havia teste do MECANISMO (`ObserveOperation` incrementa o
// contador) e nenhum teste da EMISSÃO. A diferença é concreta: o mecanismo
// funcionava enquanto `wager_operations_total` **não era emitida no caminho SQS**
// (defeito 34) e enquanto `wager_transaction_conflicts_total` **não era emitida
// em lugar nenhum** (defeito 42). Uma métrica que ninguém observa pode ficar em
// zero para sempre sem quebrar teste algum.
//
// A regra aqui: disparar o GATILHO REAL (HTTP autenticado, SQS, publisher real
// contra o LocalStack) e ler o valor em `/metrics` do MESMO registry que o
// componente usa. Nada de ler o objeto em memória: o que se prova é o que o
// Prometheus rasparia.

// --- utilidades --------------------------------------------------------------

// servidorComMetricasProprias sobe a borda HTTP com um caso de uso que conta
// conflitos no registry devolvido.
//
// O caso de uso é NOVO (não o global do TestMain) porque o contador de conflitos
// é injetado nele: é a única forma de provar que a repetição da transação chega
// à métrica.
func servidorComMetricasProprias(t *testing.T) (*httptest.Server, *observability.Metrics) {
	t.Helper()

	metricas := observability.NewMetrics()
	caso := appwagering.New(postgresUOW(t), clock.System{}, idgen.New(), metricas)
	return apiDeTesteCom(t, caso, metricas)
}

// endpointDeMetricas publica o registry no MESMO handler que a aplicação usa em
// `GET /metrics`.
//
// É o caminho para provar métrica de componente (consumidor, publicador, worker)
// sem subir um processo inteiro: o handler é o de produção, e o parser é o do
// Prometheus.
func endpointDeMetricas(t *testing.T, metricas *observability.Metrics) string {
	t.Helper()

	servidor := httptest.NewServer(metricas.Handler())
	t.Cleanup(servidor.Close)
	return servidor.URL
}

// esperarSerie espera uma série atingir o mínimo, raspando `/metrics`.
//
// O consumo e os laços são ASSÍNCRONOS: depois de enviar a mensagem não há como
// saber quando o contador subiu a não ser observando. Polling com prazo, nunca
// `Sleep` fixo.
func esperarSerie(t *testing.T, baseURL, nome string, labels map[string]string, minimo float64, prazo time.Duration) {
	t.Helper()

	fim := time.Now().Add(prazo)
	var ultimo float64
	var existe bool

	for time.Now().Before(fim) {
		valor, ok := testutil.DoEndpoint(t, baseURL).Valor(nome, labels)
		ultimo, existe = valor, ok
		if ok && valor >= minimo {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !existe {
		t.Fatalf("%s não apareceu em /metrics em %v", nome, prazo)
	}
	t.Fatalf("%s = %v depois de %v, esperado pelo menos %v", nome, ultimo, prazo, minimo)
}

// contarEDrenarDLQ devolve quantas mensagens estão na DLQ e as remove.
//
// Drenar de propósito: é a DLQ do LocalStack do compose, compartilhada com os
// outros testes. Uma mensagem deixada ali reapareceria no cenário seguinte e o
// faria falhar por um motivo que não é dele (a lição das Fases 8 e 10).
func contarEDrenarDLQ(t *testing.T, s *sqsDeTeste) int {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	total := 0
	for {
		saida, err := s.api.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
			QueueUrl:            aws.String(s.urlDLQ),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     1,
		})
		if err != nil {
			t.Fatalf("lendo a DLQ: %v", err)
		}
		if len(saida.Messages) == 0 {
			return total
		}
		total += len(saida.Messages)
		for _, m := range saida.Messages {
			if _, err := s.api.DeleteMessage(ctx, &awssqs.DeleteMessageInput{
				QueueUrl:      aws.String(s.urlDLQ),
				ReceiptHandle: m.ReceiptHandle,
			}); err != nil {
				t.Fatalf("removendo da DLQ: %v", err)
			}
		}
	}
}

// --- 1. operations_total + operation_duration_seconds (HTTP) ----------------

func TestMetricaOperacoesPorHTTP(t *testing.T) {
	limpar(t)
	servidor, _ := servidorComMetricasProprias(t)
	walletID, playerID := abrirCarteiraPeloHTTP(t, servidor, "100.00")

	// Processada: 201.
	processada := corpoDeOperacao("provider-a", "metrica-bet-ok", playerID, walletID, "BET", "25.00")
	if resp := chamar(t, servidor, http.MethodPost, "/wagering/transactions",
		tokenDoKeycloak(t, clientProviderA), processada,
		map[string]string{httpapi.HeaderIdempotencyKey: "provider-a:metrica-bet-ok"}); resp.status != http.StatusCreated {
		t.Fatalf("BET processada = %d, quer 201: %s", resp.status, resp.texto())
	}

	// Rejeitada por saldo: 422.
	rejeitada := corpoDeOperacao("provider-a", "metrica-bet-sem-saldo", playerID, walletID, "BET", "500.00")
	if resp := chamar(t, servidor, http.MethodPost, "/wagering/transactions",
		tokenDoKeycloak(t, clientProviderA), rejeitada,
		map[string]string{httpapi.HeaderIdempotencyKey: "provider-a:metrica-bet-sem-saldo"}); resp.status != http.StatusUnprocessableEntity {
		t.Fatalf("BET sem saldo = %d, quer 422: %s", resp.status, resp.texto())
	}

	familias := testutil.DoEndpoint(t, servidor.URL)

	// O MESMO significado de label nos dois caminhos (defeito 43): `kind` é o
	// tipo da operação e `status` é o desfecho do domínio — não o código HTTP.
	familias.Exigir(t, "jungleg_wager_operations_total",
		map[string]string{"kind": "BET", "status": "PROCESSED"}, 1)
	familias.Exigir(t, "jungleg_wager_operations_total",
		map[string]string{"kind": "BET", "status": "REJECTED"}, 1)

	// Latência: uma observação em cada série, com soma positiva (o relógio é o
	// de produção) e o histograma com os buckets declarados.
	contagem, soma, ok := familias.ContagemHistograma("jungleg_wager_operation_duration_seconds",
		map[string]string{"kind": "BET", "status": "PROCESSED"})
	if !ok {
		t.Fatal("a série de latência da operação processada não existe em /metrics")
	}
	if contagem != 1 {
		t.Errorf("observações da latência = %d, quer 1", contagem)
	}
	if soma <= 0 {
		t.Errorf("soma das latências = %v, quer > 0", soma)
	}

	familia := familias["jungleg_wager_operation_duration_seconds"]
	achouBuckets := false
	for _, m := range familia.GetMetric() {
		if len(m.GetLabel()) != 2 || m.GetLabel()[0].GetValue() != "BET" {
			continue
		}
		h := m.GetHistogram()
		// 10 buckets declarados em `metrics.go` + o `+Inf` que o Prometheus
		// acrescenta sempre. A primeira versão desta asserção dizia 10 e estava
		// errada — régua, não código.
		if len(h.GetBucket()) != 11 {
			t.Errorf("buckets = %d, quer 11 (10 declarados + +Inf)", len(h.GetBucket()))
		}
		infinito := h.GetBucket()[len(h.GetBucket())-1]
		if infinito.GetCumulativeCount() != h.GetSampleCount() {
			t.Errorf("bucket +Inf = %d, quer %d (a contagem total)",
				infinito.GetCumulativeCount(), h.GetSampleCount())
		}
		achouBuckets = true
	}
	if !achouBuckets {
		t.Error("não achei a série BET/PROCESSED do histograma para conferir os buckets")
	}
}

// --- 2. idempotent_replays_total --------------------------------------------

func TestMetricaReplayIdempotente(t *testing.T) {
	limpar(t)
	servidor, _ := servidorComMetricasProprias(t)
	walletID, playerID := abrirCarteiraPeloHTTP(t, servidor, "100.00")

	bet := corpoDeOperacao("provider-a", "metrica-replay", playerID, walletID, "BET", "25.00")
	cabecalhos := map[string]string{httpapi.HeaderIdempotencyKey: "provider-a:metrica-replay"}

	if resp := chamar(t, servidor, http.MethodPost, "/wagering/transactions",
		tokenDoKeycloak(t, clientProviderA), bet, cabecalhos); resp.status != http.StatusCreated {
		t.Fatalf("primeira BET = %d, quer 201: %s", resp.status, resp.texto())
	}
	if resp := chamar(t, servidor, http.MethodPost, "/wagering/transactions",
		tokenDoKeycloak(t, clientProviderA), bet, cabecalhos); resp.status != http.StatusOK {
		t.Fatalf("replay = %d, quer 200: %s", resp.status, resp.texto())
	}

	familias := testutil.DoEndpoint(t, servidor.URL)
	familias.Exigir(t, "jungleg_wager_idempotent_replays_total", nil, 1)

	// A operação financeira continua sendo UMA: o contador de operações conta
	// atendimentos (é um contador de tráfego), e o débito no banco é único.
	familias.Exigir(t, "jungleg_wager_operations_total",
		map[string]string{"kind": "BET", "status": "PROCESSED"}, 2)
	if saldo := statusDoSaldo(t, walletID); saldo != 7500 {
		t.Fatalf("saldo = %d, quer 7500 (um débito só)", saldo)
	}
}

// --- 3. transaction_conflicts_total (defeito 42) ----------------------------

func TestMetricaConflitoDeTransacao(t *testing.T) {
	limpar(t)
	servidor, _ := servidorComMetricasProprias(t)
	walletID, playerID := abrirCarteiraPeloHTTP(t, servidor, "100.00")

	// A disputa é REAL: uma conexão de fora segura a linha da carteira com
	// `FOR UPDATE`, e a operação bate no `lock_timeout` do pool (3s), é
	// classificada como transitória e repetida — três vezes, até o teto.
	ctx := context.Background()
	conn, err := poolTeste.Acquire(ctx)
	if err != nil {
		t.Fatalf("adquirindo conexão para segurar o lock: %v", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("abrindo a transação que segura o lock: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT id FROM wallets WHERE id = $1 FOR UPDATE`, walletID); err != nil {
		t.Fatalf("travando a carteira: %v", err)
	}

	bet := corpoDeOperacao("provider-a", "metrica-conflito", playerID, walletID, "BET", "10.00")
	resp := chamar(t, servidor, http.MethodPost, "/wagering/transactions",
		tokenDoKeycloak(t, clientProviderA), bet,
		map[string]string{httpapi.HeaderIdempotencyKey: "provider-a:metrica-conflito"})

	// Três tentativas, todas bloqueadas: a borda devolve indisponibilidade.
	if resp.status != http.StatusServiceUnavailable {
		t.Fatalf("BET com a carteira travada = %d, quer 503: %s", resp.status, resp.texto())
	}

	familias := testutil.DoEndpoint(t, servidor.URL)
	// Duas repetições (a terceira tentativa sobe com o erro, não é conflito
	// tratado) — o valor exato é contrato: `tentativasMaximas = 3`.
	familias.Exigir(t, "jungleg_wager_transaction_conflicts_total", nil, 2)

	// Nenhum efeito financeiro: as três tentativas fizeram rollback.
	if saldo := statusDoSaldo(t, walletID); saldo != 10000 {
		t.Errorf("saldo = %d, quer 10000 (nada foi debitado)", saldo)
	}
}

// --- 4. operations_total no caminho SQS (o defeito 34) ---------------------

func TestMetricaOperacoesPelaFila(t *testing.T) {
	limpar(t)
	fila := abrirSQS(t)
	fila.esvaziarFila(t, fila.urlTxn)
	fila.esvaziarFila(t, fila.urlDLQ)
	t.Cleanup(func() {
		fila.esvaziarFila(t, fila.urlTxn)
		fila.esvaziarFila(t, fila.urlDLQ)
	})

	metricas := observability.NewMetrics()
	base := endpointDeMetricas(t, metricas)

	walletID, playerID := carteiraComSaldo(t, "100.00")
	corpo := mensagemParaOperacao("provider-a", "fila-metrica-1", "fila-metrica-1", playerID,
		walletID, "round-1", "fortune-chimp", "BET", "10.00")
	fila.enviar(t, corpo, "fila-metrica-1")

	fila.rodarConsumidorCom(t, metricas)

	esperarSerie(t, base, "jungleg_wager_operations_total",
		map[string]string{"kind": "BET", "status": "PROCESSED"}, 1, 30*time.Second)

	// A latência do caminho assíncrono também é medida (é o que o painel usa
	// para o p99 do SQS).
	familias := testutil.DoEndpoint(t, base)
	contagem, _, ok := familias.ContagemHistograma("jungleg_wager_operation_duration_seconds",
		map[string]string{"kind": "BET", "status": "PROCESSED"})
	if !ok || contagem < 1 {
		t.Fatalf("latência do caminho SQS: contagem = %d (ok=%v), quer pelo menos 1", contagem, ok)
	}
}

// --- 5. inbox_duplicates_total ---------------------------------------------

func TestMetricaDuplicataDaInbox(t *testing.T) {
	limpar(t)
	fila := abrirSQS(t)
	fila.esvaziarFila(t, fila.urlTxn)
	fila.esvaziarFila(t, fila.urlDLQ)
	t.Cleanup(func() {
		fila.esvaziarFila(t, fila.urlTxn)
		fila.esvaziarFila(t, fila.urlDLQ)
	})

	metricas := observability.NewMetrics()
	base := endpointDeMetricas(t, metricas)

	walletID, playerID := carteiraComSaldo(t, "100.00")
	corpo := mensagemParaOperacao("provider-a", "dup-metrica", "dup-metrica", playerID,
		walletID, "round-1", "fortune-chimp", "BET", "10.00")

	// Publica e descobre o `MessageId` do BROKER, que é a identidade da inbox.
	idDaEntrega := fila.enviar(t, corpo, "dup-metrica")

	// O estado de quem morreu entre o commit e o `Delete`: a inbox já tem a
	// mensagem como PROCESSED com o MESMO hash do corpo. A reentrega encontra
	// isso e a reconhece como duplicata.
	registrarInboxComoProcessada(t, idDaEntrega, corpo)

	fila.rodarConsumidorCom(t, metricas)

	esperarSerie(t, base, "jungleg_wager_inbox_duplicates_total", nil, 1, 30*time.Second)

	// E o débito NÃO aconteceu duas vezes.
	if saldo := statusDoSaldo(t, walletID); saldo != 10000 {
		t.Fatalf("saldo = %d, quer 10000 (a reentrega não pode debitar)", saldo)
	}
}

// --- 6. dlq_messages_total --------------------------------------------------

func TestMetricaMensagemParaDLQ(t *testing.T) {
	limpar(t)
	fila := abrirSQS(t)
	fila.esvaziarFila(t, fila.urlTxn)
	fila.esvaziarFila(t, fila.urlDLQ)
	t.Cleanup(func() {
		fila.esvaziarFila(t, fila.urlTxn)
		fila.esvaziarFila(t, fila.urlDLQ)
	})

	metricas := observability.NewMetrics()
	base := endpointDeMetricas(t, metricas)

	// Corpo que NUNCA vai funcionar: o envelope está vazio. É mensagem inválida,
	// classificada como permanente — o destino é a DLQ na primeira detecção.
	fila.enviar(t, `{"type":"WagerTransactionRequested","data":{}}`, "dlq-metrica")

	fila.rodarConsumidorCom(t, metricas)

	esperarSerie(t, base, "jungleg_wager_dlq_messages_total", nil, 1, 30*time.Second)

	if total := contarEDrenarDLQ(t, fila); total != 1 {
		t.Errorf("mensagens na DLQ = %d, quer 1", total)
	}
}

// --- 7. outbox_publish_total, outbox_publish_attempts, pending_age ---------

func TestMetricasDaOutbox(t *testing.T) {
	limpar(t)
	fila := abrirFilaDeEventos(t)
	fila.esvaziar(t)
	t.Cleanup(func() { fila.esvaziar(t) })

	metricas := observability.NewMetrics()
	base := endpointDeMetricas(t, metricas)

	// A abertura grava DOIS eventos (Processed + BalanceChanged) no mesmo
	// commit da carteira.
	carteiraAberta(t, "100.00")

	// Evento antigo de propósito: a idade do pendente mais antigo é o que o
	// alerta de "outbox atrasada" lê, e um evento recém-criado daria ~0.
	if _, err := poolTeste.Exec(context.Background(),
		`UPDATE outbox_events SET occurred_at = now() - interval '30 seconds'
		 WHERE published_at IS NULL`); err != nil {
		t.Fatalf("envelhecendo os eventos pendentes: %v", err)
	}

	// Lote de 1: um evento sai, o outro fica pendente — é o que mantém o gauge
	// de atraso acima de zero de forma determinística (sem esperar 15s da
	// medição periódica).
	cfg := configDaOutbox(5*time.Second, 1)
	publicador := worker.NovoOutboxPublisher(worker.OutboxPublisherOptions{
		UOW:        postgresUOW(t),
		Publicador: fila.pub,
		Logger:     loggerDeTeste(),
		Metrics:    metricas,
		Config:     cfg,
	})

	publicados, err := publicador.PublicarPendentes(context.Background())
	if err != nil {
		t.Fatalf("PublicarPendentes: %v", err)
	}
	if publicados != 1 {
		t.Fatalf("publicados = %d, quer 1 (lote de 1)", publicados)
	}

	familias := testutil.DoEndpoint(t, base)

	familias.Exigir(t, "jungleg_wager_outbox_publish_total",
		map[string]string{"result": "success"}, 1)

	contagem, _, ok := familias.ContagemHistograma("jungleg_wager_outbox_publish_attempts", nil)
	if !ok || contagem != 1 {
		t.Errorf("tentativas até publicar: contagem = %d (ok=%v), quer 1", contagem, ok)
	}

	idade, ok := familias.Valor("jungleg_wager_outbox_pending_age_seconds", nil)
	if !ok {
		t.Fatal("o gauge de atraso da outbox não existe em /metrics")
	}
	if idade < 25 {
		t.Errorf("atraso da outbox = %vs, quer pelo menos 25s (o evento tem 30s)", idade)
	}

	// O segundo evento continua pendente: nada foi perdido nem marcado como
	// publicado sem ter saído.
	if pendentes := eventosPendentesNaOutbox(t); pendentes != 1 {
		t.Errorf("pendentes na outbox = %d, quer 1", pendentes)
	}
}

// --- 8. reconciliation_divergences_total ------------------------------------

func TestMetricaReconciliacaoDivergente(t *testing.T) {
	limpar(t)
	servidor, _ := servidorComMetricasProprias(t)
	walletID, _ := abrirCarteiraPeloHTTP(t, servidor, "100.00")

	// Divergência PROVOCADA no banco, como um incidente de verdade: o saldo
	// armazenado deixa de casar com a soma do ledger.
	if _, err := poolTeste.Exec(context.Background(),
		`UPDATE wallets SET balance_minor = balance_minor - 1000 WHERE id = $1`, walletID); err != nil {
		t.Fatalf("divergindo o saldo: %v", err)
	}

	resp := chamar(t, servidor, http.MethodPost, "/wallets/"+walletID+"/reconciliation",
		tokenDoKeycloak(t, clientInterno), nil, nil)
	if resp.status != http.StatusOK {
		t.Fatalf("reconciliação = %d, quer 200: %s", resp.status, resp.texto())
	}

	var corpo struct {
		Consistent bool `json:"consistent"`
	}
	resp.decodifica(t, &corpo)
	if corpo.Consistent {
		t.Fatal("a reconciliação disse consistente para um saldo divergente")
	}

	familias := testutil.DoEndpoint(t, servidor.URL)
	familias.Exigir(t, "jungleg_wager_reconciliation_divergences_total", nil, 1)
}

// --- 9. reference_resolution_total -----------------------------------------

func TestMetricaResolucaoDeReferencia(t *testing.T) {
	limpar(t)
	carteira := carteiraAberta(t, "100.00")

	// O REFUND chega antes da BET: fica PENDING_REFERENCE sem movimentar nada.
	reembolso := pedido(carteira, wagerdomain.KindRefund, "25.00",
		comChave("metrica-key-refund"), comExterna("metrica-ext-refund"), comReferencia("metrica-ext-bet"))
	if res := processarPeloServico(t, reembolso); res.Status != wagerdomain.StatusPendingReference {
		t.Fatalf("REFUND sem referência = %s, quer PENDING_REFERENCE", res.Status)
	}

	// A BET chega e processa; agora a referência existe.
	bet := pedido(carteira, wagerdomain.KindBet, "25.00",
		comChave("metrica-key-bet"), comExterna("metrica-ext-bet"))
	if res := processarPeloServico(t, bet); res.Status != wagerdomain.StatusProcessed {
		t.Fatalf("BET = %s, quer PROCESSED", res.Status)
	}

	// O intake agendou a próxima tentativa para "agora + 5s"; o teste antecipa o
	// instante da LINHA em vez de esperar (é fixture, não regra).
	if _, err := poolTeste.Exec(context.Background(),
		`UPDATE wager_transactions SET next_resolution_attempt_at = now() - interval '1 second'
		 WHERE idempotency_key = 'metrica-key-refund'`); err != nil {
		t.Fatalf("antecipando o instante da próxima tentativa: %v", err)
	}

	metricas := observability.NewMetrics()
	base := endpointDeMetricas(t, metricas)

	workerRef := worker.NovoReferenceWorker(worker.ReferenceWorkerOptions{
		UOW:       postgresUOW(t),
		Retomador: servico,
		Logger:    loggerDeTeste(),
		Metrics:   metricas,
		Config: &config.Config{Reference: config.ReferenceConfig{
			PollInterval:  time.Second,
			BatchSize:     25,
			MaxAttempts:   20,
			BaseBackoff:   5 * time.Second,
			MaxBackoff:    10 * time.Minute,
			ReaperEvery:   time.Minute,
			PendingMaxAge: 2 * time.Minute,
		}},
	})

	retomadas, err := workerRef.RetomarVencidas(context.Background())
	if err != nil {
		t.Fatalf("RetomarVencidas: %v", err)
	}
	if retomadas != 1 {
		t.Fatalf("retomadas = %d, quer 1", retomadas)
	}

	familias := testutil.DoEndpoint(t, base)
	familias.Exigir(t, "jungleg_wager_reference_resolution_total",
		map[string]string{"result": "resolvida"}, 1)
}

// --- 10. cardinalidade: nenhum label é identificador -----------------------

// TestMetricasSemLabelsDeAltaCardinalidade transforma em asserção a regra que
// está escrita no `metrics.go`: label só com valor de cardinalidade baixa.
//
// Um `walletId` como label criaria uma série temporal por carteira; em um serviço
// com milhares de jogadores isso derruba o Prometheus da instância. A regra não
// pode depender de revisão de código — aqui ela é medida no que está exposto.
func TestMetricasSemLabelsDeAltaCardinalidade(t *testing.T) {
	limpar(t)
	servidor, _ := servidorComMetricasProprias(t)
	walletID, playerID := abrirCarteiraPeloHTTP(t, servidor, "100.00")

	bet := corpoDeOperacao("provider-a", "metrica-cardinalidade", playerID, walletID, "BET", "25.00")
	if resp := chamar(t, servidor, http.MethodPost, "/wagering/transactions",
		tokenDoKeycloak(t, clientProviderA), bet,
		map[string]string{httpapi.HeaderIdempotencyKey: "provider-a:metrica-cardinalidade"}); resp.status != http.StatusCreated {
		t.Fatalf("BET = %d, quer 201: %s", resp.status, resp.texto())
	}

	familias := testutil.DoEndpoint(t, servidor.URL)

	proibidos := map[string]bool{
		"walletId": true, "transactionId": true, "providerId": true,
		"messageId": true, "eventId": true, "playerId": true, "correlationId": true,
	}
	formatoUUID := regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

	for nome, familia := range familias {
		if !strings.HasPrefix(nome, "jungleg_wager_") {
			continue
		}
		for _, metrica := range familia.GetMetric() {
			for _, par := range metrica.GetLabel() {
				if proibidos[par.GetName()] {
					t.Errorf("%s usa o label %q, que é identificador", nome, par.GetName())
				}
				if formatoUUID.MatchString(par.GetValue()) {
					t.Errorf("%s tem valor de label com formato de UUID: %s=%q",
						nome, par.GetName(), par.GetValue())
				}
			}
		}
	}
}
