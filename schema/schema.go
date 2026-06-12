// Package schema is the database schema model shared across sqlkit's tooling:
// the inspector introspects a live database into it, the dsl package builds it
// from in-code designs, code generation consumes it, and a future migration
// generator will too. It is a pure data model with no database or generation
// dependencies.
package schema

// Schema is a loaded database schema.
type Schema struct {
	Name string
	// Dialect identifies the source database ("postgres" or "mysql"). It
	// selects the default SQL→Go type table during code generation.
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
	// Length is the declared type length, e.g. the N of varchar(N) or char(N);
	// 0 when not applicable.
	Length int
	// Precision and Scale describe a numeric/decimal type, e.g. (10, 2) of
	// decimal(10, 2); 0 when not applicable.
	Precision int
	Scale     int
}

// ForeignKey describes a foreign-key constraint: the local Columns reference
// RefColumns of RefTable (in RefSchema).
type ForeignKey struct {
	Name       string
	Columns    []string
	RefSchema  string
	RefTable   string
	RefColumns []string
}

// GoType describes a generated Go type and the import it needs, if any.
type GoType struct {
	ImportPath string
	Name       string
	Pointer    bool
}

// String renders the type as it appears in Go source.
func (t GoType) String() string {
	if t.Pointer {
		return "*" + t.Name
	}
	return t.Name
}
