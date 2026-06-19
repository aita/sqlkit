package sqlkit_test

import (
	"context"
	stdsql "database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/sql"
)

// usersResult is a canned single-row result for the Users fixture.
func usersResult() testutil.Result {
	return testutil.Result{
		Columns: []string{"id", "email", "name"},
		Rows:    [][]driver.Value{{int64(7), "a@example.com", nil}},
	}
}

// TestBeforeAppliesToReadAndWrite verifies that a Before hook's edits to
// Statement.SQL reach the driver for both queries and execs.
func TestBeforeAppliesToReadAndWrite(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: func(string, []any) (testutil.Result, error) { return usersResult(), nil }}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithHooks(sqlkit.Hooks{
		Before: func(ctx context.Context, stmt *sqlkit.Statement) (context.Context, error) {
			stmt.SQL = "/* tagged */ " + stmt.SQL
			return ctx, nil
		},
	}))
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

// TestHooksOrder verifies hooks run in registration order, threading the context
// each Before returns into the next.
func TestHooksOrder(t *testing.T) {
	fake := &testutil.Fake{}
	var order []string
	record := func(name string) sqlkit.Hooks {
		return sqlkit.Hooks{Before: func(ctx context.Context, stmt *sqlkit.Statement) (context.Context, error) {
			order = append(order, name)
			return ctx, nil
		}}
	}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithHooks(record("a"), record("b")))

	if _, err := db.Delete(Users).Where(Users.ID.Eq(1)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if strings.Join(order, ",") != "a,b" {
		t.Fatalf("order = %v, want [a b]", order)
	}
}

// TestHooksBracketExecution verifies a Before/AfterExec pair brackets a write:
// Before runs first, AfterExec runs after the result is produced, and the
// context Before returns flows through to AfterExec.
func TestHooksBracketExecution(t *testing.T) {
	fake := &testutil.Fake{}
	type ctxKey struct{}
	var before, after bool
	var sawCtxValue bool
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithHooks(sqlkit.Hooks{
		Before: func(ctx context.Context, stmt *sqlkit.Statement) (context.Context, error) {
			before = true
			return context.WithValue(ctx, ctxKey{}, "v"), nil
		},
		AfterExec: func(ctx context.Context, stmt sqlkit.Statement, res stdsql.Result, err error) error {
			after = true
			sawCtxValue = ctx.Value(ctxKey{}) == "v"
			return err
		},
	}))

	if _, err := db.Insert(Users).Values(User{ID: 1, Email: "a@example.com"}).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if !before || !after {
		t.Fatalf("before=%v after=%v, want both true", before, after)
	}
	if !sawCtxValue {
		t.Fatal("AfterExec did not see the context value Before set")
	}
	if len(fake.Execs()) != 1 {
		t.Fatalf("execs = %d, want 1", len(fake.Execs()))
	}
}

// TestAfterExecSeesResult verifies AfterExec receives the sql.Result the write
// produced.
func TestAfterExecSeesResult(t *testing.T) {
	fake := &testutil.Fake{}
	var rows int64 = -1
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithHooks(sqlkit.Hooks{
		AfterExec: func(_ context.Context, _ sqlkit.Statement, res stdsql.Result, err error) error {
			if err == nil && res != nil {
				rows, _ = res.RowsAffected()
			}
			return err
		},
	}))

	if _, err := db.Delete(Users).Where(Users.ID.Eq(7)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if rows < 0 {
		t.Fatalf("AfterExec did not observe a result (RowsAffected = %d)", rows)
	}
}

// TestBeforeErrorAborts verifies a Before hook error stops execution.
func TestBeforeErrorAborts(t *testing.T) {
	fake := &testutil.Fake{}
	sentinel := errors.New("blocked")
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithHooks(sqlkit.Hooks{
		Before: func(ctx context.Context, _ *sqlkit.Statement) (context.Context, error) {
			return ctx, sentinel
		},
	}))

	_, err := db.Insert(Users).Values(User{ID: 1, Email: "a@example.com"}).Exec(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("Exec() error = %v, want %v", err, sentinel)
	}
	if len(fake.Execs()) != 0 {
		t.Fatalf("execs = %d, want 0 (aborted)", len(fake.Execs()))
	}
}

// TestAfterQueryErrorFailsCall verifies an AfterQuery hook can fail an otherwise
// successful read by returning an error.
func TestAfterQueryErrorFailsCall(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: func(string, []any) (testutil.Result, error) { return usersResult(), nil }}
	sentinel := errors.New("rejected")
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithHooks(sqlkit.Hooks{
		AfterQuery: func(context.Context, sqlkit.Statement, any, error) error { return sentinel },
	}))

	var users []User
	err := db.Select().From(Users).All(context.Background()).Scan(&users)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Scan() error = %v, want %v", err, sentinel)
	}
}

// captureHook records, via a Before hook, the last Statement seen on each path.
type captureHook struct {
	query, exec *sqlkit.Statement
}

func (c *captureHook) hooks() sqlkit.Hooks {
	return sqlkit.Hooks{
		Before: func(ctx context.Context, stmt *sqlkit.Statement) (context.Context, error) {
			s := *stmt
			if stmt.Op == sqlkit.OpQuery {
				c.query = &s
			} else {
				c.exec = &s
			}
			return ctx, nil
		},
	}
}

