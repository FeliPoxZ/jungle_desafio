//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
	"github.com/FeliPoxZ/jungle_desafio/internal/worker"
)

// ---------------------------------------------------------------------------
// Ajuda
// ---------------------------------------------------------------------------

// configDaOutbox é a configuração do publicador nos testes.
//
// O LEASE é curto de propósito: a recuperação de trabalho abandonado é provada
// esperando o lease vencer, e com o padrão de 30s o teste ficaria refém do
// próprio prazo. O valor de produção continua sendo o do `.env.example`.
func configDaOutbox(lease time.Duration, batch int32) *config.Config {
	return &config.Config{Outbox: config.OutboxConfig{
		Enabled:       false,
		BatchSize:     batch,
		Lease:         lease,
		PollInterval:  time.Second,
		MaxAttempts:   10,
		BaseBackoff:   2 * time.Second,
		MaxBackoff:    5 * time.Minute,
		ParkedBackoff: time.Hour,
	}}
}

// publicadorDeTeste monta um publicador com o Unit of Work REAL e o adaptador REAL.
//
// Cada chamada cria um Unit of Work novo sobre o mesmo pool (`postgresUOW`), que é
// o que simula OUTRA INSTÂNCIA: conexões e memória próprias, estado compartilhado
// apenas pelo banco.
func publicadorDeTeste(t *testing.T, fila *filaDeEventos, cfg *config.Config) *worker.OutboxPublisher {
	t.Helper()

	return worker.NovoOutboxPublisher(worker.OutboxPublisherOptions{
		UOW:        postgresUOW(t),
		Publicador: fila.pub,
		Logger:     loggerDeTeste(),
		Metrics:    observability.NewMetrics(),
		Config:     cfg,
	})
}

// eventosPendentesNaOutbox conta os eventos que ainda não foram publicados.
func eventosPendentesNaOutbox(t *testing.T) int {
	t.Helper()

	var total int
	if err := poolTeste.QueryRow(context.Background(),
		`SELECT count(*) FROM outbox_events WHERE published_at IS NULL`).Scan(&total); err != nil {
		t.Fatalf("contando pendentes da outbox: %v", err)
	}
	return total
}

