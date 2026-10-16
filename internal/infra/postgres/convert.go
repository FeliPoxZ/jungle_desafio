package postgres

import (
	"strings"

	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
)

// Helpers de conversão entre o banco e o domínio.
//
// Ficam juntos porque são a única fronteira onde representação muda: domain
// fala Money e Currency, o banco fala BIGINT e CHAR(3).

// moedaDB converte a moeda do domínio para o valor gravado.
func moedaDB(c money.Currency) string { return string(c) }

// moedaGo converte o valor lido para a moeda do domínio.
//
// # Por que o TrimSpace é obrigatório
//
// A coluna é CHAR(3). No PostgreSQL, CHAR(n) é preenchido com espaço até o
// tamanho declarado, e o driver devolve esse padding. Um Currency("BRL ") não
// passa em Validate() (comprimento 4) e quebraria silenciosamente a
// comparação de moeda.
//
// TrimSpace em vez de só trim de espaço à direita para também tolerar um valor
// que venha com espaço à esquerda por qualquer motivo.
func moedaGo(raw string) money.Currency {
	return money.Currency(strings.TrimSpace(raw))
}

// moedaGoPtr é a versão para colunas anuláveis.
func moedaGoPtr(raw *string) money.Currency {
	if raw == nil {
		return ""
	}
	return moedaGo(*raw)
}

// dinheiroMinor monta um Money a partir do valor em unidades mínimas.
//
// Usa FromMinor (e não New) porque o dado vem do banco: a validação de faixa é
// da escrita, imposta pela CHECK constraint da Fase 4. A reidratação confia na
// origem e não reaplica regra.
func dinheiroMinor(minor int64, currency money.Currency) money.Money {
	return money.FromMinor(minor, currency)
}
