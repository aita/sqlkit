package postgres_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestSelectColumnsAndAlias(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a, b AS x, t.* FROM users t",
		sql.Select(sql.Col("a"), sql.As(sql.Col("b"), "x"), sql.StarOf("t")).
			From(sql.SourceAlias(sql.Table{Name: sql.NewIdent("users")}, "t")),
		`SELECT "a", "b" AS "x", "t".* FROM "users" AS "t"`)
}

func TestSelectStar(t *testing.T) {
	assertRoundTrip(t, "SELECT * FROM users",
		sql.Select(sql.Star()).From(sql.Table{Name: sql.NewIdent("users")}),
		`SELECT * FROM "users"`)
}

func TestSelectWhereAndArgs(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users WHERE a = 1 AND b > 2",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).
			Where(sql.And(sql.Eq(sql.Col("a"), 1), sql.Gt(sql.Col("b"), 2))),
		`SELECT "id" FROM "users" WHERE (("a" = $1) AND ("b" > $2))`,
		1, 2)
}

func TestSelectDistinct(t *testing.T) {
	assertRoundTrip(t, "SELECT DISTINCT a FROM t",
		sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t")}).Distinct(),
		`SELECT DISTINCT "a" FROM "t"`)
}

func TestSelectQualifiedColumns(t *testing.T) {
	assertRoundTrip(t,
		"SELECT u.id FROM public.users u WHERE u.active",
		sql.Select(sql.Col("u.id")).
			From(sql.SourceAlias(sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}, "u")).
			Where(sql.Col("u.active")),
		`SELECT "u"."id" FROM "public"."users" AS "u" WHERE "u"."active"`)
}

func TestSelectGroupByHaving(t *testing.T) {
	assertRoundTrip(t,
		"SELECT uid, COUNT(*) FROM orders GROUP BY uid HAVING COUNT(*) > 1",
		sql.Select(sql.Col("uid"), sql.CountAll()).From(sql.Table{Name: sql.NewIdent("orders")}).
			GroupBy(sql.Col("uid")).Having(sql.Gt(sql.CountAll(), 1)),
		`SELECT "uid", COUNT(*) FROM "orders" GROUP BY "uid" HAVING (COUNT(*) > $1)`,
		1)
}

func TestSelectAggregates(t *testing.T) {
	assertRoundTrip(t,
		"SELECT SUM(amount), AVG(amount) FROM orders",
		sql.Select(sql.Func("sum", sql.Col("amount")), sql.Func("avg", sql.Col("amount"))).
			From(sql.Table{Name: sql.NewIdent("orders")}),
		`SELECT sum("amount"), avg("amount") FROM "orders"`)
}

func TestSelectOrderLimitOffset(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users ORDER BY name DESC, id ASC LIMIT 10 OFFSET 5",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).
			OrderBy(sql.Desc(sql.Col("name")), sql.Asc(sql.Col("id"))).Limit(10).Offset(5),
		`SELECT "id" FROM "users" ORDER BY "name" DESC, "id" LIMIT 10 OFFSET 5`)
}

func TestSelectOrderNulls(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users ORDER BY name DESC NULLS LAST",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).
			OrderBy(sql.Desc(sql.Col("name")).NullsLast()),
		`SELECT "id" FROM "users" ORDER BY "name" DESC NULLS LAST`)
}

func TestSelectInnerJoin(t *testing.T) {
	assertRoundTrip(t,
		"SELECT u.id FROM users u JOIN orders o ON o.uid = u.id",
		sql.Select(sql.Col("u.id")).From(sql.SourceAlias(sql.Table{Name: sql.NewIdent("users")}, "u")).
			Join(sql.SourceAlias(sql.Table{Name: sql.NewIdent("orders")}, "o"), sql.Eq(sql.Col("o.uid"), sql.Col("u.id"))),
		`SELECT "u"."id" FROM "users" AS "u" JOIN "orders" AS "o" ON ("o"."uid" = "u"."id")`)
}

