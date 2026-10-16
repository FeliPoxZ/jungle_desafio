# PROXIMO_PASSO.md — Handoff para a próxima conversa

> **INSTRUÇÃO PARA A NOVA CONVERSA (leia isto primeiro):**
> Este arquivo é um handoff. Ele diz **onde o projeto está, o que já foi decidido, qual é o
> método de trabalho acordado e exatamente qual é a próxima tarefa pontual**.
>
> Antes de responder qualquer coisa, leia também, nesta ordem:
> 1. `README.md` (raiz) — **o enunciado é a fonte de verdade**; em caso de dúvida, ele decide
> 2. `ARCHITECTURE.md` (raiz) — arquitetura, decisões e problemas resolvidos
> 3. `docs/CONFORMIDADE_README.md` — o que do enunciado já está atendido, o que falta e em que fase
> 4. `CONTEXTO.md` (raiz) — estado geral, ambiente, decisões congeladas, pendências
> 5. `PLANO.md` §3 — checklist por fase
> 6. `docs/evidencias/FASE_1.md` a `FASE_7.md` — provas do que já funciona
> 7. `docs/LEDGER_VS_WALLET.md` — a distinção central do domínio financeiro
>
> **Não comece a codar sem ler as seções 3 e 3.5.**

---

## 1. O QUE É O PROJETO

Desafio técnico (3ª fase de um processo seletivo) para vaga de **desenvolvedor back-end
Go júnior**. O enunciado está em `README.md` (raiz) e **não pode ser editado**.

Resumo: um serviço em Go com **Uber Fx** que processa operações financeiras de provedores
de jogos (`BET`, `WIN`, `LOSS`, `REFUND`, `ROLLBACK`) em ambiente **distribuído**, com
PostgreSQL, Keycloak (OIDC) e AWS SQS (LocalStack local). Exige garantias financeiras
reais: precisão monetária, idempotência persistente, ledger append-only, concorrência sem
lost update, outbox transacional, recuperação de falhas.

**Prazo original:** 2 dias. **Entrega:** código + migrations + docker-compose + README da
solução + `ARCHITECTURE.md`.

**Repositório:** `github.com/FeliPoxZ/jungle_desafio`
**Workspace:** `E:\jungleg`

---

## 2. ONDE O PROJETO ESTÁ AGORA

| Item | Estado |
| --- | --- |
| Fase 0 (fundação + infraestrutura) | ✅ **CONCLUÍDA E VALIDADA** com containers reais |
| Fase 1 (`Money`) | ✅ **CONCLUÍDA E VALIDADA** — 384 casos, 97,9% de cobertura, 10/10 no README |
| Fase 2 (`Wallet` + `LedgerEntry`) | ✅ **CONCLUÍDA E VALIDADA** — 63 casos, 99,0% de cobertura |
| Fase 3 (`WagerTransaction`) | ✅ **CONCLUÍDA E VALIDADA** — 127 casos, **100,0%** de cobertura |
| Fase 4 (schema + migrations) | ✅ **CONCLUÍDA E VALIDADA** — 29 CHECK + 4 FK + 2 UNIQUE, 32 operações inválidas recusadas pelo banco |
| Fase 5 (persistência `pgx` + Unit of Work) | ✅ **CONCLUÍDA E VALIDADA** — 38 testes (25 de integração), `-race` verde, 4 defeitos reais corrigidos |
| Fase 6 (caso de uso `wagering.Service`) | ✅ **CONCLUÍDA E VALIDADA** em 3 blocos: 6a (`Process`), 6b (`OpenWallet`), 6c (leituras + `Reconcile`) — 55 casos unitários, 27 de integração, **6 defeitos reais corrigidos** |
| Fase 7 (HTTP + autenticação) | ✅ **CONCLUÍDA E VALIDADA** em 5 blocos (7a–7e) + P1 — **49 casos** em `internal/infra/auth`, **67** em `internal/httpapi`, **30** de integração da borda HTTP (Keycloak real), **2** do binário real, **8 defeitos reais corrigidos** |
| Fase 8 (SQS + inbox) | ⬅ **PRÓXIMA** |
| Fases 9–12 | ⬜ Não iniciadas (ver checklist no `PLANO.md` §3) |

**Domínio (Fases 1–3): 574 casos. Persistência (Fase 5): 38. Caso de uso (Fase 6):
55 unitários + 27 de integração. Borda HTTP (Fase 7): 116 unitários + 32 de
integração. `go test -race` verde em 15 pacotes de unidade e nos 3 de integração.
Árvore git limpa.**

> ⚠️ Os **três** pacotes de integração usam o mesmo banco de teste e truncam
> tabelas. Cada `TestMain` toma um lock consultivo (`pg_advisory_lock`) e espera a
> vez, então `go test -tags=integration ./...` funciona **sem** `-p 1`.
>
> ⚠️ Os testes de integração da **borda HTTP** precisam do **Keycloak de pé**: o
> token é obtido por `client_credentials` no realm `jungleg` (nada é forjado). Sem
> Keycloak, eles são **pulados** com aviso.

### Histórico de commits (mais recente primeiro)

