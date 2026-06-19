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
	return s.Stmt()
}

// DropStatement builds the DROP EXTENSION IF EXISTS DDL node.
func (e Extension) DropStatement() sql.DropExtensionStmt {
	return sql.DropExtension(e.Name).IfExists().Stmt()
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
	return sql.DropType(e.QualifiedName()).IfExists().Stmt()
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
	return st.Stmt()
}

// DropStatement builds the DROP SEQUENCE IF EXISTS DDL node.
func (s Sequence) DropStatement() sql.DropSequenceStmt {
	return sql.DropSequence(s.QualifiedName()).IfExists().Stmt()
}

// Equal reports whether two enum types are identical (schema, name, and ordered
// labels), used by the diff engine to decide whether a type must be recreated.
func (e Enum) Equal(o Enum) bool {
	if e.Schema != o.Schema || e.Name != o.Name || len(e.Values) != len(o.Values) {
		return false
	}
	for i := range e.Values {
		if e.Values[i] != o.Values[i] {
			return false
		}
	}
	return true
}

// Equal reports whether two sequences are identical (schema, name, and every
// option), used by the diff engine to decide whether a sequence must be recreated.
func (s Sequence) Equal(o Sequence) bool {
	return s.Schema == o.Schema && s.Name == o.Name &&
		eqInt64Ptr(s.Increment, o.Increment) && eqInt64Ptr(s.Start, o.Start) &&
		eqInt64Ptr(s.MinValue, o.MinValue) && eqInt64Ptr(s.MaxValue, o.MaxValue) &&
		eqInt64Ptr(s.Cache, o.Cache) && eqBoolPtr(s.Cycle, o.Cycle) &&
		s.OwnedBy == o.OwnedBy
}

func eqInt64Ptr(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func eqBoolPtr(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// MaterializedView is a materialized view (PostgreSQL CREATE MATERIALIZED VIEW).
// Query is the view body — a sql.Query that compiles to plain SQL with no bind
// values. Columns, when set, names the view's columns explicitly; WithNoData
// creates it unpopulated.
type MaterializedView struct {
	Schema     string
	Name       string
	Query      sql.Query
	Columns    []string
	WithNoData bool
}

// sqlTable returns the sql-package table reference for the view.
func (v MaterializedView) sqlTable() sql.Table { return sql.Table{Schema: v.Schema, Name: v.Name} }

// QualifiedName returns the view name with an optional schema prefix.
func (v MaterializedView) QualifiedName() string { return qualify(v.Schema, v.Name) }

// CreateStatement builds the CREATE MATERIALIZED VIEW DDL node.
func (v MaterializedView) CreateStatement() sql.CreateMaterializedViewStmt {
	s := sql.CreateMaterializedView(v.sqlTable(), v.Query)
	if len(v.Columns) > 0 {
		s = s.Columns(v.Columns...)
	}
	if v.WithNoData {
		s = s.WithNoData()
	}
	return s.Stmt()
}

// DropStatement builds the DROP MATERIALIZED VIEW IF EXISTS DDL node.
func (v MaterializedView) DropStatement() sql.DropMaterializedViewStmt {
	return sql.DropMaterializedView(v.sqlTable()).IfExists().Stmt()
}

// Grant is a privilege grant (GRANT ... ON ... TO ...). An empty Privileges list
// means ALL PRIVILEGES; an empty ObjectType defaults to TABLE. CreateSQL emits
// the GRANT and DropSQL the matching REVOKE.
type Grant struct {
	Privileges      []string
	ObjectType      string
	Objects         []string
	Grantees        []string
	WithGrantOption bool
}

// CreateStatement builds the GRANT DDL node.
func (g Grant) CreateStatement() sql.GrantStmt {
	s := sql.Grant(g.Privileges...)
	if g.ObjectType != "" {
		s = s.OnType(g.ObjectType, g.Objects...)
	} else {
		s = s.On(g.Objects...)
	}
	s = s.To(g.Grantees...)
	if g.WithGrantOption {
		s = s.WithGrantOption()
	}
	return s.Stmt()
}

// DropStatement builds the REVOKE DDL node that undoes the grant.
func (g Grant) DropStatement() sql.RevokeStmt {
	s := sql.Revoke(g.Privileges...)
	if g.ObjectType != "" {
		s = s.OnType(g.ObjectType, g.Objects...)
	} else {
		s = s.On(g.Objects...)
	}
	return s.From(g.Grantees...).Stmt()
}

// Equal reports whether two grants are identical.
func (g Grant) Equal(o Grant) bool {
	return g.ObjectType == o.ObjectType && g.WithGrantOption == o.WithGrantOption &&
		stringSliceEqual(g.Privileges, o.Privileges) && stringSliceEqual(g.Objects, o.Objects) &&
		stringSliceEqual(g.Grantees, o.Grantees)
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// qualify joins a schema and name into "schema.name", or just "name" for the
// unnamed default schema.
func qualify(schema, name string) string {
	if schema == "" {
		return name
	}
	return schema + "." + name
}
