//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
)

// reivindicar lê um lote da outbox pelo caminho do repositório — o mesmo que o
// publisher (Fase 9) vai usar.
func reivindicar(t *testing.T, limite int) []ports.OutboxEvent {
	t.Helper()

	var lote []ports.OutboxEvent
	err := postgresUOW(t).Run(context.Background(), func(ctx context.Context, r ports.Repos) error {
		var e error
		lote, e = r.Outbox.ClaimBatch(ctx, limite, time.Minute, time.Now().UTC())
		return e
	})
	if err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	return lote
}

// TestOrdemDosEventosNaOutbox prova, contra o banco real, a ordem de saída da
// fila: os DOIS eventos de uma operação compartilham o `occurred_at`, e é a
// coluna `sequence` que define qual sai primeiro.
func TestOrdemDosEventosNaOutbox(t *testing.T) {
	limpar(t)

	// Abertura (2 eventos) e duas operações na MESMA carteira (2 eventos cada).
	abertura := abrirCarteira(t, "jogador-ordem", "1000.00", "abertura-ordem")
	carteira := carteiraDoBanco(t, abertura.WalletID)
	processar(t, pedido(carteira, wagerdomain.KindBet, "10.00"))
	processar(t, pedido(carteira, wagerdomain.KindWin, "20.00", comChave("key-WIN"), comExterna("ext-WIN")))

	// Confere o cenário do defeito: o par da mesma operação EMPATA no instante.
	var empatados int
	if err := poolTeste.QueryRow(context.Background(),
		`SELECT count(*) FROM (
		     SELECT occurred_at FROM outbox_events GROUP BY occurred_at HAVING count(*) > 1
		 ) AS t`).Scan(&empatados); err != nil {
		t.Fatalf("contando empates: %v", err)
	}
	if empatados == 0 {
		t.Fatal("o cenário do teste exige eventos com o mesmo occurred_at (o par de cada operação)")
	}

	// Reivindica UM por vez, na ordem da fila.
	var (
		tipos      []string
		sequencias []int64
	)
	for i := 0; i < 6; i++ {
		lote := reivindicar(t, 1)
		if len(lote) != 1 {
			t.Fatalf("lote %d veio com %d eventos", i, len(lote))
		}
		tipos = append(tipos, lote[0].EventType)
		sequencias = append(sequencias, lote[0].Sequence)
	}

	esperados := []string{
		wagerdomain.EventTypeProcessed,            // abertura
		wagerdomain.EventTypeWalletBalanceChanged, // abertura
		wagerdomain.EventTypeProcessed,            // BET
		wagerdomain.EventTypeWalletBalanceChanged, // BET
		wagerdomain.EventTypeProcessed,            // WIN
		wagerdomain.EventTypeWalletBalanceChanged, // WIN
	}
	for i := range esperados {
		if tipos[i] != esperados[i] {
			t.Fatalf("ordem = %v, esperado %v", tipos, esperados)
		}
	}
	for i := 1; i < len(sequencias); i++ {
		if sequencias[i] <= sequencias[i-1] {
			t.Fatalf("sequências = %v, esperado estritamente crescente", sequencias)
		}
	}

	// Tudo o que foi reivindicado está sob lease: outro publisher não pega agora.
	if lote := reivindicar(t, 10); len(lote) != 0 {
		t.Fatalf("lote extra = %d eventos, esperado 0 (lease)", len(lote))
	}
}

// TestOrdemDaOutboxNaoDependeDoRelogio é o teste que DISCRIMINA a correção.
//
// O teste anterior passa com `ORDER BY occurred_at` também: em empate, o
// PostgreSQL devolve as linhas na ordem física, que por acaso é a de inserção —
// ou seja, ele NÃO prova nada sobre a correção (foi verificado: 3 execuções, 3
// aprovações com o código antigo).
//
// Aqui a ordem de inserção e a ordem do relógio são OPOSTAS de propósito, que é o
// que acontece se o relógio do processo voltar (NTP, ajuste manual, VM pausada):
//
//	evento A: inserido PRIMEIRO, occurred_at no FUTURO
//	evento B: inserido DEPOIS,   occurred_at no PASSADO
//
// Ordenar por tempo publicaria B antes de A — invertendo a ordem dos efeitos.
// Ordenar por `sequence` mantém A antes de B, que é a ordem em que os fatos
// aconteceram.
func TestOrdemDaOutboxNaoDependeDoRelogio(t *testing.T) {
	limpar(t)

	agora := time.Now().UTC()
	primeiro := inserirEventoBruto(t, "evento-inserido-primeiro", agora.Add(time.Hour))
	segundo := inserirEventoBruto(t, "evento-inserido-depois", agora.Add(-time.Hour))

	if primeiro >= segundo {
		t.Fatalf("cenário inválido: sequence do primeiro = %d, do segundo = %d", primeiro, segundo)
	}

	lote := reivindicar(t, 2)
	if len(lote) != 2 {
		t.Fatalf("lote = %d eventos, esperado 2", len(lote))
	}
	if lote[0].EventType != "evento-inserido-primeiro" || lote[1].EventType != "evento-inserido-depois" {
		t.Fatalf("ordem = [%s, %s], esperado a ordem de INSERÇÃO (%s antes de %s)",
			lote[0].EventType, lote[1].EventType, "evento-inserido-primeiro", "evento-inserido-depois")
	}
}

// inserirEventoBruto grava um evento com `occurred_at` escolhido pelo teste, e
// devolve a sequência atribuída pelo banco.
//
// `available_at` é sempre no PASSADO: o evento está pronto para sair; o que o
// teste manipula é só o instante do fato.
func inserirEventoBruto(t *testing.T, tipo string, ocorridoEm time.Time) int64 {
	t.Helper()

	var sequencia int64
	err := poolTeste.QueryRow(context.Background(),
		`INSERT INTO outbox_events (event_id, aggregate_id, aggregate_type, event_type, payload, occurred_at, available_at)
		 VALUES (gen_random_uuid(), 'carteira-do-teste', 'wallet', $1, '{}'::jsonb, $2, $3)
		 RETURNING sequence`,
		tipo, ocorridoEm, time.Now().UTC().Add(-time.Minute)).Scan(&sequencia)
	if err != nil {
		t.Fatalf("inserindo evento bruto: %v", err)
	}
	return sequencia
}
