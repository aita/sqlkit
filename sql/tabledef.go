package sql

import (
	"fmt"
	"reflect"
)

// TableDef describes a table's shape for row-oriented statement generation:
// the columns in scan order, the primary key, and the columns with
// database-side defaults. It is the single place that turns row values into
// statements; the typed builders only supply values.
type TableDef struct {
	Table      Table
	Columns    []string
	PrimaryKey []string
	Defaulted  []string
}

// ColumnRef returns the qualified reference for one of the table's columns.
func (d TableDef) ColumnRef(name string) ColumnExpression {
	return Col(d.Table.QualifiedName() + "." + name)
}

// Selections returns all columns as qualified SELECT-list items in Columns
// order.
func (d TableDef) Selections() []Selection {
	out := make([]Selection, len(d.Columns))
	for i, name := range d.Columns {
		out[i] = d.ColumnRef(name)
	}
	return out
}

// SelectRows creates a SELECT of all columns from the table.
func (d TableDef) SelectRows() SelectQuery {
	return Select(d.Selections()...).From(d.Table)
}

// InsertRows creates an INSERT for rows of values given in Columns order.
// Columns with a database-side default are omitted when their value is the
// zero value in every row, letting the database fill them in.
func (d TableDef) InsertRows(rows [][]any) (InsertQuery, error) {
	include, err := d.insertMask(rows)
	if err != nil {
		return InsertQuery{}, err
	}
	var names []string
	for i, name := range d.Columns {
		if include[i] {
			names = append(names, name)
		}
	}
	insert := InsertInto(d.Table).Columns(names...)
	for _, row := range rows {
		var values []any
		for i := range d.Columns {
			if include[i] {
				values = append(values, row[i])
			}
		}
		insert = insert.Values(values...)
	}
	return insert, nil
}

// insertMask reports, per column, whether an INSERT should include it.
func (d TableDef) insertMask(rows [][]any) ([]bool, error) {
	defaulted := make(map[string]bool, len(d.Defaulted))
	for _, name := range d.Defaulted {
		defaulted[name] = true
	}
	include := make([]bool, len(d.Columns))
	for i, name := range d.Columns {
		include[i] = !defaulted[name]
	}
	for r, row := range rows {
		if len(row) != len(d.Columns) {
			return nil, fmt.Errorf("sqlkit/sql: INSERT row %d has %d values for %d columns of %s",
				r, len(row), len(d.Columns), d.Table.QualifiedName())
		}
		for i := range d.Columns {
			if !include[i] && !isZeroValue(row[i]) {
				include[i] = true
			}
		}
	}
	return include, nil
}

func isZeroValue(value any) bool {
	if value == nil {
		return true
	}
	return reflect.ValueOf(value).IsZero()
}
