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
> 6. `docs/evidencias/FASE_1.md` a `FASE_8.md` — provas do que já funciona
> 7. `docs/LEDGER_VS_WALLET.md` — a distinção central do domínio financeiro
>
> **Não comece a codar sem ler as seções 3 e 3.5.**
>
> ⭐ **A próxima fase é a 9 (publisher da outbox + workers de referência e reaper).**
> O prompt de abertura dela está pronto em **`PROMPT_FASE_9.md`** — autossuficiente,
> com o escopo, os quatro blocos, os critérios de aceite e as armadilhas. Use-o como
> primeira mensagem da nova conversa; este arquivo continua sendo o handoff completo.

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
| Fase 8 (SQS + inbox) | ✅ **CONCLUÍDA E VALIDADA** em 2 blocos (8a/8b e 8e) — **26 casos** em `internal/app/inbox`, **19** em `internal/consumer`, **9 funções** de integração com o **LocalStack real**; **8 defeitos reais corrigidos** (um de ambiente: as filas nunca eram criadas; três que só apareceram ao rodar o binário) |
| Fase 9 (publisher da outbox + worker de referências) | ⬅ **PRÓXIMA** |
| Fases 10–12 | ⬜ Não iniciadas (ver checklist no `PLANO.md` §3) |

**Domínio (Fases 1–3): 574 casos. Persistência (Fase 5): 38. Caso de uso (Fase 6):
55 unitários + 27 de integração. Borda HTTP (Fase 7): 116 unitários + 32 de
integração. Consumo por SQS (Fase 8): 45 unitários + 9 funções de integração.
`go test -race` verde em 16 pacotes com teste de unidade (17 no comando com a tag
`integration`). Árvore git limpa.**

> ⚠️ Os **três** pacotes de integração usam o mesmo banco de teste e truncam
> tabelas. Cada `TestMain` toma um lock consultivo (`pg_advisory_lock`) e espera a
> vez, então `go test -tags=integration ./...` funciona **sem** `-p 1`.
>
> ⚠️ Os testes de integração da **borda HTTP** precisam do **Keycloak de pé**: o
> token é obtido por `client_credentials` no realm `jungleg` (nada é forjado). Sem
> Keycloak, eles são **pulados** com aviso.
>
> ⚠️ Os testes de **consumo** precisam do **LocalStack com as filas criadas**. Se
> `docker exec jungleg-localstack awslocal sqs list-queues` voltar vazio, o script
> de init falhou e os testes vão falhar por isso — **não** por defeito de código
> (foi o que aconteceu até a Fase 8: o script não tinha shebang nem bit de
> execução, e o LocalStack registrava `Exec format error` no stage READY).
>
> ⚠️ Os testes da Fase 9 vão precisar **também** da `wallet-events.fifo` (o destino
> da publicação). Ela já é provisionada pelo mesmo script — a checagem do
> `list-queues` cobre as quatro filas.

### Histórico de commits (mais recente primeiro)

```
aec7cbe docs(fase8): corrige a contagem de pacotes e de defeitos no handoff e na arquitetura
ec551b2 fix(sqs): corrige tres defeitos que so apareceram ao rodar o binario
8a69ab9 docs(fase8): registra as evidencias do consumo por SQS e atualiza os documentos
eaf52a0 test(sqs): prova o consumo com LocalStack real e corrige dois defeitos de identidade  ← FASE 8e
c81e7de feat(inbox): adiciona o consumo por SQS com inbox na mesma transacao do tratamento     ← FASE 8a/8b
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

> ✅ **As Fases 0 a 8 estão commitadas e validadas. Não revalide nem recommite.**

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
docker compose up -d postgres keycloak localstack
$env:DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungleg?sslmode=disable'
$env:TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
go build -o bin\migrate.exe .\cmd\migrate\   # migrations EMBUTIDAS: recompilar
.\bin\migrate.exe up

go test -count=1 -race ./...

# integração: banco separado, IdP REAL e SQS REAL
$env:OIDC_ISSUER='http://localhost:8080/realms/jungleg'
$env:OIDC_DISCOVERY_URL='http://localhost:8080/realms/jungleg'
$env:OIDC_AUDIENCE='jungleg-api'
$env:SQS_ENDPOINT='http://localhost:4566'
docker exec jungleg-localstack awslocal sqs list-queues   # as filas precisam existir
go test -count=1 -race -tags=integration -timeout 300s ./...
```

