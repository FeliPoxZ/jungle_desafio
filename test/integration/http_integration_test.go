//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	"github.com/FeliPoxZ/jungle_desafio/internal/httpapi"
	"github.com/FeliPoxZ/jungle_desafio/internal/infra/auth"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
)

// Testes de integração da BORDA HTTP com o IdP REAL.
//
// Eles exercitam a pilha inteira: roteador do chi, middlewares, autenticação
// OIDC contra o Keycloak do docker-compose, handlers, caso de uso, PostgreSQL e
// constraints. O token não é forjado: é obtido por `client_credentials` no
// Keycloak, exatamente como um provedor de verdade faz.
//
// É assim que o README §13 pede: "PostgreSQL, o IdP e LocalStack em containers
// reais". Se o Keycloak não estiver acessível, os testes são PULADOS (com aviso),
// para `go test -tags=integration` continuar útil em quem só subiu o Postgres.
//
//	docker compose up -d postgres keycloak
//	TEST_DATABASE_URL=postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable
//	go test -count=1 -race -tags=integration -timeout 300s ./test/integration/

const (
	// issuerPadrao é o `iss` que o Keycloak do compose emite: o endereço
	// PÚBLICO (localhost:8080), porque é ele que aparece no claim.
	issuerPadrao = "http://localhost:8080/realms/jungleg"
	// clientes do realm (deploy/keycloak/realm.json).
	clientProviderA = "provider-service"
	clientProviderB = "provider-b-service"
	clientInterno   = "internal-service"
)

// ---------------------------------------------------------------------------
// Tokens de verdade, obtidos do Keycloak
// ---------------------------------------------------------------------------

var (
	tokensUmaVez sync.Once
	tokensErro   error
	tokens       map[string]string
)

// tokenDoKeycloak obtém (e memoriza) o access token de um client.
//
// O fluxo é `client_credentials` — comunicação serviço a serviço, que é o que o
// README §2 recomenda e o que o realm deste projeto provisiona.
func tokenDoKeycloak(t *testing.T, clientID string) string {
	t.Helper()

	tokensUmaVez.Do(func() {
		tokens = map[string]string{}
		segredos := map[string]string{
			clientProviderA: "provider-service-secret",
			clientProviderB: "provider-b-service-secret",
			clientInterno:   "internal-service-secret",
		}
		for id, segredo := range segredos {
			tok, err := pedirToken(clientIDBase(), id, segredo)
			if err != nil {
				tokensErro = fmt.Errorf("obtendo token de %s: %w", id, err)
				return
			}
			tokens[id] = tok
		}
	})

	if tokensErro != nil {
		t.Skipf("Keycloak não disponível: %v", tokensErro)
	}
	tok, ok := tokens[clientID]
	if !ok {
		t.Fatalf("nenhum token memorizado para %q", clientID)
	}
	return tok
}

// issuerDeTeste devolve o emissor configurado (o mesmo que a aplicação usa).
func issuerDeTeste() string { return envOu("OIDC_ISSUER", issuerPadrao) }

// clientIDBase é a raiz do realm no Keycloak (para descobrir o token endpoint).
func clientIDBase() string {
	return envOu("OIDC_DISCOVERY_URL", issuerDeTeste())
}

// pedirToken faz o POST de client_credentials.
func pedirToken(base, clientID, segredo string) (string, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {segredo},
	}

	cliente := &http.Client{Timeout: 10 * time.Second}
	resp, err := cliente.PostForm(base+"/protocol/openid-connect/token", form)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	corpo, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(corpo)))
	}

	var resposta struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(corpo, &resposta); err != nil {
		return "", fmt.Errorf("decodificando a resposta: %w", err)
	}
	if resposta.AccessToken == "" {
		return "", fmt.Errorf("resposta sem access_token")
	}
	return resposta.AccessToken, nil
}

// ---------------------------------------------------------------------------
// Servidor de teste com a pilha real
// ---------------------------------------------------------------------------

// apiDeTeste sobe o roteador de PRODUÇÃO com o autenticador OIDC real.
func apiDeTeste(t *testing.T) *httptest.Server {
	t.Helper()

	cfg := &config.Config{
		Env: "test",
		HTTP: config.HTTPConfig{
			Addr:           ":0",
			RequestTimeout: 20 * time.Second,
			MaxBodyBytes:   1 << 20,
		},
		OIDC: config.OIDCConfig{
			Issuer:       issuerDeTeste(),
			DiscoveryURL: clientIDBase(),
			Audience:     envOu("OIDC_AUDIENCE", "jungleg-api"),
			JWKSCacheTTL: time.Minute,
			ClockSkew:    30 * time.Second,
			HTTPTimeout:  5 * time.Second,
		},
	}

	verificador := auth.New(auth.Config{
		Issuer:       cfg.OIDC.Issuer,
		DiscoveryURL: cfg.OIDC.DiscoveryURL,
		Audience:     cfg.OIDC.Audience,
		JWKSCacheTTL: cfg.OIDC.JWKSCacheTTL,
		ClockSkew:    cfg.OIDC.ClockSkew,
		HTTPTimeout:  cfg.OIDC.HTTPTimeout,
	})
	autenticador := auth.NewAutenticador(verificador, observability.DiscardLogger())

	logger := observability.DiscardLogger()
	rotas := httpapi.NewRouter(cfg, logger, observability.NewMetrics(), nil)

	handlers := httpapi.NewHandlers(httpapi.HandlersDeps{
		Service:       servico,
		Routes:        rotas,
		Authenticator: autenticador,
		Logger:        logger,
		Metrics:       observability.NewMetrics(),
	})
	handlers.RegistrarRotas()

	servidor := httptest.NewServer(rotas.Base)
	t.Cleanup(servidor.Close)
	return servidor
}

