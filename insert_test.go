package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
	"github.com/aita/sqlkit/sql"
)

// Field-to-column mapping edge cases for Insert(...).Values.

func TestInsertValuesMapsSnakeCaseFields(t *testing.T) {
	// Post.UserID maps to user_id without a tag.
	insert := sqlkit.Insert(Posts).Values(Post{UserID: 7, Title: "Hello"})
	assertToSQL(t, insert,
		`INSERT INTO "public"."posts" ("user_id", "title") VALUES ($1, $2)`,
		[]any{int64(7), "Hello"})
}

func TestInsertValuesFlattensEmbeddedStructs(t *testing.T) {
	type postCore struct {
		Title string
	}
	type postInsert struct {
		postCore
		UserID int64
	}

	insert := sqlkit.Insert(Posts).Values(postInsert{postCore: postCore{Title: "Hi"}, UserID: 3})
	assertToSQL(t, insert,
		`INSERT INTO "public"."posts" ("user_id", "title") VALUES ($1, $2)`,
		[]any{int64(3), "Hi"})
}

func TestInsertValuesSkipsDashTaggedFields(t *testing.T) {
	type ignoreName struct {
		Email string
		Name  string `sqlkit:"-"`
	}

	insert := sqlkit.Insert(Users).Values(ignoreName{Email: "a@example.com", Name: "hidden"})
	assertToSQL(t, insert,
		`INSERT INTO "public"."users" ("email") VALUES ($1)`,
		[]any{"a@example.com"})
}

func TestInsertValuesSliceOfPointers(t *testing.T) {
	insert := sqlkit.Insert(Users).Values([]*User{
		{Email: "a@example.com"},
		{Email: "b@example.com"},
	})
	assertToSQL(t, insert,
		`INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2), ($3, $4)`,
		[]any{"a@example.com", (*string)(nil), "b@example.com", (*string)(nil)})
}

func TestInsertValuesMixedTypesWithSameColumns(t *testing.T) {
	// Two distinct struct types are fine as long as they map to the same
	// column set.
	type mailOnly struct {
		Email string
	}
	insert := sqlkit.Insert(Users).Values(
		insertDTO{Mail: "a@example.com"},
		mailOnly{Email: "b@example.com"},
	)
	assertToSQL(t, insert,
		`INSERT INTO "public"."users" ("email") VALUES ($1), ($2)`,
		[]any{"a@example.com", "b@example.com"})
}

func TestInsertValuesMixedColumnSetsFails(t *testing.T) {
	insert := sqlkit.Insert(Users).Values(
		User{Email: "a@example.com"},     // id, email, name
		insertDTO{Mail: "b@example.com"}, // email only
	)
	_, _, err := insert.ToSQL(pg)
	if err == nil || !strings.Contains(err.Error(), "different column sets") {
		t.Fatalf("ToSQL() error = %v, want different-column-sets error", err)
	}
}

func TestInsertValuesDefaultedColumnSetInOneRow(t *testing.T) {
	// id is defaulted; explicitly set in one row, so it is kept for both.
	insert := sqlkit.Insert(Users).Values(
		User{Email: "a@example.com"},
		User{ID: 5, Email: "b@example.com"},
	)
	assertToSQL(t, insert,
		`INSERT INTO "public"."users" ("id", "email", "name") VALUES ($1, $2, $3), ($4, $5, $6)`,
		[]any{int64(0), "a@example.com", (*string)(nil), int64(5), "b@example.com", (*string)(nil)})
}

func TestInsertValuesRejectsBadRows(t *testing.T) {
	cases := []struct {
		name    string
		insert  sqlkit.InsertQuery
		wantErr string
	}{
		{"nil pointer", sqlkit.Insert(Users).Values((*User)(nil)), "nil pointer"},
		{"non-struct", sqlkit.Insert(Users).Values(42), "must be structs"},
		{"no mapped fields", sqlkit.Insert(Users).Values(struct{ Unrelated int }{1}), "no fields"},
		{"columns with values", sqlkit.Insert(Users).Columns(Users.Email).Values(User{Email: "x"}), "Columns applies to FromSelect"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := c.insert.ToSQL(pg)
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("ToSQL() error = %v, want %q", err, c.wantErr)
			}
		})
	}
}

