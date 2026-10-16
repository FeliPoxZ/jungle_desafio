//go:build integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wagering"
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/wallet"
)

// persistirMovimento grava a movimentação: saldo novo na carteira e o
// lançamento no ledger, nesta ordem.
//
// A versão usada no UPDATE é a ANTERIOR, que vem no Movement. Usar
// carteira.Version() aqui passaria a versão JÁ incrementada pelo domínio, o
// UPDATE casaria com zero linhas e a operação falharia por "conflito" — é
// exatamente o erro que o tipo Movement existe para impedir.
func persistirMovimento(ctx context.Context, r Repos, carteira *wallet.Wallet, mov wallet.Movement) error {
	if mov.IsZero() {
		// Valor zero não movimenta dinheiro: não há nada a gravar.
		return nil
	}
	if err := r.Wallets.Update(ctx, carteira.ID(), mov.VersionBefore(), carteira.Balance()); err != nil {
		return err
	}
	return r.Ledger.Insert(ctx, mov.Entry())
}

// ---------------------------------------------------------------------------
// WalletRepo
// ---------------------------------------------------------------------------

func TestWalletInsertELeitura(t *testing.T) {
	limpar(t)

	w := carteiraNova(t, "player-1", "1000.00")

	uow := NewUnitOfWork(poolTeste)
	err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		porID, err := r.Wallets.GetByID(ctx, w.ID())
		if err != nil {
			return err
		}
		if porID.Balance().String() != "1000.00" {
			t.Errorf("saldo = %s, esperado 1000.00", porID.Balance())
		}
		if porID.Version() != 1 {
			t.Errorf("versão = %d, esperado 1", porID.Version())
		}
		if porID.PlayerID() != "player-1" {
			t.Errorf("playerID = %q", porID.PlayerID())
		}
		// A moeda vem de CHAR(3): se o padding não fosse removido, aqui seria
		// "BRL " e a comparação falharia.
		if porID.Currency() != money.BRL {
			t.Errorf("moeda = %q, esperado BRL", porID.Currency())
		}

		porPar, err := r.Wallets.GetByPlayerAndCurrency(ctx, "player-1", money.BRL)
		if err != nil {
			return err
		}
		if porPar.ID() != w.ID() {
			t.Errorf("GetByPlayerAndCurrency devolveu %q", porPar.ID())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestWalletUnicidadeDePlayerEMoeda(t *testing.T) {
	limpar(t)

	carteiraNova(t, "player-1", "100.00")

	// Mesmo par (player_id, currency), id diferente: precisa dar conflito.
	segunda, err := wallet.NewWallet(
		wallet.WalletID(uuid.NewString()), "player-1", brl(t, "50.00"), time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}

	uow := NewUnitOfWork(poolTeste)
	err = uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		return r.Wallets.Insert(ctx, segunda)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, esperado ErrConflict", err)
	}
	if !IsConflict(err) {
		t.Error("IsConflict = false")
	}
}

func TestWalletSaldoNegativoRecusadoPeloBanco(t *testing.T) {
	limpar(t)

	w := carteiraNova(t, "player-1", "100.00")

	// Tenta gravar saldo negativo direto pelo repo, burlando o domínio. A
	// CHECK constraint da Fase 4 é a barreira que sobrevive ao bug de aplicação.
	uow := NewUnitOfWork(poolTeste)
	err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		negativo := money.FromMinor(-1, money.BRL)
		return r.Wallets.Update(ctx, w.ID(), 1, negativo)
	})
	if !errors.Is(err, ErrCheck) {
		t.Fatalf("err = %v, esperado ErrCheck (CHECK balance_minor >= 0)", err)
	}
}

func TestWalletUpdateComVersaoErrada(t *testing.T) {
	limpar(t)

	w := carteiraNova(t, "player-1", "100.00")

	uow := NewUnitOfWork(poolTeste)
	err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		// Versão 99 não existe: o UPDATE não afeta linha e viramos conflito em
		// vez de sobrescrever.
		return r.Wallets.Update(ctx, w.ID(), 99, brl(t, "50.00"))
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, esperado ErrConflict", err)
	}

	// E o saldo original precisa estar intacto.
	err = uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		lida, err := r.Wallets.GetByID(ctx, w.ID())
		if err != nil {
			return err
		}
		if lida.Balance().String() != "100.00" {
			t.Errorf("saldo mudou apesar do conflito: %s", lida.Balance())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWalletNotFound(t *testing.T) {
	limpar(t)

	uow := NewUnitOfWork(poolTeste)
	err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		_, err := r.Wallets.GetByID(ctx, wallet.WalletID(uuid.NewString()))
		return err
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, esperado ErrNotFound", err)
	}
}

