# EVIDÊNCIAS — Fase 1 (`Money`) validada

> Saídas reais de comandos, coletadas em 2026-10-15. Servem como prova para o avaliador
> e como referência de "o que é sucesso" para as próximas fases.
>
> A Fase 1 é puro Go: nenhum container, banco ou fila envolvido.

## Ambiente

| Item | Versão/valor |
| --- | --- |
| Go | 1.26.5 (módulo declara `go 1.25.0` + `toolchain go1.26.5`) |
| Cache | `E:\jungleg\.gocache\{mod,build,gopath}` (o cache global não é gravável nesta máquina) |
| Pacote | `internal/domain/money` |
| Código | 541 linhas (`money.go`) |
| Testes | 2.090 linhas em 4 arquivos |

## 1. Build, vet e formatação

```
$ go build ./...
(vazio)

$ go vet ./...
(vazio)

$ gofmt -l ./internal/
(vazio)
```

## 2. Testes com detector de corrida (`go test -race ./...`)

```
?   	github.com/FeliPoxZ/jungle_desafio/cmd/api	[no test files]
ok  	github.com/FeliPoxZ/jungle_desafio/internal/app	2.231s
ok  	github.com/FeliPoxZ/jungle_desafio/internal/config	1.252s
ok  	github.com/FeliPoxZ/jungle_desafio/internal/domain/money	1.442s
ok  	github.com/FeliPoxZ/jungle_desafio/internal/httpapi	1.705s
ok  	github.com/FeliPoxZ/jungle_desafio/internal/observability	1.515s
```

## 3. Volume de teste e cobertura

```
$ go test -v ./internal/domain/money/
testes:   65
subtests: 319
total:    384 casos verdes, 0 falhas

$ go test -cover ./internal/domain/money/
ok  .../internal/domain/money	0.647s	coverage: 97.9% of statements
```

Cobertura por função: **24 de 27 funções em 100%**. As três restantes são guardas
defensivas comprovadamente inalcançáveis pelo fluxo real, documentadas no próprio código:

| Função | Cobertura | Por que não é 100% |
| --- | --- | --- |
| `rejectDuplicateKeys` | 90,5% | 3 guardas de forma (token que não é objeto, chave que não é string, erro de leitura do valor) — o `Decode` anterior em `UnmarshalJSON` já rejeitou qualquer entrada nessas condições |
| `parseAmount` | 96,8% | a guarda de overflow **dentro do laço dos inteiros** é inalcançável dado o portão de 15 dígitos (provado por exaustão: maior `units` possível = `99999999999999`, menor lado direito = `899999999999999`) |

## 4. Conformidade com o enunciado (§6.1 `Money` e §Encapsulamento)

Auditoria item a item do `README.md`: **10 de 10 conformes**.

| Requisito do README | Onde está provado |
| --- | --- |
| §6.1 L97 — criação por string, zero, soma, subtração, negação, comparação, serialização | `TestParseAmount`, `TestZero`, `TestAdd`, `TestSub`, `TestNeg`, `TestCmp`, `TestUnmarshalJSONValoresValidos` |
| §6.1 L99/L108 — representação e limites documentados | `TestPacoteTemDoc`, `TestLimiteDocumentadoNoCodigo` |
| §6.1 L102 — escala fixa 2 e ISO 4217 | `TestParseAmount`, `TestMoneyStringTemSempreDuasCasas`, `TestParseCurrency` |
| §6.1 L103 — rejeita vazio/`NaN`/`Inf`/científica/escala excedente/negativo | `TestParseAmountInvalido` (58 casos), `TestUnmarshalJSONRejeitados` (42 casos) |
| §6.1 L104 — sem arredondamento silencioso | escala exata de 2 (`"25.0"` e `"25.000"` rejeitados) |
| §6.1 L105 — aritmética e comparação exigem moedas compatíveis | `TestOperacoesExigemMesmaMoeda` (6 operações) |
| §6.1 L106 — overflow em parsing, soma, subtração e negação | `TestParseAmountInvalido`, `TestAdd`, `TestSub`, `TestNeg` |
| §6.1 L107 — negativo permitido em cálculo interno | `TestNew`, `TestFromMinorNaoValida`, `TestRoundTripJSONNaoValeParaNegativo` |
| §6.1 L110 — testes de incompatibilidade de moedas | `TestOperacoesExigemMesmaMoeda`, `TestMoedaIgualNaoEhMismatch` |
| §Encapsulamento L87/L91 — construtores com validação; valor inválido rejeitado | `TestNew` (14 casos: teto, piso, extremos do `int64`, moeda inválida) |
| §Encapsulamento L89 — criação e reidratação separadas | `TestNew` × `TestFromMinorNaoValida` |
| §Encapsulamento L93 — erro classificável, sem `panic` | `TestErroClassificavelPorErrorsIs`, `TestErroAtravessaEnvelopamento`, `TestSentinelaNaoConfundeCodigos`, `TestIsNaoCasaComErroDeOutroTipo` |
| **ELIMINATÓRIO** — nenhum cálculo em ponto flutuante | `TestSemPontoFlutuanteNoCodigo` (AST: literais `FLOAT` + identificadores `float64`/`float32`/`ParseFloat`/`FormatFloat`/`NaN`/`Inf`) + `TestImportsPermitidos` (lista fechada de imports) |

