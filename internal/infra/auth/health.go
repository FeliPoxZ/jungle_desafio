package auth

import (
	"context"
	"time"
)

// Name identifica o IdP no relatório do readiness.
func (a *Autenticador) Name() string { return "oidc" }

// CheckReadiness confirma que o IdP está alcançável.
//
// # Por que o IdP entra no readiness (P5)
//
// Sem ele, uma instância que não alcança o Keycloak responderia `200 UP` no
// readiness e continuaria recebendo tráfego — para então devolver 503 em TODA
// rota de negócio, porque não consegue validar token. Dizer "pronto" quando
// nenhuma requisição autenticada pode ser atendida é mentir para o
// orquestrador, e é o tipo de mentira que transforma um incidente pequeno
// (IdP oscilando) em indisponibilidade total do serviço.
func (a *Autenticador) CheckReadiness(ctx context.Context) error {
	if a == nil || a.verificador == nil {
		return nil
	}
	return a.verificador.CheckReadiness(ctx)
}

// CheckReadiness busca o documento de discovery do IdP, IGNORANDO o cache.
//
// # Por que furar o cache aqui
//
// É a MENOR operação que prova três coisas de uma vez: o IdP responde, a URL de
// discovery configurada está certa, e o emissor anunciado é o esperado.
//
// E precisa ser AGORA: com o cache, o readiness responderia "UP" durante todo o
// TTL mesmo com o Keycloak caído — que é exatamente o momento em que ele tem de
// dizer DOWN para a instância sair da rotação. Um readiness que consulta cache
// não é um readiness.
//
// O prazo é curto e independente do cliente HTTP (que tem o seu): readiness é
// chamado com frequência por orquestradores, e uma verificação pendurada faz o
// endpoint inteiro parecer travado.
func (v *Verifier) CheckReadiness(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	_, err := v.obterDiscovery(ctx, true)
	return err
}
