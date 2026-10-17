package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// ServidorOIDCTeste é um IdP OIDC de verdade para os testes: ele publica o
// discovery, publica o JWKS e ASSINA tokens com uma chave RSA real.
//
// # Por que um servidor que assina, e não um token de mentira
//
// Um teste que devolve `{"sub":"x"}` sem assinatura prova apenas que o
// middleware chama o verificador. O que precisa ser provado é que a CRIPTOGRAFIA
// está certa: assinatura válida passa, assinatura de outra chave não passa,
// `alg: none` não passa. Isso só é possível com uma chave de verdade — gerada no
// teste, usada para assinar e publicada no JWKS, exatamente como o Keycloak faz.
//
// O teste contra o Keycloak REAL (tag `integration`) cobre o resto: descobrir se
// o realm está configurado como o repositório diz.
type ServidorOIDCTeste struct {
	t      *testing.T
	Server *httptest.Server

	mu sync.Mutex
	// chaves indexadas por kid, na ordem de publicação.
	chaves   map[string]*rsa.PrivateKey
	ordem    []string
	kidAtual string
	// falhas injetadas para exercitar o caminho de indisponibilidade.
	discoveryStatus int
	jwksStatus      int
	discoveryCorpo  string
	// jwksURIAnunciado troca o `jwks_uri` que o discovery publica. Vazio significa
	// "o endereço deste servidor" (o normal). Existe para provar a sobrescrita
	// `OIDC_JWKS_URL`: num container, o Keycloak anuncia `localhost:8080`, que lá
	// dentro é o próprio container, e as chaves ficam inalcançáveis.
	jwksURIAnunciado string

	// contadores de acesso, para provar cache e rotação.
	discoveryBuscas int
	jwksBuscas      int
}

// NovoKeycloakDeTeste sobe o IdP de teste.
//
// A chave é gerada com 2048 bits porque o verificador RECUSA módulo menor que
// isso (RFC 7518 §3.3): gerar 1024 aqui faria o teste passar por acaso se a
// validação não existisse.
func NovoKeycloakDeTeste(t *testing.T, issuer string) *ServidorOIDCTeste {
	t.Helper()

	s := &ServidorOIDCTeste{
		t:               t,
		chaves:          map[string]*rsa.PrivateKey{},
		discoveryStatus: http.StatusOK,
		jwksStatus:      http.StatusOK,
	}
	s.adicionarChave("chave-1")

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", s.handlerDiscovery)
	mux.HandleFunc("/protocol/openid-connect/certs", s.handlerJWKS)

	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Server.Close)

	return s
}

// Issuer devolve o endereço que o servidor anuncia como emissor. É o valor que a
// configuração do verificador precisa receber.
func (s *ServidorOIDCTeste) Issuer() string { return s.Server.URL }

// adicionarChave gera e registra uma chave nova.
func (s *ServidorOIDCTeste) adicionarChave(kid string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	chave, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		s.t.Fatalf("gerando chave RSA de teste: %v", err)
	}
	s.chaves[kid] = chave
	s.ordem = append(s.ordem, kid)
	s.kidAtual = kid
}

// RotacionarChave publica uma chave NOVA e passa a assinar com ela, mantendo a
// anterior no JWKS.
//
// É o cenário real de rotação do Keycloak: por um tempo, os dois `kid` convivem,
// porque existem tokens assinados com a chave antiga ainda dentro do prazo.
func (s *ServidorOIDCTeste) RotacionarChave(kid string) {
	s.adicionarChave(kid)
}

// ApenasChaveNova troca o JWKS para publicar SOMENTE a chave informada.
//
// Serve para provar que um token assinado com uma chave que o IdP NÃO publica é
// recusado — mesmo que o `kid` esteja no cache.
func (s *ServidorOIDCTeste) ApenasChaveNova(kid string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	chave, existe := s.chaves[kid]
	if !existe {
		s.t.Fatalf("chave %q não existe", kid)
	}
	s.chaves = map[string]*rsa.PrivateKey{kid: chave}
	s.ordem = []string{kid}
	s.kidAtual = kid
}

// BuscasDeDiscovery conta quantas vezes o discovery foi servido.
func (s *ServidorOIDCTeste) BuscasDeDiscovery() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.discoveryBuscas
}

// BuscasDeJWKS conta quantas vezes o JWKS foi servido.
func (s *ServidorOIDCTeste) BuscasDeJWKS() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jwksBuscas
}

// FalharDiscovery faz o discovery responder erro.
func (s *ServidorOIDCTeste) FalharDiscovery(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discoveryStatus = status
}

