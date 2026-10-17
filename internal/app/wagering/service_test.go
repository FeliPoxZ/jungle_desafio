package wagering_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	domainerrors "github.com/FeliPoxZ/jungle_desafio/internal/domain/errors"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
)

// brl é o dinheiro dos testes. Faz pânico no literal inválido de propósito: o
// valor vem do próprio teste, então erro aqui é teste quebrado, não caso a
// cobrir.
func brl(valor string) money.Money {
	m, err := money.Parse(valor, "BRL")
	if err != nil {
		panic("valor de teste inválido: " + valor + ": " + err.Error())
	}
	return m
}

type ambiente struct {
	servico   *appwagering.Service
	estado    *estadoMemoria
	uow       *uowMemoria
	relogio   *relogioFixo
	carteiras int

	// conflitos é o contador de teste ligado ao serviço: ele prova a EMISSÃO da
	// métrica `transaction_conflicts_total`, que até a Fase 11 existia e nunca
	// era incrementada (defeito 42).
	conflitos *contadorConflitos
}

// contadorConflitos é o dublê do contador: em produção quem conta é
// `observability.Metrics`, atrás da interface `ports.ConflictCounter`.
type contadorConflitos struct{ total int }

func (c *contadorConflitos) IncConflict() { c.total++ }

func novoAmbiente() *ambiente {
	estado := novoEstado()
	uow := &uowMemoria{estado: estado}
	relogio := novoRelogio()
	conflitos := &contadorConflitos{}
	return &ambiente{
		servico:   appwagering.New(uow, relogio, &idsSequenciais{}, conflitos),
		estado:    estado,
		uow:       uow,
		relogio:   relogio,
		conflitos: conflitos,
	}
}

// abrirCarteira grava a carteira direto no estado: aqui ela é só o ponto de
// partida dos testes de operação (a abertura pela regra é o `OpenWallet`).
func (a *ambiente) abrirCarteira(saldo string) *wallet.Wallet {
	a.carteiras++
	w, err := wallet.NewWallet(
		ids.WalletID(uuidDeTeste(1, a.carteiras)),
		ids.PlayerID("jogador-1"),
		brl(saldo),
		a.relogio.Now(),
	)
	if err != nil {
		panic(err)
	}
	a.estado.wallets[w.ID()] = w
	return w
}

func (a *ambiente) saldo(id ids.WalletID) int64 {
	w, ok := a.estado.wallets[id]
	if !ok {
		return -1
	}
	return w.Balance().Minor()
}

func (a *ambiente) versao(id ids.WalletID) int64 { return a.estado.wallets[id].Version() }

func (a *ambiente) lancamentos() []string {
	var saida []string
	for _, e := range a.estado.ledger {
		saida = append(saida, e.Direction().String()+" "+e.Amount().String())
	}
	return saida
}

func (a *ambiente) tiposDeEvento() []string {
	var saida []string
	for _, e := range a.estado.outbox {
		saida = append(saida, e.EventType)
	}
	return saida
}

func (a *ambiente) transacao(provider, chave string) *wagerdomain.WagerTransaction {
	for _, t := range a.estado.txns {
		if t.ProviderID() == provider && t.IdempotencyKey() == chave {
			return t
		}
	}
	return nil
}

// pedido monta uma operação como a borda HTTP/SQS montaria.
type pedidoOpt func(*appwagering.Request)

func comChave(chave string) pedidoOpt {
	return func(r *appwagering.Request) { r.IdempotencyKey = chave }
}

func comExterna(externa string) pedidoOpt {
	return func(r *appwagering.Request) { r.ExternalTransactionID = externa }
}

func comValor(valor string) pedidoOpt {
	return func(r *appwagering.Request) { r.Amount = brl(valor) }
}

func comRodada(rodada string) pedidoOpt {
	return func(r *appwagering.Request) { r.RoundID = rodada }
}

func comReferencia(externa string) pedidoOpt {
	return func(r *appwagering.Request) { r.ReferenceExternalTxnID = externa }
}

