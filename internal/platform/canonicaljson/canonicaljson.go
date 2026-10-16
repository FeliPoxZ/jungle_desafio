// Package canonicaljson serializa valores em JSON de forma determinística.
//
// # Por que isso existe
//
// O hash do payload identifica a operação para efeito de idempotência. Se o
// mesmo pedido, enviado duas vezes, gerar dois hashes diferentes, o sistema
// trata o reenvio como operação nova — e debita duas vezes.
//
// O encoding/json do Go NÃO é determinístico para mapas: a ordem das chaves é
// arbitrária entre execuções. Este pacote resolve isso ordenando as chaves em
// todos os níveis.
//
// Não é o JSON canônico do RFC 8785 (JCS): números não passam por normalização
// de ponto flutuante, porque o contrato deste projeto recebe dinheiro como
// STRING. O objetivo é "mesma entrada, mesmos bytes", sem complexidade que o
// nosso contrato não usa.
package canonicaljson

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// Hash devolve o SHA-256 do valor canônico, em hexadecimal.
//
// É o valor gravado em `wager_transactions.payload_hash`: estável para o mesmo
// conteúdo, independente da ordem em que os campos chegaram.
func Hash(v any) (string, error) {
	b, err := Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Marshal serializa v em JSON canônico: chaves de objeto ordenadas em todos os
// níveis e nenhum espaço supérfluo.
//
// Arrays preservam a ordem original, porque em array a ordem é significativa.
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := escrever(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// escrever monta a representação canônica direto no buffer.
//
// O switch é feito sobre a forma GENÉRICA (map/slice/string/...), não sobre o
// tipo original, porque é só assim que se controla a ordem das chaves: com um
// struct, o encoding/json decide a ordem pelo campo, não pelo nome.
func escrever(buf *bytes.Buffer, v any) error {
	generico, err := genericizar(v)
	if err != nil {
		return err
	}
	return escreverGenerico(buf, generico)
}

// genericizar converte o valor para a forma genérica do encoding/json,
// preservando a precisão dos números (UseNumber) para não transformar inteiro
// grande em float64.
func genericizar(v any) (any, error) {
	bruto, err := marshalSemEscape(v)
	if err != nil {
		return nil, fmt.Errorf("canonicaljson: serializando: %w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(bruto))
	dec.UseNumber()

	var generico any
	if err := dec.Decode(&generico); err != nil {
		return nil, fmt.Errorf("canonicaljson: decodificando: %w", err)
	}
	return generico, nil
}

// escreverGenerico escreve o valor na forma genérica, ordenando chaves.
func escreverGenerico(buf *bytes.Buffer, v any) error {
	switch valor := v.(type) {
	case map[string]any:
		chaves := make([]string, 0, len(valor))
		for k := range valor {
			chaves = append(chaves, k)
		}
		sort.Strings(chaves)

		buf.WriteByte('{')
		for i, k := range chaves {
			if i > 0 {
				buf.WriteByte(',')
			}
			chave, err := marshalSemEscape(k)
			if err != nil {
				return err
			}
			buf.Write(chave)
			buf.WriteByte(':')
			if err := escreverGenerico(buf, valor[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil

	case []any:
		buf.WriteByte('[')
		for i, item := range valor {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := escreverGenerico(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil

	default:
		// Escalares (string, json.Number, bool, nil): a serialização da stdlib
		// já é determinística e compacta.
		bruto, err := marshalSemEscape(v)
		if err != nil {
			return fmt.Errorf("canonicaljson: escrevendo valor: %w", err)
		}
		buf.Write(bruto)
		return nil
	}
}

// marshalSemEscape serializa sem escapar HTML e sem o \n que o Encoder adiciona.
//
// O escape de HTML mudaria os bytes do hash de um jeito difícil de depurar, e o
// payload é dado de negócio, não HTML.
func marshalSemEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
