package httpapi

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/FeliPoxZ/jungle_desafio/internal/app/ports"
	appwagering "github.com/FeliPoxZ/jungle_desafio/internal/app/wagering"
	domainerrors "github.com/FeliPoxZ/jungle_desafio/internal/domain/errors"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	wagerdomain "github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
)

// Testes da TRADUÇÃO desfecho → status e erro → status.
//
// São testes de unidade do CONTRATO: não precisam de banco nem de IdP, e é aqui
// que a regra fica travada. A prova de que a pilha inteira funciona (token real,
// banco real) está nos testes de integração — mas se esta tabela estiver errada,
// nenhum deles salva o contrato.

func resultado(status wagerdomain.Status, replay bool) appwagering.Result {
	return appwagering.Result{
		TransactionID:    wagerdomain.TransactionID("0192f298-345e-7e38-af88-e43f851a819d"),
		Status:           status,
		IdempotentReplay: replay,
	}
}

func TestStatusDoResultado(t *testing.T) {
	casos := []struct {
		nome    string
		res     appwagering.Result
		querido int
	}{
		{"operação nova concluída ⇒ 201", resultado(wagerdomain.StatusProcessed, false), 201},
		{"replay idempotente ⇒ 200", resultado(wagerdomain.StatusProcessed, true), 200},
		{"pendência de referência ⇒ 202", resultado(wagerdomain.StatusPendingReference, false), 202},
		{"rejeição de negócio ⇒ 422", resultado(wagerdomain.StatusRejected, false), 422},
		{"falha permanente ⇒ 503", resultado(wagerdomain.StatusFailed, false), 503},
		{"PENDING durável ⇒ 500 (a borda não conhece esse desfecho)", resultado(wagerdomain.StatusPending, false), 500},
		{"estado inventado ⇒ 500", resultado(wagerdomain.Status("INVENTADO"), false), 500},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			if got := statusDoResultado(caso.res); got != caso.querido {
				t.Errorf("statusDoResultado = %d, quer %d", got, caso.querido)
			}
		})
	}
}

// A rejeição é 422: não pode virar 2xx (o provedor acharia que a aposta valeu)
// nem 400 (ele acharia que basta corrigir o corpo e reenviar).
func TestRejeicaoEh422(t *testing.T) {
	rejeitado := resultado(wagerdomain.StatusRejected, false)
	rejeitado.FailureCode = "INSUFFICIENT_FUNDS"

	if status := statusDoResultado(rejeitado); status != 422 {
		t.Errorf("status da rejeição = %d, quer 422", status)
	}
}

func TestErroDeNegocio(t *testing.T) {
	casos := []struct {
		nome          string
		err           error
		statusQuerido int
		failureCode   string
	}{
		{
			nome:          "conflito de idempotência ⇒ 409",
			err:           fmt.Errorf("wagering: chave repetida: %w", appwagering.ErrIdempotencyConflict),
			statusQuerido: 409,
		},
		{
			nome:          "carteira já existe ⇒ 409",
			err:           fmt.Errorf("wagering: %w", appwagering.ErrWalletAlreadyExists),
			statusQuerido: 409,
		},
		{
			nome:          "sentinela de conflito do repositório ⇒ 409",
			err:           ports.ErrConflict,
			statusQuerido: 409,
		},
		{
			nome:          "carteira não encontrada ⇒ 404",
			err:           fmt.Errorf("wagering: %w: %w", appwagering.ErrWalletNotFound, ports.ErrNotFound),
			statusQuerido: 404,
		},
		{
			nome:          "transação não encontrada ⇒ 404",
			err:           appwagering.ErrTransactionNotFound,
			statusQuerido: 404,
		},
		{
			nome:          "chave obrigatória ⇒ 400 com failureCode",
			err:           domainerrors.New(appwagering.CodeIdempotencyKeyRequired, "sem chave"),
			statusQuerido: 400,
			failureCode:   appwagering.CodeIdempotencyKeyRequired,
		},
		{
			nome:          "requisição inválida ⇒ 400",
			err:           domainerrors.New(appwagering.CodeInvalidRequest, "walletID inválido"),
			statusQuerido: 400,
			failureCode:   appwagering.CodeInvalidRequest,
		},
		{
			nome:          "valor inválido ⇒ 400",
			err:           domainerrors.New("INVALID_AMOUNT", "LOSS exige 0.00"),
			statusQuerido: 400,
			failureCode:   "INVALID_AMOUNT",
		},
		{
			nome:          "referência obrigatória ⇒ 400",
			err:           domainerrors.New("REFERENCE_REQUIRED", "REFUND exige referência"),
			statusQuerido: 400,
			failureCode:   "REFERENCE_REQUIRED",
		},
		{
			nome:          "tipo desconhecido ⇒ 400",
			err:           domainerrors.New("INVALID_TRANSACTION", "tipo desconhecido"),
			statusQuerido: 400,
			failureCode:   "INVALID_TRANSACTION",
		},
		{
			nome:          "OPENING de fora ⇒ 403",
			err:           domainerrors.New("OPERATION_NOT_ALLOWED", "reservado ao interno"),
			statusQuerido: 403,
		},
		{
			nome:          "falha transitória ⇒ 503",
			err:           ports.ErrTransient,
			statusQuerido: 503,
		},
		{
			nome:          "erro desconhecido ⇒ 500",
			err:           errors.New("detalhe interno do driver"),
			statusQuerido: 500,
		},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			apiErr := erroDeNegocio(caso.err)
			if apiErr == nil {
				t.Fatal("erroDeNegocio devolveu nil para um erro")
			}
			if apiErr.Status != caso.statusQuerido {
				t.Fatalf("status = %d, quer %d (%v)", apiErr.Status, caso.statusQuerido, apiErr)
			}
			if caso.failureCode != "" && apiErr.FailureCode != caso.failureCode {
				t.Errorf("failureCode = %q, quer %q", apiErr.FailureCode, caso.failureCode)
			}
			if apiErr.Status == 503 && apiErr.Headers["Retry-After"] == "" {
				t.Error("503 sem Retry-After")
			}
			// A mensagem pública nunca repete o erro interno.
			if apiErr.Internal != nil && strings.Contains(apiErr.Message, apiErr.Internal.Error()) {
				t.Errorf("a mensagem pública repete o erro interno: %q", apiErr.Message)
			}
		})
	}
}