func TestInsertFromSelectUnwrapsUnionQuery(t *testing.T) {
	union := sqlkit.Select(Users.Email).From(Users).Where(Users.ID.Eq(1)).
		UnionAll(sqlkit.Select(Users.Email).From(Users).Where(Users.ID.Eq(2)))

	insert := sqlkit.Insert(Users).Columns(Users.Email).FromSelect(union)
	assertToSQL(t, insert,
		`INSERT INTO "public"."users" ("email") (SELECT "public"."users"."email" FROM "public"."users" WHERE ("public"."users"."id" = $1)) UNION ALL (SELECT "public"."users"."email" FROM "public"."users" WHERE ("public"."users"."id" = $2))`,
		[]any{int64(1), int64(2)})
}

func TestInsertFromSelectAcceptsRawSQLQuery(t *testing.T) {
	source := sql.Select(sql.Col("email")).From(sql.Table{Name: "old_users"})

	insert := sqlkit.Insert(Users).Columns(Users.Email).FromSelect(source)
	assertToSQL(t, insert,
		`INSERT INTO "public"."users" ("email") SELECT "email" FROM "old_users"`,
		nil)
}

func TestInsertFromSelectReturningScan(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(1)}, {int64(2)}}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var ids []int64
	err := db.Insert(Users).Columns(Users.Email).
		FromSelect(db.Select(Users.Email).From(Users)).
		Returning(Users.ID).
		All(context.Background()).Scan(&ids)
	if err != nil {
		t.Fatalf("FromSelect Returning Scan error = %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("ids = %#v", ids)
	}
	want := `INSERT INTO "public"."users" ("email") SELECT "public"."users"."email" FROM "public"."users" RETURNING "public"."users"."id"`
	if got := fake.Queries()[0].SQL; got != want {
		t.Fatalf("SQL = %q,\nwant %q", got, want)
	}
}

func TestInsertExecThroughDatabase(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	if _, err := db.Insert(Users).Values(User{Email: "a@example.com"}).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 1 || !strings.HasPrefix(execs[0].SQL, "INSERT INTO") {
		t.Fatalf("execs = %#v, want one INSERT", execs)
	}
	if strings.Contains(execs[0].SQL, "RETURNING") {
		t.Fatalf("Exec without Returning produced RETURNING: %q", execs[0].SQL)
	}
}

func TestDeleteExecThroughDatabase(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	if _, err := db.Delete(Users).Where(Users.ID.Eq(1)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 1 || !strings.HasPrefix(execs[0].SQL, "DELETE FROM") {
		t.Fatalf("execs = %#v, want one DELETE", execs)
	}
}

func TestBuildErrorsSurfaceAtScanWithoutQuerying(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	var out []User
	err := db.Insert(Users).Returning().All(context.Background()).Scan(&out)
	if err == nil || !strings.Contains(err.Error(), "requires Values or FromSelect") {
		t.Fatalf("Scan() error = %v, want build error", err)
	}
	if n := len(fake.Queries()) + len(fake.Execs()); n != 0 {
		t.Fatalf("build error still reached the database: %d statements", n)
	}
}

func TestBuildErrorsSurfaceAtExecWithoutQuerying(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	if _, err := db.Insert(Users).Exec(context.Background()); err == nil {
		t.Fatal("Exec() on empty INSERT succeeded, want error")
	}
	if n := len(fake.Queries()) + len(fake.Execs()); n != 0 {
		t.Fatalf("build error still reached the database: %d statements", n)
	}
}

func TestQueryRejectsUnsupportedQueryType(t *testing.T) {
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())

	var out []User
	err := db.RawSQL(context.Background(), 42).Scan(&out)
	if err == nil || !strings.Contains(err.Error(), "must be a SQL string or sql.Query") {
		t.Fatalf("Scan() error = %v, want unsupported-query-type error", err)
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
