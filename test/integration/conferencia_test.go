//go:build integration

package integration

// Fechamento da Fase 10: a CONFERÊNCIA FINAL do README §13 e a medida de latência
// sob concorrência (diferencial opcional do §14).
//
// A conferência final é a frase que fecha o §13: "Ao final, confira o saldo
// armazenado contra a soma de créditos menos débitos do ledger. Inclua cenários
// que cruzem HTTP e SQS para a mesma operação." Ela é feita aqui de DUAS formas
// independentes, e essa duplicação é o ponto:
//
//  1. em SQL puro, somando o ledger direto da tabela — não passa pelo caso de uso
//     nem pelo domínio, então não pode "concordar" com o saldo por construção;
//  2. pelo endpoint `POST /wallets/{id}/reconciliation`, que é o contrato do §9 e
//     reconstrói o saldo em uma visão consistente dentro de uma transação de
//     leitura.
//
// Se as duas concordassem por engano (por exemplo, as duas usassem o saldo da
// carteira como referência), a segunda não provaria nada. Elas são independentes:
// uma soma linhas do ledger, a outra lê o agregado e soma o ledger de novo, em
// processos diferentes (o SQL no processo de teste, o endpoint numa instância
// real).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/inbox"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
)

// TestConferenciaFinalCruzandoHTTPESQS é a rodada consolidada do §13 com TRÊS
// instâncias: a MESMA operação chega pelas duas portas ao mesmo tempo e, no fim,
// o saldo armazenado é conferido contra a soma do ledger.
//
// # O que a corrida HTTP × SQS tem de especial
//
// HTTP e SQS chamam o MESMO caso de uso com as MESMAS garantias (README §10). A
// prova da Fase 8 fez isso em um processo; aqui a requisição HTTP vai para a
// api-1 e a mensagem é consumida pela api-2 — processos distintos, pools
// distintos, concorrentes de verdade. O que decide quem ganha é o índice único;
// quem chega segundo vira replay (HTTP 200) ou é descartado como repetição pela
// chave financeira (SQS). Em qualquer ordem o débito é UM.
//
// A resposta HTTP aceita 201 ou 200 de propósito: as duas são corretas, e qual
// acontece depende de quem chegou primeiro — que é justamente o que o teste quer
// deixar livre, porque a garantia não é sobre a ordem e sim sobre o resultado.
func TestConferenciaFinalCruzandoHTTPESQS(t *testing.T) {
	limpar(t)

	fila := abrirSQS(t)
	fila.esvaziarFila(t, fila.urlTxn)
	fila.esvaziarFila(t, fila.urlDLQ)
	t.Cleanup(func() {
		fila.drenar(t, fila.urlTxn)
		fila.esvaziarFila(t, fila.urlDLQ)
	})

	api1 := subirInstancias(t, 1, opcoesInstancia{Inicio: 1})[0]
	api2 := subirInstancias(t, 1, opcoesInstancia{Inicio: 2, Consumidor: true})[0]
	api3 := subirInstancias(t, 1, opcoesInstancia{Inicio: 3})[0]

	token, provedor := credencialDeOperacao(t)
	carteira, jogador := abrirCarteiraNaInstancia(t, api1, "1000.00")
	alvo := ids.WalletID(carteira)

	// (1) A MESMA operação pelas DUAS portas, ao mesmo tempo.
	corpoHTTP := corpoDeOperacao(provedor, "txn-cruzado", jogador, carteira, "BET", "25.00")
	corpoFila := mensagemParaOperacao(provedor, "txn-cruzado", provedor+":txn-cruzado",
		jogador, carteira, "round-1", "fortune-chimp", "BET", "25.00")

	var (
		espera   sync.WaitGroup
		opHTTP   operacao
		respHTTP resposta
		errHTTP  error
	)
	largada := make(chan struct{})
	espera.Add(1)
	go func() {
		defer espera.Done()
		<-largada
		opHTTP, respHTTP, errHTTP = tentarEnviarOperacao(api1, token, provedor+":txn-cruzado", corpoHTTP)
	}()
	close(largada)
	messageID := fila.enviar(t, corpoFila, "fase10-cruzado")
	espera.Wait()

	if errHTTP != nil {
		t.Fatalf("HTTP da operação cruzada: %v", errHTTP)
	}
	if respHTTP.status != http.StatusCreated && respHTTP.status != http.StatusOK {
		t.Fatalf("HTTP da operação cruzada = %d, quer 201 (venceu) ou 200 (replay): %s",
			respHTTP.status, respHTTP.texto())
	}
	if opHTTP.Status != "PROCESSED" {
		t.Fatalf("HTTP devolveu status %q, quer PROCESSED", opHTTP.Status)
	}
	if opHTTP.Balance == nil || opHTTP.Balance.Amount != "975.00" {
		t.Fatalf("HTTP devolveu saldo %v, quer 975.00", opHTTP.Balance)
	}

	// A mensagem foi tratada pela api-2 (a inbox registra a entrega).
	esperarAtende(t, "a api-2 tratar a mensagem cruzada", 60*time.Second, func() bool {
		status, _, _, existe := estadoDaInbox(t, messageID)
		return existe && status == "PROCESSED"
	})

	// UM débito, apesar das duas entradas.
	if saldo := saldoNoBanco(t, alvo); saldo != 97500 {
		t.Fatalf("saldo = %d, quer 97500: a mesma operação pelas duas portas debitou duas vezes", saldo)
	}

	// (2) Mais movimentações, cada uma por uma porta e uma instância diferente,
	// para a conferência final ter crédito, débito, LOSS (sem lançamento) e
	// estorno.
	win := corpoDeOperacao(provedor, "txn-final-win", jogador, carteira, "WIN", "10.00")
	if _, resp := enviarOperacao(t, api3, token, provedor+":txn-final-win", win); resp.status != http.StatusCreated {
		t.Fatalf("WIN = %d, quer 201: %s", resp.status, resp.texto())
	}

	loss := corpoDeOperacao(provedor, "txn-final-loss", jogador, carteira, "LOSS", "0.00")
	if op, resp := enviarOperacao(t, api3, token, provedor+":txn-final-loss", loss); resp.status != http.StatusCreated {
		t.Fatalf("LOSS = %d, quer 201: %s (%s)", resp.status, resp.texto(), op.Status)
	}

	// O REFUND do BET cruzado vem pela FILA, para o ledger misturar as duas portas.
	corpoRefund := map[string]any{
		"type":       inbox.TipoEsperado,
		"occurredAt": "2026-09-08T12:00:00.000Z",
		"data": map[string]any{
			"providerId":                     provedor,
			"externalTransactionId":          "txn-cruzado-refund",
			"idempotencyKey":                 provedor + ":txn-cruzado-refund",
			"playerId":                       jogador,
			"walletId":                       carteira,
			"roundId":                        "round-1",
			"gameId":                         "fortune-chimp",
			"kind":                           "REFUND",
			"money":                          map[string]any{"amount": "25.00", "currency": "BRL"},
			"referenceExternalTransactionId": "txn-cruzado",
		},
	}
	brutoRefund, err := json.Marshal(corpoRefund)
	if err != nil {
		t.Fatalf("montando o REFUND da fila: %v", err)
	}

	refundID := fila.enviar(t, string(brutoRefund), "fase10-refund")
	esperarAtende(t, "o REFUND pela fila ser tratado", 60*time.Second, func() bool {
		status, _, _, existe := estadoDaInbox(t, refundID)
		return existe && status == "PROCESSED"
	})

	// (3) A CONFERÊNCIA FINAL, em SQL, sem passar pela aplicação.
	somaSQL, linhas := somaDoLedgerEmSQL(t, alvo)
	saldoGravado := saldoNoBanco(t, alvo)
	if saldoGravado != somaSQL {
		t.Fatalf("saldo armazenado (%d) != soma do ledger em SQL (%d)", saldoGravado, somaSQL)
	}
	if saldoGravado != 101000 {
		t.Fatalf("saldo final = %d, quer 101000 (1000,00 - 25,00 + 10,00 + 25,00)", saldoGravado)
	}
	if linhas != 4 {
		t.Fatalf("lançamentos = %d, quer 4 (abertura, BET, WIN, REFUND); LOSS não produz lançamento", linhas)
	}

	// A versão: abertura, BET, WIN, REFUND = 4. LOSS NÃO incrementa.
	if versao := versaoNoBanco(t, alvo); versao != 4 {
		t.Fatalf("versão = %d, quer 4 (o LOSS não movimenta saldo nem versão)", versao)
	}

	// (4) A MESMA conferência pelo contrato do §9, numa instância que não
	// participou da operação cruzada.
	exigirReconciliacaoConsistente(t, api3, carteira, 4)

	// E as três instâncias continuam atendendo.
	for _, inst := range []*instancia{api1, api2, api3} {
		if !inst.estaPronta(t) {
			t.Fatalf("a instância %s deixou de estar pronta", inst.nome)
		}
	}

	t.Logf("Conferência final: saldo %d == soma do ledger %d em %d lançamentos; "+
		"HTTP (api-1) e SQS (api-2) na MESMA operação -> um único débito; "+
		"reconciliação pela api-3 consistente", saldoGravado, somaSQL, linhas)
}

