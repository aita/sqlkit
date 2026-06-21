package sqlkit_test

import (
	"context"
	stdsql "database/sql"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
)

func TestMySQLDialectEndToEnd(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{
				Columns: []string{"id", "email", "name"},
				Rows:    [][]driver.Value{{int64(1), "a@example.com", nil}},
			}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithDialect(sqlkit.MySQLDialect{}))
	ctx := context.Background()

	var users []User
	err := db.Select().From(Users).Where(Users.ID.Gt(0)).Limit(5).All(ctx).Scan(&users)
	if err != nil {
		t.Fatalf("All().Scan() error = %v", err)
	}
	want := "SELECT `public`.`users`.`id`, `public`.`users`.`email`, `public`.`users`.`name` FROM `public`.`users` WHERE (`public`.`users`.`id` > ?) LIMIT 5"
	if got := fake.Queries()[0].SQL; got != want {
		t.Fatalf("SQL = %q,\nwant %q", got, want)
	}

	if _, err := db.Insert(Users).Values(User{Email: "a@example.com"}).Exec(ctx); err != nil {
		t.Fatalf("Insert Exec() error = %v", err)
	}
	if got := fake.Execs()[0].SQL; got != "INSERT INTO `public`.`users` (`email`, `name`) VALUES (?, ?)" {
		t.Fatalf("INSERT SQL = %q", got)
	}
}

func TestMySQLReturningFailsAtScanWithoutQuerying(t *testing.T) {
	fake := &testutil.Fake{}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithDialect(sqlkit.MySQLDialect{}))

	var inserted []User
	err := db.Insert(Users).Values(User{Email: "a@example.com"}).
		Returning().
		All(context.Background()).Scan(&inserted)
	if err == nil || !strings.Contains(err.Error(), "does not support RETURNING") {
		t.Fatalf("Scan() error = %v, want RETURNING rejection", err)
	}
	if n := len(fake.Queries()) + len(fake.Execs()); n != 0 {
		t.Fatalf("rejected statement still reached the database: %d statements", n)
	}
}

// TestPostgreSQLCRUDLifecycleEndToEnd runs a full create/read/update/delete
// lifecycle for one row through a single database, the PostgreSQL counterpart to
// TestMySQLDialectEndToEnd. It exercises the whole pipeline together — build,
// compile (PostgreSQL `$n` placeholders), execute, and scan — including a
// RETURNING write-back, a NULL column scanning into a *string, and a write's
// RowsAffected.
func TestPostgreSQLCRUDLifecycleEndToEnd(t *testing.T) {
	// One fake backs the whole lifecycle. The INSERT ... RETURNING and the SELECT
	// both arrive as queries, so QueryFunc branches on the RETURNING clause: the
	// insert read yields just the generated id, the select yields the full row
	// (with a NULL name, to exercise *string scanning).
	fake := &testutil.Fake{
		QueryFunc: func(sqlText string, _ []any) (testutil.Result, error) {
			if strings.Contains(sqlText, "RETURNING") {
				return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(101)}}}, nil
			}
			return testutil.Result{
				Columns: []string{"id", "email", "name"},
				Rows:    [][]driver.Value{{int64(101), "alice@example.com", nil}},
			}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB()) // PostgreSQL is the default dialect.
	ctx := context.Background()

	// CREATE: insert one row and read its database-generated id back.
	var ids []int64
	if err := db.Insert(Users).Values(User{Email: "alice@example.com"}).
		Returning(Users.ID).All(ctx).Scan(&ids); err != nil {
		t.Fatalf("Insert RETURNING Scan() error = %v", err)
	}
	if len(ids) != 1 || ids[0] != 101 {
		t.Fatalf("returned ids = %#v, want [101]", ids)
	}
	wantInsert := `INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2) RETURNING "public"."users"."id"`
	if got := fake.Queries()[0].SQL; got != wantInsert {
		t.Fatalf("INSERT SQL = %q,\nwant %q", got, wantInsert)
	}

	// READ: fetch the row and scan it into a struct; the NULL name scans to nil.
	var u User
	if err := db.Select().From(Users).Where(Users.ID.Eq(101)).One(ctx).Scan(&u); err != nil {
		t.Fatalf("Select One Scan() error = %v", err)
	}
	if u.ID != 101 || u.Email != "alice@example.com" || u.Name != nil {
		t.Fatalf("scanned user = %+v, want id 101, email alice@example.com, nil name", u)
	}

	// UPDATE: change the email and confirm one row was reported affected.
	res, err := db.Update(Users).Set(Users.Email.Set("alice2@example.com")).
		Where(Users.ID.Eq(101)).Exec(ctx)
	if err != nil {
		t.Fatalf("Update Exec() error = %v", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("RowsAffected() = %d, %v; want 1, nil", n, err)
	}
	wantUpdate := `UPDATE "public"."users" SET "email" = $1 WHERE ("public"."users"."id" = $2)`
	if got := fake.Execs()[0].SQL; got != wantUpdate {
		t.Fatalf("UPDATE SQL = %q,\nwant %q", got, wantUpdate)
	}

	// DELETE: remove the row.
	if _, err := db.Delete(Users).Where(Users.ID.Eq(101)).Exec(ctx); err != nil {
		t.Fatalf("Delete Exec() error = %v", err)
	}
	wantDelete := `DELETE FROM "public"."users" WHERE ("public"."users"."id" = $1)`
	if got := fake.Execs()[1].SQL; got != wantDelete {
		t.Fatalf("DELETE SQL = %q,\nwant %q", got, wantDelete)
	}
}

