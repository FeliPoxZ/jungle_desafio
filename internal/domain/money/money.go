// Package money define o value object monetário do domínio.
//
// # Representação
//
// O valor é guardado em UNIDADES MÍNIMAS da moeda (centavos), como int64.
// Exemplo: R$ 25,00 é armazenado como 2500.
//
//	Representação interna : 2500      (int64)
//	Escala                : 2         (sempre 2 casas)
//	Contrato externo      : "25.00"   (string decimal)
//
// # Por que não float64
//
// float64 é binário e não representa decimais exatamente: 0.1 + 0.2 não é 0.3.
// Em um ledger financeiro, esse resíduo se acumula e a soma dos lançamentos
// deixa de fechar com o saldo.
//
// # Limites
//
// O valor absoluto é limitado a maxMinor (9_000_000_000_000_000 unidades
// mínimas, ou R$ 90 trilhões). O limite é bem menor que o teto do int64 para
// que as checagens de overflow possam ser feitas sem estourar elas mesmas.
// Entrada externa rejeita sinal; valores negativos existem apenas em
// diferenças e cálculos internos.
//
// # Contrato externo
//
// O valor trafega em JSON como {"amount":"25.00","currency":"BRL"}: amount é
// string decimal com escala fixa 2, nunca número, e currency é o código
// alfabético ISO 4217. MarshalJSON e UnmarshalJSON implementam esse formato, e
// UnmarshalJSON é estrito quanto à forma do objeto (campo desconhecido e chave
// repetida são erro).
package money

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/FeliPoxZ/jungle_desafio/internal/domain/errors"
)

// ---------------------------------------------------------------------------
// Tipos e limites da representação
// ---------------------------------------------------------------------------

// Currency é um código de moeda ISO 4217.
//
// É um tipo próprio (e não uma string solta) de propósito: assim o compilador
// impede trocar a moeda por qualquer texto em uma chamada de função.
type Currency string

// BRL é o real brasileiro, única moeda com cenário completo no desafio.
const BRL Currency = "BRL"

// Money é um valor monetário em uma moeda, guardado em unidades mínimas.
//
// Os campos são não exportados de propósito: um Money só nasce por New, Parse,
// Zero ou por uma operação aritmética, e nunca é alterado depois. Toda operação
// devolve um Money novo.
type Money struct {
	minor    int64
	currency Currency
}

const (
	// maxMinor é o maior valor absoluto aceito, em unidades mínimas. É o
	// limite do domínio, não o limite do int64: fica ~1000x abaixo do teto para
	// que as checagens de overflow possam ser escritas sem estourar.
	maxMinor = int64(9_000_000_000_000_000)

	// minMinor é o menor valor aceito.
	minMinor = -maxMinor

	// maxAmountDigits é o maior número de dígitos da parte inteira que ainda
	// permite multiplicar por 100 sem estourar o int64. Não é o limite de
	// valor: quem aplica o limite é a comparação com maxMinor.
	maxAmountDigits = 15
)

// ---------------------------------------------------------------------------
// Erros de domínio
// ---------------------------------------------------------------------------

// Códigos estáveis de falha. Viram failureCode no contrato HTTP.
const (
	CodeInvalidCurrency  = "INVALID_CURRENCY"
	CodeInvalidAmount    = "INVALID_AMOUNT"
	CodeCurrencyMismatch = "CURRENCY_MISMATCH"
)

// Error é o erro de domínio, compartilhado com os outros pacotes de domínio.
//
// É um alias, não uma redefinição: `money.Error`, `wallet.Error` e
// `wagering.Error` são o MESMO tipo, então `errors.Is` funciona igual nos três e
// não existe a possibilidade de duas implementações divergirem.
type Error = errors.Error

// Erros sentinela. Use errors.Is(err, money.ErrInvalidAmount) para classificar;
// não compare a mensagem.
var (
	ErrInvalidCurrency  = &Error{Code: CodeInvalidCurrency, Message: "código de moeda inválido"}
	ErrInvalidAmount    = &Error{Code: CodeInvalidAmount, Message: "valor monetário inválido"}
	ErrCurrencyMismatch = &Error{Code: CodeCurrencyMismatch, Message: "moedas incompatíveis na operação"}
)

// invalidAmount monta um erro de valor inválido com mensagem formatada.
func invalidAmount(format string, args ...any) error {
	return &Error{Code: CodeInvalidAmount, Message: fmt.Sprintf(format, args...)}
}

// ---------------------------------------------------------------------------
// Moeda
// ---------------------------------------------------------------------------

