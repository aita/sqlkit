package postgres_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

// sel builds SELECT id FROM t WHERE <where> for the common expression case.
func selWhere(where sql.Expression) sql.Query {
	return sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).Where(where)
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
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).
			Where(sql.InQuery(sql.Col("id"), sql.Select(sql.Col("uid")).From(sql.Table{Name: sql.NewIdent("orders")}))),
		`SELECT "id" FROM "users" WHERE ("id" IN (SELECT "uid" FROM "orders"))`)
}

func TestExprAnyQuantified(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users WHERE id > ANY (SELECT uid FROM orders)",
		sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("users")}).
			Where(sql.Any(sql.Col("id"), ">", sql.Select(sql.Col("uid")).From(sql.Table{Name: sql.NewIdent("orders")}))),
		`SELECT "id" FROM "users" WHERE ("id" > ANY (SELECT "uid" FROM "orders"))`)
}

func TestExprExists(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM users u WHERE EXISTS (SELECT 1 FROM orders o WHERE o.uid = u.id)",
		sql.Select(sql.Col("id")).From(sql.SourceAlias(sql.Table{Name: sql.NewIdent("users")}, "u")).
			Where(sql.Exists(sql.Select(sql.Raw("1")).From(sql.SourceAlias(sql.Table{Name: sql.NewIdent("orders")}, "o")).
				Where(sql.Eq(sql.Col("o.uid"), sql.Col("u.id"))))),
		`SELECT "id" FROM "users" AS "u" WHERE EXISTS (SELECT 1 FROM "orders" AS "o" WHERE ("o"."uid" = "u"."id"))`)
}

func TestExprCaseSearched(t *testing.T) {
	assertRoundTrip(t,
		"SELECT CASE WHEN a > 0 THEN 'pos' ELSE 'neg' END FROM t",
		sql.Select(sql.Case().When(sql.Gt(sql.Col("a"), 0), "pos").Else("neg")).From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT CASE WHEN ("a" > $1) THEN $2 ELSE $3 END FROM "t"`,
		0, "pos", "neg")
}

func TestExprCast(t *testing.T) {
	assertRoundTrip(t,
		"SELECT CAST(a AS integer), b::text FROM t",
		sql.Select(sql.Cast(sql.Col("a"), "integer"), sql.Cast(sql.Col("b"), "text")).From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT CAST("a" AS integer), CAST("b" AS text) FROM "t"`)
}

// A length-less bpchar must stay bpchar, not canonicalize to char: bare char is
// char(1), so rendering bpchar as char would change its meaning and re-parsing
// would add the (1). bpchar canonicalizes to char only when it carries a length.
func TestExprBpcharCast(t *testing.T) {
	assertRoundTrip(t,
		"SELECT CAST(a AS bpchar) FROM t",
		sql.Select(sql.Cast(sql.Col("a"), "bpchar")).From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT CAST("a" AS bpchar) FROM "t"`)
	assertRoundTrip(t,
		"SELECT CAST(a AS bpchar(5)) FROM t",
		sql.Select(sql.Cast(sql.Col("a"), "char(5)")).From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT CAST("a" AS char(5)) FROM "t"`)
}

func TestExprFunctionCall(t *testing.T) {
	assertRoundTrip(t,
		"SELECT coalesce(a, b), lower(name) FROM t",
		sql.Select(sql.Func("coalesce", sql.Col("a"), sql.Col("b")), sql.Func("lower", sql.Col("name"))).
			From(sql.Table{Name: sql.NewIdent("t")}),
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
		sql.Select(sql.CaseOf(sql.Col("status")).When("a", 1).Else(0)).From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT CASE "status" WHEN $1 THEN $2 ELSE $3 END FROM "t"`,
		"a", 1, 0)
}

func TestExprDistinctAggregates(t *testing.T) {
	assertRoundTrip(t,
		"SELECT COUNT(DISTINCT uid), SUM(DISTINCT amount) FROM orders",
		sql.Select(sql.CountDistinct(sql.Col("uid")), sql.DistinctFunc("sum", sql.Col("amount"))).
			From(sql.Table{Name: sql.NewIdent("orders")}),
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

// A unary minus written with a space (so PostgreSQL does not fold it into a
// single signed constant) reaches convertUnaryOp, which negates the literal in
// place. A plain integer becomes a signed value parameter.
func TestExprUnaryMinusInteger(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a = - 5",
		selWhere(sql.Eq(sql.Col("a"), -5)),
		`SELECT "id" FROM "t" WHERE ("a" = $1)`,
		-5)
}

// A unary plus on a constant is a no-op: the value is returned unchanged.
func TestExprUnaryPlusInteger(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a = +5",
		selWhere(sql.Eq(sql.Col("a"), 5)),
		`SELECT "id" FROM "t" WHERE ("a" = $1)`,
		5)
}

// A unary minus on a decimal literal keeps the verbatim spelling, prefixing the
// minus rather than evaluating it.
func TestExprUnaryMinusDecimal(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a = - 1.5",
		selWhere(sql.Eq(sql.Col("a"), sql.Raw("-1.5"))),
		`SELECT "id" FROM "t" WHERE ("a" = -1.5)`)
}

// A unary operator applied to a non-constant (a column reference) is modeled as
// a UnaryExpression so the typed shape round-trips — matching the compiler,
// which emits sql.Neg/sql.Pos as "(-x)".
func TestExprUnaryMinusColumn(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a = -b",
		selWhere(sql.Eq(sql.Col("a"), sql.Neg(sql.Col("b")))),
		`SELECT "id" FROM "t" WHERE ("a" = (-"b"))`)
}

// GREATEST/LEAST map to the corresponding lower-cased function calls.
func TestExprGreatestLeast(t *testing.T) {
	assertRoundTrip(t,
		"SELECT GREATEST(a, b), LEAST(c, d) FROM t",
		sql.Select(sql.Func("greatest", sql.Col("a"), sql.Col("b")), sql.Func("least", sql.Col("c"), sql.Col("d"))).
			From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT greatest("a", "b"), least("c", "d") FROM "t"`)
}

