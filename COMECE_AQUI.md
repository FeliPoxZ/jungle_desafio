# COMECE_AQUI.md — prompt de abertura para a próxima conversa

> **Como usar:** copie o bloco da seção "PROMPT" e cole como primeira mensagem na
> conversa nova. Ele é autossuficiente. As seções seguintes são para consulta.

---

## PROMPT

```
Continuando o projeto E:\jungleg (desafio técnico back-end Go + Uber Fx +
PostgreSQL + Keycloak + SQS/LocalStack para vaga júnior).

Leia ANTES de responder, nesta ordem:
  1. PROXIMO_PASSO.md  (raiz)  — handoff completo: estado, método de trabalho,
                                 ambiente e a próxima tarefa pontual
  2. ARCHITECTURE.md   (raiz)  — arquitetura, decisões e problemas resolvidos
  3. PLANO.md §3               — checklist da Fase 6 em diante
  4. docs/evidencias/FASE_5.md — prova do que já funciona

Depois de ler, confirme que entendeu três coisas antes de escrever código:
  a) a ESTRATÉGIA mudou (PROXIMO_PASSO §3.1): a partir da Fase 6 a IA escreve o
     código e explica as partes importantes depois. O ritmo continua sendo um
     bloco coerente por vez, com commit ao final;
  b) existe um COMPROMISSO FINAL (PROXIMO_PASSO §3.5): ao fechar o sistema, gerar
     dois documentos — docs/SISTEMA_EXPLICADO.md e docs/ENTREVISTA.md. Por isso,
     explicar cada bloco AGORA é investir na entrega final;
  c) a próxima tarefa é a FASE 6: o caso de uso wagering.Service, com a ordem
     exata de 12 etapas descrita em PROXIMO_PASSO §6.2.

Regras que não mudam:
  - um bloco por vez, validado de verdade (build, vet, gofmt, testes e INTEGRAÇÃO
    contra o banco real quando a fase tocar persistência) e commitado ao final;
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
  - docker: containers jungleg-postgres, jungleg-keycloak, jungleg-localstack
  - banco de teste é jungle_test via TEST_DATABASE_URL (os testes truncam tabelas)

Comece confirmando os três itens (a, b, c) e me dizendo a sua leitura da Fase 6.
NÃO escreva código na primeira resposta.
```

---

## Por que este prompt está assim

| Trecho | O que ele evita |
| --- | --- |
| "Leia ANTES de responder" + lista de arquivos | A IA começar a codar por cima de decisões já congeladas e refazer o que existe |
| Confirmar (a) a mudança de estratégia | Ela voltar ao "MODO B" (me fazer digitar) ou, no outro extremo, cuspir 5 fases de uma vez |
| Confirmar (b) os dois documentos finais | Ela entregar código sem explicar, e no fim não haver base para montar `SISTEMA_EXPLICADO.md` e `ENTREVISTA.md` |
| Apontar `PROXIMO_PASSO §6.2` | Ela esquecer que o `SELECT ... FOR UPDATE` vem **antes** de ler o saldo |
| Ambiente do PowerShell | Repetir erros já pagos: acento corrompido pelo `Set-Content`, heredoc quebrando no commit |
| "NÃO escreva código na primeira resposta" | Ela pular a leitura e já partir para implementação |

---

## Contexto que a próxima conversa vai precisar (resumo)

**Onde estamos:** Fases 0–5 concluídas e commitadas. Domínio completo (574 casos,
`wagering` com 100% de cobertura), schema com 29 CHECK e 30 operações inválidas
recusadas pelo banco, e persistência `pgx` com Unit of Work validada contra
PostgreSQL real (40 testes, `-race` verde).

**Último commit:** `2e4c2c0 refactor(arquitetura): separa dominio, contrato e
infraestrutura` — reorganizou os pacotes e criou `internal/app/ports`.

**Estrutura:**
```
internal/domain/{errors,ids,money,ledger,wallet,wagering}   regra de negócio
internal/app/ports                                          contratos
internal/infra/postgres                                     adaptador do banco
internal/httpapi · internal/platform · internal/config · internal/observability
```

**O que já está pronto para a Fase 6 usar:** `uow.Run`, `wallet.Movement` (com
`VersionBefore()`), `ledger.NewEntry` (UUID próprio, `transactionID` obrigatório),
`canonicaljson.Hash`, os eventos do `wagering`, `idgen.New()`, e os classificadores
`ports.IsConflict` / `IsRetryable` / `IsNotFound`.

**Pendências que a próxima conversa deve manter em vista:** a rota `POST /wallets`
(hoje `/internal/wallets`), a flag `-health-check` do `cmd/api`, o `go mod tidy`
para remover `testcontainers-go` (baixado e não usado), e decidir se `LOSS` valida
referência quando ela vem preenchida.
