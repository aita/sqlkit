package integration

// This file holds the test data the whole integration suite shares: the schema
// catalog applySchema materializes before every subtest, and the migration set
// the migrator tests drive. Keeping both here means every test works against
// one definition instead of borrowing the blog example's catalog or repeating
// migrations inline, so a schema change touches exactly one place.
//
// testCatalog mirrors the generated examples/generated/blogdb bindings the typed
// query tests reference (same tables, columns, keys), so the DDL this catalog
// renders matches the columns those bindings name.

import (
	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/migrate"
	"github.com/aita/sqlkit/sql"
)

// testSchema is the schema (PostgreSQL schema / MySQL database) the suite's
// tables live in. The blog bindings qualify their tables with it, and the
// migrator writes its history table into it.
const testSchema = "public"

// testCatalog is the shared schema definition. applySchema builds it into the
// meta model and executes the rendered CREATE statements for each engine.
var testCatalog = newTestCatalog()

// newTestCatalog declares the blog schema: users authored posts (has-many) and
// own at most one profile (has-one). It matches the blogdb bindings exactly so
// the same schema backs both the DDL path and the typed query path.
func newTestCatalog() *decl.Catalog {
	cat := decl.NewCatalog()
	public := cat.Schema(testSchema)

	// Registered blog authors.
	public.Table("users",
		decl.TableComment("Registered blog authors"),
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("email", decl.VarChar(255), decl.Unique(), decl.Comment("Unique login address")),
		decl.Column("name", decl.Text, decl.Nullable()),
		decl.Column("created_at", decl.Timestamp, decl.Default(sql.Raw("now()"))),
	)

	// Posts reference the user that wrote them (has-many).
	public.Table("posts",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("user_id", decl.BigInt, decl.References("users", "id")),
		decl.Column("title", decl.Text),
		decl.Column("body", decl.Text, decl.Nullable()),
		decl.Column("published", decl.Bool, decl.Default(sql.Raw("false"))),
		decl.Index("user_id"),
		decl.Check(sql.Raw("title <> ''")),
	)

	// Profiles hold one optional profile per user; the unique user_id makes it a
	// has-one relation.
	public.Table("profiles",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("user_id", decl.BigInt, decl.References("users", "id"), decl.Unique()),
		decl.Column("bio", decl.Text, decl.Nullable()),
		decl.Column("public", decl.Bool, decl.Default(sql.Raw("false"))),
	)

	return cat
}

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
