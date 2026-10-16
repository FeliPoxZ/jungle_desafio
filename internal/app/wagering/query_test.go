package wagering_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ledger"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
)

// lancamentosDaCarteira são os lançamentos do estado, na ordem em que entraram.
func (a *ambiente) idsDosLancamentos() []string {
	var saida []string
	for _, e := range a.estado.ledger {
		saida = append(saida, e.ID())
	}
	return saida
}

// paginar percorre todas as páginas e devolve os IDs na ordem em que saíram.
func (a *ambiente) paginar(t *testing.T, walletID ids.WalletID, limite int) []string {
	t.Helper()

	var (
		ids    []string
		cursor string
	)
	for volta := 0; volta < 20; volta++ {
		pagina, err := a.servico.ListLedger(context.Background(), walletID, limite, cursor)
		if err != nil {
			t.Fatalf("ListLedger: %v", err)
		}
		for _, e := range pagina.Entries {
			ids = append(ids, e.ID())
		}
		if pagina.NextCursor == "" {
			return ids
		}
		cursor = pagina.NextCursor
	}
	t.Fatal("paginação não terminou em 20 voltas")
	return nil
}

func TestGetTransactionDevolveAOperacaoGravada(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("1000.00")

	res := processar(t, a, pedido(w, wagerdomain.KindBet, "25.00"))

	tx, err := a.servico.GetTransaction(context.Background(), res.TransactionID)
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if tx.ID() != res.TransactionID || tx.Status() != wagerdomain.StatusProcessed {
		t.Fatalf("transação = %s %s", tx.ID(), tx.Status())
	}
	if tx.ProviderID() != "provider-a" || tx.ExternalTransactionID() != "ext-BET" {
		t.Fatalf("identidade do provedor = %q/%q", tx.ProviderID(), tx.ExternalTransactionID())
	}

	// A rejeição também é consultável, com o código que a explica (README §9:
	// "permitir acompanhar pendências e consultar códigos de rejeição").
	rejeitada := processar(t, a, pedido(w, wagerdomain.KindBet, "9999.00", comChave("key-rejeitada"), comExterna("ext-rejeitada")))
	tx, err = a.servico.GetTransaction(context.Background(), rejeitada.TransactionID)
	if err != nil {
		t.Fatalf("GetTransaction da rejeição: %v", err)
	}
	if tx.Status() != wagerdomain.StatusRejected || tx.FailureCode() != wallet.CodeInsufficientFunds {
		t.Fatalf("rejeição = %s %s", tx.Status(), tx.FailureCode())
	}
}

func TestLeituraDeOutroProvedorNaoEncontraNada(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("1000.00")
	res := processar(t, a, pedido(w, wagerdomain.KindBet, "25.00"))

	// O provedor certo lê a própria operação.
	if _, err := a.servico.GetProviderTransaction(context.Background(), "provider-a", res.TransactionID); err != nil {
		t.Fatalf("provedor dono deveria ler: %v", err)
	}

	// O outro recebe "não existe" — e NÃO "proibido": confirmar que a operação
	// existe já seria vazar informação sobre o outro provedor.
	_, err := a.servico.GetProviderTransaction(context.Background(), "provider-b", res.TransactionID)
	if !errors.Is(err, appwagering.ErrTransactionNotFound) {
		t.Fatalf("erro = %v, esperado TRANSACTION_NOT_FOUND", err)
	}
	if errors.Is(err, appwagering.ErrIdempotencyConflict) {
		t.Fatal("isolamento não é conflito de idempotência")
	}
	exigirCodigo(t, err, appwagering.CodeTransactionNotFound)

	// O mesmo vale pela identidade externa: a consulta é por provedor.
	if _, err := a.servico.GetByProviderExternalID(context.Background(), "provider-a", "ext-BET"); err != nil {
		t.Fatalf("busca pela identidade externa: %v", err)
	}
	if _, err := a.servico.GetByProviderExternalID(context.Background(), "provider-b", "ext-BET"); !errors.Is(err, appwagering.ErrTransactionNotFound) {
		t.Fatalf("erro = %v, esperado TRANSACTION_NOT_FOUND", err)
	}
}

