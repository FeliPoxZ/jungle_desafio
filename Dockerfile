# =============================================================================
# Dockerfile multi-stage.
#
# Estágios:
#   deps  -> baixa as dependências (camada cacheável; só refaz se go.mod mudar)
#   build -> compila um binário ESTÁTICO (CGO_ENABLED=0)
#   final -> imagem mínima, sem toolchain, rodando como usuário não-root
#
# O binário alvo é escolhido pelo build arg TARGET (api | migrate), o que
# permite usar o MESMO Dockerfile para os dois serviços do docker-compose.
# =============================================================================

# ---------------------------------------------------------------------------
# deps: camada de dependências
#
# A versão da imagem precisa atender o que o go.mod exige (go 1.26.0). Com uma
# imagem mais antiga o build falha em "go mod download" com
# "go.mod requires go >= 1.26.0", porque a imagem oficial define
# GOTOOLCHAIN=local e não baixa toolchain sozinha.
# ---------------------------------------------------------------------------
FROM golang:1.26-alpine AS deps

WORKDIR /src

# go.mod/go.sum primeiro: enquanto eles não mudarem, o cache de módulos é
# reaproveitado e o build fica rápido.
COPY go.mod go.sum* ./
RUN go mod download

# ---------------------------------------------------------------------------
# build: compilação
# ---------------------------------------------------------------------------
FROM deps AS build

ARG TARGET=api
ARG VERSION=dev
ARG COMMIT=unknown

# O código-fonte. Fica DEPOIS do go mod download de propósito: enquanto só o
# código mudar, a camada de dependências continua em cache.
#
# O que entra no contexto é controlado pelo .dockerignore (sem cache do Go,
# sem binários locais, sem documentação).
COPY . .

# -trimpath  : remove caminhos absolutos do binário (build reprodutível)
# -s -w      : remove tabela de símbolos e debug info (imagem menor)
# CGO_ENABLED=0: binário estático, roda em imagem sem libc
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
        -o /out/app ./cmd/${TARGET}

# ---------------------------------------------------------------------------
# final: runtime
# ---------------------------------------------------------------------------
FROM alpine:3.21 AS final

# wget é usado pelo healthcheck do compose (busybox já traz, mas explícito);
# ca-certificates é necessário para validar o TLS do IdP em produção.
RUN apk add --no-cache ca-certificates tzdata wget && \
    addgroup -S -g 10001 app && \
    adduser -S -u 10001 -G app -h /app app

WORKDIR /app

COPY --from=build /out/app /app/app

USER 10001:10001

# Sem porta EXPOSE fixa útil: o endereço vem de HTTP_ADDR.
# O binário lê configuração apenas do ambiente (nunca de arquivos versionados).
ENTRYPOINT ["/app/app"]
