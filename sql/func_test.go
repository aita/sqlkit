package sql

import "testing"

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

func TestNamedWindowClause(t *testing.T) {
	// Two aggregates reference one window defined in the WINDOW clause.
	q := Select(
		As(Over(Sum(Col("score"))).Named("w"), "running_total"),
		As(Over(Avg(Col("score"))).Named("w"), "running_avg"),
	).From(Table{Name: "games"}).
		Window(WindowDef("w").PartitionBy(Col("team")).OrderBy(Asc(Col("played_at"))))

	assertSQL(t, q,
		`SELECT SUM("score") OVER "w" AS "running_total", AVG("score") OVER "w" AS "running_avg" `+
			`FROM "games" WINDOW "w" AS (PARTITION BY "team" ORDER BY "played_at")`,
		nil)
}

func TestNamedWindowExtended(t *testing.T) {
	// A reference may extend the named base window with extra clauses.
	q := Select(
		As(Over(RowNumber()).Named("w").OrderBy(Desc(Col("score"))), "rn"),
	).From(Table{Name: "games"}).
		Window(WindowDef("w").PartitionBy(Col("team")))

	assertSQL(t, q,
		`SELECT ROW_NUMBER() OVER ("w" ORDER BY "score" DESC) AS "rn" FROM "games" WINDOW "w" AS (PARTITION BY "team")`,
		nil)
}