func TestGetWalletEGetWalletInexistente(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("1000.00")

	carteira, err := a.servico.GetWallet(context.Background(), w.ID())
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	if carteira.ID() != w.ID() || carteira.Balance().Minor() != 100000 {
		t.Fatalf("carteira = %s %s", carteira.ID(), carteira.Balance())
	}

	_, err = a.servico.GetWallet(context.Background(), ids.WalletID(uuidDeTeste(9, 1)))
	if !errors.Is(err, appwagering.ErrWalletNotFound) {
		t.Fatalf("erro = %v, esperado WALLET_NOT_FOUND", err)
	}

	// Identificador que NÃO é UUID: também "não encontrado" — a coluna é UUID, e
	// deixar isso virar consulta faria o banco responder 22P02 (erro permanente,
	// ou seja 500 na borda).
	_, err = a.servico.GetWallet(context.Background(), "nao-e-uuid")
	if !errors.Is(err, appwagering.ErrWalletNotFound) {
		t.Fatalf("erro = %v, esperado WALLET_NOT_FOUND", err)
	}
}

func TestListLedgerPaginaSemRepetirNemPular(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("1000.00")

	// Três lançamentos na carteira do teste.
	processar(t, a, pedido(w, wagerdomain.KindBet, "10.00"))
	processar(t, a, pedido(w, wagerdomain.KindWin, "20.00"))
	processar(t, a, pedido(w, wagerdomain.KindBet, "30.00", comChave("key-BET2"), comExterna("ext-BET2")))

	encontrados := a.paginar(t, w.ID(), 2)
	if len(encontrados) != 3 {
		t.Fatalf("lançamentos percorridos = %d, esperado 3", len(encontrados))
	}

	// Sem repetição e sem pular: o que a paginação percorreu é exatamente o
	// conjunto de lançamentos da carteira.
	esperados := map[string]bool{}
	for _, e := range a.estado.ledger {
		if e.WalletID() == w.ID() {
			esperados[e.ID()] = true
		}
	}
	vistos := map[string]bool{}
	for _, id := range encontrados {
		if vistos[id] {
			t.Fatalf("lançamento %s apareceu duas vezes", id)
		}
		if !esperados[id] {
			t.Fatalf("lançamento %s não é da carteira", id)
		}
		vistos[id] = true
	}
	if len(vistos) != len(esperados) {
		t.Fatalf("percorridos = %d, lançamentos da carteira = %d", len(vistos), len(esperados))
	}
}

func TestListLedgerAplicaPadraoETetoDaPagina(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("1000000.00")

	// 205 lançamentos, para exercitar o teto de 200.
	zero := money.FromMinor(0, money.BRL)
	for i := 0; i < 205; i++ {
		a.inserirLancamento(t, w, i, zero)
	}

	// limite 0 ⇒ padrão 50.
	pagina, err := a.servico.ListLedger(context.Background(), w.ID(), 0, "")
	if err != nil {
		t.Fatalf("ListLedger: %v", err)
	}
	if len(pagina.Entries) != 50 || pagina.NextCursor == "" {
		t.Fatalf("página padrão = %d entradas, cursor %q", len(pagina.Entries), pagina.NextCursor)
	}

	// limite absurdo ⇒ teto 200 (não uma varredura).
	pagina, err = a.servico.ListLedger(context.Background(), w.ID(), 100000, "")
	if err != nil {
		t.Fatalf("ListLedger: %v", err)
	}
	if len(pagina.Entries) != 200 || pagina.NextCursor == "" {
		t.Fatalf("página com teto = %d entradas, cursor %q", len(pagina.Entries), pagina.NextCursor)
	}
}

func TestListLedgerRecusaCursorInvalido(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("100.00")

	_, err := a.servico.ListLedger(context.Background(), w.ID(), 10, "isto-nao-e-cursor")
	if err == nil {
		t.Fatal("esperava erro de cursor inválido")
	}
	exigirCodigo(t, err, appwagering.CodeInvalidRequest)
}

func TestListLedgerDeCarteiraInexistenteERecusa(t *testing.T) {
	a := novoAmbiente()

	// Página vazia seria ambígua: o cliente não saberia se a carteira não tem
	// lançamento ou se não existe.
	_, err := a.servico.ListLedger(context.Background(), ids.WalletID(uuidDeTeste(9, 1)), 10, "")
	if !errors.Is(err, appwagering.ErrWalletNotFound) {
		t.Fatalf("erro = %v, esperado WALLET_NOT_FOUND", err)
	}

	_, err = a.servico.ListLedger(context.Background(), "nao-e-uuid", 10, "")
	if !errors.Is(err, appwagering.ErrWalletNotFound) {
		t.Fatalf("erro = %v, esperado WALLET_NOT_FOUND", err)
	}
}

