// Command sqlkitgen bootstraps a sqldef catalog from a live PostgreSQL
// or MySQL database. It introspects the schema once and emits a Go catalog file
// built on the sqldef package; from then on you maintain the schema as code
// and generate the typed table bindings and DDL from the catalog
// (sqldef.Catalog.Generate / Catalog.DDL — see examples/blog/gen).
//
// Usage:
//
//	sqlkitgen -dsn postgres://user:pass@localhost/app -schema public -package schema -out schema/schema.go
//	sqlkitgen -driver mysql -dsn 'user:pass@tcp(localhost:3306)/app' -package schema -out schema/
//
// The codegen and inspector packages are the library entry points behind this
// command; import them directly to customize the conversion.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/aita/sqlkit/codegen"
	"github.com/aita/sqlkit/inspector"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sqlkitgen:", err)
		os.Exit(1)
	}
}

func run() error {
	dsn := flag.String("dsn", "", "database connection string (required)")
	driver := flag.String("driver", "pgx", "database/sql driver name (pgx or mysql)")
	dialect := flag.String("dialect", "", "schema dialect override (postgres or mysql); inferred from -driver by default")
	schema := flag.String("schema", "", "database schema/database to load (default: public for postgres, current DB for mysql)")
	packageName := flag.String("package", "", "package name of the generated catalog file (required)")
	out := flag.String("out", "", "output file or directory; stdout if empty")
	tables := flag.String("tables", "", "comma-separated list of tables to include (default: all)")
	version := flag.Bool("version", false, "print sqlkitgen version")
	flag.Parse()

	if *version {
		fmt.Println("sqlkitgen dev")
		return nil
	}
	if *dsn == "" {
		return fmt.Errorf("-dsn is required")
	}
	if *packageName == "" {
		return fmt.Errorf("-package is required")
	}

	resolvedDialect := *dialect
	if resolvedDialect == "" {
		resolvedDialect = dialectFor(*driver)
	}

	ctx := context.Background()
	db, err := sql.Open(*driver, *dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	inspected, err := inspector.Inspect(ctx, db, resolvedDialect, *schema)
	if err != nil {
		return err
	}
	if len(inspected.Tables) == 0 {
		return fmt.Errorf("schema %q contains no tables", *schema)
	}

	file, err := codegen.GenerateCatalog(inspected, codegen.CatalogOptions{
		PackageName: *packageName,
		Tables:      splitList(*tables),
		Dialect:     resolvedDialect,
	})
	if err != nil {
		return err
	}
	return writeFiles([]codegen.File{file}, *out)
}

// writeFiles emits the generated files. With no -out, files go to stdout. A
// single file with an -out ending in .go is written to that path; otherwise
// -out is treated as a directory and each file is written under it.
func writeFiles(files []codegen.File, out string) error {
	if out == "" {
		for i, file := range files {
			if len(files) > 1 {
				fmt.Printf("// --- %s ---\n", file.Name)
			}
			if _, err := os.Stdout.Write(file.Content); err != nil {
				return err
			}
			if i < len(files)-1 {
				fmt.Println()
			}
		}
		return nil
	}

	if len(files) == 1 && strings.HasSuffix(out, ".go") {
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, files[0].Content, 0o644)
	}

	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(out, file.Name), file.Content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func splitList(csv string) []string {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// dialectFor maps a driver name to the inspector dialect.
func dialectFor(driver string) string {
	switch driver {
	case "mysql":
		return "mysql"
	default:
		return "postgres"
	}
}
