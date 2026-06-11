// Package meta contains database metadata types used by generated bindings.
package meta

import (
	"reflect"

	querysql "github.com/sqlkit-dev/sqlkit/sqlkit/sql"
)

// Database describes the database shape known to sqlkit at runtime.
type Database struct {
	Tables []Table
}

// DefineTable creates table metadata for generated table bindings.
func DefineTable(name string, opts ...TableOption) Table {
	table := Table{Name: name}
	for _, opt := range opts {
		opt(&table)
	}
	return table
}

// TableOption configures table metadata.
type TableOption func(*Table)

// WithSchema sets the table schema.
func WithSchema(schema string) TableOption {
	return func(t *Table) { t.Schema = schema }
}

// Table describes a generated table binding.
type Table struct {
	Schema  string
	Name    string
	Columns []Column
}

// QualifiedName returns the table name with an optional schema prefix.
func (t Table) QualifiedName() string {
	if t.Schema == "" {
		return t.Name
	}
	return t.Schema + "." + t.Name
}

// SQLSource marks Table as a sql.Source.
func (t Table) SQLSource() querysql.SourceExpression {
	return querysql.SourceExpression{Name: t.QualifiedName()}
}

// Column creates a column reference attached to this table.
func (t Table) Column(name string, opts ...ColumnOption) Column {
	column := Column{Name: name, table: &t}
	for _, opt := range opts {
		opt(&column)
	}
	return column
}

// ColumnOption configures column metadata.
type ColumnOption func(*Column)

// Type sets the database type name.
func Type(name string) ColumnOption {
	return func(c *Column) { c.DatabaseType = name }
}

// Varchar sets a varchar database type with a length.
func Varchar(length int) ColumnOption {
	return func(c *Column) {
		c.DatabaseType = "varchar"
		c.Length = length
	}
}

// WithGoName sets the generated Go field name.
func WithGoName(name string) ColumnOption {
	return func(c *Column) { c.GoName = name }
}

// WithGoType sets the generated Go type from either a type name string or a sample value.
func WithGoType(value any) ColumnOption {
	return func(c *Column) {
		switch v := value.(type) {
		case string:
			c.GoType = v
		default:
			c.GoType = typeName(v)
		}
	}
}

// Nullable marks a column as nullable.
func Nullable() ColumnOption {
	return func(c *Column) { c.Nullable = true }
}

// PrimaryKey marks a column as part of the primary key.
func PrimaryKey() ColumnOption {
	return func(c *Column) { c.PrimaryKey = true }
}

// Column describes a generated column binding and can be used as a SQL
// expression or SELECT-list item in query builders.
type Column struct {
	table        *Table
	Name         string
	GoName       string
	DatabaseType string
	GoType       string
	Length       int
	Nullable     bool
	PrimaryKey   bool
}

// SQLExpression marks Column as a sql.Expression.
func (Column) SQLExpression() {}

// SQLSelection marks Column as a sql.Selection.
func (Column) SQLSelection() {}

// Reference returns the qualified SQL column reference.
func (c Column) Reference() string {
	if c.table == nil || c.table.Name == "" {
		return c.Name
	}
	return c.table.QualifiedName() + "." + c.Name
}

func typeName(value any) string {
	if value == nil {
		return ""
	}
	t := reflect.TypeOf(value)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.PkgPath() == "" {
		return t.Name()
	}
	return t.PkgPath() + "." + t.Name()
}
