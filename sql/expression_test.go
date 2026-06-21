package sql

import (
	"strings"
	"testing"
)

func TestArrayConstructor(t *testing.T) {
	assertSQL(t,
		Select(As(Array(Int(1), Int(2), Int(3)), "xs")).From(Table{Name: NewIdent("t")}),
		`SELECT ARRAY[$1, $2, $3] AS "xs" FROM "t"`, []any{1, 2, 3})
}

func TestAnyArrayAndAllArray(t *testing.T) {
	// col = ANY(ARRAY[...]) — the IN replacement spelled with an array.
	assertSQL(t,
		Select(Col("id")).From(Table{Name: NewIdent("t")}).Where(AnyArray(Col("id"), "=", Array(Int(1), Int(2)))),
		`SELECT "id" FROM "t" WHERE ("id" = ANY(ARRAY[$1, $2]))`, []any{1, 2})

	// col <> ALL($1) — a single bound array parameter (the efficient NOT IN).
	assertSQL(t,
		Select(Col("id")).From(Table{Name: NewIdent("t")}).Where(AllArray(Col("id"), "<>", Val([]int{1, 2, 3}))),
		`SELECT "id" FROM "t" WHERE ("id" <> ALL($1))`, []any{[]int{1, 2, 3}})
}

func TestArraysRejectedOnMySQL(t *testing.T) {
	q := Select(As(Array(Int(1)), "xs")).From(Table{Name: NewIdent("t")})
	if _, _, err := q.ToSQL(MySQLDialect{}); err == nil || !strings.Contains(err.Error(), "arrays") {
		t.Fatalf("ToSQL(mysql) error = %v, want array rejection", err)
	}

	q2 := Select(Col("id")).From(Table{Name: NewIdent("t")}).Where(AnyArray(Col("id"), "=", Val([]int{1})))
	if _, _, err := q2.ToSQL(MySQLDialect{}); err == nil || !strings.Contains(err.Error(), "arrays") {
		t.Fatalf("ToSQL(mysql) error = %v, want array rejection", err)
	}
}

func TestBetween(t *testing.T) {
	q := Select(Col("id")).From(Table{Name: NewIdent("users")}).
		Where(Between(Col("age"), Int(18), Int(65)))
	assertSQL(t, q,
		`SELECT "id" FROM "users" WHERE ("age" BETWEEN $1 AND $2)`, []any{18, 65})

	q = Select(Col("id")).From(Table{Name: NewIdent("users")}).
		Where(NotBetween(Col("age"), Int(18), Int(65)))
	assertSQL(t, q,
		`SELECT "id" FROM "users" WHERE ("age" NOT BETWEEN $1 AND $2)`, []any{18, 65})
}

func TestInQueryAndExists(t *testing.T) {
	sub := Select(Col("user_id")).From(Table{Name: NewIdent("orders")}).Where(Gt(Col("total"), Int(100)))

	assertSQL(t, Select(Col("id")).From(Table{Name: NewIdent("users")}).Where(InQuery(Col("id"), sub)),
		`SELECT "id" FROM "users" WHERE ("id" IN (SELECT "user_id" FROM "orders" WHERE ("total" > $1)))`,
		[]any{100})

	assertSQL(t, Select(Col("id")).From(Table{Name: NewIdent("users")}).Where(NotInQuery(Col("id"), sub)),
		`SELECT "id" FROM "users" WHERE ("id" NOT IN (SELECT "user_id" FROM "orders" WHERE ("total" > $1)))`,
		[]any{100})

	assertSQL(t, Select(Col("id")).From(Table{Name: NewIdent("users")}).Where(Exists(sub)),
		`SELECT "id" FROM "users" WHERE EXISTS (SELECT "user_id" FROM "orders" WHERE ("total" > $1))`,
		[]any{100})

	assertSQL(t, Select(Col("id")).From(Table{Name: NewIdent("users")}).Where(NotExists(sub)),
		`SELECT "id" FROM "users" WHERE (NOT EXISTS (SELECT "user_id" FROM "orders" WHERE ("total" > $1)))`,
		[]any{100})
}

func TestScalarSubqueryProjection(t *testing.T) {
	// A correlated scalar subquery in the SELECT list: per-user order count.
	count := Select(CountAll()).From(Table{Name: NewIdent("orders")}).
		Where(Eq(Col("orders.user_id"), Col("users.id")))

	q := Select(Col("users.id"), As(ScalarSubquery(count), "order_count")).
		From(Table{Name: NewIdent("users")})

	assertSQL(t, q,
		`SELECT "users"."id", (SELECT COUNT(*) FROM "orders" WHERE ("orders"."user_id" = "users"."id")) AS "order_count" FROM "users"`,
		nil)
}

