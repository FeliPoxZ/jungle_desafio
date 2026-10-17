//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
	"github.com/FeliPoxZ/jungle_desafio/internal/platform/clock"
	"github.com/FeliPoxZ/jungle_desafio/internal/platform/idgen"
)

func abrirCarteira(t *testing.T, player string, saldo string, chave string) appwagering.OpenWalletResult {
	t.Helper()

	res, err := servico.OpenWallet(context.Background(), appwagering.OpenWalletRequest{
		PlayerID:       ids.PlayerID(player),
		InitialBalance: brl(t, saldo),
		IdempotencyKey: chave,
	})
	if err != nil {
		t.Fatalf("OpenWallet devolveu erro: %v", err)
	}
	return res
}

// openingNoBanco lê a linha da OPENING pela chave de idempotência.
type openingNoBanco struct {
	ID          string
	Kind        string
	Status      string
	GameID      string
	RoundID     string
	ProviderID  string
	Resultado   int64
	TemSaldoRes bool
}

func lerOpening(t *testing.T, chave string) openingNoBanco {
	t.Helper()

	var (
		o         openingNoBanco
		resultado *int64
	)
	err := poolTeste.QueryRow(context.Background(),
		`SELECT id, kind, status, COALESCE(game_id, ''), COALESCE(round_id, ''),
		        COALESCE(provider_id, ''), result_balance_minor
		 FROM wager_transactions WHERE kind = 'OPENING' AND idempotency_key = $1`, chave).
		Scan(&o.ID, &o.Kind, &o.Status, &o.GameID, &o.RoundID, &o.ProviderID, &resultado)
	if err != nil {
		t.Fatalf("lendo a OPENING: %v", err)
	}

	if resultado != nil {
		o.Resultado = *resultado
		o.TemSaldoRes = true
	}
	return o
}

// lerLancamento devolve direção, valor e os dois saldos do único lançamento da
// carteira.
func lerLancamento(t *testing.T, walletID ids.WalletID) (direcao string, valor, antes, depois int64) {
	t.Helper()

	err := poolTeste.QueryRow(context.Background(),
		`SELECT direction, amount_minor, balance_before_minor, balance_after_minor
		 FROM wallet_ledger_entries WHERE wallet_id = $1`, string(walletID)).
		Scan(&direcao, &valor, &antes, &depois)
	if err != nil {
		t.Fatalf("lendo o lançamento: %v", err)
	}
	return direcao, valor, antes, depois
}

