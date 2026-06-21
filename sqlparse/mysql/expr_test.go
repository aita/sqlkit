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

// A left-deep chain of OR flattens into a single multi-operand node, matching a
// hand-built Or.
func TestExprOrFlattening(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a = 1 OR b = 2 OR c = 3",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).Where(sql.Or(
			sql.Eq(sql.Col("a"), 1),
			sql.Eq(sql.Col("b"), 2),
			sql.Eq(sql.Col("c"), 3),
		)),
		"SELECT `id` FROM `t` WHERE ((`a` = ?) OR (`b` = ?) OR (`c` = ?))",
		1, 2, 3)
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

func TestExprRegexp(t *testing.T) {
	// REGEXP maps to a verbatim binary operator (sqlkit has no dedicated MySQL
	// REGEXP node on the build side here).
	assertRoundTrip(t,
		"SELECT id FROM t WHERE name REGEXP '^a'",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).
			Where(sql.BinaryExpression{Left: sql.Col("name"), Operator: "REGEXP", Right: sql.Val("^a")}),
		"SELECT `id` FROM `t` WHERE (`name` REGEXP ?)",
		"^a")
}

func TestExprNotRegexp(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE name NOT REGEXP '^a'",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).
			Where(sql.Not(sql.BinaryExpression{Left: sql.Col("name"), Operator: "REGEXP", Right: sql.Val("^a")})),
		"SELECT `id` FROM `t` WHERE (NOT (`name` REGEXP ?))",
		"^a")
}

func TestExprAnySubquery(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a = ANY (SELECT x FROM u)",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).
			Where(sql.Any(sql.Col("a"), "=", sql.Select(sql.Col("x")).From(sql.Table{Name: sql.NewIdent("u")}))),
		"SELECT `id` FROM `t` WHERE (`a` = ANY (SELECT `x` FROM `u`))")
}

func TestExprAllSubquery(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a > ALL (SELECT x FROM u)",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).
			Where(sql.All(sql.Col("a"), ">", sql.Select(sql.Col("x")).From(sql.Table{Name: sql.NewIdent("u")}))),
		"SELECT `id` FROM `t` WHERE (`a` > ALL (SELECT `x` FROM `u`))")
}

func TestExprNotInSubquery(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE id NOT IN (SELECT uid FROM bans)",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).
			Where(sql.NotInQuery(sql.Col("id"), sql.Select(sql.Col("uid")).From(sql.Table{Name: sql.NewIdent("bans")}))),
		"SELECT `id` FROM `t` WHERE (`id` NOT IN (SELECT `uid` FROM `bans`))")
}

func TestExprNotExists(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE NOT EXISTS (SELECT 1 FROM u)",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).
			Where(sql.Not(sql.Exists(sql.Select(sql.Raw("1")).From(sql.Table{Name: sql.NewIdent("u")})))),
		"SELECT `id` FROM `t` WHERE (NOT EXISTS (SELECT 1 FROM `u`))")
}

func TestExprBitwiseNot(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE ~a = 0",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).
			Where(sql.Eq(sql.Unary("~", sql.Col("a")), 0)),
		"SELECT `id` FROM `t` WHERE ((~`a`) = ?)",
		0)
}

func TestExprBitwiseOperators(t *testing.T) {
	// & | ^ << >> % DIV all map to verbatim binary operators.
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a & b = 1 AND c | d = 2 AND e ^ f = 3",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).Where(sql.And(
			sql.Eq(sql.BinaryExpression{Left: sql.Col("a"), Operator: "&", Right: sql.Col("b")}, 1),
			sql.Eq(sql.BinaryExpression{Left: sql.Col("c"), Operator: "|", Right: sql.Col("d")}, 2),
			sql.Eq(sql.BinaryExpression{Left: sql.Col("e"), Operator: "^", Right: sql.Col("f")}, 3),
		)),
		"SELECT `id` FROM `t` WHERE (((`a` & `b`) = ?) AND ((`c` | `d`) = ?) AND ((`e` ^ `f`) = ?))",
		1, 2, 3)
}

func TestExprShiftAndModAndDiv(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a << 1 = b >> 2 AND c % 2 = d DIV 3",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).Where(sql.And(
			sql.Eq(
				sql.BinaryExpression{Left: sql.Col("a"), Operator: "<<", Right: sql.Val(1)},
				sql.BinaryExpression{Left: sql.Col("b"), Operator: ">>", Right: sql.Val(2)},
			),
			sql.Eq(
				sql.BinaryExpression{Left: sql.Col("c"), Operator: "%", Right: sql.Val(2)},
				sql.BinaryExpression{Left: sql.Col("d"), Operator: "DIV", Right: sql.Val(3)},
			),
		)),
		"SELECT `id` FROM `t` WHERE (((`a` << ?) = (`b` >> ?)) AND ((`c` % ?) = (`d` DIV ?)))",
		1, 2, 2, 3)
}

