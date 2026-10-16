// Package auth implementa a borda de autenticação: valida o token OIDC emitido
// pelo IdP externo (Keycloak) e devolve a identidade do chamador.
//
// # Por que este pacote existe separado de `httpapi`
//
// `httpapi` conhece HTTP — status, header, corpo. Este pacote conhece OIDC —
// descobrir o emissor, buscar as chaves, conferir assinatura. Se a validação
// morasse no middleware, ela ficaria misturada com `http.Error` e não haveria
// como testá-la sem subir um servidor.
//
// O acoplamento é por porta: `httpapi` declara a interface `Autenticador` e este
// pacote a implementa. A camada HTTP nunca importa nada de OIDC, e o teste do
// middleware pode usar um autenticador falso.
//
// # Por que a implementação usa só a biblioteca padrão
//
// Um token é: base64url + JSON + assinatura RSA. A biblioteca padrão tem as três
// coisas (`encoding/base64`, `encoding/json`, `crypto/rsa`). A alternativa seria
// uma dependência de terceiros que faz o mesmo — e que traria junto as regras
// dela sobre o que aceitar. Aqui as regras são nossas, explícitas e testáveis:
// `alg` fixo em RS256, `kid` obrigatório, `iss`/`aud`/`exp`/`nbf` conferidos.
//
// A regra de ouro que este pacote segue: **nunca confiar no header do token para
// decidir como validá-lo** (`alg: none`, `alg: HS256` com a chave pública como
// segredo e `jku` apontando para outra chave são os ataques clássicos). O
// algoritmo é decidido AQUI, não pelo que o token pede.
package auth

import (
	"errors"
	"fmt"
)

// ErroClassificado diz à borda HTTP o que aconteceu, porque as duas situações
// exigem respostas DIFERENTES do cliente:
//
//	401 — o token não serve (ausente, expirado, assinatura inválida, público
//	      errado). O cliente precisa obter outro token; repetir não resolve.
//	503 — o IdP não respondeu (discovery fora, JWKS fora, resposta ilegível).
//	      O token pode estar perfeito; o serviço é que não consegue conferir.
//
// Sem essa distinção, uma queda do Keycloak viraria "token inválido" (401), o
// cliente jogaria o token bom no lixo e o incidente seria diagnosticado errado.
type ErroClassificado struct {
	// Unavailable é true quando o problema é do IdP, e não do token.
	Unavailable bool
	Err         error
}

func (e *ErroClassificado) Error() string {
	if e.Unavailable {
		return fmt.Sprintf("auth: IdP indisponível: %v", e.Err)
	}
	return fmt.Sprintf("auth: token inválido: %v", e.Err)
}

func (e *ErroClassificado) Unwrap() error { return e.Err }

// Causas. São sentinelas para o teste e para o log classificarem sem comparar
// mensagem.
var (
	// ErrTokenAusente é a requisição sem header Authorization: Bearer.
	ErrTokenAusente = errors.New("token ausente")
	// ErrTokenMalformado é o Bearer que não tem três partes ou não é base64url.
	ErrTokenMalformado = errors.New("token malformado")
	// ErrAlgoritmoNaoPermitido é `alg` diferente de RS256 — inclusive `none` e
	// HS256, que são tentativas de burlar a validação.
	ErrAlgoritmoNaoPermitido = errors.New("algoritmo de assinatura não permitido")
	// ErrAssinaturaInvalida é a assinatura que não confere com nenhuma chave
	// publicada para aquele `kid`.
	ErrAssinaturaInvalida = errors.New("assinatura inválida")
	// ErrEmissorInvalido é o `iss` diferente do emissor configurado.
	ErrEmissorInvalido = errors.New("emissor inválido")
	// ErrPublicoInvalido é o `aud` sem a audience configurada.
	ErrPublicoInvalido = errors.New("público (aud) inválido")
	// ErrTokenExpirado é o `exp` no passado (fora da tolerância de relógio).
	ErrTokenExpirado = errors.New("token expirado")
	// ErrTokenAindaNaoValido é o `nbf` (ou `iat`) no futuro.
	ErrTokenAindaNaoValido = errors.New("token ainda não é válido")
	// ErrIdentidadeIncompleta é o token válido sem o `sub` que a aplicação
	// exige para logar e correlacionar.
	ErrIdentidadeIncompleta = errors.New("identidade incompleta no token")
	// ErrIdpIndisponivel é a falha de rede/HTTP ao falar com o IdP.
	ErrIdpIndisponivel = errors.New("IdP indisponível")
	// ErrDocumentoInvalido é a resposta do IdP que não é o documento esperado.
	ErrDocumentoInvalido = errors.New("documento do IdP inválido")
)

// invalido marca erro do token (401) preservando a causa para `errors.Is`.
//
// A causa vem como parâmetro (e não como verbo de formatação) porque o `%w` só
// é aceito UMA vez por `fmt.Errorf` e estas mensagens já precisam dele. Passar a
// causa explicitamente também impede que alguém troque `%w` por `%v` sem
// perceber que a classificação do erro depende disso.
func invalido(causa error, format string, args ...any) error {
	return &ErroClassificado{Err: fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), causa)}
}

// indisponivel marca falha do IdP (503) preservando a causa.
func indisponivel(causa error, format string, args ...any) error {
	return &ErroClassificado{
		Unavailable: true,
		Err:         fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), causa),
	}
}

// IsUnavailable informa se o erro é indisponibilidade do IdP (503) e não token
// ruim (401).
//
// Aceita o erro ENVELOPADO: o middleware recebe o erro depois de passar por
// `fmt.Errorf`, e `errors.As` atravessa a cadeia.
func IsUnavailable(err error) bool {
	var classificado *ErroClassificado
	if errors.As(err, &classificado) {
		return classificado.Unavailable
	}
	return false
}
