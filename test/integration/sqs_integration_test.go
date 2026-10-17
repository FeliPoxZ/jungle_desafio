//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/inbox"
	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	"github.com/FeliPoxZ/jungle_desafio/internal/consumer"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/infra/sqs"
	"github.com/FeliPoxZ/jungle_desafio/internal/platform/canonicaljson"
)

// Testes de integração do consumo por SQS, contra o LocalStack REAL.
//
// Eles exercitam o caminho completo: fila FIFO -> long polling -> inbox
// (deduplicação) -> MESMO caso de uso do HTTP -> PostgreSQL -> remoção da
// mensagem; e, no caminho de erro, backoff por visibility e DLQ.
//
// O README §13 pede "PostgreSQL, o IdP e LocalStack em containers reais". Aqui o
// SQS é o do container `jungleg-localstack`, com as filas provisionadas por
// `deploy/localstack/init-queues.sh` (que falhava silenciosamente até a Fase 8 —
// ver o cabeçalho daquele script).
//
//	docker compose up -d postgres keycloak localstack
//	TEST_DATABASE_URL=postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable
//	go test -count=1 -race -tags=integration -timeout 300s ./test/integration/

// ---------------------------------------------------------------------------
// Infra de teste (SQS real)
// ---------------------------------------------------------------------------

// sqsDeTeste é o cliente SQS apontado para o LocalStack do compose.
type sqsDeTeste struct {
	cfg    *config.Config
	api    *awssqs.Client
	urlTxn string
	urlDLQ string
	limpar sync.Once
}

// abrirSQS monta o cliente e as URLs das duas filas.
//
// Se o LocalStack não responder, o teste é PULADO (e não falha): a suíte de
// integração continua útil em quem só subiu o Postgres e o Keycloak.
func abrirSQS(t *testing.T) *sqsDeTeste {
	t.Helper()

	cfg := &config.Config{}
	cfg.SQS.Endpoint = envOu("SQS_ENDPOINT", "http://localhost:4566")
	cfg.SQS.Region = envOu("SQS_REGION", "us-east-1")
	cfg.SQS.AccessKeyID = envOu("AWS_ACCESS_KEY_ID", "test")
	cfg.SQS.SecretAccessKey = envOu("AWS_SECRET_ACCESS_KEY", "test")
	cfg.SQS.TransactionsQueue = "wager-transactions.fifo"
	cfg.SQS.TransactionsDLQ = "wager-transactions-dlq.fifo"
	cfg.SQS.WaitTime = 1 * time.Second
	cfg.SQS.BatchSize = 10
	cfg.SQS.ConsumerConcurrency = 4
	cfg.SQS.BaseBackoff = time.Second
	cfg.SQS.MaxBackoff = 2 * time.Second
	// Visibilidade CURTA nas duas filas.
	//
	// Numa fila FIFO real a visibilidade é 60s (o provisionamento do compose), e
	// isso é irrelevante em produção — mas num teste que roda depois de um que
	// deixou mensagem em voo, uma mensagem invisível de um grupo BLOQUEIA as
	// seguintes daquele grupo por um minuto, e o sintoma ("Receive devolve zero com
	// mensagem na fila") não aponta para a causa. Encurtar a visibilidade no
	// ambiente de TESTE deixa a suíte independente dos próprios resíduos. A fila do
	// compose continua com 60s.
	cfg.SQS.VisibilityTimeout = 2 * time.Second
	// Prazo curto: o encerramento gracioso é provado em teste de unidade, e aqui
	// o que importa é o caminho da mensagem.
	cfg.SQS.ShutdownGrace = 200 * time.Millisecond

	cliente := sqs.NewClient(cfg)
	urlTxn := sqs.QueueURL(cfg, cfg.SQS.TransactionsQueue)
	urlDLQ := sqs.QueueURL(cfg, cfg.SQS.TransactionsDLQ)

	api := awssqs.New(awssqs.Options{
		Region:       cfg.SQS.Region,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.SQS.AccessKeyID, cfg.SQS.SecretAccessKey, ""),
		BaseEndpoint: aws.String(cfg.SQS.Endpoint),
	})

	// Ping na fila: é o que distingue "LocalStack fora" de "teste quebrado".
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cliente.Ping(ctx, urlTxn); err != nil {
		t.Skipf("LocalStack/SQS não disponível (%v): pulando testes de consumo", err)
	}

	// O `VisibilityTimeout` da FILA vai para 2s no ambiente de TESTE.
	//
	// A fila entregue pelo compose usa 60s, que é o valor certo em produção. Nos
	// testes, porém, a suíte compartilha UMA fila: uma mensagem recebida e não
	// apagada (resíduo de um cenário, ou de uma queda de propósito) fica invisível
	// por um minuto e reaparece no meio do teste SEGUINTE — na Fase 10 isso
	// apareceu como `SQSMensagemProcessaEMovimentaCarteira` falhando por "a fila
	// ainda tem 1 mensagem" e como uma reentrega alheia no T5. Com 2s, o resíduo
	// volta logo e `drenar` o remove antes do cenário começar.
	//
	// É idempotente e não muda o que se prova: os consumidores de produção TÊM o
	// próprio `SQS_VISIBILITY_TIMEOUT` para o retry, e a invisibilidade inicial é
	// só uma espera.
	if _, err := api.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{
		QueueUrl: &urlTxn,
		Attributes: map[string]string{
			string(types.QueueAttributeNameVisibilityTimeout): "2",
		},
	}); err != nil {
		t.Skipf("não foi possível ajustar o visibility timeout da fila de teste: %v", err)
	}

	return &sqsDeTeste{cfg: cfg, api: api, urlTxn: urlTxn, urlDLQ: urlDLQ}
}

