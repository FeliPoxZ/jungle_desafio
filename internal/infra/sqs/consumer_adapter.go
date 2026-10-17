package sqs

import (
	"context"

	"github.com/FeliPoxZ/jungle_desafio/internal/consumer"
)

// ConsumidorFila adapta o cliente SQS à porta `consumer.Fila`.
//
// # Por que a conversão existe, se os tipos são iguais
//
// `sqs.Message` e `consumer.Message` têm os mesmos campos, mas são tipos
// DIFERENTES — e é isso que mantém a direção das dependências: o consumidor não
// importa o SDK da AWS, e o adaptador é quem conhece os dois lados. Se o
// consumidor usasse o tipo do adaptador, trocar SQS por outro broker obrigaria a
// mexer na política de consumo.
//
// O custo é uma cópia de slice por lote: irrelevante comparado a uma ida de rede,
// e o compilador mantém as duas structs alinhadas (uma mudança de campo quebra
// esta conversão em tempo de compilação).
type ConsumidorFila struct {
	cliente *Cliente
}

// NovoConsumidorFila monta o adaptador.
func NovoConsumidorFila(cliente *Cliente) *ConsumidorFila {
	return &ConsumidorFila{cliente: cliente}
}

var _ consumer.Fila = (*ConsumidorFila)(nil)

// Ping repassa a verificação de readiness.
func (a *ConsumidorFila) Ping(ctx context.Context, queueURL string) error {
	return a.cliente.Ping(ctx, queueURL)
}

// Receive busca mensagens e as converte para o tipo do consumidor.
func (a *ConsumidorFila) Receive(ctx context.Context, queueURL string, max int32, waitSeconds int32) ([]consumer.Message, error) {
	mensagens, err := a.cliente.Receive(ctx, queueURL, max, waitSeconds)
	if err != nil {
		return nil, err
	}

	convertidas := make([]consumer.Message, 0, len(mensagens))
	for _, m := range mensagens {
		convertidas = append(convertidas, consumer.Message{
			ID:            m.ID,
			Body:          m.Body,
			ReceiptHandle: m.ReceiptHandle,
			ReceiveCount:  m.ReceiveCount,
			GroupID:       m.GroupID,
		})
	}
	return convertidas, nil
}

// Delete remove mensagens concluídas.
func (a *ConsumidorFila) Delete(ctx context.Context, queueURL string, handles []string) error {
	return a.cliente.Delete(ctx, queueURL, handles)
}

// ChangeVisibility adia a próxima entrega.
func (a *ConsumidorFila) ChangeVisibility(ctx context.Context, queueURL string, handles []string, seconds int32) error {
	return a.cliente.ChangeVisibility(ctx, queueURL, handles, seconds)
}

// Send entrega a mensagem em outra fila.
func (a *ConsumidorFila) Send(ctx context.Context, queueURL string, body []byte, groupID, dedupID string) error {
	return a.cliente.Send(ctx, queueURL, body, groupID, dedupID)
}
