package sqltest_test

import (
	"context"
	"strings"
	"testing"

	querytrace "github.com/aita/sqlkit/querytrace"
	sqlpkg "github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqltest"
)

// run executes a single statement against a freshly stubbed mock DB under a new
// trace, returning the trace so a matcher can be probed with Count/Events. cfg
// lets a test wire a richer parser when it needs structural analysis the default
// fakeParser does not produce.
func runStmt(t *testing.T, cfg querytrace.Config, isQuery bool, text string, args ...any) *querytrace.Trace {
	t.Helper()
	db, mock := sqltest.NewDB(t, sqltest.WithConfig(cfg), sqltest.Lenient())
	if isQuery {
		mock.ExpectQuery(sqltest.Anything()).Return(sqltest.NewRows("id").AddRow(1))
	} else {
		mock.ExpectExec(sqltest.Anything()).ReturnResult(0, 1)
	}
	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)
	if isQuery {
		rows, err := db.QueryContext(ctx, text, args...)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		for rows.Next() {
		}
		rows.Close()
	} else {
		if _, err := db.ExecContext(ctx, text, args...); err != nil {
			t.Fatalf("exec: %v", err)
		}
	}
	return trace
}

func TestOperationMatchers(t *testing.T) {
	cfg := debugConfig()
	cases := []struct {
		name    string
		isQuery bool
		text    string
		match   sqltest.Match
	}{
		{"update", false, "UPDATE users SET name = $1 WHERE id = $2", sqltest.Update()},
		{"delete", false, "DELETE FROM users WHERE id = $1", sqltest.Delete()},
		{"select-read", true, "SELECT id FROM users", sqltest.Read()},
		{"insert-write", false, "INSERT INTO users (name) VALUES ($1)", sqltest.Write()},
		{"update-write", false, "UPDATE users SET name = $1", sqltest.Write()},
		{"delete-write", false, "DELETE FROM users", sqltest.Write()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trace := runStmt(t, cfg, tc.isQuery, tc.text, anyArgs(tc.text)...)
			if n := sqltest.Count(trace, tc.match); n != 1 {
				t.Errorf("%s matched %d, want 1", tc.match, n)
			}
		})
	}
}

// anyArgs supplies a placeholder bind value for each $N in text so the driver
// accepts the statement.
func anyArgs(text string) []any {
	n := strings.Count(text, "$")
	out := make([]any, n)
	for i := range out {
		out[i] = i
	}
	return out
}

func TestDdlMatcher(t *testing.T) {
	// The default fakeParser cannot parse DDL; use the no-parser config so the
	// statement is still recorded and classified by its leading keyword.
	cfg := querytrace.NewConfig(querytrace.WithVerboseCapture())
	trace := runStmt(t, cfg, false, "CREATE TABLE widgets (id int)")
	if n := sqltest.Count(trace, sqltest.Ddl()); n != 1 {
		t.Errorf("Ddl matched %d, want 1", n)
	}
	if n := sqltest.Count(trace, sqltest.Read()); n != 0 {
		t.Errorf("Read matched %d on DDL, want 0", n)
	}
	if n := sqltest.Count(trace, sqltest.Write()); n != 0 {
		t.Errorf("Write matched %d on DDL, want 0", n)
	}
}

func TestFingerprintMatcher(t *testing.T) {
	cfg := debugConfig()
	trace := runStmt(t, cfg, true, "SELECT id FROM users WHERE id = $1", 1)
	evs := sqltest.Events(trace, sqltest.Select())
	if len(evs) != 1 || evs[0].Query == nil {
		t.Fatalf("want one select statement, got %d", len(evs))
	}
	fp := evs[0].Query.Fingerprint
	if fp == "" {
		t.Skip("no fingerprint produced by the parser")
	}
	if n := sqltest.Count(trace, sqltest.Fingerprint(fp)); n != 1 {
		t.Errorf("Fingerprint(%q) matched %d, want 1", fp, n)
	}
	if n := sqltest.Count(trace, sqltest.Fingerprint("nope")); n != 0 {
		t.Errorf("Fingerprint(nope) matched %d, want 0", n)
	}
}

