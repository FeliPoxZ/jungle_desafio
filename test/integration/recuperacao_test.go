//go:build integration

package integration

// T5, T6, T7 e T8 do README §13: interrupção e reinício.
//
// # A diferença de fundo em relação a T1–T3
//
// T1–T3 medem o estado FINAL de operações que chegaram juntas. Aqui o objeto é o
// INSTANTE ENTRE duas etapas — o commit já aconteceu e a mensagem ainda não foi
// removida — e o que se prova é que um processo NOVO, que nunca viu a operação,
// chega ao mesmo resultado financeiro. É a diferença entre "o código está certo"
// e "o sistema sobrevive à queda", que é o que vale 15 pontos no §14.

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/inbox"
	"github.com/FeliPoxZ/jungle_desafio/internal/consumer"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/infra/sqs"
)

// ---------------------------------------------------------------------------
// T5 (README §13.5): interrupção DEPOIS do commit e ANTES do delete
// ---------------------------------------------------------------------------

// filaQueNaoApaga envolve a fila REAL e transforma o `Delete` em "não apagou".
//
// # Por que esta é a forma honesta de reproduzir a queda
//
// No Windows não existe SIGTERM para processo de host, e a janela entre o commit
// e o `DeleteMessage` dura microssegundos: matar o processo exatamente ali não é
// reproduzível por sinal. O que se reproduz aqui é o ESTADO DURÁVEL que uma
// queda naquele instante deixa —
//
//	operações aplicadas e commitadas  → já aconteceu (a transação commitou)
//	inbox PROCESSED                   → já aconteceu
//	mensagem AINDA na fila            → o `DeleteMessage` nunca chegou ao broker
//
// — e esse estado é IDÊNTICO ao de um processo que morre depois do commit: o
// processo não existe mais, e o que sobrou é o banco e a fila. O que se substitui
// é o COLABORADOR que representa a etapa que morreu, exatamente como o
// `uowComOutboxQuebrada` da Fase 6 substitui o passo da outbox sem trocar o banco.
//
// O `Delete` devolve `nil` porque é o que o processo morto "acharia": ele não
// recebe erro nenhum, simplesmente não chegou a perguntar.
type filaQueNaoApaga struct {
	consumer.Fila

	mu      sync.Mutex
	handles []string
}

func (f *filaQueNaoApaga) Delete(_ context.Context, _ string, handles []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handles = append(f.handles, handles...)
	return nil
}

func (f *filaQueNaoApaga) registrados() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	copia := make([]string, len(f.handles))
	copy(copia, f.handles)
	return copia
}

// consumidorQueEnvolve roda o consumidor de PRODUÇÃO com a fila embrulhada.
//
// O processador é o real (`inbox.NovoProcessador(servico)`), o caso de uso é o
// real e a transação é a real: o que muda é só o passo de apagar a mensagem.
func consumidorQueEnvolve(t *testing.T, fila *sqsDeTeste) (*filaQueNaoApaga, func()) {
	t.Helper()

	embrulhada := &filaQueNaoApaga{Fila: sqs.NovoConsumidorFila(sqs.NewClient(fila.cfg))}

	var (
		mu     sync.Mutex
		saida  strings.Builder
		logger = slog.New(slog.NewTextHandler(&bufferProtegido{mu: &mu, destino: &saida},
			&slog.HandlerOptions{Level: slog.LevelDebug}))
	)
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		t.Logf("log do consumidor em processo:\n%s", saida.String())
	})

	consumidor := consumer.New(consumer.Options{
		Fila:        embrulhada,
		Processador: inbox.NovoProcessador(servico),
		Config:      fila.cfg,
		URLDaFila:   fila.urlTxn,
		URLDaDLQ:    fila.urlDLQ,
		Logger:      logger,
	})

	ctx, cancel := context.WithCancel(context.Background())
	parou := make(chan struct{})
	go func() {
		defer close(parou)
		_ = consumidor.Run(ctx)
	}()

	var umaVez sync.Once
	parar := func() {
		umaVez.Do(func() {
			cancel()
			select {
			case <-parou:
			case <-time.After(10 * time.Second):
			}
		})
	}
	t.Cleanup(parar)

	return embrulhada, parar
}

