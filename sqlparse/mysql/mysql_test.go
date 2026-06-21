package mysql_test

import (
	"reflect"
	"testing"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse/mysql"
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
//   - that query compiles under MySQLDialect to wantSQL with wantArgs,
//     documenting the output and guarding the sql-core rendering. Because sqlkit
//     parameterizes literals, the recompiled SQL replaces constants with ?.
func assertRoundTrip(t *testing.T, input string, want sql.Query, wantSQL string, wantArgs ...any) {
	t.Helper()
	got, err := mysql.ParseOne(input)
	if err != nil {
		t.Fatalf("ParseOne(%q) error = %v", input, err)
	}
	if diff := cmp.Diff(want, got, astCmpOpts...); diff != "" {
		t.Fatalf("AST mismatch for %q (-want +got):\n%s", input, diff)
	}
	gotSQL, args, err := got.ToSQL(sql.MySQLDialect{})
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
	got, err := mysql.ParseOne(input)
	if err != nil {
		t.Fatalf("ParseOne(%q) error = %v", input, err)
	}
	raw, ok := got.(sql.RawQuery)
	if !ok {
		t.Fatalf("ParseOne(%q) = %T, want sql.RawQuery", input, got)
	}
	text, args, err := raw.ToSQL(sql.MySQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if len(args) != 0 {
		t.Fatalf("RawQuery args = %#v, want none", args)
	}
	if text != raw.SQL() {
		t.Fatalf("RawQuery text = %q, want %q", text, raw.SQL())
	}
	again, err := mysql.ParseOne(text)
	if err != nil {
		t.Fatalf("re-parse of %q error = %v", text, err)
	}
	if !reflect.DeepEqual(again, got) {
		t.Fatalf("deparse not stable for %q:\n got: %#v\nwant: %#v", input, again, got)
	}
	return raw
}

func TestParseMultipleStatements(t *testing.T) {
	queries, err := mysql.Parse("SELECT 1; SELECT 2")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(queries) != 2 {
		t.Fatalf("Parse() returned %d queries, want 2", len(queries))
	}
}

func TestParseOneRejectsMultiple(t *testing.T) {
	if _, err := mysql.ParseOne("SELECT 1; SELECT 2"); err == nil {
		t.Fatal("ParseOne() with two statements succeeded, want error")
	}
}

func TestParseSyntaxError(t *testing.T) {
	if _, err := mysql.Parse("SELECT FROM WHERE"); err == nil {
		t.Fatal("Parse() of invalid SQL succeeded, want error")
	}
}

// Statement types the typed converters do not model still parse, falling back
// to a verbatim RawQuery rather than erroring.
func TestUnmodeledStatementFallsBackToRaw(t *testing.T) {
	for _, input := range []string{
		"SET autocommit = 1",
		"CREATE VIEW v AS SELECT id FROM users",
		"RENAME TABLE a TO b, c TO d",
	} {
		assertRaw(t, input)
	}
}

// A parsed query is a normal builder, so its statement's exported fields are
// directly inspectable.
func TestParsedQueryExposesStmt(t *testing.T) {
	got, err := mysql.ParseOne("SELECT id, name FROM users WHERE id = 1 LIMIT 10")
	if err != nil {
		t.Fatalf("ParseOne() error = %v", err)
	}
	b, ok := got.(sql.SelectBuilder)
	if !ok {
		t.Fatalf("ParseOne() = %T, want sql.SelectBuilder", got)
	}
	stmt := b.Stmt()
	if len(stmt.Columns) != 2 {
		t.Fatalf("Columns = %d, want 2", len(stmt.Columns))
	}
	if table, ok := stmt.From.(sql.Table); !ok || table.Name.String() != "users" {
		t.Fatalf("From = %#v, want table users", stmt.From)
	}
	if stmt.Where == nil {
		t.Fatal("Where = nil, want the WHERE predicate")
	}
	if !stmt.HasLimit || stmt.Limit != 10 {
		t.Fatalf("Limit = %d (has=%v), want 10", stmt.Limit, stmt.HasLimit)
	}
}

// A function name that is not a plain ASCII identifier (here a valid non-ASCII
// name) is not rejected — the TiDB parser accepted it, so it round-trips as a raw
// passthrough rather than a hard error.
func TestNonIdentifierFunctionNameFallsBackToRaw(t *testing.T) {
	q, err := mysql.ParseOne("SELECT café()")
	if err != nil {
		t.Fatalf("ParseOne error = %v, want a raw passthrough", err)
	}
	if _, ok := q.(sql.RawQuery); !ok {
		t.Fatalf("ParseOne returned %T, want a RawQuery", q)
	}
}

// Invalid UTF-8 is rejected as malformed input (not a SQL construct): the TiDB
// lexer maps the bad byte to U+FFFD/"?", losing it, so no round-trip is stable.
func TestParseRejectsInvalidUTF8(t *testing.T) {
	if _, err := mysql.ParseOne("SELECT 0 AS `\xc6`"); err == nil {
		t.Fatal("ParseOne(invalid UTF-8) error = nil, want a parse error")
	}
}

// A raw SET passthrough must be stable under re-parsing. TiDB's restore is not
// idempotent for a SET system variable (it keeps the source case once, then
// lower-cases the case-insensitive name), so the raw deparse settles it.
func TestSetSystemVariableRawIsFixpoint(t *testing.T) {
	q, err := mysql.ParseOne("SET A=0")
	if err != nil {
		t.Fatalf("ParseOne error = %v", err)
	}
	first, _, err := q.ToSQL(sql.MySQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL error = %v", err)
	}
	q2, err := mysql.ParseOne(first)
	if err != nil {
		t.Fatalf("re-parse error = %v", err)
	}
	second, _, _ := q2.ToSQL(sql.MySQLDialect{})
	if first != second {
		t.Fatalf("raw SET is not a fixpoint:\n  first:  %s\n  second: %s", first, second)
	}
}

// The reference value driver panics on inputs that exercise an unimplemented
// MyDecimal branch (e.g. an over-long numeric literal); Parse must recover and
// return an error rather than let the panic escape.
func TestParseRecoversFromDriverPanic(t *testing.T) {
	long := "0000000000000000000000000000000000000000000000000000000000000018700000000000000000"
	if _, err := mysql.ParseOne(long); err == nil {
		t.Fatal("ParseOne(over-long numeric) error = nil, want a recovered parser error")
	}
}