// TestDialectParitySameQueryAcrossEngines builds and runs the same typed query
// against each engine and asserts the two outputs differ only in dialect
// surface — identifier quoting and placeholder style — while scanning to the
// same Go values. It guards the dialect abstraction as a parity property, which
// no single-engine test covers.
func TestDialectParitySameQueryAcrossEngines(t *testing.T) {
	newFake := func() *testutil.Fake {
		return &testutil.Fake{
			QueryFunc: func(string, []any) (testutil.Result, error) {
				return testutil.Result{
					Columns: []string{"id", "email", "name"},
					Rows: [][]driver.Value{
						{int64(1), "a@example.com", "Alice"},
						{int64(2), "b@example.com", nil},
					},
				}, nil
			},
		}
	}

	cases := []struct {
		name    string
		dialect sqlkit.Dialect
		wantSQL string
	}{
		{
			name:    "postgres",
			dialect: sqlkit.PostgreSQLDialect{},
			wantSQL: `SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name" FROM "public"."users" WHERE ("public"."users"."id" > $1) LIMIT 5`,
		},
		{
			name:    "mysql",
			dialect: sqlkit.MySQLDialect{},
			wantSQL: "SELECT `public`.`users`.`id`, `public`.`users`.`email`, `public`.`users`.`name` FROM `public`.`users` WHERE (`public`.`users`.`id` > ?) LIMIT 5",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFake()
			db := sqlkit.WrapDB(fake.DB(), sqlkit.WithDialect(tc.dialect))

			var users []User
			if err := db.Select().From(Users).Where(Users.ID.Gt(0)).Limit(5).
				All(context.Background()).Scan(&users); err != nil {
				t.Fatalf("All().Scan() error = %v", err)
			}
			if got := fake.Queries()[0].SQL; got != tc.wantSQL {
				t.Fatalf("SQL = %q,\nwant %q", got, tc.wantSQL)
			}
			// The dialect changes the SQL surface, not the scanned result.
			if len(users) != 2 || users[0].ID != 1 || users[0].Email != "a@example.com" || users[1].Name != nil {
				t.Fatalf("scanned users = %+v, want two rows with a NULL second name", users)
			}
		})
	}
}

