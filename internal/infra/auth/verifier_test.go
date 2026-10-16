package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/infra/auth"
)

// verificador monta o verificador apontado para o IdP de teste.
//
// A configuração é a MESMA que a produção usa: emissor, público, tolerância de
// relógio e TTL de cache. Testar com outra configuração provaria outra coisa.
func verificador(t *testing.T, servidor *auth.ServidorOIDCTeste, audience string) *auth.Verifier {
	t.Helper()

	return auth.New(auth.Config{
		Issuer:       servidor.Issuer(),
		DiscoveryURL: servidor.Issuer(),
		Audience:     audience,
		JWKSCacheTTL: time.Minute,
		ClockSkew:    30 * time.Second,
		HTTPTimeout:  3 * time.Second,
	})
}

// --- Caminho feliz -----------------------------------------------------------

// O token de um provedor produz a identidade com o providerId do TOKEN.
//
// Esta é a asserção central do README §2: "a identidade autenticada deve
// determinar o providerId autorizado".
func TestVerifyDeTokenDeProvedor(t *testing.T) {
	servidor := auth.NovoKeycloakDeTeste(t, "")
	v := verificador(t, servidor, "jungleg-api")

	token := servidor.Emitir(auth.ClaimsDeTeste{
		Audience:   []string{"jungleg-api"},
		ProviderID: "provider-a",
		Roles:      []string{"provider"},
	})

	id, err := v.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("Verify() devolveu erro inesperado: %v", err)
	}

	if id.ProviderID != "provider-a" {
		t.Errorf("ProviderID = %q, quer %q", id.ProviderID, "provider-a")
	}
	if !id.HasRole("provider") {
		t.Errorf("Roles = %v, quer conter %q", id.Roles, "provider")
	}
	if id.IsProvider() != true {
		t.Error("IsProvider() = false, quer true para token com provider_id")
	}
	if id.Subject == "" {
		t.Error("Subject vazio")
	}
}

// O token do serviço interno NÃO tem provider_id — e isso é o que o distingue
// para fins de autorização.
func TestVerifyDeTokenInternoSemProvedor(t *testing.T) {
	servidor := auth.NovoKeycloakDeTeste(t, "")
	v := verificador(t, servidor, "jungleg-api")

	token := servidor.Emitir(auth.ClaimsDeTeste{
		Audience: []string{"jungleg-api"},
		Roles:    []string{"internal"},
	})

	id, err := v.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("Verify() devolveu erro inesperado: %v", err)
	}
	if id.ProviderID != "" {
		t.Errorf("ProviderID = %q, quer vazio para token interno", id.ProviderID)
	}
	if id.IsProvider() {
		t.Error("IsProvider() = true para token sem provider_id")
	}
	if !id.HasRole("internal") {
		t.Errorf("Roles = %v, quer conter internal", id.Roles)
	}
}

// O claim `roles` como TEXTO (e não lista) é aceito: o mapper do Keycloak pode
// publicar assim, e recusar o token inteiro por isso derrubaria toda a
// autorização com 403 silencioso.
func TestVerifyAceitaRolesComoTexto(t *testing.T) {
	servidor := auth.NovoKeycloakDeTeste(t, "")
	v := verificador(t, servidor, "jungleg-api")

	token := servidor.Emitir(auth.ClaimsDeTeste{
		Audience:       []string{"jungleg-api"},
		Roles:          []string{"internal"},
		RolesComoTexto: true,
	})

	id, err := v.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("Verify() devolveu erro inesperado: %v", err)
	}
	if !id.HasRole("internal") {
		t.Errorf("Roles = %v, quer conter internal", id.Roles)
	}
}

