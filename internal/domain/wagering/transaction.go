package wagering

import (
	"strings"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
)

// TransactionID identifica a transação internamente (uuid v7, gerado fora do
// domínio).
type TransactionID string

// PayloadEncoder serializa o payload recebido para calcular o hash.
//
// É injetado porque o hash precisa ser do MESMO formato sempre: quem chama
// fornece um JSON canônico (internal/platform/canonicaljson). Assim o domínio
// não importa encoding/json e a comparação de payload deixa de ser frágil a
// ordem de campos.
type PayloadEncoder func(v any) ([]byte, error)

// Hash identifica o conteúdo de um payload pelo SHA-256 em hexadecimal.
type Hash string

// WagerTransaction é o agregado da operação financeira: o que o provedor pediu,
// em que estado está e por que falhou.
//
// Os campos são não exportados para que o estado só mude pelos métodos de
// transição, que validam a máquina de estados.
type WagerTransaction struct {
	id        TransactionID
	walletID  wallet.WalletID
	playerID  wallet.PlayerID
	kind      Kind
	status    Status
	amount    money.Money
	gameID    string
	roundID   string
	createdAt time.Time
	updatedAt time.Time

	// Metadados de idempotência. Presentes na origem interna e na externa: a
	// chave é o que impede crédito inicial duplicado em OPENING.
	idempotencyKey string
	payloadHash    Hash

	// Específicos de operação externa.
	providerID                 string
	externalTransactionID      string
	referenceExternalTxnID     string
	referenceTransactionID     TransactionID
	failureCode                string
	resultBalance              money.Money
	referenceResolutionAttempt int
	nextResolutionAttemptAt    time.Time
}

// New valida e cria a transação em PENDING.
//
// A validação depende da origem: operação externa exige provedor,
// identificador externo, chave de idempotência e hash do payload; OPENING não
// aceita esses metadados, porque não vêm de provedor nenhum.
func New(
	id TransactionID,
	walletID wallet.WalletID,
	playerID wallet.PlayerID,
	kind Kind,
	amount money.Money,
	gameID string,
	roundID string,
	idempotencyKey string,
	providerID string,
	externalTransactionID string,
	referenceExternalTxnID string,
	payload any,
	encode PayloadEncoder,
	now time.Time,
) (*WagerTransaction, error) {
	if err := validateNew(id, walletID, playerID, kind, amount, gameID, roundID, idempotencyKey, providerID, externalTransactionID, referenceExternalTxnID, payload, encode, now); err != nil {
		return nil, err
	}

	hash, err := hashPayload(payload, encode)
	if err != nil {
		return nil, err
	}

	return &WagerTransaction{
		id:                     id,
		walletID:               walletID,
		playerID:               playerID,
		kind:                   kind,
		status:                 StatusPending,
		amount:                 amount,
		gameID:                 strings.TrimSpace(gameID),
		roundID:                strings.TrimSpace(roundID),
		createdAt:              now,
		updatedAt:              now,
		idempotencyKey:         idempotencyKey,
		payloadHash:            hash,
		providerID:             providerID,
		externalTransactionID:  externalTransactionID,
		referenceExternalTxnID: referenceExternalTxnID,
	}, nil
}

// validateNew concentra as regras de criação, por origem.
func validateNew(
	id TransactionID,
	walletID wallet.WalletID,
	playerID wallet.PlayerID,
	kind Kind,
	amount money.Money,
	gameID, roundID, idempotencyKey, providerID, externalTransactionID, referenceExternalTxnID string,
	payload any,
	encode PayloadEncoder,
	now time.Time,
) error {
	if err := kind.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(string(id)) == "" {
		return invalid("id da transação é obrigatório")
	}
	if strings.TrimSpace(string(walletID)) == "" {
		return invalid("walletID é obrigatório")
	}
	if strings.TrimSpace(string(playerID)) == "" {
		return invalid("playerID é obrigatório")
	}
	if err := amount.Currency().Validate(); err != nil {
		return invalid("moeda da transação inválida: %v", err)
	}
	if err := kind.ValidateAmount(amount.Minor()); err != nil {
		return err
	}
	if err := validateOrigin(kind, providerID, externalTransactionID, idempotencyKey, payload, encode); err != nil {
		return err
	}
	if kind.RequiresReference() {
		if strings.TrimSpace(referenceExternalTxnID) == "" {
			return &Error{
				Code:    CodeMissingReference,
				Message: kind.String() + " exige referenceExternalTransactionId",
			}
		}
	}
	if strings.TrimSpace(gameID) == "" {
		return invalid("gameID é obrigatório")
	}
	if strings.TrimSpace(roundID) == "" {
		return invalid("roundID é obrigatório")
	}
	if now.IsZero() {
		return invalid("instante de criação é obrigatório")
	}
	return nil
}