// ParseCurrency converte e valida uma string em Currency.
//
// Aceita apenas o código alfabético ISO 4217: exatamente três letras
// maiúsculas ASCII (A-Z). Exemplos válidos: "BRL", "USD", "EUR".
//
// Rejeitados com ErrInvalidCurrency: vazio, comprimento diferente de 3,
// minúsculas ("brl"), código numérico ("986"), dígitos, símbolos e espaços.
//
// Não normaliza (trim, uppercase): a borda do sistema faz isso de forma
// explícita. Aqui a política é rejeitar em vez de adivinhar.
func ParseCurrency(s string) (Currency, error) {
	c := Currency(s)
	if err := c.Validate(); err != nil {
		return "", fmt.Errorf("moeda %q: %w", s, err)
	}
	return c, nil
}

// Validate confere se o Currency é um código ISO 4217 alfabético válido.
//
// Existe separado de ParseCurrency para revalidar valores que chegam já
// tipados, como os lidos do banco na reidratação.
func (c Currency) Validate() error {
	// O código ISO 4217 alfabético é ASCII por definição da norma:
	// comparar byte a byte é correto e não há risco com UTF-8.
	if len(c) != 3 {
		return ErrInvalidCurrency
	}
	for i := 0; i < len(c); i++ {
		if c[i] < 'A' || c[i] > 'Z' {
			return ErrInvalidCurrency
		}
	}
	return nil
}

// String devolve o código da moeda ("BRL").
func (c Currency) String() string { return string(c) }

// ---------------------------------------------------------------------------
// Criação de Money
// ---------------------------------------------------------------------------

// New cria um Money a partir do valor em unidades mínimas e valida a moeda e
// o limite de representação.
//
// É o caminho de criação de valores que vieram de fora do domínio, onde nem a
// moeda nem o valor são confiáveis. Reidratação usa FromMinor.
func New(minor int64, currency Currency) (Money, error) {
	if err := currency.Validate(); err != nil {
		return Money{}, fmt.Errorf("moeda %q: %w", currency, err)
	}
	if minor < minMinor || minor > maxMinor {
		return Money{}, invalidAmount("valor %d excede o limite de %d unidades mínimas", minor, maxMinor)
	}

	return Money{minor: minor, currency: currency}, nil
}

// FromMinor reconstrói um Money a partir do que já está persistido, sem
// revalidar moeda nem faixa.
//
// Assume que o valor veio de uma linha que o próprio sistema gravou. A
// validação na leitura é responsabilidade do repositório, apoiada na
// constraint da coluna.
func FromMinor(minor int64, currency Currency) Money {
	return Money{minor: minor, currency: currency}
}

// Zero devolve o valor neutro da moeda informada (0 em unidades mínimas).
func Zero(currency Currency) (Money, error) {
	return New(0, currency)
}

// Minor devolve o valor em unidades mínimas (R$ 25,00 => 2500).
func (m Money) Minor() int64 { return m.minor }

// Currency devolve a moeda do valor.
func (m Money) Currency() Currency { return m.currency }

// ---------------------------------------------------------------------------
// Serialização
// ---------------------------------------------------------------------------

// moneyDTO é a forma de transporte do Money no JSON.
//
// Amount é string de propósito: um número JSON passaria pelo float64 do
// encoding/json e perderia precisão antes de chegar ao domínio.
type moneyDTO struct {
	Amount   string   `json:"amount"`
	Currency Currency `json:"currency"`
}

// String devolve o valor em reais com duas casas ("975.00"). Valores
// negativos, que só existem em cálculos internos, saem com sinal.
func (m Money) String() string {
	units, cents := m.minor/100, m.minor%100

	sign := ""
	if m.minor < 0 {
		units, cents = -units, -cents
		sign = "-"
	}

	return fmt.Sprintf("%s%d.%02d", sign, units, cents)
}

// MarshalJSON devolve {"amount":"25.00","currency":"BRL"}.
//
// A serialização é assimétrica de propósito: um Money negativo (diferença
// interna, saldo de conferência) sai com sinal, mas UnmarshalJSON recusa esse
// sinal, porque entrada financeira externa negativa é inválida.
func (m Money) MarshalJSON() ([]byte, error) {
	return json.Marshal(moneyDTO{Amount: m.String(), Currency: m.currency})
}

// UnmarshalJSON lê {"amount":"25.00","currency":"BRL"} aplicando o mesmo
// contrato de Parse: escala exata de 2, sem sinal e moeda ISO 4217 alfabética.
//
// É estrito quanto à forma do objeto, e não só ao valor:
//   - campos desconhecidos são rejeitados (um "amout" com typo viraria
//     cobrança de zero silenciosamente);
//   - chave repetida é rejeitada (o encoding/json ficaria com a última);
//   - amount como número JSON é rejeitado (o float64 perderia precisão antes
//     de chegar ao domínio);
//   - null, ausente, vazio, sinal, espaços e escala diferente de 2 também.
func (m *Money) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var dto moneyDTO
	if err := dec.Decode(&dto); err != nil {
		return err
	}

	// O Decode acima deixa passar chave repetida, ficando com a última. Isso é
	// aceitável para JSON de configuração, não para uma instrução financeira:
	// dois valores no mesmo campo precisam ser erro, não escolha silenciosa.
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}

	if dto.Amount == "" {
		return invalidAmount("campo amount ausente ou vazio na entrada JSON")
	}

	parsed, err := Parse(dto.Amount, dto.Currency.String())
	if err != nil {
		return err
	}

	*m = parsed
	return nil
}

