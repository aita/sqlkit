package sql

import (
	"fmt"
	"strings"
)

// CreateExtensionStmt builds a CREATE EXTENSION statement (PostgreSQL). It is a
// DDL Statement.
type CreateExtensionStmt struct {
	name        string
	ifNotExists bool
	schema      string
}

// CreateExtension starts a CREATE EXTENSION builder for an extension name.
func CreateExtension(name string) CreateExtensionStmt {
	return CreateExtensionStmt{name: name}
}

// IfNotExists adds IF NOT EXISTS.
func (s CreateExtensionStmt) IfNotExists() CreateExtensionStmt {
	s.ifNotExists = true
	return s
}

// Schema installs the extension into a specific schema (WITH SCHEMA name).
func (s CreateExtensionStmt) Schema(schema string) CreateExtensionStmt {
	s.schema = schema
	return s
}

// ToSQL compiles the CREATE EXTENSION statement.
func (s CreateExtensionStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s CreateExtensionStmt) compileDDL(c *Compiler) (string, error) {
	if s.name == "" {
		return "", fmt.Errorf("sqlkit/sql: CREATE EXTENSION requires a name")
	}
	var sb strings.Builder
	sb.WriteString("CREATE EXTENSION ")
	if s.ifNotExists {
		sb.WriteString("IF NOT EXISTS ")
	}
	sb.WriteString(c.Dialect.QuoteName(s.name))
	if s.schema != "" {
		sb.WriteString(" WITH SCHEMA ")
		sb.WriteString(c.Dialect.QuoteName(s.schema))
	}
	sb.WriteString(";")
	return sb.String(), nil
}
func (CreateExtensionStmt) ddlStatement() {}

// DropExtensionStmt builds a DROP EXTENSION statement.
type DropExtensionStmt struct {
	names    []string
	ifExists bool
	cascade  bool
}

// DropExtension starts a DROP EXTENSION builder for one or more extensions.
func DropExtension(names ...string) DropExtensionStmt {
	return DropExtensionStmt{names: names}
}

// IfExists adds IF EXISTS.
func (s DropExtensionStmt) IfExists() DropExtensionStmt {
	s.ifExists = true
	return s
}

// Cascade adds CASCADE.
func (s DropExtensionStmt) Cascade() DropExtensionStmt {
	s.cascade = true
	return s
}

// ToSQL compiles the DROP EXTENSION statement.
func (s DropExtensionStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s DropExtensionStmt) compileDDL(c *Compiler) (string, error) {
	if len(s.names) == 0 {
		return "", fmt.Errorf("sqlkit/sql: DROP EXTENSION requires a name")
	}
	var sb strings.Builder
	sb.WriteString("DROP EXTENSION ")
	if s.ifExists {
		sb.WriteString("IF EXISTS ")
	}
	for i, name := range s.names {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(c.Dialect.QuoteName(name))
	}
	if s.cascade {
		sb.WriteString(" CASCADE")
	}
	sb.WriteString(";")
	return sb.String(), nil
}
func (DropExtensionStmt) ddlStatement() {}