// ---------------------------------------------------------------------------
// Cliente de teste
// ---------------------------------------------------------------------------

// resposta é o que o teste precisa de uma chamada HTTP.
type resposta struct {
	status int
	corpo  []byte
	header http.Header
}

// decodifica preenche o valor informado com o corpo JSON.
func (r resposta) decodifica(t *testing.T, destino any) {
	t.Helper()

	if err := json.Unmarshal(r.corpo, destino); err != nil {
		t.Fatalf("corpo não é JSON (%v): %s", err, r.corpo)
	}
}

func (r resposta) texto() string { return string(r.corpo) }

// chamar faz a requisição com o token informado (vazio = sem autenticação).
func chamar(t *testing.T, servidor *httptest.Server, metodo, caminho, token string, corpo any, headers map[string]string) resposta {
	t.Helper()

	var corpoReader io.Reader
	if corpo != nil {
		bruto, err := json.Marshal(corpo)
		if err != nil {
			t.Fatalf("serializando o corpo: %v", err)
		}
		corpoReader = bytes.NewReader(bruto)
	}

	req, err := http.NewRequestWithContext(context.Background(), metodo, servidor.URL+caminho, corpoReader)
	if err != nil {
		t.Fatalf("montando a requisição: %v", err)
	}
	if corpo != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := servidor.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", metodo, caminho, err)
	}
	defer func() { _ = resp.Body.Close() }()

	lido, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resposta{status: resp.StatusCode, corpo: lido, header: resp.Header}
}

// ---------------------------------------------------------------------------
// Helpers de domínio
// ---------------------------------------------------------------------------

// corpoDeAbertura é o corpo do POST /wallets.
func corpoDeAbertura(playerID, saldo string) map[string]any {
	return map[string]any{
		"playerId":       playerID,
		"initialBalance": map[string]any{"amount": saldo, "currency": "BRL"},
	}
}

// corpoDeOperacao é o corpo do POST /wagering/transactions.
func corpoDeOperacao(providerID, externa, playerID, walletID, kind, valor string) map[string]any {
	return map[string]any{
		"providerId":            providerID,
		"externalTransactionId": externa,
		"playerId":              playerID,
		"walletId":              walletID,
		"roundId":               "round-1",
		"gameId":                "fortune-chimp",
		"kind":                  kind,
		"money":                 map[string]any{"amount": valor, "currency": "BRL"},
	}
}

// abrirCarteiraPeloHTTP abre a carteira com o token INTERNO e devolve os dados.
func abrirCarteiraPeloHTTP(t *testing.T, servidor *httptest.Server, saldo string) (walletID, playerID string) {
	t.Helper()

	playerID = novoUUID()
	resp := chamar(t, servidor, http.MethodPost, "/wallets", tokenDoKeycloak(t, clientInterno),
		corpoDeAbertura(playerID, saldo), nil)

	if resp.status != http.StatusCreated {
		t.Fatalf("POST /wallets = %d, quer 201: %s", resp.status, resp.texto())
	}

	var corpo struct {
		ID       string `json:"id"`
		PlayerID string `json:"playerId"`
		Balance  struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		} `json:"balance"`
		Version int64 `json:"version"`
	}
	resp.decodifica(t, &corpo)

	if corpo.Balance.Amount != saldo {
		t.Errorf("saldo na abertura = %q, quer %q", corpo.Balance.Amount, saldo)
	}
	if corpo.Version != 1 {
		t.Errorf("versão na abertura = %d, quer 1", corpo.Version)
	}
	return corpo.ID, corpo.PlayerID
}

// contarLinhas conta linhas de uma tabela — usado para provar "nenhum efeito
// financeiro" depois de um acesso negado.
func contarLinhas(t *testing.T, tabela string) int {
	t.Helper()

	permitidas := map[string]bool{
		"wallets": true, "wager_transactions": true,
		"wallet_ledger_entries": true, "outbox_events": true,
	}
	if !permitidas[tabela] {
		t.Fatalf("tabela %q não permitida neste helper", tabela)
	}

	var n int
	if err := poolTeste.QueryRow(context.Background(), "SELECT count(*) FROM "+tabela).Scan(&n); err != nil {
		t.Fatalf("contando %s: %v", tabela, err)
	}
	return n
}

// statusDoSaldo lê o saldo direto do banco, sem passar pelo domínio.
func statusDoSaldo(t *testing.T, walletID string) int64 {
	t.Helper()

	var saldo int64
	if err := poolTeste.QueryRow(context.Background(),
		`SELECT balance_minor FROM wallets WHERE id = $1`, walletID).Scan(&saldo); err != nil {
		t.Fatalf("lendo o saldo de %s: %v", walletID, err)
	}
	return saldo
}

// ---------------------------------------------------------------------------
// 1. Fluxo completo autenticado
// ---------------------------------------------------------------------------

