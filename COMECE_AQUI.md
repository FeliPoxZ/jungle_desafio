# COMECE_AQUI.md — prompt de abertura para a próxima conversa

> **Como usar:** copie o bloco da seção "PROMPT" e cole como primeira mensagem na
> conversa nova. Ele é autossuficiente. As seções seguintes são para consulta.

---

## PROMPT

```
Continuando o projeto E:\jungleg (desafio técnico back-end Go + Uber Fx +
PostgreSQL + Keycloak + SQS/LocalStack para vaga júnior).

⭐ O README.md DA RAIZ É A CABEÇA DE TUDO. Ele é o enunciado e o objetivo do
projeto: é por ele que se decide o que fazer e o que entregar. Sempre que houver
dúvida — de regra de negócio, de contrato, de status HTTP, de escopo — a resposta
está no README.md, e é ele que vale sobre qualquer plano, handoff ou opinião minha.
A tabela de conformidade com ele está em docs/CONFORMIDADE_README.md.

Leia ANTES de responder, nesta ordem:
  1. README.md                (raiz) — o ENUNCIADO. Fonte de verdade.
  2. PROXIMO_PASSO.md         (raiz) — handoff: estado, método, ambiente e a
                                       próxima tarefa pontual
  3. ARCHITECTURE.md          (raiz) — arquitetura, decisões e problemas resolvidos
  4. docs/CONFORMIDADE_README.md     — o que do enunciado já está atendido, o que
                                       falta e em que fase
  5. PLANO.md §3                     — checklist por fase
  6. docs/evidencias/FASE_6.md       — prova do que já funciona (o §11 é a correção
                                       da ordem da outbox)

Depois de ler, confirme que entendeu quatro coisas antes de escrever código:
  a) o README.md manda: em caso de conflito entre ele e qualquer documento interno,
     vale o enunciado;
  b) a ESTRATÉGIA (PROXIMO_PASSO §3.1): desde a Fase 6 a IA escreve o código e
     explica as partes importantes depois. O ritmo continua sendo UM BLOCO COERENTE
     por vez, com validação real e commit ao final;
  c) existe um COMPROMISSO FINAL (PROXIMO_PASSO §3.5): ao fechar o sistema, gerar
     docs/SISTEMA_EXPLICADO.md e docs/ENTREVISTA.md. Explicar cada bloco AGORA é
     investir nesses dois documentos;
  d) a próxima tarefa é a FASE 7 (HTTP + autenticação), descrita em PROXIMO_PASSO §6.

Regras que não mudam:
  - um bloco por vez, validado de verdade (go build, go vet, gofmt, go test -race e
    INTEGRAÇÃO contra o PostgreSQL real quando o bloco tocar persistência) e
    commitado ao final, com mensagem em arquivo (`git commit -F`);
  - comentário no código só onde explica DECISÃO, não o que o código faz;
  - se eu contestar e eu estiver certo, admita e corrija — sem inventar desculpa;
  - nunca modifique meus arquivos em silêncio;
  - ao terminar um bloco, me explique as partes importantes no formato:
    decisão | motivo | o que quebraria sem ela.

Ambiente (detalhes em PROXIMO_PASSO §4):
  - sempre exportar GOMODCACHE/GOCACHE/GOPATH para E:\jungleg\.gocache\*
  - PowerShell 5.1 (sem && nem ternário); make NÃO está instalado
  - mensagem de commit longa vai em ARQUIVO e usa `git commit -F` (heredoc quebra)
  - NUNCA usar Set-Content para escrever texto acentuado (grava ANSI e corrompe);
    usar a ferramenta de edição ou [System.IO.File]::WriteAllText com UTF8 sem BOM
  - `bin/migrate.exe` carrega as migrations EMBUTIDAS: rebuilde depois de criar
    migration, senão ele diz "schema já está atualizado" e não aplica nada
  - redirecionar arquivo para dentro do container precisa de cmd.exe:
    cmd /c "docker exec -i jungleg-postgres psql ... < arquivo.sql"
  - docker: containers jungleg-postgres, jungleg-keycloak, jungleg-localstack
  - banco de teste é jungle_test via TEST_DATABASE_URL (os testes truncam tabelas);
    os dois pacotes de integração se serializam por lock consultivo (sem -p 1)

Comece confirmando os quatro itens (a, b, c, d) e me dizendo a sua leitura da
Fase 7. NÃO escreva código na primeira resposta.
```

---

## Por que este prompt está assim

