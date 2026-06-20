package postgres_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/analyze"
	"github.com/aita/sqlkit/sqlparse/postgres"
)

// TestColumnRefCarriesSourcePosition checks the byte offset a parsed column
// reference records flows through analyze.Columns into ColumnRef.SrcPos, so a
// caller can map a detected column back to where it appears in the SQL text.
func TestColumnRefCarriesSourcePosition(t *testing.T) {
	src := "SELECT id FROM users WHERE email = 'x'"
	q, err := postgres.ParseOne(src)
	if err != nil {
		t.Fatalf("ParseOne(%q) error = %v", src, err)
	}

	pos := -1
	for _, u := range analyze.Columns(q) {
		if u.Column.Name == "email" {
			pos = u.Column.SrcPos
		}
	}
	if pos < 0 {
		t.Fatalf("email column SrcPos = %d, want a real offset", pos)
	}
	if got := src[pos:]; !strings.HasPrefix(got, "email") {
		t.Errorf("SrcPos %d points at %q, want it to start at \"email\"", pos, got)
	}
}
