package mysql_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestSelectColumnsAndAlias(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a, b AS x, t.* FROM users t",
		sql.Select(sql.Col("a"), sql.As(sql.Col("b"), "x"), sql.StarOf("t")).
			From(sql.SourceAlias(sql.Table{Name: "users"}, "t")),
		"SELECT `a`, `b` AS `x`, `t`.* FROM `users` AS `t`")
}

func TestSelectStar(t *testing.T) {
	assertRoundTrip(t, "SELECT * FROM users",
		sql.Select(sql.Star()).From(sql.Table{Name: "users"}),
		"SELECT * FROM `users`")
}

func TestSelectWhereAndArgs(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users WHERE a = 1 AND b > 2",
		sql.Select(sql.Col("id")).From(sql.Table{Name: "users"}).
			Where(sql.And(sql.Eq(sql.Col("a"), 1), sql.Gt(sql.Col("b"), 2))),
		"SELECT `id` FROM `users` WHERE ((`a` = ?) AND (`b` > ?))",
		1, 2)
}

func TestSelectDistinct(t *testing.T) {
	assertRoundTrip(t, "SELECT DISTINCT a FROM t",
		sql.Select(sql.Col("a")).From(sql.Table{Name: "t"}).Distinct(),
		"SELECT DISTINCT `a` FROM `t`")
}

func TestSelectQualifiedColumns(t *testing.T) {
	assertRoundTrip(t,
		"SELECT u.id FROM app.users u WHERE u.active = 1",
		sql.Select(sql.Col("u.id")).
			From(sql.SourceAlias(sql.Table{Schema: "app", Name: "users"}, "u")).
			Where(sql.Eq(sql.Col("u.active"), 1)),
		"SELECT `u`.`id` FROM `app`.`users` AS `u` WHERE (`u`.`active` = ?)",
		1)
}

func TestSelectGroupByHaving(t *testing.T) {
	assertRoundTrip(t,
		"SELECT uid, COUNT(*) FROM orders GROUP BY uid HAVING COUNT(*) > 1",
		sql.Select(sql.Col("uid"), sql.CountAll()).From(sql.Table{Name: "orders"}).
			GroupBy(sql.Col("uid")).Having(sql.Gt(sql.CountAll(), 1)),
		"SELECT `uid`, COUNT(*) FROM `orders` GROUP BY `uid` HAVING (COUNT(*) > ?)",
		1)
}

func TestSelectAggregates(t *testing.T) {
	assertRoundTrip(t,
		"SELECT SUM(amount), AVG(amount), COUNT(DISTINCT uid) FROM orders",
		sql.Select(
			sql.Func("SUM", sql.Col("amount")),
			sql.Func("AVG", sql.Col("amount")),
			sql.CountDistinct(sql.Col("uid")),
		).From(sql.Table{Name: "orders"}),
		"SELECT SUM(`amount`), AVG(`amount`), COUNT(DISTINCT `uid`) FROM `orders`")
}

func TestSelectOrderLimitOffset(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users ORDER BY name DESC, id ASC LIMIT 10 OFFSET 5",
		sql.Select(sql.Col("id")).From(sql.Table{Name: "users"}).
			OrderBy(sql.Desc(sql.Col("name")), sql.Asc(sql.Col("id"))).Limit(10).Offset(5),
		"SELECT `id` FROM `users` ORDER BY `name` DESC, `id` LIMIT 10 OFFSET 5")
}

func TestSelectInnerJoin(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a.id FROM a JOIN b ON a.id = b.aid",
		sql.Select(sql.Col("a.id")).From(sql.Table{Name: "a"}).
			Join(sql.Table{Name: "b"}, sql.Eq(sql.Col("a.id"), sql.Col("b.aid"))),
		"SELECT `a`.`id` FROM `a` JOIN `b` ON (`a`.`id` = `b`.`aid`)")
}

func TestSelectLeftAndRightJoin(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a.id FROM a LEFT JOIN b ON a.id = b.aid RIGHT JOIN c ON c.bid = b.id",
		sql.Select(sql.Col("a.id")).From(sql.Table{Name: "a"}).
			LeftJoin(sql.Table{Name: "b"}, sql.Eq(sql.Col("a.id"), sql.Col("b.aid"))).
			RightJoin(sql.Table{Name: "c"}, sql.Eq(sql.Col("c.bid"), sql.Col("b.id"))),
		"SELECT `a`.`id` FROM `a` LEFT JOIN `b` ON (`a`.`id` = `b`.`aid`) RIGHT JOIN `c` ON (`c`.`bid` = `b`.`id`)")
}

