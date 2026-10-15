// Command api é o ponto de entrada da aplicação: API HTTP + consumidor SQS +
// workers de outbox e de referências, tudo composto com Uber Fx.
package main

import (
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
	if err := app.Run(version, commit); err != nil {
		fmt.Fprintf(os.Stderr, "erro fatal: %v\n", err)
		os.Exit(1)
	}
}