// Falha de INFRAESTRUTURA nunca vira 4xx. Este é o teste que trava a regra: se
// alguém "melhorar" a classificação transformando tudo em 400, uma disputa
// momentânea de lock viraria uma aposta perdida.
func TestFalhaDeInfraestruturaNuncaEh4xx(t *testing.T) {
	erros := []error{
		ports.ErrTransient,
		fmt.Errorf("postgres: abrindo transação: %w", ports.ErrTransient),
		errors.New("connection refused"),
		errors.New("context deadline exceeded"),
	}

	for _, err := range erros {
		apiErr := erroDeNegocio(err)
		if apiErr.Status >= 400 && apiErr.Status < 500 {
			t.Errorf("erro %v virou %d: falha de infraestrutura não é erro do cliente", err, apiErr.Status)
		}
	}
}

func TestErroDeNegocioComNil(t *testing.T) {
	if erroDeNegocio(nil) != nil {
		t.Error("erroDeNegocio(nil) deveria devolver nil")
	}
}

func TestCodigoDoResultado(t *testing.T) {
	casos := map[int]string{
		422: CodeUnprocessable,
		202: CodePending,
		503: CodeServiceUnavailable,
		409: CodeConflict,
		404: CodeNotFound,
		403: CodeForbidden,
		401: CodeUnauthorized,
		500: CodeInternal,
	}
	for status, querido := range casos {
		if got := codigoDoResultado(status); got != querido {
			t.Errorf("codigoDoResultado(%d) = %q, quer %q", status, got, querido)
		}
	}
}

// --- Resposta do envio de operação ------------------------------------------

// O corpo precisa sair com `balance: null` / `failureCode: null` — e não com o
// campo ausente — para o cliente distinguir "não se aplica" de "não veio".
func TestRespostaDoResultado(t *testing.T) {
	zero, err := money.Parse("0.00", "BRL")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	casos := []struct {
		nome     string
		res      appwagering.Result
		comSaldo bool
		comFalha bool
	}{
		{
			nome:     "rejeição: failureCode preenchido e sem saldo",
			res:      appwagering.Result{Status: wagerdomain.StatusRejected, FailureCode: "INSUFFICIENT_FUNDS"},
			comFalha: true,
		},
		{
			nome: "pendência: nenhum dos dois",
			res:  appwagering.Result{Status: wagerdomain.StatusPendingReference},
		},
		{
			nome:     "concluída com saldo ZERO: saldo preenchido (zero é saldo legítimo)",
			res:      appwagering.Result{Status: wagerdomain.StatusProcessed, Balance: &zero},
			comSaldo: true,
		},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			resposta := respostaDoResultado(caso.res)
			if (resposta.Balance != nil) != caso.comSaldo {
				t.Errorf("balance preenchido = %v, quer %v", resposta.Balance != nil, caso.comSaldo)
			}
			if (resposta.FailureCode != nil) != caso.comFalha {
				t.Errorf("failureCode preenchido = %v, quer %v", resposta.FailureCode != nil, caso.comFalha)
			}
		})
	}
}

// --- O serviço real satisfaz o contrato da borda ----------------------------

// A asserção existe em `composition.go` (tempo de compilação); repeti-la como
// teste faz o erro aparecer na SUÍTE, e não só no build de quem mexer no caso de
// uso.
func TestServicoRealSatisfazContratoDaBorda(t *testing.T) {
	var servico WageringService = (*appwagering.Service)(nil)
	if servico == nil {
		t.Fatal("a conversão resultou em nil")
	}
}