// Sem audience configurada, a checagem de `aud` é desligada (útil em ambiente
// onde o realm não publica audience). O `iss` continua sendo conferido.
func TestVerifySemAudienceConfigurada(t *testing.T) {
	servidor := auth.NovoKeycloakDeTeste(t, "")
	v := verificador(t, servidor, "")

	token := servidor.Emitir(auth.ClaimsDeTeste{ProviderID: "provider-a"})

	if _, err := v.Verify(context.Background(), token); err != nil {
		t.Fatalf("Verify() sem audience configurada devolveu erro: %v", err)
	}
}

// --- Rejeições ---------------------------------------------------------------

// matrizDeRejeicoes cobre cada forma de token inválido com a causa exata.
//
// Cada caso afirma o SENTINELA (`errors.Is`), não a mensagem: é o sentinela que
// o middleware usa para decidir entre 401 e 503, e é ele que não pode mudar.
func TestVerifyRejeitaTokensInvalidos(t *testing.T) {
	servidor := auth.NovoKeycloakDeTeste(t, "")
	v := verificador(t, servidor, "jungleg-api")

	// Uma segunda chave, publicada e usada para forjar assinatura.
	servidor.RotacionarChave("chave-2")

	casos := []struct {
		nome  string
		token func() string
		causa error
		descr string
	}{
		{
			nome: "emissor diferente",
			token: func() string {
				return servidor.Emitir(auth.ClaimsDeTeste{Issuer: "http://outro-idp/realms/x", Audience: []string{"jungleg-api"}})
			},
			causa: auth.ErrEmissorInvalido,
			descr: "token de outro emissor não pode ser aceito",
		},
		{
			nome:  "audience diferente",
			token: func() string { return servidor.Emitir(auth.ClaimsDeTeste{Audience: []string{"outro-publico"}}) },
			causa: auth.ErrPublicoInvalido,
			descr: "token emitido para outro público não pode ser aceito",
		},
		{
			nome:  "sem audience",
			token: func() string { return servidor.Emitir(auth.ClaimsDeTeste{}) },
			causa: auth.ErrPublicoInvalido,
		},
		{
			nome: "expirado",
			token: func() string {
				return servidor.Emitir(auth.ClaimsDeTeste{Audience: []string{"jungleg-api"}, ExpiresEm: -time.Hour})
			},
			causa: auth.ErrTokenExpirado,
		},
		{
			nome: "expirado ha 1 segundo (fora da tolerancia)",
			token: func() string {
				return servidor.Emitir(auth.ClaimsDeTeste{Audience: []string{"jungleg-api"}, ExpiresEm: -31 * time.Second})
			},
			causa: auth.ErrTokenExpirado,
		},
		{
			nome: "nbf no futuro",
			token: func() string {
				return servidor.Emitir(auth.ClaimsDeTeste{Audience: []string{"jungleg-api"}, NotBeforeEm: time.Hour})
			},
			causa: auth.ErrTokenAindaNaoValido,
		},
		{
			nome: "sem exp",
			token: func() string {
				return servidor.Emitir(auth.ClaimsDeTeste{Audience: []string{"jungleg-api"}, OmitirExp: true})
			},
			causa: auth.ErrTokenExpirado,
		},
		{
			nome: "kid inexistente",
			token: func() string {
				return servidor.Emitir(auth.ClaimsDeTeste{Audience: []string{"jungleg-api"}, KidInexistente: true})
			},
			causa: auth.ErrAssinaturaInvalida,
		},
		{
			nome: "assinatura de outra chave",
			token: func() string {
				// Assina com a chave ATUAL, mas declara o kid da chave 1: a
				// assinatura não confere com a chave publicada para aquele kid.
				return servidor.Emitir(auth.ClaimsDeTeste{
					Audience:             []string{"jungleg-api"},
					Kid:                  "chave-1",
					AssinarComOutraChave: true,
				})
			},
			causa: auth.ErrAssinaturaInvalida,
		},
		{
			nome: "alg none",
			token: func() string {
				return servidor.Emitir(auth.ClaimsDeTeste{Audience: []string{"jungleg-api"}, Alg: "none"})
			},
			causa: auth.ErrAlgoritmoNaoPermitido,
		},
		{
			nome: "alg HS256",
			token: func() string {
				return servidor.Emitir(auth.ClaimsDeTeste{Audience: []string{"jungleg-api"}, Alg: "HS256"})
			},
			causa: auth.ErrAlgoritmoNaoPermitido,
		},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			token := caso.token()

			_, err := v.Verify(context.Background(), token)
			if err == nil {
				t.Fatalf("Verify() aceitou um token que deveria ser recusado%s", detalhe(caso.descr))
			}
			if !errors.Is(err, caso.causa) {
				t.Errorf("causa = %v, quer %v%s", err, caso.causa, detalhe(caso.descr))
			}
			if auth.IsUnavailable(err) {
				t.Error("IsUnavailable() = true para token inválido; deveria ser 401, não 503")
			}
		})
	}
}

