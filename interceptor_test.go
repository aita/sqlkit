package sqlkit_test

import (
	"context"
	stdsql "database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
	"github.com/aita/sqlkit/sql"
)

// usersResult is a canned single-row result for the Users fixture.
func usersResult() sqltest.Result {
	return sqltest.Result{
		Columns: []string{"id", "email", "name"},
		Rows:    [][]driver.Value{{int64(7), "a@example.com", nil}},
	}
}

// TestRewriteAppliesToReadAndWrite verifies that a Rewrite interceptor's edits
// to Statement.SQL reach the driver for both queries and execs.
func TestRewriteAppliesToReadAndWrite(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: func(string, []any) (sqltest.Result, error) { return usersResult(), nil }}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithInterceptor(
		sqlkit.Rewrite(func(_ context.Context, stmt sqlkit.Statement) (sqlkit.Statement, error) {
			stmt.SQL = "/* tagged */ " + stmt.SQL
			return stmt, nil
		}),
	))
	ctx := context.Background()

	var users []User
	if err := db.Select().From(Users).All(ctx).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if got := fake.Queries()[0].SQL; !strings.HasPrefix(got, "/* tagged */ SELECT") {
		t.Fatalf("query SQL = %q, want rewritten prefix", got)
	}

	if _, err := db.Update(Users).Set(Users.Email.Set("x@example.com")).Where(Users.ID.Eq(7)).Exec(ctx); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if got := fake.Execs()[0].SQL; !strings.HasPrefix(got, "/* tagged */ UPDATE") {
		t.Fatalf("exec SQL = %q, want rewritten prefix", got)
	}
}

// TestInterceptorOrder verifies interceptors run in registration order, the
// first wrapping the rest.
func TestInterceptorOrder(t *testing.T) {
	fake := &sqltest.Fake{}
	var order []string
	record := func(name string) sqlkit.Interceptor {
		return sqlkit.Rewrite(func(_ context.Context, stmt sqlkit.Statement) (sqlkit.Statement, error) {
			order = append(order, name)
			return stmt, nil
		})
	}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithInterceptor(record("a"), record("b")))

	if _, err := db.Delete(Users).Where(Users.ID.Eq(1)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if strings.Join(order, ",") != "a,b" {
		t.Fatalf("order = %v, want [a b]", order)
	}
}

// wrapInterceptor records whether it ran before and after next, on the exec path.
type wrapInterceptor struct {
	sqlkit.NoopInterceptor
	before, after bool
}

func (w *wrapInterceptor) InterceptExec(ctx context.Context, stmt sqlkit.Statement, next sqlkit.ExecHandler) (stdsql.Result, error) {
	w.before = true
	res, err := next(ctx, stmt)
	w.after = true
	return res, err
}

// TestInterceptorWrapsExecution verifies an interceptor observes execution on
// both sides of next.
func TestInterceptorWrapsExecution(t *testing.T) {
	fake := &sqltest.Fake{}
	w := &wrapInterceptor{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithInterceptor(w))

	if _, err := db.Insert(Users).Values(User{ID: 1, Email: "a@example.com"}).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if !w.before || !w.after {
		t.Fatalf("before=%v after=%v, want both true", w.before, w.after)
	}
	if len(fake.Execs()) != 1 {
		t.Fatalf("execs = %d, want 1", len(fake.Execs()))
	}
}

// stubResult is a canned sql.Result for short-circuiting interceptors.
type stubResult struct{}

func (stubResult) LastInsertId() (int64, error) { return 0, nil }
func (stubResult) RowsAffected() (int64, error) { return 1, nil }

// shortCircuit returns a canned result without calling next.
type shortCircuit struct{ sqlkit.NoopInterceptor }

func (shortCircuit) InterceptExec(context.Context, sqlkit.Statement, sqlkit.ExecHandler) (stdsql.Result, error) {
	return stubResult{}, nil
}

// TestInterceptorShortCircuits verifies an interceptor that does not call next
// keeps the statement from reaching the driver.
func TestInterceptorShortCircuits(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithInterceptor(shortCircuit{}))

	res, err := db.Insert(Users).Values(User{ID: 1, Email: "a@example.com"}).Exec(context.Background())
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("RowsAffected = %d, want 1", n)
	}
	if len(fake.Execs()) != 0 {
		t.Fatalf("execs = %d, want 0 (short-circuited)", len(fake.Execs()))
	}
}