func TestReconcileConsistenteAposAberturaEAposta(t *testing.T) {
	a := novoAmbiente()

	// Carteira aberta com 1000.00 (lançamento de abertura) e uma aposta de 25.00.
	abertura := abrir(t, a, appwagering.OpenWalletRequest{
		PlayerID: "jogador-1", InitialBalance: brl("1000.00"), IdempotencyKey: "abertura-1",
	})
	carteira := a.estado.wallets[abertura.WalletID]
	processar(t, a, pedido(carteira, wagerdomain.KindBet, "25.00"))

	rec, err := a.servico.Reconcile(context.Background(), abertura.WalletID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if !rec.Consistent || rec.Difference.Minor() != 0 {
		t.Fatalf("reconciliação = %+v", rec)
	}
	if rec.StoredBalance.Minor() != 97500 || rec.CalculatedBalance.Minor() != 97500 {
		t.Fatalf("saldos = %s e %s", rec.StoredBalance, rec.CalculatedBalance)
	}
	if rec.CheckedEntries != 2 {
		t.Fatalf("checkedEntries = %d, esperado 2 (abertura e aposta)", rec.CheckedEntries)
	}
}

func TestReconcileDetectaDivergenciaNosDoisSentidos(t *testing.T) {
	a := novoAmbiente()

	// A carteira é aberta pelo caminho real, para o ledger ter o lançamento que
	// explica o saldo.
	abertura := abrir(t, a, appwagering.OpenWalletRequest{
		PlayerID: "jogador-1", InitialBalance: brl("100.00"), IdempotencyKey: "abertura-1",
	})

	// Saldo MAIOR que o ledger: alguém creditou sem lançar.
	a.forcarSaldo(t, abertura.WalletID, "150.00")
	rec, err := a.servico.Reconcile(context.Background(), abertura.WalletID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rec.Consistent || rec.Difference.Minor() != 5000 {
		t.Fatalf("diferença = %s (consistente = %v), esperado 50.00", rec.Difference, rec.Consistent)
	}

	// Saldo MENOR que o ledger: diferença negativa. O Money aceita negativo em
	// cálculo interno, e a reconciliação precisa saber o SINAL da divergência.
	a.forcarSaldo(t, abertura.WalletID, "40.00")
	rec, err = a.servico.Reconcile(context.Background(), abertura.WalletID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rec.Consistent || rec.Difference.Minor() != -6000 {
		t.Fatalf("diferença = %s, esperado -60.00", rec.Difference)
	}
}

func TestReconcileNaoAlteraOSaldo(t *testing.T) {
	a := novoAmbiente()
	abertura := abrir(t, a, appwagering.OpenWalletRequest{
		PlayerID: "jogador-1", InitialBalance: brl("100.00"), IdempotencyKey: "abertura-1",
	})
	a.forcarSaldo(t, abertura.WalletID, "150.00")

	if _, err := a.servico.Reconcile(context.Background(), abertura.WalletID); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if saldo := a.saldo(abertura.WalletID); saldo != 15000 {
		t.Fatalf("saldo = %d; a reconciliação não pode mexer no saldo", saldo)
	}
}

func TestReconcileDeCarteiraInexistente(t *testing.T) {
	a := novoAmbiente()

	_, err := a.servico.Reconcile(context.Background(), ids.WalletID(uuidDeTeste(9, 1)))
	if !errors.Is(err, appwagering.ErrWalletNotFound) {
		t.Fatalf("erro = %v, esperado WALLET_NOT_FOUND", err)
	}

	_, err = a.servico.Reconcile(context.Background(), "nao-e-uuid")
	if !errors.Is(err, appwagering.ErrWalletNotFound) {
		t.Fatalf("erro = %v, esperado WALLET_NOT_FOUND", err)
	}
}

// inserirLancamento grava um lançamento direto no estado, para os testes de
// paginação não precisarem de 205 operações de negócio.
func (a *ambiente) inserirLancamento(t *testing.T, w *wallet.Wallet, indice int, antes money.Money) {
	t.Helper()

	txnID := wagerdomain.TransactionID("txn-" + strconv.Itoa(indice))
	entrada, err := ledger.NewEntry(w.ID(), txnID, ledger.DirectionCredit, brl("1.00"), antes, a.relogio.Now())
	if err != nil {
		t.Fatalf("NewEntry: %v", err)
	}
	a.estado.ledger = append(a.estado.ledger, entrada)
}

// forcarSaldo troca o saldo da carteira SEM lançamento — é a divergência que a
// reconciliação existe para encontrar.
func (a *ambiente) forcarSaldo(t *testing.T, walletID ids.WalletID, saldo string) {
	t.Helper()

	atual, ok := a.estado.wallets[walletID]
	if !ok {
		t.Fatalf("carteira %s não existe no estado", walletID)
	}
	a.estado.wallets[walletID] = wallet.Rehydrate(
		atual.ID(), atual.PlayerID(), brl(saldo), atual.Version(), atual.CreatedAt(), atual.UpdatedAt(),
	)
}