func comCarteira(id ids.WalletID) pedidoOpt {
	return func(r *appwagering.Request) { r.WalletID = id }
}

func comJogador(id ids.PlayerID) pedidoOpt {
	return func(r *appwagering.Request) { r.PlayerID = id }
}

func pedido(w *wallet.Wallet, kind wagerdomain.Kind, valor string, opts ...pedidoOpt) appwagering.Request {
	req := appwagering.Request{
		ProviderID:            "provider-a",
		ExternalTransactionID: "ext-" + string(kind),
		IdempotencyKey:        "key-" + string(kind),
		PlayerID:              w.PlayerID(),
		WalletID:              w.ID(),
		RoundID:               "round-1",
		GameID:                "fortune-chimp",
		Kind:                  kind,
		Amount:                brl(valor),
	}
	for _, opt := range opts {
		opt(&req)
	}
	return req
}

func processar(t *testing.T, a *ambiente, req appwagering.Request) appwagering.Result {
	t.Helper()
	res, err := a.servico.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process devolveu erro: %v", err)
	}
	return res
}

func falhar(t *testing.T, a *ambiente, req appwagering.Request) error {
	t.Helper()
	res, err := a.servico.Process(context.Background(), req)
	if err == nil {
		t.Fatalf("esperava erro, veio resultado %+v", res)
	}
	return err
}

func exigirSaldo(t *testing.T, a *ambiente, w *wallet.Wallet, esperado int64) {
	t.Helper()
	if atual := a.saldo(w.ID()); atual != esperado {
		t.Fatalf("saldo = %s, esperado %s", money.FromMinor(atual, money.BRL), money.FromMinor(esperado, money.BRL))
	}
}

func exigirLancamentos(t *testing.T, a *ambiente, esperados ...string) {
	t.Helper()
	obtidos := a.lancamentos()
	if strings.Join(obtidos, ",") != strings.Join(esperados, ",") {
		t.Fatalf("lançamentos = %v, esperado %v", obtidos, esperados)
	}
}

func exigirEventos(t *testing.T, a *ambiente, esperados ...string) {
	t.Helper()
	obtidos := a.tiposDeEvento()
	if strings.Join(obtidos, ",") != strings.Join(esperados, ",") {
		t.Fatalf("eventos = %v, esperado %v", obtidos, esperados)
	}
}

func exigirCodigo(t *testing.T, err error, esperado string) {
	t.Helper()
	if code := domainerrors.CodeOf(err); code != esperado {
		t.Fatalf("failureCode = %q, esperado %q (erro: %v)", code, esperado, err)
	}
}

func exigirRejeicao(t *testing.T, res appwagering.Result, esperado string) {
	t.Helper()
	if res.Status != wagerdomain.StatusRejected {
		t.Fatalf("status = %s, esperado REJECTED", res.Status)
	}
	if res.FailureCode != esperado {
		t.Fatalf("failureCode = %q, esperado %q", res.FailureCode, esperado)
	}
	if res.Balance != nil {
		t.Fatalf("rejeição não devolve saldo, veio %s", res.Balance)
	}
	if res.IdempotentReplay {
		t.Fatal("rejeição nova não é replay")
	}
}

// ---------------------------------------------------------------------------
// Os cinco tipos externos
// ---------------------------------------------------------------------------

func TestBETComSaldoDebitaEGeraDoisEventos(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("1000.00")

	res := processar(t, a, pedido(w, wagerdomain.KindBet, "25.00"))

	if res.Status != wagerdomain.StatusProcessed {
		t.Fatalf("status = %s, esperado PROCESSED", res.Status)
	}
	if res.Balance == nil || res.Balance.Minor() != 97500 {
		t.Fatalf("saldo devolvido = %v, esperado 975.00", res.Balance)
	}
	if res.IdempotentReplay {
		t.Fatal("primeira execução não é replay")
	}
	exigirSaldo(t, a, w, 97500)
	if v := a.versao(w.ID()); v != 2 {
		t.Fatalf("versão = %d, esperado 2", v)
	}
	exigirLancamentos(t, a, "DEBIT 25.00")
	exigirEventos(t, a, wagerdomain.EventTypeProcessed, wagerdomain.EventTypeWalletBalanceChanged)

	tx := a.transacao("provider-a", "key-BET")
	if tx == nil || tx.Status() != wagerdomain.StatusProcessed {
		t.Fatalf("transação persistida = %+v", tx)
	}
	if !tx.HasResultBalance() || tx.ResultBalance().Minor() != 97500 {
		t.Fatal("saldo resultante precisa ficar gravado para o replay")
	}
}

