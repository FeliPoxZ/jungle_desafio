package inbox

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	domainerrors "github.com/FeliPoxZ/jungle_desafio/internal/domain/errors"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
)

// Testes da porta de entrada por SQS.
//
// O que eles cobrem, e por que valem a pena mesmo tendo integração com LocalStack:
//
//   - a TRADUÇÃO da mensagem para a requisição (campo por campo, com a régua
//     estrita);
//   - a POLÍTICA de deduplicação: reentrega, corpo divergente, mensagem inválida;
//   - a CLASSIFICAÇÃO do erro (transitório × permanente), que decide entre "volta
//     para a fila" e "vai para a DLQ" — a decisão mais consequente do consumidor.
//
// Os ganchos são exercitados de VERDADE: o dublê do caso de uso executa os ganchos
// e traduz `ErrIdempotentSkip` em replay, como o serviço real faz. Testar o gancho
// por chamada direta provaria a função, não a composição.

// --- dublê do caso de uso -----------------------------------------------------

// servicoFalso executa os ganchos sobre um `ports.Repos` e devolve o desfecho que
// o teste mandar.
//
// Ele imita o `Service` no que importa para a inbox: "abrir gancho → trabalho →
// concluir gancho", com o replay curto-circuitando o trabalho.
type servicoFalso struct {
	inbox *inboxFalsa

	// O que o teste quer que a operação devolva.
	resultado appwagering.Result
	erro      error

	// Captura do que chegou, para as asserções da tradução.
	ultimaReq appwagering.Request

	// chamadas conta quantas vezes o caso de uso foi INVOCADO. É diferente de
	// `trabalhoAplicado`: numa reentrega a invocação acontece e o trabalho não.
	chamadas int

	// trabalhoAplicado conta quantas vezes o DOMÍNIO foi executado e o gancho de
	// conclusão rodou. É este número que diz "não reaplicou".
	trabalhoAplicado int
}

func (s *servicoFalso) ProcessWithHooks(_ context.Context, req appwagering.Request, hooks *appwagering.ProcessHooks) (appwagering.Result, error) {
	s.chamadas++
	s.ultimaReq = req

	repos := ports.Repos{Inbox: s.inbox}

	if hooks != nil && hooks.OnBegin != nil {
		err := hooks.OnBegin(context.Background(), repos)
		if errors.Is(err, appwagering.ErrIdempotentSkip) {
			// Mesmo comportamento do serviço real: replay, SEM executar o
			// domínio e sem rodar o gancho de conclusão.
			return appwagering.Result{IdempotentReplay: true}, nil
		}
		if err != nil {
			return appwagering.Result{}, err
		}
	}

	if s.erro != nil {
		return appwagering.Result{}, s.erro
	}

	s.trabalhoAplicado++

	if hooks != nil && hooks.OnCommit != nil {
		if err := hooks.OnCommit(context.Background(), repos, s.resultado); err != nil {
			return appwagering.Result{}, err
		}
	}
	return s.resultado, nil
}

// --- dublê do repositório da inbox -------------------------------------------

type inboxFalsa struct {
	registros map[string]*ports.InboxMessage
	// erroDeLeitura força falha no `Get`, para exercitar o caminho transitório.
	erroDeLeitura error
}

func novaInboxFalsa() *inboxFalsa {
	return &inboxFalsa{registros: map[string]*ports.InboxMessage{}}
}

func chaveDaInbox(consumer, messageID string) string { return consumer + "|" + messageID }

func (f *inboxFalsa) InsertIfAbsent(_ context.Context, m ports.InboxMessage) (bool, error) {
	k := chaveDaInbox(m.ConsumerName, m.MessageID)
	if _, existe := f.registros[k]; existe {
		return false, nil
	}
	copia := m
	// Mesma normalização do repositório real: uma linha na inbox significa PELO
	// MENOS uma entrega.
	if copia.Attempts < 1 {
		copia.Attempts = 1
	}
	f.registros[k] = &copia
	return true, nil
}

func (f *inboxFalsa) Get(_ context.Context, consumerName, messageID string) (*ports.InboxMessage, error) {
	if f.erroDeLeitura != nil {
		return nil, f.erroDeLeitura
	}
	m, existe := f.registros[chaveDaInbox(consumerName, messageID)]
	if !existe {
		return nil, ports.ErrNotFound
	}
	copia := *m
	return &copia, nil
}

