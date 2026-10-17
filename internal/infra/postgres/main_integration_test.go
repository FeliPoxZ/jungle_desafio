//go:build integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FeliPoxZ/jungle_desafio/internal/config"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
	"github.com/FeliPoxZ/jungle_desafio/internal/platform/canonicaljson"
	"github.com/FeliPoxZ/jungle_desafio/migrations"
)

// Testes de integração contra um PostgreSQL de verdade.
//
// Dependem do banco do docker-compose:
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL=postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable \
//	  go test -tags=integration ./internal/infra/postgres/
//
// Sem TEST_DATABASE_URL, os testes são pulados — para `go test ./...` continuar
// verde em máquina sem Docker.
//
// Usa um banco SEPARADO (jungle_test) porque os testes truncam tabelas: apontar
// para o banco da aplicação apagaria dados de verdade.

var poolTeste *Pool

// TestMain sobe o pool, toma o banco de teste só para esta suíte, aplica as
// migrations e limpa ao final.
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
	poolTeste, err = NewPool(ctx, cfg)
	if err != nil {
		fmt.Printf("falha ao criar o pool de teste: %v\n", err)
		os.Exit(1)
	}
	defer poolTeste.Close()

	// O banco de teste é UM só, e mais de um pacote de integração trunca as
	// tabelas. Com `go test ./...` os pacotes rodam em PROCESSOS paralelos, então
	// sem serializar um apaga os dados do outro no meio de um teste — o sintoma é
	// "registro não encontrado" e deadlock no TRUNCATE.
	segurar, err := segurarBancoDeTeste(ctx, poolTeste)
	if err != nil {
		fmt.Printf("falha ao segurar o banco de teste: %v\n", err)
		os.Exit(1)
	}
	// A conexão NÃO é devolvida ao pool: lock consultivo pertence à sessão, e
	// devolvê-la poderia liberá-lo. O fim do processo libera de qualquer forma.

	if err := migrar(dsn); err != nil {
		fmt.Printf("falha ao aplicar migrations no banco de teste: %v\n", err)
		os.Exit(1)
	}

	_ = segurar
	os.Exit(m.Run())
}

// chaveLockIntegracao identifica "a suíte de integração do jungle" no espaço de
// locks consultivos. O valor é arbitrário; só precisa ser o MESMO nos pacotes.
const chaveLockIntegracao = int64(0x6a756e676c65) // "jungle" em hexadecimal

// segurarBancoDeTeste bloqueia até este processo ser o dono do banco de teste.
//
// O lock consultivo do PostgreSQL resolve a disputa sem depender de flag de
// linha de comando (`-p 1`): quem chega segundo espera o primeiro terminar.
//
// A espera é sem prazo (`lock_timeout = 0`) de propósito: o pool configura
// `lock_timeout` de 3s para toda query da conexão, e isso vale também para a
// espera por lock consultivo — com o prazo ligado, o segundo processo morre com
// 55P03 antes de a outra suíte terminar.
func segurarBancoDeTeste(ctx context.Context, pool *Pool) (*pgxpool.Conn, error) {
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

var _ = migratepg.Postgres{}
var _ = pgxpool.Config{}

// ---------------------------------------------------------------------------
// Auxiliares
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

func brl(t *testing.T, valor string) money.Money {
	t.Helper()
	m, err := money.Parse(valor, "BRL")
	if err != nil {
		t.Fatalf("Parse(%q): %v", valor, err)
	}
	return m
}

// carteiraNova grava uma carteira de teste e devolve a entidade.
func carteiraNova(t *testing.T, playerID string, saldo string) *wallet.Wallet {
	t.Helper()

	w, err := wallet.NewWallet(
		ids.WalletID(uuid.NewString()),
		ids.PlayerID(playerID),
		brl(t, saldo),
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("NewWallet: %v", err)
	}

	uow := NewUnitOfWork(poolTeste)
	err = uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		return r.Wallets.Insert(ctx, w)
	})
	if err != nil {
		t.Fatalf("insert da carteira: %v", err)
	}
	return w
}

// hashDe calcula o hash do payload como o serviço faz, via JSON canônico.
func hashDe(t *testing.T, v any) wagering.Hash {
	t.Helper()
	h, err := canonicaljson.Hash(v)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return wagering.Hash(h)
}

// encodeCanonico é o PayloadEncoder injetado nos testes: o mesmo que a
// aplicação usa (JSON canônico), para o hash ser comparável.
func encodeCanonico(v any) ([]byte, error) { return canonicaljson.Marshal(v) }

// txnNova monta uma transação de teste.
func txnNova(t *testing.T, w *wallet.Wallet, kind wagering.Kind, valor string) *wagering.WagerTransaction {
	t.Helper()

	tx, err := wagering.New(
		wagering.TransactionID(uuid.NewString()),
		w.ID(),
		w.PlayerID(),
		kind,
		brl(t, valor),
		"game-1", "round-1",
		"key-"+uuid.NewString(),
		"provider-a", "ext-"+uuid.NewString(), "",
		"corr-teste",
		map[string]any{"amount": valor, "currency": "BRL"},
		encodeCanonico,
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("wagering.New: %v", err)
	}
	return tx
}
