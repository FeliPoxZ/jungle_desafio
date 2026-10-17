package wagering

import (
	"context"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ledger"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
)

// desfechoDaReferencia é o que a etapa 5 decidiu antes de qualquer movimentação.
type desfechoDaReferencia struct {
	desfecho    int
	failureCode string

	// referente é a operação referenciada, quando ela foi resolvida.
	referente *wagerdomain.WagerTransaction
}

const (
	// referenciaInformativa é a referência que a operação não precisa para
	// acontecer (BET, WIN e LOSS): foi conferida, ou não deu para conferir, e a
	// operação segue.
	referenciaInformativa = iota

	// referenciaPendente: a operação DEPENDE de uma referência que ainda não
	// permite decidir. Vira PENDING_REFERENCE + evento, e o worker de referências
	// assume.
	referenciaPendente

	// referenciaRejeitada: a referência existe e não serve para esta operação.
	// É definitivo: nova tentativa não muda o que está gravado.
	referenciaRejeitada

	// referenciaResolvida: referência encontrada, compatível e processada.
	referenciaResolvida
)

// resolverReferencia decide o que fazer com a referência da requisição.
//
// A régua é a mesma do README §7 para os cinco tipos, com UMA diferença: em
// BET/WIN/LOSS a referência é informativa e não bloqueia a operação. Sem essa
// diferença, um WIN cuja BET ainda não chegou ficaria pendente e o jogador não
// receberia por causa de um vínculo que ele não precisa.
func (s *Service) resolverReferencia(ctx context.Context, r ports.Repos, req Request) (desfechoDaReferencia, error) {
	if req.ReferenceExternalTxnID == "" {
		return desfechoDaReferencia{}, nil
	}
	obrigatoria := req.Kind.RequiresReference()

	// A referência é resolvida por (providerId, referenceExternalTransactionId):
	// o provedor faz parte da chave, então uma operação nunca enxerga a
	// transação de outro provedor.
	referente, err := r.Transactions.GetByProviderAndExternalID(ctx, req.ProviderID, req.ReferenceExternalTxnID)
	switch {
	case ports.IsNotFound(err):
		if obrigatoria {
			return desfechoDaReferencia{desfecho: referenciaPendente}, nil
		}
		return desfechoDaReferencia{}, nil
	case err != nil:
		return desfechoDaReferencia{}, err
	}

	if code := concordancia(referente, req); code != "" {
		return desfechoDaReferencia{desfecho: referenciaRejeitada, failureCode: code}, nil
	}

	switch {
	case referente.Status() == wagerdomain.StatusRejected || referente.Status() == wagerdomain.StatusFailed:
		// Existe e terminou sem sucesso: não há o que devolver nem o que
		// desfazer, e esperar não muda isso.
		return desfechoDaReferencia{desfecho: referenciaRejeitada, failureCode: CodeReferenceNotProcessable}, nil

	case !referente.Status().IsTerminal():
		// Existe, mas não terminou. Para a reversão isso é pendência de verdade:
		// a referência pode virar PROCESSED (aí reverte) ou REJECTED (aí recusa).
		// Para a referência informativa, não bloqueia.
		if obrigatoria {
			return desfechoDaReferencia{desfecho: referenciaPendente}, nil
		}
		return desfechoDaReferencia{}, nil
	}

	if !obrigatoria {
		return desfechoDaReferencia{desfecho: referenciaResolvida, referente: referente}, nil
	}

	// Daqui para baixo: reversão sobre referência PROCESSED. Tipo, valor e
	// reversão anterior precisam conferir.
	if !reversivel(req.Kind, referente.Kind()) {
		return desfechoDaReferencia{desfecho: referenciaRejeitada, failureCode: CodeReferenceMismatch}, nil
	}
	if !mesmoValor(referente.Amount(), req.Amount) {
		// Reversão parcial não faz parte do desafio: ou devolve tudo, ou recusa.
		return desfechoDaReferencia{desfecho: referenciaRejeitada, failureCode: CodeReferenceMismatch}, nil
	}

	revertida, err := s.jaRevertida(ctx, r, referente, req.Kind)
	if err != nil {
		return desfechoDaReferencia{}, err
	}
	if revertida {
		return desfechoDaReferencia{desfecho: referenciaRejeitada, failureCode: wagerdomain.CodeReversed}, nil
	}

	return desfechoDaReferencia{desfecho: referenciaResolvida, referente: referente}, nil
}

// concordancia confere jogador, carteira, moeda e rodada (README §7).
//
// O provedor é garantido pela chave da consulta, mas é conferido assim mesmo:
// uma consulta futura que troque de chave não pode transformar esta garantia em
// suposição.
func concordancia(referente *wagerdomain.WagerTransaction, req Request) string {
	switch {
	case referente.ProviderID() != req.ProviderID,
		referente.PlayerID() != req.PlayerID,
		referente.WalletID() != req.WalletID,
		referente.RoundID() != req.RoundID,
		referente.Amount().Currency() != req.Amount.Currency():
		return CodeReferenceMismatch
	}
	return ""
}

