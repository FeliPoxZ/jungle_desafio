package httpapi

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
)

// abrirCarteira atende POST /wallets (rota do serviço INTERNO).
//
// # A chave de idempotência da abertura, quando o cliente não manda uma
//
// O README §9 não define header de idempotência para `POST /wallets` — ele o faz
// para `POST /wagering/transactions`. Mas a abertura é a operação mais cara de
// duplicar (crédito inicial), e o caso de uso exige a chave.
//
// A decisão: se o cliente enviar `Idempotency-Key`, ela é USADA como veio (o
// servidor nunca substitui uma chave recebida). Se não enviar, a borda deriva uma
// chave determinística do próprio pedido — `abertura:{playerId}:{moeda}`. Duas
// consequências, as duas desejáveis:
//
//   - reenviar o MESMO pedido devolve a carteira existente como replay, em vez
//     de um conflito seco (o estado final é o mesmo que a unicidade
//     `(playerId, currency)` já garantiria, mas com resposta útil);
//   - reenviar o mesmo jogador com OUTRO saldo é conflito de conteúdo — que é a
//     verdade: a carteira já foi aberta com outro valor.
func (h *Handlers) abrirCarteira(w http.ResponseWriter, r *http.Request) {
	var corpo openWalletRequest
	if err := decodeJSON(r, &corpo); err != nil {
		WriteError(w, r, h.logger, ErrBadRequest(err.Error(), err))
		return
	}

	if strings.TrimSpace(corpo.PlayerID) == "" {
		WriteError(w, r, h.logger, ErrFailureBadRequest(appwagering.CodeInvalidRequest, "playerId é obrigatório"))
		return
	}
	if corpo.InitialBalance == nil {
		// `initialBalance` ausente é entrada inválida. `"0.00"` é aceito e cria
		// só a carteira (README §9) — os dois casos precisam ser distinguíveis,
		// e é por isso que o campo é ponteiro no DTO.
		WriteError(w, r, h.logger, ErrFailureBadRequest(appwagering.CodeInvalidRequest, "initialBalance é obrigatório"))
		return
	}

	chave := strings.TrimSpace(r.Header.Get(HeaderIdempotencyKey))
	if chave == "" {
		chave = chaveDeAbertura(corpo.PlayerID, corpo.InitialBalance.Currency())
	}

	res, err := h.servico.OpenWallet(r.Context(), appwagering.OpenWalletRequest{
		PlayerID:       ids.PlayerID(corpo.PlayerID),
		InitialBalance: *corpo.InitialBalance,
		IdempotencyKey: chave,
		CorrelationID:  observability.CorrelationIDFrom(r.Context()),
	})
	if err != nil {
		WriteError(w, r, h.logger, erroDeNegocio(err))
		return
	}

	h.logger.InfoContext(r.Context(), "carteira_aberta",
		slog.String("walletId", string(res.WalletID)),
		slog.String("playerId", string(res.PlayerID)),
		slog.String("transactionId", string(res.OpeningTransactionID)),
		slog.Bool("idempotentReplay", res.IdempotentReplay),
		slog.Int64("version", res.Version),
	)
	writeJSON(w, r, http.StatusCreated, walletResponse{
		ID:               string(res.WalletID),
		PlayerID:         string(res.PlayerID),
		Balance:          res.Balance,
		Version:          res.Version,
		IdempotentReplay: res.IdempotentReplay,
	})
}

// chaveDeAbertura deriva a chave de idempotência da abertura.
func chaveDeAbertura(playerID string, moeda money.Currency) string {
	return fmt.Sprintf("abertura:%s:%s", strings.TrimSpace(playerID), moeda.String())
}

// lerCarteira atende GET /wallets/{walletId} (rota do serviço interno).
func (h *Handlers) lerCarteira(w http.ResponseWriter, r *http.Request) {
	id := ids.WalletID(chi.URLParam(r, "walletId"))

	carteira, err := h.servico.GetWallet(r.Context(), id)
	if err != nil {
		WriteError(w, r, h.logger, erroDeNegocio(err))
		return
	}

	h.logger.InfoContext(r.Context(), "carteira_consultada",
		slog.String("walletId", string(carteira.ID())),
		slog.String("playerId", string(carteira.PlayerID())),
		slog.Int64("version", carteira.Version()),
	)
	writeJSON(w, r, http.StatusOK, walletResponse{
		ID:       string(carteira.ID()),
		PlayerID: string(carteira.PlayerID()),
		Balance:  carteira.Balance(),
		Version:  carteira.Version(),
	})
}

