package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
	"github.com/FeliPoxZ/jungle_desafio/internal/infra/auth"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
)

// Testes de unidade dos HANDLERS, com dublês.
//
// O que eles cobrem — e por que valem a pena, tendo integração com IdP e banco:
//
//   - os caminhos que a integração não alcança (corpo ausente, serviço devolvendo
//     desfecho que a borda precisa traduzir);
//   - a ORDEM das decisões (provedor do corpo ANTES de chamar o serviço; chave de
//     idempotência derivada da abertura);
//   - o que o handler NÃO pode fazer (tocar o serviço quando a entrada é
//     inválida, vazar erro de serviço na mensagem pública).
//
// A prova de que o token real, o banco real e as constraints funcionam fica nos
// testes de integração (`test/integration` e `internal/infra/auth`).

// --- Dublês -----------------------------------------------------------------

// fakeServico registra o que a borda pediu e devolve o que o teste mandou.
type fakeServico struct {
	resultado      appwagering.Result
	erro           error
	ultimoPedido   appwagering.Request
	ultimaAbertura appwagering.OpenWalletRequest
	pedidos        int
	aberturas      int

	aberturaResultado appwagering.OpenWalletResult
	aberturaErro      error
}

func (f *fakeServico) Process(_ context.Context, req appwagering.Request) (appwagering.Result, error) {
	f.pedidos++
	f.ultimoPedido = req
	return f.resultado, f.erro
}

func (f *fakeServico) OpenWallet(_ context.Context, req appwagering.OpenWalletRequest) (appwagering.OpenWalletResult, error) {
	f.aberturas++
	f.ultimaAbertura = req
	return f.aberturaResultado, f.aberturaErro
}

func (f *fakeServico) GetTransaction(context.Context, wagerdomain.TransactionID) (*wagerdomain.WagerTransaction, error) {
	return nil, appwagering.ErrTransactionNotFound
}

func (f *fakeServico) GetProviderTransaction(context.Context, string, wagerdomain.TransactionID) (*wagerdomain.WagerTransaction, error) {
	return nil, appwagering.ErrTransactionNotFound
}

func (f *fakeServico) GetByProviderExternalID(context.Context, string, string) (*wagerdomain.WagerTransaction, error) {
	return nil, appwagering.ErrTransactionNotFound
}

func (f *fakeServico) GetWallet(context.Context, ids.WalletID) (*wallet.Wallet, error) {
	return nil, appwagering.ErrWalletNotFound
}

func (f *fakeServico) ListLedger(context.Context, ids.WalletID, int, string) (appwagering.LedgerPage, error) {
	return appwagering.LedgerPage{}, appwagering.ErrWalletNotFound
}

func (f *fakeServico) Reconcile(context.Context, ids.WalletID) (appwagering.Reconciliation, error) {
	return appwagering.Reconciliation{}, appwagering.ErrWalletNotFound
}

// fakeAuth injeta a identidade no contexto sem validar token nenhum.
//
// Ele existe para provar que o handler USA a identidade do contexto — e não o
// corpo da requisição — para decidir o provedor.
type fakeAuth struct {
	identidade auth.Identity
}

func (f *fakeAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), f.identidade)))
	})
}

func (f *fakeAuth) RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := auth.IdentityFrom(r.Context())
			if !ok {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			for _, role := range roles {
				if id.HasRole(role) {
					next.ServeHTTP(w, r)
					return
				}
			}
			w.WriteHeader(http.StatusForbidden)
		})
	}
}

