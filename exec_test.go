package sqlkit_test

import (
	"context"
	stdsql "database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
)

// recordingExecutor is a terminal Executor that records every statement it runs
// and executes it against the given database/sql handle.
type recordingExecutor struct {
	db      *stdsql.DB
	queries []sqlkit.Statement
	execs   []sqlkit.Statement
}

func (r *recordingExecutor) Query(ctx context.Context, stmt sqlkit.Statement) (*stdsql.Rows, error) {
	r.queries = append(r.queries, stmt)
	return r.db.QueryContext(ctx, stmt.SQL, stmt.Args...)
}

func (r *recordingExecutor) Exec(ctx context.Context, stmt sqlkit.Statement) (stdsql.Result, error) {
	r.execs = append(r.execs, stmt)
	return r.db.ExecContext(ctx, stmt.SQL, stmt.Args...)
}

// TestWrapExecutorRoutesStatements verifies a WrapExecutor terminal receives both
// reads and writes in place of a database/sql handle.
func TestWrapExecutorRoutesStatements(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: func(string, []any) (sqltest.Result, error) { return usersResult(), nil }}
	rec := &recordingExecutor{db: fake.DB()}
	db := sqlkit.WrapExecutor(rec)
	ctx := context.Background()

	var users []User
	if err := db.Select().From(Users).All(ctx).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if _, err := db.Delete(Users).Where(Users.ID.Eq(1)).Exec(ctx); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	if len(rec.queries) != 1 || !strings.HasPrefix(rec.queries[0].SQL, "SELECT") {
		t.Fatalf("terminal executor queries = %+v, want one SELECT", rec.queries)
	}
	if len(rec.execs) != 1 || !strings.HasPrefix(rec.execs[0].SQL, "DELETE") {
		t.Fatalf("terminal executor execs = %+v, want one DELETE", rec.execs)
	}
}

