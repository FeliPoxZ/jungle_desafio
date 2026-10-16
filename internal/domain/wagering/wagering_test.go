package wagering

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ids"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/ledger"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
)

// ---------------------------------------------------------------------------
// Apoio
// ---------------------------------------------------------------------------

const (
	opProviderID = "provider-a"
	opExternalID = "ext-1"
	opKey        = "idem-key-1"
	opGameID     = "game-1"
	opRoundID    = "round-1"
)

var opNow = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// encodeCanonical é o encoder injetado: serializa sempre no mesmo formato, que
// é o que torna a comparação de hash confiável.
func encodeCanonical(v any) ([]byte, error) { return json.Marshal(v) }

type payloadTeste struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

func dinheiro(t *testing.T, amount, moeda string) money.Money {
	t.Helper()
	m, err := money.Parse(amount, moeda)
	if err != nil {
		t.Fatalf("Parse(%q, %q) falhou: %v", amount, moeda, err)
	}
	return m
}

// novaTransacao cria uma transação externa válida (BET) para os testes.
func novaTransacao(t *testing.T) *WagerTransaction {
	t.Helper()
	tx, err := New(
		"txn-1", "w-1", "p-1", KindBet, dinheiro(t, "25.00", "BRL"),
		opGameID, opRoundID, opKey, opProviderID, opExternalID, "",
		payloadTeste{Amount: "25.00", Currency: "BRL"}, encodeCanonical, opNow,
	)
	if err != nil {
		t.Fatalf("New falhou: %v", err)
	}
	return tx
}

// mesmoDinheiro compara dois valores da mesma moeda. Falha o teste em vez de
// devolver erro porque nos cenários de teste a moeda já é sabidamente igual.
func mesmoDinheiro(a, b money.Money) bool {
	eq, err := a.Equal(b)
	if err != nil {
		panic("mesmoDinheiro com moedas diferentes: " + err.Error())
	}
	return eq
}

// dinheiroPtr devolve ponteiro para o saldo resultante na reidratação: a
// coluna é anulável, então NULL (não calculado) e zero (carteira zerada) são
// casos diferentes.
func dinheiroPtr(t *testing.T, amount, moeda string) *money.Money {
	t.Helper()
	m := dinheiro(t, amount, moeda)
	return &m
}

// CodeOf devolve o Code de um erro de domínio do pacote.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "NAO_E_ERRO_DE_DOMINIO"
}

// ---------------------------------------------------------------------------
// Kind
// ---------------------------------------------------------------------------

func TestParseKind(t *testing.T) {
	validos := []Kind{KindOpening, KindBet, KindWin, KindLoss, KindRefund, KindRollback}
	for _, k := range validos {
		got, err := ParseKind(string(k))
		if err != nil {
			t.Errorf("ParseKind(%q) falhou: %v", k, err)
		}
		if got != k {
			t.Errorf("ParseKind(%q) = %q", k, got)
		}
		if got.String() != string(k) {
			t.Errorf("String() = %q", got.String())
		}
	}

	invalidos := []string{"", "bet", "Bet", "OPENING ", " OPENING", "DEPOSIT", "OPENIN", "OPENINGG", "123", "BET\n"}
	for _, s := range invalidos {
		if _, err := ParseKind(s); err == nil {
			t.Errorf("ParseKind(%q) deveria falhar", s)
		} else if !errors.Is(err, ErrInvalidTransaction) {
			t.Errorf("ParseKind(%q): err = %v, esperado ErrInvalidTransaction", s, err)
		}
	}
}

func TestKindPropriedades(t *testing.T) {
	casos := []struct {
		kind       Kind
		externo    bool
		referencia bool
		zero       bool
		movimenta  bool
	}{
		{KindOpening, false, false, false, true},
		{KindBet, true, false, false, true},
		{KindWin, true, false, false, true},
		{KindLoss, true, false, true, false},
		{KindRefund, true, true, false, true},
		{KindRollback, true, true, false, true},
	}

	for _, c := range casos {
		t.Run(string(c.kind), func(t *testing.T) {
			if got := c.kind.IsExternal(); got != c.externo {
				t.Errorf("IsExternal() = %v, esperado %v", got, c.externo)
			}
			if got := c.kind.IsInternal(); got != !c.externo {
				t.Errorf("IsInternal() = %v, esperado %v", got, !c.externo)
			}
			if got := c.kind.RequiresReference(); got != c.referencia {
				t.Errorf("RequiresReference() = %v, esperado %v", got, c.referencia)
			}
			if got := c.kind.AllowsZeroAmount(); got != c.zero {
				t.Errorf("AllowsZeroAmount() = %v, esperado %v", got, c.zero)
			}
			if got := c.kind.MovesMoney(); got != c.movimenta {
				t.Errorf("MovesMoney() = %v, esperado %v", got, c.movimenta)
			}
			if err := c.kind.Validate(); err != nil {
				t.Errorf("Validate() = %v", err)
			}
		})
	}
}

func TestKindValidateAmount(t *testing.T) {
	casos := []struct {
		nome    string
		kind    Kind
		minor   int64
		queroEr bool
	}{
		{"BET com valor positivo", KindBet, 2500, false},
		{"BET com um centavo", KindBet, 1, false},
		{"BET com zero", KindBet, 0, true},
		{"BET negativo", KindBet, -2500, true},
		{"WIN com valor positivo", KindWin, 2500, false},
		{"WIN com zero", KindWin, 0, true},
		{"WIN negativo", KindWin, -1, true},
		{"REFUND com valor positivo", KindRefund, 2500, false},
		{"REFUND com zero", KindRefund, 0, true},
		{"ROLLBACK com valor positivo", KindRollback, 2500, false},
		{"ROLLBACK com zero", KindRollback, 0, true},
		{"LOSS com zero exato", KindLoss, 0, false},
		{"LOSS com um centavo", KindLoss, 1, true},
		{"LOSS com valor alto", KindLoss, 2500, true},
		{"LOSS negativo", KindLoss, -1, true},
		{"OPENING com valor positivo", KindOpening, 100000, false},
		{"OPENING com zero", KindOpening, 0, true},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			err := c.kind.ValidateAmount(c.minor)
			if c.queroEr && err == nil {
				t.Fatalf("%s com %d deveria falhar", c.kind, c.minor)
			}
			if !c.queroEr && err != nil {
				t.Fatalf("%s com %d falhou: %v", c.kind, c.minor, err)
			}
			if err != nil && CodeOf(err) != FailureCodeZeroAmount {
				t.Errorf("Code = %q, esperado %q", CodeOf(err), FailureCodeZeroAmount)
			}
		})
	}
}

