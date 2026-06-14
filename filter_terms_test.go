package sqlkit_test

import (
	"testing"

	"github.com/aita/sqlkit"
)

// filterTerms is a reusable filter bundle covering both an integer column (ID)
// and a string column (Email); unset (nil) terms are skipped by Filter.
type filterTerms struct {
	ID    sqlkit.FilterTerm
	Email sqlkit.FilterTerm
}

// filtered builds "SELECT * FROM users WHERE (<term>)" from a filter bundle and
// asserts the resulting SQL/args. It exercises the FilterTerm constructors,
// which resolve to a predicate against the field's column inside Filter.
func filtered(t *testing.T, f filterTerms, wantWhere string, wantArgs []any) {
	t.Helper()
	const prefix = `SELECT "public"."users"."id", "public"."users"."email", "public"."users"."name" FROM "public"."users" WHERE `
	assertSelectSQL(t, Users.Q(newDB()).Filter(f), prefix+wantWhere, wantArgs)
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