// TestHTTPFluxoCompletoComTokenReal percorre o caminho do README §9 com tokens
// de verdade: abrir carteira (interno), apostar (provedor), ler a carteira,
// consultar o ledger, reconciliar e ler a operação pela identidade externa.
func TestHTTPFluxoCompletoComTokenReal(t *testing.T) {
	limpar(t)
	servidor := apiDeTeste(t)

	walletID, playerID := abrirCarteiraPeloHTTP(t, servidor, "1000.00")

	// --- BET de 25.00: 201 na primeira vez ---
	bet := corpoDeOperacao("provider-a", "txn-1", playerID, walletID, "BET", "25.00")
	resp := chamar(t, servidor, http.MethodPost, "/wagering/transactions", tokenDoKeycloak(t, clientProviderA),
		bet, map[string]string{httpapi.HeaderIdempotencyKey: "provider-a:txn-1"})

	if resp.status != http.StatusCreated {
		t.Fatalf("BET = %d, quer 201: %s", resp.status, resp.texto())
	}

	var resultado struct {
		TransactionID string `json:"transactionId"`
		Status        string `json:"status"`
		Balance       *struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		} `json:"balance"`
		IdempotentReplay bool `json:"idempotentReplay"`
	}
	resp.decodifica(t, &resultado)

	if resultado.Status != "PROCESSED" {
		t.Errorf("status = %q, quer PROCESSED", resultado.Status)
	}
	if resultado.Balance == nil || resultado.Balance.Amount != "975.00" {
		t.Errorf("saldo = %v, quer 975.00", resultado.Balance)
	}
	if resultado.IdempotentReplay {
		t.Error("idempotentReplay = true na primeira execução")
	}
	if resultado.TransactionID == "" {
		t.Error("transactionId vazio")
	}

	// --- Replay: 200, MESMO saldo do processamento original ---
	resp = chamar(t, servidor, http.MethodPost, "/wagering/transactions", tokenDoKeycloak(t, clientProviderA),
		bet, map[string]string{httpapi.HeaderIdempotencyKey: "provider-a:txn-1"})

	if resp.status != http.StatusOK {
		t.Fatalf("replay = %d, quer 200: %s", resp.status, resp.texto())
	}
	var replay struct {
		TransactionID string `json:"transactionId"`
		Balance       *struct {
			Amount string `json:"amount"`
		} `json:"balance"`
		IdempotentReplay bool `json:"idempotentReplay"`
	}
	resp.decodifica(t, &replay)

	if !replay.IdempotentReplay {
		t.Error("idempotentReplay = false no reenvio")
	}
	if replay.TransactionID != resultado.TransactionID {
		t.Errorf("transactionId do replay = %q, quer o mesmo (%q)", replay.TransactionID, resultado.TransactionID)
	}
	if replay.Balance == nil || replay.Balance.Amount != "975.00" {
		t.Errorf("saldo do replay = %v, quer 975.00 (o do processamento original)", replay.Balance)
	}

	// --- Carteira: leitura interna ---
	resp = chamar(t, servidor, http.MethodGet, "/wallets/"+walletID, tokenDoKeycloak(t, clientInterno), nil, nil)
	if resp.status != http.StatusOK {
		t.Fatalf("GET /wallets/{id} = %d, quer 200: %s", resp.status, resp.texto())
	}

	// --- Ledger: duas entradas (abertura + aposta), com cursor ---
	resp = chamar(t, servidor, http.MethodGet, "/wallets/"+walletID+"/ledger?limit=50", tokenDoKeycloak(t, clientInterno), nil, nil)
	if resp.status != http.StatusOK {
		t.Fatalf("GET ledger = %d, quer 200: %s", resp.status, resp.texto())
	}
	var pagina struct {
		Entries []struct {
			Direction     string                  `json:"direction"`
			Amount        struct{ Amount string } `json:"amount"`
			BalanceBefore struct{ Amount string } `json:"balanceBefore"`
			BalanceAfter  struct{ Amount string } `json:"balanceAfter"`
		} `json:"entries"`
		NextCursor *string `json:"nextCursor"`
	}
	resp.decodifica(t, &pagina)

	if len(pagina.Entries) != 2 {
		t.Fatalf("lançamentos = %d, quer 2 (abertura + aposta): %s", len(pagina.Entries), resp.texto())
	}
	if pagina.Entries[1].Direction != "DEBIT" {
		t.Errorf("direção do segundo lançamento = %q, quer DEBIT", pagina.Entries[1].Direction)
	}
	if pagina.Entries[1].BalanceBefore.Amount != "1000.00" || pagina.Entries[1].BalanceAfter.Amount != "975.00" {
		t.Errorf("saldos do lançamento = %s → %s, quer 1000.00 → 975.00",
			pagina.Entries[1].BalanceBefore.Amount, pagina.Entries[1].BalanceAfter.Amount)
	}
	if pagina.NextCursor != nil {
		t.Error("nextCursor preenchido numa página que não encheu")
	}

	// --- Reconciliação: o exemplo do README §9 ---
	resp = chamar(t, servidor, http.MethodPost, "/wallets/"+walletID+"/reconciliation", tokenDoKeycloak(t, clientInterno), nil, nil)
	if resp.status != http.StatusOK {
		t.Fatalf("reconciliação = %d, quer 200: %s", resp.status, resp.texto())
	}
	var rec struct {
		StoredBalance     struct{ Amount string } `json:"storedBalance"`
		CalculatedBalance struct{ Amount string } `json:"calculatedBalance"`
		Difference        struct{ Amount string } `json:"difference"`
		Consistent        bool                    `json:"consistent"`
		CheckedEntries    int                     `json:"checkedEntries"`
	}
	resp.decodifica(t, &rec)

	if rec.StoredBalance.Amount != "975.00" || rec.CalculatedBalance.Amount != "975.00" {
		t.Errorf("reconciliação = %s / %s, quer 975.00 / 975.00", rec.StoredBalance.Amount, rec.CalculatedBalance.Amount)
	}
	if rec.Difference.Amount != "0.00" || !rec.Consistent {
		t.Errorf("diferença = %s / consistent = %v, quer 0.00 / true", rec.Difference.Amount, rec.Consistent)
	}
	if rec.CheckedEntries != 2 {
		t.Errorf("checkedEntries = %d, quer 2", rec.CheckedEntries)
	}

	// --- Operação pela identidade do provedor ---
	resp = chamar(t, servidor, http.MethodGet,
		"/providers/provider-a/wagering/transactions/txn-1", tokenDoKeycloak(t, clientProviderA), nil, nil)
	if resp.status != http.StatusOK {
		t.Fatalf("GET por identidade externa = %d, quer 200: %s", resp.status, resp.texto())
	}
	var txn struct {
		TransactionID string  `json:"transactionId"`
		Status        string  `json:"status"`
		Kind          string  `json:"kind"`
		FailureCode   *string `json:"failureCode"`
	}
	resp.decodifica(t, &txn)

	if txn.TransactionID != resultado.TransactionID {
		t.Errorf("transactionId = %q, quer %q", txn.TransactionID, resultado.TransactionID)
	}
	if txn.Kind != "BET" || txn.Status != "PROCESSED" {
		t.Errorf("kind/status = %s/%s, quer BET/PROCESSED", txn.Kind, txn.Status)
	}
	if txn.FailureCode != nil {
		t.Errorf("failureCode = %v, quer null numa operação concluída", *txn.FailureCode)
	}

	// --- Leitura interna por identificador interno ---
	resp = chamar(t, servidor, http.MethodGet,
		"/wagering/transactions/"+resultado.TransactionID, tokenDoKeycloak(t, clientInterno), nil, nil)
	if resp.status != http.StatusOK {
		t.Fatalf("GET /wagering/transactions/{id} = %d, quer 200: %s", resp.status, resp.texto())
	}

	// O banco confirma: um único débito e saldo 975.00.
	if n := contarLinhas(t, "wallet_ledger_entries"); n != 2 {
		t.Errorf("lançamentos no banco = %d, quer 2", n)
	}
	if saldo := statusDoSaldo(t, walletID); saldo != 97500 {
		t.Errorf("saldo no banco = %d, quer 97500", saldo)
	}
}