func (f *fakeAuth) RequireProvider() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := auth.IdentityFrom(r.Context())
			if !ok {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if id.ProviderID == "" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// --- Infra de teste ---------------------------------------------------------

// ambienteHandler monta a borda com dublês e devolve o servidor de teste.
func ambienteHandler(t *testing.T, identidade auth.Identity) (*httptest.Server, *fakeServico) {
	t.Helper()

	servico := &fakeServico{}
	autenticador := &fakeAuth{identidade: identidade}

	cfg := &config.Config{
		Env:  "test",
		HTTP: config.HTTPConfig{RequestTimeout: 5 * time.Second, MaxBodyBytes: 1 << 20},
	}
	logger := observability.DiscardLogger()
	rotas := NewRouter(cfg, logger, observability.NewMetrics(), nil)

	handlers := NewHandlers(HandlersDeps{
		Service:       servico,
		Routes:        rotas,
		Authenticator: autenticador,
		Logger:        logger,
		Metrics:       observability.NewMetrics(),
		Clock:         func() time.Time { return time.Unix(0, 0) },
	})
	handlers.RegistrarRotas()

	servidor := httptest.NewServer(rotas.Base)
	t.Cleanup(servidor.Close)
	return servidor, servico
}

// chamarHandler faz a requisição e devolve a resposta crua.
func chamarHandler(t *testing.T, servidor *httptest.Server, metodo, caminho, corpo string, headers map[string]string) *http.Response {
	t.Helper()

	var leitor *strings.Reader
	if corpo != "" {
		leitor = strings.NewReader(corpo)
	} else {
		leitor = strings.NewReader("")
	}

	req, err := http.NewRequestWithContext(context.Background(), metodo, servidor.URL+caminho, leitor)
	if err != nil {
		t.Fatalf("montando a requisição: %v", err)
	}
	if corpo != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := servidor.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", metodo, caminho, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// identidadeProvedorA é a identidade de um provedor autenticado.
func identidadeProvedorA() auth.Identity {
	return auth.Identity{Subject: "service-account-provider-a", ProviderID: "provider-a", Roles: []string{"provider"}}
}

// identidadeInterna é a identidade do serviço interno.
func identidadeInterna() auth.Identity {
	return auth.Identity{Subject: "service-account-internal", Roles: []string{"internal"}}
}

// --- Envio de operação ------------------------------------------------------

// O `providerId` do CORPO não escolhe o provedor: o do TOKEN é que vale, e a
// divergência é 403 — sem sequer chamar o caso de uso.
func TestEnviarOperacaoProvedorDivergenteEh403(t *testing.T) {
	servidor, servico := ambienteHandler(t, identidadeProvedorA())

	corpo := `{"providerId":"provider-b","externalTransactionId":"x","playerId":"p","walletId":"0192f291-27dd-7d3f-8071-5f8685deef37","roundId":"r","gameId":"g","kind":"BET","money":{"amount":"10.00","currency":"BRL"}}`

	resp := chamarHandler(t, servidor, http.MethodPost, "/wagering/transactions", corpo,
		map[string]string{HeaderIdempotencyKey: "provider-a:x"})

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, quer 403", resp.StatusCode)
	}
	if servico.pedidos != 0 {
		t.Error("o caso de uso foi chamado mesmo com o provedor divergente")
	}
}

// Sem chave de idempotência ⇒ 400 e NADA é chamado: a chave é requisito de
// contrato, não decisão de negócio.
func TestEnviarOperacaoSemChaveEh400(t *testing.T) {
	servidor, servico := ambienteHandler(t, identidadeProvedorA())

	corpo := `{"externalTransactionId":"x","playerId":"p","walletId":"0192f291-27dd-7d3f-8071-5f8685deef37","roundId":"r","gameId":"g","kind":"BET","money":{"amount":"10.00","currency":"BRL"}}`

	resp := chamarHandler(t, servidor, http.MethodPost, "/wagering/transactions", corpo, nil)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, quer 400", resp.StatusCode)
	}
	if servico.pedidos != 0 {
		t.Error("o caso de uso foi chamado sem chave de idempotência")
	}

	var envelope struct {
		Error struct {
			FailureCode string `json:"failureCode"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("corpo não é JSON: %v", err)
	}
	if envelope.Error.FailureCode != appwagering.CodeIdempotencyKeyRequired {
		t.Errorf("failureCode = %q, quer %q", envelope.Error.FailureCode, appwagering.CodeIdempotencyKeyRequired)
	}
}

// O provedor vem do TOKEN: o pedido que chega ao caso de uso tem o providerId
// autenticado, mesmo quando o corpo o omite.
func TestEnviarOperacaoUsaProvedorDoToken(t *testing.T) {
	servidor, servico := ambienteHandler(t, identidadeProvedorA())
	servico.resultado = appwagering.Result{
		TransactionID: "0192f298-345e-7e38-af88-e43f851a819d",
		Status:        wagerdomain.StatusProcessed,
		Balance:       ponteiroMoney(t, "990.00"),
	}

	corpo := `{"externalTransactionId":"x","playerId":"p","walletId":"0192f291-27dd-7d3f-8071-5f8685deef37","roundId":"r","gameId":"g","kind":"BET","money":{"amount":"10.00","currency":"BRL"}}`

	resp := chamarHandler(t, servidor, http.MethodPost, "/wagering/transactions", corpo,
		map[string]string{HeaderIdempotencyKey: "provider-a:x"})

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, quer 201 (body lido abaixo)", resp.StatusCode)
	}
	if servico.ultimoPedido.ProviderID != "provider-a" {
		t.Errorf("ProviderID no caso de uso = %q, quer provider-a (do token)", servico.ultimoPedido.ProviderID)
	}
}

// Rejeição de negócio sai como 422 com o corpo do DESFECHO (transactionId e
// failureCode), e não como envelope de erro.
func TestEnviarOperacaoRejeitadaEh422ComDesfecho(t *testing.T) {
	servidor, servico := ambienteHandler(t, identidadeProvedorA())
	servico.resultado = appwagering.Result{
		TransactionID: "0192f298-345e-7e38-af88-e43f851a819d",
		Status:        wagerdomain.StatusRejected,
		FailureCode:   "INSUFFICIENT_FUNDS",
	}

	corpo := `{"externalTransactionId":"x","playerId":"p","walletId":"0192f291-27dd-7d3f-8071-5f8685deef37","roundId":"r","gameId":"g","kind":"BET","money":{"amount":"10.00","currency":"BRL"}}`

	resp := chamarHandler(t, servidor, http.MethodPost, "/wagering/transactions", corpo,
		map[string]string{HeaderIdempotencyKey: "provider-a:x"})

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, quer 422", resp.StatusCode)
	}

	var desfecho struct {
		TransactionID string  `json:"transactionId"`
		Status        string  `json:"status"`
		FailureCode   *string `json:"failureCode"`
		Balance       any     `json:"balance"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&desfecho); err != nil {
		t.Fatalf("corpo não é JSON: %v", err)
	}
	if desfecho.Status != "REJECTED" || desfecho.FailureCode == nil || *desfecho.FailureCode != "INSUFFICIENT_FUNDS" {
		t.Errorf("desfecho = %+v, quer REJECTED/INSUFFICIENT_FUNDS", desfecho)
	}
	if desfecho.TransactionID == "" {
		t.Error("rejeição sem transactionId")
	}
	if desfecho.Balance != nil {
		t.Errorf("balance = %v, quer null", desfecho.Balance)
	}
}

// Erro de infraestrutura do caso de uso ⇒ 503, e a mensagem pública NÃO repete
// o erro interno.
func TestEnviarOperacaoErroTransitorioEh503(t *testing.T) {
	servidor, servico := ambienteHandler(t, identidadeProvedorA())
	servico.erro = errors.New("postgres: lock timeout (SQLSTATE 55P03) em wallets")

	corpo := `{"externalTransactionId":"x","playerId":"p","walletId":"0192f291-27dd-7d3f-8071-5f8685deef37","roundId":"r","gameId":"g","kind":"BET","money":{"amount":"10.00","currency":"BRL"}}`

	resp := chamarHandler(t, servidor, http.MethodPost, "/wagering/transactions", corpo,
		map[string]string{HeaderIdempotencyKey: "provider-a:x"})

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, quer 500 para erro não classificado", resp.StatusCode)
	}

	// Agora o erro transitório de verdade, pelo sentinela do contrato.
	servico.erro = errTransitorio()
	resp = chamarHandler(t, servidor, http.MethodPost, "/wagering/transactions", corpo,
		map[string]string{HeaderIdempotencyKey: "provider-a:x"})

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, quer 503 para falha transitória", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("503 sem Retry-After")
	}

	bruto := corpoDeResposta(t, resp)
	for _, proibido := range []string{"SQLSTATE", "postgres", "55P03", "goroutine"} {
		if strings.Contains(bruto, proibido) {
			t.Errorf("a resposta vazou detalhe interno (%q): %s", proibido, bruto)
		}
	}
}

