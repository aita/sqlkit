package sql_test

import (
	"testing"

	. "github.com/aita/sqlkit/sql"
)

func TestUnaryExpression(t *testing.T) {
	// Arithmetic negation and unary plus render the operator against the
	// parenthesized operand.
	assertSQL(t, Select(As(Neg(Col("balance")), "n")).From(Table{Name: "accounts"}),
		`SELECT (-"balance") AS "n" FROM "accounts"`, nil)

	assertSQL(t, Select(As(Pos(Col("balance")), "p")).From(Table{Name: "accounts"}),
		`SELECT (+"balance") AS "p" FROM "accounts"`, nil)

	// A generic unary operator over a bind value.
	assertSQL(t, Select(Col("id")).From(Table{Name: "t"}).Where(Eq(Col("x"), Neg(Int(5)))),
		`SELECT "id" FROM "t" WHERE ("x" = (-$1))`, []any{5})
}

func TestSimpleCase(t *testing.T) {
	// A simple CASE compares the operand against each WHEN value.
	expr := CaseOf(Col("status")).
		When(1, "active").
		When(2, "inactive").
		Else("unknown")
	assertSQL(t, Select(As(expr, "label")).From(Table{Name: "users"}),
		`SELECT CASE "status" WHEN $1 THEN $2 WHEN $3 THEN $4 ELSE $5 END AS "label" FROM "users"`,
		[]any{1, "active", 2, "inactive", "unknown"})

	// A searched CASE (no operand) is unchanged.
	searched := Case().When(Gt(Col("age"), Int(18)), "adult").Else("minor")
	assertSQL(t, Select(As(searched, "k")).From(Table{Name: "users"}),
		`SELECT CASE WHEN ("age" > $1) THEN $2 ELSE $3 END AS "k" FROM "users"`,
		[]any{18, "adult", "minor"})
}

func TestDistinctAggregates(t *testing.T) {
	assertSQL(t, Select(CountDistinct(Col("country"))).From(Table{Name: "users"}),
		`SELECT COUNT(DISTINCT "country") FROM "users"`, nil)

	assertSQL(t, Select(DistinctFunc("SUM", Col("amount"))).From(Table{Name: "orders"}),
		`SELECT SUM(DISTINCT "amount") FROM "orders"`, nil)

	// A DISTINCT aggregate keeps its name on a dialect that remaps scalar
	// functions, since the remap is for scalars only.
	assertMySQL(t, Select(CountDistinct(Col("country"))).From(Table{Name: "users"}),
		"SELECT COUNT(DISTINCT `country`) FROM `users`", nil)
}

func TestRowExpression(t *testing.T) {
	// Row-wise comparison.
	assertSQL(t, Select(Col("id")).From(Table{Name: "t"}).
		Where(Eq(Row(Col("a"), Col("b")), Row(Int(1), Int(2)))),
		`SELECT "id" FROM "t" WHERE (("a", "b") = ($1, $2))`, []any{1, 2})

	// Row-wise IN.
	assertSQL(t, Select(Col("id")).From(Table{Name: "t"}).
		Where(In(Row(Col("a"), Col("b")), Row(1, 2), Row(3, 4))),
		`SELECT "id" FROM "t" WHERE (("a", "b") IN (($1, $2), ($3, $4)))`, []any{1, 2, 3, 4})
}

func TestIsDistinctFrom(t *testing.T) {
	// Standard IS [NOT] DISTINCT FROM on PostgreSQL.
	assertSQL(t, Select(Col("id")).From(Table{Name: "t"}).
		Where(IsDistinctFrom(Col("a"), Col("b"))),
		`SELECT "id" FROM "t" WHERE ("a" IS DISTINCT FROM "b")`, nil)

	assertSQL(t, Select(Col("id")).From(Table{Name: "t"}).
		Where(IsNotDistinctFrom(Col("a"), Val(nil))),
		`SELECT "id" FROM "t" WHERE ("a" IS NOT DISTINCT FROM $1)`, []any{nil})

	// MySQL renders the null-safe <=> operator.
	assertMySQL(t, Select(Col("id")).From(Table{Name: "t"}).
		Where(IsNotDistinctFrom(Col("a"), Col("b"))),
		"SELECT `id` FROM `t` WHERE (`a` <=> `b`)", nil)

	assertMySQL(t, Select(Col("id")).From(Table{Name: "t"}).
		Where(IsDistinctFrom(Col("a"), Col("b"))),
		"SELECT `id` FROM `t` WHERE (NOT (`a` <=> `b`))", nil)
}

func TestNewExpressionsStandardDialect(t *testing.T) {
	// The standard target renders the standard spellings.
	assertStandard(t, Select(Col("id")).From(Table{Name: "t"}).
		Where(IsDistinctFrom(Col("a"), Col("b"))),
		`SELECT "id" FROM "t" WHERE ("a" IS DISTINCT FROM "b")`, nil)
}
