package mysql_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestSelectColumnsAndAlias(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a, b AS x, t.* FROM users t",
		sql.Select(sql.Col("a"), sql.As(sql.Col("b"), "x"), sql.StarOf("t")).
			From(sql.SourceAlias(sql.Table{Name: sql.NewIdent("users")}, "t")),
		"SELECT `a`, `b` AS `x`, `t`.* FROM `users` AS `t`")
}

func TestSelectStar(t *testing.T) {
	assertRoundTrip(t, "SELECT * FROM users",
		sql.Select(sql.Star()).From(sql.Table{Name: sql.NewIdent("users")}),
		"SELECT * FROM `users`")
}

func TestSelectWhereAndArgs(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users WHERE a = 1 AND b > 2",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).
			Where(sql.And(sql.Eq(sql.Col("a"), 1), sql.Gt(sql.Col("b"), 2))),
		"SELECT `id` FROM `users` WHERE ((`a` = ?) AND (`b` > ?))",
		1, 2)
}

func TestSelectDistinct(t *testing.T) {
	assertRoundTrip(t, "SELECT DISTINCT a FROM t",
		sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t")}).Distinct(),
		"SELECT DISTINCT `a` FROM `t`")
}

func TestSelectQualifiedColumns(t *testing.T) {
	assertRoundTrip(t,
		"SELECT u.id FROM app.users u WHERE u.active = 1",
		sql.Select(sql.Col("u.id")).
			From(sql.SourceAlias(sql.Table{Schema: sql.NewIdent("app"), Name: sql.NewIdent("users")}, "u")).
			Where(sql.Eq(sql.Col("u.active"), 1)),
		"SELECT `u`.`id` FROM `app`.`users` AS `u` WHERE (`u`.`active` = ?)",
		1)
}

func TestSelectGroupByHaving(t *testing.T) {
	assertRoundTrip(t,
		"SELECT uid, COUNT(*) FROM orders GROUP BY uid HAVING COUNT(*) > 1",
		sql.Select(sql.Col("uid"), sql.CountAll()).From(sql.Table{Name: sql.NewIdent("orders")}).
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
		).From(sql.Table{Name: sql.NewIdent("orders")}),
		"SELECT SUM(`amount`), AVG(`amount`), COUNT(DISTINCT `uid`) FROM `orders`")
}

// COUNT of a plain column maps to sqlkit's Count node (not CountAll, which is
// reserved for the constant-argument COUNT(*) / COUNT(1) form).
func TestSelectCountColumn(t *testing.T) {
	assertRoundTrip(t,
		"SELECT COUNT(name) FROM t",
		sql.Select(sql.Count(sql.Col("name"))).From(sql.Table{Name: sql.NewIdent("t")}),
		"SELECT COUNT(`name`) FROM `t`")
}

// COUNT(1) counts every row, so it renders as COUNT(*).
func TestSelectCountConstantIsCountAll(t *testing.T) {
	assertRoundTrip(t,
		"SELECT COUNT(1) FROM t",
		sql.Select(sql.CountAll()).From(sql.Table{Name: sql.NewIdent("t")}),
		"SELECT COUNT(*) FROM `t`")
}

// A non-COUNT aggregate with DISTINCT maps to sqlkit's DistinctFunc.
func TestSelectDistinctAggregate(t *testing.T) {
	assertRoundTrip(t,
		"SELECT SUM(DISTINCT x) FROM t",
		sql.Select(sql.DistinctFunc("SUM", sql.Col("x"))).From(sql.Table{Name: sql.NewIdent("t")}),
		"SELECT SUM(DISTINCT `x`) FROM `t`")
}

// A zero-argument built-in renders with empty parentheses.
func TestSelectNoArgFunction(t *testing.T) {
	assertRoundTrip(t,
		"SELECT NOW()",
		sql.Select(sql.Func("NOW")),
		"SELECT NOW()")
}