// TestLatenciaSobConcorrenciaTresInstancias mede p50/p95/p99 e throughput com as
// três instâncias sob carga concorrente.
//
// # O que esta medida é, e o que ela NÃO é
//
// O §14 pede, para testes de carga (diferencial OPCIONAL), "comando reproduzível,
// ambiente, metodologia, throughput, p50/p95/p99, erros, conflitos". Este teste é
// o comando reproduzível; o ambiente e a metodologia estão no cabeçalho e nas
// evidências.
//
// Não é um benchmark: o cliente é o processo de teste, na mesma máquina, falando
// HTTP com três processos que compartilham o PostgreSQL do compose. Os números
// servem para (a) mostrar que a borda responde sob concorrência e (b) comparar
// duas topologias de contenção — carteiras DIFERENTES (paralelo) contra a MESMA
// carteira (serializada pelo `FOR UPDATE`). O que a segunda mede é o custo da
// serialização por carteira: um lock GLOBAL faria a primeira coluna parecer a
// segunda.
func TestLatenciaSobConcorrenciaTresInstancias(t *testing.T) {
	limpar(t)

	instancias := subirInstancias(t, 3, opcoesInstancia{})
	token, provedor := credencialDeOperacao(t)

	const (
		carteiras = 20
		carga     = 100
	)

	abertas := make([]string, carteiras)
	jogadores := make([]string, carteiras)
	for i := 0; i < carteiras; i++ {
		abertas[i], jogadores[i] = abrirCarteiraNaInstancia(t, instancias[i%len(instancias)], "1000.00")
	}

	// Uma carteira só, para a medição serializada.
	unica, jogadorUnico := abrirCarteiraNaInstancia(t, instancias[0], "1000.00")

	// --- cenário A: carteiras DIFERENTES (o paralelismo do §5.6) ---------------
	duracoesA, errosA, inicioA := dispararCarga(t, instancias, token, provedor, carga, func(i int) (string, string, string) {
		indice := i % carteiras
		return abertas[indice], jogadores[indice], fmt.Sprintf("txn-lat-a-%d", i)
	})
	tempoA := time.Since(inicioA)

	// --- cenário B: a MESMA carteira (serializada pelo lock) -------------------
	duracoesB, errosB, inicioB := dispararCarga(t, instancias, token, provedor, carga, func(i int) (string, string, string) {
		return unica, jogadorUnico, fmt.Sprintf("txn-lat-b-%d", i)
	})
	tempoB := time.Since(inicioB)

	if errosA != 0 || errosB != 0 {
		t.Fatalf("erros de transporte ou de status: carteiras distintas=%d, mesma carteira=%d", errosA, errosB)
	}

	// A conferência financeira continua valendo sob carga.
	somaUnica, linhasUnicas := somaDoLedgerEmSQL(t, ids.WalletID(unica))
	if somaUnica != saldoNoBanco(t, ids.WalletID(unica)) {
		t.Fatalf("sob carga a reconciliação da carteira única bateu: soma=%d", somaUnica)
	}
	if linhasUnicas != carga+1 {
		t.Fatalf("lançamentos na carteira única = %d, quer %d (abertura + uma por operação)", linhasUnicas, carga+1)
	}

	t.Logf("latência (n=%d, 3 instâncias, cliente local):\n"+
		"  carteiras DIFERENTES: p50=%s p95=%s p99=%s max=%s total=%s (%.0f ops/s)\n"+
		"  MESMA carteira     : p50=%s p95=%s p99=%s max=%s total=%s (%.0f ops/s)",
		carga,
		p(duracoesA, 50), p(duracoesA, 95), p(duracoesA, 99), maximo(duracoesA), tempoA.Round(time.Millisecond),
		float64(carga)/tempoA.Seconds(),
		p(duracoesB, 50), p(duracoesB, 95), p(duracoesB, 99), maximo(duracoesB), tempoB.Round(time.Millisecond),
		float64(carga)/tempoB.Seconds())
}

