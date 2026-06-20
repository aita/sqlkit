package sql

import (
	"fmt"
	"strings"
)

// CreateExtensionStmt is the AST for a CREATE EXTENSION statement (PostgreSQL).
// It is a DDL Statement.
type CreateExtensionStmt struct {
	posField
	Name        Ident
	IfNotExists bool
	Schema      Ident
}

// ToSQL compiles the CREATE EXTENSION statement.
func (s CreateExtensionStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s CreateExtensionStmt) compileDDL(c *Compiler) (string, error) {
	if s.Name.IsZero() {
		return "", fmt.Errorf("sqlkit/sql: CREATE EXTENSION requires a name")
	}
	var sb strings.Builder
	sb.WriteString("CREATE EXTENSION ")
	if s.IfNotExists {
		sb.WriteString("IF NOT EXISTS ")
	}
	sb.WriteString(c.Dialect.QuoteName(s.Name.String()))
	if !s.Schema.IsZero() {
		sb.WriteString(" WITH SCHEMA ")
		sb.WriteString(c.Dialect.QuoteName(s.Schema.String()))
	}
	sb.WriteString(";")
	return sb.String(), nil
}
func (CreateExtensionStmt) ddlStatement() {}

func (s CreateExtensionStmt) setPos(pos int) Query { s.NodePos = pos; return s }

// CreateExtensionBuilder is the fluent constructor for a CreateExtensionStmt.
type CreateExtensionBuilder struct{ s CreateExtensionStmt }

// CreateExtension starts a CREATE EXTENSION builder for an extension name.
func CreateExtension(name string) CreateExtensionBuilder {
	return CreateExtensionBuilder{s: CreateExtensionStmt{Name: NewIdent(name)}}
}

// Stmt returns the built CREATE EXTENSION statement.
func (b CreateExtensionBuilder) Stmt() CreateExtensionStmt { return b.s }

// IfNotExists adds IF NOT EXISTS.
func (b CreateExtensionBuilder) IfNotExists() CreateExtensionBuilder {
	b.s.IfNotExists = true
	return b
}

// Schema installs the extension into a specific schema (WITH SCHEMA name).
func (b CreateExtensionBuilder) Schema(schema string) CreateExtensionBuilder {
	b.s.Schema = NewIdent(schema)
	return b
}

// ToSQL compiles the building CREATE EXTENSION statement.
func (b CreateExtensionBuilder) ToSQL(d Dialect) (string, []any, error) { return b.s.ToSQL(d) }
func (b CreateExtensionBuilder) buildStmt() Query                       { return b.s }
func (CreateExtensionBuilder) ddlStatement()                            {}

func (b CreateExtensionBuilder) setPos(pos int) Query { b.s.NodePos = pos; return b }

// DropExtensionStmt is the AST for a DROP EXTENSION statement.
type DropExtensionStmt struct {
	posField
	Names    []Ident
	IfExists bool
	Cascade  bool
}

// ToSQL compiles the DROP EXTENSION statement.
func (s DropExtensionStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s DropExtensionStmt) compileDDL(c *Compiler) (string, error) {
	if len(s.Names) == 0 {
		return "", fmt.Errorf("sqlkit/sql: DROP EXTENSION requires a name")
	}
	var sb strings.Builder
	sb.WriteString("DROP EXTENSION ")
	if s.IfExists {
		sb.WriteString("IF EXISTS ")
	}
	for i, name := range s.Names {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(c.Dialect.QuoteName(name.String()))
	}
	if s.Cascade {
		sb.WriteString(" CASCADE")
	}
	sb.WriteString(";")
	return sb.String(), nil
}
func (DropExtensionStmt) ddlStatement() {}

func (s DropExtensionStmt) setPos(pos int) Query { s.NodePos = pos; return s }

// DropExtensionBuilder is the fluent constructor for a DropExtensionStmt.
type DropExtensionBuilder struct{ s DropExtensionStmt }

// DropExtension starts a DROP EXTENSION builder for one or more extensions.
func DropExtension(names ...string) DropExtensionBuilder {
	return DropExtensionBuilder{s: DropExtensionStmt{Names: idents(names)}}
}

// Stmt returns the built DROP EXTENSION statement.
func (b DropExtensionBuilder) Stmt() DropExtensionStmt { return b.s }

// IfExists adds IF EXISTS.
func (b DropExtensionBuilder) IfExists() DropExtensionBuilder {
	b.s.IfExists = true
	return b
}

// Cascade adds CASCADE.
func (b DropExtensionBuilder) Cascade() DropExtensionBuilder {
	b.s.Cascade = true
	return b
}

// ToSQL compiles the building DROP EXTENSION statement.
func (b DropExtensionBuilder) ToSQL(d Dialect) (string, []any, error) { return b.s.ToSQL(d) }
func (b DropExtensionBuilder) buildStmt() Query                       { return b.s }
func (DropExtensionBuilder) ddlStatement()                            {}

func (b DropExtensionBuilder) setPos(pos int) Query { b.s.NodePos = pos; return b }