// TestT5QuedaEntreCommitEDeleteReentregaReconhecida é o cenário 5 do README §13.
//
// # O que é provado, em ordem
//
//  1. uma entrega é processada e COMITADA (débito + inbox PROCESSED), e o delete
//     da mensagem "não acontece" — o estado exato de uma queda entre o commit e a
//     remoção;
//  2. o processo que a processou SAI (a queda);
//  3. o estado durável é conferido: operação aplicada, inbox PROCESSED e a
//     mensagem AINDA na fila (`ApproximateNumberOfMessagesNotVisible = 1`);
//  4. a mensagem volta a ser entregue, com o MESMO `MessageId` do broker — como o
//     vencimento do visibility timeout faria;
//  5. uma instância NOVA (processo novo, memória nova) a consome: a inbox recusa
//     a reentrega, NADA é debitado de novo, a mensagem é removida e a DLQ fica
//     vazia.
//
// # O limite desta prova, escrito antes que alguém pergunte
//
// O que se reproduz é o estado durável, não o sinal. A prova com SIGTERM real
// (container Linux, `docker stop`) está em `docs/evidencias/FASE_10.md` §6, com a
// saída do container; no Windows o `Kill` é terminação dura e não exercita o
// caminho gracioso (pendência P8 do handoff).
func TestT5QuedaEntreCommitEDeleteReentregaReconhecida(t *testing.T) {
	limpar(t)

	fila := abrirSQS(t)
	fila.esvaziarFila(t, fila.urlTxn)
	fila.esvaziarFila(t, fila.urlDLQ)
	t.Cleanup(func() {
		fila.esvaziarFila(t, fila.urlTxn)
		fila.esvaziarFila(t, fila.urlDLQ)
	})

	// O `VisibilityTimeout` da fila vai para 2s durante o cenário (e é
	// restaurado no fim). Numa queda real, a mensagem volta sozinha quando o
	// prazo vence: aqui o prazo é curto para o teste não esperar 60 segundos por
	// algo que já está provado no banco. O `ChangeMessageVisibility(0)` seria a
	// alternativa "instantânea", mas o LocalStack não antecipa a entrega com ele
	// (medido: a mensagem continuou invisível pelos 60s do prazo original), e
	// encurtar o prazo é mais fiel ao que acontece numa queda.
	restaurarVisibilidade := fila.ajustarVisibilidadeDaFila(t, fila.urlTxn, 2)
	t.Cleanup(restaurarVisibilidade)

	// A instância só serve para abrir a carteira pelo caminho real; a operação em
	// si chega pela FILA.
	abridora := subirInstancias(t, 1, opcoesInstancia{})[0]
	carteira, jogador := abrirCarteiraNaInstancia(t, abridora, "1000.00")
	alvo := ids.WalletID(carteira)

	embrulhada, pararConsumidor := consumidorQueEnvolve(t, fila)

	corpo := mensagemParaOperacao("provider-a", "txn-t5", "provider-a:txn-t5",
		jogador, carteira, "round-1", "fortune-chimp", "BET", "25.00")
	messageID := fila.enviar(t, corpo, "fase10-t5")

	// (1) O tratamento durável aconteceu — commit feito e delete tentado.
	esperarAtende(t, "o tratamento durável acontecer", 90*time.Second, func() bool {
		status, _, _, existe := estadoDaInbox(t, messageID)
		return existe && status == "PROCESSED" &&
			saldoNoBanco(t, alvo) == 97500 &&
			len(embrulhada.registrados()) > 0
	})

	handles := embrulhada.registrados()
	if len(handles) == 0 {
		t.Fatal("o consumidor não tentou apagar a mensagem: a premissa do cenário não aconteceu")
	}

	// (2) A QUEDA: o processo que consumiu sai de cena.
	pararConsumidor()

	// (3) O estado durável tem de ser exatamente o de "commit feito, delete não".
	status, _, tentativasIniciais, existe := estadoDaInbox(t, messageID)
	if !existe || status != "PROCESSED" {
		t.Fatalf("inbox: existe=%v status=%q, quer PROCESSED", existe, status)
	}
	if tentativasIniciais != 1 {
		t.Fatalf("tentativas na primeira entrega = %d, quer 1", tentativasIniciais)
	}
	if saldo := saldoNoBanco(t, alvo); saldo != 97500 {
		t.Fatalf("saldo gravado = %d, quer 97500 (o commit JÁ aconteceu antes da queda)", saldo)
	}

	visiveis, invisiveis := fila.atributosDaFila(t, fila.urlTxn)
	if visiveis != 0 || invisiveis != 1 {
		t.Fatalf("fila: visíveis=%d invisíveis=%d, quer 0 e 1 (a mensagem ainda está lá, em voo)",
			visiveis, invisiveis)
	}

	// (4) A reentrega: a mensagem volta sozinha quando o `VisibilityTimeout`
	// (2s neste cenário) vence — é o que uma queda real produz, sem intervenção.
	inicioReentrega := time.Now()

	// (5) Instância NOVA: outro processo, outro pool, outra memória.
	nova := subirInstancias(t, 1, opcoesInstancia{Inicio: 2, Consumidor: true})[0]

	// A convergência é observada no BANCO, e não nos contadores da fila.
	//
	// Os `ApproximateNumberOfMessages*` do SQS são recalibrados de tempos em
	// tempos: depois de o consumidor novo apagar a mensagem, o contador de "em
	// voo" pode continuar em 1 por mais de um minuto. Esperar por ele faria o
	// teste demorar um minuto para provar algo que o banco já prova — o contador
	// durável `inbox.attempts` vai a 2 exatamente quando a reentrega é
	// reconhecida.
	esperarAtende(t, "a instância nova reconhecer a reentrega", 90*time.Second, func() bool {
		_, _, tentativas, _ := estadoDaInbox(t, messageID)
		return tentativas >= 2
	})
	esperou := time.Since(inicioReentrega)

	// NADA foi movimentado de novo.
	exigirSaldoDaCarteira(t, carteira, 97500)
	exigirLancamentosPorID(t, carteira, "CREDIT 1000.00", "DEBIT 25.00")
	if total := contarTransacoes(t); total != 2 {
		t.Fatalf("wager_transactions = %d, quer 2 (OPENING + a BET processada UMA vez)", total)
	}

	// A reentrega foi RECONHECIDA e CONTADA: é o que o contador durável existe
	// para dizer (defeito 35 — o `ApproximateReceiveCount` do broker se perde
	// quando a mensagem é removida).
	status, _, tentativas, existe := estadoDaInbox(t, messageID)
	if !existe || status != "PROCESSED" {
		t.Fatalf("inbox depois da reentrega: existe=%v status=%q", existe, status)
	}
	if tentativas < 2 {
		t.Fatalf("attempts = %d, quer >= 2 (a reentrega tem de incrementar o contador durável)", tentativas)
	}

	// A mensagem saiu da fila: nada continua disponível para entrega.
	if restantes, _ := fila.atributosDaFila(t, fila.urlTxn); restantes != 0 {
		t.Fatalf("ainda há %d mensagem(ns) disponíveis na fila depois da reentrega", restantes)
	}

	if mortas := fila.mensagensNaDLQ(t); mortas != 0 {
		t.Fatalf("DLQ tem %d mensagem(ns): reentrega legítima não é mensagem envenenada", mortas)
	}

	exigirReconciliacaoConsistente(t, nova, carteira, 2)

	t.Logf("T5: queda entre commit e delete (mensagem em voo=%d), reentrega reconhecida pela inbox "+
		"(attempts %d -> %d em %s), nenhum débito novo, DLQ vazia",
		invisiveis, tentativasIniciais, tentativas, esperou.Round(time.Millisecond))
}

