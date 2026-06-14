package sqlkit_test

import (
	"testing"

	"github.com/aita/sqlkit"
)

func TestColumnReferenceQualifiesByRealTable(t *testing.T) {
	info := sqlkit.TableInfo{Schema: "public", Name: "users"}
	id := sqlkit.NewColumn[int64](info, "id")
	if got, want := id.Reference(), "public.users.id"; got != want {
		t.Fatalf("Reference() = %q, want %q", got, want)
	}
}

func TestColumnReferenceQualifiesByAlias(t *testing.T) {
	info := sqlkit.TableInfo{Schema: "public", Name: "employees", Alias: "manager"}
	id := sqlkit.NewColumn[int64](info, "id")
	if got, want := id.Reference(), "manager.id"; got != want {
		t.Fatalf("Reference() = %q, want %q", got, want)
	}
}

func TestTableInfoSQLSourceAlias(t *testing.T) {
	base := sqlkit.TableInfo{Schema: "public", Name: "employees"}
	if src := base.SQLSource(); src.Alias != "" || src.Name != "public.employees" {
		t.Fatalf("base SQLSource() = %#v, want Name=public.employees Alias empty", src)
	}

	aliased := base.WithAlias("manager")
	src := aliased.SQLSource()
	if src.Name != "public.employees" || src.Alias != "manager" {
		t.Fatalf("aliased SQLSource() = %#v, want Name=public.employees Alias=manager", src)
	}
	// QualifiedName stays the real name so statement targets are unaffected.
	if got := aliased.QualifiedName(); got != "public.employees" {
		t.Fatalf("aliased QualifiedName() = %q, want public.employees", got)
	}
}

func TestAliasedTableSelectQualifiesColumnsAndSource(t *testing.T) {
	manager := Users.As("manager")
	query := sqlkit.Select(manager.ID, manager.Email).From(manager)
	assertToSQL(t, query,
		`SELECT "manager"."id", "manager"."email" FROM "public"."users" AS "manager"`,
		nil)
}

// where builds "SELECT id FROM users WHERE (<pred>)" and asserts its SQL/args.
// It exercises the predicate constructors, which render only inside a clause.
func where(t *testing.T, p sqlkit.Predicate, wantWhere string, wantArgs []any) {
	t.Helper()
	const prefix = `SELECT "public"."users"."id" FROM "public"."users" WHERE `
	assertToSQL(t,
		sqlkit.Select(Users.ID).From(Users).Where(p),
		prefix+wantWhere, wantArgs)
}

// TestColumnComparisonPredicates covers the value-comparison methods that the
// typed-equality test (Eq) does not: Ne/Lt/Le/Gt/Ge.
func TestColumnComparisonPredicates(t *testing.T) {
	col := `"public"."users"."id"`
	where(t, Users.ID.Ne(1), `(`+col+` <> $1)`, []any{int64(1)})
	where(t, Users.ID.Lt(2), `(`+col+` < $1)`, []any{int64(2)})
	where(t, Users.ID.Le(3), `(`+col+` <= $1)`, []any{int64(3)})
	where(t, Users.ID.Gt(4), `(`+col+` > $1)`, []any{int64(4)})
	where(t, Users.ID.Ge(5), `(`+col+` >= $1)`, []any{int64(5)})
}

// TestColumnComparisonExprPredicates covers the *Expr comparison methods, which
// compare a column against another expression (e.g. another column) rather than
// a bind value, so they emit no args.
func TestColumnComparisonExprPredicates(t *testing.T) {
	id := `"public"."users"."id"`
	other := `"public"."posts"."user_id"`
	where(t, Users.ID.NeExpr(Posts.UserID), `(`+id+` <> `+other+`)`, nil)
	where(t, Users.ID.LtExpr(Posts.UserID), `(`+id+` < `+other+`)`, nil)
	where(t, Users.ID.LeExpr(Posts.UserID), `(`+id+` <= `+other+`)`, nil)
	where(t, Users.ID.GtExpr(Posts.UserID), `(`+id+` > `+other+`)`, nil)
	where(t, Users.ID.GeExpr(Posts.UserID), `(`+id+` >= `+other+`)`, nil)
}

