package sql_test

import (
	"testing"

	. "github.com/aita/sqlkit/sql"
)

func TestBetween(t *testing.T) {
	q := Select(Col("id")).From(Table{Name: "users"}).
		Where(Between(Col("age"), Int(18), Int(65)))
	assertSQL(t, q,
		`SELECT "id" FROM "users" WHERE ("age" BETWEEN $1 AND $2)`, []any{18, 65})

	q = Select(Col("id")).From(Table{Name: "users"}).
		Where(NotBetween(Col("age"), Int(18), Int(65)))
	assertSQL(t, q,
		`SELECT "id" FROM "users" WHERE ("age" NOT BETWEEN $1 AND $2)`, []any{18, 65})
}

func TestInQueryAndExists(t *testing.T) {
	sub := Select(Col("user_id")).From(Table{Name: "orders"}).Where(Gt(Col("total"), Int(100)))

	assertSQL(t, Select(Col("id")).From(Table{Name: "users"}).Where(InQuery(Col("id"), sub)),
		`SELECT "id" FROM "users" WHERE ("id" IN (SELECT "user_id" FROM "orders" WHERE ("total" > $1)))`,
		[]any{100})

	assertSQL(t, Select(Col("id")).From(Table{Name: "users"}).Where(NotInQuery(Col("id"), sub)),
		`SELECT "id" FROM "users" WHERE ("id" NOT IN (SELECT "user_id" FROM "orders" WHERE ("total" > $1)))`,
		[]any{100})

	assertSQL(t, Select(Col("id")).From(Table{Name: "users"}).Where(Exists(sub)),
		`SELECT "id" FROM "users" WHERE EXISTS (SELECT "user_id" FROM "orders" WHERE ("total" > $1))`,
		[]any{100})

	assertSQL(t, Select(Col("id")).From(Table{Name: "users"}).Where(NotExists(sub)),
		`SELECT "id" FROM "users" WHERE (NOT EXISTS (SELECT "user_id" FROM "orders" WHERE ("total" > $1)))`,
		[]any{100})
}

func TestNotLikeAndILike(t *testing.T) {
	assertSQL(t, Select(Col("id")).From(Table{Name: "users"}).Where(NotLike(Col("email"), Val("%@spam.com"))),
		`SELECT "id" FROM "users" WHERE ("email" NOT LIKE $1)`, []any{"%@spam.com"})
	assertSQL(t, Select(Col("id")).From(Table{Name: "users"}).Where(ILike(Col("email"), Val("%@EXAMPLE.com"))),
		`SELECT "id" FROM "users" WHERE ("email" ILIKE $1)`, []any{"%@EXAMPLE.com"})
}

func TestJSONFunctions(t *testing.T) {
	assertSQL(t,
		Select(As(JSONArray(Col("id"), Col("email")), "j")).From(Table{Name: "users"}),
		`SELECT JSON_ARRAY("id", "email") AS "j" FROM "users"`, nil)

	assertSQL(t,
		Select(As(JSONObject("id", Col("id"), "name", Col("name")), "j")).From(Table{Name: "users"}),
		`SELECT JSON_OBJECT($1, "id", $2, "name") AS "j" FROM "users"`, []any{"id", "name"})

	assertSQL(t,
		Select(As(JSONArrayAgg(Col("id")), "ids")).From(Table{Name: "users"}),
		`SELECT JSON_ARRAYAGG("id") AS "ids" FROM "users"`, nil)

	assertSQL(t,
		Select(As(JSONObjectAgg(Col("k"), Col("v")), "obj")).From(Table{Name: "kv"}),
		`SELECT JSON_OBJECTAGG("k", "v") AS "obj" FROM "kv"`, nil)
}

func TestConcatPostgres(t *testing.T) {
	assertSQL(t,
		Select(As(Concat(Col("first"), Val(" "), Col("last")), "name")).From(Table{Name: "users"}),
		`SELECT ("first" || $1 || "last") AS "name" FROM "users"`, []any{" "})
}

func TestJSONAccessPostgres(t *testing.T) {
	// Object key, returning json (->).
	assertSQL(t,
		Select(As(JSONGet(Col("data"), "name"), "n")).From(Table{Name: "events"}),
		`SELECT ("data" -> 'name') AS "n" FROM "events"`, nil)

	// Nested path with an array index, text form (->> on the last step).
	assertSQL(t,
		Select(As(JSONGetText(Col("data"), "items", 0, "id"), "id")).From(Table{Name: "events"}),
		`SELECT ((("data" -> 'items') -> 0) ->> 'id') AS "id" FROM "events"`, nil)

	// As text it composes with a string comparison.
	assertSQL(t,
		Select(Col("id")).From(Table{Name: "events"}).
			Where(Eq(JSONGetText(Col("data"), "type"), Val("click"))),
		`SELECT "id" FROM "events" WHERE (("data" ->> 'type') = $1)`, []any{"click"})
}

func TestJSONAccessRejectsBadStep(t *testing.T) {
	_, _, err := Select(JSONGet(Col("data"), 1.5)).From(Table{Name: "events"}).ToSQL(PostgreSQLDialect{})
	if err == nil {
		t.Fatal("expected error for non-string/int path step")
	}
}

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

func TestIntersectAndExceptChained(t *testing.T) {
	a := Select(Col("id")).From(Table{Name: "a"})
	b := Select(Col("id")).From(Table{Name: "b"})
	c := Select(Col("id")).From(Table{Name: "c"})

	assertSQL(t, a.Intersect(b).Intersect(c),
		`(SELECT "id" FROM "a") INTERSECT (SELECT "id" FROM "b") INTERSECT (SELECT "id" FROM "c")`, nil)
	assertSQL(t, a.Except(b).Except(c),
		`(SELECT "id" FROM "a") EXCEPT (SELECT "id" FROM "b") EXCEPT (SELECT "id" FROM "c")`, nil)
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
