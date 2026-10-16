# =============================================================================
# Makefile — atalhos do projeto. Funciona em Windows (PowerShell) e Linux/macOS.
# No Windows, rode a partir de um terminal com "make" no PATH (ex.: via msys64/git
# bash) ou use diretamente os comandos equivalentes documentados no README.md.
# =============================================================================

SHELL := /bin/sh

# O cache global de módulos do Go não é gravável nesta máquina, então o projeto
# usa um cache isolado dentro do workspace (ignorado pelo git).
GOENV := GOMODCACHE=$(CURDIR)/.gocache/mod GOCACHE=$(CURDIR)/.gocache/build GOPATH=$(CURDIR)/.gocache/gopath
GO    := $(GOENV) go

# .env é opcional: se existir, suas variáveis ficam exportadas para os targets.
ifneq (,$(wildcard ./.env))
include .env
export
endif

DATABASE_URL ?= postgres://jungleg:jungleg@localhost:5432/jungleg?sslmode=disable
POSTGRES_USER ?= jungleg
POSTGRES_DB   ?= jungleg

# --- Detecção de SO (só para o sufixo do binário) ----------------------------
ifeq ($(OS),Windows_NT)
BIN_EXT := .exe
else
BIN_EXT :=
endif

.PHONY: help
help: ## Mostra esta ajuda
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

# --- Infraestrutura ----------------------------------------------------------

.PHONY: up
up: ## Sobe Postgres, Keycloak e LocalStack em background
	docker compose up -d postgres keycloak localstack

.PHONY: up-all
up-all: ## Sobe tudo (infra + migrations + api) com build
	docker compose up -d --build

.PHONY: init-queues
init-queues: ## Cria as filas FIFO e as DLQs no LocalStack
	docker compose run --rm localstack-init

.PHONY: down
down: ## Para e remove os containers (mantém os volumes de dados)
	docker compose down --remove-orphans

.PHONY: clean
clean: ## Para tudo e apaga os volumes (perde os dados locais)
	docker compose down --remove-orphans --volumes

.PHONY: logs
logs: ## Acompanha os logs de todos os serviços
	docker compose logs -f --tail=100

.PHONY: ps
ps: ## Estado dos containers
	docker compose ps

# --- Banco de dados ----------------------------------------------------------

.PHONY: migrate-build
migrate-build: ## Compila o binário de migrations em ./bin
	$(GO) build -o bin/migrate$(BIN_EXT) ./cmd/migrate

.PHONY: migrate-up
migrate-up: migrate-build ## Aplica todas as migrations
	DATABASE_URL="$(DATABASE_URL)" ./bin/migrate$(BIN_EXT) up

.PHONY: migrate-down
migrate-down: migrate-build ## Reverte TODAS as migrations (down completo)
	DATABASE_URL="$(DATABASE_URL)" ./bin/migrate$(BIN_EXT) down

.PHONY: migrate-down-one
migrate-down-one: migrate-build ## Reverte apenas a última migration
	DATABASE_URL="$(DATABASE_URL)" ./bin/migrate$(BIN_EXT) down 1

.PHONY: migrate-version
migrate-version: migrate-build ## Mostra a versão atual do schema
	DATABASE_URL="$(DATABASE_URL)" ./bin/migrate$(BIN_EXT) version

.PHONY: migrate-reset
migrate-reset: ## Reverte tudo e reaplica (útil em desenvolvimento)
	$(MAKE) migrate-down || true
	$(MAKE) migrate-up

.PHONY: db-verify
db-verify: ## Verifica as constraints do schema (cada INSERT inválido deve ser recusado)
	docker cp deploy/postgres/verify_constraints.sql jungleg-postgres:/tmp/verify.sql
	docker exec jungleg-postgres psql -U $(POSTGRES_USER) -d $(POSTGRES_DB) \
		-v ON_ERROR_STOP=1 -f /tmp/verify.sql

.PHONY: psql
psql: ## Abre um psql na base local
	docker compose exec postgres psql -U jungleg -d jungleg

# --- Qualidade e testes ------------------------------------------------------

.PHONY: build
build: ## Compila todos os binários em ./bin
	$(GO) build -o bin/ ./cmd/...

.PHONY: run
run: ## Roda a API localmente (precisa da infra de pé)
	$(GO) run ./cmd/api

.PHONY: fmt
fmt: ## Formata o código (gofmt)
	$(GO) fmt ./...

.PHONY: fmt-check
fmt-check: ## Falha se houver arquivo fora do gofmt
	@out=$$($(GO) fmt ./... 2>&1 | grep -v '^$$' || true); \
	if [ -n "$$out" ]; then echo "Arquivos reformatados: $$out"; exit 1; fi

.PHONY: vet
vet: ## go vet
	$(GO) vet ./...

.PHONY: tidy
tidy: ## go mod tidy
	$(GO) mod tidy

.PHONY: test
test: ## Testes unitários (sem Docker)
	$(GO) test ./...

.PHONY: test-race
test-race: ## Testes unitários com detector de corrida
	$(GO) test -race ./...

.PHONY: test-cover
test-cover: ## Testes com relatório de cobertura
	$(GO) test -coverpkg=./internal/... -coverprofile=coverage.out ./internal/...
	$(GO) tool cover -func=coverage.out | tail -n 1

.PHONY: test-integration
test-integration: ## Testes de integração (exigem docker compose up + filas criadas)
	$(GO) test -race -count=1 -tags=integration ./test/...

.PHONY: test-all
test-all: test-race test-integration ## Toda a suíte

.PHONY: check
check: fmt-check vet test-race ## Verificação rápida antes de commitar

.PHONY: token
token: ## Obtém um access token de teste do Keycloak (client_credentials)
	@curl -s -X POST http://localhost:8080/realms/jungleg/protocol/openid-connect/token \
		-d grant_type=client_credentials \
		-d client_id=provider-service \
		-d client_secret=provider-service-secret | head -c 400; echo

.PHONY: health
health: ## Checa os endpoints de health da API local
	@curl -s http://localhost:8080/health/live; echo
	@curl -s http://localhost:8080/health/ready; echo
