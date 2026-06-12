package sql_test

import (
	"testing"

	. "github.com/sqlkit-dev/sqlkit/sqlkit/sql"
)

func TestCaseExpression(t *testing.T) {
	query := Select(
		As(Case().
			When(Ge(Col("score"), Int(90)), "A").
			When(Ge(Col("score"), Int(60)), "B").
			Else("C"), "grade"),
	).From(Table{Name: "exams"})

	assertSQL(t, query,
		`SELECT CASE WHEN ("score" >= $1) THEN $2 WHEN ("score" >= $3) THEN $4 ELSE $5 END AS "grade" FROM "exams"`,
		[]any{90, "A", 60, "B", "C"})
}

func TestIfExpression(t *testing.T) {
	query := Select(If(Eq(Col("published"), Val(true)), Int(1), Int(0))).From(Table{Name: "posts"})

	assertSQL(t, query,
		`SELECT CASE WHEN ("published" = $1) THEN $2 ELSE $3 END FROM "posts"`,
		[]any{true, 1, 0})
}

func TestCaseRequiresWhen(t *testing.T) {
	if _, _, err := Select(Case()).From(Table{Name: "t"}).ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("CASE without WHEN compiled, want error")
	}
}

func TestFunctionExpressions(t *testing.T) {
	query := Select(
		Coalesce(Col("name"), "anonymous"),
		NullIf(Col("a"), Col("b")),
		Greatest(Col("x"), Int(0)),
	).From(Table{Name: "t"})

	assertSQL(t, query,
		`SELECT COALESCE("name", $1), NULLIF("a", "b"), GREATEST("x", $2) FROM "t"`,
		[]any{"anonymous", 0})
}

func TestAggregatesWithGroupByHaving(t *testing.T) {
	query := Select(
		Col("org_id"),
		As(Sum(Col("amount")), "total"),
		Avg(Col("amount")),
		Min(Col("amount")),
		Max(Col("amount")),
	).
		From(Table{Name: "payments"}).
		GroupBy(Col("org_id")).
		Having(Gt(Sum(Col("amount")), Int(1000)))

	assertSQL(t, query,
		`SELECT "org_id", SUM("amount") AS "total", AVG("amount"), MIN("amount"), MAX("amount") FROM "payments" GROUP BY "org_id" HAVING (SUM("amount") > $1)`,
		[]any{1000})
}

func TestHavingAcceptsCount(t *testing.T) {
	query := Select(Col("org_id")).
		From(Table{Name: "users"}).
		GroupBy(Col("org_id")).
		Having(Gt(CountAll(), Int(10)))

	assertSQL(t, query,
		`SELECT "org_id" FROM "users" GROUP BY "org_id" HAVING (COUNT(*) > $1)`,
		[]any{10})
}

func TestWindowFunctions(t *testing.T) {
	query := Select(
		Col("id"),
		As(RowNumber().Over().PartitionBy(Col("user_id")).OrderBy(Desc(Col("created_at"))), "rn"),
		Rank().Over().OrderBy(Asc(Col("score"))),
		Sum(Col("amount")).Over().PartitionBy(Col("user_id")),
		CountAll().Over(),
	).From(Table{Name: "posts"})

	assertSQL(t, query,
		`SELECT "id", ROW_NUMBER() OVER (PARTITION BY "user_id" ORDER BY "created_at" DESC) AS "rn", RANK() OVER (ORDER BY "score"), SUM("amount") OVER (PARTITION BY "user_id"), COUNT(*) OVER () FROM "posts"`,
		nil)
}

func TestWindowFrame(t *testing.T) {
	query := Select(
		Sum(Col("amount")).Over().
			PartitionBy(Col("user_id")).
			OrderBy(Asc(Col("id"))).
			Frame("ROWS BETWEEN 1 PRECEDING AND CURRENT ROW"),
	).From(Table{Name: "payments"})

	assertSQL(t, query,
		`SELECT SUM("amount") OVER (PARTITION BY "user_id" ORDER BY "id" ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) FROM "payments"`,
		nil)
}

func TestWindowInWhereViaSubquery(t *testing.T) {
	ranked := Select(
		Col("t.id"),
		As(RowNumber().Over().PartitionBy(Col("t.user_id")).OrderBy(Desc(Col("t.id"))), "rn"),
	).From(SourceAlias(Table{Name: "posts"}, "t"))

	query := Select(Col("r.id")).
		From(Alias(ranked, "r")).
		Where(Eq(Col("r.rn"), Int(1)))

	assertSQL(t, query,
		`SELECT "r"."id" FROM (SELECT "t"."id", ROW_NUMBER() OVER (PARTITION BY "t"."user_id" ORDER BY "t"."id" DESC) AS "rn" FROM "posts" AS "t") AS "r" WHERE ("r"."rn" = $1)`,
		[]any{1})
}

func TestUnion(t *testing.T) {
	a := Select(Col("id")).From(Table{Name: "users"}).Where(Eq(Col("active"), Val(true)))
	b := Select(Col("id")).From(Table{Name: "admins"})

	query := a.Union(b).OrderBy(Asc(Col("id"))).Limit(10).Offset(5)

	assertSQL(t, query,
		`(SELECT "id" FROM "users" WHERE ("active" = $1)) UNION (SELECT "id" FROM "admins") ORDER BY "id" LIMIT 10 OFFSET 5`,
		[]any{true})
}

func TestUnionAllChained(t *testing.T) {
	a := Select(Col("id")).From(Table{Name: "a"})
	b := Select(Col("id")).From(Table{Name: "b"})
	c := Select(Col("id")).From(Table{Name: "c"})

	query := a.UnionAll(b).UnionAll(c)

	assertSQL(t, query,
		`(SELECT "id" FROM "a") UNION ALL (SELECT "id" FROM "b") UNION ALL (SELECT "id" FROM "c")`,
		nil)
}

func TestIntersectAndExcept(t *testing.T) {
	a := Select(Col("id")).From(Table{Name: "a"})
	b := Select(Col("id")).From(Table{Name: "b"})

	assertSQL(t, a.Intersect(b), `(SELECT "id" FROM "a") INTERSECT (SELECT "id" FROM "b")`, nil)
	assertSQL(t, a.Except(b), `(SELECT "id" FROM "a") EXCEPT (SELECT "id" FROM "b")`, nil)
}

func TestUnionAsSubquery(t *testing.T) {
	a := Select(Col("id")).From(Table{Name: "a"})
	b := Select(Col("id")).From(Table{Name: "b"})

	query := Select(CountAll()).From(Alias(a.Union(b), "u"))

	assertSQL(t, query,
		`SELECT COUNT(*) FROM ((SELECT "id" FROM "a") UNION (SELECT "id" FROM "b")) AS "u"`,
		nil)
}

func TestUnionPlaceholderNumbering(t *testing.T) {
	a := Select(Col("id")).From(Table{Name: "a"}).Where(Eq(Col("x"), Int(1)))
	b := Select(Col("id")).From(Table{Name: "b"}).Where(Eq(Col("y"), Int(2)))

	assertSQL(t, a.Union(b),
		`(SELECT "id" FROM "a" WHERE ("x" = $1)) UNION (SELECT "id" FROM "b" WHERE ("y" = $2))`,
		[]any{1, 2})
}

func TestSelectDistinct(t *testing.T) {
	query := Select(Col("email")).Distinct().From(Table{Name: "users"})

	assertSQL(t, query, `SELECT DISTINCT "email" FROM "users"`, nil)
}
