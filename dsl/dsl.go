// Package dsl declares database schemas as Go code, goa-style: a design is a
// tree of nested function calls evaluated when the enclosing variable is
// initialized, producing the schema model that sqlkit's code generator
// consumes. The same design that generates typed table bindings today can
// feed future tooling (e.g. a migration generator) without changes.
//
// A design lives in an ordinary Go package that dot-imports the dsl,
// goa-style, so the schema reads as bare calls:
//
//	package design
//
//	import . "github.com/aita/sqlkit/dsl"
//
//	var Blog = Schema("public", func() {
//		Table("users", func() {
//			Column("id", BigInt, PrimaryKey(), Default())
//			Column("email", VarChar(255))
//			Column("name", Text, Nullable())
//			Column("created_at", Timestamp, Default())
//		})
//		Table("posts", func() {
//			Column("id", BigInt, PrimaryKey(), Default())
//			Column("user_id", BigInt, References("users", "id"))
//			Column("title", VarChar(200))
//		})
//	})
//
// and a small generator program turns it into Go source:
//
//	files, err := design.Blog.Generate(codegen.Options{PackageName: "appdb"})
//	if err != nil { ... }
//	err = dsl.Write("appdb", files)
//
// Columns are NOT NULL unless marked Nullable. Structurally misplaced calls
// (Table outside Schema, Column outside Table) panic immediately, surfacing
// the broken design at package-init time; semantic problems (duplicate names,
// dangling foreign keys) are collected and reported by Build/Generate.
package dsl

import "fmt"

// SchemaExpr is an evaluated schema design. Build resolves it into the
// schema model; Generate additionally runs the code generator on it.
type SchemaExpr struct {
	name   string
	tables []*tableExpr
	errs   []error
}

type tableExpr struct {
	name        string
	columns     []*columnExpr
	foreignKeys []fkExpr
}

type columnExpr struct {
	name       string
	typ        DataType
	nullable   bool
	hasDefault bool
	primaryKey bool
	ref        *fkRef
	errs       []error
}

// fkRef is a single-column foreign key declared with the References option.
type fkRef struct {
	table   string
	columns []string
}

// fkExpr is a (possibly composite) foreign key declared with ForeignKey.
type fkExpr struct {
	columns    []string
	refTable   string
	refColumns []string
}

// The DSL is evaluated through package-level state: Schema installs itself as
// the current evaluation context, runs its body, and the nested calls attach
// to it. Designs are meant to be evaluated during package initialization,
// which Go runs on a single goroutine; concurrent evaluation is not
// supported.
var (
	curSchema *SchemaExpr
	curTable  *tableExpr
)

// Schema declares a database schema (PostgreSQL schema / MySQL database) and
// evaluates fn, in which Table calls declare its tables. The name may be
// empty to leave generated table identities unqualified.
func Schema(name string, fn func()) *SchemaExpr {
	if curSchema != nil {
		panic("dsl: Schema cannot be nested inside Schema")
	}
	s := &SchemaExpr{name: name}
	curSchema = s
	defer func() { curSchema = nil }()
	if fn != nil {
		fn()
	}
	return s
}

// Table declares a table inside Schema and evaluates fn, in which Column and
// ForeignKey calls declare its contents.
func Table(name string, fn func()) {
	if curSchema == nil {
		panic("dsl: Table must be called inside Schema")
	}
	if curTable != nil {
		panic("dsl: Table cannot be nested inside Table")
	}
	if name == "" {
		curSchema.fail("Table requires a name")
		return
	}
	for _, t := range curSchema.tables {
		if t.name == name {
			curSchema.fail("table %q is declared twice", name)
		}
	}
	t := &tableExpr{name: name}
	curSchema.tables = append(curSchema.tables, t)
	curTable = t
	defer func() { curTable = nil }()
	if fn != nil {
		fn()
	}
}

// Column declares a column inside Table. Columns are NOT NULL by default;
// apply options to mark them Nullable, PrimaryKey, Default(ed), or as a
// foreign key (References).
func Column(name string, typ DataType, opts ...ColumnOption) {
	if curTable == nil {
		panic("dsl: Column must be called inside Table")
	}
	if name == "" {
		curSchema.fail("table %q: Column requires a name", curTable.name)
		return
	}
	for _, c := range curTable.columns {
		if c.name == name {
			curSchema.fail("table %q: column %q is declared twice", curTable.name, name)
		}
	}
	c := &columnExpr{name: name, typ: typ}
	for _, opt := range opts {
		opt(c)
	}
	for _, err := range c.errs {
		curSchema.fail("table %q: column %q: %v", curTable.name, name, err)
	}
	c.errs = nil
	curTable.columns = append(curTable.columns, c)
}

// ForeignKey declares a (possibly composite) foreign-key constraint inside
// Table: the local columns reference refColumns of refTable, declared in the
// same schema. For the common single-column case the References column option
// is more convenient.
func ForeignKey(columns []string, refTable string, refColumns []string) {
	if curTable == nil {
		panic("dsl: ForeignKey must be called inside Table")
	}
	curTable.foreignKeys = append(curTable.foreignKeys, fkExpr{
		columns:    columns,
		refTable:   refTable,
		refColumns: refColumns,
	})
}

func (s *SchemaExpr) fail(format string, args ...any) {
	s.errs = append(s.errs, fmt.Errorf(format, args...))
}

// ColumnOption customizes a Column declaration.
type ColumnOption func(*columnExpr)

// PrimaryKey marks the column as (part of) the table's primary key. Mark
// several columns to declare a composite key.
func PrimaryKey() ColumnOption {
	return func(c *columnExpr) { c.primaryKey = true }
}

// Nullable allows NULL values; the code generator maps the column to a
// pointer type.
func Nullable() ColumnOption {
	return func(c *columnExpr) { c.nullable = true }
}

// Default marks the column as having a database-side default (serial,
// DEFAULT now(), ...), so generated bindings report it in Defaulted and
// inserts can omit it.
func Default() ColumnOption {
	return func(c *columnExpr) { c.hasDefault = true }
}

// References declares the column a foreign key to column refColumn of
// refTable in the same schema. Composite keys use the table-level ForeignKey
// instead.
func References(refTable, refColumn string) ColumnOption {
	return func(c *columnExpr) {
		if c.ref != nil {
			c.errs = append(c.errs, fmt.Errorf("References is declared twice"))
			return
		}
		c.ref = &fkRef{table: refTable, columns: []string{refColumn}}
	}
}
