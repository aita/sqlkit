// Package meta is the database schema metadata model: the flat, value-only
// abstraction of tables and columns that sqlkit's SQL generators consume. The
// sqldef package builds it from in-code catalogs and the inspector introspects
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
// sqldef catalog builds into and that the codegen generator and the DDL
// renderer consume. It is the meta-level hand-off type: sqldef.Catalog.Build
// returns one, and the generators take it (plus the target dialect) from there.
type Metadata struct {
	Tables []Table
}

// Schemas returns the distinct schemas of the tables, in first-seen order.
func (m Metadata) Schemas() []Schema {
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
}

// Type is a portable column type. It commits to an engine type name only when
// a generator picks a dialect (Engine), so the model stores no dialect itself.
// The sqldef DSL supplies portable types that resolve on both dialects; the
// inspector supplies single-dialect ones (see Of).
type Type interface {
	// Engine resolves the type for the dialect ("postgres" or "mysql"); ok is
	// false when the type has no representation on it.
	Engine(dialect string) (Engine, bool)
}

// Of returns a Type known only on the given dialect — what the inspector
// produces from one introspected engine type, and a convenient way to build a
// meta.Column by hand for a single target dialect.
func Of(dialect, name string, length, precision, scale int) Type {
	return single{dialect: dialect, eng: Engine{Name: name, Length: length, Precision: precision, Scale: scale}}
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
	// without one, e.g. by the inspector. The DDL generator renders integer
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
// target dialect; the inspector supplies it as a sql.Raw of the introspected
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
