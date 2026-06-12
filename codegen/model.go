// Package codegen renders typed sqlkit table bindings (and, optionally, row
// DTOs) from a database schema. It supports PostgreSQL and MySQL and is usable
// both through the sqlkitgen command and as a library: the Schema model can be
// built by hand or by the loader package, the SQL→Go type mapping is
// customizable through Options.TypeMapper, and generation targets (table
// bindings, DTOs, foreign-key relations) and file layout are configured through
// Options.
package codegen

// Schema is a loaded database schema.
type Schema struct {
	Name string
	// Dialect identifies the source database ("postgres" or "mysql"). It
	// selects the default SQL→Go type table; Options.Dialect overrides it.
	Dialect string
	Tables  []Table
}

// Table is a database table.
type Table struct {
	Schema      string
	Name        string
	Columns     []Column
	ForeignKeys []ForeignKey
}

// Column is a database column.
type Column struct {
	Name       string
	DataType   string // engine type name (PostgreSQL udt_name / MySQL data_type)
	Nullable   bool
	HasDefault bool
	PrimaryKey bool
}

// ForeignKey describes a foreign-key constraint: the local Columns reference
// RefColumns of RefTable (in RefSchema). Code generation turns it into typed
// Relations accessors on the involved table bindings.
type ForeignKey struct {
	Name       string
	Columns    []string
	RefSchema  string
	RefTable   string
	RefColumns []string
}
