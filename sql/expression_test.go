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

func TestScalarSubqueryProjection(t *testing.T) {
	// A correlated scalar subquery in the SELECT list: per-user order count.
	count := Select(CountAll()).From(Table{Name: "orders"}).
		Where(Eq(Col("orders.user_id"), Col("users.id")))

	q := Select(Col("users.id"), As(ScalarSubquery(count), "order_count")).
		From(Table{Name: "users"})

	assertSQL(t, q,
		`SELECT "users"."id", (SELECT COUNT(*) FROM "orders" WHERE ("orders"."user_id" = "users"."id")) AS "order_count" FROM "users"`,
		nil)
}

func TestScalarSubqueryInComparison(t *testing.T) {
	avg := Select(Avg(Col("salary"))).From(Table{Name: "employees"})

	q := Select(Col("id")).From(Table{Name: "employees"}).
		Where(Gt(Col("salary"), ScalarSubquery(avg)))

	assertSQL(t, q,
		`SELECT "id" FROM "employees" WHERE ("salary" > (SELECT AVG("salary") FROM "employees"))`,
		nil)
}

func TestScalarSubquerySharesPlaceholderNumbering(t *testing.T) {
	sub := Select(Max(Col("total"))).From(Table{Name: "orders"}).
		Where(Eq(Col("orders.user_id"), Col("users.id"))).
		Where(Gt(Col("total"), Int(10)))

	q := Select(Col("id")).From(Table{Name: "users"}).
		Where(Eq(Col("active"), Val(true))).
		Where(Gt(Col("score"), ScalarSubquery(sub)))

	assertSQL(t, q,
		`SELECT "id" FROM "users" WHERE (("active" = $1) AND ("score" > (SELECT MAX("total") FROM "orders" WHERE (("orders"."user_id" = "users"."id") AND ("total" > $2)))))`,
		[]any{true, 10})
}

func TestQuantifiedAnyAll(t *testing.T) {
	sub := Select(Col("amount")).From(Table{Name: "payments"}).Where(Gt(Col("amount"), Int(0)))

	assertSQL(t, Select(Col("id")).From(Table{Name: "users"}).Where(Any(Col("balance"), ">", sub)),
		`SELECT "id" FROM "users" WHERE ("balance" > ANY (SELECT "amount" FROM "payments" WHERE ("amount" > $1)))`,
		[]any{0})

	assertSQL(t, Select(Col("id")).From(Table{Name: "users"}).Where(All(Col("balance"), ">=", sub)),
		`SELECT "id" FROM "users" WHERE ("balance" >= ALL (SELECT "amount" FROM "payments" WHERE ("amount" > $1)))`,
		[]any{0})
}

func TestScalarSubqueryRequiresQuery(t *testing.T) {
	q := Select(ScalarSubquery(nil)).From(Table{Name: "users"})
	if _, _, err := q.ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("ScalarSubquery(nil) compiled, want error")
	}
}

func TestArithmeticAndCast(t *testing.T) {
	q := Select(As(Mul(Col("price"), Col("qty")), "total")).From(Table{Name: "items"})
	assertSQL(t, q, `SELECT ("price" * "qty") AS "total" FROM "items"`, nil)

	q = Select(As(Cast(Col("flag"), "integer"), "n")).From(Table{Name: "items"})
	assertSQL(t, q, `SELECT CAST("flag" AS integer) AS "n" FROM "items"`, nil)

	q = Select(Col("id")).From(Table{Name: "items"}).Where(Gt(Add(Col("a"), Col("b")), Int(10)))
	assertSQL(t, q, `SELECT "id" FROM "items" WHERE (("a" + "b") > $1)`, []any{10})
}

func TestOuterAndCrossJoins(t *testing.T) {
	base := Select(Col("u.id")).From(SourceAlias(Table{Name: "users"}, "u"))

	assertSQL(t, base.RightJoin(SourceAlias(Table{Name: "orgs"}, "o"), Eq(Col("u.org_id"), Col("o.id"))),
		`SELECT "u"."id" FROM "users" AS "u" RIGHT JOIN "orgs" AS "o" ON ("u"."org_id" = "o"."id")`, nil)

	assertSQL(t, base.FullJoin(SourceAlias(Table{Name: "orgs"}, "o"), Eq(Col("u.org_id"), Col("o.id"))),
		`SELECT "u"."id" FROM "users" AS "u" FULL JOIN "orgs" AS "o" ON ("u"."org_id" = "o"."id")`, nil)

	assertSQL(t, base.CrossJoin(SourceAlias(Table{Name: "orgs"}, "o")),
		`SELECT "u"."id" FROM "users" AS "u" CROSS JOIN "orgs" AS "o"`, nil)
}

