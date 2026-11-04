package sqltest_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/sqltest"
)

const rawSchemaDDL = `
	CREATE TABLE users (id BIGSERIAL PRIMARY KEY, email TEXT NOT NULL);
	CREATE TABLE posts (id BIGSERIAL PRIMARY KEY, user_id BIGINT REFERENCES users(id));
`

func TestApplySchemaSQL(t *testing.T) {
	fake := &testutil.Fake{}
	tdb := sqltest.NewRaw(fake.DB(), sqlkit.PostgreSQLDialect{})

	tdb.ApplySchemaSQL(t, rawSchemaDDL)

	stmts := fake.Statements()
	// applySchemaSQL drops the tables the DDL declares (IF EXISTS, children
	// first) before creating them, so re-applying is idempotent and a leftover
	// table never blocks the CREATE.
	if len(stmts) != 4 {
		t.Fatalf("statements = %d (%v), want 4 (2 drops + 2 creates)", len(stmts), stmts)
	}
	if !strings.HasPrefix(stmts[0], "DROP TABLE IF EXISTS") || !strings.Contains(stmts[0], `"posts"`) {
		t.Fatalf("statements[0] = %q, want drop posts", stmts[0])
	}
	if !strings.HasPrefix(stmts[1], "DROP TABLE IF EXISTS") || !strings.Contains(stmts[1], `"users"`) {
		t.Fatalf("statements[1] = %q, want drop users", stmts[1])
	}
	if !strings.HasPrefix(stmts[2], "CREATE TABLE") || !strings.Contains(stmts[2], "users") {
		t.Fatalf("statements[2] = %q, want create users", stmts[2])
	}
	if !strings.HasPrefix(stmts[3], "CREATE TABLE") || !strings.Contains(stmts[3], "posts") {
		t.Fatalf("statements[3] = %q, want create posts", stmts[3])
	}
}

func TestSetupSchemaSQLTearsDown(t *testing.T) {
	fake := &testutil.Fake{}
	tdb := sqltest.NewRaw(fake.DB(), sqlkit.PostgreSQLDialect{})

	var afterApply int
	t.Run("inner", func(t *testing.T) {
		tdb.SetupSchemaSQL(t, rawSchemaDDL)
		afterApply = len(fake.Statements())
	})

	// When the inner subtest finished, t.Cleanup dropped the created tables.
	stmts := fake.Statements()
	if len(stmts) <= afterApply {
		t.Fatalf("no teardown statements ran: before=%d after=%d", afterApply, len(stmts))
	}
	teardown := stmts[afterApply:]
	if len(teardown) != 2 {
		t.Fatalf("teardown statements = %v, want 2 drops", teardown)
	}
	if !strings.Contains(teardown[0], `"posts"`) || !strings.Contains(teardown[1], `"users"`) {
		t.Fatalf("teardown order = %v, want posts then users", teardown)
	}
	for _, stmt := range teardown {
		if !strings.HasPrefix(stmt, "DROP TABLE IF EXISTS") {
			t.Fatalf("teardown statement = %q, want DROP TABLE IF EXISTS", stmt)
		}
	}
}

// TestApplySchemaSQLParsesNameForms exercises the CREATE TABLE name parser
// through the drops it drives: quoting, schema qualification, IF NOT EXISTS, and
// the TEMPORARY/UNLOGGED qualifiers must all yield a droppable name, and
// non-CREATE-TABLE statements must be ignored.
func TestApplySchemaSQLParsesNameForms(t *testing.T) {
	fake := &testutil.Fake{}
	tdb := sqltest.NewRaw(fake.DB(), sqlkit.PostgreSQLDialect{})

	tdb.ApplySchemaSQL(t, `
		CREATE TABLE IF NOT EXISTS "app"."accounts" (id BIGINT);
		CREATE UNLOGGED TABLE sessions (id BIGINT);
		CREATE INDEX accounts_id ON "app"."accounts" (id);
	`)

	stmts := fake.Statements()
	joined := strings.Join(stmts, "\n")
	// The two tables must be tracked (and so dropped before creation); the index
	// must not be mistaken for a table.
	if !strings.Contains(joined, `DROP TABLE IF EXISTS "app"."accounts" CASCADE`) {
		t.Fatalf("schema-qualified, quoted, IF NOT EXISTS table not dropped:\n%s", joined)
	}
	if !strings.Contains(joined, `DROP TABLE IF EXISTS "sessions" CASCADE`) {
		t.Fatalf("UNLOGGED table not dropped:\n%s", joined)
	}
	if strings.Contains(joined, `DROP TABLE IF EXISTS "accounts_id"`) {
		t.Fatalf("CREATE INDEX mistaken for a table:\n%s", joined)
	}
}

