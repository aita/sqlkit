package sql

import (
	"strconv"
	"strings"
)

// CompoundQuery combines SELECT statements with a set operator such as UNION.
// Operands are parenthesized, and ORDER BY / LIMIT / OFFSET apply to the
// combined result.
type CompoundQuery struct {
	left      Query
	operator  string
	right     Query
	orderBy   []OrderTerm
	limit     int
	hasLimit  bool
	offset    int
	hasOffset bool
}

// Union combines this query with another using UNION.
func (q SelectQuery) Union(other Query) CompoundQuery {
	return CompoundQuery{left: q, operator: "UNION", right: other}
}

// UnionAll combines this query with another using UNION ALL.
func (q SelectQuery) UnionAll(other Query) CompoundQuery {
	return CompoundQuery{left: q, operator: "UNION ALL", right: other}
}

// Intersect combines this query with another using INTERSECT.
func (q SelectQuery) Intersect(other Query) CompoundQuery {
	return CompoundQuery{left: q, operator: "INTERSECT", right: other}
}

// Except combines this query with another using EXCEPT.
func (q SelectQuery) Except(other Query) CompoundQuery {
	return CompoundQuery{left: q, operator: "EXCEPT", right: other}
}

// Union appends another UNION operand.
func (q CompoundQuery) Union(other Query) CompoundQuery {
	return CompoundQuery{left: q, operator: "UNION", right: other}
}

// UnionAll appends another UNION ALL operand.
func (q CompoundQuery) UnionAll(other Query) CompoundQuery {
	return CompoundQuery{left: q, operator: "UNION ALL", right: other}
}

// Intersect appends another INTERSECT operand.
func (q CompoundQuery) Intersect(other Query) CompoundQuery {
	return CompoundQuery{left: q, operator: "INTERSECT", right: other}
}

// Except appends another EXCEPT operand.
func (q CompoundQuery) Except(other Query) CompoundQuery {
	return CompoundQuery{left: q, operator: "EXCEPT", right: other}
}

// OrderBy adds ORDER BY terms applied to the combined result.
func (q CompoundQuery) OrderBy(terms ...OrderTerm) CompoundQuery {
	q.orderBy = append(q.orderBy, terms...)
	return q
}

// Limit sets the LIMIT clause of the combined result.
func (q CompoundQuery) Limit(n int) CompoundQuery {
	q.limit = n
	q.hasLimit = true
	return q
}

// Offset sets the OFFSET clause of the combined result.
func (q CompoundQuery) Offset(n int) CompoundQuery {
	q.offset = n
	q.hasOffset = true
	return q
}

// SQLSource marks CompoundQuery as a subquery source.
func (q CompoundQuery) SQLSource() SourceExpression { return SourceExpression{Query: q} }

// ToSQL compiles the compound query.
func (q CompoundQuery) ToSQL(dialect Dialect) (string, []any, error) {
	return q.compile(NewCompiler(dialect))
}

func (q CompoundQuery) compile(c *Compiler) (string, []any, error) {
	return c.CompileCompound(q)
}

// CompileCompound compiles a set-operation query.
func (c *Compiler) CompileCompound(q CompoundQuery) (string, []any, error) {
	leftSQL, args, err := c.compileOperand(q.left)
	if err != nil {
		return "", nil, err
	}
	rightSQL, rightArgs, err := c.compileOperand(q.right)
	if err != nil {
		return "", nil, err
	}
	args = append(args, rightArgs...)

	var sb strings.Builder
	sb.WriteString(leftSQL)
	sb.WriteString(" ")
	sb.WriteString(q.operator)
	sb.WriteString(" ")
	sb.WriteString(rightSQL)
	if len(q.orderBy) > 0 {
		sb.WriteString(" ORDER BY ")
		for i, term := range q.orderBy {
			if i > 0 {
				sb.WriteString(", ")
			}
			termSQL, termArgs, err := c.CompileExpression(term.Expr)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(termSQL)
			if term.Desc {
				sb.WriteString(" DESC")
			}
			args = append(args, termArgs...)
		}
	}
	if q.hasLimit {
		sb.WriteString(" LIMIT ")
		sb.WriteString(strconv.Itoa(q.limit))
	}
	if q.hasOffset {
		sb.WriteString(" OFFSET ")
		sb.WriteString(strconv.Itoa(q.offset))
	}
	return sb.String(), args, nil
}

// compileOperand compiles one side of a set operation. Nested compounds stay
// unparenthesized so chained unions read naturally; plain SELECTs are wrapped
// to keep their own clauses scoped.
func (c *Compiler) compileOperand(query Query) (string, []any, error) {
	sql, args, err := c.compileSubquery(query)
	if err != nil {
		return "", nil, err
	}
	if _, ok := query.(CompoundQuery); ok {
		return sql, args, nil
	}
	return "(" + sql + ")", args, nil
}