func TestWalletGetByIDForUpdateBloqueiaOutraTransacao(t *testing.T) {
	limpar(t)

	w := carteiraNova(t, "player-1", "100.00")
	uow := NewUnitOfWork(poolTeste)

	const tempoSegurando = 1500 * time.Millisecond

	// Transação A toma o lock, segura por um tempo e solta sozinha.
	//
	// A versus anterior deste teste: cancelar o contexto de B no meio da query
	// deixava a CONEXÃO suja no pool (o Postgres aborta a transação e a conexão
	// fica presa), o que fazia todos os testes seguintes estourarem o
	// lock_timeout de 3s. Aqui ninguém cancela contexto: A solta o lock e o
	// teste mede quanto B esperou.
	lockTomado := make(chan struct{})
	errosA := make(chan error, 1)

	go func() {
		errosA <- comRetry(context.Background(), func() error {
			return uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
				if _, err := r.Wallets.GetByIDForUpdate(ctx, w.ID()); err != nil {
					return err
				}
				close(lockTomado)
				time.Sleep(tempoSegurando)
				return nil
			})
		})
	}()

	<-lockTomado

	// B tenta o mesmo lock enquanto A o segura. Precisa ESPERAR, não passar.
	inicio := time.Now()
	errB := comRetry(context.Background(), func() error {
		return uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
			carteira, err := r.Wallets.GetByIDForUpdate(ctx, w.ID())
			if err != nil {
				return err
			}
			// Se B leu sem esperar, leu estado desatualizado: o lock falhou.
			if carteira.Balance().String() != "100.00" {
				t.Errorf("saldo lido por B = %s, esperado 100.00", carteira.Balance())
			}
			return nil
		})
	})
	esperou := time.Since(inicio)

	if errB != nil {
		t.Fatalf("B deveria conseguir o lock depois que A soltou: %v", errB)
	}
	if esperou < tempoSegurando {
		t.Errorf("B levou %v; deveria ter esperado pelo menos %v (FOR UPDATE não bloqueou)", esperou, tempoSegurando)
	}

	if err := <-errosA; err != nil {
		t.Fatalf("transação A falhou: %v", err)
	}
}

func TestWalletLostUpdateNaoAcontece(t *testing.T) {
	// ESTE é o teste que o enunciado cobra: duas operações concorrentes na
	// mesma carteira não podem perder atualização.
	//
	// Sem FOR UPDATE, as duas leem 1000, calculam 900 e 800, e a última a
	// gravar vence — o saldo final seria 900 ou 800 em vez de 700.
	//
	// O teste não força as duas a lerem no mesmo instante com barreira: uma
	// barreira dentro da transação criaria deadlock (a que espera segura o lock
	// que a outra precisa). A asserção do saldo final já é a prova — qualquer
	// interleaving que perca atualização produz um saldo diferente de 700.
	limpar(t)

	w := carteiraNova(t, "player-1", "1000.00")
	uow := NewUnitOfWork(poolTeste)

	// O retry envolve TODO o Run, nunca uma chamada por dentro: repetir de
	// dentro deixaria a transação anterior aberta segurando o lock, e o
	// TRUNCATE do teste seguinte morreria com deadlock.
	debitar := func(valor string) error {
		return comRetryTransacao(func() error {
			return uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
				// Lock ANTES de ler o saldo: é a ordem que importa.
				carteira, err := r.Wallets.GetByIDForUpdate(ctx, w.ID())
				if err != nil {
					return err
				}

				mov, err := carteira.Debit(txnGravada(ctx, t, r, w), brl(t, valor), time.Now().UTC())
				if err != nil {
					return err
				}
				return persistirMovimento(ctx, r, carteira, mov)
			})
		})
	}

	// As duas partem juntas, sem sincronização interna à transação.
	inicio := make(chan struct{})
	erros := make(chan error, 2)
	go func() { <-inicio; erros <- debitar("100.00") }()
	go func() { <-inicio; erros <- debitar("200.00") }()
	close(inicio)

	for i := 0; i < 2; i++ {
		if err := <-erros; err != nil {
			t.Fatalf("débito concorrente falhou: %v", err)
		}
	}

	// Saldo final: 1000 - 100 - 200 = 700. Qualquer outro valor é lost update.
	final := lerSaldo(t, w.ID())
	if final != "700.00" {
		t.Fatalf("saldo final = %s, esperado 700.00 (lost update!)", final)
	}

	// E a versão precisa ter avançado duas vezes.
	if v := lerVersao(t, w.ID()); v != 3 {
		t.Errorf("versão = %d, esperado 3 (1 inicial + 2 débitos)", v)
	}

	// Os dois lançamentos têm que existir, e a soma explicar a diferença.
	err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		soma, err := r.Ledger.SumSignedByWallet(ctx, w.ID())
		if err != nil {
			return err
		}
		if soma != -30000 {
			t.Errorf("soma dos lançamentos = %d, esperado -30000", soma)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// LedgerRepo
// ---------------------------------------------------------------------------

func TestLedgerInsertELeituraComSaldo(t *testing.T) {
	limpar(t)

	w := carteiraNova(t, "player-1", "1000.00")
	uow := NewUnitOfWork(poolTeste)

	var txnID string
	err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		carteira, err := r.Wallets.GetByIDForUpdate(ctx, w.ID())
		if err != nil {
			return err
		}
		mov, err := carteira.Debit(txnGravada(ctx, t, r, w), brl(t, "25.00"), time.Now().UTC())
		if err != nil {
			return err
		}
		if err := persistirMovimento(ctx, r, carteira, mov); err != nil {
			return err
		}
		txnID = string(mov.Entry().TransactionID())
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	err = uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		lido, err := r.Ledger.GetByTransaction(ctx, w.ID(), txnID)
		if err != nil {
			return err
		}
		if lido.Amount().String() != "25.00" {
			t.Errorf("valor = %s", lido.Amount())
		}
		if lido.BalanceBefore().String() != "1000.00" || lido.BalanceAfter().String() != "975.00" {
			t.Errorf("saldos = %s -> %s", lido.BalanceBefore(), lido.BalanceAfter())
		}
		if lido.Direction() != wallet.DirectionDebit {
			t.Errorf("direção = %q", lido.Direction())
		}
		// A moeda vem do JOIN com wallets.
		if lido.Amount().Currency() != money.BRL {
			t.Errorf("moeda = %q", lido.Amount().Currency())
		}

		soma, err := r.Ledger.SumSignedByWallet(ctx, w.ID())
		if err != nil {
			return err
		}
		if soma != -2500 {
			t.Errorf("soma assinada = %d, esperado -2500 (débito é negativo)", soma)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Run de leitura: %v", err)
	}
}

func TestLedgerUnicoPorTransacao(t *testing.T) {
	limpar(t)

	w := carteiraNova(t, "player-1", "1000.00")
	uow := NewUnitOfWork(poolTeste)

	err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		carteira, err := r.Wallets.GetByIDForUpdate(ctx, w.ID())
		if err != nil {
			return err
		}
		mov, err := carteira.Debit(txnGravada(ctx, t, r, w), brl(t, "10.00"), time.Now().UTC())
		if err != nil {
			return err
		}
		return persistirMovimento(ctx, r, carteira, mov)
	})
	if err != nil {
		t.Fatal(err)
	}

	// Segundo lançamento para a MESMA transação: o UNIQUE recusa.
	//
	// O transaction_id precisa existir em wager_transactions (há FK), então
	// gravamos a operação com ID fixo antes de exercitar o índice único.
	txnFixo := uuid.NewString()
	txReferencia, err := wagering.New(
		wagering.TransactionID(txnFixo), w.ID(), w.PlayerID(),
		wagering.KindBet, brl(t, "5.00"), "game-1", "round-1",
		"key-"+uuid.NewString(), "provider-a", "ext-"+uuid.NewString(), "",
		map[string]any{"amount": "5.00"}, encodeCanonico, time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("wagering.New: %v", err)
	}
	if err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		return r.Transactions.Insert(ctx, txReferencia)
	}); err != nil {
		t.Fatalf("gravando a transação de referência: %v", err)
	}

	err = uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		// Dois lançamentos para a MESMA carteira e a MESMA transação: o
		// segundo tem que ser recusado pelo índice único.
		primeiro := wallet.RehydrateEntry(
			uuid.NewString(), w.ID(), wallet.TransactionID(txnFixo),
			wallet.DirectionDebit, brl(t, "5.00"), brl(t, "990.00"), brl(t, "985.00"), time.Now().UTC(),
		)
		if err := r.Ledger.Insert(ctx, *primeiro); err != nil {
			return err
		}
		segundo := wallet.RehydrateEntry(
			uuid.NewString(), w.ID(), wallet.TransactionID(txnFixo),
			wallet.DirectionDebit, brl(t, "5.00"), brl(t, "985.00"), brl(t, "980.00"), time.Now().UTC(),
		)
		return r.Ledger.Insert(ctx, *segundo)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, esperado ErrConflict (UNIQUE wallet_id, transaction_id)", err)
	}
}