// TestColumnRangeAndSetPredicates covers NotIn, Between, and NotBetween.
func TestColumnRangeAndSetPredicates(t *testing.T) {
	col := `"public"."users"."id"`
	where(t, Users.ID.NotIn(1, 2), `(`+col+` NOT IN ($1, $2))`, []any{int64(1), int64(2)})
	where(t, Users.ID.Between(1, 5), `(`+col+` BETWEEN $1 AND $2)`, []any{int64(1), int64(5)})
	where(t, Users.ID.NotBetween(1, 5), `(`+col+` NOT BETWEEN $1 AND $2)`, []any{int64(1), int64(5)})
}

// TestColumnSubqueryPredicates covers the subquery-valued comparisons:
// In/NotIn against a subquery and the ANY/ALL quantified comparisons.
func TestColumnSubqueryPredicates(t *testing.T) {
	sub := sqlkit.Select(Posts.UserID).From(Posts)
	subSQL := `SELECT "public"."posts"."user_id" FROM "public"."posts"`
	col := `"public"."users"."id"`
	where(t, Users.ID.InQuery(sub), `(`+col+` IN (`+subSQL+`))`, nil)
	where(t, Users.ID.NotInQuery(sub), `(`+col+` NOT IN (`+subSQL+`))`, nil)
	where(t, Users.ID.LtAny(sub), `(`+col+` < ANY (`+subSQL+`))`, nil)
	where(t, Users.ID.LeAny(sub), `(`+col+` <= ANY (`+subSQL+`))`, nil)
	where(t, Users.ID.GtAny(sub), `(`+col+` > ANY (`+subSQL+`))`, nil)
	where(t, Users.ID.GeAny(sub), `(`+col+` >= ANY (`+subSQL+`))`, nil)
	where(t, Users.ID.LtAll(sub), `(`+col+` < ALL (`+subSQL+`))`, nil)
	where(t, Users.ID.LeAll(sub), `(`+col+` <= ALL (`+subSQL+`))`, nil)
	where(t, Users.ID.GtAll(sub), `(`+col+` > ALL (`+subSQL+`))`, nil)
	where(t, Users.ID.GeAll(sub), `(`+col+` >= ALL (`+subSQL+`))`, nil)
}

// TestColumnNullPredicate covers IsNull (IsNotNull is already exercised).
func TestColumnNullPredicate(t *testing.T) {
	where(t, Users.Name.IsNull(), `("public"."users"."name" IS NULL)`, nil)
}

// TestStringPattern Predicates covers the package-level LIKE family operating on
// string expressions.
func TestStringPatternPredicates(t *testing.T) {
	col := `"public"."users"."email"`
	where(t, sqlkit.Like(Users.Email, "a%"), `(`+col+` LIKE $1)`, []any{"a%"})
	where(t, sqlkit.NotLike(Users.Email, "a%"), `(`+col+` NOT LIKE $1)`, []any{"a%"})
	where(t, sqlkit.ILike(Users.Email, "a%"), `(`+col+` ILIKE $1)`, []any{"a%"})
}

// TestColumnArithmeticExpressions covers the Add/Sub/Mul/Div expression
// builders, used inside a comparison so they render in a clause.
func TestColumnArithmeticExpressions(t *testing.T) {
	id := `"public"."users"."id"`
	where(t, Users.ID.GtExpr(Users.ID.Add(1)), `(`+id+` > (`+id+` + $1))`, []any{int64(1)})
	where(t, Users.ID.GtExpr(Users.ID.Sub(1)), `(`+id+` > (`+id+` - $1))`, []any{int64(1)})
	where(t, Users.ID.GtExpr(Users.ID.Mul(2)), `(`+id+` > (`+id+` * $1))`, []any{int64(2)})
	where(t, Users.ID.GtExpr(Users.ID.Div(2)), `(`+id+` > (`+id+` / $1))`, []any{int64(2)})
}

// TestTypedExpressionWrappers covers Cast, V, and SubExpr — the typed-expression
// escape hatches — by comparing a column against each.
func TestTypedExpressionWrappers(t *testing.T) {
	id := `"public"."users"."id"`
	where(t, Users.ID.GtExpr(sqlkit.Cast[int64](Users.Email, "integer")),
		`(`+id+` > CAST("public"."users"."email" AS integer))`, nil)
	where(t, Users.ID.GtExpr(sqlkit.V[int64](5)), `(`+id+` > $1)`, []any{int64(5)})
	where(t, Users.ID.GtExpr(sqlkit.SubExpr[int64](sqlkit.Select(Posts.ID).From(Posts))),
		`(`+id+` > (SELECT "public"."posts"."id" FROM "public"."posts"))`, nil)
}