func TestSelectLeftJoin(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users u LEFT JOIN orders o ON o.uid = u.id",
		sql.Select(sql.Col("id")).From(sql.SourceAlias(sql.Table{Name: sql.NewIdent("users")}, "u")).
			LeftJoin(sql.SourceAlias(sql.Table{Name: sql.NewIdent("orders")}, "o"), sql.Eq(sql.Col("o.uid"), sql.Col("u.id"))),
		`SELECT "id" FROM "users" AS "u" LEFT JOIN "orders" AS "o" ON ("o"."uid" = "u"."id")`)
}

func TestSelectCrossJoinFromComma(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1, t2",
		sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t1")}).CrossJoin(sql.Table{Name: sql.NewIdent("t2")}),
		`SELECT "a" FROM "t1" CROSS JOIN "t2"`)
}

func TestSelectSubquerySource(t *testing.T) {
	assertRoundTrip(t,
		"SELECT s.id FROM (SELECT id FROM users WHERE active) s",
		sql.Select(sql.Col("s.id")).From(sql.Alias(
			sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).Where(sql.Col("active")), "s")),
		`SELECT "s"."id" FROM (SELECT "id" FROM "users" WHERE "active") AS "s"`)
}

func TestSelectScalarSubquery(t *testing.T) {
	assertRoundTrip(t,
		"SELECT (SELECT COUNT(*) FROM orders o WHERE o.uid = u.id) FROM users u",
		sql.Select(sql.ScalarSubquery(
			sql.Select(sql.CountAll()).From(sql.SourceAlias(sql.Table{Name: sql.NewIdent("orders")}, "o")).
				Where(sql.Eq(sql.Col("o.uid"), sql.Col("u.id"))))).
			From(sql.SourceAlias(sql.Table{Name: sql.NewIdent("users")}, "u")),
		`SELECT (SELECT COUNT(*) FROM "orders" AS "o" WHERE ("o"."uid" = "u"."id")) FROM "users" AS "u"`)
}

func TestSelectUnionAll(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1 UNION ALL SELECT a FROM t2 ORDER BY a LIMIT 5",
		sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t1")}).
			UnionAll(sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t2")})).
			OrderBy(sql.Asc(sql.Col("a"))).Limit(5),
		`(SELECT "a" FROM "t1") UNION ALL (SELECT "a" FROM "t2") ORDER BY "a" LIMIT 5`)
}

func TestSelectIntersect(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1 INTERSECT SELECT a FROM t2",
		sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t1")}).
			Intersect(sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t2")})),
		`(SELECT "a" FROM "t1") INTERSECT (SELECT "a" FROM "t2")`)
}

func TestSelectWithCTE(t *testing.T) {
	assertRoundTrip(t,
		"WITH recent AS (SELECT id FROM orders WHERE id > 100) SELECT id FROM recent",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("recent")}).WithCTE(
			sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("orders")}).
				Where(sql.Gt(sql.Col("id"), 100)).CTE("recent")),
		`WITH "recent" AS (SELECT "id" FROM "orders" WHERE ("id" > $1)) SELECT "id" FROM "recent"`,
		100)
}

func TestSelectWindowFunction(t *testing.T) {
	assertRoundTrip(t,
		"SELECT ROW_NUMBER() OVER (PARTITION BY uid ORDER BY id DESC) FROM orders",
		sql.Select(sql.Over(sql.Func("row_number")).PartitionBy(sql.Col("uid")).OrderBy(sql.Desc(sql.Col("id")))).
			From(sql.Table{Name: sql.NewIdent("orders")}),
		`SELECT row_number() OVER (PARTITION BY "uid" ORDER BY "id" DESC) FROM "orders"`)
}

func TestSelectLiteralProjection(t *testing.T) {
	assertRoundTrip(t,
		"SELECT 1 WHERE EXISTS (SELECT 1 FROM users)",
		sql.Select(sql.Raw("1")).Where(sql.Exists(sql.Select(sql.Raw("1")).From(sql.Table{Name: sql.NewIdent("users")}))),
		`SELECT 1 WHERE EXISTS (SELECT 1 FROM "users")`)
}

func TestSelectDistinctOn(t *testing.T) {
	assertRoundTrip(t,
		"SELECT DISTINCT ON (a) a, b FROM t",
		sql.Select(sql.Col("a"), sql.Col("b")).From(sql.Table{Name: sql.NewIdent("t")}).DistinctOn(sql.Col("a")),
		`SELECT DISTINCT ON ("a") "a", "b" FROM "t"`)
}