// carteiraAberta abre a carteira pela REGRA DE NEGÓCIO e devolve a entidade.
//
// A abertura pela regra é o que deixa o lançamento de abertura e os DOIS eventos
// (Processed + BalanceChanged) na outbox — a mesma coisa que o `POST /wallets`
// faz. Criar a carteira por `INSERT` direto deixaria o ledger sem a abertura e as
// asserções de lançamento ficariam erradas (foi uma das réguas erradas da Fase 8).
func carteiraAberta(t *testing.T, saldo string) *wallet.Wallet {
	t.Helper()

	aberta, err := servico.OpenWallet(context.Background(), appwagering.OpenWalletRequest{
		PlayerID:       ids.PlayerID(uuid.NewString()),
		InitialBalance: brl(t, saldo),
		IdempotencyKey: "abertura-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("OpenWallet: %v", err)
	}

	var w *wallet.Wallet
	if err := uow.Run(context.Background(), func(ctx context.Context, r ports.Repos) error {
		var e error
		w, e = r.Wallets.GetByID(ctx, aberta.WalletID)
		return e
	}); err != nil {
		t.Fatalf("lendo a carteira aberta: %v", err)
	}
	return w
}

// ultimosEventos devolve os tipos dos `n` eventos mais recentes da outbox, em
// ordem de `sequence` (a ordem de inserção).
func ultimosEventos(t *testing.T, n int) []string {
	t.Helper()

	linhas, err := poolTeste.Query(context.Background(),
		`SELECT event_type FROM outbox_events ORDER BY sequence DESC LIMIT $1`, n)
	if err != nil {
		t.Fatalf("lendo os ultimos eventos: %v", err)
	}
	defer linhas.Close()

	var saida []string
	for linhas.Next() {
		var tipo string
		if err := linhas.Scan(&tipo); err != nil {
			t.Fatalf("lendo o tipo do evento: %v", err)
		}
		saida = append(saida, tipo)
	}
	return saida
}

// reaperDeTeste monta o reaper com a configuração da fase.
func reaperDeTeste(t *testing.T, pendenteMaxAge time.Duration) *worker.PendingReaper {
	t.Helper()

	return worker.NovoPendingReaper(worker.PendingReaperOptions{
		UOW:       postgresUOW(t),
		Retomador: servico,
		Logger:    loggerDeTeste(),
		Metrics:   observability.NewMetrics(),
		Config: &config.Config{Reference: config.ReferenceConfig{
			PollInterval:  time.Second,
			BatchSize:     25,
			MaxAttempts:   20,
			BaseBackoff:   5 * time.Second,
			MaxBackoff:    10 * time.Minute,
			ReaperEvery:   time.Minute,
			PendingMaxAge: pendenteMaxAge,
		}},
	})
}

// workerDeReferenciasDeTeste monta o worker de referências.
func workerDeReferenciasDeTeste(t *testing.T, maxAttempts int, pendenteMaxAge time.Duration) *worker.ReferenceWorker {
	t.Helper()

	return worker.NovoReferenceWorker(worker.ReferenceWorkerOptions{
		UOW:       postgresUOW(t),
		Retomador: servico,
		Logger:    loggerDeTeste(),
		Metrics:   observability.NewMetrics(),
		Config: &config.Config{Reference: config.ReferenceConfig{
			PollInterval:  time.Second,
			BatchSize:     25,
			MaxAttempts:   int32(maxAttempts),
			BaseBackoff:   5 * time.Second,
			MaxBackoff:    10 * time.Minute,
			ReaperEvery:   time.Minute,
			PendingMaxAge: pendenteMaxAge,
		}},
	})
}

// eventosPendentes lê os eventos pendentes no formato do publicador.
func eventosPendentes(t *testing.T) []worker.Evento {
	t.Helper()

	linhas, err := poolTeste.Query(context.Background(),
		`SELECT event_id, aggregate_id, event_type, payload, occurred_at, attempts
		 FROM outbox_events WHERE published_at IS NULL ORDER BY sequence`)
	if err != nil {
		t.Fatalf("lendo os eventos pendentes: %v", err)
	}
	defer linhas.Close()

	var saida []worker.Evento
	for linhas.Next() {
		var ev worker.Evento
		if err := linhas.Scan(&ev.EventID, &ev.AggregateID, &ev.Tipo, &ev.Payload, &ev.OccurredAt, &ev.Tentativas); err != nil {
			t.Fatalf("lendo evento pendente: %v", err)
		}
		saida = append(saida, ev)
	}
	return saida
}

// ---------------------------------------------------------------------------
// T6 (README §13.6): dois publishers disputando a mesma outbox
// ---------------------------------------------------------------------------

// Dois publishers sobre a MESMA outbox: nenhuma publicação perdida, nenhuma
// duplicada a rigor (cada evento sai da outbox uma vez) e a ordem por `sequence`
// preservada dentro de cada carteira.
//
// O que este teste prova, exatamente:
//   - o `FOR UPDATE SKIP LOCKED` do `ClaimBatch` faz os dois pegarem lotes
//     DIFERENTES (nenhum evento é reivindicado pelos dois);
//   - o `MarkPublished` de um não esconde o trabalho do outro;
//   - os eventos de UMA carteira saem na ordem de inserção (Processed, depois
//     BalanceChanged), que é a ordem que o `MessageGroupId = walletId` da fila
//     FIFO entrega.
func TestDoisPublishersDisputamAMesmaOutbox(t *testing.T) {
	fila := abrirFilaDeEventos(t)
	limpar(t)
	fila.esvaziar(t)

	// Quatro carteiras, dois eventos cada = 8 eventos.
	carteiras := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		carteiras = append(carteiras, string(carteiraAberta(t, "100.00").ID()))
	}
	if pendentes := eventosPendentesNaOutbox(t); pendentes != 8 {
		t.Fatalf("pendentes na outbox = %d, esperado 8", pendentes)
	}

	// Batch de 2: os dois publishers têm trabalho de verdade e disputam as
	// linhas, em vez de um pegar tudo de uma vez.
	cfg := configDaOutbox(30*time.Second, 2)
	pubA := publicadorDeTeste(t, fila, cfg)
	pubB := publicadorDeTeste(t, fila, cfg)

	inicio := make(chan struct{})
	var wg sync.WaitGroup
	for _, pub := range []*worker.OutboxPublisher{pubA, pubB} {
		wg.Add(1)
		go func(p *worker.OutboxPublisher) {
			defer wg.Done()
			<-inicio
			// Várias rodadas: as primeiras encontram trabalho, as últimas não. É
			// a corrida real entre dois processos sobre a mesma fila de trabalho.
			for i := 0; i < 5; i++ {
				if _, err := p.PublicarPendentes(context.Background()); err != nil {
					t.Errorf("PublicarPendentes: %v", err)
					return
				}
			}
		}(pub)
	}
	close(inicio)
	wg.Wait()

	if pendentes := eventosPendentesNaOutbox(t); pendentes != 0 {
		t.Fatalf("pendentes depois da disputa = %d, esperado 0 (nenhum evento perdido)", pendentes)
	}

	mensagens := fila.ler(t, 8)
	if len(mensagens) != 8 {
		t.Fatalf("a fila recebeu %d mensagens, esperado 8 (nenhuma publicacao duplicada)", len(mensagens))
	}

	// Nenhum evento publicado duas vezes: os `eventId` do corpo são distintos.
	vistos := map[string]int{}
	porCarteira := map[string][]string{}
	for _, m := range mensagens {
		id := eventIDDoCorpo(t, aws.ToString(m.Body))
		vistos[id]++

		grupo := m.Attributes[string(types.MessageSystemAttributeNameMessageGroupId)]
		porCarteira[grupo] = append(porCarteira[grupo], eventTypeDoCorpo(t, aws.ToString(m.Body)))
	}
	for id, quantas := range vistos {
		if quantas > 1 {
			t.Errorf("o evento %s foi publicado %d vezes", id, quantas)
		}
	}

	// A ordem por carteira: `WagerTransactionProcessed` antes de
	// `WalletBalanceChanged`, como na `sequence` da outbox.
	for _, carteira := range carteiras {
		tipos := porCarteira[carteira]
		if len(tipos) != 2 {
			t.Fatalf("a carteira %s tem %d mensagens, esperado 2 (tipos: %v)", carteira, len(tipos), tipos)
		}
		if tipos[0] != wagerdomain.EventTypeProcessed || tipos[1] != wagerdomain.EventTypeWalletBalanceChanged {
			t.Errorf("ordem da carteira %s = %v, esperado [%s %s]",
				carteira, tipos, wagerdomain.EventTypeProcessed, wagerdomain.EventTypeWalletBalanceChanged)
		}
	}
}

