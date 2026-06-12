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

func queryRecords(t *testing.T, fake *sqltest.Fake) sqlkit.RecordIterator {
	t.Helper()
	db := sqlkit.WrapDB(fake.DB())
	return db.RawSQL(context.Background(), "SELECT id FROM t").Records()
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
	var values []int64
	for record, err := range queryRecords(t, threeRecordFake()) {
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

func TestRecordsStreamAndStopEarly(t *testing.T) {
	fake := threeRecordFake()

	var got []int64
	for record, err := range queryRecords(t, fake) {
		if err != nil {
			t.Fatalf("Records() error = %v", err)
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

func TestRecordColumns(t *testing.T) {
	for record, err := range queryRecords(t, fakeResult([]string{"id", "email"}, []driver.Value{int64(1), "a@example.test"})) {
		if err != nil {
			t.Fatalf("Records() error = %v", err)
		}
		columns := record.Columns()
		if len(columns) != 2 || columns[0] != "id" || columns[1] != "email" {
			t.Fatalf("Columns() = %#v, want [id email]", columns)
		}
	}
}

func TestRecordScanSupportsScannerAndPointerFields(t *testing.T) {
	type row struct {
		Name sql.NullString `sqlkit:"name"` // database/sql Scanner
		Nick *string        `sqlkit:"nick"` // pointer for NULL
	}
	fake := fakeResult(
		[]string{"name", "nick"},
		[]driver.Value{"Alice", "al"},
		[]driver.Value{nil, nil},
	)

	var rows []row
	for record, err := range queryRecords(t, fake) {
		if err != nil {
			t.Fatalf("Records() error = %v", err)
		}
		var r row
		if err := record.Scan(&r); err != nil {
			t.Fatalf("record Scan() error = %v", err)
		}
		rows = append(rows, r)
	}
	if !rows[0].Name.Valid || rows[0].Name.String != "Alice" || *rows[0].Nick != "al" {
		t.Fatalf("record 0 = %#v", rows[0])
	}
	if rows[1].Name.Valid || rows[1].Nick != nil {
		t.Fatalf("record 1 = %#v, want NULLs", rows[1])
	}
}

func TestRecordCanBeScannedAgain(t *testing.T) {
	for record, err := range queryRecords(t, fakeResult([]string{"id"}, []driver.Value{int64(1)})) {
		if err != nil {
			t.Fatalf("Records() error = %v", err)
		}
		var first, second struct {
			ID int64 `sqlkit:"id"`
		}
		if err := record.Scan(&first); err != nil {
			t.Fatalf("first Scan() error = %v", err)
		}
		if err := record.Scan(&second); err != nil {
			t.Fatalf("second Scan() error = %v", err)
		}
		if first.ID != 1 || second.ID != 1 {
			t.Fatalf("scans = %#v, %#v, want both ID 1", first, second)
		}
	}
}

func TestStaleRecordScanErrors(t *testing.T) {
	var stale *sqlkit.Record
	row := 0
	for record, err := range queryRecords(t, threeRecordFake()) {
		if err != nil {
			t.Fatalf("Records() error = %v", err)
		}
		row++
		if row == 1 {
			retained := record
			stale = &retained
			continue
		}
		// The cursor is now on row 2; the record retained from row 1 must
		// refuse to scan rather than silently read row 2.
		var got struct {
			ID int64 `sqlkit:"id"`
		}
		err := stale.Scan(&got)
		if err == nil || !strings.Contains(err.Error(), "stale record") {
			t.Fatalf("stale Scan() error = %v, want stale-record error", err)
		}
		if err := record.Scan(&got); err != nil {
			t.Fatalf("current Scan() error = %v", err)
		}
		if got.ID != int64(row) {
			t.Fatalf("current record ID = %d, want %d", got.ID, row)
		}
	}
}

func TestRecordScanIgnoresUnmatchedColumns(t *testing.T) {
	for record, err := range queryRecords(t, fakeResult([]string{"id", "extra"}, []driver.Value{int64(7), "ignored"})) {
		if err != nil {
			t.Fatalf("Records() error = %v", err)
		}
		var row struct {
			ID int64 `sqlkit:"id"`
		}
		if err := record.Scan(&row); err != nil {
			t.Fatalf("record Scan() error = %v", err)
		}
		if row.ID != 7 {
			t.Fatalf("record = %#v", row)
		}
	}
}
