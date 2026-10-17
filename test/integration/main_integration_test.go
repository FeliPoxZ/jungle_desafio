//go:build integration

// Package integration exercita o sistema inteiro contra PostgreSQL de verdade.
//
// # Por que o teste mora aqui, e não dentro de um pacote de produção
//
// Este é o único lugar que pode conhecer TODAS as camadas ao mesmo tempo:
// configuração, infraestrutura, caso de uso e domínio. Se o teste morasse em
// `internal/app/wagering`, a aplicação passaria a importar a infraestrutura (só
// no arquivo de teste, mas passaria); se morasse em `internal/infra/postgres`, a
// infraestrutura passaria a importar a aplicação. Um pacote de teste neutro
// mantém a direção `app → ports ← infra` verdadeira também fora da produção.
//
// # Por que aqui a infraestrutura é real
//
// O README §13 proíbe substituir PostgreSQL, SQS e IdP por mocks nos testes de
// integração. Aqui o banco é o do compose, o Unit of Work é o de produção e os
// repositórios são os de produção. O único dublê é o adaptador da outbox, e
// apenas no teste que precisa provar que uma falha no passo final desfaz tudo —
// nesse caso o que se substitui é o COLABORADOR que falha, não o banco que faz o
// rollback.
package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	// Import em branco: o driver `postgres` do migrate se registra no init do
	// próprio pacote. Sem ele, a abertura falha com "unknown driver postgres".
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
	"github.com/FeliPoxZ/jungle_desafio/internal/infra/postgres"
	"github.com/FeliPoxZ/jungle_desafio/internal/platform/clock"
	"github.com/FeliPoxZ/jungle_desafio/internal/platform/idgen"
	"github.com/FeliPoxZ/jungle_desafio/migrations"
)

// Depende do banco do docker-compose, e de um banco SEPARADO do de aplicação:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable
//	go test -count=1 -race -tags=integration -timeout 300s ./test/integration/
//
// Sem TEST_DATABASE_URL os testes são pulados, para `go test ./...` continuar
// verde em máquina sem Docker.
var (
	poolTeste *postgres.Pool
	uow       ports.UnitOfWork
	servico   *appwagering.Service
)

func TestMain(m *testing.M) {
	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if dsn == "" {
		fmt.Println("TEST_DATABASE_URL não definida: pulando testes de integração")
		os.Exit(0)
	}

	ctx := context.Background()

	cfg := &config.Config{
		Database: config.DatabaseConfig{
			MaxConns:         10,
			MinConns:         1,
			ConnMaxLifetime:  time.Minute,
			ConnMaxIdleTime:  time.Minute,
			ConnectTimeout:   5 * time.Second,
			StatementTimeout: 10 * time.Second,
			LockTimeout:      3 * time.Second,
		},
		DatabaseURL: dsn,
	}

	var err error
	poolTeste, err = postgres.NewPool(ctx, cfg)
	if err != nil {
		fmt.Printf("falha ao criar o pool de teste: %v\n", err)
		os.Exit(1)
	}
	defer poolTeste.Close()

	// O banco de teste é UM só, compartilhado com os testes de
	// `internal/infra/postgres`, e os dois truncam tabelas. Com `go test ./...` os
	// pacotes rodam em PROCESSOS paralelos: sem serializar, um apaga os dados do
	// outro no meio de um teste.
	if _, err := segurarBancoDeTeste(ctx, poolTeste); err != nil {
		fmt.Printf("falha ao segurar o banco de teste: %v\n", err)
		os.Exit(1)
	}

	if err := migrar(dsn); err != nil {
		fmt.Printf("falha ao aplicar migrations no banco de teste: %v\n", err)
		os.Exit(1)
	}

	uow = postgres.NewUnitOfWork(poolTeste)
	servico = appwagering.New(uow, clock.System{}, idgen.New())

	// O binário real é compilado UMA vez por execução da suíte (Fase 10): os
	// cenários multi-instância sobem N processos deste MESMO binário. Compilar
	// dentro de cada teste custaria segundos por cenário e permitiria que dois
	// testes da mesma execução rodassem builds diferentes.
	caminhoDoBinario, diretorioDoBinario, err := compilarBinarioDaSuite()
	if err != nil {
		fmt.Printf("falha ao compilar o binário da suíte: %v\n", err)
		os.Exit(1)
	}
	binarioDaSuite = caminhoDoBinario

	codigo := m.Run()

	// O diretório temporário com o binário é removido no fim da execução.
	if diretorioDoBinario != "" {
		_ = os.RemoveAll(diretorioDoBinario)
	}
	os.Exit(codigo)
}