// COUNT with multiple arguments is not valid MySQL and is rejected by the parser
// before the converter sees it; an aggregate with an inner ORDER BY (GROUP_CONCAT)
// has no typed home and round-trips raw.
func TestSelectAggregateOrderByFallsBackToRaw(t *testing.T) {
	assertRaw(t, "SELECT GROUP_CONCAT(name ORDER BY name) FROM t")
}

func TestSelectOrderLimitOffset(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users ORDER BY name DESC, id ASC LIMIT 10 OFFSET 5",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).
			OrderBy(sql.Desc(sql.Col("name")), sql.Asc(sql.Col("id"))).Limit(10).Offset(5),
		"SELECT `id` FROM `users` ORDER BY `name` DESC, `id` LIMIT 10 OFFSET 5")
}

func TestSelectInnerJoin(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a.id FROM a JOIN b ON a.id = b.aid",
		sql.Select(sql.Col("a.id")).From(sql.Table{Name: sql.NewIdent("a")}).
			Join(sql.Table{Name: sql.NewIdent("b")}, sql.Eq(sql.Col("a.id"), sql.Col("b.aid"))),
		"SELECT `a`.`id` FROM `a` JOIN `b` ON (`a`.`id` = `b`.`aid`)")
}

func TestSelectLeftAndRightJoin(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a.id FROM a LEFT JOIN b ON a.id = b.aid RIGHT JOIN c ON c.bid = b.id",
		sql.Select(sql.Col("a.id")).From(sql.Table{Name: sql.NewIdent("a")}).
			LeftJoin(sql.Table{Name: sql.NewIdent("b")}, sql.Eq(sql.Col("a.id"), sql.Col("b.aid"))).
			RightJoin(sql.Table{Name: sql.NewIdent("c")}, sql.Eq(sql.Col("c.bid"), sql.Col("b.id"))),
		"SELECT `a`.`id` FROM `a` LEFT JOIN `b` ON (`a`.`id` = `b`.`aid`) RIGHT JOIN `c` ON (`c`.`bid` = `b`.`id`)")
}

func TestSelectCrossAndCommaJoin(t *testing.T) {
	assertRoundTrip(t,
		"SELECT * FROM a CROSS JOIN b",
		sql.Select(sql.Star()).From(sql.Table{Name: sql.NewIdent("a")}).CrossJoin(sql.Table{Name: sql.NewIdent("b")}),
		"SELECT * FROM `a` CROSS JOIN `b`")

	assertRoundTrip(t,
		"SELECT * FROM a, b",
		sql.Select(sql.Star()).From(sql.Table{Name: sql.NewIdent("a")}).CrossJoin(sql.Table{Name: sql.NewIdent("b")}),
		"SELECT * FROM `a` CROSS JOIN `b`")
}

func TestSelectJoinUsing(t *testing.T) {
	assertRoundTrip(t,
		"SELECT * FROM a JOIN b USING (id)",
		sql.Select(sql.Star()).From(sql.Table{Name: sql.NewIdent("a")}).JoinUsing(sql.Table{Name: sql.NewIdent("b")}, "id"),
		"SELECT * FROM `a` JOIN `b` USING (`id`)")
}

func TestSelectNaturalJoin(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a.id FROM a NATURAL JOIN b",
		sql.Select(sql.Col("a.id")).From(sql.Table{Name: sql.NewIdent("a")}).
			NaturalJoin(sql.Table{Name: sql.NewIdent("b")}),
		"SELECT `a`.`id` FROM `a` NATURAL JOIN `b`")
}

func TestSelectBetween(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE x BETWEEN 1 AND 10",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).
			Where(sql.Between(sql.Col("x"), 1, 10)),
		"SELECT `id` FROM `t` WHERE (`x` BETWEEN ? AND ?)",
		1, 10)
}

func TestSelectSubquerySource(t *testing.T) {
	assertRoundTrip(t,
		"SELECT t.id FROM (SELECT id FROM users) t",
		sql.Select(sql.Col("t.id")).From(
			sql.Alias(sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}), "t")),
		"SELECT `t`.`id` FROM (SELECT `id` FROM `users`) AS `t`")
}

