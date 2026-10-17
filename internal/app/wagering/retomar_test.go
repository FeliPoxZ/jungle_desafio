package wagering_test

import (
	"context"
	"testing"
	"time"

	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
)

// retomar é o atalho dos testes desta suíte, com a política padrão.
//
// O pedido é montado como o WORKER monta: lendo o estado e a tentativa da linha
// antes de chamar (é o que o claim do worker entrega). Um teste que passasse
// valores "de mentira" aqui não exercitaria a guarda de concorrência de verdade.
func retomar(t *testing.T, a *ambiente, id wagerdomain.TransactionID, pol appwagering.PoliticaDeReferencia) appwagering.Retomada {
	t.Helper()

	if pol.NextAttemptAt.IsZero() {
		pol.NextAttemptAt = a.relogio.Now().Add(30 * time.Second)
	}

	pedido := appwagering.RetomarRequest{
		TransactionID: id,
		Politica:      pol,
	}
	if tx := a.transacaoPorID(id); tx != nil {
		pedido.EstadoEsperado = tx.Status()
		pedido.TentativaEsperada = tx.ReferenceResolutionAttempt()
	}

	retomada, err := a.servico.Retomar(context.Background(), pedido)
	if err != nil {
		t.Fatalf("Retomar devolveu erro: %v", err)
	}
	return retomada
}

// retomarComoWorker monta o pedido com estado/tentativa ANTIGOS: é o caso de um
// worker que reivindicou a linha e, quando chegou a vez dele, outro já tinha
// agido.
func retomarComoWorker(t *testing.T, a *ambiente, id wagerdomain.TransactionID, estado wagerdomain.Status, tentativa int) appwagering.Retomada {
	t.Helper()

	retomada, err := a.servico.Retomar(context.Background(), appwagering.RetomarRequest{
		TransactionID:     id,
		EstadoEsperado:    estado,
		TentativaEsperada: tentativa,
		Politica:          appwagering.PoliticaDeReferencia{MaxAttempts: 20},
	})
	if err != nil {
		t.Fatalf("Retomar devolveu erro: %v", err)
	}
	return retomada
}

// refundPendente monta a situação do README §13.7: o REFUND (reversão) chega
// ANTES da BET que ele referencia.
//
// A carteira fica com 100.00 e o REFUND de 25.00 espera a referência — sem
// movimentar nada, porque não se reverte o que não existe.
func refundPendente(t *testing.T, a *ambiente) (*wallet.Wallet, wagerdomain.TransactionID) {
	t.Helper()

	w := a.abrirCarteira("100.00")
	res := processar(t, a, pedido(w, wagerdomain.KindRefund, "25.00",
		comChave("key-refund"), comExterna("ext-refund"), comReferencia("ext-bet")))

	if res.Status != wagerdomain.StatusPendingReference {
		t.Fatalf("o REFUND sem referência deveria ficar PENDING_REFERENCE, veio %s", res.Status)
	}
	return w, res.TransactionID
}

// chegarABET grava a BET referenciada, processada de verdade.
func chegarABET(t *testing.T, a *ambiente, w *wallet.Wallet) {
	t.Helper()

	res := processar(t, a, pedido(w, wagerdomain.KindBet, "25.00",
		comChave("key-bet"), comExterna("ext-bet")))
	if res.Status != wagerdomain.StatusProcessed {
		t.Fatalf("a BET deveria processar, veio %s (%s)", res.Status, res.FailureCode)
	}
}

// ---------------------------------------------------------------------------
// Resolução
// ---------------------------------------------------------------------------

