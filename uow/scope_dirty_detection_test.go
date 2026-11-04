package uow_test

// Dirty-detection of reference-typed (slice/map) column values: an in-place
// mutation of a JSON-tagged field or a custom Valuer/Scanner slice type must be
// detected on Flush, just like a plain scalar field reassignment. Split out from
// scope_test.go per the reference-typed-field concern (see
// TestManagedDirtyUpdatesOnlyChangedColumns there for the scalar case).

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/testutil"
	"github.com/aita/sqlkit/uow"
)

// itemsTable/itemRow: a table with a JSON-tagged map column.

type itemsTable struct {
	info sqlkit.TableInfo
	ID   sqlkit.Column[int64]
	Meta sqlkit.Column[string]
}

func newItems() itemsTable {
	info := sqlkit.TableInfo{Schema: "public", Name: "items", Checksum: "items-checksum"}
	return itemsTable{info: info, ID: sqlkit.NewColumn[int64](info, "id"), Meta: sqlkit.NewColumn[string](info, "meta")}
}
func (t itemsTable) Info() sqlkit.TableInfo             { return t.info }
func (t itemsTable) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t itemsTable) Columns() []sqlkit.AnyColumn        { return []sqlkit.AnyColumn{t.ID, t.Meta} }
func (t itemsTable) PrimaryKey() []sqlkit.AnyColumn     { return []sqlkit.AnyColumn{t.ID} }
func (t itemsTable) Defaulted() []sqlkit.AnyColumn      { return []sqlkit.AnyColumn{t.ID} }

type itemRow struct {
	ID   int64
	Meta map[string]any `sqlkit:",json"`
}

// TestManagedJSONMapMutatedInPlaceIsDirty covers a JSON-tagged map field mutated
// in place (no reassignment of the map variable). The snapshot taken at Get and
// the current value read at Flush must be independent copies, or the mutation is
// invisible to change detection: the JSON column's write wrapper (jsonValue)
// holds the same map reference both times, so comparing it to itself always
// finds no change and the write is silently dropped.
func TestManagedJSONMapMutatedInPlaceIsDirty(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: func(string, []any) (testutil.Result, error) {
		return testutil.Result{Columns: []string{"id", "meta"}, Rows: [][]driver.Value{{int64(1), []byte(`{"k":"orig"}`)}}}, nil
	}}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	var it itemRow
	found, err := scope.Get(context.Background(), newItems(), &it, int64(1))
	if err != nil || !found {
		t.Fatalf("Get = %v, %v", found, err)
	}
	it.Meta["k"] = "changed"
	if err := scope.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := len(fake.Execs()); got != 1 {
		t.Fatalf("execs = %d, want 1 UPDATE (in-place JSON map mutation must be detected as dirty)", got)
	}
}

// strList is a custom slice type implementing driver.Valuer/Scanner directly
// (not through the JSON-tag wrapper), covering a plain reference-typed column.
type strList []string

func (s strList) Value() (driver.Value, error) {
	b, err := json.Marshal([]string(s))
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func (s *strList) Scan(src any) error {
	switch v := src.(type) {
	case string:
		return json.Unmarshal([]byte(v), s)
	case []byte:
		return json.Unmarshal(v, s)
	default:
		return fmt.Errorf("strList.Scan: cannot scan %T", src)
	}
}

type tagsTable struct {
	info sqlkit.TableInfo
	ID   sqlkit.Column[int64]
	Tags sqlkit.Column[string]
}

func newTagsTable() tagsTable {
	info := sqlkit.TableInfo{Schema: "public", Name: "tagged", Checksum: "tagged-checksum"}
	return tagsTable{info: info, ID: sqlkit.NewColumn[int64](info, "id"), Tags: sqlkit.NewColumn[string](info, "tags")}
}
func (t tagsTable) Info() sqlkit.TableInfo             { return t.info }
func (t tagsTable) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t tagsTable) Columns() []sqlkit.AnyColumn        { return []sqlkit.AnyColumn{t.ID, t.Tags} }
func (t tagsTable) PrimaryKey() []sqlkit.AnyColumn     { return []sqlkit.AnyColumn{t.ID} }
func (t tagsTable) Defaulted() []sqlkit.AnyColumn      { return []sqlkit.AnyColumn{t.ID} }

type tagRow struct {
	ID   int64
	Tags strList
}

// TestManagedSliceMutatedInPlaceIsDirty covers a plain (non-JSON) slice-typed
// column implementing driver.Valuer/Scanner directly, mutated in place by index.
// Same contract and root cause as TestManagedJSONMapMutatedInPlaceIsDirty: a bare
// copy of a slice header aliases the same backing array as the live field, so an
// element mutation must still be detected.
func TestManagedSliceMutatedInPlaceIsDirty(t *testing.T) {
	fake := &testutil.Fake{QueryFunc: func(string, []any) (testutil.Result, error) {
		return testutil.Result{Columns: []string{"id", "tags"}, Rows: [][]driver.Value{{int64(1), `["red","green"]`}}}, nil
	}}
	scope := uow.NewScope(sqlkit.WrapDB(fake.DB()))
	var it tagRow
	found, err := scope.Get(context.Background(), newTagsTable(), &it, int64(1))
	if err != nil || !found {
		t.Fatalf("Get = %v, %v", found, err)
	}
	it.Tags[0] = "blue"
	if err := scope.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := len(fake.Execs()); got != 1 {
		t.Fatalf("execs = %d, want 1 UPDATE (in-place slice-element mutation must be detected as dirty)", got)
	}
}
