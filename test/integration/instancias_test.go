//go:build integration

package integration

// Infraestrutura de prova MULTI-INSTÂNCIA deste pacote.
//
// # Por que processos, e não containers
//
// O README §8 é literal: "as garantias devem ser demonstradas com pelo menos
// três PROCESSOS INDEPENDENTES, cada um com suas próprias conexões e memória".
// Um processo do binário real tem exatamente essas propriedades: PID próprio
// (memória separada pelo sistema operacional), pool de conexões próprio (cada
// processo abre o seu `pgxpool`) e nenhum estado compartilhado além do que está
// no PostgreSQL e no SQS. Um container acrescenta isolamento de imagem e de
// rede, mas não muda nada no que a concorrência enxerga — e custaria um rebuild
// a cada ciclo da suíte. Os containers entram na prova de SIGTERM (bloco 10c),
// que é a única coisa que o processo local no Windows não consegue provar.
//
// # O que aqui é INFRAESTRUTURA, e o que é asserção
//
// Esta base NÃO prova nada sozinha: ela sobe N processos e entrega um handle
// com porta, URL, PID e log. As provas (T1–T8) ficam nos arquivos de cenário.
// A única exceção é `TestTresInstanciasIndependentes`, que prova a própria
// independência — porque "três processos" é critério de aceite do README §8, e
// afirmar sem medir seria justamente o que esta fase existe para não fazer.
//
// # Rotina
//
//	docker compose up -d postgres keycloak localstack
//	$env:TEST_DATABASE_URL='postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable'
//	$env:OIDC_ISSUER='http://localhost:8080/realms/jungleg'
//	$env:OIDC_DISCOVERY_URL='http://localhost:8080/realms/jungleg'
//	$env:OIDC_AUDIENCE='jungleg-api'
//	$env:SQS_ENDPOINT='http://localhost:4566'
//	go test -count=1 -race -tags=integration -timeout 600s ./test/integration/
//
// As portas ficam na faixa 18091+ (o compose publica 8081 e o teste do binário
// em `internal/app` usa 18099 — nenhuma colide), e o banco é o `jungle_test`,
// porque os testes TRUNCAM tabelas. Nunca aponte para `jungleg`.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// portaBaseInstancias é o primeiro número da faixa usada pelas instâncias.
//
// O valor é escolhido alto e fora das portas do compose (8081), do Keycloak
// (8080) e do LocalStack (4566). A busca é para CIMA a partir daqui: se a
// máquina já tiver algo escutando (um teste anterior travado, por exemplo), a
// instância seguinte usa a próxima porta livre em vez de falhar por colisão.
const portaBaseInstancias = 18091

// binarioDaSuite é o caminho do binário real, compilado UMA vez por execução da
// suíte (no TestMain). Compilar dentro de cada teste custaria segundos por
// cenário e ainda permitiria que cada teste rodasse um código diferente se o
// repositório mudasse no meio.
var binarioDaSuite string

// opcoesInstancia decide, POR INSTÂNCIA, quais laços de fundo sobem.
//
// Nos cenários de número (T1, T2, T3) os três ficam DESLIGADOS de propósito: um
// publicador da outbox ou um consumidor rodando de fundo mexeria na fila e nas
// linhas do banco enquanto o teste mede, e a asserção passaria a depender de
// quem chegou primeiro. Ligar o laço é uma decisão do cenário, não um default.
type opcoesInstancia struct {
	// Consumidor liga `SQS_CONSUME_TRANSACTIONS` (o laço que consome a fila).
	Consumidor bool
	// Outbox liga `OUTBOX_ENABLED` (o publicador de `wallet-events.fifo`).
	Outbox bool
	// Referencia liga `REFERENCE_ENABLED` (worker de referências + reaper).
	Referencia bool
	// Extras sobrescreve variáveis de ambiente desta instância.
	Extras map[string]string
}

// instancia é um processo do binário real com a sua porta, o seu log e o seu pid.
type instancia struct {
	nome    string
	porta   int
	baseURL string
	pid     int
	cmd     *exec.Cmd
	log     *saidaDoProcesso
	parou   sync.Once
}

