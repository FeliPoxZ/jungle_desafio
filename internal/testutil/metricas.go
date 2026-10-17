// Package testutil reúne apoio de teste que não pertence a nenhum pacote de
// produção.
//
// # Por que ele existe
//
// A Fase 11 exige provar CADA métrica lendo o valor em `/metrics`, e isso é
// feito de dois jeitos: raspando o endpoint HTTP de um servidor/processo real, ou
// lendo o registry de um componente exercitado por teste de unidade. Nos dois
// casos o dado é o TEXTO do formato de exposição do Prometheus, e reescrever o
// parser em cada pacote é a receita para réguas divergentes (um teste achar que
// `{stage="x"}` é outra série).
//
// A solução aqui é a mesma biblioteca que o Prometheus usa para ler o endpoint
// (`expfmt`), que já vinha no grafo como dependência de `client_golang` — não é
// dependência nova, e não entra no binário: só testes importam este pacote.
package testutil

import (
	"io"
	"net/http"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// Familias é o resultado de uma coleta: nome da métrica -> séries.
type Familias map[string]*dto.MetricFamily

// DoEndpoint raspa `<baseURL>/metrics` e devolve as famílias.
//
// É o caminho "de fora", o mesmo que o Prometheus faz: prova que a métrica está
// EXPOSTA (registrada, com prefixo certo, fora do registry privado do teste).
func DoEndpoint(t testing.TB, baseURL string) Familias {
	t.Helper()

	cliente := &http.Client{Timeout: 10 * time.Second}
	resp, err := cliente.Get(baseURL + "/metrics")
	if err != nil {
		t.Fatalf("GET %s/metrics: %v", baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s/metrics = HTTP %d, esperado 200", baseURL, resp.StatusCode)
	}
	return DoLeitor(t, resp.Body)
}

// DoLeitor interpreta o formato de exposição do Prometheus.
func DoLeitor(t testing.TB, corpo io.Reader) Familias {
	t.Helper()

	// LegacyValidation: os nossos nomes são `jungleg_wager_*` (snake_case), o
	// formato clássico do Prometheus. O parser novo exige a escolha EXPLÍCITA do
	// esquema de validação — sem isto ele recusa o nome e faz panic.
	parser := expfmt.NewTextParser(model.LegacyValidation)
	familias, err := parser.TextToMetricFamilies(corpo)
	if err != nil {
		t.Fatalf("interpretando o exposition do Prometheus: %v", err)
	}
	return Familias(familias)
}

// Valor devolve o valor da série que casa EXATAMENTE com os labels.
//
// Para histograma devolve a CONTAGEM de observações (`_count`), que é o que o
// teste de latência quer (quantas operações foram medidas).
func (f Familias) Valor(nome string, labels map[string]string) (float64, bool) {
	familia, ok := f[nome]
	if !ok {
		return 0, false
	}
	for _, m := range familia.GetMetric() {
		if !casaLabels(m, labels) {
			continue
		}
		switch familia.GetType() {
		case dto.MetricType_COUNTER:
			return m.GetCounter().GetValue(), true
		case dto.MetricType_GAUGE:
			return m.GetGauge().GetValue(), true
		case dto.MetricType_HISTOGRAM:
			return float64(m.GetHistogram().GetSampleCount()), true
		}
	}
	return 0, false
}

// ContagemHistograma devolve `_count` e `_sum` de uma série de histograma.
func (f Familias) ContagemHistograma(nome string, labels map[string]string) (contagem uint64, soma float64, ok bool) {
	familia, existe := f[nome]
	if !existe {
		return 0, 0, false
	}
	for _, m := range familia.GetMetric() {
		if !casaLabels(m, labels) {
			continue
		}
		h := m.GetHistogram()
		if h == nil {
			return 0, 0, false
		}
		return h.GetSampleCount(), h.GetSampleSum(), true
	}
	return 0, 0, false
}

// Exigir falha o teste se a série não existir ou se o valor for diferente.
func (f Familias) Exigir(t testing.TB, nome string, labels map[string]string, esperado float64) {
	t.Helper()

	got, ok := f.Valor(nome, labels)
	if !ok {
		t.Fatalf("a série %s%s não existe em /metrics", nome, formataLabels(labels))
	}
	if got != esperado {
		t.Fatalf("%s%s = %v, esperado %v", nome, formataLabels(labels), got, esperado)
	}
}

// ExigirPeloMenos falha o teste se a série não existir ou se o valor for menor
// que o mínimo.
func (f Familias) ExigirPeloMenos(t testing.TB, nome string, labels map[string]string, minimo float64) {
	t.Helper()

	got, ok := f.Valor(nome, labels)
	if !ok {
		t.Fatalf("a série %s%s não existe em /metrics", nome, formataLabels(labels))
	}
	if got < minimo {
		t.Fatalf("%s%s = %v, esperado pelo menos %v", nome, formataLabels(labels), got, minimo)
	}
}

// ExigirExiste falha o teste se a série não existir.
func (f Familias) ExigirExiste(t testing.TB, nome string, labels map[string]string) {
	t.Helper()

	if _, ok := f.Valor(nome, labels); !ok {
		t.Fatalf("a série %s%s não existe em /metrics", nome, formataLabels(labels))
	}
}

// casaLabels compara o conjunto de labels exatamente (mesmos nomes e valores).
//
// Exato, e não "contém": um label A MAIS na série é sinal de mudança de
// contrato da métrica, e o teste precisa reprovar.
func casaLabels(m *dto.Metric, labels map[string]string) bool {
	pares := m.GetLabel()
	if len(pares) != len(labels) {
		return false
	}
	for _, par := range pares {
		esperado, ok := labels[par.GetName()]
		if !ok || esperado != par.GetValue() {
			return false
		}
	}
	return true
}

func formataLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "{}"
	}
	saida := "{"
	primeiro := true
	for nome, valor := range labels {
		if !primeiro {
			saida += ","
		}
		primeiro = false
		saida += nome + "=" + `"` + valor + `"`
	}
	return saida + "}"
}