func TestScalarSubqueryInComparison(t *testing.T) {
	avg := Select(Avg(Col("salary"))).From(Table{Name: NewIdent("employees")})

	q := Select(Col("id")).From(Table{Name: NewIdent("employees")}).
		Where(Gt(Col("salary"), ScalarSubquery(avg)))

	assertSQL(t, q,
		`SELECT "id" FROM "employees" WHERE ("salary" > (SELECT AVG("salary") FROM "employees"))`,
		nil)
}

func TestScalarSubquerySharesPlaceholderNumbering(t *testing.T) {
	sub := Select(Max(Col("total"))).From(Table{Name: NewIdent("orders")}).
		Where(Eq(Col("orders.user_id"), Col("users.id"))).
		Where(Gt(Col("total"), Int(10)))

	q := Select(Col("id")).From(Table{Name: NewIdent("users")}).
		Where(Eq(Col("active"), Val(true))).
		Where(Gt(Col("score"), ScalarSubquery(sub)))

	assertSQL(t, q,
		`SELECT "id" FROM "users" WHERE (("active" = $1) AND ("score" > (SELECT MAX("total") FROM "orders" WHERE (("orders"."user_id" = "users"."id") AND ("total" > $2)))))`,
		[]any{true, 10})
}

func TestQuantifiedAnyAll(t *testing.T) {
	sub := Select(Col("amount")).From(Table{Name: NewIdent("payments")}).Where(Gt(Col("amount"), Int(0)))

	assertSQL(t, Select(Col("id")).From(Table{Name: NewIdent("users")}).Where(Any(Col("balance"), ">", sub)),
		`SELECT "id" FROM "users" WHERE ("balance" > ANY (SELECT "amount" FROM "payments" WHERE ("amount" > $1)))`,
		[]any{0})

	assertSQL(t, Select(Col("id")).From(Table{Name: NewIdent("users")}).Where(All(Col("balance"), ">=", sub)),
		`SELECT "id" FROM "users" WHERE ("balance" >= ALL (SELECT "amount" FROM "payments" WHERE ("amount" > $1)))`,
		[]any{0})
}

func TestScalarSubqueryRequiresQuery(t *testing.T) {
	q := Select(ScalarSubquery(nil)).From(Table{Name: NewIdent("users")})
	if _, _, err := q.ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("ScalarSubquery(nil) compiled, want error")
	}
}

func TestArithmeticAndCast(t *testing.T) {
	q := Select(As(Mul(Col("price"), Col("qty")), "total")).From(Table{Name: NewIdent("items")})
	assertSQL(t, q, `SELECT ("price" * "qty") AS "total" FROM "items"`, nil)

	q = Select(As(Cast(Col("flag"), "integer"), "n")).From(Table{Name: NewIdent("items")})
	assertSQL(t, q, `SELECT CAST("flag" AS integer) AS "n" FROM "items"`, nil)

	q = Select(Col("id")).From(Table{Name: NewIdent("items")}).Where(Gt(Add(Col("a"), Col("b")), Int(10)))
	assertSQL(t, q, `SELECT "id" FROM "items" WHERE (("a" + "b") > $1)`, []any{10})
}

func TestOrderByNulls(t *testing.T) {
	q := Select(Col("id")).From(Table{Name: NewIdent("users")}).
		OrderBy(Asc(Col("name")).NullsFirst(), Desc(Col("score")).NullsLast())
	assertSQL(t, q,
		`SELECT "id" FROM "users" ORDER BY "name" NULLS FIRST, "score" DESC NULLS LAST`, nil)
}

func TestNotLikeAndILike(t *testing.T) {
	assertSQL(t, Select(Col("id")).From(Table{Name: NewIdent("users")}).Where(NotLike(Col("email"), Val("%@spam.com"))),
		`SELECT "id" FROM "users" WHERE ("email" NOT LIKE $1)`, []any{"%@spam.com"})
	assertSQL(t, Select(Col("id")).From(Table{Name: NewIdent("users")}).Where(ILike(Col("email"), Val("%@EXAMPLE.com"))),
		`SELECT "id" FROM "users" WHERE ("email" ILIKE $1)`, []any{"%@EXAMPLE.com"})
}

