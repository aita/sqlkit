package autogen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

// TestResolveNilTypeError covers the guard in Resolve for a column with no type.
func TestResolveNilTypeError(t *testing.T) {
	md := meta.Metadata{Tables: []meta.Table{{
		Schema:  meta.Schema{Name: "public"},
		Name:    "t",
		Columns: []meta.Column{{Name: "c", Type: nil}},
	}}}
	_, err := Resolve(md, mustDialect(t, "postgres"))
	if err == nil || !strings.Contains(err.Error(), "no type") {
		t.Fatalf("expected a no-type error, got %v", err)
	}
}

// TestResolveTypeNotOnDialectError covers Resolve's branch where a column's type
// has no engine mapping for the requested dialect.
func TestResolveTypeNotOnDialectError(t *testing.T) {
	md := meta.Metadata{Tables: []meta.Table{{
		Schema:  meta.Schema{Name: "public"},
		Name:    "t",
		Columns: []meta.Column{{Name: "c", Type: meta.Of("mysql", "text", 0, 0, 0)}},
	}}}
	_, err := Resolve(md, mustDialect(t, "postgres"))
	if err == nil || !strings.Contains(err.Error(), "dialect") {
		t.Fatalf("expected a dialect error, got %v", err)
	}
}

// TestResolveObjectsRoundTrip covers Resolve/Metadata for the non-table objects
// (matviews and grants), exercising the matview-body render and the grant copy.
func TestResolveObjectsRoundTrip(t *testing.T) {
	md := meta.Metadata{
		MaterializedViews: []meta.MaterializedView{{
			Schema: "public", Name: "v", Query: sql.RawSQL("SELECT 1"), Columns: []string{"x"}, WithNoData: true,
		}},
		Grants: []meta.Grant{{
			Privileges: []string{"SELECT"}, Objects: []string{"users"}, Grantees: []string{"reader"},
		}},
		Sequences: []meta.Sequence{{Schema: "public", Name: "s", Increment: i64(2)}},
	}
	d := mustDialect(t, "postgres")
	snap, err := Resolve(md, d)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(snap.MaterializedViews) != 1 || snap.MaterializedViews[0].Body == "" {
		t.Fatalf("matview not resolved: %+v", snap.MaterializedViews)
	}
	if len(snap.Grants) != 1 || snap.Grants[0].Privileges[0] != "SELECT" {
		t.Fatalf("grant not resolved: %+v", snap.Grants)
	}

	var buf bytes.Buffer
	if err := snap.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := DecodeSnapshot(&buf)
	if err != nil {
		t.Fatalf("DecodeSnapshot: %v", err)
	}
	got := decoded.Metadata()
	if len(got.MaterializedViews) != 1 || got.MaterializedViews[0].WithNoData != true {
		t.Fatalf("matview round-trip lost WithNoData: %+v", got.MaterializedViews)
	}
	if len(got.Grants) != 1 || !got.Grants[0].Equal(md.Grants[0]) {
		t.Fatalf("grant round-trip = %+v", got.Grants)
	}

	// A render of the rebuilt metadata against itself must be empty.
	res, err := Render(got, got, Options{Dialect: d})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !res.Empty() {
		t.Errorf("round-tripped objects diff against themselves:\nUp=%s", res.Up)
	}
}

// TestDecodeSnapshotError covers DecodeSnapshot's error path on malformed input.
func TestDecodeSnapshotError(t *testing.T) {
	_, err := DecodeSnapshot(bytes.NewReader([]byte("not gob")))
	if err == nil || !strings.Contains(err.Error(), "decoding schema snapshot") {
		t.Fatalf("expected a decode error, got %v", err)
	}
}