| Trecho | O que ele evita |
| --- | --- |
| "README.md é a cabeça de tudo" + `docs/CONFORMIDADE_README.md` | A IA decidir por conta própria algo que o enunciado já responde — e a entrega sair do escopo pedido |
| "Leia ANTES de responder" + lista de arquivos | Ela começar a codar por cima de decisões congeladas e refazer o que existe |
| Confirmar (b) a mudança de estratégia | Ela voltar ao "MODO B" (me fazer digitar) ou cuspir 5 fases de uma vez |
| Confirmar (c) os dois documentos finais | Ela entregar código sem explicar, e no fim não haver base para `SISTEMA_EXPLICADO.md` e `ENTREVISTA.md` |
| Ambiente (migrate embutido, `Set-Content`, `cmd /c`, lock do banco de teste) | Repetir erros já pagos nesta sessão |
| "NÃO escreva código na primeira resposta" | Ela pular a leitura e já partir para a implementação |

---

## Contexto que a próxima conversa vai precisar (resumo)

**Onde estamos:** Fases 0 a 6 concluídas e commitadas (mais o bloco 6d, que corrigiu
a ordem dos eventos na outbox). O **núcleo financeiro está pronto e provado contra
PostgreSQL real**: domínio (574 casos), schema com 29 CHECK / 4 FK / 3 índices únicos
parciais / 5 triggers, persistência `pgx` com Unit of Work, e o **caso de uso
completo** (`Process`, `OpenWallet`, leituras, `Reconcile`).

**Números:** 13 pacotes de unidade verdes com `-race`, 2 de integração; 56 casos no
caso de uso, 29 de integração; schema na versão **3**; 32 operações inválidas
recusadas pelo banco (+ 1 caso positivo).

**Últimos commits:**
```
0e89e65 fix(outbox): ordena a fila por sequencia e corrige a ordem do lote   ← 6d
52f16dc docs(handoff): atualiza PROXIMO_PASSO para a Fase 7 (HTTP e autenticacao)
21cf528 feat(app): adiciona leituras do caso de uso e reconciliacao em visao consistente  ← 6c
a397398 feat(app): adiciona abertura de carteira com lancamento de abertura e eventos      ← 6b
4f0ab9d feat(app): adiciona caso de uso de operacoes com idempotencia e ledger transacional ← 6a
```

**Estrutura:**
```
internal/domain/{errors,ids,money,ledger,wallet,wagering}   regra de negócio (sem I/O)
internal/app/ports                                          contratos (Repos, UnitOfWork, Clock, Cursor)
internal/app/wagering                                       caso de uso (Process, OpenWallet, leituras, Reconcile)
internal/infra/postgres                                     adaptador do banco
internal/httpapi · internal/platform · internal/config · internal/observability
test/integration                                            integração do caminho completo
```

**A Fase 7 vai ESTENDER `internal/httpapi`, não recriar:** já existem os middlewares
(`RequestID`, `RealIP`, `Recoverer`, `AccessLog`, `Timeout`, `MaxBody`), o
`APIError` com construtores tipados + `WriteError` + `decodeJSON` estrito, o
`Routes{Public, Internal, Provider}` com a interface `Autenticador`, e
`/health/live`, `/health/ready` e `/metrics`. Falta: o `Autenticador` de verdade
(Keycloak), os handlers com DTOs, o mapeamento desfecho→status (tabela pronta em
`PROXIMO_PASSO.md` §6.3), o módulo Fx do caso de uso e as pendências P1/P2/P4/P5/P7.

**O que a Fase 7 tem de resolver com atenção (está detalhado no §6 do handoff):**
1. **autenticação é eliminatório** no enunciado (§14);
2. o `providerId` vem do **token**, nunca do corpo;
3. operação de outro provedor responde **não encontrado** (nunca 403, que confirmaria
   que ela existe);
4. **P1**: o healthcheck do `api` no compose usa `-health-check`, flag que o binário
   não tem — então `docker compose up --build` (exigido no enunciado §15) sobe o
   `api` como `unhealthy` até isso ser corrigido;
5. **P4**: a rota hoje está montada em `/internal/wallets`, e o enunciado pede
   `POST /wallets`;
6. os testes de autenticação devem **preferir o Keycloak do container** (o enunciado
   §13 pede IdP em container real).

**⭐ Exigência do usuário para o fim do projeto (registrada no `PLANO.md` §3 e no
`PROXIMO_PASSO.md` §3.5):** os dois documentos finais precisam explicar **do básico
ao avançado, sem presumir vocabulário** — o que é dinheiro em unidades mínimas, o
que é carteira, o que é lançamento de ledger, o que é transação e lock, o que é
idempotência, o que é outbox — e só **depois** as ligações entre as partes e os
conceitos de regra de negócio (por que `LOSS` não movimenta, por que `ROLLBACK` de
`REFUND` debita, por que o replay devolve o saldo original).
