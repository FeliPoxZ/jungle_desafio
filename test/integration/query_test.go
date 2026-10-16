//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
)

func TestLeiturasDeTransacaoContraOBancoReal(t *testing.T) {
	limpar(t)

	abertura := abrirCarteira(t, "jogador-leitura", "1000.00", "abertura-leitura")
	carteira := carteiraDoBanco(t, abertura.WalletID)
	processada := processar(t, pedido(carteira, wagerdomain.KindBet, "25.00"))

	// Leitura interna, pelo identificador.
	tx, err := servico.GetTransaction(context.Background(), processada.TransactionID)
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if tx.Status() != wagerdomain.StatusProcessed || tx.Amount().Minor() != 2500 {
		t.Fatalf("transação = %s %s", tx.Status(), tx.Amount())
	}

	// Leitura do provedor DONO.
	if _, err := servico.GetProviderTransaction(context.Background(), "provider-a", processada.TransactionID); err != nil {
		t.Fatalf("provedor dono deveria ler: %v", err)
	}

	// Leitura de OUTRO provedor: "não existe", e não "proibido".
	_, err = servico.GetProviderTransaction(context.Background(), "provider-b", processada.TransactionID)
	if !errors.Is(err, appwagering.ErrTransactionNotFound) {
		t.Fatalf("erro = %v, esperado TRANSACTION_NOT_FOUND", err)
	}

	// Pela identidade externa, idem: a consulta é por provedor.
	if _, err := servico.GetByProviderExternalID(context.Background(), "provider-a", "ext-BET"); err != nil {
		t.Fatalf("busca pela identidade externa: %v", err)
	}
	if _, err := servico.GetByProviderExternalID(context.Background(), "provider-b", "ext-BET"); !errors.Is(err, appwagering.ErrTransactionNotFound) {
		t.Fatalf("erro = %v, esperado TRANSACTION_NOT_FOUND", err)
	}

	// A rejeição é consultável com o código que a explica.
	rejeitada := processar(t, pedido(carteira, wagerdomain.KindBet, "99999.00",
		comChave("key-rejeitada"), comExterna("ext-rejeitada")))
	tx, err = servico.GetTransaction(context.Background(), rejeitada.TransactionID)
	if err != nil {
		t.Fatalf("GetTransaction da rejeição: %v", err)
	}
	if tx.Status() != wagerdomain.StatusRejected || tx.FailureCode() == "" {
		t.Fatalf("rejeição = %s %q", tx.Status(), tx.FailureCode())
	}
}

func TestListLedgerPaginacaoContraOBancoReal(t *testing.T) {
	limpar(t)

	abertura := abrirCarteira(t, "jogador-pagina", "1000.00", "abertura-pagina")
	carteira := carteiraDoBanco(t, abertura.WalletID)
	processar(t, pedido(carteira, wagerdomain.KindBet, "10.00"))
	processar(t, pedido(carteira, wagerdomain.KindWin, "20.00"))
	processar(t, pedido(carteira, wagerdomain.KindBet, "30.00", comChave("key-BET2"), comExterna("ext-BET2")))

	// Quatro lançamentos (a abertura e três operações), página de 2.
	var (
		percorridos []string
		cursor      string
		paginas     int
	)
	for {
		pagina, err := servico.ListLedger(context.Background(), abertura.WalletID, 2, cursor)
		if err != nil {
			t.Fatalf("ListLedger: %v", err)
		}
		paginas++
		for _, e := range pagina.Entries {
			percorridos = append(percorridos, e.ID())
		}
		if pagina.NextCursor == "" {
			break
		}
		cursor = pagina.NextCursor
		if paginas > 10 {
			t.Fatal("paginação não terminou")
		}
	}

	if len(percorridos) != 4 {
		t.Fatalf("lançamentos percorridos = %d, esperado 4", len(percorridos))
	}
	vistos := map[string]bool{}
	for _, id := range percorridos {
		if vistos[id] {
			t.Fatalf("lançamento %s veio em duas páginas", id)
		}
		vistos[id] = true
	}

	// Ordem estável e cronológica: a abertura é o primeiro lançamento.
	primeira, err := servico.ListLedger(context.Background(), abertura.WalletID, 1, "")
	if err != nil {
		t.Fatalf("ListLedger: %v", err)
	}
	if len(primeira.Entries) != 1 || primeira.Entries[0].Direction().String() != "CREDIT" {
		t.Fatalf("primeiro lançamento = %+v", primeira.Entries)
	}
	if primeira.Entries[0].BalanceBefore().Minor() != 0 {
		t.Fatalf("o primeiro lançamento tem que ser a abertura, partindo de zero: %s",
			primeira.Entries[0].BalanceBefore())
	}

	// Cursor inválido é entrada inválida, não erro de servidor.
	if _, err := servico.ListLedger(context.Background(), abertura.WalletID, 2, "nao-e-cursor"); err == nil {
		t.Fatal("esperava erro de cursor inválido")
	}
}

