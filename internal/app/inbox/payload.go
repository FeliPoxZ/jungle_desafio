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

// operacao é o resultado da tradução: a identidade durável da mensagem e a
// requisição pronta para o caso de uso.
type operacao struct {
	// MessageID vem do ENVELOPE (`messageId`), e é a identidade durável para o
	// consumidor — a chave da deduplicação na inbox.
	//
	// # Por que o do envelope, e não o do broker
	//
	// O README §10 é explícito: "use o `messageId` do envelope como identidade
	// durável da mensagem para o consumidor e verifique seu hash em reentregas".
	// O corpo de exemplo do enunciado traz `"messageId": "msg-123"` no ENVELOPE —
	// não é o identificador interno do SQS.
	//
	// A distinção não é cosmética: o `MessageId` do broker é gerado a cada ENVIO
	// (um reenvio pelo produtor cria outro), enquanto o do envelope é a identidade
	// que o PRODUTOR escolheu — é ela que sobrevive a um reenvio e permite
	// reconhecer "esta mensagem eu já tratei".
	MessageID string

	// IDDoBroker é o identificador interno do SQS: usado em log e no
	// `MessageDeduplicationId` da DLQ.
	IDDoBroker string

	// Request é a operação, no MESMO formato que a porta HTTP produz.
	Request appwagering.Request
}

// decodificar traduz o corpo da mensagem na requisição do caso de uso.
//
// # Por que a decodificação é estrita
//
// Um campo escrito errado no produtor (`waletId`, `kind` com espaço a mais) seria
// ignorado em silêncio por um decoder permissivo, e a operação chegaria ao caso de
// uso com campo vazio — virando "entrada inválida" (400/422) em vez de "mensagem
// malformada" (DLQ). Recusar campo desconhecido faz o erro aparecer onde ele está:
// no produtor.
func decodificar(corpo []byte, idDoBroker string) (operacao, error) {
	if len(corpo) == 0 {
		return operacao{}, fmt.Errorf("%w: corpo vazio", ErrMensagemInvalida)
	}

	var env envelope
	if err := decodificarEstrito(corpo, &env); err != nil {
		return operacao{}, fmt.Errorf("%w: %v", ErrMensagemInvalida, err)
	}

	if env.Type != TipoEsperado {
		return operacao{}, fmt.Errorf("%w: type %q, esperado %q",
			ErrMensagemInvalida, env.Type, TipoEsperado)
	}
	if idDoEnvelope := strings.TrimSpace(env.MessageID); idDoEnvelope != "" {
		// Quando o envelope traz o campo, ele precisa CONCORDAR com o
		// identificador da entrega.
		//
		// Divergência significa produtor errado ou mensagem remontada no caminho —
		// resolver em silêncio faria a deduplicação depender de qual dos dois o
		// código leu. Recusar expõe o problema na hora, e é permanente (repetir não
		// conserta).
		if idDoBroker != "" && idDoEnvelope != idDoBroker {
			return operacao{}, fmt.Errorf("%w: messageId do envelope (%q) difere do id da entrega (%q)",
				ErrMensagemInvalida, idDoEnvelope, idDoBroker)
		}
	}

	d := env.Data
	if err := validarCamposObrigatorios(d); err != nil {
		return operacao{}, err
	}

	// `OPENING` é reservado à abertura interna de carteira (README §6.3). A
	// checagem também existe no caso de uso, mas aqui ela acontece ANTES de
	// abrir transação: mensagem que nunca será aplicada não deve sujar a inbox.
	kind := wagerdomain.Kind(d.Kind)
	if err := kind.Validate(); err != nil {
		return operacao{}, fmt.Errorf("%w: kind %q", ErrMensagemInvalida, d.Kind)
	}
	if kind.IsInternal() {
		return operacao{}, fmt.Errorf("%w: kind %q é reservado ao serviço interno",
			ErrMensagemInvalida, d.Kind)
	}

	return operacao{
		// O `messageId` do ENVELOPE é OPCIONAL, e a decisão é deliberada.
		//
		// O corpo de exemplo do README §10 traz `"messageId": "msg-123"`, mas quem
		// ATRIBUI esse valor é o broker, no `SendMessage` — o produtor não tem como
		// conhecê-lo antes de enviar (o valor só existe na resposta do envio).
		// Exigir o campo obrigaria o produtor a inventar um SEGUNDO identificador
		// para a mesma mensagem.
		//
		// Então: a identidade durável do consumo é o `MessageId` DO BROKER, e o
		// campo do envelope, quando presente, é conferido contra ela.
		MessageID:  escolherID(env.MessageID, idDoBroker),
		IDDoBroker: idDoBroker,
		Request: appwagering.Request{
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
		},
	}, nil
}

// escolherID devolve a identidade da mensagem: a do envelope, quando veio (já
// conferida contra a da entrega), ou a do broker.
func escolherID(idDoEnvelope, idDoBroker string) string {
	if id := strings.TrimSpace(idDoEnvelope); id != "" {
		return id
	}
	return idDoBroker
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
