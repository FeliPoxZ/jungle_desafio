package money

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Este arquivo verifica as regras do enunciado que se aplicam ao código-fonte,
// não a valores. Chamado no PLANO.md de E3 (anti-float) e de "limites
// documentados".

// ---------------------------------------------------------------------------
// Item eliminatório: nenhum cálculo monetário em ponto flutuante
// ---------------------------------------------------------------------------

// proibidos são identificadores que denunciam ponto flutuante ou as funções
// que costumam trazer float para dentro de um pacote monetário.
var proibidos = map[string]string{
	"float32":                "tipo float32 em código monetário",
	"float64":                "tipo float64 em código monetário",
	"ParseFloat":             "strconv.ParseFloat converte para float",
	"FormatFloat":            "strconv.FormatFloat converte para float",
	"Inf":                    "math.Inf em código monetário",
	"NaN":                    "math.NaN em código monetário",
	"MaxFloat64":             "constante de ponto flutuante",
	"SmallestNonzeroFloat64": "constante de ponto flutuante",
}

// TestSemPontoFlutuanteNoCodigo varre o código-fonte do pacote (fora os testes)
// em busca de tipos, literais e chamadas que envolvam ponto flutuante.
//
// É o item eliminatório do enunciado: "cálculo monetário em ponto flutuante"
// reprova o desafio, e um teste de grep no CI é o que o PLANO.md prevê.
func TestSemPontoFlutuanteNoCodigo(t *testing.T) {
	fset := token.NewFileSet()
	arquivos := parsePackage(t, fset, packageDir(t), false)

	for _, f := range arquivos {
		// Literais numéricos: 1.5 ou 1e3 não têm lugar aqui.
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.FLOAT {
				return true
			}
			t.Errorf("%s: literal de ponto flutuante %q",
				fset.Position(lit.Pos()), lit.Value)
			return true
		})

		// Identificadores suspeitos.
		ast.Inspect(f, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			motivo, achou := proibidos[id.Name]
			if !achou {
				return true
			}
			t.Errorf("%s: %s usa %q", fset.Position(id.Pos()), motivo, id.Name)
			return true
		})
	}
}

// TestMoneySoUsaInt64 trava a representação: minor é int64, não float.
func TestMoneySoUsaInt64(t *testing.T) {
	m := mustFromMinor(123456789, "BRL")
	var minor int64 = m.Minor()

	if minor != 123456789 {
		t.Errorf("Minor() = %d", minor)
	}

	// Se minor passasse a ser float, esta chamada não compilaria.
	if got := strconv.FormatInt(m.Minor(), 10); got != "123456789" {
		t.Errorf("FormatInt = %q", got)
	}
}

// ---------------------------------------------------------------------------
// Imports: só biblioteca padrão e nada de ponto flutuante
// ---------------------------------------------------------------------------

// TestImportsPermitidos garante que o pacote de domínio não arrasta
// dependência externa nem pacote de precisão flutuante.
func TestImportsPermitidos(t *testing.T) {
	// Lista fechada: o núcleo do domínio só pode usar biblioteca padrão, e só
	// estes pacotes. Qualquer import novo quebra este teste de propósito, para
	// a decisão ser consciente.
	permitidos := map[string]bool{
		"bytes":         true, // leitura do JSON recebido no UnmarshalJSON
		"encoding/json": true, // serialização do contrato externo
		"errors":        true,
		"fmt":           true,
		"strings":       true,

		// O erro de domínio é compartilhado com wallet, ledger e wagering. É a
		// única dependência interna do money, e ela aponta para um pacote sem
		// lógica: só o tipo do erro e o CodeOf. Não traz I/O, driver nem
		// aritmética.
		"github.com/FeliPoxZ/jungle_desafio/internal/domain/errors": true,
	}
	// Pacotes que indicam o caminho do float dentro de código monetário.
	vetados := map[string]string{
		"math":    "math opera em float64",
		"strconv": "strconv.ParseFloat converte para float64",
	}

	fset := token.NewFileSet()
	arquivos := parsePackage(t, fset, packageDir(t), false)

	for _, f := range arquivos {
		for _, imp := range f.Imports {
			caminho, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("import ilegível: %v", err)
			}

			if motivo, ehVetado := vetados[caminho]; ehVetado {
				t.Errorf("%s: import %q vetado: %s", fset.Position(imp.Pos()), caminho, motivo)
				continue
			}
			if !permitidos[caminho] {
				t.Errorf("%s: import %q não está na lista permitida do núcleo",
					fset.Position(imp.Pos()), caminho)
			}
		}
	}
}

