// Package sqldef declares database schemas as plain Go values. A Catalog is
// the reusable root: tables are declared as values hanging off it, so designs
// can reference each other (foreign keys take the *Table they point at), be
// split across files or packages, and feed every generator — the typed table
// bindings (codegen) and the DDL (ddl) come from the same catalog.
//
// A design package conventionally takes the name schema and imports this
// package for the building blocks:
//
//	package schema
//
//	import "github.com/aita/sqlkit/sqldef"
//
//	var Catalog = sqldef.NewCatalog()
//
//	var Users = Catalog.Table("users",
//		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
//		sqldef.Column("email", sqldef.VarChar(255), sqldef.Unique()),
//		sqldef.Column("name", sqldef.Text, sqldef.Nullable()),
//		sqldef.Column("created_at", sqldef.Timestamp, sqldef.Default("now()")),
//	)
//
//	var Posts = Catalog.Table("posts",
//		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
//		sqldef.Column("user_id", sqldef.BigInt, sqldef.References(Users, "id")),
//		sqldef.Column("title", sqldef.Text),
//		sqldef.Index("user_id"),
//		sqldef.Check("title <> ''"),
//	)
//
// A small generator program turns the catalog into Go source and DDL:
//
//	files, err := schema.Catalog.Generate(codegen.Options{PackageName: "appdb"})
//	if err != nil { ... }
//	err = sqldef.Write("appdb", files)
//	sql, err := schema.Catalog.DDL("postgres")
//
// Tables declared with Catalog.Table live in the unnamed default schema and
// generate unqualified names; Catalog.Schema("public").Table(...) qualifies
// them. Columns are NOT NULL unless marked Nullable. Declarations are plain
// values, so they compose: a shared []sqldef.TableElement (say, audit
// columns) can be appended into any table, and mistakes — duplicate names,
// dangling foreign keys — are collected and reported by Build, Generate, and
// DDL.
package sqldef

import "fmt"

// Catalog is the root a design hangs off. Create one per design package and
// declare tables on it; the generators consume the whole catalog.
type Catalog struct {
	schemas []*Schema
	errs    []error
}

// NewCatalog returns an empty catalog.
func NewCatalog() *Catalog { return &Catalog{} }

// Schema returns the named schema (PostgreSQL schema / MySQL database),
// creating it on first use.
func (c *Catalog) Schema(name string) *Schema {
	for _, s := range c.schemas {
		if s.name == name {
			return s
		}
	}
	s := &Schema{catalog: c, name: name}
	c.schemas = append(c.schemas, s)
	return s
}

// Table declares a table in the catalog's unnamed default schema. Use
// Catalog.Schema("public").Table(...) to qualify tables with a schema name.
func (c *Catalog) Table(name string, elements ...TableElement) *Table {
	return c.Schema("").Table(name, elements...)
}

func (c *Catalog) fail(format string, args ...any) {
	c.errs = append(c.errs, fmt.Errorf(format, args...))
}

// Schema is a named schema within a catalog.
type Schema struct {
	catalog *Catalog
	name    string
	tables  []*Table
}

// Name returns the schema name ("" for the default schema).
func (s *Schema) Name() string { return s.name }

// Table declares a table in the schema and returns it; the returned value is
// what foreign keys in other tables reference.
func (s *Schema) Table(name string, elements ...TableElement) *Table {
	t := &Table{schema: s, name: name}
	if name == "" {
		s.catalog.fail("Table requires a name")
		return t
	}
	for _, existing := range s.tables {
		if existing.name == name {
			s.catalog.fail("table %q is declared twice in schema %q", name, s.name)
		}
	}
	s.tables = append(s.tables, t)
	for _, element := range elements {
		element.applyTable(t)
	}
	return t
}

// Table is a declared table. Its value is the reusable handle: pass it to
// References/ForeignKey in other declarations.
type Table struct {
	schema      *Schema
	name        string
	comment     string
	columns     []*columnDef
	foreignKeys []fkDef
	indexes     []indexDef
	checks      []string
}

// Name returns the table name.
func (t *Table) Name() string { return t.name }

// Schema returns the schema the table was declared in.
func (t *Table) Schema() *Schema { return t.schema }

// ForeignKey declares a (possibly composite) foreign-key constraint after the
// fact and returns the table for chaining. It is the way to declare a
// self-referencing key, which cannot mention the table's own variable inside
// its declaration:
//
//	var Employees = Catalog.Table("employees", ...)
//	var _ = Employees.ForeignKey([]string{"manager_id"}, Employees, []string{"id"})
func (t *Table) ForeignKey(columns []string, ref *Table, refColumns []string) *Table {
	t.foreignKeys = append(t.foreignKeys, fkDef{columns: columns, ref: ref, refColumns: refColumns})
	return t
}

func (t *Table) fail(format string, args ...any) {
	args = append([]any{t.name}, args...)
	t.schema.catalog.fail("table %q: "+format, args...)
}