// saidaDoProcesso acumula stdout+stderr do processo com mutex.
//
// O `-race` reprovaria `strings.Builder` escrito por duas goroutines do sistema
// (o processo escreve em stdout e stderr por pipes distintos, e o `os/exec` lê os
// dois em goroutines separadas). O mutex não é zelo: sem ele o teste do
// consumidor já tinha dado falso positivo.
type saidaDoProcesso struct {
	mu      sync.Mutex
	destino strings.Builder
}

func (s *saidaDoProcesso) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.destino.Write(p)
}

func (s *saidaDoProcesso) texto() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.destino.String()
}

// compilarBinarioDaSuite compila `cmd/api` e devolve o caminho e o diretório
// temporário (para o TestMain remover no fim).
//
// É chamado pelo TestMain `test/integration`, e não por um teste, porque o
// binário é o MESMO para todos os cenários. O padrão é o de
// `internal/app/healthcheck_integration_test.go` (Fase 7), que sobe um processo;
// esta fase precisa de N, então o que se generaliza é a quantidade, não o
// método: mesmo `go build`, mesmo `exec.Command`, mesmas garantias de limpeza.
func compilarBinarioDaSuite() (caminho, diretorio string, err error) {
	diretorio, err = os.MkdirTemp("", "jungleg-instancia-*")
	if err != nil {
		return "", "", fmt.Errorf("criando o diretório do binário: %w", err)
	}

	nome := "jungleg-api"
	if runtime.GOOS == "windows" {
		nome += ".exe"
	}
	caminho = filepath.Join(diretorio, nome)

	cmd := exec.Command("go", "build", "-o", caminho, "./cmd/api")
	cmd.Dir = raizDoProjetoDeTeste()

	if saida, err := cmd.CombinedOutput(); err != nil {
		return "", diretorio, fmt.Errorf("compilando ./cmd/api: %w\n%s", err, saida)
	}
	return caminho, diretorio, nil
}

// raizDoProjetoDeTeste sobe três níveis a partir deste arquivo
// (`test/integration/instancias_test.go` → raiz).
func raizDoProjetoDeTeste() string {
	_, arquivo, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(arquivo)))
}

// subirInstancias sobe `quantas` processos independentes e espera o readiness de
// todos. Qualquer falha de boot despeja o log dos processos que JÁ subiram: foi
// assim que a Fase 8 descobriu o "mensagens=0 em laço" (o log ia para
// `io.Discard`, e o sintoma não apontava para a causa).
func subirInstancias(t *testing.T, quantas int, opcoes opcoesInstancia) []*instancia {
	t.Helper()

	if quantas < 1 {
		t.Fatalf("subirInstancias chamada com %d instâncias", quantas)
	}
	if binarioDaSuite == "" {
		t.Skip("binário da suíte não compilado (TEST_DATABASE_URL ausente?): pulando cenário multi-instância")
	}

	instancias := make([]*instancia, 0, quantas)
	for i := 1; i <= quantas; i++ {
		instancias = append(instancias, subirInstancia(t, i, opcoes))
	}

	for _, inst := range instancias {
		if erro := inst.esperarPronta(40 * time.Second); erro != nil {
			for _, outra := range instancias {
				t.Logf("--- log do processo %s (porta %d) ---\n%s", outra.nome, outra.porta, outra.log.texto())
			}
			t.Fatalf("a instância %s não ficou pronta: %v", inst.nome, erro)
		}
	}
	return instancias
}

// subirInstancia sobe UM processo. O nome `api-<n>` vai no DSN como
// `application_name`, o que torna as conexões de cada instância identificáveis de
// fora — é com isso que `TestTresInstanciasIndependentes` prova que o pool é
// próprio.
func subirInstancia(t *testing.T, indice int, opcoes opcoesInstancia) *instancia {
	t.Helper()

	nome := fmt.Sprintf("api-%d", indice)
	porta := portaLivre(t, portaBaseInstancias+indice-1)

	inst := &instancia{
		nome:    nome,
		porta:   porta,
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", porta),
		log:     &saidaDoProcesso{},
	}

	cmd := exec.Command(binarioDaSuite)
	cmd.Env = ambienteDaInstancia(t, opcoes, nome, porta)
	cmd.Stdout = inst.log
	cmd.Stderr = inst.log
	// Sem `cmd.Dir`: o binário não lê arquivo nenhum (as migrations são
	// embutidas por `go:embed`), então o diretório de trabalho é irrelevante — e
	// deixá-lo como o do teste evitaria um caminho relativo acidental.
	if err := cmd.Start(); err != nil {
		t.Fatalf("subindo a instância %s: %v", nome, err)
	}

	inst.cmd = cmd
	inst.pid = cmd.Process.Pid

	// O encerramento é SEMPRE garantido, inclusive se o teste falhar no meio: um
	// processo sobrevivente seguraria a carteira aberta em lock e o próximo
	// `TRUNCATE` do pacote morreria — o mesmo sintoma que a Fase 6 pagou caro.
	t.Cleanup(func() {
		inst.parar(t)
		if t.Failed() {
			t.Logf("--- log do processo %s (porta %d) ---\n%s", inst.nome, inst.porta, inst.log.texto())
		}
	})

	return inst
}

