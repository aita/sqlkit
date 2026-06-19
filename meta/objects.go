package meta

import "github.com/aita/sqlkit/sql"

// This file extends the schema model beyond tables with the non-table objects a
// catalog can declare: extensions, enum types, and sequences (PostgreSQL). They
// are flat value types like Table, carried on Metadata and rendered by the same
// DDL path (each builds a sql-package DDL node). The default-schema convention
// matches tables: an empty Schema generates an unqualified name.

// Extension is a database extension (PostgreSQL CREATE EXTENSION). Schema, when
// set, is the WITH SCHEMA target the extension's objects are installed into; the
// extension name itself is never schema-qualified.
type Extension struct {
	Name   string
	Schema string
}

// CreateStatement builds the CREATE EXTENSION IF NOT EXISTS DDL node, so creating
// the schema from scratch is idempotent.
func (e Extension) CreateStatement() sql.CreateExtensionStmt {
	s := sql.CreateExtension(e.Name).IfNotExists()
	if e.Schema != "" {
		s = s.Schema(e.Schema)
	}
	return s
}

// DropStatement builds the DROP EXTENSION IF EXISTS DDL node.
func (e Extension) DropStatement() sql.DropExtensionStmt {
	return sql.DropExtension(e.Name).IfExists()
}

// Enum is an enumerated type (PostgreSQL CREATE TYPE ... AS ENUM). Values are the
// labels, in order.
type Enum struct {
	Schema string
	Name   string
	Values []string
}

// QualifiedName returns the type name with an optional schema prefix.
func (e Enum) QualifiedName() string { return qualify(e.Schema, e.Name) }

// CreateStatement builds the CREATE TYPE ... AS ENUM DDL node.
func (e Enum) CreateStatement() sql.CreateEnumStmt {
	return sql.CreateEnum(e.QualifiedName(), e.Values...)
}

// DropStatement builds the DROP TYPE IF EXISTS DDL node.
func (e Enum) DropStatement() sql.DropTypeStmt {
	return sql.DropType(e.QualifiedName()).IfExists()
}

// Sequence is a database sequence (PostgreSQL CREATE SEQUENCE). The numeric
// options are pointers so an unset option takes the engine default rather than
// rendering a zero; OwnedBy is "" (unset), "NONE", or "table.column".
type Sequence struct {
	Schema    string
	Name      string
	Increment *int64
	Start     *int64
	MinValue  *int64
	MaxValue  *int64
	Cache     *int64
	Cycle     *bool
	OwnedBy   string
}

// QualifiedName returns the sequence name with an optional schema prefix.
func (s Sequence) QualifiedName() string { return qualify(s.Schema, s.Name) }

// CreateStatement builds the CREATE SEQUENCE DDL node, applying only the options
// the model carries.
func (s Sequence) CreateStatement() sql.CreateSequenceStmt {
	st := sql.CreateSequence(sql.Table{Schema: s.Schema, Name: s.Name})
	if s.Increment != nil {
		st = st.Increment(*s.Increment)
	}
	if s.MinValue != nil {
		st = st.MinValue(*s.MinValue)
	}
	if s.MaxValue != nil {
		st = st.MaxValue(*s.MaxValue)
	}
	if s.Start != nil {
		st = st.Start(*s.Start)
	}
	if s.Cache != nil {
		st = st.Cache(*s.Cache)
	}
	if s.Cycle != nil {
		if *s.Cycle {
			st = st.Cycle()
		} else {
			st = st.NoCycle()
		}
	}
	if s.OwnedBy != "" {
		st = st.OwnedBy(s.OwnedBy)
	}
	return st
}

// DropStatement builds the DROP SEQUENCE IF EXISTS DDL node.
func (s Sequence) DropStatement() sql.DropSequenceStmt {
	return sql.DropSequence(s.QualifiedName()).IfExists()
}

// qualify joins a schema and name into "schema.name", or just "name" for the
// unnamed default schema.
func qualify(schema, name string) string {
	if schema == "" {
		return name
	}
	return schema + "." + name
}