// chaveLockIntegracao identifica "a suíte de integração do jungle" no espaço de
// locks consultivos. O valor é arbitrário; só precisa ser o MESMO nos pacotes
// que dividem o banco de teste.
const chaveLockIntegracao = int64(0x6a756e676c65) // "jungle" em hexadecimal

// segurarBancoDeTeste bloqueia até este processo ser o dono do banco de teste.
//
// O lock consultivo do PostgreSQL resolve a disputa sem depender de flag de
// linha de comando (`-p 1`): quem chega segundo espera o primeiro terminar. A
// conexão não é devolvida ao pool de propósito — lock consultivo pertence à
// SESSÃO, e devolvê-la poderia liberá-lo antes do fim da suíte.
//
// A espera é sem prazo (`lock_timeout = 0`): o pool configura `lock_timeout` de
// 3s para toda query da conexão, e isso vale também para a espera por lock
// consultivo — com o prazo ligado, o segundo processo morre com 55P03 antes de a
// outra suíte terminar.
func segurarBancoDeTeste(ctx context.Context, pool *postgres.Pool) (*pgxpool.Conn, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, `SET lock_timeout = 0`); err != nil {
		conn.Release()
		return nil, err
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, chaveLockIntegracao); err != nil {
		conn.Release()
		return nil, err
	}
	return conn, nil
}

