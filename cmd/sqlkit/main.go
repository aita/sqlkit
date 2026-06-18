// Command sqlkit bootstraps and consumes a decl catalog. Its inspect
// subcommand introspects a live PostgreSQL or MySQL schema once and emits a Go
// catalog file built on the decl package; from then on you maintain the
// schema as code. Its generate and ddl subcommands turn a catalog package back
// into the typed table bindings and the SQL DDL without hand-writing a
// generator program: point them at the package that declares the
// *decl.Catalog and they call codegen.Generate / meta.Metadata.CreateSQL
// for you.
//
// Usage:
//
//	sqlkit inspect --dsn postgres://user:pass@localhost/app --schema public --package schema --out schema/schema.go
//	sqlkit inspect --driver mysql --dsn 'user:pass@tcp(localhost:3306)/app' --package schema --out schema/
//	sqlkit generate ./schema --name appdb --out appdb
//	sqlkit generate github.com/me/app/schema --var Catalog --name appdb --out appdb --emit-dto
//	sqlkit ddl ./schema --dialect postgres --out schema.sql
//	sqlkit migrate new "create users" --dir migrations
//
// The codegen, meta, and introspection packages are the library entry points behind
// this command; import them directly to customize the conversion.
package main

import "github.com/alecthomas/kong"

// CLI is the sqlkit command tree. Each subcommand lives in its own file:
// inspectCmd in inspect.go, generateCmd in generate.go, ddlCmd in ddl.go,
// migrateCmd in migrate.go.
type CLI struct {
	Inspect  inspectCmd       `cmd:"" help:"Introspect a live database and emit a decl catalog."`
	Generate generateCmd      `cmd:"" help:"Generate typed table bindings from a package that declares a decl catalog (no gen.go needed)."`
	Ddl      ddlCmd           `cmd:"" help:"Render a catalog package's SQL DDL (schema.sql)."`
	Migrate  migrateCmd       `cmd:"" help:"Scaffold and run schema migrations."`
	Version  kong.VersionFlag `help:"Print sqlkit version and exit." short:"V"`
}

func main() {
	cli := CLI{}
	ctx := kong.Parse(&cli,
		kong.Name("sqlkit"),
		kong.Description("Bootstrap and maintain a decl catalog from a live database."),
		kong.UsageOnError(),
		kong.Vars{"version": "sqlkit dev"},
	)
	ctx.FatalIfErrorf(ctx.Run())
}