func TestSelectJoinUsing(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users u JOIN orders o USING (uid)",
		sql.Select(sql.Col("id")).From(sql.SourceAlias(sql.Table{Name: sql.NewIdent("users")}, "u")).
			JoinUsing(sql.SourceAlias(sql.Table{Name: sql.NewIdent("orders")}, "o"), "uid"),
		`SELECT "id" FROM "users" AS "u" JOIN "orders" AS "o" USING ("uid")`)
}

func TestSelectNaturalJoin(t *testing.T) {
	assertRoundTrip(t, "SELECT id FROM a NATURAL JOIN b",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("a")}).NaturalJoin(sql.Table{Name: sql.NewIdent("b")}),
		`SELECT "id" FROM "a" NATURAL JOIN "b"`)
}

func TestSelectNaturalLeftJoin(t *testing.T) {
	assertRoundTrip(t, "SELECT id FROM a NATURAL LEFT JOIN b",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("a")}).NaturalLeftJoin(sql.Table{Name: sql.NewIdent("b")}),
		`SELECT "id" FROM "a" NATURAL LEFT JOIN "b"`)
}

func TestSelectIntersectAll(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1 INTERSECT ALL SELECT a FROM t2",
		sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t1")}).
			IntersectAll(sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t2")})),
		`(SELECT "a" FROM "t1") INTERSECT ALL (SELECT "a" FROM "t2")`)
}

func TestSelectExceptAll(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1 EXCEPT ALL SELECT a FROM t2",
		sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t1")}).
			ExceptAll(sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t2")})),
		`(SELECT "a" FROM "t1") EXCEPT ALL (SELECT "a" FROM "t2")`)
}

