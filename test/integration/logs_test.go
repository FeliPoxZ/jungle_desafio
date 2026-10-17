//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestLogsDoProcessoReal prova o que o README §12 pede — e prova do PROCESSO,
// não de um logger construído no teste.
//
// # Por que o processo, e não o objeto
//
// O defeito 27 foi exatamente isto: o processo emitia DOIS formatos no mesmo
// stdout (o boot em JSON e o log de acesso em texto), porque `app.Run` montava um
// handler próprio enquanto o resto do grafo recebia outro. Nenhum teste via,
// porque todos olhavam o objeto `*slog.Logger` que eles mesmos construíam. A
// única forma de provar o formato e a cobertura dos identificadores é subir o
// binário, disparar o caminho real e LER O STDOUT.
//
// # O que este teste faz
//
//  1. sobe o binário com `APP_ENV=production` (JSON) e os três laços ligados;
//  2. exercita os três caminhos que emitem log de negócio: requisição HTTP
//     autenticada, mensagem processada pela fila e resolução de referência pelo
//     worker;
//  3. exige, em cada linha, os identificadores que se aplicam;
//  4. exige a AUSÊNCIA de segredo e de payload financeiro completo;
//  5. repete em `APP_ENV=local` para provar o texto, e não o JSON.
func TestLogsDoProcessoReal(t *testing.T) {
	limpar(t)

	fila := abrirSQS(t)
	fila.esvaziarFila(t, fila.urlTxn)
	fila.esvaziarFila(t, fila.urlDLQ)
	t.Cleanup(func() {
		fila.esvaziarFila(t, fila.urlTxn)
		fila.esvaziarFila(t, fila.urlDLQ)
	})

	// Instância 20 para não colidir com a faixa usada pela Fase 10 (1..N) nem com
	// o teste do binário em `internal/app` (18099).
	inst := subirInstancia(t, 20, opcoesInstancia{
		Consumidor: true,
		Outbox:     true,
		Referencia: true,
		Extras: map[string]string{
			"APP_ENV":   "production",
			"LOG_LEVEL": "info",
		},
	})
	if err := inst.esperarPronta(40 * time.Second); err != nil {
		t.Fatalf("a instância não ficou pronta: %v\nlog:\n%s", err, inst.log.texto())
	}

	tokenInterno := tokenDoKeycloak(t, clientInterno)
	tokenProvedor := tokenDoKeycloak(t, clientProviderA)

	// --- 1. Caminho HTTP: abertura de carteira e BET processada ---------------
	playerID := novoUUID()
	resp := inst.chamar(t, http.MethodPost, "/wallets", tokenInterno,
		corpoDeAbertura(playerID, "1000.00"), nil)
	if resp.status != http.StatusCreated {
		t.Fatalf("POST /wallets = %d, quer 201: %s", resp.status, resp.texto())
	}
	var carteira struct {
		ID string `json:"id"`
	}
	resp.decodifica(t, &carteira)

	bet := corpoDeOperacao("provider-a", "logs-bet-1", playerID, carteira.ID, "BET", "25.00")
	resp = inst.chamar(t, http.MethodPost, "/wagering/transactions", tokenProvedor, bet,
		map[string]string{"Idempotency-Key": "provider-a:logs-bet-1"})
	if resp.status != http.StatusCreated {
		t.Fatalf("BET = %d, quer 201: %s", resp.status, resp.texto())
	}
	var processada struct {
		TransactionID string `json:"transactionId"`
	}
	resp.decodifica(t, &processada)

	// --- 2. Caminho SQS: outra BET, agora pela fila ---------------------------
	corpoDaFila := mensagemParaOperacao("provider-a", "logs-bet-2", "logs-bet-2", playerID,
		carteira.ID, "round-1", "fortune-chimp", "BET", "10.00")
	fila.enviar(t, corpoDaFila, "logs-bet-2")

	// 1000 - 25 - 10 = 965.00. A condição é o efeito durável, não o log: esperar
	// pelo log primeiro e pelo saldo depois inverteria a ordem da prova.
	esperarAtende(t, "a mensagem da fila ser aplicada", 30*time.Second, func() bool {
		return statusDoSaldo(t, carteira.ID) == 96500
	})

	// --- 3. Caminho do worker: REFUND antes da referência, resolvido depois ---
	refund := map[string]any{
		"providerId":            "provider-a",
		"externalTransactionId": "logs-refund-1",
		"playerId":              playerID,
		"walletId":              carteira.ID,
		"roundId":               "round-1",
		"gameId":                "fortune-chimp",
		"kind":                  "REFUND",
		// O valor da reversão é IGUAL ao da referenciada (README §7). A primeira
		// versão deste teste tentou reverter 25.00 sobre uma BET de 5.00 e a
		// retomada — corretamente — devolveu `REFERENCE_MISMATCH`. Régua, não
		// código.
		"money":                          map[string]any{"amount": "5.00", "currency": "BRL"},
		"referenceExternalTransactionId": "logs-bet-referenciada",
	}
	if resp := inst.chamar(t, http.MethodPost, "/wagering/transactions", tokenProvedor, refund,
		map[string]string{"Idempotency-Key": "provider-a:logs-refund-1"}); resp.status != http.StatusAccepted {
		t.Fatalf("REFUND sem referência = %d, quer 202: %s", resp.status, resp.texto())
	}

	// A referência chega.
	referenciada := corpoDeOperacao("provider-a", "logs-bet-referenciada", playerID, carteira.ID, "BET", "5.00")
	if resp := inst.chamar(t, http.MethodPost, "/wagering/transactions", tokenProvedor, referenciada,
		map[string]string{"Idempotency-Key": "provider-a:logs-bet-referenciada"}); resp.status != http.StatusCreated {
		t.Fatalf("BET referenciada = %d, quer 201: %s", resp.status, resp.texto())
	}

	// O intake agendou a retomada para "agora + 5s"; o teste antecipa o instante
	// da LINHA (fixture), e o worker da instância a pega no próximo ciclo.
	if _, err := poolTeste.Exec(context.Background(),
		`UPDATE wager_transactions SET next_resolution_attempt_at = now() - interval '1 second'
		 WHERE idempotency_key = 'provider-a:logs-refund-1'`); err != nil {
		t.Fatalf("antecipando o instante da retomada: %v", err)
	}

	esperarLinhaNoLog(t, inst, "reference_resolvida", 20*time.Second)

	// --- 4. Os identificadores, linha por linha --------------------------------
	linhas := linhasDoLog(t, inst)

	operacao := linhaComMsg(t, linhas, "operacao_concluida")
	for _, campo := range []string{"correlationId", "requestId", "transactionId", "walletId", "providerId"} {
		exigirAtributo(t, operacao, campo)
	}
	if operacao["transactionId"] != processada.TransactionID {
		t.Errorf("transactionId do log = %v, quer %s", operacao["transactionId"], processada.TransactionID)
	}
	if operacao["walletId"] != carteira.ID {
		t.Errorf("walletId do log = %v, quer %s", operacao["walletId"], carteira.ID)
	}

	consumo := linhaComMsg(t, linhas, "sqs_mensagem_processada")
	for _, campo := range []string{"correlationId", "messageId", "transactionId", "walletId", "providerId"} {
		exigirAtributo(t, consumo, campo)
	}

	retomada := linhaComMsg(t, linhas, "reference_resolvida")
	for _, campo := range []string{"correlationId", "transactionId", "walletId", "providerId"} {
		exigirAtributo(t, retomada, campo)
	}

	acesso := linhaComMsg(t, linhas, "http_request")
	for _, campo := range []string{"correlationId", "requestId"} {
		exigirAtributo(t, acesso, campo)
	}

	// --- 5. Nenhum segredo, nenhum payload financeiro completo ----------------
	segredos := []string{
		"provider-service-secret", "client_secret", "Authorization", "Bearer ",
		"access_token", "password",
	}
	for _, linha := range linhasDoLog(t, inst) {
		for _, segredo := range segredos {
			if strings.Contains(linha.original, segredo) {
				t.Errorf("o log do processo contém %q: %s", segredo, linha.original)
			}
		}
		// Payload financeiro COMPLETO: nem o DTO de entrada (amount/money) nem o
		// valor monetário em texto. O que pode entrar é o failureCode, o status e
		// o valor em unidades mínimas do log de divergência.
		for _, chave := range []string{`"amount"`, `"money"`, `"initialBalance"`} {
			if strings.Contains(linha.original, chave) {
				t.Errorf("o log do processo contém o campo financeiro %s: %s", chave, linha.original)
			}
		}
		for _, valor := range []string{"25.00", "10.00", "1000.00", "965.00"} {
			if strings.Contains(linha.original, `"`+valor+`"`) {
				t.Errorf("o log do processo contém o valor monetário %q: %s", valor, linha.original)
			}
		}
	}

	// --- 6. A mesma prova de exposição, agora no processo ----------------------
	resp = inst.chamar(t, http.MethodGet, "/metrics", "", nil, nil)
	if resp.status != http.StatusOK {
		t.Fatalf("GET /metrics do processo = %d, quer 200", resp.status)
	}
	if !strings.Contains(resp.texto(), `jungleg_wager_operations_total{kind="BET",status="PROCESSED"}`) {
		t.Errorf("o /metrics do processo não expõe a operação processada:\n%s", primeiraLinhaCom(resp.texto(), "jungleg_wager_operations_total"))
	}
}

