package dbtest_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/dbtest"
	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/internal/sqltest"
	"github.com/aita/sqlkit/meta"
)

// schemaModel builds a two-table catalog (users, then posts) as meta.Metadata.
// posts carries a foreign key to users and an index, so the rendered CREATE SQL
// (and the reverse-order drop that respects the FK) is representative rather
// than a bare pair of tables.
func schemaModel(t *testing.T) meta.Metadata {
	t.Helper()
	cat := decl.NewCatalog()
	public := cat.Schema("public")
	public.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("email", decl.VarChar(255), decl.Unique()),
	)
	public.Table("posts",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("user_id", decl.BigInt, decl.References("users", "id")),
		decl.Column("title", decl.VarChar(255)),
		decl.Index("user_id"),
	)
	md, err := cat.Build()
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	return md
}

func TestApplySchema(t *testing.T) {
	fake := &sqltest.Fake{}
	db := wrap(fake, sqlkit.PostgreSQLDialect{})

	dbtest.ApplySchema(t, db, schemaModel(t))

	stmts := fake.Statements()
	// Drops come first, children before parents (posts before users), then the
	// CREATE statements.
	if len(stmts) < 4 {
		t.Fatalf("statements = %d, want at least 4 (2 drops + creates)", len(stmts))
	}
	if !strings.HasPrefix(stmts[0], "DROP TABLE IF EXISTS") || !strings.Contains(stmts[0], `"posts"`) {
		t.Fatalf("first statement = %q, want drop posts", stmts[0])
	}
	if !strings.HasPrefix(stmts[1], "DROP TABLE IF EXISTS") || !strings.Contains(stmts[1], `"users"`) {
		t.Fatalf("second statement = %q, want drop users", stmts[1])
	}
	if !hasPrefixIn(stmts[2:], "CREATE TABLE") {
		t.Fatalf("statements %v, want a CREATE TABLE after the drops", stmts[2:])
	}
	// The foreign key and index in the model must reach the database, otherwise
	// the schema under test would not exercise them.
	ddl := strings.Join(stmts, "\n")
	if !strings.Contains(ddl, "REFERENCES") {
		t.Fatalf("schema DDL missing the foreign key:\n%s", ddl)
	}
	if !strings.Contains(ddl, "INDEX") {
		t.Fatalf("schema DDL missing the index:\n%s", ddl)
	}
}

func TestSetupSchemaTearsDown(t *testing.T) {
	fake := &sqltest.Fake{}
	db := wrap(fake, sqlkit.PostgreSQLDialect{})
	md := schemaModel(t)

	var afterApply int
	t.Run("inner", func(t *testing.T) {
		dbtest.SetupSchema(t, db, md)
		// At this point only the apply (drops + creates) has run; the cleanup
		// drop is registered but not yet executed.
		afterApply = len(fake.Statements())
	})

	// When the inner subtest finished, t.Cleanup dropped the tables again.
	stmts := fake.Statements()
	if len(stmts) <= afterApply {
		t.Fatalf("no teardown statements ran: before=%d after=%d", afterApply, len(stmts))
	}
	teardown := stmts[afterApply:]
	if len(teardown) != 2 {
		t.Fatalf("teardown statements = %v, want 2 drops", teardown)
	}
	for _, stmt := range teardown {
		if !strings.HasPrefix(stmt, "DROP TABLE IF EXISTS") {
			t.Fatalf("teardown statement = %q, want DROP TABLE IF EXISTS", stmt)
		}
	}
	if !strings.Contains(teardown[0], `"posts"`) || !strings.Contains(teardown[1], `"users"`) {
		t.Fatalf("teardown order = %v, want posts then users", teardown)
	}
}

func hasPrefixIn(stmts []string, prefix string) bool {
	for _, stmt := range stmts {
		if strings.HasPrefix(stmt, prefix) {
			return true
		}
	}
	return false
}