func TestSelectNamedWindow(t *testing.T) {
	assertRoundTrip(t,
		"SELECT ROW_NUMBER() OVER w FROM t WINDOW w AS (PARTITION BY a ORDER BY b)",
		sql.Select(sql.Over(sql.Func("row_number")).Named("w")).From(sql.Table{Name: sql.NewIdent("t")}).
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

func TestSelectWindowFrame(t *testing.T) {
	assertRoundTrip(t,
		"SELECT SUM(x) OVER (ORDER BY id ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) FROM t",
		sql.Select(sql.Over(sql.Func("sum", sql.Col("x"))).
			OrderBy(sql.Asc(sql.Col("id"))).
			Frame("ROWS BETWEEN 1 PRECEDING AND CURRENT ROW")).From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT sum("x") OVER (ORDER BY "id" ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) FROM "t"`)
}

func TestSelectNamedWindowFrame(t *testing.T) {
	assertRoundTrip(t,
		"SELECT SUM(x) OVER w FROM t WINDOW w AS (ORDER BY id RANGE UNBOUNDED PRECEDING)",
		sql.Select(sql.Over(sql.Func("sum", sql.Col("x"))).Named("w")).From(sql.Table{Name: sql.NewIdent("t")}).
			Window(sql.WindowDef("w").OrderBy(sql.Asc(sql.Col("id"))).Frame("RANGE UNBOUNDED PRECEDING")),
		`SELECT sum("x") OVER "w" FROM "t" WINDOW "w" AS (ORDER BY "id" RANGE UNBOUNDED PRECEDING)`)
}

func TestSelectRightJoin(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a.id FROM a RIGHT JOIN b ON a.id = b.aid",
		sql.Select(sql.Col("a.id")).From(sql.Table{Name: sql.NewIdent("a")}).
			RightJoin(sql.Table{Name: sql.NewIdent("b")}, sql.Eq(sql.Col("a.id"), sql.Col("b.aid"))),
		`SELECT "a"."id" FROM "a" RIGHT JOIN "b" ON ("a"."id" = "b"."aid")`)
}

func TestSelectFullJoin(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a.id FROM a FULL JOIN b ON a.id = b.aid",
		sql.Select(sql.Col("a.id")).From(sql.Table{Name: sql.NewIdent("a")}).
			FullJoin(sql.Table{Name: sql.NewIdent("b")}, sql.Eq(sql.Col("a.id"), sql.Col("b.aid"))),
		`SELECT "a"."id" FROM "a" FULL JOIN "b" ON ("a"."id" = "b"."aid")`)
}

// A CTE with explicit column names exercises the AliasColnames branch of
// convertWith.
func TestSelectCTEWithColumns(t *testing.T) {
	assertRoundTrip(t,
		"WITH c(x, y) AS (SELECT a, b FROM t) SELECT x FROM c",
		sql.Select(sql.Col("x")).From(sql.Table{Name: sql.NewIdent("c")}).WithCTE(
			sql.Select(sql.Col("a"), sql.Col("b")).From(sql.Table{Name: sql.NewIdent("t")}).
				CTE("c", sql.Columns("x", "y"))),
		`WITH "c" ("x", "y") AS (SELECT "a", "b" FROM "t") SELECT "x" FROM "c"`)
}

// A WITH RECURSIVE clause exercises the Recursive branch of convertWith.
func TestSelectRecursiveCTE(t *testing.T) {
	assertRoundTrip(t,
		"WITH RECURSIVE c(n) AS (SELECT 1) SELECT n FROM c",
		sql.Select(sql.Col("n")).From(sql.Table{Name: sql.NewIdent("c")}).WithCTE(
			sql.Select(sql.Raw("1")).CTE("c", sql.Columns("n"), sql.Recursive())),
		`WITH RECURSIVE "c" ("n") AS (SELECT 1) SELECT "n" FROM "c"`)
}

// DELETE ... USING with a table alias and a qualified WHERE round-trips, with
// the FROM relation's alias dropped (the builder models USING joins, not the
// target alias).
func TestDeleteUsingWithTargetAlias(t *testing.T) {
	assertRoundTrip(t,
		"DELETE FROM t a USING b WHERE a.id = b.id",
		sql.DeleteFrom(sql.Table{Name: sql.NewIdent("t")}).Using(sql.Table{Name: sql.NewIdent("b")}).
			Where(sql.Eq(sql.Col("a.id"), sql.Col("b.id"))),
		`DELETE FROM "t" USING "b" WHERE ("a"."id" = "b"."id")`)
}

func TestGrantOnSequence(t *testing.T) {
	assertRoundTrip(t,
		"GRANT SELECT ON SEQUENCE s TO r",
		sql.Grant("SELECT").OnType("SEQUENCE", "s").To("r"),
		`GRANT SELECT ON SEQUENCE "s" TO "r";`)
}

func TestGrantOnDatabase(t *testing.T) {
	assertRoundTrip(t,
		"GRANT ALL PRIVILEGES ON DATABASE d TO r",
		sql.Grant().OnType("DATABASE", "d").To("r"),
		`GRANT ALL PRIVILEGES ON DATABASE "d" TO "r";`)
}

// A window frame whose start bound is UNBOUNDED PRECEDING and end bound CURRENT
// ROW exercises those two frameBound cases.
func TestSelectWindowFrameRangeUnboundedToCurrent(t *testing.T) {
	assertRoundTrip(t,
		"SELECT COUNT(*) OVER (ORDER BY b RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM t",
		sql.Select(sql.Over(sql.CountAll()).
			OrderBy(sql.Asc(sql.Col("b"))).
			Frame("RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW")).From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT COUNT(*) OVER (ORDER BY "b" RANGE BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM "t"`)
}

// A window frame whose start bound is CURRENT ROW and end bound UNBOUNDED
// FOLLOWING exercises the remaining frameBound cases.
func TestSelectWindowFrameRowsCurrentToUnbounded(t *testing.T) {
	assertRoundTrip(t,
		"SELECT COUNT(*) OVER (ORDER BY b ROWS BETWEEN CURRENT ROW AND UNBOUNDED FOLLOWING) FROM t",
		sql.Select(sql.Over(sql.CountAll()).
			OrderBy(sql.Asc(sql.Col("b"))).
			Frame("ROWS BETWEEN CURRENT ROW AND UNBOUNDED FOLLOWING")).From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT COUNT(*) OVER (ORDER BY "b" ROWS BETWEEN CURRENT ROW AND UNBOUNDED FOLLOWING) FROM "t"`)
}

// A window frame whose end bound is N FOLLOWING exercises frameOffset for the
// end side.
func TestSelectWindowFrameOffsetFollowing(t *testing.T) {
	assertRoundTrip(t,
		"SELECT SUM(x) OVER (ORDER BY id ROWS BETWEEN 1 PRECEDING AND 2 FOLLOWING) FROM t",
		sql.Select(sql.Over(sql.Func("sum", sql.Col("x"))).
			OrderBy(sql.Asc(sql.Col("id"))).
			Frame("ROWS BETWEEN 1 PRECEDING AND 2 FOLLOWING")).From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT sum("x") OVER (ORDER BY "id" ROWS BETWEEN 1 PRECEDING AND 2 FOLLOWING) FROM "t"`)
}

// A CREATE MATERIALIZED VIEW with explicit columns and WITH NO DATA exercises
// the Columns and skip-data branches of convertCreateTableAs.
func TestCreateMaterializedViewColumns(t *testing.T) {
	assertRoundTrip(t,
		"CREATE MATERIALIZED VIEW mv (a) AS SELECT id FROM t WITH NO DATA",
		sql.CreateMaterializedView(sql.Table{Name: sql.NewIdent("mv")},
			sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")})).Columns("a").WithNoData(),
		"CREATE MATERIALIZED VIEW \"mv\" (\"a\") AS\nSELECT \"id\" FROM \"t\" WITH NO DATA;")
}

// CREATE TABLE AS that is not a materialized view (a plain data-copying CTAS) is
// not modeled and falls back to raw.
func TestCreateTableAsFallsBackToRaw(t *testing.T) {
	assertRaw(t, "CREATE TABLE copy AS SELECT id FROM t")
}

// A compound (set) query with both LIMIT and OFFSET applied to the whole result
// exercises those branches of convertCompound.
func TestCompoundLimitOffset(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1 UNION SELECT a FROM t2 LIMIT 5 OFFSET 2",
		sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t1")}).
			Union(sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t2")})).
			Limit(5).Offset(2),
		`(SELECT "a" FROM "t1") UNION (SELECT "a" FROM "t2") LIMIT 5 OFFSET 2`)
}

// A three-way chained UNION nests left-associatively.
func TestCompoundThreeWayUnion(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1 UNION SELECT a FROM t2 UNION SELECT a FROM t3",
		sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t1")}).
			Union(sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t2")})).
			Union(sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t3")})),
		`(SELECT "a" FROM "t1") UNION (SELECT "a" FROM "t2") UNION (SELECT "a" FROM "t3")`)
}

// A bare NULL constant in the select list renders verbatim.
func TestExprNullConstant(t *testing.T) {
	assertRoundTrip(t,
		"SELECT NULL FROM t",
		sql.Select(sql.Raw("NULL")).From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT NULL FROM "t"`)
}

// An equality against NULL binds a nil value (the parser does not rewrite it to
// IS NULL).
func TestExprEqualsNullBindsNil(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a = NULL",
		selWhere(sql.Eq(sql.Col("a"), nil)),
		`SELECT "id" FROM "t" WHERE ("a" = $1)`,
		nil)
}

// COLLATE applied to an ORDER BY term round-trips.
func TestSelectOrderByCollate(t *testing.T) {
	assertRoundTrip(t,
		`SELECT id FROM t ORDER BY name COLLATE "C"`,
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).
			OrderBy(sql.Asc(sql.Collate(sql.Col("name"), "C"))),
		`SELECT "id" FROM "t" ORDER BY "name" COLLATE "C"`)
}

// A GROUP BY mixing a plain column with ROLLUP and CUBE elements exercises the
// per-element grouping-set conversion.
func TestGroupByMixedRollupCube(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a, b, c FROM t GROUP BY a, ROLLUP (b), CUBE (c)",
		sql.Select(sql.Col("a"), sql.Col("b"), sql.Col("c")).From(sql.Table{Name: sql.NewIdent("t")}).
			GroupBy(sql.Col("a"), sql.Rollup(sql.Col("b")), sql.Cube(sql.Col("c"))),
		`SELECT "a", "b", "c" FROM "t" GROUP BY "a", ROLLUP ("b"), CUBE ("c")`)
}
