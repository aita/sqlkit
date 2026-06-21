package postgres_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestCreateIndex(t *testing.T) {
	assertRoundTrip(t,
		"CREATE INDEX idx_users_email ON users (email)",
		sql.CreateIndex("idx_users_email", sql.Table{Name: sql.NewIdent("users")}, "email"),
		`CREATE INDEX "idx_users_email" ON "users" ("email");`)
}

func TestCreateUniqueIndexIfNotExists(t *testing.T) {
	assertRoundTrip(t,
		"CREATE UNIQUE INDEX IF NOT EXISTS uq ON t (a, b)",
		sql.CreateIndex("uq", sql.Table{Name: sql.NewIdent("t")}, "a", "b").Unique().IfNotExists(),
		`CREATE UNIQUE INDEX IF NOT EXISTS "uq" ON "t" ("a", "b");`)
}

// An expression index is not modeled, so it round-trips raw.
func TestExpressionIndexFallsBackToRaw(t *testing.T) {
	assertRaw(t, "CREATE INDEX lower_email ON users (lower(email))")
}

func TestDropIndexIfExists(t *testing.T) {
	assertRoundTrip(t,
		"DROP INDEX IF EXISTS idx_users_email",
		sql.DropIndex("idx_users_email", sql.Table{}),
		`DROP INDEX IF EXISTS "idx_users_email";`)
}

func TestCreateView(t *testing.T) {
	assertRoundTrip(t,
		"CREATE VIEW active_users AS SELECT id, name FROM users WHERE active",
		sql.CreateView(sql.Table{Name: sql.NewIdent("active_users")},
			sql.Select(sql.Col("id"), sql.Col("name")).From(sql.Table{Name: sql.NewIdent("users")}).Where(sql.Col("active"))),
		"CREATE VIEW \"active_users\" AS\nSELECT \"id\", \"name\" FROM \"users\" WHERE \"active\";")
}

func TestCreateOrReplaceViewColumns(t *testing.T) {
	assertRoundTrip(t,
		"CREATE OR REPLACE VIEW v (a, b) AS SELECT x, y FROM t",
		sql.CreateView(sql.Table{Name: sql.NewIdent("v")},
			sql.Select(sql.Col("x"), sql.Col("y")).From(sql.Table{Name: sql.NewIdent("t")})).OrReplace().Columns("a", "b"),
		"CREATE OR REPLACE VIEW \"v\" (\"a\", \"b\") AS\nSELECT \"x\", \"y\" FROM \"t\";")
}

// A view whose body has a literal cannot keep a static body, so it round-trips
// raw rather than parameterizing the constant.
func TestViewWithLiteralFallsBackToRaw(t *testing.T) {
	assertRaw(t, "CREATE VIEW vips AS SELECT id FROM users WHERE tier = 'gold'")
}

func TestDropViewIfExistsCascade(t *testing.T) {
	assertRoundTrip(t,
		"DROP VIEW IF EXISTS reporting.summary CASCADE",
		sql.DropView(sql.Table{Schema: sql.NewIdent("reporting"), Name: sql.NewIdent("summary")}).IfExists().Cascade(),
		`DROP VIEW IF EXISTS "reporting"."summary" CASCADE;`)
}

func TestCommentOnTable(t *testing.T) {
	assertRoundTrip(t,
		"COMMENT ON TABLE users IS 'application users'",
		sql.CommentOnTable(sql.Table{Name: sql.NewIdent("users")}, "application users"),
		`COMMENT ON TABLE "users" IS 'application users';`)
}

func TestCommentOnColumn(t *testing.T) {
	assertRoundTrip(t,
		"COMMENT ON COLUMN users.email IS 'contact address'",
		sql.CommentOnColumn(sql.Table{Name: sql.NewIdent("users")}, "email", "contact address"),
		`COMMENT ON COLUMN "users"."email" IS 'contact address';`)
}

// This sequence exercises the option branches the existing tests do not:
// NO MINVALUE, an explicit MAXVALUE, NO CYCLE, and OWNED BY NONE.
func TestCreateSequenceComplementaryOptions(t *testing.T) {
	assertRoundTrip(t,
		"CREATE SEQUENCE s NO MINVALUE MAXVALUE 100 NO CYCLE OWNED BY NONE",
		sql.CreateSequence(sql.Table{Name: sql.NewIdent("s")}).
			NoMinValue().MaxValue(100).NoCycle().OwnedBy("NONE"),
		`CREATE SEQUENCE "s" NO MINVALUE MAXVALUE 100 NO CYCLE OWNED BY NONE;`)
}

// A COMMENT ON COLUMN whose object name carries a schema (schema.table.column)
// exercises the qualified branch of tableColumnFromNameList.
func TestCommentOnQualifiedColumn(t *testing.T) {
	assertRoundTrip(t,
		"COMMENT ON COLUMN s.t.c IS 'hi'",
		sql.CommentOnColumn(sql.Table{Schema: sql.NewIdent("s"), Name: sql.NewIdent("t")}, "c", "hi"),
		`COMMENT ON COLUMN "s"."t"."c" IS 'hi';`)
}
