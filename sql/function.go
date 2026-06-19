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

// CreateFunctionStmt is the AST for a CREATE FUNCTION statement. The argument
// types, return type, language, options, and body are rendered verbatim, so they
// must already be valid for the target engine. It is a DDL Statement.
type CreateFunctionStmt struct {
	Name      string
	OrReplace bool
	Args      []FunctionArg
	Returns   string
	Language  string
	Options   []string
	Body      string
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

// CreateFunctionBuilder is the fluent constructor for a CreateFunctionStmt.
type CreateFunctionBuilder struct{ s CreateFunctionStmt }

// CreateFunction starts a CREATE FUNCTION builder for a (optionally
// schema-qualified) function name.
func CreateFunction(name string) CreateFunctionBuilder {
	return CreateFunctionBuilder{s: CreateFunctionStmt{Name: name}}
}

// Stmt returns the built CREATE FUNCTION statement.
func (b CreateFunctionBuilder) Stmt() CreateFunctionStmt { return b.s }

// OrReplace makes it CREATE OR REPLACE FUNCTION.
func (b CreateFunctionBuilder) OrReplace() CreateFunctionBuilder {
	b.s.OrReplace = true
	return b
}

// Arg appends a plain IN parameter with the given name and type.
func (b CreateFunctionBuilder) Arg(name, typ string) CreateFunctionBuilder {
	b.s.Args = append(append([]FunctionArg(nil), b.s.Args...), FunctionArg{Name: name, Type: typ})
	return b
}

// Args appends fully specified parameters.
func (b CreateFunctionBuilder) Args(args ...FunctionArg) CreateFunctionBuilder {
	b.s.Args = append(append([]FunctionArg(nil), b.s.Args...), args...)
	return b
}

// Returns sets the return type clause (e.g. "integer", "SETOF users",
// "trigger").
func (b CreateFunctionBuilder) Returns(returnType string) CreateFunctionBuilder {
	b.s.Returns = returnType
	return b
}

// Language sets the implementation language (e.g. "sql", "plpgsql").
func (b CreateFunctionBuilder) Language(language string) CreateFunctionBuilder {
	b.s.Language = language
	return b
}

// Option appends routine option keywords rendered before AS (e.g. "IMMUTABLE",
// "STRICT", "SECURITY DEFINER").
func (b CreateFunctionBuilder) Option(options ...string) CreateFunctionBuilder {
	b.s.Options = append(append([]string(nil), b.s.Options...), options...)
	return b
}

// Body sets the function body, emitted dollar-quoted (AS $$ body $$).
func (b CreateFunctionBuilder) Body(body string) CreateFunctionBuilder {
	b.s.Body = body
	return b
}

// ToSQL compiles the building CREATE FUNCTION statement.
func (b CreateFunctionBuilder) ToSQL(d Dialect) (string, []any, error) { return b.s.ToSQL(d) }
func (b CreateFunctionBuilder) buildStmt() Query                       { return b.s }
func (CreateFunctionBuilder) ddlStatement()                            {}

// CompileCreateFunction compiles a CREATE FUNCTION statement.
func (c *Compiler) CompileCreateFunction(s CreateFunctionStmt) (string, error) {
	if s.Name == "" {
		return "", fmt.Errorf("sqlkit/sql: CREATE FUNCTION requires a name")
	}
	if s.Language == "" {
		return "", fmt.Errorf("sqlkit/sql: CREATE FUNCTION requires a language")
	}
	if strings.Contains(s.Body, "$$") {
		return "", fmt.Errorf("sqlkit/sql: CREATE FUNCTION body cannot contain the dollar-quote delimiter $$")
	}
	var sb strings.Builder
	sb.WriteString("CREATE ")
	if s.OrReplace {
		sb.WriteString("OR REPLACE ")
	}
	sb.WriteString("FUNCTION ")
	sb.WriteString(c.Dialect.QuoteIdent(s.Name))
	sb.WriteString("(")
	for i, arg := range s.Args {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(renderFunctionArg(arg))
	}
	sb.WriteString(")")
	if s.Returns != "" {
		sb.WriteString(" RETURNS ")
		sb.WriteString(s.Returns)
	}
	sb.WriteString(" LANGUAGE ")
	sb.WriteString(s.Language)
	for _, opt := range s.Options {
		sb.WriteString(" ")
		sb.WriteString(opt)
	}
	sb.WriteString(" AS $$")
	sb.WriteString(s.Body)
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

// DropFunctionStmt is the AST for a DROP FUNCTION statement. Argument types
// disambiguate an overloaded function; they are rendered verbatim. HasArgs
// reports whether an argument-type list was given (an empty list still renders
// "()"). Behavior is "", "CASCADE", or "RESTRICT". It is a DDL Statement.
type DropFunctionStmt struct {
	Name     string
	IfExists bool
	ArgTypes []string
	HasArgs  bool
	Behavior string
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

// DropFunctionBuilder is the fluent constructor for a DropFunctionStmt.
type DropFunctionBuilder struct{ s DropFunctionStmt }

// DropFunction starts a DROP FUNCTION builder for a function name.
func DropFunction(name string) DropFunctionBuilder {
	return DropFunctionBuilder{s: DropFunctionStmt{Name: name}}
}

// Stmt returns the built DROP FUNCTION statement.
func (b DropFunctionBuilder) Stmt() DropFunctionStmt { return b.s }

// IfExists adds IF EXISTS.
func (b DropFunctionBuilder) IfExists() DropFunctionBuilder {
	b.s.IfExists = true
	return b
}

// ArgTypes pins the argument-type list that identifies an overload (DROP
// FUNCTION f(int, text)). Call with no types for an empty "()" list.
func (b DropFunctionBuilder) ArgTypes(types ...string) DropFunctionBuilder {
	b.s.ArgTypes = types
	b.s.HasArgs = true
	return b
}

// Cascade adds CASCADE, also dropping dependent objects.
func (b DropFunctionBuilder) Cascade() DropFunctionBuilder {
	b.s.Behavior = "CASCADE"
	return b
}

// Restrict adds RESTRICT, refusing to drop when dependents exist.
func (b DropFunctionBuilder) Restrict() DropFunctionBuilder {
	b.s.Behavior = "RESTRICT"
	return b
}

// ToSQL compiles the building DROP FUNCTION statement.
func (b DropFunctionBuilder) ToSQL(d Dialect) (string, []any, error) { return b.s.ToSQL(d) }
func (b DropFunctionBuilder) buildStmt() Query                       { return b.s }
func (DropFunctionBuilder) ddlStatement()                            {}

// CompileDropFunction compiles a DROP FUNCTION statement.
func (c *Compiler) CompileDropFunction(s DropFunctionStmt) (string, error) {
	if s.Name == "" {
		return "", fmt.Errorf("sqlkit/sql: DROP FUNCTION requires a name")
	}
	var sb strings.Builder
	sb.WriteString("DROP FUNCTION ")
	if s.IfExists {
		sb.WriteString("IF EXISTS ")
	}
	sb.WriteString(c.Dialect.QuoteIdent(s.Name))
	if s.HasArgs {
		sb.WriteString("(")
		sb.WriteString(strings.Join(s.ArgTypes, ", "))
		sb.WriteString(")")
	}
	if s.Behavior != "" {
		sb.WriteString(" ")
		sb.WriteString(s.Behavior)
	}
	sb.WriteString(";")
	return sb.String(), nil
}
