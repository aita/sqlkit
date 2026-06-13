// Command sqlkitgen bootstraps a sqldef catalog from a live PostgreSQL
// or MySQL database. Its inspect subcommand introspects the schema once and
// emits a Go catalog file built on the sqldef package; from then on you
// maintain the schema as code and generate the typed table bindings and DDL
// from the catalog (codegen.Generate / Catalog.DDL — see examples/blog/gen).
//
// Usage:
//
//	sqlkitgen inspect --dsn postgres://user:pass@localhost/app --schema public --package schema --out schema/schema.go
//	sqlkitgen inspect --driver mysql --dsn 'user:pass@tcp(localhost:3306)/app' --package schema --out schema/
//
// The codegen and inspector packages are the library entry points behind this
// command; import them directly to customize the conversion.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alecthomas/kong"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/aita/sqlkit/codegen"
	"github.com/aita/sqlkit/inspector"
)

// CLI is the sqlkitgen command tree.
type CLI struct {
	Inspect inspectCmd       `cmd:"" help:"Introspect a live database and emit a sqldef catalog."`
	Version kong.VersionFlag `help:"Print sqlkitgen version and exit." short:"V"`
}

// inspectCmd introspects a live database and emits a sqldef catalog file.
type inspectCmd struct {
	DSN     string   `help:"Database connection string." required:""`
	Driver  string   `help:"database/sql driver name (pgx or mysql)." default:"pgx"`
	Dialect string   `help:"Schema dialect override (postgres or mysql); inferred from --driver by default."`
	Schema  string   `help:"Database schema/database to load (default: public for postgres, current DB for mysql)."`
	Package string   `help:"Package name of the generated catalog file." required:""`
	Out     string   `help:"Output file or directory; stdout if empty."`
	Tables  []string `help:"Tables to include (default: all)."`
}

func (c *inspectCmd) Run() error {
	dialect := c.Dialect
	if dialect == "" {
		dialect = dialectFor(c.Driver)
	}

	ctx := context.Background()
	db, err := sql.Open(c.Driver, c.DSN)
	if err != nil {
		return err
	}
	defer db.Close()

	inspected, err := inspector.Inspect(ctx, db, dialect, c.Schema)
	if err != nil {
		return err
	}
	if len(inspected.Tables) == 0 {
		return fmt.Errorf("schema %q contains no tables", c.Schema)
	}

	file, err := codegen.GenerateCatalog(inspected, codegen.CatalogOptions{
		PackageName: c.Package,
		Tables:      c.Tables,
		Dialect:     dialect,
	})
	if err != nil {
		return err
	}
	return writeFiles([]codegen.File{file}, c.Out)
}

func main() {
	cli := CLI{}
	ctx := kong.Parse(&cli,
		kong.Name("sqlkitgen"),
		kong.Description("Bootstrap and maintain a sqldef catalog from a live database."),
		kong.UsageOnError(),
		kong.Vars{"version": "sqlkitgen dev"},
	)
	ctx.FatalIfErrorf(ctx.Run())
}

// writeFiles emits the generated files. With no out, files go to stdout. A
// single file with an out ending in .go is written to that path; otherwise
// out is treated as a directory and each file is written under it.
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

// dialectFor maps a driver name to the inspector dialect.
func dialectFor(driver string) string {
	switch driver {
	case "mysql":
		return "mysql"
	default:
		return "postgres"
	}
}