// TestInterceptorErrorAborts verifies an interceptor error stops execution.
func TestInterceptorErrorAborts(t *testing.T) {
	fake := &sqltest.Fake{}
	sentinel := errors.New("blocked")
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithInterceptor(
		sqlkit.Rewrite(func(context.Context, sqlkit.Statement) (sqlkit.Statement, error) {
			return sqlkit.Statement{}, sentinel
		}),
	))

	_, err := db.Insert(Users).Values(User{ID: 1, Email: "a@example.com"}).Exec(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("Exec() error = %v, want %v", err, sentinel)
	}
	if len(fake.Execs()) != 0 {
		t.Fatalf("execs = %d, want 0 (aborted)", len(fake.Execs()))
	}
}

// captureInterceptor records the last Statement seen on each path.
type captureInterceptor struct {
	sqlkit.NoopInterceptor
	query, exec *sqlkit.Statement
}

func (c *captureInterceptor) InterceptQuery(ctx context.Context, stmt sqlkit.Statement, next sqlkit.QueryHandler) (*stdsql.Rows, error) {
	s := stmt
	c.query = &s
	return next(ctx, stmt)
}

func (c *captureInterceptor) InterceptExec(ctx context.Context, stmt sqlkit.Statement, next sqlkit.ExecHandler) (stdsql.Result, error) {
	s := stmt
	c.exec = &s
	return next(ctx, stmt)
}

// TestStatementCarriesOpAndDSL verifies Op and the SQL DSL the interceptor sees:
// non-nil Query for builder statements, nil for a RawSQL string.
func TestStatementCarriesOpAndDSL(t *testing.T) {
	fake := &sqltest.Fake{QueryFunc: func(string, []any) (sqltest.Result, error) { return usersResult(), nil }}
	cap := &captureInterceptor{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithInterceptor(cap))
	ctx := context.Background()

	var users []User
	if err := db.Select().From(Users).All(ctx).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if cap.query == nil || cap.query.Op != sqlkit.OpQuery {
		t.Fatalf("query Op = %v, want OpQuery", cap.query)
	}
	if cap.query.Query == nil {
		t.Fatal("builder query Statement.Query = nil, want the SQL DSL")
	}

	if _, err := db.Delete(Users).Where(Users.ID.Eq(1)).Exec(ctx); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if cap.exec == nil || cap.exec.Op != sqlkit.OpExec {
		t.Fatalf("exec Op = %v, want OpExec", cap.exec)
	}

	cap.query = nil
	if err := db.RawSQL(ctx, "SELECT 1").Scan(&users); err != nil && !errors.Is(err, sqlkit.ErrNoRows) {
		t.Fatalf("RawSQL Scan() error = %v", err)
	}
	if cap.query == nil || cap.query.Query != nil {
		t.Fatalf("RawSQL Statement.Query = %v, want nil", cap.query)
	}
}

// TestInterceptorPropagatesToSession verifies a database's interceptors apply to
// statements run through a session it opens.
func TestInterceptorPropagatesToSession(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithInterceptor(
		sqlkit.Rewrite(func(_ context.Context, stmt sqlkit.Statement) (sqlkit.Statement, error) {
			stmt.SQL = "/* sess */ " + stmt.SQL
			return stmt, nil
		}),
	))
	ctx := context.Background()
	session := db.NewSession()
	defer session.Close()

	if _, err := session.Delete(Users).Where(Users.ID.Eq(1)).Exec(ctx); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if got := fake.Execs()[0].SQL; !strings.HasPrefix(got, "/* sess */ DELETE") {
		t.Fatalf("session exec SQL = %q, want rewritten prefix", got)
	}
}

// TestSavepointBypassesInterceptors verifies transaction-control statements are
// not seen by interceptors.
func TestSavepointBypassesInterceptors(t *testing.T) {
	fake := &sqltest.Fake{}
	cap := &captureInterceptor{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithInterceptor(cap))
	ctx := context.Background()
	session := db.NewSession()
	defer session.Close()

	if _, err := session.Savepoint(ctx); err != nil {
		t.Fatalf("Savepoint() error = %v", err)
	}
	if cap.exec != nil {
		t.Fatalf("interceptor saw a savepoint statement: %q", cap.exec.SQL)
	}
}

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
