//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/fx"

	"github.com/FeliPoxZ/jungle_desafio/internal/app"
	"github.com/FeliPoxZ/jungle_desafio/internal/infra/postgres"
)

// TestCicloDeVidaFxComStartEStopReais exercita o ciclo de vida do Fx com o GRAFO
// DE PRODUÇÃO: `app.ModuleOptions()` inteiro, PostgreSQL, IdP e SQS reais.
//
// O README §13 pede "uma verificação da composição Fx e de seu início e
// encerramento, incluindo liberação de recursos dos workers". O que já existia era
// `fx.ValidateApp` (o grafo FECHA) — que não executa construtor nenhum. O que
// falta é o que este teste faz:
//
//  1. **Start real**: o servidor HTTP sobe e o `/health/ready` responde 200 com
//     PostgreSQL, IdP, SQS e `workers` de pé;
//  2. **ordem dos hooks**: dois hooks-sonda registrados por ÚLTIMO param PRIMEIRO
//     (ordem inversa do registro), e a ordem observada é a inversa;
//  3. **o pool fechado por último**: a sonda de parada consegue dar `Ping` no pool
//     (ele ainda está aberto quando os componentes param) e, depois do `Stop`, o
//     pool está fechado.
func TestCicloDeVidaFxComStartEStopReais(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if dsn == "" {
		dsn = "postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable"
	}

	porta := portaLivre(t, 18210)
	t.Setenv("APP_ENV", "test")
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("DB_MAX_CONNS", "4")
	t.Setenv("DB_MIN_CONNS", "1")
	t.Setenv("OIDC_ISSUER", issuerDeTeste())
	t.Setenv("OIDC_DISCOVERY_URL", clientIDBase())
	t.Setenv("OIDC_AUDIENCE", envOu("OIDC_AUDIENCE", "jungleg-api"))
	t.Setenv("SQS_ENDPOINT", envOu("SQS_ENDPOINT", "http://localhost:4566"))
	t.Setenv("HTTP_ADDR", fmt.Sprintf("127.0.0.1:%d", porta))
	t.Setenv("HTTP_STOP_TIMEOUT", "15s")
	t.Setenv("HTTP_SHUTDOWN_TIMEOUT", "5s")
	// Os laços ficam desligados: o que se prova aqui é o ciclo de vida do grafo,
	// não o consumo. Com eles ligados, o teste também dependeria do estado da fila.
	t.Setenv("SQS_CONSUME_TRANSACTIONS", "false")
	t.Setenv("OUTBOX_ENABLED", "false")
	t.Setenv("REFERENCE_ENABLED", "false")

	var (
		ordemDeParada []string
		erroDoPing    error
		pool          *postgres.Pool
	)

	aplicacao := fx.New(
		app.ModuleOptions(),
		fx.NopLogger,
		fx.StopTimeout(15*time.Second),
		// Registrados DEPOIS de todos os módulos: param PRIMEIRO.
		fx.Invoke(func(lc fx.Lifecycle, p *postgres.Pool) {
			pool = p
			lc.Append(fx.Hook{OnStop: func(ctx context.Context) error {
				ordemDeParada = append(ordemDeParada, "sonda-1")
				// Se o pool já estivesse fechado aqui, a ordem do projeto estaria
				// errada: componentes parariam DEPOIS da dependência.
				erroDoPing = p.Ping(ctx)
				return nil
			}})
		}),
		fx.Invoke(func(lc fx.Lifecycle) {
			lc.Append(fx.Hook{OnStop: func(context.Context) error {
				ordemDeParada = append(ordemDeParada, "sonda-2")
				return nil
			}})
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := aplicacao.Start(ctx); err != nil {
		t.Fatalf("Start do grafo real: %v", err)
	}

	// O servidor HTTP está de pé e o readiness do grafo real responde 200.
	url := fmt.Sprintf("http://127.0.0.1:%d/health/ready", porta)
	cliente := &http.Client{Timeout: 5 * time.Second}
	var corpo []byte
	pronto := false
	for fim := time.Now().Add(20 * time.Second); time.Now().Before(fim); {
		resp, err := cliente.Get(url)
		if err == nil {
			corpo = make([]byte, 2048)
			n, _ := resp.Body.Read(corpo)
			_ = resp.Body.Close()
			corpo = corpo[:n]
			if resp.StatusCode == http.StatusOK {
				pronto = true
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !pronto {
		t.Fatalf("/health/ready não respondeu 200 no grafo real: %s", corpo)
	}
	for _, dependencia := range []string{`"postgres":"UP"`, `"oidc":"UP"`, `"sqs":"UP"`, `"workers":"UP"`} {
		if !strings.Contains(string(corpo), dependencia) {
			t.Errorf("readiness sem %s: %s", dependencia, corpo)
		}
	}

	if err := aplicacao.Stop(ctx); err != nil {
		t.Fatalf("Stop do grafo real: %v", err)
	}

	// 2. Ordem inversa do registro.
	if len(ordemDeParada) != 2 || ordemDeParada[0] != "sonda-2" || ordemDeParada[1] != "sonda-1" {
		t.Errorf("ordem de parada = %v, esperado [sonda-2 sonda-1]", ordemDeParada)
	}

	// 3. O pool ainda estava aberto quando a última sonda parou.
	if erroDoPing != nil {
		t.Errorf("o pool já estava fechado durante o OnStop dos componentes: %v", erroDoPing)
	}

	// ...e fechado depois do encerramento completo.
	if err := pool.Ping(context.Background()); err == nil {
		t.Error("o pool deveria estar FECHADO depois do Stop")
	}
}