// ambienteDaInstancia monta o ambiente do processo.
//
// Os valores NÃO são os de produção, e cada escolha tem motivo:
//
//   - `DB_MAX_CONNS=6`: três instâncias com o default de 20 seriam 60 conexões
//     (mais o pool do próprio teste e o de `internal/infra/postgres`), e o
//     PostgreSQL de teste é compartilhado. `DB_MIN_CONNS=1` garante que cada
//     instância mantém ao menos uma conexão aberta — é o que permite observar o
//     pool de cada uma em `pg_stat_activity`.
//   - `HTTP_STOP_TIMEOUT=12s` cobre o pior caso validado pela config
//     (`HTTP_SHUTDOWN_TIMEOUT` 5s + `SQS_SHUTDOWN_GRACE` 2s +
//     `WORKERS_SHUTDOWN_GRACE` 2s = 9s). Com o valor de produção (50s) um
//     encerramento lento viraria teste lento; com menos que 9s a instância NÃO
//     SOBE — é o defeito 33, agora com três parcelas.
//   - `SQS_WAIT_TIME=1s`: o long polling de 20s faria cada parada esperar o
//     Receive voltar. Não muda nada do que se prova e corta segundos por teste.
//   - `SQS_VISIBILITY_TIMEOUT=5s`: a config recusa menos que isso (proteção
//     contra reprocessamento concorrente). A invisibilidade REAL de uma mensagem
//     abandonada é a da FILA (60s no compose); quem precisa encurtá-la no teste
//     o faz com `ChangeMessageVisibility(0)`, e o motivo está escrito no cenário.
//   - `OUTBOX_LEASE=5s` e backoff de 1s/2s: os mínimos aceitos pela config. O
//     lease de produção (30s) tornaria qualquer cenário de recuperação um teste
//     de 30 segundos.
//   - `LOG_LEVEL=info` com buffer por processo (não `io.Discard`): o log é o
//     diagnóstico quando a asserção falha, e o buffer impede que ele inunde a
//     saída de uma suíte com 50 requisições.
func ambienteDaInstancia(t *testing.T, opcoes opcoesInstancia, nome string, porta int) []string {
	t.Helper()

	dsn := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if dsn == "" {
		dsn = "postgres://jungleg:jungleg@localhost:5432/jungle_test?sslmode=disable"
	}

	valores := map[string]string{
		"APP_ENV":                   "test",
		"LOG_LEVEL":                 "info",
		"HTTP_ADDR":                 fmt.Sprintf("127.0.0.1:%d", porta),
		"HTTP_SHUTDOWN_TIMEOUT":     "5s",
		"HTTP_STOP_TIMEOUT":         "12s",
		"DATABASE_URL":              dsnComNomeDaAplicacao(dsn, nome),
		"DB_MAX_CONNS":              "6",
		"DB_MIN_CONNS":              "1",
		"OIDC_ISSUER":               issuerDeTeste(),
		"OIDC_DISCOVERY_URL":        clientIDBase(),
		"OIDC_AUDIENCE":             envOu("OIDC_AUDIENCE", "jungleg-api"),
		"SQS_ENDPOINT":              envOu("SQS_ENDPOINT", "http://localhost:4566"),
		"SQS_WAIT_TIME":             "1s",
		"SQS_VISIBILITY_TIMEOUT":    "5s",
		"SQS_BASE_BACKOFF":          "1s",
		"SQS_MAX_BACKOFF":           "2s",
		"SQS_SHUTDOWN_GRACE":        "2s",
		"SQS_CONSUME_TRANSACTIONS":  strconv.FormatBool(opcoes.Consumidor),
		"OUTBOX_ENABLED":            strconv.FormatBool(opcoes.Outbox),
		"OUTBOX_POLL_INTERVAL":      "200ms",
		"OUTBOX_LEASE":              "5s",
		"OUTBOX_BASE_BACKOFF":       "1s",
		"OUTBOX_MAX_BACKOFF":        "2s",
		"OUTBOX_PARKED_BACKOFF":     "2s",
		"REFERENCE_ENABLED":         strconv.FormatBool(opcoes.Referencia),
		"REFERENCE_POLL_INTERVAL":   "200ms",
		"REFERENCE_BASE_BACKOFF":    "1s",
		"REFERENCE_MAX_BACKOFF":     "2s",
		"REFERENCE_PENDING_MAX_AGE": "2m",
		"REFERENCE_REAPER_INTERVAL": "1s",
		"WORKERS_SHUTDOWN_GRACE":    "2s",
	}
	for chave, valor := range opcoes.Extras {
		valores[chave] = valor
	}

	// O `os/exec` deduplica o ambiente ficando com a ÚLTIMA ocorrência de cada
	// nome, então herdar `os.Environ()` e acrescentar por cima é o que garante
	// que uma variável exportada pelo shell (um `SQS_CONSUME_TRANSACTIONS=true`
	// esquecido, por exemplo) não mude o cenário por acidente.
	env := os.Environ()
	for chave, valor := range valores {
		env = append(env, chave+"="+valor)
	}
	return env
}