func TestBETSemSaldoRejeitaComINSUFFICIENTFUNDS(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("100.00")

	res := processar(t, a, pedido(w, wagerdomain.KindBet, "180.00"))

	exigirRejeicao(t, res, wallet.CodeInsufficientFunds)
	exigirSaldo(t, a, w, 10000)
	if v := a.versao(w.ID()); v != 1 {
		t.Fatalf("rejeição não pode mexer na versão, versão = %d", v)
	}
	exigirLancamentos(t, a)
	exigirEventos(t, a, wagerdomain.EventTypeRejected)
}

func TestWINCredita(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("100.00")

	res := processar(t, a, pedido(w, wagerdomain.KindWin, "30.00"))

	if res.Status != wagerdomain.StatusProcessed {
		t.Fatalf("status = %s", res.Status)
	}
	exigirSaldo(t, a, w, 13000)
	exigirLancamentos(t, a, "CREDIT 30.00")
}

func TestLOSSNaoMovimentaMasProduzEvento(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("100.00")

	res := processar(t, a, pedido(w, wagerdomain.KindLoss, "0.00"))

	if res.Status != wagerdomain.StatusProcessed {
		t.Fatalf("status = %s, esperado PROCESSED", res.Status)
	}
	exigirSaldo(t, a, w, 10000)
	if v := a.versao(w.ID()); v != 1 {
		t.Fatalf("LOSS não incrementa versão, versão = %d", v)
	}
	exigirLancamentos(t, a)
	// Produz o evento de conclusão e NADA de mudança de saldo: o provedor
	// precisa saber que a rodada acabou, mas o saldo não mudou.
	exigirEventos(t, a, wagerdomain.EventTypeProcessed)
	if res.Balance == nil || res.Balance.Minor() != 10000 {
		t.Fatalf("LOSS devolve o saldo observado, veio %v", res.Balance)
	}
}

func TestREFUNDDevolveIntegralmenteABET(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("100.00")

	processar(t, a, pedido(w, wagerdomain.KindBet, "40.00"))
	res := processar(t, a, pedido(w, wagerdomain.KindRefund, "40.00", comReferencia("ext-BET")))

	if res.Status != wagerdomain.StatusProcessed {
		t.Fatalf("status = %s (%s)", res.Status, res.FailureCode)
	}
	exigirSaldo(t, a, w, 10000)
	exigirLancamentos(t, a, "DEBIT 40.00", "CREDIT 40.00")

	refund := a.transacao("provider-a", "key-REFUND")
	if refund == nil || refund.ReferenceTransactionID() == "" {
		t.Fatal("o estorno precisa gravar a referência interna resolvida")
	}
	if refund.ReferenceTransactionID() != a.transacao("provider-a", "key-BET").ID() {
		t.Fatal("referência gravada aponta para outra transação")
	}
}

func TestROLLBACKDesfazBETeDesfazEstorno(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("100.00")

	processar(t, a, pedido(w, wagerdomain.KindBet, "40.00"))
	processar(t, a, pedido(w, wagerdomain.KindRefund, "40.00", comReferencia("ext-BET")))

	// Desfazer a BET já estornada creditaria de novo o mesmo débito: recusa.
	res := processar(t, a, pedido(w, wagerdomain.KindRollback, "40.00",
		comReferencia("ext-BET"), comChave("key-RB-BET"), comExterna("ext-RB-BET")))
	exigirRejeicao(t, res, wagerdomain.CodeReversed)

	// Desfazer o ESTORNO debita e é aceito: o saldo volta ao que era depois da
	// aposta, sem dinheiro criado do nada.
	res = processar(t, a, pedido(w, wagerdomain.KindRollback, "40.00", comReferencia("ext-REFUND")))
	if res.Status != wagerdomain.StatusProcessed {
		t.Fatalf("status = %s (%s)", res.Status, res.FailureCode)
	}
	exigirSaldo(t, a, w, 6000)
	exigirLancamentos(t, a, "DEBIT 40.00", "CREDIT 40.00", "DEBIT 40.00")
}

