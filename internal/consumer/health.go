package consumer

import (
	"context"
	"time"
)

// NomeDaDependencia é como o SQS aparece no relatório do readiness.
const NomeDaDependencia = "sqs"

// VerificadorDeFila é o mínimo que o readiness precisa do broker.
//
// Deliberadamente MENOR que `Fila`: verificar dependência não exige saber
// receber, apagar ou publicar. Com uma porta estreita, o teste do readiness não
// precisa de um dublê com cinco métodos, e o adaptador só implementa o que existe.
type VerificadorDeFila interface {
	Ping(ctx context.Context, queueURL string) error
}

// HealthChecker verifica o SQS para o `/health/ready`.
//
// # Por que o SQS entra no readiness (fecha a P5)
//
// Readiness responde "esta instância consegue atender?". Com o SQS fora, o
// consumidor não recebe nada e a API aceita operações que nunca serão processadas
// pelo caminho assíncrono — e o orquestrador continuaria mandando tráfego.
// Registrar a dependência faz a instância SAIR da rotação, que é o
// comportamento que se espera de um readiness que não mente.
//
// O que ele verifica é a fila ESPECÍFICA (`GetQueueAttributes`), e não "o SQS
// responde": com a fila ausente — ambiente mal provisionado — o serviço não
// consumiria nada, e responder `UP` seria falso.
type HealthChecker struct {
	fila     VerificadorDeFila
	queueURL string
	prazo    time.Duration
}

// NovoHealthChecker monta o verificador do SQS.
func NovoHealthChecker(fila VerificadorDeFila, queueURL string) *HealthChecker {
	return &HealthChecker{fila: fila, queueURL: queueURL, prazo: 3 * time.Second}
}

// Name identifica a dependência no relatório.
func (h *HealthChecker) Name() string { return NomeDaDependencia }

// CheckReadiness confirma que a fila de entrada está acessível.
func (h *HealthChecker) CheckReadiness(ctx context.Context) error {
	if h == nil || h.fila == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, h.prazo)
	defer cancel()

	return h.fila.Ping(ctx, h.queueURL)
}
