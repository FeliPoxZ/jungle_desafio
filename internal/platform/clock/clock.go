// Package clock fornece a leitura do relógio do processo.
//
// Não tem regra de negócio: só responde "que horas são", do jeito que a
// aplicação precisa.
package clock

import "time"

// System lê o relógio do sistema operacional.
//
// Satisfaz `ports.Clock` sem importá-lo: em Go a interface é satisfeita pela
// forma, então a plataforma não ganha dependência da aplicação.
type System struct{}

// Now devolve o instante corrente em UTC.
//
// UTC sempre, e não o fuso do processo: os mesmos instantes vão para colunas
// TIMESTAMPTZ, para o `occurredAt` dos eventos e para o carimbo do ledger.
// Deixar o fuso local vazar faria o mesmo fato ser gravado de formas diferentes
// conforme a máquina em que o serviço roda — e comparação de instante com fuso
// diferente é exatamente o tipo de erro que aparece só em produção.
func (System) Now() time.Time { return time.Now().UTC() }
