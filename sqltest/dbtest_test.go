package sqltest_test

import (
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/sqltest"
)

// userRow / postRow are caller-defined DTOs, mirroring how tests bring their own
// row types against the generated table bindings.
type userRow struct {
	ID    int64
	Email string
	Name  *string
}

type postRow struct {
	ID     int64
	UserID int64
	Title  string
}

// Minimal hand-written bindings, the same shape sqlkit generates, so the tests
// do not depend on the example packages.
type usersTable struct {
	info  sqlkit.TableInfo
	ID    sqlkit.Column[int64]
	Email sqlkit.Column[string]
	Name  sqlkit.Column[*string]
}

func newUsers() usersTable {
	info := sqlkit.TableInfo{Schema: "public", Name: "users"}
	return usersTable{
		info:  info,
		ID:    sqlkit.NewColumn[int64](info, "id"),
		Email: sqlkit.NewColumn[string](info, "email"),
		Name:  sqlkit.NewColumn[*string](info, "name"),
	}
}

func (t usersTable) Info() sqlkit.TableInfo             { return t.info }
func (t usersTable) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t usersTable) Columns() []sqlkit.AnyColumn        { return []sqlkit.AnyColumn{t.ID, t.Email, t.Name} }
func (t usersTable) PrimaryKey() []sqlkit.AnyColumn     { return []sqlkit.AnyColumn{t.ID} }
func (t usersTable) Defaulted() []sqlkit.AnyColumn      { return []sqlkit.AnyColumn{t.ID} }

type postsTable struct {
	info   sqlkit.TableInfo
	ID     sqlkit.Column[int64]
	UserID sqlkit.Column[int64]
	Title  sqlkit.Column[string]
}

func newPosts() postsTable {
	info := sqlkit.TableInfo{Schema: "public", Name: "posts"}
	return postsTable{
		info:   info,
		ID:     sqlkit.NewColumn[int64](info, "id"),
		UserID: sqlkit.NewColumn[int64](info, "user_id"),
		Title:  sqlkit.NewColumn[string](info, "title"),
	}
}

func (t postsTable) Info() sqlkit.TableInfo             { return t.info }
func (t postsTable) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t postsTable) Columns() []sqlkit.AnyColumn {
	return []sqlkit.AnyColumn{t.ID, t.UserID, t.Title}
}
func (t postsTable) PrimaryKey() []sqlkit.AnyColumn { return []sqlkit.AnyColumn{t.ID} }
func (t postsTable) Defaulted() []sqlkit.AnyColumn  { return []sqlkit.AnyColumn{t.ID} }

func wrap(fake *testutil.Fake, dialect sqlkit.Dialect) *sqlkit.Database {
	return sqlkit.WrapDB(fake.DB(), sqlkit.WithDialect(dialect))
}

func TestInsertIDReturning(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(42)}}}, nil
		},
	}
	db := wrap(fake, sqlkit.PostgreSQLDialect{})

	id := sqltest.InsertID(t, db, newUsers(), userRow{Email: "alice@example.com"})
	if id != 42 {
		t.Fatalf("InsertID = %d, want 42", id)
	}
	// PostgreSQL reads the key back via a RETURNING query, not an exec.
	queries := fake.Queries()
	if len(queries) != 1 {
		t.Fatalf("queries = %d, want 1", len(queries))
	}
	if !strings.Contains(queries[0].SQL, "RETURNING") || !strings.Contains(queries[0].SQL, `"id"`) {
		t.Fatalf("query = %q, want RETURNING \"id\"", queries[0].SQL)
	}
	if len(fake.Execs()) != 0 {
		t.Fatalf("execs = %d, want 0 (RETURNING path)", len(fake.Execs()))
	}
}

func TestInsertIDLastInsertID(t *testing.T) {
	fake := &testutil.Fake{LastInsertID: 7}
	db := wrap(fake, sqlkit.MySQLDialect{})

	id := sqltest.InsertID(t, db, newUsers(), userRow{Email: "bob@example.com"})
	if id != 7 {
		t.Fatalf("InsertID = %d, want 7 (LastInsertId)", id)
	}
	// MySQL has no RETURNING: a plain exec, then LastInsertId.
	execs := fake.Execs()
	if len(execs) != 1 {
		t.Fatalf("execs = %d, want 1", len(execs))
	}
	if strings.Contains(execs[0].SQL, "RETURNING") {
		t.Fatalf("exec = %q, must not contain RETURNING on MySQL", execs[0].SQL)
	}
	if len(fake.Queries()) != 0 {
		t.Fatalf("queries = %d, want 0 (LastInsertId path)", len(fake.Queries()))
	}
}

