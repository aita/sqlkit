package sql

import (
	"fmt"
	"strings"
)

// CreateEnumStmt builds a CREATE TYPE ... AS ENUM statement (PostgreSQL). It is
// a DDL Statement.
type CreateEnumStmt struct {
	name   string
	values []string
}

// CreateEnum starts a CREATE TYPE name AS ENUM (values...) builder. The name may
// be schema-qualified.
func CreateEnum(name string, values ...string) CreateEnumStmt {
	return CreateEnumStmt{name: name, values: values}
}

// ToSQL compiles the CREATE TYPE ... AS ENUM statement.
func (s CreateEnumStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s CreateEnumStmt) compileDDL(c *Compiler) (string, error) {
	if s.name == "" {
		return "", fmt.Errorf("sqlkit/sql: CREATE TYPE requires a name")
	}
	var sb strings.Builder
	sb.WriteString("CREATE TYPE ")
	sb.WriteString(c.Dialect.QuoteIdent(s.name))
	sb.WriteString(" AS ENUM (")
	for i, value := range s.values {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(literal(value))
	}
	sb.WriteString(");")
	return sb.String(), nil
}
func (CreateEnumStmt) ddlStatement() {}

// DropTypeStmt builds a DROP TYPE statement (PostgreSQL).
type DropTypeStmt struct {
	names    []string
	ifExists bool
	cascade  bool
}

// DropType starts a DROP TYPE builder for one or more (possibly
// schema-qualified) type names.
func DropType(names ...string) DropTypeStmt {
	return DropTypeStmt{names: names}
}

// IfExists adds IF EXISTS.
func (s DropTypeStmt) IfExists() DropTypeStmt {
	s.ifExists = true
	return s
}

// Cascade adds CASCADE.
func (s DropTypeStmt) Cascade() DropTypeStmt {
	s.cascade = true
	return s
}

// ToSQL compiles the DROP TYPE statement.
func (s DropTypeStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s DropTypeStmt) compileDDL(c *Compiler) (string, error) {
	if len(s.names) == 0 {
		return "", fmt.Errorf("sqlkit/sql: DROP TYPE requires a name")
	}
	var sb strings.Builder
	sb.WriteString("DROP TYPE ")
	if s.ifExists {
		sb.WriteString("IF EXISTS ")
	}
	for i, name := range s.names {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(c.Dialect.QuoteIdent(name))
	}
	if s.cascade {
		sb.WriteString(" CASCADE")
	}
	sb.WriteString(";")
	return sb.String(), nil
}
func (DropTypeStmt) ddlStatement() {}

// AlterTypeAddValueStmt builds an ALTER TYPE ... ADD VALUE statement, adding a
// label to an enum type (PostgreSQL). It is a DDL Statement.
type AlterTypeAddValueStmt struct {
	name        string
	value       string
	ifNotExists bool
	before      string
	after       string
}

// AlterTypeAddValue starts a builder that adds value to an enum type.
func AlterTypeAddValue(name, value string) AlterTypeAddValueStmt {
	return AlterTypeAddValueStmt{name: name, value: value}
}

// IfNotExists adds IF NOT EXISTS, skipping the value if it already exists.
func (s AlterTypeAddValueStmt) IfNotExists() AlterTypeAddValueStmt {
	s.ifNotExists = true
	return s
}

// Before places the new value before an existing one.
func (s AlterTypeAddValueStmt) Before(neighbor string) AlterTypeAddValueStmt {
	s.before, s.after = neighbor, ""
	return s
}

// After places the new value after an existing one.
func (s AlterTypeAddValueStmt) After(neighbor string) AlterTypeAddValueStmt {
	s.after, s.before = neighbor, ""
	return s
}

// ToSQL compiles the ALTER TYPE ... ADD VALUE statement.
func (s AlterTypeAddValueStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s AlterTypeAddValueStmt) compileDDL(c *Compiler) (string, error) {
	if s.name == "" || s.value == "" {
		return "", fmt.Errorf("sqlkit/sql: ALTER TYPE ADD VALUE requires a type name and a value")
	}
	var sb strings.Builder
	sb.WriteString("ALTER TYPE ")
	sb.WriteString(c.Dialect.QuoteIdent(s.name))
	sb.WriteString(" ADD VALUE ")
	if s.ifNotExists {
		sb.WriteString("IF NOT EXISTS ")
	}
	sb.WriteString(literal(s.value))
	switch {
	case s.before != "":
		sb.WriteString(" BEFORE " + literal(s.before))
	case s.after != "":
		sb.WriteString(" AFTER " + literal(s.after))
	}
	sb.WriteString(";")
	return sb.String(), nil
}
func (AlterTypeAddValueStmt) ddlStatement() {}

// AlterTypeRenameValueStmt builds an ALTER TYPE ... RENAME VALUE statement.
type AlterTypeRenameValueStmt struct {
	name     string
	oldValue string
	newValue string
}

// AlterTypeRenameValue starts a builder that renames an enum label.
func AlterTypeRenameValue(name, oldValue, newValue string) AlterTypeRenameValueStmt {
	return AlterTypeRenameValueStmt{name: name, oldValue: oldValue, newValue: newValue}
}

// ToSQL compiles the ALTER TYPE ... RENAME VALUE statement.
func (s AlterTypeRenameValueStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s AlterTypeRenameValueStmt) compileDDL(c *Compiler) (string, error) {
	if s.name == "" || s.oldValue == "" || s.newValue == "" {
		return "", fmt.Errorf("sqlkit/sql: ALTER TYPE RENAME VALUE requires a type name and both values")
	}
	return "ALTER TYPE " + c.Dialect.QuoteIdent(s.name) + " RENAME VALUE " +
		literal(s.oldValue) + " TO " + literal(s.newValue) + ";", nil
}
func (AlterTypeRenameValueStmt) ddlStatement() {}