func TestConcatPostgres(t *testing.T) {
	assertSQL(t,
		Select(As(Concat(Col("first"), Val(" "), Col("last")), "name")).From(Table{Name: NewIdent("users")}),
		`SELECT ("first" || $1 || "last") AS "name" FROM "users"`, []any{" "})
}

func TestCaseExpression(t *testing.T) {
	query := Select(
		As(Case().
			When(Ge(Col("score"), Int(90)), "A").
			When(Ge(Col("score"), Int(60)), "B").
			Else("C"), "grade"),
	).From(Table{Name: NewIdent("exams")})

	assertSQL(t, query,
		`SELECT CASE WHEN ("score" >= $1) THEN $2 WHEN ("score" >= $3) THEN $4 ELSE $5 END AS "grade" FROM "exams"`,
		[]any{90, "A", 60, "B", "C"})
}

func TestIfExpression(t *testing.T) {
	query := Select(If(Eq(Col("published"), Val(true)), Int(1), Int(0))).From(Table{Name: NewIdent("posts")})

	assertSQL(t, query,
		`SELECT CASE WHEN ("published" = $1) THEN $2 ELSE $3 END FROM "posts"`,
		[]any{true, 1, 0})
}

func TestCaseRequiresWhen(t *testing.T) {
	if _, _, err := Select(Case()).From(Table{Name: NewIdent("t")}).ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("CASE without WHEN compiled, want error")
	}
}

func TestUnaryExpression(t *testing.T) {
	// Arithmetic negation and unary plus render the operator against the
	// parenthesized operand.
	assertSQL(t, Select(As(Neg(Col("balance")), "n")).From(Table{Name: NewIdent("accounts")}),
		`SELECT (-"balance") AS "n" FROM "accounts"`, nil)

	assertSQL(t, Select(As(Pos(Col("balance")), "p")).From(Table{Name: NewIdent("accounts")}),
		`SELECT (+"balance") AS "p" FROM "accounts"`, nil)

	// A generic unary operator over a bind value.
	assertSQL(t, Select(Col("id")).From(Table{Name: NewIdent("t")}).Where(Eq(Col("x"), Neg(Int(5)))),
		`SELECT "id" FROM "t" WHERE ("x" = (-$1))`, []any{5})
}

func TestSimpleCase(t *testing.T) {
	// A simple CASE compares the operand against each WHEN value.
	expr := CaseOf(Col("status")).
		When(1, "active").
		When(2, "inactive").
		Else("unknown")
	assertSQL(t, Select(As(expr, "label")).From(Table{Name: NewIdent("users")}),
		`SELECT CASE "status" WHEN $1 THEN $2 WHEN $3 THEN $4 ELSE $5 END AS "label" FROM "users"`,
		[]any{1, "active", 2, "inactive", "unknown"})

	// A searched CASE (no operand) is unchanged.
	searched := Case().When(Gt(Col("age"), Int(18)), "adult").Else("minor")
	assertSQL(t, Select(As(searched, "k")).From(Table{Name: NewIdent("users")}),
		`SELECT CASE WHEN ("age" > $1) THEN $2 ELSE $3 END AS "k" FROM "users"`,
		[]any{18, "adult", "minor"})
}

func TestRowExpression(t *testing.T) {
	// Row-wise comparison.
	assertSQL(t, Select(Col("id")).From(Table{Name: NewIdent("t")}).
		Where(Eq(Row(Col("a"), Col("b")), Row(Int(1), Int(2)))),
		`SELECT "id" FROM "t" WHERE (("a", "b") = ($1, $2))`, []any{1, 2})

	// Row-wise IN.
	assertSQL(t, Select(Col("id")).From(Table{Name: NewIdent("t")}).
		Where(In(Row(Col("a"), Col("b")), Row(1, 2), Row(3, 4))),
		`SELECT "id" FROM "t" WHERE (("a", "b") IN (($1, $2), ($3, $4)))`, []any{1, 2, 3, 4})

	// A single-element row keeps the ROW keyword: (x) alone is a parenthesized
	// scalar, not a one-field row, so it must render as ROW(x).
	assertSQL(t, Select(Col("id")).From(Table{Name: NewIdent("t")}).
		Where(Eq(Col("a"), Row(Int(1)))),
		`SELECT "id" FROM "t" WHERE ("a" = ROW($1))`, []any{1})
}