func TestSelectCrossAndCommaJoin(t *testing.T) {
	assertRoundTrip(t,
		"SELECT * FROM a CROSS JOIN b",
		sql.Select(sql.Star()).From(sql.Table{Name: "a"}).CrossJoin(sql.Table{Name: "b"}),
		"SELECT * FROM `a` CROSS JOIN `b`")

	assertRoundTrip(t,
		"SELECT * FROM a, b",
		sql.Select(sql.Star()).From(sql.Table{Name: "a"}).CrossJoin(sql.Table{Name: "b"}),
		"SELECT * FROM `a` CROSS JOIN `b`")
}

func TestSelectJoinUsing(t *testing.T) {
	assertRoundTrip(t,
		"SELECT * FROM a JOIN b USING (id)",
		sql.Select(sql.Star()).From(sql.Table{Name: "a"}).JoinUsing(sql.Table{Name: "b"}, "id"),
		"SELECT * FROM `a` JOIN `b` USING (`id`)")
}

func TestSelectSubquerySource(t *testing.T) {
	assertRoundTrip(t,
		"SELECT t.id FROM (SELECT id FROM users) t",
		sql.Select(sql.Col("t.id")).From(
			sql.Alias(sql.Select(sql.Col("id")).From(sql.Table{Name: "users"}), "t")),
		"SELECT `t`.`id` FROM (SELECT `id` FROM `users`) AS `t`")
}

func TestSelectUnion(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1 UNION SELECT a FROM t2",
		sql.Select(sql.Col("a")).From(sql.Table{Name: "t1"}).
			Union(sql.Select(sql.Col("a")).From(sql.Table{Name: "t2"})),
		"(SELECT `a` FROM `t1`) UNION (SELECT `a` FROM `t2`)")
}

func TestSelectUnionAllOrderLimit(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1 UNION ALL SELECT a FROM t2 ORDER BY a LIMIT 5",
		sql.Select(sql.Col("a")).From(sql.Table{Name: "t1"}).
			UnionAll(sql.Select(sql.Col("a")).From(sql.Table{Name: "t2"})).
			OrderBy(sql.Asc(sql.Col("a"))).Limit(5),
		"(SELECT `a` FROM `t1`) UNION ALL (SELECT `a` FROM `t2`) ORDER BY `a` LIMIT 5")
}

func TestSelectCTE(t *testing.T) {
	assertRoundTrip(t,
		"WITH t AS (SELECT id FROM users) SELECT id FROM t",
		sql.Select(sql.Col("id")).From(sql.Table{Name: "t"}).
			WithCTE(sql.Select(sql.Col("id")).From(sql.Table{Name: "users"}).CTE("t")),
		"WITH `t` AS (SELECT `id` FROM `users`) SELECT `id` FROM `t`")
}

func TestSelectWindowFunction(t *testing.T) {
	assertRoundTrip(t,
		"SELECT ROW_NUMBER() OVER (PARTITION BY dept ORDER BY salary DESC) FROM emp",
		sql.Select(
			sql.Over(sql.Func("ROW_NUMBER")).
				PartitionBy(sql.Col("dept")).
				OrderBy(sql.Desc(sql.Col("salary"))),
		).From(sql.Table{Name: "emp"}),
		"SELECT ROW_NUMBER() OVER (PARTITION BY `dept` ORDER BY `salary` DESC) FROM `emp`")
}

func TestSelectRollupFallsBackToRaw(t *testing.T) {
	assertRaw(t, "SELECT a, COUNT(*) FROM t GROUP BY a WITH ROLLUP")
}

func TestSelectForUpdate(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users WHERE id = 1 FOR UPDATE",
		sql.Select(sql.Col("id")).From(sql.Table{Name: "users"}).
			Where(sql.Eq(sql.Col("id"), 1)).ForUpdate(),
		"SELECT `id` FROM `users` WHERE (`id` = ?) FOR UPDATE",
		1)
}