func TestOpenWalletGravaCarteiraOpeningLedgerEEventos(t *testing.T) {
	limpar(t)

	res := abrirCarteira(t, "jogador-abertura", "1000.00", "abertura-1")

	// A carteira nasce com o saldo inicial e VERSÃO 1: o crédito de abertura não
	// é movimentação, então não dobra o saldo nem leva a versão a 2 (README §9).
	if saldo := saldoNoBanco(t, res.WalletID); saldo != 100000 {
		t.Fatalf("saldo gravado = %d, esperado 100000", saldo)
	}
	if versao := versaoNoBanco(t, res.WalletID); versao != 1 {
		t.Fatalf("versão gravada = %d, esperado 1", versao)
	}

	// O lançamento de abertura parte de ZERO e chega no saldo inicial.
	direcao, valor, antes, depois := lerLancamento(t, res.WalletID)
	if direcao != "CREDIT" || valor != 100000 {
		t.Fatalf("lançamento = %s %d", direcao, valor)
	}
	if antes != 0 || depois != 100000 {
		t.Fatalf("saldo do lançamento = %d → %d, esperado 0 → 100000", antes, depois)
	}

	// A OPENING está concluída, sem jogo, rodada nem provedor.
	opening := lerOpening(t, "abertura-1")
	if opening.Kind != "OPENING" || opening.Status != "PROCESSED" {
		t.Fatalf("OPENING = %s %s", opening.Kind, opening.Status)
	}
	if opening.GameID != "" || opening.RoundID != "" || opening.ProviderID != "" {
		t.Fatalf("OPENING com jogo/rodada/provedor: %q %q %q", opening.GameID, opening.RoundID, opening.ProviderID)
	}
	if !opening.TemSaldoRes || opening.Resultado != 100000 {
		t.Fatalf("saldo resultante da OPENING = %v", opening.Resultado)
	}

	// Dois eventos na MESMA transação, com a versão 1 no evento de saldo.
	eventos := eventosNaOutbox(t)
	if len(eventos) != 2 {
		t.Fatalf("eventos = %v, esperado 2", tiposDeEvento(t))
	}
	if eventos[0].Tipo != wagerdomain.EventTypeProcessed || eventos[1].Tipo != wagerdomain.EventTypeWalletBalanceChanged {
		t.Fatalf("tipos = %v", tiposDeEvento(t))
	}

	// O payload é conferido como JSON, e não por busca de texto: a coluna é
	// JSONB, e o PostgreSQL reformata o documento (espaço depois dos
	// dois-pontos). Afirmar sobre o VALOR não depende da formatação.
	var envelope struct {
		Version int `json:"version"`
		Data    struct {
			WalletVersion int64 `json:"walletVersion"`
			BalanceBefore struct {
				Amount string `json:"amount"`
			} `json:"balanceBefore"`
			BalanceAfter struct {
				Amount string `json:"amount"`
			} `json:"balanceAfter"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(eventos[1].Payload), &envelope); err != nil {
		t.Fatalf("payload do evento não é JSON: %v", err)
	}
	if envelope.Version != wagerdomain.EventVersion {
		t.Fatalf("versão do envelope = %d", envelope.Version)
	}
	if envelope.Data.WalletVersion != 1 {
		t.Fatalf("walletVersion = %d, esperado 1", envelope.Data.WalletVersion)
	}
	if envelope.Data.BalanceBefore.Amount != "0.00" || envelope.Data.BalanceAfter.Amount != "1000.00" {
		t.Fatalf("saldos do evento = %s → %s", envelope.Data.BalanceBefore.Amount, envelope.Data.BalanceAfter.Amount)
	}

	// A soma do ledger é exatamente o saldo armazenado — é isso que a
	// reconciliação do README vai conferir.
	soma, err := somaDoLedger(res.WalletID)
	if err != nil {
		t.Fatalf("somando o ledger: %v", err)
	}
	if soma != saldoNoBanco(t, res.WalletID) {
		t.Fatalf("soma do ledger = %d, saldo = %d", soma, saldoNoBanco(t, res.WalletID))
	}
}

func TestOpenWalletComSaldoZeroNaoCriaOpening(t *testing.T) {
	limpar(t)

	res := abrirCarteira(t, "jogador-zero", "0.00", "abertura-zero")

	if saldo := saldoNoBanco(t, res.WalletID); saldo != 0 {
		t.Fatalf("saldo = %d, esperado 0", saldo)
	}
	if res.OpeningTransactionID != "" {
		t.Fatal("saldo zero não cria OPENING")
	}
	if total := contarTransacoes(t); total != 0 {
		t.Fatalf("transações = %d, esperado 0", total)
	}
	if eventos := eventosNaOutbox(t); len(eventos) != 0 {
		t.Fatalf("eventos = %v, esperado nenhum", tiposDeEvento(t))
	}
	if linhas := lancamentosNoBanco(t, res.WalletID); len(linhas) != 0 {
		t.Fatalf("ledger = %v, esperado vazio", linhas)
	}
}

func TestOpenWalletDuplicadaEConflito(t *testing.T) {
	limpar(t)
	abrirCarteira(t, "jogador-dup", "1000.00", "abertura-1")

	_, err := servico.OpenWallet(context.Background(), appwagering.OpenWalletRequest{
		PlayerID:       ids.PlayerID("jogador-dup"),
		InitialBalance: brl(t, "500.00"),
		IdempotencyKey: "abertura-2",
	})
	if !errors.Is(err, appwagering.ErrWalletAlreadyExists) {
		t.Fatalf("erro = %v, esperado conflito de carteira duplicada", err)
	}

	var carteiras int
	if err := poolTeste.QueryRow(context.Background(), `SELECT count(*) FROM wallets`).Scan(&carteiras); err != nil {
		t.Fatalf("contando carteiras: %v", err)
	}
	if carteiras != 1 {
		t.Fatalf("carteiras = %d, esperado 1", carteiras)
	}
}

func TestOpenWalletIdempotenteNaoDuplicaNada(t *testing.T) {
	limpar(t)

	primeira := abrirCarteira(t, "jogador-idem", "1000.00", "abertura-1")
	replay := abrirCarteira(t, "jogador-idem", "1000.00", "abertura-1")

	if !replay.IdempotentReplay || replay.WalletID != primeira.WalletID {
		t.Fatalf("replay = %+v", replay)
	}
	if total := contarTransacoes(t); total != 1 {
		t.Fatalf("transações = %d, esperado 1", total)
	}
	if linhas := lancamentosNoBanco(t, primeira.WalletID); len(linhas) != 1 {
		t.Fatalf("ledger = %v, esperado 1 lançamento", linhas)
	}
	if eventos := eventosNaOutbox(t); len(eventos) != 2 {
		t.Fatalf("eventos = %d, esperado 2", len(eventos))
	}
}

func TestOpenWalletComOutboxQuebradaNaoDeixaNemCarteira(t *testing.T) {
	limpar(t)

	quebrado := appwagering.New(uowComOutboxQuebrada{interno: postgresUOW(t)}, clock.System{}, idgen.New(), nil)
	_, err := quebrado.OpenWallet(context.Background(), appwagering.OpenWalletRequest{
		PlayerID:       ids.PlayerID("jogador-quebrado"),
		InitialBalance: brl(t, "1000.00"),
		IdempotencyKey: "abertura-quebrada",
	})
	if err == nil {
		t.Fatal("esperava erro da outbox")
	}

	// Carteira, OPENING, lançamento e eventos entram no MESMO commit: sem o
	// evento, nada fica — nem a carteira.
	var carteiras int
	if err := poolTeste.QueryRow(context.Background(), `SELECT count(*) FROM wallets`).Scan(&carteiras); err != nil {
		t.Fatalf("contando carteiras: %v", err)
	}
	if carteiras != 0 || contarTransacoes(t) != 0 {
		t.Fatalf("carteiras = %d, transações = %d; esperado 0 e 0", carteiras, contarTransacoes(t))
	}
}

// TestAberturaMaisApostaFechaComOExemploDoREADME monta o cenário da
// reconciliação do enunciado: abertura de 1000.00 e uma aposta de 25.00.
func TestAberturaMaisApostaFechaComOExemploDoREADME(t *testing.T) {
	limpar(t)

	abertura := abrirCarteira(t, "jogador-readme", "1000.00", "abertura-readme")
	carteira := carteiraDoBanco(t, abertura.WalletID)

	processar(t, pedido(carteira, wagerdomain.KindBet, "25.00"))

	if saldo := saldoNoBanco(t, abertura.WalletID); saldo != 97500 {
		t.Fatalf("saldo = %d, esperado 97500", saldo)
	}
	soma, err := somaDoLedger(abertura.WalletID)
	if err != nil {
		t.Fatalf("somando o ledger: %v", err)
	}
	if soma != 97500 {
		t.Fatalf("soma do ledger = %d, esperado 97500", soma)
	}
	if linhas := lancamentosNoBanco(t, abertura.WalletID); len(linhas) != 2 {
		t.Fatalf("lançamentos = %v, esperado 2 (abertura e aposta)", linhas)
	}
}

// somaDoLedger soma os lançamentos com sinal, direto do banco.
func somaDoLedger(walletID ids.WalletID) (int64, error) {
	var soma int64
	err := poolTeste.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(CASE WHEN direction = 'DEBIT' THEN -amount_minor ELSE amount_minor END), 0)
		 FROM wallet_ledger_entries WHERE wallet_id = $1`, string(walletID)).Scan(&soma)
	if err != nil {
		return 0, err
	}
	return soma, nil
}

// carteiraDoBanco relê a carteira e a reidrata no domínio, para usá-la como
// entrada de uma operação.
func carteiraDoBanco(t *testing.T, id ids.WalletID) *wallet.Wallet {
	t.Helper()

	var (
		player string
		saldo  int64
		versao int64
	)
	err := poolTeste.QueryRow(context.Background(),
		`SELECT player_id, balance_minor, version FROM wallets WHERE id = $1`, string(id)).
		Scan(&player, &saldo, &versao)
	if err != nil {
		t.Fatalf("lendo a carteira: %v", err)
	}

	// Instantes zerados: a reidratação não valida e a operação só usa identidade,
	// dono e saldo.
	return wallet.Rehydrate(id, ids.PlayerID(player), money.FromMinor(saldo, money.BRL), versao, time.Time{}, time.Time{})
}
