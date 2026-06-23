package uow_test

import (
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
