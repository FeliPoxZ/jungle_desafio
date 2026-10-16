package ports

import (
	"context"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ledger"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
)

// WalletRepository lê e grava carteiras.
type WalletRepository interface {
	// Insert cria a carteira. Devolve conflito se já existir carteira para o
	// mesmo par (player, moeda) — é a proteção contra duas carteiras iguais.
	Insert(ctx context.Context, w *wallet.Wallet) error

	// GetByID lê a carteira pelo identificador.
	GetByID(ctx context.Context, id ids.WalletID) (*wallet.Wallet, error)

	// GetByPlayerAndCurrency lê a carteira de um jogador na moeda.
	GetByPlayerAndCurrency(ctx context.Context, playerID ids.PlayerID, currency money.Currency) (*wallet.Wallet, error)

	// GetByIDForUpdate lê a carteira **tomando o lock da linha** (SELECT FOR
	// UPDATE).
	//
	// É o método que a aplicação usa antes de movimentar dinheiro, e a ordem
	// importa: o lock vem ANTES de ler o saldo. Ler o saldo e só depois pedir o
	// lock permitiria que outra transação alterasse o saldo no meio — e o
	// resultado seria lost update.
	GetByIDForUpdate(ctx context.Context, id ids.WalletID) (*wallet.Wallet, error)

	// Update grava o saldo novo, condicionado à versão esperada.
	//
	// Devolve conflito quando a versão mudou: significa que outro processo mexeu
	// na carteira entre a leitura e a gravação, e sobrescrever perderia a
	// alteração dele.
	//
	// A versão esperada é a que veio no `Movement.VersionBefore()`, **nunca**
	// `carteira.Version()` depois do débito — isso passaria a versão já
	// incrementada e o UPDATE casaria com zero linhas.
	Update(ctx context.Context, id ids.WalletID, expectedVersion int64, balance money.Money) error
}

// TransactionRepository lê e grava transações de aposta.
type TransactionRepository interface {
	// Insert grava a transação. Devolve conflito se a chave de idempotência já
	// existir para o provedor, ou se já houver OPENING com a mesma chave.
	Insert(ctx context.Context, t *wagering.WagerTransaction) error

	// GetByID lê a transação pelo identificador interno.
	GetByID(ctx context.Context, id wagering.TransactionID) (*wagering.WagerTransaction, error)

	// GetByIDForUpdate lê a transação tomando o lock da linha.
	GetByIDForUpdate(ctx context.Context, id wagering.TransactionID) (*wagering.WagerTransaction, error)

	// GetByProviderAndExternalID acha a transação pelo identificador do provedor.
	GetByProviderAndExternalID(ctx context.Context, providerID, externalID string) (*wagering.WagerTransaction, error)

	// GetByIdempotencyKey acha a transação pela chave de idempotência.
	//
	// É o primeiro passo do processamento: se já existe, é reenvio do provedor.
	GetByIdempotencyKey(ctx context.Context, providerID, key string) (*wagering.WagerTransaction, error)

	// GetOpeningByIdempotencyKey acha a OPENING (crédito inicial) pela chave.
	//
	// Separado do anterior porque a unicidade de OPENING é por chave sozinha, e
	// não por (provedor, chave): é o que impede creditar a carteira duas vezes.
	GetOpeningByIdempotencyKey(ctx context.Context, key string) (*wagering.WagerTransaction, error)

	// GetByReferenceAndKind acha a transação de um tipo que já aponta para uma
	// referência (por exemplo, o REFUND de uma BET).
	//
	// Existe para a recusa de dupla reversão ter mensagem boa. Quem IMPEDE a
	// dupla reversão é o índice parcial `UNIQUE (reference_transaction_id, kind)`,
	// mas ele fala pelo `23505`, que aborta a transação e não diz qual das
	// unicidades foi violada. Consultando antes, o serviço sabe que o que houve
	// foi "esta referência já foi revertida desse jeito" e responde com o código
	// estável `ALREADY_REVERSED`.
	//
	// A consulta não substitui o índice: entre ela e o INSERT existe uma janela,
	// e quem decide a corrida continua sendo o banco.
	GetByReferenceAndKind(ctx context.Context, referenceID wagering.TransactionID, kind wagering.Kind) (*wagering.WagerTransaction, error)

	// Save grava o estado atual da transação (status, saldo resultante,
	// referência resolvida, tentativas).
	Save(ctx context.Context, t *wagering.WagerTransaction) error

	// ListPendingReferencesDue devolve as transações em PENDING_REFERENCE cujo
	// próximo instante de tentativa já passou — é a fila do worker de
	// referências.
	ListPendingReferencesDue(ctx context.Context, now time.Time, limit int) ([]*wagering.WagerTransaction, error)

	// ListByWallet devolve as transações de uma carteira, com cursor.
	ListByWallet(ctx context.Context, walletID ids.WalletID, limit int, cursor *Cursor) ([]*wagering.WagerTransaction, error)
}

