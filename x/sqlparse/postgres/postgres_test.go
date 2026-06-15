package postgres_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/x/sqlparse"
	"github.com/aita/sqlkit/x/sqlparse/postgres"
)

// assertRoundTrip parses input and checks it both ways, for maximum precision:
//   - the parsed query is structurally identical to want (reflect.DeepEqual,
//     unexported fields included), proving the parser builds the expected AST
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
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AST mismatch for %q\n got: %#v\nwant: %#v", input, got, want)
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

// assertUnsupported checks that parsing input fails with an UnsupportedError.
func assertUnsupported(t *testing.T, input string) {
	t.Helper()
	_, err := postgres.ParseOne(input)
	if err == nil {
		t.Fatalf("ParseOne(%q) succeeded, want unsupported error", input)
	}
	var unsupported *sqlparse.UnsupportedError
	if !errors.As(err, &unsupported) {
		t.Fatalf("ParseOne(%q) error = %v, want UnsupportedError", input, err)
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

// TestSelfRegistration verifies the package's init registered its parser under
// the PostgreSQL dialect name, so a blank import makes it resolvable by name.
func TestSelfRegistration(t *testing.T) {
	name := sql.PostgreSQLDialect{}.Name()
	p, ok := sqlparse.For(name)
	if !ok {
		t.Fatalf("sqlparse.For(%q) not registered", name)
	}
	if _, isPostgres := p.(postgres.Parser); !isPostgres {
		t.Fatalf("sqlparse.For(%q) returned %T, want postgres.Parser", name, p)
	}
}