// ---------------------------------------------------------------------------
// T8 (README §13.8): reinício preservando idempotência, pendências e consistência
// ---------------------------------------------------------------------------

// TestT8ReinicioPreservaIdempotenciaPendenciaEConsistencia é o cenário 8.
//
// O roteiro, e por que cada passo importa:
//
//  1. a instância A abre a carteira, recebe um REFUND cuja BET ainda não existe
//     (`PENDING_REFERENCE`, sintoma do §13.7) e depois recebe a BET. O worker de
//     referências está DESLIGADO nela: a pendência fica no banco, sem desfecho;
//  2. A MORRE (processo novo não sabe de nada do que ela tinha em memória);
//  3. a instância B sobe — outro processo, outra memória, agora COM o worker;
//  4. reenviar a BET para B devolve REPLAY: mesma transação, mesmo saldo
//     (75.00), nenhum débito novo. A idempotência é do banco, não da memória;
//  5. o worker de B resolve a pendência sozinho: o `PENDING_REFERENCE` é DURÁVEL,
//     e quem o conclui é um processo que nunca viu a requisição do REFUND;
//  6. a conferência final do §13 fecha: saldo armazenado == soma do ledger, pelo
//     endpoint `POST /wallets/{id}/reconciliation`, respondido por B.
func TestT8ReinicioPreservaIdempotenciaPendenciaEConsistencia(t *testing.T) {
	limpar(t)

	instanciaA := subirInstancias(t, 1, opcoesInstancia{})[0]

	token, provedor := credencialDeOperacao(t)
	carteira, jogador := abrirCarteiraNaInstancia(t, instanciaA, "100.00")
	alvo := ids.WalletID(carteira)

	// (1a) REFUND antes da referência: fica pendente (202).
	corpoRefund := corpoDeOperacao(provedor, "txn-rec-refund", jogador, carteira, "REFUND", "25.00")
	corpoRefund["referenceExternalTransactionId"] = "txn-rec-bet"

	refund, respRefund := enviarOperacao(t, instanciaA, token, provedor+":txn-rec-refund", corpoRefund)
	if respRefund.status != http.StatusAccepted {
		t.Fatalf("REFUND antes da referência = %d, quer 202: %s", respRefund.status, respRefund.texto())
	}
	if refund.Status != "PENDING_REFERENCE" {
		t.Fatalf("REFUND status = %s, quer PENDING_REFERENCE", refund.Status)
	}
	if saldo := saldoNoBanco(t, alvo); saldo != 10000 {
		t.Fatalf("saldo depois do REFUND pendente = %d, quer 10000 (pendência não movimenta)", saldo)
	}

	// (1b) A BET referenciada chega — processada normalmente.
	corpoBet := corpoDeOperacao(provedor, "txn-rec-bet", jogador, carteira, "BET", "25.00")
	bet, respBet := enviarOperacao(t, instanciaA, token, provedor+":txn-rec-bet", corpoBet)
	if respBet.status != http.StatusCreated {
		t.Fatalf("BET = %d, quer 201: %s", respBet.status, respBet.texto())
	}
	if bet.Balance == nil || bet.Balance.Amount != "75.00" {
		t.Fatalf("saldo da BET = %v, quer 75.00", bet.Balance)
	}

	// (2) A queda: a instância que atendeu tudo morre.
	instanciaA.parar(t)
	if instanciaA.viva(t) {
		t.Fatal("a instância A continuou respondendo depois de ser encerrada")
	}

	// (3) Instância NOVA, com o worker de referências ligado.
	instanciaB := subirInstancias(t, 1, opcoesInstancia{Inicio: 4, Referencia: true})[0]
	if instanciaB.pid == instanciaA.pid {
		t.Fatal("a instância B reusou o pid da A: não é um processo novo")
	}

	// (4) A idempotência sobreviveu ao reinício: mesma chave, mesmo conteúdo.
	replay, respReplay := enviarOperacao(t, instanciaB, token, provedor+":txn-rec-bet", corpoBet)
	if respReplay.status != http.StatusOK {
		t.Fatalf("reenvio da BET para a instância nova = %d, quer 200 (replay): %s",
			respReplay.status, respReplay.texto())
	}
	if !replay.IdempotentReplay {
		t.Fatal("o reenvio não foi marcado como replay")
	}
	if replay.TransactionID != bet.TransactionID {
		t.Fatalf("replay devolveu a transação %s, quer a original %s", replay.TransactionID, bet.TransactionID)
	}
	if replay.Balance == nil || replay.Balance.Amount != "75.00" {
		t.Fatalf("replay devolveu o saldo %v, quer o do processamento original (75.00)", replay.Balance)
	}
	exigirLancamentosPorID(t, carteira, "CREDIT 100.00", "DEBIT 25.00")

	// (5) A pendência é DURÁVEL e é resolvida pela instância NOVA.
	chaveRefund := provedor + ":txn-rec-refund"
	esperarAtende(t, "o worker da instância nova resolver a pendência", 120*time.Second, func() bool {
		status, _, _, _ := transacaoNoBanco(t, provedor, chaveRefund)
		return status == "PROCESSED"
	})

	// E foi ELA quem resolveu: o log é do processo que nunca viu a requisição.
	if !instanciaB.temNoLog("reference_resolvida") {
		t.Fatalf("a resolução não aparece no log da instância nova; log:\n%s", instanciaB.log.texto())
	}

	// (6) O resultado financeiro final e a conferência do §13.
	exigirSaldoDaCarteira(t, carteira, 10000)
	if versao := versaoNoBanco(t, alvo); versao != 3 {
		t.Fatalf("versão = %d, quer 3 (abertura, BET, REFUND)", versao)
	}
	exigirLancamentosPorID(t, carteira, "CREDIT 100.00", "DEBIT 25.00", "CREDIT 25.00")
	if total := contarTransacoes(t); total != 3 {
		t.Fatalf("wager_transactions = %d, quer 3 (OPENING + BET + REFUND)", total)
	}
	exigirReconciliacaoConsistente(t, instanciaB, carteira, 3)

	t.Logf("T8: instância A (pid %d) morreu; instância B (pid %d) devolveu replay da BET e resolveu "+
		"o REFUND pendente; saldo 100.00, 3 lançamentos, reconciliação consistente",
		instanciaA.pid, instanciaB.pid)
}

