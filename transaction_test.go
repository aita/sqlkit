package sqlkit_test

import (
	"context"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
	"github.com/aita/sqlkit/sql"
)

// TestSessionSavepointWorkflow drives a savepoint through its full lifecycle —
// open, roll back to it, then release it — and verifies each transaction-control
// statement reaches the database in order, inside the session's transaction.
func TestSessionSavepointWorkflow(t *testing.T) {
	fake := &sqltest.Fake{}
	session := newSession(t, fake)
	ctx := context.Background()

	// A write begins the transaction.
	if _, err := session.Delete(Users).Where(Users.ID.Eq(1)).Exec(ctx); err != nil {
		t.Fatalf("Delete Exec() error = %v", err)
	}

	name, err := session.Savepoint(ctx)
	if err != nil {
		t.Fatalf("Savepoint() error = %v", err)
	}
	if err := session.RollbackTo(ctx, name); err != nil {
		t.Fatalf("RollbackTo() error = %v", err)
	}
	if err := session.Release(ctx, name); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	stmts := fake.Statements()
	want := []string{
		`DELETE FROM "public"."users" WHERE ("public"."users"."id" = $1)`,
		"SAVEPOINT " + name,
		"ROLLBACK TO SAVEPOINT " + name,
		"RELEASE SAVEPOINT " + name,
	}
	if len(stmts) != len(want) {
		t.Fatalf("statements = %#v, want %#v", stmts, want)
	}
	for i, w := range want {
		if stmts[i] != w {
			t.Fatalf("statement[%d] = %q, want %q", i, stmts[i], w)
		}
	}
	if fake.Begins() != 1 {
		t.Fatalf("begins = %d, want 1 (savepoints ran inside the session transaction)", fake.Begins())
	}
}

// TestSavepointControlWithoutTransactionErrors verifies that RollbackTo and
// Release reject a session whose terminal cannot transact, rather than sending
// transaction-control SQL outside any transaction.
func TestSavepointControlWithoutTransactionErrors(t *testing.T) {
	fake := &sqltest.Fake{}
	rec := &recordingExecutor{db: fake.DB()} // implements only Executor, not TxExecutor
	db := sqlkit.WrapExecutor(rec)
	session := db.NewSession()
	defer session.Close()
	ctx := context.Background()

	if err := session.RollbackTo(ctx, "sp_1"); err == nil {
		t.Fatal("RollbackTo() without a transaction should error")
	}
	if err := session.Release(ctx, "sp_1"); err == nil {
		t.Fatal("Release() without a transaction should error")
	}
	if len(rec.execs) != 0 {
		t.Fatalf("terminal saw %d statements, want 0 (no control SQL sent)", len(rec.execs))
	}
}

// TestSessionExecRunsInTransaction covers Session.Exec: an arbitrary sql.Query
// runs through the session's lazily-begun transaction.
func TestSessionExecRunsInTransaction(t *testing.T) {
	fake := &sqltest.Fake{}
	session := newSession(t, fake)

	stmt := sql.Update(sql.Table{Schema: "public", Name: "users"}).Set(sql.Set("email", sql.Val("x@x")))
	if _, err := session.Exec(context.Background(), stmt); err != nil {
		t.Fatalf("Session.Exec() error = %v", err)
	}
	if fake.Begins() != 1 {
		t.Fatalf("begins = %d, want 1", fake.Begins())
	}
	execs := fake.Execs()
	if len(execs) != 1 || execs[0].SQL != `UPDATE "public"."users" SET "email" = $1` {
		t.Fatalf("execs = %#v", execs)
	}
}

// TestSessionDialect covers Session.Dialect, which mirrors the database dialect
// the session was opened from.
func TestSessionDialect(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithDialect(sqlkit.MySQLDialect{}))
	session := db.NewSession()
	defer session.Close()
	if session.Dialect().Name() != "mysql" {
		t.Fatalf("Session.Dialect() = %q, want mysql", session.Dialect().Name())
	}
}

// TestSessionSetOperations covers the session-bound Union/UnionAll/Intersect/
// Except: each runs through the session's transaction regardless of the
// operands' own (absent) executors.
func TestSessionSetOperations(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{Columns: []string{"id"}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())
	a := sqlkit.Select(Users.ID).From(Users)
	b := sqlkit.Select(Posts.ID).From(Posts)
	ctx := context.Background()

	for name, build := range map[string]func(*sqlkit.Session) sqlkit.UnionQuery{
		"Union":     func(s *sqlkit.Session) sqlkit.UnionQuery { return s.Union(a, b) },
		"UnionAll":  func(s *sqlkit.Session) sqlkit.UnionQuery { return s.UnionAll(a, b) },
		"Intersect": func(s *sqlkit.Session) sqlkit.UnionQuery { return s.Intersect(a, b) },
		"Except":    func(s *sqlkit.Session) sqlkit.UnionQuery { return s.Except(a, b) },
	} {
		session := db.NewSession()
		var ids []int64
		if err := build(session).All(ctx).Scan(&ids); err != nil {
			t.Fatalf("session.%s().All().Scan() error = %v", name, err)
		}
		if err := session.Commit(); err != nil {
			t.Fatalf("session.%s Commit() error = %v", name, err)
		}
	}
}

// TestSessionExplicitRollback covers Rollback on a touched session and the
// double-finish guard.
func TestSessionExplicitRollback(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	session := db.NewSession()
	ctx := context.Background()

	if _, err := session.Delete(Users).Where(Users.ID.Eq(1)).Exec(ctx); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if err := session.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if fake.Begins() != 1 || fake.Commits() != 0 {
		t.Fatalf("begins=%d commits=%d, want 1/0", fake.Begins(), fake.Commits())
	}
	// The session is finished; a second Rollback reports it.
	if err := session.Rollback(); err == nil {
		t.Fatal("second Rollback() should report the session is already finished")
	}
}
