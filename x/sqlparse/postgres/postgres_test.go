package postgres_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/x/sqlparse"
	"github.com/aita/sqlkit/x/sqlparse/postgres"
)

// assertRoundTrip parses input, compiles the result under PostgreSQLDialect, and
// checks the compiled SQL and bind arguments. Because sqlkit parameterizes
// literals, the recompiled SQL replaces constants with $N placeholders.
func assertRoundTrip(t *testing.T, input, wantSQL string, wantArgs ...any) {
	t.Helper()
	query, err := postgres.ParseOne(input)
	if err != nil {
		t.Fatalf("ParseOne(%q) error = %v", input, err)
	}
	gotSQL, args, err := query.ToSQL(sql.PostgreSQLDialect{})
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