// ---------------------------------------------------------------------------
// Recuperação 1: queda ENTRE o commit e a publicação
// ---------------------------------------------------------------------------

// O evento foi confirmado no banco (commit) e o processo morreu ANTES de
// publicar. Nada foi perdido: ele continua na outbox com `published_at` NULL, o
// lease vence e OUTRA instância o publica.
//
// É a garantia do README §3: "nenhuma dessas situações pode gerar (…) perda de um
// evento cujo registro foi confirmado no banco".
func TestQuedaEntreCommitEPublicacaoOutraInstanciaAssume(t *testing.T) {
	fila := abrirFilaDeEventos(t)
	limpar(t)
	fila.esvaziar(t)

	carteiraAberta(t, "1000.00")
	if pendentes := eventosPendentesNaOutbox(t); pendentes != 2 {
		t.Fatalf("pendentes = %d, esperado 2", pendentes)
	}

	// A instância "A" reivindica o lote e MORRE antes de publicar: o lease fica
	// gravado e `published_at` continua NULL. O claim é feito direto no Unit of
	// Work porque o publicador não oferece "reivindicar e morrer" — e é
	// exatamente esse estado que interessa observar.
	lease := 2 * time.Second
	if err := uow.Run(context.Background(), func(ctx context.Context, r ports.Repos) error {
		_, err := r.Outbox.ClaimBatch(ctx, 50, lease, time.Now().UTC())
		return err
	}); err != nil {
		t.Fatalf("claim da instancia A: %v", err)
	}
	if pendentes := eventosPendentesNaOutbox(t); pendentes != 2 {
		t.Fatalf("depois do claim, pendentes = %d: o claim NAO pode marcar como publicado", pendentes)
	}

	// A instância "B" tenta publicar enquanto o lease de A vale: não pega nada.
	cfg := configDaOutbox(lease, 50)
	pubB := publicadorDeTeste(t, fila, cfg)

	if publicados, err := pubB.PublicarPendentes(context.Background()); err != nil {
		t.Fatalf("PublicarPendentes (lease ativo): %v", err)
	} else if publicados != 0 {
		t.Fatalf("publicados = %d com o lease de A ativo, esperado 0", publicados)
	}
	// Se o lease não segurasse a linha, os eventos já estariam marcados como
	// publicados aqui — e a asserção acima teria falhado. A contagem final de
	// mensagens na fila (2, e não 4) fecha o mesmo fato pelo outro lado.

	// O lease vence: o evento VOLTA sozinho para o trabalho.
	time.Sleep(lease + 500*time.Millisecond)

	publicados, err := pubB.PublicarPendentes(context.Background())
	if err != nil {
		t.Fatalf("PublicarPendentes (lease vencido): %v", err)
	}
	if publicados != 2 {
		t.Fatalf("publicados = %d depois do lease vencer, esperado 2", publicados)
	}
	if pendentes := eventosPendentesNaOutbox(t); pendentes != 0 {
		t.Fatalf("pendentes depois da recuperacao = %d, esperado 0", pendentes)
	}

	if mensagens := fila.ler(t, 2); len(mensagens) != 2 {
		t.Fatalf("a fila recebeu %d mensagens depois da recuperacao, esperado 2", len(mensagens))
	}
}