func (f *inboxFalsa) CountAttempt(_ context.Context, consumerName, messageID string, _ time.Time) error {
	if m, existe := f.registros[chaveDaInbox(consumerName, messageID)]; existe {
		m.Attempts++
	}
	return nil
}

func (f *inboxFalsa) MarkProcessed(_ context.Context, consumerName, messageID string, now time.Time) error {
	return f.marcar(consumerName, messageID, "PROCESSED", "", now)
}

func (f *inboxFalsa) MarkRejected(_ context.Context, consumerName, messageID, failureCode string, now time.Time) error {
	return f.marcar(consumerName, messageID, "REJECTED", failureCode, now)
}

func (f *inboxFalsa) MarkFailed(_ context.Context, consumerName, messageID, reason string, now time.Time) error {
	return f.marcar(consumerName, messageID, "FAILED", reason, now)
}

func (f *inboxFalsa) marcar(consumerName, messageID, status, failureCode string, now time.Time) error {
	m, existe := f.registros[chaveDaInbox(consumerName, messageID)]
	if !existe {
		return ports.ErrNotFound
	}
	m.Status = status
	m.FailureCode = failureCode
	m.CompletedAt = now
	return nil
}

// --- corpo de mensagem --------------------------------------------------------

// corpoDeOperacao monta o corpo do envelope como o README §10 o descreve.
//
// O `messageId` do ENVELOPE é parametrizável porque ele é a identidade durável
// usada na inbox: o teste precisa alinhá-lo com o identificador da entrega (o
// processador recusa divergência) e, num caso, omiti-lo.
func corpoDeOperacao(kind, valor string) string {
	return corpoComMessageID("msg-1", kind, valor)
}

// corpoComMessageID é o corpo com um `messageId` de envelope específico.
func corpoComMessageID(messageID, kind, valor string) string {
	return fmt.Sprintf(`{
      "messageId": %q,
      "type": "WagerTransactionRequested",
      "occurredAt": "2026-09-08T12:00:00.000Z",
      "data": {
        "providerId": "provider-a",
        "externalTransactionId": "transaction-123",
        "idempotencyKey": "provider-a:transaction-123",
        "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
        "walletId": "0192f291-27dd-7d3f-8071-5f8685deef37",
        "roundId": "round-987",
        "gameId": "fortune-chimp",
        "kind": %q,
        "money": { "amount": %q, "currency": "BRL" }
      }
    }`, messageID, kind, valor)
}

// --- tradução da mensagem -----------------------------------------------------

// A mensagem vira a MESMA requisição que o HTTP produz.
//
// Esta é a asserção central da equivalência HTTP × SQS: campo por campo, do
// envelope para o `Request` do caso de uso. A chave de idempotência vem do CORPO
// (`data.idempotencyKey`), conforme o README §10.
func TestDecodificarMapeiaTodosOsCampos(t *testing.T) {
	// O `messageId` do envelope e o da entrega coincidem: é o contrato do
	// produtor deste projeto, e o processador recusa divergência.
	const idDaEntrega = "msg-1"

	op, err := decodificar([]byte(corpoDeOperacao("BET", "25.00")), idDaEntrega)
	if err != nil {
		t.Fatalf("decodificar devolveu erro: %v", err)
	}
	if op.MessageID != idDaEntrega {
		t.Errorf("MessageID = %q, quer %q (a identidade durável é a do ENVELOPE)", op.MessageID, idDaEntrega)
	}
	if op.IDDoBroker != idDaEntrega {
		t.Errorf("IDDoBroker = %q, quer %q", op.IDDoBroker, idDaEntrega)
	}
	req := op.Request

	esperado := struct {
		providerID, externalID, chave, playerID, walletID, roundID, gameID, kind string
		valor                                                                    int64
	}{
		providerID: "provider-a",
		externalID: "transaction-123",
		chave:      "provider-a:transaction-123",
		playerID:   "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
		walletID:   "0192f291-27dd-7d3f-8071-5f8685deef37",
		roundID:    "round-987",
		gameID:     "fortune-chimp",
		kind:       "BET",
		valor:      2500,
	}

	if req.ProviderID != esperado.providerID {
		t.Errorf("ProviderID = %q, quer %q", req.ProviderID, esperado.providerID)
	}
	if req.ExternalTransactionID != esperado.externalID {
		t.Errorf("ExternalTransactionID = %q, quer %q", req.ExternalTransactionID, esperado.externalID)
	}
	if req.IdempotencyKey != esperado.chave {
		t.Errorf("IdempotencyKey = %q, quer %q (vem do CORPO, não do MessageId)", req.IdempotencyKey, esperado.chave)
	}
	if string(req.PlayerID) != esperado.playerID {
		t.Errorf("PlayerID = %q, quer %q", req.PlayerID, esperado.playerID)
	}
	if string(req.WalletID) != esperado.walletID {
		t.Errorf("WalletID = %q, quer %q", req.WalletID, esperado.walletID)
	}
	if req.RoundID != esperado.roundID || req.GameID != esperado.gameID {
		t.Errorf("roundId/gameId = %q/%q, quer %q/%q", req.RoundID, req.GameID, esperado.roundID, esperado.gameID)
	}
	if req.Kind != wagerdomain.Kind(esperado.kind) {
		t.Errorf("Kind = %q, quer %q", req.Kind, esperado.kind)
	}
	if req.Amount.Minor() != esperado.valor {
		t.Errorf("Amount = %d unidades mínimas, quer %d", req.Amount.Minor(), esperado.valor)
	}
}