// listarLedger atende GET /wallets/{walletId}/ledger?cursor=...&limit=50.
//
// # O cursor é OPACO
//
// O cliente não interpreta nem monta o valor: ele devolve o `nextCursor` da
// página anterior. Assim a ordenação interna pode mudar (inclusive de coluna)
// sem quebrar contrato. Cursor inválido é erro de ENTRADA (400), e não erro de
// servidor: é texto que o cliente mandou.
//
// # `limit`
//
// Ausente ou ≤ 0 ⇒ padrão 50; acima de 200 ⇒ truncado em 200 (o teto existe para
// o cliente não transformar paginação em varredura). `limit` que não é número é
// entrada inválida — recusar é melhor do que ignorar em silêncio e devolver um
// tamanho que o cliente não pediu.
func (h *Handlers) listarLedger(w http.ResponseWriter, r *http.Request) {
	id := ids.WalletID(chi.URLParam(r, "walletId"))

	limite := 0
	if bruto := strings.TrimSpace(r.URL.Query().Get("limit")); bruto != "" {
		n, err := strconv.Atoi(bruto)
		if err != nil {
			WriteError(w, r, h.logger, ErrFailureBadRequest(appwagering.CodeInvalidRequest, "limit deve ser um número inteiro"))
			return
		}
		limite = n
	}

	pagina, err := h.servico.ListLedger(r.Context(), id, limite, r.URL.Query().Get("cursor"))
	if err != nil {
		WriteError(w, r, h.logger, erroDeNegocio(err))
		return
	}

	resposta := ledgerPageResponse{Entries: make([]ledgerEntryResponse, 0, len(pagina.Entries))}
	for _, entrada := range pagina.Entries {
		resposta.Entries = append(resposta.Entries, novoLancamento(entrada))
	}
	if pagina.NextCursor != "" {
		cursor := pagina.NextCursor
		resposta.NextCursor = &cursor
	}

	h.logger.InfoContext(r.Context(), "ledger_consultado",
		slog.String("walletId", string(id)),
		slog.Int("entries", len(resposta.Entries)),
		slog.Bool("hasMore", resposta.NextCursor != nil),
	)
	writeJSON(w, r, http.StatusOK, resposta)
}

// reconciliar atende POST /wallets/{walletId}/reconciliation.
//
// A resposta reporta divergência (README §9). Além dela, a divergência vai para
// o LOG (WARN) e para a métrica `reconciliation_divergences_total`: uma
// conferência que ninguém lê não serve para nada, e o requisito é explícito
// quanto aos três canais.
//
// A reconciliação NÃO altera saldo — o caso de uso a executa em transação
// somente-leitura, e há teste que prova que o PostgreSQL recusa escrita nesse
// modo.
func (h *Handlers) reconciliar(w http.ResponseWriter, r *http.Request) {
	id := ids.WalletID(chi.URLParam(r, "walletId"))

	rec, err := h.servico.Reconcile(r.Context(), id)
	if err != nil {
		WriteError(w, r, h.logger, erroDeNegocio(err))
		return
	}

	if !rec.Consistent {
		h.metrics.IncReconciliationDivergence()
		h.logger.WarnContext(r.Context(), "reconciliacao_divergente",
			slog.String("walletId", string(rec.WalletID)),
			// Os valores são unidades mínimas em texto: o log precisa dizer
			// QUANTO divergiu, mas sem despejar payload financeiro completo.
			slog.String("storedMinor", strconv.FormatInt(rec.StoredBalance.Minor(), 10)),
			slog.String("calculatedMinor", strconv.FormatInt(rec.CalculatedBalance.Minor(), 10)),
			slog.String("differenceMinor", strconv.FormatInt(rec.Difference.Minor(), 10)),
			slog.Int("checkedEntries", rec.CheckedEntries),
			slog.String("correlationId", observability.CorrelationIDFrom(r.Context())),
		)
	}

	writeJSON(w, r, http.StatusOK, reconciliationResponse{
		WalletID:          string(rec.WalletID),
		StoredBalance:     rec.StoredBalance,
		CalculatedBalance: rec.CalculatedBalance,
		Difference:        rec.Difference,
		Consistent:        rec.Consistent,
		CheckedEntries:    rec.CheckedEntries,
	})
}