// rejectDuplicateKeys varre o objeto JSON e devolve erro se o mesmo campo
// aparecer mais de uma vez. É a única coisa que o Decode de UnmarshalJSON não
// cobre: em chave repetida, o encoding/json fica com a última em silêncio.
//
// As guardas de forma daqui (token que não é objeto, chave que não é string)
// são inalcançáveis a partir de UnmarshalJSON, porque o Decode anterior já
// rejeitou qualquer entrada que não fosse objeto bem formado. Ficam porque
// esta função é uma unidade testável por si só e não depende de quem a chama.
func rejectDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))

	// Abre o objeto.
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return invalidAmount("entrada JSON deveria ser um objeto")
	}

	vistos := make(map[string]bool)
	for dec.More() {
		// Em objeto JSON a chave é sempre string.
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		nome, ok := tok.(string)
		if !ok {
			return invalidAmount("chave JSON inesperada")
		}
		if vistos[nome] {
			return invalidAmount("campo %q aparece mais de uma vez na entrada JSON", nome)
		}
		vistos[nome] = true

		// Pula o valor (RawMessage consome objeto ou array aninhado inteiro).
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Parsing do contrato externo
// ---------------------------------------------------------------------------

// Parse converte o par (amount, currency) do contrato externo em Money.
//
// O amount é uma string decimal estrita: sem sinal, com exatamente duas casas
// decimais e sem separador de milhar. Exemplos válidos: "0.00", "25.00",
// "1000.00".
//
// Rejeitados com ErrInvalidAmount: string vazia, sinal ("+25.00", "-25.00"),
// espaços, vírgula decimal ("25,00"), escala diferente de 2 ("25.0",
// "25.000"), notação científica ("1e2"), texto e valores acima de
// 9_000_000_000_000_000 unidades mínimas.
//
// Não normaliza nada: a borda do sistema normaliza de forma explícita.
func Parse(amount, currency string) (Money, error) {
	cur, err := ParseCurrency(currency)
	if err != nil {
		return Money{}, err
	}

	minor, err := parseAmount(amount)
	if err != nil {
		return Money{}, err
	}

	return New(minor, cur)
}

// parseAmount converte "25.00" em 2500, sem passar por ponto flutuante: os
// dígitos são acumulados como dígitos.
func parseAmount(s string) (int64, error) {
	if s == "" {
		return 0, invalidAmount("%q está vazio", s)
	}
	if s[0] == '+' || s[0] == '-' {
		return 0, invalidAmount("%q tem sinal; o sinal é definido pela operação, não pela entrada", s)
	}

	before, after, ok := strings.Cut(s, ".")
	if !ok {
		return 0, invalidAmount("%q não tem separador decimal; esperado algo como \"25.00\"", s)
	}

	intPart, fracPart := before, after
	if intPart == "" {
		return 0, invalidAmount("%q não tem parte inteira", s)
	}
	// A parte inteira cabe em 15 dígitos: assim units*100 é seguro.
	// O limite de valor de verdade é conferido depois, em total.
	if len(intPart) > maxAmountDigits {
		return 0, invalidAmount("%q excede o limite de %d unidades mínimas", s, maxMinor)
	}
	if len(fracPart) != 2 {
		return 0, invalidAmount("%q tem %d casas decimais; esperado exatamente 2", s, len(fracPart))
	}

	var units int64
	for i := 0; i < len(intPart); i++ {
		if intPart[i] < '0' || intPart[i] > '9' {
			return 0, invalidAmount("%q não é um decimal válido", s)
		}
		d := int64(intPart[i] - '0')
		// Comparação sem overflow de "units*10+d > maxMinor".
		//
		// Com o portão de 15 dígitos acima, esta guarda é inalcançável: o
		// maior units possível antes da última volta é 99999999999999 (14
		// noves) e o menor lado direito possível é (maxMinor-9)/10 =
		// 899999999999999. Fica aqui porque é ela que mantém o laço seguro se
		// o portão de comprimento for afrouxado um dia; quem barra o excesso
		// hoje é a comparação com total, depois do laço.
		if units > (maxMinor-d)/10 {
			return 0, invalidAmount("%q excede o limite de %d unidades mínimas", s, maxMinor)
		}
		units = units*10 + d
	}

	var cents int64
	for i := 0; i < len(fracPart); i++ {
		if fracPart[i] < '0' || fracPart[i] > '9' {
			return 0, invalidAmount("%q não é um decimal válido", s)
		}
		cents = cents*10 + int64(fracPart[i]-'0')
	}

	total := units*100 + cents
	if total > maxMinor {
		return 0, invalidAmount("%q excede o limite de %d unidades mínimas", s, maxMinor)
	}
	return total, nil
}

// ---------------------------------------------------------------------------
// Aritmética
// ---------------------------------------------------------------------------

// Add soma dois valores da mesma moeda, rejeitando estouro do limite nos dois
// sentidos.
//
// Dois operandos negativos estouram o piso; dois positivos, o teto. Como o
// sinal não é conhecido de antemão, as duas checagens são necessárias.
func (m Money) Add(other Money) (Money, error) {
	if err := m.checkSameCurrency(other); err != nil {
		return Money{}, err
	}
	// Isola m.minor em minMinor <= m.minor+other.minor <= maxMinor sem fazer
	// a soma, que poderia estourar.
	if m.minor < minMinor-other.minor {
		return Money{}, invalidAmount("soma excede o limite de %d unidades mínimas", maxMinor)
	}
	if m.minor > maxMinor-other.minor {
		return Money{}, invalidAmount("soma excede o limite de %d unidades mínimas", maxMinor)
	}
	return New(m.minor+other.minor, m.currency)
}

// Sub subtrai other de m, rejeitando estouro do limite nos dois sentidos.
func (m Money) Sub(other Money) (Money, error) {
	if err := m.checkSameCurrency(other); err != nil {
		return Money{}, err
	}
	// Isola m.minor em minMinor <= m.minor-other.minor <= maxMinor sem fazer
	// a subtração, que poderia estourar.
	if m.minor < minMinor+other.minor {
		return Money{}, invalidAmount("subtração excede o limite de %d unidades mínimas", maxMinor)
	}
	if m.minor > maxMinor+other.minor {
		return Money{}, invalidAmount("subtração excede o limite de %d unidades mínimas", maxMinor)
	}
	return New(m.minor-other.minor, m.currency)
}

// Neg inverte o sinal do valor.
func (m Money) Neg() (Money, error) {
	// A negação estoura o teto apenas se o valor estiver abaixo de -maxMinor.
	// Negar minMinor dá exatamente maxMinor, que é permitido.
	if m.minor < -maxMinor {
		return Money{}, invalidAmount("negação excede o limite de %d unidades mínimas", maxMinor)
	}
	return New(-m.minor, m.currency)
}

// checkSameCurrency garante que a operação acontece entre a mesma moeda.
func (m Money) checkSameCurrency(other Money) error {
	if m.currency != other.currency {
		return &Error{
			Code:    CodeCurrencyMismatch,
			Message: fmt.Sprintf("operação entre moedas diferentes: %q e %q", m.currency, other.currency),
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Comparação
// ---------------------------------------------------------------------------

// Resultados possíveis de Cmp, nos mesmos moldes de strcmp e cmp.Compare.
const (
	CmpLess    = -1 // o receptor é menor
	CmpEqual   = 0  // mesmo valor
	CmpGreater = 1  // o receptor é maior
)

// Cmp compara dois valores da mesma moeda, devolvendo CmpLess, CmpEqual ou
// CmpGreater. Moedas diferentes são erro, não "diferentes".
func (m Money) Cmp(other Money) (int, error) {
	if err := m.checkSameCurrency(other); err != nil {
		return 0, err
	}
	switch {
	case m.minor < other.minor:
		return CmpLess, nil
	case m.minor > other.minor:
		return CmpGreater, nil
	default:
		return CmpEqual, nil
	}
}

// Equal informa se os dois valores são iguais, na mesma moeda.
func (m Money) Equal(other Money) (bool, error) {
	c, err := m.Cmp(other)
	if err != nil {
		return false, err
	}
	return c == CmpEqual, nil
}

// Sign devolve CmpLess, CmpEqual ou CmpGreater conforme o valor seja negativo,
// zero ou positivo.
func (m Money) Sign() int {
	switch {
	case m.minor < 0:
		return CmpLess
	case m.minor > 0:
		return CmpGreater
	default:
		return CmpEqual
	}
}

// IsZero informa se o valor é zero.
func (m Money) IsZero() bool { return m.minor == 0 }

// IsNegative informa se o valor é negativo.
func (m Money) IsNegative() bool { return m.minor < 0 }

// IsPositive informa se o valor é positivo.
func (m Money) IsPositive() bool { return m.minor > 0 }
