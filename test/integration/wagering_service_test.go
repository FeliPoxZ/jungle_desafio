//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
	"github.com/FeliPoxZ/jungle_desafio/internal/platform/clock"
	"github.com/FeliPoxZ/jungle_desafio/internal/platform/idgen"
)

// processar roda a operação e exige sucesso do ponto de vista da infraestrutura:
// rejeição de negócio é desfecho, e vem em Result.
func processar(t *testing.T, req appwagering.Request) appwagering.Result {
	t.Helper()

	res, err := servico.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process devolveu erro: %v", err)
	}
	return res
}

func exigirLancamentosNaCarteira(t *testing.T, w *wallet.Wallet, esperados ...string) {
	t.Helper()

	obtidos := lancamentosNoBanco(t, w.ID())
	if strings.Join(obtidos, ",") != strings.Join(esperados, ",") {
		t.Fatalf("ledger = %v, esperado %v", obtidos, esperados)
	}
}

func exigirSaldoNoBanco(t *testing.T, w *wallet.Wallet, esperado int64) {
	t.Helper()

	if atual := saldoNoBanco(t, w.ID()); atual != esperado {
		t.Fatalf("saldo gravado = %d, esperado %d", atual, esperado)
	}
}

// ---------------------------------------------------------------------------
// Caminho felizes contra o banco real
// ---------------------------------------------------------------------------

