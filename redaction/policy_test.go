package redaction_test

import (
	"reflect"
	"testing"

	"github.com/aita/sqlkit/redaction"
	"github.com/aita/sqlkit/sql"
)

// TestColumnsMalformedSpecsIgnored pins splitSpec's rejection paths: a spec with
// fewer than two dotted parts, or with an empty table or column segment, is
// dropped rather than producing a pattern that masks (or fails to mask) the wrong
// column. A malformed spec must never silently widen the policy.
func TestColumnsMalformedSpecsIgnored(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}

	tests := []struct {
		name string
		spec string
	}{
		{"single segment, no table part", "name"},
		{"empty string", ""},
		{"trailing dot leaves empty column", "users."},
		{"leading dot leaves empty table", ".name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := redaction.New(redaction.Columns(tt.spec))
			// The malformed spec is ignored, so nothing is sensitive and the literal
			// is left intact.
			q := sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), sql.Val("taro")))
			if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{"taro"}) {
				t.Errorf("malformed spec %q masked unexpectedly: args = %v, want [taro]", tt.spec, got)
			}
		})
	}
}

// TestColumnsValidSpecAmongMalformed pins that a valid spec still takes effect when
// passed alongside malformed ones — the malformed entries are skipped, not fatal.
func TestColumnsValidSpecAmongMalformed(t *testing.T) {
	users := sql.Table{Schema: sql.NewIdent("public"), Name: sql.NewIdent("users")}
	m := redaction.New(redaction.Columns("name", "users.name", ""))
	q := sql.Select(sql.Star()).From(users).Where(sql.Eq(sql.Col("name"), sql.Val("taro")))
	if got := args(t, m.Apply(q)); !reflect.DeepEqual(got, []any{"*****"}) {
		t.Errorf("valid spec did not apply: args = %v, want [*****]", got)
	}
}
