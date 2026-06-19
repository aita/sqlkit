package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
)

func TestReturningIsExplicit(t *testing.T) {
	// Without Returning, no RETURNING clause is generated.
	insert := sqlkit.Insert(Users).Values(User{Email: "a@example.com"})
	assertToSQL(t, insert,
		`INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2)`,
		[]any{"a@example.com", (*string)(nil)})

	// Returning() with no arguments returns all table columns.
	all := sqlkit.Insert(Users).Values(User{Email: "a@example.com"}).Returning()
	assertToSQL(t, all,
		`INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2) RETURNING "public"."users"."id", "public"."users"."email", "public"."users"."name"`,
		[]any{"a@example.com", (*string)(nil)})

	// Returning(columns...) returns just those columns.
	ids := sqlkit.Insert(Users).Values(User{Email: "a@example.com"}).Returning(Users.ID)
	assertToSQL(t, ids,
		`INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2) RETURNING "public"."users"."id"`,
		[]any{"a@example.com", (*string)(nil)})
}

func TestReturningSpecificColumnsScan(t *testing.T) {
	fake := &testutil.Fake{
		QueryFunc: func(string, []any) (testutil.Result, error) {
			return testutil.Result{Columns: []string{"id"}, Rows: [][]driver.Value{{int64(7)}}}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var ids []int64
	err := db.Delete(Users).Where(Users.Email.Eq("gone@example.com")).
		Returning(Users.ID).
		All(context.Background()).Scan(&ids)
	if err != nil {
		t.Fatalf("Returning(ID).All().Scan() error = %v", err)
	}
	if len(ids) != 1 || ids[0] != 7 {
		t.Fatalf("ids = %#v", ids)
	}
	want := `DELETE FROM "public"."users" WHERE ("public"."users"."email" = $1) RETURNING "public"."users"."id"`
	if got := fake.Queries()[0].SQL; got != want {
		t.Fatalf("DELETE SQL = %q,\nwant %q", got, want)
	}
}
