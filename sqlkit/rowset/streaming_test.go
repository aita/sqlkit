package rowset_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/sqlkit-dev/sqlkit/internal/sqltest"
	"github.com/sqlkit-dev/sqlkit/sqlkit/rowset"
)

func queryRowSet(t *testing.T, fake *sqltest.Fake) rowset.RowSet {
	t.Helper()
	db := fake.DB()
	t.Cleanup(func() { _ = db.Close() })
	rows, err := db.QueryContext(context.Background(), "SELECT id FROM t")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	return rowset.FromSQLRows(rows)
}

func threeRowFake() *sqltest.Fake {
	return &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{
				Columns: []string{"id"},
				Rows:    [][]driver.Value{{int64(1)}, {int64(2)}, {int64(3)}},
			}, nil
		},
	}
}

func TestIterStreamsAndStopsEarly(t *testing.T) {
	fake := threeRowFake()
	rs := queryRowSet(t, fake)

	var got []int64
	for row, err := range rs.Iter() {
		if err != nil {
			t.Fatalf("Iter() error = %v", err)
		}
		value, _ := row.Get("id")
		got = append(got, value.(int64))
		if len(got) == 2 {
			break // stop before the third row
		}
	}
	if len(got) != 2 || got[1] != 2 {
		t.Fatalf("got = %#v", got)
	}
	// The driver was asked for at most the rows we consumed (plus lookahead),
	// not the whole result set.
	if fake.RowReads() > 2 {
		t.Fatalf("driver row reads = %d, want <= 2 after early break", fake.RowReads())
	}
}

func TestSQLRowSetIsSinglePass(t *testing.T) {
	rs := queryRowSet(t, threeRowFake())

	count := 0
	for _, err := range rs.Iter() {
		if err != nil {
			t.Fatalf("first pass error = %v", err)
		}
		count++
	}
	if count != 3 {
		t.Fatalf("first pass rows = %d, want 3", count)
	}

	var second error
	for _, err := range rs.Iter() {
		second = err
	}
	if second == nil || !strings.Contains(second.Error(), "single-pass") {
		t.Fatalf("second pass error = %v, want single-pass error", second)
	}
}

func TestScanStructReadsOnlyFirstRow(t *testing.T) {
	fake := threeRowFake()
	rs := queryRowSet(t, fake)

	var row struct {
		ID int64 `sqlkit:"id"`
	}
	if err := rs.Scan(&row); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if row.ID != 1 {
		t.Fatalf("row = %#v", row)
	}
	if fake.RowReads() > 1 {
		t.Fatalf("driver row reads = %d, want 1 for single-struct Scan", fake.RowReads())
	}
}

func TestScanSliceStreamsAllRows(t *testing.T) {
	rs := queryRowSet(t, threeRowFake())

	var rows []struct {
		ID int64 `sqlkit:"id"`
	}
	if err := rs.Scan(&rows); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(rows) != 3 || rows[2].ID != 3 {
		t.Fatalf("rows = %#v", rows)
	}
}

func TestRowSetScanSupportsScannerAndPointerFields(t *testing.T) {
	fake := &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{
				Columns: []string{"name", "nick"},
				Rows: [][]driver.Value{
					{"Alice", "al"},
					{nil, nil},
				},
			}, nil
		},
	}
	rs := queryRowSet(t, fake)

	var rows []struct {
		Name sql.NullString `sqlkit:"name"` // database/sql Scanner
		Nick *string        `sqlkit:"nick"` // pointer for NULL
	}
	if err := rs.Scan(&rows); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if !rows[0].Name.Valid || rows[0].Name.String != "Alice" || *rows[0].Nick != "al" {
		t.Fatalf("row 0 = %#v", rows[0])
	}
	if rows[1].Name.Valid || rows[1].Nick != nil {
		t.Fatalf("row 1 = %#v, want NULLs", rows[1])
	}
}
