package auth

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/FeliPoxZ/jungle_desafio/internal/observability"
)

// Verificador é o contrato que o middleware usa. Fica aqui (no consumidor) pelo
// mesmo motivo das outras portas do projeto: o teste troca por um dublê.
type Verificador interface {
	Verify(ctx context.Context, rawToken string) (Identity, error)
}

// RoleProvider é a role do provedor de jogos.
const RoleProvider = "provider"

// RoleInternal é a role do serviço interno.
const RoleInternal = "internal"

// Middleware autentica a requisição e coloca a identidade no contexto.
//
// # O que ele NÃO faz
//
// Não decide autorização. Autenticar ("quem é você") e autorizar ("o que você
// pode") são coisas diferentes, e misturá-las produz o erro clássico de
// responder 403 para um token ausente: sem identidade não há o que autorizar, e
// a resposta correta é 401.
//
// # Códigos
//
//   - token ausente, inválido, expirado, de outro emissor/público, alg errado,
//     assinatura que não confere ⇒ **401**;
//   - IdP indisponível (discovery ou JWKS fora do ar) ⇒ **503**, com
//     `Retry-After`. O token pode estar perfeito; o serviço é que não consegue
//     conferi-lo, e transformar isso em 401 faria o cliente descartar um token
//     válido.
//
// # Por que a resposta não diz QUAL foi o problema
//
// A mensagem pública é genérica ("token inválido") de propósito: dizer "a chave
// kid X não existe" ou "o exp passou 3 minutos" ajuda quem está testando ataques
// a ajustar o token. O detalhe vai para o LOG, com o correlationId.
func Middleware(v Verificador, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bruto, err := bearerToken(r)
			if err != nil {
				negar(w, r, logger, err)
				return
			}

			identidade, err := v.Verify(r.Context(), bruto)
			if err != nil {
				negar(w, r, logger, err)
				return
			}
			// O correlationId é repassado para o log de acesso identificar a
			// operação autenticada (README §12).
			logger.DebugContext(r.Context(), "requisicao_autenticada",
				slog.String("subject", identidade.Subject),
				slog.String("providerId", identidade.ProviderID),
				slog.Any("roles", identidade.Roles),
				slog.String("correlationId", observability.CorrelationIDFrom(r.Context())),
			)

			next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), identidade)))
		})
	}
}

// RequireRole exige que a identidade tenha UMA das roles informadas.
//
// Deve ser montado DEPOIS do Middleware: dependendo do contexto, ele devolve 401
// quando não há identidade — o que só acontece por erro de composição da rota,
// e falhar fechado é a única resposta aceitável em autorização.
func RequireRole(roles ...string) func(http.Handler) http.Handler {
	permitidas := make([]string, 0, len(roles))
	for _, r := range roles {
		if trimmed := strings.TrimSpace(r); trimmed != "" {
			permitidas = append(permitidas, trimmed)
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identidade, ok := IdentityFrom(r.Context())
			if !ok {
				escreverErro(w, http.StatusUnauthorized, "UNAUTHORIZED",
					"autenticação obrigatória")
				return
			}

			for _, role := range permitidas {
				if identidade.HasRole(role) {
					next.ServeHTTP(w, r)
					return
				}
			}

			// 403: a identidade é conhecida, mas não tem a role. A mensagem não
			// lista as roles exigidas — isso só ajudaria quem está sondando.
			escreverErro(w, http.StatusForbidden, "FORBIDDEN",
				"a identidade autenticada não tem permissão para esta operação")
		})
	}
}

// RequireProvider garante que a identidade é de um PROVEDOR, e devolve o
// providerId autenticado para quem precisar dele.
//
// Um token de provedor sem `provider_id` é recusado aqui (403): sem o claim não
// existe provedor autorizado, e tratar isso como "provedor vazio" faria toda
// operação cair em "não encontrado" — um sintoma longe da causa.
func RequireProvider() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identidade, ok := IdentityFrom(r.Context())
			if !ok {
				escreverErro(w, http.StatusUnauthorized, "UNAUTHORIZED",
					"autenticação obrigatória")
				return
			}
			if strings.TrimSpace(identidade.ProviderID) == "" {
				escreverErro(w, http.StatusForbidden, "FORBIDDEN",
					"o token não identifica um provedor autorizado")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// negar traduz o erro de validação na resposta do contrato.
func negar(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	if IsUnavailable(err) {
		// O erro completo vai para o log; o cliente recebe o mínimo.
		logger.ErrorContext(r.Context(), "idp_indisponivel",
			slog.String("error", err.Error()),
			slog.String("path", r.URL.Path),
		)
		w.Header().Set("Retry-After", "5")
		escreverErro(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE",
			"não foi possível validar a autenticação no momento")
		return
	}

	logger.WarnContext(r.Context(), "token_recusado",
		slog.String("error", err.Error()),
		slog.String("path", r.URL.Path),
	)
	escreverErro(w, http.StatusUnauthorized, "UNAUTHORIZED",
		"credenciais ausentes ou inválidas")
}

// escreverErro escreve o MESMO envelope de erro usado pelo resto da API.
//
// O middleware não importa `httpapi` (seria ciclo: `httpapi` importa este
// pacote). Por isso ele conhece o formato — que é contrato público e está
// travado por teste dos dois lados.
func escreverErro(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)

	corpo := struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{}
	corpo.Error.Code = code
	corpo.Error.Message = message

	_ = json.NewEncoder(w).Encode(corpo)
}
