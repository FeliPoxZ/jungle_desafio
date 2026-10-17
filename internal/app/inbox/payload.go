package inbox

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	domainerrors "github.com/FeliPoxZ/jungle_desafio/internal/domain/errors"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/platform/idgen"
)

// envelope é o corpo da mensagem do broker (README §10).
//
// `type` é o nome do evento que PEDE a operação: "WagerTransactionRequested".
// Um envelope com outro `type` não é uma operação — recusar é melhor do que
// tentar interpretar.
type envelope struct {
	MessageID  string        `json:"messageId"`
	Type       string        `json:"type"`
	OccurredAt string        `json:"occurredAt"`
	Data       dadosOperacao `json:"data"`
}

// dadosOperacao são os campos de negócio da mensagem.
//
// Os nomes espelham EXATAMENTE o corpo do `POST /wagering/transactions` do README
// §9: é o que faz a mesma operação, vinda pelas duas portas, produzir a mesma
// requisição — e, portanto, o mesmo hash de idempotência.
type dadosOperacao struct {
	ProviderID             string      `json:"providerId"`
	ExternalTransactionID  string      `json:"externalTransactionId"`
	IdempotencyKey         string      `json:"idempotencyKey"`
	PlayerID               string      `json:"playerId"`
	WalletID               string      `json:"walletId"`
	RoundID                string      `json:"roundId"`
	GameID                 string      `json:"gameId"`
	Kind                   string      `json:"kind"`
	Money                  money.Money `json:"money"`
	ReferenceExternalTxnID string      `json:"referenceExternalTransactionId"`
}

// TipoEsperado é o `type` do envelope que pede uma operação.
const TipoEsperado = "WagerTransactionRequested"

// decodificar traduz o corpo da mensagem na requisição do caso de uso.
//
// # Por que a decodificação é estrita
//
// Um campo escrito errado no produtor (`waletId`, `kind` com espaço a mais) seria
// ignorado em silêncio por um decoder permissivo, e a operação chegaria ao caso de
// uso com campo vazio — virando "entrada inválida" (400/422) em vez de "mensagem
// malformada" (DLQ). Recusar campo desconhecido faz o erro aparecer onde ele está:
// no produtor.
func decodificar(corpo []byte) (appwagering.Request, error) {
	if len(corpo) == 0 {
		return appwagering.Request{}, fmt.Errorf("%w: corpo vazio", ErrMensagemInvalida)
	}

	var env envelope
	if err := decodificarEstrito(corpo, &env); err != nil {
		return appwagering.Request{}, fmt.Errorf("%w: %v", ErrMensagemInvalida, err)
	}

	if env.Type != TipoEsperado {
		return appwagering.Request{}, fmt.Errorf("%w: type %q, esperado %q",
			ErrMensagemInvalida, env.Type, TipoEsperado)
	}

	d := env.Data
	if err := validarCamposObrigatorios(d); err != nil {
		return appwagering.Request{}, err
	}

	// `OPENING` é reservado à abertura interna de carteira (README §6.3). A
	// checagem também existe no caso de uso, mas aqui ela acontece ANTES de
	// abrir transação: mensagem que nunca será aplicada não deve sujar a inbox.
	kind := wagerdomain.Kind(d.Kind)
	if err := kind.Validate(); err != nil {
		return appwagering.Request{}, fmt.Errorf("%w: kind %q", ErrMensagemInvalida, d.Kind)
	}
	if kind.IsInternal() {
		return appwagering.Request{}, fmt.Errorf("%w: kind %q é reservado ao serviço interno",
			ErrMensagemInvalida, d.Kind)
	}

	return appwagering.Request{
		ProviderID:             d.ProviderID,
		ExternalTransactionID:  d.ExternalTransactionID,
		IdempotencyKey:         d.IdempotencyKey,
		PlayerID:               ids.PlayerID(d.PlayerID),
		WalletID:               ids.WalletID(d.WalletID),
		RoundID:                d.RoundID,
		GameID:                 d.GameID,
		Kind:                   kind,
		Amount:                 d.Money,
		ReferenceExternalTxnID: d.ReferenceExternalTxnID,
	}, nil
}

