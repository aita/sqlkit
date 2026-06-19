package sql

import (
	"fmt"
	"strings"
)

// CreateEnumStmt is the AST for a CREATE TYPE ... AS ENUM statement (PostgreSQL).
// It has no options, so it is constructed directly with CreateEnum.
type CreateEnumStmt struct {
	Name   string
	Values []string
}

// CreateEnum builds a CREATE TYPE name AS ENUM (values...) statement. The name
// may be schema-qualified.
func CreateEnum(name string, values ...string) CreateEnumStmt {
	return CreateEnumStmt{Name: name, Values: values}
}

// ToSQL compiles the CREATE TYPE ... AS ENUM statement.
func (s CreateEnumStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s CreateEnumStmt) compileDDL(c *Compiler) (string, error) {
	if s.Name == "" {
		return "", fmt.Errorf("sqlkit/sql: CREATE TYPE requires a name")
	}
	var sb strings.Builder
	sb.WriteString("CREATE TYPE ")
	sb.WriteString(c.Dialect.QuoteIdent(s.Name))
	sb.WriteString(" AS ENUM (")
	for i, value := range s.Values {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(literal(value))
	}
	sb.WriteString(");")
	return sb.String(), nil
}
func (CreateEnumStmt) ddlStatement() {}

// DropTypeStmt is the AST for a DROP TYPE statement (PostgreSQL).
type DropTypeStmt struct {
	Names    []string
	IfExists bool
	Cascade  bool
}

// ToSQL compiles the DROP TYPE statement.
func (s DropTypeStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s DropTypeStmt) compileDDL(c *Compiler) (string, error) {
	if len(s.Names) == 0 {
		return "", fmt.Errorf("sqlkit/sql: DROP TYPE requires a name")
	}
	var sb strings.Builder
	sb.WriteString("DROP TYPE ")
	if s.IfExists {
		sb.WriteString("IF EXISTS ")
	}
	for i, name := range s.Names {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(c.Dialect.QuoteIdent(name))
	}
	if s.Cascade {
		sb.WriteString(" CASCADE")
	}
	sb.WriteString(";")
	return sb.String(), nil
}
func (DropTypeStmt) ddlStatement() {}

// DropTypeBuilder is the fluent constructor for a DropTypeStmt.
type DropTypeBuilder struct{ s DropTypeStmt }

// DropType starts a DROP TYPE builder for one or more (possibly
// schema-qualified) type names.
func DropType(names ...string) DropTypeBuilder {
	return DropTypeBuilder{s: DropTypeStmt{Names: names}}
}

// Stmt returns the built DROP TYPE statement.
func (b DropTypeBuilder) Stmt() DropTypeStmt { return b.s }

// IfExists adds IF EXISTS.
func (b DropTypeBuilder) IfExists() DropTypeBuilder {
	b.s.IfExists = true
	return b
}

// Cascade adds CASCADE.
func (b DropTypeBuilder) Cascade() DropTypeBuilder {
	b.s.Cascade = true
	return b
}

// ToSQL compiles the building DROP TYPE statement.
func (b DropTypeBuilder) ToSQL(d Dialect) (string, []any, error) { return b.s.ToSQL(d) }
func (b DropTypeBuilder) buildStmt() Query                       { return b.s }
func (DropTypeBuilder) ddlStatement()                            {}

// AlterTypeAddValueStmt is the AST for an ALTER TYPE ... ADD VALUE statement,
// adding a label to an enum type (PostgreSQL). At most one of Before/After is
// set. It is a DDL Statement.
type AlterTypeAddValueStmt struct {
	Name        string
	Value       string
	IfNotExists bool
	Before      string
	After       string
}

// ToSQL compiles the ALTER TYPE ... ADD VALUE statement.
func (s AlterTypeAddValueStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s AlterTypeAddValueStmt) compileDDL(c *Compiler) (string, error) {
	if s.Name == "" || s.Value == "" {
		return "", fmt.Errorf("sqlkit/sql: ALTER TYPE ADD VALUE requires a type name and a value")
	}
	var sb strings.Builder
	sb.WriteString("ALTER TYPE ")
	sb.WriteString(c.Dialect.QuoteIdent(s.Name))
	sb.WriteString(" ADD VALUE ")
	if s.IfNotExists {
		sb.WriteString("IF NOT EXISTS ")
	}
	sb.WriteString(literal(s.Value))
	switch {
	case s.Before != "":
		sb.WriteString(" BEFORE " + literal(s.Before))
	case s.After != "":
		sb.WriteString(" AFTER " + literal(s.After))
	}
	sb.WriteString(";")
	return sb.String(), nil
}
func (AlterTypeAddValueStmt) ddlStatement() {}

// AlterTypeAddValueBuilder is the fluent constructor for an AlterTypeAddValueStmt.
type AlterTypeAddValueBuilder struct{ s AlterTypeAddValueStmt }

// AlterTypeAddValue starts a builder that adds value to an enum type.
func AlterTypeAddValue(name, value string) AlterTypeAddValueBuilder {
	return AlterTypeAddValueBuilder{s: AlterTypeAddValueStmt{Name: name, Value: value}}
}

// Stmt returns the built ALTER TYPE ... ADD VALUE statement.
func (b AlterTypeAddValueBuilder) Stmt() AlterTypeAddValueStmt { return b.s }

// IfNotExists adds IF NOT EXISTS, skipping the value if it already exists.
func (b AlterTypeAddValueBuilder) IfNotExists() AlterTypeAddValueBuilder {
	b.s.IfNotExists = true
	return b
}

// Before places the new value before an existing one.
func (b AlterTypeAddValueBuilder) Before(neighbor string) AlterTypeAddValueBuilder {
	b.s.Before, b.s.After = neighbor, ""
	return b
}

// After places the new value after an existing one.
func (b AlterTypeAddValueBuilder) After(neighbor string) AlterTypeAddValueBuilder {
	b.s.After, b.s.Before = neighbor, ""
	return b
}

// ToSQL compiles the building ALTER TYPE ... ADD VALUE statement.
func (b AlterTypeAddValueBuilder) ToSQL(d Dialect) (string, []any, error) { return b.s.ToSQL(d) }
func (b AlterTypeAddValueBuilder) buildStmt() Query                       { return b.s }
func (AlterTypeAddValueBuilder) ddlStatement()                            {}

// AlterTypeRenameValueStmt is the AST for an ALTER TYPE ... RENAME VALUE
// statement. It has no options, so it is constructed directly.
type AlterTypeRenameValueStmt struct {
	Name     string
	OldValue string
	NewValue string
}

// AlterTypeRenameValue builds a statement that renames an enum label.
func AlterTypeRenameValue(name, oldValue, newValue string) AlterTypeRenameValueStmt {
	return AlterTypeRenameValueStmt{Name: name, OldValue: oldValue, NewValue: newValue}
}

// ToSQL compiles the ALTER TYPE ... RENAME VALUE statement.
func (s AlterTypeRenameValueStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s AlterTypeRenameValueStmt) compileDDL(c *Compiler) (string, error) {
	if s.Name == "" || s.OldValue == "" || s.NewValue == "" {
		return "", fmt.Errorf("sqlkit/sql: ALTER TYPE RENAME VALUE requires a type name and both values")
	}
	return "ALTER TYPE " + c.Dialect.QuoteIdent(s.Name) + " RENAME VALUE " +
		literal(s.OldValue) + " TO " + literal(s.NewValue) + ";", nil
}
func (AlterTypeRenameValueStmt) ddlStatement() {}
