package wagering

// Códigos de falha que o SERVIÇO decide.
//
// Os que o domínio já nomeia vêm de lá — `wallet.CodeInsufficientFunds`,
// `wagering.CodeReversed`, `money.CodeInvalidCurrency` — para não existirem dois
// textos para o mesmo fato. Os daqui só a aplicação pode decidir, porque
// dependem de comparar a requisição com o que está gravado (referência,
// carteira, reversão anterior).
//
// O texto é contrato: vira `failureCode` na resposta HTTP e o cliente decide o
// que fazer por ele (A8 do PLANO).
const (
	// CodeInvalidRequest é a requisição que não chega a ser uma operação:
	// campo obrigatório ausente, identificador em branco, tipo desconhecido.
	//
	// Nada é gravado nesse caminho, e a MESMA chave de idempotência pode ser
	// reenviada depois de corrigida — é isso que distingue entrada corrigível de
	// resultado definitivo.
	CodeInvalidRequest = "INVALID_REQUEST"

	// CodeIdempotencyKeyRequired é a operação externa sem chave de idempotência.
	//
	// O servidor NÃO calcula uma chave no lugar da que faltou: substituir em
	// silêncio faria o reenvio do provedor parecer uma operação nova.
	CodeIdempotencyKeyRequired = "IDEMPOTENCY_KEY_REQUIRED"

	// CodeIdempotencyConflict é a chave reutilizada com conteúdo diferente, ou o
	// par (providerId, externalTransactionId) reapresentado sob outra chave.
	CodeIdempotencyConflict = "IDEMPOTENCY_CONFLICT"

	// CodeWalletNotFound é a carteira informada que não existe.
	//
	// Diferente das outras rejeições, esta NÃO é gravada como transação
	// rejeitada: `wager_transactions.wallet_id` tem chave estrangeira para
	// `wallets`, então não existe linha para gravar.
	CodeWalletNotFound = "WALLET_NOT_FOUND"

	// CodeWalletPlayerMismatch é a carteira que não pertence ao jogador
	// informado. A carteira é identificada por (jogador, moeda), então o par
	// divergente é requisição incoerente.
	CodeWalletPlayerMismatch = "WALLET_PLAYER_MISMATCH"

	// CodeReferenceNotFound é a referência que nunca chegou, com o TTL de
	// tentativas esgotado. Produzido pelo worker de referências (Fase 9).
	CodeReferenceNotFound = "REFERENCE_NOT_FOUND"

	// CodeReferenceNotProcessable é a referência que existe, mas terminou
	// rejeitada ou com falha permanente: não há o que reverter.
	CodeReferenceNotProcessable = "REFERENCE_NOT_PROCESSABLE"

	// CodeReferenceMismatch é a referência incompatível com a operação:
	// divergência de jogador, carteira, moeda ou rodada; tipo que não se reverte;
	// ou valor diferente do referenciado.
	CodeReferenceMismatch = "REFERENCE_MISMATCH"

	// CodeRollbackInsufficientFunds é o estorno que exigiria saldo inexistente.
	//
	// É DELIBERADAMENTE diferente de `wallet.CodeInsufficientFunds`: um é "o
	// jogador não tem saldo para apostar", o outro é "não dá para desfazer". Com
	// um código só, a operação de suporte perderia a informação de que precisa
	// (README §7).
	CodeRollbackInsufficientFunds = "ROLLBACK_INSUFFICIENT_FUNDS"
)