```
cc986df docs(fase7): registra as evidencias da Fase 7 e atualiza arquitetura e conformidade
31cf6b5 feat(api): adiciona a flag -health-check usada pelo healthcheck do compose      ← P1
343ec51 test(http): prova a fase 7 com o Keycloak e o PostgreSQL reais                  ← FASE 7e
95e85bb feat(http): adiciona os endpoints de wagering e carteira com autenticacao OIDC  ← FASE 7b/7d
1d43174 feat(auth): adiciona autenticacao OIDC com validacao real de token e provider do token ← FASE 7a
ec6c014 feat(app): compoe a aplicacao com Fx e configura o encerramento e o readiness
8443531 docs: adiciona auditoria de conformidade com o enunciado e atualiza o prompt de abertura
0e89e65 fix(outbox): ordena a fila por sequencia e corrige a ordem do lote                ← FASE 6d
52f16dc docs(handoff): atualiza PROXIMO_PASSO para a Fase 7 (HTTP e autenticacao)
21cf528 feat(app): adiciona leituras do caso de uso e reconciliacao em visao consistente  ← FASE 6c
a397398 feat(app): adiciona abertura de carteira com lancamento de abertura e eventos      ← FASE 6b
0cf1526 docs(handoff): atualiza PROXIMO_PASSO para a Fase 6b (abertura de carteira)
4f0ab9d feat(app): adiciona caso de uso de operacoes com idempotencia e ledger transacional ← FASE 6a
aaa6c24 docs(arquitetura): cria ARCHITECTURE.md e o prompt de abertura da proxima conversa
2e4c2c0 refactor(arquitetura): separa dominio, contrato e infraestrutura
182df42 docs(estrategia): registra a mudanca de metodo e o compromisso dos dois documentos finais
4a3a2d5 docs(handoff): atualiza PROXIMO_PASSO para a Fase 6 (caso de uso)
756bed2 feat(postgres): adiciona repositorios pgx com unit of work e locking por carteira   ← FASE 5
879810f docs(handoff): atualiza PROXIMO_PASSO para a Fase 5 (pgx + unit of work)
b209f85 feat(db): adiciona schema inicial com constraints de saldo, ledger imutavel e idempotencia ← FASE 4
bdcf0e1 fix(wagering): valida saldo resultante antes de transicionar e remove parametro morto
04194ca docs(handoff): atualiza PROXIMO_PASSO para a Fase 4
72c3aaf feat(wagering): adiciona transacao de aposta com tipos, estados e eventos   ← FASE 3
11cbb9b docs: adiciona LEDGER_VS_WALLET.md e limpa comentarios do pacote wallet
44e5e46 docs(handoff): atualiza PROXIMO_PASSO para a Fase 3
98fbee6 feat(wallet): adiciona carteira com debito/credito e ledger append-only   ← FASE 2
442a025 docs: registra o hash do commit da Fase 1 no plano e no handoff
b046e6a feat(money): completa o value object com aritmetica, comparacao e serializacao   ← FASE 1
3172772 bloco 2 monet incompleto
3a36aec feat(money): adiciona o value object Currency com erro de dominio tipado
1eef5b2 chore(setup): fundacao do projeto com Fx, config validada, HTTP e observabilidade
```

> ✅ **As Fases 0 a 7 estão commitadas e validadas. Não revalide nem recommite.**

---

## 3. ⭐ MÉTODO DE TRABALHO — ESTRATÉGIA MUDOU A PARTIR DA FASE 6

### 3.1. A MUDANÇA (leia com atenção)

**Até a Fase 5** o ritmo era "MODO B": a IA explicava um bloco pequeno, **o usuário digitava**,
e depois vinha o próximo bloco. Foi assim que o núcleo do domínio (`Money`, `Wallet`,
`WagerTransaction`) foi construído e aprendido.

**A partir da Fase 6 a estratégia é outra:**

> **A IA escreve o código.** O usuário revisa, e a IA explica **as partes importantes**
> depois de escrever. O ritmo continua sendo um bloco coerente por vez, com commit ao final
> de cada um — mas quem digita é a IA.
>
> **Motivo:** o tempo é curto e as Fases 6–12 são de integração (serviço, HTTP, SQS, outbox,
> workers). O valor está em fechar o sistema funcionando, não em digitar cada linha.
>
> ⚠️ **Ainda assim: explicar faz parte da entrega.** Código entregue sem a explicação das
> decisões é entrega incompleta nesta sessão. O usuário está se preparando para uma
> entrevista e precisa saber defender cada parte.

### 3.2. O que continua valendo

| Regra | Como aplicar |
| --- | --- |
| **Explicar o que foi feito** | Depois de escrever, explicar as partes importantes: **decisão**, **motivo** e **o que quebraria sem ela** |
| **Um bloco coerente por vez** | Não entregar 3 fases juntas. Bloco → validação → commit |
| **Aceitar contestação** | ⭐ O usuário questiona e isso é bom. Se ele estiver certo, **admitir e corrigir** |
| **Validar de verdade** | `go build`/`go vet`/`gofmt` limpos, testes verdes, e **integração contra o banco real** quando a fase tocar persistência |
| **Commit ao final de cada bloco** | Com mensagem que conta a história: o que foi feito, o que foi decidido e o que foi encontrado |
| **Atualizar `PLANO.md` e `PROXIMO_PASSO.md`** | A cada fase concluída, com as decisões e evidências |
| **Registrar achados** | Bug encontrado durante o trabalho vai para a mensagem de commit **e** para as evidências |

