package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
)

// The fixtures below mirror what sqlkit generates: a typed table binding
// with a Get accessor. All statements start from the db or session handle
// (db.Select().From(t), db.Insert(t).Values(...), db.Update(t), ...).

type User struct {
	ID    int64
	Email string
	Name  *string
}

type UsersTable struct {
	info  sqlkit.TableInfo
	ID    sqlkit.Column[int64]
	Email sqlkit.Column[string]
	Name  sqlkit.Column[*string]
}

var Users = func() UsersTable {
	info := sqlkit.TableInfo{Schema: "public", Name: "users"}
	return UsersTable{
		info:  info,
		ID:    sqlkit.NewColumn[int64](info, "id"),
		Email: sqlkit.NewColumn[string](info, "email"),
		Name:  sqlkit.NewColumn[*string](info, "name"),
	}
}()

func (t UsersTable) Get(ctx context.Context, ex sqlkit.Querier, id int64, dest any) (bool, error) {
	err := ex.Select().From(t).Where(t.ID.Eq(id)).Limit(1).All(ctx).Scan(dest)
	if err != nil {
		if errors.Is(err, sqlkit.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (t UsersTable) Info() sqlkit.TableInfo             { return t.info }
func (t UsersTable) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t UsersTable) Columns() []sqlkit.AnyColumn {
	return []sqlkit.AnyColumn{t.ID, t.Email, t.Name}
}
func (t UsersTable) PrimaryKey() []sqlkit.AnyColumn { return []sqlkit.AnyColumn{t.ID} }
func (t UsersTable) Defaulted() []sqlkit.AnyColumn  { return []sqlkit.AnyColumn{t.ID} }
func (t UsersTable) As(alias string) UsersTable {
	info := t.info.WithAlias(alias)
	return UsersTable{
		info:  info,
		ID:    sqlkit.NewColumn[int64](info, "id"),
		Email: sqlkit.NewColumn[string](info, "email"),
		Name:  sqlkit.NewColumn[*string](info, "name"),
	}
}

type Post struct {
	ID     int64
	UserID int64
	Title  string
}

type PostsTable struct {
	info   sqlkit.TableInfo
	ID     sqlkit.Column[int64]
	UserID sqlkit.Column[int64]
	Title  sqlkit.Column[string]
}

var Posts = func() PostsTable {
	info := sqlkit.TableInfo{Schema: "public", Name: "posts"}
	return PostsTable{
		info:   info,
		ID:     sqlkit.NewColumn[int64](info, "id"),
		UserID: sqlkit.NewColumn[int64](info, "user_id"),
		Title:  sqlkit.NewColumn[string](info, "title"),
	}
}()

func (t PostsTable) Info() sqlkit.TableInfo             { return t.info }
func (t PostsTable) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t PostsTable) Columns() []sqlkit.AnyColumn {
	return []sqlkit.AnyColumn{t.ID, t.UserID, t.Title}
}
func (t PostsTable) PrimaryKey() []sqlkit.AnyColumn { return []sqlkit.AnyColumn{t.ID} }
func (t PostsTable) Defaulted() []sqlkit.AnyColumn  { return []sqlkit.AnyColumn{t.ID} }

func newSession(t *testing.T, fake *sqltest.Fake) *sqlkit.Session {
	t.Helper()
	db := sqlkit.WrapDB(fake.DB())
	session := db.NewSession()
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestSessionRunsStatementsInTransactionAndCommits(t *testing.T) {
	fake := &sqltest.Fake{}
	session := newSession(t, fake)
	ctx := context.Background()

	if _, err := session.Update(Users).
		Set(Users.Email.Set("new@example.com")).
		Where(Users.ID.Eq(7)).
		Exec(ctx); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if err := session.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if fake.Commits() != 1 {
		t.Fatalf("commits = %d, want 1", fake.Commits())
	}
	execs := fake.Execs()
	if len(execs) != 1 || !strings.HasPrefix(execs[0].SQL, "UPDATE") {
		t.Fatalf("execs = %#v, want one UPDATE", execs)
	}
}

func TestSessionCloseRollsBackUncommitted(t *testing.T) {
	fake := &sqltest.Fake{}
	session := newSession(t, fake)
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if fake.Commits() != 0 {
		t.Fatalf("commits = %d, want 0", fake.Commits())
	}
	if err := session.Commit(); err == nil {
		t.Fatal("Commit() after Close() succeeded, want error")
	}
}

func TestGetByPrimaryKey(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(sql string, args []any) (sqltest.Result, error) {
			return sqltest.Result{
				Columns: []string{"id", "email", "name"},
				Rows:    [][]driver.Value{{int64(7), "a@example.com", nil}},
			}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())
	ctx := context.Background()

	var user User
	found, err := Users.Get(ctx, db, 7, &user)
	if err != nil || !found {
		t.Fatalf("Get() found=%v err=%v", found, err)
	}
	if user.ID != 7 || user.Email != "a@example.com" || user.Name != nil {
		t.Fatalf("Get() = %#v", user)
	}

	want := `SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name" FROM "public"."users" WHERE ("public"."users"."id" = $1) LIMIT 1`
	if got := fake.Queries()[0].SQL; got != want {
		t.Fatalf("Get() SQL = %q,\nwant %q", got, want)
	}
}

func TestGetReportsNotFound(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	var user User
	found, err := Users.Get(context.Background(), db, 404, &user)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if found {
		t.Fatalf("Get() found = true with %#v, want false", user)
	}
}

func TestInsertReturningScansIntoSlice(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(sql string, args []any) (sqltest.Result, error) {
			return sqltest.Result{
				Columns: []string{"id", "email", "name"},
				Rows:    [][]driver.Value{{int64(101), "alice@example.com", nil}},
			}, nil
		},
	}
	session := newSession(t, fake)

	var inserted []User
	err := session.Insert(Users).Values(User{Email: "alice@example.com"}).
		Returning().
		All(context.Background()).Scan(&inserted)
	if err != nil {
		t.Fatalf("Insert().Values().All().Scan() error = %v", err)
	}
	if len(inserted) != 1 || inserted[0].ID != 101 {
		t.Fatalf("inserted = %#v, want database-generated id 101", inserted)
	}

	want := `INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2) RETURNING "public"."users"."id", "public"."users"."email", "public"."users"."name"`
	if got := fake.Queries()[0].SQL; got != want {
		t.Fatalf("INSERT SQL = %q,\nwant %q", got, want)
	}
}

func TestSessionBeginsLazily(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{Columns: []string{"id", "email", "name"}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())
	session := db.NewSession()

	// Creating the session opens no transaction.
	if fake.Begins() != 0 {
		t.Fatalf("begins after NewSession = %d, want 0", fake.Begins())
	}

	// The first statement begins it.
	var users []User
	if err := session.Select().From(Users).All(context.Background()).Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if fake.Begins() != 1 {
		t.Fatalf("begins after first statement = %d, want 1", fake.Begins())
	}

	// Further statements reuse it.
	if _, err := session.Update(Users).Set(Users.Email.Set("x")).Where(Users.ID.Eq(1)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if fake.Begins() != 1 {
		t.Fatalf("begins after second statement = %d, want 1", fake.Begins())
	}
	if err := session.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if fake.Commits() != 1 {
		t.Fatalf("commits = %d, want 1", fake.Commits())
	}
}

func TestUntouchedSessionCommitAndCloseAreNoOps(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	// Commit on an untouched session: nothing begun, nothing committed.
	session := db.NewSession()
	if err := session.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	// Close on an untouched session.
	other := db.NewSession()
	if err := other.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if fake.Begins() != 0 || fake.Commits() != 0 {
		t.Fatalf("begins=%d commits=%d, want 0/0", fake.Begins(), fake.Commits())
	}
}

func TestClosedSessionRejectsStatements(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	session := db.NewSession()
	if err := session.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	var users []User
	err := session.Select().From(Users).All(context.Background()).Scan(&users)
	if err == nil {
		t.Fatal("statement on closed session succeeded, want error")
	}
	if n := len(fake.Queries()); n != 0 {
		t.Fatalf("closed session reached the database: %d queries", n)
	}
}

func TestWithSessionCommitsOnSuccess(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	ctx := context.Background()

	err := db.WithSession(func(s *sqlkit.Session) error {
		_, err := s.Update(Users).Set(Users.Email.Set("x@example.com")).Where(Users.ID.Eq(1)).Exec(ctx)
		return err
	})
	if err != nil {
		t.Fatalf("WithSession() error = %v", err)
	}
	if fake.Begins() != 1 || fake.Commits() != 1 {
		t.Fatalf("begins=%d commits=%d, want 1/1", fake.Begins(), fake.Commits())
	}
}

func TestWithSessionRollsBackOnError(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	ctx := context.Background()

	wantErr := errors.New("boom")
	err := db.WithSession(func(s *sqlkit.Session) error {
		if _, err := s.Delete(Users).Where(Users.ID.Eq(1)).Exec(ctx); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("WithSession() error = %v, want boom", err)
	}
	if fake.Begins() != 1 || fake.Commits() != 0 {
		t.Fatalf("begins=%d commits=%d, want 1/0 (rolled back)", fake.Begins(), fake.Commits())
	}
}

func TestWithSessionRollsBackOnPanicAndRepanics(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	ctx := context.Background()

	defer func() {
		if recover() == nil {
			t.Fatal("panic was swallowed")
		}
		if fake.Commits() != 0 {
			t.Fatalf("commits = %d, want 0 after panic", fake.Commits())
		}
	}()
	_ = db.WithSession(func(s *sqlkit.Session) error {
		_, _ = s.Delete(Users).Where(Users.ID.Eq(1)).Exec(ctx)
		panic("boom")
	})
}

func TestWithSessionNoStatementsIsFree(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	if err := db.WithSession(func(*sqlkit.Session) error { return nil }); err != nil {
		t.Fatalf("WithSession() error = %v", err)
	}
	if fake.Begins() != 0 || fake.Commits() != 0 {
		t.Fatalf("begins=%d commits=%d, want 0/0", fake.Begins(), fake.Commits())
	}
}