// TestStatementCarriesOpAndDSL verifies Op and the SQL DSL a hook sees: non-nil
// Query for builder statements, nil for a RawSQL string.
func TestStatementCarriesOpAndDSL(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: func(string, []any) (testutil.Result, error) { return usersResult(), nil }}
	cap := &captureHook{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithHooks(cap.hooks()))
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

// TestHooksPropagateToSession verifies a database's hooks apply to statements
// run through a session it opens.
func TestHooksPropagateToSession(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithHooks(sqlkit.Hooks{
		Before: func(ctx context.Context, stmt *sqlkit.Statement) (context.Context, error) {
			stmt.SQL = "/* sess */ " + stmt.SQL
			return ctx, nil
		},
	}))
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

// TestSavepointBypassesHooks verifies transaction-control statements are not
// seen by hooks.
func TestSavepointBypassesHooks(t *testing.T) {
	fake := &testutil.Fake{}
	cap := &captureHook{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithHooks(cap.hooks()))
	ctx := context.Background()
	session := db.NewSession()
	defer session.Close()

	if _, err := session.Savepoint(ctx); err != nil {
		t.Fatalf("Savepoint() error = %v", err)
	}
	if cap.exec != nil {
		t.Fatalf("hook saw a savepoint statement: %q", cap.exec.SQL)
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

// TestEmptyHooksPassThrough covers a zero Hooks value (all fields nil)
// registered as a hook: neither the read nor the write is altered, and both
// reach the executor unchanged.
func TestEmptyHooksPassThrough(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{Columns: []string{"id", "email", "name"}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithHooks(sqlkit.Hooks{}))
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

// captureResults records, via an AfterQuery hook, every successful result it
// sees as the destination given to Scan.
type captureResults struct {
	stmts []sqlkit.Statement
	dests []any
}

func (c *captureResults) hooks() sqlkit.Hooks {
	return sqlkit.Hooks{
		AfterQuery: func(_ context.Context, stmt sqlkit.Statement, dest any, err error) error {
			if err != nil {
				return err
			}
			c.stmts = append(c.stmts, stmt)
			c.dests = append(c.dests, dest)
			return nil
		},
	}
}

// twoUsersResult is a canned two-row result for the Users fixture.
func twoUsersResult() testutil.Result {
	return testutil.Result{
		Columns: []string{"id", "email", "name"},
		Rows: [][]driver.Value{
			{int64(7), "a@example.com", nil},
			{int64(8), "b@example.com", nil},
		},
	}
}

// TestAfterQuerySeesScannedSlice verifies AfterQuery receives the whole
// materialized slice once, with the statement that produced it.
func TestAfterQuerySeesScannedSlice(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: func(string, []any) (testutil.Result, error) { return twoUsersResult(), nil }}
	obs := &captureResults{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithHooks(obs.hooks()))

	var users []User
	if err := db.Select().From(Users).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(obs.dests) != 1 {
		t.Fatalf("observed %d results, want 1", len(obs.dests))
	}
	got, ok := obs.dests[0].(*[]User)
	if !ok || len(*got) != 2 || (*got)[0].ID != 7 || (*got)[1].ID != 8 {
		t.Fatalf("observed dest = %+v, want the two scanned users", obs.dests[0])
	}
	if obs.stmts[0].SQL == "" || obs.stmts[0].Op != sqlkit.OpQuery {
		t.Fatalf("observed statement = %+v, want the SELECT it scanned", obs.stmts[0])
	}
}

// TestAfterQuerySingleRow verifies a single-row destination yields one
// observation carrying that struct.
func TestAfterQuerySingleRow(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: func(string, []any) (testutil.Result, error) { return usersResult(), nil }}
	obs := &captureResults{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithHooks(obs.hooks()))

	var user User
	if err := db.Select().From(Users).Where(Users.ID.Eq(7)).One(context.Background()).Scan(&user); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(obs.dests) != 1 {
		t.Fatalf("observed %d results, want 1", len(obs.dests))
	}
	if got, ok := obs.dests[0].(*User); !ok || got.ID != 7 {
		t.Fatalf("observed dest = %+v, want the one scanned user", obs.dests[0])
	}
}

// TestAfterQueryInheritedBySession verifies a session runs the database's
// AfterQuery hook over results scanned through its transaction.
func TestAfterQueryInheritedBySession(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: func(string, []any) (testutil.Result, error) { return usersResult(), nil }}
	obs := &captureResults{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithHooks(obs.hooks()))
	session := db.NewSession()
	defer session.Close()

	var users []User
	if err := session.Select().From(Users).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(obs.dests) != 1 {
		t.Fatalf("session observed %d results, want the database's hook to fire once", len(obs.dests))
	}
}

// TestAfterQueryNotRunForWrites verifies AfterQuery is a read-side hook: a write
// fires AfterExec, never AfterQuery.
func TestAfterQueryNotRunForWrites(t *testing.T) {
	fake := &testutil.Fake{}
	obs := &captureResults{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithHooks(obs.hooks()))

	if _, err := db.Delete(Users).Where(Users.ID.Eq(7)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if len(obs.dests) != 0 {
		t.Fatalf("AfterQuery observed %d results for a write, want 0", len(obs.dests))
	}
}