// The remaining niladic SQL value functions round-trip as their verbatim
// keyword (the common subset is already covered in expr_test.go).
func TestExprMoreSQLValueFunctions(t *testing.T) {
	for _, kw := range []string{
		"LOCALTIME",
		"LOCALTIMESTAMP",
		"CURRENT_ROLE",
		"CURRENT_CATALOG",
		"CURRENT_SCHEMA",
		"USER",
	} {
		assertRoundTrip(t,
			"SELECT id FROM t WHERE x = "+kw,
			selWhere(sql.Eq(sql.Col("x"), sql.Raw(kw))),
			`SELECT "id" FROM "t" WHERE ("x" = `+kw+`)`)
	}
}

// PostgreSQL parses a 'false' boolean literal as a 'bool' typecast of the
// string 'f', which convertTypeCast folds back to a boolean value.
func TestExprBooleanFalseLiteral(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE active = false",
		selWhere(sql.Eq(sql.Col("active"), false)),
		`SELECT "id" FROM "t" WHERE ("active" = $1)`,
		false)
}

// NULLIF(a, b) maps to a nullif function call.
func TestExprNullIf(t *testing.T) {
	assertRoundTrip(t,
		"SELECT NULLIF(a, b) FROM t",
		sql.Select(sql.Func("nullif", sql.Col("a"), sql.Col("b"))).From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT nullif("a", "b") FROM "t"`)
}

// x <> ALL(ARRAY[...]) maps to an ALL-quantified array comparison.
func TestExprArrayAllQuantified(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE x <> ALL(ARRAY[1, 2])",
		selWhere(sql.AllArray(sql.Col("x"), "<>", sql.Array(1, 2))),
		`SELECT "id" FROM "t" WHERE ("x" <> ALL(ARRAY[$1, $2]))`,
		1, 2)
}

// IN with a list of string literals binds each as a parameter.
func TestExprInStringList(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a IN ('x', 'y')",
		selWhere(sql.In(sql.Col("a"), "x", "y")),
		`SELECT "id" FROM "t" WHERE ("a" IN ($1, $2))`,
		"x", "y")
}

// NOT ILIKE (the !~~* operator) maps to a negated ILIKE.
func TestExprNotILike(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a NOT ILIKE 'x'",
		selWhere(sql.Not(sql.ILike(sql.Col("a"), "x"))),
		`SELECT "id" FROM "t" WHERE (NOT ("a" ILIKE $1))`,
		"x")
}

// FROM ONLY t drops the ONLY qualifier (the builder does not model inheritance
// control) and parses to a plain table source.
func TestSelectFromOnly(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM ONLY t",
		sql.Select(sql.Col("a")).From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT "a" FROM "t"`)
}

// A set-returning function in FROM is not modeled and round-trips raw.
func TestSelectFunctionSourceFallsBackToRaw(t *testing.T) {
	assertRaw(t, "SELECT * FROM generate_series(1, 5)")
}

// BETWEEN SYMMETRIC is not modeled and round-trips raw.
func TestExprBetweenSymmetricFallsBackToRaw(t *testing.T) {
	assertRaw(t, "SELECT id FROM t WHERE x BETWEEN SYMMETRIC 1 AND 5")
}