func TestKindValidateAmountComTipoInvalido(t *testing.T) {
	if err := Kind("DEPOSIT").ValidateAmount(100); err == nil {
		t.Error("tipo inválido deveria falhar")
	} else if !errors.Is(err, ErrInvalidTransaction) {
		t.Errorf("err = %v, esperado ErrInvalidTransaction", err)
	}
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

func TestParseStatus(t *testing.T) {
	validos := []Status{StatusPending, StatusPendingReference, StatusProcessed, StatusRejected, StatusFailed}
	for _, s := range validos {
		got, err := ParseStatus(string(s))
		if err != nil {
			t.Errorf("ParseStatus(%q) falhou: %v", s, err)
		}
		if got != s || got.String() != string(s) {
			t.Errorf("ParseStatus(%q) = %q", s, got)
		}
	}

	for _, s := range []string{"", "pending", "PENDING ", "DONE", "PROCESSING", "PENDING_REFERENCE ", "UNKNOWN"} {
		if _, err := ParseStatus(s); err == nil {
			t.Errorf("ParseStatus(%q) deveria falhar", s)
		} else if !errors.Is(err, ErrInvalidTransaction) {
			t.Errorf("ParseStatus(%q): err = %v", s, err)
		}
	}
}

func TestStatusIsTerminal(t *testing.T) {
	casos := map[Status]bool{
		StatusPending:          false,
		StatusPendingReference: false,
		StatusProcessed:        true,
		StatusRejected:         true,
		StatusFailed:           true,
	}
	for s, esperado := range casos {
		if got := s.IsTerminal(); got != esperado {
			t.Errorf("%s.IsTerminal() = %v, esperado %v", s, got, esperado)
		}
	}
}

func TestMaquinaDeEstados(t *testing.T) {
	todos := []Status{StatusPending, StatusPendingReference, StatusProcessed, StatusRejected, StatusFailed}

	// Matriz explícita: de -> para -> permitido.
	permitido := map[Status]map[Status]bool{
		StatusPending: {
			StatusPendingReference: true,
			StatusProcessed:        true,
			StatusRejected:         true,
			StatusFailed:           true,
		},
		StatusPendingReference: {
			StatusProcessed: true,
			StatusRejected:  true,
			StatusFailed:    true,
		},
		StatusProcessed: {},
		StatusRejected:  {},
		StatusFailed:    {},
	}

	for _, de := range todos {
		for _, para := range todos {
			t.Run(string(de)+"_para_"+string(para), func(t *testing.T) {
				esperado := permitido[de][para]
				if got := de.CanTransition(para); got != esperado {
					t.Errorf("CanTransition(%s -> %s) = %v, esperado %v", de, para, got, esperado)
				}

				err := de.ValidateTransition(para)
				if esperado {
					if err != nil {
						t.Errorf("ValidateTransition(%s -> %s) falhou: %v", de, para, err)
					}
					return
				}
				if err == nil {
					t.Fatalf("ValidateTransition(%s -> %s) deveria falhar", de, para)
				}
				// Estado terminal tem erro próprio: a causa é diferente de
				// uma transição apenas fora de ordem.
				if de.IsTerminal() {
					if !errors.Is(err, ErrTerminalState) {
						t.Errorf("%s é terminal: err = %v, esperado ErrTerminalState", de, err)
					}
				} else if !errors.Is(err, ErrInvalidTransition) {
					t.Errorf("err = %v, esperado ErrInvalidTransition", err)
				}
			})
		}
	}
}

func TestAllowedTransitionsNaoVazaInterno(t *testing.T) {
	// A fatia devolvida é cópia: mutar o retorno não pode alterar a máquina.
	tx := novaTransacao(t)
	antes := StatusPending.AllowedTransitions()

	copia := StatusPending.AllowedTransitions()
	copia[0] = StatusProcessed

	depois := StatusPending.AllowedTransitions()
	if len(antes) != len(depois) {
		t.Fatalf("tamanho mudou: %d -> %d", len(antes), len(depois))
	}
	for i := range antes {
		if antes[i] != depois[i] {
			t.Errorf("a máquina de estados foi alterada pela cópia devolvida")
		}
	}
	if tx.Status() != StatusPending {
		t.Errorf("status = %q", tx.Status())
	}
}

// ---------------------------------------------------------------------------
// Testes de criação: origem
// ---------------------------------------------------------------------------

func TestNewRegrasDeOrigem(t *testing.T) {
	casos := []struct {
		nome      string
		kind      Kind
		provider  string
		external  string
		key       string
		reference string
		payload   any
		encode    PayloadEncoder
		amount    string
		queroEr   bool
		code      string
	}{
		{"BET valida", KindBet, opProviderID, opExternalID, opKey, "", payloadTeste{"25.00", "BRL"}, encodeCanonical, "25.00", false, ""},
		{"WIN valida", KindWin, opProviderID, opExternalID, opKey, "", payloadTeste{"25.00", "BRL"}, encodeCanonical, "25.00", false, ""},
		{"LOSS com zero", KindLoss, opProviderID, opExternalID, opKey, "", payloadTeste{"0.00", "BRL"}, encodeCanonical, "0.00", false, ""},
		{"REFUND com referencia", KindRefund, opProviderID, opExternalID, opKey, "ext-bet", payloadTeste{"25.00", "BRL"}, encodeCanonical, "25.00", false, ""},
		{"ROLLBACK com referencia", KindRollback, opProviderID, opExternalID, opKey, "ext-bet", payloadTeste{"25.00", "BRL"}, encodeCanonical, "25.00", false, ""},
		{"OPENING interna valida", KindOpening, "", "", opKey, "", nil, nil, "1000.00", false, ""},

		{"BET sem providerID", KindBet, "", opExternalID, opKey, "", payloadTeste{}, encodeCanonical, "25.00", true, CodeInvalidTransaction},
		{"BET sem externalID", KindBet, opProviderID, "", opKey, "", payloadTeste{}, encodeCanonical, "25.00", true, CodeInvalidTransaction},
		{"BET sem idempotencyKey", KindBet, opProviderID, opExternalID, "", "", payloadTeste{}, encodeCanonical, "25.00", true, CodeInvalidTransaction},
		{"BET sem payload", KindBet, opProviderID, opExternalID, opKey, "", nil, encodeCanonical, "25.00", true, CodeInvalidTransaction},
		{"BET sem encoder", KindBet, opProviderID, opExternalID, opKey, "", payloadTeste{}, nil, "25.00", true, CodeInvalidTransaction},
		{"REFUND sem referencia", KindRefund, opProviderID, opExternalID, opKey, "", payloadTeste{}, encodeCanonical, "25.00", true, CodeMissingReference},
		{"ROLLBACK sem referencia", KindRollback, opProviderID, opExternalID, opKey, "", payloadTeste{}, encodeCanonical, "25.00", true, CodeMissingReference},
		{"OPENING com providerID", KindOpening, opProviderID, "", opKey, "", nil, nil, "1000.00", true, CodeInternalKind},
		{"OPENING com externalID", KindOpening, "", opExternalID, opKey, "", nil, nil, "1000.00", true, CodeInternalKind},
		{"OPENING sem chave", KindOpening, "", "", "", "", nil, nil, "1000.00", true, CodeInvalidTransaction},
		{"tipo desconhecido", Kind("DEPOSIT"), opProviderID, opExternalID, opKey, "", payloadTeste{}, encodeCanonical, "25.00", true, CodeInvalidTransaction},
		{"BET com zero", KindBet, opProviderID, opExternalID, opKey, "", payloadTeste{}, encodeCanonical, "0.00", true, FailureCodeZeroAmount},
		{"LOSS com valor", KindLoss, opProviderID, opExternalID, opKey, "", payloadTeste{}, encodeCanonical, "0.01", true, FailureCodeZeroAmount},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			tx, err := New(
				"txn-1", "w-1", "p-1", c.kind, dinheiro(t, c.amount, "BRL"),
				opGameID, opRoundID, c.key, c.provider, c.external, c.reference,
				c.payload, c.encode, opNow,
			)
			if c.queroEr {
				if err == nil {
					t.Fatalf("New deveria falhar, devolveu %+v", tx)
				}
				if CodeOf(err) != c.code {
					t.Errorf("Code = %q, esperado %q", CodeOf(err), c.code)
				}
				if tx != nil {
					t.Error("transação deveria ser nil em caso de erro")
				}
				return
			}

			if err != nil {
				t.Fatalf("New falhou: %v", err)
			}
			if tx.Status() != StatusPending {
				t.Errorf("status inicial = %q, esperado PENDING", tx.Status())
			}
			if !tx.CreatedAt().Equal(opNow) || !tx.UpdatedAt().Equal(opNow) {
				t.Error("createdAt/updatedAt deveriam ser iguais na criação")
			}
			if tx.PayloadHash() == "" {
				t.Error("payloadHash não pode ser vazio")
			}
			if tx.ID() != "txn-1" || tx.WalletID() != "w-1" || tx.PlayerID() != "p-1" {
				t.Errorf("identidade: %q/%q/%q", tx.ID(), tx.WalletID(), tx.PlayerID())
			}
			if tx.Kind() != c.kind {
				t.Errorf("kind = %q", tx.Kind())
			}
		})
	}
}