// ---------------------------------------------------------------------------
// Recuperação 2: queda ENTRE a publicação e o `MarkPublished`
// ---------------------------------------------------------------------------

// A mensagem JÁ ESTÁ na fila e o evento continua pendente (o processo morreu
// antes do `MarkPublished`). O lease vence, outra instância republica, e a
// republicação preserva o `eventId` e o CORPO — requisito literal do README §11.
//
// O consumidor deduplica pelo `eventId`; é por isso que a republicação é segura,
// e é por isso que o `MessageDeduplicationId` NÃO é o `eventId`: o broker
// engoliria a segunda publicação dentro da janela de 5 minutos, e o sistema
// passaria a depender de uma garantia que EXPIRA.
func TestQuedaEntrePublicacaoEMarkPublishedRepublicaComOMesmoEvento(t *testing.T) {
	fila := abrirFilaDeEventos(t)
	limpar(t)
	fila.esvaziar(t)

	carteiraAberta(t, "500.00")

	lease := 2 * time.Second

	// A instância "A" reivindica...
	if err := uow.Run(context.Background(), func(ctx context.Context, r ports.Repos) error {
		_, err := r.Outbox.ClaimBatch(ctx, 50, lease, time.Now().UTC())
		return err
	}); err != nil {
		t.Fatalf("claim da instancia A: %v", err)
	}

	// ... PUBLICA o lote (as mensagens saem de verdade) e morre ANTES de marcar.
	for _, ev := range eventosPendentes(t) {
		if err := fila.pub.Publicar(context.Background(), ev); err != nil {
			t.Fatalf("publicacao manual da instancia A: %v", err)
		}
	}

	if pendentes := eventosPendentesNaOutbox(t); pendentes != 2 {
		t.Fatalf("pendentes = %d: o evento precisa continuar pendente apos a queda", pendentes)
	}

	primeiras := fila.ler(t, 2)
	if len(primeiras) != 2 {
		t.Fatalf("a instancia A deixou %d mensagens na fila, esperado 2", len(primeiras))
	}

	// O lease vence e a instância "B" assume.
	time.Sleep(lease + 500*time.Millisecond)

	pubB := publicadorDeTeste(t, fila, configDaOutbox(lease, 50))
	republicados, err := pubB.PublicarPendentes(context.Background())
	if err != nil {
		t.Fatalf("PublicarPendentes (instancia B): %v", err)
	}
	if republicados != 2 {
		t.Fatalf("republicados = %d, esperado 2", republicados)
	}

	segundas := fila.ler(t, 2)
	if len(segundas) != 2 {
		t.Fatalf("a republicacao deixou %d mensagens na fila, esperado 2 (o SQS nao pode deduplicar a republicacao)", len(segundas))
	}

	// O `eventId` do corpo é o mesmo nas duas publicações, e o corpo é idêntico.
	corposPorEvento := map[string][]string{}
	for _, m := range append(primeiras, segundas...) {
		corpo := aws.ToString(m.Body)
		id := eventIDDoCorpo(t, corpo)
		corposPorEvento[id] = append(corposPorEvento[id], corpo)
	}
	if len(corposPorEvento) != 2 {
		t.Fatalf("eventos distintos = %d, esperado 2 (4 mensagens, 2 eventos, cada um publicado 2 vezes)", len(corposPorEvento))
	}
	for id, corpos := range corposPorEvento {
		if len(corpos) != 2 {
			t.Fatalf("o evento %s foi publicado %d vezes, esperado 2", id, len(corpos))
		}
		if corpos[0] != corpos[1] {
			t.Errorf("o corpo do evento %s mudou na republicacao:\n%s\n%s", id, corpos[0], corpos[1])
		}
	}

	if pendentes := eventosPendentesNaOutbox(t); pendentes != 0 {
		t.Fatalf("pendentes depois da republicacao = %d, esperado 0", pendentes)
	}
}