func TestROLLBACKSemSaldoTemCodigoProprio(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("100.00")

	processar(t, a, pedido(w, wagerdomain.KindWin, "30.00"))
	processar(t, a, pedido(w, wagerdomain.KindBet, "130.00", comChave("key-BET2"), comExterna("ext-BET2")))

	res := processar(t, a, pedido(w, wagerdomain.KindRollback, "30.00", comReferencia("ext-WIN")))

	exigirRejeicao(t, res, appwagering.CodeRollbackInsufficientFunds)
	exigirSaldo(t, a, w, 0)
}

// ---------------------------------------------------------------------------
// Idempotência
// ---------------------------------------------------------------------------

func TestReplayDevolveOSaldoDoProcessamentoOriginal(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("1000.00")

	primeiro := processar(t, a, pedido(w, wagerdomain.KindBet, "25.00"))
	if primeiro.Balance.Minor() != 97500 {
		t.Fatalf("primeiro saldo = %v", primeiro.Balance)
	}

	// A carteira muda DEPOIS do processamento original.
	processar(t, a, pedido(w, wagerdomain.KindBet, "100.00", comChave("key-outra"), comExterna("ext-outra")))
	exigirSaldo(t, a, w, 87500)

	replay := processar(t, a, pedido(w, wagerdomain.KindBet, "25.00"))

	if !replay.IdempotentReplay {
		t.Fatal("mesma chave e mesmo conteúdo é replay")
	}
	if replay.TransactionID != primeiro.TransactionID {
		t.Fatalf("replay devolveu outra transação: %s e %s", replay.TransactionID, primeiro.TransactionID)
	}
	if replay.Balance.Minor() != 97500 {
		t.Fatalf("replay devolveu o saldo de AGORA (%v); o contrato pede o do processamento original", replay.Balance)
	}
	// E não movimentou nada: um débito no ledger, não dois.
	exigirLancamentos(t, a, "DEBIT 25.00", "DEBIT 100.00")
}

func TestMesmaChaveComConteudoDiferenteEConflito(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("1000.00")

	processar(t, a, pedido(w, wagerdomain.KindBet, "25.00"))

	err := falhar(t, a, pedido(w, wagerdomain.KindBet, "30.00"))
	exigirCodigo(t, err, appwagering.CodeIdempotencyConflict)

	exigirSaldo(t, a, w, 97500)
	exigirLancamentos(t, a, "DEBIT 25.00")
}

func TestIdentidadeExternaComOutraChaveEConflito(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("1000.00")

	processar(t, a, pedido(w, wagerdomain.KindBet, "25.00"))

	// Mesma (providerId, externalTransactionId), chave de idempotência nova.
	err := falhar(t, a, pedido(w, wagerdomain.KindBet, "25.00", comChave("key-nova")))
	exigirCodigo(t, err, appwagering.CodeIdempotencyConflict)

	exigirSaldo(t, a, w, 97500)
	exigirLancamentos(t, a, "DEBIT 25.00")
}

