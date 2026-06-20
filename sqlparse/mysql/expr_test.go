package mysql_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse/mysql"
)

func TestExprLiterals(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a = 1 AND b = 'x' AND c = 2.5 AND d IS NULL",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).Where(sql.And(
			sql.Eq(sql.Col("a"), 1),
			sql.Eq(sql.Col("b"), "x"),
			sql.Eq(sql.Col("c"), mysql.Decimal("2.5")),
			sql.IsNull(sql.Col("d")),
		)),
		"SELECT `id` FROM `t` WHERE ((`a` = ?) AND (`b` = ?) AND (`c` = ?) AND (`d` IS NULL))",
		1, "x", mysql.Decimal("2.5"))
}

func TestExprDecimalPreservesScale(t *testing.T) {
	// Fixed-point literals keep their exact digits (scale and trailing zeros),
	// which a float64 conversion would drop — 100.00 must not become 100.
	assertRoundTrip(t,
		"SELECT id FROM t WHERE price = 100.00 AND rate = 0.10",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).Where(sql.And(
			sql.Eq(sql.Col("price"), mysql.Decimal("100.00")),
			sql.Eq(sql.Col("rate"), mysql.Decimal("0.10")),
		)),
		"SELECT `id` FROM `t` WHERE ((`price` = ?) AND (`rate` = ?))",
		mysql.Decimal("100.00"), mysql.Decimal("0.10"))
}

func TestExprNegativeDecimal(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE balance = -2.50",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).
			Where(sql.Eq(sql.Col("balance"), mysql.Decimal("-2.50"))),
		"SELECT `id` FROM `t` WHERE (`balance` = ?)",
		mysql.Decimal("-2.50"))
}

func TestExprBooleanLiterals(t *testing.T) {
	// MySQL has no boolean type: TRUE and FALSE are the integers 1 and 0.
	assertRoundTrip(t,
		"SELECT id FROM t WHERE active = TRUE",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).Where(sql.Eq(sql.Col("active"), 1)),
		"SELECT `id` FROM `t` WHERE (`active` = ?)",
		1)
}

func TestExprNegativeNumber(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE balance > -50",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).Where(sql.Gt(sql.Col("balance"), -50)),
		"SELECT `id` FROM `t` WHERE (`balance` > ?)",
		-50)
}

func TestExprArithmetic(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a + b * 2 = c",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).Where(
			sql.Eq(
				sql.BinaryExpression{
					Left:     sql.Col("a"),
					Operator: "+",
					Right: sql.BinaryExpression{
						Left: sql.Col("b"), Operator: "*", Right: sql.Val(2),
					},
				},
				sql.Col("c"),
			)),
		"SELECT `id` FROM `t` WHERE ((`a` + (`b` * ?)) = `c`)",
		2)
}

func TestExprComparisonOperators(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a <> 1 AND b <= 2 AND c >= 3",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).Where(sql.And(
			sql.Ne(sql.Col("a"), 1),
			sql.Le(sql.Col("b"), 2),
			sql.Ge(sql.Col("c"), 3),
		)),
		"SELECT `id` FROM `t` WHERE ((`a` <> ?) AND (`b` <= ?) AND (`c` >= ?))",
		1, 2, 3)
}

func TestExprNullSafeEqual(t *testing.T) {
	// MySQL's null-safe equality <=> is IS NOT DISTINCT FROM.
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a <=> b",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).
			Where(sql.IsNotDistinctFrom(sql.Col("a"), sql.Col("b"))),
		"SELECT `id` FROM `t` WHERE (`a` <=> `b`)")
}

func TestExprLikeInBetween(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE name LIKE 'a%' AND x IN (1, 2) AND y NOT BETWEEN 3 AND 4",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).Where(sql.And(
			sql.Like(sql.Col("name"), "a%"),
			sql.In(sql.Col("x"), 1, 2),
			sql.NotBetween(sql.Col("y"), 3, 4),
		)),
		"SELECT `id` FROM `t` WHERE ((`name` LIKE ?) AND (`x` IN (?, ?)) AND (`y` NOT BETWEEN ? AND ?))",
		"a%", 1, 2, 3, 4)
}

