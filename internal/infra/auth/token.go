package auth

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// algoritmoAssinatura é o ÚNICO algoritmo aceito.
//
// # Por que fixo, e não "o que o token pedir"
//
// Confiar no campo `alg` do header para decidir como validar é a vulnerabilidade
// clássica de JWT. Três ataques concretos que ela permite:
//
//   - `alg: "none"` — o atacante remove a assinatura e o serviço "valida";
//   - `alg: "HS256"` — o atacante assina com HMAC usando a CHAVE PÚBLICA (que é
//     pública) como segredo; um serviço que aceita os dois algoritmos confere
//     como HMAC e aprova;
//   - `alg` trocado por outro RSA fraco, ou por um algoritmo que o serviço
//     implementa com menos cuidado.
//
// Aqui o algoritmo é uma constante do código. O header pode pedir o que quiser:
// se não for RS256, o token é recusado ANTES de qualquer criptografia.
const algoritmoAssinatura = "RS256"

// Claims são os campos que este serviço lê do token.
//
// Os números vêm de `json.Number` (o decoder usa `UseNumber`), e não de
// `float64`: converter um `exp` para float e comparar com o relógio é a mesma
// classe de erro que usar `float64` para dinheiro — perde precisão exatamente
// onde a decisão é feita.
type Claims struct {
	Issuer    string
	Subject   string
	Audience  []string
	Azp       string
	Expires   int64
	NotBefore int64
	IssuedAt  int64
	JTI       string

	// ProviderID é o `provider_id` do token. Vazio quando o token não é de
	// provedor (serviço interno, operador).
	ProviderID string

	// Roles são as roles do chamador.
	Roles []string

	// raw guarda os claims completos, para diagnóstico.
	raw map[string]any
}

// token é o resultado do parsing: header, claims e o material assinado.
type token struct {
	header     cabecalhoToken
	claims     Claims
	assinatura []byte
	assinado   []byte // "header.payload": o que a assinatura cobre
	kid        string
}

// cabecalhoToken é o header JOSE.
type cabecalhoToken struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// analisarToken valida a ESTRUTURA do token e devolve as partes assinadas.
//
// Nada de criptografia acontece aqui: esta função só garante que o token tem a
// forma de um JWT RS256 antes de alguém gastar CPU com RSA.
func analisarToken(raw string) (token, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return token{}, invalido(ErrTokenMalformado, "token vazio")
	}

	partes := strings.Split(raw, ".")
	if len(partes) != 3 {
		return token{}, invalido(ErrTokenMalformado, "JWT deve ter 3 partes, tem %d", len(partes))
	}

	cabecalhoBruto, err := base64URLDecode(partes[0])
	if err != nil {
		return token{}, invalido(ErrTokenMalformado, "header não é base64url: %v", err)
	}

	// `DisallowUnknownFields` NÃO é usado aqui: o header JOSE tem campos
	// opcionais (`cty`, `jku`, `x5t`...) e recusar um token legítimo por causa
	// de um campo extra seria pior do que ignorá-lo. O que importa é não LER
	// esses campos — `jku` e `x5u` jamais são seguidos.
	var cabecalho cabecalhoToken
	if err := json.Unmarshal(cabecalhoBruto, &cabecalho); err != nil {
		return token{}, invalido(ErrTokenMalformado, "header não é JSON: %v", err)
	}

	if cabecalho.Alg != algoritmoAssinatura {
		// Mensagem deliberadamente explícita: este é o token de ataque mais
		// comum, e o log precisa dizer exatamente o que chegou.
		return token{}, invalido(ErrAlgoritmoNaoPermitido, "alg = %q, permitido apenas %s", cabecalho.Alg, algoritmoAssinatura)
	}
	if cabecalho.Typ != "" && !strings.EqualFold(cabecalho.Typ, "JWT") {
		return token{}, invalido(ErrTokenMalformado, "typ = %q (esperado JWT)", cabecalho.Typ)
	}
	if strings.TrimSpace(cabecalho.Kid) == "" {
		// Sem `kid` não há como saber QUAL chave pública confere a assinatura.
		// Tentar todas seria aceitar qualquer chave do IdP para qualquer token.
		return token{}, invalido(ErrTokenMalformado, "header sem kid")
	}

	claimsBruto, err := base64URLDecode(partes[1])
	if err != nil {
		return token{}, invalido(ErrTokenMalformado, "payload não é base64url: %v", err)
	}

	dec := json.NewDecoder(strings.NewReader(string(claimsBruto)))
	// `UseNumber` evita float64 em `exp`/`nbf`/`iat`.
	dec.UseNumber()

	var cru map[string]any
	if err := dec.Decode(&cru); err != nil {
		return token{}, invalido(ErrTokenMalformado, "payload não é JSON: %v", err)
	}

	claims, err := claimsDe(cru)
	if err != nil {
		return token{}, err
	}

	assinatura, err := base64URLDecode(partes[2])
	if err != nil {
		return token{}, invalido(ErrTokenMalformado, "assinatura não é base64url: %v", err)
	}

	return token{
		header:     cabecalho,
		claims:     claims,
		assinatura: assinatura,
		assinado:   []byte(partes[0] + "." + partes[1]),
		kid:        cabecalho.Kid,
	}, nil
}