func TestIsDistinctFrom(t *testing.T) {
	// Standard IS [NOT] DISTINCT FROM on PostgreSQL.
	assertSQL(t, Select(Col("id")).From(Table{Name: NewIdent("t")}).
		Where(IsDistinctFrom(Col("a"), Col("b"))),
		`SELECT "id" FROM "t" WHERE ("a" IS DISTINCT FROM "b")`, nil)

	assertSQL(t, Select(Col("id")).From(Table{Name: NewIdent("t")}).
		Where(IsNotDistinctFrom(Col("a"), Val(nil))),
		`SELECT "id" FROM "t" WHERE ("a" IS NOT DISTINCT FROM $1)`, []any{nil})

	// MySQL renders the null-safe <=> operator.
	assertMySQL(t, Select(Col("id")).From(Table{Name: NewIdent("t")}).
		Where(IsNotDistinctFrom(Col("a"), Col("b"))),
		"SELECT `id` FROM `t` WHERE (`a` <=> `b`)", nil)

	assertMySQL(t, Select(Col("id")).From(Table{Name: NewIdent("t")}).
		Where(IsDistinctFrom(Col("a"), Col("b"))),
		"SELECT `id` FROM `t` WHERE (NOT (`a` <=> `b`))", nil)

	// The standard target uses the standard spelling.
	assertStandard(t, Select(Col("id")).From(Table{Name: NewIdent("t")}).
		Where(IsDistinctFrom(Col("a"), Col("b"))),
		`SELECT "id" FROM "t" WHERE ("a" IS DISTINCT FROM "b")`, nil)
}

func TestRegexpPostgreSQL(t *testing.T) {
	cases := []struct {
		expr Expression
		want string
	}{
		{Regexp(Col("name"), Val("^a")), `("name" ~ $1)`},
		{NotRegexp(Col("name"), Val("^a")), `("name" !~ $1)`},
		{IRegexp(Col("name"), Val("^a")), `("name" ~* $1)`},
		{NotIRegexp(Col("name"), Val("^a")), `("name" !~* $1)`},
	}
	for _, tc := range cases {
		q := Select(Col("id")).From(Table{Name: NewIdent("t")}).Where(tc.expr)
		got, args, err := q.ToSQL(PostgreSQLDialect{})
		if err != nil {
			t.Fatalf("ToSQL() error = %v", err)
		}
		want := `SELECT "id" FROM "t" WHERE ` + tc.want
		if got != want {
			t.Errorf("ToSQL() = %q, want %q", got, want)
		}
		// left then pattern: only the pattern binds a value.
		if len(args) != 1 || args[0] != "^a" {
			t.Errorf("ToSQL() args = %#v, want [\"^a\"]", args)
		}
	}
}

