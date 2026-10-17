// Package sqs é o adaptador do AWS SQS: recebe, apaga, ajusta visibilidade e
// envia para a DLQ.
//
// # Por que ele é separado de `internal/consumer`
//
// Aqui mora o PROTOCOLO do broker (cliente AWS, nomes de atributo, batch), e lá
// mora a POLÍTICA (quando repetir, quando desistir, como encerrar no SIGTERM).
// Misturar os dois deixaria a política impossível de testar sem LocalStack — e é
// justamente ela que tem as decisões que valem a pena discutir.
//
// O consumidor declara a porta de que precisa (`consumer.Fila`) e este pacote a
// implementa: o consumidor não importa o SDK da AWS, e o adaptador não importa
// regra de negócio.
package sqs

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go"

	"github.com/FeliPoxZ/jungle_desafio/internal/config"
)

// Message é uma mensagem recebida, no formato que o consumidor entende.
type Message struct {
	// ID é a identidade durável da mensagem (o `MessageId` do SQS).
	ID string
	// Body é o corpo cru.
	Body []byte
	// ReceiveCount é quantas vezes a mensagem já foi entregue (inclusive agora).
	ReceiveCount int
	// ReceiptHandle é o comprovante de posse desta ENTREGA.
	//
	// Não é o identificador da mensagem: ele muda a cada recebimento, e é ele que
	// `Delete` e `ChangeVisibility` exigem. Guardar o handle errado (de outra
	// entrega) faz a operação falhar.
	ReceiptHandle string
	// GroupID é o `MessageGroupId` da fila FIFO — no nosso caso, a carteira.
	GroupID string
}

// Cliente é o cliente do SQS já configurado (região, credenciais, endpoint).
type Cliente struct {
	api *awssqs.Client
}

// NewClient monta o cliente a partir da configuração.
//
// O endpoint é configurável porque o LocalStack não é a AWS: apontar
// `SQS_ENDPOINT` para `http://localhost:4566` faz o SDK assinar e enviar para lá,
// sem tocar em credencial real. As credenciais são fixas em desenvolvimento e, em
// produção, vêm da cadeia padrão do SDK (variável de ambiente, perfil, role da
// instância).
func NewClient(cfg *config.Config) *Cliente {
	opcoes := awssqs.Options{
		Region: cfg.SQS.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.SQS.AccessKeyID, cfg.SQS.SecretAccessKey, ""),
	}
	if endpoint := strings.TrimSpace(cfg.SQS.Endpoint); endpoint != "" {
		opcoes.BaseEndpoint = aws.String(endpoint)
	}
	return &Cliente{api: awssqs.New(opcoes)}
}

// QueueURL monta a URL da fila a partir do endpoint e do nome.
//
// O nome vem da configuração (`SQS_TRANSACTIONS_QUEUE`) e a URL é derivada: com
// LocalStack o formato é `<endpoint>/000000000000/<nome>`. Em vez de fixar o
// formato, a montagem é preguiçosa e só acontece quando o endpoint é local — em
// produção (sem `SQS_ENDPOINT`), quem resolve o nome é o `GetQueueUrl` do SDK.
//
// Devolve string vazia quando não há endpoint configurado, e nesse caso o
// consumidor precisa da URL explícita.
func QueueURL(cfg *config.Config, nome string) string {
	endpoint := strings.TrimRight(strings.TrimSpace(cfg.SQS.Endpoint), "/")
	if endpoint == "" {
		return ""
	}
	return endpoint + "/000000000000/" + nome
}

// Receive busca até `max` mensagens, esperando até `waitSeconds` por elas.
//
// Long polling (`WaitTimeSeconds > 0`) é obrigatório na prática: sem ele, cada
// chamada vazia volta na hora e o consumidor transforma a fila em um laço de
// requisições — gastando API, CPU e dinheiro.
func (c *Cliente) Receive(ctx context.Context, queueURL string, max int32, waitSeconds int32) ([]Message, error) {
	saida, err := c.api.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl:            &queueURL,
		MaxNumberOfMessages: max,
		WaitTimeSeconds:     waitSeconds,
		MessageSystemAttributeNames: []types.MessageSystemAttributeName{
			types.MessageSystemAttributeNameApproximateReceiveCount,
			types.MessageSystemAttributeNameMessageGroupId,
		},
	})
	if err != nil {
		return nil, err
	}

	mensagens := make([]Message, 0, len(saida.Messages))
	for _, m := range saida.Messages {
		mensagens = append(mensagens, Message{
			ID:            texto(m.MessageId),
			Body:          []byte(texto(m.Body)),
			ReceiveCount:  contagem(m.Attributes),
			ReceiptHandle: texto(m.ReceiptHandle),
			GroupID:       m.Attributes[string(types.MessageSystemAttributeNameMessageGroupId)],
		})
	}
	return mensagens, nil
}

// Delete remove as mensagens da fila.
//
// Em lote, e não uma por uma: um lote de 10 mensagens concluídas gastaria 10
// chamadas. O lote do SQS aceita até 10 entradas, e é o consumidor que garante
// isso.
func (c *Cliente) Delete(ctx context.Context, queueURL string, handles []string) error {
	if len(handles) == 0 {
		return nil
	}

	entradas := make([]types.DeleteMessageBatchRequestEntry, 0, len(handles))
	for i, handle := range handles {
		h := handle
		entradas = append(entradas, types.DeleteMessageBatchRequestEntry{
			// O identificador da entrada é do LOTE, não da mensagem: é o que o
			// SQS devolve no relatório de falhas para dizer qual item falhou.
			Id:            aws.String(itoa(i)),
			ReceiptHandle: &h,
		})
	}

	saida, err := c.api.DeleteMessageBatch(ctx, &awssqs.DeleteMessageBatchInput{
		QueueUrl: &queueURL,
		Entries:  entradas,
	})
	if err != nil {
		return err
	}
	if len(saida.Failed) > 0 {
		// Falha parcial: algumas mensagens continuam na fila e serão
		// reentregues. Não é erro fatal (a reentrega é inofensiva por causa da
		// inbox), mas precisa aparecer.
		return fmt.Errorf("sqs: %d de %d remoções falharam: %s",
			len(saida.Failed), len(entradas), texto(saida.Failed[0].Message))
	}
	return nil
}

