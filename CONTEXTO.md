# CONTEXTO.md — Estado do projeto para continuar em qualquer sessão

> **Leia este arquivo PRIMEIRO** ao retomar o trabalho. Ele tem o estado atual, as decisões
> congeladas, as pendências e o próximo passo. Os detalhes estão nos outros documentos.
>
> **Documentos do projeto:**
> | Arquivo | Para que serve |
> | --- | --- |
> | `README.md` (raiz) | Enunciado oficial do desafio — **não editar** |
> | **`PROXIMO_PASSO.md`** | ⭐ **HANDOFF**: onde paramos exatamente e como continuar. Ler ao abrir nova conversa |
> | `PLANO.md` | Plano completo: decisões (A1–A14), checklist por fase, cronograma de 2 dias |
> | `docs/INFRAESTRUTURA.md` | ⭐ O que são Postgres, Keycloak, SQS, LocalStack e Docker — conceito antes de configuração |
> | `docs/FX_EXPLICADO.md` | ⭐ O Uber Fx do zero: as 3 palavras, o grafo desenhado, o passo a passo da execução e o ciclo de vida |
> | `docs/GUIA_FASE_0.md` | Guia didático da Fase 0: pasta por pasta, arquivo por arquivo, linha a linha |
> | `docs/QUESTOES.md` | Perguntas de fixação + gabarito (termômetro de aprendizado) |
> | `docs/evidencias/FASE_0.md` | Saídas reais dos comandos que validaram a Fase 0 |
> | `CONTEXTO.md` | Este arquivo — estado atual e próximos passos |

**Ordem de leitura recomendada:** `docs/INFRAESTRUTURA.md` (o quê) → `docs/FX_EXPLICADO.md` (a composição) → `docs/GUIA_FASE_0.md` §1–7 (código) → `docs/QUESTOES.md` (fixação).

---

## 1. ESTADO ATUAL

| Item | Valor |
| --- | --- |
| Fase atual | **Fase 0 concluída e VALIDADA com infraestrutura real.** Próximo: Fase 1 (`Money`) |
| Última fase concluída | Fase 0 — fundação + infraestrutura de pé |
| Testes | `go test -race ./...` verde (4 pacotes) |
| Build | `go build ./...` e `go vet ./...` limpos; `gofmt -l` vazio |
| Infraestrutura | PostgreSQL, Keycloak e LocalStack **rodando e validados** (ver `docs/evidencias/FASE_0.md`) |

**O que já existe e funciona:**
- Estrutura de módulo, `go.mod`/`go.sum` com 20+ dependências fixadas
- `Dockerfile` multi-stage com alvo selecionável (`api`/`migrate`), usuário não-root
- `docker-compose.yml`: PostgreSQL 16, Keycloak 26.4, LocalStack, `migrate`, `api`
- `deploy/localstack/init-queues.sh`: 4 filas FIFO + 2 DLQs + redrive (idempotente)
- `deploy/keycloak/realm.json`: realm, 4 clients, roles `provider`/`internal`, claim `provider_id`, usuários de teste
- `deploy/postgres/init/01-databases.sql`: bancos `jungleg`, `jungle_test`, `keycloak`
- `internal/config`: leitura + validação única com acúmulo de problemas
- `internal/observability`: slog JSON com correlação automática; 13 métricas Prometheus
- `internal/httpapi`: middlewares, erros tipados, `/health/live`, `/health/ready`, `/metrics`
- `internal/app`: composição Fx, ciclo de vida, shutdown gracioso
- `cmd/api/main.go`: entrypoint de 15 linhas
- **Smoke test real:** app subiu em `:8099`, endpoints e `/metrics` responderam

**O que NÃO existe ainda:** qualquer regra financeira, qualquer tabela, qualquer
repositório, qualquer autenticação, qualquer worker.

---

## 2. AMBIENTE (aprendizado caro — não repita os erros)