// TestLogsDoProcessoEmModoLocal prova o OUTRO formato: `APP_ENV=local` escreve
// texto legível, e não JSON.
//
// É a outra metade do defeito 27 — o formato é decisão de UM lugar (a config), e
// as duas metades precisam ser observadas no stdout do processo.
func TestLogsDoProcessoEmModoLocal(t *testing.T) {
	limpar(t)

	inst := subirInstancia(t, 21, opcoesInstancia{
		Extras: map[string]string{
			"APP_ENV":   "local",
			"LOG_LEVEL": "info",
		},
	})
	if err := inst.esperarPronta(40 * time.Second); err != nil {
		t.Fatalf("a instância não ficou pronta: %v\nlog:\n%s", err, inst.log.texto())
	}

	tokenInterno := tokenDoKeycloak(t, clientInterno)
	tokenProvedor := tokenDoKeycloak(t, clientProviderA)

	playerID := novoUUID()
	resp := inst.chamar(t, http.MethodPost, "/wallets", tokenInterno,
		corpoDeAbertura(playerID, "100.00"), nil)
	if resp.status != http.StatusCreated {
		t.Fatalf("POST /wallets = %d, quer 201: %s", resp.status, resp.texto())
	}
	var carteira struct {
		ID string `json:"id"`
	}
	resp.decodifica(t, &carteira)

	bet := corpoDeOperacao("provider-a", "local-bet-1", playerID, carteira.ID, "BET", "10.00")
	resp = inst.chamar(t, http.MethodPost, "/wagering/transactions", tokenProvedor, bet,
		map[string]string{"Idempotency-Key": "provider-a:local-bet-1"})
	if resp.status != http.StatusCreated {
		t.Fatalf("BET = %d, quer 201: %s", resp.status, resp.texto())
	}

	esperarLinhaNoLog(t, inst, "operacao_concluida", 20*time.Second)

	texto := inst.log.texto()
	if strings.Contains(texto, `{"time"`) {
		t.Errorf("APP_ENV=local emitiu JSON; o esperado é texto:\n%s", texto)
	}

	achouTexto := false
	for _, linha := range strings.Split(texto, "\n") {
		linha = strings.TrimSpace(linha)
		if linha == "" || !strings.Contains(linha, "operacao_concluida") {
			continue
		}
		// O formato do TextHandler: `time=... level=INFO msg=operacao_concluida ...`
		if !strings.Contains(linha, "level=INFO") || !strings.Contains(linha, "msg=operacao_concluida") {
			t.Errorf("linha em texto fora do formato esperado: %s", linha)
		}
		achouTexto = true
	}
	if !achouTexto {
		t.Fatal("não achei a linha operacao_concluida no stdout do processo em modo local")
	}
}