func TestSelectUnion(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1 UNION SELECT a FROM t2",
		sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t1")}).
			Union(sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t2")})),
		"(SELECT `a` FROM `t1`) UNION (SELECT `a` FROM `t2`)")
}

func TestSelectUnionAllOrderLimit(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1 UNION ALL SELECT a FROM t2 ORDER BY a LIMIT 5",
		sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t1")}).
			UnionAll(sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t2")})).
			OrderBy(sql.Asc(sql.Col("a"))).Limit(5),
		"(SELECT `a` FROM `t1`) UNION ALL (SELECT `a` FROM `t2`) ORDER BY `a` LIMIT 5")
}

func TestSelectCTE(t *testing.T) {
	assertRoundTrip(t,
		"WITH t AS (SELECT id FROM users) SELECT id FROM t",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).
			WithCTE(sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).CTE("t")),
		"WITH `t` AS (SELECT `id` FROM `users`) SELECT `id` FROM `t`")
}

func TestSelectMultipleCTEs(t *testing.T) {
	assertRoundTrip(t,
		"WITH a AS (SELECT 1), b AS (SELECT 2) SELECT x FROM a",
		sql.Select(sql.Col("x")).From(sql.Table{Name: sql.NewIdent("a")}).
			WithCTE(
				sql.Select(sql.Raw("1")).CTE("a"),
				sql.Select(sql.Raw("2")).CTE("b"),
			),
		"WITH `a` AS (SELECT 1), `b` AS (SELECT 2) SELECT `x` FROM `a`")
}

// A RECURSIVE CTE with an explicit column list exercises the Recursive and Columns
// CTE options and a set-operation CTE body.
func TestSelectRecursiveCTE(t *testing.T) {
	assertRoundTrip(t,
		"WITH RECURSIVE nums (n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM nums WHERE n < 5) SELECT n FROM nums",
		sql.Select(sql.Col("n")).From(sql.Table{Name: sql.NewIdent("nums")}).
			WithCTE(
				sql.Select(sql.Raw("1")).
					UnionAll(sql.Select(sql.BinaryExpression{Left: sql.Col("n"), Operator: "+", Right: sql.Val(1)}).
						From(sql.Table{Name: sql.NewIdent("nums")}).
						Where(sql.Lt(sql.Col("n"), 5))).
					CTE("nums", sql.Columns("n"), sql.Recursive()),
			),
		"WITH RECURSIVE `nums` (`n`) AS ((SELECT 1) UNION ALL (SELECT (`n` + ?) FROM `nums` WHERE (`n` < ?))) SELECT `n` FROM `nums`",
		1, 5)
}

func TestSelectWindowFunction(t *testing.T) {
	assertRoundTrip(t,
		"SELECT ROW_NUMBER() OVER (PARTITION BY dept ORDER BY salary DESC) FROM emp",
		sql.Select(
			sql.Over(sql.Func("ROW_NUMBER")).
				PartitionBy(sql.Col("dept")).
				OrderBy(sql.Desc(sql.Col("salary"))),
		).From(sql.Table{Name: sql.NewIdent("emp")}),
		"SELECT ROW_NUMBER() OVER (PARTITION BY `dept` ORDER BY `salary` DESC) FROM `emp`")
}

func TestSelectRollupFallsBackToRaw(t *testing.T) {
	assertRaw(t, "SELECT a, COUNT(*) FROM t GROUP BY a WITH ROLLUP")
}

func TestSelectForUpdate(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users WHERE id = 1 FOR UPDATE",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).
			Where(sql.Eq(sql.Col("id"), 1)).ForUpdate(),
		"SELECT `id` FROM `users` WHERE (`id` = ?) FOR UPDATE",
		1)
}

func TestSelectForShare(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users FOR SHARE",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).ForShare(),
		"SELECT `id` FROM `users` FOR SHARE")
}

func TestSelectForUpdateNoWait(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users FOR UPDATE NOWAIT",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).ForUpdate(sql.NoWait()),
		"SELECT `id` FROM `users` FOR UPDATE NOWAIT")
}

