// Package rowset provides a dynamic, reflection-based view over query
// results, as an escape hatch for when even the result shape is unknown.
package rowset

import (
	"database/sql"
	"fmt"
)

// RowSet is a read-only view over query results and their field metadata.
// A RowSet built over *sql.Rows streams: rows are read from the database one
// at a time during iteration or Scan, never materialized all at once, and
// the underlying rows are closed when iteration finishes or stops early.
// Such a RowSet is single-pass — iterating it a second time yields an error.
type RowSet struct {
	fields []Field
	source rowSource
}

type rowSource interface {
	Fields() ([]Field, error)
	// iterate streams rows to yield until exhaustion or yield returns false,
	// and returns the first read error.
	iterate(yield func(Row) bool) error
}

type memorySource struct {
	fields []Field
	rows   []Row
}

func newRowSet(fields []Field, rows []Row) RowSet {
	return RowSet{source: memorySource{fields: append([]Field(nil), fields...), rows: append([]Row(nil), rows...)}}
}

func (s memorySource) Fields() ([]Field, error) { return append([]Field(nil), s.fields...), nil }

func (s memorySource) iterate(yield func(Row) bool) error {
	for _, row := range s.rows {
		if !yield(row) {
			return nil
		}
	}
	return nil
}

type sqlRowsSource struct {
	rows     *sql.Rows
	consumed bool
}

// FromSQLRows wraps database/sql rows for streaming iteration and mapping.
// The rows are read on demand and closed when iteration completes or stops;
// the resulting RowSet is single-pass.
func FromSQLRows(rows *sql.Rows) RowSet { return RowSet{source: &sqlRowsSource{rows: rows}} }

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

func (s *sqlRowsSource) iterate(yield func(Row) bool) error {
	if s.consumed {
		return fmt.Errorf("rowset: result set already consumed; a RowSet over sql.Rows is single-pass")
	}
	s.consumed = true
	defer s.rows.Close()

	columns, err := s.rows.Columns()
	if err != nil {
		return err
	}
	for s.rows.Next() {
		values := make([]any, len(columns))
		scanTargets := make([]any, len(columns))
		for i := range scanTargets {
			scanTargets[i] = &values[i]
		}
		if err := s.rows.Scan(scanTargets...); err != nil {
			return err
		}
		rowValues := make(map[string]any, len(columns))
		for i, column := range columns {
			rowValues[column] = values[i]
		}
		if !yield(newRow(rowValues)) {
			return nil
		}
	}
	return s.rows.Err()
}

// Fields returns a copy of the row set field metadata.
func (rs RowSet) Fields() []Field {
	fields, err := rs.collectFields()
	if err != nil {
		return nil
	}
	return fields
}

func (rs RowSet) collectFields() ([]Field, error) {
	if len(rs.fields) > 0 {
		return append([]Field(nil), rs.fields...), nil
	}
	if rs.source == nil {
		return nil, nil
	}
	fields, err := rs.source.Fields()
	if err != nil {
		return nil, fmt.Errorf("rowset: read fields: %w", err)
	}
	return fields, nil
}

// iterate streams the rows to yield and returns the first read error.
func (rs RowSet) iterate(yield func(Row) bool) error {
	if rs.source == nil {
		return nil
	}
	if err := rs.source.iterate(yield); err != nil {
		return fmt.Errorf("rowset: read rows: %w", err)
	}
	return nil
}