// TestWrapExecutorRunsBeneathInterceptors verifies the WrapExecutor terminal is
// the innermost handler: interceptors wrap around it, so it sees their edits.
func TestWrapExecutorRunsBeneathInterceptors(t *testing.T) {
	fake := &sqltest.Fake{}
	rec := &recordingExecutor{db: fake.DB()}
	db := sqlkit.WrapExecutor(rec,
		sqlkit.WithInterceptor(sqlkit.Rewrite(func(_ context.Context, stmt sqlkit.Statement) (sqlkit.Statement, error) {
			stmt.SQL = "/* tagged */ " + stmt.SQL
			return stmt, nil
		})),
	)

	if _, err := db.Delete(Users).Where(Users.ID.Eq(1)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if len(rec.execs) != 1 || !strings.HasPrefix(rec.execs[0].SQL, "/* tagged */ DELETE") {
		t.Fatalf("terminal executor exec SQL = %+v, want interceptor-rewritten prefix", rec.execs)
	}
}

// TestWrapExecutorAppliesToSession verifies a session opened from a WrapExecutor
// database inherits its terminal executor.
func TestWrapExecutorAppliesToSession(t *testing.T) {
	fake := &sqltest.Fake{}
	rec := &recordingExecutor{db: fake.DB()}
	db := sqlkit.WrapExecutor(rec)
	ctx := context.Background()
	session := db.NewSession()
	defer session.Close()

	if _, err := session.Delete(Users).Where(Users.ID.Eq(1)).Exec(ctx); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if len(rec.execs) != 1 {
		t.Fatalf("terminal executor execs = %d, want 1 (session inherits WrapExecutor terminal)", len(rec.execs))
	}
}

// TestSessionRunsInItsTransaction verifies a session of a WrapDB database runs
// statements through its own transaction — nothing bypasses it to the *sql.DB.
func TestSessionRunsInItsTransaction(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	session := db.NewSession()
	defer session.Close()

	if _, err := session.Delete(Users).Where(Users.ID.Eq(1)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if fake.Begins() != 1 {
		t.Fatalf("begins = %d, want 1 (session statement ran inside its transaction)", fake.Begins())
	}
}

// TestWrapExecutorSavepointWithoutTx verifies that a session whose terminal is a
// plain Executor (no TxExecutor) rejects savepoints instead of running SAVEPOINT
// SQL outside any transaction.
func TestWrapExecutorSavepointWithoutTx(t *testing.T) {
	fake := &sqltest.Fake{}
	rec := &recordingExecutor{db: fake.DB()} // implements only Executor
	db := sqlkit.WrapExecutor(rec)
	session := db.NewSession()
	defer session.Close()

	if _, err := session.Savepoint(context.Background()); err == nil {
		t.Fatal("Savepoint() error = nil, want an error (session has no transaction)")
	}
	if len(rec.execs) != 0 {
		t.Fatalf("terminal saw %d statements, want 0 (no SAVEPOINT SQL sent)", len(rec.execs))
	}
}

// txExecutorStub is a WrapExecutor terminal that begins transactions (TxExecutor),
// recording the lifecycle so tests can assert work routed through its own Tx.
type txExecutorStub struct {
	db        *stdsql.DB
	begins    int
	commits   int
	rollbacks int
	execs     []string
}

func (e *txExecutorStub) Query(ctx context.Context, stmt sqlkit.Statement) (*stdsql.Rows, error) {
	return e.db.QueryContext(ctx, stmt.SQL, stmt.Args...)
}

func (e *txExecutorStub) Exec(ctx context.Context, stmt sqlkit.Statement) (stdsql.Result, error) {
	return e.db.ExecContext(ctx, stmt.SQL, stmt.Args...)
}

func (e *txExecutorStub) Begin(ctx context.Context, opts *stdsql.TxOptions) (sqlkit.Tx, error) {
	e.begins++
	tx, err := e.db.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &stubTx{e: e, tx: tx}, nil
}

type stubTx struct {
	e  *txExecutorStub
	tx *stdsql.Tx
}

func (t *stubTx) Query(ctx context.Context, stmt sqlkit.Statement) (*stdsql.Rows, error) {
	return t.tx.QueryContext(ctx, stmt.SQL, stmt.Args...)
}

func (t *stubTx) Exec(ctx context.Context, stmt sqlkit.Statement) (stdsql.Result, error) {
	t.e.execs = append(t.e.execs, stmt.SQL)
	return t.tx.ExecContext(ctx, stmt.SQL, stmt.Args...)
}

func (t *stubTx) Commit() error { t.e.commits++; return t.tx.Commit() }

func (t *stubTx) Rollback() error { t.e.rollbacks++; return t.tx.Rollback() }

// TestWrapExecutorTxCommit verifies a WithSession over a TxExecutor terminal runs
// its statements in the terminal's own transaction and commits through it.
func TestWrapExecutorTxCommit(t *testing.T) {
	fake := &sqltest.Fake{}
	term := &txExecutorStub{db: fake.DB()}
	db := sqlkit.WrapExecutor(term)

	err := db.WithSession(func(s *sqlkit.Session) error {
		_, err := s.Delete(Users).Where(Users.ID.Eq(1)).Exec(context.Background())
		return err
	})
	if err != nil {
		t.Fatalf("WithSession() error = %v", err)
	}
	if term.begins != 1 || term.commits != 1 || term.rollbacks != 0 {
		t.Fatalf("begins=%d commits=%d rollbacks=%d, want 1/1/0", term.begins, term.commits, term.rollbacks)
	}
}

// TestWrapExecutorTxRollback verifies a WithSession that fails rolls back through
// the TxExecutor terminal's own transaction.
func TestWrapExecutorTxRollback(t *testing.T) {
	fake := &sqltest.Fake{}
	term := &txExecutorStub{db: fake.DB()}
	db := sqlkit.WrapExecutor(term)
	sentinel := errors.New("boom")

	err := db.WithSession(func(s *sqlkit.Session) error {
		if _, err := s.Delete(Users).Where(Users.ID.Eq(1)).Exec(context.Background()); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithSession() error = %v, want %v", err, sentinel)
	}
	if term.begins != 1 || term.commits != 0 || term.rollbacks != 1 {
		t.Fatalf("begins=%d commits=%d rollbacks=%d, want 1/0/1", term.begins, term.commits, term.rollbacks)
	}
}

// TestWrapExecutorTxSavepoint verifies a savepoint runs through the TxExecutor
// terminal's own transaction.
func TestWrapExecutorTxSavepoint(t *testing.T) {
	fake := &sqltest.Fake{}
	term := &txExecutorStub{db: fake.DB()}
	db := sqlkit.WrapExecutor(term)
	session := db.NewSession()
	defer session.Close()

	if _, err := session.Savepoint(context.Background()); err != nil {
		t.Fatalf("Savepoint() error = %v", err)
	}
	if term.begins != 1 {
		t.Fatalf("begins = %d, want 1 (savepoint began the terminal's transaction)", term.begins)
	}
	found := false
	for _, sqlText := range term.execs {
		if strings.HasPrefix(sqlText, "SAVEPOINT ") {
			found = true
		}
	}
	if !found {
		t.Fatalf("savepoint did not run through the terminal's Tx; execs = %v", term.execs)
	}
}

func TestUnboundChainFailsAtExecution(t *testing.T) {
	// Package-level constructors build subqueries / ToSQL-only chains; they
	// are not bound to an executor, so executing them errors.
	var out []User
	err := sqlkit.Select().From(Users).All(context.Background()).Scan(&out)
	if err == nil || !strings.Contains(err.Error(), "not bound to an executor") {
		t.Fatalf("unbound Select Scan() = %v, want no-executor error", err)
	}

	if _, err := sqlkit.Insert(Users).Values(User{Email: "x"}).Exec(context.Background()); err == nil {
		t.Fatal("unbound Insert Exec() succeeded, want error")
	}
	if _, err := sqlkit.Update(Users).Set(Users.Email.Set("x")).Exec(context.Background()); err == nil {
		t.Fatal("unbound Update Exec() succeeded, want error")
	}
	if _, err := sqlkit.Delete(Users).Exec(context.Background()); err == nil {
		t.Fatal("unbound Delete Exec() succeeded, want error")
	}
	if _, err := sqlkit.Select().From(Users).Count(context.Background()); err == nil {
		t.Fatal("unbound Count() succeeded, want error")
	}
}
