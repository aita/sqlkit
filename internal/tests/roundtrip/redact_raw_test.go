package roundtrip_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aita/sqlkit/redaction"
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse/postgres"
)

// rawArgs compiles q for PostgreSQL and returns its bind arguments.
func rawArgs(t *testing.T, q sql.Query) []any {
	t.Helper()
	_, a, err := q.ToSQL(sql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}
	return a
}

// TestRedactRawWithParser checks that, given a parser, the redactor reaches the
// sensitive literals inside Raw(...) expression fragments and RawSQL(...)
// statements — which the identifier-scan heuristic alone cannot mask — by
// re-parsing them into typed AST and rewriting that.
func TestRedactRawWithParser(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	m := redaction.New(redaction.Columns("users.name"), redaction.WithRawParser(postgres.ParseOne))

	tests := []struct {
		name string
		q    sql.Query
		want []any
	}{
		{
			// The literal lives INSIDE the raw fragment — unreachable without parsing.
			name: "literal inside a raw predicate fragment",
			q:    sql.Select(sql.Star()).From(users).Where(sql.Raw("name = 'taro'")),
			want: []any{"*****"},
		},
		{
			name: "raw function over the column compared to a literal",
			q:    sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Raw("lower(name)"), sql.Val("taro"))),
			want: []any{"*****"},
		},
		{
			name: "non-sensitive literal inside raw is left intact",
			q:    sql.Select(sql.Star()).From(users).Where(sql.Raw("email = 'x@y.z'")),
			want: []any{"x@y.z"},
		},
		{
			name: "whole raw statement is re-parsed and masked",
			q:    sql.RawSQL("SELECT * FROM users WHERE name = 'taro' AND email = 'x@y.z'"),
			want: []any{"*****", "x@y.z"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rawArgs(t, m.Apply(tt.q)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("masked args = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestRedactRawWithoutParserFallsBack pins that, with no parser, a literal embedded
// in a raw fragment is NOT reached (documented limitation), while the heuristic
// still masks a literal compared to a raw reference to a sensitive column.
func TestRedactRawWithoutParserFallsBack(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	m := redaction.New(redaction.Columns("users.name"))

	// Heuristic path: the compared literal is masked because the raw operand names name.
	q1 := sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Raw("lower(name)"), sql.Val("taro")))
	if got := rawArgs(t, m.Apply(q1)); !reflect.DeepEqual(got, []any{"*****"}) {
		t.Errorf("heuristic raw-operand masking = %v, want [*****]", got)
	}

	// Without a parser, a literal inside the raw fragment is opaque and survives.
	q2 := sql.Select(sql.Star()).From(users).Where(sql.Raw("name = 'taro'"))
	text, _, err := m.Apply(q2).ToSQL(sql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL: %v", err)
	}
	if want := "name = 'taro'"; !strings.Contains(text, want) {
		t.Errorf("expected raw fragment %q to survive unmasked, got %q", want, text)
	}
}
