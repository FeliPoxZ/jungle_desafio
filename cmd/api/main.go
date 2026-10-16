// Command api é o ponto de entrada da aplicação: API HTTP + consumidor SQS +
// workers de outbox e de referências, tudo composto com Uber Fx.
//
// Ele também implementa o modo `-health-check`, usado pelo healthcheck do
// Docker Compose: o mesmo binário, sem subir nada, consulta o próprio readiness
// e devolve 0 (pronto) ou 1 (não pronto). Isso evita instalar `curl` na imagem
// final só para o healthcheck.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/FeliPoxZ/jungle_desafio/internal/app"
)

// version e commit são injetados no build via -ldflags (ver Dockerfile/Makefile).
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	var (
		healthCheck = flag.Bool("health-check", false,
			"consulta /health/ready no próprio processo e sai com 0 (pronto) ou 1 (não pronto)")
		healthURL = flag.String("health-check-url", "",
			"URL consultada pelo modo -health-check (padrão: derivada de HTTP_ADDR)")
		showVersion = flag.Bool("version", false, "mostra a versão e sai")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("%s (%s)\n", version, commit)
		return
	}

	if *healthCheck {
		url := *healthURL
		if url == "" {
			// `HTTP_ADDR` é a mesma variável que a aplicação usa para escutar:
			// derivando a URL dela, o healthcheck continua correto se a porta
			// mudar no compose.
			url = app.HealthCheckURL(os.Getenv("HTTP_ADDR"))
		}
		if err := app.RunHealthCheck(context.Background(), url); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	if err := app.Run(version, commit); err != nil {
		fmt.Fprintf(os.Stderr, "erro fatal: %v\n", err)
		os.Exit(1)
	}
}