func TestParamMatchers(t *testing.T) {
	cfg := debugConfig()
	trace := runStmt(t, cfg, true, "SELECT id FROM users WHERE id = $1 AND name = $2", 1, "a")
	if n := sqltest.Count(trace, sqltest.Params(2)); n != 1 {
		t.Errorf("Params(2) matched %d, want 1", n)
	}
	if n := sqltest.Count(trace, sqltest.Params(3)); n != 0 {
		t.Errorf("Params(3) matched %d, want 0", n)
	}
	if n := sqltest.Count(trace, sqltest.ParamsAtLeast(2)); n != 1 {
		t.Errorf("ParamsAtLeast(2) matched %d, want 1", n)
	}
	if n := sqltest.Count(trace, sqltest.ParamsAtLeast(3)); n != 0 {
		t.Errorf("ParamsAtLeast(3) matched %d, want 0", n)
	}
}

func TestSqlTextMatchers(t *testing.T) {
	cfg := debugConfig()
	const text = "SELECT id FROM users WHERE id = $1"
	trace := runStmt(t, cfg, true, text, 1)

	if n := sqltest.Count(trace, sqltest.Sql("  "+text+"  ")); n != 1 {
		t.Errorf("Sql (trimmed) matched %d, want 1", n)
	}
	if n := sqltest.Count(trace, sqltest.Sql("SELECT 1")); n != 0 {
		t.Errorf("Sql mismatch matched %d, want 0", n)
	}
	if n := sqltest.Count(trace, sqltest.SqlContains("FROM USERS")); n != 1 {
		t.Errorf("SqlContains (case-insensitive) matched %d, want 1", n)
	}
	if n := sqltest.Count(trace, sqltest.SqlContains("orders")); n != 0 {
		t.Errorf("SqlContains(orders) matched %d, want 0", n)
	}
	if n := sqltest.Count(trace, sqltest.SqlMatches(`(?i)from\s+users`)); n != 1 {
		t.Errorf("SqlMatches matched %d, want 1", n)
	}
	if n := sqltest.Count(trace, sqltest.SqlMatches(`from\s+orders`)); n != 0 {
		t.Errorf("SqlMatches mismatch matched %d, want 0", n)
	}
}

func TestNamedMatcher(t *testing.T) {
	db, mock := sqltest.NewDB(t, sqltest.WithConfig(debugConfig()), sqltest.Lenient())
	mock.ExpectQuery(sqltest.Anything()).Return(sqltest.NewRows("id").AddRow(1))

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)
	ctx = querytrace.WithQueryName(ctx, "LoadUser")

	rows, err := db.QueryContext(ctx, "SELECT id FROM users")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows.Close()

	if n := sqltest.Count(trace, sqltest.Named("LoadUser")); n != 1 {
		t.Errorf("Named(LoadUser) matched %d, want 1", n)
	}
	if n := sqltest.Count(trace, sqltest.Named("Other")); n != 0 {
		t.Errorf("Named(Other) matched %d, want 0", n)
	}
}

func TestLabelMatcher(t *testing.T) {
	db, mock := sqltest.NewDB(t, sqltest.WithConfig(debugConfig()), sqltest.Lenient())
	mock.ExpectQuery(sqltest.Anything()).Return(sqltest.NewRows("id").AddRow(1))

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)
	ctx = querytrace.WithLabel(ctx, "tenant", "acme")

	rows, err := db.QueryContext(ctx, "SELECT id FROM users")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	rows.Close()

	if n := sqltest.Count(trace, sqltest.Label("tenant", "acme")); n != 1 {
		t.Errorf("Label match = %d, want 1", n)
	}
	if n := sqltest.Count(trace, sqltest.Label("tenant", "other")); n != 0 {
		t.Errorf("Label mismatch = %d, want 0", n)
	}
}