// TestLinhaApareceEntreAsDuasLeiturasDeIdempotenciaEUmReplay é o teste de
// regressão de um defeito real achado pela suíte de integração: 12 disparos
// simultâneos da MESMA operação faziam alguns pedidos legítimos receberem
// conflito.
//
// A causa: a consulta por chave e a consulta por identidade externa são dois
// comandos separados, e a linha pode ser COMITADA por outra instância entre os
// dois. O código antigo, ao achar a linha na segunda consulta, concluía "está
// registrada sob outra chave" sem olhar a chave da linha encontrada.
func TestLinhaApareceEntreAsDuasLeiturasDeIdempotenciaEUmReplay(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("1000.00")

	primeiro := processar(t, a, pedido(w, wagerdomain.KindBet, "25.00"))

	// A consulta por chave "não vê" a linha uma vez; a consulta por identidade
	// externa vê. É exatamente a janela entre os dois comandos.
	a.uow.esconderChave = 1
	replay := processar(t, a, pedido(w, wagerdomain.KindBet, "25.00"))

	if !replay.IdempotentReplay {
		t.Fatal("mesma chave e mesmo conteúdo é replay, mesmo quando a linha aparece no meio da leitura")
	}
	if replay.TransactionID != primeiro.TransactionID {
		t.Fatalf("replay devolveu outra transação: %s e %s", replay.TransactionID, primeiro.TransactionID)
	}
	exigirLancamentos(t, a, "DEBIT 25.00")
}

// ---------------------------------------------------------------------------
// Referências
// ---------------------------------------------------------------------------

func TestReversaoSemReferenciaAindaFicaPendente(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("100.00")

	res := processar(t, a, pedido(w, wagerdomain.KindRefund, "40.00", comReferencia("ext-que-nao-existe")))

	if res.Status != wagerdomain.StatusPendingReference {
		t.Fatalf("status = %s, esperado PENDING_REFERENCE", res.Status)
	}
	if res.Balance != nil || res.FailureCode != "" {
		t.Fatalf("pendência não devolve saldo nem código de falha: %+v", res)
	}
	exigirSaldo(t, a, w, 10000)
	exigirLancamentos(t, a)
	exigirEventos(t, a, wagerdomain.EventTypePendingReference)

	tx := a.transacao("provider-a", "key-REFUND")
	if tx == nil || tx.Status() != wagerdomain.StatusPendingReference {
		t.Fatalf("pendência precisa ficar PERSISTIDA: %+v", tx)
	}
	if tx.ReferenceResolutionAttempt() != 1 {
		t.Fatalf("tentativa = %d, esperado 1", tx.ReferenceResolutionAttempt())
	}
	if !tx.NextResolutionAttemptAt().After(a.relogio.Now()) {
		t.Fatal("a próxima tentativa precisa estar no futuro")
	}
}

func TestReversaoDeReferenciaRejeitadaNaoProcessa(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("100.00")

	// A referência existe, mas foi recusada por saldo: não há o que devolver.
	res := processar(t, a, pedido(w, wagerdomain.KindBet, "180.00"))
	if res.Status != wagerdomain.StatusRejected {
		t.Fatalf("preparação falhou: %s", res.Status)
	}

	res = processar(t, a, pedido(w, wagerdomain.KindRefund, "180.00", comReferencia("ext-BET")))
	exigirRejeicao(t, res, appwagering.CodeReferenceNotProcessable)
	exigirSaldo(t, a, w, 10000)
}

func TestReversaoDuplicadaEALREADYREVERSED(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("100.00")

	processar(t, a, pedido(w, wagerdomain.KindBet, "40.00"))
	processar(t, a, pedido(w, wagerdomain.KindRefund, "40.00", comReferencia("ext-BET")))

	res := processar(t, a, pedido(w, wagerdomain.KindRefund, "40.00",
		comReferencia("ext-BET"), comChave("key-REFUND-2"), comExterna("ext-REFUND-2")))

	exigirRejeicao(t, res, wagerdomain.CodeReversed)
	exigirSaldo(t, a, w, 10000)
}