// ---------------------------------------------------------------------------
// T7 (README §13.7): REFUND antes da referência
// ---------------------------------------------------------------------------

// Primeira metade do T7: o REFUND chega ANTES da BET, fica PENDING_REFERENCE
// (sem movimentar nada), a BET chega, o worker de referências resolve e a
// reversão acontece — com o ledger correto.
func TestREFUNDAntesDaReferenciaResolveDepois(t *testing.T) {
	limpar(t)

	ctx := context.Background()
	carteira := carteiraAberta(t, "100.00")

	// O REFUND (reversão) aponta para uma BET que ainda NÃO existe.
	reembolso := pedido(carteira, wagerdomain.KindRefund, "25.00",
		comChave("key-refund"), comExterna("ext-refund"), comReferencia("ext-bet"))
	res := processarPeloServico(t, reembolso)
	if res.Status != wagerdomain.StatusPendingReference {
		t.Fatalf("status do REFUND sem referencia = %s, esperado PENDING_REFERENCE", res.Status)
	}
	if saldo := saldoNoBanco(t, carteira.ID()); saldo != 10000 {
		t.Fatalf("o REFUND pendente NAO pode movimentar: saldo = %d", saldo)
	}

	// A BET chega e processa (debita 25.00).
	bet := pedido(carteira, wagerdomain.KindBet, "25.00", comChave("key-bet"), comExterna("ext-bet"))
	if r := processarPeloServico(t, bet); r.Status != wagerdomain.StatusProcessed {
		t.Fatalf("status da BET = %s, esperado PROCESSED", r.Status)
	}

	// O instante agendado no intake é "agora + 5s"; o teste não espera 5s de
	// verdade — antecipa o relógio da LINHA, que é o mesmo efeito de o tempo
	// passar. É fixture, não regra: a regra (resolver quando vence) é a mesma.
	if _, err := poolTeste.Exec(ctx,
		`UPDATE wager_transactions SET next_resolution_attempt_at = now() - interval '1 second'
		 WHERE idempotency_key = 'key-refund'`); err != nil {
		t.Fatalf("antecipando o instante da proxima tentativa: %v", err)
	}

	workerRef := workerDeReferenciasDeTeste(t, 20, 2*time.Minute)
	retomadas, err := workerRef.RetomarVencidas(ctx)
	if err != nil {
		t.Fatalf("RetomarVencidas: %v", err)
	}
	if retomadas != 1 {
		t.Fatalf("retomadas = %d, esperado 1", retomadas)
	}

	status, failureCode, referencia, resultado := transacaoNoBanco(t, "provider-a", "key-refund")
	if status != "PROCESSED" {
		t.Fatalf("status do REFUND depois da retomada = %s (%s), esperado PROCESSED", status, failureCode)
	}
	if referencia == "" {
		t.Error("a referencia interna resolvida nao foi gravada")
	}
	// 100.00 - 25.00 (BET) + 25.00 (REFUND) = 100.00
	if saldo := saldoNoBanco(t, carteira.ID()); saldo != 10000 {
		t.Errorf("saldo = %d, esperado 10000", saldo)
	}
	if resultado != 10000 {
		t.Errorf("saldo devolvido ao provedor = %d, esperado 10000", resultado)
	}

	// O ledger: abertura (CREDIT 100), BET (DEBIT 25) e REFUND (CREDIT 25).
	lancamentos := lancamentosNoBanco(t, carteira.ID())
	esperados := []string{"CREDIT 100.00", "DEBIT 25.00", "CREDIT 25.00"}
	if len(lancamentos) != len(esperados) {
		t.Fatalf("lancamentos = %v, esperado %v", lancamentos, esperados)
	}
	for i := range esperados {
		if lancamentos[i] != esperados[i] {
			t.Errorf("lancamento %d = %s, esperado %s", i, lancamentos[i], esperados[i])
		}
	}

	// A retomada gravou os eventos na MESMA transação do movimento: os dois
	// ÚLTIMOS da outbox são Processed e BalanceChanged.
	ultimos := ultimosEventos(t, 2)
	if len(ultimos) != 2 ||
		ultimos[1] != wagerdomain.EventTypeProcessed ||
		ultimos[0] != wagerdomain.EventTypeWalletBalanceChanged {
		t.Errorf("os dois ultimos eventos = %v, esperado [%s %s]",
			ultimos, wagerdomain.EventTypeProcessed, wagerdomain.EventTypeWalletBalanceChanged)
	}
}