// IS FALSE / IS UNKNOWN map to IS comparisons against the verbatim truth-value
// keyword (the TRUE cases are covered in expr_test.go).
func TestExprBooleanTestFalseUnknown(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a IS FALSE AND b IS UNKNOWN",
		selWhere(sql.And(
			sql.BinaryExpression{Left: sql.Col("a"), Operator: "IS", Right: sql.Raw("FALSE")},
			sql.BinaryExpression{Left: sql.Col("b"), Operator: "IS", Right: sql.Raw("UNKNOWN")},
		)),
		`SELECT "id" FROM "t" WHERE (("a" IS FALSE) AND ("b" IS UNKNOWN))`)
}

// IS NOT FALSE / IS NOT UNKNOWN map to IS NOT comparisons.
func TestExprBooleanTestNotFalseNotUnknown(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE a IS NOT FALSE AND b IS NOT UNKNOWN",
		selWhere(sql.And(
			sql.BinaryExpression{Left: sql.Col("a"), Operator: "IS NOT", Right: sql.Raw("FALSE")},
			sql.BinaryExpression{Left: sql.Col("b"), Operator: "IS NOT", Right: sql.Raw("UNKNOWN")},
		)),
		`SELECT "id" FROM "t" WHERE (("a" IS NOT FALSE) AND ("b" IS NOT UNKNOWN))`)
}

// An ON CONFLICT target listing several parenthesised columns is treated as an
// expression target (each column becomes a reference).
func TestInsertOnConflictMultipleExprTargets(t *testing.T) {
	assertRoundTrip(t,
		"INSERT INTO t (a) VALUES (1) ON CONFLICT ((a), (b)) DO NOTHING",
		sql.InsertInto(sql.Table{Name: sql.NewIdent("t")}).Columns("a").Values(1).
			OnConflictExpr(sql.Col("a"), sql.Col("b")).DoNothing(),
		`INSERT INTO "t" ("a") VALUES ($1) ON CONFLICT (("a"), ("b")) DO NOTHING`,
		1)
}

// An array typecast renders the element type with a trailing [] suffix.
func TestExprArrayCast(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a::int[] FROM t",
		sql.Select(sql.Cast(sql.Col("a"), "integer[]")).From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT CAST("a" AS integer[]) FROM "t"`)
}

// A string literal cast to boolean is not the t/f form PostgreSQL uses for
// TRUE/FALSE, so it stays a plain CAST with the value bound.
func TestExprStringCastToBoolean(t *testing.T) {
	assertRoundTrip(t,
		"SELECT '1'::boolean FROM t",
		sql.Select(sql.Cast(sql.Val("1"), "boolean")).From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT CAST($1 AS boolean) FROM "t"`,
		"1")
}

// ALTER TABLE ... ADD CONSTRAINT CHECK with a boolean predicate exercises the
// CHECK branch of applyAddConstraint together with staticExpr's BoolExpr path.
func TestAlterTableAddCheckBooleanExpr(t *testing.T) {
	assertRoundTrip(t,
		"ALTER TABLE t ADD CONSTRAINT c CHECK (a > 0 AND b > 0)",
		sql.AlterTable(sql.Table{Name: sql.NewIdent("t")}).AddCheck("c", sql.And(
			sql.BinaryExpression{Left: sql.Col("a"), Operator: ">", Right: sql.Raw("0")},
			sql.BinaryExpression{Left: sql.Col("b"), Operator: ">", Right: sql.Raw("0")},
		)),
		`ALTER TABLE "t" ADD CONSTRAINT "c" CHECK ((("a" > 0) AND ("b" > 0)));`)
}

// ALTER TABLE ... ADD PRIMARY KEY is not modeled by the typed builder and
// round-trips raw.
func TestAlterTableAddPrimaryKeyFallsBackToRaw(t *testing.T) {
	assertRaw(t, "ALTER TABLE t ADD PRIMARY KEY (a)")
}

// An inline column REFERENCES that names multiple referenced columns is not a
// valid column-level foreign key and keeps the whole statement raw.
func TestCreateTableMultiColumnInlineReferenceFallsBackToRaw(t *testing.T) {
	assertRaw(t, "CREATE TABLE t (a int, b int, uid int REFERENCES users (a, b))")
}