func TestExprNullSafeNotEqualXor(t *testing.T) {
	// XOR maps to a verbatim binary operator.
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a XOR b",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).
			Where(sql.BinaryExpression{Left: sql.Col("a"), Operator: "XOR", Right: sql.Col("b")}),
		"SELECT `id` FROM `t` WHERE (`a` XOR `b`)")
}

func TestExprIsNotNull(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a IS NOT NULL",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).
			Where(sql.IsNotNull(sql.Col("a"))),
		"SELECT `id` FROM `t` WHERE (`a` IS NOT NULL)")
}

func TestExprRow(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE (a, b) IN ((1, 2), (3, 4))",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).
			Where(sql.In(sql.Row(sql.Col("a"), sql.Col("b")),
				sql.Row(sql.Val(1), sql.Val(2)), sql.Row(sql.Val(3), sql.Val(4)))),
		"SELECT `id` FROM `t` WHERE ((`a`, `b`) IN ((?, ?), (?, ?)))",
		1, 2, 3, 4)
}

// A positional placeholder passes through verbatim as ?.
func TestExprParamMarker(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a = ?",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).
			Where(sql.BinaryExpression{Left: sql.Col("a"), Operator: "=", Right: sql.Raw("?")}),
		"SELECT `id` FROM `t` WHERE (`a` = ?)")
}

// A CASE with an operand (simple CASE) maps to sqlkit's CaseOf and renders each
// WHEN value as a bound comparison value.
func TestExprCaseOf(t *testing.T) {
	assertRoundTrip(t,
		"SELECT CASE status WHEN 1 THEN 'a' WHEN 2 THEN 'b' END FROM t",
		sql.Select(
			sql.CaseOf(sql.Col("status")).
				When(sql.Val(1), sql.Val("a")).
				When(sql.Val(2), sql.Val("b")),
		).From(sql.Table{Name: sql.NewIdent("t")}),
		"SELECT CASE `status` WHEN ? THEN ? WHEN ? THEN ? END FROM `t`",
		1, "a", 2, "b")
}

// A unary plus is a no-op the converter unwraps to its operand.
func TestExprUnaryPlus(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE +a = 1",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).
			Where(sql.Eq(sql.Col("a"), 1)),
		"SELECT `id` FROM `t` WHERE (`a` = ?)",
		1)
}

// NOT over a parenthesized logical expression wraps the whole conjunction.
func TestExprNotLogical(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE NOT (a AND b)",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).
			Where(sql.Not(sql.And(sql.Col("a"), sql.Col("b")))),
		"SELECT `id` FROM `t` WHERE (NOT (`a` AND `b`))")
}

// A schema-qualified function call has no typed home and round-trips raw.
func TestExprSchemaQualifiedFunctionFallsBackToRaw(t *testing.T) {
	assertRaw(t, "SELECT mydb.myfunc(a) FROM t")
}

// Hexadecimal and bit-string literals decode to the driver's binary-literal type,
// a fmt.Stringer, so the converter preserves their exact text in a Decimal rather
// than rounding through a float.
func TestExprHexAndBitLiterals(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a = 0x1F AND b = b'101'",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).Where(sql.And(
			sql.Eq(sql.Col("a"), mysql.Decimal("0x1f")),
			sql.Eq(sql.Col("b"), mysql.Decimal("0x05")),
		)),
		"SELECT `id` FROM `t` WHERE ((`a` = ?) AND (`b` = ?))",
		mysql.Decimal("0x1f"), mysql.Decimal("0x05"))
}

// Decimal carries the exact literal text as a driver.Valuer and a fmt.Stringer.
func TestDecimalValueAndString(t *testing.T) {
	d := mysql.Decimal("100.00")
	if got := d.String(); got != "100.00" {
		t.Errorf("String() = %q, want %q", got, "100.00")
	}
	v, err := d.Value()
	if err != nil {
		t.Fatalf("Value() error = %v", err)
	}
	if v != "100.00" {
		t.Errorf("Value() = %#v, want %q", v, "100.00")
	}
}

func TestExprUnmodeledFallsBackToRaw(t *testing.T) {
	assertRaw(t, "SELECT id FROM t WHERE name LIKE 'a%' ESCAPE '!'")
}