// esvaziarFila apaga tudo o que estiver nas duas filas.
//
// Os testes compartilham o LocalStack do compose, e uma mensagem deixada por um
// teste anterior (por exemplo, uma que foi para a DLQ) faria a asserção do
// próximo falhar por um motivo que não é dele.
func (s *sqsDeTeste) esvaziarFila(t *testing.T, queueURL string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for {
		saida, err := s.api.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
			QueueUrl:            &queueURL,
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     1,
		})
		if err != nil || len(saida.Messages) == 0 {
			return
		}
		for _, m := range saida.Messages {
			_, _ = s.api.DeleteMessage(ctx, &awssqs.DeleteMessageInput{
				QueueUrl: &queueURL, ReceiptHandle: m.ReceiptHandle,
			})
		}
	}
}

// enviar publica uma mensagem na fila de entrada.
//
// # O `MessageGroupId` é ÚNICO por mensagem, e isso não é detalhe de teste
//
// Numa fila FIFO, as mensagens do MESMO grupo são entregues em ORDEM, e uma
// mensagem que está em voo (recebida e ainda não removida) **BLOQUEIA as
// seguintes do mesmo grupo**: elas não são entregues enquanto a primeira não for
// removida ou o visibility timeout (60s nesta fila) vencer.
//
// Com um grupo fixo, a suíte ficava refém dos próprios resíduos: uma mensagem
// deixada em voo por um teste bloqueava a do teste seguinte, e o sintoma era
// `Receive` devolvendo ZERO com a mensagem parada na fila — nada parecido com a
// causa (foi preciso despejar o log do consumidor e ver "mensagens=0" em laço
// para chegar nela). Um grupo por mensagem elimina o acoplamento entre testes sem
// enfraquecer o que se está provando: a deduplicação da inbox é por
// `(consumer, messageId)`, e não tem relação com o grupo.
//
// O `MessageDeduplicationId` também é explícito porque a fila é FIFO com
// `ContentBasedDeduplication=false` — decisão do projeto: a deduplicação que vale
// é a NOSSA (chave financeira + inbox), não a do broker. Com o dedup id igual, o
// SQS descartaria a segunda; com ids diferentes, ela chega e a inbox a reconhece.
func (s *sqsDeTeste) enviar(t *testing.T, corpo string, dedupID string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	saida, err := s.api.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl:       &s.urlTxn,
		MessageBody:    aws.String(corpo),
		MessageGroupId: aws.String("grupo-" + dedupID),
		// O dedup id leva um NONCE por envio: com o valor repetido entre execuções
		// da suíte, o SQS (que deduplica por 5 minutos) devolve o `MessageId` da
		// mensagem ORIGINAL — uma que já foi consumida e não existe mais — e o
		// `Receive` seguinte volta zero com a fila vazia. O sintoma não tem
		// nenhuma relação com a causa, e custou uma sessão de depuração.
		MessageDeduplicationId: aws.String(dedupID + "-" + novoUUID()),
	})
	if err != nil {
		t.Fatalf("enviando mensagem para a fila: %v", err)
	}
	return aws.ToString(saida.MessageId)
}

// mensagensNaDLQ devolve quantas mensagens estão na fila morta.
func (s *sqsDeTeste) mensagensNaDLQ(t *testing.T) int {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	total := 0
	for {
		saida, err := s.api.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
			QueueUrl:            &s.urlDLQ,
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     1,
			MessageSystemAttributeNames: []types.MessageSystemAttributeName{
				types.MessageSystemAttributeNameApproximateReceiveCount,
			},
		})
		if err != nil {
			t.Fatalf("lendo a DLQ: %v", err)
		}
		if len(saida.Messages) == 0 {
			return total
		}
		total += len(saida.Messages)
		for _, m := range saida.Messages {
			// Não apaga: a asserção pode querer o corpo depois.
			_ = m
		}
		return total
	}
}

// ---------------------------------------------------------------------------
// Consumidor de teste
// ---------------------------------------------------------------------------

