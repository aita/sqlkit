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
// A RecordSet built over *sql.Rows streams: records are read from the
// database one at a time during iteration or Scan, never materialized all at
// once, and the underlying rows are closed when iteration finishes or stops
// early. Such a RecordSet is single-pass — iterating it a second time yields
// an error.
type RecordSet struct {
	source recordSource
}

type recordSource interface {
	Fields() ([]Field, error)
	// iterate streams records to yield until exhaustion or yield returns
	// false, and returns the first read error.
	iterate(yield func(Record) bool) error
}

type memoryRecordSource struct {
	fields  []Field
	records []Record
}

func newRecordSet(fields []Field, rows [][]any) RecordSet {
	columnNames := make([]string, len(fields))
	for i, field := range fields {
		columnNames[i] = field.Name
	}
	columns := newRecordColumns(columnNames)
	records := make([]Record, len(rows))
	for i, values := range rows {
		records[i] = Record{columns: columns, values: append([]any(nil), values...)}
	}
	return RecordSet{source: memoryRecordSource{fields: append([]Field(nil), fields...), records: records}}
}

func (s memoryRecordSource) Fields() ([]Field, error) { return append([]Field(nil), s.fields...), nil }

func (s memoryRecordSource) iterate(yield func(Record) bool) error {
	for _, record := range s.records {
		if !yield(record) {
			return nil
		}
	}
	return nil
}

type sqlRowsSource struct {
	rows     *stdsql.Rows
	consumed bool
}

// FromSQLRows wraps database/sql rows for streaming iteration and mapping.
// The rows are read on demand and closed when iteration completes or stops;
// the resulting RecordSet is single-pass.
func FromSQLRows(rows *stdsql.Rows) RecordSet { return RecordSet{source: &sqlRowsSource{rows: rows}} }

func (s *sqlRowsSource) Fields() ([]Field, error) {
	columnTypes, err := s.rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	fields := make([]Field, len(columnTypes))
	for i, column := range columnTypes {
		nullable, _ := column.Nullable()
		fields[i] = Field{Name: column.Name(), DatabaseType: column.DatabaseTypeName(), Nullable: nullable}
	}
	return fields, nil
}

func (s *sqlRowsSource) iterate(yield func(Record) bool) error {
	if s.consumed {
		return fmt.Errorf("sqlkit: result set already consumed; a RecordSet over sql.Rows is single-pass")
	}
	s.consumed = true
	defer s.rows.Close()

	columnNames, err := s.rows.Columns()
	if err != nil {
		return err
	}
	columns := newRecordColumns(columnNames)
	scanTargets := make([]any, len(columnNames))
	for s.rows.Next() {
		values := make([]any, len(columnNames))
		for i := range values {
			scanTargets[i] = &values[i]
		}
		if err := s.rows.Scan(scanTargets...); err != nil {
			return err
		}
		if !yield(Record{columns: columns, values: values}) {
			return nil
		}
	}
	return s.rows.Err()
}

// Fields returns a copy of the record set field metadata.
func (rs RecordSet) Fields() []Field {
	if rs.source == nil {
		return nil
	}
	fields, err := rs.source.Fields()
	if err != nil {
		return nil
	}
	return fields
}

// iterate streams the records to yield and returns the first read error.
func (rs RecordSet) iterate(yield func(Record) bool) error {
	if rs.source == nil {
		return nil
	}
	if err := rs.source.iterate(yield); err != nil {
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
// releases the underlying result set.
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
// mapping is resolved once per call, not per record.
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
		plan := recordScanPlan(dest.Type(), record.columns)
		scanErr = scanRecordWithPlan(record, dest, plan)
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
		if err := scanRecordWithPlan(record, item, plan); err != nil {
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
