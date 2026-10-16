package httpapi

import (
	"github.com/FeliPoxZ/jungle_desafio/internal/infra/auth"
)

// RegistrarRotas monta as rotas de negócio nos sub-roteadores certos.
//
// # Por que a rota INTERNA pode exigir role, e a de provedor não basta ter token
//
// O README §2 separa dois mundos:
//
//   - **provedor**: envia operações e consulta as PRÓPRIAS transações;
//   - **serviço interno**: abre carteira, lê carteira, lista ledger, reconcilia.
//
// A separação não é decorativa. Se um token de provedor abrisse carteira, ele
// poderia criar saldo a partir do zero — o crédito inicial é dinheiro entrando no
// sistema, e nenhum provedor tem esse direito. Por isso essas rotas carregam
// `RequireRole(internal)`.
//
// # Por que o isolamento do provedor é middleware, e não checagem no handler
//
// `RequireProvider` garante que existe um `providerId` autenticado no contexto
// ANTES de o handler rodar. É esse valor — vindo do TOKEN — que o handler usa
// para consultar e para comparar com o `:providerId` do path. O corpo da
// requisição nunca escolhe o provedor.
func (h *Handlers) RegistrarRotas() {
	if h.auth == nil {
		// Composição sem autenticador: nada é registrado. Falhar FECHADO é a
		// única opção aceitável — registrar rota de negócio sem autenticação
		// seria expor operação financeira sem identidade, que é item
		// eliminatório do README §14.
		return
	}

	// Rotas de carteira: serviço interno.
	internas := h.routes.Base.With(
		h.auth.Middleware,
		h.auth.RequireRole(auth.RoleInternal),
	)
	internas.Post("/wallets", h.abrirCarteira)
	internas.Get("/wallets/{walletId}", h.lerCarteira)
	internas.Get("/wallets/{walletId}/ledger", h.listarLedger)
	internas.Post("/wallets/{walletId}/reconciliation", h.reconciliar)

	// Rotas do provedor.
	provedor := h.routes.Base.With(
		h.auth.Middleware,
		h.auth.RequireProvider(),
	)
	provedor.Post("/wagering/transactions", h.enviarOperacao)
	provedor.Get("/providers/{providerId}/wagering/transactions/{externalTransactionId}", h.lerPorIdentidadeExterna)

	// Leitura do serviço interno por identificador INTERNO da operação.
	// Fica nas rotas internas: o `${transactionId}` é o UUID que só o serviço
	// interno manipula; o provedor consulta pela identidade DELE (rota acima).
	internas.Get("/wagering/transactions/{transactionId}", h.lerTransacao)
}
