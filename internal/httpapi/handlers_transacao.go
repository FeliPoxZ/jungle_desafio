package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/infra/auth"
)

// transactionResponse é a leitura de uma operação.
//
// Ele existe para o cliente "acompanhar pendências e consultar códigos de
// rejeição ou falha" (README §9): sem `status` e `failureCode` na leitura, uma
// operação rejeitada seria indistinguível de uma operação que não existe.
type transactionResponse struct {
	TransactionID          string   `json:"transactionId"`
	ProviderID             string   `json:"providerId"`
	ExternalTransactionID  string   `json:"externalTransactionId"`
	PlayerID               string   `json:"playerId"`
	WalletID               string   `json:"walletId"`
	RoundID                string   `json:"roundId"`
	GameID                 string   `json:"gameId"`
	Kind                   string   `json:"kind"`
	Status                 string   `json:"status"`
	Money                  MoneyDTO `json:"money"`
	ReferenceExternalTxnID *string  `json:"referenceExternalTransactionId"`
	ReferenceTransactionID *string  `json:"referenceTransactionId"`
	FailureCode            *string  `json:"failureCode"`
	ResultBalance          any      `json:"resultBalance"`
	CreatedAt              string   `json:"createdAt"`
	UpdatedAt              string   `json:"updatedAt"`
}

// lerTransacao atende GET /wagering/transactions/{transactionId}.
//
// # Por que esta rota é do serviço INTERNO
//
// O `{transactionId}` é o identificador INTERNO (UUID gerado por nós). O
// provedor não o conhece e não tem por que conhecê-lo: ele consulta a operação
// pela identidade DELE, em
// `GET /providers/{providerId}/wagering/transactions/{externalTransactionId}`.
//
// A separação não é burocracia: `GetTransaction` responde por qualquer operação,
// de qualquer provedor. Expô-la ao provedor entregaria o isolamento — ele
// poderia varrer UUIDs e ler operações alheias.
func (h *Handlers) lerTransacao(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "transactionId")

	tx, err := h.servico.GetTransaction(r.Context(), wagering.TransactionID(id))
	if err != nil {
		WriteError(w, r, h.logger, erroDeNegocio(err))
		return
	}

	h.logger.InfoContext(r.Context(), "transacao_consultada",
		slog.String("transactionId", string(tx.ID())),
		slog.String("walletId", string(tx.WalletID())),
		slog.String("providerId", tx.ProviderID()),
		slog.String("status", tx.Status().String()),
	)
	writeJSON(w, r, http.StatusOK, novaTransacao(tx))
}

// lerPorIdentidadeExterna atende
// GET /providers/{providerId}/wagering/transactions/{externalTransactionId}.
//
// # As duas checagens, e por que as duas
//
//  1. `{providerId}` do PATH x `providerId` do TOKEN: diferente ⇒ **403**. Aqui
//     a pergunta é explícita sobre outro provedor ("me dê a operação do
//     provider-b"), e o contrato do A9 manda 403;
//  2. `GetByProviderExternalID`: o provedor do token entra na CONSULTA SQL, então
//     uma operação que não é dele simplesmente não é encontrada ⇒ **404**.
//
// Sem a segunda, um token de `provider-a` lendo por identidade externa
// alcançaria a operação de `provider-b` que por acaso tivesse o mesmo
// `externalTransactionId`.
func (h *Handlers) lerPorIdentidadeExterna(w http.ResponseWriter, r *http.Request) {
	identidade, ok := auth.IdentityFrom(r.Context())
	if !ok {
		WriteError(w, r, h.logger, ErrUnauthorized("autenticação obrigatória", nil))
		return
	}

	providerDoPath := chi.URLParam(r, "providerId")
	if providerDoPath != identidade.ProviderID {
		h.logger.WarnContext(r.Context(), "consulta_de_outro_provedor",
			slog.String("providerIdToken", identidade.ProviderID),
			slog.String("providerIdPath", providerDoPath),
			slog.String("path", r.URL.Path),
		)
		WriteError(w, r, h.logger, ErrForbidden("a identidade autenticada não corresponde ao provedor consultado"))
		return
	}

	externalID := chi.URLParam(r, "externalTransactionId")
	tx, err := h.servico.GetByProviderExternalID(r.Context(), identidade.ProviderID, externalID)
	if err != nil {
		// "Não encontrado" aqui cobre os dois casos — não existe, ou não é
		// deste provedor — e de propósito: distinguir confirmaria a existência
		// de uma operação alheia.
		WriteError(w, r, h.logger, erroDeNegocio(err))
		return
	}

	h.logger.InfoContext(r.Context(), "transacao_consultada",
		slog.String("transactionId", string(tx.ID())),
		slog.String("providerId", identidade.ProviderID),
		slog.String("externalTransactionId", externalID),
	)
	writeJSON(w, r, http.StatusOK, novaTransacao(tx))
}

// novaTransacao monta a resposta de leitura.
func novaTransacao(tx *wagering.WagerTransaction) transactionResponse {
	resp := transactionResponse{
		TransactionID:         string(tx.ID()),
		ProviderID:            tx.ProviderID(),
		ExternalTransactionID: tx.ExternalTransactionID(),
		PlayerID:              string(tx.PlayerID()),
		WalletID:              string(tx.WalletID()),
		RoundID:               tx.RoundID(),
		GameID:                tx.GameID(),
		Kind:                  tx.Kind().String(),
		Status:                tx.Status().String(),
		Money:                 tx.Amount(),
		CreatedAt:             tx.CreatedAt().UTC().Format(rfc3339Milissegundos),
		UpdatedAt:             tx.UpdatedAt().UTC().Format(rfc3339Milissegundos),
	}

	if v := tx.ReferenceExternalTransactionID(); v != "" {
		resp.ReferenceExternalTxnID = &v
	}
	if v := tx.ReferenceTransactionID(); v != "" {
		s := string(v)
		resp.ReferenceTransactionID = &s
	}
	if v := tx.FailureCode(); v != "" {
		resp.FailureCode = &v
	}
	if tx.HasResultBalance() {
		resp.ResultBalance = tx.ResultBalance()
	}
	return resp
}