// O caminho INVERSO do teste acima, e o defeito 50: a BET desfeita por ROLLBACK
// e depois estornada por REFUND.
//
// A guarda de coerência existia só para o ROLLBACK (desfazer uma BET já estornada
// era recusado), e não para o REFUND. O resultado era dinheiro criado do nada:
//
//	BET 40 → ROLLBACK 40 → REFUND 40   ⇒   100.00 → 60.00 → 100.00 → 140.00
//
// Achado na Fase 12, ao escrever o `SISTEMA_EXPLICADO.md`: a afirmação "os dois
// sentidos são barrados" não se sustentava no código, e o teste que a sustentasse
// não existia. Agora existe, e falha se a assimetria voltar.
func TestREFUNDDeBETJaDesfeitaPorROLLBACKEhRejeitado(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("100.00")

	processar(t, a, pedido(w, wagerdomain.KindBet, "40.00"))
	processar(t, a, pedido(w, wagerdomain.KindRollback, "40.00", comReferencia("ext-BET")))
	// O ROLLBACK devolveu o débito: a carteira voltou ao saldo inicial.
	exigirSaldo(t, a, w, 10000)

	res := processar(t, a, pedido(w, wagerdomain.KindRefund, "40.00", comReferencia("ext-BET")))

	exigirRejeicao(t, res, wagerdomain.CodeReversed)
	exigirSaldo(t, a, w, 10000)
	// Dois lançamentos: o débito da BET e o crédito do ROLLBACK. O REFUND
	// rejeitado não movimenta nada.
	exigirLancamentos(t, a, "DEBIT 40.00", "CREDIT 40.00")
}

func TestReferenciaDivergenteEReferenceMISMATCH(t *testing.T) {
	casos := []struct {
		nome string
		opts []pedidoOpt
	}{
		{"rodada diferente", []pedidoOpt{comRodada("round-2")}},
		{"valor diferente do referenciado", []pedidoOpt{comValor("30.00")}},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			a := novoAmbiente()
			w := a.abrirCarteira("100.00")
			processar(t, a, pedido(w, wagerdomain.KindBet, "40.00"))

			opts := append([]pedidoOpt{comReferencia("ext-BET")}, caso.opts...)
			res := processar(t, a, pedido(w, wagerdomain.KindRefund, "40.00", opts...))

			if res.FailureCode != appwagering.CodeReferenceMismatch {
				t.Fatalf("failureCode = %q, esperado %q", res.FailureCode, appwagering.CodeReferenceMismatch)
			}
			exigirSaldo(t, a, w, 6000)
			exigirLancamentos(t, a, "DEBIT 40.00")
		})
	}
}

func TestReferenciaDeOutraCarteiraEReferenceMISMATCH(t *testing.T) {
	a := novoAmbiente()
	daCarteira := a.abrirCarteira("100.00")
	outra := a.abrirCarteira("100.00")

	processar(t, a, pedido(outra, wagerdomain.KindBet, "40.00"))

	res := processar(t, a, pedido(daCarteira, wagerdomain.KindRefund, "40.00", comReferencia("ext-BET")))

	if res.FailureCode != appwagering.CodeReferenceMismatch {
		t.Fatalf("failureCode = %q, esperado %q", res.FailureCode, appwagering.CodeReferenceMismatch)
	}
	exigirSaldo(t, a, daCarteira, 10000)
}

func TestReferenciaInformativaNaoBloqueiaOProcessamento(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("100.00")

	// WIN com referência que ainda não existe: a referência é informativa, e
	// atrasar o pagamento por causa dela seria pior do que processar.
	res := processar(t, a, pedido(w, wagerdomain.KindWin, "30.00", comReferencia("ext-BET-que-nao-chegou")))

	if res.Status != wagerdomain.StatusProcessed {
		t.Fatalf("status = %s, esperado PROCESSED", res.Status)
	}
	exigirSaldo(t, a, w, 13000)

	// E a referência informativa NÃO ocupa o vínculo interno da reversão.
	if tx := a.transacao("provider-a", "key-WIN"); tx.ReferenceTransactionID() != "" {
		t.Fatal("referência informativa não pode virar reference_transaction_id")
	}
}

// ---------------------------------------------------------------------------
// Carteira
// ---------------------------------------------------------------------------

