// Command sqlkitgen generates typed sqlkit table bindings from a live
// PostgreSQL schema.
//
// Usage:
//
//	sqlkitgen -dsn postgres://user:pass@localhost/app -schema public -package appdb -out appdb/tables.go
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sqlkit-dev/sqlkit/internal/codegen"
	"github.com/sqlkit-dev/sqlkit/internal/loader"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "sqlkitgen:", err)
		os.Exit(1)
	}
}

func run() error {
	dsn := flag.String("dsn", "", "database connection string (required)")
	driver := flag.String("driver", "pgx", "database/sql driver name (pgx; mysql is reserved for future support)")
	schema := flag.String("schema", "public", "database schema to load")
	packageName := flag.String("package", "", "package name of the generated file (required)")
	out := flag.String("out", "", "output file (default stdout)")
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

	ctx := context.Background()
	db, err := sql.Open(*driver, *dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	model, err := loader.Load(ctx, db, dialectFor(*driver), *schema)
	if err != nil {
		return err
	}
	if len(model.Tables) == 0 {
		return fmt.Errorf("schema %q contains no tables", *schema)
	}

	source, err := codegen.Generate(model, codegen.Options{PackageName: *packageName})
	if err != nil {
		return err
	}

	if *out == "" {
		_, err = os.Stdout.Write(source)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}
	return os.WriteFile(*out, source, 0o644)
}

// dialectFor maps a driver name to the loader dialect.
func dialectFor(driver string) string {
	switch driver {
	case "mysql":
		return "mysql"
	default:
		return "postgres"
	}
}
