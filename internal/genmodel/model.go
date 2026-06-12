// Package genmodel contains the database model consumed by code generation.
package genmodel

// Schema is a loaded database schema.
type Schema struct {
	Name   string
	Tables []Table
}

// Table is a database table.
type Table struct {
	Schema  string
	Name    string
	Columns []Column
}

// Column is a database column.
type Column struct {
	Name       string
	DataType   string // engine type name (PostgreSQL udt_name today, e.g. "int8")
	Nullable   bool
	HasDefault bool
	PrimaryKey bool
}
