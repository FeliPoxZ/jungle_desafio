package postgres

import (
	"context"
	"time"
)

// Checker verifica se o PostgreSQL responde, para o `/health/ready`.
//
// Ele satisfaz a interface `httpapi.HealthChecker` SEM importar `httpapi`: em Go
// a interface é satisfeita por estrutura, e manter este pacote sem conhecer a
// camada HTTP é o que preserva a direção das dependências do ARCHITECTURE §1.
type Checker struct {
	pool *Pool
	// prazo por verificação: o readiness tem prazo próprio, mas um ping que
	// trave não pode consumir o orçamento inteiro dos outros checkers.
	prazo time.Duration
}

// NewHealthChecker monta o verificador do banco.
func NewHealthChecker(pool *Pool) *Checker {
	return &Checker{pool: pool, prazo: 2 * time.Second}
}

// Name identifica a dependência no relatório do readiness.
func (c *Checker) Name() string { return "postgres" }

// CheckReadiness faz um ping no banco.
//
// `Ping` e não uma query de negócio: o que o readiness responde é "consigo abrir
// conexão e falar com o servidor". Uma tabela específica poderia estar ausente
// por migration não aplicada, e isso é problema de boot, não de readiness.
func (c *Checker) CheckReadiness(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.prazo)
	defer cancel()
	return c.pool.Ping(ctx)
}
