//go:build integration

package integration

// T1, T2 e T3 do README §13, com TRÊS PROCESSOS INDEPENDENTES.
//
// A diferença em relação às provas das fases anteriores não é de cenário, é de
// topologia: em `wagering_service_test.go` (Fase 6) os cenários rodam em
// goroutines do MESMO processo, com o MESMO pool e o MESMO `Service` em memória;
// aqui cada aposta sai por um processo diferente, com o seu pool, a sua memória e
// a sua conexão TCP. É o que o README §8 cobra: "as garantias devem ser
// demonstradas com pelo menos três processos independentes, cada um com suas
// próprias conexões e memória".
//
// O que os três cenários têm em comum, e por que isso é o coração da nota de
// concorrência (§14, 20 pontos): nenhum deles depende de ordenação, de sorte ou
// de `sleep`. A simultaneidade vem de uma BARREIRA (um canal que todos esperam
// fechado) e a observação é sempre do estado DURÁVEL no PostgreSQL — nunca do
// que a memória de um processo acha que aconteceu.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
	"github.com/FeliPoxZ/jungle_desafio/internal/httpapi"
)

// ---------------------------------------------------------------------------
// Ajuda comum aos cenários
// ---------------------------------------------------------------------------

// credencialDeOperacao é o par (token, providerId) que a borda autoriza.
//
// O `providerId` vem do TOKEN, nunca do corpo (README §2). O corpo repete o mesmo
// valor porque o contrato do §9 o exige — e a borda recusa divergência.
func credencialDeOperacao(t *testing.T) (token, providerID string) {
	t.Helper()
	return tokenDoKeycloak(t, clientProviderA), "provider-a"
}

// abrirCarteiraNaInstancia abre a carteira pelo CAMINHO REAL (`POST /wallets`),
// com o token interno.
//
// Abrir pela regra, e não por INSERT, é o que deixa o lançamento de abertura e os
// dois eventos na outbox — sem eles as asserções de "quantos eventos" e "quantos
// lançamentos" ficariam erradas por um motivo que não é o do teste.
func abrirCarteiraNaInstancia(t *testing.T, inst *instancia, saldo string) (walletID, playerID string) {
	t.Helper()

	playerID = novoUUID()
	resp := inst.chamar(t, http.MethodPost, "/wallets", tokenDoKeycloak(t, clientInterno),
		corpoDeAbertura(playerID, saldo), nil)
	if resp.status != http.StatusCreated {
		t.Fatalf("%s: POST /wallets = %d, quer 201: %s", inst.nome, resp.status, resp.texto())
	}

	var corpo struct {
		ID       string `json:"id"`
		PlayerID string `json:"playerId"`
		Version  int64  `json:"version"`
	}
	resp.decodifica(t, &corpo)
	if corpo.ID == "" {
		t.Fatalf("%s: a carteira aberta veio sem id", inst.nome)
	}
	return corpo.ID, corpo.PlayerID
}

// operacao é o corpo de resposta do `POST /wagering/transactions`.
//
// Os ponteiros em `Balance` e `FailureCode` preservam a distinção do contrato:
// `null` (o dado não se aplica ao desfecho) é diferente de `"0.00"` e de `""`.
type operacao struct {
	TransactionID    string  `json:"transactionId"`
	Status           string  `json:"status"`
	IdempotentReplay bool    `json:"idempotentReplay"`
	FailureCode      *string `json:"failureCode"`
	Balance          *struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	} `json:"balance"`
}

// enviarOperacao manda a operação por uma instância e decodifica a resposta.
//
// Não exige status: quem chama olha o status, porque nos cenários de corrida
// 201 e 422 são AMBOS resultados legítimos — o que não pode acontecer é erro de
// transporte.
func enviarOperacao(t *testing.T, inst *instancia, token, chave string, corpo map[string]any) (operacao, resposta) {
	t.Helper()

	op, resp, err := tentarEnviarOperacao(inst, token, chave, corpo)
	if err != nil {
		t.Fatalf("%s: enviando a operação: %v", inst.nome, err)
	}
	return op, resp
}

