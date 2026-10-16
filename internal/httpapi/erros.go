package httpapi

import (
	"errors"
	"net/http"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	domainerrors "github.com/FeliPoxZ/jungle_desafio/internal/domain/errors"
)

// statusDoResultado traduz o desfecho do caso de uso no status HTTP.
//
// # Por que isto é uma função só, e não uma cadeia de `if`s em cada handler
//
// O contrato do README §9 distingue cinco situações que o cliente precisa saber
// separar: operação nova concluída, replay idempotente, pendência de referência,
// rejeição de negócio e erro de entrada. Se a decisão morasse dentro de cada
// handler, cinco handlers teriam cinco versões da mesma regra — e a divergência
// apareceria em produção, num status errado, sem teste vermelho.
//
// # 201 x 200: o que muda para o cliente
//
// `201 Created` diz "este pedido criou o resultado"; `200 OK` diz "o resultado
// já existia e aqui está o da primeira vez". Um cliente que contasse apenas os
// 201 como "operações novas" contaria certo; com 200 em tudo, ele contaria a
// mesma operação duas vezes.
//
// # 202 x 422: pendente não é rejeitado
//
// `PENDING_REFERENCE` (202) significa "aceitei e ainda vou resolver". `REJECTED`
// (422) significa "decidi e não vai acontecer". Tratar as duas como 4xx faria o
// provedor reenviar uma operação que já está esperando a referência — gerando
// mais pendências, não soluções.
func statusDoResultado(res appwagering.Result) int {
	switch res.Status.String() {
	case "PROCESSED":
		if res.IdempotentReplay {
			return http.StatusOK
		}
		return http.StatusCreated
	case "PENDING_REFERENCE":
		return http.StatusAccepted
	case "REJECTED":
		return http.StatusUnprocessableEntity
	case "FAILED":
		// FAILED é falha PERMANENTE de infraestrutura registrada para auditoria
		// (README §6.3). Não é 2xx (não houve sucesso) nem 4xx (o cliente não
		// errou): é indisponibilidade do serviço.
		return http.StatusServiceUnavailable
	default:
		// Desfecho que a borda não conhece (PENDING durável, ou estado novo no
		// domínio sem atualizar este mapa). 500 é o lado honesto: não inventamos
		// status de sucesso para um desfecho desconhecido.
		return http.StatusInternalServerError
	}
}

// codigoDoResultado devolve o `code` do envelope para um desfecho não-2xx.
func codigoDoResultado(status int) string {
	switch status {
	case http.StatusUnprocessableEntity:
		return CodeUnprocessable
	case http.StatusAccepted:
		return CodePending
	case http.StatusServiceUnavailable:
		return CodeServiceUnavailable
	case http.StatusConflict:
		return CodeConflict
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusForbidden:
		return CodeForbidden
	case http.StatusUnauthorized:
		return CodeUnauthorized
	default:
		return CodeInternal
	}
}

// erroDeNegocio traduz erro do caso de uso em `*APIError`.
//
// # A ordem das verificações importa
//
//  1. **conflito e não encontrado** — os sentinelas de `ports` são a fonte da
//     verdade, porque o repositório devolve os MESMOS objetos (são alias, ver
//     ARCHITECTURE §1). Um `23505` cru que tenha escapado vira 409 aqui, e não
//     500;
//  2. **código de domínio** — `INVALID_REQUEST`, `IDEMPOTENCY_KEY_REQUIRED`,
//     `OPERATION_NOT_ALLOWED`... Cada um vira o status prescrito no A9/README;
//  3. **transitório** — `ports.IsRetryable` (lock timeout, deadlock, conexão):
//     503 com `Retry-After`, porque o cliente PODE repetir;
//  4. **qualquer outro** — 500, sem vazar driver nem stack.
//
// # A regra que nunca pode ser quebrada
//
// Falha de INFRAESTRUTURA nunca vira 4xx. Se um `55P03` (lock timeout) saísse
// como 400, o provedor concluiria que o pedido dele está errado e não repetiria
// — e uma disputa momentânea de lock viraria uma aposta perdida.
func erroDeNegocio(err error) *APIError {
	if err == nil {
		return nil
	}

	switch {
	case ports.IsConflict(err) || errors.Is(err, appwagering.ErrIdempotencyConflict) ||
		errors.Is(err, appwagering.ErrWalletAlreadyExists):
		return ErrConflict("a operação já existe com conteúdo diferente")

	case ports.IsNotFound(err) || errors.Is(err, appwagering.ErrTransactionNotFound) ||
		errors.Is(err, appwagering.ErrWalletNotFound):
		return ErrNotFound("recurso não encontrado")
	}

	switch domainerrors.CodeOf(err) {
	// --- Entrada inválida: NADA foi gravado; o cliente corrige e reenvia -----
	//
	// É esta distinção que o README §7 chama de "distinguir entradas corrigíveis
	// de resultados definitivos": aqui a mesma chave de idempotência pode ser
	// reenviada depois de corrigida, porque não existe operação gravada.
	case appwagering.CodeInvalidRequest,
		appwagering.CodeIdempotencyKeyRequired,
		"INVALID_AMOUNT",
		"REFERENCE_REQUIRED",
		"INVALID_TRANSACTION",
		"INVALID_CURRENCY",
		"CURRENCY_MISMATCH",
		"INVALID_MONEY":
		return ErrFailureBadRequest(domainerrors.CodeOf(err), mensagemDeEntrada(domainerrors.CodeOf(err)))

	// --- Operação reservada ao serviço interno ------------------------------
	case "OPERATION_NOT_ALLOWED":
		// `OPENING` vindo de fora: o provedor não pode abrir carteira nem
		// creditar saldo inicial por requisição externa (README §6.3).
		// 403, e não 400: o pedido está bem formado; a identidade é que não tem
		// direito a esta operação.
		return ErrForbidden("esta operação é reservada ao serviço interno")
	}

	if ports.IsRetryable(err) {
		// Esgotadas as tentativas de uma falha transitória. O cliente PODE
		// repetir com segurança: a operação é idempotente pela chave.
		return ErrUnavailable("serviço temporariamente indisponível; repita a operação", 1, err)
	}

	return ErrInternal(err)
}

// mensagemDeEntrada dá texto público à entrada corrigível sem repetir detalhe
// interno no corpo.
func mensagemDeEntrada(code string) string {
	switch code {
	case appwagering.CodeIdempotencyKeyRequired:
		return "o header Idempotency-Key é obrigatório"
	case "REFERENCE_REQUIRED":
		return "esta operação exige referenceExternalTransactionId"
	case "INVALID_TRANSACTION":
		return "tipo de operação desconhecido ou reservado ao serviço interno"
	case "INVALID_AMOUNT":
		return "valor inválido para este tipo de operação"
	case "INVALID_CURRENCY", "CURRENCY_MISMATCH":
		return "moeda inválida ou incompatível com a carteira"
	default:
		return "requisição inválida"
	}
}