// --- Abertura de carteira ---------------------------------------------------

// Corpo sem `initialBalance` ⇒ 400, sem chamar o caso de uso. O campo é
// PONTEIRO justamente para essa distinção: ausente é erro, `"0.00"` é válido.
func TestAbrirCarteiraSemSaldoInicialEh400(t *testing.T) {
	servidor, servico := ambienteHandler(t, identidadeInterna())

	resp := chamarHandler(t, servidor, http.MethodPost, "/wallets",
		`{"playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1"}`, nil)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, quer 400", resp.StatusCode)
	}
	if servico.aberturas != 0 {
		t.Error("o caso de uso foi chamado sem initialBalance")
	}
}

// Saldo inicial "0.00" é ACEITO (README §9) e chega ao caso de uso.
func TestAbrirCarteiraComSaldoZeroEhAceito(t *testing.T) {
	servidor, servico := ambienteHandler(t, identidadeInterna())
	servico.aberturaResultado = appwagering.OpenWalletResult{
		WalletID: "0192f291-27dd-7d3f-8071-5f8685deef37",
		PlayerID: "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
		Balance:  *ponteiroMoney(t, "0.00"),
		Version:  1,
	}

	resp := chamarHandler(t, servidor, http.MethodPost, "/wallets",
		`{"playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1","initialBalance":{"amount":"0.00","currency":"BRL"}}`, nil)

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, quer 201", resp.StatusCode)
	}
	if servico.aberturas != 1 {
		t.Fatal("o caso de uso não foi chamado")
	}
	if servico.ultimaAbertura.InitialBalance.Minor() != 0 {
		t.Errorf("saldo inicial = %d, quer 0", servico.ultimaAbertura.InitialBalance.Minor())
	}
}

