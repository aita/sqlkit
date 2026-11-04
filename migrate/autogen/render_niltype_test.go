package autogen

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/meta"
)

// A column whose Type is nil (a malformed or hand-built meta.Metadata) must
// surface a descriptive compile-time error from Render, not a runtime panic.
// meta's own DDL path (columnType.RenderType) and the renderer's sameTypeErr
// already guard this; the createTable/AddColumn render path must not be the
// odd one out that dereferences a nil Type.
func TestRenderNilColumnTypeErrorsNotPanics(t *testing.T) {
	target := meta.Metadata{
		Tables: []meta.Table{{
			Name:    "widgets",
			Columns: []meta.Column{{Name: "id", Type: nil}},
		}},
	}

	_, err := Render(meta.Metadata{}, target, Options{Dialect: mustDialect(t, "postgres")})
	if err == nil {
		t.Fatalf("Render with a nil-type column: got nil error, want a descriptive type error")
	}
	if !strings.Contains(err.Error(), "id") || !strings.Contains(err.Error(), "type") {
		t.Fatalf("Render with a nil-type column: error %q should name the column and mention its missing type", err)
	}
}
