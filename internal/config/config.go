// Package config carrega e valida toda a configuração da aplicação a partir de
// variáveis de ambiente.
//
// Princípios adotados aqui:
//
//   - Falhar cedo (fail fast): qualquer configuração ausente ou inválida impede
//     o processo de subir. Um serviço financeiro que sobe "mais ou menos" é pior
//     do que um serviço que não sobe.
//   - Sem estado global: a configuração é um valor construído uma vez, injetado
//     pelo Fx e passado por referência. Nenhum pacote lê os.Getenv diretamente.
//   - Defaults seguros para desenvolvimento local, sempre sobrescrevíveis.
package config

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config é a raiz de configuração da aplicação, dividida por área de
// responsabilidade para manter os construtores pequenos e explícitos.
type Config struct {
	Env         string          `env:"APP_ENV" envDefault:"local"`
	HTTP        HTTPConfig      `envPrefix:"HTTP_"`
	Database    DatabaseConfig  `envPrefix:"DB_"`
	OIDC        OIDCConfig      `envPrefix:"OIDC_"`
	SQS         SQSConfig       `envPrefix:"SQS_"`
	Outbox      OutboxConfig    `envPrefix:"OUTBOX_"`
	Reference   ReferenceConfig `envPrefix:"REFERENCE_"`
	LogLevel    string          `env:"LOG_LEVEL" envDefault:"info"`
	ShutdownTTL time.Duration   `env:"SHUTDOWN_TIMEOUT" envDefault:"25s"`

	// DatabaseURL não tem prefixo DB_ por ser uma string de conexão completa,
	// padronizada desse jeito em praticamente todo ecossistema Go.
	//
	// Deliberadamente SEM a tag `required` da lib de env: a obrigatoriedade é
	// verificada em Validate(), junto com todas as outras regras. Motivo: com a
	// tag, a lib aborta no parsing e o operador só descobre um problema por vez,
	// com mensagem genérica em inglês. Uma única autoridade de validação reporta
	// todos os problemas de uma vez (ver TestValidateReportsEveryProblem).
	DatabaseURL string `env:"DATABASE_URL"`
}

// HTTPConfig controla o servidor HTTP.
type HTTPConfig struct {
	Addr            string        `env:"ADDR" envDefault:":8080"`
	ReadTimeout     time.Duration `env:"READ_TIMEOUT" envDefault:"10s"`
	WriteTimeout    time.Duration `env:"WRITE_TIMEOUT" envDefault:"15s"`
	IdleTimeout     time.Duration `env:"IDLE_TIMEOUT" envDefault:"60s"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"20s"`
	RequestTimeout  time.Duration `env:"REQUEST_TIMEOUT" envDefault:"10s"`
	MaxBodyBytes    int64         `env:"MAX_BODY_BYTES" envDefault:"1048576"` // 1 MiB
	// StopTimeout é o prazo TOTAL do shutdown do processo (o `fx.StopTimeout`):
	// quanto tempo o Fx espera os hooks de parada antes de desistir. Precisa ser
	// MAIOR que ShutdownTimeout, senão o Fx mataria o servidor HTTP antes de ele
	// terminar de drenar as requisições em andamento.
	StopTimeout time.Duration `env:"STOP_TIMEOUT" envDefault:"25s"`
}

// DatabaseConfig controla o pool do PostgreSQL.
type DatabaseConfig struct {
	MaxConns         int32         `env:"MAX_CONNS" envDefault:"20"`
	MinConns         int32         `env:"MIN_CONNS" envDefault:"2"`
	ConnMaxLifetime  time.Duration `env:"CONN_MAX_LIFETIME" envDefault:"30m"`
	ConnMaxIdleTime  time.Duration `env:"CONN_MAX_IDLE_TIME" envDefault:"5m"`
	ConnectTimeout   time.Duration `env:"CONNECT_TIMEOUT" envDefault:"5s"`
	StatementTimeout time.Duration `env:"STATEMENT_TIMEOUT" envDefault:"10s"`
	LockTimeout      time.Duration `env:"LOCK_TIMEOUT" envDefault:"5s"`
	// MigrationsOnStart aplica as migrations no boot. Fica desligado por padrão
	// porque aplicar schema é responsabilidade do job de migration, não da API:
	// com várias instâncias subindo juntas, todas tentariam migrar ao mesmo tempo.
	MigrationsOnStart bool `env:"MIGRATIONS_ON_START" envDefault:"false"`
}

