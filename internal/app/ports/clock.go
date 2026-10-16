package ports

import "time"

// Clock é a fonte de tempo da aplicação.
//
// É contrato, e não `time.Now()` chamado direto, porque os instantes que o
// processamento grava DECIDEM comportamento: o próximo instante de tentativa de
// uma referência pendente, o `occurredAt` dos eventos e os carimbos do ledger.
// Com o relógio injetado, o teste afirma o valor gravado em vez de comparar com
// uma janela de tolerância.
//
// A implementação de produção fica em `internal/platform/clock`, que satisfaz
// esta interface estruturalmente: a interface é do CONSUMIDOR, então a
// plataforma não precisa importar a aplicação para ser injetável.
type Clock interface {
	// Now devolve o instante corrente.
	Now() time.Time
}
