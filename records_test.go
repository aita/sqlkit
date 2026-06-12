package sqlkit_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
)

func pendingRows(t *testing.T, fake *sqltest.Fake) sqlkit.Rows {
	t.Helper()
	db := sqlkit.WrapDB(fake.DB())
	return db.RawSQL(context.Background(), "SELECT id FROM t")
}

func fakeResult(columns []string, rows ...[]driver.Value) *sqltest.Fake {
	return &sqltest.Fake{
		QueryFunc: func(string, []any) (sqltest.Result, error) {
			return sqltest.Result{Columns: columns, Rows: rows}, nil
		},
	}
}

func threeRowFake() *sqltest.Fake {
	return fakeResult([]string{"id"}, []driver.Value{int64(1)}, []driver.Value{int64(2)}, []driver.Value{int64(3)})
}

type idRow struct {
	ID int64 `sqlkit:"id"`
}

func TestRecordsYieldsScannedStructs(t *testing.T) {
	var values []int64
	for row, err := range sqlkit.Records[idRow](pendingRows(t, threeRowFake())) {
		if err != nil {
			t.Fatalf("Records() error = %v", err)
		}
		values = append(values, row.ID)
	}

	if len(values) != 3 || values[0] != 1 || values[2] != 3 {
		t.Fatalf("Records() = %#v, want [1 2 3]", values)
	}
}

func TestRecordsYieldsScalars(t *testing.T) {
	var values []int64
	for id, err := range sqlkit.Records[int64](pendingRows(t, threeRowFake())) {
		if err != nil {
			t.Fatalf("Records() error = %v", err)
		}
		values = append(values, id)
	}

	if len(values) != 3 || values[1] != 2 {
		t.Fatalf("Records() = %#v, want [1 2 3]", values)
	}
}

func TestRecordsYieldsStructPointers(t *testing.T) {
	var values []*idRow
	for row, err := range sqlkit.Records[*idRow](pendingRows(t, threeRowFake())) {
		if err != nil {
			t.Fatalf("Records() error = %v", err)
		}
		values = append(values, row)
	}

	if len(values) != 3 || values[0].ID != 1 || values[2].ID != 3 {
		t.Fatalf("Records() = %#v, want IDs [1 2 3]", values)
	}
	if values[0] == values[1] {
		t.Fatal("Records() yielded the same pointer for different rows")
	}
}

func TestRecordsStreamAndStopEarly(t *testing.T) {
	fake := threeRowFake()

	var got []int64
	for row, err := range sqlkit.Records[idRow](pendingRows(t, fake)) {
		if err != nil {
			t.Fatalf("Records() error = %v", err)
		}
		got = append(got, row.ID)
		if len(got) == 2 {
			break // stop before the third row
		}
	}
	if len(got) != 2 || got[1] != 2 {
		t.Fatalf("got = %#v", got)
	}
	// The driver was asked for at most the rows we consumed (plus
	// lookahead), not the whole result set.
	if fake.RowReads() > 2 {
		t.Fatalf("driver row reads = %d, want <= 2 after early break", fake.RowReads())
	}
}

func TestRecordsSupportsScannerAndPointerFields(t *testing.T) {
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
	for r, err := range sqlkit.Records[row](pendingRows(t, fake)) {
		if err != nil {
			t.Fatalf("Records() error = %v", err)
		}
		rows = append(rows, r)
	}
	if !rows[0].Name.Valid || rows[0].Name.String != "Alice" || *rows[0].Nick != "al" {
		t.Fatalf("row 0 = %#v", rows[0])
	}
	if rows[1].Name.Valid || rows[1].Nick != nil {
		t.Fatalf("row 1 = %#v, want NULLs", rows[1])
	}
}

func TestRecordsErrorsOnUnmatchedColumn(t *testing.T) {
	fake := fakeResult([]string{"id", "mystery"}, []driver.Value{int64(1), "x"})

	var seen int
	var lastErr error
	for _, err := range sqlkit.Records[idRow](pendingRows(t, fake)) {
		lastErr = err
		seen++
	}
	if seen != 1 || lastErr == nil || !strings.Contains(lastErr.Error(), "mystery") {
		t.Fatalf("Records() yields = %d, err = %v; want one yield with unmatched-column error", seen, lastErr)
	}
}

func TestFirstReturnsFirstRowOnly(t *testing.T) {
	fake := threeRowFake()

	row, err := sqlkit.First[idRow](pendingRows(t, fake))
	if err != nil {
		t.Fatalf("First() error = %v", err)
	}
	if row.ID != 1 {
		t.Fatalf("First() = %#v, want ID 1", row)
	}
	if fake.RowReads() > 1 {
		t.Fatalf("driver row reads = %d, want 1 for First", fake.RowReads())
	}
}

func TestFirstReturnsErrNoRowsWhenEmpty(t *testing.T) {
	_, err := sqlkit.First[idRow](pendingRows(t, fakeResult([]string{"id"})))
	if !errors.Is(err, sqlkit.ErrNoRows) {
		t.Fatalf("First() error = %v, want ErrNoRows", err)
	}
}

func TestRecordsSurfacesBuildErrors(t *testing.T) {
	db := sqlkit.WrapDB(fakeResult([]string{"id"}).DB())
	rows := db.RawSQL(context.Background(), 42) // unsupported query type

	var got error
	for _, err := range sqlkit.Records[idRow](rows) {
		got = err
	}
	if got == nil {
		t.Fatal("Records() yielded no error for an invalid query")
	}
}