// migrar aplica as migrations embutidas no banco de teste.
func migrar(dsn string) error {
	src, err := iofs.New(migrations.Source(), ".")
	if err != nil {
		return fmt.Errorf("abrindo migrations: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		return fmt.Errorf("criando migrator: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("aplicando migrations: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Fixtures e leitura do estado gravado
// ---------------------------------------------------------------------------

// limpar apaga o conteúdo das tabelas entre testes, respeitando as FKs.
func limpar(t *testing.T) {
	t.Helper()

	_, err := poolTeste.Exec(context.Background(),
		`TRUNCATE wallet_ledger_entries, outbox_events, inbox_messages, wager_transactions, wallets CASCADE`)
	if err != nil {
		t.Fatalf("falha ao limpar as tabelas: %v", err)
	}
}

// brl monta dinheiro em BRL a partir do texto do teste.
func brl(t *testing.T, valor string) money.Money {
	t.Helper()

	m, err := money.Parse(valor, "BRL")
	if err != nil {
		t.Fatalf("Parse(%q): %v", valor, err)
	}
	return m
}

// carteiraNova grava uma carteira com saldo e devolve a entidade.
func carteiraNova(t *testing.T, saldo string) *wallet.Wallet {
	t.Helper()

	w, err := wallet.NewWallet(
		ids.WalletID(uuid.NewString()),
		ids.PlayerID(uuid.NewString()),
		brl(t, saldo),
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("NewWallet: %v", err)
	}

	if err := uow.Run(context.Background(), func(ctx context.Context, r ports.Repos) error {
		return r.Wallets.Insert(ctx, w)
	}); err != nil {
		t.Fatalf("insert da carteira: %v", err)
	}
	return w
}

// pedido monta a operação como a borda HTTP montaria.
type pedidoOpt func(*appwagering.Request)

func comChave(chave string) pedidoOpt {
	return func(r *appwagering.Request) { r.IdempotencyKey = chave }
}

func comExterna(externa string) pedidoOpt {
	return func(r *appwagering.Request) { r.ExternalTransactionID = externa }
}

func comReferencia(externa string) pedidoOpt {
	return func(r *appwagering.Request) { r.ReferenceExternalTxnID = externa }
}

// postgresUOW devolve um Unit of Work NOVO sobre o mesmo pool: é o que simula
// outro processo, sem estado em memória compartilhado.
func postgresUOW(t *testing.T) ports.UnitOfWork {
	t.Helper()
	return postgres.NewUnitOfWork(poolTeste)
}

// brlBruto é o mesmo brl, para uso dentro das opções de pedido (sem *testing.T).
func brlBruto(valor string) money.Money {
	m, err := money.Parse(valor, "BRL")
	if err != nil {
		panic(err)
	}
	return m
}

func pedido(w *wallet.Wallet, kind wagerdomain.Kind, valor string, opts ...pedidoOpt) appwagering.Request {
	req := appwagering.Request{
		ProviderID:            "provider-a",
		ExternalTransactionID: "ext-" + kind.String(),
		IdempotencyKey:        "key-" + kind.String(),
		PlayerID:              w.PlayerID(),
		WalletID:              w.ID(),
		RoundID:               "round-1",
		GameID:                "fortune-chimp",
		Kind:                  kind,
		Amount:                brlBruto(valor),
	}
	for _, opt := range opts {
		opt(&req)
	}
	return req
}

// saldoNoBanco lê o saldo direto do PostgreSQL, sem passar pelo domínio.
func saldoNoBanco(t *testing.T, id ids.WalletID) int64 {
	t.Helper()

	var saldo int64
	err := poolTeste.QueryRow(context.Background(),
		`SELECT balance_minor FROM wallets WHERE id = $1`, string(id)).Scan(&saldo)
	if err != nil {
		t.Fatalf("lendo o saldo: %v", err)
	}
	return saldo
}

// versaoNoBanco lê a versão da carteira.
func versaoNoBanco(t *testing.T, id ids.WalletID) int64 {
	t.Helper()

	var versao int64
	err := poolTeste.QueryRow(context.Background(),
		`SELECT version FROM wallets WHERE id = $1`, string(id)).Scan(&versao)
	if err != nil {
		t.Fatalf("lendo a versão: %v", err)
	}
	return versao
}

// lancamentosNoBanco devolve "DIREÇÃO valor" de cada lançamento, em ordem.
func lancamentosNoBanco(t *testing.T, id ids.WalletID) []string {
	t.Helper()

	linhas, err := poolTeste.Query(context.Background(),
		`SELECT direction, amount_minor FROM wallet_ledger_entries
		 WHERE wallet_id = $1 ORDER BY created_at, id`, string(id))
	if err != nil {
		t.Fatalf("lendo o ledger: %v", err)
	}
	defer linhas.Close()

	var saida []string
	for linhas.Next() {
		var (
			direcao string
			valor   int64
		)
		if err := linhas.Scan(&direcao, &valor); err != nil {
			t.Fatalf("lendo linha do ledger: %v", err)
		}
		saida = append(saida, fmt.Sprintf("%s %s", direcao, money.FromMinor(valor, money.BRL)))
	}
	return saida
}

// eventoNaOutbox é o que interessa conferir de um evento gravado.
type eventoNaOutbox struct {
	Tipo       string
	Agregado   string
	Payload    string
	Publicado  bool
	OcorridoEm time.Time
}

func eventosNaOutbox(t *testing.T) []eventoNaOutbox {
	t.Helper()

	// Lê direto da tabela, e NÃO pelo ClaimBatch: reivindicar aplicaria lease e
	// mudaria o estado que este teste quer observar.
	linhas, err := poolTeste.Query(context.Background(),
		`SELECT event_type, aggregate_id, payload::text, published_at IS NOT NULL
		 FROM outbox_events ORDER BY occurred_at, event_id`)
	if err != nil {
		t.Fatalf("lendo a outbox: %v", err)
	}
	defer linhas.Close()

	var saida []eventoNaOutbox
	for linhas.Next() {
		var e eventoNaOutbox
		if err := linhas.Scan(&e.Tipo, &e.Agregado, &e.Payload, &e.Publicado); err != nil {
			t.Fatalf("lendo evento: %v", err)
		}
		saida = append(saida, e)
	}
	return saida
}

func tiposDeEvento(t *testing.T) []string {
	t.Helper()

	var saida []string
	for _, e := range eventosNaOutbox(t) {
		saida = append(saida, e.Tipo)
	}
	return saida
}

// transacaoNoBanco devolve status, failure_code e a referência interna.
func transacaoNoBanco(t *testing.T, providerID, chave string) (status, failureCode, referencia string, resultado int64) {
	t.Helper()

	var (
		codigo     *string
		ref        *string
		saldoFinal *int64
	)
	err := poolTeste.QueryRow(context.Background(),
		`SELECT status, failure_code, reference_transaction_id, result_balance_minor
		 FROM wager_transactions WHERE provider_id = $1 AND idempotency_key = $2`,
		providerID, chave).Scan(&status, &codigo, &ref, &saldoFinal)
	if err != nil {
		t.Fatalf("lendo a transação: %v", err)
	}

	if codigo != nil {
		failureCode = *codigo
	}
	if ref != nil {
		referencia = *ref
	}
	if saldoFinal != nil {
		resultado = *saldoFinal
	}
	return status, failureCode, referencia, resultado
}

func contarTransacoes(t *testing.T) int {
	t.Helper()

	var total int
	if err := poolTeste.QueryRow(context.Background(),
		`SELECT count(*) FROM wager_transactions`).Scan(&total); err != nil {
		t.Fatalf("contando transações: %v", err)
	}
	return total
}

// ---------------------------------------------------------------------------
// Unit of Work com a outbox quebrada
// ---------------------------------------------------------------------------

// uowComOutboxQuebrada abre a transação REAL e troca só o adaptador da outbox.
//
// É o que permite provar atomicidade de verdade: a carteira, o ledger e a
// transação são os de produção, dentro da transação do PostgreSQL; o que falha é
// o último passo. Se o rollback não acontecesse, o saldo e o lançamento
// ficariam.
type uowComOutboxQuebrada struct{ interno ports.UnitOfWork }

func (u uowComOutboxQuebrada) Run(ctx context.Context, fn func(context.Context, ports.Repos) error) error {
	return u.interno.Run(ctx, func(ctx context.Context, r ports.Repos) error {
		r.Outbox = outboxQueFalha{}
		return fn(ctx, r)
	})
}

func (u uowComOutboxQuebrada) RunReadOnly(ctx context.Context, fn func(context.Context, ports.Repos) error) error {
	return u.interno.RunReadOnly(ctx, fn)
}

type outboxQueFalha struct{}

func (outboxQueFalha) Insert(context.Context, ports.OutboxEvent) error {
	return errors.New("outbox indisponível")
}

func (outboxQueFalha) ClaimBatch(context.Context, int, time.Duration, time.Time) ([]ports.OutboxEvent, error) {
	return nil, errors.New("outbox indisponível")
}

func (outboxQueFalha) MarkPublished(context.Context, string, time.Time) error {
	return errors.New("outbox indisponível")
}

func (outboxQueFalha) MarkRetry(context.Context, string, time.Time, string) error {
	return errors.New("outbox indisponível")
}

func (outboxQueFalha) PendingStats(context.Context, time.Time) (int, time.Duration, error) {
	return 0, 0, errors.New("outbox indisponível")
}