// LedgerRepository persiste e lê lançamentos do ledger.
//
// # Não existe Update nem Delete — de propósito
//
// O ledger é append-only: a imutabilidade é imposta por trigger no banco. Não ter
// método de alteração aqui é a primeira barreira; se alguém precisar "corrigir"
// um lançamento, a resposta é movimentação nova (estorno), não reescrever a
// história.
type LedgerRepository interface {
	// Insert grava o lançamento. Devolve conflito se a operação já tiver
	// lançamento (UNIQUE wallet_id + transaction_id).
	Insert(ctx context.Context, e ledger.Entry) error

	// GetByTransaction lê o lançamento de uma operação.
	GetByTransaction(ctx context.Context, walletID ids.WalletID, transactionID string) (*ledger.Entry, error)

	// ListByWallet devolve os lançamentos de uma carteira em ordem cronológica,
	// com cursor.
	ListByWallet(ctx context.Context, walletID ids.WalletID, limit int, cursor *Cursor) ([]*ledger.Entry, error)

	// SumSignedByWallet devolve a soma dos valores com sinal (crédito positivo,
	// débito negativo) em unidades mínimas.
	//
	// É a base da reconciliação: comparar esta soma com o saldo armazenado da
	// carteira revela divergência. Soma no banco (e não em Go) porque o volume
	// pode ser grande e o BIGINT do PostgreSQL é exato.
	SumSignedByWallet(ctx context.Context, walletID ids.WalletID) (int64, error)
}

// InboxMessage é uma mensagem recebida do SQS, deduplicada por
// (consumer, messageID).
type InboxMessage struct {
	ID           string
	ConsumerName string
	MessageID    string
	BodyHash     string
	Status       string
	FailureCode  string
	ReceivedAt   time.Time
	CompletedAt  time.Time
}

// InboxRepository registra as mensagens já recebidas.
//
// Existe para o consumo do SQS ser idempotente: a mesma mensagem pode ser
// entregue mais de uma vez (o SQS garante "at least once"), e sem este registro a
// segunda entrega debitaria de novo.
type InboxRepository interface {
	// InsertIfAbsent grava a mensagem se for nova.
	//
	// Devolve false **sem erro** quando a mensagem já existia: reentrega é o
	// caminho esperado, não uma falha.
	InsertIfAbsent(ctx context.Context, m InboxMessage) (bool, error)

	// Get lê a mensagem registrada.
	Get(ctx context.Context, consumerName, messageID string) (*InboxMessage, error)

	// MarkProcessed marca a mensagem como concluída.
	MarkProcessed(ctx context.Context, consumerName, messageID string, now time.Time) error

	// MarkFailed registra a falha e incrementa as tentativas.
	MarkFailed(ctx context.Context, consumerName, messageID, reason string, now time.Time) error
}

