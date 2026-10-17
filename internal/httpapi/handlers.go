package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/infra/auth"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
)

// enviarOperacaoRequest é o corpo do POST /wagering/transactions.
//
// `Kind` e `Money` são strings/tipos do domínio porque o parsing precisa ser o
// MESMO da regra de negócio: se a borda aceitasse "25" e o domínio só aceitasse
// "25.00", a validação dependeria do caminho — e o hash de idempotência, que é
// calculado sobre o valor já convertido, mudaria conforme a porta de entrada
// (HTTP x SQS), quebrando a equivalência exigida no README §9.
type enviarOperacaoRequest struct {
	// ProviderID é OPCIONAL no corpo e, quando enviado, precisa coincidir com o
	// provedor do token. Aceitá-lo como escolha do provedor seria entregar o
	// isolamento ao cliente.
	ProviderID string `json:"providerId"`

	ExternalTransactionID  string   `json:"externalTransactionId"`
	PlayerID               string   `json:"playerId"`
	WalletID               string   `json:"walletId"`
	RoundID                string   `json:"roundId"`
	GameID                 string   `json:"gameId"`
	Kind                   string   `json:"kind"`
	Money                  MoneyDTO `json:"money"`
	ReferenceExternalTxnID string   `json:"referenceExternalTransactionId"`
}

// enviarOperacao atende POST /wagering/transactions.
//
// # O caminho de uma operação, em ordem
//
//  1. identidade do token (middleware): quem é o provedor, e ele existe?
//  2. corpo: decodificação ESTRITA (campo desconhecido é erro);
//  3. `providerId` do corpo x `providerId` do token → 403 se divergirem;
//  4. `Idempotency-Key` do header → o caso de uso recusa se faltar;
//  5. `Process` — a MESMA função que o consumidor de SQS chama (Fase 8);
//  6. desfecho → status.
//
// Nenhuma regra financeira é decidida aqui. A borda traduz e delega: se algum
// dia este handler precisar de um `if` sobre saldo, a regra está no lugar errado.
func (h *Handlers) enviarOperacao(w http.ResponseWriter, r *http.Request) {
	identidade, ok := auth.IdentityFrom(r.Context())
	if !ok {
		// Inalcançável pelo caminho normal (o middleware roda antes), mas
		// aconteceria se a rota fosse montada sem autenticação. Falhar fechado.
		WriteError(w, r, h.logger, ErrUnauthorized("autenticação obrigatória", nil))
		return
	}

	inicio := h.relogio()

	var corpo enviarOperacaoRequest
	if err := decodeJSON(r, &corpo); err != nil {
		WriteError(w, r, h.logger, ErrBadRequest(err.Error(), err))
		return
	}

	// O provedor autorizado é o DO TOKEN. O corpo só pode confirmá-lo.
	if corpo.ProviderID != "" && corpo.ProviderID != identidade.ProviderID {
		h.logger.WarnContext(r.Context(), "provedor_divergente_do_token",
			slog.String("providerIdToken", identidade.ProviderID),
			slog.String("providerIdCorpo", corpo.ProviderID),
			slog.String("path", r.URL.Path),
		)
		WriteError(w, r, h.logger, ErrForbidden("o provedor do corpo não corresponde à identidade autenticada"))
		return
	}

	chave := r.Header.Get(HeaderIdempotencyKey)
	if chave == "" {
		// A chave é OBRIGATÓRIA (README §9) e o servidor NUNCA calcula uma no
		// lugar da que faltou: substituir em silêncio faria o reenvio do
		// provedor parecer uma operação nova.
		WriteError(w, r, h.logger, ErrFailureBadRequest(
			appwagering.CodeIdempotencyKeyRequired, "o header Idempotency-Key é obrigatório"))
		return
	}

	res, err := h.servico.Process(r.Context(), appwagering.Request{
		ProviderID:             identidade.ProviderID,
		ExternalTransactionID:  corpo.ExternalTransactionID,
		IdempotencyKey:         chave,
		PlayerID:               ids.PlayerID(corpo.PlayerID),
		WalletID:               ids.WalletID(corpo.WalletID),
		RoundID:                corpo.RoundID,
		GameID:                 corpo.GameID,
		Kind:                   wagering.Kind(corpo.Kind),
		Amount:                 corpo.Money,
		ReferenceExternalTxnID: corpo.ReferenceExternalTxnID,
		CorrelationID:          observability.CorrelationIDFrom(r.Context()),
	})
	if err != nil {
		h.observarOperacao(corpo.Kind, "error", time.Since(inicio))
		WriteError(w, r, h.logger, erroDeNegocio(err))
		return
	}

	status := statusDoResultado(res)
	h.observarOperacao(corpo.Kind, res.Status.String(), time.Since(inicio))
	h.registrarResultado(r, res, identidade, corpo.WalletID)
	writeJSON(w, r, status, respostaDoResultado(res))
}