// tentarEnviarOperacao é a versão sem `t.Fatalf`, para uso em goroutines.
func tentarEnviarOperacao(inst *instancia, token, chave string, corpo map[string]any) (operacao, resposta, error) {
	resp, err := inst.tentarChamar(http.MethodPost, "/wagering/transactions", token, corpo,
		map[string]string{httpapi.HeaderIdempotencyKey: chave})
	if err != nil {
		return operacao{}, resposta{}, err
	}

	var op operacao
	if err := json.Unmarshal(resp.corpo, &op); err != nil {
		return operacao{}, resp, fmt.Errorf("corpo da operação não é JSON (%v): %s", err, resp.texto())
	}
	return op, resp, nil
}

// ---------------------------------------------------------------------------
// T1 (README §13.1): a MESMA aposta 50 vezes em paralelo
// ---------------------------------------------------------------------------

// TestT1MesmaAposta50VezesEntreTresInstancias é o cenário 1 do README §13, com o
// número do enunciado (50 — a prova da Fase 6 usou 12 disparos em um processo).
//
// 50 requisições da MESMA aposta, distribuídas entre os três processos e
// liberadas por uma barreira, têm de produzir:
//
//   - UM débito no ledger (o índice único `(wallet_id, transaction_id)` e a
//     idempotência por `(provider_id, idempotency_key)` são quem decide, não o
//     processo);
//   - UMA linha em `wager_transactions`;
//   - a versão da carteira em 2 (uma única movimentação);
//   - resposta 201 para exatamente UMA delas e 200 (`idempotentReplay: true`)
//     para as outras 49, TODAS com o mesmo `transactionId` e o MESMO saldo.
//
// # Por que a barreira, e não `go` direto
//
// Disparar 50 goroutines já é concorrente, mas elas não chegam juntas: a primeira
// pode terminar antes de a trigésima começar, e aí o cenário vira 50 chamadas
// sequenciais — verde sem provar nada. A barreira (`close(largada)`) faz todas
// esperarem o mesmo instante, que é o que o README chama de "em paralelo".
func TestT1MesmaAposta50VezesEntreTresInstancias(t *testing.T) {
	limpar(t)
	instancias := subirInstancias(t, 3, opcoesInstancia{})

	token, provedor := credencialDeOperacao(t)
	carteira, jogador := abrirCarteiraNaInstancia(t, instancias[0], "1000.00")

	corpo := corpoDeOperacao(provedor, "txn-50x", jogador, carteira, "BET", "25.00")
	chave := provedor + ":txn-50x"

	const disparos = 50

	type resultado struct {
		op   operacao
		resp resposta
		err  error
	}
	saida := make([]resultado, disparos)

	largada := make(chan struct{})
	var espera sync.WaitGroup

	for i := 0; i < disparos; i++ {
		espera.Add(1)
		go func(i int) {
			defer espera.Done()
			<-largada
			// Round-robin entre os três processos: 17, 17 e 16 disparos.
			inst := instancias[i%len(instancias)]
			op, resp, err := tentarEnviarOperacao(inst, token, chave, corpo)
			saida[i] = resultado{op: op, resp: resp, err: err}
		}(i)
	}
	close(largada)
	espera.Wait()

	// (a) Nenhum erro de transporte: o que se prova aqui é a garantia financeira,
	// e um 503 esconderia a discussão atrás de um retry de rede.
	var criadas, repetidas int
	var transacao string
	for i, r := range saida {
		if r.err != nil {
			t.Fatalf("disparo %d: %v", i, r.err)
		}
		switch r.resp.status {
		case http.StatusCreated:
			criadas++
		case http.StatusOK:
			repetidas++
		default:
			t.Fatalf("disparo %d (%s): status %d, quer 201 ou 200: %s",
				i, instancias[i%len(instancias)].nome, r.resp.status, r.resp.texto())
		}
		if r.op.Status != "PROCESSED" {
			t.Fatalf("disparo %d: status %s (%v), quer PROCESSED", i, r.op.Status, r.op.FailureCode)
		}
		if r.op.Balance == nil || r.op.Balance.Amount != "975.00" {
			t.Fatalf("disparo %d: saldo devolvido %v, quer 975.00 (o do processamento original)", i, r.op.Balance)
		}
		if r.op.IdempotentReplay != (r.resp.status == http.StatusOK) {
			t.Fatalf("disparo %d: status %d com idempotentReplay=%v", i, r.resp.status, r.op.IdempotentReplay)
		}
		if transacao == "" {
			transacao = r.op.TransactionID
		} else if r.op.TransactionID != transacao {
			t.Fatalf("disparo %d devolveu a transação %s; o esperado era a MESMA %s",
				i, r.op.TransactionID, transacao)
		}
	}
	if criadas != 1 || repetidas != disparos-1 {
		t.Fatalf("criadas=%d repetidas=%d, esperado 1 e %d", criadas, repetidas, disparos-1)
	}

	// (b) O estado DURÁVEL: um débito, uma linha, uma movimentação.
	exigirSaldoDaCarteira(t, carteira, 97500)
	if versao := versaoNoBanco(t, ids.WalletID(carteira)); versao != 2 {
		t.Fatalf("versão da carteira = %d, esperado 2 (uma única movimentação)", versao)
	}
	exigirLancamentosPorID(t, carteira, "CREDIT 1000.00", "DEBIT 25.00")
	if total := contarTransacoes(t); total != 2 {
		t.Fatalf("wager_transactions = %d, esperado 2 (a OPENING da abertura + UMA BET)", total)
	}

	// (c) Nada foi publicado: o publicador está desligado nas três instâncias, e
	// a outbox guarda exatamente os eventos da abertura (2) e da aposta (2).
	if tipos := tiposDeEvento(t); len(tipos) != 4 {
		t.Fatalf("outbox = %v, esperado os 2 da abertura + os 2 da aposta (nada publicado)", tipos)
	}
	for _, e := range eventosNaOutbox(t) {
		if e.Publicado {
			t.Fatal("com OUTBOX_ENABLED=false nada podia estar publicado")
		}
	}

	// (d) A porta HTTP não usa a inbox: o único registro de dedup aqui é a
	// idempotência FINANCEIRA (índices únicos + hash).
	if total := contarInbox(t); total != 0 {
		t.Fatalf("inbox = %d, esperado 0 (a entrada foi HTTP)", total)
	}

	t.Logf("T1 (HTTP): %d disparos entre %s, %d criada, %d replays, transação %s",
		disparos, nomesDas(instancias), criadas, repetidas, transacao)
}

