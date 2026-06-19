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