// OIDCConfig descreve o IdP (Keycloak local) e como validar os tokens.
type OIDCConfig struct {
	// Issuer é o valor EXATO do claim "iss" esperado no token.
	// Obrigatoriedade verificada em Validate() — ver comentário em DatabaseURL.
	Issuer string `env:"ISSUER"`
	// DiscoveryURL é de onde o serviço busca /.well-known/openid-configuration.
	// Pode diferir do Issuer quando o nome interno da rede Docker (keycloak)
	// não é o mesmo endereço que o cliente usa para obter o token (localhost).
	DiscoveryURL string `env:"DISCOVERY_URL" envDefault:""`
	Audience     string `env:"AUDIENCE" envDefault:""`
	// JWKSCacheTTL controla por quanto tempo as chaves públicas ficam em cache
	// antes de nova consulta ao IdP.
	JWKSCacheTTL time.Duration `env:"JWKS_CACHE_TTL" envDefault:"15m"`
	// ClockSkew é a tolerância de relógio ao validar exp/nbf/iat.
	ClockSkew time.Duration `env:"CLOCK_SKEW" envDefault:"30s"`
	// HTTPTimeout limita cada chamada ao IdP (discovery e JWKS). É separado do
	// HTTP_REQUEST_TIMEOUT porque o segundo é o prazo da REQUISIÇÃO do cliente:
	// o do IdP precisa ser curto (já estamos esperando por ele) e o do cliente
	// precisa acomodar o processamento inteiro.
	HTTPTimeout time.Duration `env:"HTTP_TIMEOUT" envDefault:"5s"`
	// TokenURL é o endpoint de tokens do IdP, usado APENAS por ferramentas e
	// testes (o serviço em si nunca emite token — README §2).
	TokenURL string `env:"TOKEN_URL" envDefault:""`
}

// SQSConfig descreve o broker e as filas usadas pela aplicação.
type SQSConfig struct {
	Endpoint            string        `env:"ENDPOINT" envDefault:"http://localhost:4566"`
	Region              string        `env:"REGION" envDefault:"us-east-1"`
	AccessKeyID         string        `env:"ACCESS_KEY_ID" envDefault:"test"`
	SecretAccessKey     string        `env:"SECRET_ACCESS_KEY" envDefault:"test"`
	TransactionsQueue   string        `env:"TRANSACTIONS_QUEUE" envDefault:"wager-transactions.fifo"`
	TransactionsDLQ     string        `env:"TRANSACTIONS_DLQ" envDefault:"wager-transactions-dlq.fifo"`
	EventsQueue         string        `env:"EVENTS_QUEUE" envDefault:"wallet-events.fifo"`
	EventsDLQ           string        `env:"EVENTS_DLQ" envDefault:"wallet-events-dlq.fifo"`
	MaxReceiveCount     int32         `env:"MAX_RECEIVE_COUNT" envDefault:"5"`
	VisibilityTimeout   time.Duration `env:"VISIBILITY_TIMEOUT" envDefault:"30s"`
	WaitTime            time.Duration `env:"WAIT_TIME" envDefault:"20s"`
	BatchSize           int32         `env:"BATCH_SIZE" envDefault:"10"`
	ConsumerConcurrency int           `env:"CONSUMER_CONCURRENCY" envDefault:"8"`
	// BaseBackoff e MaxBackoff são o RETRY do consumidor, aplicado por
	// `ChangeMessageVisibility`: a mensagem não é removida nem descartada, apenas
	// fica invisível pelo prazo calculado (exponencial com teto e jitter).
	//
	// O teto existe para a fila não parecer travada: sem ele, a décima tentativa
	// adiaria a mensagem por horas.
	BaseBackoff time.Duration `env:"BASE_BACKOFF" envDefault:"2s"`
	MaxBackoff  time.Duration `env:"MAX_BACKOFF" envDefault:"1m"`
	// ShutdownGrace é quanto tempo o consumidor espera o trabalho em voo antes de
	// LIBERAR a visibilidade do que sobrou (reentrega segura).
	//
	// Precisa caber dentro do `HTTP_STOP_TIMEOUT` (o prazo total do Fx), junto com
	// o encerramento do servidor HTTP — a config valida essa ordem.
	ShutdownGrace         time.Duration `env:"SHUTDOWN_GRACE" envDefault:"15s"`
	ProvisionQueuesOnBoot bool          `env:"PROVISION_QUEUES_ON_BOOT" envDefault:"false"`
	// ConsumeTransactions liga o consumidor da fila de entrada. Em testes de
	// unidade e em execuções só-HTTP pode ser desligado.
	ConsumeTransactions bool `env:"CONSUME_TRANSACTIONS" envDefault:"false"`
}