// TestT1MesmaOperacao50VezesPelaFila é o T1 pela SEGUNDA porta de entrada.
//
// 50 entrega DISTINTAS (50 `MessageId` do broker, 50 dedup ids, 50 grupos) com o
// MESMO corpo. A inbox NÃO pode ser o que segura a repetição: `(consumer_name,
// message_id)` é diferente em todas as 50, então todas passam pela inbox — e é a
// idempotência FINANCEIRA que tem de barrar 49. É literalmente o que o README
// §13 pede: "os testes de duplicidade devem exercitar a deduplicação da
// APLICAÇÃO, com recebimentos repetidos comprovados".
//
// Três consumidores reais disputam a fila: a mensagem que a instância A está
// processando pode ser reentregue à B se A demorar, e mesmo assim o débito é um.
func TestT1MesmaOperacao50VezesPelaFila(t *testing.T) {
	limpar(t)

	fila := abrirSQS(t)
	fila.esvaziarFila(t, fila.urlTxn)
	fila.esvaziarFila(t, fila.urlDLQ)
	t.Cleanup(func() {
		fila.esvaziarFila(t, fila.urlTxn)
		fila.esvaziarFila(t, fila.urlDLQ)
	})

	// Os três processos com o consumidor LIGADO: é a topologia de produção de um
	// serviço com três réplicas.
	instancias := subirInstancias(t, 3, opcoesInstancia{Consumidor: true})

	// A carteira é aberta pela instância 1; as mensagens podem ser consumidas por
	// qualquer uma das três.
	carteira, jogador := abrirCarteiraNaInstancia(t, instancias[0], "1000.00")

	const entrega = 50
	corpo := mensagemParaOperacao("provider-a", "txn-sqs-50x", "provider-a:txn-sqs-50x",
		jogador, carteira, "round-1", "fortune-chimp", "BET", "25.00")

	for i := 0; i < entrega; i++ {
		// O nonce vai no dedup id (`enviar` já acrescenta um). Grupo único por
		// mensagem: numa FIFO, um grupo fixo faria a segunda esperar a primeira
		// ser removida, e o teste mediria ordenação em vez de idempotência.
		fila.enviar(t, corpo, fmt.Sprintf("fase10-t1-%d", i))
	}

	// Convergência observada no BANCO e na FILA (o consumo é assíncrono): o saldo
	// certo e a fila vazia significam que as 50 foram tratadas e removidas.
	alvo := ids.WalletID(carteira)
	esperarAtende(t, "as 50 entregas serem tratadas", 120*time.Second, func() bool {
		return saldoNoBanco(t, alvo) == 97500 && fila.mensagensNaFila(t, fila.urlTxn) == 0
	})

	// Um débito, uma linha, versão 2.
	if versao := versaoNoBanco(t, alvo); versao != 2 {
		t.Fatalf("versão da carteira = %d, esperado 2", versao)
	}
	exigirLancamentosPorID(t, carteira, "CREDIT 1000.00", "DEBIT 25.00")
	if total := contarTransacoes(t); total != 2 {
		t.Fatalf("wager_transactions = %d, esperado 2 (OPENING + UMA BET)", total)
	}

	// A prova de que a deduplicação foi da APLICAÇÃO, e não da inbox: 50
	// recebimentos registrados, todos PROCESSED, para UMA movimentação.
	processadas, rejeitadas := statusDaInbox(t)
	if processadas != entrega {
		t.Fatalf("inbox PROCESSED = %d, esperado %d (cada entrega é uma mensagem distinta para o broker)",
			processadas, entrega)
	}
	if rejeitadas != 0 {
		t.Fatalf("inbox com %d mensagens rejeitadas; o reenvio da mesma operação é replay, não erro", rejeitadas)
	}

	// Nenhuma foi para a DLQ: repetição não é mensagem envenenada.
	if mortas := fila.mensagensNaDLQ(t); mortas != 0 {
		t.Fatalf("DLQ tem %d mensagem(ns); repetição legítima não é falha permanente", mortas)
	}

	t.Logf("T1 (SQS): %d entregas distintas entre %s, 1 débito, inbox com %d PROCESSED",
		entrega, nomesDas(instancias), processadas)
}

