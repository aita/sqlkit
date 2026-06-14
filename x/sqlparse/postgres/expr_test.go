package postgres_test

import "testing"

func TestExprComparisons(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a <> 1 AND b <= 2 AND c >= 3",
		`SELECT "id" FROM "t" WHERE (("a" <> $1) AND ("b" <= $2) AND ("c" >= $3))`,
		1, 2, 3)
}

func TestExprArithmetic(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a + 1 = b * 2",
		`SELECT "id" FROM "t" WHERE (("a" + $1) = ("b" * $2))`,
		1, 2)
}

func TestExprOr(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a = 1 OR b = 2",
		`SELECT "id" FROM "t" WHERE (("a" = $1) OR ("b" = $2))`,
		1, 2)
}

func TestExprNot(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE NOT a = 1",
		`SELECT "id" FROM "t" WHERE (NOT ("a" = $1))`,
		1)
}

func TestExprLike(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE name LIKE 'a%' AND email NOT LIKE '%x'",
		`SELECT "id" FROM "t" WHERE (("name" LIKE $1) AND ("email" NOT LIKE $2))`,
		"a%", "%x")
}

func TestExprILike(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE name ILIKE 'a%'",
		`SELECT "id" FROM "t" WHERE ("name" ILIKE $1)`,
		"a%")
}

func TestExprIn(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE id IN (1, 2, 3)",
		`SELECT "id" FROM "t" WHERE ("id" IN ($1, $2, $3))`,
		1, 2, 3)
}

func TestExprNotIn(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE id NOT IN (1, 2)",
		`SELECT "id" FROM "t" WHERE ("id" NOT IN ($1, $2))`,
		1, 2)
}

func TestExprBetween(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE age BETWEEN 18 AND 30",
		`SELECT "id" FROM "t" WHERE ("age" BETWEEN $1 AND $2)`,
		18, 30)
}

func TestExprNullTest(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a IS NULL AND b IS NOT NULL",
		`SELECT "id" FROM "t" WHERE (("a" IS NULL) AND ("b" IS NOT NULL))`)
}

func TestExprInSubquery(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users WHERE id IN (SELECT uid FROM orders)",
		`SELECT "id" FROM "users" WHERE ("id" IN (SELECT "uid" FROM "orders"))`)
}

func TestExprAnyQuantified(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users WHERE id > ANY (SELECT uid FROM orders)",
		`SELECT "id" FROM "users" WHERE ("id" > ANY (SELECT "uid" FROM "orders"))`)
}

func TestExprExists(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users u WHERE EXISTS (SELECT 1 FROM orders o WHERE o.uid = u.id)",
		`SELECT "id" FROM "users" AS "u" WHERE EXISTS (SELECT 1 FROM "orders" AS "o" WHERE ("o"."uid" = "u"."id"))`)
}

func TestExprCaseSearched(t *testing.T) {
	assertRoundTrip(t,
		"SELECT CASE WHEN a > 0 THEN 'pos' ELSE 'neg' END FROM t",
		`SELECT CASE WHEN ("a" > $1) THEN $2 ELSE $3 END FROM "t"`,
		0, "pos", "neg")
}

func TestExprCast(t *testing.T) {
	assertRoundTrip(t,
		"SELECT CAST(a AS integer), b::text FROM t",
		`SELECT CAST("a" AS integer), CAST("b" AS text) FROM "t"`)
}

func TestExprFunctionCall(t *testing.T) {
	assertRoundTrip(t,
		"SELECT coalesce(a, b), lower(name) FROM t",
		`SELECT coalesce("a", "b"), lower("name") FROM "t"`)
}

func TestExprBooleanLiteral(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE active = true",
		`SELECT "id" FROM "t" WHERE ("active" = $1)`,
		true)
}

func TestExprNegativeNumber(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE balance = -5",
		`SELECT "id" FROM "t" WHERE ("balance" = $1)`,
		-5)
}

func TestExprSimpleCase(t *testing.T) {
	assertRoundTrip(t,
		"SELECT CASE status WHEN 'a' THEN 1 ELSE 0 END FROM t",
		`SELECT CASE "status" WHEN $1 THEN $2 ELSE $3 END FROM "t"`,
		"a", 1, 0)
}

func TestExprDistinctAggregates(t *testing.T) {
	assertRoundTrip(t,
		"SELECT COUNT(DISTINCT uid), SUM(DISTINCT amount) FROM orders",
		`SELECT COUNT(DISTINCT "uid"), sum(DISTINCT "amount") FROM "orders"`)
}

func TestExprIsDistinctFrom(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a IS DISTINCT FROM b AND c IS NOT DISTINCT FROM d",
		`SELECT "id" FROM "t" WHERE (("a" IS DISTINCT FROM "b") AND ("c" IS NOT DISTINCT FROM "d"))`)
}

func TestExprRowIn(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE (a, b) IN ((1, 2), (3, 4))",
		`SELECT "id" FROM "t" WHERE (("a", "b") IN (($1, $2), ($3, $4)))`,
		1, 2, 3, 4)
}

func TestExprArrayAny(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE x = ANY(ARRAY[1, 2, 3])",
		`SELECT "id" FROM "t" WHERE ("x" = ANY(ARRAY[$1, $2, $3]))`,
		1, 2, 3)
}

func TestExprSimilarTo(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE name SIMILAR TO 'a%' AND x NOT SIMILAR TO 'b%'",
		`SELECT "id" FROM "t" WHERE (("name" SIMILAR TO $1) AND ("x" NOT SIMILAR TO $2))`,
		"a%", "b%")
}
