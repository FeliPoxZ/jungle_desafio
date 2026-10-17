package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FeliPoxZ/jungle_desafio/internal/config"
)

// Pool é o pool de conexões da aplicação.
type Pool struct {
	*pgxpool.Pool
}

// NewPool cria o pool com os limites e timeouts da configuração.
//
// # Por que os timeouts são definidos no pool e não em cada query
//
// statement_timeout e lock_timeout são do SERVIDOR e valem para toda query da
// conexão. Definir aqui garante que nenhuma query fique presa para sempre — nem
// uma que alguém escreva no futuro esquecendo o contexto.
//
// O lock_timeout importa especialmente para nós: com SELECT ... FOR UPDATE na
// carteira, uma disputa longa precisa falhar rápido (e virar ErrTransient, para
// retry) em vez de segurar conexão do pool.
func NewPool(ctx context.Context, cfg *config.Config) (*Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("postgres: DSN inválida: %w", err)
	}

	poolCfg.MaxConns = cfg.Database.MaxConns
	poolCfg.MinConns = cfg.Database.MinConns
	poolCfg.MaxConnLifetime = cfg.Database.ConnMaxLifetime
	poolCfg.MaxConnIdleTime = cfg.Database.ConnMaxIdleTime
	poolCfg.ConnConfig.ConnectTimeout = cfg.Database.ConnectTimeout

	poolCfg.ConnConfig.RuntimeParams["statement_timeout"] = fmt.Sprintf("%d", cfg.Database.StatementTimeout.Milliseconds())
	poolCfg.ConnConfig.RuntimeParams["lock_timeout"] = fmt.Sprintf("%d", cfg.Database.LockTimeout.Milliseconds())
	// Instante sempre em UTC, independente do servidor.
	poolCfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	// Aparece no pg_stat_activity: ajuda a achar a query culpada num incidente.
	//
	// O DSN VENCE o default quando traz `application_name` (Fase 10). Antes desta
	// correção o valor era sobrescrito sempre, e três instâncias no mesmo banco
	// apareciam TODAS como "jungleg-api": não havia como dizer qual processo
	// segurava o lock de uma carteira, nem atribuir uma query culpada à
	// instância que a emitiu. Com uma instância o problema era invisível; a
	// prova multi-instância do README §8 o expôs na primeira execução.
	if _, definidoPeloDSN := poolCfg.ConnConfig.RuntimeParams["application_name"]; !definidoPeloDSN {
		poolCfg.ConnConfig.RuntimeParams["application_name"] = "jungleg-api"
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: criando o pool: %w", err)
	}

	return &Pool{Pool: pool}, nil
}

// Ping verifica a conectividade, usado pelo readiness.
func (p *Pool) Ping(ctx context.Context) error {
	if err := p.Pool.Ping(ctx); err != nil {
		return fmt.Errorf("postgres: ping: %w", err)
	}
	return nil
}

// Close encerra o pool.
//
// Existe como método de `*Pool` (e não como chamada direta a `p.Pool.Close()` na
// composição) para o `fx.Lifecycle` registrar o fechamento no tipo que ele
// conhece, e para ficar claro no grafo que ESTE recurso tem ciclo de vida.
func (p *Pool) Close() { p.Pool.Close() }

// Stats devolve as estatísticas do pool, para métricas.
func (p *Pool) Stats() *pgxpool.Stat { return p.Pool.Stat() }

// querier é o mínimo que um repositório precisa para executar SQL.
//
// É satisfeito tanto por *pgxpool.Pool (fora de transação) quanto por pgx.Tx
// (dentro da Unit of Work). Esta interface pequena é o que permite o MESMO
// repositório funcionar nos dois cenários, sem duplicar consulta.
//
// A interface fica aqui (e não no pacote de domínio) porque ela descreve um
// detalhe do driver, não um conceito de negócio.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Verificação em tempo de compilação: os dois tipos satisfazem a interface.
var (
	_ querier = (*pgxpool.Pool)(nil)
	_ querier = (pgx.Tx)(nil)
)