func TestSelectForShareNoWait(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users FOR SHARE NOWAIT",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).ForShare(sql.NoWait()),
		"SELECT `id` FROM `users` FOR SHARE NOWAIT")
}

func TestSelectForUpdateSkipLocked(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users FOR UPDATE SKIP LOCKED",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).ForUpdate(sql.SkipLocked()),
		"SELECT `id` FROM `users` FOR UPDATE SKIP LOCKED")
}

func TestSelectForShareSkipLocked(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users FOR SHARE SKIP LOCKED",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).ForShare(sql.SkipLocked()),
		"SELECT `id` FROM `users` FOR SHARE SKIP LOCKED")
}

func TestSelectForUpdateOfTables(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users FOR UPDATE OF users",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).ForUpdate(sql.LockOf("users")),
		"SELECT `id` FROM `users` FOR UPDATE OF `users`")
}

func TestSelectIntersect(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1 INTERSECT SELECT a FROM t2",
		sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t1")}).
			Intersect(sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t2")})),
		"(SELECT `a` FROM `t1`) INTERSECT (SELECT `a` FROM `t2`)")
}

func TestSelectExcept(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1 EXCEPT SELECT a FROM t2",
		sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t1")}).
			Except(sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t2")})),
		"(SELECT `a` FROM `t1`) EXCEPT (SELECT `a` FROM `t2`)")
}