// claimsDe traduz o mapa de claims para o tipo tipado.
func claimsDe(cru map[string]any) (Claims, error) {
	c := Claims{
		Issuer:     stringClaim(cru, "iss"),
		Subject:    stringClaim(cru, "sub"),
		Azp:        stringClaim(cru, "azp"),
		JTI:        stringClaim(cru, "jti"),
		ProviderID: stringClaim(cru, "provider_id"),
		raw:        cru,
	}

	aud, err := audienceDe(cru["aud"])
	if err != nil {
		return Claims{}, err
	}
	c.Audience = aud

	if c.Expires, err = numeroDe(cru["exp"]); err != nil {
		return Claims{}, err
	}
	if c.NotBefore, err = numeroDe(cru["nbf"]); err != nil {
		return Claims{}, err
	}
	if c.IssuedAt, err = numeroDe(cru["iat"]); err != nil {
		return Claims{}, err
	}

	roles, err := rolesDe(cru["roles"])
	if err != nil {
		return Claims{}, err
	}
	c.Roles = roles

	return c, nil
}

// validarClaimsTemporais confere `exp` e `nbf` com a tolerância de relógio.
func (v *Verifier) validarClaimsTemporais(t token) error {
	agora := v.agora()

	if t.claims.Expires == 0 {
		return invalido(ErrTokenExpirado, "token sem exp")
	}
	// Sem tolerância, um token que vence AGORA é ambíguo (o `<=` do RFC 7519
	// diz que exp é "a partir de quando não é mais válido"). Com tolerância
	// zero, tratamos o instante exato como expirado: a decisão precisa ser
	// determinística, e negar na dúvida é o lado seguro.
	if !agora.Before(time.Unix(t.claims.Expires, 0).Add(v.cfg.ClockSkew)) {
		return invalido(ErrTokenExpirado, "token expirou em %s", time.Unix(t.claims.Expires, 0).UTC().Format(time.RFC3339))
	}

	if t.claims.NotBefore != 0 {
		// `nbf` é "válido A PARTIR DE", então o instante exato é válido: a
		// comparação é estrita.
		if agora.Add(v.cfg.ClockSkew).Before(time.Unix(t.claims.NotBefore, 0)) {
			return invalido(ErrTokenAindaNaoValido, "token válido a partir de %s", time.Unix(t.claims.NotBefore, 0).UTC().Format(time.RFC3339))
		}
	}
	return nil
}

// validarEmissorEPublico confere `iss` e `aud`.
func (v *Verifier) validarEmissorEPublico(t token) error {
	if !mesmoEmissor(t.claims.Issuer, v.cfg.Issuer) {
		return invalido(ErrEmissorInvalido, "iss = %q, esperado %q", t.claims.Issuer, v.cfg.Issuer)
	}
	if v.cfg.Audience == "" {
		return nil
	}

	presente := false
	for _, a := range t.claims.Audience {
		if a == v.cfg.Audience {
			presente = true
			break
		}
	}
	if !presente {
		return invalido(ErrPublicoInvalido, "aud = %v, esperado %q", t.claims.Audience, v.cfg.Audience)
	}

	// OIDC Core §3.1.3.7: com MÚLTIPLAS audiences, o `azp` é obrigatório e
	// precisa identificar a parte autorizada. Sem esta checagem, um token
	// emitido para outro cliente que também listasse a nossa audience passaria.
	if len(t.claims.Audience) > 1 {
		if t.claims.Azp == "" {
			return invalido(ErrPublicoInvalido, "token com múltiplas audiences sem azp")
		}
		if t.claims.Azp != v.cfg.Audience {
			return invalido(ErrPublicoInvalido, "azp = %q, esperado %q", t.claims.Azp, v.cfg.Audience)
		}
	}
	return nil
}