// dsnComNomeDaAplicacao acrescenta `application_name` à string de conexão.
//
// É o que faz `pg_stat_activity` mostrar `api-1`, `api-2` e `api-3`: sem isso,
// todas as conexões do banco de teste seriam indistinguíveis e "cada um com suas
// próprias conexões" continuaria sendo afirmação, não medida.
func dsnComNomeDaAplicacao(dsn, nome string) string {
	separador := "?"
	if strings.Contains(dsn, "?") {
		separador = "&"
	}
	return dsn + separador + "application_name=" + nome
}

// portaLivre devolve a primeira porta livre a partir de `inicio`.
func portaLivre(t *testing.T, inicio int) int {
	t.Helper()

	for porta := inicio; porta < inicio+100; porta++ {
		ouvinte, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", porta))
		if err != nil {
			continue
		}
		_ = ouvinte.Close()
		return porta
	}
	t.Fatalf("nenhuma porta livre na faixa %d-%d", inicio, inicio+99)
	return 0
}

// esperarPronta consulta o `/health/ready` até 200 (o mesmo endpoint que o
// healthcheck do compose usa). O corpo da última resposta vai no erro: um
// readiness 503 diz QUAL dependência está fora, e é a diferença entre "o
// processo não subiu" e "o processo subiu sem o SQS".
func (i *instancia) esperarPronta(prazo time.Duration) error {
	cliente := &http.Client{Timeout: 2 * time.Second}
	fim := time.Now().Add(prazo)

	var ultima string
	for time.Now().Before(fim) {
		resp, err := cliente.Get(i.baseURL + "/health/ready")
		if err == nil {
			corpo, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			ultima = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(corpo)))
		} else {
			ultima = err.Error()
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("readiness não respondeu 200 em %s (última leitura: %s)", prazo, ultima)
}

// estaPronta diz se a instância responde readiness 200 agora.
func (i *instancia) estaPronta(t *testing.T) bool {
	t.Helper()

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(i.baseURL + "/health/ready")
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode == http.StatusOK
}

// viva diz se o processo da instância ainda está respondendo (liveness).
func (i *instancia) viva(t *testing.T) bool {
	t.Helper()

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(i.baseURL + "/health/live")
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode == http.StatusOK
}

// parar encerra o processo. Idempotente: o cenário pode derrubar a instância no
// meio (T8) e o `t.Cleanup` chama de novo no fim.
func (i *instancia) parar(t *testing.T) {
	t.Helper()

	i.parou.Do(func() {
		if i.cmd == nil || i.cmd.Process == nil {
			return
		}
		_ = i.cmd.Process.Kill()
		_, _ = i.cmd.Process.Wait()
	})
}