// ---------------------------------------------------------------------------
// T6 (README §13.6): três publishers reais disputando a mesma outbox
// ---------------------------------------------------------------------------

// TestT6TresPublishersReaisDisputamAMesmaOutbox repete, com TRÊS PROCESSOS, o
// que a Fase 9 provou com duas instâncias lógicas sobre o mesmo pool.
//
// # O que a repetição acrescenta, e o que ela NÃO acrescenta
//
// Acrescenta: três pools distintos, três processos distintos e `OUTBOX_BATCH_SIZE=1`,
// que força disputa REAL pelo mesmo lote a cada evento (com o lote padrão de 50, o
// primeiro que acorda leva tudo e os outros dois nunca tocam na outbox — seria
// uma prova de um publisher com dois espectadores).
//
// NÃO acrescenta: a ordem por carteira. Ela é garantida pela combinação
// "lote lido por `sequence` + `MessageGroupId = walletId`", e com o lote de 1 a
// ordem de ENVIO passa a depender de qual publisher termina primeiro — é a
// limitação registrada no ARCHITECTURE §16 ("ordem por carteira depende do lote").
// Por isso a asserção forte aqui é a PARTIÇÃO: 12 eventos, 12 mensagens, nenhum
// `eventId` repetido, nenhum evento perdido. A ordem é reportada e não é condição
// de aprovação, porque neste arranjo ela não é garantida pelo desenho.
func TestT6TresPublishersReaisDisputamAMesmaOutbox(t *testing.T) {
	limpar(t)

	fila := abrirFilaDeEventos(t)
	fila.esvaziar(t)
	t.Cleanup(func() { fila.esvaziar(t) })

	instancias := subirInstancias(t, 3, opcoesInstancia{
		Outbox: true,
		Extras: map[string]string{
			"OUTBOX_BATCH_SIZE":    "1",
			"OUTBOX_POLL_INTERVAL": "100ms",
		},
	})

	token, provedor := credencialDeOperacao(t)

	// Três carteiras e três apostas: 2 eventos por operação = 12 eventos.
	const (
		operacoes = 6
		eventos   = operacoes * 2
	)
	carteiras := make([]string, operacoes/2)
	jogadores := make([]string, operacoes/2)
	for i := range carteiras {
		carteiras[i], jogadores[i] = abrirCarteiraNaInstancia(t, instancias[i], "100.00")
	}
	for i := range carteiras {
		corpo := corpoDeOperacao(provedor, fmt.Sprintf("txn-t6-%d", i), jogadores[i], carteiras[i], "BET", "10.00")
		_, resp := enviarOperacao(t, instancias[(i+1)%len(instancias)], token,
			fmt.Sprintf("%s:txn-t6-%d", provedor, i), corpo)
		if resp.status != http.StatusCreated {
			t.Fatalf("BET %d = %d, quer 201: %s", i, resp.status, resp.texto())
		}
	}

	// Todos os eventos saem da outbox, sem ninguém perdido pelo caminho.
	esperarAtende(t, "a outbox esvaziar", 120*time.Second, func() bool {
		return eventosPendentesNaOutbox(t) == 0
	})

	// Leitura em rodadas de 10: o SQS recusa `MaxNumberOfMessages` acima de 10
	// (a primeira versão desta asserção pediu 14 e o broker devolveu
	// `InvalidParameterValue` — régua errada, não defeito do sistema).
	var lidas []types.Message
	prazoLeitura := time.Now().Add(30 * time.Second)
	for len(lidas) < eventos && time.Now().Before(prazoLeitura) {
		lidas = append(lidas, fila.ler(t, 10)...)
	}
	if len(lidas) != eventos {
		t.Fatalf("mensagens publicadas = %d, quer %d (nenhum evento pode ser perdido)", len(lidas), eventos)
	}

	// A PARTIÇÃO: cada `eventId` aparece exatamente uma vez.
	porEvento := map[string]int{}
	porGrupo := map[string][]string{}
	for _, mensagem := range lidas {
		corpo := aws.ToString(mensagem.Body)
		id := eventIDDoCorpo(t, corpo)
		porEvento[id]++
		grupo := mensagem.Attributes["MessageGroupId"]
		porGrupo[grupo] = append(porGrupo[grupo], eventTypeDoCorpo(t, corpo))
	}
	if len(porEvento) != eventos {
		t.Fatalf("eventIds distintos = %d, quer %d", len(porEvento), eventos)
	}
	for id, quantas := range porEvento {
		if quantas != 1 {
			t.Fatalf("o evento %s foi publicado %d vezes por UMA publicação de cada (duplicado)", id, quantas)
		}
	}

	// Quem publicou o quê: o log de cada processo conta. A soma tem de fechar
	// exatamente em `eventos` — nenhuma publicação a mais (republicação indevida)
	// e nenhuma a menos.
	publicados := 0
	participantes := 0
	distribuicao := make([]string, 0, len(instancias))
	for _, inst := range instancias {
		quantos := inst.quantasVezesNoLog("outbox_evento_publicado")
		publicados += quantos
		if quantos > 0 {
			participantes++
		}
		distribuicao = append(distribuicao, fmt.Sprintf("%s=%d", inst.nome, quantos))
	}
	if publicados != eventos {
		t.Fatalf("publicações registradas nos logs = %d, quer %d (distribuição: %s)",
			publicados, eventos, strings.Join(distribuicao, ", "))
	}
	if participantes < 2 {
		t.Logf("AVISO: só %d processo(s) publicaram nesta execução (%s); a invariante testada é a "+
			"partição dos eventos, não a distribuição entre as réplicas",
			participantes, strings.Join(distribuicao, ", "))
	}

	// A ordem por carteira, reportada (ver o comentário do teste sobre o limite).
	ordemPreservada := 0
	for _, tipos := range porGrupo {
		esperado := true
		for i := 0; i < len(tipos); i += 2 {
			if i+1 >= len(tipos) || tipos[i] != "WagerTransactionProcessed" ||
				tipos[i+1] != "WalletBalanceChanged" {
				esperado = false
				break
			}
		}
		if esperado {
			ordemPreservada++
		}
	}

	t.Logf("T6: %d eventos publicados por %d publisher(es) reais (%s); 0 duplicados, 0 pendentes; "+
		"ordem por carteira preservada em %d de %d grupos",
		eventos, participantes, strings.Join(distribuicao, ", "), ordemPreservada, len(porGrupo))
}

