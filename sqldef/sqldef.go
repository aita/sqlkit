// Package sqldef declares database schemas as plain Go values that build
// directly into the meta model. A catalog (sqldef.NewCatalog) is the reusable
// root: each Table call assembles and returns a meta.Table immediately, so
// foreign keys reference those values (References(Users, "id")), catalogs can
// be split across files or packages, and the catalog Builds into a
// meta.Metadata that every generator — the typed table bindings (codegen) and
// the DDL (meta.Metadata.CreateSQL) — consumes.
//
// A catalog package conventionally takes the name schema and imports this
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
// Catalog.Table returns a meta.Table, so Users and Posts are meta.Table
// values. Column types are portable (meta.Type carries both dialects), so the
// table is dialect-agnostic; the generators resolve the type when you choose a
// dialect. sqldef is purely the schema DSL: Build assembles the meta.Metadata
// and the generators consume it directly. A small generator program turns the
// catalog into Go source and DDL:
//
//	md, err := schema.Catalog.Build()
//	if err != nil { ... }
//	files, err := codegen.Generate(md, codegen.Options{PackageName: "appdb"})
//	err = codegen.Write("appdb", files)
//	d, err := sql.DialectByName("postgres")
//	sqlText, err := md.CreateSQL(d)
//
// Tables declared with Catalog.Table live in the unnamed default schema and
// generate unqualified names (use this for schemaless engines like MySQL or
// SQLite); Catalog.Schema("public").Table(...) qualifies them. Columns are NOT
// NULL unless marked Nullable. Mistakes — duplicate names, dangling foreign
// keys — are collected and reported by Build.
package sqldef

import (
	"fmt"

	"github.com/aita/sqlkit/meta"
)

// Catalog is the root the tables hang off: it accumulates the
// meta.Tables declared on it, plus any declaration errors, and Builds them
// into a meta.Metadata. Create one with NewCatalog.
type Catalog struct {
	defaultSchema string   // schema Catalog.Table declares into ("" = unnamed)
	schemaNames   []string // distinct schema names in first-seen order
	tables        []meta.Table
	registered    map[string]bool // schema\x00name -> declared, for FK target checks
	errs          []error
}

// CatalogOption configures a Catalog at construction.
type CatalogOption func(*Catalog)

// DefaultSchema sets the schema that Catalog.Table (and any table declared
// without an explicit Schema handle) lands in. Without it the default schema
// is unnamed, which generates unqualified names.
func DefaultSchema(name string) CatalogOption {
	return func(c *Catalog) { c.defaultSchema = name }
}