// OutboxConfig controla o worker publicador de eventos.
type OutboxConfig struct {
	Enabled      bool          `env:"ENABLED" envDefault:"false"`
	BatchSize    int32         `env:"BATCH_SIZE" envDefault:"50"`
	Lease        time.Duration `env:"LEASE" envDefault:"30s"`
	PollInterval time.Duration `env:"POLL_INTERVAL" envDefault:"1s"`
	MaxAttempts  int32         `env:"MAX_ATTEMPTS" envDefault:"10"`
	BaseBackoff  time.Duration `env:"BASE_BACKOFF" envDefault:"2s"`
	MaxBackoff   time.Duration `env:"MAX_BACKOFF" envDefault:"5m"`
}

// ReferenceConfig controla o worker que resolve referências pendentes.
type ReferenceConfig struct {
	Enabled       bool          `env:"ENABLED" envDefault:"false"`
	PollInterval  time.Duration `env:"POLL_INTERVAL" envDefault:"2s"`
	BatchSize     int32         `env:"BATCH_SIZE" envDefault:"25"`
	MaxAttempts   int32         `env:"MAX_ATTEMPTS" envDefault:"20"`
	BaseBackoff   time.Duration `env:"BASE_BACKOFF" envDefault:"5s"`
	MaxBackoff    time.Duration `env:"MAX_BACKOFF" envDefault:"10m"`
	ReaperEvery   time.Duration `env:"REAPER_INTERVAL" envDefault:"1m"`
	PendingMaxAge time.Duration `env:"PENDING_MAX_AGE" envDefault:"2m"`
}

