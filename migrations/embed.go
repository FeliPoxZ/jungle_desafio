// Package migrations embute os arquivos SQL de migration no binário.
//
// O embed mora aqui (e não em cmd/migrate) porque //go:embed só enxerga o
// diretório do próprio pacote ou subdiretórios: do cmd/migrate não seria
// possível alcançar a pasta migrations/ da raiz.
//
// Efeito colateral desejado: as migrations vão dentro do binário, então a
// imagem Docker do serviço de migration não precisa copiar a pasta SQL.
package migrations

import (
	"embed"
	"io/fs"
)

// FS contém os arquivos .sql versionados.
//
//go:embed *.sql
var FS embed.FS

// Source devolve o sistema de arquivos com os SQL embutidos.
func Source() fs.FS { return FS }
