package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
)

// nullNameUsers has NULL names at the tail of the NULL-greatest ascending order.
func nullNameUsers() [][]driver.Value {
	return [][]driver.Value{
		{int64(1), "a@example.com", "Alice"},
		{int64(2), "b@example.com", nil},
		{int64(3), "c@example.com", nil},
	}
}

// pageSQL runs one Paginate and returns the SQL it issued and the page.
func pageSQL(t *testing.T, pager *sqlkit.Paginator, rows [][]driver.Value, cur sqlkit.Cursor) (string, sqlkit.Page) {
	t.Helper()
	var gotSQL string
	db := sqlkit.WrapDB(paginatorFake(rows, &gotSQL, nil).DB())
	var users []MyUserDTO
	page, err := pager.Paginate(context.Background(), db.Select().From(Users), &users, cur)
	if err != nil {
		t.Fatalf("Paginate() error = %v", err)
	}
	return gotSQL, page
}

// A nullable key orders NULLs as the greatest value, portably: a "col IS NULL"
// term precedes the column in the key's direction.
func TestPaginateNullableKeyOrdersNullsGreatest(t *testing.T) {
	asc := sqlkit.NewPaginator(sqlkit.KeyAsc(Users.Name), sqlkit.KeyAsc(Users.ID)).Limit(2)
	got, _ := pageSQL(t, asc, nullNameUsers(), sqlkit.Start())
	if want := `ORDER BY ("public"."users"."name" IS NULL), "public"."users"."name", "public"."users"."id" LIMIT`; !containsSQL(got, want) {
		t.Fatalf("SQL = %s\nwant it to contain %s", got, want)
	}
	desc := sqlkit.NewPaginator(sqlkit.KeyDesc(Users.Name), sqlkit.KeyAsc(Users.ID)).Limit(2)
	got, _ = pageSQL(t, desc, nullNameUsers(), sqlkit.Start())
	if want := `ORDER BY ("public"."users"."name" IS NULL) DESC, "public"."users"."name" DESC, "public"."users"."id" LIMIT`; !containsSQL(got, want) {
		t.Fatalf("SQL = %s\nwant it to contain %s", got, want)
	}
}

// A non-nullable key keeps the plain ORDER BY so an index can serve it.
func TestPaginateNonNullableKeyOrderUnchanged(t *testing.T) {
	pager := sqlkit.NewPaginator(sqlkit.KeyAsc(Users.Email), sqlkit.KeyAsc(Users.ID)).Limit(2)
	got, _ := pageSQL(t, pager, threeUsers(), sqlkit.Start())
	if containsSQL(got, "IS NULL") {
		t.Fatalf("SQL = %s, want no IS NULL ordering for non-nullable keys", got)
	}
}

// Paging past a NULL key value must not drop the rows after it: the cursor at
// a NULL boundary seeks with "name IS NULL AND id > ?" (nothing is greater than
// NULL), and at a non-NULL boundary the "greater" side also admits NULLs.
func TestPaginateNullableKeySeek(t *testing.T) {
	pager := sqlkit.NewPaginator(sqlkit.KeyAsc(Users.Name), sqlkit.KeyAsc(Users.ID)).Limit(1)

	// Page 1 ends at Alice (non-NULL): next page admits greater names and NULLs.
	_, p1 := pageSQL(t, pager, nullNameUsers()[:2], sqlkit.Start())
	got, p2 := pageSQL(t, pager, nullNameUsers()[1:3], p1.Next())
	if want := `(("public"."users"."name" > $1) OR ("public"."users"."name" IS NULL)) OR (("public"."users"."name" = $2) AND ("public"."users"."id" > $3))`; !containsSQL(got, want) {
		t.Fatalf("seek after non-NULL boundary:\n%s\nwant it to contain\n%s", got, want)
	}

	// Page 2 ends at a NULL name: the next page continues among NULLs by id.
	got, _ = pageSQL(t, pager, nullNameUsers()[2:3], p2.Next())
	if want := `WHERE (("public"."users"."name" IS NULL) AND ("public"."users"."id" > $1))`; !containsSQL(got, want) {
		t.Fatalf("seek after NULL boundary:\n%s\nwant it to contain\n%s", got, want)
	}

	// Going back from a NULL boundary: every non-NULL name, plus earlier NULLs.
	got, _ = pageSQL(t, pager, nullNameUsers()[:1], p2.Prev())
	if want := `("public"."users"."name" IS NOT NULL) OR (("public"."users"."name" IS NULL) AND ("public"."users"."id" < $1))`; !containsSQL(got, want) {
		t.Fatalf("backward seek from NULL boundary:\n%s\nwant it to contain\n%s", got, want)
	}
}

func containsSQL(sqlText, fragment string) bool { return strings.Contains(sqlText, fragment) }
