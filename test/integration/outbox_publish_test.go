//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"

	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/infra/sqs"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
	"github.com/FeliPoxZ/jungle_desafio/internal/worker"
)

// filaDeEventos abre o cliente e o adaptador do DESTINO dos eventos.
//
// As filas de eventos são as mesmas do compose (`wallet-events.fifo` e a DLQ),
// pelo mesmo motivo das filas de entrada: o README §13 exige SQS real nos testes
// de integração. Se o LocalStack não responder, o teste é PULADO — e o motivo
// aparece na mensagem (fila ausente NÃO é o mesmo que teste quebrado).
type filaDeEventos struct {
	api     *awssqs.Client
	cliente *sqs.Cliente
	pub     *sqs.PublicadorEventos
	url     string
}

func abrirFilaDeEventos(t *testing.T) *filaDeEventos {
	t.Helper()

	cfg := &config.Config{}
	cfg.SQS.Endpoint = envOu("SQS_ENDPOINT", "http://localhost:4566")
	cfg.SQS.Region = envOu("SQS_REGION", "us-east-1")
	cfg.SQS.AccessKeyID = envOu("AWS_ACCESS_KEY_ID", "test")
	cfg.SQS.SecretAccessKey = envOu("AWS_SECRET_ACCESS_KEY", "test")
	cfg.SQS.EventsQueue = "wallet-events.fifo"
	cfg.SQS.EventsDLQ = "wallet-events-dlq.fifo"

	cliente := sqs.NewClient(cfg)
	url := sqs.QueueURL(cfg, cfg.SQS.EventsQueue)

	api := awssqs.New(awssqs.Options{
		Region:       cfg.SQS.Region,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.SQS.AccessKeyID, cfg.SQS.SecretAccessKey, ""),
		BaseEndpoint: aws.String(cfg.SQS.Endpoint),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cliente.Ping(ctx, url); err != nil {
		t.Skipf("LocalStack/SQS não disponível em %s (%v): pulando a publicação real", url, err)
	}

	return &filaDeEventos{api: api, cliente: cliente, pub: sqs.NovoPublicadorEventos(cliente, url), url: url}
}

// esvaziar apaga tudo o que estiver na fila de eventos.
//
// Os testes compartilham o LocalStack do compose: uma mensagem deixada pelo teste
// anterior faria a asserção do próximo falhar por um motivo que não é dele.
func (f *filaDeEventos) esvaziar(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for {
		saida, err := f.api.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
			QueueUrl:            &f.url,
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     1,
		})
		if err != nil || len(saida.Messages) == 0 {
			return
		}
		for _, m := range saida.Messages {
			_, _ = f.api.DeleteMessage(ctx, &awssqs.DeleteMessageInput{
				QueueUrl: &f.url, ReceiptHandle: m.ReceiptHandle,
			})
		}
	}
}

// ler devolve até `max` mensagens da fila de eventos, já removendo as lidas.
func (f *filaDeEventos) ler(t *testing.T, max int32) []types.Message {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var lidas []types.Message
	prazo := time.Now().Add(10 * time.Second)

	for len(lidas) < int(max) && time.Now().Before(prazo) {
		saida, err := f.api.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
			QueueUrl:              &f.url,
			MaxNumberOfMessages:   max,
			WaitTimeSeconds:       2,
			MessageAttributeNames: []string{"All"},
			MessageSystemAttributeNames: []types.MessageSystemAttributeName{
				types.MessageSystemAttributeNameMessageGroupId,
			},
		})
		if err != nil {
			t.Fatalf("lendo a fila de eventos: %v", err)
		}
		for _, m := range saida.Messages {
			lidas = append(lidas, m)
			// Remover na hora: a fila é FIFO e uma mensagem EM VOO bloqueia as
			// seguintes do MESMO grupo (a mesma armadilha que custou tempo na
			// Fase 8). Sem apagar, o segundo evento da carteira não seria
			// entregue dentro do prazo do teste.
			if _, err := f.api.DeleteMessage(ctx, &awssqs.DeleteMessageInput{
				QueueUrl: &f.url, ReceiptHandle: m.ReceiptHandle,
			}); err != nil {
				t.Fatalf("removendo a mensagem lida: %v", err)
			}
		}
	}
	return lidas
}