// README §13.7, primeira metade: a referência chega DEPOIS e a reversão acontece.
func TestRetomadaResolveAReferenciaQueChegou(t *testing.T) {
	a := novoAmbiente()
	w, refundID := refundPendente(t, a)

	// Nada foi movimentado enquanto esperava.
	exigirSaldo(t, a, w, 10000)
	exigirLancamentos(t, a)

	// A BET referenciada chega (e debita 25.00).
	chegarABET(t, a, w)
	exigirSaldo(t, a, w, 7500)

	// O worker de referências passa depois do instante agendado.
	a.relogio.avancar(10 * time.Second)

	retomada := retomar(t, a, refundID, appwagering.PoliticaDeReferencia{MaxAttempts: 5})

	if retomada.Desfecho != appwagering.RetomadaResolvida {
		t.Fatalf("desfecho = %q, esperado resolvida (motivo: %s)", retomada.Desfecho, retomada.Motivo)
	}
	if retomada.Result.Status != wagerdomain.StatusProcessed {
		t.Errorf("status = %s, esperado PROCESSED", retomada.Result.Status)
	}

	// 100.00 - 25.00 (BET) + 25.00 (REFUND) = 100.00
	exigirSaldo(t, a, w, 10000)
	exigirLancamentos(t, a, "DEBIT 25.00", "CREDIT 25.00")

	// A referência INTERNA ficou gravada: é o que impede uma segunda reversão do
	// mesmo tipo.
	if interno := a.transacao("provider-a", "key-refund").ReferenceTransactionID(); interno == "" {
		t.Error("a referência interna resolvida não foi gravada")
	}

	exigirEventos(t, a,
		wagerdomain.EventTypePendingReference, // a pendência, no intake
		wagerdomain.EventTypeProcessed,        // a BET
		wagerdomain.EventTypeWalletBalanceChanged,
		wagerdomain.EventTypeProcessed, // o REFUND, na retomada
		wagerdomain.EventTypeWalletBalanceChanged,
	)
}

// A referência disponível vence o TTL: rejeitar uma reversão cuja referência
// EXISTE seria destruir informação correta para respeitar um relógio.
func TestRetomadaResolveMesmoComOTetoDeTentativasVencido(t *testing.T) {
	a := novoAmbiente()
	w, refundID := refundPendente(t, a)
	chegarABET(t, a, w)

	a.relogio.avancar(time.Hour)

	retomada := retomar(t, a, refundID, appwagering.PoliticaDeReferencia{
		MaxAttempts:   1,
		PendingMaxAge: time.Second,
	})

	if retomada.Desfecho != appwagering.RetomadaResolvida {
		t.Fatalf("desfecho = %q, esperado resolvida", retomada.Desfecho)
	}
	exigirSaldo(t, a, w, 10000)
}

// ---------------------------------------------------------------------------
// Pendência
// ---------------------------------------------------------------------------

// A referência não chegou: a operação continua pendente, com a tentativa
// incrementada e um novo instante agendado — e SEM movimentar nada.
func TestRetomadaSemReferenciaReagendaSemMovimentar(t *testing.T) {
	a := novoAmbiente()
	w, refundID := refundPendente(t, a)

	a.relogio.avancar(10 * time.Second)
	proximo := a.relogio.Now().Add(30 * time.Second)

	retomada := retomar(t, a, refundID, appwagering.PoliticaDeReferencia{
		MaxAttempts:   5,
		NextAttemptAt: proximo,
	})

	if retomada.Desfecho != appwagering.RetomadaPendente {
		t.Fatalf("desfecho = %q, esperado pendente", retomada.Desfecho)
	}
	if retomada.Result.Status != wagerdomain.StatusPendingReference {
		t.Errorf("status = %s, esperado PENDING_REFERENCE", retomada.Result.Status)
	}

	// AUSÊNCIA de movimento: é o que a asserção de saldo e de ledger prova.
	exigirSaldo(t, a, w, 10000)
	exigirLancamentos(t, a)

	tx := a.transacao("provider-a", "key-refund")
	if tx.ReferenceResolutionAttempt() != 2 {
		t.Errorf("tentativa = %d, esperado 2", tx.ReferenceResolutionAttempt())
	}
	if !tx.NextResolutionAttemptAt().Equal(proximo) {
		t.Errorf("próximo instante = %v, esperado %v", tx.NextResolutionAttemptAt(), proximo)
	}

	// Um evento de pendência por rodada: é o que o provedor consome para saber
	// que a operação continua esperando.
	exigirEventos(t, a,
		wagerdomain.EventTypePendingReference,
		wagerdomain.EventTypePendingReference,
	)
}

