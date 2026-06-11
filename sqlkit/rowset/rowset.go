package rowset

import (
	"database/sql"
	"fmt"
)

// RowSet is a read-only view over query results and their field metadata.
type RowSet struct {
	fields []Field
	source rowSource
}

type rowSource interface {
	Fields() ([]Field, error)
	Rows() ([]Row, error)
}

type memorySource struct {
	fields []Field
	rows   []Row
}

func newRowSet(fields []Field, rows []Row) RowSet {
	return RowSet{source: memorySource{fields: append([]Field(nil), fields...), rows: append([]Row(nil), rows...)}}
}

func (s memorySource) Fields() ([]Field, error) { return append([]Field(nil), s.fields...), nil }
func (s memorySource) Rows() ([]Row, error)     { return append([]Row(nil), s.rows...), nil }

type sqlRowsSource struct{ rows *sql.Rows }

// FromSQLRows wraps database/sql rows for later scanning and mapping.
func FromSQLRows(rows *sql.Rows) RowSet { return RowSet{source: sqlRowsSource{rows: rows}} }

func (s sqlRowsSource) Fields() ([]Field, error) {
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

func (s sqlRowsSource) Rows() ([]Row, error) {
	columns, err := s.rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []Row
	for s.rows.Next() {
		values := make([]any, len(columns))
		scanTargets := make([]any, len(columns))
		for i := range scanTargets {
			scanTargets[i] = &values[i]
		}
		if err := s.rows.Scan(scanTargets...); err != nil {
			return nil, err
		}
		rowValues := make(map[string]any, len(columns))
		for i, column := range columns {
			rowValues[column] = values[i]
		}
		out = append(out, newRow(rowValues))
	}
	if err := s.rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
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

func (rs RowSet) collectRows() ([]Row, error) {
	if rs.source == nil {
		return nil, nil
	}
	rows, err := rs.source.Rows()
	if err != nil {
		return nil, fmt.Errorf("rowset: read rows: %w", err)
	}
	return rows, nil
}
