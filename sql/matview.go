package sql

import (
	"fmt"
	"strings"
)

// CreateMaterializedViewStmt is the AST for a CREATE MATERIALIZED VIEW statement
// (PostgreSQL). Like a view its body must compile to plain SQL with no bind
// values; inline literals with Raw. It is a DDL Statement.
type CreateMaterializedViewStmt struct {
	posField
	Table       Table
	Query       Query
	Columns     []Ident
	IfNotExists bool
	WithNoData  bool
}

// ToSQL compiles the CREATE MATERIALIZED VIEW statement.
func (s CreateMaterializedViewStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s CreateMaterializedViewStmt) compileDDL(c *Compiler) (string, error) {
	if s.Query == nil {
		return "", fmt.Errorf("sqlkit/sql: CREATE MATERIALIZED VIEW requires a query")
	}
	body, args, err := s.Query.ToSQL(c.Dialect)
	if err != nil {
		return "", err
	}
	if len(args) > 0 {
		return "", fmt.Errorf("sqlkit/sql: CREATE MATERIALIZED VIEW body must not bind values; use Raw to inline literals")
	}
	var sb strings.Builder
	sb.WriteString("CREATE MATERIALIZED VIEW ")
	if s.IfNotExists {
		sb.WriteString("IF NOT EXISTS ")
	}
	sb.WriteString(c.qualify(s.Table))
	if len(s.Columns) > 0 {
		sb.WriteString(" (" + c.quoteAll(s.Columns) + ")")
	}
	sb.WriteString(" AS\n")
	sb.WriteString(body)
	if s.WithNoData {
		sb.WriteString(" WITH NO DATA")
	}
	sb.WriteString(";")
	return sb.String(), nil
}
func (CreateMaterializedViewStmt) ddlStatement() {}

func (s CreateMaterializedViewStmt) setPos(pos int) Query { s.NodePos = pos; return s }

// CreateMaterializedViewBuilder is the fluent constructor for a
// CreateMaterializedViewStmt.
type CreateMaterializedViewBuilder struct{ s CreateMaterializedViewStmt }

// CreateMaterializedView starts a builder for a materialized view defined by
// query.
func CreateMaterializedView(table Table, query Query) CreateMaterializedViewBuilder {
	return CreateMaterializedViewBuilder{s: CreateMaterializedViewStmt{Table: table, Query: astOf(query)}}
}

// Stmt returns the built CREATE MATERIALIZED VIEW statement.
func (b CreateMaterializedViewBuilder) Stmt() CreateMaterializedViewStmt { return b.s }

// IfNotExists adds IF NOT EXISTS.
func (b CreateMaterializedViewBuilder) IfNotExists() CreateMaterializedViewBuilder {
	b.s.IfNotExists = true
	return b
}

// Columns sets explicit column names.
func (b CreateMaterializedViewBuilder) Columns(columns ...string) CreateMaterializedViewBuilder {
	b.s.Columns = idents(columns)
	return b
}

// WithNoData adds WITH NO DATA, creating the view unpopulated.
func (b CreateMaterializedViewBuilder) WithNoData() CreateMaterializedViewBuilder {
	b.s.WithNoData = true
	return b
}

// ToSQL compiles the building CREATE MATERIALIZED VIEW statement.
func (b CreateMaterializedViewBuilder) ToSQL(d Dialect) (string, []any, error) {
	return b.s.ToSQL(d)
}
func (b CreateMaterializedViewBuilder) buildStmt() Query { return b.s }
func (CreateMaterializedViewBuilder) ddlStatement()      {}

func (b CreateMaterializedViewBuilder) setPos(pos int) Query { b.s.NodePos = pos; return b }

// DropMaterializedViewStmt is the AST for a DROP MATERIALIZED VIEW statement.
type DropMaterializedViewStmt struct {
	posField
	Table    Table
	IfExists bool
	Cascade  bool
}

// ToSQL compiles the DROP MATERIALIZED VIEW statement.
func (s DropMaterializedViewStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s DropMaterializedViewStmt) compileDDL(c *Compiler) (string, error) {
	var sb strings.Builder
	sb.WriteString("DROP MATERIALIZED VIEW ")
	if s.IfExists {
		sb.WriteString("IF EXISTS ")
	}
	sb.WriteString(c.qualify(s.Table))
	if s.Cascade {
		sb.WriteString(" CASCADE")
	}
	sb.WriteString(";")
	return sb.String(), nil
}
func (DropMaterializedViewStmt) ddlStatement() {}

func (s DropMaterializedViewStmt) setPos(pos int) Query { s.NodePos = pos; return s }

