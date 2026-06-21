package sql

import (
	"strconv"
	"strings"
)

// CompoundStmt is the immutable AST for a set operation (UNION/INTERSECT/EXCEPT)
// combining two query operands. Operands are parenthesized as needed, and
// ORDER BY / LIMIT / OFFSET apply to the combined result. Build one with a
// SelectBuilder/CompoundBuilder set-operation method.
type CompoundStmt struct {
	posField
	Left      Query
	Operator  string
	Right     Query
	OrderBy   []OrderTerm
	Limit     int
	HasLimit  bool
	Offset    int
	HasOffset bool
}

// SQLSource marks CompoundStmt as a subquery source.
func (q CompoundStmt) SQLSource() SourceExpression { return SourceExpression{Query: q} }

// ToSQL compiles the compound statement.
func (q CompoundStmt) ToSQL(dialect Dialect) (string, []any, error) {
	return q.compile(NewCompiler(dialect))
}

func (q CompoundStmt) compile(c *Compiler) (string, []any, error) {
	return c.CompileCompound(q)
}

func (q CompoundStmt) setPos(pos int) Query { q.NodePos = pos; return q }

// Union combines this query with another using UNION.
func (b SelectBuilder) Union(other Query) CompoundBuilder { return compound(b.s, "UNION", other) }

// UnionAll combines this query with another using UNION ALL.
func (b SelectBuilder) UnionAll(other Query) CompoundBuilder {
	return compound(b.s, "UNION ALL", other)
}

// Intersect combines this query with another using INTERSECT.
func (b SelectBuilder) Intersect(other Query) CompoundBuilder {
	return compound(b.s, "INTERSECT", other)
}

// IntersectAll combines this query with another using INTERSECT ALL, which keeps
// duplicate rows (the multiset intersection) rather than collapsing them.
func (b SelectBuilder) IntersectAll(other Query) CompoundBuilder {
	return compound(b.s, "INTERSECT ALL", other)
}

// Except combines this query with another using EXCEPT.
func (b SelectBuilder) Except(other Query) CompoundBuilder { return compound(b.s, "EXCEPT", other) }

// ExceptAll combines this query with another using EXCEPT ALL, the multiset
// difference: a row in the left result is dropped once per matching right row
// rather than entirely.
func (b SelectBuilder) ExceptAll(other Query) CompoundBuilder {
	return compound(b.s, "EXCEPT ALL", other)
}

// CompoundBuilder is the fluent constructor for a CompoundStmt, separate from the
// AST data for the same reason as SelectBuilder.
type CompoundBuilder struct{ s CompoundStmt }

func compound(left Query, operator string, right Query) CompoundBuilder {
	return CompoundBuilder{s: CompoundStmt{Left: left, Operator: operator, Right: astOf(right)}}
}

// Stmt returns the built set-operation statement — the pure AST value.
func (b CompoundBuilder) Stmt() CompoundStmt { return b.s }

// Union appends another UNION operand.
func (b CompoundBuilder) Union(other Query) CompoundBuilder { return compound(b.s, "UNION", other) }

// UnionAll appends another UNION ALL operand.
func (b CompoundBuilder) UnionAll(other Query) CompoundBuilder {
	return compound(b.s, "UNION ALL", other)
}

// Intersect appends another INTERSECT operand.
func (b CompoundBuilder) Intersect(other Query) CompoundBuilder {
	return compound(b.s, "INTERSECT", other)
}

// IntersectAll appends another INTERSECT ALL operand.
func (b CompoundBuilder) IntersectAll(other Query) CompoundBuilder {
	return compound(b.s, "INTERSECT ALL", other)
}

// Except appends another EXCEPT operand.
func (b CompoundBuilder) Except(other Query) CompoundBuilder { return compound(b.s, "EXCEPT", other) }

// ExceptAll appends another EXCEPT ALL operand.
func (b CompoundBuilder) ExceptAll(other Query) CompoundBuilder {
	return compound(b.s, "EXCEPT ALL", other)
}

// OrderBy adds ORDER BY terms applied to the combined result.
func (b CompoundBuilder) OrderBy(terms ...OrderTerm) CompoundBuilder {
	b.s.OrderBy = append(append([]OrderTerm(nil), b.s.OrderBy...), terms...)
	return b
}

// Limit sets the LIMIT clause of the combined result.
func (b CompoundBuilder) Limit(n int) CompoundBuilder {
	b.s.Limit = n
	b.s.HasLimit = true
	return b
}

