package postgres_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestSelectColumnsAndAlias(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a, b AS x, t.* FROM users t",
		sql.Select(sql.Col("a"), sql.As(sql.Col("b"), "x"), sql.StarOf("t")).
			From(sql.SourceAlias(sql.Table{Name: "users"}, "t")),
		`SELECT "a", "b" AS "x", "t".* FROM "users" AS "t"`)
}

func TestSelectStar(t *testing.T) {
	assertRoundTrip(t, "SELECT * FROM users",
		sql.Select(sql.Star()).From(sql.Table{Name: "users"}),
		`SELECT * FROM "users"`)
}

func TestSelectWhereAndArgs(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users WHERE a = 1 AND b > 2",
		sql.Select(sql.Col("id")).From(sql.Table{Name: "users"}).
			Where(sql.And(sql.Eq(sql.Col("a"), 1), sql.Gt(sql.Col("b"), 2))),
		`SELECT "id" FROM "users" WHERE (("a" = $1) AND ("b" > $2))`,
		1, 2)
}

func TestSelectDistinct(t *testing.T) {
	assertRoundTrip(t, "SELECT DISTINCT a FROM t",
		sql.Select(sql.Col("a")).From(sql.Table{Name: "t"}).Distinct(),
		`SELECT DISTINCT "a" FROM "t"`)
}

func TestSelectQualifiedColumns(t *testing.T) {
	assertRoundTrip(t,
		"SELECT u.id FROM public.users u WHERE u.active",
		sql.Select(sql.Col("u.id")).
			From(sql.SourceAlias(sql.Table{Schema: "public", Name: "users"}, "u")).
			Where(sql.Col("u.active")),
		`SELECT "u"."id" FROM "public"."users" AS "u" WHERE "u"."active"`)
}

func TestSelectGroupByHaving(t *testing.T) {
	assertRoundTrip(t,
		"SELECT uid, COUNT(*) FROM orders GROUP BY uid HAVING COUNT(*) > 1",
		sql.Select(sql.Col("uid"), sql.CountAll()).From(sql.Table{Name: "orders"}).
			GroupBy(sql.Col("uid")).Having(sql.Gt(sql.CountAll(), 1)),
		`SELECT "uid", COUNT(*) FROM "orders" GROUP BY "uid" HAVING (COUNT(*) > $1)`,
		1)
}

func TestSelectAggregates(t *testing.T) {
	assertRoundTrip(t,
		"SELECT SUM(amount), AVG(amount) FROM orders",
		sql.Select(sql.Func("sum", sql.Col("amount")), sql.Func("avg", sql.Col("amount"))).
			From(sql.Table{Name: "orders"}),
		`SELECT sum("amount"), avg("amount") FROM "orders"`)
}

func TestSelectOrderLimitOffset(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users ORDER BY name DESC, id ASC LIMIT 10 OFFSET 5",
		sql.Select(sql.Col("id")).From(sql.Table{Name: "users"}).
			OrderBy(sql.Desc(sql.Col("name")), sql.Asc(sql.Col("id"))).Limit(10).Offset(5),
		`SELECT "id" FROM "users" ORDER BY "name" DESC, "id" LIMIT 10 OFFSET 5`)
}

func TestSelectOrderNulls(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users ORDER BY name DESC NULLS LAST",
		sql.Select(sql.Col("id")).From(sql.Table{Name: "users"}).
			OrderBy(sql.Desc(sql.Col("name")).NullsLast()),
		`SELECT "id" FROM "users" ORDER BY "name" DESC NULLS LAST`)
}

func TestSelectInnerJoin(t *testing.T) {
	assertRoundTrip(t,
		"SELECT u.id FROM users u JOIN orders o ON o.uid = u.id",
		sql.Select(sql.Col("u.id")).From(sql.SourceAlias(sql.Table{Name: "users"}, "u")).
			Join(sql.SourceAlias(sql.Table{Name: "orders"}, "o"), sql.Eq(sql.Col("o.uid"), sql.Col("u.id"))),
		`SELECT "u"."id" FROM "users" AS "u" JOIN "orders" AS "o" ON ("o"."uid" = "u"."id")`)
}