func TestTruncateTables(t *testing.T) {
	fake := &testutil.Fake{}
	tdb := sqltest.NewRaw(fake.DB(), sqlkit.PostgreSQLDialect{})
	tdb.ApplySchemaSQL(t, rawSchemaDDL)
	before := len(fake.Statements())

	// Truncate with no names clears every table the raw schema created.
	tdb.TruncateTables(t)
	stmts := fake.Statements()[before:]
	if len(stmts) != 1 {
		t.Fatalf("truncate statements = %v, want 1 CASCADE truncate", stmts)
	}
	if !strings.HasPrefix(stmts[0], "TRUNCATE TABLE") || !strings.HasSuffix(stmts[0], "CASCADE") {
		t.Fatalf("truncate statement = %q, want a single TRUNCATE ... CASCADE", stmts[0])
	}
	if !strings.Contains(stmts[0], `"users"`) || !strings.Contains(stmts[0], `"posts"`) {
		t.Fatalf("truncate statement = %q, want both tables", stmts[0])
	}
}

func TestNewRawMySQLTeardown(t *testing.T) {
	fake := &testutil.Fake{}
	tdb := sqltest.NewRaw(fake.DB(), sqlkit.MySQLDialect{})

	tdb.ApplySchemaSQL(t, "CREATE TABLE `users` (id BIGINT PRIMARY KEY);")
	tdb.TruncateTables(t)

	stmts := fake.Statements()
	joined := strings.Join(stmts, "\n")
	// MySQL toggles foreign-key checks rather than using CASCADE.
	if !strings.Contains(joined, "SET FOREIGN_KEY_CHECKS = 0") || !strings.Contains(joined, "SET FOREIGN_KEY_CHECKS = 1") {
		t.Fatalf("MySQL teardown did not toggle FOREIGN_KEY_CHECKS:\n%s", joined)
	}
	if strings.Contains(joined, "CASCADE") {
		t.Fatalf("MySQL path used CASCADE:\n%s", joined)
	}
	if !strings.Contains(joined, "TRUNCATE TABLE `users`") {
		t.Fatalf("MySQL truncate missing per-table TRUNCATE:\n%s", joined)
	}
}

// Each SetupSchemaSQL call tears down the tables it created, even when a later
// call records a different set.
func TestSetupSchemaSQLTearsDownEveryCall(t *testing.T) {
	fake := &testutil.Fake{}
	tdb := sqltest.NewRaw(fake.DB(), sqlkit.PostgreSQLDialect{})

	var afterApply int
	t.Run("inner", func(t *testing.T) {
		tdb.SetupSchemaSQL(t, "CREATE TABLE a (id int)")
		tdb.SetupSchemaSQL(t, "CREATE TABLE b (id int)")
		afterApply = len(fake.Statements())
	})
	teardown := strings.Join(fake.Statements()[afterApply:], "\n")
	for _, name := range []string{`"a"`, `"b"`} {
		if !strings.Contains(teardown, "DROP TABLE IF EXISTS "+name) {
			t.Errorf("teardown %q does not drop %s", teardown, name)
		}
	}
}

// The metadata-driven methods have nothing to act on for a NewRaw handle and
// must fail the test instead of silently doing nothing.
func TestRawHandleRejectsMetadataMethods(t *testing.T) {
	for name, call := range map[string]func(*sqltest.DB, testing.TB){
		"SetupSchema": func(d *sqltest.DB, t testing.TB) { d.SetupSchema(t) },
		"ApplySchema": func(d *sqltest.DB, t testing.TB) { d.ApplySchema(t) },
		"Truncate":    func(d *sqltest.DB, t testing.TB) { d.Truncate(t) },
	} {
		t.Run(name, func(t *testing.T) {
			fake := &testutil.Fake{}
			tdb := sqltest.NewRaw(fake.DB(), sqlkit.PostgreSQLDialect{})
			rec := &fatalRecorder{TB: t}
			func() {
				defer func() { _ = recover() }()
				call(tdb, rec)
			}()
			if !rec.failed {
				t.Fatalf("%s on a NewRaw handle did not fail the test", name)
			}
		})
	}
}

// fatalRecorder records a Fatalf instead of ending the real test, panicking to
// stop the helper the way runtime.Goexit would.
type fatalRecorder struct {
	testing.TB
	failed bool
}

func (r *fatalRecorder) Helper() {}
func (r *fatalRecorder) Fatalf(string, ...any) {
	r.failed = true
	panic("fatal")
}