// dispararCarga dispara `carga` operações concorrentes, em round-robin pelas
// instâncias, e devolve as durações medidas no CLIENTE.
func dispararCarga(
	t *testing.T,
	instancias []*instancia,
	token, provedor string,
	carga int,
	planejar func(i int) (carteira, jogador, externa string),
) (duracoes []time.Duration, erros int, inicio time.Time) {
	t.Helper()

	duracoes = make([]time.Duration, carga)
	errosPorIndice := make([]bool, carga)

	largada := make(chan struct{})
	var espera sync.WaitGroup

	inicio = time.Now()
	for i := 0; i < carga; i++ {
		espera.Add(1)
		go func(i int) {
			defer espera.Done()
			carteira, jogador, externa := planejar(i)
			corpo := corpoDeOperacao(provedor, externa, jogador, carteira, "BET", "1.00")
			<-largada

			comecou := time.Now()
			op, _, err := tentarEnviarOperacao(instancias[i%len(instancias)], token, provedor+":"+externa, corpo)
			duracoes[i] = time.Since(comecou)
			if err != nil || op.Status != "PROCESSED" {
				errosPorIndice[i] = true
			}
		}(i)
	}
	close(largada)
	espera.Wait()

	for _, falhou := range errosPorIndice {
		if falhou {
			erros++
		}
	}
	return duracoes, erros, inicio
}