// RestaurarDiscovery volta o discovery a responder 200.
func (s *ServidorOIDCTeste) RestaurarDiscovery() { s.FalharDiscovery(http.StatusOK) }

// FalharJWKS faz o JWKS responder erro.
func (s *ServidorOIDCTeste) FalharJWKS(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jwksStatus = status
}

// RestaurarJWKS volta o JWKS a responder 200.
func (s *ServidorOIDCTeste) RestaurarJWKS() { s.FalharJWKS(http.StatusOK) }

// ComIssuerDiferente publica um discovery cujo `issuer` NÃO é o configurado.
//
// Prova que o serviço não confia cegamente no que o discovery anuncia: um DNS
// trocado no meio do caminho não pode fazer o serviço aceitar tokens de outro
// emissor.
func (s *ServidorOIDCTeste) ComIssuerDiferente(issuer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.discoveryCorpo = issuer
}

// ComJWKSEm troca o `jwks_uri` anunciado pelo discovery.
//
// `url` inalcançável reproduz o que acontece com o Keycloak em container: o
// documento anuncia `http://localhost:8080/...`, que de dentro do container é o
// PRÓPRIO container. A prova de que a sobrescrita resolve isso está em
// `TestJWKSURLSobrescreveOAnunciadoPeloDiscovery`.
func (s *ServidorOIDCTeste) ComJWKSEm(url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jwksURIAnunciado = url
}

// --- handlers ----------------------------------------------------------------