func TestBETGravaSaldoLedgerEEventos(t *testing.T) {
	limpar(t)
	w := carteiraNova(t, "1000.00")

	res := processar(t, pedido(w, wagerdomain.KindBet, "25.00"))

	if res.Status != wagerdomain.StatusProcessed {
		t.Fatalf("status = %s (%s)", res.Status, res.FailureCode)
	}
	if res.Balance == nil || res.Balance.Minor() != 97500 {
		t.Fatalf("saldo devolvido = %v", res.Balance)
	}
	if res.IdempotentReplay {
		t.Fatal("primeira execução não é replay")
	}

	exigirSaldoNoBanco(t, w, 97500)
	if versao := versaoNoBanco(t, w.ID()); versao != 2 {
		t.Fatalf("versão gravada = %d, esperado 2", versao)
	}
	exigirLancamentosNaCarteira(t, w, "DEBIT 25.00")

	// Dois eventos, e o payload é o snapshot do envelope — com valor em string
	// decimal, como o contrato pede.
	eventos := eventosNaOutbox(t)
	if len(eventos) != 2 {
		t.Fatalf("eventos = %v, esperado 2", tiposDeEvento(t))
	}
	if eventos[0].Tipo != wagerdomain.EventTypeProcessed || eventos[1].Tipo != wagerdomain.EventTypeWalletBalanceChanged {
		t.Fatalf("tipos de evento = %v", tiposDeEvento(t))
	}
	for _, e := range eventos {
		if e.Agregado != string(w.ID()) {
			t.Fatalf("aggregateId = %s, esperado a carteira %s", e.Agregado, w.ID())
		}
		if e.Publicado {
			t.Fatal("nada pode estar publicado antes do publisher (Fase 9)")
		}
	}

	var envelope struct {
		EventType string `json:"eventType"`
		Version   int    `json:"version"`
		Data      struct {
			Money struct {
				Amount   string `json:"amount"`
				Currency string `json:"currency"`
			} `json:"money"`
			WalletVersion int64 `json:"walletVersion"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(eventos[1].Payload), &envelope); err != nil {
		t.Fatalf("payload do evento não é JSON: %v", err)
	}
	if envelope.EventType != wagerdomain.EventTypeWalletBalanceChanged || envelope.Version != wagerdomain.EventVersion {
		t.Fatalf("envelope = %+v", envelope)
	}
	if envelope.Data.Money.Amount != "25.00" || envelope.Data.Money.Currency != "BRL" {
		t.Fatalf("dinheiro no evento = %+v", envelope.Data.Money)
	}
	if envelope.Data.WalletVersion != 2 {
		t.Fatalf("walletVersion = %d, esperado 2", envelope.Data.WalletVersion)
	}
}

func TestLOSSNaoCriaLedgerNemMudaVersao(t *testing.T) {
	limpar(t)
	w := carteiraNova(t, "100.00")

	res := processar(t, pedido(w, wagerdomain.KindLoss, "0.00"))

	if res.Status != wagerdomain.StatusProcessed {
		t.Fatalf("status = %s (%s)", res.Status, res.FailureCode)
	}
	exigirSaldoNoBanco(t, w, 10000)
	if versao := versaoNoBanco(t, w.ID()); versao != 1 {
		t.Fatalf("versão = %d, LOSS não incrementa versão", versao)
	}
	exigirLancamentosNaCarteira(t, w)

	// Produz o evento de conclusão e NENHUM de mudança de saldo.
	if tipos := tiposDeEvento(t); len(tipos) != 1 || tipos[0] != wagerdomain.EventTypeProcessed {
		t.Fatalf("eventos = %v, esperado só WagerTransactionProcessed", tipos)
	}
}

func TestBETSemSaldoRejeitaSemTocarNoSaldo(t *testing.T) {
	limpar(t)
	w := carteiraNova(t, "100.00")

	res := processar(t, pedido(w, wagerdomain.KindBet, "180.00"))

	if res.Status != wagerdomain.StatusRejected || res.FailureCode != wallet.CodeInsufficientFunds {
		t.Fatalf("resultado = %+v", res)
	}
	exigirSaldoNoBanco(t, w, 10000)
	if versao := versaoNoBanco(t, w.ID()); versao != 1 {
		t.Fatalf("versão = %d, esperado 1", versao)
	}
	exigirLancamentosNaCarteira(t, w)

	status, codigo, _, resultado := transacaoNoBanco(t, "provider-a", "key-BET")
	if status != "REJECTED" || codigo != wallet.CodeInsufficientFunds || resultado != 0 {
		t.Fatalf("transação gravada: status=%s código=%s resultado=%d", status, codigo, resultado)
	}
	if tipos := tiposDeEvento(t); len(tipos) != 1 || tipos[0] != wagerdomain.EventTypeRejected {
		t.Fatalf("eventos = %v", tipos)
	}
}

// ---------------------------------------------------------------------------
// Idempotência
// ---------------------------------------------------------------------------

func TestReplayDevolveOSaldoDoProcessamentoOriginal(t *testing.T) {
	limpar(t)
	w := carteiraNova(t, "1000.00")

	primeiro := processar(t, pedido(w, wagerdomain.KindBet, "25.00"))

	// A carteira muda depois, por outra operação.
	processar(t, pedido(w, wagerdomain.KindBet, "100.00", comChave("key-outra"), comExterna("ext-outra")))
	exigirSaldoNoBanco(t, w, 87500)

	replay := processar(t, pedido(w, wagerdomain.KindBet, "25.00"))

	if !replay.IdempotentReplay {
		t.Fatal("mesma chave e mesmo conteúdo tem que ser replay")
	}
	if replay.TransactionID != primeiro.TransactionID {
		t.Fatalf("replay devolveu outra transação: %s e %s", replay.TransactionID, primeiro.TransactionID)
	}
	if replay.Balance.Minor() != 97500 {
		t.Fatalf("replay devolveu %v; o contrato pede o saldo do processamento ORIGINAL", replay.Balance)
	}
	exigirLancamentosNaCarteira(t, w, "DEBIT 25.00", "DEBIT 100.00")
	if total := contarTransacoes(t); total != 2 {
		t.Fatalf("transações = %d, esperado 2 (o replay não cria linha)", total)
	}
}

func TestIdempotenciaSobreviveAProcessoNovo(t *testing.T) {
	limpar(t)
	w := carteiraNova(t, "1000.00")

	primeiro := processar(t, pedido(w, wagerdomain.KindBet, "25.00"))

	// Serviço novo, Unit of Work novo: em memória não há nada. Se a idempotência
	// dependesse de estado do processo, aqui apareceria.
	outroServico := appwagering.New(
		postgresUOW(t), clock.System{}, idgen.New(), nil,
	)
	res, err := outroServico.Process(context.Background(), pedido(w, wagerdomain.KindBet, "25.00"))
	if err != nil {
		t.Fatalf("Process no serviço novo: %v", err)
	}

	if !res.IdempotentReplay || res.TransactionID != primeiro.TransactionID {
		t.Fatalf("resultado = %+v", res)
	}
	if res.Balance.Minor() != 97500 {
		t.Fatalf("saldo = %v", res.Balance)
	}
	exigirLancamentosNaCarteira(t, w, "DEBIT 25.00")
}

func TestMesmaChaveComConteudoDiferenteEConflito(t *testing.T) {
	limpar(t)
	w := carteiraNova(t, "1000.00")

	processar(t, pedido(w, wagerdomain.KindBet, "25.00"))

	_, err := servico.Process(context.Background(), pedido(w, wagerdomain.KindBet, "30.00"))
	if err == nil {
		t.Fatal("esperava conflito")
	}
	if !errors.Is(err, appwagering.ErrIdempotencyConflict) {
		t.Fatalf("erro = %v, esperado conflito de idempotência", err)
	}
	exigirSaldoNoBanco(t, w, 97500)
	exigirLancamentosNaCarteira(t, w, "DEBIT 25.00")
}

func TestIdentidadeExternaComOutraChaveEConflito(t *testing.T) {
	limpar(t)
	w := carteiraNova(t, "1000.00")

	processar(t, pedido(w, wagerdomain.KindBet, "25.00"))

	_, err := servico.Process(context.Background(), pedido(w, wagerdomain.KindBet, "25.00", comChave("key-nova")))
	if !errors.Is(err, appwagering.ErrIdempotencyConflict) {
		t.Fatalf("erro = %v, esperado conflito de identidade externa", err)
	}
	exigirLancamentosNaCarteira(t, w, "DEBIT 25.00")
}

// ---------------------------------------------------------------------------
// O cenário do README §8: duas apostas de 80 sobre saldo de 100
// ---------------------------------------------------------------------------

func TestCenarioDoREADMEDuasApostasDe80Sobre100(t *testing.T) {
	limpar(t)
	w := carteiraNova(t, "100.00")

	var (
		espera     sync.WaitGroup
		resultados [2]appwagering.Result
		erros      [2]error
		chaves     = [2]string{"a", "b"}
	)

	// Duas apostas DIFERENTES, ao mesmo tempo, na mesma carteira.
	espera.Add(2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer espera.Done()
			req := pedido(w, wagerdomain.KindBet, "80.00",
				comChave("key-80-"+chaves[i]),
				comExterna("ext-80-"+chaves[i]))
			resultados[i], erros[i] = servico.Process(context.Background(), req)
		}(i)
	}
	espera.Wait()

	for i := 0; i < 2; i++ {
		if erros[i] != nil {
			t.Fatalf("aposta %d: %v", i, erros[i])
		}
	}

	var processadas, rejeitadas int
	for i := 0; i < 2; i++ {
		switch resultados[i].Status {
		case wagerdomain.StatusProcessed:
			processadas++
		case wagerdomain.StatusRejected:
			rejeitadas++
			if resultados[i].FailureCode != wallet.CodeInsufficientFunds {
				t.Fatalf("rejeição com código %q, esperado %q", resultados[i].FailureCode, wallet.CodeInsufficientFunds)
			}
		default:
			t.Fatalf("status inesperado: %s", resultados[i].Status)
		}
	}
	if processadas != 1 || rejeitadas != 1 {
		t.Fatalf("processadas=%d rejeitadas=%d, esperado 1 e 1", processadas, rejeitadas)
	}

	// O resultado financeiro do README: saldo 20.00 e UM débito no ledger.
	exigirSaldoNoBanco(t, w, 2000)
	if versao := versaoNoBanco(t, w.ID()); versao != 2 {
		t.Fatalf("versão = %d, esperado 2 (uma movimentação)", versao)
	}
	exigirLancamentosNaCarteira(t, w, "DEBIT 80.00")

	// Reenvios não podem alterar nada.
	processar(t, pedido(w, wagerdomain.KindBet, "80.00", comChave("key-80-a"), comExterna("ext-80-a")))
	processar(t, pedido(w, wagerdomain.KindBet, "80.00", comChave("key-80-b"), comExterna("ext-80-b")))
	exigirSaldoNoBanco(t, w, 2000)
	exigirLancamentosNaCarteira(t, w, "DEBIT 80.00")

	// Eventos: conclusão e mudança de saldo da aposta que passou, e a rejeição da
	// que não passou. A ORDEM entre eles não é afirmada, porque depende do
	// instante de cada transação.
	contagem := map[string]int{}
	for _, tipo := range tiposDeEvento(t) {
		contagem[tipo]++
	}
	if contagem[wagerdomain.EventTypeProcessed] != 1 ||
		contagem[wagerdomain.EventTypeWalletBalanceChanged] != 1 ||
		contagem[wagerdomain.EventTypeRejected] != 1 {
		t.Fatalf("eventos = %v, esperado 1 Processed, 1 BalanceChanged e 1 Rejected", contagem)
	}
}

func TestMesmaApostaEmParaleloProcessaUmaVez(t *testing.T) {
	limpar(t)
	w := carteiraNova(t, "1000.00")

	const disparos = 12

	var (
		espera sync.WaitGroup
		mu     sync.Mutex
		vistos = map[string]int{}
	)

	espera.Add(disparos)
	for i := 0; i < disparos; i++ {
		go func() {
			defer espera.Done()
			res, err := servico.Process(context.Background(), pedido(w, wagerdomain.KindBet, "25.00"))
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				vistos["erro: "+err.Error()]++
				return
			}
			vistos[string(res.TransactionID)]++
		}()
	}
	espera.Wait()

	for chave, quantos := range vistos {
		if strings.HasPrefix(chave, "erro: ") {
			t.Fatalf("%s (apareceu %d vez(es)); mapa completo: %v", chave, quantos, vistos)
		}
		if quantos != disparos {
			t.Fatalf("transação %s apareceu %d vezes, esperado %d; mapa completo: %v", chave, quantos, disparos, vistos)
		}
	}

	// Um débito, uma linha de transação, uma carteira movimentada uma vez.
	exigirSaldoNoBanco(t, w, 97500)
	if versao := versaoNoBanco(t, w.ID()); versao != 2 {
		t.Fatalf("versão = %d, esperado 2", versao)
	}
	exigirLancamentosNaCarteira(t, w, "DEBIT 25.00")
	if total := contarTransacoes(t); total != 1 {
		t.Fatalf("transações = %d, esperado 1", total)
	}
}

// ---------------------------------------------------------------------------
// Referências e reversões
// ---------------------------------------------------------------------------

func TestREFUNDResolveReferenciaNaMesmaTransacao(t *testing.T) {
	limpar(t)
	w := carteiraNova(t, "100.00")

	processar(t, pedido(w, wagerdomain.KindBet, "40.00"))
	res := processar(t, pedido(w, wagerdomain.KindRefund, "40.00", comReferencia("ext-BET")))

	if res.Status != wagerdomain.StatusProcessed {
		t.Fatalf("status = %s (%s)", res.Status, res.FailureCode)
	}
	exigirSaldoNoBanco(t, w, 10000)
	exigirLancamentosNaCarteira(t, w, "DEBIT 40.00", "CREDIT 40.00")

	status, _, referencia, _ := transacaoNoBanco(t, "provider-a", "key-REFUND")
	if status != "PROCESSED" || referencia == "" {
		t.Fatalf("estorno gravado: status=%s referência=%q", status, referencia)
	}

	// A referência interna tem que apontar para a BET, e a BET é a única com o
	// external_transaction_id informado.
	var idDaBet string
	if err := poolTeste.QueryRow(context.Background(),
		`SELECT id FROM wager_transactions WHERE external_transaction_id = 'ext-BET'`).Scan(&idDaBet); err != nil {
		t.Fatalf("lendo a BET: %v", err)
	}
	if referencia != idDaBet {
		t.Fatalf("referência = %s, esperado %s", referencia, idDaBet)
	}
}

func TestREFUNDAntesDaReferenciaFicaPendenteEDuravel(t *testing.T) {
	limpar(t)
	w := carteiraNova(t, "100.00")

	res := processar(t, pedido(w, wagerdomain.KindRefund, "40.00", comReferencia("ext-que-nao-chegou")))

	if res.Status != wagerdomain.StatusPendingReference {
		t.Fatalf("status = %s", res.Status)
	}
	if res.Balance != nil || res.FailureCode != "" {
		t.Fatalf("pendência não devolve saldo nem código: %+v", res)
	}
	exigirSaldoNoBanco(t, w, 10000)
	exigirLancamentosNaCarteira(t, w)

	status, _, _, _ := transacaoNoBanco(t, "provider-a", "key-REFUND")
	if status != "PENDING_REFERENCE" {
		t.Fatalf("status gravado = %s, esperado PENDING_REFERENCE", status)
	}
	if tipos := tiposDeEvento(t); len(tipos) != 1 || tipos[0] != wagerdomain.EventTypePendingReference {
		t.Fatalf("eventos = %v", tipos)
	}

	// A pendência é DURÁVEL e tem prazo: o worker de referências (Fase 9) a
	// encontra pela consulta que já existe, depois do backoff.
	var (
		pendentes int
		agendadas int
	)
	if err := poolTeste.QueryRow(context.Background(),
		`SELECT count(*) FROM wager_transactions WHERE status = 'PENDING_REFERENCE'`).Scan(&pendentes); err != nil {
		t.Fatalf("contando pendências: %v", err)
	}
	if err := poolTeste.QueryRow(context.Background(),
		`SELECT count(*) FROM wager_transactions
		 WHERE status = 'PENDING_REFERENCE' AND next_resolution_attempt_at > now()`).Scan(&agendadas); err != nil {
		t.Fatalf("lendo o agendamento: %v", err)
	}
	if pendentes != 1 || agendadas != 1 {
		t.Fatalf("pendentes=%d agendadas=%d, esperado 1 e 1", pendentes, agendadas)
	}

	// Reenvio da MESMA operação devolve a pendência persistida, sem criar linha.
	replay := processar(t, pedido(w, wagerdomain.KindRefund, "40.00", comReferencia("ext-que-nao-chegou")))
	if !replay.IdempotentReplay || replay.Status != wagerdomain.StatusPendingReference {
		t.Fatalf("replay da pendência = %+v", replay)
	}
	if total := contarTransacoes(t); total != 1 {
		t.Fatalf("transações = %d, esperado 1", total)
	}
}

func TestROLLBACKDesfazEstornoSemCriarDinheiro(t *testing.T) {
	limpar(t)
	w := carteiraNova(t, "100.00")

	processar(t, pedido(w, wagerdomain.KindBet, "40.00"))
	processar(t, pedido(w, wagerdomain.KindRefund, "40.00", comReferencia("ext-BET")))

	// ROLLBACK da BET já estornada: recusa, porque creditaria o mesmo débito de
	// novo — dinheiro criado do nada.
	res := processar(t, pedido(w, wagerdomain.KindRollback, "40.00",
		comChave("key-RB-BET"), comExterna("ext-RB-BET"), comReferencia("ext-BET")))
	if res.Status != wagerdomain.StatusRejected || res.FailureCode != wagerdomain.CodeReversed {
		t.Fatalf("resultado = %+v", res)
	}

	// ROLLBACK do ESTORNO: debita e é aceito. O saldo volta a 60.00.
	res = processar(t, pedido(w, wagerdomain.KindRollback, "40.00", comReferencia("ext-REFUND")))
	if res.Status != wagerdomain.StatusProcessed {
		t.Fatalf("status = %s (%s)", res.Status, res.FailureCode)
	}
	exigirSaldoNoBanco(t, w, 6000)
	exigirLancamentosNaCarteira(t, w, "DEBIT 40.00", "CREDIT 40.00", "DEBIT 40.00")
}

func TestReversaoDuplicadaBarradaPeloIndiceDoBanco(t *testing.T) {
	limpar(t)
	w := carteiraNova(t, "100.00")

	processar(t, pedido(w, wagerdomain.KindBet, "40.00"))
	processar(t, pedido(w, wagerdomain.KindRefund, "40.00", comReferencia("ext-BET")))

	res := processar(t, pedido(w, wagerdomain.KindRefund, "40.00",
		comChave("key-REFUND-2"), comExterna("ext-REFUND-2"), comReferencia("ext-BET")))

	if res.Status != wagerdomain.StatusRejected || res.FailureCode != wagerdomain.CodeReversed {
		t.Fatalf("resultado = %+v", res)
	}
	exigirSaldoNoBanco(t, w, 10000)
	exigirLancamentosNaCarteira(t, w, "DEBIT 40.00", "CREDIT 40.00")
}

func TestROLLBACKSemSaldoTemCodigoProprio(t *testing.T) {
	limpar(t)
	w := carteiraNova(t, "100.00")

	processar(t, pedido(w, wagerdomain.KindWin, "30.00"))
	processar(t, pedido(w, wagerdomain.KindBet, "130.00", comChave("key-BET2"), comExterna("ext-BET2")))

	res := processar(t, pedido(w, wagerdomain.KindRollback, "30.00", comReferencia("ext-WIN")))

	if res.FailureCode != appwagering.CodeRollbackInsufficientFunds {
		t.Fatalf("failureCode = %q, esperado %q", res.FailureCode, appwagering.CodeRollbackInsufficientFunds)
	}
	exigirSaldoNoBanco(t, w, 0)
}

// ---------------------------------------------------------------------------
// Atomicidade
// ---------------------------------------------------------------------------

func TestFalhaNaOutboxDesfazTudoNoBancoReal(t *testing.T) {
	limpar(t)
	w := carteiraNova(t, "1000.00")

	quebrado := appwagering.New(uowComOutboxQuebrada{interno: postgresUOW(t)}, clock.System{}, idgen.New(), nil)
	_, err := quebrado.Process(context.Background(), pedido(w, wagerdomain.KindBet, "25.00"))
	if err == nil {
		t.Fatal("esperava erro da outbox")
	}

	// O ROLLBACK do PostgreSQL é quem desfaz: saldo, versão, ledger e a própria
	// transação voltam ao estado anterior.
	exigirSaldoNoBanco(t, w, 100000)
	if versao := versaoNoBanco(t, w.ID()); versao != 1 {
		t.Fatalf("versão = %d, esperado 1", versao)
	}
	exigirLancamentosNaCarteira(t, w)
	if total := contarTransacoes(t); total != 0 {
		t.Fatalf("transações = %d, esperado 0", total)
	}
	if eventos := eventosNaOutbox(t); len(eventos) != 0 {
		t.Fatalf("eventos = %v, esperado nenhum", tiposDeEvento(t))
	}
}