func TestSelectIntersectAllExceptAll(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1 INTERSECT ALL SELECT a FROM t2",
		sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t1")}).
			IntersectAll(sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t2")})),
		"(SELECT `a` FROM `t1`) INTERSECT ALL (SELECT `a` FROM `t2`)")

	assertRoundTrip(t,
		"SELECT a FROM t1 EXCEPT ALL SELECT a FROM t2",
		sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t1")}).
			ExceptAll(sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t2")})),
		"(SELECT `a` FROM `t1`) EXCEPT ALL (SELECT `a` FROM `t2`)")
}

// A three-way set operation folds left-to-right and carries a statement-level
// ORDER BY / LIMIT on the compound.
func TestSelectUnionChainOrderLimit(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t1 UNION SELECT a FROM t2 UNION SELECT a FROM t3 ORDER BY a LIMIT 5 OFFSET 2",
		sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t1")}).
			Union(sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t2")})).
			Union(sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t3")})).
			OrderBy(sql.Asc(sql.Col("a"))).Limit(5).Offset(2),
		"(SELECT `a` FROM `t1`) UNION (SELECT `a` FROM `t2`) UNION (SELECT `a` FROM `t3`) ORDER BY `a` LIMIT 5 OFFSET 2")
}

// A parenthesized set operation as the left operand of another exercises the
// nested SetOprSelectList path (a list element that is itself a set list).
func TestSelectNestedSetOperation(t *testing.T) {
	assertRoundTrip(t,
		"(SELECT a FROM t1 UNION SELECT a FROM t2) UNION ALL SELECT a FROM t3",
		sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t1")}).
			Union(sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t2")})).
			UnionAll(sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t3")})),
		"((SELECT `a` FROM `t1`) UNION (SELECT `a` FROM `t2`)) UNION ALL (SELECT `a` FROM `t3`)")
}

// A set operation used as a FROM subquery source exercises the set-operation
// branch of the result-set/subquery converter.
func TestSelectSetOperationSubquerySource(t *testing.T) {
	assertRoundTrip(t,
		"SELECT x.a FROM ((SELECT a FROM t1) UNION (SELECT a FROM t2)) x",
		sql.Select(sql.Col("x.a")).From(sql.Alias(
			sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t1")}).
				Union(sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t2")})),
			"x",
		)),
		"SELECT `x`.`a` FROM ((SELECT `a` FROM `t1`) UNION (SELECT `a` FROM `t2`)) AS `x`")
}

// A parenthesized inner set list with its own LIMIT exercises the inner-list
// ORDER BY / LIMIT branch of the set-operation list converter.
func TestSelectNestedSetOperationWithLimit(t *testing.T) {
	assertRoundTrip(t,
		"(SELECT a FROM t1 UNION SELECT a FROM t2 LIMIT 3) UNION ALL SELECT a FROM t3",
		sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t1")}).
			Union(sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t2")})).
			Limit(3).
			UnionAll(sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t3")})),
		"((SELECT `a` FROM `t1`) UNION (SELECT `a` FROM `t2`) LIMIT 3) UNION ALL (SELECT `a` FROM `t3`)")
}

// A WINDOW clause names a window the SELECT list references by name.
func TestSelectNamedWindowClause(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id, ROW_NUMBER() OVER w FROM emp WINDOW w AS (PARTITION BY dept ORDER BY salary)",
		sql.Select(
			sql.Col("id"),
			sql.Over(sql.Func("ROW_NUMBER")).Named("w"),
		).From(sql.Table{Name: sql.NewIdent("emp")}).
			Window(sql.WindowDef("w").PartitionBy(sql.Col("dept")).OrderBy(sql.Asc(sql.Col("salary")))),
		"SELECT `id`, ROW_NUMBER() OVER `w` FROM `emp` WINDOW `w` AS (PARTITION BY `dept` ORDER BY `salary`)")
}

// A bare NULL and a bare float literal in the select list render as raw constants.
func TestSelectRawLiterals(t *testing.T) {
	assertRoundTrip(t,
		"SELECT NULL, 1.5, 'x' FROM t",
		sql.Select(sql.Raw("NULL"), sql.Raw("1.5"), sql.Raw("'x'")).From(sql.Table{Name: sql.NewIdent("t")}),
		"SELECT NULL, 1.5, 'x' FROM `t`")
}

// A LIMIT bound that is not an integer constant has no typed home and round-trips
// raw.
func TestSelectNonIntegerLimitFallsBackToRaw(t *testing.T) {
	assertRaw(t, "SELECT id FROM t LIMIT ?")
}

// A window frame clause and a window referencing another window both exceed
// sqlkit's named-window model and round-trip raw.
func TestSelectWindowFrameFallsBackToRaw(t *testing.T) {
	assertRaw(t, "SELECT SUM(x) OVER (ORDER BY id ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) FROM t")
}

// GROUP BY ... WITH ROLLUP has no typed home and round-trips raw (companion to the
// existing rollup test, exercised here with HAVING).
func TestSelectScalarSubqueryInWhere(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE x = (SELECT MAX(y) FROM u)",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).
			Where(sql.Eq(sql.Col("x"),
				sql.ScalarSubquery(sql.Select(sql.Func("MAX", sql.Col("y"))).From(sql.Table{Name: sql.NewIdent("u")})))),
		"SELECT `id` FROM `t` WHERE (`x` = (SELECT MAX(`y`) FROM `u`))")
}

// TiDB tolerates an empty backtick-quoted table name (FROM “), which the
// compiler cannot render; the statement falls back to raw rather than error.
func TestEmptyTableNameFallsBackToRaw(t *testing.T) {
	assertRaw(t, "SELECT a FROM ``")
}

// A ? placeholder in the select list is an unbound ValueExpr; it must pass
// through verbatim rather than render its nil value as NULL.
func TestSelectPlaceholderPreserved(t *testing.T) {
	assertRoundTrip(t,
		"SELECT ? AS a FROM t",
		sql.Select(sql.As(sql.Raw("?"), "a")).From(sql.Table{Name: sql.NewIdent("t")}),
		"SELECT ? AS `a` FROM `t`")
}

// A quoted identifier containing a dot (`a.b`) is a single column name, but
// sqlkit stores a column as a dotted string and re-splits it to recover the
// qualifier, so it cannot be told apart from the qualified a.b; it round-trips
// as raw rather than rendering the wrong `a`.`b`.
func TestColumnIdentifierWithDotFallsBackToRaw(t *testing.T) {
	assertRaw(t, "SELECT `a.b` FROM t")
}