// Load lê as variáveis de ambiente, valida o resultado e devolve a Config.
// Qualquer erro retornado aqui impede o boot da aplicação.
//
// A config é devolvida como PONTEIRO: o Fx a injeta em vários construtores e
// trafegar por valor copiaria a struct inteira em cada injeção — além de exigir
// tipos distintos (*Config e Config) em pontos diferentes do grafo.
func Load() (*Config, error) {
	var cfg Config
	if err := env.ParseWithOptions(&cfg, env.Options{
		// Prefixo global opcional vazio: os nomes já são explícitos.
		RequiredIfNoDef: false,
	}); err != nil {
		return nil, fmt.Errorf("config: lendo variáveis de ambiente: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate aplica regras que vão além da presença da variável: coerência entre
// valores, faixas aceitáveis e normalizações.
func (c *Config) Validate() error {
	var problems []string

	if strings.TrimSpace(c.DatabaseURL) == "" {
		problems = append(problems, "DATABASE_URL é obrigatória")
	}
	if !strings.HasPrefix(c.DatabaseURL, "postgres://") && !strings.HasPrefix(c.DatabaseURL, "postgresql://") {
		problems = append(problems, "DATABASE_URL deve começar com postgres:// ou postgresql://")
	}
	if c.Database.MaxConns <= 0 {
		problems = append(problems, "DB_MAX_CONNS deve ser maior que zero")
	}
	if c.Database.MinConns < 0 || c.Database.MinConns > c.Database.MaxConns {
		problems = append(problems, "DB_MIN_CONNS deve estar entre zero e DB_MAX_CONNS")
	}

	if strings.TrimSpace(c.OIDC.Issuer) == "" {
		problems = append(problems, "OIDC_ISSUER é obrigatório")
	}
	if !strings.HasPrefix(c.OIDC.Issuer, "http://") && !strings.HasPrefix(c.OIDC.Issuer, "https://") {
		problems = append(problems, "OIDC_ISSUER deve ser uma URL http(s)")
	}
	if c.OIDC.ClockSkew < 0 {
		problems = append(problems, "OIDC_CLOCK_SKEW não pode ser negativo")
	}
	if c.OIDC.JWKSCacheTTL <= 0 {
		problems = append(problems, "OIDC_JWKS_CACHE_TTL deve ser maior que zero")
	}
	if c.OIDC.HTTPTimeout <= 0 {
		problems = append(problems, "OIDC_HTTP_TIMEOUT deve ser maior que zero")
	}

	if c.HTTP.RequestTimeout <= 0 {
		problems = append(problems, "HTTP_REQUEST_TIMEOUT deve ser maior que zero")
	}
	if c.HTTP.MaxBodyBytes <= 0 {
		problems = append(problems, "HTTP_MAX_BODY_BYTES deve ser maior que zero")
	}
	if c.HTTP.StopTimeout <= 0 {
		problems = append(problems, "HTTP_STOP_TIMEOUT deve ser maior que zero")
	}
	// O shutdown do processo tem de caber o shutdown do servidor: o Fx espera os
	// hooks de parada até StopTimeout, e o hook do HTTP usa ShutdownTimeout. Com
	// a ordem invertida, o servidor seria interrompido no meio da drenagem.
	if c.HTTP.StopTimeout < c.HTTP.ShutdownTimeout {
		problems = append(problems, "HTTP_STOP_TIMEOUT deve ser maior ou igual a HTTP_SHUTDOWN_TIMEOUT")
	}
	// O encerramento do consumidor e o do servidor acontecem DENTRO do mesmo
	// StopTimeout: se a soma dos dois não couber, um deles é abortado no meio.
	if c.SQS.ConsumeTransactions && c.HTTP.StopTimeout < c.HTTP.ShutdownTimeout+c.SQS.ShutdownGrace {
		problems = append(problems,
			"HTTP_STOP_TIMEOUT deve cobrir HTTP_SHUTDOWN_TIMEOUT + SQS_SHUTDOWN_GRACE quando SQS_CONSUME_TRANSACTIONS está ligado")
	}

	if c.SQS.BatchSize < 1 || c.SQS.BatchSize > 10 {
		problems = append(problems, "SQS_BATCH_SIZE deve estar entre 1 e 10 (limite do SQS)")
	}
	if c.SQS.ConsumerConcurrency < 1 {
		problems = append(problems, "SQS_CONSUMER_CONCURRENCY deve ser maior que zero")
	}
	if c.SQS.VisibilityTimeout < 5*time.Second {
		problems = append(problems, "SQS_VISIBILITY_TIMEOUT muito baixo: risco de reprocessamento concorrente da mesma mensagem")
	}
	if c.SQS.MaxReceiveCount < 1 {
		problems = append(problems, "SQS_MAX_RECEIVE_COUNT deve ser maior que zero")
	}
	if c.SQS.BaseBackoff < time.Second {
		problems = append(problems, "SQS_BASE_BACKOFF muito baixo: repetir rápido demais sobre um broker fora do ar só amplifica a falha")
	}
	if c.SQS.MaxBackoff < c.SQS.BaseBackoff {
		problems = append(problems, "SQS_MAX_BACKOFF deve ser maior ou igual a SQS_BASE_BACKOFF")
	}
	if c.SQS.ShutdownGrace <= 0 {
		problems = append(problems, "SQS_SHUTDOWN_GRACE deve ser maior que zero")
	}
	for _, q := range []struct{ name, value string }{
		{"SQS_TRANSACTIONS_QUEUE", c.SQS.TransactionsQueue},
		{"SQS_TRANSACTIONS_DLQ", c.SQS.TransactionsDLQ},
		{"SQS_EVENTS_QUEUE", c.SQS.EventsQueue},
		{"SQS_EVENTS_DLQ", c.SQS.EventsDLQ},
	} {
		if !strings.HasSuffix(q.value, ".fifo") {
			problems = append(problems, q.name+" deve terminar em .fifo (usamos FIFO para ordenação por carteira)")
		}
	}

	if c.Outbox.BatchSize < 1 {
		problems = append(problems, "OUTBOX_BATCH_SIZE deve ser maior que zero")
	}
	if c.Outbox.Lease < 5*time.Second {
		problems = append(problems, "OUTBOX_LEASE muito baixo: outra instância pode republicar um evento em andamento")
	}
	if c.Reference.MaxAttempts < 1 {
		problems = append(problems, "REFERENCE_MAX_ATTEMPTS deve ser maior que zero")
	}

	if _, err := c.SlogLevel(); err != nil {
		problems = append(problems, err.Error())
	}

	if len(problems) > 0 {
		return fmt.Errorf("config: configuração inválida:\n  - %s", strings.Join(problems, "\n  - "))
	}

	// Normalização: permite comparar o issuer com o claim "iss" sem depender de
	// barra final digitada na variável de ambiente.
	c.OIDC.Issuer = strings.TrimRight(c.OIDC.Issuer, "/")
	if c.OIDC.DiscoveryURL == "" {
		c.OIDC.DiscoveryURL = c.OIDC.Issuer
	}
	c.OIDC.DiscoveryURL = strings.TrimRight(c.OIDC.DiscoveryURL, "/")

	return nil
}

// SlogLevel converte LOG_LEVEL para slog.Level.
func (c *Config) SlogLevel() (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(c.LogLevel)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("LOG_LEVEL inválido: %q (use debug, info, warn ou error)", c.LogLevel)
	}
}

// IsLocal indica se estamos em ambiente de desenvolvimento local.
func (c *Config) IsLocal() bool {
	return strings.EqualFold(c.Env, "local") || strings.EqualFold(c.Env, "test")
}