// A tradução da mensagem é ESTRITA, como a da borda HTTP.
func TestDecodificarRejeitaMensagemInvalida(t *testing.T) {
	casos := map[string]string{
		"corpo vazio":        ``,
		"não é JSON":         `{isto não é json`,
		"campo desconhecido": `{"messageId":"m","type":"WagerTransactionRequested","data":{},"extra":1}`,
		"type errado":        `{"messageId":"m","type":"OutroEvento","data":{}}`,
		"type ausente":       `{"messageId":"m","data":{}}`,
		"sem providerId":     `{"messageId":"m","type":"WagerTransactionRequested","data":{"externalTransactionId":"x","idempotencyKey":"k","playerId":"p","walletId":"w","roundId":"r","gameId":"g","kind":"BET","money":{"amount":"1.00","currency":"BRL"}}}`,
		"sem chave":          `{"messageId":"m","type":"WagerTransactionRequested","data":{"providerId":"a","externalTransactionId":"x","playerId":"p","walletId":"w","roundId":"r","gameId":"g","kind":"BET","money":{"amount":"1.00","currency":"BRL"}}}`,
		"sem money":          `{"messageId":"m","type":"WagerTransactionRequested","data":{"providerId":"a","externalTransactionId":"x","idempotencyKey":"k","playerId":"p","walletId":"w","roundId":"r","gameId":"g","kind":"BET"}}`,
		"kind desconhecido":  `{"messageId":"m","type":"WagerTransactionRequested","data":{"providerId":"a","externalTransactionId":"x","idempotencyKey":"k","playerId":"p","walletId":"w","roundId":"r","gameId":"g","kind":"PIX","money":{"amount":"1.00","currency":"BRL"}}}`,
		"dois documentos":    `{"messageId":"m","type":"WagerTransactionRequested","data":{}}{"messageId":"m2"}`,
		"escala excedente":   `{"messageId":"m","type":"WagerTransactionRequested","data":{"providerId":"a","externalTransactionId":"x","idempotencyKey":"k","playerId":"p","walletId":"w","roundId":"r","gameId":"g","kind":"BET","money":{"amount":"1.000","currency":"BRL"}}}`,
		"notação científica": `{"messageId":"m","type":"WagerTransactionRequested","data":{"providerId":"a","externalTransactionId":"x","idempotencyKey":"k","playerId":"p","walletId":"w","roundId":"r","gameId":"g","kind":"BET","money":{"amount":"1e2","currency":"BRL"}}}`,
	}

	for nome, corpo := range casos {
		t.Run(nome, func(t *testing.T) {
			_, err := decodificar([]byte(corpo), "")
			if err == nil {
				t.Fatal("decodificar aceitou uma mensagem que deveria ser recusada")
			}
			if !errors.Is(err, ErrMensagemInvalida) {
				t.Errorf("causa = %v, quer ErrMensagemInvalida", err)
			}
		})
	}
}

// `OPENING` é reservado à abertura interna de carteira (README §6.3): uma
// mensagem que o pede nunca será aplicada, então é recusada ANTES de abrir
// transação — não suja a inbox.
func TestDecodificarRejeitaOPENING(t *testing.T) {
	_, err := decodificar([]byte(corpoDeOperacao("OPENING", "100.00")), "")
	if err == nil {
		t.Fatal("decodificar aceitou OPENING vindo do broker")
	}
	if !errors.Is(err, ErrMensagemInvalida) {
		t.Errorf("causa = %v, quer ErrMensagemInvalida", err)
	}
}

