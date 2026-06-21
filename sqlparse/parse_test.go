package sqlparse_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
)

// markerQuery is a trivial sql.Query whose identity can be compared by pointer,
// so a test can assert ParseOne returns the exact query the parser produced
// (sql's builder structs are not comparable with ==).
type markerQuery struct{ id string }

func (markerQuery) ToSQL(sql.Dialect) (string, []any, error) { return "", nil, nil }

// fakeParser is a stub Parser that returns canned queries and an optional error,
// so the dialect-agnostic ParseOne logic can be tested without a real frontend.
type fakeParser struct {
	queries []sql.Query
	err     error
}

func (p fakeParser) Parse(text string) ([]sql.Query, error) {
	return p.queries, p.err
}

func TestParseOneSingle(t *testing.T) {
	want := markerQuery{id: "only"}
	p := fakeParser{queries: []sql.Query{want}}

	got, err := sqlparse.ParseOne(p, "SELECT id FROM users")
	if err != nil {
		t.Fatalf("ParseOne returned error: %v", err)
	}
	if got != sql.Query(want) {
		t.Errorf("ParseOne = %v, want %v", got, want)
	}
}

func TestParseOneParseError(t *testing.T) {
	sentinel := errors.New("syntax error")
	p := fakeParser{err: sentinel}

	got, err := sqlparse.ParseOne(p, "NONSENSE")
	if got != nil {
		t.Errorf("ParseOne query = %v, want nil on error", got)
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("ParseOne err = %v, want %v (the parser's error verbatim)", err, sentinel)
	}
}

func TestParseOneNoStatement(t *testing.T) {
	p := fakeParser{queries: nil}

	got, err := sqlparse.ParseOne(p, "")
	if got != nil {
		t.Errorf("ParseOne query = %v, want nil", got)
	}
	if !errors.Is(err, sqlparse.ErrNoStatement) {
		t.Errorf("ParseOne err = %v, want ErrNoStatement", err)
	}
}

func TestParseOneMultipleStatements(t *testing.T) {
	q := markerQuery{id: "x"}
	p := fakeParser{queries: []sql.Query{q, q, q}}

	got, err := sqlparse.ParseOne(p, "SELECT 1; SELECT 2; SELECT 3")
	if got != nil {
		t.Errorf("ParseOne query = %v, want nil", got)
	}
	if err == nil {
		t.Fatal("ParseOne err = nil, want a statement-count error")
	}
	// Not ErrNoStatement — this is the "too many" branch.
	if errors.Is(err, sqlparse.ErrNoStatement) {
		t.Errorf("ParseOne err = ErrNoStatement, want a count error")
	}
	if !strings.Contains(err.Error(), "got 3") {
		t.Errorf("ParseOne err = %q, want it to report the statement count", err)
	}
}

func TestUnsupportedError(t *testing.T) {
	err := sqlparse.Unsupported("GROUPING SETS")

	var ue *sqlparse.UnsupportedError
	if !errors.As(err, &ue) {
		t.Fatalf("Unsupported did not return an *UnsupportedError, got %T", err)
	}
	if ue.Construct != "GROUPING SETS" {
		t.Errorf("Construct = %q, want %q", ue.Construct, "GROUPING SETS")
	}
	if !strings.Contains(err.Error(), "GROUPING SETS") {
		t.Errorf("Error() = %q, want it to name the construct", err.Error())
	}
	if !strings.HasPrefix(err.Error(), "sqlkit/sqlparse: unsupported SQL construct: ") {
		t.Errorf("Error() = %q, want the package-prefixed message", err.Error())
	}
}

func TestUnsupportedf(t *testing.T) {
	err := sqlparse.Unsupportedf("expression node %s", "*pg_query.A_ArrayExpr")

	var ue *sqlparse.UnsupportedError
	if !errors.As(err, &ue) {
		t.Fatalf("Unsupportedf did not return an *UnsupportedError, got %T", err)
	}
	if ue.Construct != "expression node *pg_query.A_ArrayExpr" {
		t.Errorf("Construct = %q, want the formatted construct", ue.Construct)
	}
}