// --- utilidades -------------------------------------------------------------

// linhaDoLog é uma linha do stdout do processo, crua e interpretada.
type linhaDoLog struct {
	original string
	campos   map[string]any
}

// linhasDoLog interpreta o stdout como JSON e devolve as linhas.
//
// Em produção TODA linha precisa ser JSON: uma linha que não parseia é o próprio
// defeito 27 de volta (dois formatos no mesmo stdout). Por isso a falha é dura.
func linhasDoLog(t *testing.T, inst *instancia) []linhaDoLog {
	t.Helper()

	var linhas []linhaDoLog
	for _, bruta := range strings.Split(inst.log.texto(), "\n") {
		bruta = strings.TrimSpace(bruta)
		if bruta == "" {
			continue
		}
		var campos map[string]any
		if err := json.Unmarshal([]byte(bruta), &campos); err != nil {
			t.Fatalf("linha do stdout não é JSON (%v): %s", err, bruta)
		}
		linhas = append(linhas, linhaDoLog{original: bruta, campos: campos})
	}
	return linhas
}

// linhaComMsg devolve a PRIMEIRA linha com o `msg` pedido.
func linhaComMsg(t *testing.T, linhas []linhaDoLog, msg string) map[string]any {
	t.Helper()

	for _, linha := range linhas {
		if linha.campos["msg"] == msg {
			return linha.campos
		}
	}
	t.Fatalf("o log do processo não tem a linha msg=%q", msg)
	return nil
}

