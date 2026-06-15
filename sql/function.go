package sql

import (
	"fmt"
	"strings"
)

// FunctionArg is one parameter of a CREATE FUNCTION signature: a type (required)
// with an optional name, mode (IN/OUT/INOUT/VARIADIC), and DEFAULT expression
// text. Type and Default are rendered verbatim.
type FunctionArg struct {
	Mode    string
	Name    string
	Type    string
	Default string
}

// CreateFunctionStmt builds a CREATE FUNCTION statement. The argument types,
// return type, language, options, and body are rendered verbatim, so they must
// already be valid for the target engine. It is a DDL Statement.
type CreateFunctionStmt struct {
	name      string
	orReplace bool
	args      []FunctionArg
	returns   string
	language  string
	options   []string
	body      string
}

// CreateFunction starts a CREATE FUNCTION builder for a (optionally
// schema-qualified) function name.
func CreateFunction(name string) CreateFunctionStmt {
	return CreateFunctionStmt{name: name}
}

// OrReplace makes it CREATE OR REPLACE FUNCTION.
func (s CreateFunctionStmt) OrReplace() CreateFunctionStmt {
	s.orReplace = true
	return s
}

// Arg appends a plain IN parameter with the given name and type.
func (s CreateFunctionStmt) Arg(name, typ string) CreateFunctionStmt {
	s.args = append(s.args, FunctionArg{Name: name, Type: typ})
	return s
}

// Args appends fully specified parameters.
func (s CreateFunctionStmt) Args(args ...FunctionArg) CreateFunctionStmt {
	s.args = append(s.args, args...)
	return s
}

// Returns sets the return type clause (e.g. "integer", "SETOF users",
// "trigger").
func (s CreateFunctionStmt) Returns(returnType string) CreateFunctionStmt {
	s.returns = returnType
	return s
}

// Language sets the implementation language (e.g. "sql", "plpgsql").
func (s CreateFunctionStmt) Language(language string) CreateFunctionStmt {
	s.language = language
	return s
}

// Option appends routine option keywords rendered before AS (e.g. "IMMUTABLE",
// "STRICT", "SECURITY DEFINER").
func (s CreateFunctionStmt) Option(options ...string) CreateFunctionStmt {
	s.options = append(s.options, options...)
	return s
}

// Body sets the function body, emitted dollar-quoted (AS $$ body $$).
func (s CreateFunctionStmt) Body(body string) CreateFunctionStmt {
	s.body = body
	return s
}

// ToSQL compiles the CREATE FUNCTION statement.
func (s CreateFunctionStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s CreateFunctionStmt) compileDDL(c *Compiler) (string, error) {
	return c.CompileCreateFunction(s)
}
func (CreateFunctionStmt) ddlStatement() {}

// CompileCreateFunction compiles a CREATE FUNCTION statement.
func (c *Compiler) CompileCreateFunction(s CreateFunctionStmt) (string, error) {
	if s.name == "" {
		return "", fmt.Errorf("sqlkit/sql: CREATE FUNCTION requires a name")
	}
	if s.language == "" {
		return "", fmt.Errorf("sqlkit/sql: CREATE FUNCTION requires a language")
	}
	if strings.Contains(s.body, "$$") {
		return "", fmt.Errorf("sqlkit/sql: CREATE FUNCTION body cannot contain the dollar-quote delimiter $$")
	}
	var sb strings.Builder
	sb.WriteString("CREATE ")
	if s.orReplace {
		sb.WriteString("OR REPLACE ")
	}
	sb.WriteString("FUNCTION ")
	sb.WriteString(c.Dialect.QuoteIdent(s.name))
	sb.WriteString("(")
	for i, arg := range s.args {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(renderFunctionArg(arg))
	}
	sb.WriteString(")")
	if s.returns != "" {
		sb.WriteString(" RETURNS ")
		sb.WriteString(s.returns)
	}
	sb.WriteString(" LANGUAGE ")
	sb.WriteString(s.language)
	for _, opt := range s.options {
		sb.WriteString(" ")
		sb.WriteString(opt)
	}
	sb.WriteString(" AS $$")
	sb.WriteString(s.body)
	sb.WriteString("$$;")
	return sb.String(), nil
}

func renderFunctionArg(arg FunctionArg) string {
	var parts []string
	if arg.Mode != "" {
		parts = append(parts, arg.Mode)
	}
	if arg.Name != "" {
		parts = append(parts, arg.Name)
	}
	parts = append(parts, arg.Type)
	out := strings.Join(parts, " ")
	if arg.Default != "" {
		out += " DEFAULT " + arg.Default
	}
	return out
}

// DropFunctionStmt builds a DROP FUNCTION statement. Argument types disambiguate
// an overloaded function; they are rendered verbatim. It is a DDL Statement.
type DropFunctionStmt struct {
	name     string
	ifExists bool
	argTypes []string
	hasArgs  bool
	behavior string // "", "CASCADE", or "RESTRICT"
}

// DropFunction starts a DROP FUNCTION builder for a function name.
func DropFunction(name string) DropFunctionStmt {
	return DropFunctionStmt{name: name}
}

// IfExists adds IF EXISTS.
func (s DropFunctionStmt) IfExists() DropFunctionStmt {
	s.ifExists = true
	return s
}

// ArgTypes pins the argument-type list that identifies an overload (DROP
// FUNCTION f(int, text)). Call with no types for an empty "()" list.
func (s DropFunctionStmt) ArgTypes(types ...string) DropFunctionStmt {
	s.argTypes = types
	s.hasArgs = true
	return s
}

// Cascade adds CASCADE, also dropping dependent objects.
func (s DropFunctionStmt) Cascade() DropFunctionStmt {
	s.behavior = "CASCADE"
	return s
}

// Restrict adds RESTRICT, refusing to drop when dependents exist.
func (s DropFunctionStmt) Restrict() DropFunctionStmt {
	s.behavior = "RESTRICT"
	return s
}

// ToSQL compiles the DROP FUNCTION statement.
func (s DropFunctionStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s DropFunctionStmt) compileDDL(c *Compiler) (string, error) {
	return c.CompileDropFunction(s)
}
func (DropFunctionStmt) ddlStatement() {}

// CompileDropFunction compiles a DROP FUNCTION statement.
func (c *Compiler) CompileDropFunction(s DropFunctionStmt) (string, error) {
	if s.name == "" {
		return "", fmt.Errorf("sqlkit/sql: DROP FUNCTION requires a name")
	}
	var sb strings.Builder
	sb.WriteString("DROP FUNCTION ")
	if s.ifExists {
		sb.WriteString("IF EXISTS ")
	}
	sb.WriteString(c.Dialect.QuoteIdent(s.name))
	if s.hasArgs {
		sb.WriteString("(")
		sb.WriteString(strings.Join(s.argTypes, ", "))
		sb.WriteString(")")
	}
	if s.behavior != "" {
		sb.WriteString(" ")
		sb.WriteString(s.behavior)
	}
	sb.WriteString(";")
	return sb.String(), nil
}
