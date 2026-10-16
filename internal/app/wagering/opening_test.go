package wagering_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ledger"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
)

func abertura(player string, saldo string, chave string) appwagering.OpenWalletRequest {
	return appwagering.OpenWalletRequest{
		PlayerID:       ids.PlayerID(player),
		InitialBalance: brl(saldo),
		IdempotencyKey: chave,
	}
}

func abrir(t *testing.T, a *ambiente, req appwagering.OpenWalletRequest) appwagering.OpenWalletResult {
	t.Helper()

	res, err := a.servico.OpenWallet(context.Background(), req)
	if err != nil {
		t.Fatalf("OpenWallet devolveu erro: %v", err)
	}
	return res
}

// card do teste de abertura: a carteira do estado em memória.
func carteiraDe(t *testing.T, a *ambiente, id ids.WalletID) (saldo int64, versao int64) {
	t.Helper()

	w, ok := a.estado.wallets[id]
	if !ok {
		t.Fatalf("carteira %s não existe no estado", id)
	}
	return w.Balance().Minor(), w.Version()
}

func TestOpenWalletComSaldoCriaOpeningLedgerEDoisEventos(t *testing.T) {
	a := novoAmbiente()

	res := abrir(t, a, abertura("jogador-1", "1000.00", "abertura-1"))

	// A carteira nasce com o saldo inicial e versão 1 — o crédito de abertura NÃO
	// é uma movimentação, e por isso não dobra o saldo nem leva a versão a 2.
	if saldo, versao := carteiraDe(t, a, res.WalletID); saldo != 100000 || versao != 1 {
		t.Fatalf("carteira = saldo %d, versão %d; esperado 100000 e 1", saldo, versao)
	}
	if res.Version != 1 || res.Balance.Minor() != 100000 {
		t.Fatalf("resultado = %+v", res)
	}
	if res.IdempotentReplay {
		t.Fatal("primeira abertura não é replay")
	}

	// Um lançamento de crédito, partindo de ZERO e chegando no saldo inicial.
	if len(a.estado.ledger) != 1 {
		t.Fatalf("lançamentos = %d, esperado 1", len(a.estado.ledger))
	}
	entrada := a.estado.ledger[0]
	if entrada.Direction() != ledger.DirectionCredit || entrada.Amount().Minor() != 100000 {
		t.Fatalf("lançamento = %s %s", entrada.Direction(), entrada.Amount())
	}
	if entrada.BalanceBefore().Minor() != 0 || entrada.BalanceAfter().Minor() != 100000 {
		t.Fatalf("saldo do lançamento = %s → %s, esperado 0.00 → 1000.00",
			entrada.BalanceBefore(), entrada.BalanceAfter())
	}
	if entrada.TransactionID() != res.OpeningTransactionID {
		t.Fatal("o lançamento precisa apontar para a OPENING")
	}

	// Dois eventos: conclusão e mudança de saldo.
	exigirEventos(t, a, wagerdomain.EventTypeProcessed, wagerdomain.EventTypeWalletBalanceChanged)

	// E a OPENING ficou concluída, com o saldo resultante gravado para o replay.
	opening := a.estado.txns[0]
	if opening.Kind() != wagerdomain.KindOpening || opening.Status() != wagerdomain.StatusProcessed {
		t.Fatalf("OPENING = %s %s", opening.Kind(), opening.Status())
	}
	if !opening.HasResultBalance() || opening.ResultBalance().Minor() != 100000 {
		t.Fatal("a OPENING precisa gravar o saldo resultante")
	}
	if opening.GameID() != "" || opening.RoundID() != "" || opening.ProviderID() != "" {
		t.Fatalf("OPENING não pertence a jogo, rodada nem provedor: %q %q %q",
			opening.GameID(), opening.RoundID(), opening.ProviderID())
	}

	// O evento de saldo carrega a versão 1, que é a versão da carteira na
	// abertura (README §9).
	var mudanca *ports.OutboxEvent
	for i := range a.estado.outbox {
		if a.estado.outbox[i].EventType == wagerdomain.EventTypeWalletBalanceChanged {
			mudanca = &a.estado.outbox[i]
		}
	}
	if mudanca == nil {
		t.Fatal("faltou o evento de mudança de saldo")
	}
	if !strings.Contains(string(mudanca.Payload), `"walletVersion":1`) {
		t.Fatalf("payload do evento = %s", mudanca.Payload)
	}
	if !strings.Contains(string(mudanca.Payload), `"amount":"1000.00"`) {
		t.Fatalf("payload do evento = %s", mudanca.Payload)
	}
}

func TestOpenWalletComSaldoZeroNaoCriaOpening(t *testing.T) {
	a := novoAmbiente()

	res := abrir(t, a, abertura("jogador-1", "0.00", "abertura-zero"))

	if saldo, versao := carteiraDe(t, a, res.WalletID); saldo != 0 || versao != 1 {
		t.Fatalf("carteira = saldo %d, versão %d", saldo, versao)
	}
	if res.OpeningTransactionID != "" {
		t.Fatal("saldo zero não cria OPENING")
	}
	if len(a.estado.txns) != 0 {
		t.Fatalf("transações = %d, esperado 0", len(a.estado.txns))
	}
	if len(a.estado.ledger) != 0 || len(a.estado.outbox) != 0 {
		t.Fatalf("ledger = %d, eventos = %d; esperado 0 e 0", len(a.estado.ledger), len(a.estado.outbox))
	}
}