func TestUpdateFromAndDeleteUsing(t *testing.T) {
	upd := Update(Table{Name: "users"}).
		Set(Set("name", Col("o.name"))).
		From(SourceAlias(Table{Name: "orgs"}, "o")).
		Where(Eq(Col("users.org_id"), Col("o.id")))
	assertSQL(t, upd,
		`UPDATE "users" SET "name" = "o"."name" FROM "orgs" AS "o" WHERE ("users"."org_id" = "o"."id")`, nil)

	del := DeleteFrom(Table{Name: "users"}).
		Using(SourceAlias(Table{Name: "orgs"}, "o")).
		Where(And(Eq(Col("users.org_id"), Col("o.id")), Eq(Col("o.active"), Val(false))))
	assertSQL(t, del,
		`DELETE FROM "users" USING "orgs" AS "o" WHERE (("users"."org_id" = "o"."id") AND ("o"."active" = $1))`,
		[]any{false})
}

func TestLateralJoin(t *testing.T) {
	recent := Select(Col("p.id")).From(SourceAlias(Table{Name: "posts"}, "p")).
		Where(Eq(Col("p.user_id"), Col("u.id"))).Limit(1)

	q := Select(Col("u.id"), Col("r.id")).
		From(SourceAlias(Table{Name: "users"}, "u")).
		Join(Lateral(Alias(recent, "r")), Raw("true"))

	assertSQL(t, q,
		`SELECT "u"."id", "r"."id" FROM "users" AS "u" JOIN LATERAL (SELECT "p"."id" FROM "posts" AS "p" WHERE ("p"."user_id" = "u"."id") LIMIT 1) AS "r" ON true`,
		nil)
}

func TestOrderByNulls(t *testing.T) {
	q := Select(Col("id")).From(Table{Name: "users"}).
		OrderBy(Asc(Col("name")).NullsFirst(), Desc(Col("score")).NullsLast())
	assertSQL(t, q,
		`SELECT "id" FROM "users" ORDER BY "name" NULLS FIRST, "score" DESC NULLS LAST`, nil)
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

	// PostgreSQL's SQL/JSON JSON_OBJECT requires the "key VALUE value" separator.
	assertSQL(t,
		Select(As(JSONObject("id", Col("id"), "name", Col("name")), "j")).From(Table{Name: "users"}),
		`SELECT JSON_OBJECT($1 VALUE "id", $2 VALUE "name") AS "j" FROM "users"`, []any{"id", "name"})

	assertSQL(t,
		Select(As(JSONArrayAgg(Col("id")), "ids")).From(Table{Name: "users"}),
		`SELECT JSON_ARRAYAGG("id") AS "ids" FROM "users"`, nil)

	assertSQL(t,
		Select(As(JSONObjectAgg(Col("k"), Col("v")), "obj")).From(Table{Name: "kv"}),
		`SELECT JSON_OBJECTAGG("k" VALUE "v") AS "obj" FROM "kv"`, nil)
}