// p devolve o percentil pedido de uma amostra.
func p(duracoes []time.Duration, percentil int) time.Duration {
	if len(duracoes) == 0 {
		return 0
	}
	ordenadas := make([]time.Duration, len(duracoes))
	copy(ordenadas, duracoes)
	sort.Slice(ordenadas, func(i, j int) bool { return ordenadas[i] < ordenadas[j] })

	indice := (percentil * len(ordenadas)) / 100
	if indice >= len(ordenadas) {
		indice = len(ordenadas) - 1
	}
	return ordenadas[indice].Round(time.Millisecond)
}

func maximo(duracoes []time.Duration) time.Duration {
	var maior time.Duration
	for _, d := range duracoes {
		if d > maior {
			maior = d
		}
	}
	return maior.Round(time.Millisecond)
}

// somaDoLedgerEmSQL soma o ledger DIRETO da tabela.
//
// `CREDIT` soma, `DEBIT` subtrai — a mesma convenção do domínio, mas escrita em
// SQL aqui de propósito: a conferência final não pode depender do código que ela
// está conferindo.
func somaDoLedgerEmSQL(t *testing.T, carteira ids.WalletID) (soma int64, linhas int) {
	t.Helper()

	err := poolTeste.QueryRow(context.Background(), `
		SELECT coalesce(sum(CASE WHEN direction = 'CREDIT' THEN amount_minor ELSE -amount_minor END), 0),
		       count(*)
		  FROM wallet_ledger_entries
		 WHERE wallet_id = $1`, string(carteira)).Scan(&soma, &linhas)
	if err != nil {
		t.Fatalf("somando o ledger em SQL: %v", err)
	}
	return soma, linhas
}