// validateOrigin aplica as regras que distinguem operação interna de externa.
func validateOrigin(kind Kind, providerID, externalTransactionID, idempotencyKey string, payload any, encode PayloadEncoder) error {
	if kind.IsInternal() {
		if strings.TrimSpace(providerID) != "" || strings.TrimSpace(externalTransactionID) != "" {
			return &Error{
				Code:    CodeInternalKind,
				Message: "OPENING é operação interna e não aceita provedor nem identificador externo",
			}
		}
		// A chave de idempotência continua obrigatória: é ela que impede
		// crédito inicial duplicado para o mesmo jogador e moeda.
		if strings.TrimSpace(idempotencyKey) == "" {
			return invalid("idempotencyKey é obrigatória")
		}
		return nil
	}

	if strings.TrimSpace(providerID) == "" {
		return invalid("providerID é obrigatório em operação externa")
	}
	if strings.TrimSpace(externalTransactionID) == "" {
		return invalid("externalTransactionID é obrigatório em operação externa")
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return invalid("idempotencyKey é obrigatória")
	}
	if payload == nil {
		return invalid("payload é obrigatório em operação externa")
	}
	if encode == nil {
		return invalid("encoder de payload é obrigatório")
	}
	return nil
}

// hashPayload calcula o hash do payload recebido.
func hashPayload(payload any, encode PayloadEncoder) (Hash, error) {
	if payload == nil {
		return Hash(sumHex(nil)), nil
	}
	b, err := encode(payload)
	if err != nil {
		return "", invalid("payload não pôde ser serializado: %v", err)
	}
	return Hash(sumHex(b)), nil
}

// Rehydrate reconstrói a transação do que está persistido, sem revalidar e sem
// reaplicar movimentação, transição ou emissão de evento.
func Rehydrate(
	id TransactionID,
	walletID wallet.WalletID,
	playerID wallet.PlayerID,
	kind Kind,
	status Status,
	amount money.Money,
	gameID, roundID string,
	idempotencyKey string,
	payloadHash Hash,
	providerID, externalTransactionID, referenceExternalTxnID string,
	referenceTransactionID TransactionID,
	failureCode string,
	resultBalance money.Money,
	attempt int,
	nextAttemptAt time.Time,
	createdAt, updatedAt time.Time,
) *WagerTransaction {
	return &WagerTransaction{
		id:                         id,
		walletID:                   walletID,
		playerID:                   playerID,
		kind:                       kind,
		status:                     status,
		amount:                     amount,
		gameID:                     gameID,
		roundID:                    roundID,
		idempotencyKey:             idempotencyKey,
		payloadHash:                payloadHash,
		providerID:                 providerID,
		externalTransactionID:      externalTransactionID,
		referenceExternalTxnID:     referenceExternalTxnID,
		referenceTransactionID:     referenceTransactionID,
		failureCode:                failureCode,
		resultBalance:              resultBalance,
		referenceResolutionAttempt: attempt,
		nextResolutionAttemptAt:    nextAttemptAt,
		createdAt:                  createdAt,
		updatedAt:                  updatedAt,
	}
}

// ---------------------------------------------------------------------------
// Acessores
// ---------------------------------------------------------------------------

// ID devolve o identificador interno.
func (t *WagerTransaction) ID() TransactionID { return t.id }

// WalletID devolve a carteira da operação.
func (t *WagerTransaction) WalletID() wallet.WalletID { return t.walletID }

// PlayerID devolve o jogador da operação.
func (t *WagerTransaction) PlayerID() wallet.PlayerID { return t.playerID }

// Kind devolve o tipo da operação.
func (t *WagerTransaction) Kind() Kind { return t.kind }

// Status devolve o estado atual.
func (t *WagerTransaction) Status() Status { return t.status }

// Amount devolve o valor da operação.
func (t *WagerTransaction) Amount() money.Money { return t.amount }

// GameID devolve o jogo.
func (t *WagerTransaction) GameID() string { return t.gameID }

// RoundID devolve a rodada.
func (t *WagerTransaction) RoundID() string { return t.roundID }

// IdempotencyKey devolve a chave de idempotência.
func (t *WagerTransaction) IdempotencyKey() string { return t.idempotencyKey }

// PayloadHash devolve o hash do payload recebido.
func (t *WagerTransaction) PayloadHash() Hash { return t.payloadHash }

// ProviderID devolve o provedor (vazio em OPENING).
func (t *WagerTransaction) ProviderID() string { return t.providerID }

// ExternalTransactionID devolve o identificador do provedor (vazio em OPENING).
func (t *WagerTransaction) ExternalTransactionID() string { return t.externalTransactionID }

// ReferenceExternalTransactionID devolve a referência externa, quando houver.
func (t *WagerTransaction) ReferenceExternalTransactionID() string { return t.referenceExternalTxnID }

// ReferenceTransactionID devolve a referência interna já resolvida.
func (t *WagerTransaction) ReferenceTransactionID() TransactionID { return t.referenceTransactionID }

// FailureCode devolve o código de falha, quando houver.
func (t *WagerTransaction) FailureCode() string { return t.failureCode }

// ResultBalance devolve o saldo resultante devolvido ao provedor.
func (t *WagerTransaction) ResultBalance() money.Money { return t.resultBalance }

