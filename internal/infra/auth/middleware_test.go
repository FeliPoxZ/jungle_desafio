package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FeliPoxZ/jungle_desafio/internal/infra/auth"
	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
)

// --- Dublês ------------------------------------------------------------------

// verificadorFalso devolve o que o teste mandar, sem tocar em rede.
//
// O teste do MIDDLEWARE não precisa de criptografia: o que ele prova é a
// tradução de erro em status e a passagem da identidade pelo contexto. A
// criptografia é provada em `verifier_test.go`, com chave de verdade.
type verificadorFalso struct {
	identidade auth.Identity
	err        error
	chamadas   int
	tokens     []string
}

func (f *verificadorFalso) Verify(_ context.Context, token string) (auth.Identity, error) {
	f.chamadas++
	f.tokens = append(f.tokens, token)
	return f.identidade, f.err
}

// identidadeDeProvedor é a identidade típica de um provedor autenticado.
func identidadeDeProvedor(providerID string) auth.Identity {
	return auth.Identity{
		Subject:    "service-account-" + providerID,
		ProviderID: providerID,
		Roles:      []string{auth.RoleProvider},
	}
}

// identidadeInterna é a identidade do serviço interno.
func identidadeInterna() auth.Identity {
	return auth.Identity{Subject: "service-account-internal", Roles: []string{auth.RoleInternal}}
}

// servidorDeTeste monta um handler protegido pelos middlewares informados.
func servidorDeTeste(f *verificadorFalso, middlewares ...func(http.Handler) http.Handler) http.Handler {
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// O handler de prova: se chegou aqui, a identidade tem de estar no
		// contexto. Sem ela, a resposta é 500 — falha de composição.
		id, ok := auth.IdentityFrom(r.Context())
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"erro":"identidade ausente no contexto"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"subject":    id.Subject,
			"providerId": id.ProviderID,
			"roles":      id.Roles,
		})
	})

	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	// Autenticação por último na cadeia externa = primeiro a rodar.
	return handler
}

func requisicao(token string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/wagering/transactions", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

// --- 401: token ausente ou inválido -----------------------------------------

// Sem header Authorization ⇒ 401. Nunca 403: sem identidade não há o que
// autorizar, e o cliente precisa saber que o problema é a credencial.
func TestMiddlewareSemTokenResponde401(t *testing.T) {
	falso := &verificadorFalso{}
	handler := servidorDeTeste(falso, auth.Middleware(falso, observability.DiscardLogger()))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requisicao(""))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, quer 401", rec.Code)
	}
	if falso.chamadas != 0 {
		t.Error("o verificador foi chamado sem token; não deveria")
	}
	assertEnvelopeDeErro(t, rec.Body.Bytes(), "UNAUTHORIZED")
}

// Esquema diferente de Bearer ⇒ 401, sem chamar o verificador.
func TestMiddlewareEsquemaDiferenteResponde401(t *testing.T) {
	falso := &verificadorFalso{}
	handler := servidorDeTeste(falso, auth.Middleware(falso, observability.DiscardLogger()))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpzZW5oYQ==")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, quer 401", rec.Code)
	}
	if falso.chamadas != 0 {
		t.Error("o verificador foi chamado com esquema Basic")
	}
}

// Token que o verificador recusa ⇒ 401, sem tocar o handler.
func TestMiddlewareTokenInvalidoResponde401(t *testing.T) {
	falso := &verificadorFalso{err: errors.New("assinatura inválida")}
	handler := servidorDeTeste(falso, auth.Middleware(falso, observability.DiscardLogger()))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requisicao("token-qualquer"))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, quer 401", rec.Code)
	}
	assertEnvelopeDeErro(t, rec.Body.Bytes(), "UNAUTHORIZED")
}

// O token chega ao verificador SEM o prefixo "Bearer ".
func TestMiddlewareEntregaTokenSemPrefixo(t *testing.T) {
	falso := &verificadorFalso{identidade: identidadeInterna()}
	handler := servidorDeTeste(falso, auth.Middleware(falso, observability.DiscardLogger()))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requisicao("abc.def.ghi"))

	if len(falso.tokens) != 1 || falso.tokens[0] != "abc.def.ghi" {
		t.Errorf("tokens recebidos = %v, quer [abc.def.ghi]", falso.tokens)
	}
}