// exigirAtributo falha se o campo não existir ou estiver vazio.
func exigirAtributo(t *testing.T, campos map[string]any, nome string) {
	t.Helper()

	valor, existe := campos[nome]
	if !existe {
		t.Errorf("a linha %v não tem o campo %q", campos["msg"], nome)
		return
	}
	if texto, ok := valor.(string); ok && texto == "" {
		t.Errorf("o campo %q da linha %v está vazio", nome, campos["msg"])
	}
}

// esperarLinhaNoLog espera uma linha com o `msg` aparecer no stdout.
//
// Polling com prazo: o consumo e a retomada são assíncronos, e um `Sleep` fixo
// seria frágil nos dois sentidos (lento ou curto demais).
func esperarLinhaNoLog(t *testing.T, inst *instancia, msg string, prazo time.Duration) {
	t.Helper()

	fim := time.Now().Add(prazo)
	for time.Now().Before(fim) {
		texto := inst.log.texto()
		// Os DOIS formatos: JSON (`"msg":"x"`) e texto (`msg=x`). O helper serve
		// aos dois modos de propósito — em modo local, procurar o JSON nunca
		// acharia nada, e foi assim que este teste falhou na primeira execução.
		if strings.Contains(texto, `"msg":"`+msg+`"`) || strings.Contains(texto, " msg="+msg+" ") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("a linha msg=%q não apareceu no stdout em %v; log:\n%s", msg, prazo, inst.log.texto())
}

// primeiraLinhaCom devolve a primeira linha do texto que contém o trecho.
func primeiraLinhaCom(texto, trecho string) string {
	for _, linha := range strings.Split(texto, "\n") {
		if strings.Contains(linha, trecho) {
			return linha
		}
	}
	return "(nenhuma linha com " + trecho + ")"
}