func TestLedgerImutavelNoBanco(t *testing.T) {
	limpar(t)

	w := carteiraNova(t, "player-1", "1000.00")
	uow := NewUnitOfWork(poolTeste)

	var entry wallet.Entry
	err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		carteira, err := r.Wallets.GetByIDForUpdate(ctx, w.ID())
		if err != nil {
			return err
		}
		mov, err := carteira.Debit(txnGravada(ctx, t, r, w), brl(t, "10.00"), time.Now().UTC())
		if err != nil {
			return err
		}
		if err := persistirMovimento(ctx, r, carteira, mov); err != nil {
			return err
		}
		entry = mov.Entry()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// UPDATE direto no banco: o trigger da Fase 4 levanta exceção. Não existe
	// método de update no repositório de propósito — este teste prova que, se
	// alguém escrever SQL na mão, o banco ainda recusa.
	_, err = poolTeste.Exec(context.Background(),
		`UPDATE wallet_ledger_entries SET amount_minor = 1 WHERE id = $1`, entry.ID())
	if err == nil {
		t.Fatal("UPDATE no ledger deveria ser recusado pelo trigger")
	}
	if !errors.Is(Classify(err), ErrCheck) {
		t.Errorf("err = %v, esperado ErrCheck (restrict_violation do trigger)", err)
	}

	_, err = poolTeste.Exec(context.Background(),
		`DELETE FROM wallet_ledger_entries WHERE id = $1`, entry.ID())
	if err == nil {
		t.Fatal("DELETE no ledger deveria ser recusado pelo trigger")
	}
}