type columnDef struct {
	name        string
	typ         DataType
	comment     string
	nullable    bool
	hasDefault  bool
	defaultExpr string
	primaryKey  bool
	unique      bool
	ref         *fkDef
	errs        []error
}

// fkDef is a foreign key; column-level References produce single-column ones.
type fkDef struct {
	columns    []string
	ref        *Table
	refColumns []string
}

// indexDef is an Index or UniqueIndex declaration.
type indexDef struct {
	columns []string
	unique  bool
}

// TableElement is a table declaration item: Column, Index, UniqueIndex,
// Check, TableComment, and ForeignKey produce them.
type TableElement interface {
	applyTable(t *Table)
}

type tableElement func(*Table)

func (f tableElement) applyTable(t *Table) { f(t) }

// Column declares a column. Columns are NOT NULL by default; apply options to
// mark them Nullable, PrimaryKey, Default(ed), Unique, commented, or as a
// foreign key (References).
func Column(name string, typ DataType, opts ...ColumnOption) TableElement {
	return tableElement(func(t *Table) {
		if name == "" {
			t.fail("Column requires a name")
			return
		}
		for _, existing := range t.columns {
			if existing.name == name {
				t.fail("column %q is declared twice", name)
			}
		}
		c := &columnDef{name: name, typ: typ}
		for _, opt := range opts {
			opt(c)
		}
		for _, err := range c.errs {
			t.fail("column %q: %v", name, err)
		}
		c.errs = nil
		t.columns = append(t.columns, c)
	})
}

// ForeignKey declares a (possibly composite) foreign-key constraint: the
// local columns reference refColumns of the ref table. For the common
// single-column case the References column option is more convenient; for a
// self-referencing key use the Table.ForeignKey method.
func ForeignKey(columns []string, ref *Table, refColumns []string) TableElement {
	return tableElement(func(t *Table) {
		t.foreignKeys = append(t.foreignKeys, fkDef{columns: columns, ref: ref, refColumns: refColumns})
	})
}

// Index declares a secondary index over the named columns, named
// idx_<table>_<columns>.
func Index(columns ...string) TableElement {
	return tableElement(func(t *Table) {
		t.indexes = append(t.indexes, indexDef{columns: columns})
	})
}

// UniqueIndex declares a unique index (or UNIQUE constraint) over the named
// columns, named uq_<table>_<columns>. For a single column the Unique column
// option is more convenient.
func UniqueIndex(columns ...string) TableElement {
	return tableElement(func(t *Table) {
		t.indexes = append(t.indexes, indexDef{columns: columns, unique: true})
	})
}

// Check declares a CHECK constraint. expr is the bare boolean expression,
// e.g. "price > 0". Constraints are named ck_<table>_<n> in declaration
// order.
func Check(expr string) TableElement {
	return tableElement(func(t *Table) {
		t.checks = append(t.checks, expr)
	})
}

// TableComment sets the table's comment. Column comments use the Comment
// option instead.
func TableComment(text string) TableElement {
	return tableElement(func(t *Table) {
		t.comment = text
	})
}

// ColumnOption customizes a Column declaration.
type ColumnOption func(*columnDef)

// PrimaryKey marks the column as (part of) the table's primary key. Mark
// several columns to declare a composite key.
func PrimaryKey() ColumnOption {
	return func(c *columnDef) { c.primaryKey = true }
}

// Nullable allows NULL values; the code generator maps the column to a
// pointer type.
func Nullable() ColumnOption {
	return func(c *columnDef) { c.nullable = true }
}

// Default marks the column as having a database-side default, so generated
// bindings report it in Defaulted and inserts can omit it. The optional
// expression is used by the DDL generator: Default("now()") renders as
// DEFAULT now(), while a bare Default() on an integer primary key renders as
// a serial/auto-increment column.
func Default(expr ...string) ColumnOption {
	return func(c *columnDef) {
		c.hasDefault = true
		switch len(expr) {
		case 0:
		case 1:
			c.defaultExpr = expr[0]
		default:
			c.errs = append(c.errs, fmt.Errorf("Default takes at most one expression"))
		}
	}
}

// Unique declares a single-column unique index (named uq_<table>_<column>) on
// the column. Multi-column uniqueness uses the table-level UniqueIndex.
func Unique() ColumnOption {
	return func(c *columnDef) { c.unique = true }
}

// Comment sets the column's comment; the code generator carries it into the
// generated bindings as a doc comment. Table comments use the table-level
// TableComment.
func Comment(text string) ColumnOption {
	return func(c *columnDef) { c.comment = text }
}

// References declares the column a foreign key to column refColumn of the ref
// table, which may live in any schema of the same catalog. Composite keys use
// the table-level ForeignKey instead.
func References(ref *Table, refColumn string) ColumnOption {
	return func(c *columnDef) {
		if c.ref != nil {
			c.errs = append(c.errs, fmt.Errorf("References is declared twice"))
			return
		}
		c.ref = &fkDef{ref: ref, refColumns: []string{refColumn}}
	}
}