// --- política de deduplicação -------------------------------------------------

func processadorDeTeste(t *testing.T) (*Processador, *servicoFalso, *inboxFalsa) {
	t.Helper()

	inbox := novaInboxFalsa()
	servico := &servicoFalso{inbox: inbox}
	return NovoProcessador(servico), servico, inbox
}

// Mensagem nova: processada e registrada como concluída.
func TestProcessarMensagemNova(t *testing.T) {
	p, servico, inbox := processadorDeTeste(t)
	servico.resultado = resultadoProcessado()

	corpo := corpoDeOperacao("BET", "25.00")
	res, err := p.Processar(context.Background(), Entrada{IDDaEntrega: "msg-1", Corpo: []byte(corpo)})
	if err != nil {
		t.Fatalf("Processar devolveu erro: %v", err)
	}

	if !res.Processada || res.Duplicada {
		t.Errorf("resultado = %+v, quer Processada sem Duplicada", res)
	}
	if res.Status != "PROCESSED" {
		t.Errorf("status = %q, quer PROCESSED", res.Status)
	}

	registro, err := inbox.Get(context.Background(), ConsumerName, "msg-1")
	if err != nil {
		t.Fatalf("mensagem não ficou registrada na inbox: %v", err)
	}
	if registro.Status != "PROCESSED" {
		t.Errorf("status na inbox = %q, quer PROCESSED", registro.Status)
	}
	if registro.CompletedAt.IsZero() {
		t.Error("completed_at não foi preenchido (a conclusão precisa de instante)")
	}
	if registro.BodyHash == "" {
		t.Error("body_hash vazio: sem ele não há como detectar corpo divergente")
	}
	if servico.chamadas != 1 {
		t.Errorf("o caso de uso foi chamado %d vezes, quer 1", servico.chamadas)
	}
}

// REENTREGA da mesma mensagem: nada é reaplicado, e o resultado diz "duplicada".
//
// É o cenário do SQS *at-least-once*, e o que a inbox existe para cobrir.
func TestProcessarReentregaNaoReaplica(t *testing.T) {
	p, servico, inbox := processadorDeTeste(t)
	servico.resultado = resultadoProcessado()

	corpo := []byte(corpoDeOperacao("BET", "25.00"))

	// Primeira entrega.
	if _, err := p.Processar(context.Background(), Entrada{IDDaEntrega: "msg-1", Corpo: corpo}); err != nil {
		t.Fatalf("primeira entrega: %v", err)
	}

	// Segunda entrega da MESMA mensagem (mesmo identificador e mesmo corpo).
	res, err := p.Processar(context.Background(), Entrada{IDDaEntrega: "msg-1", Corpo: corpo})
	if err != nil {
		t.Fatalf("segunda entrega devolveu erro: %v", err)
	}

	if !res.Duplicada {
		t.Errorf("resultado = %+v, quer Duplicada", res)
	}
	if !res.Processada {
		t.Error("mensagem duplicada precisa ser removível da fila (Processada = true)")
	}

	// O TRABALHO não foi refeito — que é o que importa. A invocação do caso de
	// uso aconteceu (é ela que detecta a duplicata), mas o domínio não rodou.
	if servico.trabalhoAplicado != 1 {
		t.Errorf("o domínio foi aplicado %d vezes; a reentrega não pode reaplicar", servico.trabalhoAplicado)
	}
	if servico.chamadas != 2 {
		t.Errorf("invocações = %d, quer 2 (a segunda é a que reconhece a duplicata)", servico.chamadas)
	}

	// O contador durável de entregas chega a 2: a primeira no registro, a segunda
	// na reentrega. É o que sobra quando o `ApproximateReceiveCount` do broker se
	// perde (mensagem removida ou morta na DLQ).
	registro, _ := inbox.Get(context.Background(), ConsumerName, "msg-1")
	if registro.Attempts != 2 {
		t.Errorf("attempts = %d, quer 2 (uma entrega no registro, uma na reentrega)", registro.Attempts)
	}
}

