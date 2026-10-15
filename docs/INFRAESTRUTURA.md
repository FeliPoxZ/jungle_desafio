# INFRAESTRUTURA — O que são essas peças e por que elas existem

> Este documento responde **"o que é isso?"** antes de qualquer configuração.
> Se você abriu o `docker-compose.yml` e não entendeu por que tem 4 serviços estranhos ali,
> **este é o arquivo certo**. Leia ele **antes** do `GUIA_FASE_0.md` §5.
>
> Nada aqui é configuração. Aqui é conceito: qual problema cada peça resolve.

---

## ÍNDICE

1. [A resposta curta: 4 peças, 4 problemas](#1-a-resposta-curta-4-peças-4-problemas)
2. [PostgreSQL — onde a verdade mora](#2-postgresql--onde-a-verdade-mora)
3. [Keycloak — quem é você e o que pode fazer](#3-keycloak--quem-é-você-e-o-que-pode-fazer)
4. [SQS — a fila de mensagens](#4-sqs--a-fila-de-mensagens)
5. [LocalStack — a AWS de mentira (mas de verdade)](#5-localstack--a-aws-de-mentira-mas-de-verdade)
6. [Docker e Docker Compose — a caixa e o maquinista](#6-docker-e-docker-compose--a-caixa-e-o-maquinista)
7. [Os 4 padrões que amarram tudo](#7-os-4-padrões-que-amarram-tudo)
8. [Variáveis de ambiente — por que não escrever no código](#8-variáveis-de-ambiente--por-que-não-escrever-no-código)
9. [Onde cada peça toca o código](#9-onde-cada-peça-toca-o-código)
10. [Glossário](#10-glossário)

---

## 1. A resposta curta: 4 peças, 4 problemas

O desafio pede um sistema que movimenta **dinheiro de verdade** (bem, de mentira, mas com as garantias de verdade). Dinheiro exige três coisas que um programa simples **não** consegue dar sozinho:

| Problema | Pergunta que ele levanta | Peça que resolve |
| --- | --- | --- |
| **Durabilidade** | "Se o processo morrer agora, o dinheiro movimentado se perde?" | **PostgreSQL** |
| **Identidade** | "Quem está mandando essa ordem? Tem autorização?" | **Keycloak** |
| **Entrega confiável** | "Como garantir que essa operação chegue mesmo se a rede falhar?" | **SQS** |

E a quarta peça existe porque a terceira é um serviço **pago da Amazon**, e você não quer pagar (nem depender de internet) para desenvolver:

| Problema | Peça que resolve |
| --- | --- |
| "Como testar SQS sem conta na AWS?" | **LocalStack** |

### O desenho geral

```
                    ┌──────────────────────────────────────┐
   Provedor de      │                                      │
   jogos (ex.:      │        NOSSA APLICAÇÃO (Go)          │
   fortune-chimp)   │                                      │
        │           │  ┌────────────┐   ┌───────────────┐  │
        │  1. HTTP  │  │  API HTTP  │   │   Workers     │  │
        └──────────►│  │ (recebe)   │   │ (processam em │  │
                    │  └─────┬──────┘   │  segundo      │  │
                    │        │          │  plano)       │  │
                    │        ▼          └───────┬───────┘  │
                    │  ┌──────────────────┐     │          │
                    │  │ REGRAS DE NEGÓCIO│◄────┘          │
                    │  │ (o domínio)      │                │
                    │  └───────┬──────────┘                │
                    └──────────┼───────────────────────────┘
                               │
        ┌──────────────────────┼──────────────────────┐
        │                      │                      │
        ▼                      ▼                      ▼
  ┌───────────┐         ┌───────────┐          ┌───────────┐
  │POSTGRESQL │         │ KEYCLOAK  │          │    SQS    │
  │           │         │           │          │           │
  │ "o dinheiro          │ "quem é   │          │ "enfileira│
  │  está aqui,          │  você?"   │          │  as       │
  │  para sempre"        │           │          │  ordens"  │
  └───────────┘         └───────────┘          └───────────┘
        ▲                                            ▲
        │                                            │
        └──────────── ambos rodando no ──────────────┘
                      LOCALSTACK / DOCKER
```

---

## 2. PostgreSQL — onde a verdade mora

### 2.1. O que é

**PostgreSQL** (ou "Postgres") é um **banco de dados relacional**: um programa cujo único trabalho é guardar dados em disco de forma organizada, e permitir consultá-los depois — mesmo que o seu programa tenha sido desligado no meio do caminho.

"Relacional" significa que os dados ficam em **tabelas** com linhas e colunas, como uma planilha, e que você pode **relacionar** tabelas entre si:

```
tabela: wallets
┌──────────────────────┬──────────────┬───────────────┐
│ id (UUID)            │ player_id    │ balance_minor │
├──────────────────────┼──────────────┼───────────────┤
│ 0192f291-27dd-...    │ 0192f28f-... │ 100000        │  ← R$ 1000,00 em centavos
└──────────────────────┴──────────────┴───────────────┘

tabela: wallet_ledger_entries  (o extrato)
┌──────────────────────┬──────────────────────┬───────────┬──────────────┐
│ id                   │ wallet_id            │ direction │ amount_minor │
├──────────────────────┼──────────────────────┼───────────┼──────────────┤
│ 0192f2a0-...         │ 0192f291-27dd-...    │ CREDIT    │ 100000       │
│ 0192f2b1-...         │ 0192f291-27dd-...    │ DEBIT     │ 2500         │
└──────────────────────┴──────────────────────┴───────────┴──────────────┘
                              ▲
                              └── aponta para a carteira (é isso que é "relacional")
```

### 2.2. Por que NÃO guardar em memória ou num arquivo JSON

Imagine que o saldo da carteira ficava numa variável Go:

```go
saldo := int64(100000)  // R$ 1000,00
```

**Três problemas fatais:**

1. **Desligou, perdeu.** Reiniciar o processo zera tudo. O enunciado exige *"idempotência deve sobreviver ao reinício de todos os processos"*.
2. **Duas instâncias não conversam.** O teste obrigatório exige **3 processos independentes** movimentando as **mesmas** carteiras. Cada um teria a sua própria variável `saldo`, e os três divergiriam.
3. **Sem garantias.** Não existe "ou tudo ou nada": se o programa morrer no meio de um débito, você não sabe se ele aconteceu.

**Postgres resolve os três.** Ele é um **processo separado**, durável (grava em disco), e compartilhado por todas as instâncias da sua aplicação.

### 2.3. As duas palavras que você PRECISA saber: transação e ACID

**Transação** = um bloco de operações que o banco trata como **uma coisa só**.

```
BEGIN;
  UPDATE wallets SET balance_minor = 97500 WHERE id = '...';   ┐
  INSERT INTO wallet_ledger_entries (...) VALUES (...);        ├─ tudo junto
  INSERT INTO outbox_events (...) VALUES (...);                ┘
COMMIT;   ← só aqui "vale para sempre"
```

Se algo falhar no meio, ou o programa morrer antes do `COMMIT`:
```
ROLLBACK   ← o banco desfaz TUDO. Como se nada tivesse acontecido.
```

⭐ **A propriedade se chama ATOMICIDADE** — a primeira letra do acrônimo **ACID**:

| Letra | Nome | Significado em português |
| --- | --- | --- |
| **A** | Atomicidade | Ou tudo acontece, ou nada acontece |
| **C** | Consistência | As regras que você definiu são sempre respeitadas (ex.: saldo nunca negativo) |
| **I** | Isolamento | Uma transação não vê o "meio" de outra em andamento |
| **D** | Durabilidade | Depois do `COMMIT`, está salvo — mesmo se a energia cair |

> 🎯 **Na entrevista:** quando perguntarem *"como você garante que não houve movimentação duplicada?"*, parte da resposta é: **"a movimentação e o registro no ledger estão na MESMA transação, então é impossível um existir sem o outro."**

### 2.4. Constraints — regras que o BANCO faz cumprir

Uma **constraint** (restrição) é uma regra declarada na tabela que o banco **rejeita** se for violada. É a diferença entre "meu código lembra de checar" e "é impossível acontecer".

```sql
ALTER TABLE wallets
  ADD CONSTRAINT wallets_balance_non_negative CHECK (balance_minor >= 0);
```

Agora, se **qualquer** coisa — seu código, um script, um bug futuro, um estagiário com `psql` — tentar deixar o saldo negativo, o Postgres devolve erro e **recusa**.

⭐ **Por que isso é exigência do enunciado (item 5.3):** *"As invariantes financeiras devem ser garantidas no banco, independentemente de locks locais"*. Ou seja: mesmo que você erre a lógica no Go, o banco impede o desastre. É uma rede de segurança no último andar.

**Os três tipos de constraint que vamos usar:**

| Constraint | O que faz | Exemplo nosso |
| --- | --- | --- |
| `CHECK` | Valida uma condição | `balance_minor >= 0` (saldo não negativo) |
| `UNIQUE` | Impede duplicata | `(provider_id, idempotency_key)` — mesma chave duas vezes |
| `FOREIGN KEY` | Garante que a referência existe | lançamento do ledger aponta para carteira que existe |

### 2.5. O problema do "lost update" — preste atenção, isso cai em entrevista

Carteira com **R$ 100,00**. Chegam **duas** apostas de **R$ 80,00** ao mesmo tempo, em instâncias diferentes.

**Sem proteção nenhuma:**

```
Instância A                    Instância B                  Banco
────────────────────────────────────────────────────────────────────
SELECT saldo → 100             │                            balance = 100
                               SELECT saldo → 100           balance = 100
100 - 80 = 20                  │                            balance = 100
                               │  100 - 80 = 20             balance = 100
UPDATE balance = 20            │                            balance = 20
                               UPDATE balance = 20          balance = 20
                               │
RESULTADO: saldo 20, DUAS apostas aceitas de 80 = 160 debitados de 100!
           ❌ Isso é saldo negativo disfarçado. ELIMINATÓRIO.
```

Isso se chama **lost update** (atualização perdida): a segunda escrita **apagou** o efeito da primeira. As duas transações leram o mesmo valor inicial.

**Solução 1 — `SELECT ... FOR UPDATE` (lock pessimista):**

```
Instância A                    Instância B                  Banco
────────────────────────────────────────────────────────────────────
BEGIN                          │
SELECT ... FOR UPDATE ─────────┼──────────────────────────► TRAVA a linha!
  → saldo 100                  │                            (B espera)
UPDATE balance = 20            │   BEGIN                   
COMMIT ────────────────────────┼──────────────────────────► libera a trava
                               │   SELECT ... FOR UPDATE → saldo 20
                               │   20 - 80 = -60 ❌ saldo insuficiente!
                               │   ROLLBACK
                               │
RESULTADO: 1 aposta processada, 1 rejeitada, saldo 20. ✅ CORRETO.
```

⭐ `FOR UPDATE` = *"tranque esta linha até eu terminar; quem quiser mexer nela, espere na fila"*. Isso é o **lock pessimista**. Ele resolve, mas serializa: B teve que **esperar** A terminar.

**Como o desafio exige isso:** o item 8 diz literalmente: *"uma carteira com 100.00 BRL recebe, ao mesmo tempo, duas apostas distintas de 80.00 BRL. O resultado deve conter uma aposta processada, uma rejeição por saldo insuficiente, saldo final de 20.00 e um único débito no ledger."*

⚠️ **E o ponto-chave:** o lock é **por carteira**, nunca global. Duas carteiras diferentes devem processar **em paralelo**. Item 5.6: *"locks globais são proibidos"*.

### 2.6. O que configuramos e por quê

No `docker-compose.yml`:

| Configuração | Por quê |
| --- | --- |
| `image: postgres:16-alpine` | Versão 16 é estável; `alpine` é a variante leve (~80 MB vs ~400 MB) |
| `POSTGRES_USER/PASSWORD/DB` | Usuário e banco inicial. O `01-databases.sql` cria os outros dois |
| `log_lock_waits=on` | ⭐ Loga quando uma transação **espera por lock**. É o que vai **provar** que o teste das duas apostas de 80.00 realmente disputou |
| `log_min_duration_statement=500ms` | Loga query lenta (ajuda a achar `SELECT` sem índice) |
| `max_connections=200` | Limite de conexões. A app usa pool de 20; o teste exige 3 instâncias → 60 + Keycloak + folga |
| `healthcheck: pg_isready` | O Docker espera o Postgres **responder** antes de subir a app. Sem isso, a app tentaria conectar num banco que ainda está inicializando |
| `volumes: postgres-data` | Os dados sobrevivem a `docker compose down` |
| `/docker-entrypoint-initdb.d` | Scripts que rodam **só na primeira vez** (volume vazio) — cria `jungle_test` e o banco do Keycloak |

---

## 3. Keycloak — quem é você e o que pode fazer

### 3.1. O problema antes da solução

Sua API recebe `POST /wagering/transactions`. **Quem** está mandando?

**Ideia ingênua:** o cliente manda o `providerId` no corpo do JSON.

```json
{ "providerId": "provider-a", "kind": "BET", "money": {...} }
```

❌ **Isso é catastrófico.** Qualquer pessoa na internet escreveria `"providerId": "provider-b"` e mexeria na carteira de outro provedor. O enunciado chama isso de **eliminatório**: *"acesso não autorizado a operações ou transações"*.

**O que falta:** uma **terceira parte confiável** que diga *"eu verifiquei quem é esse cliente, e ele é mesmo o provider-a"*.

⭐ **Essa terceira parte é o conceito de IdP (Identity Provider / Provedor de Identidade).** É um serviço cuja única responsabilidade é **autenticar** (provar quem você é).

### 3.2. OAuth 2.0 e OIDC — os dois protocolos

Aqui estão três siglas que assustam e são simples:

| Sigla | Nome | O que significa na prática |
| --- | --- | --- |
| **OAuth 2.0** | — | Protocolo de **autorização**: "como um serviço obtém permissão para agir em nome de alguém" |
| **OIDC** | OpenID Connect | Camada de **identidade** em cima do OAuth 2.0: acrescenta "quem é o usuário" |
| **JWT** | JSON Web Token | O **formato** do documento que prova a identidade |

**A analogia do crachá de evento:**

```
    ┌─────────────────────────────────────────────────────────────┐
    │  VOCÊ CHEGA NA PORTARIA (o IdP / Keycloak)                  │
    │                                                             │
    │  "Sou o provedor de jogos provider-a, aqui está minha       │
    │   credencial (client_id + client_secret)"                   │
    │                                                             │
    │  A PORTARIA CONFERE NO LIVRO DE REGISTRO                    │
    │  Se está na lista → emite um CRACHÁ (o token JWT)           │
    └──────────────────────────┬──────────────────────────────────┘
                               │
                               ▼
              ┌────────────────────────────────────┐
              │  CRACHÁ (JWT) — é só um texto:     │
              │                                    │
              │  header:  {"alg":"RS256"}          │  ← como foi assinado
              │  payload: {                        │  ← O QUE ELE AFIRMA
              │    "iss": "http://localhost...",   │     quem emitiu
              │    "aud": "jungleg-api",           │     para quem
              │    "exp": 1730000000,              │     validade
              │    "sub": "service-account-...",   │     quem é
              │    "provider_id": "provider-a",    │  ⭐ O QUE IMPORTA
              │    "roles": ["provider"]           │  ⭐ O QUE PODE FAZER
              │  }                                 │
              │  signature: (assinatura digital)   │  ⭐ A PROVA
              └────────────────────────────────────┘
                               │
                               ▼
    ┌─────────────────────────────────────────────────────────────┐
    │  A PORTARIA INTERNA (nossa API)                             │
    │  Recebe o crachá e CONFERE A ASSINATURA                     │
    │  usando a chave PÚBLICA do Keycloak                         │
    │  Se a assinatura confere → o crachá é autêntico             │
    │  Ninguém pode falsificar sem a chave PRIVADA                │
    └─────────────────────────────────────────────────────────────┘
```

⭐ **A parte genial do JWT:** ele é assinado com uma chave **privada** que **só o Keycloak tem**. Sua API verifica com a chave **pública** (que qualquer um pode ter). Resultado: qualquer um pode **ler** o crachá, mas **ninguém** pode **falsificar** — para forjar, precisaria da chave privada.

É por isso que a API **não precisa** perguntar ao Keycloak a cada requisição: ela valida a assinatura **sozinha**, off-line, usando a chave pública em cache. (Isso é o **JWKS** — JSON Web Key Set, o conjunto de chaves públicas que fica em `/.well-known/jwks.json`.)

⚠️ **Nuance importante:** o JWT é **codificado** (Base64), **não criptografado**. Qualquer um pode ler o conteúdo. Por isso **nunca** se coloca dado sensível num token — e é por isso que o `provider_id` pode estar lá sem problema: não é segredo, é uma afirmação **assinada**.

### 3.3. `client_credentials` — o fluxo entre serviços

O OAuth 2.0 tem vários "fluxos" (grants) para situações diferentes. O nosso é o mais simples: **`client_credentials`** (credenciais de cliente).

**Serve para:** comunicação **serviço-a-serviço**, sem ser humano envolvido. Não tem tela de login, não tem senha digitada.

```
POST /realms/jungleg/protocol/openid-connect/token
  grant_type=client_credentials
  client_id=provider-service
  client_secret=provider-service-secret

  ↓ resposta

{ "access_token": "eyJhbGciOiJSUzI1NiIs...", "expires_in": 900, "token_type": "Bearer" }
```

Repare: o cliente **se autentica com a própria identidade**, não em nome de um usuário. É exatamente o caso de um provedor de jogos chamando nossa API.

| Fluxo | Para quem | Temos? |
| --- | --- | --- |
| `client_credentials` | Serviço → Serviço | ✅ **o nosso** |
| `authorization_code` | Usuário com navegador | ❌ fora do escopo (*"Cadastro de senhas e emissão própria de tokens estão fora do escopo"*) |
| `password` (direct access grant) | Teste manual com usuário/senha | ✅ só para o usuário `operator` de debug |

### 3.4. Roles (papéis) — o que você PODE fazer

Autenticação responde **"quem é você"**. **Autorização** responde **"o que você pode fazer"**. São coisas diferentes, e o enunciado exige as duas.

No Keycloak, autorização se expressa com **roles**. Definimos duas:

| Role | Quem tem | O que pode |
| --- | --- | --- |
| `provider` | Os clients dos provedores | Enviar operações e **ler apenas as próprias** |
| `internal` | O serviço interno | Abrir carteira, ler carteira, reconciliar — as operações "de dentro" |

⭐ **A pergunta de entrevista:** *por que abrir carteira é `internal` e não `provider`?* Porque abrir carteira com saldo inicial é **criar dinheiro do nada**. Se um provedor pudesse fazer isso, ele inventaria saldo. É uma operação administrativa, não do parceiro.

### 3.5. O que configuramos no `realm.json` e por quê

Primeiro: **o que é um realm?** Um **espaço isolado de identidades**. É como um "condomínio" no Keycloak: tem seus próprios usuários, clients e roles, separado de qualquer outro realm. O nosso se chama `jungleg`.

| O que configuramos | Por quê |
| --- | --- |
| **realm `jungleg`** | Isolamento. Nada de outro sistema interfere |
| `accessTokenLifespan: 900` | Token vale 15 minutos. ⭐ Token eterno é risco: se vazar, vale para sempre |
| `sslRequired: none` | Só em desenvolvimento (HTTP local). Em produção seria `external` |
| `bruteForceProtected: true` | Proteção contra força bruta (útil para o usuário `operator`) |
| **role `provider`** | Autorização dos provedores |
| **role `internal`** | Autorização do serviço interno |
| **client `provider-service`** | Representa o provedor `provider-a`. `client_credentials`, secret fixo de teste |
| **client `provider-b-service`** | ⭐ **Representa o `provider-b`.** Existe **só** para provar o isolamento: com ele dá para testar "token de B tentando ler transação de A → 403". Sem um segundo provedor, essa exigência seria **impossível de demonstrar** |
| **client `internal-service`** | Role `internal`. É quem abre carteira |
| **client `jungleg-api`** | ⭐ **Não é utilizável.** `bearerOnly: true`. Existe apenas para ser a **audience** dos tokens |
| **Mapper `provider_id`** | ⭐ O claim fixo `"provider_id": "provider-a"` no token do `provider-service` |
| **Mapper de audience** | Adiciona `"aud": "jungleg-api"` ao token |
| **Usuario `operator`** | Para debug manual pelo Postman/curl |

**As duas configurações que mais importam — entenda bem estas duas:**

**① Por que o `provider_id` está no TOKEN e não no corpo da requisição?**

```
❌ ERRADO: providerId vem do corpo
   Cliente diz: {"providerId": "provider-b", ...}
   → qualquer um se declara outro provedor. FALHA DE SEGURANÇA.

✅ CERTO: providerId vem do token assinado
   O Keycloak assina "esse token é do provider-a"
   A API confere a assinatura e CONFIA
   → o cliente pode até escrever "provider-b" no corpo; a API recusa com 403
```

**② Por que existe a audience `jungleg-api`?**

Imagine que a empresa tenha **outro** sistema (um CRM, por exemplo) no mesmo Keycloak. Um token emitido para o CRM também seria válido no realm... e sua API o aceitaria. A **audience** diz *"este token foi emitido PARA a jungleg-api"*. Sua API confere: `aud` contém `jungleg-api`? Se não, recusa. É a checagem de "esse crachá é do prédio certo?".

### 3.6. A variável `OIDC_ISSUER` vs `OIDC_DISCOVERY_URL` (a que mais confunde)

Essa eu vou explicar com calma porque é genuinamente confuso e você vai ser perguntado.

Um token tem o campo `iss` (**iss**uer = emissor):
```json
{ "iss": "http://localhost:8080/realms/jungleg", ... }
```

Sua API precisa conferir: **o `iss` do token é exatamente o que eu espero?** Se não for, o token não é do meu IdP → recuso. Isso é `OIDC_ISSUER`.

**Agora o problema:** onde esse endereço aparece?

| Situação | O endereço real do Keycloak |
| --- | --- |
| Você, no navegador/curl na sua máquina | `localhost:8080` |
| A app rodando **dentro** de um container | `keycloak:8080` (nome do serviço na rede Docker) |

Dentro do container, `localhost` **é o próprio container**! Então a app não alcança `localhost:8080`.

**Solução: separar as duas coisas.**

```
OIDC_ISSUER        = http://localhost:8080/realms/jungleg
                     └─ o que está ESCRITO no token. Usado para COMPARAR.

OIDC_DISCOVERY_URL = http://host.docker.internal:8080/realms/jungleg
                     └─ ONDE BUSCAR as chaves públicas. Usado para CONECTAR.
                     (host.docker.internal = "a máquina hospedeira, vista de dentro do container")
```

⭐ **A regra:** `ISSUER` é o que o token **diz**; `DISCOVERY_URL` é **por onde eu chego** no IdP. Podem ser endereços diferentes. Em produção geralmente são iguais — mas em Docker local **não são**, e é aí que muita gente perde horas com um erro tipo `oidc: issuer did not match the issuer returned by provider`.

---

## 4. SQS — a fila de mensagens

### 4.1. O que é

**SQS** = **S**imple **Q**ueue **S**ervice (Serviço de Fila Simples), da Amazon. É um **serviço de filas**: um lugar onde um programa **deixa** mensagens e outro programa **pega** depois.

**A analogia do restaurante:**

```
   SEM FILA (chamada direta)              COM FILA (SQS)
   ─────────────────────────              ──────────────
   Garçom leva o pedido                   Garçom PENDURA o pedido
   DIRETO na cozinha                      num varal de pedidos
   │                                      │
   │ Se a cozinha estiver ocupada?        │ O cozinheiro pega quando
   │   O garçom ESPERA (ou desiste)       │   estiver livre
   │ Se a cozinha estiver fechada?        │ Se a cozinha fechar?
   │   O pedido SE PERDE                  │   O pedido fica no varal
   │                                        para quando reabrir
   ▼                                      ▼
   Acoplado e frágil                      Desacoplado e resistente
```

### 4.2. Por que precisamos de uma fila neste desafio

O enunciado é explícito (item 10): *"Provisione as filas `wager-transactions.fifo` e `wager-transactions-dlq.fifo`"*. Ou seja, **é exigido**. Mas *por que* o desafio exige?

**Porque o provedor de jogos precisa de uma garantia:** *"aceitei a aposta e vou processá-la, mesmo que você esteja fora do ar por 5 minutos"*.

```
Provedor envia 10.000 apostas numa rajada
        │
        ├─ SEM fila: se a API não aguentar, as apostas PERDEM-SE
        │            e o provedor recebe erro 503
        │
        └─ COM fila: as mensagens FICAM na fila (durável, na AWS)
                     Nossa API consome no ritmo que aguentar.
                     Nenhuma aposta se perde.
```

**Isso se chama desacoplamento:** o provedor não precisa que nossa API esteja no ar no exato milissegundo. Ele deixa a mensagem na fila e segue a vida.

### 4.3. Os conceitos do SQS que você precisa saber

**① Mensagem** — um texto JSON. No nosso caso:
```json
{
  "messageId": "msg-123",
  "type": "WagerTransactionRequested",
  "occurredAt": "2026-09-08T12:00:00.000Z",
  "data": { "providerId": "provider-a", "kind": "BET", "money": {"amount":"25.00","currency":"BRL"} }
}
```

**② Consumidor (consumer)** — o programa que **pega** mensagens. É a nossa aplicação. Ela fica num laço:
```
para sempre:
    mensagens = SQS.ReceiveMessage(fila)     ← pega 1 a 10 mensagens
    para cada mensagem:
        processa
        SQS.DeleteMessage(mensagem)          ← ⭐ diz "terminei, pode apagar"
```

**③ At-least-once (pelo menos uma vez)** — ⭐ **O conceito mais importante daqui.**

O SQS **garante** que a mensagem chega **pelo menos uma vez**. Ele **não garante** que chega **exatamente** uma vez. Ou seja: **a mesma mensagem pode chegar duas vezes**. Pode ser reentrega por timeout, falha de rede, ou o consumidor ter morrido antes de apagar.

⭐ O enunciado diz literalmente (item 3): *"Assuma entrega **at-least-once**"*. E a decorrência é: **seu código TEM que ser idempotente.** Você não pode confiar na fila para não duplicar — você tem que se proteger.

> 🎯 Isso é uma pergunta clássica de entrevista: *"o SQS entrega uma vez só?"* — **Não.** E a resposta seguinte é: *"por isso a idempotência é persistida no banco, e não na fila."*

**④ Visibility Timeout** — ⭐ o segundo conceito mais importante.

Quando um consumidor **pega** uma mensagem, ela não é apagada — ela fica **invisível** por um tempo. Se o consumidor terminar e chamar `DeleteMessage`, ela é apagada. Se o consumidor **morrer** sem apagar, o tempo passa e a mensagem **volta** para a fila, para outro consumidor.

```
t=0s    Consumidor A pega a mensagem   → fica INVISÍVEL por 60s
t=5s    A processa e apaga             → fim, some da fila ✅
        ─── ou ───
t=0s    Consumidor A pega              → invisível por 60s
t=10s   A MORRE (sem apagar)
t=60s   Visibility timeout expira      → a mensagem VOLTA para a fila
t=61s   Consumidor B pega a MESMA mensagem → ⚠️ REPROCESSAMENTO
```

⭐ **É aqui que nasce a exigência de idempotência.** E é exatamente por isso que decidimos deixar `VisibilityTimeout` **folgado** (60s na entrada): se fosse curto demais, a mensagem voltaria para a fila **enquanto o primeiro consumidor ainda está processando** → dois processamentos simultâneos.

**⑤ FIFO vs Standard** — dois tipos de fila:

| | **Standard** | **FIFO** (`.fifo`) |
| --- | --- | --- |
| Ordem | ❌ pode chegar fora de ordem | ✅ **ordem garantida** por grupo |
| Duplicatas | possíveis | deduplicação por 5 min |
| Throughput | altíssimo | menor (mas suficiente) |
| **Usamos?** | ❌ | ✅ |

⭐ **Por que FIFO?** Porque temos um caso onde a **ordem importa**:
```
BET de R$ 50   (aposta)
   ↓ precisa vir ANTES
ROLLBACK de R$ 50 (desfaz a aposta)
```
Se o `ROLLBACK` chegasse **antes** da `BET`, ele não acharia a referência. FIFO resolve isso com o **`MessageGroupId`**: mensagens com o **mesmo** group id são entregues **em ordem** (e nunca em paralelo). Vamos usar o **`walletId`** como group id — assim, tudo de uma carteira é processado **em ordem**, e carteiras diferentes continuam em paralelo. ⭐

**⑥ DLQ — Dead Letter Queue (fila de mensagens mortas)**

E se uma mensagem **sempre** falhar? Seja por bug ou por dado corrompido? Ela ficaria voltando para a fila **para sempre**, travando tudo.

**Solução:** após N tentativas, o SQS **move** a mensagem para uma fila separada: a DLQ. Ali ela fica **parada**, para um humano investigar.

```
wager-transactions.fifo
        │
        │ falhou 5 vezes (maxReceiveCount=5)
        ▼
wager-transactions-dlq.fifo   ← "cemitério": fica parada, esperando análise
```

⭐ O enunciado exige (item 10): *"erros permanentes ou tentativas esgotadas devem chegar à DLQ"*. E o item 12 pede uma **métrica** de DLQ — porque se tem mensagem na DLQ, tem problema que ninguém viu.

**⑦ Redrive Policy** — a configuração que liga as duas:
```json
{ "deadLetterTargetArn": "arn:...:wager-transactions-dlq.fifo", "maxReceiveCount": "5" }
```
"Depois de 5 recebimentos sem sucesso deste item, mande para *aquela* fila lá."

### 4.4. As 4 filas que configuramos

```
        ENTRADA (o que chega para nós)
   ┌────────────────────────────────────────────┐
   │  wager-transactions.fifo                   │  ← provedores enviam apostas
   │         │ falhou 5x                       │
   │         ▼                                  │
   │  wager-transactions-dlq.fifo               │  ← cemitério
   └────────────────────────────────────────────┘

        SAÍDA (o que avisamos para o mundo)
   ┌────────────────────────────────────────────┐
   │  wallet-events.fifo                        │  ← avisamos que o saldo mudou
   │         │ falhou 5x                       │
   │         ▼                                  │
   │  wallet-events-dlq.fifo                    │  ← cemitério
   └────────────────────────────────────────────┘
```

| Fila | Papel |
| --- | --- |
| `wager-transactions.fifo` | Recebe as operações (BET/WIN/LOSS/REFUND/ROLLBACK) |
| `wager-transactions-dlq.fifo` | Mensagens que falharam 5 vezes |
| `wallet-events.fifo` | Publicamos nossos eventos (saldo mudou, transação processada) |
| `wallet-events-dlq.fifo` | Eventos que não conseguimos publicar |

### 4.5. O `ContentBasedDeduplication=false` — por que desligamos

O SQS FIFO tem uma deduplicação própria: se você mandar a mesma mensagem duas vezes em **5 minutos**, ele descarta. Parece ótimo. **Por que não usamos?**

**Porque 5 minutos não é garantia nenhuma.** O enunciado exige (item 5.2): *"Idempotência deve ser persistente e sobreviver ao reinício de todos os processos."*

| | Dedup do SQS | Nossa idempotência |
| --- | --- | --- |
| Onde vive | na AWS (5 min de janela) | **no PostgreSQL, para sempre** |
| Sobrevive a restart? | ⚠️ a janela é curta | ✅ sim |
| Detecta chave reutilizada com conteúdo diferente? | ❌ | ✅ 409 |
| Devolve o **resultado original** no replay? | ❌ | ✅ |

⭐ **A lição:** a dedup da fila é uma **otimização**, não uma garantia. Desligar ela (`false`) é uma **decisão consciente** para que ninguém confie nela por engano. A garantia de verdade está no banco, com `UNIQUE (provider_id, idempotency_key)`.

### 4.6. O que configuramos nas filas e por quê

| Configuração | Valor | Por quê |
| --- | --- | --- |
| `FifoQueue` | `true` | Ordem por carteira (ver ⑤ acima) |
| `ContentBasedDeduplication` | `false` | ⭐ Decisão consciente: a garantia é nossa (ver 4.5) |
| `VisibilityTimeout` | 60s (entrada) / 30s (saída) | Tempo para processar sem que a mensagem volte. **A validação recusa valores < 5s** |
| `ReceiveMessageWaitTimeSeconds` | 20 | ⭐ **Long polling**: a requisição fica aberta até 20s esperando mensagem. Sem isso, o consumidor perguntaria "tem algo?" milhares de vezes por minuto (custo e latência) |
| `RedrivePolicy` | `maxReceiveCount=5` | Após 5 tentativas → DLQ |

---

## 5. LocalStack — a AWS de mentira (mas de verdade)

### 5.1. O problema

O SQS é um serviço **da Amazon**, na nuvem. Para usar você precisa de: conta AWS, cartão de crédito, credenciais, internet. E cada mensagem custa (pouquíssimo, mas custa).

**Para desenvolver, isso é horrível:**
- Teste de integração dependeria de internet
- Cada execução de teste gastaria dinheiro
- Difícil de limpar (mensagem já enviada não sai da fila real)
- Sem isolamento entre testes

### 5.2. A solução

**LocalStack** é um programa que **imita os serviços da AWS rodando na sua máquina**, dentro de um container Docker.

```
SEU CÓDIGO                        SEU CÓDIGO
    │                                  │
    │ fala o protocolo da AWS          │ fala o MESMO protocolo
    ▼                                  ▼
┌──────────────┐                 ┌──────────────┐
│ AWS DE VERDADE│                │  LOCALSTACK  │
│ sqs.us-east-1.│                │ localhost:4566│
│ amazonaws.com │                │              │
└──────────────┘                 └──────────────┘
   custa, precisa                    grátis, offline,
   de internet                       descartável
```

⭐ **O ponto essencial:** **o código da aplicação é idêntico nos dois casos.** A única diferença é uma variável de ambiente:

```
SQS_ENDPOINT=http://localhost:4566          ← LocalStack (desenvolvimento/teste)
SQS_ENDPOINT=(vazio)                        ← AWS de verdade (produção)
```

A biblioteca oficial da AWS (`aws-sdk-go-v2`) respeita `BaseEndpoint`: se você aponta para `localhost:4566`, ela manda tudo para lá **falando o mesmo protocolo**. O LocalStack entende e responde.

⭐ **Por que isso satisfaz o enunciado:** o item 13 exige *"Execute PostgreSQL, o IdP e LocalStack ou MiniStack em containers reais"* e o item 14 diz que *"substituição integral de PostgreSQL, SQS e IdP por mocks"* é **eliminatório**. O LocalStack é a solução elegante: **não é mock** (é um serviço real, em container, com o protocolo real), mas **é local e grátis**.

> 🎯 **Diferença crucial para a entrevista:** mock = código falso que simula. LocalStack = **serviço real** que implementa o protocolo de verdade. Por isso ele **vale** no requisito de "testes de integração reais".

### 5.3. O que configuramos

| Configuração | Por quê |
| --- | --- |
| `image: localstack/localstack:stable` | Versão estável |
| `SERVICES: sqs` | ⭐ Sobe **só** o SQS. O LocalStack sabe imitar ~80 serviços AWS; subir todos consumiria GBs de RAM sem necessidade |
| `PERSISTENCE: "1"` | As filas sobrevivem ao restart do container |
| `ports: 4566:4566` | Porta única para todos os serviços AWS no LocalStack |
| `healthcheck: curl .../_localstack/health` | Espera o LocalStack **estar pronto** antes de rodar o init |
| `./deploy/localstack:/etc/localstack/init/ready.d` | ⭐ Pasta especial: o LocalStack **executa os scripts dela quando fica pronto**. É assim que `init-queues.sh` roda automático |
| `image: amazon/aws-cli` (serviço `localstack-init`) | Container com o **CLI oficial da AWS** para criar as filas |

---

## 6. Docker e Docker Compose — a caixa e o maquinista

### 6.1. Docker: a caixa

**Docker** empacota um programa **com todo o ambiente dele** numa "caixa" (imagem). Dentro da caixa tem: o sistema operacional mínimo, as bibliotecas, os arquivos de configuração e o programa.

```
SEM DOCKER                          COM DOCKER
"na minha máquina funciona"         "funciona em qualquer lugar"
├─ precisa instalar Postgres 16     ├─ docker run postgres:16
├─ versão diferente do colega?      ├─ SEMPRE a mesma versão
├─ conflito de porta com outro      ├─ portas isoladas
│  serviço da sua máquina           │
└─ desinstalar deixa lixo           └─ docker rm, nada sobra
```

**Imagem vs Container** — a confusão mais comum:

| | Imagem | Container |
| --- | --- | --- |
| O que é | A **receita** / a classe | O **prato pronto** / o objeto |
| Analogia | `.exe` instalador | o programa rodando |
| Muda? | Não (é imutável) | Sim (é o processo vivo) |
| Quantos? | Uma imagem | Vários containers da mesma imagem |

### 6.2. Docker Compose: o maquinista

**Docker puro** = você orquestra na mão: `docker run postgres...`, `docker run keycloak...`, e precisa lembrar a ordem, as redes, os volumes...

**Docker Compose** = você descreve **tudo num arquivo YAML** e dá uma ordem só:

```yaml
services:
  postgres:  { image: postgres:16-alpine, ... }
  keycloak:  { image: quay.io/keycloak/keycloak:26.4, ... }
  localstack:{ image: localstack/localstack:stable, ... }
  api:       { build: ., ... }
```
```sh
docker compose up -d     # ⭐ sobe TUDO, na ordem certa, na mesma rede
```

⭐ **O que o Compose resolve de graça:**
- **Rede compartilhada:** os containers se enxergam pelo **nome do serviço** (`postgres`, `keycloak`, `localstack`). É por isso que a app usa `postgres:5432` e não `localhost:5432`
- **Ordem de subida:** `depends_on` com `condition`
- **Volumes nomeados:** os dados sobrevivem
- **Variáveis de ambiente:** um lugar só, com `${VAR:-default}`

### 6.3. O `depends_on` com `condition` — a parte que evita dor de cabeça

Sem isso, o Docker só espera o container **iniciar** (não estar pronto):

```
❌ SEM condition:
   t=0s  postgres inicia (mas ainda carregando, não aceita conexão)
   t=1s  api inicia → tenta conectar → ERRO "connection refused" → morre

✅ COM condition: service_healthy:
   t=0s  postgres inicia
   t=8s  postgres responde ao pg_isready → "healthy"
   t=8s  api inicia → conecta com sucesso
```

Nosso compose usa:
| Condition | Espera |
| --- | --- |
| `service_healthy` | O container ficar **saudável** (healthcheck passa) |
| `service_completed_successfully` | O container **terminar com sucesso** — usado no `migrate` |

---

## 7. Os 4 padrões que amarram tudo

Estes padrões **não são peças de infraestrutura** — são **formas de programar** que resolvem problemas de sistema distribuído. Você vai ver esses nomes no `PLANO.md` e precisa saber o que significam.

### 7.1. Idempotência — "fazer duas vezes tem o mesmo efeito que uma"

**O problema:** o SQS entrega a mesma mensagem duas vezes (at-least-once). O cliente reenvia porque não recebeu a resposta. Sua API processaria a aposta **duas vezes** → débito duplicado.

**A solução:** toda operação carrega uma **chave única** (`Idempotency-Key`). Antes de processar, você procura essa chave no banco:

```
Chegou a operação com chave "provider-a:transaction-123"
        │
        ├─ Já existe com o MESMO conteúdo?
        │     └─ NÃO processa de novo. Devolve o RESULTADO SALVO.
        │        (com idempotentReplay: true)
        │
        ├─ Já existe com conteúdo DIFERENTE?
        │     └─ 409 CONFLITO (alguém reusou a chave para outra coisa)
        │
        └─ Não existe?
              └─ Processa normalmente
```

**A analogia do número de protocolo:** você liga para o banco e dá o **número do protocolo**. Se ligar de novo com o mesmo número, o atendente consulta e diz "isso já foi resolvido, o resultado foi X" — não abre um segundo pedido.

⭐ **Por que a implementação é no BANCO e não em memória:**

```go
// ❌ ERRADO: mapa em memória
var processadas = map[string]bool{}
```
Se o processo reiniciar, o mapa **zera** → a mesma aposta seria processada de novo. E com 3 instâncias, cada uma teria o **seu** mapa. O enunciado chama isso de eliminatório: *"idempotência restrita à memória"*.

```sql
-- ✅ CERTO: constraint no banco
ALTER TABLE wager_transactions ADD CONSTRAINT ... UNIQUE (provider_id, idempotency_key);
```
O banco **recusa** a segunda inserção. Compartilhado por todas as instâncias, sobrevive a restart.

### 7.2. Transactional Outbox — "não publique antes de salvar"

**O problema (sutil e clássico):** você processa uma aposta e precisa **avisar o mundo**. A ordem ingênua:

```
❌ ERRADO
  1. COMMIT no banco          (o dinheiro mudou) ✅
  2. Publica no SQS           ← se o processo MORRER aqui?
                                  → o dinheiro mudou, mas NINGUÉM foi avisado
                                  → inconsistência silenciosa
```

Ou invertendo:
```
❌ TAMBÉM ERRADO
  1. Publica no SQS           (avisou o mundo) ✅
  2. COMMIT no banco          ← se falhar?
                                  → avisou sobre um dinheiro que NÃO mudou
                                  → e o enunciado proíbe: "publicação anterior ao commit" é ELIMINATÓRIO
```

**Duas operações em sistemas diferentes não podem ser atômicas.** Não existe "commit no Postgres e no SQS ao mesmo tempo".

**A solução — o padrão Outbox:**

```
   DENTRO DA MESMA TRANSAÇÃO SQL:
   ┌────────────────────────────────────────────────┐
   │ BEGIN                                          │
   │   UPDATE wallets SET balance_minor = 97500     │  ← o dinheiro
   │   INSERT INTO wallet_ledger_entries (...)      │  ← o extrato
   │   INSERT INTO outbox_events (                  │  ← ⭐ o RECADO, guardado
   │     event_id, event_type, payload, published_at=NULL
   │   )                                            │
   │ COMMIT  ← tudo junto, atômico ✅               │
   └────────────────────────────────────────────────┘
                    │
                    │  (depois, em outro momento)
                    ▼
   ┌────────────────────────────────────────────────┐
   │  WORKER PUBLICADOR (separado, em laço)         │
   │  1. Pega os eventos com published_at = NULL    │
   │  2. Publica no SQS                             │
   │  3. UPDATE outbox_events SET published_at=now()│
   └────────────────────────────────────────────────┘
```

⭐ **A sacada:** o "recado" é salvo **no mesmo commit** do dinheiro. Então **nunca** existe dinheiro mudado sem recado salvo. A publicação vira uma **consequência** do commit, não parte dele.

**E se o worker morrer entre publicar e marcar como publicado?** Ele republica na próxima rodada. Pode duplicar o evento no SQS — por isso o `eventId` é **estável** e o consumidor do outro lado também é idempotente. **At-least-once de novo.**

### 7.3. Inbox — "já tratei essa mensagem"

O espelho do outbox, para o **lado da entrada**.

**O problema:** a mensagem do SQS chega, você processa, e o processo morre **antes** de apagar a mensagem. Ela volta (visibility timeout) e é reprocessada.

**A solução:** uma tabela `inbox_messages` com `UNIQUE (consumer_name, message_id)`. Ao receber:

```
  DENTRO DA MESMA TRANSAÇÃO:
  1. INSERT INTO inbox_messages (message_id, body_hash, ...)
     └─ Violou o UNIQUE? → JÁ TRATEI esta mensagem → pula (ou devolve o resultado)
        └─ Mas o body_hash é diferente? → mensagem adulterada → rejeita
  2. processa o domínio
  3. grava o outbox
  4. marca a inbox como concluída
  COMMIT
       │
       ▼
  5. SÓ AGORA: DeleteMessage no SQS
```

⭐ **Por que "só agora":** o enunciado exige (item 10): *"Remova a mensagem da fila somente após o commit do seu tratamento durável"*. Se apagasse antes do commit e o commit falhasse, a operação estaria perdida.

### 7.4. Máquina de estados — "de onde para onde eu posso ir"

Uma transação tem **estados** e **transições permitidas**:

```
                  ┌──────────┐
                  │ PENDING  │  (aceita, ainda processando)
                  └────┬─────┘
                       │
        ┌──────────────┼──────────────┬─────────────────┐
        ▼              ▼              ▼                 ▼
   ┌──────────┐  ┌──────────┐   ┌──────────┐   ┌───────────────────┐
   │PROCESSED │  │ REJECTED │   │  FAILED  │   │PENDING_REFERENCE  │
   │ (deu bom)│  │(regra de │   │(infra    │   │(espera a          │
   │ TERMINAL │  │ negócio) │   │ quebrou) │   │ referência chegar)│
   └──────────┘  │ TERMINAL │   │ TERMINAL │   └─────────┬─────────┘
                 └──────────┘   └──────────┘             │
                                                          ▼
                                              (quando a referência chega,
                                               vai para PROCESSED ou REJECTED)
```

⭐ **"Terminal" quer dizer: não sai mais daqui.** Uma transação `PROCESSED` **nunca** volta a ser `PENDING`. Um replay **consulta** o resultado salvo, não reprocessa. O enunciado exige isso no item 7: *"Uma transação terminal não deve sofrer novas transições. Replay consulta seu resultado persistido sem reaplicar a operação."*

**A analogia do pedido de pizza:** depois que a pizza foi **entregue**, o pedido não pode voltar para "em preparo". Se o cliente ligar perguntando, você consulta o histórico: "foi entregue às 20h15". Não refaz a pizza.

---

## 8. Variáveis de ambiente — por que não escrever no código

Você deve ter estranhado o `.env.example`. Por que não simplesmente **escrever** `localhost:5432` no código Go?

```go
// ❌ Por que isso é ruim
db, _ := pgx.Connect(ctx, "postgres://jungleg:jungleg@localhost:5432/jungleg")
```

| Problema | Consequência |
| --- | --- |
| A **senha** fica no Git | ⭐ Segredo vazado. Está lá no histórico, para sempre |
| Endereço **fixo** | O container usa `postgres:5432`, sua máquina usa `localhost:5432` |
| Ambiente **único** | Dev, teste e produção precisariam de código diferente |
| Não dá para **mudar sem recompilar** | Ajustar um timeout exigiria rebuild da imagem |

**A regra de 12 fatores (12-factor app)** — princípio consolidado de arquitetura:
> *"Armazene a configuração no ambiente, não no código."*

⭐ **A analogia do controle remoto:** o código é a **TV** (igual para todos). As variáveis de ambiente são os **botões** — você ajusta o volume sem abrir a TV e soldar um resistor.

**No nosso projeto, o fluxo é:**

```
.env.example          →  documenta QUAIS variáveis existem, com exemplo (VAI para o git)
.env                  →  os valores REAIS da sua máquina (NÃO vai para o git)
   │
   ▼
internal/config/config.go  →  lê o ambiente, valida, devolve *Config
   │
   ▼
Fx injeta *Config em quem precisa
   │
   ▼
O código NUNCA chama os.Getenv diretamente
```

⭐ **Por que "o código nunca chama `os.Getenv`":** porque assim existe **um único lugar** que sabe ler configuração (`config.Load`), com validação centralizada e testável. Se cada pacote lesse o ambiente por conta própria, você teria 40 pontos de falha silenciosa e nenhum teste possível.

E é por isso que a validação **derruba o boot**: melhor o processo **não subir** do que subir com `OIDC_ISSUER` vazio e falhar de forma estranha na primeira requisição autenticada.

---

## 9. Onde cada peça toca o código

Este mapa responde *"onde eu vou mexer quando precisar falar com X?"*:

| Peça | Onde configuramos | Onde o código usa | Fase |
| --- | --- | --- | --- |
| **PostgreSQL** | `docker-compose.yml` (`postgres`), `deploy/postgres/init/` | `internal/infra/postgres/` (pool, repositórios, UoW) | 4 e 5 |
| **Keycloak** | `docker-compose.yml` (`keycloak`), `deploy/keycloak/realm.json` | `internal/infra/auth/` (verificador OIDC) + `internal/httpapi/middleware` | 7 |
| **SQS** | `deploy/localstack/init-queues.sh` | `internal/infra/sqsx/` (produtor, consumidor) + `internal/worker/` | 8 |
| **LocalStack** | `docker-compose.yml` (`localstack`, `localstack-init`) | **nenhum** — é só o SQS rodando local (muda apenas `SQS_ENDPOINT`) | 8 |
| **Docker/Compose** | `Dockerfile`, `docker-compose.yml` | **nenhum** | 0, 12 |

⭐ **Repare no padrão:** toda a infraestrutura mora em `internal/infra/`. O **domínio** (`internal/domain/`) não aparece nessa tabela — porque ele **não toca em nenhuma dessas peças**. É exatamente o que o item 4 do enunciado exige: *"O domínio deve permanecer independente de Fx, HTTP, SQS e bibliotecas de persistência."*

```
internal/domain/          ← NÃO SABE que existe Postgres, Keycloak ou SQS
     ▲
     │ usa
     │
internal/infra/           ← SABE conversar com todos eles
```

---

## 10. Glossário

Termos que você vai encontrar no código, no plano e nas perguntas da entrevista.

| Termo | Significado curto |
| --- | --- |
| **ACID** | Atomicidade, Consistência, Isolamento, Durabilidade — as garantias de uma transação |
| **At-least-once** | Entrega "pelo menos uma vez": pode duplicar. Exige idempotência |
| **Audience (`aud`)** | Para quem o token foi emitido. Impede aceitar token de outro sistema |
| **Claim** | Um campo dentro do payload do JWT (`iss`, `aud`, `exp`, `provider_id`...) |
| **client_credentials** | Fluxo OAuth serviço-a-serviço, sem usuário humano |
| **Constraint** | Regra que o banco faz cumprir (`CHECK`, `UNIQUE`, `FOREIGN KEY`) |
| **Consumer** | Quem **pega** mensagens de uma fila |
| **Container** | Um processo rodando a partir de uma imagem Docker |
| **DLQ** | Dead Letter Queue: fila de mensagens que falharam demais || **`FOR UPDATE`** | `SELECT` que **trava** as linhas lidas até o fim da transação |
| **FIFO** | First In, First Out: fila que **garante ordem** por grupo |
| **Idempotência** | Fazer a mesma operação duas vezes tem o mesmo efeito que uma |
| **IdP** | Identity Provider: serviço que autentica (o Keycloak) |
| **Imagem** | O "molde" imutável de um container |
| **Inbox** | Tabela que registra mensagens já recebidas (dedup de entrada) |
| **Issuer (`iss`)** | Quem emitiu o token. A API compara com `OIDC_ISSUER` |
| **JWKS** | Conjunto de chaves **públicas** do IdP (para validar assinaturas) |
| **JWT** | JSON Web Token: o "crachá" assinado digitalmente |
| **LocalStack** | Imitação local dos serviços AWS (usamos para o SQS) |
| **Long polling** | Ficar esperando mensagem até 20s em vez de perguntar sem parar |
| **Lost update** | Duas transações leem o mesmo valor e a segunda apaga o efeito da primeira |
| **Message Group Id** | Agrupa mensagens FIFO que precisam de ordem (usamos `walletId`) |
| **OAuth 2.0** | Protocolo de **autorização** |
| **OIDC** | Camada de **identidade** sobre o OAuth 2.0 |
| **Outbox** | Tabela onde o evento é salvo **na mesma transação** do dado |
| **Redrive policy** | Regra que manda para a DLQ após N tentativas |
| **Realm** | Espaço isolado de identidades no Keycloak |
| **Role (papel)** | O que uma identidade **pode fazer** (`provider`, `internal`) |
| **Rollback** | Desfazer a transação (em Go: `tx.Rollback()`) |
| **SKIP LOCKED** | Pegar linhas **sem esperar** as que já estão travadas (para workers concorrentes) |
| **Transação** | Bloco de operações tratado como uma coisa só |
| **Visibility Timeout** | Tempo que a mensagem fica invisível após ser lida antes de voltar à fila |
| **Volume** | Área de disco persistente do Docker (sobrevive ao container) |
| **Worker** | Processo em segundo plano que executa trabalho assíncrono |

---

## Próximo passo

Agora que você sabe **o que cada peça é**, releia o **§5 do `GUIA_FASE_0.md`** (a configuração arquivo por arquivo). Ele vai fazer muito mais sentido: cada linha de configuração é uma resposta a um dos problemas deste documento.

Depois me diga:
1. Qual dessas 4 peças (Postgres, Keycloak, SQS, Docker) ainda está nebulosa? Eu aprofundo só nela.
2. Se quiser, eu faço o mesmo tipo de documento para os **padrões da §7** com exemplos de código reais do projeto (quando eles existirem, nas Fases 5–9).
