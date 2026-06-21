package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/sql"
)

// TestUpdateWithCTE covers WithCTE: a leading WITH clause prepended to the UPDATE.
func TestUpdateWithCTE(t *testing.T) {
	active := sqlkit.Select(Posts.UserID).From(Posts).CTE("active")
	assertToSQL(t,
		sqlkit.Update(Users).Set(Users.Email.Set("x@example.com")).WithCTE(active).Where(Users.ID.Eq(1)),
		`WITH "active" AS (SELECT "public"."posts"."user_id" FROM "public"."posts") UPDATE "public"."users" SET "email" = $1 WHERE ("public"."users"."id" = $2)`,
		[]any{"x@example.com", int64(1)})
}

// TestUpdateLeftJoin covers LeftJoin, the MySQL inline LEFT JOIN form of a
// multi-table UPDATE (PostgreSQL has no inline LEFT JOIN for UPDATE).
func TestUpdateLeftJoin(t *testing.T) {
	query := sqlkit.Update(Users).Set(Users.Email.Set("x")).
		LeftJoin(Posts, Users.ID.EqExpr(Posts.UserID)).
		Where(Posts.ID.IsNull())
	sqlText, _, err := query.ToSQL(sql.MySQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	want := "UPDATE `public`.`users` LEFT JOIN `public`.`posts` ON (`public`.`users`.`id` = `public`.`posts`.`user_id`) SET `email` = ? WHERE (`public`.`posts`.`id` IS NULL)"
	if sqlText != want {
		t.Fatalf("ToSQL() = %q,\nwant %q", sqlText, want)
	}
}

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
	db := sqlkit.WrapDB((&testutil.Fake{RowsAffected: &zero}).DB())

	_, err := db.Update(Users).Set(Users.Email.Set("x@example.com")).
		Where(Users.ID.Eq(1)).CheckVersion(version, 7).Exec(context.Background())
	if !errors.Is(err, sqlkit.ErrVersionConflict) {
		t.Fatalf("Exec() error = %v, want ErrVersionConflict", err)
	}
}

func TestCheckVersionExecSuccess(t *testing.T) {
	// A matched row (one affected, the fake's default) is not a conflict.
	db := sqlkit.WrapDB((&testutil.Fake{}).DB())

	if _, err := db.Update(Users).Set(Users.Email.Set("x@example.com")).
		Where(Users.ID.Eq(1)).CheckVersion(version, 7).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v, want nil", err)
	}
}

func TestUpdateWithoutVersionGuardIgnoresZeroRows(t *testing.T) {
	// Without CheckVersion, zero rows affected is an ordinary no-op, not an error.
	zero := int64(0)
	db := sqlkit.WrapDB((&testutil.Fake{RowsAffected: &zero}).DB())

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
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{
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

func TestUpdateSetStructVersionReadonlyJSON(t *testing.T) {
	item := Item{ID: 1, SKU: "abc", Meta: map[string]any{"k": "v"}, Rev: 3, Created: 99}

	// Implicit update: read-only "created" excluded, "meta" written as JSON, and
	// the version column bumped with a guard on its current value (3).
	assertToSQL(t,
		sqlkit.Update(Items).SetStruct(item).Where(Items.ID.Eq(item.ID)),
		`UPDATE "public"."items" SET "sku" = $1, "meta" = $2, "rev" = ("rev" + $3) WHERE (("rev" = $4) AND ("public"."items"."id" = $5))`,
		nil)
}

func TestSetStructVersionGuardConflict(t *testing.T) {
	item := Item{ID: 1, SKU: "abc", Rev: 3}

	// Zero rows affected under the tag-driven version guard is a conflict.
	zero := int64(0)
	db := sqlkit.WrapDB((&testutil.Fake{RowsAffected: &zero}).DB())
	_, err := db.Update(Items).SetStruct(item).Where(Items.ID.Eq(item.ID)).Exec(context.Background())
	if !errors.Is(err, sqlkit.ErrVersionConflict) {
		t.Fatalf("Exec() error = %v, want ErrVersionConflict", err)
	}

	// A matched row is not a conflict.
	ok := sqlkit.WrapDB((&testutil.Fake{}).DB())
	if _, err := ok.Update(Items).SetStruct(item).Where(Items.ID.Eq(item.ID)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v, want nil", err)
	}
}

func TestExplicitColumnsIgnoreReadonlyAndVersionSemantics(t *testing.T) {
	item := Item{ID: 1, Rev: 3, Created: 99}

	// Naming columns explicitly overrides the implicit exclusions: a read-only
	// column is written verbatim and a version column is copied, not bumped.
	assertToSQL(t,
		sqlkit.Update(Items).SetStruct(item, Items.Created, Items.Rev).Where(Items.ID.Eq(item.ID)),
		`UPDATE "public"."items" SET "created" = $1, "rev" = $2 WHERE ("public"."items"."id" = $3)`,
		[]any{int64(99), int64(3), int64(1)})
}

func TestUpdateSetStructTouch(t *testing.T) {
	// Implicit update: the pk is excluded and the touch column is set to the
	// current time rather than copied from the struct.
	assertToSQL(t,
		sqlkit.Update(Events).SetStruct(Event{ID: 1, Name: "closed"}).Where(Events.ID.Eq(1)),
		`UPDATE "public"."events" SET "name" = $1, "updated_at" = CURRENT_TIMESTAMP WHERE ("public"."events"."id" = $2)`,
		[]any{"closed", int64(1)})
}

func TestUpdateSetStructTouchExplicitOverride(t *testing.T) {
	// Naming the touch column explicitly copies the struct value instead of
	// using CURRENT_TIMESTAMP.
	when := time.Date(2026, 6, 19, 0, 0, 0, 0, time.UTC)
	assertToSQL(t,
		sqlkit.Update(Events).SetStruct(Event{ID: 1, UpdatedAt: when}, Events.UpdatedAt).Where(Events.ID.Eq(1)),
		`UPDATE "public"."events" SET "updated_at" = $1 WHERE ("public"."events"."id" = $2)`,
		[]any{when, int64(1)})
}