func TestExprNotLikeAndNotIn(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE name NOT LIKE 'a%' AND x NOT IN (1, 2)",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).Where(sql.And(
			sql.NotLike(sql.Col("name"), "a%"),
			sql.NotIn(sql.Col("x"), 1, 2),
		)),
		"SELECT `id` FROM `t` WHERE ((`name` NOT LIKE ?) AND (`x` NOT IN (?, ?)))",
		"a%", 1, 2)
}

func TestExprNot(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE NOT active = 1",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).
			Where(sql.Not(sql.Eq(sql.Col("active"), 1))),
		"SELECT `id` FROM `t` WHERE (NOT (`active` = ?))",
		1)
}

func TestExprCase(t *testing.T) {
	assertRoundTrip(t,
		"SELECT CASE WHEN a > 0 THEN 'pos' ELSE 'neg' END FROM t",
		sql.Select(
			sql.Case().When(sql.Gt(sql.Col("a"), 0), sql.Val("pos")).Else(sql.Val("neg")),
		).From(sql.Table{Name: sql.NewIdent("t")}),
		"SELECT CASE WHEN (`a` > ?) THEN ? ELSE ? END FROM `t`",
		0, "pos", "neg")
}

func TestExprFunctionCall(t *testing.T) {
	assertRoundTrip(t,
		"SELECT UPPER(name), COALESCE(a, b) FROM t",
		sql.Select(
			sql.Func("UPPER", sql.Col("name")),
			sql.Func("COALESCE", sql.Col("a"), sql.Col("b")),
		).From(sql.Table{Name: sql.NewIdent("t")}),
		"SELECT UPPER(`name`), COALESCE(`a`, `b`) FROM `t`")
}

func TestExprScalarSubquery(t *testing.T) {
	assertRoundTrip(t,
		"SELECT (SELECT MAX(price) FROM products) AS top FROM stores",
		sql.Select(sql.As(
			sql.ScalarSubquery(sql.Select(sql.Func("MAX", sql.Col("price"))).From(sql.Table{Name: sql.NewIdent("products")})),
			"top",
		)).From(sql.Table{Name: sql.NewIdent("stores")}),
		"SELECT (SELECT MAX(`price`) FROM `products`) AS `top` FROM `stores`")
}

func TestExprExists(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users u WHERE EXISTS (SELECT 1 FROM orders o WHERE o.uid = u.id)",
		sql.Select(sql.Col("id")).From(sql.SourceAlias(sql.Table{Name: sql.NewIdent("users")}, "u")).
			Where(sql.Exists(sql.Select(sql.Raw("1")).
				From(sql.SourceAlias(sql.Table{Name: sql.NewIdent("orders")}, "o")).
				Where(sql.Eq(sql.Col("o.uid"), sql.Col("u.id"))))),
		"SELECT `id` FROM `users` AS `u` WHERE EXISTS (SELECT 1 FROM `orders` AS `o` WHERE (`o`.`uid` = `u`.`id`))")
}

func TestExprInSubquery(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users WHERE id IN (SELECT uid FROM orders)",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).
			Where(sql.InQuery(sql.Col("id"), sql.Select(sql.Col("uid")).From(sql.Table{Name: sql.NewIdent("orders")}))),
		"SELECT `id` FROM `users` WHERE (`id` IN (SELECT `uid` FROM `orders`))")
}

func TestExprCast(t *testing.T) {
	assertRoundTrip(t,
		"SELECT CAST(x AS SIGNED), CAST(y AS CHAR(10)) FROM t",
		sql.Select(
			sql.Cast(sql.Col("x"), "SIGNED"),
			sql.Cast(sql.Col("y"), "CHAR(10)"),
		).From(sql.Table{Name: sql.NewIdent("t")}),
		"SELECT CAST(`x` AS SIGNED), CAST(`y` AS CHAR(10)) FROM `t`")
}

func TestExprUnmodeledFallsBackToRaw(t *testing.T) {
	assertRaw(t, "SELECT id FROM t WHERE name LIKE 'a%' ESCAPE '!'")
}
