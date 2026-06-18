// Package meta is the database schema metadata model: the flat, value-only
// abstraction of tables and columns that sqlkit's SQL generators consume. The
// decl package builds it from in-code catalogs and the introspection introspects
// a live database into it (for the initial bootstrap); the codegen generator
// takes it from there, and the model renders its own DDL (see ddl.go). Default
// and check expressions are carried as dialect-agnostic sql.Expression AST
// nodes; the dialect is supplied to the generators at call time, not stored on
// the model.
//
// Everything is a value type with no back-references, so a catalog may be split
// across packages: each exposes the []Table it owns and the generators run
// over the concatenation.
package meta

import "github.com/aita/sqlkit/sql"

// Metadata is a resolved set of tables — the dialect-agnostic value that a
// decl catalog builds into and that the codegen generator and the DDL
// renderer consume. It is the meta-level hand-off type: decl.Catalog.Build
// returns one, and the generators take it (plus the target dialect) from there.
type Metadata struct {
	Tables []Table
	// Extensions, Enums, and Sequences are the non-table objects the schema
	// declares (PostgreSQL). CreateSQL emits them around the tables in dependency
	// order — extensions and enums before the tables that use them, sequences too,
	// and DropSQL in reverse.
	Extensions []Extension
	Enums      []Enum
	Sequences  []Sequence
}

// Schemas returns the distinct schemas of the tables, in first-seen order. When
// the same schema name appears with differing Create flags (e.g. one table
// marks it for creation and another does not), the result reports Create=true:
// the schema is managed if any of its tables ask for it.
func (m Metadata) Schemas() []Schema {
	var out []Schema
	index := map[string]int{}
	for _, t := range m.Tables {
		if i, ok := index[t.Schema.Name]; ok {
			if t.Schema.Create {
				out[i].Create = true
			}
			continue
		}
		index[t.Schema.Name] = len(out)
		out = append(out, t.Schema)
	}
	return out
}

// Schema identifies the schema (PostgreSQL schema / MySQL database) a table
// belongs to. It is a small value, not a container: construct
// meta.Schema{Name: "public"} wherever one is needed. The zero value is the
// unnamed default schema, which generates unqualified names.
//
// Create marks the schema for migration management: when set (and the schema is
// named), Metadata.CreateSQL emits a CREATE SCHEMA for it and DropSQL a DROP
// SCHEMA. It defaults to false, so sqlkit never creates a schema it was not
// told to — in particular the default schema, which is assumed to already
// exist.
type Schema struct {
	Name   string
	Create bool
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

// Engine is a column type resolved for a specific dialect: the engine type
// name plus any length/precision modifiers.
type Engine struct {
	Name string
	// Length is the declared type length, e.g. the N of varchar(N) or char(N);
	// 0 when not applicable.
	Length int
	// Precision and Scale describe a numeric/decimal type, e.g. (10, 2) of
	// decimal(10, 2); 0 when not applicable.
	Precision int
	Scale     int
	// Array reports that the column is a one-dimensional array of the named
	// element type (PostgreSQL only). The Name/Length/Precision/Scale describe the
	// element; the DDL renderer appends "[]" and the code generator maps the
	// column to a Go slice of the element's type.
	Array bool
}

// Type is a portable column type. It commits to an engine type name only when
// a generator picks a dialect (Engine), so the model stores no dialect itself.
// The decl DSL supplies portable types that resolve on both dialects; the
// introspection supplies single-dialect ones (see Of).
type Type interface {
	// Engine resolves the type for the dialect ("postgres" or "mysql"); ok is
	// false when the type has no representation on it.
	Engine(dialect string) (Engine, bool)
}

// GoTyped is an optional interface a meta.Type may implement to pin the Go type
// it maps to, letting the schema fix the SQL→Go mapping at the source instead of
// relying on the code generator's built-in type tables. codegen's
// DefaultTypeMapper honors it (after its explicit Overrides, so a codegen-side
// override still wins). The decl DSL's DataType implements it via DataType.Go;
// the introspection's single-dialect types do not, so introspected schemas keep
// using the generator's tables.
type GoTyped interface {
	// GoType returns the Go type pinned for the dialect ("postgres" or "mysql");
	// ok is false when no type is pinned (for that dialect), so the mapper falls
	// back to its tables.
	GoType(dialect string) (GoType, bool)
}

// Of returns a Type known only on the given dialect — what the introspection
// produces from one introspected engine type, and a convenient way to build a
// meta.Column by hand for a single target dialect.
func Of(dialect, name string, length, precision, scale int) Type {
	return single{dialect: dialect, eng: Engine{Name: name, Length: length, Precision: precision, Scale: scale}}
}

// OfArray is Of for a one-dimensional array column whose elements are the named
// engine type (PostgreSQL only): the resolved Engine carries Array=true, so the
// DDL renderer appends "[]" and the code generator maps the column to a Go slice
// of the element's type. It is what the introspection produces from an array column.
func OfArray(dialect, name string, length, precision, scale int) Type {
	return single{dialect: dialect, eng: Engine{Name: name, Length: length, Precision: precision, Scale: scale, Array: true}}
}

type single struct {
	dialect string
	eng     Engine
}

func (s single) Engine(dialect string) (Engine, bool) {
	if dialect != s.dialect {
		return Engine{}, false
	}
	return s.eng, true
}

// Column is a database column. Type is portable; generators resolve it to an
// engine type name for the target dialect.
type Column struct {
	Name       string
	Type       Type
	Nullable   bool
	HasDefault bool
	// DefaultExpr is the default's SQL expression when known (catalogs declare
	// it with Default(sql.Raw("now()"))); it is nil when HasDefault is set
	// without one, e.g. by the introspect. The DDL generator renders integer
	// primary keys that have a default but no expression as serial/auto-increment
	// columns, and compiles the expression for the target dialect otherwise.
	DefaultExpr sql.Expression
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
// keyword or outer parentheses). The DDL generator compiles Expression for the
// target dialect; the introspection supplies it as a sql.Raw of the introspected
// clause.
type Check struct {
	Name       string
	Expression sql.Expression
}

// ReferentialAction is a foreign-key ON DELETE / ON UPDATE action. The empty
// value means none was declared (the engine default, NO ACTION) and renders
// nothing; the named values render their clause.
type ReferentialAction string

// The referential actions a foreign key may take on delete/update. NoAction is
// the explicit form of the default; the empty ReferentialAction renders nothing.
const (
	NoAction   ReferentialAction = "NO ACTION"
	Restrict   ReferentialAction = "RESTRICT"
	Cascade    ReferentialAction = "CASCADE"
	SetNull    ReferentialAction = "SET NULL"
	SetDefault ReferentialAction = "SET DEFAULT"
)

// ForeignKey describes a foreign-key constraint: the local Columns reference
// RefColumns of RefTable (in RefSchema). OnDelete/OnUpdate carry the referential
// actions; an empty action is the engine default and renders nothing.
type ForeignKey struct {
	Name       string
	Columns    []string
	RefSchema  Schema
	RefTable   string
	RefColumns []string
	OnDelete   ReferentialAction
	OnUpdate   ReferentialAction
}

// PrimaryKeyColumns returns the names of the table's primary-key columns, in
// column order.
func (t Table) PrimaryKeyColumns() []string {
	var pk []string
	for _, c := range t.Columns {
		if c.PrimaryKey {
			pk = append(pk, c.Name)
		}
	}
	return pk
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
