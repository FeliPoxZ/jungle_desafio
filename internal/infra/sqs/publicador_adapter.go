package sqs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/smithy-go"

	"github.com/FeliPoxZ/jungle_desafio/internal/worker"
)

// PublicadorEventos adapta o cliente do SQS à porta `worker.Publicador`.
//
// # Por que a conversão existe, se o adaptador poderia implementar a porta direto
//
// O cliente (`Cliente`) fala o PROTOCOLO do broker e recebe a URL da fila em cada
// chamada — ele é usado pelo consumidor (fila de entrada e DLQ) e precisa
// continuar genérico. O publicador da outbox tem UM destino, resolvido na
// composição. Este tipo guarda essa decisão (qual fila) num lugar só, e mantém a
// porta `worker.Publicador` sem URL no meio: a política de publicação não escolhe
// fila.
type PublicadorEventos struct {
	cliente  *Cliente
	queueURL string
}

// NovoPublicadorEventos monta o adaptador para uma fila específica.
func NovoPublicadorEventos(cliente *Cliente, queueURL string) *PublicadorEventos {
	return &PublicadorEventos{cliente: cliente, queueURL: queueURL}
}

var _ worker.Publicador = (*PublicadorEventos)(nil)

// Publicar envia o evento para a fila de saída.
func (a *PublicadorEventos) Publicar(ctx context.Context, e worker.Evento) error {
	if a.queueURL == "" {
		return errors.New("sqs: URL da fila de eventos não configurada")
	}
	return a.cliente.Publicar(ctx, a.queueURL, e)
}

// Publicar envia um evento para uma fila FIFO, com os atributos de roteamento.
//
// # As duas decisões desta função, e o que cada uma sustenta
//
//  1. **`MessageGroupId` = `aggregateId`** (a carteira). A fila é FIFO por
//     GRUPO: mensagens do mesmo grupo saem em ordem, e grupos diferentes seguem
//     em paralelo. Como o `aggregateId` dos eventos é o `walletId` e o lock da
//     carteira serializa as operações dela, a ordem de inserção na outbox
//     (`sequence`) é a ordem dos efeitos por carteira — e é essa ordem que o
//     consumidor enxerga. Um grupo fixo ("eventos") serializaria o sistema
//     inteiro; nenhum grupo não é aceito pela fila FIFO.
//
//  2. **`MessageDeduplicationId` por TENTATIVA, e não por evento.** Se fosse o
//     `eventId`, o SQS descartaria a REPUBLICAÇÃO dentro da janela de 5 minutos:
//     o evento que voltou do lease seria engolido pelo broker, e a mensagem
//     original (já entregue) continuaria sendo a única — exatamente o contrário
//     do que o README §11 pede ("republicações devem preservar o `eventId`").
//     A deduplicação que vale é a do CONSUMIDOR, pelo `eventId` do corpo, e ela
//     não tem janela de 5 minutos (é um registro durável). O nonce cobre o caso
//     em que o número da tentativa NÃO muda: queda entre a publicação e o
//     `MarkPublished` não incrementa `attempts`, e o segundo envio teria o mesmo
//     dedup id — e seria engolido.
//
// Os `MessageAttributes` (`eventType` e `eventId`) permitem ao consumidor
// rotear/filtrar sem desserializar o corpo. Eles NÃO substituem o corpo: o
// contrato de consumo é o payload, e os atributos são conveniência de roteamento.
func (c *Cliente) Publicar(ctx context.Context, queueURL string, e worker.Evento) error {
	grupo := e.AggregateID
	if grupo == "" {
		// Fila FIFO exige grupo. Um evento sem agregado não deveria existir (o
		// caso de uso sempre grava a carteira), mas se existir é melhor
		// entregá-lo num grupo próprio do que recusar o lote inteiro: ele não tem
		// ordem relativa com ninguém.
		grupo = "sem-agregado"
	}

	entrada := &awssqs.SendMessageInput{
		QueueUrl:               &queueURL,
		MessageBody:            aws.String(string(e.Payload)),
		MessageGroupId:         aws.String(grupo),
		MessageDeduplicationId: aws.String(fmt.Sprintf("%s-t%d-%s", e.EventID, e.Tentativas+1, nonce())),
		MessageAttributes: map[string]types.MessageAttributeValue{
			"eventType": atributo(e.Tipo),
			"eventId":   atributo(e.EventID),
		},
	}

	if _, err := c.api.SendMessage(ctx, entrada); err != nil {
		if recusaPermanente(err) {
			// O motivo de existir uma marca de "permanente": sem ela, um corpo
			// grande demais (ou com caractere que o SQS não aceita) gastaria as
			// dez tentativas antes de parar, atrasando os outros eventos do lote.
			return fmt.Errorf("%w: %w", worker.ErrEventoRecusado, err)
		}
		return err
	}
	return nil
}

// atributo monta um atributo de mensagem do tipo String.
func atributo(valor string) types.MessageAttributeValue {
	return types.MessageAttributeValue{
		DataType:    aws.String("String"),
		StringValue: aws.String(valor),
	}
}

// nonce é um sufixo único por envio.
//
// O relógio em nanossegundos basta: duas publicações do mesmo evento no mesmo
// nanossegundo não acontecem (a publicação é uma ida de rede). Não é usado
// `math/rand` aqui de propósito — o nonce precisa ser único, não estatístico, e
// uma semente fixa em teste o tornaria repetível.
func nonce() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// recusaPermanente diz se o broker recusou a MENSAGEM (e não a chamada).
//
// Só estes códigos são definitivos: repetir o mesmo corpo daria a mesma resposta.
// Qualquer outro erro — incluindo os que não são da API (`smithy.APIError`) —
// continua sendo transitório, porque a escolha conservadora é tentar de novo: o
// teto de tentativas limita o custo de estar errado.
func recusaPermanente(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorCode() {
	case "InvalidParameterValue",
		"AWS.SimpleQueueService.InvalidParameterValue",
		"InvalidMessageContents",
		"MessageTooLong":
		return true
	}
	return false
}