// ---------------------------------------------------------------------------
// 2. Contrato de status
// ---------------------------------------------------------------------------

// TestHTTPContratoDeStatus percorre cada situação que o contrato precisa
// distinguir (README §9).
func TestHTTPContratoDeStatus(t *testing.T) {
	limpar(t)
	servidor := apiDeTeste(t)

	interno := tokenDoKeycloak(t, clientInterno)
	provedor := tokenDoKeycloak(t, clientProviderA)

	walletID, playerID := abrirCarteiraPeloHTTP(t, servidor, "100.00")

	casos := []struct {
		nome    string
		metodo  string
		caminho string
		token   string
		corpo   any
		headers map[string]string
		status  int
		// envelope é o formato esperado do corpo:
		//   "erro"  -> {"error":{"code","message","failureCode"}}
		//   "wager" -> o objeto da operação, com failureCode
		//
		// A distinção não é cosmética: a REJEIÇÃO de negócio é um DESFECHO da
		// operação (ela existe, foi decidida e tem transactionId), enquanto o
		// erro de transporte é o que impede a operação de existir. Um cliente
		// que somasse 422 como "não aconteceu nada" perderia a auditoria.
		envelope string
		codigo   string
		failure  string
	}{
		{
			nome:     "sem Idempotency-Key é entrada inválida",
			metodo:   http.MethodPost,
			caminho:  "/wagering/transactions",
			token:    provedor,
			corpo:    corpoDeOperacao("provider-a", "txn-sem-chave", playerID, walletID, "BET", "10.00"),
			status:   http.StatusBadRequest,
			envelope: "erro",
			codigo:   "INVALID_REQUEST",
			failure:  "IDEMPOTENCY_KEY_REQUIRED",
		},
		{
			nome:    "campo desconhecido no corpo é recusado",
			metodo:  http.MethodPost,
			caminho: "/wagering/transactions",
			token:   provedor,
			corpo: map[string]any{
				"providerId": "provider-a", "externalTransactionId": "txn-x",
				"playerId": playerID, "walletId": walletID, "roundId": "r", "gameId": "g",
				"kind": "BET", "money": map[string]any{"amount": "10.00", "currency": "BRL"},
				"campoInventado": true,
			},
			headers:  map[string]string{httpapi.HeaderIdempotencyKey: "provider-a:txn-x"},
			status:   http.StatusBadRequest,
			envelope: "erro",
			codigo:   "INVALID_REQUEST",
		},
		{
			nome:     "OPENING enviado de fora é proibido",
			metodo:   http.MethodPost,
			caminho:  "/wagering/transactions",
			token:    provedor,
			corpo:    corpoDeOperacao("provider-a", "txn-opening", playerID, walletID, "OPENING", "10.00"),
			headers:  map[string]string{httpapi.HeaderIdempotencyKey: "provider-a:txn-opening"},
			status:   http.StatusForbidden,
			envelope: "erro",
			codigo:   "FORBIDDEN",
		},
		{
			nome:     "aposta sem saldo é rejeição de negócio",
			metodo:   http.MethodPost,
			caminho:  "/wagering/transactions",
			token:    provedor,
			corpo:    corpoDeOperacao("provider-a", "txn-cara", playerID, walletID, "BET", "10000.00"),
			headers:  map[string]string{httpapi.HeaderIdempotencyKey: "provider-a:txn-cara"},
			status:   http.StatusUnprocessableEntity,
			envelope: "wager",
			failure:  "INSUFFICIENT_FUNDS",
		},
		{
			nome:     "carteira inexistente",
			metodo:   http.MethodGet,
			caminho:  "/wallets/" + novoUUID(),
			token:    interno,
			status:   http.StatusNotFound,
			envelope: "erro",
			codigo:   "NOT_FOUND",
		},
		{
			nome:     "carteira com identificador malformado é não encontrado (e não 500)",
			metodo:   http.MethodGet,
			caminho:  "/wallets/nao-e-uuid",
			token:    interno,
			status:   http.StatusNotFound,
			envelope: "erro",
			codigo:   "NOT_FOUND",
		},
		{
			nome:     "transação inexistente",
			metodo:   http.MethodGet,
			caminho:  "/wagering/transactions/" + novoUUID(),
			token:    interno,
			status:   http.StatusNotFound,
			envelope: "erro",
			codigo:   "NOT_FOUND",
		},
		{
			nome:     "limit do ledger não numérico é entrada inválida",
			metodo:   http.MethodGet,
			caminho:  "/wallets/" + walletID + "/ledger?limit=abc",
			token:    interno,
			status:   http.StatusBadRequest,
			envelope: "erro",
			codigo:   "INVALID_REQUEST",
		},
		{
			// O cursor é OPACO: o cliente devolve o que recebeu, não monta. Um
			// valor inventado é erro de ENTRADA (400), e não 500.
			nome:     "cursor inválido é entrada inválida",
			metodo:   http.MethodGet,
			caminho:  "/wallets/" + walletID + "/ledger?cursor=" + url.QueryEscape("###nao-e-cursor###"),
			token:    interno,
			status:   http.StatusBadRequest,
			envelope: "erro",
			codigo:   "INVALID_REQUEST",
		},
		{
			nome:    "moeda diferente da carteira é rejeição gravada",
			metodo:  http.MethodPost,
			caminho: "/wagering/transactions",
			token:   provedor,
			corpo: map[string]any{
				"providerId": "provider-a", "externalTransactionId": "txn-usd",
				"playerId": playerID, "walletId": walletID, "roundId": "r", "gameId": "g",
				"kind": "BET", "money": map[string]any{"amount": "10.00", "currency": "USD"},
			},
			headers:  map[string]string{httpapi.HeaderIdempotencyKey: "provider-a:txn-usd"},
			status:   http.StatusUnprocessableEntity,
			envelope: "wager",
			failure:  "WALLET_CURRENCY_MISMATCH",
		},
		{
			nome:     "reversão sem referência é entrada inválida",
			metodo:   http.MethodPost,
			caminho:  "/wagering/transactions",
			token:    provedor,
			corpo:    corpoDeOperacao("provider-a", "txn-refund", playerID, walletID, "REFUND", "10.00"),
			headers:  map[string]string{httpapi.HeaderIdempotencyKey: "provider-a:txn-refund"},
			status:   http.StatusBadRequest,
			envelope: "erro",
			codigo:   "INVALID_REQUEST",
			failure:  "REFERENCE_REQUIRED",
		},
		{
			nome:     "LOSS com valor diferente de zero é entrada inválida",
			metodo:   http.MethodPost,
			caminho:  "/wagering/transactions",
			token:    provedor,
			corpo:    corpoDeOperacao("provider-a", "txn-loss", playerID, walletID, "LOSS", "1.00"),
			headers:  map[string]string{httpapi.HeaderIdempotencyKey: "provider-a:txn-loss"},
			status:   http.StatusBadRequest,
			envelope: "erro",
			codigo:   "INVALID_REQUEST",
			failure:  "INVALID_AMOUNT",
		},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			resp := chamar(t, servidor, caso.metodo, caso.caminho, caso.token, caso.corpo, caso.headers)

			if resp.status != caso.status {
				t.Fatalf("status = %d, quer %d: %s", resp.status, caso.status, resp.texto())
			}

			if caso.envelope == "wager" {
				var desfecho struct {
					TransactionID string  `json:"transactionId"`
					Status        string  `json:"status"`
					FailureCode   *string `json:"failureCode"`
					Balance       any     `json:"balance"`
				}
				resp.decodifica(t, &desfecho)

				if desfecho.Status != "REJECTED" {
					t.Errorf("status do desfecho = %q, quer REJECTED", desfecho.Status)
				}
				if desfecho.TransactionID == "" {
					t.Error("rejeição sem transactionId: a operação é auditável e precisa ser identificável")
				}
				if desfecho.FailureCode == nil || *desfecho.FailureCode != caso.failure {
					t.Errorf("failureCode = %v, quer %q", desfecho.FailureCode, caso.failure)
				}
				if desfecho.Balance != nil {
					t.Errorf("balance = %v, quer null numa rejeição (nada foi movimentado)", desfecho.Balance)
				}
			} else {
				var envelope struct {
					Error struct {
						Code        string `json:"code"`
						Message     string `json:"message"`
						FailureCode string `json:"failureCode"`
					} `json:"error"`
				}
				resp.decodifica(t, &envelope)

				if envelope.Error.Code != caso.codigo {
					t.Errorf("code = %q, quer %q", envelope.Error.Code, caso.codigo)
				}
				if caso.failure != "" && envelope.Error.FailureCode != caso.failure {
					t.Errorf("failureCode = %q, quer %q", envelope.Error.FailureCode, caso.failure)
				}
				if envelope.Error.Message == "" {
					t.Error("mensagem pública vazia")
				}
			}

			// Nada de detalhe de driver, stack ou SQL no corpo.
			for _, proibido := range []string{"pgx", "SQLSTATE", "goroutine", "SELECT ", "INSERT "} {
				if strings.Contains(resp.texto(), proibido) {
					t.Errorf("a resposta vazou detalhe interno (%q): %s", proibido, resp.texto())
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 3. Conflito de idempotência
// ---------------------------------------------------------------------------

// A mesma chave com conteúdo diferente ⇒ 409. É o que impede que um reenvio
// "parecido" seja tratado como replay e o cliente acredite que apostou outro
// valor.
func TestHTTPConflitoDeIdempotencia(t *testing.T) {
	limpar(t)
	servidor := apiDeTeste(t)

	walletID, playerID := abrirCarteiraPeloHTTP(t, servidor, "500.00")
	provedor := tokenDoKeycloak(t, clientProviderA)

	primeira := corpoDeOperacao("provider-a", "txn-conf-1", playerID, walletID, "BET", "10.00")
	resp := chamar(t, servidor, http.MethodPost, "/wagering/transactions", provedor,
		primeira, map[string]string{httpapi.HeaderIdempotencyKey: "provider-a:conf"})
	if resp.status != http.StatusCreated {
		t.Fatalf("primeira BET = %d, quer 201: %s", resp.status, resp.texto())
	}

	// Mesma chave, valor diferente.
	diferente := corpoDeOperacao("provider-a", "txn-conf-1", playerID, walletID, "BET", "20.00")
	resp = chamar(t, servidor, http.MethodPost, "/wagering/transactions", provedor,
		diferente, map[string]string{httpapi.HeaderIdempotencyKey: "provider-a:conf"})

	if resp.status != http.StatusConflict {
		t.Fatalf("chave reutilizada = %d, quer 409: %s", resp.status, resp.texto())
	}

	// E o saldo não pode ter mudado.
	if saldo := statusDoSaldo(t, walletID); saldo != 49000 {
		t.Errorf("saldo = %d, quer 49000 (o conflito não pode movimentar nada)", saldo)
	}
}

// ---------------------------------------------------------------------------
// 4. Autenticação e autorização (eliminatório)
// ---------------------------------------------------------------------------

// TestHTTPRejeitaCredenciaisAusentesOuInvalidas cobre token ausente, malformado,
// de outro emissor e com assinatura inválida.
func TestHTTPRejeitaCredenciaisAusentesOuInvalidas(t *testing.T) {
	limpar(t)
	servidor := apiDeTeste(t)

	walletID, playerID := abrirCarteiraPeloHTTP(t, servidor, "100.00")
	_ = playerID

	casos := []struct {
		nome   string
		token  string
		status int
	}{
		{"sem token", "", http.StatusUnauthorized},
		{"token lixo", "nao-e-um-jwt", http.StatusUnauthorized},
		{"token com três partes falsas", "aaa.bbb.ccc", http.StatusUnauthorized},
		{"token expirado (assinado por outra chave)", "eyJhbGciOiJSUzI1NiIsImtpZCI6ImZha2UifQ.eyJpc3MiOiJodHRwOi8vbG9jYWxob3N0OjgwODAvcmVhbG1zL2p1bmdsZWciLCJzdWIiOiJ4IiwiYXVkIjoianVuZ2xlZy1hcGkiLCJleHAiOjF9.aaaa", http.StatusUnauthorized},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			resp := chamar(t, servidor, http.MethodGet, "/wallets/"+walletID, caso.token, nil, nil)
			if resp.status != caso.status {
				t.Fatalf("status = %d, quer %d: %s", resp.status, caso.status, resp.texto())
			}
		})
	}

	// Nenhum efeito financeiro: o ledger continua com uma entrada (a abertura).
	if n := contarLinhas(t, "wallet_ledger_entries"); n != 1 {
		t.Errorf("lançamentos = %d, quer 1 — acesso negado não pode movimentar nada", n)
	}
}

// O eliminatório mais importante: um provedor NÃO pode abrir carteira, ler
// carteira nem reconciliar — e a tentativa não pode produzir efeito financeiro.
func TestHTTPProvedorNaoExecutaOperacaoInterna(t *testing.T) {
	limpar(t)
	servidor := apiDeTeste(t)

	provedor := tokenDoKeycloak(t, clientProviderA)

	// Carteira existente, criada com o token interno, para a leitura ter alvo.
	walletID, playerID := abrirCarteiraPeloHTTP(t, servidor, "300.00")

	carteirasAntes := contarLinhas(t, "wallets")
	ledgerAntes := contarLinhas(t, "wallet_ledger_entries")
	transacoesAntes := contarLinhas(t, "wager_transactions")

	casos := []struct {
		nome    string
		metodo  string
		caminho string
		corpo   any
	}{
		{"abrir carteira", http.MethodPost, "/wallets", corpoDeAbertura(novoUUID(), "1000.00")},
		{"ler carteira", http.MethodGet, "/wallets/" + walletID, nil},
		{"listar ledger", http.MethodGet, "/wallets/" + walletID + "/ledger", nil},
		{"reconciliar", http.MethodPost, "/wallets/" + walletID + "/reconciliation", nil},
		{"ler transação interna por id", http.MethodGet, "/wagering/transactions/" + novoUUID(), nil},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			resp := chamar(t, servidor, caso.metodo, caso.caminho, provedor, caso.corpo, nil)
			if resp.status != http.StatusForbidden {
				t.Fatalf("status = %d, quer 403: %s", resp.status, resp.texto())
			}
			// A resposta não pode revelar o recurso.
			if strings.Contains(resp.texto(), walletID) || strings.Contains(resp.texto(), playerID) {
				t.Errorf("a resposta de 403 vazou dados do recurso: %s", resp.texto())
			}
		})
	}

	// NENHUM efeito financeiro: as contagens não podem ter mudado.
	if depois := contarLinhas(t, "wallets"); depois != carteirasAntes {
		t.Errorf("carteiras = %d, quer %d — acesso negado criou carteira", depois, carteirasAntes)
	}
	if depois := contarLinhas(t, "wallet_ledger_entries"); depois != ledgerAntes {
		t.Errorf("lançamentos = %d, quer %d — acesso negado mexeu no ledger", depois, ledgerAntes)
	}
	if depois := contarLinhas(t, "wager_transactions"); depois != transacoesAntes {
		t.Errorf("transações = %d, quer %d — acesso negado gravou transação", depois, transacoesAntes)
	}
}

