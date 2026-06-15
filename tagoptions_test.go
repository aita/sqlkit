package sqlkit_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
)

// Items exercises the write-time tag options: a JSON column, an optimistic-lock
// version column, and a database-generated read-only column. Its columns are
// id (pk), sku, meta (json), rev (version), created (read-only). None are
// defaulted, so column selection turns purely on the tag options.
type ItemsTable struct {
	info    sqlkit.TableInfo
	ID      sqlkit.Column[int64]
	SKU     sqlkit.Column[string]
	Meta    sqlkit.Column[string]
	Rev     sqlkit.Column[int64]
	Created sqlkit.Column[int64]
}

var Items = func() ItemsTable {
	info := sqlkit.TableInfo{Schema: "public", Name: "items"}
	return ItemsTable{
		info:    info,
		ID:      sqlkit.NewColumn[int64](info, "id"),
		SKU:     sqlkit.NewColumn[string](info, "sku"),
		Meta:    sqlkit.NewColumn[string](info, "meta"),
		Rev:     sqlkit.NewColumn[int64](info, "rev"),
		Created: sqlkit.NewColumn[int64](info, "created"),
	}
}()

func (t ItemsTable) Info() sqlkit.TableInfo             { return t.info }
func (t ItemsTable) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t ItemsTable) Columns() []sqlkit.AnyColumn {
	return []sqlkit.AnyColumn{t.ID, t.SKU, t.Meta, t.Rev, t.Created}
}
func (t ItemsTable) PrimaryKey() []sqlkit.AnyColumn { return []sqlkit.AnyColumn{t.ID} }
func (t ItemsTable) Defaulted() []sqlkit.AnyColumn  { return nil }

// Item is a row DTO that opts into every write-time tag option.
type Item struct {
	ID      int64          `sqlkit:"id"`
	SKU     string         `sqlkit:"sku"`
	Meta    map[string]any `sqlkit:"meta,json"`
	Rev     int64          `sqlkit:"rev,version"`
	Created int64          `sqlkit:"created,readonly"`
}

func TestInsertSkipsReadonlyAndMarshalsJSON(t *testing.T) {
	item := Item{ID: 1, SKU: "abc", Meta: map[string]any{"k": "v"}, Rev: 3, Created: 99}

	// The read-only "created" column is omitted; "meta" is present.
	assertToSQL(t, sqlkit.Insert(Items).Values(item),
		`INSERT INTO "public"."items" ("id", "sku", "meta", "rev") VALUES ($1, $2, $3, $4)`,
		nil)

	// At execution the JSON column reaches the driver as its JSON encoding.
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	if _, err := db.Insert(Items).Values(item).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	args := fake.Execs()[0].Args
	if len(args) != 4 {
		t.Fatalf("driver args = %#v, want 4 (created excluded)", args)
	}
	meta, ok := args[2].([]byte)
	if !ok || string(meta) != `{"k":"v"}` {
		t.Fatalf("meta arg = %#v, want JSON bytes", args[2])
	}
}

func TestInsertJSONNilWritesNull(t *testing.T) {
	// A nil map encodes as SQL NULL rather than the JSON literal null.
	fake := &sqltest.Fake{}
	db := sqlkit.WrapDB(fake.DB())
	if _, err := db.Insert(Items).Values(Item{ID: 1, SKU: "x"}).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if got := fake.Execs()[0].Args[2]; got != nil {
		t.Fatalf("meta arg = %#v, want nil (SQL NULL)", got)
	}
}

func TestUpdateSetStructVersionReadonlyJSON(t *testing.T) {
	item := Item{ID: 1, SKU: "abc", Meta: map[string]any{"k": "v"}, Rev: 3, Created: 99}

	// Implicit update: read-only "created" excluded, "meta" written as JSON, and
	// the version column bumped with a guard on its current value (3).
	assertToSQL(t,
		sqlkit.Update(Items).SetStruct(item).Where(Items.ID.Eq(item.ID)),
		`UPDATE "public"."items" SET "sku" = $1, "meta" = $2, "rev" = ("rev" + $3) WHERE (("rev" = $4) AND ("public"."items"."id" = $5))`,
		nil)
}

func TestSetStructVersionGuardConflict(t *testing.T) {
	item := Item{ID: 1, SKU: "abc", Rev: 3}

	// Zero rows affected under the tag-driven version guard is a conflict.
	zero := int64(0)
	db := sqlkit.WrapDB((&sqltest.Fake{RowsAffected: &zero}).DB())
	_, err := db.Update(Items).SetStruct(item).Where(Items.ID.Eq(item.ID)).Exec(context.Background())
	if !errors.Is(err, sqlkit.ErrVersionConflict) {
		t.Fatalf("Exec() error = %v, want ErrVersionConflict", err)
	}

	// A matched row is not a conflict.
	ok := sqlkit.WrapDB((&sqltest.Fake{}).DB())
	if _, err := ok.Update(Items).SetStruct(item).Where(Items.ID.Eq(item.ID)).Exec(context.Background()); err != nil {
		t.Fatalf("Exec() error = %v, want nil", err)
	}
}

func TestExplicitColumnsIgnoreReadonlyAndVersionSemantics(t *testing.T) {
	item := Item{ID: 1, Rev: 3, Created: 99}

	// Naming columns explicitly overrides the implicit exclusions: a read-only
	// column is written verbatim and a version column is copied, not bumped.
	assertToSQL(t,
		sqlkit.Update(Items).SetStruct(item, Items.Created, Items.Rev).Where(Items.ID.Eq(item.ID)),
		`UPDATE "public"."items" SET "created" = $1, "rev" = $2 WHERE ("public"."items"."id" = $3)`,
		[]any{int64(99), int64(3), int64(1)})
}

func TestScanJSONColumn(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{
				Columns: []string{"id", "sku", "meta", "rev", "created"},
				Rows: [][]driver.Value{
					{int64(1), "abc", []byte(`{"k":"v"}`), int64(3), int64(99)},
					{int64(2), "def", nil, int64(1), int64(0)}, // NULL meta
				},
			}, nil
		},
	}
	db := sqlkit.WrapDB(fake.DB())

	var items []Item
	if err := db.Select().From(Items).All(context.Background()).Scan(&items); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if want := map[string]any{"k": "v"}; !reflect.DeepEqual(items[0].Meta, want) {
		t.Fatalf("items[0].Meta = %#v, want %#v", items[0].Meta, want)
	}
	if items[1].Meta != nil {
		t.Fatalf("items[1].Meta = %#v, want nil for NULL", items[1].Meta)
	}
}
