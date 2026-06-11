package sql

import "fmt"

// Compiler turns SQL expressions and queries into SQL text plus arguments.
type Compiler struct {
	Dialect Dialect
	ordinal int
}

// NewCompiler creates a compiler for the supplied dialect.
func NewCompiler(dialect Dialect) *Compiler {
	if dialect == nil {
		dialect = PostgreSQLDialect{}
	}
	return &Compiler{Dialect: dialect}
}

// CompileExpression compiles an expression fragment.
func (c *Compiler) CompileExpression(expr Expression) (string, []any, error) {
	switch e := expr.(type) {
	case RawExpression:
		return string(e), nil, nil
	case ColumnExpression:
		return c.Dialect.QuoteIdent(e.Reference()), nil, nil
	case interface{ Reference() string }:
		return c.Dialect.QuoteIdent(e.Reference()), nil, nil
	case ValueExpression:
		return c.nextPlaceholder(), []any{e.Value}, nil
	case BinaryExpression:
		leftSQL, leftArgs, err := c.CompileExpression(e.Left)
		if err != nil {
			return "", nil, err
		}
		rightSQL, rightArgs, err := c.CompileExpression(e.Right)
		if err != nil {
			return "", nil, err
		}
		args := append(leftArgs, rightArgs...)
		return "(" + leftSQL + " " + e.Operator + " " + rightSQL + ")", args, nil
	default:
		return "", nil, fmt.Errorf("sqlkit/sql: unsupported expression %T", expr)
	}
}

// CompileSelection compiles a SELECT-list item.
func (c *Compiler) CompileSelection(selection Selection) (string, error) {
	switch s := selection.(type) {
	case ColumnExpression:
		return c.Dialect.QuoteIdent(s.Reference()), nil
	case interface{ Reference() string }:
		return c.Dialect.QuoteIdent(s.Reference()), nil
	case CountExpression:
		if s.All || s.Target == nil {
			return "COUNT(*)", nil
		}
		target, err := c.CompileSelection(s.Target)
		if err != nil {
			return "", err
		}
		return "COUNT(" + target + ")", nil
	default:
		return "", fmt.Errorf("sqlkit/sql: unsupported selection %T", selection)
	}
}

// CompileSource compiles a FROM/JOIN source.
func (c *Compiler) CompileSource(source Source) (string, []any, error) {
	if source == nil {
		return "", nil, fmt.Errorf("sqlkit/sql: missing source")
	}
	s := source.SQLSource()
	if s.Query != nil {
		sql, args, err := s.Query.ToSQL(c.Dialect)
		if err != nil {
			return "", nil, err
		}
		compiled := "(" + sql + ")"
		if s.Alias != "" {
			compiled += " AS " + c.Dialect.QuoteIdent(s.Alias)
		}
		return compiled, args, nil
	}
	if s.Name == "" {
		return "", nil, fmt.Errorf("sqlkit/sql: missing source name")
	}
	compiled := c.Dialect.QuoteIdent(s.Name)
	if s.Alias != "" {
		compiled += " AS " + c.Dialect.QuoteIdent(s.Alias)
	}
	return compiled, nil, nil
}

// CompileJoin compiles a JOIN clause.
func (c *Compiler) CompileJoin(join JoinClause) (string, []any, error) {
	sourceSQL, args, err := c.CompileSource(join.Source)
	if err != nil {
		return "", nil, err
	}
	compiled := join.Kind + " " + sourceSQL
	if join.On != nil {
		onSQL, onArgs, err := c.CompileExpression(join.On)
		if err != nil {
			return "", nil, err
		}
		compiled += " ON " + onSQL
		args = append(args, onArgs...)
	}
	return compiled, args, nil
}

func (c *Compiler) nextPlaceholder() string {
	c.ordinal++
	return c.Dialect.Placeholder(c.ordinal)
}