// O teto de tentativas vencido sem referência vira REJECTED com o código estável
// do README §7.
//
// A tentativa do INTAKE já conta: `MarkPendingReference(1, ...)` grava 1 quando a
// operação entra em espera. Com `MaxAttempts: 1`, a primeira retomada encontra o
// teto consumido — e é isso que o teto significa ("uma tentativa, e só").
func TestRetomadaExpiraEmREFERENCENOTFOUND(t *testing.T) {
	a := novoAmbiente()
	w, refundID := refundPendente(t, a)

	a.relogio.avancar(time.Hour)

	retomada := retomar(t, a, refundID, appwagering.PoliticaDeReferencia{
		MaxAttempts: 1,
	})

	if retomada.Desfecho != appwagering.RetomadaRejeitada {
		t.Fatalf("desfecho = %q, esperado rejeitada", retomada.Desfecho)
	}
	if retomada.Result.FailureCode != appwagering.CodeReferenceNotFound {
		t.Errorf("failureCode = %q, esperado %q", retomada.Result.FailureCode, appwagering.CodeReferenceNotFound)
	}

	// A rejeição por expiração não movimenta dinheiro e deixa o evento.
	exigirSaldo(t, a, w, 10000)
	exigirLancamentos(t, a)
	exigirEventos(t, a,
		wagerdomain.EventTypePendingReference,
		wagerdomain.EventTypeRejected,
	)
}

// O TTL também vence por IDADE, mesmo com tentativas sobrando.
func TestRetomadaExpiraPelaIdadeDaPendencia(t *testing.T) {
	a := novoAmbiente()
	_, refundID := refundPendente(t, a)

	a.relogio.avancar(2 * time.Hour)

	retomada := retomar(t, a, refundID, appwagering.PoliticaDeReferencia{
		MaxAttempts:   100,
		PendingMaxAge: time.Minute,
	})

	if retomada.Desfecho != appwagering.RetomadaRejeitada {
		t.Fatalf("desfecho = %q, esperado rejeitada por idade", retomada.Desfecho)
	}
	if retomada.Result.FailureCode != appwagering.CodeReferenceNotFound {
		t.Errorf("failureCode = %q", retomada.Result.FailureCode)
	}
}

// ---------------------------------------------------------------------------
// Guardas de concorrência
// ---------------------------------------------------------------------------

// transacaoPorID procura a transação no estado em memória.
func (a *ambiente) transacaoPorID(id wagerdomain.TransactionID) *wagerdomain.WagerTransaction {
	for _, t := range a.estado.txns {
		if t.ID() == id {
			return t
		}
	}
	return nil
}

// Dois workers sobre a mesma linha: o segundo chega com o estado/tentativa que o
// claim dele leu, e precisa ser IGNORADO — senão o TTL é consumido duas vezes
// mais rápido.
func TestRetomadaIgnoraQuandoOutroWorkerJaAgiu(t *testing.T) {
	a := novoAmbiente()
	_, refundID := refundPendente(t, a)
	a.relogio.avancar(10 * time.Second)

	// O primeiro worker age: reagenda (tentativa 1 -> 2).
	primeira := retomar(t, a, refundID, appwagering.PoliticaDeReferencia{MaxAttempts: 20})
	if primeira.Desfecho != appwagering.RetomadaPendente {
		t.Fatalf("a primeira retomada deveria reagendar, veio %q", primeira.Desfecho)
	}

	// O segundo worker tem a visão ANTIGA da linha (estado PENDING_REFERENCE,
	// tentativa 1), como se tivesse reivindicado antes do primeiro comitar.
	segunda := retomarComoWorker(t, a, refundID, wagerdomain.StatusPendingReference, 1)

	if segunda.Desfecho != appwagering.RetomadaIgnorada {
		t.Fatalf("desfecho = %q, esperado ignorada (outro worker já agiu)", segunda.Desfecho)
	}

	tx := a.transacaoPorID(refundID)
	if tx.ReferenceResolutionAttempt() != 2 {
		t.Errorf("tentativa = %d, esperado 2: o segundo worker NÃO pode consumir outra tentativa",
			tx.ReferenceResolutionAttempt())
	}
}