// O serviço interno NÃO pode enviar operação de provedor: a porta é a do
// provedor, e o corpo não escolhe a identidade.
func TestHTTPInternoNaoEnviaOperacaoDeProvedor(t *testing.T) {
	limpar(t)
	servidor := apiDeTeste(t)

	walletID, playerID := abrirCarteiraPeloHTTP(t, servidor, "100.00")

	resp := chamar(t, servidor, http.MethodPost, "/wagering/transactions", tokenDoKeycloak(t, clientInterno),
		corpoDeOperacao("provider-a", "txn-interno", playerID, walletID, "BET", "10.00"),
		map[string]string{httpapi.HeaderIdempotencyKey: "interno:txn-interno"})

	if resp.status != http.StatusForbidden {
		t.Fatalf("status = %d, quer 403 (token interno não é de provedor): %s", resp.status, resp.texto())
	}
	if n := contarLinhas(t, "wallet_ledger_entries"); n != 1 {
		t.Errorf("lançamentos = %d, quer 1 — nada pode ter se movimentado", n)
	}
}

// ---------------------------------------------------------------------------
// 5. Isolamento entre provedores
// ---------------------------------------------------------------------------

// O provedor não escolhe o provedor pelo corpo, e não alcança operação alheia.
func TestHTTPIsolamentoEntreProvedores(t *testing.T) {
	limpar(t)
	servidor := apiDeTeste(t)

	walletID, playerID := abrirCarteiraPeloHTTP(t, servidor, "1000.00")

	// provider-a registra a operação.
	resp := chamar(t, servidor, http.MethodPost, "/wagering/transactions", tokenDoKeycloak(t, clientProviderA),
		corpoDeOperacao("provider-a", "txn-isolamento", playerID, walletID, "BET", "10.00"),
		map[string]string{httpapi.HeaderIdempotencyKey: "provider-a:txn-isolamento"})
	if resp.status != http.StatusCreated {
		t.Fatalf("BET de provider-a = %d, quer 201: %s", resp.status, resp.texto())
	}

	// (1) provider-b pedindo o `providerId` de provider-a no corpo ⇒ 403.
	resp = chamar(t, servidor, http.MethodPost, "/wagering/transactions", tokenDoKeycloak(t, clientProviderB),
		corpoDeOperacao("provider-a", "txn-forjado", playerID, walletID, "BET", "10.00"),
		map[string]string{httpapi.HeaderIdempotencyKey: "provider-b:txn-forjado"})
	if resp.status != http.StatusForbidden {
		t.Fatalf("corpo com providerId alheio = %d, quer 403: %s", resp.status, resp.texto())
	}

	// (2) `providerId` do path diferente do token ⇒ 403.
	resp = chamar(t, servidor, http.MethodGet,
		"/providers/provider-a/wagering/transactions/txn-isolamento", tokenDoKeycloak(t, clientProviderB), nil, nil)
	if resp.status != http.StatusForbidden {
		t.Fatalf("consulta de outro provedor no path = %d, quer 403: %s", resp.status, resp.texto())
	}

	// (3) provider-b lendo a PRÓPRIA rota, mas o id externo de provider-a ⇒ 404
	// (não 403: devolver 403 confirmaria que a operação existe).
	resp = chamar(t, servidor, http.MethodGet,
		"/providers/provider-b/wagering/transactions/txn-isolamento", tokenDoKeycloak(t, clientProviderB), nil, nil)
	if resp.status != http.StatusNotFound {
		t.Fatalf("id externo alheio na própria rota = %d, quer 404: %s", resp.status, resp.texto())
	}
	if strings.Contains(resp.texto(), "provider-a") {
		t.Errorf("a resposta de 404 vazou o provedor dono da operação: %s", resp.texto())
	}

	// (4) A MESMA identidade externa em outro provedor é outra operação.
	//
	// Este caso é o coração do isolamento, e a primeira versão deste teste
	// esperava um conflito — estava ERRADA. A identidade financeira é o PAR
	// `(providerId, externalTransactionId)` (README §9: "uma operação
	// financeira identificada por (providerId, externalTransactionId)"), então
	// `provider-b` tem todo o direito de registrar a própria `txn-isolamento`, e
	// ela é OUTRA operação. O que não pode acontecer é ele receber o resultado,
	// o saldo ou o `transactionId` da operação de `provider-a`.
	resp = chamar(t, servidor, http.MethodPost, "/wagering/transactions", tokenDoKeycloak(t, clientProviderB),
		corpoDeOperacao("provider-b", "txn-isolamento", playerID, walletID, "BET", "10.00"),
		map[string]string{httpapi.HeaderIdempotencyKey: "provider-b:txn-isolamento"})

	if resp.status != http.StatusCreated {
		t.Fatalf("BET própria de provider-b = %d, quer 201: %s", resp.status, resp.texto())
	}
	var deB struct {
		TransactionID    string `json:"transactionId"`
		IdempotentReplay bool   `json:"idempotentReplay"`
	}
	resp.decodifica(t, &deB)

	if deB.IdempotentReplay {
		t.Error("provider-b recebeu IdempotentReplay: ele estaria vendo a operação de provider-a")
	}

	// O replay DELE devolve o resultado DELE — nunca o do outro provedor.
	resp = chamar(t, servidor, http.MethodPost, "/wagering/transactions", tokenDoKeycloak(t, clientProviderB),
		corpoDeOperacao("provider-b", "txn-isolamento", playerID, walletID, "BET", "10.00"),
		map[string]string{httpapi.HeaderIdempotencyKey: "provider-b:txn-isolamento"})
	if resp.status != http.StatusOK {
		t.Fatalf("replay de provider-b = %d, quer 200: %s", resp.status, resp.texto())
	}
	var replayDeB struct {
		TransactionID    string `json:"transactionId"`
		IdempotentReplay bool   `json:"idempotentReplay"`
	}
	resp.decodifica(t, &replayDeB)

	if !replayDeB.IdempotentReplay || replayDeB.TransactionID != deB.TransactionID {
		t.Errorf("replay de provider-b devolveu transactionId %q; quer o dele (%q)",
			replayDeB.TransactionID, deB.TransactionID)
	}

	// provider-b continua sem alcançar a operação de provider-a.
	resp = chamar(t, servidor, http.MethodGet,
		"/providers/provider-b/wagering/transactions/txn-forjado", tokenDoKeycloak(t, clientProviderB), nil, nil)
	if resp.status != http.StatusNotFound {
		t.Errorf("id externo de operação de provider-a visto por provider-b = %d, quer 404", resp.status)
	}

	// DOIS débitos: a BET de provider-a e a de provider-b. Nunca três.
	if n := contarLinhas(t, "wallet_ledger_entries"); n != 3 {
		t.Errorf("lançamentos = %d, quer 3 (abertura + BET de A + BET de B)", n)
	}
	if saldo := statusDoSaldo(t, walletID); saldo != 98000 {
		t.Errorf("saldo = %d, quer 98000 — dois débitos de 10.00", saldo)
	}
}