// Segunda metade do T7: a referência NUNCA chega e o teto de tentativas estoura
// → REJECTED com REFERENCE_NOT_FOUND e evento de rejeição.
func TestReferenciaQueNuncaChegaExpiraEmREFERENCENOTFOUND(t *testing.T) {
	limpar(t)

	ctx := context.Background()
	carteira := carteiraAberta(t, "100.00")

	reembolso := pedido(carteira, wagerdomain.KindRefund, "25.00",
		comChave("key-refund"), comExterna("ext-refund"), comReferencia("ext-bet"))
	if res := processarPeloServico(t, reembolso); res.Status != wagerdomain.StatusPendingReference {
		t.Fatalf("status = %s, esperado PENDING_REFERENCE", res.Status)
	}

	if _, err := poolTeste.Exec(ctx,
		`UPDATE wager_transactions SET next_resolution_attempt_at = now() - interval '1 second'
		 WHERE idempotency_key = 'key-refund'`); err != nil {
		t.Fatalf("antecipando o instante da proxima tentativa: %v", err)
	}

	// MaxAttempts = 1: a tentativa do INTAKE já consome o teto.
	workerRef := workerDeReferenciasDeTeste(t, 1, 2*time.Minute)
	if _, err := workerRef.RetomarVencidas(ctx); err != nil {
		t.Fatalf("RetomarVencidas: %v", err)
	}

	status, failureCode, _, _ := transacaoNoBanco(t, "provider-a", "key-refund")
	if status != "REJECTED" {
		t.Fatalf("status = %s, esperado REJECTED", status)
	}
	if failureCode != appwagering.CodeReferenceNotFound {
		t.Errorf("failureCode = %q, esperado %q", failureCode, appwagering.CodeReferenceNotFound)
	}
	if saldo := saldoNoBanco(t, carteira.ID()); saldo != 10000 {
		t.Errorf("a expiracao NAO pode movimentar dinheiro: saldo = %d", saldo)
	}

	// O evento de rejeição existe e carrega o código estável no payload.
	var payload string
	if err := poolTeste.QueryRow(ctx,
		`SELECT payload::text FROM outbox_events
		 WHERE event_type = $1 ORDER BY sequence DESC LIMIT 1`,
		wagerdomain.EventTypeRejected).Scan(&payload); err != nil {
		t.Fatalf("lendo o evento de rejeicao: %v", err)
	}
	if !strings.Contains(payload, appwagering.CodeReferenceNotFound) {
		t.Errorf("o evento de rejeicao deveria carregar %s: %s", appwagering.CodeReferenceNotFound, payload)
	}
}

