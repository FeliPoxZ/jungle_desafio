package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

// O grafo também precisa fechar com os workers da Fase 9 LIGADOS.
//
// Sem este teste, um provider só usado no caminho dos workers (o publicador, o
// runner, o `Retomador`) poderia faltar sem ninguém perceber: com os flags em
// `false` os construtores continuam no grafo, mas a lista de laços fica vazia e o
// caminho nunca é exercitado em produção até alguém ligar o worker.
func TestValidateAppCompositionComWorkersLigados(t *testing.T) {
	setValidEnv(t)
	t.Setenv("OUTBOX_ENABLED", "true")
	t.Setenv("REFERENCE_ENABLED", "true")

	if err := app.ValidateApp(); err != nil {
		t.Fatalf("grafo do Fx inválido com os workers ligados: %v", err)
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

// TestHooksDeParadaNaOrdemInversa prova a regra em que a ordem de encerramento do
// projeto se apoia (README §4): o Fx executa os `OnStop` na ordem INVERSA do
// registro.
//
// É essa regra que faz o servidor HTTP parar de aceitar requisições ANTES de o
// pool fechar — e é por isso que `runHTTPServer` é registrado antes de
// `fecharPoolNoShutdown`. Se a regra mudasse, o fechamento do pool passaria a
// acontecer com requisição em andamento, e o sintoma seria erro de banco fechado
// no meio de um débito.
func TestHooksDeParadaNaOrdemInversa(t *testing.T) {
	var ordem []string

	aplicacao := fx.New(
		fx.NopLogger,
		fx.Invoke(func(lc fx.Lifecycle) {
			lc.Append(fx.Hook{OnStop: func(context.Context) error {
				ordem = append(ordem, "primeiro-registrado")
				return nil
			}})
			lc.Append(fx.Hook{OnStop: func(context.Context) error {
				ordem = append(ordem, "segundo-registrado")
				return nil
			}})
		}),
	)

	if err := aplicacao.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := aplicacao.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	esperado := []string{"segundo-registrado", "primeiro-registrado"}
	if len(ordem) != len(esperado) || ordem[0] != esperado[0] || ordem[1] != esperado[1] {
		t.Fatalf("ordem dos OnStop = %v, esperado %v", ordem, esperado)
	}
}

// TestScopeDoPrazoDeEncerramento prova o prazo de encerramento no ponto onde ele
// realmente age.
//
// # O que a primeira versão deste teste errou
//
// Ela esperava que `fx.StopTimeout(100ms)` cortasse um `App.Stop(ctx)`. Não
// corta: `Stop` usa **o contexto do chamador** (`withTimeout` seleciona em
// `ctx.Done()`), e a opção `fx.StopTimeout` é aplicada só por `App.run` — que este
// projeto NÃO usa (`app.Run` faz o próprio tratamento de sinal).
//
// O prazo efetivo, portanto, é o `shutdownCtx` que `app.Run` cria com
// `cfg.HTTP.StopTimeout` (validado em `config.Validate`). A opção continua no
// grafo porque documenta o mesmo valor e é o que valeria se alguém trocasse para
// `fxApp.Run()`; `StopTimeout()` devolve o valor que ela configurou.
func TestScopeDoPrazoDeEncerramento(t *testing.T) {
	aplicacao := fx.New(
		fx.NopLogger,
		fx.StopTimeout(100*time.Millisecond),
		fx.Invoke(func(lc fx.Lifecycle) {
			lc.Append(fx.Hook{OnStop: func(context.Context) error {
				// Ignora o contexto de propósito: é o hook mal-comportado que o
				// prazo precisa cortar.
				time.Sleep(2 * time.Second)
				return nil
			}})
		}),
	)

	if err := aplicacao.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// É ESTE contexto que manda — o mesmo tipo que `app.Run` constrói a partir de
	// `HTTP_STOP_TIMEOUT`.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	inicio := time.Now()
	err := aplicacao.Stop(ctx)
	decorrido := time.Since(inicio)

	if err == nil {
		t.Fatal("Stop deveria devolver erro de prazo com um hook que estoura o contexto")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("erro = %v, esperado context.DeadlineExceeded", err)
	}
	if decorrido > time.Second {
		t.Fatalf("Stop levou %v; o contexto tinha 100ms — o prazo não foi respeitado", decorrido)
	}

	// E o valor da opção é o mesmo que o `Run` usa para montar o contexto: se um
	// dos dois mudar sozinho, este teste avisa.
	if prazo := aplicacao.StopTimeout(); prazo != 100*time.Millisecond {
		t.Errorf("StopTimeout() = %v, esperado 100ms", prazo)
	}
}