### 3.3. Formato da entrega de cada bloco

1. **O que foi feito** (em uma frase)
2. **O código** (arquivos e por que estão separados assim)
3. **As decisões importantes** — tabela `decisão | motivo | o que quebraria sem ela`
4. **Os achados** — bug, imprevisto, ajuste de desenho
5. **A validação** — comandos e saída real
6. **O commit**

### 3.4. Princípios de qualidade já cobrados (lições das Fases 1–5)

> **1. Provar com teste antes de afirmar que uma guarda basta.** Na Fase 1 a IA afirmou duas
> vezes "uma guarda basta" e as duas estavam erradas — a varredura exaustiva de 81 combinações
> achou 10 furos no `Add`. **Varredura > raciocínio** em sinal, limite e overflow.

> **2. Quando um teste falhar, verificar se a régua está certa antes de acusar o código.**
> A IA quase acusou o código do usuário três vezes por expectativa errada do próprio teste.

> **3. Retry envolve a TRANSAÇÃO INTEIRA**, nunca uma chamada por dentro dela. Repetir dentro
> da transação deixa a tentativa que falhou com a conexão aberta segurando o lock, e o sintoma
> aparece longe (o próximo `TRUNCATE` morre com deadlock). A unidade de retry é a transação —
> vale para os testes e para a Fase 6.

> **4. Consultar o schema antes de escrever SQL.** Dois "defeitos" da Fase 5 eram testes que
> ignoravam fato do schema: o ledger tem FK para `wager_transactions`, e `id` é `UUID`.

> **5. Antes de escrever helper, verificar se já existe na stdlib.** "Evitar um import" nunca
> é justificativa.

> **6. Comentário só onde explica decisão.** Código enxuto, sem doc comment onde o nome já diz.

> **7. Cuidado com `Set-Content` no PowerShell 5.1:** grava em ANSI e **corrompe acentos**.
> Usar a ferramenta de edição ou `[System.IO.File]::WriteAllLines` com `UTF8Encoding($false)`.
> Confirmar com `$texto.Contains('ç')` depois de escrever. Já aconteceu duas vezes.

> **8. Não confie em contagem de falha tirada de pipeline do PowerShell.** Um `Select-String`
> sem `-Pattern` já produziu um "24 falhas" falso. Ler o output real do `go test`.

> **9. `git commit` com mensagem longa:** usar arquivo de mensagem (`git commit -F arquivo`),
> não heredoc — o `@'...'@` do PowerShell 5.1 quebra com aspas e acentos.

### 3.5. 🎯 COMPROMISSO FINAL: DOIS DOCUMENTOS DE ENTREGA

Quando o sistema estiver **funcionando e fechado** (Fases 6–12 concluídas), a IA deve gerar
**dois arquivos** — e eles fazem parte da entrega, não são opcionais:

> ⭐ **Exigência do usuário (registrada em 2026-10-16):** os dois documentos precisam explicar
> **do básico ao avançado**, e não presumir vocabulário. Ou seja: começar pelo que é dinheiro
> em unidades mínimas, o que é uma carteira, o que é um lançamento de ledger, o que é uma
> transação/lock, o que é idempotência, o que é outbox — e só DEPOIS as ligações entre as
> partes (quem chama quem, onde cada garantia é imposta) e os conceitos de regra de negócio
> (por que `LOSS` não movimenta, por que `ROLLBACK` de `REFUND` debita, por que o replay
> devolve o saldo original). Quem lê precisa entender sem já saber o jargão.

#### Documento 1 — Explicação do sistema completo

**Arquivo sugerido:** `docs/SISTEMA_EXPLICADO.md`

Explicar **tudo o que o sistema faz**, para quem vai ler o código ou mantê-lo:

- o fluxo completo de uma requisição, do HTTP ao commit e à publicação do evento;
- cada componente e por que ele existe (domínio, repositórios, UoW, outbox, inbox, workers);
- **as lógicas que foram pensadas e seguidas** — as decisões de arquitetura com o motivo;
- **os problemas que foram resolvidos** — a lista de achados de todas as fases (as guardas
  de overflow, o `Neg` invertido, o `CHAR(3)` com padding, o `55P03` mal classificado, o
  `Movement`, o `newEntryID` que não era UUID...), com o sintoma e a correção;
- os limites conhecidos e o que ficou de fora conscientemente.

#### Documento 2 — Preparação para a entrevista

**Arquivo sugerido:** `docs/ENTREVISTA.md`

**Formato de perguntas e respostas**, na lógica de quem entrevista:

- perguntas de **arquitetura**: "por que `READ COMMITTED` com `FOR UPDATE` e não
  `SERIALIZABLE`?", "por que outbox transacional e não publicar direto no SQS?", "por que o
  ledger é separado da carteira?";
- perguntas de **decisão**: "por que `int64` em unidades mínimas e não `decimal`?", "por que
  o domínio não importa `encoding/json`?";