// ---------------------------------------------------------------------------
// 6. IdP fora do ar
// ---------------------------------------------------------------------------

// Com o IdP inacessível, a rota de negócio responde 503 e NÃO produz efeito
// financeiro. 503 (e não 401) porque o token pode estar perfeito.
func TestHTTPIdPForaDoArResponde503SemEfeito(t *testing.T) {
	limpar(t)
	servidor := apiDeTeste(t)

	walletID, playerID := abrirCarteiraPeloHTTP(t, servidor, "100.00")
	ledgerAntes := contarLinhas(t, "wallet_ledger_entries")

	// Servidor OIDC inalcançável: porta fechada no loopback.
	morto := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endereco := morto.URL
	morto.Close()

	verificador := auth.New(auth.Config{
		Issuer:       endereco,
		DiscoveryURL: endereco,
		Audience:     "jungleg-api",
		HTTPTimeout:  500 * time.Millisecond,
	})
	autenticador := auth.NewAutenticador(verificador, observability.DiscardLogger())

	cfg := &config.Config{Env: "test", HTTP: config.HTTPConfig{RequestTimeout: 5 * time.Second, MaxBodyBytes: 1 << 20}}
	logger := observability.DiscardLogger()
	rotas := httpapi.NewRouter(cfg, logger, observability.NewMetrics(), nil)
	handlers := httpapi.NewHandlers(httpapi.HandlersDeps{
		Service: servico, Routes: rotas, Authenticator: autenticador,
		Logger: logger, Metrics: observability.NewMetrics(),
	})
	handlers.RegistrarRotas()

	semIdP := httptest.NewServer(rotas.Base)
	defer semIdP.Close()

	resp := chamar(t, semIdP, http.MethodPost, "/wagering/transactions", tokenDoKeycloak(t, clientProviderA),
		corpoDeOperacao("provider-a", "txn-idp-fora", playerID, walletID, "BET", "10.00"),
		map[string]string{httpapi.HeaderIdempotencyKey: "provider-a:txn-idp-fora"})

	if resp.status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, quer 503 com o IdP fora do ar: %s", resp.status, resp.texto())
	}
	if resp.header.Get("Retry-After") == "" {
		t.Error("503 sem Retry-After")
	}

	// NENHUM efeito financeiro.
	if depois := contarLinhas(t, "wallet_ledger_entries"); depois != ledgerAntes {
		t.Errorf("lançamentos = %d, quer %d — IdP fora do ar mexeu no ledger", depois, ledgerAntes)
	}
	if saldo := statusDoSaldo(t, walletID); saldo != 10000 {
		t.Errorf("saldo = %d, quer 10000", saldo)
	}
}