// rodarConsumidor sobe UM consumidor sobre o LocalStack e o derruba no fim.
//
// O log vai para um buffer em vez de `io.Discard`: quando um teste desta suíte
// falha, a PRIMEIRA pergunta é "o consumidor recebeu a mensagem, e o que ele fez
// com ela?" — e sem o log a depuração vira adivinhação. O buffer é despejado
// automaticamente na falha.
func (s *sqsDeTeste) rodarConsumidor(t *testing.T) func() {
	t.Helper()

	var (
		mu    sync.Mutex
		saida strings.Builder
	)
	logger := slog.New(slog.NewTextHandler(&bufferProtegido{mu: &mu, destino: &saida}, &slog.HandlerOptions{Level: slog.LevelDebug}))

	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		t.Logf("log do consumidor:\n%s", saida.String())
	})

	processador := inbox.NovoProcessador(servico)
	consumidor := consumer.New(consumer.Options{
		Fila:        sqs.NovoConsumidorFila(sqs.NewClient(s.cfg)),
		Processador: processador,
		Config:      s.cfg,
		URLDaFila:   s.urlTxn,
		URLDaDLQ:    s.urlDLQ,
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

	return parar
}

// bufferProtegido é um `io.Writer` com mutex: o consumidor escreve de várias
// goroutines, e o `-race` reprovaria o `strings.Builder` sem proteção.
type bufferProtegido struct {
	mu      *sync.Mutex
	destino *strings.Builder
}

func (b *bufferProtegido) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.destino.Write(p)
}

// ---------------------------------------------------------------------------
// Helpers de asserção
// ---------------------------------------------------------------------------

// esperarAtende espera uma condição ficar verdadeira.
//
// O consumo é ASSÍNCRONO: depois de enviar para a fila não há como saber quando a
// mensagem foi processada a não ser observando o banco. Esperar por condição (em
// vez de dormir um tempo fixo) mantém o teste estável e rápido.
func esperarAtende(t *testing.T, descricao string, prazo time.Duration, condicao func() bool) {
	t.Helper()

	fim := time.Now().Add(prazo)
	for time.Now().Before(fim) {
		if condicao() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("a condição não foi atendida em %v: %s", prazo, descricao)
}

// estadoDaInbox lê o registro da mensagem direto do banco.
func estadoDaInbox(t *testing.T, messageID string) (status string, failureCode string, attempts int, existe bool) {
	t.Helper()

	var (
		st, fc *string
		tent   int
	)
	err := poolTeste.QueryRow(context.Background(),
		`SELECT status, failure_code, attempts FROM inbox_messages
		 WHERE consumer_name = $1 AND message_id = $2`,
		inbox.ConsumerName, messageID).Scan(&st, &fc, &tent)

	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return "", "", 0, false
		}
		t.Fatalf("lendo a inbox: %v", err)
	}

	if st != nil {
		status = *st
	}
	if fc != nil {
		failureCode = *fc
	}
	return status, failureCode, tent, true
}

// contarInbox devolve quantos registros a inbox tem para este consumidor.
func contarInbox(t *testing.T) int {
	t.Helper()

	var n int
	if err := poolTeste.QueryRow(context.Background(),
		`SELECT count(*) FROM inbox_messages WHERE consumer_name = $1`,
		inbox.ConsumerName).Scan(&n); err != nil {
		t.Fatalf("contando a inbox: %v", err)
	}
	return n
}

// hashDoCorpo reproduz o `body_hash` da inbox para um corpo.
//
// É a MESMA construção do processador (canonicaljson de `{"sha256": <digest>}`):
// sem ela, o teste não conseguiria preparar um registro "já consumido" com o hash
// certo, e a reentrega legítima viraria "corpo divergente" — que é o oposto do que
// se quer provar.
func hashDoCorpo(t *testing.T, corpo string) string {
	t.Helper()

	soma := sha256.Sum256([]byte(corpo))
	hash, err := canonicaljson.Hash(map[string]any{"sha256": hex.EncodeToString(soma[:])})
	if err != nil {
		t.Fatalf("calculando o hash do corpo: %v", err)
	}
	return hash
}

// registrarInboxComoProcessada simula "consumi e commitei, mas não removi da fila".
//
// É o estado de quem morreu entre o commit e o `DeleteMessage` — e o que a
// reentrega encontra.
func registrarInboxComoProcessada(t *testing.T, messageID, corpo string) {
	t.Helper()
	inserirInbox(t, messageID, hashDoCorpo(t, corpo), "PROCESSED")
}

// registrarInboxComHashDiferente cria o conflito: mesmo identificador, hash de
// corpo que NÃO é o que vai chegar.
func registrarInboxComHashDiferente(t *testing.T, messageID string) {
	t.Helper()
	inserirInbox(t, messageID, "hash-de-um-corpo-que-nao-e-o-que-chegou", "PROCESSED")
}

