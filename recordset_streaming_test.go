package sqlkit_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
)

func queryRecordSet(t *testing.T, fake *sqltest.Fake) sqlkit.RecordSet {
	t.Helper()
	db := fake.DB()
	t.Cleanup(func() { _ = db.Close() })
	rows, err := db.QueryContext(context.Background(), "SELECT id FROM t")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	return sqlkit.FromSQLRows(rows)
}

func threeRecordFake() *sqltest.Fake {
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
	fake := threeRecordFake()
	rs := queryRecordSet(t, fake)

	var got []int64
	for record, err := range rs.Iter() {
		if err != nil {
			t.Fatalf("Iter() error = %v", err)
		}
		value, _ := record.Get("id")
		got = append(got, value.(int64))
		if len(got) == 2 {
			break // stop before the third record
		}
	}
	if len(got) != 2 || got[1] != 2 {
		t.Fatalf("got = %#v", got)
	}
	// The driver was asked for at most the records we consumed (plus
	// lookahead), not the whole result set.
	if fake.RowReads() > 2 {
		t.Fatalf("driver row reads = %d, want <= 2 after early break", fake.RowReads())
	}
}

func TestSQLRecordSetIsSinglePass(t *testing.T) {
	rs := queryRecordSet(t, threeRecordFake())

	count := 0
	for _, err := range rs.Iter() {
		if err != nil {
			t.Fatalf("first pass error = %v", err)
		}
		count++
	}
	if count != 3 {
		t.Fatalf("first pass records = %d, want 3", count)
	}

	var second error
	for _, err := range rs.Iter() {
		second = err
	}
	if second == nil || !strings.Contains(second.Error(), "single-pass") {
		t.Fatalf("second pass error = %v, want single-pass error", second)
	}
}

func TestScanStructReadsOnlyFirstRecord(t *testing.T) {
	fake := threeRecordFake()
	rs := queryRecordSet(t, fake)

	var record struct {
		ID int64 `sqlkit:"id"`
	}
	if err := rs.Scan(&record); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if record.ID != 1 {
		t.Fatalf("record = %#v", record)
	}
	if fake.RowReads() > 1 {
		t.Fatalf("driver row reads = %d, want 1 for single-struct Scan", fake.RowReads())
	}
}

func TestScanSliceStreamsAllRecords(t *testing.T) {
	rs := queryRecordSet(t, threeRecordFake())

	var records []struct {
		ID int64 `sqlkit:"id"`
	}
	if err := rs.Scan(&records); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(records) != 3 || records[2].ID != 3 {
		t.Fatalf("records = %#v", records)
	}
}

func TestRecordSetScanSupportsScannerAndPointerFields(t *testing.T) {
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
	rs := queryRecordSet(t, fake)

	var records []struct {
		Name sql.NullString `sqlkit:"name"` // database/sql Scanner
		Nick *string        `sqlkit:"nick"` // pointer for NULL
	}
	if err := rs.Scan(&records); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if !records[0].Name.Valid || records[0].Name.String != "Alice" || *records[0].Nick != "al" {
		t.Fatalf("record 0 = %#v", records[0])
	}
	if records[1].Name.Valid || records[1].Nick != nil {
		t.Fatalf("record 1 = %#v, want NULLs", records[1])
	}
}