func TestNewCamposObrigatorios(t *testing.T) {
	base := payloadTeste{Amount: "25.00", Currency: "BRL"}

	casos := []struct {
		nome    string
		id      TransactionID
		wallet  ids.WalletID
		player  ids.PlayerID
		game    string
		round   string
		now     time.Time
		moeda   string
		amount  string
		queroEr bool
	}{
		{"id vazio", "", "w-1", "p-1", opGameID, opRoundID, opNow, "BRL", "25.00", true},
		{"id so espacos", "  ", "w-1", "p-1", opGameID, opRoundID, opNow, "BRL", "25.00", true},
		{"walletID vazio", "txn-1", "", "p-1", opGameID, opRoundID, opNow, "BRL", "25.00", true},
		{"playerID vazio", "txn-1", "w-1", "", opGameID, opRoundID, opNow, "BRL", "25.00", true},
		{"gameID vazio", "txn-1", "w-1", "p-1", "", opRoundID, opNow, "BRL", "25.00", true},
		{"roundID vazio", "txn-1", "w-1", "p-1", opGameID, "", opNow, "BRL", "25.00", true},
		{"instante zero", "txn-1", "w-1", "p-1", opGameID, opRoundID, time.Time{}, "BRL", "25.00", true},
		{"moeda invalida", "txn-1", "w-1", "p-1", opGameID, opRoundID, opNow, "xx", "25.00", true},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			var amount money.Money
			if c.moeda == "xx" {
				amount = money.FromMinor(2500, money.Currency("xx"))
			} else {
				amount = dinheiro(t, c.amount, c.moeda)
			}

			tx, err := New(c.id, c.wallet, c.player, KindBet, amount, c.game, c.round,
				opKey, opProviderID, opExternalID, "", base, encodeCanonical, c.now)
			if c.queroEr && err == nil {
				t.Fatalf("New deveria falhar, devolveu %+v", tx)
			}
			if !c.queroEr && err != nil {
				t.Fatalf("New falhou: %v", err)
			}
		})
	}
}