// DropMaterializedViewBuilder is the fluent constructor for a
// DropMaterializedViewStmt.
type DropMaterializedViewBuilder struct{ s DropMaterializedViewStmt }

// DropMaterializedView starts a DROP MATERIALIZED VIEW builder.
func DropMaterializedView(table Table) DropMaterializedViewBuilder {
	return DropMaterializedViewBuilder{s: DropMaterializedViewStmt{Table: table}}
}

// Stmt returns the built DROP MATERIALIZED VIEW statement.
func (b DropMaterializedViewBuilder) Stmt() DropMaterializedViewStmt { return b.s }

// IfExists adds IF EXISTS.
func (b DropMaterializedViewBuilder) IfExists() DropMaterializedViewBuilder {
	b.s.IfExists = true
	return b
}

// Cascade adds CASCADE.
func (b DropMaterializedViewBuilder) Cascade() DropMaterializedViewBuilder {
	b.s.Cascade = true
	return b
}

// ToSQL compiles the building DROP MATERIALIZED VIEW statement.
func (b DropMaterializedViewBuilder) ToSQL(d Dialect) (string, []any, error) { return b.s.ToSQL(d) }
func (b DropMaterializedViewBuilder) buildStmt() Query                       { return b.s }
func (DropMaterializedViewBuilder) ddlStatement()                            {}

func (b DropMaterializedViewBuilder) setPos(pos int) Query { b.s.NodePos = pos; return b }

// RefreshMaterializedViewStmt is the AST for a REFRESH MATERIALIZED VIEW
// statement.
type RefreshMaterializedViewStmt struct {
	posField
	Table        Table
	Concurrently bool
	WithNoData   bool
}

// ToSQL compiles the REFRESH MATERIALIZED VIEW statement.
func (s RefreshMaterializedViewStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s RefreshMaterializedViewStmt) compileDDL(c *Compiler) (string, error) {
	if s.Table.Name.IsZero() {
		return "", fmt.Errorf("sqlkit/sql: REFRESH MATERIALIZED VIEW requires a name")
	}
	// PostgreSQL rejects CONCURRENTLY together with WITH NO DATA ("CONCURRENTLY and
	// WITH NO DATA options cannot be used together"): CONCURRENTLY needs an
	// already-populated view, WITH NO DATA empties it. Reject the pair at compile
	// time rather than emit SQL the engine refuses.
	if s.Concurrently && s.WithNoData {
		return "", fmt.Errorf("sqlkit/sql: REFRESH MATERIALIZED VIEW cannot combine CONCURRENTLY with WITH NO DATA")
	}
	var sb strings.Builder
	sb.WriteString("REFRESH MATERIALIZED VIEW ")
	if s.Concurrently {
		sb.WriteString("CONCURRENTLY ")
	}
	sb.WriteString(c.qualify(s.Table))
	if s.WithNoData {
		sb.WriteString(" WITH NO DATA")
	}
	sb.WriteString(";")
	return sb.String(), nil
}
func (RefreshMaterializedViewStmt) ddlStatement() {}

func (s RefreshMaterializedViewStmt) setPos(pos int) Query { s.NodePos = pos; return s }

// RefreshMaterializedViewBuilder is the fluent constructor for a
// RefreshMaterializedViewStmt.
type RefreshMaterializedViewBuilder struct{ s RefreshMaterializedViewStmt }

// RefreshMaterializedView starts a REFRESH MATERIALIZED VIEW builder.
func RefreshMaterializedView(table Table) RefreshMaterializedViewBuilder {
	return RefreshMaterializedViewBuilder{s: RefreshMaterializedViewStmt{Table: table}}
}

// Stmt returns the built REFRESH MATERIALIZED VIEW statement.
func (b RefreshMaterializedViewBuilder) Stmt() RefreshMaterializedViewStmt { return b.s }

// Concurrently adds CONCURRENTLY.
func (b RefreshMaterializedViewBuilder) Concurrently() RefreshMaterializedViewBuilder {
	b.s.Concurrently = true
	return b
}

// WithNoData adds WITH NO DATA, leaving the view unpopulated.
func (b RefreshMaterializedViewBuilder) WithNoData() RefreshMaterializedViewBuilder {
	b.s.WithNoData = true
	return b
}

// ToSQL compiles the building REFRESH MATERIALIZED VIEW statement.
func (b RefreshMaterializedViewBuilder) ToSQL(d Dialect) (string, []any, error) {
	return b.s.ToSQL(d)
}
func (b RefreshMaterializedViewBuilder) buildStmt() Query { return b.s }
func (RefreshMaterializedViewBuilder) ddlStatement()      {}

func (b RefreshMaterializedViewBuilder) setPos(pos int) Query { b.s.NodePos = pos; return b }
