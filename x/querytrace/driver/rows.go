package driver

import (
	stddriver "database/sql/driver"
	"io"
	"reflect"
	"time"

	qt "github.com/aita/sqlkit/x/querytrace"
)

// rows wraps a driver.Rows to time how the cursor is consumed: when the first
// and last rows are read, how many rows pass, and when it is closed. It records
// the timings into the shared RowsInfo also held by the query's Event.
//
// It implements the optional column-type interfaces transparently, delegating
// to the underlying cursor when it supports them and returning database/sql's
// own defaults when it does not, so wrapping does not change how rows scan.
type rows struct {
	r    stddriver.Rows
	info *qt.RowsInfo
}

var (
	_ stddriver.Rows                           = (*rows)(nil)
	_ stddriver.RowsNextResultSet              = (*rows)(nil)
	_ stddriver.RowsColumnTypeScanType         = (*rows)(nil)
	_ stddriver.RowsColumnTypeDatabaseTypeName = (*rows)(nil)
	_ stddriver.RowsColumnTypeNullable         = (*rows)(nil)
	_ stddriver.RowsColumnTypeLength           = (*rows)(nil)
	_ stddriver.RowsColumnTypePrecisionScale   = (*rows)(nil)
)

func newRows(r stddriver.Rows, info *qt.RowsInfo) *rows {
	return &rows{r: r, info: info}
}

// Columns returns the underlying column names.
func (rw *rows) Columns() []string { return rw.r.Columns() }

// Next reads the next row and updates the iteration timings on success.
func (rw *rows) Next(dest []stddriver.Value) error {
	err := rw.r.Next(dest)
	if err == nil {
		now := time.Now()
		if rw.info.FirstNextAt.IsZero() {
			rw.info.FirstNextAt = now
		}
		rw.info.LastNextAt = now
		rw.info.NextCount++
		rw.info.IterationDuration = now.Sub(rw.info.QueryReturnedAt)
	}
	return err
}

// Close closes the cursor and records the close time and open duration.
func (rw *rows) Close() error {
	err := rw.r.Close()
	now := time.Now()
	rw.info.ClosedAt = now
	rw.info.Closed = true
	rw.info.OpenDuration = now.Sub(rw.info.QueryReturnedAt)
	return err
}

// HasNextResultSet delegates when supported, else reports no further result set.
func (rw *rows) HasNextResultSet() bool {
	if n, ok := rw.r.(stddriver.RowsNextResultSet); ok {
		return n.HasNextResultSet()
	}
	return false
}

// NextResultSet delegates when supported, else reports end of result sets.
func (rw *rows) NextResultSet() error {
	if n, ok := rw.r.(stddriver.RowsNextResultSet); ok {
		return n.NextResultSet()
	}
	return io.EOF
}

// ColumnTypeScanType delegates when supported, else returns the empty-interface
// type database/sql uses by default.
func (rw *rows) ColumnTypeScanType(index int) reflect.Type {
	if t, ok := rw.r.(stddriver.RowsColumnTypeScanType); ok {
		return t.ColumnTypeScanType(index)
	}
	return reflect.TypeOf((*any)(nil)).Elem()
}

// ColumnTypeDatabaseTypeName delegates when supported, else returns "".
func (rw *rows) ColumnTypeDatabaseTypeName(index int) string {
	if t, ok := rw.r.(stddriver.RowsColumnTypeDatabaseTypeName); ok {
		return t.ColumnTypeDatabaseTypeName(index)
	}
	return ""
}

// ColumnTypeNullable delegates when supported, else reports unknown nullability.
func (rw *rows) ColumnTypeNullable(index int) (nullable, ok bool) {
	if t, found := rw.r.(stddriver.RowsColumnTypeNullable); found {
		return t.ColumnTypeNullable(index)
	}
	return false, false
}

// ColumnTypeLength delegates when supported, else reports unknown length.
func (rw *rows) ColumnTypeLength(index int) (length int64, ok bool) {
	if t, found := rw.r.(stddriver.RowsColumnTypeLength); found {
		return t.ColumnTypeLength(index)
	}
	return 0, false
}

// ColumnTypePrecisionScale delegates when supported, else reports unknown.
func (rw *rows) ColumnTypePrecisionScale(index int) (precision, scale int64, ok bool) {
	if t, found := rw.r.(stddriver.RowsColumnTypePrecisionScale); found {
		return t.ColumnTypePrecisionScale(index)
	}
	return 0, 0, false
}