## 5. Laboratório de erros — propagação até o `failureCode`

Saída real de `TestLaboratorioErros` (erro nascendo no fundo da cadeia, sendo
envelopado duas vezes por `fmt.Errorf("%w")`, e o `errors.Is` respondendo):

```
OK    "25.00"  "BRL"  -> minor=2500
ERRO  "25.00"  "BRL1" -> Code="INVALID_CURRENCY" | é moeda? true  | é valor? false
ERRO  "-25.00" "BRL"  -> Code="INVALID_AMOUNT"   | é moeda? false | é valor? true
ERRO  "25.000" "BRL"  -> Code="INVALID_AMOUNT"   | é moeda? false | é valor? true
ERRO  "1e2"    "BRL"  -> Code="INVALID_AMOUNT"   | é moeda? false | é valor? true
ERRO  "abc"    "BRL"  -> Code="INVALID_AMOUNT"   | é moeda? false | é valor? true
```

Os dois booleanos são sempre opostos: é o contrato de classificação funcionando.
O `Code` é o que a Fase 7 vai mapear para o `failureCode` do HTTP.

## 6. Decisões de contrato registradas na Fase 1

| Decisão | Motivo |
| --- | --- |
| `New` valida moeda **e faixa**; `FromMinor` não valida | README §Encapsulamento L87 ("construtores com validação") e L91 ("valores de domínio inválidos devem ser rejeitados") exigem validação na criação; L89 ("separe criação e reidratação") mantém a leitura leve. A validação da leitura fica no repositório (Fase 5) + `CHECK constraint` (Fase 4) |
| Limite e escala são ambos `INVALID_AMOUNT` | `failureCode` estável e pequeno. Os planejados `ErrOverflow`/`ErrScaleExceeded` foram descartados: a distinção fica na mensagem, não em código novo |
| `UnmarshalJSON` rejeita campo desconhecido e chave repetida | O `encoding/json` normal ignoraria os dois; um typo em `amount` viraria cobrança de `R$ 0,00` silenciosa |
| Serialização **assimétrica** para negativo | `MarshalJSON` emite sinal (diferença interna, conciliação); `UnmarshalJSON` recusa (README §6.1: negativo proibido na entrada externa). Travado por `TestRoundTripJSONNaoValeParaNegativo` |
| `CmpLess`/`CmpEqual`/`CmpGreater` em vez de `Less`/`Equal`/`Greater` | `money.Equal` era ambíguo; prefixo deixa claro que são resultados de `Cmp` |
| `Equal` devolve `(bool, error)` | Moeda diferente é erro (README L105), não "diferente". Assimetria proposital com `IsZero`/`IsNegative`, que não envolvem outro operando |

## 7. Achados corrigidos durante a Fase 1

| # | Achado | Como foi encontrado | Correção |
| --- | --- | --- | --- |
| 1 | Guarda do `Add` com apenas um lado (teto) | varredura exaustiva de 81 combinações de valores: **10 furos**, todos com dois operandos negativos | guarda de piso adicionada |
| 2 | Guarda do `Sub` com sinal invertido (`minMinor - other` em vez de `minMinor + other`) | revisão do usuário sobre o exemplo pedido | sinal corrigido |
| 3 | Guarda do `Sub` sem o lado do teto | mesma varredura | segunda guarda adicionada |
| 4 | Guarda do `Neg` invertida (`m.minor > -maxMinor` rejeitava **quase todo** valor válido) | teste de propriedade com 131.085 valores | trocada por `m.minor < -maxMinor` |
| 5 | `New` aceitava valor fora da faixa | questionamento do usuário sobre criação inválida | validação de faixa adicionada |
| 6 | `UnmarshalJSON` aceitava campo desconhecido e chave repetida | suíte do Bloco 7 | `DisallowUnknownFields` + `rejectDuplicateKeys` |
| 7 | `gofmt` pendente (linhas em branco duplas) | `gofmt -l` | normalizado |

## 8. Reprovação no item eliminatório — verificação automatizada

O enunciado reprova "cálculo monetário em ponto flutuante". A verificação deixou de ser
manual: `TestSemPontoFlutuanteNoCodigo` percorre a AST do pacote e falha se encontrar

- qualquer `ast.BasicLit` com `token.FLOAT` (`1.5`, `1e3`);
- qualquer identificador `float32`, `float64`, `ParseFloat`, `FormatFloat`, `Inf`, `NaN`.

E `TestImportsPermitidos` mantém lista **fechada** de imports do núcleo
(`bytes`, `encoding/json`, `errors`, `fmt`, `strings`): qualquer import novo quebra o
teste de propósito, para a decisão ser consciente e não acidental.
