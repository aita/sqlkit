package uow_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/uow"
)

// docsTable is a binding with a JSON column, to check the uow UPDATE path stores
// a json-tagged field as its JSON encoding (as Insert does), not the raw Go value.
type docsTable struct {
	info sqlkit.TableInfo
	ID   sqlkit.Column[int64]
	Meta sqlkit.Column[string]
}

func newDocs() docsTable {
	info := sqlkit.TableInfo{Schema: "public", Name: "docs", Checksum: "docs-checksum"}
	return docsTable{
		info: info,
		ID:   sqlkit.NewColumn[int64](info, "id"),
		Meta: sqlkit.NewColumn[string](info, "meta"),
	}
}

func (t docsTable) Info() sqlkit.TableInfo             { return t.info }
func (t docsTable) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t docsTable) Columns() []sqlkit.AnyColumn        { return []sqlkit.AnyColumn{t.ID, t.Meta} }
func (t docsTable) PrimaryKey() []sqlkit.AnyColumn     { return []sqlkit.AnyColumn{t.ID} }
func (t docsTable) Defaulted() []sqlkit.AnyColumn      { return []sqlkit.AnyColumn{t.ID} }

type docRow struct {
	ID   int64          `sqlkit:"id"`
	Meta map[string]any `sqlkit:"meta,json"`
}

// TestExplicitUpdateEncodesJSONColumn covers the detached-row Update path: a
// json column reaches the driver as its JSON encoding, not the raw map.
func TestExplicitUpdateEncodesJSONColumn(t *testing.T) {
	fake := &testutil.Fake{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	if err := scope.Update(newDocs(), docRow{ID: 1, Meta: map[string]any{"k": "v"}}); err != nil {
		t.Fatal(err)
	}
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	upd := fake.Execs()[0]
	b, ok := upd.Args[0].([]byte)
	if !ok {
		t.Fatalf("meta arg = %T %v, want JSON []byte", upd.Args[0], upd.Args[0])
	}
	if string(b) != `{"k":"v"}` {
		t.Fatalf("meta JSON = %s, want {\"k\":\"v\"}", b)
	}
}

// TestManagedUpdateEncodesJSONColumn covers the managed snapshot-diff path: a
// reassigned json field is detected as changed and emitted as its JSON encoding.
func TestManagedUpdateEncodesJSONColumn(t *testing.T) {
	fake := &testutil.Fake{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	d := &docRow{ID: 1, Meta: map[string]any{"k": "v"}}
	if err := scope.Attach(newDocs(), d); err != nil {
		t.Fatal(err)
	}
	d.Meta = map[string]any{"k": "w"} // reassign so the snapshot diff sees a change
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	upd := fake.Execs()[0]
	b, ok := upd.Args[0].([]byte)
	if !ok || string(b) != `{"k":"w"}` {
		t.Fatalf("meta arg = %T %v, want JSON {\"k\":\"w\"}", upd.Args[0], upd.Args[0])
	}
}

// TestBulkUpdateRoutesJSONColumnPerRow covers that, even with batching on, a
// json column is updated one row at a time rather than through a VALUES join: a
// json value binds via a driver.Valuer, which a VALUES source cannot carry as a
// typed column. Without this the batched path would emit one UPDATE ... FROM
// (VALUES ...) the engine rejects.
func TestBulkUpdateRoutesJSONColumnPerRow(t *testing.T) {
	fake := &testutil.Fake{}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()), uow.WithBatchSize(10))
	a := &docRow{ID: 1, Meta: map[string]any{"k": "a"}}
	b := &docRow{ID: 2, Meta: map[string]any{"k": "b"}}
	if err := scope.Attach(newDocs(), a, b); err != nil {
		t.Fatal(err)
	}
	a.Meta = map[string]any{"k": "a2"} // both rows change the same (json) column...
	b.Meta = map[string]any{"k": "b2"} // ...so batching would otherwise group them
	if err := scope.Flush(ctx()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	execs := fake.Execs()
	if len(execs) != 2 {
		t.Fatalf("got %d execs, want 2 per-row UPDATEs: %v", len(execs), fake.Statements())
	}
	for _, e := range execs {
		if strings.Contains(e.SQL, "VALUES") {
			t.Fatalf("json update used a VALUES join: %s", e.SQL)
		}
		if _, ok := e.Args[0].([]byte); !ok {
			t.Fatalf("update arg = %T, want JSON []byte per row", e.Args[0])
		}
	}
}
