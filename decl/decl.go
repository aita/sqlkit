// Package decl declares database schemas as plain Go values that build
// directly into the meta model. A catalog (decl.NewCatalog) is the reusable
// root: each Table call assembles and returns a meta.Table immediately. Foreign
// keys name their target table (References("users", "id")) and are resolved
// when the catalog Builds, so tables may be declared in any order, reference
// themselves, and be split across files or packages. Build assembles the
// meta.Metadata that every generator — the typed table bindings (codegen) and
// the DDL (meta.Metadata.CreateSQL) — consumes.
//
// A catalog package conventionally takes the name schema and imports this
// package for the building blocks:
//
//	package schema
//
//	import (
//		"github.com/aita/sqlkit/sql"
//		"github.com/aita/sqlkit/decl"
//	)
//
//	var Catalog = decl.NewCatalog()
//
//	var Users = Catalog.Table("users",
//		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
//		decl.Column("email", decl.VarChar(255), decl.Unique()),
//		decl.Column("name", decl.Text, decl.Nullable()),
//		decl.Column("created_at", decl.Timestamp, decl.Default(sql.Raw("now()"))),
//	)
//
//	var Posts = Catalog.Table("posts",
//		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
//		decl.Column("user_id", decl.BigInt, decl.References("users", "id")),
//		decl.Column("title", decl.Text),
//		decl.Index("user_id"),
//		decl.Check(sql.Raw("title <> ''")),
//	)
//
// Catalog.Table returns a meta.Table, so Users and Posts are meta.Table
// values. Column types are portable (meta.Type carries both dialects), so the
// table is dialect-agnostic; the generators resolve the type when you choose a
// dialect. decl is purely the schema DSL: Build resolves foreign keys,
// validates, and assembles the meta.Metadata the generators consume. A small
// generator program turns the catalog into Go source and DDL:
//
//	md, err := schema.Catalog.Build()
//	if err != nil { ... }
//	files, err := codegen.Generate(md, codegen.Options{PackageName: "appdb"})
//	err = codegen.Write("appdb", files)
//	d, err := sql.DialectByName("postgres")
//	sqlText, err := md.CreateSQL(d)
//
// Tables declared with Catalog.Table live in the unnamed default schema and
// generate unqualified names (use this for schemaless engines like MySQL);
// Catalog.Schema("public").Table(...) qualifies them. Columns are NOT
// NULL unless marked Nullable. Mistakes — duplicate names, dangling foreign
// keys — are collected and reported by Build.
package decl

