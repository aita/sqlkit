package postgres_test

import (
	"reflect"
	"testing"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse/postgres"
	"github.com/google/go-cmp/cmp"
)

// astCmpOpts compare two ASTs structurally, unexported fields included, but
// ignore source positions: the parser records a byte offset (NodePos) on every
// node it builds, while the builder-constructed want carries NoPos, so a
// structural test must look past them. NodePos is a plain int, so it is ignored
// by field name rather than by type. Positions have their own test (pos_test.go).
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
//   - that query compiles under PostgreSQLDialect to wantSQL with wantArgs,
//     documenting the output and guarding the sql-core rendering. Because sqlkit
//     parameterizes literals, the recompiled SQL replaces constants with $N.
func assertRoundTrip(t *testing.T, input string, want sql.Query, wantSQL string, wantArgs ...any) {
	t.Helper()
	got, err := postgres.ParseOne(input)
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

// assertRaw checks that input parses to a verbatim RawQuery — the deparse
// fallback the parser uses for statements (and sub-constructs) the typed
// converters do not model — and that the passthrough is stable: the RawQuery
// emits its text unchanged with no args, and that text re-parses to an equal
// RawQuery.
func assertRaw(t *testing.T, input string) sql.RawQuery {
	t.Helper()
	got, err := postgres.ParseOne(input)
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
	again, err := postgres.ParseOne(text)
	if err != nil {
		t.Fatalf("re-parse of %q error = %v", text, err)
	}
	if !reflect.DeepEqual(again, got) {
		t.Fatalf("deparse not stable for %q:\n got: %#v\nwant: %#v", input, again, got)
	}
	return raw
}

// Statement types the typed converters do not model (here a utility command)
// still parse, falling back to a verbatim RawQuery.
func TestUnmodeledStatementFallsBackToRaw(t *testing.T) {
	for _, input := range []string{
		"VACUUM users",
		"SET search_path TO public",
		"LISTEN events",
	} {
		assertRaw(t, input)
	}
}

func TestParseMultipleStatements(t *testing.T) {
	queries, err := postgres.Parse("SELECT 1; SELECT 2")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(queries) != 2 {
		t.Fatalf("Parse() returned %d queries, want 2", len(queries))
	}
}

func TestParseOneRejectsMultiple(t *testing.T) {
	if _, err := postgres.ParseOne("SELECT 1; SELECT 2"); err == nil {
		t.Fatal("ParseOne() with two statements succeeded, want error")
	}
}

func TestParseSyntaxError(t *testing.T) {
	if _, err := postgres.Parse("SELECT FROM WHERE"); err == nil {
		t.Fatal("Parse() of invalid SQL succeeded, want error")
	}
}

// CREATE INDEX forms the typed builder cannot model each report Unsupported and
// round-trip via the verbatim raw passthrough.
func TestCreateIndexUnsupportedFormsFallBackToRaw(t *testing.T) {
	for _, input := range []string{
		"CREATE INDEX CONCURRENTLY i ON t (a)",
		"CREATE INDEX i ON t USING gin (a)",
		"CREATE INDEX i ON t (a DESC)",
		"CREATE INDEX i ON t (lower(a))",
		"CREATE INDEX i ON t (a) WHERE a > 0",
		"CREATE INDEX i ON t (a) INCLUDE (b)",
		"CREATE UNIQUE INDEX i ON t (a) NULLS NOT DISTINCT",
	} {
		assertRaw(t, input)
	}
}

// CREATE VIEW options and CHECK OPTION are not modeled and round-trip raw.
func TestCreateViewUnsupportedFormsFallBackToRaw(t *testing.T) {
	assertRaw(t, "CREATE OR REPLACE VIEW v WITH (security_barrier) AS SELECT 1")
	assertRaw(t, "CREATE VIEW v AS SELECT 1 WITH CHECK OPTION")
}

// COMMENT on an object type the builder does not model, and COMMENT ... IS NULL,
// round-trip raw.
func TestCommentUnsupportedFallsBackToRaw(t *testing.T) {
	assertRaw(t, "COMMENT ON SCHEMA s IS 'x'")
	assertRaw(t, "COMMENT ON TABLE t IS NULL")
}

// A CREATE EXTENSION option other than SCHEMA (here VERSION) is unsupported and
// round-trips raw.
func TestCreateExtensionVersionFallsBackToRaw(t *testing.T) {
	assertRaw(t, "CREATE EXTENSION hstore VERSION '1.0'")
}

// DROP INDEX without IF EXISTS, and the CONCURRENTLY form, round-trip raw.
func TestDropIndexUnsupportedFallsBackToRaw(t *testing.T) {
	assertRaw(t, "DROP INDEX i")
	assertRaw(t, "DROP INDEX CONCURRENTLY i")
}

// x = ALL (subquery) maps to an ALL-quantified comparison.
func TestExprAllQuantified(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE x = ALL (SELECT y FROM u)",
		selWhere(sql.All(sql.Col("x"), "=", sql.Select(sql.Col("y")).From(sql.Table{Name: sql.NewIdent("u")}))),
		`SELECT "id" FROM "t" WHERE ("x" = ALL (SELECT "y" FROM "u"))`)
}

// x = SOME (subquery) is the synonym PostgreSQL canonicalises to IN.
func TestExprSomeQuantifiedEqualsIn(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE x = SOME (SELECT y FROM u)",
		selWhere(sql.InQuery(sql.Col("x"), sql.Select(sql.Col("y")).From(sql.Table{Name: sql.NewIdent("u")}))),
		`SELECT "id" FROM "t" WHERE ("x" IN (SELECT "y" FROM "u"))`)
}