func TestNewPayloadHashDeterministico(t *testing.T) {
	// O hash precisa ser estável para o mesmo conteúdo, porque é ele que
	// distingue replay idempotente de conflito de payload.
	novo := func(p payloadTeste) *WagerTransaction {
		tx, err := New("txn-1", "w-1", "p-1", KindBet, dinheiro(t, "25.00", "BRL"),
			opGameID, opRoundID, opKey, opProviderID, opExternalID, "", p, encodeCanonical, opNow)
		if err != nil {
			t.Fatalf("New falhou: %v", err)
		}
		return tx
	}

	a := novo(payloadTeste{Amount: "25.00", Currency: "BRL"})
	b := novo(payloadTeste{Amount: "25.00", Currency: "BRL"})
	c := novo(payloadTeste{Amount: "26.00", Currency: "BRL"})

	if a.PayloadHash() != b.PayloadHash() {
		t.Errorf("mesmo payload gerou hashes diferentes: %s vs %s", a.PayloadHash(), b.PayloadHash())
	}
	if a.PayloadHash() == c.PayloadHash() {
		t.Error("payloads diferentes geraram o mesmo hash")
	}
	if !a.MatchesPayload(b.PayloadHash()) {
		t.Error("MatchesPayload deveria ser verdadeiro para o mesmo hash")
	}
	if a.MatchesPayload(c.PayloadHash()) {
		t.Error("MatchesPayload deveria ser falso para hash diferente")
	}
}

func TestNewComPayloadQueNaoSerializa(t *testing.T) {
	// Canal não serializa em JSON: o erro tem que nascer na criação, não no
	// momento de publicar o evento.
	_, err := New("txn-1", "w-1", "p-1", KindBet, dinheiro(t, "25.00", "BRL"),
		opGameID, opRoundID, opKey, opProviderID, opExternalID, "",
		make(chan int), encodeCanonical, opNow)
	if err == nil {
		t.Fatal("payload que não serializa deveria falhar")
	}
}

// ---------------------------------------------------------------------------
// Transições
// ---------------------------------------------------------------------------

func TestMarkProcessed(t *testing.T) {
	tx := novaTransacao(t)
	depois := opNow.Add(time.Minute)

	if err := tx.MarkProcessed(dinheiro(t, "975.00", "BRL"), depois); err != nil {
		t.Fatalf("MarkProcessed falhou: %v", err)
	}
	if tx.Status() != StatusProcessed {
		t.Errorf("status = %q", tx.Status())
	}
	if !mesmoDinheiro(tx.ResultBalance(), dinheiro(t, "975.00", "BRL")) {
		t.Errorf("resultBalance = %s", tx.ResultBalance())
	}
	if !tx.UpdatedAt().Equal(depois) {
		t.Errorf("updatedAt = %v", tx.UpdatedAt())
	}
	if !tx.CreatedAt().Equal(opNow) {
		t.Errorf("createdAt não pode mudar: %v", tx.CreatedAt())
	}
	if !tx.IsTerminal() {
		t.Error("PROCESSED deveria ser terminal")
	}
}

func TestMarkProcessedComMoedaInvalida(t *testing.T) {
	tx := novaTransacao(t)
	invalido := money.FromMinor(100, money.Currency("xx"))

	if err := tx.MarkProcessed(invalido, opNow.Add(time.Minute)); err == nil {
		t.Fatal("moeda inválida deveria falhar")
	}
	// A validação roda ANTES da transição: erro não pode deixar a transação em
	// estado terminal sem o saldo que a justifica.
	if tx.Status() != StatusPending {
		t.Errorf("status = %q, esperado PENDING (validação antes da transição)", tx.Status())
	}
	if !tx.UpdatedAt().Equal(opNow) {
		t.Errorf("updatedAt mudou apesar do erro: %v", tx.UpdatedAt())
	}
	if !tx.ResultBalance().IsZero() {
		t.Errorf("resultBalance preenchido apesar do erro: %s", tx.ResultBalance())
	}
}

func TestMarkProcessedComMoedaDiferente(t *testing.T) {
	// O saldo devolvido ao provedor é o da carteira da operação. Resultado em
	// outra moeda é resposta incoerente, mesmo sendo uma moeda válida.
	tx := novaTransacao(t)

	err := tx.MarkProcessed(dinheiro(t, "975.00", "USD"), opNow.Add(time.Minute))
	if err == nil {
		t.Fatal("saldo resultante em outra moeda deveria falhar")
	}
	// Reusa o sentinela do dinheiro: uma moeda incompatível é sempre a mesma
	// falha, e o mapeamento da Fase 7 traduz um sentinela para um failureCode.
	if !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Errorf("err = %v, esperado money.ErrCurrencyMismatch", err)
	}
	if tx.Status() != StatusPending {
		t.Errorf("status = %q, esperado PENDING", tx.Status())
	}
	if !tx.ResultBalance().IsZero() {
		t.Errorf("resultBalance preenchido: %s", tx.ResultBalance())
	}
}

func TestMarkProcessedComSaldoGrande(t *testing.T) {
	tx := novaTransacao(t)

	// O teto do domínio é aceito: a validação aqui é de moeda, não de faixa.
	if err := tx.MarkProcessed(dinheiro(t, "90000000000000.00", "BRL"), opNow.Add(time.Minute)); err != nil {
		t.Fatalf("saldo no teto deveria ser aceito: %v", err)
	}
	if tx.Status() != StatusProcessed {
		t.Errorf("status = %q", tx.Status())
	}
}

func TestMarkRejected(t *testing.T) {
	tx := novaTransacao(t)
	depois := opNow.Add(time.Minute)

	if err := tx.MarkRejected("INSUFFICIENT_FUNDS", depois); err != nil {
		t.Fatalf("MarkRejected falhou: %v", err)
	}
	if tx.Status() != StatusRejected {
		t.Errorf("status = %q", tx.Status())
	}
	if tx.FailureCode() != "INSUFFICIENT_FUNDS" {
		t.Errorf("failureCode = %q", tx.FailureCode())
	}
	if !tx.UpdatedAt().Equal(depois) {
		t.Errorf("updatedAt = %v", tx.UpdatedAt())
	}
}

func TestMarkRejectedSemFailureCode(t *testing.T) {
	tx := novaTransacao(t)

	if err := tx.MarkRejected("", opNow.Add(time.Minute)); err == nil {
		t.Fatal("failureCode vazio deveria falhar")
	}
	if tx.Status() != StatusPending {
		t.Errorf("estado mudou apesar do erro: %q", tx.Status())
	}
	if tx.FailureCode() != "" {
		t.Errorf("failureCode = %q", tx.FailureCode())
	}
}