func (s *ServidorOIDCTeste) handlerDiscovery(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.discoveryBuscas++
	status := s.discoveryStatus
	issuer := s.Server.URL
	if s.discoveryCorpo != "" {
		issuer = s.discoveryCorpo
	}
	jwksURI := s.Server.URL + "/protocol/openid-connect/certs"
	if s.jwksURIAnunciado != "" {
		jwksURI = s.jwksURIAnunciado
	}
	s.mu.Unlock()

	if status != http.StatusOK {
		w.WriteHeader(status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer":                                issuer,
		"jwks_uri":                              jwksURI,
		"authorization_endpoint":                issuer + "/protocol/openid-connect/auth",
		"token_endpoint":                        issuer + "/protocol/openid-connect/token",
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (s *ServidorOIDCTeste) handlerJWKS(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.jwksBuscas++
	status := s.jwksStatus
	chaves := make([]map[string]any, 0, len(s.ordem))
	for _, kid := range s.ordem {
		chave := s.chaves[kid]
		chaves = append(chaves, map[string]any{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": kid,
			"n":   base64.RawURLEncoding.EncodeToString(chave.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(chave.E)).Bytes()),
		})
	}
	s.mu.Unlock()

	if status != http.StatusOK {
		w.WriteHeader(status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": chaves})
}

// --- Emissão de token --------------------------------------------------------

// ClaimsDeTeste descreve o token a emitir. Campo com valor zero é OMITIDO do
// token, o que permite testar cada ausência (sem `exp`, sem `aud`, sem `sub`).
type ClaimsDeTeste struct {
	Issuer               string
	Subject              string
	Audience             []string
	Azp                  string
	ExpiresEm            time.Duration // em relação a agora; 0 = omitido
	NotBeforeEm          time.Duration // em relação a agora; 0 = omitido
	IssuedAtEm           time.Duration // em relação a agora; 0 = omitido
	ProviderID           string
	Roles                []string
	Kid                  string // vazio = chave atual
	Alg                  string // vazio = RS256
	OmitirExp            bool
	KidInexistente       bool
	RolesComoTexto       bool
	AssinarComOutraChave bool
}

// Emitir assina um token com os claims informados.
func (s *ServidorOIDCTeste) Emitir(claims ClaimsDeTeste) string {
	s.t.Helper()

	if claims.Subject == "" {
		claims.Subject = "sujeito-de-teste"
	}
	return s.emitir(claims)
}

// EmitirSemSubject assina um token SEM o claim `sub`.
//
// Existe separado porque `Emitir` preenche o sujeito por padrão (é o caso
// normal), e o teste da ausência precisa de um caminho explícito.
func (s *ServidorOIDCTeste) EmitirSemSubject(claims ClaimsDeTeste) string {
	s.t.Helper()
	return s.emitir(claims)
}

// emitir é o caminho comum de emissão.
func (s *ServidorOIDCTeste) emitir(claims ClaimsDeTeste) string {
	if claims.Issuer == "" {
		claims.Issuer = s.Server.URL
	}

	corpo := map[string]any{
		"iss": claims.Issuer,
		"jti": "jti-de-teste",
	}
	if claims.Subject != "" {
		corpo["sub"] = claims.Subject
	}
	if len(claims.Audience) == 1 {
		corpo["aud"] = claims.Audience[0]
	} else if len(claims.Audience) > 1 {
		corpo["aud"] = claims.Audience
	}
	if claims.Azp != "" {
		corpo["azp"] = claims.Azp
	}

	agora := time.Now()
	if !claims.OmitirExp {
		validade := claims.ExpiresEm
		if validade == 0 {
			validade = time.Hour
		}
		corpo["exp"] = agora.Add(validade).Unix()
	}
	if claims.NotBeforeEm != 0 {
		corpo["nbf"] = agora.Add(claims.NotBeforeEm).Unix()
	}
	if claims.IssuedAtEm != 0 {
		corpo["iat"] = agora.Add(claims.IssuedAtEm).Unix()
	}
	if claims.ProviderID != "" {
		corpo["provider_id"] = claims.ProviderID
	}
	if len(claims.Roles) > 0 {
		if claims.RolesComoTexto {
			corpo["roles"] = claims.Roles[0]
		} else {
			corpo["roles"] = claims.Roles
		}
	}

	s.mu.Lock()
	kid := claims.Kid
	chaveDoKid := s.chaves[kid]
	chaveAtual := s.chaves[s.kidAtual]
	kidAtual := s.kidAtual
	s.mu.Unlock()

	chave := chaveDoKid
	if claims.AssinarComOutraChave {
		// Assina com a chave ATUAL, mantendo o `kid` declarado: é o cenário de
		// assinatura forjada, e o verificador precisa recusar.
		chave = chaveAtual
	}
	if claims.KidInexistente {
		// O `kid` do header é falso, mas a assinatura é REAL (com a chave
		// atual). Assim o teste prova a recusa por `kid` desconhecido, e não a
		// recusa por assinatura vazia — que passaria por outro caminho.
		kid = "kid-que-nao-existe"
		chave = chaveAtual
	}
	if chave == nil {
		chave = chaveAtual
	}
	if kid == "" {
		kid = kidAtual
	}

	alg := claims.Alg
	if alg == "" {
		alg = "RS256"
	}

	return assinarToken(s.t, kid, alg, corpo, chave, s.tokenComAlgNenhum)
}

// CodificarParaTeste serializa um objeto em base64url sem padding. Exposto para
// os testes montarem cabeçalhos artesanais (sem `kid`, `typ` estranho).
func CodificarParaTeste(v any) string { return codificar(v) }

// Base64ParaTeste codifica um texto qualquer em base64url sem padding.
func Base64ParaTeste(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

// tokenComAlgNenhum monta um token com `alg: none` e SEM assinatura — o ataque
// mais clássico contra validação de JWT.
func (s *ServidorOIDCTeste) tokenComAlgNenhum(corpo map[string]any) string {
	cabecalho := map[string]any{"alg": "none", "typ": "JWT"}
	// O `kid` continua presente: o ataque não depende da ausência dele.
	s.mu.Lock()
	cabecalho["kid"] = s.kidAtual
	s.mu.Unlock()

	return codificar(cabecalho) + "." + codificar(corpo) + "."
}

// assinarToken monta e assina o JWT.
//
// Uma função livre (e não método) porque parte dos testes precisa montar tokens
// com cabeçalho ARTESANAL — `alg` desconhecido, `typ` estranho —, o que um
// construtor "correto" não permitiria.
func assinarToken(
	t *testing.T,
	kid, alg string,
	corpo map[string]any,
	chave *rsa.PrivateKey,
	algNenhum func(map[string]any) string,
) string {
	t.Helper()

	if alg == "none" && algNenhum != nil {
		return algNenhum(corpo)
	}

	cabecalho := map[string]any{"alg": alg, "typ": "JWT", "kid": kid}
	assinado := codificar(cabecalho) + "." + codificar(corpo)

	assinatura, err := assinarRS256(chave, assinado)
	if err != nil {
		t.Fatalf("assinando token de teste: %v", err)
	}
	return assinado + "." + assinatura
}

// codificar serializa um objeto em base64url sem padding.
func codificar(v any) string {
	bruto, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(bruto)
}

// assinarRS256 assina com RSASSA-PKCS1-v1_5 + SHA-256 — o mesmo algoritmo que
// `verificarAssinatura` confere, do outro lado.
func assinarRS256(chave *rsa.PrivateKey, conteudo string) (string, error) {
	digest := sha256.Sum256([]byte(conteudo))
	assinatura, err := rsa.SignPKCS1v15(rand.Reader, chave, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(assinatura), nil
}
