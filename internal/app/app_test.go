package app_test

import (
	"strings"
	"testing"

	"go.uber.org/fx"

	"github.com/FeliPoxZ/jungle_desafio/internal/app"
)

// setValidEnv define as variáveis obrigatórias para uma configuração válida.
func setValidEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://jungleg:jungleg@localhost:5432/jungleg?sslmode=disable")
	t.Setenv("OIDC_ISSUER", "http://localhost:8080/realms/jungleg")
}

// TestValidateAppComposition valida o GRAFO de dependências do Fx.
//
// O que este teste pega SEM precisar de banco, fila ou IdP:
//   - construtor com dependência que ninguém fornece;
//   - dependência cíclica;
//   - tipo fornecido/consumido de forma incompatível;
//   - fx.Invoke com assinatura inválida.
//
// Importante entender a diferença: fx.ValidateApp NÃO executa os construtores —
// ele só verifica se o grafo fecha. Erro dentro de um construtor (como
// configuração inválida) só aparece na construção real, coberta pelo teste
// abaixo.
func TestValidateAppComposition(t *testing.T) {
	setValidEnv(t)

	if err := app.ValidateApp(); err != nil {
		t.Fatalf("grafo do Fx inválido: %v", err)
	}
}

// TestBuildAppFailsWithInvalidConfig garante que configuração inválida impede a
// CONSTRUÇÃO da aplicação, e não apenas o start.
//
// fx.New executa todos os providers durante a montagem do grafo, então o erro
// de config.Load aflora aqui. É exatamente o comportamento desejado: a aplicação
// falha antes de abrir qualquer conexão, com o processo nem chegando a subir.
func TestBuildAppFailsWithInvalidConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("OIDC_ISSUER", "")

	application := fx.New(
		app.ModuleOptions(),
		fx.NopLogger,
	)

	err := application.Err()
	if err == nil {
		// Se não houver erro acumulado, encerra o app (nenhum hook foi iniciado).
		_ = application.Stop(t.Context())
		t.Fatal("a construção da aplicação deveria falhar com configuração inválida")
	}

	if !strings.Contains(err.Error(), "DATABASE_URL") && !strings.Contains(err.Error(), "OIDC_ISSUER") {
		t.Errorf("o erro deveria apontar a variável faltante; recebido: %v", err)
	}
}
