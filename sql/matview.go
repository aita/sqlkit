package sql

import (
	"fmt"
	"strings"
)

// CreateMaterializedViewStmt builds a CREATE MATERIALIZED VIEW statement
// (PostgreSQL). Like a view its body must compile to plain SQL with no bind
// values; inline literals with Raw. It is a DDL Statement.
type CreateMaterializedViewStmt struct {
	table       Table
	query       Query
	columns     []string
	ifNotExists bool
	withNoData  bool
}

// CreateMaterializedView starts a builder for a materialized view defined by
// query.
func CreateMaterializedView(table Table, query Query) CreateMaterializedViewStmt {
	return CreateMaterializedViewStmt{table: table, query: query}
}

// IfNotExists adds IF NOT EXISTS.
func (s CreateMaterializedViewStmt) IfNotExists() CreateMaterializedViewStmt {
	s.ifNotExists = true
	return s
}

// Columns sets explicit column names.
func (s CreateMaterializedViewStmt) Columns(columns ...string) CreateMaterializedViewStmt {
	s.columns = columns
	return s
}

// WithNoData adds WITH NO DATA, creating the view unpopulated.
func (s CreateMaterializedViewStmt) WithNoData() CreateMaterializedViewStmt {
	s.withNoData = true
	return s
}

// ToSQL compiles the CREATE MATERIALIZED VIEW statement.
func (s CreateMaterializedViewStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s CreateMaterializedViewStmt) compileDDL(c *Compiler) (string, error) {
	if s.query == nil {
		return "", fmt.Errorf("sqlkit/sql: CREATE MATERIALIZED VIEW requires a query")
	}
	body, args, err := s.query.ToSQL(c.Dialect)
	if err != nil {
		return "", err
	}
	if len(args) > 0 {
		return "", fmt.Errorf("sqlkit/sql: CREATE MATERIALIZED VIEW body must not bind values; use Raw to inline literals")
	}
	var sb strings.Builder
	sb.WriteString("CREATE MATERIALIZED VIEW ")
	if s.ifNotExists {
		sb.WriteString("IF NOT EXISTS ")
	}
	sb.WriteString(c.qualify(s.table))
	if len(s.columns) > 0 {
		sb.WriteString(" (" + c.quoteAll(s.columns) + ")")
	}
	sb.WriteString(" AS\n")
	sb.WriteString(body)
	if s.withNoData {
		sb.WriteString(" WITH NO DATA")
	}
	sb.WriteString(";")
	return sb.String(), nil
}
func (CreateMaterializedViewStmt) ddlStatement() {}

// DropMaterializedViewStmt builds a DROP MATERIALIZED VIEW statement.
type DropMaterializedViewStmt struct {
	table    Table
	ifExists bool
	cascade  bool
}

// DropMaterializedView starts a DROP MATERIALIZED VIEW builder.
func DropMaterializedView(table Table) DropMaterializedViewStmt {
	return DropMaterializedViewStmt{table: table}
}

// IfExists adds IF EXISTS.
func (s DropMaterializedViewStmt) IfExists() DropMaterializedViewStmt {
	s.ifExists = true
	return s
}

// Cascade adds CASCADE.
func (s DropMaterializedViewStmt) Cascade() DropMaterializedViewStmt {
	s.cascade = true
	return s
}

// ToSQL compiles the DROP MATERIALIZED VIEW statement.
func (s DropMaterializedViewStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s DropMaterializedViewStmt) compileDDL(c *Compiler) (string, error) {
	var sb strings.Builder
	sb.WriteString("DROP MATERIALIZED VIEW ")
	if s.ifExists {
		sb.WriteString("IF EXISTS ")
	}
	sb.WriteString(c.qualify(s.table))
	if s.cascade {
		sb.WriteString(" CASCADE")
	}
	sb.WriteString(";")
	return sb.String(), nil
}
func (DropMaterializedViewStmt) ddlStatement() {}

// RefreshMaterializedViewStmt builds a REFRESH MATERIALIZED VIEW statement.
type RefreshMaterializedViewStmt struct {
	table      Table
	concurrent bool
	withNoData bool
}

// RefreshMaterializedView starts a REFRESH MATERIALIZED VIEW builder.
func RefreshMaterializedView(table Table) RefreshMaterializedViewStmt {
	return RefreshMaterializedViewStmt{table: table}
}

// Concurrently adds CONCURRENTLY.
func (s RefreshMaterializedViewStmt) Concurrently() RefreshMaterializedViewStmt {
	s.concurrent = true
	return s
}

// WithNoData adds WITH NO DATA, leaving the view unpopulated.
func (s RefreshMaterializedViewStmt) WithNoData() RefreshMaterializedViewStmt {
	s.withNoData = true
	return s
}

// ToSQL compiles the REFRESH MATERIALIZED VIEW statement.
func (s RefreshMaterializedViewStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s RefreshMaterializedViewStmt) compileDDL(c *Compiler) (string, error) {
	if s.table.Name == "" {
		return "", fmt.Errorf("sqlkit/sql: REFRESH MATERIALIZED VIEW requires a name")
	}
	var sb strings.Builder
	sb.WriteString("REFRESH MATERIALIZED VIEW ")
	if s.concurrent {
		sb.WriteString("CONCURRENTLY ")
	}
	sb.WriteString(c.qualify(s.table))
	if s.withNoData {
		sb.WriteString(" WITH NO DATA")
	}
	sb.WriteString(";")
	return sb.String(), nil
}
func (RefreshMaterializedViewStmt) ddlStatement() {}
