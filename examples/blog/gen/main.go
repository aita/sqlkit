// Command gen regenerates the blogdb bindings and schema.sql from the
// catalog in ../schema. Run from examples/blog:
//
//	go run ./gen
package main

import (
	"log"
	"os"

	"github.com/aita/sqlkit/codegen"
	"github.com/aita/sqlkit/examples/blog/schema"
	"github.com/aita/sqlkit/sqldef"
)

const sqlHeader = `-- Generated from schema/schema.go by 'go run ./gen'. Apply with:
--
--   psql "$DATABASE_URL" -f schema.sql

`

func main() {
	files, err := schema.Catalog.Generate(codegen.Options{PackageName: "blogdb"})
	if err != nil {
		log.Fatal(err)
	}
	if err := sqldef.Write("blogdb", files); err != nil {
		log.Fatal(err)
	}

	ddl, err := schema.Catalog.DDL("postgres")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("schema.sql", []byte(sqlHeader+ddl), 0o644); err != nil {
		log.Fatal(err)
	}
}
