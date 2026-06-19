package postgres_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse/postgres"
)

// sel builds SELECT id FROM t WHERE <where> for the common expression case.
func selWhere(where sql.Expression) sql.Query {
	return sql.Select(sql.Col("id")).From(sql.Table{Name: "t"}).Where(where)
}

func TestExprComparisons(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a <> 1 AND b <= 2 AND c >= 3",
		selWhere(sql.And(sql.Ne(sql.Col("a"), 1), sql.Le(sql.Col("b"), 2), sql.Ge(sql.Col("c"), 3))),
		`SELECT "id" FROM "t" WHERE (("a" <> $1) AND ("b" <= $2) AND ("c" >= $3))`,
		1, 2, 3)
}

// A numeric (decimal) literal is kept verbatim rather than parameterized as a
// lossy float64, so its exact spelling round-trips.
func TestExprDecimalLiteral(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE price > 12.34",
		selWhere(sql.Gt(sql.Col("price"), sql.Raw("12.34"))),
		`SELECT "id" FROM "t" WHERE ("price" > 12.34)`)
}

// A wide decimal that no Go numeric type can hold exactly still round-trips
// because the literal text is preserved.
func TestExprWideDecimalLiteral(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE n = 99999999999999999999.999999999",
		selWhere(sql.Eq(sql.Col("n"), sql.Raw("99999999999999999999.999999999"))),
		`SELECT "id" FROM "t" WHERE ("n" = 99999999999999999999.999999999)`)
}

// A negated numeric literal keeps its verbatim spelling with the sign folded in.
func TestExprNegativeDecimalLiteral(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE balance < -0.5",
		selWhere(sql.Lt(sql.Col("balance"), sql.Raw("-0.5"))),
		`SELECT "id" FROM "t" WHERE ("balance" < -0.5)`)
}

// Niladic SQL value functions round-trip as their verbatim keyword.
func TestExprSQLValueFunctions(t *testing.T) {
	cases := []struct {
		expr string
		raw  string
	}{
		{"CURRENT_DATE", "CURRENT_DATE"},
		{"CURRENT_TIMESTAMP", "CURRENT_TIMESTAMP"},
		{"CURRENT_USER", "CURRENT_USER"},
		{"SESSION_USER", "SESSION_USER"},
	}
	for _, tc := range cases {
		assertRoundTrip(t,
			"SELECT id FROM t WHERE created_by = "+tc.expr,
			selWhere(sql.Eq(sql.Col("created_by"), sql.Raw(tc.raw))),
			`SELECT "id" FROM "t" WHERE ("created_by" = `+tc.raw+`)`)
	}
}

// CURRENT_TIMESTAMP with a precision is not modeled, so the statement falls back
// to raw.
func TestExprCurrentTimestampPrecisionFallsBackToRaw(t *testing.T) {
	assertRaw(t, "SELECT CURRENT_TIMESTAMP(3)")
}

func TestExprBooleanTest(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE active IS TRUE AND deleted IS NOT TRUE",
		selWhere(sql.And(
			sql.BinaryExpression{Left: sql.Col("active"), Operator: "IS", Right: sql.Raw("TRUE")},
			sql.BinaryExpression{Left: sql.Col("deleted"), Operator: "IS NOT", Right: sql.Raw("TRUE")},
		)),
		`SELECT "id" FROM "t" WHERE (("active" IS TRUE) AND ("deleted" IS NOT TRUE))`)
}

// INSERT ... DEFAULT VALUES is not modeled by the builder; it round-trips raw.
func TestInsertDefaultValuesFallsBackToRaw(t *testing.T) {
	assertRaw(t, "INSERT INTO t DEFAULT VALUES")
}

func TestExprArithmetic(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a + 1 = b * 2",
		selWhere(sql.Eq(sql.Add(sql.Col("a"), 1), sql.Mul(sql.Col("b"), 2))),
		`SELECT "id" FROM "t" WHERE (("a" + $1) = ("b" * $2))`,
		1, 2)
}

func TestExprOr(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a = 1 OR b = 2",
		selWhere(sql.Or(sql.Eq(sql.Col("a"), 1), sql.Eq(sql.Col("b"), 2))),
		`SELECT "id" FROM "t" WHERE (("a" = $1) OR ("b" = $2))`,
		1, 2)
}

func TestExprNot(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE NOT a = 1",
		selWhere(sql.Not(sql.Eq(sql.Col("a"), 1))),
		`SELECT "id" FROM "t" WHERE (NOT ("a" = $1))`,
		1)
}

func TestExprLike(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE name LIKE 'a%' AND email NOT LIKE '%x'",
		selWhere(sql.And(sql.Like(sql.Col("name"), "a%"), sql.NotLike(sql.Col("email"), "%x"))),
		`SELECT "id" FROM "t" WHERE (("name" LIKE $1) AND ("email" NOT LIKE $2))`,
		"a%", "%x")
}

