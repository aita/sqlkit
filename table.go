package sqlkit

import (
	"github.com/aita/sqlkit/sql"
)

// TableMeta is implemented by generated table bindings: the table identity
// and column metadata the builders use to generate statements. Row data flows
// in through Insert(...).Values (struct fields mapped to columns by name) and
// out through Scan, so bindings carry no row-type parameter.
type TableMeta interface {
	sql.Source

	// Info returns the table identity.
	Info() TableInfo

	// Columns returns all column bindings in declaration order.
	Columns() []AnyColumn

	// PrimaryKey returns the primary key column bindings.
	PrimaryKey() []AnyColumn

	// Defaulted returns the columns that have a database-side default.
	// INSERT omits them when the row value is the Go zero value, letting the
	// database fill them in.
	Defaulted() []AnyColumn
}

// tableDef converts a table binding to the sql-level table definition. All
// row-oriented SQL generation (INSERT column selection, primary key
// targeting) lives in sql.TableDef; this package only supplies values and
// scans results.
func tableDef(table TableMeta) sql.TableDef {
	return sql.TableDef{
		Table:      table.Info().sqlTable(),
		Columns:    columnNames(table.Columns()),
		PrimaryKey: columnNames(table.PrimaryKey()),
		Defaulted:  columnNames(table.Defaulted()),
	}
}

func columnNames(columns []AnyColumn) []string {
	names := make([]string, len(columns))
	for i, column := range columns {
		names[i] = column.ColumnName()
	}
	return names
}