// respostaDoResultado monta o corpo do envio de operação.
//
// `balance` e `failureCode` saem como `null` quando o dado não existe — e o
// contrato depende disso:
//
//	PROCESSED          → balance preenchido, failureCode null
//	REJECTED           → balance null, failureCode preenchido
//	PENDING_REFERENCE  → ambos null
func respostaDoResultado(res appwagering.Result) wagerResponse {
	resposta := wagerResponse{
		TransactionID:    string(res.TransactionID),
		Status:           res.Status.String(),
		IdempotentReplay: res.IdempotentReplay,
		Balance:          nil,
		FailureCode:      nil,
	}
	if res.Balance != nil {
		resposta.Balance = *res.Balance
	}
	if res.FailureCode != "" {
		resposta.FailureCode = res.FailureCode
	}
	return resposta
}

// registrarResultado acompanha a operação com log e métrica.
//
// Campos do README §12: correlationId (automático, pelo handler de contexto),
// transactionId, walletId e providerId. O valor monetário NÃO entra no log
// (payload financeiro completo é proibido).
//
// `walletId` vinha faltando aqui até a Fase 11: a operação era rastreável pelo
// transactionId, mas quem chegava com o id da CARTEIRA na mão (o suporte do
// provedor, o plantonista) não tinha como ligar o fato ao pedido.
func (h *Handlers) registrarResultado(r *http.Request, res appwagering.Result, identidade auth.Identity, walletID string) {
	if res.IdempotentReplay {
		h.metrics.IncReplay()
	}

	// Rejeição de negócio é desfecho legítimo, mas é o que o provedor precisa
	// acompanhar — por isso sai em WARN, com o failureCode.
	if res.FailureCode != "" {
		h.logger.WarnContext(r.Context(), "operacao_rejeitada",
			slog.String("transactionId", string(res.TransactionID)),
			slog.String("walletId", walletID),
			slog.String("providerId", identidade.ProviderID),
			slog.String("failureCode", res.FailureCode),
			slog.String("status", res.Status.String()),
			slog.String("correlationId", observability.CorrelationIDFrom(r.Context())),
		)
		return
	}

	h.logger.InfoContext(r.Context(), "operacao_concluida",
		slog.String("transactionId", string(res.TransactionID)),
		slog.String("walletId", walletID),
		slog.String("providerId", identidade.ProviderID),
		slog.String("status", res.Status.String()),
		slog.Bool("idempotentReplay", res.IdempotentReplay),
		slog.String("correlationId", observability.CorrelationIDFrom(r.Context())),
	)
}

// observarOperacao alimenta `wager_operations_total` e a latência.
//
// Os labels são de cardinalidade BAIXA (tipo e status final), nunca
// identificadores: usar walletId como label explodiria a série temporal do
// Prometheus.
//
// # O que mudou na Fase 11 (defeito 43)
//
// Antes, o caminho HTTP chamava `observeOperation(res.Status.String(),
// StatusLabel(statusHTTP))`, ou seja, o label `kind` recebia **PROCESSED** e o
// label `status` recebia **2xx_201**. O caminho SQS (Fase 8) sempre usou
// `kind="BET"`, `status="PROCESSED"`. O MESMO nome de métrica tinha dois
// significados para o mesmo label — somar por `kind` num painel misturava tipo
// de operação com desfecho e ninguém percebia, porque nenhum teste lia a métrica
// em `/metrics`.
//
// Agora as duas portas emitem a MESMA semântica: `kind` é o tipo da operação e
// `status` é o desfecho do domínio. O código HTTP continua no log de acesso.
func (h *Handlers) observarOperacao(kind, status string, duracao time.Duration) {
	if kind == "" {
		kind = "unknown"
	}
	h.metrics.ObserveOperation(kind, status, duracao)
}