// OutboxEvent é um evento de domínio a publicar.
type OutboxEvent struct {
	EventID       string
	AggregateID   string
	AggregateType string
	EventType     string
	Payload       []byte
	OccurredAt    time.Time
	AvailableAt   time.Time
	Attempts      int
	LastError     string
	PublishedAt   time.Time
	CreatedAt     time.Time
}

// OutboxRepository guarda os eventos na mesma transação que a mudança de estado.
//
// # Por que outbox, e não publicar direto no SQS
//
// Publicar no SQS dentro da transação do banco não funciona: o SQS é externo e não
// participa do commit. Se o SQS aceitar a mensagem e o commit falhar, o evento
// fala de uma mudança que não aconteceu. Se o commit passar e o SQS falhar, a
// mudança acontece sem ninguém ser avisado.
//
// Com a outbox, o evento é gravado **na mesma transação** que o saldo e o ledger:
// ou tudo entra, ou nada entra. Um worker separado lê a tabela e publica depois,
// com retry e lease. A entrega passa a ser "pelo menos uma vez", e o consumidor
// deduplica pelo `eventId`.
type OutboxRepository interface {
	// Insert grava o evento na outbox.
	Insert(ctx context.Context, e OutboxEvent) error

	// ClaimBatch reivindica até `limit` eventos disponíveis, aplicando um lease
	// de `leaseFor`.
	//
	// Usa FOR UPDATE SKIP LOCKED: dois publishers rodando ao mesmo tempo pegam
	// lotes DIFERENTES, sem um bloquear o outro e sem reivindicar o mesmo evento.
	//
	// O lease empurra o `available_at` para o futuro: se este processo morrer
	// antes de publicar, o evento volta sozinho quando o prazo passar — em vez de
	// ficar preso para sempre.
	ClaimBatch(ctx context.Context, limit int, leaseFor time.Duration, now time.Time) ([]OutboxEvent, error)

	// MarkPublished marca o evento como publicado.
	MarkPublished(ctx context.Context, eventID string, now time.Time) error

	// MarkRetry agenda uma nova tentativa e registra o motivo.
	//
	// O contador de tentativas é incrementado **no banco** (`attempts + 1`), e não
	// a partir do valor lido: duas instâncias falhando ao mesmo tempo contariam a
	// mesma tentativa se o número viesse do processo.
	MarkRetry(ctx context.Context, eventID string, nextAttempt time.Time, reason string) error

	// PendingStats devolve quantos eventos estão pendentes e a idade do mais
	// antigo. É o que alimenta o alerta de "outbox parada".
	PendingStats(ctx context.Context, now time.Time) (pendentes int, idade time.Duration, err error)
}

// Repos reúne os repositórios válidos dentro de uma transação.
//
// Quem recebe `Repos` está dentro de uma transação: os cinco repositórios
// compartilham a MESMA conexão e o MESMO snapshot. Ler a carteira por um e gravar
// o ledger por outro participa do mesmo commit — é isso que faz a operação
// financeira ser atômica.
type Repos struct {
	Wallets      WalletRepository
	Transactions TransactionRepository
	Ledger       LedgerRepository
	Inbox        InboxRepository
	Outbox       OutboxRepository
}

// UnitOfWork executa um bloco dentro de uma transação.
type UnitOfWork interface {
	// Run abre a transação, entrega os repositórios e faz commit.
	//
	// Se `fn` devolver erro, faz rollback e nada fica gravado. Se `fn` entrar em
	// pânico, o rollback também acontece e o pânico é propagado.
	Run(ctx context.Context, fn func(ctx context.Context, r Repos) error) error

	// RunReadOnly executa um bloco de leitura, com isolamento de snapshot.
	//
	// Existe separado porque leitura longa (reconciliação, relatório) não deve
	// segurar lock nem competir com a escrita.
	RunReadOnly(ctx context.Context, fn func(ctx context.Context, r Repos) error) error
}