// ---------------------------------------------------------------------------
// Reaper: PENDING antigo
// ---------------------------------------------------------------------------

// Uma transação presa em PENDING (o caso do README §6.3) é retomada pelo reaper e
// chega a um desfecho — nunca fica presa para sempre.
//
// Hoje essa linha não existe por caminho normal: o caso de uso grava PENDING e o
// estado definitivo no MESMO commit, então nenhum PENDING é confirmado. A prova é
// feita com inserção DIRETA, que é o cenário que o reaper protege (aceite
// assíncrono futuro, ou linha presa por correção manual).
func TestReaperRetomaPendingAntigo(t *testing.T) {
	limpar(t)

	ctx := context.Background()
	carteira := carteiraAberta(t, "100.00")

	idPresa := uuid.NewString()
	if _, err := poolTeste.Exec(ctx,
		`INSERT INTO wager_transactions (
			id, wallet_id, player_id, kind, status, amount_minor, currency,
			game_id, round_id, idempotency_key, payload_hash,
			provider_id, external_transaction_id, reference_external_txn_id,
			reference_resolution_attempt, created_at, updated_at
		) VALUES ($1,$2,$3,'REFUND','PENDING',2500,'BRL','game-1','round-1',$4,'hash-fixo',
			'provider-a','ext-presa','ext-bet',0, now() - interval '10 minutes', now() - interval '10 minutes')`,
		idPresa, string(carteira.ID()), string(carteira.PlayerID()), "key-presa-"+uuid.NewString()); err != nil {
		t.Fatalf("inserindo a transacao presa em PENDING: %v", err)
	}

	// Com PENDING_MAX_AGE de 2 minutos e a linha com 10 minutos de idade, ela
	// está na janela do reaper.
	reaper := reaperDeTeste(t, 2*time.Minute)
	if quantas := varrerAgora(t, reaper); quantas != 1 {
		t.Fatalf("o reaper deveria ter retomado 1 linha, retomou %d", quantas)
	}

	var status string
	if err := poolTeste.QueryRow(ctx,
		`SELECT status FROM wager_transactions WHERE id = $1`, idPresa).Scan(&status); err != nil {
		t.Fatalf("lendo o estado da linha retomada: %v", err)
	}
	if status == "PENDING" {
		t.Fatal("a linha continuou em PENDING: o reaper nao deu desfecho")
	}
	// O desfecho esperado é PENDING_REFERENCE: a operação depende de uma
	// referência que não chegou, e agora é o worker de referências que cuida dela.
	if status != "PENDING_REFERENCE" {
		t.Errorf("status = %s, esperado PENDING_REFERENCE", status)
	}

	var proximo *time.Time
	if err := poolTeste.QueryRow(ctx,
		`SELECT next_resolution_attempt_at FROM wager_transactions WHERE id = $1`, idPresa).Scan(&proximo); err != nil {
		t.Fatalf("lendo o proximo instante: %v", err)
	}
	if proximo == nil {
		t.Error("a linha retomada precisa de next_resolution_attempt_at")
	}
}

// ---------------------------------------------------------------------------
// Auxiliares
// ---------------------------------------------------------------------------

// processarPeloServico aplica a operação pelo MESMO caso de uso do HTTP/SQS.
func processarPeloServico(t *testing.T, req appwagering.Request) appwagering.Result {
	t.Helper()

	res, err := servico.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process(%s): %v", req.Kind, err)
	}
	return res
}

// eventTypeDoCorpo lê o `eventType` do envelope publicado.
func eventTypeDoCorpo(t *testing.T, corpo string) string {
	t.Helper()

	var envelope struct {
		EventType string `json:"eventType"`
	}
	if err := json.Unmarshal([]byte(corpo), &envelope); err != nil {
		t.Fatalf("corpo publicado nao e o envelope: %v", err)
	}
	return envelope.EventType
}

// varrerAgora força o reaper a rodar uma vez, sem esperar o ticker.
func varrerAgora(t *testing.T, reaper *worker.PendingReaper) int {
	t.Helper()

	quantas, err := reaper.Varrer(context.Background())
	if err != nil {
		t.Fatalf("reaper: %v", err)
	}
	return quantas
}
