// Package wallet define a carteira do jogador: identidade, dono, moeda, saldo e
// versão.
//
// # O que a carteira é, e o que ela NÃO é
//
// A carteira guarda o saldo **atual** — é a resposta para "quanto tem agora". O
// histórico de como o saldo chegou ali está no pacote `ledger`, que é um agregado
// separado. A carteira não guarda lista de lançamentos, e isso é deliberado:
// carregar o histórico inteiro para fazer um débito de R$ 5 seria inviável.
//
// A distinção completa está em `docs/LEDGER_VS_WALLET.md`.
//
// # Invariantes
//
//   - o saldo nunca é negativo (README §6.2)
//   - toda movimentação acontece na moeda da carteira
//   - a versão começa em 1 e só muda quando o saldo muda
//   - cada mudança de saldo tem o lançamento correspondente, criado na mesma
//     operação — quem grava recebe os dois juntos no `Movement`
//
// # Dinheiro
//
// O saldo é um `money.Money`, que guarda int64 em unidades mínimas. Toda
// aritmética passa pelo `money.Sub`/`Add`, que já tratam overflow e moeda
// incompatível. **Não há aritmética de saldo escrita à mão neste pacote.**
package wallet

import (
	domainerrors "github.com/FeliPoxZ/jungle_desafio/internal/domain/errors"
)

// Códigos estáveis de falha da carteira. Viram failureCode no contrato HTTP.
const (
	// CodeInvalidWallet cobre carteira malformada: id ou dono ausente, saldo
	// inicial negativo, instante ausente, moeda inválida.
	CodeInvalidWallet = "INVALID_WALLET"

	// CodeInsufficientFunds é o débito que deixaria o saldo negativo.
	//
	// É o código mais importante do arquivo: é ele que o serviço traduz para a
	// rejeição de uma `BET` sem saldo (README §6.2).
	CodeInsufficientFunds = "INSUFFICIENT_FUNDS"

	// CodeWalletCurrencyMismatch é a movimentação em moeda diferente da moeda da
	// carteira.
	//
	// Deliberadamente DIFERENTE de CodeInsufficientFunds: uma `BET` em USD numa
	// carteira BRL não é "sem saldo", é erro de quem chamou. Se os dois códigos
	// fossem o mesmo, uma confusão de moeda viraria uma rejeição de negócio
	// silenciosa — e o cliente acharia que o jogador está sem dinheiro.
	CodeWalletCurrencyMismatch = "WALLET_CURRENCY_MISMATCH"
)

// Error é o erro de domínio, compartilhado com os outros pacotes.
type Error = domainerrors.Error

// Erros sentinela. Classifique com errors.Is, nunca comparando a mensagem.
var (
	ErrInvalidWallet          = &Error{Code: CodeInvalidWallet, Message: "carteira inválida"}
	ErrInsufficientFunds      = &Error{Code: CodeInsufficientFunds, Message: "saldo insuficiente"}
	ErrWalletCurrencyMismatch = &Error{Code: CodeWalletCurrencyMismatch, Message: "moeda incompatível com a da carteira"}
)

// invalid monta um erro de carteira inválida com mensagem formatada.
func invalid(format string, args ...any) error {
	return domainerrors.New(CodeInvalidWallet, format, args...)
}

// insufficient monta o erro de saldo insuficiente, sempre com os dois valores
// envolvidos.
//
// Os dois números na mensagem não são enfeite: é o que torna a rejeição
// auditável. "Saldo insuficiente" sozinho não permite responder se foi por pouco
// ou por muito, nem detectar tentativa de explorar a regra.
func insufficient(want, have int64) error {
	return domainerrors.New(CodeInsufficientFunds,
		"débito de %d excede o saldo de %d", want, have)
}