import (
	"fmt"
	"strings"

	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

// Catalog is the root the tables hang off: it accumulates the
// meta.Tables declared on it, plus any declaration errors, and Builds them
// into a meta.Metadata. Create one with NewCatalog.
type Catalog struct {
	defaultSchema string          // schema Catalog.Table declares into ("" = unnamed)
	schemaNames   []string        // distinct schema names in first-seen order
	createSchemas map[string]bool // schema name -> managed by migrations (CREATE/DROP SCHEMA)
	tables        []meta.Table
	tableIndex    map[string]int    // schema\x00name -> index into tables, for FK resolution
	bindingNames  map[string]string // schema\x00name -> binding identifier override (decl.BindingName)
	pendingFKs    []pendingFK       // foreign keys awaiting resolution at Build time
	extensions    []meta.Extension
	enums         []meta.Enum
	sequences     []meta.Sequence
	matviews      []meta.MaterializedView
	grants        []meta.Grant
	objectNames   map[string]bool // schema\x00kind\x00name -> declared, for duplicate detection
	errs          []error
}

// pendingFK is a foreign key collected while a table is declared but resolved
// only at Build, once every table is registered. Table is the index into
// Catalog.tables of the table the key belongs to.
type pendingFK struct {
	table int
	def   fkDef
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
// point of the DSL: var Catalog = decl.NewCatalog(). Options configure
// catalog-wide defaults such as DefaultSchema.
func NewCatalog(opts ...CatalogOption) *Catalog {
	c := &Catalog{tableIndex: map[string]int{}, createSchemas: map[string]bool{}, bindingNames: map[string]string{}, objectNames: map[string]bool{}}
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
//
// By default the schema is assumed to already exist and is not managed by
// sqlkit. Pass Create to put it under migration management: the generated
// CreateSQL emits a CREATE SCHEMA IF NOT EXISTS for it and DropSQL a DROP
// SCHEMA. sqlkit never creates the default schema (Catalog.Table's schema),
// which is assumed to exist; mark additional schemas explicitly.
func (d *Catalog) Schema(name string, opts ...SchemaOption) *Schema {
	cfg := schemaConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.create && name != "" {
		d.createSchemas[name] = true
	}
	return &Schema{catalog: d, name: name}
}

// SchemaOption configures a schema declared with Catalog.Schema.
type SchemaOption func(*schemaConfig)

type schemaConfig struct {
	create bool
}

// Create marks the schema for migration management, so CreateSQL emits a CREATE
// SCHEMA IF NOT EXISTS for it (and DropSQL a DROP SCHEMA). Without it a schema
// is assumed to already exist and is left alone:
//
//	Catalog.Schema("audit", decl.Create()).Table("log", ...)
func Create() SchemaOption {
	return func(c *schemaConfig) { c.create = true }
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

// Table declares a table in the schema and returns the assembled meta.Table.
// Foreign keys it declares are resolved later, by Build, against every table in
// the catalog — so a table may reference one declared after it, or itself.
func (s *Schema) Table(name string, elements ...TableElement) meta.Table {
	d := s.catalog
	b := &tableBuilder{catalog: d, schema: s.name, name: name}
	key := tableKey(s.name, name)
	if name == "" {
		d.fail("Table requires a name")
	} else if _, dup := d.tableIndex[key]; dup {
		d.fail("table %q is declared twice in schema %q", name, s.name)
	}
	for _, element := range elements {
		element.applyTable(b)
	}
	table := b.build()
	idx := len(d.tables)
	d.tables = append(d.tables, table)
	if name != "" {
		if _, exists := d.tableIndex[key]; !exists {
			d.schemaNamesAdd(s.name)
			d.tableIndex[key] = idx
		}
		// A binding-name override is a codegen hint, not part of the schema, so it
		// lives on the catalog (bridged to codegen via BindingNames) rather than on
		// the returned meta.Table.
		if b.bindingName != "" {
			d.bindingNames[key] = b.bindingName
		}
	}
	// Foreign keys reference tables by name, which may not be declared yet, so
	// queue them for Build to resolve once the whole catalog is known.
	for _, fk := range collectForeignKeys(b) {
		d.pendingFKs = append(d.pendingFKs, pendingFK{table: idx, def: fk})
	}
	return table
}

// BindingNames returns a codegen.Options.BindingName hook that maps each table
// to the identifier declared for it with BindingName, and the empty string for
// tables without one (so codegen falls back to its default derivation). It
// returns nil when no table in the catalog declares a BindingName, so callers
// can assign it unconditionally:
//
//	codegen.Generate(md, codegen.Options{
//		PackageName: "appdb",
//		BindingName: catalog.BindingNames(),
//	})
func (d *Catalog) BindingNames() func(meta.Table) string {
	if len(d.bindingNames) == 0 {
		return nil
	}
	overrides := make(map[string]string, len(d.bindingNames))
	for k, v := range d.bindingNames {
		overrides[k] = v
	}
	return func(table meta.Table) string {
		return overrides[tableKey(table.Schema.Name, table.Name)]
	}
}

func (d *Catalog) schemaNamesAdd(name string) {
	for _, n := range d.schemaNames {
		if n == name {
			return
		}
	}
	d.schemaNames = append(d.schemaNames, name)
}

// tableBuilder is the mutable scratch a Table's elements write into before it
// is finalized into a meta.Table.
type tableBuilder struct {
	catalog     *Catalog
	schema      string
	name        string
	bindingName string
	comment     string
	columns     []*columnDef
	foreignKeys []fkDef
	indexes     []indexDef
	checks      []sql.Expression
}

func (b *tableBuilder) fail(format string, args ...any) {
	b.catalog.fail("table %q: "+format, append([]any{b.name}, args...)...)
}

// build finalizes the scratch into a meta.Table, validating and assigning
// constraint names. Type resolution is deferred to the generators.
func (b *tableBuilder) build() meta.Table {
	d := b.catalog
	mt := meta.Table{
		Schema:  meta.Schema{Name: b.schema, Create: d.createSchemas[b.schema]},
		Name:    b.name,
		Comment: b.comment,
	}
	if b.name != "" && len(b.columns) == 0 {
		d.fail("table %q declares no columns", b.name)
	}
	for _, c := range b.columns {
		if c.primaryKey && c.nullable {
			d.fail("table %q: column %q: a primary key column cannot be nullable", b.name, c.name)
		}
		mt.Columns = append(mt.Columns, c.toMeta())
	}

	// Foreign keys are not resolved here: Build collects them across the whole
	// catalog so a key may target a table declared later, or the table itself.

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
		if isBlankExpr(expr) {
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
	defaultExpr sql.Expression
	primaryKey  bool
	unique      bool
	ref         *fkDef
	errs        []error
}

// toMeta assembles the column's meta.Column. Column-level Unique/References are
// table-level constructs (an index, a foreign key) and are not part of the
// column itself, so they are left to the table builder.
func (c *columnDef) toMeta() meta.Column {
	return meta.Column{
		Name:        c.name,
		Type:        c.typ,
		Nullable:    c.nullable,
		HasDefault:  c.hasDefault,
		DefaultExpr: c.defaultExpr,
		PrimaryKey:  c.primaryKey,
		Comment:     c.comment,
	}
}

// fkDef is a foreign key; column-level References produce single-column ones.
// The target table is named (refTable, optionally schema-qualified via
// refSchema/refQualified) and resolved against the catalog at Build.
type fkDef struct {
	columns      []string
	refSchema    string // explicit target schema; used only when refQualified
	refTable     string
	refQualified bool // target schema was given explicitly ("schema.table")
	refColumns   []string
	onDelete     meta.ReferentialAction
	onUpdate     meta.ReferentialAction
}

// splitRef parses a foreign-key target. A "schema.table" string qualifies the
// schema explicitly; a bare "table" resolves in the referencing table's own
// schema at Build.
func splitRef(ref string) (schema, table string, qualified bool) {
	if i := strings.IndexByte(ref, '.'); i >= 0 {
		return ref[:i], ref[i+1:], true
	}
	return "", ref, false
}

// ReferentialAction is a foreign-key ON DELETE / ON UPDATE action, re-exported
// from the meta model so a catalog declares them without importing meta.
type ReferentialAction = meta.ReferentialAction

// The referential actions a foreign key may take, for use with OnDelete/OnUpdate.
const (
	NoAction   = meta.NoAction
	Restrict   = meta.Restrict
	Cascade    = meta.Cascade
	SetNull    = meta.SetNull
	SetDefault = meta.SetDefault
)

// FKOption customizes a foreign key declared with References or ForeignKey.
type FKOption func(*fkDef)

// OnDelete sets the foreign key's ON DELETE referential action.
func OnDelete(action ReferentialAction) FKOption {
	return func(f *fkDef) { f.onDelete = action }
}

// OnUpdate sets the foreign key's ON UPDATE referential action.
func OnUpdate(action ReferentialAction) FKOption {
	return func(f *fkDef) { f.onUpdate = action }
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
// columns reference refColumns of refTable. refTable names a table in the
// catalog ("users", or "schema.users" to cross schemas), resolved at Build, so
// it may be declared after this one or be this table itself. For the common
// single-column case the References column option is more convenient.
func ForeignKey(columns []string, refTable string, refColumns []string, opts ...FKOption) TableElement {
	return tableElement(func(b *tableBuilder) {
		schema, table, qualified := splitRef(refTable)
		fk := fkDef{columns: columns, refSchema: schema, refTable: table, refQualified: qualified, refColumns: refColumns}
		for _, opt := range opts {
			opt(&fk)
		}
		b.foreignKeys = append(b.foreignKeys, fk)
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
// sql.Raw("price > 0") or sql.Gt(sql.Col("price"), sql.Raw("0")). Constraints
// are named ck_<table>_<n> in declaration order. The expression must not bind
// values; wrap literals in sql.Raw to inline them.
func Check(expr sql.Expression) TableElement {
	return tableElement(func(b *tableBuilder) {
		b.checks = append(b.checks, expr)
	})
}

// isBlankExpr reports whether a declared default/check expression is missing —
// nil, or a Raw fragment that is empty or whitespace.
func isBlankExpr(expr sql.Expression) bool {
	if expr == nil {
		return true
	}
	if raw, ok := expr.(sql.RawExpression); ok {
		return strings.TrimSpace(string(raw)) == ""
	}
	return false
}

// TableComment sets the table's comment. Column comments use the Comment
// option instead.
func TableComment(text string) TableElement {
	return tableElement(func(b *tableBuilder) {
		b.comment = text
	})
}

// BindingName overrides the base Go identifier the code generator derives for
// the table's binding. By default the binding name comes from the table name
// (table "users" -> var Users, type UsersTable, …); set this to choose another,
// e.g. when the table name maps to an awkward identifier:
//
//	var People = Catalog.Table("tbl_person",
//		decl.BindingName("Person"),
//		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
//		// ...
//	)
//
// The name backs every binding identifier (the package var, table type, and—in
// the relation-aware layout—the columns type, its constructor, the relations
// type, and the filter bundle). It must be a valid, exported Go identifier and
// distinct across the generated tables. The override is a codegen hint kept on
// the catalog, not the schema model; wire it into generation with
// codegen.Options.BindingName: Catalog.BindingNames().
func BindingName(name string) TableElement {
	return tableElement(func(b *tableBuilder) {
		if name == "" {
			b.fail("BindingName requires a name")
			return
		}
		b.bindingName = name
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
// expression is used by the DDL generator: Default(sql.Raw("now()")) renders as
// DEFAULT now(), while a bare Default() on an integer column renders as a
// serial/auto-increment column — whether or not it is the primary key. A blank
// expression (nil or an empty sql.Raw) is treated as absent, equivalent to a
// bare Default(). The expression must not bind values; wrap literals in
// sql.Raw to inline them.
func Default(expr ...sql.Expression) ColumnOption {
	return func(c *columnDef) {
		c.hasDefault = true
		switch len(expr) {
		case 0:
		case 1:
			if !isBlankExpr(expr[0]) {
				c.defaultExpr = expr[0]
			}
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

// References declares the column a foreign key to column refColumn of refTable.
// refTable names a table in the catalog ("users", or "schema.users" to cross
// schemas), resolved at Build, so it may be declared after this one or be this
// table itself (a self-reference). Composite keys use the table-level
// ForeignKey instead.
func References(refTable string, refColumn string, opts ...FKOption) ColumnOption {
	return func(c *columnDef) {
		if c.ref != nil {
			c.errs = append(c.errs, fmt.Errorf("References is declared twice"))
			return
		}
		schema, table, qualified := splitRef(refTable)
		fk := &fkDef{refSchema: schema, refTable: table, refQualified: qualified, refColumns: []string{refColumn}}
		for _, opt := range opts {
			opt(fk)
		}
		c.ref = fk
	}
}
