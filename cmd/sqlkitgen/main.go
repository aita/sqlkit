// Command sqlkitgen bootstraps and consumes a sqldef catalog. Its inspect
// subcommand introspects a live PostgreSQL or MySQL schema once and emits a Go
// catalog file built on the sqldef package; from then on you maintain the
// schema as code. Its generate and ddl subcommands turn a catalog package back
// into the typed table bindings and the SQL DDL without hand-writing a
// generator program: point them at the package that declares the
// *sqldef.Catalog and they call codegen.Generate / ddl.Generate for you.
//
// Usage:
//
//	sqlkitgen inspect --dsn postgres://user:pass@localhost/app --schema public --package schema --out schema/schema.go
//	sqlkitgen inspect --driver mysql --dsn 'user:pass@tcp(localhost:3306)/app' --package schema --out schema/
//	sqlkitgen generate ./schema --name appdb --out appdb
//	sqlkitgen generate github.com/me/app/schema --var Catalog --name appdb --out appdb --emit-dto
//	sqlkitgen ddl ./schema --dialect postgres --out schema.sql
//
// The codegen, ddl, and inspector packages are the library entry points behind
// this command; import them directly to customize the conversion.
package main

import "github.com/alecthomas/kong"

// CLI is the sqlkitgen command tree. Each subcommand lives in its own file:
// inspectCmd in inspect.go, generateCmd in generate.go, ddlCmd in ddl.go.
type CLI struct {
	Inspect  inspectCmd       `cmd:"" help:"Introspect a live database and emit a sqldef catalog."`
	Generate generateCmd      `cmd:"" help:"Generate typed table bindings from a package that declares a sqldef catalog (no gen.go needed)."`
	Ddl      ddlCmd           `cmd:"" help:"Render a catalog package's SQL DDL (schema.sql)."`
	Version  kong.VersionFlag `help:"Print sqlkitgen version and exit." short:"V"`
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
