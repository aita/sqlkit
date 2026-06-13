// Package meta is the database schema metadata model: the flat, value-only
// abstraction of tables and columns that sqlkit's SQL generators consume. The
// sqldef package builds it from in-code designs and the inspector introspects
// a live database into it (for the initial bootstrap); the codegen and ddl
// generators take it from there. It is a pure data model with no database,
// dialect, or generation dependencies — the dialect is supplied to the
// generators at call time, not stored on the model.
//
// Everything is a value type with no back-references, so a design may be split
// across packages: each exposes the []Table it owns and the generators run
// over the concatenation.
package meta

// Meta is a resolved set of tables — the dialect-agnostic value that a sqldef
// catalog builds into and that the codegen and ddl generators consume. It is
// the meta-level hand-off type: sqldef.Catalog.Build returns one, and the
// generators take it (plus the target dialect) from there.
type Meta struct {
	Tables []Table
}

// Schemas returns the distinct schemas of the tables, in first-seen order.
func (m Meta) Schemas() []Schema {
	var out []Schema
	seen := map[string]bool{}
	for _, t := range m.Tables {
		if !seen[t.Schema.Name] {
			seen[t.Schema.Name] = true
			out = append(out, t.Schema)
		}
	}
	return out
}

// Schema identifies the schema (PostgreSQL schema / MySQL database) a table
// belongs to. It is a small value, not a container: construct
// meta.Schema{Name: "public"} wherever one is needed. The zero value is the
// unnamed default schema, which generates unqualified names.
type Schema struct {
	Name string
}

// Table is a database table. Schema is the schema it belongs to.
type Table struct {
	Schema      Schema
	Name        string
	Comment     string
	Columns     []Column
	ForeignKeys []ForeignKey
	Indexes     []Index
	Checks      []Check
}

// Type is a portable column type: the engine type name for each supported
// dialect plus optional length/precision modifiers. The model stores no
// dialect — a Type carries every dialect it is valid on, so one Catalog
// targets both. Generators resolve it to a target engine name with Engine.
type Type struct {
	Postgres string
	MySQL    string
	// Length is the declared type length, e.g. the N of varchar(N) or char(N);
	// 0 when not applicable.
	Length int
	// Precision and Scale describe a numeric/decimal type, e.g. (10, 2) of
	// decimal(10, 2); 0 when not applicable.
	Precision int
	Scale     int
}

// Engine returns the engine type name for the dialect ("postgres" or "mysql")
// and whether the type is available on it. An empty engine name reports the
// type as unavailable on that dialect.
func (t Type) Engine(dialect string) (string, bool) {
	switch dialect {
	case "mysql":
		return t.MySQL, t.MySQL != ""
	default:
		return t.Postgres, t.Postgres != ""
	}
}

// Column is a database column. Type is portable; generators resolve it to an
// engine type name for the target dialect.
type Column struct {
	Name       string
	Type       Type
	Nullable   bool
	HasDefault bool
	// DefaultExpr is the default's SQL expression when known (designs declare
	// it with Default("now()")); HasDefault may be set without it, e.g. by the
	// inspector. The DDL generator renders integer primary keys that have a
	// default but no expression as serial/auto-increment columns.
	DefaultExpr string
	PrimaryKey  bool
	Comment     string
}

// Index is a secondary index over Columns (the primary key index is not
// listed; it is implied by Column.PrimaryKey). Unique indexes cover both
// explicit unique indexes and the indexes backing UNIQUE constraints.
type Index struct {
	Name    string
	Columns []string
	Unique  bool
}

// Check is a CHECK constraint with its bare boolean expression (no CHECK
// keyword or outer parentheses).
type Check struct {
	Name       string
	Expression string
}

// ForeignKey describes a foreign-key constraint: the local Columns reference
// RefColumns of RefTable (in RefSchema).
type ForeignKey struct {
	Name       string
	Columns    []string
	RefSchema  Schema
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