func TestMarkFailed(t *testing.T) {
	tx := novaTransacao(t)

	if err := tx.MarkFailed("", opNow.Add(time.Minute)); err != nil {
		t.Fatalf("MarkFailed falhou: %v", err)
	}
	if tx.Status() != StatusFailed {
		t.Errorf("status = %q", tx.Status())
	}
	// Sem código informado, o padrão é o código de falha de processamento.
	if tx.FailureCode() != CodeProcessingFailed {
		t.Errorf("failureCode = %q, esperado %q", tx.FailureCode(), CodeProcessingFailed)
	}
}

func TestMarkPendingReference(t *testing.T) {
	tx := novaTransacao(t)
	depois := opNow.Add(time.Minute)
	proxima := depois.Add(5 * time.Second)

	if err := tx.MarkPendingReference(1, proxima, depois); err != nil {
		t.Fatalf("MarkPendingReference falhou: %v", err)
	}
	if tx.Status() != StatusPendingReference {
		t.Errorf("status = %q", tx.Status())
	}
	if tx.ReferenceResolutionAttempt() != 1 {
		t.Errorf("tentativa = %d", tx.ReferenceResolutionAttempt())
	}
	if !tx.NextResolutionAttemptAt().Equal(proxima) {
		t.Errorf("nextAttemptAt = %v", tx.NextResolutionAttemptAt())
	}
	if tx.IsTerminal() {
		t.Error("PENDING_REFERENCE não é terminal")
	}
}

func TestMarkPendingReferenceValidacoes(t *testing.T) {
	casos := []struct {
		nome    string
		attempt int
		next    time.Time
	}{
		{"tentativa zero", 0, opNow.Add(time.Second)},
		{"tentativa negativa", -1, opNow.Add(time.Second)},
		{"proximo instante zero", 1, time.Time{}},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			tx := novaTransacao(t)
			if err := tx.MarkPendingReference(c.attempt, c.next, opNow.Add(time.Minute)); err == nil {
				t.Fatal("deveria falhar")
			}
			if tx.Status() != StatusPending {
				t.Errorf("estado mudou: %q", tx.Status())
			}
		})
	}
}

func TestResolveReference(t *testing.T) {
	tx := novaTransacao(t)
	depois := opNow.Add(time.Minute)

	if err := tx.MarkPendingReference(1, depois.Add(time.Second), depois); err != nil {
		t.Fatal(err)
	}
	if err := tx.ResolveReference("txn-original", 2); err != nil {
		t.Fatalf("ResolveReference falhou: %v", err)
	}
	if tx.ReferenceTransactionID() != "txn-original" {
		t.Errorf("referenceTransactionID = %q", tx.ReferenceTransactionID())
	}
	if tx.ReferenceResolutionAttempt() != 2 {
		t.Errorf("tentativa = %d", tx.ReferenceResolutionAttempt())
	}
	// Resolver não muda de estado: quem decide o estado é o processamento.
	if tx.Status() != StatusPendingReference {
		t.Errorf("status = %q, esperado PENDING_REFERENCE", tx.Status())
	}
}

func TestResolveReferenceSomenteEmPendingReference(t *testing.T) {
	tx := novaTransacao(t)

	if err := tx.ResolveReference("txn-original", 1); err == nil {
		t.Fatal("resolver em PENDING deveria falhar")
	} else if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("err = %v, esperado ErrInvalidTransition", err)
	}

	if err := tx.ResolveReference("", 1); err == nil {
		t.Error("referência vazia deveria falhar")
	}
	if err := tx.ResolveReference("txn", 0); err == nil {
		t.Error("tentativa zero deveria falhar")
	}
}

// ---------------------------------------------------------------------------
// Estado terminal
// ---------------------------------------------------------------------------

func TestEstadoTerminalRecusaQualquerTransicao(t *testing.T) {
	terminais := map[string]func(*WagerTransaction, time.Time) error{
		"PROCESSED": func(tx *WagerTransaction, now time.Time) error {
			return tx.MarkProcessed(dinheiro(t, "1.00", "BRL"), now)
		},
		"REJECTED": func(tx *WagerTransaction, now time.Time) error {
			return tx.MarkRejected("INSUFFICIENT_FUNDS", now)
		},
		"FAILED": func(tx *WagerTransaction, now time.Time) error {
			return tx.MarkFailed(CodeProcessingFailed, now)
		},
	}

	for nome, chegarEmTerminal := range terminais {
		t.Run(nome, func(t *testing.T) {
			tx := novaTransacao(t)
			if err := chegarEmTerminal(tx, opNow.Add(time.Minute)); err != nil {
				t.Fatalf("não consegui chegar em %s: %v", nome, err)
			}

			estadoFinal := tx.Status()
			atualizadoEm := tx.UpdatedAt()
			depois := opNow.Add(time.Hour)

			tentativas := map[string]func() error{
				"MarkProcessed":        func() error { return tx.MarkProcessed(dinheiro(t, "1.00", "BRL"), depois) },
				"MarkRejected":         func() error { return tx.MarkRejected("X", depois) },
				"MarkFailed":           func() error { return tx.MarkFailed("X", depois) },
				"MarkPendingReference": func() error { return tx.MarkPendingReference(1, depois, depois) },
			}

			for acao, fn := range tentativas {
				err := fn()
				if err == nil {
					t.Errorf("%s a partir de %s deveria falhar", acao, estadoFinal)
					continue
				}
				if !errors.Is(err, ErrTerminalState) {
					t.Errorf("%s: err = %v, esperado ErrTerminalState", acao, err)
				}
			}

			if tx.Status() != estadoFinal {
				t.Errorf("estado mudou: %q -> %q", estadoFinal, tx.Status())
			}
			if !tx.UpdatedAt().Equal(atualizadoEm) {
				t.Errorf("updatedAt mudou: %v -> %v", atualizadoEm, tx.UpdatedAt())
			}
		})
	}
}

func TestTransicaoExigeInstante(t *testing.T) {
	tx := novaTransacao(t)

	if err := tx.MarkProcessed(dinheiro(t, "1.00", "BRL"), time.Time{}); err == nil {
		t.Fatal("instante zero deveria falhar")
	}
	if tx.Status() != StatusPending {
		t.Errorf("estado mudou: %q", tx.Status())
	}
}