// ---------------------------------------------------------------------------
// T7 (README §13.7): REFUND antes da referência, resolvido por OUTRA instância
// ---------------------------------------------------------------------------

// TestT7REFUNDAntesDaReferenciaResolvidoPorOutraInstancia repete o T7 da Fase 9
// com três processos REAIS, e acrescenta o que só a topologia multi-instância
// mostra: quem recebe o REFUND (api-1) e quem recebe a BET (api-3) NÃO são quem
// resolve a pendência (api-2, a única com o worker ligado).
//
// A outra metade do §13.7 — rejeição por expiração do TTL — foi provada na Fase 9
// (`TestReferenciaQueNuncaChegaExpiraEmREFERENCENOTFOUND`) contra o mesmo
// PostgreSQL real. Repeti-la aqui não acrescentaria nada: o mecanismo é o mesmo
// (`Retomar` + `REFERENCE_MAX_ATTEMPTS`/`REFERENCE_PENDING_MAX_AGE`), o estado é
// durável e não há nada de multi-instância na expiração. O que a topologia muda é
// QUEM resolve, e é isso que este teste isola.
func TestT7REFUNDAntesDaReferenciaResolvidoPorOutraInstancia(t *testing.T) {
	limpar(t)

	// Só a api-2 tem o worker de referências: é o que permite atribuir a
	// resolução a um processo específico.
	api1 := subirInstancias(t, 1, opcoesInstancia{Inicio: 1})[0]
	api2 := subirInstancias(t, 1, opcoesInstancia{Inicio: 2, Referencia: true})[0]
	api3 := subirInstancias(t, 1, opcoesInstancia{Inicio: 3})[0]

	token, provedor := credencialDeOperacao(t)
	carteira, jogador := abrirCarteiraNaInstancia(t, api1, "100.00")
	alvo := ids.WalletID(carteira)

	// O REFUND chega primeiro, por api-1, e fica pendente.
	corpoRefund := corpoDeOperacao(provedor, "txn-t7-refund", jogador, carteira, "REFUND", "25.00")
	corpoRefund["referenceExternalTransactionId"] = "txn-t7-bet"

	refund, respRefund := enviarOperacao(t, api1, token, provedor+":txn-t7-refund", corpoRefund)
	if respRefund.status != http.StatusAccepted {
		t.Fatalf("REFUND antes da BET = %d, quer 202: %s", respRefund.status, respRefund.texto())
	}
	if refund.Status != "PENDING_REFERENCE" {
		t.Fatalf("REFUND status = %s, quer PENDING_REFERENCE", refund.Status)
	}

	// A BET chega DEPOIS, por api-3 — outro processo, outro pool.
	corpoBet := corpoDeOperacao(provedor, "txn-t7-bet", jogador, carteira, "BET", "25.00")
	bet, respBet := enviarOperacao(t, api3, token, provedor+":txn-t7-bet", corpoBet)
	if respBet.status != http.StatusCreated {
		t.Fatalf("BET = %d, quer 201: %s", respBet.status, respBet.texto())
	}
	if bet.Balance == nil || bet.Balance.Amount != "75.00" {
		t.Fatalf("saldo da BET = %v, quer 75.00", bet.Balance)
	}

	// api-2 resolve sozinha, depois do backoff de 5s que o caso de uso agendou.
	chaveRefund := provedor + ":txn-t7-refund"
	esperarAtende(t, "a pendência ser resolvida pelo worker da api-2", 120*time.Second, func() bool {
		status, _, _, _ := transacaoNoBanco(t, provedor, chaveRefund)
		return status == "PROCESSED"
	})

	if !api2.temNoLog("reference_resolvida") {
		t.Fatalf("a resolução não aparece no log da api-2; log:\n%s", api2.log.texto())
	}
	if api1.temNoLog("reference_resolvida") || api3.temNoLog("reference_resolvida") {
		t.Fatal("uma instância sem o worker registrou resolução de referência")
	}

	// E as duas que não resolveram continuam atendendo normalmente.
	if !api1.estaPronta(t) || !api3.estaPronta(t) {
		t.Fatal("uma das instâncias sem worker deixou de atender depois da resolução")
	}

	// Resultado financeiro: 100.00 → BET 25.00 → REFUND 25.00 = 100.00.
	exigirSaldoDaCarteira(t, carteira, 10000)
	exigirLancamentosPorID(t, carteira, "CREDIT 100.00", "DEBIT 25.00", "CREDIT 25.00")
	if versao := versaoNoBanco(t, alvo); versao != 3 {
		t.Fatalf("versão = %d, quer 3", versao)
	}
	exigirReconciliacaoConsistente(t, api3, carteira, 3)

	// O REFUND resolvido aponta para a BET, e o saldo devolvido é o do instante da
	// conclusão (100.00) — o replay dele devolveria esse mesmo saldo.
	replay, respReplay := enviarOperacao(t, api3, token, chaveRefund, corpoRefund)
	if respReplay.status != http.StatusOK || !replay.IdempotentReplay {
		t.Fatalf("replay do REFUND = %d replay=%v, quer 200 e true", respReplay.status, replay.IdempotentReplay)
	}
	if replay.Balance == nil || replay.Balance.Amount != "100.00" {
		t.Fatalf("replay do REFUND devolveu %v, quer 100.00", replay.Balance)
	}

	t.Logf("T7: REFUND recebido pela %s e BET pela %s; a pendência foi resolvida pela %s "+
		"(a única com o worker); saldo final 100.00, 3 lançamentos, reconciliação consistente",
		api1.nome, api3.nome, api2.nome)
}

