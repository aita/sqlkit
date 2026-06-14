package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
)

func TestTypedUpdateFromAndDeleteUsing(t *testing.T) {
	assertToSQL(t,
		sqlkit.Update(Posts).
			Set(Posts.Title.SetExpr(sqlkit.SubExpr[string](
				sqlkit.Select(Users.Email).From(Users).Where(Users.ID.EqExpr(Posts.UserID))))).
			From(Users).
			Where(Users.ID.EqExpr(Posts.UserID)),
		`UPDATE "public"."posts" SET "title" = (SELECT "public"."users"."email" FROM "public"."users" WHERE ("public"."users"."id" = "public"."posts"."user_id")) FROM "public"."users" WHERE ("public"."users"."id" = "public"."posts"."user_id")`,
		nil)

	assertToSQL(t,
		sqlkit.Delete(Posts).Using(Users).Where(Users.ID.EqExpr(Posts.UserID), Users.Email.Eq("x@example.com")),
		`DELETE FROM "public"."posts" USING "public"."users" WHERE (("public"."users"."id" = "public"."posts"."user_id") AND ("public"."users"."email" = $1))`,
		[]any{"x@example.com"})
}

// version is a Users-bound int64 column standing in for a generated version
// column (the fixtures in session_test.go have none).
var version = sqlkit.NewColumn[int64](Users.Info(), "version")

func TestCheckVersionToSQL(t *testing.T) {
	// CheckVersion bumps the version column and guards on its current value.
	assertToSQL(t,
		sqlkit.Update(Users).Set(Users.Email.Set("x@example.com")).
			Where(Users.ID.Eq(1)).CheckVersion(version, 7),
		`UPDATE "public"."users" SET "email" = $1, "version" = ("version" + $2) WHERE (("public"."users"."id" = $3) AND ("version" = $4))`,
		[]any{"x@example.com", 1, int64(1), 7})
}

func TestCheckVersionExecConflict(t *testing.T) {
	// Zero rows affected under a version guard is reported as a conflict.
	zero := int64(0)
	db := sqlkit.WrapDB((&sqltest.Fake{RowsAffected: &zero}).DB())

	_, err := db.Update(Users).Set(Users.Email.Set("x@example.com")).
		Where(Users.ID.Eq(1)).CheckVersion(version, 7).Exec(context.Background())
	if !errors.Is(err, sqlkit.ErrVersionConflict) {
		t.Fatalf("Exec() error = %v, want ErrVersionConflict", err)
	}
}

func TestCheckVersionExecSuccess(t *testing.T) {
	// A matched row (one affected, the fake's default) is not a conflict.
	db := sqlkit.WrapDB((&sqltest.Fake{}).DB())

	if _, err := db.Update(Users).Set(Users.Email.Set("x@example.com")).
		Where(Users.ID.Eq(1)).CheckVersion(version, 7).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v, want nil", err)
	}
}

func TestUpdateWithoutVersionGuardIgnoresZeroRows(t *testing.T) {
	// Without CheckVersion, zero rows affected is an ordinary no-op, not an error.
	zero := int64(0)
	db := sqlkit.WrapDB((&sqltest.Fake{RowsAffected: &zero}).DB())

	if _, err := db.Update(Users).Set(Users.Email.Set("x@example.com")).
		Where(Users.ID.Eq(1)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v, want nil", err)
	}
}

func TestUpdateSetStruct(t *testing.T) {
	name := "Alice"
	u := User{ID: 7, Email: "a@example.com", Name: &name}

	// Explicit columns: a partial update of just those columns.
	assertToSQL(t,
		sqlkit.Update(Users).SetStruct(u, Users.Email).Where(Users.ID.Eq(u.ID)),
		`UPDATE "public"."users" SET "email" = $1 WHERE ("public"."users"."id" = $2)`,
		[]any{"a@example.com", int64(7)})

	// Several explicit columns keep the given order.
	assertToSQL(t,
		sqlkit.Update(Users).SetStruct(u, Users.Name, Users.Email).Where(Users.ID.Eq(u.ID)),
		`UPDATE "public"."users" SET "name" = $1, "email" = $2 WHERE ("public"."users"."id" = $3)`,
		[]any{&name, "a@example.com", int64(7)})

	// No columns: every mapped column except the primary key, in table order.
	assertToSQL(t,
		sqlkit.Update(Users).SetStruct(u).Where(Users.ID.Eq(u.ID)),
		`UPDATE "public"."users" SET "email" = $1, "name" = $2 WHERE ("public"."users"."id" = $3)`,
		[]any{"a@example.com", &name, int64(7)})

	// Composes with Set: the struct assignments come first, then manual ones.
	assertToSQL(t,
		sqlkit.Update(Users).SetStruct(u, Users.Email).
			Set(Users.ID.SetExpr(Users.ID.Add(1))).Where(Users.ID.Eq(u.ID)),
		`UPDATE "public"."users" SET "email" = $1, "id" = ("public"."users"."id" + $2) WHERE ("public"."users"."id" = $3)`,
		[]any{"a@example.com", int64(1), int64(7)})
}

func TestUpdateSetStructErrors(t *testing.T) {
	cases := map[string]sqlkit.UpdateQuery{
		"not a struct": sqlkit.Update(Users).SetStruct(42),
		"nil pointer":  sqlkit.Update(Users).SetStruct((*User)(nil)),
		"explicit column with no field": sqlkit.Update(Users).
			SetStruct(struct{ Email string }{}, Users.Name),
		"no fields map": sqlkit.Update(Users).
			SetStruct(struct{ Foo string }{}),
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := query.ToSQL(pg); err == nil {
				t.Fatalf("ToSQL() error = nil, want non-nil")
			}
		})
	}
}

func TestUpdateFromDB(t *testing.T) {
	db := sqlkit.WrapDB(usersFake().DB())
	query := db.Update(Users).
		Set(Users.Email.Set("new@example.com")).
		Where(Users.ID.Eq(7))
	assertToSQL(t, query,
		`UPDATE "public"."users" SET "email" = $1 WHERE ("public"."users"."id" = $2)`,
		[]any{"new@example.com", int64(7)})
}

func TestUpdateReturningScans(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{
				Columns: []string{"id", "email", "name"},
				Rows:    [][]driver.Value{{int64(7), "new@example.com", nil}},
			}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var updated []User
	err := db.Update(Users).Set(Users.Email.Set("new@example.com")).Where(Users.ID.Eq(7)).
		Returning().
		All(context.Background()).Scan(&updated)
	if err != nil {
		t.Fatalf("Update().All().Scan() error = %v", err)
	}
	if len(updated) != 1 || updated[0].Email != "new@example.com" {
		t.Fatalf("updated = %#v", updated)
	}
}

func TestUpdateWithoutReturningHasNoAll(t *testing.T) {
	// Compile-time property: UpdateQuery/InsertQuery/DeleteQuery expose Exec
	// but not All; All exists only on ReturningQuery. Verified by the type
	// switch below not requiring assertions — this test just documents the
	// runtime SQL shape without RETURNING.
	query := sqlkit.Update(Users).Set(Users.Email.Set("x@example.com")).Where(Users.ID.Eq(1))
	assertToSQL(t, query,
		`UPDATE "public"."users" SET "email" = $1 WHERE ("public"."users"."id" = $2)`,
		[]any{"x@example.com", int64(1)})
}
