package auth

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// limiteCorpoIdP evita que uma resposta enorme do IdP consuma memória. O
// documento de discovery e um JWKS de Keycloak cabem folgadamente nisto.
const limiteCorpoIdP = 1 << 20 // 1 MiB

// discoveryDocument é o subconjunto de /.well-known/openid-configuration que
// usamos. Os campos desconhecidos são ignorados de propósito: o documento tem
// dezenas de chaves e nenhuma delas muda a validação.
type discoveryDocument struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}

// jwksDocument é o documento de chaves públicas.
type jwksDocument struct {
	Keys []jwkKey `json:"keys"`
}

// jwkKey é uma chave pública RSA em JWK (RFC 7517/7518).
type jwkKey struct {
	Kty string   `json:"kty"`
	Kid string   `json:"kid"`
	Use string   `json:"use"`
	Alg string   `json:"alg"`
	N   string   `json:"n"`
	E   string   `json:"e"`
	X5c []string `json:"x5c"`
}

// Verifier valida tokens OIDC do IdP configurado.
//
// # O que ele faz, e nesta ordem
//
//  1. descobre o emissor (/.well-known/openid-configuration) e guarda a URL do
//     JWKS — uma vez, com cache;
//  2. busca as chaves públicas (JWKS) e converte cada uma para *rsa.PublicKey;
//  3. confere o token: três partes, `alg` RS256, `kid` conhecido, assinatura,
//     `iss`, `aud`, `exp`, `nbf`;
//  4. traduz os claims para `Identity`.
//
// # Rotação de chave
//
// Duas defesas, porque uma só não basta:
//
//   - **TTL**: passado o prazo (`JWKSCacheTTL`), a próxima validação rebusca o
//     JWKS. É o que faz uma chave NOVA ser aceita sem reiniciar o serviço;
//   - **`kid` desconhecido**: mesmo dentro do TTL, um token assinado por uma
//     chave que não está no cache dispara UMA rebusca antes de recusar. Sem
//     isso, o intervalo entre a rotação no IdP e o fim do TTL seria uma janela
//     de 401 em massa para tokens perfeitamente válidos.
//
// # Concorrência
//
// O cache é protegido por `sync.RWMutex`. O I/O acontece FORA do lock: segurar o
// mutex durante uma chamada de rede transformaria uma lentidão do IdP em fila de
// todas as requisições do serviço. O custo é que duas requisições simultâneas
// podem buscar o JWKS ao mesmo tempo — uma busca redundante, não um erro.
type Verifier struct {
	cfg    Config
	client *http.Client
	agora  func() time.Time

	mu          sync.RWMutex
	discovery   discoveryDocument
	descobriuEm time.Time
	cache       map[string]*rsa.PublicKey
	chavesEm    time.Time
	jwksURI     string
}

// New monta o verificador.
//
// O cliente HTTP tem prazo e não segue redirecionamento: o endereço do JWKS vem
// do documento de discovery do IdP, e um redirecionamento para outro host seria
// uma forma de trocar a origem das chaves sem passar pela configuração.
func New(cfg Config) *Verifier {
	cfg = cfg.comDefaults()
	return &Verifier{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.HTTPTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		agora: time.Now,
	}
}

// Issuer devolve o emissor configurado (útil no log de boot).
func (v *Verifier) Issuer() string { return v.cfg.Issuer }

// Verify valida o token e devolve a identidade do chamador.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (Identity, error) {
	token, err := analisarToken(rawToken)
	if err != nil {
		return Identity{}, err
	}

	chave, err := v.chavePara(ctx, token.kid)
	if err != nil {
		return Identity{}, err
	}

	if err := verificarAssinatura(token, chave); err != nil {
		// Uma única rebusca: o `kid` está no cache, mas a chave pode ter sido
		// rotacionada e o cache estar servindo a versão antiga da MESMA chave...
		// ou o token é realmente inválido. A rebusca distingue os dois casos.
		chaveNova, e := v.rebuscarChave(ctx, token.kid)
		if e != nil || chaveNova == nil {
			return Identity{}, err
		}
		if err := verificarAssinatura(token, chaveNova); err != nil {
			return Identity{}, err
		}
	}

	if err := v.validarClaimsTemporais(token); err != nil {
		return Identity{}, err
	}
	if err := v.validarEmissorEPublico(token); err != nil {
		return Identity{}, err
	}

	return token.identidade()
}