// ---------------------------------------------------------------------------
// T2 (README §8): 100.00 BRL contra duas apostas simultâneas de 80.00
// ---------------------------------------------------------------------------

// TestT2DuasApostasDe80Sobre100EmInstanciasDiferentes é O TESTE OBRIGATÓRIO do
// README §8, com a exigência que a Fase 6 não cumpria: cada aposta chega por uma
// INSTÂNCIA diferente.
//
// O que tem de acontecer, e por quê:
//
//   - as duas chegam juntas (barreira) e as duas passam pelo `SELECT ... FOR
//     UPDATE` da MESMA linha de carteira: a segunda ESPERA a primeira;
//   - quem entra primeiro debita 80.00 (saldo 100.00 → 20.00) e comita;
//   - quem entra depois lê o saldo JÁ ATUALIZADO (20.00), tenta debitar 80.00 e
//     o domínio recusa por saldo insuficiente: `REJECTED`/`INSUFFICIENT_FUNDS`,
//     SEM tocar no saldo e SEM lançamento;
//   - resultado: 1 PROCESSED, 1 REJECTED, saldo 20.00, UM débito, versão 2;
//   - reenviar as duas 10 vezes não muda NADA (é replay das duas, inclusive da
//     rejeitada: a decisão é terminal e durável).
//
// # O que quebraria sem o lock
//
// Sem `FOR UPDATE`, as duas leriam 100.00 ao mesmo tempo, as duas calculariam
// 20.00, e o `UPDATE ... WHERE version = 1` faria UMA delas perder a corrida e
// falhar por conflito — ou, pior, sem a versão, as duas gravariam 20.00 e 80.00
// sumiriam do sistema (lost update). É o cenário que o README §5.7 proíbe.
func TestT2DuasApostasDe80Sobre100EmInstanciasDiferentes(t *testing.T) {
	limpar(t)
	instancias := subirInstancias(t, 3, opcoesInstancia{})

	token, provedor := credencialDeOperacao(t)
	carteira, jogador := abrirCarteiraNaInstancia(t, instancias[0], "100.00")
	alvo := ids.WalletID(carteira)

	corpos := [2]map[string]any{
		corpoDeOperacao(provedor, "txn-80-a", jogador, carteira, "BET", "80.00"),
		corpoDeOperacao(provedor, "txn-80-b", jogador, carteira, "BET", "80.00"),
	}
	chaves := [2]string{provedor + ":txn-80-a", provedor + ":txn-80-b"}
	// Cada aposta por uma instância DIFERENTE — é o que torna a corrida real:
	// dois processos, dois pools, duas conexões.
	destinos := [2]*instancia{instancias[1], instancias[2]}

	type resultado struct {
		op   operacao
		resp resposta
		err  error
	}
	var saida [2]resultado

	largada := make(chan struct{})
	var espera sync.WaitGroup

	for i := 0; i < 2; i++ {
		espera.Add(1)
		go func(i int) {
			defer espera.Done()
			<-largada
			op, resp, err := tentarEnviarOperacao(destinos[i], token, chaves[i], corpos[i])
			saida[i] = resultado{op: op, resp: resp, err: err}
		}(i)
	}
	close(largada)
	espera.Wait()

	// Exatamente UMA processada e UMA rejeitada por saldo insuficiente.
	var processadas, rejeitadas int
	for i := range saida {
		if saida[i].err != nil {
			t.Fatalf("aposta %d (%s): %v", i, chaves[i], saida[i].err)
		}
		switch saida[i].resp.status {
		case http.StatusCreated:
			processadas++
			if saida[i].op.Status != "PROCESSED" {
				t.Fatalf("aposta %d: 201 com status %s", i, saida[i].op.Status)
			}
			if saida[i].op.Balance == nil || saida[i].op.Balance.Amount != "20.00" {
				t.Fatalf("aposta %d: saldo devolvido %v, quer 20.00", i, saida[i].op.Balance)
			}
		case http.StatusUnprocessableEntity:
			rejeitadas++
			if saida[i].op.Status != "REJECTED" {
				t.Fatalf("aposta %d: 422 com status %s", i, saida[i].op.Status)
			}
			if saida[i].op.FailureCode == nil || *saida[i].op.FailureCode != wallet.CodeInsufficientFunds {
				t.Fatalf("aposta %d: failureCode %v, quer %q", i, saida[i].op.FailureCode, wallet.CodeInsufficientFunds)
			}
			if saida[i].op.Balance != nil {
				t.Fatalf("aposta %d: rejeição devolveu saldo %v; rejeitada não tem resultado financeiro",
					i, saida[i].op.Balance)
			}
		default:
			t.Fatalf("aposta %d: status %d, quer 201 ou 422: %s", i, saida[i].resp.status, saida[i].resp.texto())
		}
	}
	if processadas != 1 || rejeitadas != 1 {
		t.Fatalf("processadas=%d rejeitadas=%d, o README §8 exige exatamente 1 e 1", processadas, rejeitadas)
	}

	// O resultado financeiro do enunciado: 20.00 e UM débito.
	if saldo := saldoNoBanco(t, alvo); saldo != 2000 {
		t.Fatalf("saldo gravado = %d, quer 2000 (20.00)", saldo)
	}
	if versao := versaoNoBanco(t, alvo); versao != 2 {
		t.Fatalf("versão = %d, quer 2 (uma única movimentação)", versao)
	}
	exigirLancamentosPorID(t, carteira, "CREDIT 100.00", "DEBIT 80.00")

	// Duas linhas de transação: a processada e a rejeitada (a rejeição é auditável).
	if total := contarTransacoes(t); total != 3 {
		t.Fatalf("wager_transactions = %d, quer 3 (OPENING + BET processada + BET rejeitada)", total)
	}

	// ---------------------------------------------------------------------
	// Reenvios: 10 vezes cada, alternando as instâncias. NADA pode mudar.
	// ---------------------------------------------------------------------
	antes := saldoNoBanco(t, alvo)
	transacaoOriginal := [2]string{saida[0].op.TransactionID, saida[1].op.TransactionID}

	// Qual das duas criou o débito? É ela que responde 200 no reenvio; a outra
	// responde 422 de novo. O STATUS DO DESFECHO não muda com o reenvio (a
	// rejeição é terminal e durável); o que muda é a marca `idempotentReplay`.
	criada := -1
	for i := range saida {
		if saida[i].resp.status == http.StatusCreated {
			criada = i
		}
	}
	if criada < 0 {
		t.Fatal("nenhuma das duas apostas foi criada")
	}

	for volta := 0; volta < 10; volta++ {
		for i := 0; i < 2; i++ {
			inst := instancias[(volta+i)%len(instancias)]
			op, resp := enviarOperacao(t, inst, token, chaves[i], corpos[i])

			esperado := http.StatusUnprocessableEntity
			if i == criada {
				esperado = http.StatusOK
			}
			if resp.status != esperado {
				t.Fatalf("reenvio %d da aposta %d: status %d, quer %d: %s",
					volta, i, resp.status, esperado, resp.texto())
			}
			if op.TransactionID != transacaoOriginal[i] {
				t.Fatalf("reenvio %d da aposta %d: transação %s, quer a original %s",
					volta, i, op.TransactionID, transacaoOriginal[i])
			}
			if !op.IdempotentReplay {
				t.Fatalf("reenvio %d da aposta %d: idempotentReplay=false", volta, i)
			}
		}
	}

	if depois := saldoNoBanco(t, alvo); depois != antes {
		t.Fatalf("o saldo mudou de %d para %d com reenvios", antes, depois)
	}
	if versao := versaoNoBanco(t, alvo); versao != 2 {
		t.Fatalf("a versão mudou para %d com reenvios", versao)
	}
	exigirLancamentosPorID(t, carteira, "CREDIT 100.00", "DEBIT 80.00")
	if total := contarTransacoes(t); total != 3 {
		t.Fatalf("reenvios criaram transações: %d, quer 3", total)
	}

	// Eventos: 2 da abertura + 2 da processada + 1 da rejeição.
	contagem := map[string]int{}
	for _, tipo := range tiposDeEvento(t) {
		contagem[tipo]++
	}
	if len(contagem) != 3 ||
		contagem["WagerTransactionProcessed"] != 2 ||
		contagem["WalletBalanceChanged"] != 2 ||
		contagem["WagerTransactionRejected"] != 1 {
		t.Fatalf("eventos = %v, quer 2 Processed + 2 BalanceChanged + 1 Rejected", contagem)
	}

	t.Logf("T2: 100.00 + duas BET de 80.00 entre %s e %s -> 1 PROCESSED, 1 REJECTED/%s, saldo 20.00, 1 débito",
		destinos[0].nome, destinos[1].nome, wallet.CodeInsufficientFunds)
}