### 4.4. Line endings e outras ferramentas

- Scripts shell, SQL e Dockerfile rodam em Linux nos containers: garantir **LF** (o
  `.gitattributes` já normaliza) **e o bit de execução** — o LocalStack **executa**
  os scripts de `init/ready.d`, e sem shebang + `chmod +x` ele falha com
  `Exec format error` (foi o defeito 28, e as filas nunca eram criadas);
- Go **1.26.5** · PowerShell **5.1** (sem `&&`, sem ternário) · git 2.52.0
- **`make` NÃO está instalado** — os alvos do Makefile são documentação executável
- **`package main` não pode ser importado nem testado** → toda lógica em `internal/`
- **`bin/migrate.exe` carrega as migrations EMBUTIDAS** (`go:embed`): depois de
  criar ou alterar uma migration é preciso **recompilá-lo**, senão ele responde
  "schema já está atualizado" e não aplica nada;
- Redirecionar arquivo para dentro do container precisa de `cmd.exe`:
  `cmd /c "docker exec -i jungleg-postgres psql ... < arquivo.sql"`;
- **`docker exec` + `awslocal`**: use `2>&1 | Select-String ...`; o PowerShell
  trata o stderr do comando como erro e o exit code fica 1 mesmo quando o comando
  funcionou.

### 4.5. As filas do SQS (checagem obrigatória antes de confiar em teste verde)

```powershell
docker exec jungleg-localstack awslocal sqs list-queues
```

Se voltar **vazio**, o script de provisionamento não rodou — e os testes de
consumo vão falhar (ou pior: passar sem exercitar nada). Para provisionar na mão:

```powershell
docker compose run --rm localstack-init
# ou, dentro do container:
docker exec jungleg-localstack sh /etc/localstack/init/ready.d/init-queues.sh
```

As filas esperadas: `wager-transactions.fifo`, `wager-transactions-dlq.fifo`,
`wallet-events.fifo`, `wallet-events-dlq.fifo`.

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
## 6. ✅ CONCLUÍDA: FASE 7 — HTTP + autenticação (registro)

> **Estado: CONCLUÍDA E VALIDADA em 5 blocos + 1 pendência.** Evidências em
> `docs/evidencias/FASE_7.md`; as decisões estão no `ARCHITECTURE.md` §10.

| Bloco | Commit | O que entregou |
| --- | --- | --- |
| composição | `ec6c014` | Fx com `infra` + `usecase`, `StopTimeout` da config (P7), readiness de Postgres e IdP (P5) |
| **7a** | `1d43174` | `internal/infra/auth`: OIDC real (discovery, JWKS com cache/rotação, RS256, `alg` fixo, `kid` obrigatório, 401 × 503) — **sem dependência nova** |
| **7b/7d** | `95e85bb` | os 7 endpoints do §9, DTOs, mapeamento de desfecho/erro em status, rotas por role (P2 e P4) |
| **7e** | `343ec51` | integração com **Keycloak real** (token por `client_credentials`) e PostgreSQL; binário real |
| **P1** | `31cf6b5` | `-health-check` para o healthcheck do compose |

O que ficou pronto e é usado pela Fase 8: `Autenticador` (OIDC), `Routes` com
`Authenticator`, `processing de desfecho→status`, `Handlers` com `WageringService`,
health checkers no grupo do Fx, e o logger único por processo.

---

## 6b. ✅ CONCLUÍDA: FASE 8 — consumo do SQS (o que acabou de ser feito)