// identidade traduz os claims validados para `Identity`.
//
// É aqui que a regra do README §2 se materializa: o `providerId` sai do TOKEN.
func (t token) identidade() (Identity, error) {
	id := Identity{
		Subject:    strings.TrimSpace(t.claims.Subject),
		ProviderID: strings.TrimSpace(t.claims.ProviderID),
		Roles:      t.claims.Roles,
		raw:        t.claims.raw,
	}
	if id.Subject == "" {
		return Identity{}, invalido(ErrIdentidadeIncompleta, "token sem sub")
	}
	return id, nil
}

// --- Verificação da assinatura ----------------------------------------------

// verificarAssinatura confere a assinatura RSASSA-PKCS1-v1_5 com SHA-256.
//
// Só o `crypto/rsa` da biblioteca padrão é usado: `rsa.VerifyPKCS1v15` aplica a
// codificação EMSA-PKCS1-v1_5 e compara. Implementar essa codificação à mão
// (padding, DigestInfo, comparação em tempo constante) seria a receita para um
// erro sutil de segurança — e não há motivo para isso.
func verificarAssinatura(t token, chave *rsa.PublicKey) error {
	if chave == nil {
		return invalido(ErrAssinaturaInvalida, "chave pública ausente")
	}
	digest := sha256.Sum256(t.assinado)
	if err := rsa.VerifyPKCS1v15(chave, crypto.SHA256, digest[:], t.assinatura); err != nil {
		return invalido(ErrAssinaturaInvalida, "kid %q: %v", t.kid, err)
	}
	return nil
}

// --- Conversão de claims -----------------------------------------------------

// stringClaim lê um claim de texto. Claim de texto com outro tipo é ignorado
// (vira vazio) em vez de virar erro: `iss` ausente já é recusado na validação, e
// o token de outro emissor não deve derrubar o serviço com 500.
func stringClaim(cru map[string]any, nome string) string {
	if v, ok := cru[nome].(string); ok {
		return v
	}
	return ""
}

// audienceDe lê `aud`, que o RFC 7519 permite ser string OU lista de strings.
func audienceDe(v any) ([]string, error) {
	switch valor := v.(type) {
	case nil:
		return nil, nil
	case string:
		return []string{valor}, nil
	case []any:
		audiencias := make([]string, 0, len(valor))
		for _, item := range valor {
			s, ok := item.(string)
			if !ok {
				return nil, invalido(ErrTokenMalformado, "aud contém item que não é texto")
			}
			audiencias = append(audiencias, s)
		}
		return audiencias, nil
	default:
		return nil, invalido(ErrTokenMalformado, "aud com tipo inesperado (%T)", v)
	}
}

// numeroDe converte um claim numérico (Date) em inteiro.
//
// Com `UseNumber` o valor chega como `json.Number`, então `Int64()` é exato. O
// caso `float64` fica coberto por segurança, para o dia em que alguém decodificar
// sem `UseNumber` — e aí o erro aparece aqui, e não numa comparação de instante
// silenciosamente errada.
func numeroDe(v any) (int64, error) {
	switch valor := v.(type) {
	case nil:
		return 0, nil
	case json.Number:
		n, err := valor.Int64()
		if err != nil {
			return 0, invalido(ErrTokenMalformado, "claim numérico inválido (%q)", valor.String())
		}
		return n, nil
	case float64:
		return int64(valor), nil
	default:
		return 0, invalido(ErrTokenMalformado, "claim de data com tipo inesperado (%T)", v)
	}
}

// rolesDe lê o claim `roles`.
//
// O Keycloak deste projeto publica as roles num claim próprio (`roles`), por
// mapper de client role — não em `realm_access.roles`, que é o formato padrão.
// Aceitar os dois formatos (texto ou lista) é barato e evita que uma mudança de
// mapper no realm derrube toda a autorização com 403 silencioso.
func rolesDe(v any) ([]string, error) {
	switch valor := v.(type) {
	case nil:
		return nil, nil
	case string:
		if strings.TrimSpace(valor) == "" {
			return nil, nil
		}
		return []string{valor}, nil
	case []any:
		roles := make([]string, 0, len(valor))
		for _, item := range valor {
			s, ok := item.(string)
			if !ok {
				return nil, invalido(ErrTokenMalformado, "roles contém item que não é texto")
			}
			roles = append(roles, s)
		}
		return roles, nil
	default:
		return nil, invalido(ErrTokenMalformado, "roles com tipo inesperado (%T)", v)
	}
}

// base64URLDecode decodifica base64url SEM padding, que é o que o JWT usa
// (RFC 7515 §2). `RawURLEncoding` já faz exatamente isso.
func base64URLDecode(s string) ([]byte, error) {
	bruto, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("base64url: %w", err)
	}
	return bruto, nil
}