// TestUpsertOnConflictEndToEnd runs an INSERT ... ON CONFLICT DO UPDATE with
// RETURNING all the way through: the conflict clause is compiled, the statement
// reaches the database as a query, and the returned id scans back. It ties the
// upsert builder to the execute-and-scan path that the ToSQL-only upsert tests
// stop short of.
func TestUpsertOnConflictEndToEnd(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(7)}}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())
	ctx := context.Background()

	name := "Alice"
	var ids []int64
	err := db.Insert(Users).
		Values(User{ID: 7, Email: "a@example.com", Name: &name}).
		OnConflict(Users.ID).
		DoUpdate(Users.Email.SetExpr(Users.Email.Excluded())).
		Returning(Users.ID).
		All(ctx).Scan(&ids)
	if err != nil {
		t.Fatalf("upsert Scan() error = %v", err)
	}
	if len(ids) != 1 || ids[0] != 7 {
		t.Fatalf("returned ids = %#v, want [7]", ids)
	}
	want := `INSERT INTO "public"."users" ("id", "email", "name") VALUES ($1, $2, $3) ON CONFLICT ("id") DO UPDATE SET "email" = excluded."email" RETURNING "public"."users"."id"`
	if got := fake.Queries()[0].SQL; got != want {
		t.Fatalf("upsert SQL = %q,\nwant %q", got, want)
	}
}

// TestHooksObserveStatementsAcrossTransactionEndToEnd registers an audit hook on
// the database and runs a read and two writes inside one WithSession
// transaction. The hook must observe every statement (with the right Op) and
// fire its After callbacks once each, while the transaction control — the single
// begin and commit — stays invisible to it. It integrates hooks, the session
// transaction lifecycle, and the read/write execution paths in one flow.
func TestHooksObserveStatementsAcrossTransactionEndToEnd(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{
				Columns: []string{"id", "email", "name"},
				Rows:    [][]driver.Value{{int64(1), "a@example.com", nil}},
			}, nil
		},
	}
	var ops []sqlkit.Op
	var afterQueries, afterExecs int
	hooks := sqlkit.Hooks{
		Before: func(ctx context.Context, stmt *sqlkit.Statement) (context.Context, error) {
			ops = append(ops, stmt.Op)
			return ctx, nil
		},
		AfterQuery: func(context.Context, sqlkit.Statement, any, error) error { afterQueries++; return nil },
		AfterExec:  func(context.Context, sqlkit.Statement, stdsql.Result, error) error { afterExecs++; return nil },
	}
	db := sqlkit.WrapDB(fake.DB(), sqlkit.WithHooks(hooks))
	ctx := context.Background()

	err := db.WithSession(ctx, func(s *sqlkit.Session) error {
		var u User
		if err := s.Select().From(Users).Where(Users.ID.Eq(1)).One(ctx).Scan(&u); err != nil {
			return err
		}
		if _, err := s.Update(Users).Set(Users.Email.Set("b@example.com")).
			Where(Users.ID.Eq(1)).Exec(ctx); err != nil {
			return err
		}
		_, err := s.Delete(Users).Where(Users.ID.Eq(1)).Exec(ctx)
		return err
	})
	if err != nil {
		t.Fatalf("WithSession() error = %v", err)
	}

	wantOps := []sqlkit.Op{sqlkit.OpQuery, sqlkit.OpExec, sqlkit.OpExec}
	if !reflect.DeepEqual(ops, wantOps) {
		t.Fatalf("hook saw ops %v, want %v", ops, wantOps)
	}
	if afterQueries != 1 || afterExecs != 2 {
		t.Fatalf("afterQueries=%d afterExecs=%d, want 1/2", afterQueries, afterExecs)
	}
	// All three statements ran inside exactly one transaction that committed; the
	// hook never saw the begin or commit.
	if fake.Begins() != 1 || fake.Commits() != 1 || fake.Rollbacks() != 0 {
		t.Fatalf("begins=%d commits=%d rollbacks=%d, want 1/1/0",
			fake.Begins(), fake.Commits(), fake.Rollbacks())
	}
}
