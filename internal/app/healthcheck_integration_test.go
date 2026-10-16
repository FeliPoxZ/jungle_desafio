//go:build integration

package app_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/app"
)

// TestBinarioRealHealthCheckEShutdown sobe o BINÁRIO de verdade e verifica os
// dois comportamentos que o Docker Compose usa (pendências P1 e P7):
//
//  1. `-health-check` responde 0 quando o serviço está pronto — é o
//     healthcheck declarado no compose, que antes apontava para uma flag
//     inexistente e deixava o contêiner `unhealthy`;
//  2. o processo encerra de forma graciosa ao receber o sinal de término,
//     dentro do prazo configurado (`HTTP_STOP_TIMEOUT`), e sai com código 0.
//
// É um teste de INTEGRAÇÃO porque depende do PostgreSQL e do Keycloak reais: sem
// eles o `/health/ready` responde 503, e o healthcheck não teria o que provar.
func TestBinarioRealHealthCheckEShutdown(t *testing.T) {
	binario := compilarBinario(t)

	porta := portaDeTeste(t)
	endereco := fmt.Sprintf("127.0.0.1:%d", porta)

	postgres := envOu(t, "TEST_DATABASE_URL", "postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable")
	issuer := envOu(t, "OIDC_ISSUER", "http://localhost:8080/realms/jungleg")
	discovery := envOu(t, "OIDC_DISCOVERY_URL", issuer)

	cmd := exec.Command(binario)
	cmd.Env = append(os.Environ(),
		"HTTP_ADDR="+endereco,
		"DATABASE_URL="+postgres,
		"OIDC_ISSUER="+issuer,
		"OIDC_DISCOVERY_URL="+discovery,
		"OIDC_AUDIENCE="+envOu(t, "OIDC_AUDIENCE", "jungleg-api"),
		"HTTP_STOP_TIMEOUT=15s",
		// Precisa caber dentro do StopTimeout (a config valida a ordem): o Fx
		// espera os hooks de parada por StopTimeout, e o hook do HTTP drena as
		// requisições em andamento por ShutdownTimeout.
		"HTTP_SHUTDOWN_TIMEOUT=10s",
		"LOG_LEVEL=warn",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("subindo o binário: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})

	// Espera o serviço ficar pronto, consultando o MESMO endpoint que o
	// `-health-check` consulta.
	pronto := esperarPronto(t, "http://"+endereco+"/health/ready", 30*time.Second)

	if !pronto {
		corpo := corpoDe(t, "http://"+endereco+"/health/ready")
		t.Fatalf("o serviço não ficou pronto em 30s; /health/ready respondeu:\n%s", corpo)
	}

	// (1) A flag do healthcheck precisa responder 0 com o serviço pronto.
	if err := app.RunHealthCheck(context.Background(), "http://"+endereco+"/health/ready"); err != nil {
		t.Fatalf("RunHealthCheck() com o serviço pronto devolveu erro: %v", err)
	}

	// E o BINÁRIO chamado com a flag precisa sair com 0 — é o que o Docker lê.
	saida := exec.Command(binario, "-health-check", "-health-check-url", "http://"+endereco+"/health/ready")
	saida.Env = os.Environ()
	if out, err := saida.CombinedOutput(); err != nil {
		t.Fatalf("`api -health-check` devolveu erro (%v); saída: %s", err, out)
	}

	// (2) Encerramento gracioso. No Windows não existe SIGTERM: `Kill` é
	// terminação dura, e o que se verifica ali é que o processo morre sem
	// travar. Nos demais sistemas o sinal é o mesmo que o `docker stop` envia.
	if runtime.GOOS == "windows" {
		if err := cmd.Process.Kill(); err != nil {
			t.Fatalf("encerrando o processo: %v", err)
		}
		return
	}

	inicio := time.Now()
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("enviando o sinal de término: %v", err)
	}

	terminou := make(chan error, 1)
	go func() { terminou <- cmd.Wait() }()

	select {
	case err := <-terminou:
		// Saída limpa: o Run devolve nil depois do shutdown.
		if err != nil {
			t.Fatalf("o processo terminou com erro: %v", err)
		}
		if decorrido := time.Since(inicio); decorrido > 20*time.Second {
			t.Errorf("shutdown levou %v; o prazo configurado era 15s", decorrido)
		}
	case <-time.After(25 * time.Second):
		t.Fatal("o processo não encerrou dentro do prazo de shutdown")
	}
}

// TestBinarioRealFalhaComConfigInvalida confirma que configuração inválida
// impede o boot — e o processo sai com código diferente de zero.
func TestBinarioRealFalhaComConfigInvalida(t *testing.T) {
	binario := compilarBinario(t)

	// DATABASE_URL válida, OIDC_ISSUER ausente.
	cmd := exec.Command(binario)
	cmd.Env = []string{
		"HTTP_ADDR=127.0.0.1:0",
		"DATABASE_URL=postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable",
	}

	saida, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("o binário subiu com configuração inválida; saída: %s", saida)
	}
	if !strings.Contains(string(saida), "OIDC_ISSUER") {
		t.Errorf("a saída não menciona OIDC_ISSUER: %s", saida)
	}
}

// --- utilidades --------------------------------------------------------------

// compilarBinario compila o `cmd/api` para um arquivo temporário.
func compilarBinario(t *testing.T) string {
	t.Helper()

	nome := "jungleg-api-teste"
	if runtime.GOOS == "windows" {
		nome += ".exe"
	}
	destino := filepath.Join(t.TempDir(), nome)

	cmd := exec.Command("go", "build", "-o", destino, "./cmd/api")
	cmd.Dir = raizDoProjeto(t)

	if saida, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compilando cmd/api: %v\n%s", err, saida)
	}
	return destino
}

// raizDoProjeto encontra a raiz a partir deste arquivo de teste.
func raizDoProjeto(t *testing.T) string {
	t.Helper()

	_, arquivo, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("não foi possível localizar o arquivo de teste")
	}
	// .../internal/app/healthcheck_integration_test.go -> .../
	return filepath.Dir(filepath.Dir(filepath.Dir(arquivo)))
}

// portaDeTeste escolhe uma porta alta. Uma porta fixa colidiria com o compose
// rodando na mesma máquina (ele publica 8081).
func portaDeTeste(t *testing.T) int {
	t.Helper()
	return 18099
}

// esperarPronto consulta o readiness até ele responder 200.
func esperarPronto(t *testing.T, url string, prazo time.Duration) bool {
	t.Helper()

	fim := time.Now().Add(prazo)
	cliente := &http.Client{Timeout: 2 * time.Second}

	for time.Now().Before(fim) {
		resp, err := cliente.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}

// corpoDe devolve o corpo da resposta, para o diagnóstico da falha.
func corpoDe(t *testing.T, url string) string {
	t.Helper()

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Sprintf("(sem resposta: %v)", err)
	}
	defer func() { _ = resp.Body.Close() }()

	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	return fmt.Sprintf("HTTP %d: %s", resp.StatusCode, buf[:n])
}

// envOu lê a variável ou usa o padrão.
func envOu(t *testing.T, chave, padrao string) string {
	t.Helper()

	if v := strings.TrimSpace(os.Getenv(chave)); v != "" {
		return v
	}
	return padrao
}