// ---------------------------------------------------------------------------
// T3 (README §13.3 + §5.6): carteiras distintas em paralelo, sem lock global
// ---------------------------------------------------------------------------

// TestT3CarteirasDistintasEmParaleloSemLockGlobal prova as duas metades do
// requisito:
//
//	README §13.3 — "processe carteiras distintas simultaneamente";
//	README §5.6  — "carteiras independentes devem avançar em paralelo; locks
//	               globais são proibidos".
//
// # Como o paralelismo é provado sem asserção de TEMPO frágil
//
// Comparar "N operações em carteiras diferentes" com "N na mesma carteira" por
// cronômetro é honesto, mas instável: com operações de poucos milissegundos, o
// ruído da máquina (agendamento, JIT do pool, GC) pode inverter a razão e o teste
// vira moeda. Aqui a diferença é CONSTRUÍDA, não medida:
//
//  1. uma transação do PRÓPRIO teste segura `SELECT ... FOR UPDATE` na carteira A;
//  2. com o lock preso, as operações nas carteiras B..F têm de CONCLUIR — se
//     houvesse um lock global (tabela, schema, advisory, ou `SERIALIZABLE` que
//     aborte o lote), elas não concluiriam;
//  3. a operação na carteira A tem de ficar BLOQUEADA enquanto o lock está preso,
//     e é o PostgreSQL quem confirma isso: `pg_stat_activity` mostra um backend
//     com `application_name = api-*` esperando lock;
//  4. liberado o lock, a operação de A conclui.
//
// A asserção é sempre sobre estado observável (concluiu / está esperando), nunca
// sobre "ficou rápido o suficiente". Os tempos medidos são REPORTADOS no log para
// a evidência, sem serem condição de aprovação.
func TestT3CarteirasDistintasEmParaleloSemLockGlobal(t *testing.T) {
	limpar(t)
	instancias := subirInstancias(t, 3, opcoesInstancia{})

	token, provedor := credencialDeOperacao(t)

	// Seis carteiras abertas pelo caminho real.
	const total = 6
	carteiras := make([]string, total)
	jogadores := make([]string, total)
	for i := 0; i < total; i++ {
		carteiras[i], jogadores[i] = abrirCarteiraNaInstancia(t, instancias[i%len(instancias)], "100.00")
	}
	segurada := carteiras[0]

	// (1) Prende a linha da carteira 0 numa transação do teste.
	ctx := context.Background()
	transacaoDoTeste, err := poolTeste.Begin(ctx)
	if err != nil {
		t.Fatalf("abrindo a transação do teste: %v", err)
	}
	liberada := false
	liberar := func() {
		if liberada {
			return
		}
		liberada = true
		// Rollback (e não commit): o teste não quer alterar nada, só segurar a linha.
		_ = transacaoDoTeste.Rollback(ctx)
	}
	defer liberar()

	if _, err := transacaoDoTeste.Exec(ctx,
		`SELECT 1 FROM wallets WHERE id = $1 FOR UPDATE`, segurada); err != nil {
		t.Fatalf("prendendo a carteira %s: %v", segurada, err)
	}

	// (2) Com o lock preso, as outras cinco carteiras concluem — em paralelo.
	type medida struct {
		indice  int
		duracao time.Duration
		op      operacao
		err     error
	}
	medidas := make([]medida, 0, total-1)

	type resultado struct {
		indice  int
		op      operacao
		err     error
		duracao time.Duration
	}
	saidas := make([]resultado, total-1)

	largada := make(chan struct{})
	var espera sync.WaitGroup
	for i := 1; i < total; i++ {
		espera.Add(1)
		go func(indice int) {
			defer espera.Done()
			corpo := corpoDeOperacao(provedor, "txn-par-"+jogadores[indice][:8], jogadores[indice],
				carteiras[indice], "BET", "10.00")
			chave := provedor + ":txn-par-" + jogadores[indice][:8]

			<-largada
			inicio := time.Now()
			op, _, err := tentarEnviarOperacao(instancias[indice%len(instancias)], token, chave, corpo)
			saidas[indice-1] = resultado{indice: indice, op: op, err: err, duracao: time.Since(inicio)}
		}(i)
	}
	close(largada)
	espera.Wait()

	// O lock da carteira 0 continuou PRESO durante todas essas operações: a
	// transação do teste só é liberada no defer. Se o código tivesse um lock
	// global, estas cinco operações estariam presas atrás dela.
	for _, s := range saidas {
		if s.err != nil {
			t.Fatalf("carteira %d: %v", s.indice, s.err)
		}
		if s.op.Status != "PROCESSED" {
			t.Fatalf("carteira %d: status %s, quer PROCESSED mesmo com a carteira 0 presa",
				s.indice, s.op.Status)
		}
		medidas = append(medidas, medida{indice: s.indice, duracao: s.duracao, op: s.op})
	}

	if esperando := instanciasEsperandoLock(t); esperando != 0 {
		t.Fatalf("%d backend(s) esperando lock com carteiras distintas em andamento: há lock global", esperando)
	}

	// (3) A operação na carteira PRESA tem de bloquear.
	corpoSegurada := corpoDeOperacao(provedor, "txn-segurada", jogadores[0], segurada, "BET", "10.00")
	chaveSegurada := provedor + ":txn-segurada"

	bloqueada := make(chan resultado, 1)
	go func() {
		inicio := time.Now()
		op, _, err := tentarEnviarOperacao(instancias[0], token, chaveSegurada, corpoSegurada)
		bloqueada <- resultado{indice: 0, op: op, err: err, duracao: time.Since(inicio)}
	}()

	const janela = 400 * time.Millisecond
	select {
	case r := <-bloqueada:
		t.Fatalf("a operação na carteira presa terminou em %s com o lock ainda preso (status %s): não houve espera pelo lock",
			r.duracao, r.op.Status)
	case <-time.After(janela):
		// Continua bloqueada — é o esperado.
	}

	// E o PostgreSQL mostra QUEM espera: um backend das instâncias, não um
	// qualquer. É o `application_name` da Fase 10 (defeito 40) servindo de
	// instrumento de medida.
	esperarAtende(t, "um backend de instância esperando o lock da carteira", 10*time.Second, func() bool {
		return instanciasEsperandoLock(t) >= 1
	})

	// (4) Liberado o lock, a operação conclui.
	liberar()

	var presa resultado
	select {
	case presa = <-bloqueada:
	case <-time.After(30 * time.Second):
		t.Fatal("a operação na carteira presa não concluiu nem depois de liberar o lock")
	}
	if presa.err != nil {
		t.Fatalf("operação na carteira presa: %v", presa.err)
	}
	if presa.op.Status != "PROCESSED" {
		t.Fatalf("operação na carteira presa: status %s (%v)", presa.op.Status, presa.op.FailureCode)
	}

	// Conferência final do T3: cada carteira com UMA movimentação de 10.00.
	for i := 0; i < total; i++ {
		if saldo := saldoNoBanco(t, ids.WalletID(carteiras[i])); saldo != 9000 {
			t.Fatalf("carteira %d: saldo %d, quer 9000", i, saldo)
		}
		if versao := versaoNoBanco(t, ids.WalletID(carteiras[i])); versao != 2 {
			t.Fatalf("carteira %d: versão %d, quer 2", i, versao)
		}
	}

	var maior, menor time.Duration
	for i, m := range medidas {
		if i == 0 || m.duracao > maior {
			maior = m.duracao
		}
		if i == 0 || m.duracao < menor {
			menor = m.duracao
		}
	}
	t.Logf("T3: %d carteiras distintas concluíram com a carteira 0 PRESA (durações %s..%s); "+
		"a operação na carteira presa esperou %s e só concluiu depois da liberação",
		len(medidas), menor.Round(time.Millisecond), maior.Round(time.Millisecond), presa.duracao.Round(time.Millisecond))
}

