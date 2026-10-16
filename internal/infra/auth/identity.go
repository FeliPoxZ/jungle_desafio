package auth

import (
	"context"
	"net/http"
	"strings"
)

// Identity é quem chamou, segundo o token JÁ VALIDADO.
//
// # A regra que este tipo materializa
//
// O `ProviderID` sai do TOKEN e de nenhum outro lugar. O README §2 é explícito:
// "a identidade autenticada deve determinar o `providerId` autorizado". Se ele
// viesse do corpo da requisição, qualquer token válido — de qualquer provedor —
// operaria a carteira de qualquer outro: o isolamento entre provedores, que é
// item eliminatório (§14), deixaria de existir.
//
// `ProviderID` vazio significa "não é um token de provedor" (é o serviço
// interno, ou um operador). Um token de provedor sem `provider_id` é RECUSADO na
// validação, e não tratado como interno.
type Identity struct {
	// Subject é o `sub` do token: identifica o client/usuário no IdP.
	Subject string
	// ProviderID é o `provider_id` do token. Vazio para o serviço interno.
	ProviderID string
	// Roles são as roles do token. No Keycloak deste projeto elas vêm no claim
	// `roles`, por mapper de client role (ver deploy/keycloak/realm.json).
	Roles []string

	// raw são os claims completos, para diagnóstico e para campos que a borda
	// venha a exigir sem mudar este tipo.
	raw map[string]any
}

// HasRole informa se a identidade tem a role informada.
func (i Identity) HasRole(role string) bool {
	for _, r := range i.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// IsProvider informa se a identidade é de um provedor de jogos.
func (i Identity) IsProvider() bool { return i.ProviderID != "" }

// Claims devolve uma cópia dos claims validados.
func (i Identity) Claims() map[string]any {
	copia := make(map[string]any, len(i.raw))
	for k, v := range i.raw {
		copia[k] = v
	}
	return copia
}

// identityContextKey é a chave do contexto. É um tipo próprio (não string) para
// nenhum outro pacote conseguir escrever ou ler a identidade por acidente.
type identityContextKey struct{}

// WithIdentity põe a identidade no contexto da requisição.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityContextKey{}, id)
}

// IdentityFrom extrai a identidade autenticada do contexto.
//
// O segundo retorno é false quando NÃO há identidade — o que só acontece em rota
// que não passou pelo middleware de autenticação. Quem chama deve tratar isso
// como falha de programação e responder 401, nunca assumir identidade anônima:
// um handler que respondesse dados a uma identidade vazia seria um furo de
// autorização silencioso.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityContextKey{}).(Identity)
	return id, ok
}

// bearerToken extrai o token do header Authorization.
func bearerToken(r *http.Request) (string, error) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		return "", invalido(ErrTokenAusente, "header Authorization ausente")
	}

	// O esquema é case-insensitive (RFC 7235).
	const prefixo = "bearer "
	if len(header) < len(prefixo) || !strings.EqualFold(header[:len(prefixo)], prefixo) {
		return "", invalido(ErrTokenAusente, "header Authorization não usa o esquema Bearer")
	}

	token := strings.TrimSpace(header[len(prefixo):])
	if token == "" {
		return "", invalido(ErrTokenAusente, "token vazio")
	}
	return token, nil
}