// Sem `Idempotency-Key` no header, a borda DERIVA uma chave determinística do
// pedido — e o mesmo pedido reenviado cai na mesma chave, virando replay.
func TestAbrirCarteiraDerivaChaveDeterministica(t *testing.T) {
	servidor, servico := ambienteHandler(t, identidadeInterna())
	servico.aberturaResultado = appwagering.OpenWalletResult{
		WalletID: "0192f291-27dd-7d3f-8071-5f8685deef37",
		PlayerID: "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
		Balance:  *ponteiroMoney(t, "1000.00"),
		Version:  1,
	}

	corpo := `{"playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1","initialBalance":{"amount":"1000.00","currency":"BRL"}}`

	primeira := chamarHandler(t, servidor, http.MethodPost, "/wallets", corpo, nil)
	if primeira.StatusCode != http.StatusCreated {
		t.Fatalf("primeira abertura = %d, quer 201", primeira.StatusCode)
	}
	chave1 := servico.ultimaAbertura.IdempotencyKey

	segunda := chamarHandler(t, servidor, http.MethodPost, "/wallets", corpo, nil)
	if segunda.StatusCode != http.StatusCreated {
		t.Fatalf("segunda abertura = %d, quer 201", segunda.StatusCode)
	}
	chave2 := servico.ultimaAbertura.IdempotencyKey

	if chave1 == "" || chave1 != chave2 {
		t.Errorf("chave derivada não é determinística: %q e %q", chave1, chave2)
	}
}

// A chave RECEBIDA é usada como veio: o servidor nunca substitui uma chave do
// cliente por uma calculada (README §9).
func TestAbrirCarteiraUsaChaveDoCliente(t *testing.T) {
	servidor, servico := ambienteHandler(t, identidadeInterna())
	servico.aberturaResultado = appwagering.OpenWalletResult{
		WalletID: "0192f291-27dd-7d3f-8071-5f8685deef37",
		PlayerID: "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
		Balance:  *ponteiroMoney(t, "10.00"),
		Version:  1,
	}

	resp := chamarHandler(t, servidor, http.MethodPost, "/wallets",
		`{"playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1","initialBalance":{"amount":"10.00","currency":"BRL"}}`,
		map[string]string{HeaderIdempotencyKey: "abertura-do-cliente"})

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, quer 201", resp.StatusCode)
	}
	if servico.ultimaAbertura.IdempotencyKey != "abertura-do-cliente" {
		t.Errorf("chave = %q, quer %q", servico.ultimaAbertura.IdempotencyKey, "abertura-do-cliente")
	}
}