func TestLedgerCursorPaginacao(t *testing.T) {
	limpar(t)

	w := carteiraNova(t, "player-1", "1000.00")
	uow := NewUnitOfWork(poolTeste)

	// Três lançamentos em instantes distintos, para a ordem ser determinística.
	for i := 0; i < 3; i++ {
		err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
			carteira, err := r.Wallets.GetByIDForUpdate(ctx, w.ID())
			if err != nil {
				return err
			}
			agora := time.Now().UTC().Add(time.Duration(i) * time.Millisecond)
			mov, err := carteira.Debit(txnGravada(ctx, t, r, w), brl(t, "10.00"), agora)
			if err != nil {
				return err
			}
			return persistirMovimento(ctx, r, carteira, mov)
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		pagina1, err := r.Ledger.ListByWallet(ctx, w.ID(), 2, nil)
		if err != nil {
			return err
		}
		if len(pagina1) != 2 {
			t.Fatalf("página 1 devolveu %d lançamentos, esperado 2", len(pagina1))
		}

		cursor := CursorDeEntries(pagina1)
		if cursor == nil {
			t.Fatal("cursor não foi montado")
		}
		// Round-trip do cursor, como o cliente faria.
		decodificado, err := DecodeCursor(EncodeCursor(*cursor))
		if err != nil {
			return err
		}

		pagina2, err := r.Ledger.ListByWallet(ctx, w.ID(), 2, &decodificado)
		if err != nil {
			return err
		}
		if len(pagina2) != 1 {
			t.Fatalf("página 2 devolveu %d lançamentos, esperado 1", len(pagina2))
		}
		// Nenhum lançamento pode aparecer nas duas páginas.
		if pagina2[0].ID() == pagina1[0].ID() || pagina2[0].ID() == pagina1[1].ID() {
			t.Error("paginação repetiu lançamento entre páginas")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TxnRepo — idempotência
// ---------------------------------------------------------------------------

func TestTxnInsertEIdempotencia(t *testing.T) {
	limpar(t)

	w := carteiraNova(t, "player-1", "1000.00")
	tx := txnNova(t, w, wagering.KindBet, "25.00")

	uow := NewUnitOfWork(poolTeste)
	err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		return r.Transactions.Insert(ctx, tx)
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// A MESMA chave de idempotência, id novo: o índice único recusa. É assim
	// que o reenvio do provedor é detectado.
	duplicada, err := wagering.New(
		wagering.TransactionID(uuid.NewString()), w.ID(), w.PlayerID(),
		wagering.KindBet, brl(t, "25.00"), "game-1", "round-1",
		tx.IdempotencyKey(), "provider-a", "ext-outro", "",
		map[string]any{"amount": "25.00"}, encodeCanonico, time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}

	err = uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		return r.Transactions.Insert(ctx, duplicada)
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, esperado ErrConflict (idempotência)", err)
	}

	// E a leitura pela chave encontra a original.
	err = uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		achada, err := r.Transactions.GetByIdempotencyKey(ctx, "provider-a", tx.IdempotencyKey())
		if err != nil {
			return err
		}
		if achada.ID() != tx.ID() {
			t.Errorf("achou %q, esperado %q", achada.ID(), tx.ID())
		}
		if achada.Status() != wagering.StatusPending {
			t.Errorf("status = %q, esperado PENDING", achada.Status())
		}
		if achada.Amount().String() != "25.00" {
			t.Errorf("valor = %s", achada.Amount())
		}
		if achada.Kind() != wagering.KindBet {
			t.Errorf("kind = %q", achada.Kind())
		}
		if achada.PayloadHash() != tx.PayloadHash() {
			t.Errorf("hash = %q, esperado %q", achada.PayloadHash(), tx.PayloadHash())
		}
		if achada.GameID() != "game-1" || achada.RoundID() != "round-1" {
			t.Errorf("game/round = %q/%q", achada.GameID(), achada.RoundID())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("leitura: %v", err)
	}
}

func TestTxnOpeningIdempotencia(t *testing.T) {
	limpar(t)

	w := carteiraNova(t, "player-1", "1000.00")

	novaOpening := func(chave string) *wagering.WagerTransaction {
		tx, err := wagering.New(
			wagering.TransactionID(uuid.NewString()), w.ID(), w.PlayerID(),
			wagering.KindOpening, brl(t, "1000.00"), "game-1", "round-1",
			chave, "", "", "", nil, nil, time.Now().UTC(),
		)
		if err != nil {
			t.Fatalf("wagering.New: %v", err)
		}
		return tx
	}

	uow := NewUnitOfWork(poolTeste)
	if err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		return r.Transactions.Insert(ctx, novaOpening("abertura-1"))
	}); err != nil {
		t.Fatalf("primeira OPENING: %v", err)
	}

	// Mesma chave para OPENING: o índice parcial impede o crédito inicial
	// duplicado — a falha mais cara do desafio.
	err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		return r.Transactions.Insert(ctx, novaOpening("abertura-1"))
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, esperado ErrConflict (crédito inicial duplicado)", err)
	}

	// Duas OPENING com chaves DIFERENTES são permitidas pelo banco (a
	// unicidade é por chave, não por jogador). Quem impede abrir duas carteiras
	// para o mesmo par é o UNIQUE de wallets.
	if err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		return r.Transactions.Insert(ctx, novaOpening("abertura-2"))
	}); err != nil {
		t.Fatalf("OPENING com outra chave deveria passar: %v", err)
	}
}

