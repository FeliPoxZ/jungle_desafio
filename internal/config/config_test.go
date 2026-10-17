package config_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/config"
)

// baseEnv é o conjunto mínimo de variáveis obrigatórias para uma configuração
// válida. Cada teste parte daqui e altera só o que quer provar.
func baseEnv(t *testing.T) {
	t.Helper()
	// Limpa variáveis que poderiam vazar do ambiente do desenvolvedor ou da CI
	// e mascarar o comportamento testado.
	for _, key := range []string{
		"DB_MAX_CONNS", "DB_MIN_CONNS", "SQS_BATCH_SIZE", "SQS_VISIBILITY_TIMEOUT",
		"SQS_TRANSACTIONS_QUEUE", "HTTP_REQUEST_TIMEOUT", "OIDC_ISSUER",
		"OIDC_DISCOVERY_URL", "LOG_LEVEL", "OUTBOX_LEASE", "REFERENCE_MAX_ATTEMPTS",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("DATABASE_URL", "postgres://jungleg:jungleg@localhost:5432/jungleg?sslmode=disable")
	t.Setenv("OIDC_ISSUER", "http://localhost:8080/realms/jungleg")
}

func TestLoadAppliesDefaults(t *testing.T) {
	baseEnv(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() devolveu erro inesperado: %v", err)
	}

	if cfg.Env != "local" {
		t.Errorf("Env = %q, quer %q", cfg.Env, "local")
	}
	if cfg.HTTP.Addr != ":8080" {
		t.Errorf("HTTP.Addr = %q, quer %q", cfg.HTTP.Addr, ":8080")
	}
	if cfg.Database.MaxConns != 20 {
		t.Errorf("Database.MaxConns = %d, quer 20", cfg.Database.MaxConns)
	}
	if cfg.Database.MigrationsOnStart {
		t.Error("Database.MigrationsOnStart deveria ser false por padrão (o job de migration é quem migra)")
	}
	if cfg.SQS.TransactionsQueue != "wager-transactions.fifo" {
		t.Errorf("SQS.TransactionsQueue = %q", cfg.SQS.TransactionsQueue)
	}
	if cfg.SQS.VisibilityTimeout != 30*time.Second {
		t.Errorf("SQS.VisibilityTimeout = %v, quer 30s", cfg.SQS.VisibilityTimeout)
	}
	if cfg.OIDC.ClockSkew != 30*time.Second {
		t.Errorf("OIDC.ClockSkew = %v, quer 30s", cfg.OIDC.ClockSkew)
	}
}

// A URL de discovery cai para o próprio issuer quando não informada: em
// desenvolvimento local o mesmo endereço serve para as duas coisas.
func TestLoadDiscoveryURLFallsBackToIssuer(t *testing.T) {
	baseEnv(t)
	t.Setenv("OIDC_DISCOVERY_URL", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() devolveu erro inesperado: %v", err)
	}
	if cfg.OIDC.DiscoveryURL != cfg.OIDC.Issuer {
		t.Errorf("DiscoveryURL = %q, quer igual ao Issuer %q", cfg.OIDC.DiscoveryURL, cfg.OIDC.Issuer)
	}
}

// Barra final no issuer é normalização, não erro: o claim "iss" do Keycloak não
// tem barra no fim e a comparação precisa ser estável.
func TestLoadNormalizesIssuerTrailingSlash(t *testing.T) {
	baseEnv(t)
	t.Setenv("OIDC_ISSUER", "http://localhost:8080/realms/jungleg/")
	t.Setenv("OIDC_DISCOVERY_URL", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() devolveu erro inesperado: %v", err)
	}
	if cfg.OIDC.Issuer != "http://localhost:8080/realms/jungleg" {
		t.Errorf("Issuer = %q, quer sem barra final", cfg.OIDC.Issuer)
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(t *testing.T)
		wantMessage string
	}{
		{
			name:        "DATABASE_URL vazia",
			mutate:      func(t *testing.T) { t.Setenv("DATABASE_URL", "") },
			wantMessage: "DATABASE_URL é obrigatória",
		},
		{
			name:        "DATABASE_URL sem esquema postgres",
			mutate:      func(t *testing.T) { t.Setenv("DATABASE_URL", "mysql://user:pass@localhost/db") },
			wantMessage: "deve começar com postgres://",
		},
		{
			name:        "OIDC_ISSUER vazio",
			mutate:      func(t *testing.T) { t.Setenv("OIDC_ISSUER", "") },
			wantMessage: "OIDC_ISSUER é obrigatório",
		},
		{
			name:        "OIDC_ISSUER sem esquema",
			mutate:      func(t *testing.T) { t.Setenv("OIDC_ISSUER", "localhost:8080/realms/jungleg") },
			wantMessage: "deve ser uma URL http(s)",
		},
		{
			name:        "SQS_BATCH_SIZE acima do limite do SQS",
			mutate:      func(t *testing.T) { t.Setenv("SQS_BATCH_SIZE", "11") },
			wantMessage: "SQS_BATCH_SIZE deve estar entre 1 e 10",
		},
		{
			name:        "fila sem sufixo .fifo",
			mutate:      func(t *testing.T) { t.Setenv("SQS_TRANSACTIONS_QUEUE", "wager-transactions") },
			wantMessage: "deve terminar em .fifo",
		},
		{
			name:        "visibility timeout perigoso",
			mutate:      func(t *testing.T) { t.Setenv("SQS_VISIBILITY_TIMEOUT", "1s") },
			wantMessage: "SQS_VISIBILITY_TIMEOUT muito baixo",
		},
		{
			name:        "lease da outbox curto demais",
			mutate:      func(t *testing.T) { t.Setenv("OUTBOX_LEASE", "1s") },
			wantMessage: "OUTBOX_LEASE muito baixo",
		},
		{
			name: "DB_MIN_CONNS maior que MAX_CONNS",
			mutate: func(t *testing.T) {
				t.Setenv("DB_MAX_CONNS", "4")
				t.Setenv("DB_MIN_CONNS", "8")
			},
			wantMessage: "DB_MIN_CONNS deve estar entre zero e DB_MAX_CONNS",
		},
		{
			name:        "LOG_LEVEL inválido",
			mutate:      func(t *testing.T) { t.Setenv("LOG_LEVEL", "verbose") },
			wantMessage: "LOG_LEVEL inválido",
		},
		{
			name:        "REFERENCE_MAX_ATTEMPTS zerado",
			mutate:      func(t *testing.T) { t.Setenv("REFERENCE_MAX_ATTEMPTS", "0") },
			wantMessage: "REFERENCE_MAX_ATTEMPTS deve ser maior que zero",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseEnv(t)
			tt.mutate(t)

			_, err := config.Load()
			if err == nil {
				t.Fatalf("Load() deveria falhar, mas devolveu nil")
			}
			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Errorf("mensagem de erro = %q, quer conter %q", err.Error(), tt.wantMessage)
			}
		})
	}
}