// MESMA identidade de mensagem com CORPO DIFERENTE: erro permanente.
//
// Não é reentrega — o SQS garante entrega "pelo menos uma vez", não "conteúdo
// diferente". Repetir não resolve, então a mensagem vai para a DLQ.
func TestProcessarCorpoDivergenteEhPermanente(t *testing.T) {
	p, servico, _ := processadorDeTeste(t)
	servico.resultado = resultadoProcessado()

	ctx := context.Background()
	if _, err := p.Processar(ctx, Entrada{IDDaEntrega: "msg-1", Corpo: []byte(corpoDeOperacao("BET", "25.00"))}); err != nil {
		t.Fatalf("primeira entrega: %v", err)
	}

	// Mesmo messageId, valor diferente.
	_, err := p.Processar(ctx, Entrada{IDDaEntrega: "msg-1", Corpo: []byte(corpoDeOperacao("BET", "99.00"))})
	if err == nil {
		t.Fatal("corpo divergente foi aceito")
	}
	if !errors.Is(err, ErrCorpoDivergente) {
		t.Errorf("causa = %v, quer ErrCorpoDivergente", err)
	}
}

// O `messageId` do ENVELOPE é OPCIONAL — quem gera a identidade da mensagem é o
// broker, no envio.
//
// O corpo de exemplo do README §10 traz o campo, mas o produtor não tem como
// conhecê-lo: o `MessageId` é atribuído pelo SQS e devolvido em `SendMessage`.
// Exigir o campo obrigaria o produtor a inventar um segundo identificador para a
// mesma mensagem — e aí existiriam dois, que podem divergir.
func TestProcessarSemMessageIDNoEnvelopeEhAceito(t *testing.T) {
	p, servico, inbox := processadorDeTeste(t)
	servico.resultado = resultadoProcessado()

	semID := `{"type":"WagerTransactionRequested","data":{"providerId":"a","externalTransactionId":"x","idempotencyKey":"k","playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1","walletId":"0192f291-27dd-7d3f-8071-5f8685deef37","roundId":"r","gameId":"g","kind":"BET","money":{"amount":"1.00","currency":"BRL"}}}`

	res, err := p.Processar(context.Background(), Entrada{IDDaEntrega: "msg-sem-id", Corpo: []byte(semID)})
	if err != nil {
		t.Fatalf("mensagem sem messageId no envelope foi recusada: %v", err)
	}
	if !res.Processada {
		t.Errorf("resultado = %+v, quer Processada", res)
	}

	// E o registro da inbox usa o identificador do BROKER.
	if _, _, _, existe := estadoDaInboxFalsa(t, inbox, "msg-sem-id"); !existe {
		t.Error("a mensagem não foi registrada na inbox com o identificador da entrega")
	}
}

// Divergência entre o `messageId` do envelope e o identificador da entrega é
// PERMANENTE (não se conserta repetindo).
func TestProcessarComIdentidadeDivergenteEhInvalida(t *testing.T) {
	p, _, _ := processadorDeTeste(t)

	corpo := corpoComMessageID("identidade-do-envelope", "BET", "1.00")

	_, err := p.Processar(context.Background(), Entrada{IDDaEntrega: "outra-identidade", Corpo: []byte(corpo)})
	if err == nil {
		t.Fatal("mensagem com identidade divergente foi aceita")
	}
	if !errors.Is(err, ErrMensagemInvalida) {
		t.Errorf("causa = %v, quer ErrMensagemInvalida", err)
	}
}

// estadoDaInboxFalsa lê o registro do dublê.
func estadoDaInboxFalsa(t *testing.T, f *inboxFalsa, messageID string) (string, string, int, bool) {
	t.Helper()

	registro, err := f.Get(context.Background(), ConsumerName, messageID)
	if err != nil {
		return "", "", 0, false
	}
	return registro.Status, registro.FailureCode, registro.Attempts, true
}

// Corpo ilegível: erro permanente, e NADA é registrado na inbox.
//
// Registrar uma mensagem que jamais será aplicada sujaria a tabela com lixo e
// faria a reentrega do mesmo lixo parecer "duplicata tratada".
func TestProcessarCorpoIlegivelNaoRegistraNaInbox(t *testing.T) {
	p, servico, inbox := processadorDeTeste(t)

	_, err := p.Processar(context.Background(), Entrada{IDDaEntrega: "msg-lixo", Corpo: []byte("{nao-e-json")})
	if err == nil {
		t.Fatal("corpo ilegível foi aceito")
	}
	if !errors.Is(err, ErrMensagemInvalida) {
		t.Errorf("causa = %v, quer ErrMensagemInvalida", err)
	}
	if len(inbox.registros) != 0 {
		t.Errorf("a inbox ganhou %d registro(s) de uma mensagem inválida", len(inbox.registros))
	}
	if servico.chamadas != 0 {
		t.Error("o caso de uso foi chamado para uma mensagem inválida")
	}
}