func TestTxnSaveEstadoFinal(t *testing.T) {
	limpar(t)

	w := carteiraNova(t, "player-1", "1000.00")
	tx := txnNova(t, w, wagering.KindBet, "25.00")

	uow := NewUnitOfWork(poolTeste)
	if err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		return r.Transactions.Insert(ctx, tx)
	}); err != nil {
		t.Fatal(err)
	}

	// Transição no domínio e gravação do estado.
	if err := tx.MarkProcessed(brl(t, "975.00"), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		return r.Transactions.Save(ctx, tx)
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		lida, err := r.Transactions.GetByID(ctx, tx.ID())
		if err != nil {
			return err
		}
		if lida.Status() != wagering.StatusProcessed {
			t.Errorf("status = %q", lida.Status())
		}
		if !lida.HasResultBalance() {
			t.Fatal("result_balance não foi gravado")
		}
		if lida.ResultBalance().String() != "975.00" {
			t.Errorf("saldo resultante = %s", lida.ResultBalance())
		}
		if !lida.IsTerminal() {
			t.Error("deveria estar em estado terminal")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTxnSaveResultBalanceZeroNaoVirouNull(t *testing.T) {
	// Zero é saldo legítimo (carteira zerada). Não pode ser confundido com
	// "não calculado": é exatamente o caso do débito que zera a carteira.
	limpar(t)

	w := carteiraNova(t, "player-1", "100.00")
	tx := txnNova(t, w, wagering.KindBet, "100.00")

	uow := NewUnitOfWork(poolTeste)
	if err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		return r.Transactions.Insert(ctx, tx)
	}); err != nil {
		t.Fatal(err)
	}

	if err := tx.MarkProcessed(brl(t, "0.00"), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		return r.Transactions.Save(ctx, tx)
	}); err != nil {
		t.Fatal(err)
	}

	err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		lida, err := r.Transactions.GetByID(ctx, tx.ID())
		if err != nil {
			return err
		}
		if !lida.HasResultBalance() {
			t.Error("saldo zero foi gravado como NULL: perdeu a diferença entre 'zero' e 'não calculado'")
		}
		if !lida.ResultBalance().IsZero() {
			t.Errorf("saldo = %s", lida.ResultBalance())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTxnListPendingReferencesDue(t *testing.T) {
	limpar(t)

	w := carteiraNova(t, "player-1", "1000.00")
	uow := NewUnitOfWork(poolTeste)

	agora := time.Now().UTC()

	// Uma pendente vencida e outra agendada para o futuro.
	// REFUND exige referenceExternalTransactionId: usamos a mesma referência
	// para as duas, porque o que o teste exercita é o agendamento, não a
	// resolução da referência.
	vencida, err := wagering.New(
		wagering.TransactionID(uuid.NewString()), w.ID(), w.PlayerID(),
		wagering.KindRefund, brl(t, "25.00"), "game-1", "round-1",
		"key-"+uuid.NewString(), "provider-a", "ext-"+uuid.NewString(), "ext-original",
		map[string]any{"amount": "25.00"}, encodeCanonico, time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("wagering.New(refund vencida): %v", err)
	}
	futura, err := wagering.New(
		wagering.TransactionID(uuid.NewString()), w.ID(), w.PlayerID(),
		wagering.KindRefund, brl(t, "25.00"), "game-1", "round-1",
		"key-"+uuid.NewString(), "provider-a", "ext-"+uuid.NewString(), "ext-original-2",
		map[string]any{"amount": "25.00"}, encodeCanonico, time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("wagering.New(refund futura): %v", err)
	}

	for _, tx := range []*wagering.WagerTransaction{vencida, futura} {
		if err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
			return r.Transactions.Insert(ctx, tx)
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := vencida.MarkPendingReference(1, agora.Add(-time.Minute), agora); err != nil {
		t.Fatal(err)
	}
	if err := futura.MarkPendingReference(1, agora.Add(time.Hour), agora); err != nil {
		t.Fatal(err)
	}
	for _, tx := range []*wagering.WagerTransaction{vencida, futura} {
		if err := uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
			return r.Transactions.Save(ctx, tx)
		}); err != nil {
			t.Fatal(err)
		}
	}

	err = uow.Run(context.Background(), func(ctx context.Context, r Repos) error {
		due, err := r.Transactions.ListPendingReferencesDue(ctx, agora, 10)
		if err != nil {
			return err
		}
		if len(due) != 1 {
			t.Fatalf("devolveu %d pendentes, esperado 1", len(due))
		}
		if due[0].ID() != vencida.ID() {
			t.Errorf("devolveu %q, esperado %q", due[0].ID(), vencida.ID())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// InboxRepo
// ---------------------------------------------------------------------------

func TestInboxDeduplicacao(t *testing.T) {
	limpar(t)

	uow := NewUnitOfWork(poolTeste)
	ctx := context.Background()

	primeira := InboxMessage{
		ID:           uuid.NewString(),
		ConsumerName: "wager-transactions",
		MessageID:    "msg-1",
		BodyHash:     "hash-a",
	}

	var inseriu bool
	if err := uow.Run(ctx, func(ctx context.Context, r Repos) error {
		var err error
		inseriu, err = r.Inbox.InsertIfAbsent(ctx, primeira)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !inseriu {
		t.Fatal("primeira mensagem deveria ser nova")
	}

	// Mesma mensagem de novo: NÃO é erro, é o caminho esperado. Devolve false.
	segunda := primeira
	segunda.ID = uuid.NewString()
	if err := uow.Run(ctx, func(ctx context.Context, r Repos) error {
		var err error
		inseriu, err = r.Inbox.InsertIfAbsent(ctx, segunda)
		return err
	}); err != nil {
		t.Fatalf("segunda inserção não deveria dar erro: %v", err)
	}
	if inseriu {
		t.Error("mensagem repetida foi tratada como nova")
	}

	// O hash guardado é o da PRIMEIRA: é com ele que o serviço compara para
	// distinguir replay (hash igual) de conflito (hash diferente).
	err := uow.Run(ctx, func(ctx context.Context, r Repos) error {
		lida, err := r.Inbox.Get(ctx, "wager-transactions", "msg-1")
		if err != nil {
			return err
		}
		if lida.BodyHash != "hash-a" {
			t.Errorf("hash = %q, esperado hash-a", lida.BodyHash)
		}
		if lida.Status != "RECEIVED" {
			t.Errorf("status = %q", lida.Status)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInboxMarcarConclusao(t *testing.T) {
	limpar(t)

	uow := NewUnitOfWork(poolTeste)
	ctx := context.Background()
	agora := time.Now().UTC()

	if err := uow.Run(ctx, func(ctx context.Context, r Repos) error {
		_, err := r.Inbox.InsertIfAbsent(ctx, InboxMessage{
			ID: uuid.NewString(), ConsumerName: "c-1", MessageID: "m-1", BodyHash: "h-1",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := uow.Run(ctx, func(ctx context.Context, r Repos) error {
		return r.Inbox.MarkProcessed(ctx, "c-1", "m-1", agora)
	}); err != nil {
		t.Fatal(err)
	}

	err := uow.Run(ctx, func(ctx context.Context, r Repos) error {
		lida, err := r.Inbox.Get(ctx, "c-1", "m-1")
		if err != nil {
			return err
		}
		if lida.Status != "PROCESSED" {
			t.Errorf("status = %q", lida.Status)
		}
		if lida.CompletedAt.IsZero() {
			t.Error("completed_at não foi gravado")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Marcar mensagem inexistente é not found.
	err = uow.Run(ctx, func(ctx context.Context, r Repos) error {
		return r.Inbox.MarkFailed(ctx, "c-1", "nao-existe", "X", agora)
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, esperado ErrNotFound", err)
	}
}

// ---------------------------------------------------------------------------
// OutboxRepo — SKIP LOCKED
// ---------------------------------------------------------------------------

func TestOutboxClaimBatchComSkipLocked(t *testing.T) {
	// Duas instâncias reivindicando ao mesmo tempo NÃO podem pegar o mesmo
	// evento. É o SKIP LOCKED que garante isso.
	limpar(t)

	uow := NewUnitOfWork(poolTeste)
	ctx := context.Background()
	agora := time.Now().UTC()

	// Cinco eventos disponíveis.
	for i := 0; i < 5; i++ {
		if err := uow.Run(ctx, func(ctx context.Context, r Repos) error {
			return r.Outbox.Insert(ctx, OutboxEvent{
				EventID:       uuid.NewString(),
				AggregateID:   "wallet-1",
				AggregateType: "wallet",
				EventType:     "WalletBalanceChanged",
				Payload:       []byte(`{"x":1}`),
				OccurredAt:    agora,
			})
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Duas goroutines reivindicando 2 cada, simultaneamente.
	var mu sync.Mutex
	vistos := map[string]int{}
	erros := make(chan error, 2)

	reivindicar := func(chave string) error {
		var lote []OutboxEvent
		err := comRetry(context.Background(), func() error {
			return uow.Run(ctx, func(ctx context.Context, r Repos) error {
				var err error
				lote, err = r.Outbox.ClaimBatch(ctx, 2, 30*time.Second, time.Now().UTC())
				return err
			})
		})
		if err != nil {
			return err
		}
		mu.Lock()
		defer mu.Unlock()
		for _, e := range lote {
			vistos[e.EventID]++
		}
		return nil
	}

	go func() { erros <- reivindicar("a") }()
	go func() { erros <- reivindicar("b") }()

	for i := 0; i < 2; i++ {
		if err := <-erros; err != nil {
			t.Fatalf("claim falhou: %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if len(vistos) != 4 {
		t.Errorf("eventos distintos reivindicados = %d, esperado 4", len(vistos))
	}
	for id, n := range vistos {
		if n != 1 {
			t.Errorf("evento %s foi reivindicado %d vezes", id, n)
		}
	}
}

func TestOutboxLeaseImpedeRepeticaoImediata(t *testing.T) {
	limpar(t)

	uow := NewUnitOfWork(poolTeste)
	ctx := context.Background()
	agora := time.Now().UTC()

	if err := uow.Run(ctx, func(ctx context.Context, r Repos) error {
		return r.Outbox.Insert(ctx, OutboxEvent{
			EventID: uuid.NewString(), AggregateID: "w-1", AggregateType: "wallet",
			EventType: "X", Payload: []byte(`{}`), OccurredAt: agora,
		})
	}); err != nil {
		t.Fatal(err)
	}

	var primeiro []OutboxEvent
	if err := uow.Run(ctx, func(ctx context.Context, r Repos) error {
		var err error
		primeiro, err = r.Outbox.ClaimBatch(ctx, 1, 30*time.Second, agora)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(primeiro) != 1 {
		t.Fatalf("primeiro claim devolveu %d, esperado 1", len(primeiro))
	}

	// Reivindicar de novo imediatamente: o lease empurrou available_at para o
	// futuro, então não há nada. Se o processo tivesse morrido, o evento
	// voltaria sozinho quando o lease expirasse.
	if err := uow.Run(ctx, func(ctx context.Context, r Repos) error {
		segundo, err := r.Outbox.ClaimBatch(ctx, 1, 30*time.Second, time.Now().UTC())
		if err != nil {
			return err
		}
		if len(segundo) != 0 {
			t.Errorf("segundo claim devolveu %d, esperado 0 (lease ativo)", len(segundo))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestOutboxPublicacaoERetry(t *testing.T) {
	limpar(t)

	uow := NewUnitOfWork(poolTeste)
	ctx := context.Background()
	agora := time.Now().UTC()
	eventID := uuid.NewString()

	if err := uow.Run(ctx, func(ctx context.Context, r Repos) error {
		return r.Outbox.Insert(ctx, OutboxEvent{
			EventID: eventID, AggregateID: "w-1", AggregateType: "wallet",
			EventType: "WalletBalanceChanged", Payload: []byte(`{"walletId":"w-1"}`), OccurredAt: agora,
		})
	}); err != nil {
		t.Fatal(err)
	}

	// Retry: attempts incrementa no banco e o evento sai da fila até o backoff.
	if err := uow.Run(ctx, func(ctx context.Context, r Repos) error {
		return r.Outbox.MarkRetry(ctx, eventID, agora.Add(time.Hour), "SQS indisponível")
	}); err != nil {
		t.Fatal(err)
	}

	if err := uow.Run(ctx, func(ctx context.Context, r Repos) error {
		lote, err := r.Outbox.ClaimBatch(ctx, 10, time.Minute, time.Now().UTC())
		if err != nil {
			return err
		}
		if len(lote) != 0 {
			t.Errorf("evento em backoff foi reivindicado: %d", len(lote))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Depois do backoff, volta a aparecer e o attempts já contou 1.
	err := uow.Run(ctx, func(ctx context.Context, r Repos) error {
		lote, err := r.Outbox.ClaimBatch(ctx, 10, time.Minute, agora.Add(2*time.Hour))
		if err != nil {
			return err
		}
		if len(lote) != 1 {
			t.Fatalf("evento deveria reaparecer após o backoff, veio %d", len(lote))
		}
		if lote[0].Attempts != 1 {
			t.Errorf("attempts = %d, esperado 1", lote[0].Attempts)
		}
		if lote[0].LastError != "SQS indisponível" {
			t.Errorf("last_error = %q", lote[0].LastError)
		}

		if err := r.Outbox.MarkPublished(ctx, eventID, time.Now().UTC()); err != nil {
			return err
		}
		presentes := []OutboxEvent{lote[0]}
		_ = presentes
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Publicado nunca mais é reivindicado.
	err = uow.Run(ctx, func(ctx context.Context, r Repos) error {
		lote, err := r.Outbox.ClaimBatch(ctx, 10, time.Minute, agora.Add(3*time.Hour))
		if err != nil {
			return err
		}
		if len(lote) != 0 {
			t.Errorf("evento publicado foi reivindicado: %d", len(lote))
		}

		pendentes, idade, err := r.Outbox.PendingStats(ctx, agora.Add(3*time.Hour))
		if err != nil {
			return err
		}
		if pendentes != 0 {
			t.Errorf("pendentes = %d, esperado 0", pendentes)
		}
		if idade != 0 {
			t.Errorf("idade = %v, esperado 0", idade)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOutboxPendingStats(t *testing.T) {
	limpar(t)

	uow := NewUnitOfWork(poolTeste)
	ctx := context.Background()
	ocorrido := time.Now().UTC().Add(-10 * time.Minute)

	for i := 0; i < 3; i++ {
		if err := uow.Run(ctx, func(ctx context.Context, r Repos) error {
			return r.Outbox.Insert(ctx, OutboxEvent{
				EventID: uuid.NewString(), AggregateID: "w-1", AggregateType: "wallet",
				EventType: "X", Payload: []byte(`{}`), OccurredAt: ocorrido,
			})
		}); err != nil {
			t.Fatal(err)
		}
	}

	err := uow.Run(ctx, func(ctx context.Context, r Repos) error {
		pendentes, idade, err := r.Outbox.PendingStats(ctx, time.Now().UTC())
		if err != nil {
			return err
		}
		if pendentes != 3 {
			t.Errorf("pendentes = %d, esperado 3", pendentes)
		}
		if idade < 9*time.Minute {
			t.Errorf("idade = %v, esperado ~10min", idade)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// Atomicidade
// ---------------------------------------------------------------------------

func TestRollbackNaoDeixaResiduo(t *testing.T) {
	// O ponto central da Fase 5: se qualquer parte falhar, NADA fica gravado.
	// Sem isso, existiria carteira debitada sem lançamento no ledger — o saldo
	// armazenado deixaria de fechar com a soma dos lançamentos.
	limpar(t)

	w := carteiraNova(t, "player-1", "1000.00")
	uow := NewUnitOfWork(poolTeste)
	ctx := context.Background()

	erroProposital := errors.New("falha simulada depois de gravar")

	err := uow.Run(ctx, func(ctx context.Context, r Repos) error {
		carteira, err := r.Wallets.GetByIDForUpdate(ctx, w.ID())
		if err != nil {
			return err
		}
		mov, err := carteira.Debit(txnGravada(ctx, t, r, w), brl(t, "250.00"), time.Now().UTC())
		if err != nil {
			return err
		}
		if err := persistirMovimento(ctx, r, carteira, mov); err != nil {
			return err
		}
		if err := r.Outbox.Insert(ctx, OutboxEvent{
			EventID: uuid.NewString(), AggregateID: string(w.ID()), AggregateType: "wallet",
			EventType: "WalletBalanceChanged", Payload: []byte(`{}`), OccurredAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
		// Tudo gravado; agora falha e força o rollback.
		return erroProposital
	})
	if !errors.Is(err, erroProposital) {
		t.Fatalf("err = %v, esperado o erro proposital", err)
	}

	// Nada pode ter sobrado.
	if saldo := lerSaldo(t, w.ID()); saldo != "1000.00" {
		t.Errorf("saldo = %s, esperado 1000.00 (rollback não desfez o débito)", saldo)
	}
	if v := lerVersao(t, w.ID()); v != 1 {
		t.Errorf("versão = %d, esperado 1", v)
	}

	err = uow.Run(ctx, func(ctx context.Context, r Repos) error {
		entradas, err := r.Ledger.ListByWallet(ctx, w.ID(), 10, nil)
		if err != nil {
			return err
		}
		if len(entradas) != 0 {
			t.Errorf("ledger tem %d lançamentos, esperado 0", len(entradas))
		}

		lote, err := r.Outbox.ClaimBatch(ctx, 10, time.Minute, time.Now().UTC())
		if err != nil {
			return err
		}
		if len(lote) != 0 {
			t.Errorf("outbox tem %d eventos, esperado 0", len(lote))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCommitGravaTudoJunto(t *testing.T) {
	// O espelho do teste anterior: quando dá certo, carteira, ledger e outbox
	// ficam consistentes entre si.
	limpar(t)

	w := carteiraNova(t, "player-1", "1000.00")
	uow := NewUnitOfWork(poolTeste)
	ctx := context.Background()

	err := uow.Run(ctx, func(ctx context.Context, r Repos) error {
		carteira, err := r.Wallets.GetByIDForUpdate(ctx, w.ID())
		if err != nil {
			return err
		}
		mov, err := carteira.Debit(txnGravada(ctx, t, r, w), brl(t, "250.00"), time.Now().UTC())
		if err != nil {
			return err
		}
		if err := persistirMovimento(ctx, r, carteira, mov); err != nil {
			return err
		}
		return r.Outbox.Insert(ctx, OutboxEvent{
			EventID: uuid.NewString(), AggregateID: string(w.ID()), AggregateType: "wallet",
			EventType: "WalletBalanceChanged", Payload: []byte(`{}`), OccurredAt: time.Now().UTC(),
		})
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if saldo := lerSaldo(t, w.ID()); saldo != "750.00" {
		t.Errorf("saldo = %s, esperado 750.00", saldo)
	}

	err = uow.Run(ctx, func(ctx context.Context, r Repos) error {
		soma, err := r.Ledger.SumSignedByWallet(ctx, w.ID())
		if err != nil {
			return err
		}
		// A soma dos lançamentos explica a diferença de saldo: 1000 - 250.
		if soma != -25000 {
			t.Errorf("soma = %d, esperado -25000", soma)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// Auxiliares
// ---------------------------------------------------------------------------

// txnGravada grava uma transação BET PENDING e devolve o ID dela.
//
// O ledger tem chave estrangeira para wager_transactions: não existe lançamento
// sem operação que o origine. Nos testes gravamos a transação primeiro, que é
// exatamente a ordem do fluxo real da Fase 6.
func txnGravada(ctx context.Context, t *testing.T, r Repos, w *wallet.Wallet) wallet.TransactionID {
	t.Helper()

	tx := txnNova(t, w, wagering.KindBet, "25.00")
	if err := r.Transactions.Insert(ctx, tx); err != nil {
		t.Fatalf("gravando a transação da movimentação: %v", err)
	}
	return wallet.TransactionID(tx.ID())
}

// esperarLargada espera as goroutines estarem prontas para começar juntas.
//
// Aborta se qualquer goroutine terminar antes (com erro), para o teste falhar
// em vez de travar na espera.
func esperarLargada(t *testing.T, pronto *sync.WaitGroup, erros <-chan error) {
	t.Helper()

	liberado := make(chan struct{})
	go func() {
		pronto.Wait()
		close(liberado)
	}()

	select {
	case <-liberado:
		return
	case err := <-erros:
		t.Fatalf("goroutine terminou antes da largada: %v", err)
	}
}

// comRetry repete a operação quando o erro é transitório.
//
// Necessário nos testes concorrentes: dois escritores na mesma carteira podem
// disputar o lock e um deles estourar o lock_timeout. Repetir é exatamente o
// que a Fase 6 vai fazer em produção — este helper também documenta que a
// classificação está funcionando.
//
// Não repete quando o CONTEXTO do chamador acabou: nesse caso a espera não é
// disputa, é limite do teste.
func comRetry(ctx context.Context, fn func() error) error {
	var err error
	for tentativa := 0; tentativa < 30; tentativa++ {
		err = fn()
		if err == nil || !errors.Is(err, ErrTransient) {
			return err
		}
		if ctx.Err() != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("esgotou as tentativas: %w", err)
}

// comRetryTransacao repete uma transação INTEIRA quando o erro é transitório.
//
// Envolve o uow.Run completo, nunca uma chamada por dentro dele. Repetir dentro
// da transação é o erro clássico: a tentativa que falhou deixa a conexão com a
// transação aberta segurando o lock, e a repetição seguinte trava. O sintoma
// aparece longe — o TRUNCATE do teste seguinte morre com deadlock.
//
// É também o padrão que a Fase 6 deve seguir: a unidade de retry é a transação.
func comRetryTransacao(fn func() error) error {
	return comRetry(context.Background(), fn)
}

// lerSaldo lê o saldo direto do banco, sem passar pelo repositório.
func lerSaldo(t *testing.T, id wallet.WalletID) string {
	t.Helper()
	var minor int64
	if err := poolTeste.QueryRow(context.Background(),
		`SELECT balance_minor FROM wallets WHERE id = $1`, string(id)).Scan(&minor); err != nil {
		t.Fatalf("lendo saldo: %v", err)
	}
	return money.FromMinor(minor, money.BRL).String()
}

// lerVersao lê a versão direto do banco.
func lerVersao(t *testing.T, id wallet.WalletID) int64 {
	t.Helper()
	var v int64
	if err := poolTeste.QueryRow(context.Background(),
		`SELECT version FROM wallets WHERE id = $1`, string(id)).Scan(&v); err != nil {
		t.Fatalf("lendo versão: %v", err)
	}
	return v
}