// ---------------------------------------------------------------------------
// Auxiliares
// ---------------------------------------------------------------------------

// reconciliacao é o corpo de `POST /wallets/{id}/reconciliation`.
type reconciliacao struct {
	StoredBalance     dinheiro `json:"storedBalance"`
	CalculatedBalance dinheiro `json:"calculatedBalance"`
	Difference        dinheiro `json:"difference"`
	Consistent        bool     `json:"consistent"`
	CheckedEntries    int      `json:"checkedEntries"`
}

type dinheiro struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// reconciliar chama o endpoint de reconciliação de uma instância real.
func reconciliar(t *testing.T, inst *instancia, carteira string) reconciliacao {
	t.Helper()

	resp := inst.chamar(t, http.MethodPost, "/wallets/"+carteira+"/reconciliation",
		tokenDoKeycloak(t, clientInterno), nil, nil)
	if resp.status != http.StatusOK {
		t.Fatalf("%s: reconciliação = %d, quer 200: %s", inst.nome, resp.status, resp.texto())
	}

	var corpo reconciliacao
	resp.decodifica(t, &corpo)
	return corpo
}

// exigirReconciliacaoConsistente é a CONFERÊNCIA FINAL do README §13, a mesma em
// todos os cenários: saldo armazenado == soma de créditos − débitos do ledger.
func exigirReconciliacaoConsistente(t *testing.T, inst *instancia, carteira string, lancamentos int) {
	t.Helper()

	rec := reconciliar(t, inst, carteira)
	if !rec.Consistent {
		t.Fatalf("reconciliação INCONSISTENTE em %s: armazenado %s, calculado %s, diferença %s",
			inst.nome, rec.StoredBalance.Amount, rec.CalculatedBalance.Amount, rec.Difference.Amount)
	}
	if rec.StoredBalance.Amount != rec.CalculatedBalance.Amount {
		t.Fatalf("saldo armazenado %s != calculado %s", rec.StoredBalance.Amount, rec.CalculatedBalance.Amount)
	}
	if rec.Difference.Amount != "0.00" {
		t.Fatalf("diferença = %s, quer 0.00", rec.Difference.Amount)
	}
	if rec.CheckedEntries != lancamentos {
		t.Fatalf("checkedEntries = %d, quer %d", rec.CheckedEntries, lancamentos)
	}
}