// Replay da abertura sai com `idempotentReplay: true` e o MESMO status 201: o
// que a resposta diz é que nada foi criado desta vez.
func TestAbrirCarteiraReplay(t *testing.T) {
	servidor, servico := ambienteHandler(t, identidadeInterna())
	servico.aberturaResultado = appwagering.OpenWalletResult{
		WalletID: "0192f291-27dd-7d3f-8071-5f8685deef37",
		PlayerID: "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
		Balance:  *ponteiroMoney(t, "1000.00"),
		Version:  1,

		IdempotentReplay: true,
	}

	resp := chamarHandler(t, servidor, http.MethodPost, "/wallets",
		`{"playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1","initialBalance":{"amount":"1000.00","currency":"BRL"}}`, nil)

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, quer 201", resp.StatusCode)
	}

	var corpo struct {
		ID               string `json:"id"`
		Version          int64  `json:"version"`
		IdempotentReplay bool   `json:"idempotentReplay"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&corpo); err != nil {
		t.Fatalf("corpo não é JSON: %v", err)
	}
	if !corpo.IdempotentReplay {
		t.Error("idempotentReplay = false no replay da abertura")
	}
	if corpo.Version != 1 {
		t.Errorf("version = %d, quer 1", corpo.Version)
	}
}

// Carteira duplicada ⇒ 409.
func TestAbrirCarteiraDuplicadaEh409(t *testing.T) {
	servidor, servico := ambienteHandler(t, identidadeInterna())
	servico.aberturaErro = appwagering.ErrWalletAlreadyExists

	resp := chamarHandler(t, servidor, http.MethodPost, "/wallets",
		`{"playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1","initialBalance":{"amount":"10.00","currency":"BRL"}}`, nil)

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, quer 409", resp.StatusCode)
	}
}

// --- ID do recurso ----------------------------------------------------------

// Identificador malformado na rota NÃO pode virar 500: o caso de uso devolve
// ErrWalletNotFound, e a borda traduz em 404.
func TestLerCarteiraComIDMalformadoEh404(t *testing.T) {
	servidor, _ := ambienteHandler(t, identidadeInterna())

	resp := chamarHandler(t, servidor, http.MethodGet, "/wallets/nao-e-uuid", "", nil)

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, quer 404", resp.StatusCode)
	}
}

// O `limit` não numérico é erro de ENTRADA (400), e o caso de uso nem é chamado.
func TestListarLedgerComLimitInvalidoEh400(t *testing.T) {
	servidor, _ := ambienteHandler(t, identidadeInterna())

	resp := chamarHandler(t, servidor, http.MethodGet,
		"/wallets/0192f291-27dd-7d3f-8071-5f8685deef37/ledger?limit=abc", "", nil)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, quer 400", resp.StatusCode)
	}
}

// --- Leitura por identidade externa ----------------------------------------

// O `providerId` do PATH diferente do token ⇒ 403, sem consultar o serviço.
func TestLerPorIdentidadeExternaDeOutroProvedorEh403(t *testing.T) {
	servidor, servico := ambienteHandler(t, identidadeProvedorA())

	resp := chamarHandler(t, servidor, http.MethodGet,
		"/providers/provider-b/wagering/transactions/ext-1", "", nil)

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, quer 403", resp.StatusCode)
	}
	if servico.pedidos != 0 {
		t.Error("o caso de uso foi consultado para outro provedor")
	}
}

// Operação inexistente (ou de outro provedor) ⇒ 404, sem revelar nada.
func TestLerPorIdentidadeExternaInexistenteEh404(t *testing.T) {
	servidor, _ := ambienteHandler(t, identidadeProvedorA())

	resp := chamarHandler(t, servidor, http.MethodGet,
		"/providers/provider-a/wagering/transactions/ext-inexistente", "", nil)

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, quer 404", resp.StatusCode)
	}
	if bruto := corpoDeResposta(t, resp); strings.Contains(bruto, "provider-b") {
		t.Errorf("a resposta de 404 vazou outro provedor: %s", bruto)
	}
}

// Sem identidade no contexto (rota montada sem autenticação), a leitura por
// identidade externa falha FECHADO: 401, nunca 200.
func TestLerPorIdentidadeExternaSemIdentidadeEh401(t *testing.T) {
	servico := &fakeServico{}
	cfg := &config.Config{Env: "test", HTTP: config.HTTPConfig{RequestTimeout: 5 * time.Second, MaxBodyBytes: 1 << 20}}
	logger := observability.DiscardLogger()
	rotas := NewRouter(cfg, logger, observability.NewMetrics(), nil)

	// Autenticador que NÃO injeta identidade (simula composição errada).
	handlers := NewHandlers(HandlersDeps{
		Service: servico, Routes: rotas, Authenticator: &fakeAuth{}, Logger: logger,
		Metrics: observability.NewMetrics(),
	})
	handlers.RegistrarRotas()

	servidor := httptest.NewServer(rotas.Base)
	defer servidor.Close()

	resp := chamarHandler(t, servidor, http.MethodGet,
		"/providers/provider-a/wagering/transactions/ext-1", "", nil)

	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, quer 401 ou 403 (falhar fechado)", resp.StatusCode)
	}
	if servico.pedidos != 0 {
		t.Error("o caso de uso foi consultado sem identidade")
	}
}

// --- Reconciliação ----------------------------------------------------------

// Divergência vira métrica e continua respondendo 200 com `consistent: false`:
// divergência é RESULTADO da conferência, não erro de transporte.
func TestReconciliacaoDivergenteResponde200(t *testing.T) {
	servico := &fakeServico{}
	servicoReconciliacao := &fakeServicoReconciliacao{fakeServico: servico}
	_ = servicoReconciliacao

	cfg := &config.Config{Env: "test", HTTP: config.HTTPConfig{RequestTimeout: 5 * time.Second, MaxBodyBytes: 1 << 20}}
	logger := observability.DiscardLogger()
	metricas := observability.NewMetrics()
	rotas := NewRouter(cfg, logger, metricas, nil)

	handlers := NewHandlers(HandlersDeps{
		Service: &fakeServicoComReconciliacao{
			fakeServico: servico,
			rec: appwagering.Reconciliation{
				WalletID:          "0192f291-27dd-7d3f-8071-5f8685deef37",
				StoredBalance:     *ponteiroMoney(t, "975.00"),
				CalculatedBalance: *ponteiroMoney(t, "980.00"),
				// `difference = stored − calculated` pode ser NEGATIVA, e o
				// negativo vem da aritmética — o parsing do domínio recusa sinal
				// na entrada, de propósito ("o sinal é definido pela operação").
				Difference:     negar(t, "5.00"),
				Consistent:     false,
				CheckedEntries: 2,
			},
		},
		Routes: rotas, Authenticator: &fakeAuth{identidade: identidadeInterna()},
		Logger: logger, Metrics: metricas,
	})
	handlers.RegistrarRotas()

	servidor := httptest.NewServer(rotas.Base)
	defer servidor.Close()

	resp := chamarHandler(t, servidor, http.MethodPost,
		"/wallets/0192f291-27dd-7d3f-8071-5f8685deef37/reconciliation", "", nil)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quer 200", resp.StatusCode)
	}

	var corpo struct {
		Consistent     bool                    `json:"consistent"`
		Difference     struct{ Amount string } `json:"difference"`
		CheckedEntries int                     `json:"checkedEntries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&corpo); err != nil {
		t.Fatalf("corpo não é JSON: %v", err)
	}
	if corpo.Consistent || corpo.Difference.Amount != "-5.00" || corpo.CheckedEntries != 2 {
		t.Errorf("resposta = %+v, quer consistent=false, difference=-5.00, checkedEntries=2", corpo)
	}
}