// ---------------------------------------------------------------------------
// 7. Endpoints públicos continuam públicos
// ---------------------------------------------------------------------------

// Health e métricas não exigem token: o Kubernetes/Docker precisa consultá-los
// sem credencial de negócio.
func TestHTTPHealthEMetricasSaoPublicos(t *testing.T) {
	servidor := apiDeTeste(t)

	for _, caminho := range []string{"/health/live", "/metrics"} {
		resp := chamar(t, servidor, http.MethodGet, caminho, "", nil, nil)
		if resp.status != http.StatusOK {
			t.Errorf("GET %s = %d, quer 200 sem token", caminho, resp.status)
		}
	}

	// Readiness com o Postgres de pé: 200 e a dependência reportada.
	resp := chamar(t, servidor, http.MethodGet, "/health/ready", "", nil, nil)
	if resp.status != http.StatusOK {
		t.Errorf("GET /health/ready = %d, quer 200: %s", resp.status, resp.texto())
	}
}

// ---------------------------------------------------------------------------
// utilidades
// ---------------------------------------------------------------------------

// novoUUID gera um identificador para os corpos dos testes.
func novoUUID() string { return uuid.NewString() }

// envOu lê a variável de ambiente ou usa o padrão.
func envOu(chave, padrao string) string {
	if v := strings.TrimSpace(os.Getenv(chave)); v != "" {
		return v
	}
	return padrao
}