func TestRegexpMySQL(t *testing.T) {
	q := Select(Col("id")).From(Table{Name: NewIdent("t")}).Where(Regexp(Col("name"), Val("^a")))
	got, _, err := q.ToSQL(MySQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if want := "SELECT `id` FROM `t` WHERE (`name` REGEXP ?)"; got != want {
		t.Errorf("ToSQL() = %q, want %q", got, want)
	}

	neg := Select(Col("id")).From(Table{Name: NewIdent("t")}).Where(NotRegexp(Col("name"), Val("^a")))
	got, _, _ = neg.ToSQL(MySQLDialect{})
	if want := "SELECT `id` FROM `t` WHERE (`name` NOT REGEXP ?)"; got != want {
		t.Errorf("ToSQL() = %q, want %q", got, want)
	}

	// MySQL cannot express a case-insensitive match inline.
	ci := Select(Col("id")).From(Table{Name: NewIdent("t")}).Where(IRegexp(Col("name"), Val("^a")))
	if _, _, err := ci.ToSQL(MySQLDialect{}); err == nil || !strings.Contains(err.Error(), "case-insensitive") {
		t.Fatalf("ToSQL() error = %v, want case-insensitive rejection", err)
	}
}

func TestRegexpStandard(t *testing.T) {
	cases := []struct {
		expr Expression
		want string
	}{
		{Regexp(Col("name"), Val("^a")), `("name" LIKE_REGEX ?)`},
		{NotRegexp(Col("name"), Val("^a")), `(NOT ("name" LIKE_REGEX ?))`},
		{IRegexp(Col("name"), Val("^a")), `("name" LIKE_REGEX ? FLAG 'i')`},
		{NotIRegexp(Col("name"), Val("^a")), `(NOT ("name" LIKE_REGEX ? FLAG 'i'))`},
	}
	for _, tc := range cases {
		q := Select(Col("id")).From(Table{Name: NewIdent("t")}).Where(tc.expr)
		got, _, err := q.ToSQL(StandardSQLDialect{})
		if err != nil {
			t.Fatalf("ToSQL() error = %v", err)
		}
		if want := `SELECT "id" FROM "t" WHERE ` + tc.want; got != want {
			t.Errorf("ToSQL() = %q, want %q", got, want)
		}
	}
}

func TestSimilarTo(t *testing.T) {
	assertSQL(t,
		Select(Col("id")).From(Table{Name: NewIdent("t")}).Where(SimilarTo(Col("name"), Val("%(foo|bar)%"))),
		`SELECT "id" FROM "t" WHERE ("name" SIMILAR TO $1)`, []any{"%(foo|bar)%"})

	assertSQL(t,
		Select(Col("id")).From(Table{Name: NewIdent("t")}).Where(NotSimilarTo(Col("name"), Val("a%"))),
		`SELECT "id" FROM "t" WHERE ("name" NOT SIMILAR TO $1)`, []any{"a%"})

	assertStandard(t,
		Select(Col("id")).From(Table{Name: NewIdent("t")}).Where(SimilarTo(Col("name"), Val("a%"))),
		`SELECT "id" FROM "t" WHERE ("name" SIMILAR TO ?)`, []any{"a%"})
}

func TestSimilarToRejectedOnMySQL(t *testing.T) {
	q := Select(Col("id")).From(Table{Name: NewIdent("t")}).Where(SimilarTo(Col("name"), Val("a%")))
	if _, _, err := q.ToSQL(MySQLDialect{}); err == nil || !strings.Contains(err.Error(), "SIMILAR TO") {
		t.Fatalf("ToSQL(mysql) error = %v, want SIMILAR TO rejection", err)
	}
}

// TestComparisonAndArithmeticHelpers covers the free comparison/arithmetic
// helpers and a few small expression accessors.
func TestComparisonAndArithmeticHelpers(t *testing.T) {
	assertSQL(t, Select(Lt(Col("a"), Val(1))).From(orgs), `SELECT ("a" < $1) FROM "orgs"`, []any{1})
	assertSQL(t, Select(Le(Col("a"), Val(1))).From(orgs), `SELECT ("a" <= $1) FROM "orgs"`, []any{1})
	assertSQL(t, Select(Sub(Col("a"), Val(1))).From(orgs), `SELECT ("a" - $1) FROM "orgs"`, []any{1})
	assertSQL(t, Select(Div(Col("a"), Val(1))).From(orgs), `SELECT ("a" / $1) FROM "orgs"`, []any{1})
	assertSQL(t, Select(Mod(Col("a"), Val(1))).From(orgs), `SELECT ("a" % $1) FROM "orgs"`, []any{1})

	// NotIn renders NOT IN as a WHERE predicate.
	assertSQL(t, Select(Col("a")).From(orgs).Where(NotIn(Col("a"), 1, 2)),
		`SELECT "a" FROM "orgs" WHERE ("a" NOT IN ($1, $2))`, []any{1, 2})

	// A raw expression reports NoPos.
	if Raw("x").Pos() != NoPos {
		t.Fatalf("Raw.Pos() = %d, want NoPos", Raw("x").Pos())
	}

	// NamedWindow.Frame sets a raw frame clause.
	w := WindowDef("w").PartitionBy(Col("a")).Frame("ROWS BETWEEN 1 PRECEDING AND CURRENT ROW")
	assertSQL(t, Select(Over(Func("sum", Col("x"))).Named("w")).From(orgs).Window(w),
		`SELECT sum("x") OVER "w" FROM "orgs" WINDOW "w" AS (PARTITION BY "a" ROWS BETWEEN 1 PRECEDING AND CURRENT ROW)`, nil)
}

// TestLogicalSingleOperand covers And/Or with a single operand (returned as-is).
func TestLogicalSingleOperand(t *testing.T) {
	if _, ok := And(Col("a")).(ColumnExpression); !ok {
		t.Fatal("And with one operand should return it unchanged")
	}
	if _, ok := Or(Col("a")).(ColumnExpression); !ok {
		t.Fatal("Or with one operand should return it unchanged")
	}
}