// Um erro de configuração deve apontar TODOS os problemas de uma vez, não só o
// primeiro: isso evita o ciclo "sobe, corrige um, sobe de novo".
func TestValidateReportsEveryProblem(t *testing.T) {
	baseEnv(t)
	t.Setenv("DATABASE_URL", "")
	t.Setenv("OIDC_ISSUER", "")
	t.Setenv("SQS_BATCH_SIZE", "99")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() deveria falhar")
	}
	for _, want := range []string{"DATABASE_URL é obrigatória", "OIDC_ISSUER é obrigatório", "SQS_BATCH_SIZE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("erro deveria mencionar %q; recebido: %v", want, err)
		}
	}
}

// Ligar o consumidor de SQS com os DEFAULTS precisa resultar em configuração
// válida.
//
// Existe porque já não resultou: o `HTTP_STOP_TIMEOUT` padrão era 25s, e a
// validação exige que ele cubra o `HTTP_SHUTDOWN_TIMEOUT` (20s) **mais** o
// `SQS_SHUTDOWN_GRACE` (15s) quando o consumidor está ligado. O sintoma era a
// aplicação não subir — com a mensagem "configuração inválida" — num ambiente em
// que o operador só tinha ligado o worker. Um default que viola a própria regra
// de validação é um defeito, e é este teste que o trava.
func TestLoadComConsumidorLigadoUsaDefaultsValidos(t *testing.T) {
	baseEnv(t)
	t.Setenv("SQS_CONSUME_TRANSACTIONS", "true")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() com o consumidor ligado e defaults devolveu erro: %v", err)
	}

	soma := cfg.HTTP.ShutdownTimeout + cfg.SQS.ShutdownGrace
	if cfg.HTTP.StopTimeout < soma {
		t.Errorf("StopTimeout (%v) não cobre ShutdownTimeout + ShutdownGrace (%v)",
			cfg.HTTP.StopTimeout, soma)
	}
}

// A ordem invertida é recusada: o Fx mataria o servidor no meio da drenagem.
func TestLoadRejeitaStopTimeoutMenorQueShutdown(t *testing.T) {
	baseEnv(t)
	t.Setenv("HTTP_STOP_TIMEOUT", "5s")
	t.Setenv("HTTP_SHUTDOWN_TIMEOUT", "20s")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load() aceitou StopTimeout menor que ShutdownTimeout")
	}
	if !strings.Contains(err.Error(), "HTTP_STOP_TIMEOUT") {
		t.Errorf("a mensagem deveria apontar HTTP_STOP_TIMEOUT; recebido: %v", err)
	}
}

func TestSlogLevel(t *testing.T) {
	tests := []struct {
		in      string
		want    slog.Level
		wantErr bool
	}{
		{in: "debug", want: slog.LevelDebug},
		{in: "info", want: slog.LevelInfo},
		{in: "INFO", want: slog.LevelInfo},
		{in: "warn", want: slog.LevelWarn},
		{in: "warning", want: slog.LevelWarn},
		{in: "error", want: slog.LevelError},
		{in: "", want: slog.LevelInfo},
		{in: "trace", wantErr: true},
	}

	for _, tt := range tests {
		t.Run("nivel_"+tt.in, func(t *testing.T) {
			cfg := config.Config{LogLevel: tt.in}
			got, err := cfg.SlogLevel()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("SlogLevel(%q) deveria devolver erro", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("SlogLevel(%q) devolveu erro inesperado: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("SlogLevel(%q) = %v, quer %v", tt.in, got, tt.want)
			}
		})
	}
}
