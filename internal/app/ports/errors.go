package ports

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Erros do contrato de persistência.
//
// A aplicação classifica resultado de repositório por estes sentinelas, e NÃO
// importando o pacote do banco. Quem implementa (hoje o `infra/postgres`) traduz
// o erro do driver para cá.
var (
	// ErrNotFound é "o registro não existe".
	ErrNotFound = errors.New("registro não encontrado")

	// ErrConflict é "o estado não permitiu a operação".
	//
	// Cobre três situações diferentes, e de propósito: violação de índice único
	// (idempotência, duplicidade), versão desatualizada no controle otimista, e
	// unicidade de carteira por jogador. Em todas o significado é o mesmo: alguém
	// chegou primeiro.
	ErrConflict = errors.New("conflito")

	// ErrTransient é a falha que PODE dar certo se repetida: deadlock, timeout de
	// lock, conexão caindo, sobrecarga.
	//
	// É o sentinela mais importante do sistema: é ele que decide entre repetir e
	// desistir. Um timeout de lock classificado como permanente faria o serviço
	// rejeitar uma operação legítima por causa de uma disputa momentânea.
	ErrTransient = errors.New("falha transitória")

	// ErrPermanent é a falha que repetir não resolve.
	ErrPermanent = errors.New("falha permanente")

	// ErrCheck é violação de regra do banco (CHECK, NOT NULL, trigger).
	//
	// Não deveria acontecer com o domínio validando antes — quando acontece,
	// significa que a regra do domínio e a do banco divergiram, e isso é bug.
	ErrCheck = errors.New("violação de regra do banco")

	// ErrForeignKey é referência a registro inexistente.
	ErrForeignKey = errors.New("violação de chave estrangeira")
)

// ConflictCounter conta uma transação que precisou ser REPETIDA por conflito de
// concorrência (unicidade, lock) ou falha transitória do banco.
//
// Por que uma interface de um método, e não o pacote de métricas: o caso de uso
// não conhece Prometheus (decisão do ARCHITECTURE §16). Ele conhece a IDEIA de
// "alguém conta conflitos"; quem conta é a infraestrutura de observabilidade,
// ligada no Fx. É o que tira a métrica do papel sem inverter a direção das
// dependências.
type ConflictCounter interface {
	IncConflict()
}

// IsNotFound informa se o erro é "não existe".
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// IsConflict informa se o erro é conflito de estado ou de unicidade.
func IsConflict(err error) bool { return errors.Is(err, ErrConflict) }

// IsRetryable informa se vale a pena repetir a operação.
func IsRetryable(err error) bool { return errors.Is(err, ErrTransient) }

// IsCheckViolation informa se o banco recusou por regra própria.
func IsCheckViolation(err error) bool { return errors.Is(err, ErrCheck) }

// IsForeignKeyViolation informa se faltou um registro referenciado.
func IsForeignKeyViolation(err error) bool { return errors.Is(err, ErrForeignKey) }

// EncodeCursor serializa a posição de paginação em base64url.
//
// Formato: base64url(createdAt RFC3339Nano | id).
//
// O cursor é **opaco** de propósito: o cliente não monta nem interpreta o valor,
// só devolve na próxima página. Assim a ordenação pode mudar sem quebrar o
// contrato, e ninguém depende do formato interno.
func EncodeCursor(c Cursor) string {
	bruto := c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID
	return base64.RawURLEncoding.EncodeToString([]byte(bruto))
}

// DecodeCursor lê o cursor devolvido pelo cliente.
//
// Todo caminho de erro aqui é entrada malformada do cliente: por isso a borda
// HTTP traduz isto em 400, e não em 500.
func DecodeCursor(s string) (Cursor, error) {
	if strings.TrimSpace(s) == "" {
		return Cursor{}, errors.New("cursor vazio")
	}

	bruto, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, fmt.Errorf("cursor não é base64url válido: %w", err)
	}

	partes := strings.SplitN(string(bruto), "|", 2)
	if len(partes) != 2 {
		return Cursor{}, errors.New("cursor malformado")
	}

	instante, err := time.Parse(time.RFC3339Nano, partes[0])
	if err != nil {
		return Cursor{}, fmt.Errorf("cursor com instante inválido: %w", err)
	}
	if partes[1] == "" {
		return Cursor{}, errors.New("cursor sem identificador")
	}

	return Cursor{CreatedAt: instante, ID: partes[1]}, nil
}