> **Estado: CONCLUÍDA E VALIDADA em 2 blocos.** Evidências em
> `docs/evidencias/FASE_8.md`; as decisões estão no `ARCHITECTURE.md` §11.

| Bloco | Commit | O que entregou |
| --- | --- | --- |
| **8a/8b** | `c81e7de` | ganchos transacionais no caso de uso, `internal/app/inbox`, `internal/consumer`, `internal/infra/sqs`, migration 000004 (`inbox.attempts`), módulo Fx `worker`, readiness do SQS, e a correção do script de filas (shebang + bit de execução) |
| **8e** | `eaf52a0` | integração contra o **LocalStack real** (9 funções) e a correção dos dois defeitos de identidade da mensagem |

**O caminho, em uma frase por peça:**

```
internal/infra/sqs    → protocolo do broker (receber/apagar/adiar/enviar em lote)
internal/app/inbox    → tradução (envelope → Request, régua estrita) + política de dedup
internal/consumer     → laço, semáforo, retry por visibilidade, DLQ, encerramento
app/wagering          → o MESMO caso de uso do HTTP, com ganchos que registram a inbox
```

**O que a próxima fase (9) vai usar daqui:**

- o **consumidor** é o modelo do loop de worker (contexto cancelável, semáforo,
  encerramento com prazo e liberação de trabalho);
- o **readiness** já aceita um checker novo: registrar o worker de referências é
  `fx.Provide` + `group:"health_checkers"`;
- a **outbox** (`ClaimBatch`, lease, `MarkRetry`, `MarkPublished`) está pronta e
  provada no repositório desde a Fase 5/6d — falta o loop que publica;
- a tabela `inbox_messages` já tem `attempts` e os status `RECEIVED`/`PROCESSED`/
  `REJECTED`/`FAILED`;
- `wager_retries_total{stage}`, `wager_dlq_messages_total` e
  `wager_outbox_publish_total{result}` já existem como métricas.

### 6b.1. Os dois defeitos de identidade da mensagem (para não repetir)

1. **A identidade durável do consumo é o `MessageId` DO BROKER**, não o
   `messageId` do envelope: quem atribui esse valor é o broker, no envio, e o
   produtor não tem como conhecê-lo antes. O campo do envelope é **opcional** e,
   quando presente, é conferido contra o da entrega (divergência ⇒ mensagem
   inválida, permanente).
2. **O `return operacao{}, nil` que abortava o parsing** (introduzido na correção
   do item 1) devolvia requisição VAZIA com erro `nil` — o sintoma era
   `IDEMPOTENCY_KEY_REQUIRED` para um corpo que TINHA a chave. **Lição:** o teste
   de integração diz QUE algo está errado; o teste de unidade com o corpo REAL
   entregue pelo broker diz ONDE. O arquivo `internal/app/inbox/payload_real_test.go`
   existe por isso.

### 6b.2. Armadilhas do LocalStack que custaram tempo

- **`FIFO` + `MessageGroupId` repetido = mensagem bloqueada.** Uma mensagem em voo
  (recebida e não removida) **bloqueia as seguintes do mesmo grupo** até o
  visibility timeout. O sintoma é `Receive` devolvendo **zero** com mensagem
  parada na fila — nada parecido com a causa. Testes devem usar um grupo por
  mensagem;
- **`MessageDeduplicationId` repetido entre execuções**: o SQS deduplica por 5
  minutos e devolve o `MessageId` da mensagem ORIGINAL (já consumida). Use um
  nonce;
- **visibility timeout**: a fila do compose usa 60s; o ambiente de TESTE usa 2s
  para a suíte não ficar refém dos próprios resíduos;
- **as filas precisam existir**: `docker exec jungleg-localstack awslocal sqs
  list-queues`. Vazio = o script de init falhou, e nenhum teste de consumo tem
  validade;
- **`localstack/localstack:latest` é Pro** e morre com "License activation
  failed": a versão está **pinada** em `4.0.3` no compose;