func TestMoedaDaOperacaoDiferenteDaCarteira(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("100.00")

	req := pedido(w, wagerdomain.KindBet, "25.00")
	req.Amount = money.FromMinor(2500, "USD")

	res := processar(t, a, req)

	exigirRejeicao(t, res, wallet.CodeWalletCurrencyMismatch)
	exigirSaldo(t, a, w, 10000)
}

func TestCarteiraDeOutroJogador(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("100.00")

	res := processar(t, a, pedido(w, wagerdomain.KindBet, "25.00", comJogador("jogador-2")))

	exigirRejeicao(t, res, appwagering.CodeWalletPlayerMismatch)
	exigirSaldo(t, a, w, 10000)
}

func TestCarteiraInexistenteNaoViraRejeicaoGravada(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("100.00")

	// UUID válido de carteira que não existe: a operação não tem onde ser
	// gravada (a FK impede), então é erro, e não rejeição.
	inexistente := ids.WalletID(uuidDeTeste(9, 1))
	err := falhar(t, a, pedido(w, wagerdomain.KindBet, "25.00", comCarteira(inexistente)))

	exigirCodigo(t, err, appwagering.CodeWalletNotFound)
	if len(a.estado.txns) != 0 {
		t.Fatal("sem carteira não há linha para gravar: a FK impede")
	}
	if len(a.estado.outbox) != 0 {
		t.Fatal("operação que não existe não gera evento")
	}
}

// ---------------------------------------------------------------------------
// Entrada inválida: nada é gravado, e a mesma chave pode ser reenviada
// ---------------------------------------------------------------------------

func TestEntradaInvalidaNaoGravaNada(t *testing.T) {
	casos := []struct {
		nome     string
		montar   func(w *wallet.Wallet) appwagering.Request
		esperado string
	}{
		{
			"sem chave de idempotência",
			func(w *wallet.Wallet) appwagering.Request {
				return pedido(w, wagerdomain.KindBet, "25.00", comChave(""))
			},
			appwagering.CodeIdempotencyKeyRequired,
		},
		{
			"OPENING enviado de fora",
			func(w *wallet.Wallet) appwagering.Request {
				return pedido(w, wagerdomain.KindOpening, "25.00")
			},
			wagerdomain.CodeInternalKind,
		},
		{
			"LOSS com valor diferente de zero",
			func(w *wallet.Wallet) appwagering.Request {
				return pedido(w, wagerdomain.KindLoss, "10.00")
			},
			wagerdomain.FailureCodeZeroAmount,
		},
		{
			"REFUND sem referência",
			func(w *wallet.Wallet) appwagering.Request {
				return pedido(w, wagerdomain.KindRefund, "25.00")
			},
			wagerdomain.CodeMissingReference,
		},
		{
			"autorreferência",
			func(w *wallet.Wallet) appwagering.Request {
				return pedido(w, wagerdomain.KindRefund, "25.00", comReferencia("ext-REFUND"))
			},
			appwagering.CodeInvalidRequest,
		},
		{
			"sem rodada",
			func(w *wallet.Wallet) appwagering.Request {
				return pedido(w, wagerdomain.KindBet, "25.00", comRodada("  "))
			},
			appwagering.CodeInvalidRequest,
		},
		{
			// `wallets.id` é UUID: um valor que não é UUID seria recusado pelo
			// PostgreSQL com 22P02, e o cliente receberia 500 em vez de 400.
			"walletID que não é UUID",
			func(w *wallet.Wallet) appwagering.Request {
				return pedido(w, wagerdomain.KindBet, "25.00", comCarteira("nao-e-uuid"))
			},
			appwagering.CodeInvalidRequest,
		},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			a := novoAmbiente()
			w := a.abrirCarteira("100.00")

			err := falhar(t, a, caso.montar(w))
			exigirCodigo(t, err, caso.esperado)

			if len(a.estado.txns) != 0 || len(a.estado.ledger) != 0 || len(a.estado.outbox) != 0 {
				t.Fatal("requisição inválida não deixa transação, lançamento nem evento")
			}
			exigirSaldo(t, a, w, 10000)
		})
	}
}

