package wagering

import (
	"github.com/FeliPoxZ/jungle_desafio/internal/domain/money"
	"github.com/FeliPoxZ/jungle_desafio/internal/platform/canonicaljson"
)

// payloadDeNegocio é a projeção canônica dos campos de NEGÓCIO da requisição, e
// é ela que o hash de idempotência identifica.
//
// # O que fica de fora, e por quê
//
// A chave de idempotência e os metadados de transporte ficam de fora (README
// §9: "exclua a chave de idempotência e os metadados de transporte"). Não é
// detalhe de estilo: o HTTP manda a chave no header e o SQS manda em
// `data.idempotencyKey`; se ela entrasse no hash, o MESMO pedido chegando pelas
// duas portas geraria dois hashes, duas operações e dois débitos.
//
// # O que entra
//
// Tudo o que muda o significado financeiro do pedido: provedor, transação
// externa, jogador, carteira, rodada, jogo, tipo, valor e a referência externa
// quando existir.
//
// # Normalização
//
// O hash é calculado sobre o `Money` JÁ convertido em unidades mínimas, nunca
// sobre o texto recebido: "025.00" e "25.00" produzem o mesmo hash. Formas não
// canônicas ("25", "25.000", "+25.00", notação científica) são RECUSADAS no
// parsing — não arredondadas nem normalizadas em silêncio.
type payloadDeNegocio struct {
	ProviderID             string      `json:"providerId"`
	ExternalTransactionID  string      `json:"externalTransactionId"`
	PlayerID               string      `json:"playerId"`
	WalletID               string      `json:"walletId"`
	RoundID                string      `json:"roundId"`
	GameID                 string      `json:"gameId"`
	Kind                   string      `json:"kind"`
	Money                  money.Money `json:"money"`
	ReferenceExternalTxnID string      `json:"referenceExternalTransactionId,omitempty"`
}

// payload é o que o agregado serializa para calcular o hash.
func (r Request) payload() payloadDeNegocio {
	return payloadDeNegocio{
		ProviderID:             r.ProviderID,
		ExternalTransactionID:  r.ExternalTransactionID,
		PlayerID:               string(r.PlayerID),
		WalletID:               string(r.WalletID),
		RoundID:                r.RoundID,
		GameID:                 r.GameID,
		Kind:                   r.Kind.String(),
		Money:                  r.Amount,
		ReferenceExternalTxnID: r.ReferenceExternalTxnID,
	}
}

// codificarPayload é o encoder injetado no agregado: JSON canônico, para o hash
// não depender da ordem em que os campos chegaram.
//
// O domínio define O QUE serializar; como serializar (e a garantia de
// determinismo) fica na plataforma. É por isso que o domínio não importa
// `encoding/json`.
func codificarPayload(v any) ([]byte, error) { return canonicaljson.Marshal(v) }
