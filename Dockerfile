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
# ---------------------------------------------------------------------------
FROM golang:1.25-alpine AS deps

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