// TestStrconvSoNoTeste confirma que o strconv usado para ler os próprios
// arquivos vive só nos testes: o código de produção não pode importá-lo,
// porque é o caminho mais curto para float dentro de um pacote monetário.
func TestStrconvSoNoTeste(t *testing.T) {
	fset := token.NewFileSet()
	arquivos := parsePackage(t, fset, packageDir(t), false)

	for _, f := range arquivos {
		for _, imp := range f.Imports {
			caminho, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("import ilegível: %v", err)
			}
			if caminho == "strconv" {
				t.Errorf("%s: strconv importado no código de produção", fset.Position(imp.Pos()))
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Documentação e estilo
// ---------------------------------------------------------------------------

// TestTiposExportadosTemDoc garante que tudo que é exportado tem comentário,
// nos arquivos de código e nos valores de um bloco const/var.
//
// O critério é o mesmo do `go vet` e do golint: para um bloco com vários
// valores, o comentário pode estar no bloco ou na linha de cada um.
func TestTiposExportadosTemDoc(t *testing.T) {
	fset := token.NewFileSet()
	arquivos := parsePackage(t, fset, packageDir(t), false)

	for _, f := range arquivos {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Name.IsExported() && d.Doc == nil {
					t.Errorf("%s: função exportada %s sem comentário",
						fset.Position(d.Pos()), d.Name.Name)
				}

			case *ast.GenDecl:
				if d.Tok != token.TYPE && d.Tok != token.CONST && d.Tok != token.VAR {
					continue
				}
				for _, spec := range d.Specs {
					nome, doc := nomeEDoc(spec)
					if nome == "" || !ast.IsExported(nome) {
						continue
					}
					if d.Doc == nil && doc == nil {
						t.Errorf("%s: %s exportado sem comentário",
							fset.Position(spec.Pos()), nome)
					}
				}
			}
		}
	}
}

// nomeEDoc devolve o nome e o comentário preso à especificação.
func nomeEDoc(spec ast.Spec) (string, *ast.CommentGroup) {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		return s.Name.Name, s.Doc
	case *ast.ValueSpec:
		if len(s.Names) == 0 {
			return "", nil
		}
		return s.Names[0].Name, s.Doc
	}
	return "", nil
}

// TestPacoteTemDoc garante um comentário de pacote completo, que é onde a
// representação (unidades mínimas), o limite e a justificativa de não usar
// float ficam documentados.
func TestPacoteTemDoc(t *testing.T) {
	fset := token.NewFileSet()
	arquivos := parsePackage(t, fset, packageDir(t), false)

	var texto string
	for _, f := range arquivos {
		if f.Doc != nil {
			texto += f.Doc.Text()
		}
	}

	obrigatorios := []string{
		"unidades mínimas", "int64", "float64", "limites", "maxMinor",
		// O contrato externo precisa estar documentado no pacote: é ele que a
		// borda HTTP usa, e a serialização só existe para servi-lo.
		"amount", "currency", "json",
	}
	for _, trecho := range obrigatorios {
		if !strings.Contains(strings.ToLower(texto), strings.ToLower(trecho)) {
			t.Errorf("comentário de pacote não menciona %q", trecho)
		}
	}
}

// TestLimiteDocumentadoNoCodigo confere que maxMinor está declarado com
// comentário explicando que é o limite, e que o valor documentado bate com o
// valor real.
func TestLimiteDocumentadoNoCodigo(t *testing.T) {
	if maxMinor != 9_000_000_000_000_000 {
		t.Errorf("maxMinor = %d, fora do documentado", maxMinor)
	}
	if minMinor != -maxMinor {
		t.Errorf("minMinor = %d, esperado %d", minMinor, -maxMinor)
	}
	// O limite precisa caber no int64 com folga, para as checagens de overflow
	// não estourarem elas mesmas.
	if maxMinor > 9_223_372_036_854_775_807/1000 {
		t.Errorf("maxMinor = %d tem menos de 1000x de folga até o teto do int64", maxMinor)
	}

	fset := token.NewFileSet()
	arquivos := parsePackage(t, fset, packageDir(t), false)

	for _, f := range arquivos {
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) == 0 || vs.Names[0].Name != "maxMinor" {
					continue
				}
				doc := gen.Doc
				if doc == nil {
					doc = vs.Doc
				}
				if doc == nil {
					t.Error("maxMinor precisa de comentário documentando o limite")
					return
				}
				// O comentário tem que dizer o que o valor significa.
				if !strings.Contains(strings.ToLower(doc.Text()), "maior valor") {
					t.Errorf("comentário de maxMinor não explica que é o limite: %q", doc.Text())
				}
				return
			}
		}
	}
	t.Error("maxMinor não encontrado no código")
}

// ---------------------------------------------------------------------------
// Helpers de leitura do próprio código
// ---------------------------------------------------------------------------

func packageDir(t *testing.T) string {
	t.Helper()
	_, este, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("não foi possível localizar o diretório do pacote")
	}
	return filepath.Dir(este)
}

// parsePackage lê os arquivos .go do diretório. Com incluirTestes=false,
// ignora os arquivos _test.go, que é o que a maioria das regras de estilo
// querem (as regras valem para o código de produção).
func parsePackage(t *testing.T, fset *token.FileSet, dir string, incluirTestes bool) []*ast.File {
	t.Helper()

	pkgs, err := parser.ParseDir(fset, dir, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("erro ao parsear o pacote em %s: %v", dir, err)
	}

	var arquivos []*ast.File
	for _, pkg := range pkgs {
		for nome, f := range pkg.Files {
			if !incluirTestes && strings.HasSuffix(nome, "_test.go") {
				continue
			}
			arquivos = append(arquivos, f)
		}
	}
	if len(arquivos) == 0 {
		t.Fatalf("nenhum arquivo .go encontrado em %s", dir)
	}
	return arquivos
}