func TestSelectLeftJoin(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users u LEFT JOIN orders o ON o.uid = u.id",
		sql.Select(sql.Col("id")).From(sql.SourceAlias(sql.Table{Name: "users"}, "u")).
			LeftJoin(sql.SourceAlias(sql.Table{Name: "orders"}, "o"), sql.Eq(sql.Col("o.uid"), sql.Col("u.id"))),
		`SELECT "id" FROM "users" AS "u" LEFT JOIN "orders" AS "o" ON ("o"."uid" = "u"."id")`)
}

func TestSelectCrossJoinFromComma(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1, t2",
		sql.Select(sql.Col("a")).From(sql.Table{Name: "t1"}).CrossJoin(sql.Table{Name: "t2"}),
		`SELECT "a" FROM "t1" CROSS JOIN "t2"`)
}

func TestSelectSubquerySource(t *testing.T) {
	assertRoundTrip(t,
		"SELECT s.id FROM (SELECT id FROM users WHERE active) s",
		sql.Select(sql.Col("s.id")).From(sql.Alias(
			sql.Select(sql.Col("id")).From(sql.Table{Name: "users"}).Where(sql.Col("active")), "s")),
		`SELECT "s"."id" FROM (SELECT "id" FROM "users" WHERE "active") AS "s"`)
}

func TestSelectScalarSubquery(t *testing.T) {
	assertRoundTrip(t,
		"SELECT (SELECT COUNT(*) FROM orders o WHERE o.uid = u.id) FROM users u",
		sql.Select(sql.ScalarSubquery(
			sql.Select(sql.CountAll()).From(sql.SourceAlias(sql.Table{Name: "orders"}, "o")).
				Where(sql.Eq(sql.Col("o.uid"), sql.Col("u.id"))))).
			From(sql.SourceAlias(sql.Table{Name: "users"}, "u")),
		`SELECT (SELECT COUNT(*) FROM "orders" AS "o" WHERE ("o"."uid" = "u"."id")) FROM "users" AS "u"`)
}

func TestSelectUnionAll(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1 UNION ALL SELECT a FROM t2 ORDER BY a LIMIT 5",
		sql.Select(sql.Col("a")).From(sql.Table{Name: "t1"}).
			UnionAll(sql.Select(sql.Col("a")).From(sql.Table{Name: "t2"})).
			OrderBy(sql.Asc(sql.Col("a"))).Limit(5),
		`(SELECT "a" FROM "t1") UNION ALL (SELECT "a" FROM "t2") ORDER BY "a" LIMIT 5`)
}

func TestSelectIntersect(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1 INTERSECT SELECT a FROM t2",
		sql.Select(sql.Col("a")).From(sql.Table{Name: "t1"}).
			Intersect(sql.Select(sql.Col("a")).From(sql.Table{Name: "t2"})),
		`(SELECT "a" FROM "t1") INTERSECT (SELECT "a" FROM "t2")`)
}

func TestSelectWithCTE(t *testing.T) {
	assertRoundTrip(t,
		"WITH recent AS (SELECT id FROM orders WHERE id > 100) SELECT id FROM recent",
		sql.Select(sql.Col("id")).From(sql.Table{Name: "recent"}).WithCTE(
			sql.Select(sql.Col("id")).From(sql.Table{Name: "orders"}).
				Where(sql.Gt(sql.Col("id"), 100)).CTE("recent")),
		`WITH "recent" AS (SELECT "id" FROM "orders" WHERE ("id" > $1)) SELECT "id" FROM "recent"`,
		100)
}

func TestSelectWindowFunction(t *testing.T) {
	assertRoundTrip(t,
		"SELECT ROW_NUMBER() OVER (PARTITION BY uid ORDER BY id DESC) FROM orders",
		sql.Select(sql.Over(sql.Func("row_number")).PartitionBy(sql.Col("uid")).OrderBy(sql.Desc(sql.Col("id")))).
			From(sql.Table{Name: "orders"}),
		`SELECT row_number() OVER (PARTITION BY "uid" ORDER BY "id" DESC) FROM "orders"`)
}