// O estado esperado mudou (o reaper pegou um PENDING que já virou
// PENDING_REFERENCE): ignorar.
func TestRetomadaIgnoraQuandoOEstadoEsperadoMudou(t *testing.T) {
	a := novoAmbiente()
	_, refundID := refundPendente(t, a)
	a.relogio.avancar(10 * time.Second)

	// O worker"acha" que a linha está em PENDING (visão de um claim antigo).
	retomada := retomarComoWorker(t, a, refundID, wagerdomain.StatusPending, 0)

	if retomada.Desfecho != appwagering.RetomadaIgnorada {
		t.Fatalf("desfecho = %q, esperado ignorada", retomada.Desfecho)
	}
	if tx := a.transacaoPorID(refundID); tx.Status() != wagerdomain.StatusPendingReference {
		t.Errorf("o estado não podia mudar: veio %s", tx.Status())
	}
	if tx := a.transacaoPorID(refundID); tx.ReferenceResolutionAttempt() != 1 {
		t.Errorf("a tentativa não podia mudar: veio %d", tx.ReferenceResolutionAttempt())
	}
	exigirEventos(t, a, wagerdomain.EventTypePendingReference)
}

// Estado terminal: outra instância concluiu entre o claim e o lock.
func TestRetomadaIgnoraTransacaoTerminal(t *testing.T) {
	a := novoAmbiente()
	w := a.abrirCarteira("100.00")
	res := processar(t, a, pedido(w, wagerdomain.KindBet, "25.00", comChave("key-1")))

	eventosAntes := len(a.tiposDeEvento())

	retomada := retomar(t, a, res.TransactionID, appwagering.PoliticaDeReferencia{MaxAttempts: 5})

	if retomada.Desfecho != appwagering.RetomadaIgnorada {
		t.Fatalf("desfecho = %q, esperado ignorada", retomada.Desfecho)
	}
	if retomada.Result.Status != wagerdomain.StatusProcessed {
		t.Errorf("status devolvido = %s, esperado o estado terminal PROCESSED", retomada.Result.Status)
	}
	if len(a.tiposDeEvento()) != eventosAntes {
		t.Error("retomada de transação terminal não pode emitir evento")
	}
	exigirSaldo(t, a, w, 7500)
}

// Transação que não existe: nada a retomar, e não é erro.
func TestRetomadaDeTransacaoInexistenteEIgnorada(t *testing.T) {
	a := novoAmbiente()

	retomada := retomar(t, a, "00000000-0000-7000-8000-000000009999", appwagering.PoliticaDeReferencia{})

	if retomada.Desfecho != appwagering.RetomadaIgnorada {
		t.Fatalf("desfecho = %q, esperado ignorada", retomada.Desfecho)
	}
}

// A referência existe e não serve: a retomada rejeita, com o código da régua.
func TestRetomadaRejeitaReferenciaIncompativel(t *testing.T) {
	a := novoAmbiente()
	_, refundID := refundPendente(t, a)

	// A BET referenciada chega em OUTRA carteira: a régua de concordância recusa.
	outra := a.abrirCarteira("500.00")
	processar(t, a, pedido(outra, wagerdomain.KindBet, "25.00",
		comChave("key-bet-outra"), comExterna("ext-bet")))

	a.relogio.avancar(10 * time.Second)

	retomada := retomar(t, a, refundID, appwagering.PoliticaDeReferencia{MaxAttempts: 5})

	if retomada.Desfecho != appwagering.RetomadaRejeitada {
		t.Fatalf("desfecho = %q, esperado rejeitada", retomada.Desfecho)
	}
	if retomada.Result.FailureCode != appwagering.CodeReferenceMismatch {
		t.Errorf("failureCode = %q, esperado %q", retomada.Result.FailureCode, appwagering.CodeReferenceMismatch)
	}
}
