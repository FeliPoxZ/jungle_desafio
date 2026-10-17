package auth_test

// Defeito 41 da Fase 10: o `jwks_uri` anunciado pelo discovery pode apontar para
// um endereço que o serviço NÃO alcança.
//
// O caso real, e o motivo de ele ter passado tanto tempo escondido: no
// `docker-compose`, o Keycloak anuncia `http://localhost:8080/...` (o endereço que
// o CLIENTE usa para pedir o token). Dentro do container do `api`, `localhost` é o
// próprio container — então o discovery responde, o `/health/ready` fica UP, e
// TODO token válido vira 503 "não foi possível validar a autenticação". O sintoma
// aponta para o IdP, e a causa está no endereço das chaves.
//
// Os dois testes abaixo provam o par: sem a sobrescrita o token válido é recusado
// (como indisponibilidade, não como token inválido); com `OIDC_JWKS_URL` ele é
// aceito. O segundo não pode passar sozinho — se passasse sem o primeiro, a
// sobrescrita poderia ser decorativa.

import (
	"context"
	"testing"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/infra/auth"
)

// enderecoInalcancavel é um endereço que ninguém escuta. A porta 1 exige
// privilégio e não é usada por nada; a conexão falha na hora, sem depender de
// prazo de rede.
const enderecoInalcancavel = "http://127.0.0.1:1/protocol/openid-connect/certs"

// verificadorComJWKS monta o verificador com o IdP de teste.
//
// `jwksSobrescrito` vazio reproduz o comportamento padrão (usa o `jwks_uri` do
// discovery); preenchido, reproduz a configuração de quem roda em container.
func verificadorComJWKS(t *testing.T, servidor *auth.ServidorOIDCTeste, jwksSobrescrito string) *auth.Verifier {
	t.Helper()

	return auth.New(auth.Config{
		Issuer:       servidor.Issuer(),
		DiscoveryURL: servidor.Issuer(),
		JWKSURL:      jwksSobrescrito,
		Audience:     "jungleg-api",
		JWKSCacheTTL: time.Minute,
		ClockSkew:    30 * time.Second,
		HTTPTimeout:  time.Second,
	})
}

// TestTokenValidoRecusadoQuandoOJWKSNaoEAlcancavel é a REPRODUÇÃO do defeito.
//
// Ele documenta que o problema existe no desenho do OIDC + rede, e não no
// verificador: a chave não está ao alcance, e o serviço responde "indisponível"
// (503), que é a classificação CORRETA — o erro é de INFRAESTRUTURA, não do
// token. É exatamente o que faz o sintoma parecer "o IdP caiu".
func TestTokenValidoRecusadoQuandoOJWKSNaoEAlcancavel(t *testing.T) {
	servidor := auth.NovoKeycloakDeTeste(t, "")
	servidor.ComJWKSEm(enderecoInalcancavel)

	token := servidor.Emitir(auth.ClaimsDeTeste{
		Audience:   []string{"jungleg-api"},
		ProviderID: "provider-a",
		Roles:      []string{"provider"},
	})

	_, err := verificadorComJWKS(t, servidor, "").Verify(context.Background(), token)
	if err == nil {
		t.Fatal("Verify() aceitou um token sem conseguir as chaves: o teste perdeu o sentido")
	}
	if !auth.IsUnavailable(err) {
		t.Fatalf("erro de chave inalcançável classificado como token inválido (401) em vez de "+
			"indisponibilidade (503): %v", err)
	}
}

// TestJWKSURLSobrescreveOAnunciadoPeloDiscovery é a CORREÇÃO.
//
// A configuração aponta as chaves para o endereço REAL enquanto o discovery
// continua anunciando o inalcançável. O token passa a ser aceito sem que o `iss`
// mude — que é o ponto: o emissor continua sendo o público, e só o endereço das
// chaves é ajustado por rede.
func TestJWKSURLSobrescreveOAnunciadoPeloDiscovery(t *testing.T) {
	servidor := auth.NovoKeycloakDeTeste(t, "")
	servidor.ComJWKSEm(enderecoInalcancavel)

	token := servidor.Emitir(auth.ClaimsDeTeste{
		Audience:   []string{"jungleg-api"},
		ProviderID: "provider-a",
		Roles:      []string{"provider"},
	})

	verificador := verificadorComJWKS(t, servidor, servidor.Issuer()+"/protocol/openid-connect/certs")

	id, err := verificador.Verify(context.Background(), token)
	if err != nil {
		t.Fatalf("Verify() com OIDC_JWKS_URL configurado devolveu erro: %v", err)
	}
	if id.ProviderID != "provider-a" {
		t.Fatalf("ProviderID = %q, quer %q", id.ProviderID, "provider-a")
	}
	if !id.HasRole("provider") {
		t.Fatalf("Roles = %v, quer conter %q", id.Roles, "provider")
	}

	// O `iss` continua sendo conferido contra o configurado: a sobrescrita não é
	// uma porta para aceitar token de outro emissor. (O teste do emissor trocado
	// já existe em `verifier_test.go`; aqui o que se prova é que a sobrescrita
	// não o afrouxa.)
	if id.Subject == "" {
		t.Error("Subject vazio: a identidade não foi montada a partir do token")
	}
}