// --- Estrutura do token ------------------------------------------------------

// Tokens que nem chegam a ser um JWT, ou que não têm a forma exigida.
func TestVerifyRejeitaTokenMalformado(t *testing.T) {
	servidor := auth.NovoKeycloakDeTeste(t, "")
	v := verificador(t, servidor, "jungleg-api")

	cabecalhoSemKid := auth.CodificarParaTeste(map[string]any{"alg": "RS256", "typ": "JWT"})
	corpoOk := auth.CodificarParaTeste(map[string]any{
		"iss": servidor.Issuer(), "sub": "s", "aud": "jungleg-api",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	casos := map[string]string{
		"vazio":                 "",
		"uma parte":             "abc",
		"duas partes":           "abc.def",
		"header não é base64":   "!!!.abc.def",
		"header não é JSON":     auth.Base64ParaTeste("nao-e-json") + "." + auth.Base64ParaTeste("{}") + ".abc",
		"payload não é JSON":    cabecalhoSemKid + "." + auth.Base64ParaTeste("nao-e-json") + ".abc",
		"sem kid no cabeçalho":  cabecalhoSemKid + "." + corpoOk + "." + auth.Base64ParaTeste("assinatura"),
		"assinatura não base64": cabecalhoSemKid + "." + corpoOk + ".!!!",
	}

	for nome, token := range casos {
		t.Run(nome, func(t *testing.T) {
			if _, err := v.Verify(context.Background(), token); err == nil {
				t.Fatal("Verify() aceitou um token malformado")
			}
			if _, err := v.Verify(context.Background(), token); err != nil && auth.IsUnavailable(err) {
				t.Errorf("token malformado não é indisponibilidade do IdP: %v", err)
			}
		})
	}
}

// Token sem `sub` é recusado: sem sujeito não há como logar quem chamou nem
// correlacionar a operação (README §12).
func TestVerifyRejeitaTokenSemSubject(t *testing.T) {
	servidor := auth.NovoKeycloakDeTeste(t, "")
	v := verificador(t, servidor, "jungleg-api")

	// `Emitir` preenche `sub` por padrão; aqui montamos o token sem ele.
	token := servidor.EmitirSemSubject(auth.ClaimsDeTeste{Audience: []string{"jungleg-api"}})

	_, err := v.Verify(context.Background(), token)
	if err == nil {
		t.Fatal("Verify() aceitou token sem sub")
	}
	if !errors.Is(err, auth.ErrIdentidadeIncompleta) {
		t.Errorf("causa = %v, quer ErrIdentidadeIncompleta", err)
	}
}

// --- Cache e rotação de chaves ----------------------------------------------

// O JWKS é buscado UMA vez e reaproveitado: sem cache, cada requisição faria uma
// chamada de rede ao IdP, e o IdP viraria o gargalo (e o ponto único de falha)
// de toda a API.
func TestJWKSECacheado(t *testing.T) {
	servidor := auth.NovoKeycloakDeTeste(t, "")
	v := verificador(t, servidor, "jungleg-api")

	token := servidor.Emitir(auth.ClaimsDeTeste{Audience: []string{"jungleg-api"}})

	for i := 0; i < 5; i++ {
		if _, err := v.Verify(context.Background(), token); err != nil {
			t.Fatalf("verificação %d falhou: %v", i, err)
		}
	}

	if buscas := servidor.BuscasDeJWKS(); buscas != 1 {
		t.Errorf("JWKS foi buscado %d vezes, quer 1 (cache não está funcionando)", buscas)
	}
	if buscas := servidor.BuscasDeDiscovery(); buscas != 1 {
		t.Errorf("discovery foi buscado %d vezes, quer 1", buscas)
	}
}

// Um token assinado com a chave NOVA é aceito sem reiniciar o serviço: o `kid`
// desconhecido dispara rebusca do JWKS. Sem isso, toda rotação de chave do IdP
// seria uma janela de 401 em massa até o TTL do cache vencer.
func TestRotacaoDeChaveAceitaTokenNovo(t *testing.T) {
	servidor := auth.NovoKeycloakDeTeste(t, "")
	v := verificador(t, servidor, "jungleg-api")

	// Aquece o cache com a chave 1.
	antigo := servidor.Emitir(auth.ClaimsDeTeste{Audience: []string{"jungleg-api"}})
	if _, err := v.Verify(context.Background(), antigo); err != nil {
		t.Fatalf("token da chave 1 recusado: %v", err)
	}
	// TTL longo: a rebusca tem de ser disparada pelo `kid`, e não pelo vencimento.
	v = auth.New(auth.Config{
		Issuer: servidor.Issuer(), DiscoveryURL: servidor.Issuer(),
		Audience: "jungleg-api", JWKSCacheTTL: time.Hour, ClockSkew: 30 * time.Second,
	})
	if _, err := v.Verify(context.Background(), antigo); err != nil {
		t.Fatalf("token da chave 1 recusado no segundo verificador: %v", err)
	}

	// O IdP rotaciona e passa a assinar com a chave 2.
	servidor.RotacionarChave("chave-2")
	novo := servidor.Emitir(auth.ClaimsDeTeste{Audience: []string{"jungleg-api"}})

	if _, err := v.Verify(context.Background(), novo); err != nil {
		t.Fatalf("token da chave NOVA recusado (rotação não foi acompanhada): %v", err)
	}
}

// Se o IdP deixa de publicar a chave com que o token foi assinado, o token é
// recusado — mesmo que o `kid` esteja no cache. É o que impede que uma chave
// revogada continue valendo até o cache vencer.
func TestChaveRemovidaDoJWKSTornaTokenInvalido(t *testing.T) {
	servidor := auth.NovoKeycloakDeTeste(t, "")
	v := verificador(t, servidor, "jungleg-api")

	// Assina com a chave 2, mas publica apenas a chave 1.
	servidor.RotacionarChave("chave-2")
	servidor.ApenasChaveNova("chave-1")

	token := servidor.Emitir(auth.ClaimsDeTeste{Audience: []string{"jungleg-api"}, Kid: "chave-2"})

	_, err := v.Verify(context.Background(), token)
	if err == nil {
		t.Fatal("Verify() aceitou token assinado com chave que o IdP não publica")
	}
	if !errors.Is(err, auth.ErrAssinaturaInvalida) {
		t.Errorf("causa = %v, quer ErrAssinaturaInvalida", err)
	}
}

// --- IdP indisponível (503, nunca 401) --------------------------------------

// Discovery fora do ar é INDISPONIBILIDADE: o token pode estar perfeito e o
// serviço é que não consegue conferi-lo. Se isso virasse 401, o cliente
// descartaria um token válido e o incidente seria diagnosticado errado.
func TestDiscoveryForaDoArEhIndisponivel(t *testing.T) {
	servidor := auth.NovoKeycloakDeTeste(t, "")
	v := verificador(t, servidor, "jungleg-api")

	servidor.FalharDiscovery(503)

	token := servidor.Emitir(auth.ClaimsDeTeste{Audience: []string{"jungleg-api"}})

	_, err := v.Verify(context.Background(), token)
	if err == nil {
		t.Fatal("Verify() não devolveu erro com o discovery fora do ar")
	}
	if !auth.IsUnavailable(err) {
		t.Errorf("IsUnavailable() = false; IdP fora do ar precisa ser 503, não 401 (%v)", err)
	}
}

// JWKS fora do ar também é indisponibilidade.
func TestJWKSForaDoArEhIndisponivel(t *testing.T) {
	servidor := auth.NovoKeycloakDeTeste(t, "")
	v := verificador(t, servidor, "jungleg-api")

	servidor.FalharJWKS(500)

	token := servidor.Emitir(auth.ClaimsDeTeste{Audience: []string{"jungleg-api"}})

	_, err := v.Verify(context.Background(), token)
	if err == nil {
		t.Fatal("Verify() não devolveu erro com o JWKS fora do ar")
	}
	if !auth.IsUnavailable(err) {
		t.Errorf("IsUnavailable() = false para JWKS fora do ar: %v", err)
	}
}

// Discovery que anuncia OUTRO emissor é recusado como indisponibilidade: o
// serviço não pode passar a confiar em tokens de terceiro porque a resposta do
// IdP mudou.
func TestDiscoveryComOutroEmissorEhRecusado(t *testing.T) {
	servidor := auth.NovoKeycloakDeTeste(t, "")
	v := verificador(t, servidor, "jungleg-api")

	servidor.ComIssuerDiferente("http://atacante/realms/jungleg")

	token := servidor.Emitir(auth.ClaimsDeTeste{Audience: []string{"jungleg-api"}})

	_, err := v.Verify(context.Background(), token)
	if err == nil {
		t.Fatal("Verify() aceitou discovery que anuncia outro emissor")
	}
	if !auth.IsUnavailable(err) {
		t.Errorf("IsUnavailable() = false para discovery divergente: %v", err)
	}
}

// O IdP fora do ar NÃO derruba um token que já estava em cache válido... exceto
// pelo discovery vencido. Aqui o que se afirma é o essencial: erro
// classificado, sem panic e sem aceitar token inválido.
func TestIdPForaDoArNaoAceitaTokenInvalido(t *testing.T) {
	servidor := auth.NovoKeycloakDeTeste(t, "")
	v := verificador(t, servidor, "jungleg-api")

	servidor.FalharDiscovery(500)
	servidor.FalharJWKS(500)

	forjado := servidor.Emitir(auth.ClaimsDeTeste{Audience: []string{"jungleg-api"}, Alg: "none"})

	if _, err := v.Verify(context.Background(), forjado); err == nil {
		t.Fatal("token com alg none foi aceito com o IdP fora do ar")
	}
}

// --- CheckReadiness ----------------------------------------------------------

// O readiness do IdP tem de refletir o estado real: é ele que tira a instância
// da rotação quando o Keycloak está inacessível.
func TestCheckReadinessDoIdP(t *testing.T) {
	servidor := auth.NovoKeycloakDeTeste(t, "")
	v := verificador(t, servidor, "jungleg-api")

	if err := v.CheckReadiness(context.Background()); err != nil {
		t.Fatalf("CheckReadiness() com IdP no ar devolveu erro: %v", err)
	}

	servidor.FalharDiscovery(503)

	if err := v.CheckReadiness(context.Background()); err == nil {
		t.Fatal("CheckReadiness() deveria falhar com o IdP fora do ar")
	}
}

// --- utilidades --------------------------------------------------------------

func detalhe(s string) string {
	if s == "" {
		return ""
	}
	return " (" + s + ")"
}
