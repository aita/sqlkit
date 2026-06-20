package sqlite_test

import (
	"reflect"
	"testing"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse/sqlite"
	"github.com/google/go-cmp/cmp"
)

// astCmpOpts compare two ASTs structurally, unexported fields included, but
// ignore source positions: the parser records a byte offset (NodePos) on every
// node it builds, while the builder-constructed want carries NoPos, so a
// structural test must look past them. NodePos is a plain int, so it is ignored
// by field name. Positions have their own test (pos_test.go).
var astCmpOpts = cmp.Options{
	cmp.Exporter(func(reflect.Type) bool { return true }),
	cmp.FilterPath(func(p cmp.Path) bool {
		sf, ok := p.Last().(cmp.StructField)
		return ok && sf.Name() == "NodePos"
	}, cmp.Ignore()),
}

// assertRoundTrip parses input and checks it both ways, for maximum precision:
//   - the parsed query is structurally identical to want (every field but source
//     positions, unexported included), proving the parser builds the expected AST
//     nodes — not merely an SQL string that recompiles the same way;
//   - that query compiles under PostgreSQLDialect to wantSQL with wantArgs. The
//     SQLite engine and its dialect were dropped from sqlkit, so a parsed SQLite
//     statement is exercised against the dialect-agnostic AST and a concrete
//     output dialect. Because sqlkit parameterizes literals, the recompiled SQL
//     replaces constants with $N.
func assertRoundTrip(t *testing.T, input string, want sql.Query, wantSQL string, wantArgs ...any) {
	t.Helper()
	got, err := sqlite.ParseOne(input)
	if err != nil {
		t.Fatalf("ParseOne(%q) error = %v", input, err)
	}
	if diff := cmp.Diff(want, got, astCmpOpts...); diff != "" {
		t.Fatalf("AST mismatch for %q (-want +got):\n%s", input, diff)
	}
	gotSQL, args, err := got.ToSQL(sql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if gotSQL != wantSQL {
		t.Fatalf("round-trip SQL =\n  %q\nwant\n  %q", gotSQL, wantSQL)
	}
	if len(wantArgs) == 0 {
		if len(args) != 0 {
			t.Fatalf("args = %#v, want none", args)
		}
		return
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

// assertSQL parses input and checks only that it compiles under PostgreSQLDialect
// to wantSQL with wantArgs. It covers constructs whose expected AST is too
// verbose to hand-build (recursive CTEs, window definitions); the structural
// guarantee is carried by the assertRoundTrip cases.
func assertSQL(t *testing.T, input, wantSQL string, wantArgs ...any) {
	t.Helper()
	got, err := sqlite.ParseOne(input)
	if err != nil {
		t.Fatalf("ParseOne(%q) error = %v", input, err)
	}
	gotSQL, args, err := got.ToSQL(sql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if gotSQL != wantSQL {
		t.Fatalf("SQL for %q =\n  %q\nwant\n  %q", input, gotSQL, wantSQL)
	}
	if len(wantArgs) == 0 {
		if len(args) != 0 {
			t.Fatalf("args = %#v, want none", args)
		}
		return
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

// assertRaw checks that input parses to a verbatim RawQuery — the deparse
// fallback the parser uses for statements the typed converters do not model —
// and that the passthrough is stable: the RawQuery emits its text unchanged with
// no args, and that text re-parses to an equal RawQuery.
func assertRaw(t *testing.T, input string) {
	t.Helper()
	got, err := sqlite.ParseOne(input)
	if err != nil {
		t.Fatalf("ParseOne(%q) error = %v", input, err)
	}
	raw, ok := got.(sql.RawQuery)
	if !ok {
		t.Fatalf("ParseOne(%q) = %T, want sql.RawQuery", input, got)
	}
	text, args, err := raw.ToSQL(sql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if len(args) != 0 {
		t.Fatalf("RawQuery args = %#v, want none", args)
	}
	if text != raw.SQL() {
		t.Fatalf("RawQuery text = %q, want %q", text, raw.SQL())
	}
	again, err := sqlite.ParseOne(text)
	if err != nil {
		t.Fatalf("re-parse of %q error = %v", text, err)
	}
	if !reflect.DeepEqual(again, got) {
		t.Fatalf("deparse not stable for %q", input)
	}
}

func tbl(name string) sql.Table { return sql.Table{Name: sql.NewIdent(name)} }

func TestSelectColumnsAndWhere(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id, name FROM users WHERE active = 1",
		sql.Select(sql.Col("id"), sql.Col("name")).From(tbl("users")).
			Where(sql.Eq(sql.Col("active"), 1)),
		`SELECT "id", "name" FROM "users" WHERE ("active" = $1)`,
		1)
}

func TestSelectAliasStarAndQualifiedStar(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a, b AS x, t.* FROM users t",
		sql.Select(sql.Col("a"), sql.As(sql.Col("b"), "x"), sql.StarOf("t")).
			From(sql.SourceAlias(tbl("users"), "t")),
		`SELECT "a", "b" AS "x", "t".* FROM "users" AS "t"`)
}

func TestSelectStar(t *testing.T) {
	assertRoundTrip(t, "SELECT * FROM t",
		sql.Select(sql.Star()).From(tbl("t")),
		`SELECT * FROM "t"`)
}

func TestSelectDistinct(t *testing.T) {
	assertRoundTrip(t, "SELECT DISTINCT a FROM t",
		sql.Select(sql.Col("a")).From(tbl("t")).Distinct(),
		`SELECT DISTINCT "a" FROM "t"`)
}

func TestSelectLeftJoinOn(t *testing.T) {
	assertRoundTrip(t,
		"SELECT * FROM users u LEFT JOIN orders o ON o.user_id = u.id",
		sql.Select(sql.Star()).From(sql.SourceAlias(tbl("users"), "u")).
			LeftJoin(sql.SourceAlias(tbl("orders"), "o"),
				sql.Eq(sql.Col("o.user_id"), sql.Col("u.id"))),
		`SELECT * FROM "users" AS "u" LEFT JOIN "orders" AS "o" ON ("o"."user_id" = "u"."id")`)
}

func TestSelectJoinUsing(t *testing.T) {
	assertRoundTrip(t,
		"SELECT * FROM a JOIN b USING (id)",
		sql.Select(sql.Star()).From(tbl("a")).JoinUsing(tbl("b"), "id"),
		`SELECT * FROM "a" JOIN "b" USING ("id")`)
}

func TestSelectCrossJoinComma(t *testing.T) {
	assertRoundTrip(t,
		"SELECT * FROM a, b",
		sql.Select(sql.Star()).From(tbl("a")).CrossJoin(tbl("b")),
		`SELECT * FROM "a" CROSS JOIN "b"`)
}

func TestSelectGroupByHaving(t *testing.T) {
	assertRoundTrip(t,
		"SELECT k, COUNT(*) FROM t GROUP BY k HAVING COUNT(*) > 1",
		sql.Select(sql.Col("k"), sql.CountAll()).From(tbl("t")).
			GroupBy(sql.Col("k")).Having(sql.Gt(sql.CountAll(), 1)),
		`SELECT "k", COUNT(*) FROM "t" GROUP BY "k" HAVING (COUNT(*) > $1)`,
		1)
}

func TestSelectOrderLimitOffset(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t ORDER BY a DESC, b LIMIT 10 OFFSET 5",
		sql.Select(sql.Col("a")).From(tbl("t")).
			OrderBy(sql.Desc(sql.Col("a")), sql.Asc(sql.Col("b"))).Limit(10).Offset(5),
		`SELECT "a" FROM "t" ORDER BY "a" DESC, "b" LIMIT 10 OFFSET 5`)
}

func TestSelectSubquerySource(t *testing.T) {
	assertRoundTrip(t,
		"SELECT b.id FROM (SELECT id FROM t) AS b",
		sql.Select(sql.Col("b.id")).
			From(sql.Alias(sql.Select(sql.Col("id")).From(tbl("t")), "b")),
		`SELECT "b"."id" FROM (SELECT "id" FROM "t") AS "b"`)
}

func TestExprLogicalAndNot(t *testing.T) {
	assertRoundTrip(t,
		"SELECT * FROM t WHERE a = 1 AND (b = 2 OR NOT (c = 3))",
		sql.Select(sql.Star()).From(tbl("t")).Where(
			sql.And(sql.Eq(sql.Col("a"), 1),
				sql.Or(sql.Eq(sql.Col("b"), 2), sql.Not(sql.Eq(sql.Col("c"), 3))))),
		`SELECT * FROM "t" WHERE (("a" = $1) AND (("b" = $2) OR (NOT ("c" = $3))))`,
		1, 2, 3)
}

func TestExprInList(t *testing.T) {
	assertRoundTrip(t,
		"SELECT * FROM t WHERE x IN (1, 2, 3)",
		sql.Select(sql.Star()).From(tbl("t")).Where(sql.In(sql.Col("x"), 1, 2, 3)),
		`SELECT * FROM "t" WHERE ("x" IN ($1, $2, $3))`,
		1, 2, 3)
}

func TestExprBetweenAndIsNull(t *testing.T) {
	assertRoundTrip(t,
		"SELECT * FROM t WHERE (z BETWEEN 1 AND 9) AND (y IS NULL)",
		sql.Select(sql.Star()).From(tbl("t")).Where(
			sql.And(sql.Between(sql.Col("z"), 1, 9), sql.IsNull(sql.Col("y")))),
		`SELECT * FROM "t" WHERE (("z" BETWEEN $1 AND $2) AND ("y" IS NULL))`,
		1, 9)
}

func TestExprArithmeticAndConcat(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a + b * 2, x || y FROM t",
		sql.Select(
			sql.BinaryExpression{Left: sql.Col("a"), Operator: "+",
				Right: sql.BinaryExpression{Left: sql.Col("b"), Operator: "*", Right: sql.Val(2)}},
			sql.Concat(sql.Col("x"), sql.Col("y")),
		).From(tbl("t")),
		`SELECT ("a" + ("b" * $1)), ("x" || "y") FROM "t"`,
		2)
}

func TestExprCountDistinct(t *testing.T) {
	assertRoundTrip(t,
		"SELECT COUNT(*), COUNT(DISTINCT id) FROM t",
		sql.Select(sql.CountAll(), sql.CountDistinct(sql.Col("id"))).From(tbl("t")),
		`SELECT COUNT(*), COUNT(DISTINCT "id") FROM "t"`)
}

func TestExprCase(t *testing.T) {
	assertRoundTrip(t,
		"SELECT CASE WHEN a > 0 THEN 'p' ELSE 'n' END FROM t",
		sql.Select(sql.Case().When(sql.Gt(sql.Col("a"), 0), "p").Else("n")).From(tbl("t")),
		`SELECT CASE WHEN ("a" > $1) THEN $2 ELSE $3 END FROM "t"`,
		0, "p", "n")
}

func TestExprExists(t *testing.T) {
	assertRoundTrip(t,
		"SELECT * FROM t WHERE EXISTS (SELECT 1 FROM u WHERE u.t_id = t.id)",
		sql.Select(sql.Star()).From(tbl("t")).Where(sql.Exists(
			sql.Select(sql.Raw("1")).From(tbl("u")).
				Where(sql.Eq(sql.Col("u.t_id"), sql.Col("t.id"))))),
		`SELECT * FROM "t" WHERE EXISTS (SELECT 1 FROM "u" WHERE ("u"."t_id" = "t"."id"))`)
}

func TestExprScalarSubquery(t *testing.T) {
	assertRoundTrip(t,
		"SELECT (SELECT MAX(x) FROM u) FROM t",
		sql.Select(sql.ScalarSubquery(
			sql.Select(sql.Func("MAX", sql.Col("x"))).From(tbl("u")))).From(tbl("t")),
		`SELECT (SELECT MAX("x") FROM "u") FROM "t"`)
}

func TestExprInSubquery(t *testing.T) {
	assertRoundTrip(t,
		"SELECT * FROM t WHERE x IN (SELECT id FROM u)",
		sql.Select(sql.Star()).From(tbl("t")).Where(
			sql.InQuery(sql.Col("x"), sql.Select(sql.Col("id")).From(tbl("u")))),
		`SELECT * FROM "t" WHERE ("x" IN (SELECT "id" FROM "u"))`)
}

func TestCompoundUnion(t *testing.T) {
	assertRoundTrip(t,
		"SELECT a FROM t UNION SELECT a FROM s",
		sql.Select(sql.Col("a")).From(tbl("t")).
			Union(sql.Select(sql.Col("a")).From(tbl("s"))),
		`(SELECT "a" FROM "t") UNION (SELECT "a" FROM "s")`)
}

func TestCompoundChainWithOrderLimit(t *testing.T) {
	assertSQL(t,
		"SELECT a FROM t UNION ALL SELECT a FROM s INTERSECT SELECT a FROM r ORDER BY a LIMIT 5",
		`((SELECT "a" FROM "t") UNION ALL (SELECT "a" FROM "s")) INTERSECT (SELECT "a" FROM "r") ORDER BY "a" LIMIT 5`)
}

func TestCTE(t *testing.T) {
	assertSQL(t,
		"WITH c AS (SELECT id FROM t) SELECT id FROM c",
		`WITH "c" AS (SELECT "id" FROM "t") SELECT "id" FROM "c"`)
}

func TestRecursiveCTE(t *testing.T) {
	assertSQL(t,
		"WITH RECURSIVE nums(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM nums WHERE n < 10) SELECT n FROM nums",
		`WITH RECURSIVE "nums" ("n") AS ((SELECT 1) UNION ALL (SELECT ("n" + $1) FROM "nums" WHERE ("n" < $2))) SELECT "n" FROM "nums"`,
		1, 10)
}

func TestWindowInline(t *testing.T) {
	assertSQL(t,
		"SELECT ROW_NUMBER() OVER (PARTITION BY k ORDER BY id DESC) FROM t",
		`SELECT ROW_NUMBER() OVER (PARTITION BY "k" ORDER BY "id" DESC) FROM "t"`)
}

func TestWindowNamed(t *testing.T) {
	assertSQL(t,
		"SELECT SUM(x) OVER w FROM t WINDOW w AS (PARTITION BY k)",
		`SELECT SUM("x") OVER "w" FROM "t" WINDOW "w" AS (PARTITION BY "k")`)
}

func TestInsertMultiRow(t *testing.T) {
	assertRoundTrip(t,
		"INSERT INTO t (a, b) VALUES (1, 2), (3, 4)",
		sql.InsertInto(tbl("t")).Columns("a", "b").Values(1, 2).Values(3, 4),
		`INSERT INTO "t" ("a", "b") VALUES ($1, $2), ($3, $4)`,
		1, 2, 3, 4)
}

func TestInsertFromSelect(t *testing.T) {
	assertRoundTrip(t,
		"INSERT INTO t (a) SELECT a FROM s",
		sql.InsertInto(tbl("t")).Columns("a").
			FromSelect(sql.Select(sql.Col("a")).From(tbl("s"))),
		`INSERT INTO "t" ("a") SELECT "a" FROM "s"`)
}

func TestInsertUpsert(t *testing.T) {
	assertRoundTrip(t,
		"INSERT INTO t (a) VALUES (1) ON CONFLICT (a) DO UPDATE SET b = excluded.b",
		sql.InsertInto(tbl("t")).Columns("a").Values(1).
			OnConflict("a").DoUpdate(sql.Set("b", sql.Excluded("b"))),
		`INSERT INTO "t" ("a") VALUES ($1) ON CONFLICT ("a") DO UPDATE SET "b" = excluded."b"`,
		1)
}

func TestInsertDoNothing(t *testing.T) {
	assertRoundTrip(t,
		"INSERT INTO t (a) VALUES (1) ON CONFLICT DO NOTHING",
		sql.InsertInto(tbl("t")).Columns("a").Values(1).DoNothing(),
		`INSERT INTO "t" ("a") VALUES ($1) ON CONFLICT DO NOTHING`,
		1)
}

func TestUpdateReturning(t *testing.T) {
	assertRoundTrip(t,
		"UPDATE t SET a = 1, b = 2 WHERE id = 3 RETURNING id",
		sql.Update(tbl("t")).Set(sql.Set("a", 1), sql.Set("b", 2)).
			Where(sql.Eq(sql.Col("id"), 3)).Returning(sql.Col("id")),
		`UPDATE "t" SET "a" = $1, "b" = $2 WHERE ("id" = $3) RETURNING "id"`,
		1, 2, 3)
}

func TestDelete(t *testing.T) {
	assertRoundTrip(t,
		"DELETE FROM t WHERE id = 1",
		sql.DeleteFrom(tbl("t")).Where(sql.Eq(sql.Col("id"), 1)),
		`DELETE FROM "t" WHERE ("id" = $1)`,
		1)
}

func TestCreateTable(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, age INT DEFAULT 0)",
		sql.CreateTable(tbl("t")).
			Column(sql.ColumnDef{Name: sql.NewIdent("id"), Type: sql.EngineType{Name: "INTEGER"}, AutoIncrement: true}).
			Column(sql.ColumnDef{Name: sql.NewIdent("name"), Type: sql.RawType("TEXT"), NotNull: true}).
			Column(sql.ColumnDef{Name: sql.NewIdent("age"), Type: sql.RawType("INT"), Default: sql.Raw("0")}).
			PrimaryKey("id"),
		"CREATE TABLE \"t\" (\n    \"id\" integer,\n    \"name\" TEXT NOT NULL,\n    \"age\" INT DEFAULT 0,\n    PRIMARY KEY (\"id\")\n);")
}

func TestCreateTableTablePrimaryKey(t *testing.T) {
	assertRoundTrip(t,
		"CREATE TABLE IF NOT EXISTS t (a INT, b INT, PRIMARY KEY (a, b))",
		sql.CreateTable(tbl("t")).IfNotExists().
			Column(sql.ColumnDef{Name: sql.NewIdent("a"), Type: sql.RawType("INT")}).
			Column(sql.ColumnDef{Name: sql.NewIdent("b"), Type: sql.RawType("INT")}).
			PrimaryKey("a", "b"),
		"CREATE TABLE IF NOT EXISTS \"t\" (\n    \"a\" INT,\n    \"b\" INT,\n    PRIMARY KEY (\"a\", \"b\")\n);")
}

func TestCreateIndex(t *testing.T) {
	assertRoundTrip(t,
		"CREATE UNIQUE INDEX idx ON t (a, b)",
		sql.CreateIndex("idx", tbl("t"), "a", "b").Unique(),
		`CREATE UNIQUE INDEX "idx" ON "t" ("a", "b");`)
}

func TestDropTable(t *testing.T) {
	assertRoundTrip(t,
		"DROP TABLE IF EXISTS t",
		sql.DropTable(tbl("t")).IfExists(),
		`DROP TABLE IF EXISTS "t";`)
}

// Statements and constructs the typed converters do not model still parse,
// falling back to a verbatim RawQuery that round-trips.
func TestUnmodeledFallsBackToRaw(t *testing.T) {
	for _, input := range []string{
		"REPLACE INTO t (a) VALUES (1)",
		"INSERT INTO t DEFAULT VALUES",
		"DROP INDEX idx",
		"ALTER TABLE t ADD COLUMN c INT",
		"CREATE VIEW v AS SELECT 1",
		"CREATE TABLE t AS SELECT a FROM s",
		"CREATE TABLE t (id INTEGER, c TEXT COLLATE NOCASE)",
	} {
		assertRaw(t, input)
	}
}

func TestParseMultipleStatements(t *testing.T) {
	queries, err := sqlite.Parse("SELECT 1; SELECT 2")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(queries) != 2 {
		t.Fatalf("Parse() returned %d queries, want 2", len(queries))
	}
}

func TestParseOneRejectsMultiple(t *testing.T) {
	if _, err := sqlite.ParseOne("SELECT 1; SELECT 2"); err == nil {
		t.Fatal("ParseOne() with two statements succeeded, want error")
	}
}

func TestParseSyntaxError(t *testing.T) {
	if _, err := sqlite.Parse("SELECT FROM"); err == nil {
		t.Fatal("Parse() of invalid SQL succeeded, want error")
	}
}