// --- 503: IdP indisponível ---------------------------------------------------

// IdP indisponível ⇒ 503 (e não 401): o token pode estar perfeito e o serviço é
// que não consegue conferi-lo.
func TestMiddlewareIdPIndisponivelResponde503(t *testing.T) {
	servidor := auth.NovoKeycloakDeTeste(t, "")
	v := verificador(t, servidor, "jungleg-api")
	servidor.FalharDiscovery(503)

	token := servidor.Emitir(auth.ClaimsDeTeste{Audience: []string{"jungleg-api"}, ProviderID: "provider-a"})
	handler := servidorDeTeste(nil, auth.Middleware(v, observability.DiscardLogger()))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requisicao(token))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, quer 503 (IdP fora do ar)", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got == "" {
		t.Error("503 sem Retry-After: o cliente não sabe quando repetir")
	}
	assertEnvelopeDeErro(t, rec.Body.Bytes(), "SERVICE_UNAVAILABLE")
}

// --- Contexto ----------------------------------------------------------------

// A identidade validada fica disponível no contexto, com o providerId do TOKEN.
func TestMiddlewareColocaIdentidadeNoContexto(t *testing.T) {
	falso := &verificadorFalso{identidade: identidadeDeProvedor("provider-a")}
	handler := servidorDeTeste(falso, auth.Middleware(falso, observability.DiscardLogger()))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requisicao("token"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quer 200 (body=%s)", rec.Code, rec.Body.String())
	}

	var corpo struct {
		Subject    string   `json:"subject"`
		ProviderID string   `json:"providerId"`
		Roles      []string `json:"roles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &corpo); err != nil {
		t.Fatalf("corpo não é JSON: %v", err)
	}
	if corpo.ProviderID != "provider-a" {
		t.Errorf("providerId no contexto = %q, quer provider-a", corpo.ProviderID)
	}
}

// Sem autenticação, o handler NÃO encontra identidade — e é isso que faz
// `RequireRole` responder 401 em vez de liberar.
func TestSemMiddlewareNaoHaIdentidade(t *testing.T) {
	handler := servidorDeTeste(nil)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requisicao("token"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, quer 500 (o handler de prova exige identidade)", rec.Code)
	}
}

// --- RequireRole -------------------------------------------------------------

// A role exigida presente ⇒ passa.
func TestRequireRolePermite(t *testing.T) {
	falso := &verificadorFalso{identidade: identidadeInterna()}
	handler := servidorDeTeste(falso,
		auth.Middleware(falso, observability.DiscardLogger()),
		auth.RequireRole(auth.RoleInternal),
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requisicao("token"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quer 200 (body=%s)", rec.Code, rec.Body.String())
	}
}

// Identidade autenticada SEM a role ⇒ 403: ela é conhecida, mas não tem
// permissão. O corpo não confirma quais roles existem.
func TestRequireRoleNegaComRoleErrada(t *testing.T) {
	falso := &verificadorFalso{identidade: identidadeDeProvedor("provider-a")}
	handler := servidorDeTeste(falso,
		auth.Middleware(falso, observability.DiscardLogger()),
		auth.RequireRole(auth.RoleInternal),
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requisicao("token"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, quer 403", rec.Code)
	}
	assertEnvelopeDeErro(t, rec.Body.Bytes(), "FORBIDDEN")
}

// Uma das roles listadas basta (requisito "internal OU operator").
func TestRequireRoleAceitaQualquerUmaDasRoles(t *testing.T) {
	falso := &verificadorFalso{identidade: auth.Identity{Subject: "s", Roles: []string{"operator"}}}
	handler := servidorDeTeste(falso,
		auth.Middleware(falso, observability.DiscardLogger()),
		auth.RequireRole(auth.RoleInternal, "operator"),
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requisicao("token"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quer 200", rec.Code)
	}
}

// `RequireRole` montado SEM autenticação antes: não há identidade no contexto, e
// a resposta é 401 (e não 200 nem 500). Falhar fechado é a única opção em
// autorização.
func TestRequireRoleSemAutenticacaoResponde401(t *testing.T) {
	handler := servidorDeTeste(nil, auth.RequireRole(auth.RoleInternal))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requisicao("token"))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, quer 401", rec.Code)
	}
}

// --- RequireProvider ---------------------------------------------------------

// Token de provedor (com provider_id) ⇒ passa.
func TestRequireProviderPermiteProvedor(t *testing.T) {
	falso := &verificadorFalso{identidade: identidadeDeProvedor("provider-b")}
	handler := servidorDeTeste(falso,
		auth.Middleware(falso, observability.DiscardLogger()),
		auth.RequireProvider(),
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requisicao("token"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quer 200", rec.Code)
	}
}

// Token do serviço interno NÃO é de provedor ⇒ 403. Sem `provider_id` não existe
// provedor autorizado; tratar como "provedor vazio" faria a operação cair em
// "não encontrado", longe da causa.
func TestRequireProviderNegaTokenInterno(t *testing.T) {
	falso := &verificadorFalso{identidade: identidadeInterna()}
	handler := servidorDeTeste(falso,
		auth.Middleware(falso, observability.DiscardLogger()),
		auth.RequireProvider(),
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requisicao("token"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, quer 403", rec.Code)
	}
}

// --- Autenticador ------------------------------------------------------------

// O `*auth.Autenticador` (o tipo que o Fx injeta nas rotas) expõe os três
// middlewares como MÉTODOS, e o encadeamento funciona igual ao das funções
// soltas. É esta forma que a interface `httpapi.Autenticador` exige.
func TestAutenticadorComposto(t *testing.T) {
	servidor := auth.NovoKeycloakDeTeste(t, "")
	v := verificador(t, servidor, "jungleg-api")
	a := auth.NewAutenticador(v, observability.DiscardLogger())

	token := servidor.Emitir(auth.ClaimsDeTeste{
		Audience:   []string{"jungleg-api"},
		ProviderID: "provider-a",
		Roles:      []string{auth.RoleProvider},
	})

	handler := servidorDeTeste(nil, a.Middleware, a.RequireProvider())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requisicao(token))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quer 200 (body=%s)", rec.Code, rec.Body.String())
	}
}

// O `Autenticador` também serve o readiness: ele é o `HealthChecker` do IdP.
func TestAutenticadorEhHealthChecker(t *testing.T) {
	servidor := auth.NovoKeycloakDeTeste(t, "")
	v := verificador(t, servidor, "jungleg-api")
	a := auth.NewAutenticador(v, observability.DiscardLogger())

	if a.Name() != "oidc" {
		t.Errorf("Name() = %q, quer oidc", a.Name())
	}
	if err := a.CheckReadiness(context.Background()); err != nil {
		t.Fatalf("CheckReadiness() com IdP no ar: %v", err)
	}

	servidor.FalharDiscovery(503)

	if err := a.CheckReadiness(context.Background()); err == nil {
		t.Fatal("CheckReadiness() deveria falhar com o IdP fora do ar")
	}
}

// --- utilidades --------------------------------------------------------------

func assertEnvelopeDeErro(t *testing.T, corpo []byte, codigoEsperado string) {
	t.Helper()

	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(corpo, &envelope); err != nil {
		t.Fatalf("corpo de erro não é JSON: %v (body=%s)", err, corpo)
	}
	if envelope.Error.Code != codigoEsperado {
		t.Errorf("code = %q, quer %q", envelope.Error.Code, codigoEsperado)
	}
	if envelope.Error.Message == "" {
		t.Error("mensagem pública vazia")
	}
	// O detalhe do erro NÃO pode ir para o cliente.
	if len(corpo) > 0 && (contains(string(corpo), "assinatura inválida") ||
		contains(string(corpo), "kid") || contains(string(corpo), "expirou")) {
		t.Errorf("a resposta vazou detalhe de validação: %s", corpo)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
