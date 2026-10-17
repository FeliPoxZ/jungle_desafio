// Package worker é a política dos laços que ficam FORA do caminho da requisição:
// o publicador da outbox, o worker de referências e o reaper de `PENDING`.
//
// # Por que a política mora aqui, e não nos adaptadores
//
// O adaptador do SQS (`internal/infra/sqs`) sabe ENVIAR. Ele não sabe — e não
// deve saber — que uma falha de rede merece nova tentativa, que uma recusa do
// broker é definitiva, nem quanto esperar entre as duas. Essa decisão é a parte
// que interessa discutir, e ela precisa ser testável sem LocalStack: aqui o
// destino entra por uma porta (`Publicador`), e o teste usa um dublê que registra
// exatamente o que foi pedido — inclusive o que NÃO foi.
//
// Mesma divisão do consumidor da Fase 8: lá `internal/consumer` tem a política e
// `internal/infra/sqs` o protocolo. Aqui a repetição é do mesmo desenho, de
// propósito: dois lugares com a mesma responsabilidade deveriam ser lidos da
// mesma forma.
package worker

import (
	"context"
	"math/rand"
	"time"
)

// esperaMinima é o piso de qualquer reagendamento.
//
// Existe porque o backoff com jitter pode devolver um valor próximo de zero (o
// jitter é subtraído), e uma nova tentativa imediata martelaria o destino que
// acabou de falhar.
const esperaMinima = time.Second

// esperaComBackoff calcula quanto esperar antes da próxima tentativa.
//
// # Exponencial com teto e jitter
//
//		base * 2^(tentativa-1), limitado ao teto, com até 25% de jitter
//
//	  - **exponencial**: um destino fora do ar não deve ser martelado a cada
//	    segundo; a espera cresce a cada falha;
//	  - **teto**: sem ele, a vigésima tentativa adiaria o trabalho por dias e a
//	    fila pareceria travada. O teto também é o que mantém o prazo dentro do que
//	    o operador consegue explicar;
//	  - **jitter**: sem ele, todas as instâncias que falharam no mesmo instante
//	    voltariam no mesmo instante — o rebanho sincronizado transforma uma
//	    oscilação em sobrecarga.
//
// O jitter é SUBTRAÍDO, e não somado depois do limite: somado, o resultado
// ultrapassaria o teto e o teto deixaria de ser teto. Este é o defeito 32 da §15
// do ARCHITECTURE, encontrado no consumidor da Fase 8 — e a mesma armadilha
// valeria aqui.
//
// O deslocamento é limitado a 20 antes de ser aplicado: com tentativa grande,
// `1 << n` estoura o `int64` ANTES de o teto entrar em ação.
func esperaComBackoff(r *rand.Rand, tentativa int, base, teto time.Duration) time.Duration {
	if tentativa < 1 {
		tentativa = 1
	}
	if base <= 0 {
		base = time.Second
	}
	if teto <= 0 {
		teto = time.Minute
	}
	if teto < base {
		teto = base
	}

	deslocamento := tentativa - 1
	if deslocamento > 20 {
		deslocamento = 20
	}

	atraso := base * time.Duration(int64(1)<<uint(deslocamento))
	if atraso > teto || atraso <= 0 {
		atraso = teto
	}

	if atraso > esperaMinima {
		variacao := time.Duration(r.Int63n(int64(atraso/4) + 1))
		atraso -= variacao
	}
	if atraso < esperaMinima {
		atraso = esperaMinima
	}
	return atraso
}

// dormir espera o prazo, mas acorda no cancelamento.
//
// O relógio é do `time`, e não do `ports.Clock`: este é o tempo do LAÇO (espera
// entre ciclos), não um instante de negócio que precise ser afirmado no teste.
func dormir(ctx context.Context, d time.Duration) {
	if d <= 0 {
		// Sem espera, mas ainda assim cedendo o processador: um laço apertado
		// monopolizaria a CPU.
		select {
		case <-ctx.Done():
		case <-time.After(time.Millisecond):
		}
		return
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