// TestOutboxPublicaNaFilaDeEventos é a prova central do Bloco 9a: os eventos
// gravados na MESMA transação da operação são publicados no destino provisionado,
// com o corpo IDÊNTICO ao snapshot da outbox e o roteamento por carteira.
//
// Não há dublê nenhum: Unit of Work, repositórios, caso de uso e adaptador do SQS
// são os de produção, e o broker é o LocalStack.
func TestOutboxPublicaNaFilaDeEventos(t *testing.T) {
	fila := abrirFilaDeEventos(t)
	limpar(t)
	fila.esvaziar(t)

	ctx := context.Background()

	// 1) Carteira pela REGRA DE NEGÓCIO: a abertura com saldo positivo grava a
	//    OPENING, o lançamento e DOIS eventos (Processed + BalanceChanged), no
	//    mesmo commit.
	aberta, err := servico.OpenWallet(ctx, appwagering.OpenWalletRequest{
		PlayerID:       ids.PlayerID(uuid.NewString()),
		InitialBalance: brl(t, "1000.00"),
		IdempotencyKey: "abertura-publicacao-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("OpenWallet: %v", err)
	}

	antes := eventosNaOutbox(t)
	if len(antes) != 2 {
		t.Fatalf("a abertura deveria ter deixado 2 eventos na outbox, veio %d", len(antes))
	}

	// 2) O publicador REAL, sobre o banco REAL e o SQS REAL.
	publicador := worker.NovoOutboxPublisher(worker.OutboxPublisherOptions{
		UOW:        uow,
		Publicador: fila.pub,
		Logger:     loggerDeTeste(),
		Metrics:    observability.NewMetrics(),
		Config: &config.Config{Outbox: config.OutboxConfig{
			BatchSize:     50,
			Lease:         30 * time.Second,
			PollInterval:  time.Second,
			MaxAttempts:   10,
			BaseBackoff:   2 * time.Second,
			MaxBackoff:    5 * time.Minute,
			ParkedBackoff: time.Hour,
		}},
	})

	publicados, err := publicador.PublicarPendentes(ctx)
	if err != nil {
		t.Fatalf("publicando o lote: %v", err)
	}
	if publicados != 2 {
		t.Fatalf("publicados = %d, quer 2 (Processed + BalanceChanged)", publicados)
	}

	// 3) A outbox ficou sem pendentes.
	for _, e := range eventosNaOutbox(t) {
		if !e.Publicado {
			t.Errorf("o evento %s continuou pendente depois da publicação", e.Tipo)
		}
	}

	// 4) As mensagens estão NA FILA, com o roteamento por carteira.
	mensagens := fila.ler(t, 2)
	if len(mensagens) != 2 {
		t.Fatalf("li %d mensagens da fila de eventos, quer 2", len(mensagens))
	}

	corpos := map[string]string{}
	for _, m := range mensagens {
		corpos[aws.ToString(m.MessageId)] = aws.ToString(m.Body)

		// Roteamento: o grupo é a CARTEIRA (é o que dá ordem por carteira na
		// fila FIFO), e os atributos permitem filtrar sem desserializar o corpo.
		if grupo := m.Attributes[string(types.MessageSystemAttributeNameMessageGroupId)]; grupo != string(aberta.WalletID) {
			t.Errorf("MessageGroupId = %q, quer a carteira %s", grupo, aberta.WalletID)
		}
		if tipo, ok := m.MessageAttributes["eventType"]; !ok || aws.ToString(tipo.StringValue) == "" {
			t.Errorf("o atributo eventType não veio na mensagem %s", aws.ToString(m.MessageId))
		}
		if id, ok := m.MessageAttributes["eventId"]; !ok || aws.ToString(id.StringValue) == "" {
			t.Errorf("o atributo eventId não veio na mensagem %s", aws.ToString(m.MessageId))
		}
	}

	// O corpo publicado tem de ser BYTE A BYTE o payload gravado na outbox — é o
	// que garante que a publicação não reinterpreta o passado.
	envelopes := map[string]map[string]any{}
	for _, corpo := range corpos {
		var envelope map[string]any
		if err := json.Unmarshal([]byte(corpo), &envelope); err != nil {
			t.Fatalf("corpo publicado não é o envelope esperado: %v (corpo: %s)", err, corpo)
		}
		tipo, ok := envelope["eventType"].(string)
		if !ok {
			t.Fatalf("corpo publicado sem eventType: %s", corpo)
		}
		envelopes[tipo] = envelope
	}

	for _, tipo := range []string{"WagerTransactionProcessed", "WalletBalanceChanged"} {
		envelope, existe := envelopes[tipo]
		if !existe {
			t.Fatalf("o evento %s não chegou à fila (recebidos: %v)", tipo, chavesDe(envelopes))
		}
		if envelope["eventId"] == nil || envelope["eventId"] == "" {
			t.Errorf("%s chegou sem eventId", tipo)
		}
		if envelope["aggregateId"] != string(aberta.WalletID) {
			t.Errorf("%s: aggregateId = %v, quer a carteira %s", tipo, envelope["aggregateId"], aberta.WalletID)
		}
	}

	// Os corpos da fila têm de casar com os payloads gravados, sem sobra nem
	// falta de byte.
	for _, e := range antes {
		if !contemCorpo(corpos, e.Payload) {
			t.Errorf("nenhuma mensagem da fila tem o corpo exato do evento %s gravado na outbox:\n%s", e.Tipo, e.Payload)
		}
	}
}

// TestOutboxRepublicacaoPreservaOEvento prova o requisito literal do README §11:
// publicar de novo o MESMO evento (o caso da queda entre a publicação e o
// `MarkPublished`) produz uma SEGUNDA mensagem na fila, com o `eventId`
// preservado e o corpo IDÊNTICO.
//
// É por isso que o `MessageDeduplicationId` é por TENTATIVA e não por evento: com
// o `eventId` como dedup id, o SQS engoliria esta segunda publicação dentro da
// janela de 5 minutos e o sistema dependeria da deduplicação do broker — que,
// passados os 5 minutos, não existe mais.
func TestOutboxRepublicacaoPreservaOEvento(t *testing.T) {
	fila := abrirFilaDeEventos(t)
	limpar(t)
	fila.esvaziar(t)

	ctx := context.Background()

	aberta, err := servico.OpenWallet(ctx, appwagering.OpenWalletRequest{
		PlayerID:       ids.PlayerID(uuid.NewString()),
		InitialBalance: brl(t, "100.00"),
		IdempotencyKey: "abertura-republicacao-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("OpenWallet: %v", err)
	}
	_ = aberta

	// Um único evento é o suficiente para provar a republicação; o segundo é
	// publicado depois.
	publicar := func() {
		t.Helper()
		publicador := worker.NovoOutboxPublisher(worker.OutboxPublisherOptions{
			UOW:        uow,
			Publicador: fila.pub,
			Logger:     loggerDeTeste(),
			Metrics:    observability.NewMetrics(),
			Config: &config.Config{Outbox: config.OutboxConfig{
				BatchSize: 1, Lease: 30 * time.Second, PollInterval: time.Second,
				MaxAttempts: 10, BaseBackoff: 2 * time.Second, MaxBackoff: 5 * time.Minute, ParkedBackoff: time.Hour,
			}},
		})
		if _, err := publicador.PublicarPendentes(ctx); err != nil {
			t.Fatalf("publicando: %v", err)
		}
	}

	publicar()
	primeiras := fila.ler(t, 1)
	if len(primeiras) != 1 {
		t.Fatalf("a primeira publicação deveria ter posto 1 mensagem na fila, veio %d", len(primeiras))
	}

	// Simula a queda ENTRE a publicação e o `MarkPublished`: o evento volta a ser
	// pendente e o lease é liberado, como se o processo tivesse morrido.
	if _, err := poolTeste.Exec(ctx,
		`UPDATE outbox_events SET published_at = NULL, available_at = now() - interval '1 second'
		 WHERE event_id = $1`, primeiroEventoID(t)); err != nil {
		t.Fatalf("devolvendo o evento para a outbox: %v", err)
	}

	publicar()
	segundas := fila.ler(t, 1)
	if len(segundas) != 1 {
		t.Fatalf("a republicação deveria ter posto uma SEGUNDA mensagem na fila, veio %d", len(segundas))
	}

	// Os dois envios são mensagens DIFERENTES para o broker (MessageId distinto)
	// e o mesmo evento no corpo.
	if aws.ToString(primeiras[0].MessageId) == aws.ToString(segundas[0].MessageId) {
		t.Errorf("os dois envios devolveram o MESMO MessageId (%s): o SQS deduplicou a republicação",
			aws.ToString(primeiras[0].MessageId))
	}
	if aws.ToString(primeiras[0].Body) != aws.ToString(segundas[0].Body) {
		t.Errorf("o corpo republicado mudou:\nprimeiro: %s\nsegundo:  %s",
			aws.ToString(primeiras[0].Body), aws.ToString(segundas[0].Body))
	}

	// O `eventId` do corpo é o mesmo — é ele que o consumidor usa para deduplicar.
	idPrimeiro := eventIDDoCorpo(t, aws.ToString(primeiras[0].Body))
	idSegundo := eventIDDoCorpo(t, aws.ToString(segundas[0].Body))
	if idPrimeiro == "" || idPrimeiro != idSegundo {
		t.Errorf("eventId do corpo mudou na republicação: %q e %q", idPrimeiro, idSegundo)
	}
}

// eventIDDoCorpo lê o `eventId` do envelope publicado.
func eventIDDoCorpo(t *testing.T, corpo string) string {
	t.Helper()

	var envelope struct {
		EventID string `json:"eventId"`
	}
	if err := json.Unmarshal([]byte(corpo), &envelope); err != nil {
		t.Fatalf("corpo publicado não é o envelope esperado: %v", err)
	}
	return envelope.EventID
}

// primeiroEventoID devolve o `event_id` do evento mais antigo da outbox.
func primeiroEventoID(t *testing.T) string {
	t.Helper()

	var id string
	if err := poolTeste.QueryRow(context.Background(),
		`SELECT event_id FROM outbox_events ORDER BY sequence LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("lendo o primeiro evento da outbox: %v", err)
	}
	return id
}

// contemCorpo diz se algum dos corpos é exatamente o payload esperado.
func contemCorpo(corpos map[string]string, esperado string) bool {
	for _, corpo := range corpos {
		if corpo == esperado {
			return true
		}
	}
	return false
}

func chavesDe(m map[string]map[string]any) []string {
	saida := make([]string, 0, len(m))
	for k := range m {
		saida = append(saida, k)
	}
	return saida
}

// loggerDeTeste mostra só o que interessa quando algo dá errado: avisos e erros.
//
// O silêncio total esconderia a CAUSA (a lição da Fase 8: o log do consumidor ia
// para `io.Discard` e a falha dizia apenas "a condição não foi atendida").
func loggerDeTeste() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}