// ---------------------------------------------------------------------------
// Fluxo completo
// ---------------------------------------------------------------------------

func TestFluxoCompletoBATEProcessed(t *testing.T) {
	tx := novaTransacao(t)

	if err := tx.MarkProcessed(dinheiro(t, "975.00", "BRL"), opNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	ev, err := tx.ProcessedEvent("evt-1", "corr-1", "", &[]money.Money{dinheiro(t, "975.00", "BRL")}[0], opNow.Add(time.Second))
	if err != nil {
		t.Fatalf("ProcessedEvent falhou: %v", err)
	}

	if ev.EventType != EventTypeProcessed {
		t.Errorf("eventType = %q", ev.EventType)
	}
	if ev.Version != EventVersion {
		t.Errorf("version = %d", ev.Version)
	}
	if ev.AggregateID != "w-1" {
		t.Errorf("aggregateId = %q (deveria ser o walletId, que é o MessageGroupId)", ev.AggregateID)
	}

	data, ok := ev.Data.(ProcessedData)
	if !ok {
		t.Fatalf("data tem tipo %T", ev.Data)
	}
	if data.Status != StatusProcessed || data.BalanceAfter == nil {
		t.Errorf("data = %+v", data)
	}
}

func TestFluxoPendenciaESucesso(t *testing.T) {
	tx := novaTransacao(t)
	depois := opNow.Add(time.Minute)

	if err := tx.MarkPendingReference(1, depois.Add(5*time.Second), depois); err != nil {
		t.Fatal(err)
	}
	if err := tx.ResolveReference("txn-original", 2); err != nil {
		t.Fatal(err)
	}
	if err := tx.MarkProcessed(dinheiro(t, "975.00", "BRL"), depois.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}

	if tx.Status() != StatusProcessed {
		t.Errorf("status = %q", tx.Status())
	}
	if tx.ReferenceResolutionAttempt() != 2 {
		t.Errorf("tentativa final = %d", tx.ReferenceResolutionAttempt())
	}
}

func TestLOSSProduzEventoProcessedSemSaldo(t *testing.T) {
	tx, err := New("txn-1", "w-1", "p-1", KindLoss, dinheiro(t, "0.00", "BRL"),
		opGameID, opRoundID, opKey, opProviderID, opExternalID, "",
		payloadTeste{Amount: "0.00", Currency: "BRL"}, encodeCanonical, opNow)
	if err != nil {
		t.Fatalf("New falhou: %v", err)
	}
	if err := tx.MarkProcessed(dinheiro(t, "975.00", "BRL"), opNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	// LOSS também produz Processed, mas sem saldo resultante: não houve
	// movimentação, então não há o que reportar.
	ev, err := tx.ProcessedEvent("evt-1", "corr-1", "", nil, opNow.Add(time.Second))
	if err != nil {
		t.Fatalf("ProcessedEvent falhou: %v", err)
	}
	data := ev.Data.(ProcessedData)
	if data.BalanceAfter != nil {
		t.Errorf("LOSS não deveria ter balanceAfter: %+v", data.BalanceAfter)
	}
	if tx.Kind().MovesMoney() {
		t.Error("LOSS não movimenta dinheiro")
	}
}

// ---------------------------------------------------------------------------
// Eventos
// ---------------------------------------------------------------------------

func TestNewEventValidacoes(t *testing.T) {
	base := RejectedData{TransactionID: "txn-1"}

	casos := []struct {
		nome    string
		id      string
		tipo    string
		agreg   string
		quando  time.Time
		data    any
		queroEr bool
	}{
		{"valido", "evt-1", EventTypeRejected, "w-1", opNow, base, false},
		{"sem eventID", "", EventTypeRejected, "w-1", opNow, base, true},
		{"sem eventType", "evt-1", "", "w-1", opNow, base, true},
		{"sem aggregateID", "evt-1", EventTypeRejected, "", opNow, base, true},
		{"sem occurredAt", "evt-1", EventTypeRejected, "w-1", time.Time{}, base, true},
		{"sem data", "evt-1", EventTypeRejected, "w-1", opNow, nil, true},
		{"data que nao serializa", "evt-1", EventTypeRejected, "w-1", opNow, make(chan int), true},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			ev, err := NewEvent(c.id, c.tipo, c.agreg, "corr", "", c.quando, c.data)
			if c.queroEr {
				if err == nil {
					t.Fatalf("NewEvent deveria falhar, devolveu %+v", ev)
				}
				if ev != nil {
					t.Error("evento deveria ser nil em caso de erro")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewEvent falhou: %v", err)
			}
			if ev.Version != EventVersion {
				t.Errorf("version = %d, esperado %d", ev.Version, EventVersion)
			}
			if ev.EventType != c.tipo {
				t.Errorf("eventType = %q", ev.EventType)
			}
		})
	}
}

func TestRejectedEvent(t *testing.T) {
	tx := novaTransacao(t)
	depois := opNow.Add(time.Minute)
	if err := tx.MarkRejected("INSUFFICIENT_FUNDS", depois); err != nil {
		t.Fatal(err)
	}

	ev, err := tx.RejectedEvent("evt-1", "corr-1", "causa-1", depois)
	if err != nil {
		t.Fatalf("RejectedEvent falhou: %v", err)
	}
	if ev.EventType != EventTypeRejected {
		t.Errorf("eventType = %q", ev.EventType)
	}
	if ev.CausationID != "causa-1" {
		t.Errorf("causationId = %q", ev.CausationID)
	}

	data := ev.Data.(RejectedData)
	if data.FailureCode != "INSUFFICIENT_FUNDS" {
		t.Errorf("failureCode = %q", data.FailureCode)
	}
	if !mesmoDinheiro(data.Money, dinheiro(t, "25.00", "BRL")) {
		t.Errorf("money = %s", data.Money)
	}
}

func TestPendingReferenceEvent(t *testing.T) {
	tx := novaTransacao(t)
	depois := opNow.Add(time.Minute)
	proxima := depois.Add(5 * time.Second)

	if err := tx.MarkPendingReference(3, proxima, depois); err != nil {
		t.Fatal(err)
	}

	ev, err := tx.PendingReferenceEvent("evt-1", "corr-1", "", depois)
	if err != nil {
		t.Fatalf("PendingReferenceEvent falhou: %v", err)
	}
	if ev.EventType != EventTypePendingReference {
		t.Errorf("eventType = %q", ev.EventType)
	}

	data := ev.Data.(PendingReferenceData)
	if data.Attempt != 3 {
		t.Errorf("attempt = %d", data.Attempt)
	}
	if !data.NextAttemptAt.Equal(proxima) {
		t.Errorf("nextAttemptAt = %v", data.NextAttemptAt)
	}
	if data.ReferenceExternalTxnID != "" {
		t.Errorf("referência externa = %q", data.ReferenceExternalTxnID)
	}
}

func TestNewBalanceChangedEvent(t *testing.T) {
	entry, err := ledger.NewEntry("w-1", "txn-1", ledger.DirectionDebit,
		dinheiro(t, "25.00", "BRL"), dinheiro(t, "1000.00", "BRL"), opNow)
	if err != nil {
		t.Fatal(err)
	}

	ev, err := NewBalanceChangedEvent("evt-1", "corr-1", "txn-1", "txn-1", entry, 2, opNow)
	if err != nil {
		t.Fatalf("NewBalanceChangedEvent falhou: %v", err)
	}
	if ev.EventType != EventTypeWalletBalanceChanged {
		t.Errorf("eventType = %q", ev.EventType)
	}

	data := ev.Data.(BalanceChangedData)
	if data.WalletVersion != 2 {
		t.Errorf("walletVersion = %d", data.WalletVersion)
	}
	if data.Direction != ledger.DirectionDebit {
		t.Errorf("direction = %q", data.Direction)
	}
	if data.BalanceBefore.String() != "1000.00" || data.BalanceAfter.String() != "975.00" {
		t.Errorf("saldos = %s -> %s", data.BalanceBefore, data.BalanceAfter)
	}
}

func TestNewBalanceChangedEventValidacoes(t *testing.T) {
	entry, err := ledger.NewEntry("w-1", "txn-1", ledger.DirectionDebit,
		dinheiro(t, "25.00", "BRL"), dinheiro(t, "1000.00", "BRL"), opNow)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := NewBalanceChangedEvent("evt-1", "c", "", "txn-1", ledger.Entry{}, 2, opNow); err == nil {
		t.Error("entry vazio deveria falhar (LOSS e rejeição não geram este evento)")
	}
	if _, err := NewBalanceChangedEvent("evt-1", "c", "", "txn-1", entry, 0, opNow); err == nil {
		t.Error("versão 0 deveria falhar")
	}
	if _, err := NewBalanceChangedEvent("evt-1", "c", "", "txn-1", entry, -1, opNow); err == nil {
		t.Error("versão negativa deveria falhar")
	}
}

func TestEventoSerializaNoFormatoDoContrato(t *testing.T) {
	tx := novaTransacao(t)
	depois := opNow.Add(time.Minute)
	if err := tx.MarkRejected("INSUFFICIENT_FUNDS", depois); err != nil {
		t.Fatal(err)
	}
	ev, err := tx.RejectedEvent("evt-1", "corr-1", "", depois)
	if err != nil {
		t.Fatal(err)
	}

	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal falhou: %v", err)
	}

	var volta map[string]any
	if err := json.Unmarshal(b, &volta); err != nil {
		t.Fatalf("Unmarshal falhou: %v", err)
	}
	for _, campo := range []string{"eventId", "eventType", "aggregateId", "correlationId", "occurredAt", "version", "data"} {
		if _, ok := volta[campo]; !ok {
			t.Errorf("envelope sem o campo %q: %s", campo, b)
		}
	}
	if _, ok := volta["causationId"]; ok {
		t.Errorf("causationId vazio não deveria aparecer (omitempty): %s", b)
	}

	data := volta["data"].(map[string]any)
	if data["failureCode"] != "INSUFFICIENT_FUNDS" {
		t.Errorf("data.failureCode = %v", data["failureCode"])
	}
	if _, ok := data["money"].(map[string]any); !ok {
		t.Errorf("money deveria ser objeto com amount e currency: %v", data["money"])
	}
}