// --- Discovery e JWKS --------------------------------------------------------

// documentoDeDiscovery devolve o discovery, buscando se ainda não tem ou se o
// cache venceu.
func (v *Verifier) documentoDeDiscovery(ctx context.Context) (discoveryDocument, error) {
	return v.obterDiscovery(ctx, false)
}

// obterDiscovery é o caminho único de obtenção do documento.
//
// `forcar` existe para o readiness: ele precisa saber se o IdP responde AGORA, e
// servir o cache responderia "está tudo bem" justamente quando o IdP caiu. É o
// único ponto do sistema que precisa furar o cache — a validação de token não
// precisa, porque lá o cache é uma defesa deliberada contra indisponibilidade
// momentânea do IdP.
func (v *Verifier) obterDiscovery(ctx context.Context, forcar bool) (discoveryDocument, error) {
	v.mu.RLock()
	fresco := v.jwksURI != "" && v.agora().Before(v.descobriuEm.Add(v.cfg.JWKSCacheTTL))
	docCache := v.discovery
	v.mu.RUnlock()

	if fresco && !forcar {
		return docCache, nil
	}

	url := strings.TrimRight(v.cfg.DiscoveryURL, "/") + "/.well-known/openid-configuration"

	var doc discoveryDocument
	if err := v.buscarJSON(ctx, url, &doc); err != nil {
		return discoveryDocument{}, err
	}
	if doc.JWKSURI == "" {
		return discoveryDocument{}, indisponivel(ErrDocumentoInvalido, "discovery sem jwks_uri em %s", url)
	}
	// O `issuer` do documento precisa bater com o configurado. Um discovery
	// apontando para outro emissor (DNS trocado, proxy no meio) seria uma forma
	// de fazer o serviço confiar em tokens de terceiro.
	if doc.Issuer != "" && !mesmoEmissor(doc.Issuer, v.cfg.Issuer) {
		return discoveryDocument{}, indisponivel(ErrDocumentoInvalido,
			"discovery aponta o emissor %q, esperado %q", doc.Issuer, v.cfg.Issuer)
	}

	v.mu.Lock()
	v.discovery = doc
	v.jwksURI = doc.JWKSURI
	v.descobriuEm = v.agora()
	v.mu.Unlock()

	return doc, nil
}

// chavePara devolve a chave pública do `kid`.
func (v *Verifier) chavePara(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.RLock()
	chave, tem := v.cache[kid]
	vencido := !v.agora().Before(v.chavesEm.Add(v.cfg.JWKSCacheTTL))
	v.mu.RUnlock()

	if tem && !vencido {
		return chave, nil
	}
	if tem && vencido {
		// Cache vencido: tenta atualizar, mas serve a chave antiga se o IdP
		// estiver fora. Preferir a chave conhecida a derrubar o serviço é a
		// escolha certa: a assinatura ainda será conferida com ela.
		if nova, err := v.atualizarChaves(ctx); err == nil {
			if k, ok := nova[kid]; ok {
				return k, nil
			}
		}
		return chave, nil
	}
	if !tem {
		nova, err := v.atualizarChaves(ctx)
		if err != nil {
			return nil, err
		}
		if k, ok := nova[kid]; ok {
			return k, nil
		}
	}
	return nil, invalido(ErrAssinaturaInvalida, "nenhuma chave publicada para o kid %q", kid)
}

// rebuscarChave força uma busca nova e devolve a chave do `kid`, se houver.
func (v *Verifier) rebuscarChave(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	nova, err := v.atualizarChaves(ctx)
	if err != nil {
		return nil, err
	}
	return nova[kid], nil
}