// The string concatenation operator parses to a binary "||" expression.
func TestExprConcatOperator(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a || b FROM t",
		sql.Select(sql.BinaryExpression{Left: sql.Col("a"), Operator: "||", Right: sql.Col("b")}).
			From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT ("a" || "b") FROM "t"`)
}

// The POSIX regular-expression match operators each parse to a binary
// expression carrying the verbatim operator.
func TestExprRegexpOperators(t *testing.T) {
	for _, op := range []string{"~", "!~", "~*", "!~*"} {
		assertRoundTrip(t,
			"SELECT id FROM t WHERE name "+op+" 'x'",
			selWhere(sql.BinaryExpression{Left: sql.Col("name"), Operator: op, Right: sql.Val("x")}),
			`SELECT "id" FROM "t" WHERE ("name" `+op+` $1)`,
			"x")
	}
}

// The JSON/JSONB accessor operators parse to binary expressions; the right
// operand is bound as a parameter.
func TestExprJSONAccessOperators(t *testing.T) {
	cases := []struct {
		op  string
		arg string
	}{
		{"->", "a"},
		{"->>", "a"},
		{"#>", "{a,b}"},
		{"#>>", "{a}"},
	}
	for _, tc := range cases {
		assertRoundTrip(t,
			"SELECT data"+tc.op+"'"+tc.arg+"' FROM t",
			sql.Select(sql.BinaryExpression{Left: sql.Col("data"), Operator: tc.op, Right: sql.Val(tc.arg)}).
				From(sql.Table{Name: sql.NewIdent("t")}),
			`SELECT ("data" `+tc.op+` $1) FROM "t"`,
			tc.arg)
	}
}

// x <> ALL (subquery) is the canonical spelling of NOT IN with a subquery.
func TestExprNotInSubquery(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE x <> ALL (SELECT y FROM u)",
		selWhere(sql.NotInQuery(sql.Col("x"), sql.Select(sql.Col("y")).From(sql.Table{Name: sql.NewIdent("u")}))),
		`SELECT "id" FROM "t" WHERE ("x" NOT IN (SELECT "y" FROM "u"))`)
}

// x <> ANY (subquery) keeps the operator as an ANY-quantified comparison.
func TestExprAnyQuantifiedOperator(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE x <> ANY (SELECT y FROM u)",
		selWhere(sql.Any(sql.Col("x"), "<>", sql.Select(sql.Col("y")).From(sql.Table{Name: sql.NewIdent("u")}))),
		`SELECT "id" FROM "t" WHERE ("x" <> ANY (SELECT "y" FROM "u"))`)
}

// A GROUPING SETS element that nests ROLLUP is not modeled and round-trips raw.
func TestGroupByNestedGroupingSetFallsBackToRaw(t *testing.T) {
	assertRaw(t, "SELECT a FROM t GROUP BY GROUPING SETS (ROLLUP (a, b))")
}

// An empty grouping set GROUP BY () is not modeled and round-trips raw.
func TestGroupByEmptySetFallsBackToRaw(t *testing.T) {
	assertRaw(t, "SELECT a FROM t GROUP BY ()")
}

// A cast to a quoted type name that is not a valid bare identifier (e.g. "0")
// must keep its quotes, or CAST(x AS 0) is invalid SQL.
func TestExprCastToQuotedTypeName(t *testing.T) {
	assertRoundTrip(t,
		`SELECT CAST(a AS "0") FROM t`,
		sql.Select(sql.Cast(sql.Col("a"), `"0"`)).From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT CAST("a" AS "0") FROM "t"`)
}

// A cast to a quoted, mixed-case type name must keep its quotes, or an unquoted
// upper-case identifier folds to lower case on re-parse and breaks the fixpoint.
func TestExprCastToMixedCaseTypeName(t *testing.T) {
	assertRoundTrip(t,
		`SELECT CAST(a AS "MyType") FROM t`,
		sql.Select(sql.Cast(sql.Col("a"), `"MyType"`)).From(sql.Table{Name: sql.NewIdent("t")}),
		`SELECT CAST("a" AS "MyType") FROM "t"`)
}

// A quoted identifier containing a dot ("a.b") is a single column name, but
// sqlkit stores a column as a dotted string and re-splits it to recover the
// qualifier, so it cannot be told apart from the qualified a.b. Such a name has
// no faithful typed form and round-trips as raw.
func TestColumnIdentifierWithDotFallsBackToRaw(t *testing.T) {
	assertRaw(t, `SELECT "a.b" FROM t`)
}

// An empty ROW() is parseable but the compiler requires at least one row
// element, so it round-trips as raw rather than erroring.
func TestEmptyRowFallsBackToRaw(t *testing.T) {
	assertRaw(t, "SELECT a FROM t WHERE r = ROW()")
}

// A function name that is not a bare identifier (a quoted name with a space or an
// embedded quote) has no faithful unquoted form, since sqlkit renders a function
// name unquoted; it round-trips as raw rather than emitting invalid SQL.
func TestNonBareFunctionNameFallsBackToRaw(t *testing.T) {
	assertRaw(t, `SELECT "My Func"()`)
}