// ---------------------------------------------------------------------------
// Rehydrate
// ---------------------------------------------------------------------------

func TestRehydrateNaoReaplicaNada(t *testing.T) {
	criacao := opNow
	atualizacao := opNow.Add(3 * time.Hour)
	proxima := atualizacao.Add(5 * time.Second)

	tx := Rehydrate(
		"txn-1", "w-1", "p-1", KindRefund, StatusRejected, dinheiro(t, "25.00", "BRL"),
		opGameID, opRoundID, opKey, "hash-abc", opProviderID, opExternalID, "ext-bet", "txn-original",
		"REFERENCE_NOT_FOUND", dinheiroPtr(t, "975.00", "BRL"), 7, proxima, criacao, atualizacao,
	)

	if tx.Status() != StatusRejected {
		t.Errorf("status = %q", tx.Status())
	}
	if !tx.UpdatedAt().Equal(atualizacao) || !tx.CreatedAt().Equal(criacao) {
		t.Error("instantes alterados na reidratação")
	}
	if tx.FailureCode() != "REFERENCE_NOT_FOUND" {
		t.Errorf("failureCode = %q", tx.FailureCode())
	}
	if tx.ReferenceTransactionID() != "txn-original" {
		t.Errorf("referenceTransactionID = %q", tx.ReferenceTransactionID())
	}
	if tx.ReferenceResolutionAttempt() != 7 {
		t.Errorf("tentativa = %d", tx.ReferenceResolutionAttempt())
	}
	if tx.PayloadHash() != "hash-abc" {
		t.Errorf("payloadHash = %q", tx.PayloadHash())
	}
	if !mesmoDinheiro(tx.ResultBalance(), dinheiro(t, "975.00", "BRL")) {
		t.Errorf("resultBalance = %s", tx.ResultBalance())
	}
	if tx.Kind() != KindRefund || tx.ProviderID() != opProviderID {
		t.Error("campos alterados na reidratação")
	}
}

// ---------------------------------------------------------------------------
// Acessores
// ---------------------------------------------------------------------------

