// Command gen regenerates the blogdb bindings and schema.sql from the
// catalog in ../schema. Run from examples/blog:
//
//	go run ./gen
package main

import (
	"log"
	"os"

	"github.com/aita/sqlkit/codegen"
	"github.com/aita/sqlkit/ddl"
	"github.com/aita/sqlkit/examples/blog/schema"
)

const sqlHeader = `-- Generated from schema/schema.go by 'go run ./gen'. Apply with:
--
--   psql "$DATABASE_URL" -f schema.sql

`

func main() {
	catalog, err := schema.Catalog.Build()
	if err != nil {
		log.Fatal(err)
	}

	files, err := codegen.Generate(catalog, codegen.Options{PackageName: "blogdb"})
	if err != nil {
		log.Fatal(err)
	}
	if err := codegen.Write("blogdb", files); err != nil {
		log.Fatal(err)
	}

	sql, err := ddl.Generate(catalog, "postgres")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("schema.sql", []byte(sqlHeader+sql), 0o644); err != nil {
		log.Fatal(err)
	}
}