// --- desfechos e classificação -----------------------------------------------

// Rejeição de negócio: o desfecho é TERMINAL (a mensagem pode ser removida) e a
// inbox registra REJECTED com o código de falha.
func TestProcessarRejeicaoDeNegocioEhTerminal(t *testing.T) {
	p, servico, inbox := processadorDeTeste(t)
	servico.resultado = appwagering.Result{
		Status:      wagerdomain.StatusRejected,
		FailureCode: "INSUFFICIENT_FUNDS",
	}

	res, err := p.Processar(context.Background(), Entrada{IDDaEntrega: "msg-r", Corpo: []byte(corpoComMessageID("msg-r", "BET", "10000.00"))})
	if err != nil {
		t.Fatalf("rejeição devolveu erro: %v", err)
	}
	if !res.Processada {
		t.Error("rejeição terminal precisa permitir a remoção da mensagem")
	}
	if res.FailureCode != "INSUFFICIENT_FUNDS" {
		t.Errorf("failureCode = %q, quer INSUFFICIENT_FUNDS", res.FailureCode)
	}

	registro, _ := inbox.Get(context.Background(), ConsumerName, "msg-r")
	if registro.Status != "REJECTED" {
		t.Errorf("status na inbox = %q, quer REJECTED (distinto de PROCESSED para a auditoria)", registro.Status)
	}
	if registro.FailureCode != "INSUFFICIENT_FUNDS" {
		t.Errorf("failure_code = %q, quer INSUFFICIENT_FUNDS", registro.FailureCode)
	}
}

// Operação recusada por ENTRADA: nada foi gravado, e a mesma mensagem reenviada
// continuaria inválida — permanente.
func TestProcessarEntradaInvalidaEhPermanente(t *testing.T) {
	p, servico, _ := processadorDeTeste(t)
	servico.erro = domainErrorDeEntrada()

	_, err := p.Processar(context.Background(), Entrada{IDDaEntrega: "msg-e", Corpo: []byte(corpoComMessageID("msg-e", "BET", "25.00"))})
	if err == nil {
		t.Fatal("entrada inválida foi aceita")
	}
	if !errors.Is(err, ErrOperacaoInvalida) {
		t.Errorf("causa = %v, quer ErrOperacaoInvalida", err)
	}
}

// Falha de infraestrutura: TRANSITÓRIA — a mensagem volta para a fila.
func TestProcessarFalhaTransitoriaEhRetentavel(t *testing.T) {
	p, servico, _ := processadorDeTeste(t)
	servico.erro = ports.ErrTransient

	res, err := p.Processar(context.Background(), Entrada{IDDaEntrega: "msg-t", Corpo: []byte(corpoComMessageID("msg-t", "BET", "25.00"))})
	if err != nil {
		t.Fatalf("falha transitória devolveu erro de classificação: %v", err)
	}
	if !res.Transitoria {
		t.Errorf("resultado = %+v, quer Transitoria", res)
	}
	if res.Processada {
		t.Error("falha transitória não pode marcar a mensagem como processada (ela não foi removida da fila)")
	}
}

// Falha ao LER a inbox (banco fora) também é transitória.
func TestProcessarFalhaAoConsultarInboxEhTransitoria(t *testing.T) {
	p, servico, inbox := processadorDeTeste(t)
	inbox.erroDeLeitura = ports.ErrTransient

	res, err := p.Processar(context.Background(), Entrada{IDDaEntrega: "msg-b", Corpo: []byte(corpoComMessageID("msg-b", "BET", "25.00"))})
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !res.Transitoria {
		t.Errorf("resultado = %+v, quer Transitoria quando o banco não responde", res)
	}
	_ = servico
}

// --- auxiliares ---------------------------------------------------------------

func resultadoProcessado() appwagering.Result {
	return appwagering.Result{
		TransactionID: wagerdomain.TransactionID("0192f298-345e-7e38-af88-e43f851a819d"),
		Status:        wagerdomain.StatusProcessed,
	}
}

// domainErrorDeEntrada monta um erro de entrada como o caso de uso o produz.
func domainErrorDeEntrada() error {
	return domainerrors.New(appwagering.CodeInvalidRequest, "walletID não é um identificador válido")
}
