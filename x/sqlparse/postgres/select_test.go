package postgres_test

import "testing"

func TestSelectColumnsAndAlias(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a, b AS x, t.* FROM users t",
		`SELECT "a", "b" AS "x", "t".* FROM "users" AS "t"`)
}

func TestSelectStar(t *testing.T) {
	assertRoundTrip(t, "SELECT * FROM users", `SELECT * FROM "users"`)
}

func TestSelectWhereAndArgs(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users WHERE a = 1 AND b > 2",
		`SELECT "id" FROM "users" WHERE (("a" = $1) AND ("b" > $2))`,
		1, 2)
}

func TestSelectDistinct(t *testing.T) {
	assertRoundTrip(t, "SELECT DISTINCT a FROM t", `SELECT DISTINCT "a" FROM "t"`)
}

func TestSelectQualifiedColumns(t *testing.T) {
	assertRoundTrip(t,
		"SELECT u.id FROM public.users u WHERE u.active",
		`SELECT "u"."id" FROM "public"."users" AS "u" WHERE "u"."active"`)
}

func TestSelectGroupByHaving(t *testing.T) {
	assertRoundTrip(t,
		"SELECT uid, COUNT(*) FROM orders GROUP BY uid HAVING COUNT(*) > 1",
		`SELECT "uid", COUNT(*) FROM "orders" GROUP BY "uid" HAVING (COUNT(*) > $1)`,
		1)
}

func TestSelectAggregates(t *testing.T) {
	assertRoundTrip(t,
		"SELECT SUM(amount), AVG(amount) FROM orders",
		`SELECT sum("amount"), avg("amount") FROM "orders"`)
}

func TestSelectOrderLimitOffset(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users ORDER BY name DESC, id ASC LIMIT 10 OFFSET 5",
		`SELECT "id" FROM "users" ORDER BY "name" DESC, "id" LIMIT 10 OFFSET 5`)
}

func TestSelectOrderNulls(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users ORDER BY name DESC NULLS LAST",
		`SELECT "id" FROM "users" ORDER BY "name" DESC NULLS LAST`)
}

func TestSelectInnerJoin(t *testing.T) {
	assertRoundTrip(t,
		"SELECT u.id FROM users u JOIN orders o ON o.uid = u.id",
		`SELECT "u"."id" FROM "users" AS "u" JOIN "orders" AS "o" ON ("o"."uid" = "u"."id")`)
}

func TestSelectLeftJoin(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users u LEFT JOIN orders o ON o.uid = u.id",
		`SELECT "id" FROM "users" AS "u" LEFT JOIN "orders" AS "o" ON ("o"."uid" = "u"."id")`)
}

func TestSelectCrossJoinFromComma(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1, t2",
		`SELECT "a" FROM "t1" CROSS JOIN "t2"`)
}

func TestSelectSubquerySource(t *testing.T) {
	assertRoundTrip(t,
		"SELECT s.id FROM (SELECT id FROM users WHERE active) s",
		`SELECT "s"."id" FROM (SELECT "id" FROM "users" WHERE "active") AS "s"`)
}

func TestSelectScalarSubquery(t *testing.T) {
	assertRoundTrip(t,
		"SELECT (SELECT COUNT(*) FROM orders o WHERE o.uid = u.id) FROM users u",
		`SELECT (SELECT COUNT(*) FROM "orders" AS "o" WHERE ("o"."uid" = "u"."id")) FROM "users" AS "u"`)
}

func TestSelectUnionAll(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1 UNION ALL SELECT a FROM t2 ORDER BY a LIMIT 5",
		`(SELECT "a" FROM "t1") UNION ALL (SELECT "a" FROM "t2") ORDER BY "a" LIMIT 5`)
}

func TestSelectIntersect(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1 INTERSECT SELECT a FROM t2",
		`(SELECT "a" FROM "t1") INTERSECT (SELECT "a" FROM "t2")`)
}

func TestSelectWithCTE(t *testing.T) {
	assertRoundTrip(t,
		"WITH recent AS (SELECT id FROM orders WHERE id > 100) SELECT id FROM recent",
		`WITH "recent" AS (SELECT "id" FROM "orders" WHERE ("id" > $1)) SELECT "id" FROM "recent"`,
		100)
}

func TestSelectWindowFunction(t *testing.T) {
	assertRoundTrip(t,
		"SELECT ROW_NUMBER() OVER (PARTITION BY uid ORDER BY id DESC) FROM orders",
		`SELECT row_number() OVER (PARTITION BY "uid" ORDER BY "id" DESC) FROM "orders"`)
}

func TestSelectLiteralProjection(t *testing.T) {
	assertRoundTrip(t,
		"SELECT 1 WHERE EXISTS (SELECT 1 FROM users)",
		`SELECT 1 WHERE EXISTS (SELECT 1 FROM "users")`)
}

func TestSelectDistinctOnUnsupported(t *testing.T) {
	assertUnsupported(t, "SELECT DISTINCT ON (a) a, b FROM t")
}
