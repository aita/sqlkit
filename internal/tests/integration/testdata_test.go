package integration

// This file holds the test data the whole integration suite shares: the schema
// catalog applySchema materializes before every subtest, and the migration set
// the migrator tests drive. Keeping both here means every test works against
// one definition instead of repeating tables or migrations inline, so a schema
// change touches exactly one place.
//
// The blog catalog itself lives in the suite's own ./schema package, and the
// blogdb bindings the typed query tests reference are generated from it, so the
// DDL this catalog renders matches the columns those bindings name.

import (
	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/internal/integration/schema"
	"github.com/aita/sqlkit/migrate"
)

// testSchema is the schema (PostgreSQL schema / MySQL database) the suite's
// tables live in. The blog bindings qualify their tables with it, and the
// migrator writes its history table into it.
const testSchema = "public"

// testCatalog is the shared schema definition: the same blog catalog the blogdb
// bindings are generated from. applySchema builds it into the meta model and
// executes the rendered CREATE statements for each engine.
var testCatalog = schema.Catalog

// testMigrationTables names the tables the migration set owns, outside the
// shared catalog. The migrator tests drop these (plus the history table) to
// isolate themselves on the shared connection.
var testMigrationTables = []string{"tags"}

// testMigrations is the shared versioned migration set: it creates a tags table
// and then adds a nullable color column, each with a matching Down. The tables
// it touches are deliberately separate from testCatalog so the migrator tests
// do not collide with the schema the query tests apply.
func testMigrations() []migrate.Migration {
	return []migrate.Migration{
		{
			Revision:     "0001",
			DownRevision: "",
			Name:         "create_tags",
			Up: func(op *migrate.Operator) {
				op.CreateTable("tags",
					decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
					decl.Column("name", decl.VarChar(64), decl.Unique()),
				)
			},
			Down: func(op *migrate.Operator) { op.DropTable("tags") },
		},
		{
			Revision:     "0002",
			DownRevision: "0001",
			Name:         "add_tags_color",
			Up: func(op *migrate.Operator) {
				op.AddColumn("tags", decl.Column("color", decl.VarChar(16), decl.Nullable()))
			},
			Down: func(op *migrate.Operator) { op.DropColumn("tags", "color") },
		},
	}
}