func TestExprNotBetween(t *testing.T) {
	assertRoundTrip(t,
		"SELECT id FROM t WHERE age NOT BETWEEN 1 AND 5",
		selWhere(sql.NotBetween(sql.Col("age"), 1, 5)),
		`SELECT "id" FROM "t" WHERE ("age" NOT BETWEEN $1 AND $2)`,
		1, 5)
}

func TestUpdateMultipleAssignments(t *testing.T) {
	assertRoundTrip(t,
		"UPDATE t SET a = 1, b = 2 WHERE id = 3",
		sql.Update(sql.Table{Name: sql.NewIdent("t")}).
			Set(sql.Set("a", 1), sql.Set("b", 2)).Where(sql.Eq(sql.Col("id"), 3)),
		`UPDATE "t" SET "a" = $1, "b" = $2 WHERE ("id" = $3)`,
		1, 2, 3)
}

func TestGrantUsageOnSchema(t *testing.T) {
	assertRoundTrip(t,
		"GRANT USAGE ON SCHEMA s TO r",
		sql.Grant("USAGE").OnType("SCHEMA", "s").To("r"),
		`GRANT USAGE ON SCHEMA "s" TO "r";`)
}

// A parameter reference is reproduced as written, including an out-of-range $0:
// the grammar accepts it and only the server rejects it at execution, so the
// parser passes it through faithfully rather than validating it.
func TestParamRefPassthrough(t *testing.T) {
	q, err := postgres.ParseOne("SELECT $0")
	if err != nil {
		t.Fatalf("ParseOne(SELECT $0) error = %v, want passthrough", err)
	}
	got, _, err := q.ToSQL(sql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL error = %v", err)
	}
	if got != "SELECT $0" {
		t.Fatalf("ToSQL() = %q, want %q", got, "SELECT $0")
	}
}

// A construct pg_query accepts but sqlkit does not model round-trips as a raw
// passthrough, never a hard parse error — even when the deparse is imperfect.
// Faithfully representing what the backing parser accepted is the contract.
func TestUnmodeledConstructFallsBackToRaw(t *testing.T) {
	q, err := postgres.ParseOne("SELECT !0 WHER, 0 < $0")
	if err != nil {
		t.Fatalf("ParseOne error = %v, want a raw passthrough", err)
	}
	if _, ok := q.(sql.RawQuery); !ok {
		t.Fatalf("ParseOne returned %T, want a RawQuery", q)
	}
}

// A WITH RECURSIVE SEARCH BREADTH/DEPTH FIRST clause adds a synthetic ordering
// column, and CYCLE adds cycle-detection columns and can affect recursion
// termination; a WINDOW definition extending another named window inherits
// that window's ORDER BY/frame. sqlkit has no AST node for any of these, so
// silently dropping them would change the statement's meaning — they must
// round-trip as a raw passthrough instead.
func TestSearchCycleAndWindowExtendsWindowFallBackToRaw(t *testing.T) {
	for _, input := range []string{
		"WITH RECURSIVE t(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM t WHERE n < 5) SEARCH DEPTH FIRST BY n SET seq SELECT n FROM t",
		"WITH RECURSIVE t(n, path) AS (SELECT 1, ARRAY[1] UNION ALL SELECT n + 1, path || n FROM t WHERE n < 5) CYCLE n SET is_cycle USING path SELECT n FROM t",
		"SELECT sum(x) OVER w FROM t WINDOW w AS (base_win PARTITION BY y), base_win AS (ORDER BY z)",
	} {
		raw := assertRaw(t, input)
		if raw.SQL() == "" {
			t.Errorf("empty raw SQL for %q", input)
		}
	}
}

// Clauses pg_query accepts but sqlkit cannot model faithfully must fall back to a
// raw passthrough rather than be silently dropped (which would change the
// statement's meaning). Each of these carries a meaning-bearing attribute the
// typed converters do not represent — an explicit SIMILAR TO ESCAPE, a CTE
// materialization fence, a FROM column-alias list, FROM ONLY, FETCH WITH TIES,
// or GROUP BY DISTINCT — so it must round-trip as raw, not a modeled query that
// erases the attribute.
func TestUnmodeledClausesFallBackToRaw(t *testing.T) {
	for _, input := range []string{
		"SELECT a FROM t WHERE x SIMILAR TO 'a%' ESCAPE '!'",
		"WITH c AS MATERIALIZED (SELECT 1 AS a) SELECT a FROM c",
		"WITH c AS NOT MATERIALIZED (SELECT 1 AS a) SELECT a FROM c",
		"SELECT x FROM (SELECT 1 AS a) AS s(x)",
		"SELECT a FROM ONLY t",
		"SELECT a FROM t ORDER BY a FETCH FIRST 2 ROWS WITH TIES",
		"SELECT a FROM t GROUP BY DISTINCT a, b",
	} {
		raw := assertRaw(t, input)
		if raw.SQL() == "" {
			t.Errorf("empty raw SQL for %q", input)
		}
	}
}

// The default-escape SIMILAR TO and a plain table/derived-table alias are still
// modeled (not regressed to raw by the ESCAPE / column-list guards).
func TestSimilarAndAliasStillModeled(t *testing.T) {
	for _, input := range []string{
		"SELECT a FROM t WHERE x SIMILAR TO 'a%'",
		"SELECT a FROM t AS s",
		"SELECT x FROM (SELECT 1 AS x) AS s",
	} {
		q, err := postgres.ParseOne(input)
		if err != nil {
			t.Fatalf("ParseOne(%q) error = %v", input, err)
		}
		if _, ok := q.(sql.RawQuery); ok {
			t.Errorf("%q regressed to a RawQuery; want a modeled query", input)
		}
	}
}