func TestExprILike(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE name ILIKE 'a%'",
		selWhere(sql.ILike(sql.Col("name"), "a%")),
		`SELECT "id" FROM "t" WHERE ("name" ILIKE $1)`,
		"a%")
}

func TestExprIn(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE id IN (1, 2, 3)",
		selWhere(sql.In(sql.Col("id"), 1, 2, 3)),
		`SELECT "id" FROM "t" WHERE ("id" IN ($1, $2, $3))`,
		1, 2, 3)
}

func TestExprNotIn(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE id NOT IN (1, 2)",
		selWhere(sql.NotIn(sql.Col("id"), 1, 2)),
		`SELECT "id" FROM "t" WHERE ("id" NOT IN ($1, $2))`,
		1, 2)
}

func TestExprBetween(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE age BETWEEN 18 AND 30",
		selWhere(sql.Between(sql.Col("age"), 18, 30)),
		`SELECT "id" FROM "t" WHERE ("age" BETWEEN $1 AND $2)`,
		18, 30)
}

func TestExprNullTest(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a IS NULL AND b IS NOT NULL",
		selWhere(sql.And(sql.IsNull(sql.Col("a")), sql.IsNotNull(sql.Col("b")))),
		`SELECT "id" FROM "t" WHERE (("a" IS NULL) AND ("b" IS NOT NULL))`)
}

func TestExprInSubquery(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users WHERE id IN (SELECT uid FROM orders)",
		sql.Select(sql.Col("id")).From(sql.Table{Name: "users"}).
			Where(sql.InQuery(sql.Col("id"), sql.Select(sql.Col("uid")).From(sql.Table{Name: "orders"}))),
		`SELECT "id" FROM "users" WHERE ("id" IN (SELECT "uid" FROM "orders"))`)
}

func TestExprAnyQuantified(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users WHERE id > ANY (SELECT uid FROM orders)",
		sql.Select(sql.Col("id")).From(sql.Table{Name: "users"}).
			Where(sql.Any(sql.Col("id"), ">", sql.Select(sql.Col("uid")).From(sql.Table{Name: "orders"}))),
		`SELECT "id" FROM "users" WHERE ("id" > ANY (SELECT "uid" FROM "orders"))`)
}

func TestExprExists(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users u WHERE EXISTS (SELECT 1 FROM orders o WHERE o.uid = u.id)",
		sql.Select(sql.Col("id")).From(sql.SourceAlias(sql.Table{Name: "users"}, "u")).
			Where(sql.Exists(sql.Select(sql.Raw("1")).From(sql.SourceAlias(sql.Table{Name: "orders"}, "o")).
				Where(sql.Eq(sql.Col("o.uid"), sql.Col("u.id"))))),
		`SELECT "id" FROM "users" AS "u" WHERE EXISTS (SELECT 1 FROM "orders" AS "o" WHERE ("o"."uid" = "u"."id"))`)
}

func TestExprCaseSearched(t *testing.T) {
	assertRoundTrip(t,
		"SELECT CASE WHEN a > 0 THEN 'pos' ELSE 'neg' END FROM t",
		sql.Select(sql.Case().When(sql.Gt(sql.Col("a"), 0), "pos").Else("neg")).From(sql.Table{Name: "t"}),
		`SELECT CASE WHEN ("a" > $1) THEN $2 ELSE $3 END FROM "t"`,
		0, "pos", "neg")
}

func TestExprCast(t *testing.T) {
	assertRoundTrip(t,
		"SELECT CAST(a AS integer), b::text FROM t",
		sql.Select(sql.Cast(sql.Col("a"), "integer"), sql.Cast(sql.Col("b"), "text")).From(sql.Table{Name: "t"}),
		`SELECT CAST("a" AS integer), CAST("b" AS text) FROM "t"`)
}

func TestExprFunctionCall(t *testing.T) {
	assertRoundTrip(t,
		"SELECT coalesce(a, b), lower(name) FROM t",
		sql.Select(sql.Func("coalesce", sql.Col("a"), sql.Col("b")), sql.Func("lower", sql.Col("name"))).
			From(sql.Table{Name: "t"}),
		`SELECT coalesce("a", "b"), lower("name") FROM "t"`)
}

func TestExprBooleanLiteral(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE active = true",
		selWhere(sql.Eq(sql.Col("active"), true)),
		`SELECT "id" FROM "t" WHERE ("active" = $1)`,
		true)
}

func TestExprNegativeNumber(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE balance = -5",
		selWhere(sql.Eq(sql.Col("balance"), -5)),
		`SELECT "id" FROM "t" WHERE ("balance" = $1)`,
		-5)
}

func TestExprSimpleCase(t *testing.T) {
	assertRoundTrip(t,
		"SELECT CASE status WHEN 'a' THEN 1 ELSE 0 END FROM t",
		sql.Select(sql.CaseOf(sql.Col("status")).When("a", 1).Else(0)).From(sql.Table{Name: "t"}),
		`SELECT CASE "status" WHEN $1 THEN $2 ELSE $3 END FROM "t"`,
		"a", 1, 0)
}

