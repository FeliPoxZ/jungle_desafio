// Comando migrate aplica e reverte as migrations do banco.
//
// Uso:
//
//	migrate up              aplica todas as migrations pendentes
//	migrate down [n]        reverte n migrations (sem n, reverte todas)
//	migrate version         mostra a versão atual do schema
//
// A URL do banco vem de DATABASE_URL (mesma variável da aplicação).
//
// Códigos de saída, no mesmo contrato do CLI oficial do golang-migrate:
//
//	0  operação aplicou mudança
//	1  nada a fazer (schema já atualizado ou sem migrations para reverter)
//	2  erro
package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	// Import anônimo de propósito: o driver se registra via init() e é ele que
	// faz a URL "postgres://..." ser reconhecida pelo migrate.
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/FeliPoxZ/jungle_desafio/migrations"
)

const (
	exitOK      = 0
	exitNoop    = 1
	exitFailure = 2

	usage = `uso: migrate <comando>

comandos:
  up              aplica todas as migrations pendentes
  down [n]        reverte n migrations (sem n, reverte todas)
  version         mostra a versão atual do schema
`
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	os.Exit(run(logger, os.Args[1:]))
}

func run(logger *slog.Logger, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return exitFailure
	}

	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		logger.Error("DATABASE_URL não está definida")
		return exitFailure
	}

	m, err := newMigrator(databaseURL)
	if err != nil {
		logger.Error("não foi possível preparar as migrations", "erro", err)
		return exitFailure
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			logger.Warn("erro ao fechar a fonte", "erro", srcErr)
		}
		if dbErr != nil {
			logger.Warn("erro ao fechar o banco", "erro", dbErr)
		}
	}()

	switch args[0] {
	case "up":
		return cmdUp(logger, m)
	case "down":
		return cmdDown(logger, m, args[1:])
	case "version":
		return cmdVersion(logger, m)
	default:
		fmt.Fprint(os.Stderr, usage)
		return exitFailure
	}
}

// newMigrator monta o migrator a partir dos SQL embutidos e da URL do banco.
func newMigrator(databaseURL string) (*migrate.Migrate, error) {
	src, err := iofs.New(migrations.Source(), ".")
	if err != nil {
		return nil, fmt.Errorf("abrindo as migrations embutidas: %w", err)
	}
	return migrate.NewWithSourceInstance("iofs", src, databaseURL)
}

func cmdUp(logger *slog.Logger, m *migrate.Migrate) int {
	err := m.Up()
	switch {
	case errors.Is(err, migrate.ErrNoChange):
		logger.Info("schema já está atualizado, nada a aplicar")
		return exitNoop
	case err != nil:
		logger.Error("falha ao aplicar as migrations", "erro", err)
		return exitFailure
	}

	version, dirty, verr := m.Version()
	if verr != nil {
		logger.Warn("migrations aplicadas, mas não foi possível ler a versão", "erro", verr)
		return exitOK
	}
	logger.Info("migrations aplicadas", "versao", version, "dirty", dirty)
	return exitOK
}

func cmdDown(logger *slog.Logger, m *migrate.Migrate, args []string) int {
	var err error

	if len(args) == 0 {
		// Sem argumento: reverte tudo.
		err = m.Down()
	} else {
		n, convErr := strconv.Atoi(args[0])
		if convErr != nil || n < 1 {
			logger.Error("número de migrations a reverter precisa ser um inteiro >= 1", "valor", args[0])
			return exitFailure
		}
		// Steps com valor negativo reverte n migrations.
		err = m.Steps(-n)
	}

	switch {
	case errors.Is(err, migrate.ErrNoChange):
		logger.Info("nada para reverter")
		return exitNoop
	case err != nil:
		logger.Error("falha ao reverter as migrations", "erro", err)
		return exitFailure
	}

	version, dirty, verr := m.Version()
	if verr != nil {
		logger.Info("migrations revertidas; schema sem versão (nenhuma migration aplicada)")
		return exitOK
	}
	logger.Info("migrations revertidas", "versao", version, "dirty", dirty)
	return exitOK
}

func cmdVersion(logger *slog.Logger, m *migrate.Migrate) int {
	version, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		logger.Info("nenhuma migration aplicada")
		return exitNoop
	}
	if err != nil {
		logger.Error("falha ao ler a versão do schema", "erro", err)
		return exitFailure
	}

	logger.Info("versão do schema", "versao", version, "dirty", dirty)
	fmt.Printf("%d\n", version)
	return exitOK
}
