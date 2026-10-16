package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// DefaultHealthCheckURL é o endereço que o modo `-health-check` consulta. Ele
// aponta para o próprio processo (rota de readiness) na porta padrão.
const DefaultHealthCheckURL = "http://127.0.0.1:8080/health/ready"

// healthCheckTimeout limita a consulta do modo `-health-check`. O compose define
// `timeout: 5s` para o healthcheck; ficar abaixo disso garante que o binário
// devolva o veredito antes de o Docker desistir dele.
const healthCheckTimeout = 3 * time.Second

// HealthCheckURL monta a URL de readiness a partir do endereço de escuta.
//
// # Por que existe (P1)
//
// O `docker-compose.yml` já declarava `test: ["CMD", "/app/api", "-health-check"]`
// — mas a flag não existia: o contêiner subia `unhealthy` e o
// `docker compose up --build` do README §15 falhava na prática. Uma flag que o
// ambiente espera e o binário ignora é pior do que não ter healthcheck: o
// relatório do orquestrador passa a mentir.
//
// A URL sai de `HTTP_ADDR` para a porta bater com a configurada. Endereço vazio
// (`:8080` também é vazio de host) vira `127.0.0.1` — o healthcheck roda DENTRO
// do contêiner, e `0.0.0.0` não é um destino válido para o cliente.
func HealthCheckURL(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return DefaultHealthCheckURL
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// Sem porta explícita (ex.: apenas "8080"): assume que é a porta.
		return "http://127.0.0.1:" + strings.TrimPrefix(addr, ":") + "/health/ready"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s/health/ready", net.JoinHostPort(host, port))
}

// RunHealthCheck consulta o readiness e devolve erro quando NÃO está pronto.
//
// É o modo `-health-check` do binário: existe para o healthcheck do Docker, que
// executa o MESMO binário com a flag em vez de instalar `curl` na imagem. Sai com
// 0 quando o serviço está pronto e 1 quando não está.
//
// Ele NÃO carrega a configuração da aplicação: o healthcheck roda num processo
// curto, e exigir `DATABASE_URL`/`OIDC_ISSUER` ali faria o veredito depender de
// variável de ambiente em vez do estado do serviço.
func RunHealthCheck(ctx context.Context, url string) error {
	if strings.TrimSpace(url) == "" {
		url = DefaultHealthCheckURL
	}

	ctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("health-check: montando requisição para %s: %w", url, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("health-check: %s não respondeu: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// 503 do readiness significa "estou de pé, mas sem dependência": o
		// Docker deve continuar tentando, e é isso que o status 1 pede.
		return fmt.Errorf("health-check: %s respondeu %d", url, resp.StatusCode)
	}
	return nil
}
