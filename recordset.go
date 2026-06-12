package sqlkit

import (
	stdsql "database/sql"
	"fmt"
	"iter"
	"reflect"
)

// Field describes one column in a RecordSet.
type Field struct {
	Name         string
	DatabaseType string
	GoType       string
	Nullable     bool
}

// RecordSet is a dynamic, read-only view over query results and their field
// metadata, as an escape hatch for when even the result shape is unknown.
// It streams: records are read from the database one at a time during
// iteration or Scan, never materialized all at once, and the underlying
// rows are closed when iteration finishes or stops early. A RecordSet is
// single-pass — iterating it a second time yields an error.
type RecordSet struct {
	rows     *stdsql.Rows
	consumed *bool
}

// FromSQLRows wraps database/sql rows for streaming iteration and mapping.
// The rows are read on demand and closed when iteration completes or stops;
// the resulting RecordSet is single-pass.
func FromSQLRows(rows *stdsql.Rows) RecordSet {
	return RecordSet{rows: rows, consumed: new(bool)}
}

// Fields returns a copy of the record set field metadata.
func (rs RecordSet) Fields() []Field {
	if rs.rows == nil {
		return nil
	}
	columnTypes, err := rs.rows.ColumnTypes()
	if err != nil {
		return nil
	}
	fields := make([]Field, len(columnTypes))
	for i, column := range columnTypes {
		nullable, _ := column.Nullable()
		fields[i] = Field{Name: column.Name(), DatabaseType: column.DatabaseTypeName(), Nullable: nullable}
	}
	return fields
}

// iterate streams the records to yield until exhaustion or yield returns
// false, and returns the first read error. The yielded Record is a view of
// the rows cursor, valid only until the next iteration step.
func (rs RecordSet) iterate(yield func(Record) bool) error {
	if rs.rows == nil {
		return nil
	}
	if *rs.consumed {
		return fmt.Errorf("sqlkit: result set already consumed; a RecordSet is single-pass")
	}
	*rs.consumed = true
	defer rs.rows.Close()

	columnNames, err := rs.rows.Columns()
	if err != nil {
		return fmt.Errorf("sqlkit: read columns: %w", err)
	}
	record := Record{rows: rs.rows, columns: newRecordColumns(columnNames), current: new(int)}
	for rs.rows.Next() {
		*record.current++
		record.row = *record.current
		if !yield(record) {
			return nil
		}
	}
	if err := rs.rows.Err(); err != nil {
		return fmt.Errorf("sqlkit: read records: %w", err)
	}
	return nil
}

// RecordIterator is the range-over-func iterator shape used by RecordSet.
// It yields each record with a nil error; if reading fails, the final yield
// carries the error with a zero Record.
type RecordIterator = iter.Seq2[Record, error]

// Iter returns a streaming iterator usable directly in a for range loop:
//
//	for record, err := range rs.Iter() {
//		if err != nil { return err }
//		...
//	}
//
// Records are fetched on demand; breaking out of the loop stops reading and
// releases the underlying result set. Each yielded Record is only valid for
// that loop step — copy values out with Scan instead of retaining it.
func (rs RecordSet) Iter() RecordIterator {
	return func(yield func(Record, error) bool) {
		err := rs.iterate(func(record Record) bool { return yield(record, nil) })
		if err != nil {
			yield(Record{}, err)
		}
	}
}

// Records returns a streaming iterator; see Iter.
func (rs RecordSet) Records() RecordIterator { return rs.Iter() }

// Scan copies records into dest, streaming them from the source. Dest must
// be a pointer to a struct (first record only; further records are not
// read), a slice of structs, or a slice of pointers to structs. Columns are
// matched by `sqlkit` tags or exported field names; the column-to-field
// mapping is resolved once per call, and values are scanned straight into
// the destination fields.
func (rs RecordSet) Scan(dest any) error {
	value := reflect.ValueOf(dest)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return errRecordNonNilPointer("scan")
	}

	elem := value.Elem()
	switch elem.Kind() {
	case reflect.Struct:
		return scanFirstRecordIntoStruct(rs, elem)
	case reflect.Slice:
		return scanRecordsIntoSlice(rs, elem)
	default:
		return fmt.Errorf("sqlkit: scan destination must point to a struct or slice")
	}
}

// scanFirstRecordIntoStruct reads only the first record and stops the
// iteration, releasing the rest of the result set. With no records, dest is
// left untouched.
func scanFirstRecordIntoStruct(rs RecordSet, dest reflect.Value) error {
	var scanErr error
	err := rs.iterate(func(record Record) bool {
		scanErr = scanRecordInto(record, dest)
		return false
	})
	if err != nil {
		return err
	}
	return scanErr
}

func scanRecordsIntoSlice(rs RecordSet, dest reflect.Value) error {
	elemType := dest.Type().Elem()
	if elemType.Kind() == reflect.Pointer {
		elemType = elemType.Elem()
	}
	if elemType.Kind() != reflect.Struct {
		return fmt.Errorf("sqlkit: scan slice element must be a struct or pointer to struct")
	}

	mapped := reflect.MakeSlice(dest.Type(), 0, 8)
	index := 0
	var plan []fieldColumn
	var scanErr error
	err := rs.iterate(func(record Record) bool {
		if plan == nil {
			// All records of a set share one column layout, so the plan is
			// built from the first record and reused.
			plan = recordScanPlan(elemType, record.columns)
		}
		item := reflect.New(elemType).Elem()
		targets := recordScanTargets(item, plan, len(record.columns.names))
		if err := record.rows.Scan(targets...); err != nil {
			scanErr = fmt.Errorf("sqlkit: scan record %d: %w", index, err)
			return false
		}
		if dest.Type().Elem().Kind() == reflect.Pointer {
			mapped = reflect.Append(mapped, item.Addr())
		} else {
			mapped = reflect.Append(mapped, item)
		}
		index++
		return true
	})
	if err != nil {
		return err
	}
	if scanErr != nil {
		return scanErr
	}
	dest.Set(mapped)
	return nil
}