- **montar JSON com aspas dentro de `docker exec … awslocal sqs send-message`
  CORROMPE o corpo** (o primeiro envio ponta a ponta chegou como
  `invalid character 't' …` e foi, corretamente, para a DLQ). Envie lendo de um
  ARQUIVO copiado para dentro do container: `docker cp` + `BODY=$(cat arquivo)`.

### 6b.3. Os três defeitos que SÓ apareceram ao rodar o binário

O teste de integração exercita o consumidor **embutido no processo de teste**. Subir
o `bin/api.exe` com `SQS_CONSUME_TRANSACTIONS=true` e enviar mensagem pela fila
revelou três coisas que nenhum teste via — e é por isso que a Fase 9 exige a mesma
prova:

| # | Defeito | Sintoma |
| --- | --- | --- |
| 33 | **O default do `HTTP_STOP_TIMEOUT` (25s) violava a própria regra de validação** (20s do servidor + 15s do consumidor) | ligar o consumidor fazia a aplicação **não subir**: "configuração inválida" num ambiente correto |
| 34 | A métrica `wager_operations_total` **não era emitida no caminho SQS** | o caminho principal do serviço invisível no painel |
| 35 | O contador durável de entregas (`inbox.attempts`) **nunca incrementava** | `attempts = 0` para sempre; a reentrega não tocava no contador |

A prova ponta a ponta que os pegou (e que a Fase 9 deve repetir, agora para a
publicação):