// ReferenceResolutionAttempt devolve a tentativa atual de resolução da referência.
func (t *WagerTransaction) ReferenceResolutionAttempt() int { return t.referenceResolutionAttempt }

// NextResolutionAttemptAt devolve o instante da próxima tentativa.
func (t *WagerTransaction) NextResolutionAttemptAt() time.Time { return t.nextResolutionAttemptAt }

// CreatedAt devolve o instante de criação.
func (t *WagerTransaction) CreatedAt() time.Time { return t.createdAt }

// UpdatedAt devolve o instante da última transição.
func (t *WagerTransaction) UpdatedAt() time.Time { return t.updatedAt }

// IsTerminal informa se a operação já teve resultado definitivo.
func (t *WagerTransaction) IsTerminal() bool { return t.status.IsTerminal() }

// MatchesPayload informa se o hash informado é o mesmo da transação criada.
// É o que distingue replay idempotente de conflito de payload.
func (t *WagerTransaction) MatchesPayload(hash Hash) bool { return t.payloadHash == hash }

// ---------------------------------------------------------------------------
// Transições
// ---------------------------------------------------------------------------

// MarkProcessed conclui a operação com sucesso.
//
// Valida o saldo resultante ANTES de transicionar: erro de validação não pode
// deixar a transação em estado terminal sem o saldo que a justifica.
func (t *WagerTransaction) MarkProcessed(resultBalance money.Money, now time.Time) error {
	if err := resultBalance.Currency().Validate(); err != nil {
		return invalid("moeda do saldo resultante inválida: %v", err)
	}
	// O saldo devolvido ao provedor é o da carteira da operação: resultado em
	// outra moeda seria uma resposta incoerente para o cliente. Reusa o
	// sentinela do dinheiro para moeda incompatível ser sempre a mesma falha.
	if resultBalance.Currency() != t.amount.Currency() {
		return money.ErrCurrencyMismatch
	}
	if err := t.transicionar(StatusProcessed, now); err != nil {
		return err
	}
	t.resultBalance = resultBalance
	return nil
}

// MarkRejected registra a recusa por regra de negócio, com o código estável que
// o cliente vai receber.
//
// O código é o que carrega o significado para o cliente: a mensagem humana é
// derivada dele na borda, então o domínio não guarda texto de erro.
func (t *WagerTransaction) MarkRejected(failureCode string, now time.Time) error {
	if strings.TrimSpace(failureCode) == "" {
		return invalid("failureCode é obrigatório na rejeição")
	}
	if err := t.transicionar(StatusRejected, now); err != nil {
		return err
	}
	t.failureCode = failureCode
	return nil
}

// MarkFailed registra falha permanente de infraestrutura, para auditoria.
func (t *WagerTransaction) MarkFailed(failureCode string, now time.Time) error {
	if strings.TrimSpace(failureCode) == "" {
		failureCode = CodeProcessingFailed
	}
	if err := t.transicionar(StatusFailed, now); err != nil {
		return err
	}
	t.failureCode = failureCode
	return nil
}

// MarkPendingReference registra que a operação depende de uma referência ainda
// não disponível, guardando a tentativa e o próximo instante de tentativa.
//
// Uma referência pendente pode ser retomada por outra instância depois de uma
// interrupção: é para isso que a tentativa e o instante ficam persistidos.
func (t *WagerTransaction) MarkPendingReference(attempt int, nextAttemptAt time.Time, now time.Time) error {
	if attempt < 1 {
		return invalid("tentativa de resolução precisa ser >= 1, veio %d", attempt)
	}
	if nextAttemptAt.IsZero() {
		return invalid("próximo instante de tentativa é obrigatório")
	}
	if err := t.transicionar(StatusPendingReference, now); err != nil {
		return err
	}
	t.referenceResolutionAttempt = attempt
	t.nextResolutionAttemptAt = nextAttemptAt
	return nil
}

// ResolveReference guarda a referência interna resolvida e a tentativa em que
// isso aconteceu, sem mudar de estado: quem decide o estado é o processamento.
func (t *WagerTransaction) ResolveReference(referenceID TransactionID, attempt int) error {
	if strings.TrimSpace(string(referenceID)) == "" {
		return invalid("referência resolvida não pode ser vazia")
	}
	if attempt < 1 {
		return invalid("tentativa de resolução precisa ser >= 1, veio %d", attempt)
	}
	if t.status != StatusPendingReference {
		return invalidTransition("só é possível resolver referência em PENDING_REFERENCE, estado atual " + t.status.String())
	}
	t.referenceTransactionID = referenceID
	t.referenceResolutionAttempt = attempt
	return nil
}

// transicionar é o único ponto que muda o estado: valida a máquina, recusa
// estado terminal e atualiza o instante.
func (t *WagerTransaction) transicionar(to Status, now time.Time) error {
	if now.IsZero() {
		return invalid("instante da transição é obrigatório")
	}
	if err := t.status.ValidateTransition(to); err != nil {
		return err
	}
	t.status = to
	t.updatedAt = now
	return nil
}
