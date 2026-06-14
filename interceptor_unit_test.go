package sqlkit_test

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
	"github.com/aita/sqlkit/sql"
)

// TestOpString covers the Op.String mapping, including the unknown fallback.
func TestOpString(t *testing.T) {
	cases := map[sqlkit.Op]string{
		sqlkit.OpQuery: "query",
		sqlkit.OpExec:  "exec",
		sqlkit.Op(99):  "unknown",
	}
	for op, want := range cases {
		if got := op.String(); got != want {
			t.Errorf("Op(%d).String() = %q, want %q", int(op), got, want)
		}
	}
}

// TestStatementRecompile covers Recompile: it refreshes SQL/Args from the AST
// for the statement's dialect.
func TestStatementRecompile(t *testing.T) {
	stmt := sqlkit.Statement{
		Query:   sql.Select(sql.Raw("1")),
		Dialect: sql.PostgreSQLDialect{},
		SQL:     "stale",
	}
	if err := stmt.Recompile(); err != nil {
		t.Fatalf("Recompile() error = %v", err)
	}
	if stmt.SQL != "SELECT 1" {
		t.Fatalf("Recompile() SQL = %q, want %q", stmt.SQL, "SELECT 1")
	}
}

// TestStatementRecompileNoQuery covers the error path: a statement with no AST
// (a raw-string statement) cannot be recompiled.
func TestStatementRecompileNoQuery(t *testing.T) {
	stmt := sqlkit.Statement{Dialect: sql.PostgreSQLDialect{}}
	if err := stmt.Recompile(); err == nil {
		t.Fatal("Recompile() with nil Query should error")
	}
}

// passthrough embeds NoopInterceptor without overriding either method, so both
// InterceptQuery and InterceptExec take the pass-through path to next.
type passthrough struct{ sqlkit.NoopInterceptor }

// TestNoopInterceptorPassThrough covers NoopInterceptor's InterceptQuery and
// InterceptExec: registered as an interceptor, neither method alters the
// statement, so the read and write reach the executor unchanged.
func TestNoopInterceptorPassThrough(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{Columns: []string{"id", "email", "name"}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithInterceptor(passthrough{}))
	ctx := context.Background()

	var users []User
	if err := db.Select().From(Users).All(ctx).Scan(&users); err != nil {
		t.Fatalf("Select All().Scan() error = %v", err)
	}
	if _, err := db.Delete(Users).Where(Users.ID.Eq(1)).Exec(ctx); err != nil {
		t.Fatalf("Delete Exec() error = %v", err)
	}
	if len(fake.Execs()) != 1 {
		t.Fatalf("execs = %d, want 1", len(fake.Execs()))
	}
}