func TestCallerMatcher(t *testing.T) {
	// WithVerboseCapture turns caller capture on, so the statement carries the
	// test function it was issued from.
	cfg := querytrace.NewConfig(querytrace.WithVerboseCapture())
	trace := runStmt(t, cfg, true, "SELECT 1")
	if n := sqltest.Count(trace, sqltest.Caller("match_test.go")); n != 1 {
		t.Errorf("Caller(match_test.go) matched %d, want 1", n)
	}
	if n := sqltest.Count(trace, sqltest.Caller("no_such_file")); n != 0 {
		t.Errorf("Caller(no_such_file) matched %d, want 0", n)
	}
}

func TestTransactionMatchers(t *testing.T) {
	db, mock := sqltest.NewDB(t, sqltest.WithConfig(debugConfig()), sqltest.Lenient())
	mock.ExpectExec(sqltest.Anything()).ReturnResult(0, 1)

	trace := querytrace.New("op")
	ctx := querytrace.WithTrace(context.Background(), trace)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO users (name) VALUES ($1)", "a"); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// Begin/Commit/Rollback match against the full event timeline, so they are
	// exercised through Expect.InOrder rather than Count (which drops tx events).
	sqltest.Expect(t, trace).InOrder(sqltest.Begin(), sqltest.Rollback())
}

func TestComposedMatchers(t *testing.T) {
	cfg := debugConfig()
	trace := runStmt(t, cfg, true, "SELECT id FROM users WHERE id = $1", 1)

	all := sqltest.All(sqltest.Select(), sqltest.Table("users"))
	if n := sqltest.Count(trace, all); n != 1 {
		t.Errorf("All(select, table users) matched %d, want 1", n)
	}
	if !strings.Contains(all.String(), "and") {
		t.Errorf("All.String() = %q, want an 'and' join", all.String())
	}

	any := sqltest.Any(sqltest.Insert(), sqltest.Select())
	if n := sqltest.Count(trace, any); n != 1 {
		t.Errorf("Any(insert, select) matched %d, want 1", n)
	}
	if n := sqltest.Count(trace, sqltest.Any(sqltest.Insert(), sqltest.Delete())); n != 0 {
		t.Errorf("Any(insert, delete) matched %d, want 0", n)
	}
	if !strings.Contains(any.String(), "or") {
		t.Errorf("Any.String() = %q, want an 'or' join", any.String())
	}

	not := sqltest.Not(sqltest.Insert())
	if n := sqltest.Count(trace, not); n != 1 {
		t.Errorf("Not(insert) matched %d, want 1", n)
	}
	if !strings.HasPrefix(not.String(), "not ") {
		t.Errorf("Not.String() = %q, want 'not ' prefix", not.String())
	}
}

// TestHasTopLevelJoinMatcher wires a parser that emits a JOIN so the analyzed
// HasTopLevelJoin flag is set, then checks the matcher both ways.
func TestHasTopLevelJoinMatcher(t *testing.T) {
	cfg := querytrace.NewConfig(querytrace.WithParser(joinParser{}), querytrace.WithVerboseCapture())
	join := runStmt(t, cfg, true, "SELECT id FROM users JOIN accounts ON users.id = accounts.user_id")
	if n := sqltest.Count(join, sqltest.HasTopLevelJoin()); n != 1 {
		t.Errorf("HasTopLevelJoin matched %d on a join, want 1", n)
	}
	plain := runStmt(t, cfg, true, "SELECT id FROM users")
	if n := sqltest.Count(plain, sqltest.HasTopLevelJoin()); n != 0 {
		t.Errorf("HasTopLevelJoin matched %d on a plain select, want 0", n)
	}
}

// joinParser renders a SELECT with a JOIN when the SQL text contains JOIN, and a
// plain SELECT otherwise, so the analyzer derives HasTopLevelJoin.
type joinParser struct{}

func (joinParser) Parse(text string) ([]sqlpkg.Query, error) {
	q := sqlpkg.Select(sqlpkg.Col("id")).From(sqlpkg.Tbl("users"))
	if strings.Contains(strings.ToUpper(text), "JOIN") {
		q = q.Join(sqlpkg.Tbl("accounts"), sqlpkg.Eq(sqlpkg.Col("users.id"), sqlpkg.Col("accounts.user_id")))
	}
	return []sqlpkg.Query{q}, nil
}