// chamar faz uma requisição à instância, com a mesma forma do `chamar` do
// servidor em memória (Fase 7) — o tipo `resposta` é o mesmo, então as
// asserções existentes continuam valendo.
func (i *instancia) chamar(t *testing.T, metodo, caminho, token string, corpo any, cabecalhos map[string]string) resposta {
	t.Helper()

	resp, err := i.tentarChamar(metodo, caminho, token, corpo, cabecalhos)
	if err != nil {
		t.Fatalf("%s %s %s: %v", i.nome, metodo, caminho, err)
	}
	return resp
}

// tentarChamar é o `chamar` que devolve erro em vez de falhar o teste.
//
// Existe porque os cenários de concorrência disparam requisições de GOROUTINES, e
// `t.Fatalf` chamado fora da goroutine do teste não interrompe o teste: ele sai
// do processo errado. Aqui o erro viaja como valor, e quem decide é quem espera o
// grupo — depois da barreira, na goroutine principal.
func (i *instancia) tentarChamar(metodo, caminho, token string, corpo any, cabecalhos map[string]string) (resposta, error) {
	var leitor io.Reader
	if corpo != nil {
		bruto, err := json.Marshal(corpo)
		if err != nil {
			return resposta{}, fmt.Errorf("serializando o corpo: %w", err)
		}
		leitor = bytes.NewReader(bruto)
	}

	req, err := http.NewRequestWithContext(context.Background(), metodo, i.baseURL+caminho, leitor)
	if err != nil {
		return resposta{}, fmt.Errorf("montando a requisição: %w", err)
	}
	if corpo != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for chave, valor := range cabecalhos {
		req.Header.Set(chave, valor)
	}

	// O prazo é folgado de propósito: numa corrida de 80+80 a segunda requisição
	// ESPERA o lock da carteira, e o tempo de espera é o próprio objeto do teste
	// (medido à parte). Um timeout curto transformaria a prova em falha de rede.
	cliente := &http.Client{Timeout: 60 * time.Second}
	resp, err := cliente.Do(req)
	if err != nil {
		return resposta{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	lido, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resposta{}, fmt.Errorf("lendo a resposta: %w", err)
	}
	return resposta{status: resp.StatusCode, corpo: lido, header: resp.Header}, nil
}

// ---------------------------------------------------------------------------
// Evidência de independência: as conexões de cada processo
// ---------------------------------------------------------------------------

// backendsPorAplicacao conta as conexões do banco de teste agrupadas por
// `application_name` — ou seja, por INSTÂNCIA.
//
// É a medida que transforma "cada um com suas próprias conexões" (README §8) em
// número: se as três instâncias compartilhassem um pool, apareceriam no máximo
// dois nomes (o do processo de teste e um só para todas).
func backendsPorAplicacao(t *testing.T) map[string]int {
	t.Helper()

	linhas, err := poolTeste.Query(context.Background(),
		`SELECT coalesce(application_name, '') AS app, count(*)
		   FROM pg_stat_activity
		  WHERE datname = current_database()
		  GROUP BY 1`)
	if err != nil {
		t.Fatalf("lendo pg_stat_activity: %v", err)
	}
	defer linhas.Close()

	saida := map[string]int{}
	for linhas.Next() {
		var (
			nome  string
			total int
		)
		if err := linhas.Scan(&nome, &total); err != nil {
			t.Fatalf("lendo linha de pg_stat_activity: %v", err)
		}
		saida[nome] = total
	}
	return saida
}

// instanciasEsperandoLock conta quantos backends das INSTÂNCIAS estão esperando
// lock AGORA.
//
// É a medida que sustenta o T3 sem cronômetro: `wait_event_type = 'Lock'` é o
// PostgreSQL dizendo "esta sessão está parada esperando um lock", e o filtro por
// `application_name` restringe a contagem às conexões das instâncias — sem ele,
// qualquer outra suíte esperando o `pg_advisory_lock` do harness entraria na
// conta e o número mentiria.
//
// (O `application_name` só é possível por causa da correção do defeito 40: antes
// dela todas as instâncias se chamavam "jungleg-api", e não haveria como separar
// as conexões do app das do processo de teste.)
func instanciasEsperandoLock(t *testing.T) int {
	t.Helper()

	var total int
	err := poolTeste.QueryRow(context.Background(),
		`SELECT count(*)
		   FROM pg_stat_activity
		  WHERE wait_event_type = 'Lock'
		    AND application_name LIKE 'api-%'`).Scan(&total)
	if err != nil {
		t.Fatalf("contando sessões esperando lock: %v", err)
	}
	return total
}

// ---------------------------------------------------------------------------
// A prova de que são TRÊS PROCESSOS INDEPENDENTES (README §8)
// ---------------------------------------------------------------------------
// TestTresInstanciasIndependentes fecha o critério do README §8 antes de qualquer
// cenário financeiro: três processos de verdade, com conexões e memória próprias,
// e a queda de um sem efeito nos outros.
//
// O que é medido, e por que cada medida importa:
//
//  1. **PIDs distintos** — prova de PROCESSO, não de goroutine. Memória separada é
//     consequência do sistema operacional, não de disciplina do código. As três
//     instâncias também não compartilham `servico`, `uow` nem pool: cada uma
//     construiu o próprio grafo do Fx a partir do zero.
//  2. **`pg_stat_activity` com três `application_name` distintos** — prova de que
//     cada processo abriu o SEU pool. É a diferença entre esta fase e as
//     anteriores: `postgresUOW` simula outra instância, mas usa o pool do teste,
//     então nunca provou conexões independentes.
//  3. **Derrubar a `api-1` não afeta `api-2` e `api-3`** — prova de que não há
//     estado compartilhado em memória: o que sobrou continua atendendo. Se
//     houvesse (um cache, um contador, um lock local), a queda de um derrubaria
//     a garantia dos outros — e é exatamente isso o que o eliminatório
//     "dependência de uma única instância" do README §14 pune.
func TestTresInstanciasIndependentes(t *testing.T) {
	instancias := subirInstancias(t, 3, opcoesInstancia{})

	// (1) Três processos, três PIDs.
	pids := map[int]string{}
	for _, inst := range instancias {
		if inst.pid <= 0 {
			t.Fatalf("a instância %s não tem pid", inst.nome)
		}
		if outro, repetido := pids[inst.pid]; repetido {
			t.Fatalf("as instâncias %s e %s compartilham o pid %d", inst.nome, outro, inst.pid)
		}
		pids[inst.pid] = inst.nome
		if !inst.estaPronta(t) {
			t.Fatalf("a instância %s (pid %d) não está pronta", inst.nome, inst.pid)
		}
	}

	// (2) Cada processo com o seu pool: um conjunto de conexões por
	// `application_name`. `DB_MIN_CONNS=1` garante pelo menos uma conexão viva em
	// cada pool; o assert pede >= 1 e, mais importante, TRÊS nomes distintos.
	backends := backendsPorAplicacao(t)
	for _, inst := range instancias {
		if backends[inst.nome] < 1 {
			t.Logf("conexões por aplicação: %v", backends)
			t.Fatalf("a instância %s não tem conexão identificada no banco (application_name=%q)", inst.nome, inst.nome)
		}
	}
	doTeste := backends[""]
	if len(backends) < 4 { // três instâncias + o pool do processo de teste
		t.Logf("conexões por aplicação: %v", backends)
		t.Fatalf("esperava as três instâncias identificadas no banco; vieram %d nomes (teste=%d)", len(backends), doTeste)
	}

	// (3) A queda de uma não derruba as outras — e as conexões dela somem.
	derrubada := instancias[0]
	derrubada.parar(t)

	esperarAtende(t, "as conexões da api-1 desaparecerem do banco", 20*time.Second, func() bool {
		return backendsPorAplicacao(t)[derrubada.nome] == 0
	})

	for _, inst := range instancias[1:] {
		if !inst.viva(t) || !inst.estaPronta(t) {
			t.Fatalf("a instância %s parou de atender quando a %s caiu", inst.nome, derrubada.nome)
		}
	}

	// E as que sobraram continuam com o pool próprio, agora sem a primeira.
	restantes := backendsPorAplicacao(t)
	for _, inst := range instancias[1:] {
		if restantes[inst.nome] < 1 {
			t.Fatalf("a instância %s perdeu as conexões depois da queda da %s: %v",
				inst.nome, derrubada.nome, restantes)
		}
	}
}