func TestExprDistinctAggregates(t *testing.T) {
	assertRoundTrip(t,
		"SELECT COUNT(DISTINCT uid), SUM(DISTINCT amount) FROM orders",
		sql.Select(sql.CountDistinct(sql.Col("uid")), sql.DistinctFunc("sum", sql.Col("amount"))).
			From(sql.Table{Name: "orders"}),
		`SELECT COUNT(DISTINCT "uid"), sum(DISTINCT "amount") FROM "orders"`)
}

func TestExprIsDistinctFrom(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a IS DISTINCT FROM b AND c IS NOT DISTINCT FROM d",
		selWhere(sql.And(sql.IsDistinctFrom(sql.Col("a"), sql.Col("b")), sql.IsNotDistinctFrom(sql.Col("c"), sql.Col("d")))),
		`SELECT "id" FROM "t" WHERE (("a" IS DISTINCT FROM "b") AND ("c" IS NOT DISTINCT FROM "d"))`)
}

func TestExprRowIn(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE (a, b) IN ((1, 2), (3, 4))",
		selWhere(sql.In(sql.Row(sql.Col("a"), sql.Col("b")), sql.Row(1, 2), sql.Row(3, 4))),
		`SELECT "id" FROM "t" WHERE (("a", "b") IN (($1, $2), ($3, $4)))`,
		1, 2, 3, 4)
}

func TestExprArrayAny(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE x = ANY(ARRAY[1, 2, 3])",
		selWhere(sql.AnyArray(sql.Col("x"), "=", sql.Array(1, 2, 3))),
		`SELECT "id" FROM "t" WHERE ("x" = ANY(ARRAY[$1, $2, $3]))`,
		1, 2, 3)
}

func TestExprSimilarTo(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE name SIMILAR TO 'a%' AND x NOT SIMILAR TO 'b%'",
		selWhere(sql.And(sql.SimilarTo(sql.Col("name"), "a%"), sql.NotSimilarTo(sql.Col("x"), "b%"))),
		`SELECT "id" FROM "t" WHERE (("name" SIMILAR TO $1) AND ("x" NOT SIMILAR TO $2))`,
		"a%", "b%")
}

// TestParsePositions checks the parser records each column reference and string
// literal's byte offset into the source, so a caller can map a node back to its
// place in the original SQL.
func TestParsePositions(t *testing.T) {
	src := "SELECT id FROM users WHERE name = 'taro'"
	q, err := postgres.ParseOne(src)
	if err != nil {
		t.Fatal(err)
	}
	sel, ok := sql.Unwrap(q).(sql.SelectStmt)
	if !ok {
		t.Fatalf("not a SELECT: %T", sql.Unwrap(q))
	}
	bin, ok := sel.Where.(sql.BinaryExpression)
	if !ok {
		t.Fatalf("WHERE is not a comparison: %T", sel.Where)
	}
	if bin.Pos() == sql.NoPos {
		t.Error("comparison node carries no position")
	}
	col, ok := bin.Left.(sql.ColumnExpression)
	if !ok {
		t.Fatalf("left operand is not a column: %T", bin.Left)
	}
	val, ok := bin.Right.(sql.ValueExpression)
	if !ok {
		t.Fatalf("right operand is not a value: %T", bin.Right)
	}

	if got, want := col.Pos(), strings.Index(src, "name"); got != want {
		t.Errorf("column offset = %d, want %d", got, want)
	} else if src[got:got+len("name")] != "name" {
		t.Errorf("column offset %d does not point at \"name\"", got)
	}
	if got, want := val.Pos(), strings.Index(src, "'taro'"); got != want {
		t.Errorf("literal offset = %d, want %d", got, want)
	} else if !strings.HasPrefix(src[got:], "'taro'") {
		t.Errorf("literal offset %d does not point at \"'taro'\"", got)
	}
}

// TestParseCompositePositions checks the parser records positions on composite
// expression nodes too, not only the leaves: the WHERE's AND and each of its
// operands (an IN list and a LIKE comparison) report a position.
func TestParseCompositePositions(t *testing.T) {
	src := "SELECT id FROM users WHERE id IN (1, 2) AND name LIKE 'a%'"
	q, err := postgres.ParseOne(src)
	if err != nil {
		t.Fatal(err)
	}
	sel := sql.Unwrap(q).(sql.SelectStmt)
	and, ok := sel.Where.(sql.LogicalExpression)
	if !ok {
		t.Fatalf("WHERE is not an AND: %T", sel.Where)
	}
	if and.Pos() == sql.NoPos {
		t.Error("AND node carries no position")
	}
	if len(and.Operands) != 2 {
		t.Fatalf("AND has %d operands, want 2", len(and.Operands))
	}
	for _, op := range and.Operands {
		n, ok := op.(sql.Node)
		if !ok {
			t.Fatalf("operand %T does not satisfy Node", op)
		}
		if n.Pos() == sql.NoPos {
			t.Errorf("operand %T carries no position", op)
		}
	}
}