func TestOpenWalletDuplicadaEConflito(t *testing.T) {
	a := novoAmbiente()
	abrir(t, a, abertura("jogador-1", "1000.00", "abertura-1"))

	// Outra abertura para o mesmo (jogador, moeda), com OUTRA chave.
	_, err := a.servico.OpenWallet(context.Background(), abertura("jogador-1", "500.00", "abertura-2"))
	if err == nil {
		t.Fatal("esperava conflito de carteira duplicada")
	}
	if !errors.Is(err, appwagering.ErrWalletAlreadyExists) {
		t.Fatalf("erro = %v, esperado conflito de carteira", err)
	}
	exigirCodigo(t, err, appwagering.CodeWalletConflict)

	if len(a.estado.wallets) != 1 {
		t.Fatalf("carteiras = %d, esperado 1", len(a.estado.wallets))
	}
}

func TestOpenWalletMesmaChaveConteudoDiferenteEConflito(t *testing.T) {
	a := novoAmbiente()
	abrir(t, a, abertura("jogador-1", "1000.00", "abertura-1"))

	_, err := a.servico.OpenWallet(context.Background(), abertura("jogador-1", "700.00", "abertura-1"))
	if !errors.Is(err, appwagering.ErrIdempotencyConflict) {
		t.Fatalf("erro = %v, esperado conflito de idempotência", err)
	}
	if len(a.estado.wallets) != 1 || len(a.estado.ledger) != 1 {
		t.Fatal("conflito não pode criar carteira nem lançamento")
	}
}

func TestOpenWalletReplayDevolveACarteiraComoElaEsta(t *testing.T) {
	a := novoAmbiente()
	primeira := abrir(t, a, abertura("jogador-1", "1000.00", "abertura-1"))

	// A carteira muda DEPOIS da abertura.
	carteira := a.estado.wallets[primeira.WalletID]
	processar(t, a, pedido(carteira, wagerdomain.KindBet, "25.00"))

	replay := abrir(t, a, abertura("jogador-1", "1000.00", "abertura-1"))

	if !replay.IdempotentReplay {
		t.Fatal("mesma chave e mesmo conteúdo é replay")
	}
	if replay.WalletID != primeira.WalletID {
		t.Fatalf("replay devolveu outra carteira: %s e %s", replay.WalletID, primeira.WalletID)
	}
	// A carteira é um RECURSO, e o replay devolve o estado atual dela: 975.00 e
	// versão 2 (a aposta debitou 25.00 depois da abertura).
	if replay.Balance.Minor() != 97500 || replay.Version != 2 {
		t.Fatalf("replay = saldo %s, versão %d; esperado 975.00 e 2", replay.Balance, replay.Version)
	}
	if len(a.estado.wallets) != 1 {
		t.Fatalf("carteiras = %d, esperado 1", len(a.estado.wallets))
	}
	if len(a.estado.ledger) != 2 {
		t.Fatalf("lançamentos = %d, esperado 2 (abertura e aposta)", len(a.estado.ledger))
	}
}

func TestOpenWalletEntradaInvalidaNaoCriaNada(t *testing.T) {
	casos := []struct {
		nome     string
		montar   func() appwagering.OpenWalletRequest
		esperado string
	}{
		{
			"sem chave",
			func() appwagering.OpenWalletRequest { return abertura("jogador-1", "1000.00", "") },
			appwagering.CodeIdempotencyKeyRequired,
		},
		{
			"sem jogador",
			func() appwagering.OpenWalletRequest { return abertura("", "1000.00", "k") },
			appwagering.CodeInvalidRequest,
		},
		{
			"saldo negativo",
			func() appwagering.OpenWalletRequest {
				req := abertura("jogador-1", "0.00", "k")
				req.InitialBalance = money.FromMinor(-100, money.BRL)
				return req
			},
			appwagering.CodeInvalidRequest,
		},
		{
			"moeda inválida",
			func() appwagering.OpenWalletRequest {
				req := abertura("jogador-1", "0.00", "k")
				req.InitialBalance = money.FromMinor(100, money.Currency("xx"))
				return req
			},
			money.CodeInvalidCurrency,
		},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			a := novoAmbiente()

			_, err := a.servico.OpenWallet(context.Background(), caso.montar())
			if err == nil {
				t.Fatal("esperava erro de entrada")
			}
			exigirCodigo(t, err, caso.esperado)

			if len(a.estado.wallets) != 0 || len(a.estado.txns) != 0 || len(a.estado.ledger) != 0 {
				t.Fatal("entrada inválida não cria carteira, transação nem lançamento")
			}
		})
	}
}

func TestOpenWalletFalhaNaOutboxNaoDeixaNemCarteira(t *testing.T) {
	a := novoAmbiente()
	a.uow.falhaOutbox = errors.New("outbox indisponível")

	_, err := a.servico.OpenWallet(context.Background(), abertura("jogador-1", "1000.00", "abertura-1"))
	if err == nil {
		t.Fatal("esperava erro da outbox")
	}

	// A carteira, a OPENING e o lançamento entram no MESMO commit: sem o evento,
	// nada fica.
	if len(a.estado.wallets) != 0 {
		t.Fatal("a carteira não pode ficar sem o registro da abertura")
	}
	if len(a.estado.txns) != 0 || len(a.estado.ledger) != 0 || len(a.estado.outbox) != 0 {
		t.Fatal("nada pode ficar gravado pela metade")
	}
}