func TestSelectLiteralProjection(t *testing.T) {
	assertRoundTrip(t,
		"SELECT 1 WHERE EXISTS (SELECT 1 FROM users)",
		sql.Select(sql.Raw("1")).Where(sql.Exists(sql.Select(sql.Raw("1")).From(sql.Table{Name: "users"}))),
		`SELECT 1 WHERE EXISTS (SELECT 1 FROM "users")`)
}

func TestSelectDistinctOn(t *testing.T) {
	assertRoundTrip(t,
		"SELECT DISTINCT ON (a) a, b FROM t",
		sql.Select(sql.Col("a"), sql.Col("b")).From(sql.Table{Name: "t"}).DistinctOn(sql.Col("a")),
		`SELECT DISTINCT ON ("a") "a", "b" FROM "t"`)
}

func TestSelectJoinUsing(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users u JOIN orders o USING (uid)",
		sql.Select(sql.Col("id")).From(sql.SourceAlias(sql.Table{Name: "users"}, "u")).
			JoinUsing(sql.SourceAlias(sql.Table{Name: "orders"}, "o"), "uid"),
		`SELECT "id" FROM "users" AS "u" JOIN "orders" AS "o" USING ("uid")`)
}

func TestSelectNaturalJoin(t *testing.T) {
	assertRoundTrip(t, "SELECT id FROM a NATURAL JOIN b",
		sql.Select(sql.Col("id")).From(sql.Table{Name: "a"}).NaturalJoin(sql.Table{Name: "b"}),
		`SELECT "id" FROM "a" NATURAL JOIN "b"`)
}

func TestSelectNaturalLeftJoin(t *testing.T) {
	assertRoundTrip(t, "SELECT id FROM a NATURAL LEFT JOIN b",
		sql.Select(sql.Col("id")).From(sql.Table{Name: "a"}).NaturalLeftJoin(sql.Table{Name: "b"}),
		`SELECT "id" FROM "a" NATURAL LEFT JOIN "b"`)
}

func TestSelectIntersectAll(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1 INTERSECT ALL SELECT a FROM t2",
		sql.Select(sql.Col("a")).From(sql.Table{Name: "t1"}).
			IntersectAll(sql.Select(sql.Col("a")).From(sql.Table{Name: "t2"})),
		`(SELECT "a" FROM "t1") INTERSECT ALL (SELECT "a" FROM "t2")`)
}

func TestSelectExceptAll(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1 EXCEPT ALL SELECT a FROM t2",
		sql.Select(sql.Col("a")).From(sql.Table{Name: "t1"}).
			ExceptAll(sql.Select(sql.Col("a")).From(sql.Table{Name: "t2"})),
		`(SELECT "a" FROM "t1") EXCEPT ALL (SELECT "a" FROM "t2")`)
}

func TestSelectNamedWindow(t *testing.T) {
	assertRoundTrip(t,
		"SELECT ROW_NUMBER() OVER w FROM t WINDOW w AS (PARTITION BY a ORDER BY b)",
		sql.Select(sql.Over(sql.Func("row_number")).Named("w")).From(sql.Table{Name: "t"}).
			Window(sql.WindowDef("w").PartitionBy(sql.Col("a")).OrderBy(sql.Asc(sql.Col("b")))),
		`SELECT row_number() OVER "w" FROM "t" WINDOW "w" AS (PARTITION BY "a" ORDER BY "b")`)
}

func TestSelectStandaloneValues(t *testing.T) {
	assertRoundTrip(t,
		"VALUES (1, 'a'), (2, 'b')",
		sql.Values().Row(1, "a").Row(2, "b"),
		`VALUES ($1, $2), ($3, $4)`,
		1, "a", 2, "b")
}

func TestSelectWindowFrameUnsupported(t *testing.T) {
	assertUnsupported(t,
		"SELECT SUM(x) OVER (ORDER BY id ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) FROM t")
}