// ---------------------------------------------------------------------------
// Auxiliares de asserção dos cenários
// ---------------------------------------------------------------------------

// exigirSaldoDaCarteira confere o saldo de uma carteira identificada por STRING.
//
// As helpers da Fase 6 recebem a entidade `*wallet.Wallet`; os cenários desta
// fase obtêm o id como texto (ele vem do JSON da abertura, como o cliente HTTP o
// recebe). Duas assinaturas evitam converter entidade só para conferir número.
func exigirSaldoDaCarteira(t *testing.T, carteira string, esperado int64) {
	t.Helper()

	if atual := saldoNoBanco(t, ids.WalletID(carteira)); atual != esperado {
		t.Fatalf("saldo gravado = %d, quer %d", atual, esperado)
	}
}

// exigirLancamentosPorID confere o ledger de uma carteira identificada por string.
func exigirLancamentosPorID(t *testing.T, carteira string, esperados ...string) {
	t.Helper()

	obtidos := lancamentosNoBanco(t, ids.WalletID(carteira))
	if strings.Join(obtidos, ",") != strings.Join(esperados, ",") {
		t.Fatalf("ledger = %v, esperado %v", obtidos, esperados)
	}
}

// statusDaInbox conta as mensagens da inbox por status.
func statusDaInbox(t *testing.T) (processadas, rejeitadas int) {
	t.Helper()

	linhas, err := poolTeste.Query(context.Background(),
		`SELECT status, count(*) FROM inbox_messages GROUP BY status`)
	if err != nil {
		t.Fatalf("lendo a inbox: %v", err)
	}
	defer linhas.Close()

	for linhas.Next() {
		var (
			status string
			total  int
		)
		if err := linhas.Scan(&status, &total); err != nil {
			t.Fatalf("lendo status da inbox: %v", err)
		}
		switch status {
		case "PROCESSED":
			processadas = total
		case "REJECTED":
			rejeitadas = total
		}
	}
	return processadas, rejeitadas
}

// nomesDas devolve os nomes das instâncias, para o log da evidência.
func nomesDas(instancias []*instancia) string {
	nomes := make([]string, 0, len(instancias))
	for _, inst := range instancias {
		nomes = append(nomes, inst.nome)
	}
	return strings.Join(nomes, ", ")
}
