//go:build integration

package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/config"
)

// TestApplicationNameVemDoDSNQuandoDefinido trava a correção do defeito 40.
//
// # O sintoma que este teste impede de voltar
//
// O `NewPool` definia `application_name = "jungleg-api"` DEPOIS de interpretar o
// DSN, sobrescrevendo qualquer valor que viesse de lá. Com um processo só, isso
// nunca incomodou. Na prova de três instâncias (README §8) ficou evidente: o
// `pg_stat_activity` mostrava três pools indistinguíveis — todas as conexões com
// o mesmo nome —, e não havia como atribuir uma conexão, um lock ou uma query
// culpada à instância que a produziu.
//
// O contrato agora é: o DSN manda; sem DSN, o default continua sendo o de antes.
func TestApplicationNameVemDoDSNQuandoDefinido(t *testing.T) {
	const nome = "jungle-teste-fase10"

	pool := poolComDSN(t, dsnComParametro(t, dsnDeTeste(t), "application_name", nome))
	defer pool.Close()

	if obtido := applicationNameDaConexao(t, pool); obtido != nome {
		t.Fatalf("application_name = %q, esperado %q (o DSN tem de vencer o default)", obtido, nome)
	}
}

// TestApplicationNamePadraoSemDSN garante que a correção não mudou o
// comportamento de quem NÃO informa o parâmetro: o nome padrão continua o mesmo,
// que é o que as Fases 5–9 já viam no banco.
func TestApplicationNamePadraoSemDSN(t *testing.T) {
	pool := poolComDSN(t, dsnDeTeste(t))
	defer pool.Close()

	if obtido := applicationNameDaConexao(t, pool); obtido != "jungleg-api" {
		t.Fatalf("application_name = %q, esperado o default %q", obtido, "jungleg-api")
	}
}

// poolComDSN cria um pool de verdade com o DSN informado.
func poolComDSN(t *testing.T, dsn string) *Pool {
	t.Helper()

	cfg := &config.Config{
		Database: config.DatabaseConfig{
			MaxConns:         2,
			MinConns:         1,
			ConnMaxLifetime:  time.Minute,
			ConnMaxIdleTime:  time.Minute,
			ConnectTimeout:   5 * time.Second,
			StatementTimeout: 5 * time.Second,
			LockTimeout:      3 * time.Second,
		},
		DatabaseURL: dsn,
	}

	pool, err := NewPool(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	return pool
}

// applicationNameDaConexao pergunta ao SERVIDOR qual é o application_name da
// sessão — é a única leitura que prova o que de fato chegou no startup packet.
func applicationNameDaConexao(t *testing.T, pool *Pool) string {
	t.Helper()

	var nome string
	if err := pool.QueryRow(context.Background(),
		`SELECT current_setting('application_name')`).Scan(&nome); err != nil {
		t.Fatalf("lendo application_name: %v", err)
	}
	return nome
}

// dsnDeTeste é o DSN do banco de teste (o TestMain já garantiu que existe).
func dsnDeTeste(t *testing.T) string {
	t.Helper()

	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL não definida: pulando teste que precisa do PostgreSQL")
	}
	return dsn
}

// dsnComParametro acrescenta um parâmetro à string de conexão.
func dsnComParametro(t *testing.T, dsn, chave, valor string) string {
	t.Helper()

	separador := "?"
	if strings.Contains(dsn, "?") {
		separador = "&"
	}
	return dsn + separador + chave + "=" + valor
}