### 2.1. Cache do Go (OBRIGATÓRIO)
O cache global `C:\Users\PoxZe\go` **não é gravável** nesta máquina. Todo comando Go precisa:

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'
$env:GOCACHE='E:\jungleg\.gocache\build'
$env:GOPATH='E:\jungleg\.gocache\gopath'
```

O `Makefile` já faz isso automaticamente. A pasta `.gocache/` está no `.gitignore`.

### 2.2. Docker (instalado e FUNCIONANDO)

| Item | Valor |
| --- | --- |
| Versão | Docker **29.8.2**, Compose **v5.6.0**, storage `overlayfs` |
| Caminho do CLI | `C:\Users\PoxZe\AppData\Local\Programs\DockerDesktop\resources\bin` |
| PATH | Registrado no PATH do **usuário**; sessões abertas antes não enxergam |
| Estado | Containers `jungleg-postgres`, `jungleg-keycloak`, `jungleg-localstack` **healthy** |

⭐ **Para trabalhar nesta sessão, prefixe o PATH:**
```powershell
$env:Path = "C:\Users\PoxZe\AppData\Local\Programs\DockerDesktop\resources\bin;$env:Path"
```

⚠️ **Duas armadilhas já resolvidas (não repita):**
1. **LocalStack:** usar `localstack/localstack:4.0.3`. As tags `stable`/`latest` são **Pro** e exigem `LOCALSTACK_AUTH_TOKEN`; sem ele o container morre com *"License activation failed"*.
2. **Sandbox × Docker:** o sandbox do harness não abre o *named pipe* do Docker. Comandos Docker precisam de acesso ampliado (`danger-full-access`), ou o usuário roda no terminal dele.
3. **Docker Desktop não inicia sozinho** (`autoStart: False`). Se o pipe não existir, iniciar `Docker Desktop.exe` e aguardar ~5s.

### 2.3. Line endings em scripts shell
`deploy/localstack/init-queues.sh` é executado em container Linux. Ao editá-lo pelo
Windows, **garanta LF** (não CRLF), senão o shell falha com
`syntax error: unexpected end of file`. O `.gitattributes` do repositório resolve isso
para quem clonar.

### 2.3. Ferramentas disponíveis

| Ferramenta | Status |
| --- | --- |
| Go | ✅ 1.26.5 (módulo declara `go 1.25.0` + `toolchain go1.26.5`) |
| git | ✅ 2.52.0 |
| Docker + Compose | ✅ instalado (ver restrição acima) |
| `make` | ❌ não instalado (os alvos servem de documentação executável) |
| PowerShell | 5.1 (não 7) — sem `&&`, sem ternário |
| openssl | ✅ msys64 |

---

## 3. DECISÕES CONGELADAS (resumo — detalhes em `PLANO.md` §1)

| ID | Decisão |
| --- | --- |
| A1 | chi · pgx/v5 · golang-migrate · aws-sdk-go-v2 · go-oidc · google/uuid (v7) · Prometheus · testes com lib padrão |
| A2 | `Money` = `int64` em unidades mínimas + moeda ISO 4217; escala fixa 2; nunca float |
| A3 | Uma transação SQL por operação (Unit of Work); repositório nunca comita sozinho |
| A4 | Concorrência: `SELECT ... FOR UPDATE` na carteira + constraints no banco (**sem** lock global) |
| A5 | Idempotência: duas constraints únicas + hash canônico (JSON ordenado, sem metadados de transporte) |
| A6 | Estados: `PENDING → PROCESSED/REJECTED/FAILED/PENDING_REFERENCE`; terminais não transicionam |
| A7 | Referências resolvidas por `(providerId, externalTransactionId)`; `PENDING_REFERENCE` + worker com backoff |
| A8 | Tabela de `failureCode` estáveis (18 códigos) |
| A9 | Contrato HTTP completo (200/201/202/400/401/403/404/409/422/500/503) |
| A10 | Keycloak: roles `provider`/`internal`, claim `provider_id` no token, audience `jungleg-api` |
| A11 | Inbox (`consumer_name, message_id`) e outbox com lease (`locked_until`) |
| A12 | 4 eventos: `WagerTransactionProcessed`, `WagerTransactionRejected`, `WalletBalanceChanged`, `WagerTransactionPendingReference` |
| A13 | slog JSON + correlação automática + métricas Prometheus (sem IDs como label) |
| A14 | Fx: módulos, `fx.Lifecycle`; shutdown na ordem servidor → workers → recursos |

---

## 4. PENDÊNCIAS ANOTADAS (corrigir nas fases indicadas)

| # | Pendência | Fase |
| --- | --- | --- |
| P1 | Healthcheck do serviço `api` no compose usa `-health-check`, flag que não existe | 7 |
| P2 | Ordem `AccessLog` × `Recoverer`: requisição com panic pode não gerar log de acesso | 7 |
| P3 | Trocar o `select` + `time.After(50ms)` por `net.Listen` + `server.Serve(ln)` (erro de bind síncrono) | 7 |
| P4 | Revisar path `/internal/wallets`: o enunciado pede `POST /wallets` (o "internal" é autorização, não path) | 7 |
| P5 | Registrar fechamento de pool/SQS no módulo de infra, para fechar **por último** | 5 |
| P6 | `cmd/migrate` ainda não existe (o compose o referencia) | 4 |
| P7 | Registrar `fx.StopTimeout` coerente com `SHUTDOWN_TIMEOUT` da config | 8 |
| P8 | `shutdown gracioso` não pôde ser testado com SIGTERM real (precisa de Docker) | 8 |
| P9 | Pasta `E:\jungleg\jungle_desafio\` é um **segundo repositório git** (clone vazio). Pendente de decisão: apagar ou reorganizar | — |
| P10 | `jungle_desafio` ficou como gitlink no commit `1eef5b2` (inofensivo, mas convém limpar o índice) | — |

---

## 5. REVISÃO ANOTADA

O revisor apontou que eu fui **rápido demais** sem garantir entendimento. Correção aplicada:

- Criado `docs/GUIA_FASE_0.md` (guia didático completo, linha a linha)
- Criado `docs/QUESTOES.md` (fixação + gabarito)
- **Novo ritmo combinado a partir da Fase 1:**

| Regra | Como se aplica |
| --- | --- |
| Um arquivo por vez | Nunca mais entregar 6 arquivos de uma vez sem explicação |
| Explicar antes de escrever | O "porquê" e as exigências do enunciado vêm **antes** do código |
| Checkpoint a cada arquivo | O usuário confirma que entendeu antes de seguir |
| Código do núcleo é do usuário | `Money`, `Wallet`, `Ledger`, `WagerTransaction` são escritos por ele, com contrato e revisão |
| Problemas viram lição | Todo bug encontrado vira uma seção explicada (ver `GUIA_FASE_0.md` §9) |

---

## 6. PRÓXIMO PASSO — Fase 1: `Money`

**Divisão combinada:** o usuário escreve o núcleo; eu entrego o contrato, reviso e testo.

Ordem proposta (um bloco por vez, com checkpoint):
1. **Eu:** entrego o contrato do `Money` (tipos, assinaturas, invariantes, limites, tabela de casos de teste) **sem código**
2. **Usuário:** implementa `internal/domain/money/money.go`
3. **Eu:** reviso linha por linha, aponto o que está errado e por quê
4. **Usuário:** escreve os testes (ou eu escrevo e ele explica)
5. **Nós:** rodamos `go test -race ./internal/domain/money/...` até verde
6. Repetimos para `Currency`, `Wallet`, `LedgerEntry`, `WagerTransaction`

**Perguntas em aberto para o usuário:**
- Preferiu TDD (testes antes) ou implementação primeiro?
- Responder as perguntas de `docs/QUESTOES.md` que não souber (fechar lacunas antes de avançar)
- Decisão sobre a pasta `jungle_desafio` (P9)