// validarCamposObrigatorios recusa a mensagem que nem chega a ser uma operação.
//
// A validação é feita aqui (e não só no caso de uso) porque a mensagem inválida
// precisa ser identificada como PERMANENTE antes de qualquer escrita: é isso que
// a manda para a DLQ em vez de ficar voltando para a fila.
func validarCamposObrigatorios(d dadosOperacao) error {
	faltando := func(campo, valor string) error {
		if strings.TrimSpace(valor) == "" {
			return fmt.Errorf("%w: %s é obrigatório", ErrMensagemInvalida, campo)
		}
		return nil
	}

	for _, campo := range []struct{ nome, valor string }{
		{"data.providerId", d.ProviderID},
		{"data.externalTransactionId", d.ExternalTransactionID},
		{"data.idempotencyKey", d.IdempotencyKey},
		{"data.playerId", d.PlayerID},
		{"data.walletId", d.WalletID},
		{"data.roundId", d.RoundID},
		{"data.gameId", d.GameID},
		{"data.kind", d.Kind},
	} {
		if err := faltando(campo.nome, campo.valor); err != nil {
			return err
		}
	}

	// `money` é um value object: o parsing já recusou notação científica, escala
	// excedente e moeda inválida. Aqui só se confere que ele veio.
	if strings.TrimSpace(d.Money.Currency().String()) == "" {
		return fmt.Errorf("%w: data.money.currency é obrigatório", ErrMensagemInvalida)
	}
	return nil
}

// codigoDeEntrada devolve o `Code` quando o erro é de ENTRADA (nada foi gravado e
// reenviar a mesma mensagem não resolve), ou vazio.
//
// A lista é a mesma da borda HTTP (`httpapi/erros.go`), e é de propósito: as duas
// portas precisam concordar sobre o que é "entrada inválida". Se elas divergissem,
// a mesma mensagem seria permanente por HTTP e transitória por SQS.
func codigoDeEntrada(err error) string {
	switch code := domainerrors.CodeOf(err); code {
	case appwagering.CodeInvalidRequest,
		appwagering.CodeIdempotencyKeyRequired,
		"INVALID_AMOUNT",
		"REFERENCE_REQUIRED",
		"INVALID_TRANSACTION",
		"INVALID_CURRENCY",
		"CURRENCY_MISMATCH",
		"INVALID_MONEY",
		"OPERATION_NOT_ALLOWED":
		return code
	}

	// Conflito de identidade financeira não é transitório nem de entrada: é o
	// pedido reenviado sob OUTRA chave. A mensagem está bem formada, e a mesma
	// mensagem reenviada continuaria conflitando — então é permanente.
	if errors.Is(err, appwagering.ErrIdempotencyConflict) {
		return appwagering.CodeIdempotencyConflict
	}
	return ""
}

// decodificarEstrito decodifica o JSON recusando campo desconhecido.
//
// A mesma régua do `decodeJSON` da borda HTTP: as duas portas aceitam e recusam
// exatamente os mesmos corpos. Duas réguas diferentes para o mesmo contrato é o
// caminho mais curto para o bug "funciona por HTTP e não por SQS".
func decodificarEstrito(corpo []byte, destino any) error {
	dec := json.NewDecoder(bytes.NewReader(corpo))
	dec.DisallowUnknownFields()
	dec.UseNumber()

	if err := dec.Decode(destino); err != nil {
		return err
	}
	// Um segundo documento JSON no mesmo corpo também é recusado: qual deles
	// valeria?
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("o corpo deve conter um único objeto JSON")
	}
	return nil
}

// digestDoCorpo devolve o SHA-256 do corpo em hexadecimal.
//
// É o `body_hash` da inbox. Guarda o CONTEÚDO: hash igual é reentrega da mesma
// mensagem (caminho esperado), hash diferente é conflito real — mensagem forjada
// ou bug de produtor reutilizando o identificador.
func digestDoCorpo(corpo []byte) string {
	soma := sha256.Sum256(corpo)
	return hex.EncodeToString(soma[:])
}

// novoID gera o identificador do registro da inbox.
//
// É uma variável (e não uma função que chama o gerador direto) para o teste poder
// fixar o valor — a mesma razão de o caso de uso receber `idgen.Generator` por
// injeção.
var novoID = func() string { return gerador.NewID() }

// gerador é o gerador de identificadores do processo.
//
// Poderia ser injetado pelo Fx (como no caso de uso), mas o identificador da
// inbox não participa de nenhuma asserção de negócio: ele é só a chave primária
// de uma linha de auditoria. Um valor injetado adicionaria um parâmetro a mais em
// todo o caminho por nenhum ganho de testabilidade.
var gerador idgen.Generator = idgen.New()

// agora é o relógio do processo, em UTC. Variável pelo mesmo motivo de `novoID`:
// o teste precisa afirmar `completed_at` sem esperar o relógio andar.
var agora = func() time.Time { return time.Now().UTC() }
