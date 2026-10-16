package postgres

import (
	"testing"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
)

// As asserções abaixo são resolvidas em tempo de COMPILAÇÃO.
//
// # Por que isto existe
//
// Os contratos ficam em `app/ports` e as implementações aqui. Se alguém mudar a
// assinatura de um método do repositório, nada quebraria até a aplicação chamar —
// e o erro apareceria longe, no meio de outra coisa.
//
// Com as asserções, a divergência quebra o build aqui, com a mensagem dizendo
// exatamente qual método não bate. É a rede de proteção da fronteira entre
// aplicação e infraestrutura.
var (
	_ ports.WalletRepository      = (*walletRepo)(nil)
	_ ports.TransactionRepository = (*txnRepo)(nil)
	_ ports.LedgerRepository      = (*ledgerRepo)(nil)
	_ ports.InboxRepository       = (*inboxRepo)(nil)
	_ ports.OutboxRepository      = (*outboxRepo)(nil)
	_ ports.UnitOfWork            = (*uow)(nil)
)

// TestNewUnitOfWorkDevolveOContrato confirma que a fábrica devolve o tipo do
// contrato, e não o concreto.
//
// É o que permite o Fx injetar `ports.UnitOfWork` na aplicação sem que ela
// conheça este pacote. Note que o pool é nil: a fábrica só monta a struct, sem
// abrir conexão — se ela tentasse conectar aqui, o teste falharia, e isso é
// desejado, porque o Fx decide quando a conexão acontece.
func TestNewUnitOfWorkDevolveOContrato(t *testing.T) {
	var u ports.UnitOfWork = NewUnitOfWork(nil)
	if u == nil {
		t.Fatal("NewUnitOfWork devolveu nil")
	}
}