// fakeServicoComReconciliacao devolve um resultado de reconciliação fixo.
type fakeServicoComReconciliacao struct {
	*fakeServico
	rec appwagering.Reconciliation
}

func (f *fakeServicoComReconciliacao) Reconcile(context.Context, ids.WalletID) (appwagering.Reconciliation, error) {
	return f.rec, nil
}

// fakeServicoReconciliacao existe só para manter a referência usada acima.
type fakeServicoReconciliacao struct{ *fakeServico }

// --- utilidades -------------------------------------------------------------

func ponteiroMoney(t *testing.T, valor string) *money.Money {
	t.Helper()
	m := negarOuNao(t, valor)
	return &m
}

// negar devolve o valor negado (usado para `difference` negativa), provando de
// passagem que o domínio produz negativo pela aritmética.
func negar(t *testing.T, valor string) money.Money {
	t.Helper()

	m, err := money.Parse(valor, "BRL")
	if err != nil {
		t.Fatalf("Parse(%q): %v", valor, err)
	}
	negado, err := m.Neg()
	if err != nil {
		t.Fatalf("Neg(%q): %v", valor, err)
	}
	return negado
}

// negarOuNao é só para deixar `ponteiroMoney` legível.
func negarOuNao(t *testing.T, valor string) money.Money {
	t.Helper()
	m, err := money.Parse(valor, "BRL")
	if err != nil {
		t.Fatalf("Parse(%q): %v", valor, err)
	}
	return m
}

func corpoDeResposta(t *testing.T, resp *http.Response) string {
	t.Helper()

	bruto, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("lendo a resposta: %v", err)
	}
	return string(bruto)
}

// errTransitorio devolve o sentinela de falha transitória do contrato.
//
// O sentinela real (não um erro inventado): é ele que o caso de uso devolve
// quando o retry de uma falha transitória se esgota, e é ele que a borda precisa
// classificar como 503.
func errTransitorio() error { return ports.ErrTransient }