func TestInsertBulk(t *testing.T) {
	fake := &testutil.Fake{}
	db := wrap(fake, sqlkit.PostgreSQLDialect{})

	sqltest.InsertRows(t, db, newUsers(),
		userRow{Email: "alice@example.com"},
		userRow{Email: "bob@example.com"},
	)
	execs := fake.Execs()
	if len(execs) != 1 {
		t.Fatalf("execs = %d, want 1", len(execs))
	}
	// Two rows, two columns each (email, name) → four bind args.
	if len(execs[0].Args) != 4 {
		t.Fatalf("args = %d, want 4", len(execs[0].Args))
	}
}

func TestInsertReturning(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{
				Columns: []string{"id", "email", "name"},
				Rows: [][]driver.Value{
					{int64(1), "alice@example.com", nil},
					{int64(2), "bob@example.com", nil},
				},
			}, nil
		},
	}
	db := wrap(fake, sqlkit.PostgreSQLDialect{})

	got := sqltest.InsertReturning(t, db, newUsers(),
		userRow{Email: "alice@example.com"},
		userRow{Email: "bob@example.com"},
	)
	if len(got) != 2 {
		t.Fatalf("returned rows = %d, want 2", len(got))
	}
	if got[0].ID != 1 || got[1].ID != 2 {
		t.Fatalf("returned ids = %d,%d, want 1,2", got[0].ID, got[1].ID)
	}
	queries := fake.Queries()
	if len(queries) != 1 || !strings.Contains(queries[0].SQL, "RETURNING") {
		t.Fatalf("query = %v, want a single RETURNING query", queries)
	}
}

// TestInsertReturningSkipsOnMySQL covers the RETURNING-unsupported skip branch.
// It runs in an inner subtest so the Skip does not skip the parent.
func TestInsertReturningSkipsOnMySQL(t *testing.T) {
	fake := &testutil.Fake{}
	db := wrap(fake, sqlkit.MySQLDialect{})

	skipped := true
	t.Run("inner", func(t *testing.T) {
		sqltest.InsertReturning(t, db, newUsers(), userRow{Email: "a@example.com"})
		skipped = false // unreachable when Skipf fires
	})
	if !skipped {
		t.Fatalf("InsertReturning did not skip on a dialect without RETURNING")
	}
	if len(fake.Statements()) != 0 {
		t.Fatalf("statements = %v, want none on the skip path", fake.Statements())
	}
}

// TestInsertReturningEmpty returns nil without touching the database when given
// no rows.
func TestInsertReturningEmpty(t *testing.T) {
	fake := &testutil.Fake{}
	db := wrap(fake, sqlkit.PostgreSQLDialect{})

	if got := sqltest.InsertReturning[userRow](t, db, newUsers()); got != nil {
		t.Fatalf("InsertReturning with no rows = %v, want nil", got)
	}
	if len(fake.Statements()) != 0 {
		t.Fatalf("statements = %v, want none for an empty insert", fake.Statements())
	}
}

// TestInsertRowsEmpty is a no-op that issues no statement.
func TestInsertRowsEmpty(t *testing.T) {
	fake := &testutil.Fake{}
	db := wrap(fake, sqlkit.PostgreSQLDialect{})
	sqltest.InsertRows(t, db, newUsers())
	if len(fake.Statements()) != 0 {
		t.Fatalf("statements = %v, want none for an empty InsertRows", fake.Statements())
	}
}

func TestFixtureLoadOrder(t *testing.T) {
	fake := &testutil.Fake{}
	db := wrap(fake, sqlkit.PostgreSQLDialect{})

	users, posts := newUsers(), newPosts()
	sqltest.NewFixture("blog").
		Add(users, userRow{Email: "alice@example.com"}).
		Add(posts, postRow{UserID: 1, Title: "Hello"}, postRow{UserID: 1, Title: "World"}).
		Load(t, db)

	stmts := fake.Statements()
	if len(stmts) != 2 {
		t.Fatalf("statements = %d, want 2", len(stmts))
	}
	if !strings.Contains(stmts[0], `"users"`) {
		t.Fatalf("first statement = %q, want users insert", stmts[0])
	}
	if !strings.Contains(stmts[1], `"posts"`) {
		t.Fatalf("second statement = %q, want posts insert", stmts[1])
	}
}
