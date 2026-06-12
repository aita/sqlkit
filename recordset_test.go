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

func fakeResult(columns []string, rows ...[]driver.Value) *sqltest.Fake {
	return &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{Columns: columns, Rows: rows}, nil
		},
	}
}

func threeRecordFake() *sqltest.Fake {
	return fakeResult([]string{"id"}, []driver.Value{int64(1)}, []driver.Value{int64(2)}, []driver.Value{int64(3)})
}

func TestRecordsCanBeRanged(t *testing.T) {
	rs := queryRecordSet(t, threeRecordFake())

	var values []int64
	for record, err := range rs.Records() {
		if err != nil {
			t.Fatalf("Records() error = %v", err)
		}
		var row struct {
			ID int64 `sqlkit:"id"`
		}
		if err := record.Scan(&row); err != nil {
			t.Fatalf("record Scan() error = %v", err)
		}
		values = append(values, row.ID)
	}

	if len(values) != 3 || values[0] != 1 || values[2] != 3 {
		t.Fatalf("Records() = %#v, want [1 2 3]", values)
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
		var row struct {
			ID int64 `sqlkit:"id"`
		}
		if err := record.Scan(&row); err != nil {
			t.Fatalf("record Scan() error = %v", err)
		}
		got = append(got, row.ID)
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

func TestRecordSetIsSinglePass(t *testing.T) {
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

func TestRecordSetScanStructSlice(t *testing.T) {
	type user struct {
		ID    int64  `sqlkit:"id"`
		Email string `sqlkit:"email"`
	}
	rs := queryRecordSet(t, fakeResult(
		[]string{"id", "email"},
		[]driver.Value{int64(1), "one@example.test"},
		[]driver.Value{int64(2), "two@example.test"},
	))

	var users []user
	if err := rs.Scan(&users); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(users) != 2 || users[0].ID != 1 || users[1].Email != "two@example.test" {
		t.Fatalf("Scan() = %#v, want mapped users", users)
	}
}

func TestRecordSetScanSupportsScannerAndPointerFields(t *testing.T) {
	rs := queryRecordSet(t, fakeResult(
		[]string{"name", "nick"},
		[]driver.Value{"Alice", "al"},
		[]driver.Value{nil, nil},
	))

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

func TestRecordScanStruct(t *testing.T) {
	type user struct {
		Email string `sqlkit:"email"`
	}
	rs := queryRecordSet(t, fakeResult([]string{"email"}, []driver.Value{"record@example.test"}))

	var got user
	for record, err := range rs.Iter() {
		if err != nil {
			t.Fatalf("Iter() error = %v", err)
		}
		if err := record.Scan(&got); err != nil {
			t.Fatalf("Scan() error = %v", err)
		}
		break
	}
	if got.Email != "record@example.test" {
		t.Fatalf("Scan() = %#v, want mapped email", got)
	}
}

func TestRecordScanIsSingleShot(t *testing.T) {
	rs := queryRecordSet(t, fakeResult([]string{"id"}, []driver.Value{int64(1)}))

	for record, err := range rs.Iter() {
		if err != nil {
			t.Fatalf("Iter() error = %v", err)
		}
		var row struct {
			ID int64 `sqlkit:"id"`
		}
		if err := record.Scan(&row); err != nil {
			t.Fatalf("first Scan() error = %v", err)
		}
		err := record.Scan(&row)
		if err == nil || !strings.Contains(err.Error(), "already scanned") {
			t.Fatalf("second Scan() error = %v, want already-scanned error", err)
		}
	}
}

func TestRecordSetScanIgnoresUnmatchedColumns(t *testing.T) {
	rs := queryRecordSet(t, fakeResult(
		[]string{"id", "extra"},
		[]driver.Value{int64(7), "ignored"},
	))

	var record struct {
		ID int64 `sqlkit:"id"`
	}
	if err := rs.Scan(&record); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if record.ID != 7 {
		t.Fatalf("record = %#v", record)
	}
}