func TestSetOpPrecedenceMixed(t *testing.T) {
	a := Select(Col("id")).From(Table{Name: "a"})
	b := Select(Col("id")).From(Table{Name: "b"})
	cq := Select(Col("id")).From(Table{Name: "c"})

	// (a UNION b) INTERSECT c must keep the UNION grouped, since INTERSECT binds
	// tighter; without parens PostgreSQL would read a UNION (b INTERSECT c).
	assertSQL(t, a.Union(b).Intersect(cq),
		`((SELECT "id" FROM "a") UNION (SELECT "id" FROM "b")) INTERSECT (SELECT "id" FROM "c")`, nil)

	// Same-operator chains stay unparenthesized.
	assertSQL(t, a.Union(b).Union(cq),
		`(SELECT "id" FROM "a") UNION (SELECT "id" FROM "b") UNION (SELECT "id" FROM "c")`, nil)
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

func TestScalarStringFunctions(t *testing.T) {
	query := Select(
		Lower(Col("name")),
		Upper(Col("name")),
		CharLength(Col("name")),
		Trim(Col("name")),
		Replace(Col("name"), "_", " "),
		Substring(Col("name"), 1, 3),
		Lpad(Col("code"), 6, "0"),
	).From(Table{Name: "t"})

	assertSQL(t, query,
		`SELECT LOWER("name"), UPPER("name"), CHAR_LENGTH("name"), TRIM("name"), REPLACE("name", $1, $2), SUBSTRING("name", $3, $4), LPAD("code", $5, $6) FROM "t"`,
		[]any{"_", " ", 1, 3, 6, "0"})
}

func TestScalarStringFunctionsMore(t *testing.T) {
	query := Select(
		ConcatWs("-", Col("area"), Col("number")),
		Md5(Col("email")),
		Ascii(Col("code")),
		Reverse(Col("name")),
		Lpad(Col("code"), 6, "0"),
	).From(Table{Name: "t"})

	assertSQL(t, query,
		`SELECT CONCAT_WS($1, "area", "number"), MD5("email"), ASCII("code"), REVERSE("name"), LPAD("code", $2, $3) FROM "t"`,
		[]any{"-", 6, "0"})
}

func TestScalarNumericFunctions(t *testing.T) {
	query := Select(
		Abs(Col("balance")),
		Ceil(Col("rate")),
		Floor(Col("rate")),
		Round(Col("rate"), 2),
		Power(Col("base"), Int(2)),
		Sqrt(Col("area")),
		Sign(Col("delta")),
	).From(Table{Name: "t"})

	assertSQL(t, query,
		`SELECT ABS("balance"), CEIL("rate"), FLOOR("rate"), ROUND("rate", $1), POWER("base", $2), SQRT("area"), SIGN("delta") FROM "t"`,
		[]any{2, 2})
}

func TestTrigonometricFunctions(t *testing.T) {
	query := Select(
		Pi(),
		Sin(Col("x")),
		Cos(Col("x")),
		Atan2(Col("y"), Col("x")),
		Degrees(Col("rad")),
		Radians(Col("deg")),
	).From(Table{Name: "t"})

	assertSQL(t, query,
		`SELECT PI(), SIN("x"), COS("x"), ATAN2("y", "x"), DEGREES("rad"), RADIANS("deg") FROM "t"`,
		nil)
}

func TestStatisticalAggregates(t *testing.T) {
	query := Select(
		StdDev(Col("v")),
		StdDevPop(Col("v")),
		Variance(Col("v")),
		VarSamp(Col("v")),
		BitOr(Col("flags")),
	).From(Table{Name: "samples"}).GroupBy(Col("g"))

	assertSQL(t, query,
		`SELECT STDDEV("v"), STDDEV_POP("v"), VARIANCE("v"), VAR_SAMP("v"), BIT_OR("flags") FROM "samples" GROUP BY "g"`,
		nil)
}

func TestMoreWindowFunctions(t *testing.T) {
	query := Select(
		As(PercentRank().Over().OrderBy(Asc(Col("score"))), "pr"),
		As(CumeDist().Over().OrderBy(Asc(Col("score"))), "cd"),
		As(Ntile(4).Over().OrderBy(Desc(Col("score"))), "quartile"),
		FirstValue(Col("name")).Over().PartitionBy(Col("team")).OrderBy(Asc(Col("score"))),
		NthValue(Col("name"), 2).Over().PartitionBy(Col("team")),
	).From(Table{Name: "players"})

	assertSQL(t, query,
		`SELECT PERCENT_RANK() OVER (ORDER BY "score") AS "pr", CUME_DIST() OVER (ORDER BY "score") AS "cd", NTILE($1) OVER (ORDER BY "score" DESC) AS "quartile", FIRST_VALUE("name") OVER (PARTITION BY "team" ORDER BY "score"), NTH_VALUE("name", $2) OVER (PARTITION BY "team") FROM "players"`,
		[]any{4, 2})
}

func TestDateTimeFunctions(t *testing.T) {
	query := Select(
		Now(),
		As(CurrentDate(), "today"),
		CurrentTimestamp(),
		As(Extract("year", Col("created_at")), "yr"),
	).From(Table{Name: "events"})

	assertSQL(t, query,
		`SELECT NOW(), CURRENT_DATE AS "today", CURRENT_TIMESTAMP, EXTRACT(year FROM "created_at") AS "yr" FROM "events"`,
		nil)
}

func TestPositionExpression(t *testing.T) {
	query := Select(As(Position("@", Col("email")), "at")).From(Table{Name: "users"})

	assertSQL(t, query,
		`SELECT POSITION($1 IN "email") AS "at" FROM "users"`,
		[]any{"@"})
}

func TestStringAggPostgres(t *testing.T) {
	query := Select(As(StringAgg(Col("name"), ", "), "names")).
		From(Table{Name: "users"}).
		GroupBy(Col("org_id"))

	assertSQL(t, query,
		`SELECT STRING_AGG("name", ', ') AS "names" FROM "users" GROUP BY "org_id"`,
		nil)
}

func TestExtractRequiresField(t *testing.T) {
	if _, _, err := Select(ExtractExpression{Operand: Col("x")}).From(Table{Name: "t"}).ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("EXTRACT without a field compiled, want error")
	}
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

func TestIntersectAndExceptAll(t *testing.T) {
	a := Select(Col("id")).From(Table{Name: "a"})
	b := Select(Col("id")).From(Table{Name: "b"})

	assertSQL(t, a.IntersectAll(b), `(SELECT "id" FROM "a") INTERSECT ALL (SELECT "id" FROM "b")`, nil)
	assertSQL(t, a.ExceptAll(b), `(SELECT "id" FROM "a") EXCEPT ALL (SELECT "id" FROM "b")`, nil)
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