func TestReconcileFechaOExemploDoREADME(t *testing.T) {
	limpar(t)

	// O exemplo do README §9: abertura de 1000.00 e uma aposta de 25.00.
	abertura := abrirCarteira(t, "jogador-reconcile", "1000.00", "abertura-reconcile")
	carteira := carteiraDoBanco(t, abertura.WalletID)
	processar(t, pedido(carteira, wagerdomain.KindBet, "25.00"))

	rec, err := servico.Reconcile(context.Background(), abertura.WalletID)
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
		t.Fatalf("checkedEntries = %d, esperado 2", rec.CheckedEntries)
	}
}

func TestReconcileDetectaSaldoAdulteradoESeRecusaAMexer(t *testing.T) {
	limpar(t)

	abertura := abrirCarteira(t, "jogador-divergente", "100.00", "abertura-divergente")

	// Adultera o saldo POR FORA do fluxo: é a divergência que a reconciliação
	// existe para encontrar (e que, no sistema real, indicaria bug ou intervenção
	// manual no banco).
	if _, err := poolTeste.Exec(context.Background(),
		`UPDATE wallets SET balance_minor = balance_minor + 5000 WHERE id = $1`,
		string(abertura.WalletID)); err != nil {
		t.Fatalf("adulterando o saldo: %v", err)
	}

	rec, err := servico.Reconcile(context.Background(), abertura.WalletID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if rec.Consistent {
		t.Fatal("a divergência tinha que aparecer")
	}
	if rec.Difference.Minor() != 5000 {
		t.Fatalf("difference = %s, esperado 50.00 (stored − calculated)", rec.Difference)
	}
	if rec.CheckedEntries != 1 {
		t.Fatalf("checkedEntries = %d, esperado 1", rec.CheckedEntries)
	}

	// A reconciliação RELATA e não corrige: o saldo adulterado continua lá.
	if saldo := saldoNoBanco(t, abertura.WalletID); saldo != 15000 {
		t.Fatalf("saldo = %d; a reconciliação não pode alterar saldo", saldo)
	}
}

func TestReconcileDeCarteiraInexistenteERecusa(t *testing.T) {
	limpar(t)

	_, err := servico.Reconcile(context.Background(), ids.WalletID("carteira-que-nao-existe"))
	if !errors.Is(err, appwagering.ErrWalletNotFound) {
		t.Fatalf("erro = %v, esperado WALLET_NOT_FOUND", err)
	}
}

// TestTransacaoSomenteLeituraRecusaEscrita prova o mecanismo em que a
// reconciliação se apoia: a visão consistente é uma transação READ ONLY, então
// nem um bug conseguiria alterar saldo ali dentro.
func TestTransacaoSomenteLeituraRecusaEscrita(t *testing.T) {
	limpar(t)

	// A entidade é montada SEM gravar: quem tenta gravar é o bloco somente-leitura.
	w, err := wallet.NewWallet(
		ids.WalletID(uuid.NewString()), ids.PlayerID(uuid.NewString()), brl(t, "100.00"), time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("NewWallet: %v", err)
	}

	uow := postgresUOW(t)
	err = uow.RunReadOnly(context.Background(), func(ctx context.Context, r ports.Repos) error {
		return r.Wallets.Insert(ctx, w)
	})
	if err == nil {
		t.Fatal("escrita em transação somente-leitura tinha que falhar")
	}

	var carteiras int
	if err := poolTeste.QueryRow(context.Background(), `SELECT count(*) FROM wallets`).Scan(&carteiras); err != nil {
		t.Fatalf("contando carteiras: %v", err)
	}
	if carteiras != 0 {
		t.Fatalf("carteiras = %d, esperado 0", carteiras)
	}
}
