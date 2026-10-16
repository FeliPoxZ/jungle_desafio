package auth

import "time"

// Config é o que a validação OIDC precisa saber do IdP.
//
// Note que `Issuer` e `DiscoveryURL` são separados de propósito: no Docker
// Compose o Java — o Keycloak — emite o `iss` com o endereço que o CLIENTE usa
// (`http://localhost:8080/realms/jungleg`), enquanto o serviço, de dentro da
// rede, busca o discovery em `http://host.docker.internal:8080/realms/jungleg`.
// Com um campo só, ou o `iss` não bate (todo token vira 401) ou o serviço não
// alcança o IdP.
type Config struct {
	// Issuer é o valor EXATO esperado no claim `iss`, sem barra no fim.
	Issuer string

	// DiscoveryURL é a base onde fica /.well-known/openid-configuration.
	// Vazio significa "igual ao Issuer".
	DiscoveryURL string

	// Audience é o `aud` obrigatório. Vazio desliga a checagem — usado só em
	// ambiente onde não há audience configurada; em produção é exigido.
	Audience string

	// JWKSCacheTTL é por quanto tempo as chaves públicas ficam em cache antes
	// de nova consulta. Curto demais castiga o IdP a cada requisição; longo
	// demais atrasa a rotação de chave. 15 minutos é o padrão.
	JWKSCacheTTL time.Duration

	// ClockSkew é a tolerância de relógio ao conferir `exp` e `nbf`. Sem ela,
	// uma diferença de segundos entre o relógio do IdP e o do serviço rejeita
	// tokens recém-emitidos — falha intermitente e difícil de diagnosticar.
	ClockSkew time.Duration

	// HTTPTimeout limita cada chamada ao IdP (discovery e JWKS).
	HTTPTimeout time.Duration
}

// comDefaults preenche o que não foi informado.
func (c Config) comDefaults() Config {
	if c.DiscoveryURL == "" {
		c.DiscoveryURL = c.Issuer
	}
	if c.JWKSCacheTTL <= 0 {
		c.JWKSCacheTTL = 15 * time.Minute
	}
	if c.ClockSkew < 0 {
		c.ClockSkew = 0
	}
	if c.HTTPTimeout <= 0 {
		c.HTTPTimeout = 5 * time.Second
	}
	return c
}