// atualizarChaves busca o JWKS e troca o cache.
func (v *Verifier) atualizarChaves(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	doc, err := v.documentoDeDiscovery(ctx)
	if err != nil {
		return nil, err
	}

	var jwks jwksDocument
	if err := v.buscarJSON(ctx, doc.JWKSURI, &jwks); err != nil {
		return nil, err
	}
	if len(jwks.Keys) == 0 {
		return nil, indisponivel(ErrDocumentoInvalido, "JWKS sem chaves em %s", doc.JWKSURI)
	}

	chaves := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	var ultimoErro error

	for _, k := range jwks.Keys {
		// Chave de outro uso (assinatura x cifra): ignorar é o correto, e não
		// recusar o documento inteiro.
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		if k.Kty != "" && k.Kty != "RSA" {
			continue
		}
		if k.Alg != "" && k.Alg != algoritmoAssinatura {
			continue
		}

		publica, err := k.chavePublica()
		if err != nil {
			// Uma chave malformada no documento não invalida as outras: o IdP
			// pode estar publicando uma chave EC ao lado das RSA.
			ultimoErro = err
			continue
		}
		chaves[k.Kid] = publica
	}

	if len(chaves) == 0 {
		if ultimoErro != nil {
			return nil, indisponivel(ErrDocumentoInvalido,
				"nenhuma chave RSA utilizável em %s: %v", doc.JWKSURI, ultimoErro)
		}
		return nil, indisponivel(ErrDocumentoInvalido, "nenhuma chave de assinatura RSA em %s", doc.JWKSURI)
	}

	v.mu.Lock()
	v.cache = chaves
	v.chavesEm = v.agora()
	v.mu.Unlock()

	return chaves, nil
}

// buscarJSON faz um GET e decodifica o JSON, traduzindo cada falha.
func (v *Verifier) buscarJSON(ctx context.Context, url string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return indisponivel(ErrDocumentoInvalido, "montando requisição para %s: %v", url, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := v.client.Do(req)
	if err != nil {
		return indisponivel(ErrIdpIndisponivel, "buscando %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return indisponivel(ErrDocumentoInvalido, "buscando %s: HTTP %d", url, resp.StatusCode)
	}

	corpo, err := io.ReadAll(io.LimitReader(resp.Body, limiteCorpoIdP))
	if err != nil {
		return indisponivel(ErrIdpIndisponivel, "lendo %s: %v", url, err)
	}
	if err := json.Unmarshal(corpo, dst); err != nil {
		return indisponivel(ErrDocumentoInvalido, "decodificando %s: %v", url, err)
	}
	return nil
}

// mesmoEmissor compara dois emissores ignorando barra final.
func mesmoEmissor(a, b string) bool {
	return strings.TrimRight(strings.TrimSpace(a), "/") == strings.TrimRight(strings.TrimSpace(b), "/")
}

// chavePublica converte o JWK para *rsa.PublicKey.
func (k jwkKey) chavePublica() (*rsa.PublicKey, error) {
	modulo, err := base64URLDecode(k.N)
	if err != nil {
		return nil, fmt.Errorf("JWK %q: módulo (n) inválido: %w", k.Kid, err)
	}
	expoente, err := base64URLDecode(k.E)
	if err != nil {
		return nil, fmt.Errorf("JWK %q: expoente (e) inválido: %w", k.Kid, err)
	}
	if len(expoente) == 0 || len(expoente) > 4 {
		return nil, fmt.Errorf("JWK %q: expoente com %d bytes", k.Kid, len(expoente))
	}

	var e uint64
	for _, b := range expoente {
		e = e<<8 | uint64(b)
	}
	if e < 3 || e%2 == 0 {
		// Expoente par ou 1 não é chave RSA válida (e precisa ser ímpar e ≥ 3).
		return nil, fmt.Errorf("JWK %q: expoente inválido (%d)", k.Kid, e)
	}

	chave := &rsa.PublicKey{N: new(big.Int).SetBytes(modulo), E: int(e)}
	// RFC 7518 §3.3: RSA com módulo menor que 2048 bits não é aceitável para
	// assinatura. Aceitar 1024 bits "porque o IdP publicou" seria aceitar uma
	// chave que qualquer um quebra.
	if chave.N.BitLen() < 2048 {
		return nil, fmt.Errorf("JWK %q: módulo com %d bits (mínimo 2048)", k.Kid, chave.N.BitLen())
	}
	return chave, nil
}