// reversivel diz se o tipo referenciado pode ser desfeito pelo tipo pedido:
// REFUND devolve uma BET; ROLLBACK desfaz BET, WIN ou REFUND (README §7).
func reversivel(kind, referente wagerdomain.Kind) bool {
	switch kind {
	case wagerdomain.KindRefund:
		return referente == wagerdomain.KindBet
	case wagerdomain.KindRollback:
		switch referente {
		case wagerdomain.KindBet, wagerdomain.KindWin, wagerdomain.KindRefund:
			return true
		}
	}
	return false
}

// jaRevertida informa se a referência já recebeu a reversão deste tipo — ou, no
// caso de REFUND e ROLLBACK sobre a MESMA BET, se o débito já foi devolvido pelo
// outro tipo.
//
// A dupla reversão do MESMO tipo é impedida pelo índice parcial
// UNIQUE (reference_transaction_id, kind). Esta consulta existe para o cliente
// receber `ALREADY_REVERSED`, com significado, em vez de um 23505 sem explicação.
//
// A segunda condição é a coerência entre REFUND e ROLLBACK, e ela vale nos DOIS
// sentidos: os dois devolvem o MESMO débito da BET. O índice permite que um de
// cada tipo conviva sobre a mesma aposta, e sem esta guarda o segundo creditaria
// de novo o valor já devolvido — dinheiro criado do nada.
//
//	BET 40 → ROLLBACK(BET) 40 → REFUND(BET) 40
//	   100 → 60 → 100 → 140   (25 de lucro para quem só apostou)
//
// O caminho legítimo para desfazer um estorno é ROLLBACK apontando para o
// REFUND, que DEBITA e é aceito — porque a referência ali é a transação do
// estorno, não a BET.
//
// O defeito 50 foi exatamente a assimetria: o ROLLBACK já era barrado quando
// havia REFUND (teste `TestROLLBACKDesfazBETeDesfazEstorno`), mas o REFUND não
// era barrado quando havia ROLLBACK — e esse caminho terminava com mais dinheiro
// do que começou.
func (s *Service) jaRevertida(ctx context.Context, r ports.Repos, referente *wagerdomain.WagerTransaction, kind wagerdomain.Kind) (bool, error) {
	existe, err := s.existeReversao(ctx, r, referente.ID(), kind)
	if err != nil || existe {
		return existe, err
	}

	if referente.Kind() != wagerdomain.KindBet {
		return false, nil
	}

	switch kind {
	case wagerdomain.KindRollback:
		// A BET já foi estornada por REFUND: desfazê-la creditaria de novo.
		return s.existeReversao(ctx, r, referente.ID(), wagerdomain.KindRefund)
	case wagerdomain.KindRefund:
		// A BET já foi desfeita por ROLLBACK: estorná-la creditaria de novo.
		return s.existeReversao(ctx, r, referente.ID(), wagerdomain.KindRollback)
	}
	return false, nil
}

// existeReversao consulta uma reversão já aplicada sobre a referência.
func (s *Service) existeReversao(ctx context.Context, r ports.Repos, referencia wagerdomain.TransactionID, kind wagerdomain.Kind) (bool, error) {
	_, err := r.Transactions.GetByReferenceAndKind(ctx, referencia, kind)
	switch {
	case err == nil:
		return true, nil
	case ports.IsNotFound(err):
		return false, nil
	default:
		return false, err
	}
}

// direcaoDoMovimento diz para onde o dinheiro vai.
//
// BET debita; WIN e REFUND creditam; ROLLBACK é o CONTRÁRIO do movimento que ele
// desfaz — desfazer uma BET devolve (crédito), desfazer um WIN ou um REFUND tira
// (débito). LOSS não chega aqui: não movimenta dinheiro.
func direcaoDoMovimento(kind wagerdomain.Kind, referente *wagerdomain.WagerTransaction) (ledger.Direction, error) {
	switch kind {
	case wagerdomain.KindBet:
		return ledger.DirectionDebit, nil
	case wagerdomain.KindWin, wagerdomain.KindRefund:
		return ledger.DirectionCredit, nil
	case wagerdomain.KindRollback:
		if referente == nil {
			return "", invalid(CodeReferenceMismatch, "ROLLBACK sem referência resolvida")
		}
		return direcaoContraria(referente.Kind())
	}
	return "", invalid(CodeInvalidRequest, "tipo %q não movimenta dinheiro", kind)
}

// direcaoContraria devolve o sentido oposto ao movimento do tipo referenciado.
func direcaoContraria(kind wagerdomain.Kind) (ledger.Direction, error) {
	switch kind {
	case wagerdomain.KindBet:
		return ledger.DirectionCredit, nil
	case wagerdomain.KindWin, wagerdomain.KindRefund:
		return ledger.DirectionDebit, nil
	}
	return "", invalid(CodeReferenceMismatch, "ROLLBACK não desfaz a operação %q", kind)
}

// mesmoValor compara o valor da reversão com o valor referenciado.
func mesmoValor(referente, solicitado money.Money) bool {
	igual, err := referente.Equal(solicitado)
	return err == nil && igual
}