// NewCatalog returns an empty catalog to declare tables on. It is the entry
// point of the DSL: var Catalog = sqldef.NewCatalog(). Options configure
// catalog-wide defaults such as DefaultSchema.
func NewCatalog(opts ...CatalogOption) *Catalog {
	c := &Catalog{registered: map[string]bool{}}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func tableKey(schema, name string) string { return schema + "\x00" + name }

func (d *Catalog) fail(format string, args ...any) {
	d.errs = append(d.errs, fmt.Errorf(format, args...))
}

// Schema returns a handle for the named schema (PostgreSQL schema / MySQL
// database). Declaring tables through it qualifies them with the schema name.
func (d *Catalog) Schema(name string) *Schema {
	return &Schema{catalog: d, name: name}
}

// Table declares a table in the catalog's default schema (unnamed unless set
// with DefaultSchema) and returns the assembled meta.Table. Use
// Catalog.Schema("other").Table(...) to place a table in a different schema.
func (d *Catalog) Table(name string, elements ...TableElement) meta.Table {
	return d.Schema(d.defaultSchema).Table(name, elements...)
}

// Schema is a handle for declaring tables in a named schema of a catalog.
type Schema struct {
	catalog *Catalog
	name    string
}

// Name returns the schema name ("" for the default schema).
func (s *Schema) Name() string { return s.name }

// Table declares a table in the schema and returns the assembled meta.Table —
// the value foreign keys in other tables reference.
func (s *Schema) Table(name string, elements ...TableElement) meta.Table {
	d := s.catalog
	b := &tableBuilder{catalog: d, schema: s.name, name: name}
	if name == "" {
		d.fail("Table requires a name")
	} else if d.registered[tableKey(s.name, name)] {
		d.fail("table %q is declared twice in schema %q", name, s.name)
	}
	for _, element := range elements {
		element.applyTable(b)
	}
	table := b.build()
	if name != "" {
		if !d.registered[tableKey(s.name, name)] {
			d.schemaNamesAdd(s.name)
		}
		d.registered[tableKey(s.name, name)] = true
	}
	d.tables = append(d.tables, table)
	return table
}

func (d *Catalog) schemaNamesAdd(name string) {
	for _, n := range d.schemaNames {
		if n == name {
			return
		}
	}
	d.schemaNames = append(d.schemaNames, name)
}

// AddForeignKey declares a (possibly self-referencing) foreign key on an
// already-declared table, returning the updated table. It is the way to
// declare a self-reference, which cannot mention the table's own variable
// inside its Table(...) call:
//
//	var Employees = Catalog.Table("employees", ...)
//	var _ = Catalog.AddForeignKey(Employees, []string{"manager_id"}, Employees, []string{"id"})
func (d *Catalog) AddForeignKey(table meta.Table, columns []string, ref meta.Table, refColumns []string) meta.Table {
	key := tableKey(table.Schema.Name, table.Name)
	for i := range d.tables {
		t := &d.tables[i]
		if tableKey(t.Schema.Name, t.Name) != key {
			continue
		}
		if fk, ok := d.resolveFK(t.Name, t.Columns, fkDef{columns: columns, ref: ref, refColumns: refColumns}); ok {
			t.ForeignKeys = append(t.ForeignKeys, fk)
		}
		return *t
	}
	d.fail("AddForeignKey: table %q is not declared in this catalog", table.Name)
	return table
}

// tableBuilder is the mutable scratch a Table's elements write into before it
// is finalized into a meta.Table.
type tableBuilder struct {
	catalog     *Catalog
	schema      string
	name        string
	comment     string
	columns     []*columnDef
	foreignKeys []fkDef
	indexes     []indexDef
	checks      []string
}

func (b *tableBuilder) fail(format string, args ...any) {
	b.catalog.fail("table %q: "+format, append([]any{b.name}, args...)...)
}

// build finalizes the scratch into a meta.Table, validating and assigning
// constraint names. Type resolution is deferred to the generators.
func (b *tableBuilder) build() meta.Table {
	d := b.catalog
	mt := meta.Table{Schema: meta.Schema{Name: b.schema}, Name: b.name, Comment: b.comment}
	if b.name != "" && len(b.columns) == 0 {
		d.fail("table %q declares no columns", b.name)
	}
	for _, c := range b.columns {
		if c.primaryKey && c.nullable {
			d.fail("table %q: column %q: a primary key column cannot be nullable", b.name, c.name)
		}
		mt.Columns = append(mt.Columns, meta.Column{
			Name:        c.name,
			Type:        c.typ,
			Nullable:    c.nullable,
			HasDefault:  c.hasDefault,
			DefaultExpr: c.defaultExpr,
			PrimaryKey:  c.primaryKey,
			Comment:     c.comment,
		})
	}

	for _, fk := range collectForeignKeys(b) {
		if resolved, ok := d.resolveFK(b.name, mt.Columns, fk); ok {
			mt.ForeignKeys = append(mt.ForeignKeys, resolved)
		}
	}

	indexNames := map[string]bool{}
	for _, ix := range collectIndexes(b) {
		if len(ix.columns) == 0 {
			d.fail("table %q: index declares no columns", b.name)
			continue
		}
		ok := true
		for _, name := range ix.columns {
			if !hasMetaColumn(mt.Columns, name) {
				d.fail("table %q: index references unknown column %q", b.name, name)
				ok = false
			}
		}
		if !ok {
			continue
		}
		prefix := "idx"
		if ix.unique {
			prefix = "uq"
		}
		name := fmt.Sprintf("%s_%s_%s", prefix, b.name, joinCols(ix.columns))
		if indexNames[name] {
			d.fail("table %q: index %q is declared twice", b.name, name)
			continue
		}
		indexNames[name] = true
		mt.Indexes = append(mt.Indexes, meta.Index{Name: name, Columns: ix.columns, Unique: ix.unique})
	}

	for i, expr := range b.checks {
		if isBlank(expr) {
			d.fail("table %q: Check requires an expression", b.name)
			continue
		}
		mt.Checks = append(mt.Checks, meta.Check{
			Name:       fmt.Sprintf("ck_%s_%d", b.name, i+1),
			Expression: expr,
		})
	}
	return mt
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
	ref        meta.Table
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
	applyTable(b *tableBuilder)
}

type tableElement func(*tableBuilder)

func (f tableElement) applyTable(b *tableBuilder) { f(b) }

// Column declares a column. Columns are NOT NULL by default; apply options to
// mark them Nullable, PrimaryKey, Default(ed), Unique, commented, or as a
// foreign key (References).
func Column(name string, typ DataType, opts ...ColumnOption) TableElement {
	return tableElement(func(b *tableBuilder) {
		if name == "" {
			b.fail("Column requires a name")
			return
		}
		for _, existing := range b.columns {
			if existing.name == name {
				b.fail("column %q is declared twice", name)
			}
		}
		c := &columnDef{name: name, typ: typ}
		for _, opt := range opts {
			opt(c)
		}
		for _, err := range c.errs {
			b.fail("column %q: %v", name, err)
		}
		c.errs = nil
		b.columns = append(b.columns, c)
	})
}

// ForeignKey declares a (possibly composite) foreign-key constraint: the local
// columns reference refColumns of the ref table. For the common single-column
// case the References column option is more convenient; for a self-referencing
// key use Catalog.AddForeignKey.
func ForeignKey(columns []string, ref meta.Table, refColumns []string) TableElement {
	return tableElement(func(b *tableBuilder) {
		b.foreignKeys = append(b.foreignKeys, fkDef{columns: columns, ref: ref, refColumns: refColumns})
	})
}

// Index declares a secondary index over the named columns, named
// idx_<table>_<columns>.
func Index(columns ...string) TableElement {
	return tableElement(func(b *tableBuilder) {
		b.indexes = append(b.indexes, indexDef{columns: columns})
	})
}

// UniqueIndex declares a unique index (or UNIQUE constraint) over the named
// columns, named uq_<table>_<columns>. For a single column the Unique column
// option is more convenient.
func UniqueIndex(columns ...string) TableElement {
	return tableElement(func(b *tableBuilder) {
		b.indexes = append(b.indexes, indexDef{columns: columns, unique: true})
	})
}

// Check declares a CHECK constraint. expr is the bare boolean expression, e.g.
// "price > 0". Constraints are named ck_<table>_<n> in declaration order.
func Check(expr string) TableElement {
	return tableElement(func(b *tableBuilder) {
		b.checks = append(b.checks, expr)
	})
}

// TableComment sets the table's comment. Column comments use the Comment
// option instead.
func TableComment(text string) TableElement {
	return tableElement(func(b *tableBuilder) {
		b.comment = text
	})
}

// ColumnOption customizes a Column declaration.
type ColumnOption func(*columnDef)

// PrimaryKey marks the column as (part of) the table's primary key. Mark
// several columns to declare a composite key.
func PrimaryKey() ColumnOption {
	return func(c *columnDef) { c.primaryKey = true }
}

// Nullable allows NULL values; the code generator maps the column to a pointer
// type.
func Nullable() ColumnOption {
	return func(c *columnDef) { c.nullable = true }
}

// Default marks the column as having a database-side default, so generated
// bindings report it in Defaulted and inserts can omit it. The optional
// expression is used by the DDL generator: Default("now()") renders as DEFAULT
// now(), while a bare Default() on an integer primary key renders as a
// serial/auto-increment column.
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
// table. Composite keys use the table-level ForeignKey instead.
func References(ref meta.Table, refColumn string) ColumnOption {
	return func(c *columnDef) {
		if c.ref != nil {
			c.errs = append(c.errs, fmt.Errorf("References is declared twice"))
			return
		}
		c.ref = &fkDef{ref: ref, refColumns: []string{refColumn}}
	}
}