// temNoLog diz se o processo registrou a mensagem (o `msg=` do log estruturado).
func (i *instancia) temNoLog(mensagem string) bool {
	return strings.Contains(i.log.texto(), "msg="+mensagem)
}

// quantasVezesNoLog conta ocorrências de uma mensagem no log do processo.
func (i *instancia) quantasVezesNoLog(mensagem string) int {
	return strings.Count(i.log.texto(), "msg="+mensagem)
}

// atributosDaFila devolve quantas mensagens estão VISÍVEIS e quantas estão em voo.
//
// É a leitura que distingue "a mensagem foi removida" de "a mensagem ainda está
// na fila, invisível porque alguém a recebeu e não apagou" — o estado exato de
// uma queda entre o commit e o delete.
func (s *sqsDeTeste) atributosDaFila(t *testing.T, queueURL string) (visiveis, invisiveis int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	saida, err := s.api.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl: &queueURL,
		AttributeNames: []types.QueueAttributeName{
			types.QueueAttributeNameApproximateNumberOfMessages,
			types.QueueAttributeNameApproximateNumberOfMessagesNotVisible,
		},
	})
	if err != nil {
		t.Fatalf("lendo atributos da fila: %v", err)
	}
	visiveis, _ = strconv.Atoi(saida.Attributes[string(types.QueueAttributeNameApproximateNumberOfMessages)])
	invisiveis, _ = strconv.Atoi(saida.Attributes[string(types.QueueAttributeNameApproximateNumberOfMessagesNotVisible)])
	return visiveis, invisiveis
}