- perguntas de **garantia**: "como você impede débito duplicado?", "onde está a proteção
  contra lost update?", "o que acontece se o processo morrer no meio?";
- perguntas de **erro e aprendizado**: "qual foi o bug mais difícil?", "o que você faria
  diferente?";
- as respostas devem deixar **arquitetura e lógica bem visíveis**, com o motivo e a
  consequência — não só o "o quê";
- incluir as perguntas difíceis (as que expõem limitação) com resposta honesta.

> ⚠️ **Estes dois documentos são o motivo pelo qual a IA deve explicar cada bloco durante o
> caminho:** eles serão montados a partir dessas explicações. Explicar agora é investir na
> entrega final.

---

## 4. AMBIENTE — DETALHES QUE CAUSAM ERRO SE IGNORADOS

### 4.1. Cache do Go (OBRIGATÓRIO)

O cache global `C:\Users\PoxZe\go` é gravável, mas o projeto usa cache isolado. Todo comando:

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'
$env:GOCACHE='E:\jungleg\.gocache\build'
$env:GOPATH='E:\jungleg\.gocache\gopath'
```

O `Makefile` já faz isso. `/.gocache/` está no `.gitignore`.

### 4.2. Docker (necessário das Fases 4 em diante)

```powershell
$env:Path = "C:\Users\PoxZe\AppData\Local\Programs\DockerDesktop\resources\bin;$env:Path"
```

| Particularidade | Detalhe |
| --- | --- |
| Sandbox × Docker | O sandbox pode bloquear o named pipe. Se bloquear, usar `sandbox_permissions` com `justification` em português |
| Docker Desktop | **Não inicia sozinho**. Se o pipe não existir: `Start-Process 'C:\Users\PoxZe\AppData\Local\Programs\DockerDesktop\Docker Desktop.exe'` e aguardar ~5–30s |
| LocalStack | **SÓ** `localstack/localstack:4.0.3`. Tags `stable`/`latest` são **Pro** e morrem com *"License activation failed"* |
| Containers esperados | `jungleg-postgres`, `jungleg-keycloak`, `jungleg-localstack` — todos `healthy` |
| Bancos | `jungleg` (aplicação), `jungle_test` (testes de integração), `keycloak` |

### 4.3. Banco de teste

Os testes de integração **truncam tabelas**: apontar para `jungleg` apagaria dados de verdade.
Sempre usar o banco separado:

```powershell
docker compose up -d postgres
$env:DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungleg?sslmode=disable'
$env:TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
.\bin\migrate.exe up
go test -count=1 -race ./...
go test -count=1 -race -tags=integration -timeout 300s ./...
```

### 4.4. Line endings e outras ferramentas

- Scripts shell, SQL e Dockerfile rodam em Linux nos containers: garantir **LF** (o
  `.gitattributes` já normaliza).
- Go **1.26.5** · PowerShell **5.1** (sem `&&`, sem ternário) · git 2.52.0
- **`make` NÃO está instalado** — os alvos do Makefile são documentação executável
- **`package main` não pode ser importado nem testado** → toda lógica em `internal/`

---

## 5. DECISÕES TÉCNICAS JÁ CONGELADAS

**Estrutura de pacotes (reorganizada depois da Fase 5 — commit `2e4c2c0`):**

```
internal/domain/{errors,ids,money,ledger,wallet,wagering}   regra de negócio, sem I/O
internal/app/ports                                          CONTRATOS (interfaces + erros + Cursor + Clock)
internal/app/wagering                                       CASO DE USO (Process, OpenWallet, leituras, Reconcile)
internal/infra/postgres                                     adaptador do banco
internal/httpapi · internal/platform · internal/config · internal/observability
test/integration                                            integração do caminho completo
```

A direção das dependências é `app → ports ← infra`: a aplicação não importa o banco, e o
banco não importa a aplicação. Motivos e histórico em `ARCHITECTURE.md` §1. O caso de uso
depende de `ports.UnitOfWork`/`ports.Repos`, `ports.Clock` e `idgen.Generator` — nunca de
`infra/postgres`.

⚠️ **Os sentinelas de erro são os MESMOS objetos nos dois lados** (`postgres.ErrConflict` é
alias de `ports.ErrConflict`). Não recrie um jogo de erros na infra: `ports.IsConflict`
devolveria `false` para erro do repositório, **silenciosamente**. Há teste de regressão que
passa um `PgError` de verdade pela classificação.

⚠️ **`domainerrors.Error` é o tipo de erro dos QUATRO pacotes de domínio**, por alias
(`money`, `wallet`, `ledger`, `wagering`). Não redefina o struct em nenhum deles: um tipo
próprio fica **fora** da travessia do `errors.As` que o `domainerrors.CodeOf` usa, e o
`failureCode` da resposta sai **em branco** para todo erro daquele pacote — foi o defeito 13
da §15 do `ARCHITECTURE.md`, e passou despercebido porque cada pacote testava com a sua
própria cópia do extrator de Code.

⚠️ **O ledger é pacote próprio desde a reorganização**: `ledger.Entry`, `ledger.Direction`,
`ledger.NewEntry`. `wallet.Debit` devolve `Movement`, cujo `Entry()` é um `ledger.Entry`.

⚠️ **`TransactionID` é um tipo só** (`ids.TransactionID`), com alias em `wagering` e
`ledger`. Não crie conversão entre os dois: são o mesmo conceito, e uma conversão esquecida
viraria string vazia — exatamente o que `ledger.NewEntry` recusa.

⚠️ **Identificador que não é UUID não pode chegar ao banco**: `wallets.id`,
`wager_transactions.id` e `wallet_ledger_entries.wallet_id` são colunas `UUID`, e um texto
qualquer numa consulta a elas vira `22P02`, que o driver classifica como falha
**permanente** (500). O caso de uso confere o formato antes de consultar
(`internal/app/wagering/ids.go`) — e os dublês de teste geram UUID de verdade, para não
testarem um caminho que produção não tem.

Detalhamento completo em `PLANO.md` §1 (A1–A14). O que afeta as Fases 6–7:

| ID | Decisão |
| --- | --- |
| **A2** | `Money` = `int64` em unidades mínimas + `Currency` ISO 4217, escala 2, **nunca float**. Limite `9_000_000_000_000_000` |
| A2 | `New` valida moeda **e** faixa; `FromMinor` reidrata sem validar |
| A2 | JSON: `{"amount":"25.00","currency":"BRL"}`; `UnmarshalJSON` é estrito (campo desconhecido e chave repetida são erro) |
| **A3** | Uma **Unit of Work**: toda operação de negócio em **uma** transação (`wager_transactions` + `wallets` + `wallet_ledger_entries` + `inbox_messages` + `outbox_events`) |
| **A4** | Concorrência: **`READ COMMITTED` + `SELECT ... FOR UPDATE`** + versão. Não `SERIALIZABLE` |
| **A5** | Idempotência **persistente**: índice único parcial + comparação de hash canônico |
| **A8** | Tabela de `failureCode` estáveis — o `Code` do erro de domínio vira `failureCode` no HTTP |
| A9 | Contrato HTTP: `201` nova, `200` replay, `202` pendente, `422` rejeição, `409` conflito, `403` provedor errado |
| A9 | Ledger paginado: cursor opaco `base64url(createdAt RFC 3339Nano \| entryId)`, `limit` padrão 50 e **teto 200** |
| A10 | `providerId` vem do TOKEN; operação de outro provedor responde **não encontrado** (nunca 403, que confirmaria a existência dela) |
| A12 | Eventos com envelope: `eventId`, `eventType`, `aggregateId` (= `walletId`), `correlationId`, `causationId?`, `occurredAt`, `version`, `data` |

---

## 6. ✅ CONCLUÍDA: FASE 7 — HTTP + autenticação

> **Estado: CONCLUÍDA E VALIDADA em 5 blocos + 1 pendência.** A descrição abaixo é
> o **registro** do que foi feito (o plano original), não uma tarefa pendente.
> Evidências completas em `docs/evidencias/FASE_7.md`.
>
> | Bloco | Commit | O que entregou |
> | --- | --- | --- |
> | composição | `ec6c014` | Fx com `infra` + `usecase`, `StopTimeout` da config (P7), readiness de Postgres e IdP (P5) |
> | **7a** | `1d43174` | `internal/infra/auth`: OIDC real (discovery, JWKS com cache/rotação, RS256, `alg` fixo, `kid` obrigatório, 401 × 503) |
> | **7b/7d** | `95e85bb` | os 7 endpoints do §9, DTOs, mapeamento de desfecho/erro em status, rotas por role (P2 e P4) |
> | **7e** | `343ec51` | integração com **Keycloak real** (token por `client_credentials`) e PostgreSQL; binário real |
> | **P1** | `31cf6b5` | `-health-check` para o healthcheck do compose |
>
> **8 defeitos reais corrigidos** (dois de código dormente desde a Fase 0, quatro de
> composição Fx, um de healthcheck, um de ordem de middleware e o **defeito 27**, o
> log de acesso saindo em texto enquanto o boot saía em JSON — que só apareceu ao
> **rodar o binário**) — lista completa no `ARCHITECTURE.md` §15 (defeitos 20 a 27)
> e no `FASE_7.md` §6.

### 6.1. O que a Fase 6 deixou pronto (não reimplementar)

`internal/app/wagering` tem o caso de uso COMPLETO. A Fase 7 é a borda: traduzir
HTTP em `Request`/`OpenWalletRequest` e o desfecho em resposta.

| Necessidade do handler | O que usar |
| --- | --- |
| Operação do provedor | `servico.Process(ctx, appwagering.Request{...})` → `Result` + erro |
| Abertura de carteira | `servico.OpenWallet(ctx, appwagering.OpenWalletRequest{...})` → `OpenWalletResult` |
| Leitura interna | `GetTransaction`, `GetWallet`, `ListLedger` |
| Leitura do provedor | `GetProviderTransaction(providerID, id)` e `GetByProviderExternalID(providerID, externalID)` — **já aplicam o isolamento** (operação de outro provedor = "não encontrado") |
| Reconciliação | `Reconcile(ctx, walletID)` → `Reconciliation` |
| Código de falha | `domainerrors.CodeOf(err)` — é `failureCode` no corpo de erro |
| Idempotência do corpo | `Request.IdempotencyKey` vem do header; o serviço recusa chave vazia com `IDEMPOTENCY_KEY_REQUIRED` |

`app/ports` ganhou `Clock` (implementação em `platform/clock.System`) e os
repositórios ganharam `GetByReferenceAndKind` e `CountByWallet`.

### 6.2. O que JÁ EXISTIA em `internal/httpapi` (a Fase 0 deixou pronto)

Foi tudo **estendido**, nada recriado:

| Já existe | Onde |
| --- | --- |
| Middlewares: `RequestID`, `RealIP`, `Recoverer`, `AccessLog`, `Timeout`, `MaxBody` | `middleware.go`, `recover.go` |
| `APIError` com construtores tipados (`ErrBadRequest`, `ErrFailureBadRequest`, `ErrUnauthorized`, `ErrForbidden`, `ErrNotFound`, `ErrConflict`, `ErrUnprocessable`, `ErrUnavailable` com `Retry-After`, `ErrInternal`) e `WriteError` | `response.go` |
| `decodeJSON` estrito (campo desconhecido, chave repetida, `Content-Type`) | `response.go` |
| `Routes{Public, Internal, Provider}` + interface `Autenticador` | `router.go` |
| `/health/live`, `/health/ready` (com `health_checkers`), `/metrics` | `health.go`, `router.go` |

O que faltava naquele momento — e o que cada bloco fez: o **`Autenticador` de
verdade** (7a), os **handlers** com DTOs e o **mapeamento desfecho→status** (7b), o
**módulo Fx do caso de uso** (composição) e as pendências **P1/P2/P4/P5/P7**.

### 6.3. O mapeamento desfecho → HTTP (é o que o avaliador cobra)

| Situação | Status | Corpo |
| --- | --- | --- |
| Operação nova concluída | `201` | `{transactionId,status,balance,idempotentReplay:false}` |
| Replay idempotente | `200` | idem com `idempotentReplay:true` — **saldo do processamento original** |
| `PENDING_REFERENCE` | `202` | `{transactionId,status:"PENDING_REFERENCE",failureCode:null}` |
| Rejeição de negócio | `422` | `{transactionId,status:"REJECTED",failureCode}` |
| Abertura de carteira | `201` | `{id,playerId,balance,version}` (+ `idempotentReplay` no reenvio) |
| Reconciliação | `200` | `{walletId,storedBalance,calculatedBalance,difference,consistent,checkedEntries}` |
| Ledger paginado | `200` | `{entries:[...],nextCursor}` |

Erros (`WriteError` + `domainerrors.CodeOf`):

| Erro | Status |
| --- | --- |
| `ErrIdempotencyConflict`, `ErrWalletAlreadyExists` | `409` |
| `ErrWalletNotFound`, `ErrTransactionNotFound` | `404` |
| Códigos de entrada inválida (`INVALID_REQUEST`, `IDEMPOTENCY_KEY_REQUIRED`, `INVALID_AMOUNT`, `REFERENCE_REQUIRED`, `INVALID_TRANSACTION`, `INVALID_CURRENCY`, cursor inválido) | `400` |
| `OPERATION_NOT_ALLOWED` (`OPENING` de fora) | `403` |
| `ports.IsRetryable(err)` (transitório esgotado) | `503` + `Retry-After` |
| Qualquer outro | `500` (sem vazar stack nem erro de driver) |

⚠️ **Nunca** mapear `error` de infraestrutura para `4xx`: a diferença entre
"o cliente errou" e "o serviço está indisponível" é contrato (README §9).

### 6.4. Autenticação e isolamento (A10 — é eliminatório)

- OIDC discovery + JWKS com cache e rotação; valida `iss`, `aud`, `exp`, `nbf`, `alg` `RS256`;
- **o `providerId` vem do TOKEN**, nunca do corpo: `POST /wagering/transactions` com
  `providerId` diferente do claim ⇒ `403`; `GET /providers/:providerId/...` idem;
- `POST /wallets`, `GET /wallets*`, `POST /wallets/:id/reconciliation` ⇒ role `internal`
  (provedor recebe `403` **sem efeito financeiro**);
- token ausente/inválido ⇒ `401`;
- IdP indisponível ⇒ `503` e **nenhum efeito financeiro** (o teste tem que conferir o banco);
- os endpoints públicos seguem públicos: `/health/live`, `/health/ready`, `/metrics`.

### 6.5. Pendências desta fase — ✅ TODAS RESOLVIDAS

| Pendência | Como ficou |
| --- | --- |
| **P1** | `cmd/api -health-check` existe, com a URL derivada de `HTTP_ADDR`; provado subindo o **binário real** (`TestBinarioRealHealthCheckEShutdown`) |
| **P4** | `POST /wallets` no caminho do enunciado; o mount `/internal/wallets` saiu |
| **P5** | Postgres **e IdP** no grupo `health_checkers` (o do IdP fura o cache de discovery de propósito). O SQS entra na Fase 8, junto com o cliente |
| **P2** | `AccessLog` passou a envolver o `Recoverer`: antes, o log registrava **200** para uma requisição que respondeu **500** (defeito 21 da §15) |
| **P7** | `StopTimeout` vem de `HTTP_STOP_TIMEOUT`, lido **antes** do grafo, e a config exige que ele cubra o `HTTP_SHUTDOWN_TIMEOUT` |

### 6.6. Fx — como ficou

- `fx.Module("infra")`: o pool (`*postgres.Pool`, com `Close` no `Lifecycle`),
  `ports.UnitOfWork` (alias de `postgres.NewUnitOfWork`) e o `*auth.Verifier`;
- `fx.Module("usecase")`: `ports.Clock`, `idgen.Generator` e `appwagering.New`;
- `fx.Module("auth")`: `*auth.Autenticador` + health checker do IdP no grupo;
- `fx.Module("httpapi")`: `*httpapi.Routes`, `*http.Server`, `*httpapi.Handlers`,
  o registro das rotas e o `runHTTPServer`;
- `app.ValidateApp()` valida o **grafo inteiro** sem subir nada — e agora ele cobre
  todos os módulos de produção.

Três armadilhas do Fx que custaram tempo e estão documentadas na §10 do
`ARCHITECTURE.md`: `Provide` recebe construtor (não valor) e casa por tipo **exato**
(interface × concreto); grupo é `fx.Annotate(fn, fx.ResultTags(...))` e o `group`
pertence ao **campo** de uma struct `fx.In`; e o pool precisa de
`context.Background()` com prazo, não do contexto de start.

### 6.7. Testes que a fase TEM — onde estão

| O que a fase precisava | Onde está |
| --- | --- |
| OIDC de teste **assinando de verdade** | `internal/infra/auth/oidc_testserver_test.go` (chave RSA de 2048 bits gerada no teste, JWKS servido localmente) |
| token ausente / inválido / expirado / assinatura de outra chave / role errada / provedor errado | `internal/infra/auth/{verifier,middleware}_test.go` (11 formas de token inválido com a causa exata) |
| **isolamento** entre provedores, inclusive em replay, e o corpo não escolhendo o provedor | `test/integration/http_integration_test.go` + dublês em `internal/httpapi/handlers_test.go` |
| **sem efeito financeiro** em acesso negado | `test/integration/...`: contagem de `wallets`, `wager_transactions` e `wallet_ledger_entries` antes/depois do 401/403/503 |
| contrato de status completo | `internal/httpapi/erros_test.go` (unitário) + 11 subtests de contrato na integração |
| IdP fora ⇒ `503` | unitário (verificador e middleware) e integração (com saldo e ledger conferidos) |
| **Keycloak real** (a prova principal que o README §13 pede) | `test/integration/...`: token por `client_credentials` nos três clients do realm |

### 6.8. Checkpoint que FOI executado

```powershell
$env:GOMODCACHE='E:\jungleg\.gocache\mod'; $env:GOCACHE='E:\jungleg\.gocache\build'; $env:GOPATH='E:\jungleg\.gocache\gopath'
docker compose up -d postgres
# migrations EMBUTIDAS no binário: rebuilde depois de criar/alterar migration
go build -o bin\migrate.exe .\cmd\migrate\
$env:DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungleg?sslmode=disable'
.\bin\migrate.exe up
go build ./... ; go vet ./... ; go vet -tags=integration ./... ; gofmt -l .\internal\ .\cmd\ .\migrations\ .\test\
go test -count=1 -race ./...
$env:TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
go test -count=1 -race -tags=integration -timeout 300s ./...
```

> `gofmt -l .` na raiz lista o cache de módulos (`.gocache/mod`) — formatar
> dependência de terceiros não é nosso. Por isso a checagem é escopada.

> **Nota para a próxima fase:** a integração da borda HTTP precisa do **Keycloak
> de pé**, porque o token é obtido de verdade (`client_credentials`). Sem ele, os
> testes são pulados — e aí a fase pareceria verde sem provar nada. Conferir
> `docker ps` antes de confiar no resultado.

Os commits que fecharam o bloco:
```
ec6c014 feat(app): compoe a aplicacao com Fx e configura o encerramento e o readiness
1d43174 feat(auth): adiciona autenticacao OIDC com validacao real de token e provider do token
95e85bb feat(http): adiciona os endpoints de wagering e carteira com autenticacao OIDC
343ec51 test(http): prova a fase 7 com o Keycloak e o PostgreSQL reais
31cf6b5 feat(api): adiciona a flag -health-check usada pelo healthcheck do compose
cc986df docs(fase7): registra as evidencias da Fase 7 e atualiza arquitetura e conformidade
```

---

## 7. PENDÊNCIAS CONHECIDAS

Do `CONTEXTO.md` §4: **P1 a P5 e P7 foram resolvidas na Fase 7** (P1 `-health-check`,
P2 ordem `AccessLog` × `Recoverer`, P4 `POST /wallets`, P5 health checkers, P7
`StopTimeout` da config). Continuam abertas:

- **P3** (`net.Listen` em vez de `select`+`time.After` no start do servidor — o
  `select` com `time.After` já detecta erro de bind, mas há uma janela de 50ms
  arbitrária; a alternativa é `net.Listen` explícito antes de servir);
- **P8** (testar SIGTERM real): **parcialmente resolvido** — o teste do binário real
  encerra o processo por sinal em Linux/macOS e confere o prazo; no Windows o
  `Kill` é terminação dura, então a prova de SIGTERM fica para o ambiente Linux
  (que é onde o container roda).

**Resolvidas:** P1, P2, P4, P5, P6, P7, `canonicaljson` criado, padding do `CHAR(3)`
tratado com `TrimSpace` e testado, `Movement` resolvendo a versão do UPDATE condicional,
`CodeOf` unificado em `domainerrors`, `wagering.Error` virou alias (defeito 13 da §15 do
`ARCHITECTURE.md`), `LOSS` com referência decidido (informativa — `ARCHITECTURE.md` §9 e
`FASE_6.md` §8), `chimw.RequestSize(0)` removido (defeito 20), ordem dos middlewares
corrigida (defeito 21).

**Abertas:**
- **`ARCHITECTURE.md` está atualizado até a Fase 7** — a §10 (HTTP e autenticação)
  saiu de `[PLANEJADO]` e está completa. Falta apenas a §11 (SQS — Fase 8) e a parte
  de observabilidade de negócio dos workers (Fases 8–9 e 11);
- `testcontainers-go` foi baixado e **não é usado** (os testes apontam para o Postgres do
  compose via `TEST_DATABASE_URL`). Remover com `go mod tidy` na Fase 12;
- **métricas de negócio** (`wager_operations_total{kind,status}`, `wager_idempotent_replays_total`)
  ficaram para a Fase 11 de propósito, para o caso de uso não nascer acoplado ao Prometheus —
  inclusive o **log e a métrica de divergência da reconciliação**;
- `migrate.exe` sobrando na raiz (além de `bin/migrate.exe`): lixo de build, só limpar;
- **`bin/migrate.exe` carrega as migrations embutidas** (`go:embed`): depois de criar ou alterar
  uma migration, é preciso rebuidá-lo, senão ele responde "schema já está atualizado" e não
  aplica nada (aconteceu na 000002);
- a ordem dos eventos na outbox **foi resolvida** (bloco 6d, migration 000003): a fila é lida
  por `sequence`, e o lote sai ordenado por ela. Ficou registrada em `ARCHITECTURE.md` §8 a
  armadilha que apareceu no caminho — **`RETURNING` não garante a ordem das linhas**, então o
  `UPDATE` do `ClaimBatch` virou CTE com `SELECT ... ORDER BY sequence` de fora;
- o **`wager_transactions` de `OPENING`** tem `game_id` e `round_id` vazios por decisão
  (migration 000002); a reversão dessa migration exige decidir o que fazer com essas linhas — a
  condição está documentada no próprio `.down.sql`.

---

## 8. CHECKLIST DE ARRANQUE PARA A NOVA CONVERSA (Fase 8 — SQS + inbox)

- [ ] Ler `README.md` (o enunciado é a fonte de verdade), `ARCHITECTURE.md` (§10 e §11), `PLANO.md` §3 (Fase 8), `docs/CONFORMIDADE_README.md` §10 e `docs/evidencias/FASE_7.md`
- [ ] Confirmar containers de pé (`docker ps`) — **incluindo o Keycloak**, que os testes da borda HTTP usam
- [ ] **Confirmar com o usuário que a estratégia mudou** (seção 3.1): a IA escreve o código e explica depois
- [ ] **Reusar** `appwagering.Process` — o consumidor de SQS compartilha o MESMO caso de uso do HTTP (é o que garante a equivalência das duas portas, README §10)
- [ ] Fluxo da inbox: `InsertIfAbsent` (`consumerName`, `messageId`) na **mesma transação** do tratamento durável (README §6.5); hash do corpo para distinguir reentrega de conflito real
- [ ] Remover a mensagem da fila **só depois do commit**; rejeição terminal remove; transitório não remove (backoff por visibility); permanente ou `ApproximateReceiveCount > MaxReceiveCount` vai para a DLQ
- [ ] `SIGTERM`: parar de buscar, concluir o lote no prazo, senão **liberar visibilidade** (`ChangeMessageVisibility 0`) para reentrega segura
- [ ] Registrar o **SQS no grupo `health_checkers`** (fecha a P5) e documentar `MessageGroupId` (`walletId`) e `MessageDeduplicationId`
- [ ] Testes de integração contra o **LocalStack real**: reentrega da mesma mensagem, hash divergente, rejeição terminal, transitório, mensagem inválida → DLQ, e o cenário cruzando **HTTP × SQS** para a mesma operação (prova a equivalência do hash)
- [ ] Aceitar contestação do usuário: se ele questionar e estiver certo, **admitir e corrigir**
- [ ] Depois de aprovado, commitar (um bloco por vez, com a mensagem contando a história)
- [ ] Lembrar do **compromisso final** (seção 3.5): `docs/SISTEMA_EXPLICADO.md` e `docs/ENTREVISTA.md`

**As Fases 0 a 7 estão commitadas e validadas. Não revalide nem recommite.**
