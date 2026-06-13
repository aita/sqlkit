package main

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/migrate/autogen"
)

func TestParseRenames(t *testing.T) {
	r, err := parseRenames([]string{"users.name:full_name"}, []string{"old_t:new_t"})
	if err != nil {
		t.Fatalf("parseRenames error = %v", err)
	}
	want := autogen.Renames{
		Columns: []autogen.ColumnRename{{Table: "users", From: "name", To: "full_name"}},
		Tables:  []autogen.TableRename{{From: "old_t", To: "new_t"}},
	}
	if len(r.Columns) != 1 || r.Columns[0] != want.Columns[0] {
		t.Errorf("columns = %+v, want %+v", r.Columns, want.Columns)
	}
	if len(r.Tables) != 1 || r.Tables[0] != want.Tables[0] {
		t.Errorf("tables = %+v, want %+v", r.Tables, want.Tables)
	}
}

func TestParseRenamesInvalid(t *testing.T) {
	for _, spec := range []string{"nocolon", "users.:x", "users.name:", ".name:x"} {
		if _, err := parseRenames([]string{spec}, nil); err == nil {
			t.Errorf("parseRenames(%q) expected error", spec)
		}
	}
	if _, err := parseRenames(nil, []string{"noseparator"}); err == nil {
		t.Errorf("parseRenames table %q expected error", "noseparator")
	}
}

func TestDownRevisionField(t *testing.T) {
	tests := []struct {
		name   string
		heads  []string
		parent string
		merge  bool
		want   string
		errs   bool
	}{
		{name: "single head", heads: []string{"0003"}, want: `DownRevision: "0003"`},
		{name: "no heads", heads: nil, want: `DownRevision: ""`},
		{name: "explicit parent", heads: []string{"a", "b"}, parent: "a", want: `DownRevision: "a"`},
		{name: "merge", heads: []string{"a", "b"}, merge: true, want: "DownRevisions:"},
		{name: "ambiguous", heads: []string{"a", "b"}, errs: true},
		{name: "merge no heads", merge: true, errs: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := downRevisionField(tt.heads, tt.parent, tt.merge)
			if tt.errs {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("downRevisionField = %q, want substring %q", got, tt.want)
			}
		})
	}
}

func TestAutogenerateRequiresOneSource(t *testing.T) {
	// Neither source selected.
	if err := (&autogenerateCmd{}).Run(); err == nil || !strings.Contains(err.Error(), "exactly one source") {
		t.Errorf("no source: expected 'exactly one source' error, got %v", err)
	}
	// Both sources selected.
	if err := (&autogenerateCmd{FromDB: true, FromGit: "HEAD"}).Run(); err == nil || !strings.Contains(err.Error(), "exactly one source") {
		t.Errorf("both sources: expected 'exactly one source' error, got %v", err)
	}
}
