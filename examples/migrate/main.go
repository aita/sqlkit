// Command migrate ships the migrations in ./migrations as a standalone binary,
// rather than running them through the sqlkit binary. The migrations register
// themselves with migrations.Registry; this program builds a Migrator from that
// registry and drives migrate.NewCLI() with kong, which provides the same
// up/down/status/new/... commands the sqlkit binary does. The migrate package
// itself does not depend on kong — NewCLI returns the command grammar and this
// program (your own binary) supplies the parser.
//
// Run against a real database:
//
//	createdb app
//	DATABASE_URL=postgres://localhost/app go run . up
//	DATABASE_URL=postgres://localhost/app go run . status
//
// Other commands: down, stamp, pending, current, heads, history, sql up|down,
// and new. Run with no arguments (or --help) to see them.
package main

import (
	"context"
	"log"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/alecthomas/kong"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/examples/migrate/migrations"
	"github.com/aita/sqlkit/migrate"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("set DATABASE_URL to a PostgreSQL connection string")
	}
	db, err := sqlkit.Open("pgx", dsn)
	if err != nil {
		log.Fatal(err)
	}

	// The Migrator resolves the apply order from the migrations' revision DAG, so
	// registration order does not matter.
	m := migrations.Registry.NewMigrator(db)

	parser, err := kong.New(migrate.NewCLI(),
		kong.Name("migrate"),
		kong.Description("Run and scaffold schema migrations."),
	)
	if err != nil {
		log.Fatal(err)
	}
	kctx, err := parser.Parse(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	if err := kctx.Run(m.Runner(context.Background())); err != nil {
		log.Fatal(err)
	}
}