// ---------------------------------------------------------------------------
// Atomicidade e retentativa
// ---------------------------------------------------------------------------

func TestFalhaNaOutboxNaoDeixaResiduo(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("1000.00")
	a.uow.falhaOutbox = errors.New("outbox indisponível")

	_, err := a.servico.Process(context.Background(), pedido(w, wagerdomain.KindBet, "25.00"))
	if err == nil {
		t.Fatal("esperava erro da outbox")
	}

	// O commit não aconteceu: saldo, versão, ledger, transação e evento voltam ao
	// estado anterior. É o mesmo que o ROLLBACK faz no banco.
	exigirSaldo(t, a, w, 100000)
	if v := a.versao(w.ID()); v != 1 {
		t.Fatalf("versão = %d, esperado 1", v)
	}
	exigirLancamentos(t, a)
	if len(a.estado.txns) != 0 {
		t.Fatal("a transação não pode ficar gravada sem o evento")
	}
}

func TestFalhaTransitoriaERepetida(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("1000.00")
	a.uow.falhaTransitoriaEm = 1

	res := processar(t, a, pedido(w, wagerdomain.KindBet, "25.00"))

	if res.Status != wagerdomain.StatusProcessed {
		t.Fatalf("status = %s", res.Status)
	}
	if a.uow.chamadas != 2 {
		t.Fatalf("execuções = %d, esperado 2 (uma que falhou e uma que concluiu)", a.uow.chamadas)
	}
	// A repetição ACONTECEU, então a métrica de conflito precisa ter contado
	// exatamente uma vez (defeito 42: a métrica existia e nunca saía de zero).
	if a.conflitos.total != 1 {
		t.Fatalf("conflitos contados = %d, esperado 1", a.conflitos.total)
	}
	exigirSaldo(t, a, w, 97500)
	// E o lançamento é UM só: a tentativa que falhou não gravou nada.
	exigirLancamentos(t, a, "DEBIT 25.00")
}

func TestRetentativaEsgotadaDevolveErroTransitorio(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("1000.00")
	a.uow.falharSempreTransitorio = true

	err := falhar(t, a, pedido(w, wagerdomain.KindBet, "25.00"))

	if !ports.IsRetryable(err) {
		t.Fatalf("erro de infraestrutura precisa continuar classificável como transitório: %v", err)
	}
	if code := domainerrors.CodeOf(err); code != "" {
		t.Fatalf("falha de infraestrutura não é código de domínio, veio %q", code)
	}
	if a.uow.chamadas != 3 {
		t.Fatalf("execuções = %d, esperado 3 (o teto)", a.uow.chamadas)
	}
	// Duas repetições e uma desistência: o contador conta as REPETIÇÕES, não as
	// tentativas — a última não é conflito tratado, é o erro que sobe.
	if a.conflitos.total != 2 {
		t.Fatalf("conflitos contados = %d, esperado 2", a.conflitos.total)
	}
	exigirSaldo(t, a, w, 100000)
	exigirLancamentos(t, a)
}

// TestConflitoDeUnicidadeERepetidoEContado cobre o OUTRO motivo de repetição: a
// violação de unicidade (alguém chegou primeiro), que o serviço trata igual a
// falha transitória — repetir a transação inteira e decidir na segunda passada.
func TestConflitoDeUnicidadeERepetidoEContado(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("1000.00")
	a.uow.falharSempreConflito = true

	err := falhar(t, a, pedido(w, wagerdomain.KindBet, "25.00"))

	if !ports.IsConflict(err) {
		t.Fatalf("o erro precisa continuar classificável como conflito: %v", err)
	}
	// Duas repetições, e a terceira tentativa sobe com o conflito original.
	if a.conflitos.total != 2 {
		t.Fatalf("conflitos contados = %d, esperado 2", a.conflitos.total)
	}
	if a.uow.chamadas != 3 {
		t.Fatalf("execuções = %d, esperado 3", a.uow.chamadas)
	}
	exigirSaldo(t, a, w, 100000)
}