```
carteira 1000.00 (pela API) → mensagem na fila → saldo 97500
ledger: CREDIT 100000 + DEBIT 2500 · inbox PROCESSED
reconciliação 975.00/975.00/dif 0.00/consistent/entries 2
métrica jungleg_wager_operations_total{kind="BET",status="PROCESSED"} 1
readiness: {"oidc":"UP","postgres":"UP","sqs":"UP"}
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
- **`ARCHITECTURE.md` está atualizado até a Fase 8** — as §10 (HTTP e autenticação)
  e §11 (consumo do SQS) estão completas. Falta a parte de workers (publisher da
  outbox e worker de referências — Fase 9) e a observabilidade de negócio (Fase 11);
- `testcontainers-go` foi baixado e **não é usado** (os testes apontam para o Postgres do
  compose via `TEST_DATABASE_URL`). Remover com `go mod tidy` na Fase 12;
- **métricas de negócio**: 7 das 12 já são emitidas (4 pela borda HTTP, 3 pelo
  consumidor). Conflitos, atraso da outbox e idade do pendente entram nas Fases 9–11;
- `migrate.exe` sobrando na raiz (além de `bin/migrate.exe`): lixo de build, só limpar;
- **`bin/migrate.exe` carrega as migrations embutidas** (`go:embed`): depois de criar ou alterar
  uma migration, é preciso rebuidá-lo, senão ele responde "schema já está atualizado" e não
  aplica nada (aconteceu na 000002 e de novo na 000004);
- a ordem dos eventos na outbox **foi resolvida** (bloco 6d, migration 000003): a fila é lida
  por `sequence`, e o lote sai ordenado por ela. Ficou registrada em `ARCHITECTURE.md` §8 a
  armadilha que apareceu no caminho — **`RETURNING` não garante a ordem das linhas**, então o
  `UPDATE` do `ClaimBatch` virou CTE com `SELECT ... ORDER BY sequence` de fora;
- o **`wager_transactions` de `OPENING`** tem `game_id` e `round_id` vazios por decisão
  (migration 000002); a reversão dessa migration exige decidir o que fazer com essas linhas — a
  condição está documentada no próprio `.down.sql`;
- **a URL da fila é derivada do endpoint** (`<endpoint>/000000000000/<nome>`) para o
  LocalStack. Em produção (sem `SQS_ENDPOINT`) o que sobra é o NOME, e a resolução
  teria de vir do `GetQueueUrl` do SDK — não exercitado (limitação registrada no
  `ARCHITECTURE.md` §16);
- **sem prova de ordem por carteira no consumo**: os testes usam um
  `MessageGroupId` por mensagem (para não ficarem reféns dos próprios resíduos), o
  que **não** exercita a ordem dentro do grupo. A ordem é garantida pela fila FIFO +
  `aggregateId = walletId` dos eventos; a prova de ordem por carteira é a Fase 10.

---

## 8. CHECKLIST DE ARRANQUE PARA A NOVA CONVERSA (Fase 9)

> ⭐ **O prompt de abertura da Fase 9 está em `PROMPT_FASE_9.md`** — autossuficiente,
> com o escopo do §11 do README, os quatro blocos, os critérios de aceite, as
> armadilhas de ambiente e o formato da primeira resposta esperada. Use-o como
> primeira mensagem; o checklist abaixo é o resumo operacional.

- [ ] Ler `README.md` (**§11, §12, §13 e §14 — a fase**; o enunciado é a fonte de verdade), `ARCHITECTURE.md` (§8 ordem da outbox, §11 consumo, §15 defeitos, §16 limitações), `docs/CONFORMIDADE_README.md` §11, `PLANO.md` §3 (Fase 9) e `docs/evidencias/FASE_8.md`
- [ ] Confirmar containers de pé (`docker ps`) e **as QUATRO filas** (`awslocal sqs list-queues`) — sem `wallet-events.fifo` o publisher não tem para onde publicar
- [ ] **Confirmar com o usuário que a estratégia continua** (seção 3.1): a IA escreve o código e explica depois
- [ ] **9a** `worker.OutboxPublisher` + a porta `Publicador` e o adaptador SQS: `ClaimBatch` → publicar → `MarkPublished`; falha → `MarkRetry` com backoff exponencial + teto + jitter; **publicar ANTES de marcar**; `MarkPublished` com `context.WithoutCancel`
- [ ] **9b** `worker.ReferenceWorker`: `ListPendingReferencesDue`, reconstruir o `Request` das colunas persistidas, `resolverReferencia`, e ramificar em resolvida (movimento + eventos) / pendente (attempt+1 + backoff + evento) / rejeitada / expirada (`REFERENCE_NOT_FOUND`). Mais o `PendingReaper` para `PENDING` antigo
- [ ] **9c** `worker.Runner` com `errgroup` (publisher + referências + reaper conforme a config), backoff em falha de laço, shutdown limpo, registro no Fx e no grupo `health_checkers`, e a validação de que ligar worker com os DEFAULTS não impede o boot (defeito 33)
- [ ] **9d** Testes: publicar de verdade em `wallet-events.fifo` e ler de volta; crash entre commit e publish; crash entre publish e `MarkPublished` **republicando com o mesmo `eventId`**; dois publishers disputando a mesma outbox; lease expirado recuperado; `REFUND` antes da referência resolve depois; expiração vira `REJECTED`; reaper retoma `PENDING` antigo
- [ ] Métricas do §12 desta fase: atraso da outbox (`PendingStats`), resultados de publicação, retries, conflitos e idade do pendente mais antigo
- [ ] **Prova pelo binário real** com os workers ligados (operação → evento na outbox → publicado na fila) — foi assim que apareceram 3 dos 8 defeitos da Fase 8
- [ ] Aceitar contestação do usuário: se ele questionar e estiver certo, **admitir e corrigir**
- [ ] Depois de aprovado, commitar (um bloco por vez, com a mensagem contando a história)
- [ ] Atualizar `docs/evidencias/FASE_9.md`, `ARCHITECTURE.md`, `docs/CONFORMIDADE_README.md`, `PROXIMO_PASSO.md`, `PLANO.md` e `.env.example`
- [ ] Lembrar do **compromisso final** (seção 3.5): `docs/SISTEMA_EXPLICADO.md` e `docs/ENTREVISTA.md`

**As Fases 0 a 8 estão commitadas e validadas. Não revalide nem recommite.**