// ajustarVisibilidadeDaFila muda o `VisibilityTimeout` da fila e devolve a função
// que restaura o valor original.
//
// # Por que mexer na fila, em vez de forçar a visibilidade da mensagem
//
// A tentativa óbvia — `ChangeMessageVisibility(handle, 0)` — foi MEDIDA e não
// funciona no LocalStack: a chamada devolve sucesso e a mensagem continua
// invisível até o prazo ORIGINAL da fila (60s no compose), o que fazia o T5
// demorar 60 segundos esperando a reentrega. Encurtar o prazo da fila é, além de
// mais rápido, mais FIEL ao que acontece numa queda: a mensagem volta sozinha
// quando o `VisibilityTimeout` vence, sem ninguém empurrá-la.
//
// A fila é compartilhada com os outros testes deste pacote, que rodam em série
// (o pacote é um processo, e o lock consultivo do TestMain serializa os pacotes),
// então o valor é restaurado no `t.Cleanup`.
func (s *sqsDeTeste) ajustarVisibilidadeDaFila(t *testing.T, queueURL string, segundos int32) func() {
	t.Helper()

	ler := func() int32 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		saida, err := s.api.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
			QueueUrl:       &queueURL,
			AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameVisibilityTimeout},
		})
		if err != nil {
			t.Fatalf("lendo o visibility timeout da fila: %v", err)
		}
		valor, err := strconv.Atoi(saida.Attributes[string(types.QueueAttributeNameVisibilityTimeout)])
		if err != nil {
			return 60
		}
		return int32(valor)
	}

	definir := func(valor int32) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if _, err := s.api.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{
			QueueUrl: &queueURL,
			Attributes: map[string]string{
				string(types.QueueAttributeNameVisibilityTimeout): strconv.Itoa(int(valor)),
			},
		}); err != nil {
			t.Fatalf("ajustando o visibility timeout da fila: %v", err)
		}
	}

	original := ler()
	definir(segundos)

	return func() { definir(original) }
}