// ChangeVisibility adia a próxima entrega das mensagens.
//
// É o RETRY do consumidor: a mensagem não é removida nem descartada, volta depois
// do prazo. `seconds = 0` devolve a mensagem imediatamente — usado no encerramento
// para liberar o que não deu tempo de concluir.
func (c *Cliente) ChangeVisibility(ctx context.Context, queueURL string, handles []string, seconds int32) error {
	if len(handles) == 0 {
		return nil
	}

	entradas := make([]types.ChangeMessageVisibilityBatchRequestEntry, 0, len(handles))
	for i, handle := range handles {
		h := handle
		entradas = append(entradas, types.ChangeMessageVisibilityBatchRequestEntry{
			Id:                aws.String(itoa(i)),
			ReceiptHandle:     &h,
			VisibilityTimeout: seconds,
		})
	}

	saida, err := c.api.ChangeMessageVisibilityBatch(ctx, &awssqs.ChangeMessageVisibilityBatchInput{
		QueueUrl: &queueURL,
		Entries:  entradas,
	})
	if err != nil {
		return err
	}
	if len(saida.Failed) > 0 {
		return fmt.Errorf("sqs: %d de %d ajustes de visibilidade falharam: %s",
			len(saida.Failed), len(entradas), texto(saida.Failed[0].Message))
	}
	return nil
}

// Send reenvia uma mensagem para outra fila (a DLQ).
//
// # Por que o envio é EXPLÍCITO, e não só o redrive do broker
//
// O `RedrivePolicy` cobre o caso "tentou demais": quando o
// `ApproximateReceiveCount` passa do `maxReceiveCount`, o SQS move a mensagem
// sozinho. Isso resolve falha transitória persistente, mas NÃO resolve a mensagem
// que nunca vai funcionar (corpo ilegível, hash divergente): ela ficaria
// recebendo e voltando até estourar o limite, gastando ciclos e atrasando as
// outras mensagens do mesmo grupo FIFO.
//
// Mandando explicitamente, a DLQ recebe na hora, com o motivo, e a fila principal
// segue limpa. O redrive continua sendo a rede de segurança para o que escapou.
func (c *Cliente) Send(ctx context.Context, queueURL string, body []byte, groupID, dedupID string) error {
	entrada := &awssqs.SendMessageInput{
		QueueUrl:    &queueURL,
		MessageBody: aws.String(string(body)),
	}

	// As duas filas são FIFO, e uma FIFO exige os dois campos. `MessageGroupId`
	// preserva a ordem DENTRO do grupo: duas mensagens da mesma carteira são
	// entregues na ordem de envio.
	if groupID != "" {
		entrada.MessageGroupId = aws.String(groupID)
	} else {
		// Sem grupo de origem (mensagem que nem foi parseada), a DLQ precisa de
		// algum grupo: usa-se um valor fixo, o que serializa as mensagens de
		// "lixo" entre si — e isso não atrapalha nada, porque elas não têm ordem
		// entre si de qualquer forma.
		entrada.MessageGroupId = aws.String("dlq")
	}
	if dedupID != "" {
		entrada.MessageDeduplicationId = aws.String(dedupID)
	}

	_, err := c.api.SendMessage(ctx, entrada)
	return err
}

// Ping verifica se o SQS responde, para o `/health/ready`.
//
// `GetQueueAttributes` e não `ListQueues`: a pergunta do readiness é "esta fila
// específica está acessível com estas credenciais?", e `ListQueues` poderia passar
// com a fila ausente — que é exatamente o estado que impede o serviço de consumir
// qualquer coisa.
func (c *Cliente) Ping(ctx context.Context, queueURL string) error {
	if queueURL == "" {
		return errors.New("sqs: URL da fila não configurada")
	}
	_, err := c.api.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl:       &queueURL,
		AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn},
	})
	return err
}

// IsNotFound informa se o erro é "a fila não existe".
//
// O readiness precisa distinguir "não alcanço o SQS" (transitório, o
// orquestrador tenta de novo) de "a fila não foi provisionada" (erro de
// ambiente, e o serviço nunca vai consumir nada até alguém criar a fila). Os
// dois derrubam a readiness, mas só o primeiro é motivo para esperar.
func IsNotFound(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "AWS.SimpleQueueService.NonExistentQueue"
	}
	return false
}

// --- auxiliares ---------------------------------------------------------------

func texto(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func itoa(i int) string { return strconv.Itoa(i) }

// contagem lê o `ApproximateReceiveCount`.
//
// O nome diz "approximate" porque o SQS não promete exatidão — e para nós isso é
// suficiente: o número serve para decidir "isto já tentou demais?" e para o
// diagnóstico, nunca para uma decisão financeira (essas vêm do banco).
func contagem(attrs map[string]string) int {
	bruto, existe := attrs[string(types.MessageSystemAttributeNameApproximateReceiveCount)]
	if !existe {
		return 1
	}
	n, err := strconv.Atoi(bruto)
	if err != nil || n < 1 {
		return 1
	}
	return n
}
