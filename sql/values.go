package sql

import (
	"fmt"
	"strings"
)

// ValuesQuery is a standalone VALUES row constructor used as a query:
// VALUES (1, 'a'), (2, 'b'). Being a Query it can stand on its own, serve as a
// derived-table source (wrap it with Alias to name it and its columns), feed
// INSERT ... SELECT (InsertQuery.FromSelect), or back a CTE. Every row must hold
// the same number of values.
//
// It differs from ValuesSource, which is the (VALUES ...) AS alias(cols) form
// built specifically as a FROM/JOIN source, and from InsertQuery.Values, which
// is the VALUES list of an INSERT.
type ValuesQuery struct {
	posField
	rows [][]Expression
}

// Values creates a VALUES query from rows of expressions. Append further rows
// ergonomically with Row.
func Values(rows ...[]Expression) ValuesQuery { return ValuesQuery{rows: rows} }

// Row appends one row of values. Non-expression values become bind values.
func (q ValuesQuery) Row(values ...any) ValuesQuery {
	row := make([]Expression, len(values))
	for i, value := range values {
		row[i] = Expr(value)
	}
	q.rows = append(append([][]Expression(nil), q.rows...), row)
	return q
}

// SQLSource marks ValuesQuery as a subquery source, so it renders as
// (VALUES ...) and can be aliased into a FROM/JOIN clause.
func (q ValuesQuery) SQLSource() SourceExpression { return SourceExpression{Query: q} }

// ToSQL compiles the VALUES query.
func (q ValuesQuery) ToSQL(dialect Dialect) (string, []any, error) {
	return q.compile(NewCompiler(dialect))
}

func (q ValuesQuery) compile(c *Compiler) (string, []any, error) {
	return c.CompileValues(q)
}

// CompileValues compiles a standalone VALUES query.
func (c *Compiler) CompileValues(q ValuesQuery) (string, []any, error) {
	if len(q.rows) == 0 {
		return "", nil, fmt.Errorf("sqlkit/sql: VALUES requires at least one row")
	}
	width := len(q.rows[0])
	if width == 0 {
		return "", nil, fmt.Errorf("sqlkit/sql: VALUES row 0 has no values")
	}
	var sb strings.Builder
	var args []any
	sb.WriteString("VALUES ")
	for ri, row := range q.rows {
		if len(row) != width {
			return "", nil, fmt.Errorf("sqlkit/sql: VALUES row %d has %d values, want %d", ri, len(row), width)
		}
		if ri > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("(")
		for ci, value := range row {
			if ci > 0 {
				sb.WriteString(", ")
			}
			valueSQL, valueArgs, err := c.CompileExpression(value)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(valueSQL)
			args = append(args, valueArgs...)
		}
		sb.WriteString(")")
	}
	return sb.String(), args, nil
}