// Offset sets the OFFSET clause of the combined result.
func (b CompoundBuilder) Offset(n int) CompoundBuilder {
	b.s.Offset = n
	b.s.HasOffset = true
	return b
}

// SQLSource marks the building compound query as a subquery source.
func (b CompoundBuilder) SQLSource() SourceExpression { return b.s.SQLSource() }

// ToSQL compiles the building compound statement.
func (b CompoundBuilder) ToSQL(dialect Dialect) (string, []any, error) { return b.s.ToSQL(dialect) }

func (b CompoundBuilder) compile(c *Compiler) (string, []any, error) { return b.s.compile(c) }

func (b CompoundBuilder) buildStmt() Query { return b.s }

func (b CompoundBuilder) setPos(pos int) Query { b.s.NodePos = pos; return b }

// CompileCompound compiles a set-operation query.
func (c *Compiler) CompileCompound(q CompoundStmt) (string, []any, error) {
	leftSQL, args, err := c.compileOperand(q.Left, q.Operator)
	if err != nil {
		return "", nil, err
	}
	rightSQL, rightArgs, err := c.compileOperand(q.Right, q.Operator)
	if err != nil {
		return "", nil, err
	}
	args = append(args, rightArgs...)

	var sb strings.Builder
	sb.WriteString(leftSQL)
	sb.WriteString(" ")
	sb.WriteString(q.Operator)
	sb.WriteString(" ")
	sb.WriteString(rightSQL)
	if len(q.OrderBy) > 0 {
		sb.WriteString(" ORDER BY ")
		for i, term := range q.OrderBy {
			if i > 0 {
				sb.WriteString(", ")
			}
			termSQL, termArgs, err := c.CompileExpression(term.Expr)
			if err != nil {
				return "", nil, err
			}
			sb.WriteString(termSQL)
			if err := c.writeOrderModifiers(&sb, term); err != nil {
				return "", nil, err
			}
			args = append(args, termArgs...)
		}
	}
	if q.HasLimit {
		sb.WriteString(" LIMIT ")
		sb.WriteString(strconv.Itoa(q.Limit))
	}
	if q.HasOffset {
		sb.WriteString(" OFFSET ")
		sb.WriteString(strconv.Itoa(q.Offset))
	}
	return sb.String(), args, nil
}

// compileOperand compiles one side of a set operation, given the enclosing
// operator. A nested compound stays unparenthesized only when it shares that
// operator — UNION/UNION ALL or INTERSECT chains are left-associative and read
// naturally without parentheses. Mixing operators needs explicit parentheses to
// preserve left-to-right grouping, since INTERSECT binds tighter than
// UNION/EXCEPT (so a UNION b INTERSECT c would parse as a UNION (b INTERSECT c)).
// A nested compound that carries its own ORDER BY / LIMIT / OFFSET is also always
// wrapped: those clauses bind to that nested result alone, so dropping the
// parentheses would re-scope them to the whole chain (and emit invalid SQL with an
// ORDER BY mid-chain). Plain SELECTs are always wrapped to keep their own clauses
// scoped.
func (c *Compiler) compileOperand(query Query, parentOperator string) (string, []any, error) {
	sql, args, err := c.compileSubquery(query)
	if err != nil {
		return "", nil, err
	}
	if sub, ok := astOf(query).(CompoundStmt); ok {
		if sub.Operator == parentOperator && len(sub.OrderBy) == 0 && !sub.HasLimit && !sub.HasOffset {
			return sql, args, nil
		}
		return "(" + sql + ")", args, nil
	}
	return "(" + sql + ")", args, nil
}

// builder is implemented by the fluent builders, each returning the immutable
// statement AST it has assembled.
type builder interface{ buildStmt() Query }

// astOf normalizes a query to its pure AST value: a builder yields the statement
// it has assembled, any other query passes through. It lets a builder be used in
// a subquery (or executed) position while the structural switches (Walk,
// compileOperand) and introspection need only handle the statement types.
func astOf(q Query) Query {
	if b, ok := q.(builder); ok {
		return b.buildStmt()
	}
	return q
}

// Unwrap returns the underlying statement AST of a builder, or q unchanged when
// it is already a statement. Introspection that type-switches on the concrete
// statement types (querytrace) calls it so a builder and its built statement are
// treated alike.
func Unwrap(q Query) Query { return astOf(q) }
