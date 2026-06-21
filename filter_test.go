package sqlkit_test

import (
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/sql"
)

func TestUpdateAndDeleteFilter(t *testing.T) {
	// The same filter bundle that narrows a SELECT (TestSelectFilter) narrows an
	// UPDATE and a DELETE: Filter resolves against the target table's columns, so
	// one reusable bundle serves a read or a write.
	assertToSQL(t,
		sqlkit.Update(Users).
			Filter(userFilter{Email: sqlkit.Eq("a@x")}).
			Set(Users.Email.Set("new@x")),
		`UPDATE "public"."users" SET "email" = $1 WHERE ("public"."users"."email" = $2)`,
		[]any{"new@x", "a@x"})

	assertToSQL(t,
		sqlkit.Delete(Users).Filter(userFilter{Email: sqlkit.Eq("a@x")}),
		`DELETE FROM "public"."users" WHERE ("public"."users"."email" = $1)`,
		[]any{"a@x"})
}

func TestUpdateAndDeleteFilterDefersError(t *testing.T) {
	// A bad filter (not a struct or Predicate) defers its build error to the
	// terminal, matching SelectQuery.Filter.
	if _, _, err := sqlkit.Update(Users).Filter(123).ToSQL(pg); err == nil {
		t.Fatal("UpdateQuery.Filter with a bad filter should defer an error")
	}
	if _, _, err := sqlkit.Delete(Users).Filter(123).ToSQL(pg); err == nil {
		t.Fatal("DeleteQuery.Filter with a bad filter should defer an error")
	}
}

func TestSelectFilter(t *testing.T) {
	db := newDB()
	assertSelectSQL(t, Users.Q(db).Filter(userFilter{Email: sqlkit.Eq("a@x")}),
		`SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name" FROM "public"."users" WHERE ("public"."users"."email" = $1)`,
		[]any{"a@x"})
}

// TestFilterMapsFieldByTag covers columnKey's `sqlkit` tag branch: a filter
// field whose name does not match a column maps to one via its tag.
func TestFilterMapsFieldByTag(t *testing.T) {
	type taggedFilter struct {
		Mail sqlkit.FilterTerm `sqlkit:"email"`
	}
	db := newDB()
	assertSelectSQL(t, Users.Q(db).Filter(taggedFilter{Mail: sqlkit.Eq("a@x")}),
		`SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name" FROM "public"."users" WHERE ("public"."users"."email" = $1)`,
		[]any{"a@x"})
}

func TestFilterRelationExists(t *testing.T) {
	db := newDB()
	// A nested filter on a relation narrows the base table with a correlated
	// EXISTS over the related table, joined on the foreign key.
	assertSelectSQL(t, Users.Q(db).Filter(usersByPosts{Posts: &postsByTitle{Title: sqlkit.Eq("Hello")}}),
		`SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name" FROM "public"."users" WHERE EXISTS (SELECT 1 FROM "public"."posts" WHERE (("public"."posts"."user_id" = "public"."users"."id") AND ("public"."posts"."title" = $1)))`,
		[]any{"Hello"})

	// An empty nested filter means "has any related row": just the join.
	assertSelectSQL(t, Users.Q(db).Filter(usersByPosts{Posts: &postsByTitle{}}),
		`SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name" FROM "public"."users" WHERE EXISTS (SELECT 1 FROM "public"."posts" WHERE ("public"."posts"."user_id" = "public"."users"."id"))`,
		nil)

	// The same relation filter narrows an UPDATE and a DELETE through the same
	// correlated EXISTS, no join needed.
	assertToSQL(t,
		db.Update(Users).Filter(usersByPosts{Posts: &postsByTitle{Title: sqlkit.Eq("Hello")}}).
			Set(Users.Email.Set("x@x")),
		`UPDATE "public"."users" SET "email" = $1 WHERE EXISTS (SELECT 1 FROM "public"."posts" WHERE (("public"."posts"."user_id" = "public"."users"."id") AND ("public"."posts"."title" = $2)))`,
		[]any{"x@x", "Hello"})

	assertToSQL(t,
		db.Delete(Users).Filter(usersByPosts{Posts: &postsByTitle{Title: sqlkit.Eq("Hello")}}),
		`DELETE FROM "public"."users" WHERE EXISTS (SELECT 1 FROM "public"."posts" WHERE (("public"."posts"."user_id" = "public"."users"."id") AND ("public"."posts"."title" = $1)))`,
		[]any{"Hello"})
}

func TestFilterRelationExistsMultiHop(t *testing.T) {
	db := newDB()
	// A relation filter nested inside another relation filter (users → posts →
	// users) nests the correlated EXISTS: the inner EXISTS over users is
	// correlated to the posts row of the outer EXISTS.
	assertSelectSQL(t,
		Users.Q(db).Filter(usersByPostAuthor{Posts: &postsByAuthor{User: &userFilter{Email: sqlkit.Eq("a@example.com")}}}),
		`SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name" FROM "public"."users" WHERE EXISTS (SELECT 1 FROM "public"."posts" WHERE (("public"."posts"."user_id" = "public"."users"."id") AND EXISTS (SELECT 1 FROM "public"."users" WHERE (("public"."users"."id" = "public"."posts"."user_id") AND ("public"."users"."email" = $1)))))`,
		[]any{"a@example.com"})
}

func TestSelectFilterRequiresFromTable(t *testing.T) {
	db := newDB()
	// A subquery source is not a table, so Filter defers an error to the terminal.
	q := sqlkit.Select().From(sqlkit.Select().From(Users).SQLSource()).Filter(userFilter{ID: sqlkit.Eq(1)})
	if _, _, err := q.ToSQL(sql.PostgreSQLDialect{}); err == nil {
		t.Fatal("Filter without a From table should defer an error")
	}
	_ = db
}

// TestFilterComparisonTerms covers the comparison FilterTerm constructors that
// the existing Eq test does not: Ne/Lt/Le/Gt/Ge.
func TestFilterComparisonTerms(t *testing.T) {
	id := `"public"."users"."id"`
	filtered(t, filterTerms{ID: sqlkit.Ne(1)}, `(`+id+` <> $1)`, []any{int64(1)})
	filtered(t, filterTerms{ID: sqlkit.Lt(2)}, `(`+id+` < $1)`, []any{int64(2)})
	filtered(t, filterTerms{ID: sqlkit.Le(3)}, `(`+id+` <= $1)`, []any{int64(3)})
	filtered(t, filterTerms{ID: sqlkit.Gt(4)}, `(`+id+` > $1)`, []any{int64(4)})
	filtered(t, filterTerms{ID: sqlkit.Ge(5)}, `(`+id+` >= $1)`, []any{int64(5)})
}

// TestFilterSetAndPatternTerms covers In, FilterLike, and FilterIsNull.
func TestFilterSetAndPatternTerms(t *testing.T) {
	filtered(t, filterTerms{ID: sqlkit.In(1, 2, 3)},
		`("public"."users"."id" IN ($1, $2, $3))`, []any{int64(1), int64(2), int64(3)})
	filtered(t, filterTerms{Email: sqlkit.FilterLike("a%")},
		`("public"."users"."email" LIKE $1)`, []any{"a%"})
	filtered(t, filterTerms{Email: sqlkit.FilterIsNull()},
		`("public"."users"."email" IS NULL)`, nil)
}