func TestAcessoresDaTransacao(t *testing.T) {
	tx := novaTransacao(t)

	if tx.Amount().String() != "25.00" {
		t.Errorf("Amount() = %s", tx.Amount())
	}
	if tx.Amount().Minor() != 2500 {
		t.Errorf("Amount().Minor() = %d", tx.Amount().Minor())
	}
	if tx.GameID() != opGameID {
		t.Errorf("GameID() = %q", tx.GameID())
	}
	if tx.RoundID() != opRoundID {
		t.Errorf("RoundID() = %q", tx.RoundID())
	}
	if tx.IdempotencyKey() != opKey {
		t.Errorf("IdempotencyKey() = %q", tx.IdempotencyKey())
	}
	if tx.ProviderID() != opProviderID {
		t.Errorf("ProviderID() = %q", tx.ProviderID())
	}
	if tx.ExternalTransactionID() != opExternalID {
		t.Errorf("ExternalTransactionID() = %q", tx.ExternalTransactionID())
	}
	if tx.ReferenceExternalTransactionID() != "" {
		t.Errorf("ReferenceExternalTransactionID() = %q, esperado vazio", tx.ReferenceExternalTransactionID())
	}
	if tx.ReferenceTransactionID() != "" {
		t.Errorf("ReferenceTransactionID() = %q, esperado vazio", tx.ReferenceTransactionID())
	}
	if tx.FailureCode() != "" {
		t.Errorf("FailureCode() = %q, esperado vazio", tx.FailureCode())
	}
	if !tx.ResultBalance().IsZero() {
		t.Errorf("ResultBalance() = %s, esperado zero", tx.ResultBalance())
	}
	if tx.ReferenceResolutionAttempt() != 0 {
		t.Errorf("ReferenceResolutionAttempt() = %d", tx.ReferenceResolutionAttempt())
	}
	if !tx.NextResolutionAttemptAt().IsZero() {
		t.Errorf("NextResolutionAttemptAt() = %v, esperado zero", tx.NextResolutionAttemptAt())
	}
	if tx.IsTerminal() {
		t.Error("PENDING não é terminal")
	}
}

func TestAcessoresComReferenciaPreenchida(t *testing.T) {
	tx := Rehydrate(
		"txn-1", "w-1", "p-1", KindRefund, StatusPendingReference, dinheiro(t, "25.00", "BRL"),
		opGameID, opRoundID, opKey, "hash-abc", opProviderID, opExternalID, "ext-bet", "txn-original",
		"", dinheiroPtr(t, "0.00", "BRL"), 2, opNow.Add(time.Second), opNow, opNow,
	)

	if tx.ReferenceExternalTransactionID() != "ext-bet" {
		t.Errorf("ReferenceExternalTransactionID() = %q", tx.ReferenceExternalTransactionID())
	}
	if tx.ReferenceTransactionID() != "txn-original" {
		t.Errorf("ReferenceTransactionID() = %q", tx.ReferenceTransactionID())
	}
	if tx.ReferenceResolutionAttempt() != 2 {
		t.Errorf("ReferenceResolutionAttempt() = %d", tx.ReferenceResolutionAttempt())
	}
	if !tx.NextResolutionAttemptAt().Equal(opNow.Add(time.Second)) {
		t.Errorf("NextResolutionAttemptAt() = %v", tx.NextResolutionAttemptAt())
	}
}

// ---------------------------------------------------------------------------
// Erros
// ---------------------------------------------------------------------------

func TestErrorTextoECodigos(t *testing.T) {
	casos := []struct {
		err  error
		want string
	}{
		{ErrInvalidTransaction, "wagering: INVALID_TRANSACTION: transação inválida"},
		{ErrInvalidTransition, "wagering: INVALID_TRANSITION: transição de estado inválida"},
		{ErrTerminalState, "wagering: TERMINAL_STATE: estado terminal não aceita transição"},
		{ErrMissingReference, "wagering: REFERENCE_REQUIRED: referência à transação original é obrigatória"},
		{ErrInternalKind, "wagering: OPERATION_NOT_ALLOWED: tipo de operação interna não é aceito de fora"},
		{ErrAlreadyReversed, "wagering: ALREADY_REVERSED: referência já revertida para este tipo"},
		{ErrProcessingFailed, "wagering: PROCESSING_FAILED: falha permanente de processamento"},
	}

	for _, c := range casos {
		if got := c.err.Error(); got != c.want {
			t.Errorf("Error() = %q, esperado %q", got, c.want)
		}
	}
}

func TestErrosNaoSeConfundem(t *testing.T) {
	todos := []error{
		ErrInvalidTransaction, ErrInvalidTransition, ErrTerminalState,
		ErrMissingReference, ErrInternalKind, ErrAlreadyReversed, ErrProcessingFailed,
	}

	for i, a := range todos {
		for j, b := range todos {
			if i == j {
				continue
			}
			if errors.Is(a, b) {
				t.Errorf("%v casou com %v", a, b)
			}
		}
	}
}

func TestIsNaoCasaComErroDeOutroTipo(t *testing.T) {
	// Error.Is só reconhece *Error do pacote: um erro comum não pode casar com
	// um sentinela de domínio.
	if errors.Is(ErrInvalidTransition, errors.New("erro qualquer")) {
		t.Error("sentinela casou com erro que não é *Error")
	}
	if errors.Is(ErrTerminalState, errors.New("outro")) {
		t.Error("sentinela casou com erro que não é *Error")
	}
	if !errors.Is(errors.Join(errors.New("contexto"), ErrMissingReference), ErrMissingReference) {
		t.Error("erro de domínio não atravessou o Join")
	}
}

func TestErroAtravessaEnvelopamento(t *testing.T) {
	tx := novaTransacao(t)
	if err := tx.MarkRejected("X", opNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	err := tx.MarkFailed("Y", opNow.Add(2*time.Minute))
	if err == nil {
		t.Fatal("esperado erro de estado terminal")
	}

	envelopado := errors.Join(errors.New("camada de aplicação"), err)
	if !errors.Is(envelopado, ErrTerminalState) {
		t.Errorf("errors.Is não atravessou: %v", envelopado)
	}
	if CodeOf(envelopado) != CodeTerminalState {
		t.Errorf("CodeOf = %q", CodeOf(envelopado))
	}
}
