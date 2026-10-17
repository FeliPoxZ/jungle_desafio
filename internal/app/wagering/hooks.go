package wagering

import (
	"context"
	"errors"
	"fmt"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
)

// ProcessHooks são os ganchos que rodam DENTRO da transação de uma operação.
//
// # Por que eles existem
//
// O README §6.5 exige que, na entrada por SQS, "o registro da inbox e a conclusão
// durável do tratamento compartilhem a transação SQL das alterações de domínio,
// do ledger e dos eventos correspondentes". Sem ganchos, o registro da inbox
// ficaria numa transação própria — e uma queda entre as duas deixaria o débito
// feito e a mensagem sem registro (ou o inverso). Uma transação só resolve isso.
//
// O gancho é a forma de estender o caso de uso sem que ele passe a conhecer SQS,
// broker ou inbox: quem implementa o gancho é a aplicação, e ela decide o que
// significa "começar" e "concluir" para aquela porta de entrada.
type ProcessHooks struct {
	// OnBegin roda ANTES de qualquer leitura de domínio.
	//
	// Devolver `ErrIdempotentSkip` aborta a transação SEM erro e devolve
	// `IdempotentReplay: true`: é a mensagem reentregue cujo tratamento durável
	// já foi confirmado antes.
	OnBegin func(ctx context.Context, r ports.Repos) error

	// OnCommit roda depois do domínio e antes do commit, com o desfecho em mãos.
	//
	// O desfecho é a informação que o gancho precisa para decidir como marcar a
	// conclusão: `PROCESSED` e `PENDING_REFERENCE` concluíram; `REJECTED` foi
	// recusada por regra de negócio (terminal, e também concluída).
	OnCommit func(ctx context.Context, r ports.Repos, res Result) error
}

// ErrIdempotentSkip sinaliza "este trabalho já foi feito; encerre a transação sem
// mudar nada".
//
// É um erro porque é a única forma de interromper o `uow.Run` de dentro do
// gancho — mas NÃO é uma falha: o serviço o traduz em resultado de replay, e o
// consumidor o trata como sucesso. Ele nunca chega ao chamador externo.
var ErrIdempotentSkip = errors.New("wagering: trabalho já confirmado; nada a fazer")

// ErrInboxBodyConflict é a MESMA identidade de mensagem chegando com conteúdo
// diferente.
//
// Não é reentrega (o SQS garante entrega "pelo menos uma vez", não "conteúdo
// diferente"): é mensagem forjada, bug do produtor ou corrupção. Repetir não
// resolve, então o consumidor trata como falha PERMANENTE (DLQ), e não como
// transitória.
var ErrInboxBodyConflict = errors.New("wagering: mesma mensagem com conteúdo diferente")

// replayResult é o desfecho de uma mensagem que já havia sido tratada.
func replayResult() Result {
	return Result{IdempotentReplay: true}
}

// resolverGancho traduz o erro devolvido pelo `OnBegin`.
//
// `ErrIdempotentSkip` vira replay; o resto sobe. É aqui que a decisão de "pular"
// fica num lugar só — e é isto que impede o consumidor de precisar conhecer o
// sentinela do caso de uso.
func resolverGancho(err error) (Result, bool, error) {
	if err == nil {
		return Result{}, false, nil
	}
	if errors.Is(err, ErrIdempotentSkip) {
		return replayResult(), true, nil
	}
	return Result{}, false, fmt.Errorf("wagering: gancho da operação: %w", err)
}