func inserirInbox(t *testing.T, messageID, hash, status string) {
	t.Helper()

	agora := time.Now().UTC()
	_, err := poolTeste.Exec(context.Background(),
		`INSERT INTO inbox_messages
		   (id, consumer_name, message_id, body_hash, status, received_at, completed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		novoUUID(), inbox.ConsumerName, messageID, hash, status, agora)
	if err != nil {
		t.Fatalf("preparando a inbox: %v", err)
	}
}

// mensagemParaOperacao monta o envelope do README §10.
//
// Os nomes dos campos são os MESMOS do corpo do `POST /wagering/transactions`:
// é isso que faz a mesma operação, vinda pelas duas portas, virar a mesma
// requisição — e o mesmo hash de idempotência (README §9).
//
// # O `messageId` do envelope NÃO é preenchido, e isso é decisão
//
// O corpo de exemplo do README §10 traz `"messageId": "msg-123"`, mas quem ATRIBUI
// esse valor é o broker, no `SendMessage` — o produtor não tem como conhecê-lo
// antes de enviar (ele só existe na resposta do envio). Por isso o campo é
// OPCIONAL: quando vem, é conferido contra o identificador da entrega e
// divergência é recusada como mensagem inválida; a identidade durável do CONSUMO
// é a do broker (ver `inbox.operacao`).
//
// Preencher os dois exigiria enviar, ler o `MessageId` devolvido e reenviar o
// corpo com ele dentro — o que colocaria DUAS mensagens na fila.
func mensagemParaOperacao(providerID, externa, chave, playerID, walletID, roundID, gameID, kind, valor string) string {
	corpo := map[string]any{
		"type":       inbox.TipoEsperado,
		"occurredAt": "2026-09-08T12:00:00.000Z",
		"data": map[string]any{
			"providerId":            providerID,
			"externalTransactionId": externa,
			"idempotencyKey":        chave,
			"playerId":              playerID,
			"walletId":              walletID,
			"roundId":               roundID,
			"gameId":                gameID,
			"kind":                  kind,
			"money":                 map[string]any{"amount": valor, "currency": "BRL"},
		},
	}

	bruto, err := json.Marshal(corpo)
	if err != nil {
		panic(err)
	}
	return string(bruto)
}

// carteiraComSaldo abre uma carteira PELA REGRA DE NEGÓCIO e devolve id e jogador.
//
// # Por que `OpenWallet` e não um INSERT direto
//
// A régua do README §9 para a leitura do ledger inclui a ABERTURA: o exemplo do
// enunciado ("abertura 1000.00 + BET 25.00 → checkedEntries: 2") só fecha quando a
// carteira nasceu pelo caminho de negócio, que grava o lançamento de crédito
// partindo de `0.00`.
//
// Um `INSERT` direto de carteira (como faz o `carteiraNova` de outros testes)
// cria saldo SEM lançamento — o que é útil para testar um débito isolado, mas
// deixa a carteira irreconciliável de propósito. Usar esse atalho aqui faria os
// testes de SQS afirmarem "1 lançamento" enquanto o README descreve 2, e a
// diferença pareceria um defeito do consumo.
func carteiraComSaldo(t *testing.T, saldo string) (string, string) {
	t.Helper()

	playerID := novoUUID()

	res, err := servico.OpenWallet(context.Background(), appwagering.OpenWalletRequest{
		PlayerID:       ids.PlayerID(playerID),
		InitialBalance: brl(t, saldo),
		IdempotencyKey: "abertura-" + playerID,
	})
	if err != nil {
		t.Fatalf("abrir carteira: %v", err)
	}
	return string(res.WalletID), string(res.PlayerID)
}

// ---------------------------------------------------------------------------
// 1. Caminho completo: SQS -> inbox -> caso de uso -> PostgreSQL
// ---------------------------------------------------------------------------

// Uma mensagem na fila movimenta a carteira e é removida depois do commit.
func TestSQSMensagemProcessaEMovimentaCarteira(t *testing.T) {
	limpar(t)
	s := abrirSQS(t)
	s.esvaziarFila(t, s.urlTxn)
	s.esvaziarFila(t, s.urlDLQ)

	walletID, playerID := carteiraComSaldo(t, "1000.00")

	corpo := mensagemParaOperacao("provider-a", "txn-sqs-1", "provider-a:txn-sqs-1",
		playerID, walletID, "round-1", "fortune-chimp", "BET", "25.00")

	parar := s.rodarConsumidor(t)
	// A identidade durável do consumo é o `MessageId` DO BROKER, que só existe
	// depois do envio — o envelope não o traz.
	idDaMensagem := s.enviar(t, corpo, "dedup-fluxo-1")

	esperarAtende(t, "a BET da fila ser aplicada", 30*time.Second, func() bool {
		return len(lancamentosNoBanco(t, ids.WalletID(walletID))) == 2
	})
	parar()

	// Saldo 975.00 e DOIS lançamentos (abertura + aposta): a mensagem foi aplicada
	// uma única vez.
	if saldo := saldoNoBanco(t, ids.WalletID(walletID)); saldo != 97500 {
		t.Errorf("saldo = %d, quer 97500", saldo)
	}
	if n := len(lancamentosNoBanco(t, ids.WalletID(walletID))); n != 2 {
		t.Errorf("lançamentos = %d, quer 2", n)
	}

	// A inbox registrou a mensagem como PROCESSED, com o instante de conclusão.
	status, failureCode, _, existe := estadoDaInbox(t, idDaMensagem)
	if !existe {
		t.Fatal("a mensagem não foi registrada na inbox")
	}
	if status != "PROCESSED" {
		t.Errorf("status na inbox = %q, quer PROCESSED (failureCode=%q)", status, failureCode)
	}

	// E a mensagem saiu da fila (não é mais entregue).
	if restantes := s.mensagensNaFila(t, s.urlTxn); restantes != 0 {
		t.Errorf("a fila ainda tem %d mensagem(ns) depois do processamento", restantes)
	}
}

// mensagensNaFila conta o que sobrou na fila.
func (s *sqsDeTeste) mensagensNaFila(t *testing.T, queueURL string) int {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	saida, err := s.api.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl:            &queueURL,
		MaxNumberOfMessages: 10,
		WaitTimeSeconds:     2,
	})
	if err != nil {
		t.Fatalf("lendo a fila: %v", err)
	}
	return len(saida.Messages)
}

// ---------------------------------------------------------------------------
// 2. Reentrega da mesma mensagem
// ---------------------------------------------------------------------------

// A MESMA mensagem (mesmo identificador do broker) entregue duas vezes é aplicada
// UMA vez.
//
// É o cenário do SQS *at-least-once*, e o que a inbox existe para cobrir.
//
// # Por que o teste INSERE o registro da inbox antes de enviar
//
// O `MessageId` é atribuído pelo broker: não há como pedir duas entregas do MESMO
// identificador pelo SDK (um segundo `SendMessage` gera outro). A forma de
// reproduzir a reentrega exata é deixar o registro já lá, com o MESMO hash do
// corpo, e então entregar a mensagem: é o estado do sistema depois de um consumo
// bem-sucedido cuja remoção da fila não aconteceu (o processo caiu entre o commit
// e o `Delete`).
//
// O caminho "mesma operação, duas entregas" (identificadores diferentes, mesma
// ENVELOPE) é coberto pelo teste seguinte, e o unitário
// (`TestProcessarReentregaNaoReaplica`) cobre a mesma identidade sem infra.
func TestSQSReentregaDaMesmaMensagemNaoReaplica(t *testing.T) {
	limpar(t)
	s := abrirSQS(t)
	s.esvaziarFila(t, s.urlTxn)
	s.esvaziarFila(t, s.urlDLQ)

	walletID, playerID := carteiraComSaldo(t, "1000.00")

	corpo := mensagemParaOperacao("provider-a", "txn-reentrega", "provider-a:txn-reentrega",
		playerID, walletID, "round-1", "fortune-chimp", "BET", "25.00")

	// Estado "já consumi e commitei, mas não removi da fila": o registro na inbox
	// com o hash do corpo que vai chegar.
	idDaMensagem := s.enviar(t, corpo, "dedup-reentrega-1")
	registrarInboxComoProcessada(t, idDaMensagem, corpo)

	parar := s.rodarConsumidor(t)
	parar()

	// A mensagem foi removida (nada a refazer) e NADA foi debitado: o corpo só
	// existe na inbox, não no ledger.
	esperarAtende(t, "a reentrega ser barrada pela inbox", 20*time.Second, func() bool {
		return s.mensagensNaFila(t, s.urlTxn) == 0
	})

	if n := len(lancamentosNoBanco(t, ids.WalletID(walletID))); n != 1 {
		t.Errorf("lançamentos = %d, quer 1 (só a abertura): a reentrega não pode debitar", n)
	}
	if saldo := saldoNoBanco(t, ids.WalletID(walletID)); saldo != 100000 {
		t.Errorf("saldo = %d, quer 100000 — a reentrega movimentou a carteira", saldo)
	}
	if n := s.mensagensNaDLQ(t); n != 0 {
		t.Errorf("a DLQ tem %d mensagem(ns): reentrega não é erro, é o caminho esperado", n)
	}
}

// A MESMA OPERAÇÃO enviada duas vezes (dois `MessageId` do broker, mesmo conteúdo)
// é aplicada UMA vez.
//
// As duas entregas passam pela inbox — são mensagens diferentes para o broker — e
// quem barra o segundo débito é a idempotência FINANCEIRA (a chave do corpo). É a
// defesa em profundidade do README §10: deduplicação de entrega **e** de negócio.
func TestSQSMesmaOperacaoDuasVezesNaoDuplicaDebito(t *testing.T) {
	limpar(t)
	s := abrirSQS(t)
	s.esvaziarFila(t, s.urlTxn)
	s.esvaziarFila(t, s.urlDLQ)

	walletID, playerID := carteiraComSaldo(t, "1000.00")

	corpo := mensagemParaOperacao("provider-a", "txn-reentrega-2", "provider-a:txn-reentrega-2",
		playerID, walletID, "round-1", "fortune-chimp", "BET", "25.00")

	parar := s.rodarConsumidor(t)

	// Dois envios com dedup ids DIFERENTES: a deduplicação do broker não barra
	// nenhum dos dois, então quem precisa barrar o segundo débito é a chave.
	s.enviar(t, corpo, "dedup-reentrega-2a")
	s.enviar(t, corpo, "dedup-reentrega-2b")

	esperarAtende(t, "a primeira entrega ser aplicada", 30*time.Second, func() bool {
		return len(lancamentosNoBanco(t, ids.WalletID(walletID))) == 2
	})
	// Dá tempo de a segunda ser processada (como replay) antes de parar.
	time.Sleep(2 * time.Second)
	parar()

	if n := len(lancamentosNoBanco(t, ids.WalletID(walletID))); n != 2 {
		t.Errorf("lançamentos = %d, quer 2 (abertura + UMA aposta)", n)
	}
	// UM débito: 1000 -> 975.
	if saldo := saldoNoBanco(t, ids.WalletID(walletID)); saldo != 97500 {
		t.Errorf("saldo = %d, quer 97500 — a segunda entrega debitou de novo", saldo)
	}
	if n := s.mensagensNaDLQ(t); n != 0 {
		t.Errorf("a DLQ tem %d mensagem(ns)", n)
	}
}

// ---------------------------------------------------------------------------
// 3. Corpo divergente para a mesma mensagem
// ---------------------------------------------------------------------------

// A MESMA identidade de mensagem com corpo diferente é conflito real, e vai para a
// DLQ.
//
// Não é reentrega: o SQS garante entrega "pelo menos uma vez", não "conteúdo
// diferente". Repetir não resolve.
//
// # Por que o teste INSERE o registro com outro hash antes de enviar
//
// O `MessageId` é do broker: não há como entregar duas mensagens com o MESMO
// identificador e corpos diferentes pelo SDK. O que se pode reproduzir é o estado
// que caracteriza o conflito — um registro na inbox cujo `body_hash` não é o do
// corpo que chegou — e é exatamente ele que o processador detecta. O caminho do
// DLQ (classificação do erro permanente, envio com grupo e dedup, remoção da fila
// principal) é infra REAL, e é isso que o teste prova contra o LocalStack.
func TestSQSCorpoDivergenteVaiParaDLQ(t *testing.T) {
	limpar(t)
	s := abrirSQS(t)
	s.esvaziarFila(t, s.urlTxn)
	s.esvaziarFila(t, s.urlDLQ)

	walletID, playerID := carteiraComSaldo(t, "1000.00")

	corpo := mensagemParaOperacao("provider-a", "txn-div-1", "provider-a:txn-div-1",
		playerID, walletID, "round-1", "fortune-chimp", "BET", "25.00")

	idDaMensagem := s.enviar(t, corpo, "dedup-divergente")
	registrarInboxComHashDiferente(t, idDaMensagem)

	parar := s.rodarConsumidor(t)

	esperarAtende(t, "a mensagem divergente chegar à DLQ", 30*time.Second, func() bool {
		return s.mensagensNaDLQ(t) > 0
	})
	parar()

	// NADA foi movimentado: o corpo divergente nunca é aplicado.
	if saldo := saldoNoBanco(t, ids.WalletID(walletID)); saldo != 100000 {
		t.Errorf("saldo = %d, quer 100000 — o corpo divergente foi aplicado", saldo)
	}
	if n := len(lancamentosNoBanco(t, ids.WalletID(walletID))); n != 1 {
		t.Errorf("lançamentos = %d, quer 1 (só a abertura)", n)
	}

	// E a mensagem saiu da fila principal (não fica girando).
	if restantes := s.mensagensNaFila(t, s.urlTxn); restantes != 0 {
		t.Errorf("a fila principal ainda tem %d mensagem(ns); a divergente deveria ter ido para a DLQ", restantes)
	}
}

// ---------------------------------------------------------------------------
// 4. Mensagem inválida
// ---------------------------------------------------------------------------

// Corpo que não é uma mensagem vai DIRETO para a DLQ, sem sujar a inbox.
//
// Numa fila FIFO isso importa mais do que parece: uma mensagem envenenada bloqueia
// o GRUPO inteiro enquanto gira até estourar o maxReceiveCount.
func TestSQSMensagemInvalidaVaiParaDLQ(t *testing.T) {
	limpar(t)
	s := abrirSQS(t)
	s.esvaziarFila(t, s.urlTxn)
	s.esvaziarFila(t, s.urlDLQ)

	// Não é JSON.
	s.rodarConsumidor(t)
	s.enviar(t, `{"messageId":"msg-lixo","type":"WagerTransactionRequested","data":{`, "dedup-lixo")

	esperarAtende(t, "a mensagem inválida chegar à DLQ", 30*time.Second, func() bool {
		return s.mensagensNaDLQ(t) > 0
	})

	if restantes := s.mensagensNaFila(t, s.urlTxn); restantes != 0 {
		t.Errorf("a fila principal ainda tem %d mensagem(ns)", restantes)
	}
	// Nada foi registrado na inbox: uma mensagem que jamais será aplicada não
	// deve ocupar a tabela de deduplicação. (Não dá para procurar por um
	// identificador específico: quem o atribui é o broker, e a mensagem foi
	// recusada ANTES de qualquer escrita.)
	if n := contarInbox(t); n != 0 {
		t.Errorf("a inbox tem %d registro(s) de uma mensagem inválida", n)
	}
	if n := contarLinhas(t, "wallet_ledger_entries"); n != 0 {
		t.Errorf("lançamentos = %d, quer 0 — mensagem inválida não movimenta nada", n)
	}
}

// ---------------------------------------------------------------------------
// 5. Rejeição de negócio é terminal
// ---------------------------------------------------------------------------

// Aposta sem saldo: a mensagem é REMOVIDA (não volta para a fila, não vai para a
// DLQ) e a inbox registra REJECTED com o código estável.
func TestSQSRejeicaoDeNegocioEhTerminal(t *testing.T) {
	limpar(t)
	s := abrirSQS(t)
	s.esvaziarFila(t, s.urlTxn)
	s.esvaziarFila(t, s.urlDLQ)

	walletID, playerID := carteiraComSaldo(t, "100.00")

	corpo := mensagemParaOperacao("provider-a", "txn-sem-saldo", "provider-a:txn-sem-saldo",
		playerID, walletID, "round-1", "fortune-chimp", "BET", "10000.00")

	parar := s.rodarConsumidor(t)
	idDaMensagem := s.enviar(t, corpo, "dedup-sem-saldo")

	esperarAtende(t, "a rejeição ser registrada", 30*time.Second, func() bool {
		status, _, _, existe := estadoDaInbox(t, idDaMensagem)
		return existe && status == "REJECTED"
	})
	parar()

	status, failureCode, _, existe := estadoDaInbox(t, idDaMensagem)
	if !existe {
		t.Fatal("a mensagem não foi registrada na inbox")
	}
	if status != "REJECTED" {
		t.Errorf("status = %q, quer REJECTED", status)
	}
	if failureCode != "INSUFFICIENT_FUNDS" {
		t.Errorf("failureCode = %q, quer INSUFFICIENT_FUNDS", failureCode)
	}

	// A carteira não se moveu, e a mensagem não ficou girando nem foi para a DLQ.
	if saldo := saldoNoBanco(t, ids.WalletID(walletID)); saldo != 10000 {
		t.Errorf("saldo = %d, quer 10000", saldo)
	}
	if restantes := s.mensagensNaFila(t, s.urlTxn); restantes != 0 {
		t.Errorf("a fila ainda tem %d mensagem(ns): rejeição terminal permite a remoção", restantes)
	}
	if n := s.mensagensNaDLQ(t); n != 0 {
		t.Errorf("a DLQ tem %d mensagem(ns): rejeição de negócio não é falha de infraestrutura", n)
	}
}

// ---------------------------------------------------------------------------
// 6. HTTP × SQS: a MESMA operação pelas duas portas
// ---------------------------------------------------------------------------

// A mesma operação enviada por SQS e por HTTP movimenta a carteira UMA vez.
//
// Esta é a prova da equivalência do hash de idempotência (README §9/§10): se as
// duas portas montassem a requisição de forma diferente, o hash seria diferente e
// o débito aconteceria duas vezes.
func TestSQSESHTTPCompartilhamAIdempotencia(t *testing.T) {
	limpar(t)
	s := abrirSQS(t)
	s.esvaziarFila(t, s.urlTxn)
	s.esvaziarFila(t, s.urlDLQ)

	walletID, playerID := carteiraComSaldo(t, "1000.00")
	servidorHTTP := apiDeTeste(t)

	const (
		externa = "txn-duas-portas"
		chave   = "provider-a:txn-duas-portas"
	)

	// (1) A operação chega por SQS e é aplicada.
	corpoSQS := mensagemParaOperacao("provider-a", externa, chave,
		playerID, walletID, "round-1", "fortune-chimp", "BET", "25.00")

	parar := s.rodarConsumidor(t)
	s.enviar(t, corpoSQS, "dedup-duas-portas")

	esperarAtende(t, "a operação do SQS ser aplicada", 30*time.Second, func() bool {
		return len(lancamentosNoBanco(t, ids.WalletID(walletID))) == 2
	})
	parar()

	// (2) A MESMA operação chega por HTTP, com a mesma chave e os mesmos campos.
	corpoHTTP := corpoDeOperacao("provider-a", externa, playerID, walletID, "BET", "25.00")
	resp := chamar(t, servidorHTTP, http.MethodPost, "/wagering/transactions",
		tokenDoKeycloak(t, clientProviderA), corpoHTTP,
		map[string]string{"Idempotency-Key": chave})

	if resp.status != http.StatusOK {
		t.Fatalf("a segunda porta respondeu %d, quer 200 (replay): %s", resp.status, resp.texto())
	}

	var replay struct {
		TransactionID    string `json:"transactionId"`
		Status           string `json:"status"`
		IdempotentReplay bool   `json:"idempotentReplay"`
	}
	resp.decodifica(t, &replay)

	if !replay.IdempotentReplay {
		t.Error("a operação vinda pelas duas portas NÃO foi reconhecida como a mesma: o hash divergiu")
	}
	if replay.Status != "PROCESSED" {
		t.Errorf("status = %q, quer PROCESSED", replay.Status)
	}

	// (3) O banco confirma: UM débito e o saldo de uma única aposta.
	if n := len(lancamentosNoBanco(t, ids.WalletID(walletID))); n != 2 {
		t.Errorf("lançamentos = %d, quer 2 (abertura + UMA aposta)", n)
	}
	if saldo := saldoNoBanco(t, ids.WalletID(walletID)); saldo != 97500 {
		t.Errorf("saldo = %d, quer 97500 — a operação foi aplicada duas vezes", saldo)
	}
}

// ---------------------------------------------------------------------------
// 7. Concorrência entre as duas portas
// ---------------------------------------------------------------------------

// SQS e HTTP entregando a MESMA operação AO MESMO TEMPO: um aplica, o outro
// reconhece como replay ou recebe conflito — nunca dois débitos.
//
// É a validação de concorrência HTTP × SQS que o README §10 pede.
func TestSQSConcorrenteComHTTPNaoDuplicaDebito(t *testing.T) {
	limpar(t)
	s := abrirSQS(t)
	s.esvaziarFila(t, s.urlTxn)
	s.esvaziarFila(t, s.urlDLQ)

	walletID, playerID := carteiraComSaldo(t, "1000.00")
	servidorHTTP := apiDeTeste(t)
	token := tokenDoKeycloak(t, clientProviderA)

	const (
		externa = "txn-concorrente"
		chave   = "provider-a:txn-concorrente"
	)

	parar := s.rodarConsumidor(t)

	// Dispara os dois ao mesmo tempo.
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		s.enviar(t, mensagemParaOperacao("provider-a", externa, chave,
			playerID, walletID, "round-1", "fortune-chimp", "BET", "25.00"), "dedup-concorrente")
	}()

	go func() {
		defer wg.Done()
		_ = chamar(t, servidorHTTP, http.MethodPost, "/wagering/transactions", token,
			corpoDeOperacao("provider-a", externa, playerID, walletID, "BET", "25.00"),
			map[string]string{"Idempotency-Key": chave})
	}()

	wg.Wait()

	esperarAtende(t, "a operação ser aplicada uma vez", 30*time.Second, func() bool {
		return len(lancamentosNoBanco(t, ids.WalletID(walletID))) == 2
	})
	// Dá tempo de a segunda porta terminar (replay/conflito) antes de parar.
	time.Sleep(1500 * time.Millisecond)
	parar()

	if n := len(lancamentosNoBanco(t, ids.WalletID(walletID))); n != 2 {
		t.Errorf("lançamentos = %d, quer 2 (abertura + UMA aposta)", n)
	}
	if saldo := saldoNoBanco(t, ids.WalletID(walletID)); saldo != 97500 {
		t.Errorf("saldo = %d, quer 97500", saldo)
	}
}

// ---------------------------------------------------------------------------
// 8. Readiness do SQS
// ---------------------------------------------------------------------------

// O health checker do SQS reporta UP com a fila acessível e DOWN com a fila
// errada — é ele que fecha a P5 (readiness de PostgreSQL e SQS).
func TestSQSReadinessReportaAFila(t *testing.T) {
	s := abrirSQS(t)

	// Fila existente: UP.
	bom := consumer.NovoHealthChecker(sqs.NovoConsumidorFila(sqs.NewClient(s.cfg)), s.urlTxn)
	if bom.Name() != "sqs" {
		t.Errorf("Name() = %q, quer sqs", bom.Name())
	}
	if err := bom.CheckReadiness(context.Background()); err != nil {
		t.Errorf("readiness com a fila acessível devolveu erro: %v", err)
	}

	// O caminho do consumidor: enviar e RECEBER pela mesma porta que o `Run` usa.
	//
	// Sem esta verificação, uma falha de `Receive` aparece nos outros testes como
	// "a condição não foi atendida" — que não diz NADA sobre a causa. Foi um teste
	// como este que localizou a diferença entre o `Receive` do teste e o do laço.
	fila := sqs.NovoConsumidorFila(sqs.NewClient(s.cfg))
	s.esvaziarFila(t, s.urlTxn)
	s.enviar(t, `{"messageId":"msg-receive-direto"}`, "dedup-receive-direto")

	// Espera por CONDIÇÃO, e não uma leitura só: sob carga (a suíte inteira
	// rodando em paralelo com outros pacotes) o SQS pode levar alguns segundos
	// para tornar a mensagem visível, e uma leitura isolada transforma isso em
	// falha intermitente — o tipo de teste que destrói a confiança na suíte.
	var mensagens []consumer.Message
	esperarAtende(t, "a mensagem ficar disponível para o Receive", 20*time.Second, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		lidas, err := fila.Receive(ctx, s.urlTxn, 10, 2)
		if err != nil || len(lidas) == 0 {
			return false
		}
		mensagens = lidas
		return true
	})

	if len(mensagens) != 1 {
		t.Fatalf("Receive devolveu %d mensagem(ns), quer 1", len(mensagens))
	}
	if mensagens[0].ID == "" || len(mensagens[0].Body) == 0 || mensagens[0].ReceiptHandle == "" {
		t.Errorf("mensagem recebida incompleta: %+v", mensagens[0])
	}
	if mensagens[0].ReceiveCount < 1 {
		t.Errorf("ReceiveCount = %d, quer >= 1", mensagens[0].ReceiveCount)
	}
	if mensagens[0].GroupID == "" {
		t.Error("MessageGroupId vazio: ele é o que preserva a ordem por carteira")
	}

	// Fila inexistente: DOWN. Um readiness que responde UP com a fila ausente
	// mandaria tráfego para uma instância que não consome nada.
	ruim := consumer.NovoHealthChecker(sqs.NovoConsumidorFila(sqs.NewClient(s.cfg)),
		s.cfg.SQS.Endpoint+"/000000000000/fila-que-nao-existe.fifo")
	if err := ruim.CheckReadiness(context.Background()); err == nil {
		t.Error("readiness com fila inexistente devolveu nil: deveria reportar DOWN")
	}
}
