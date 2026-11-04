package sqlite

import (
	"strconv"
	"strings"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	rsql "github.com/rqlite/sql"
)

// setOperand is satisfied by the query builders that can start a set operation
// (SelectBuilder and CompoundBuilder), letting convertCompound chain operators
// without caring which concrete builder it holds.
type setOperand interface {
	Union(sql.Query) sql.CompoundBuilder
	UnionAll(sql.Query) sql.CompoundBuilder
	Intersect(sql.Query) sql.CompoundBuilder
	IntersectAll(sql.Query) sql.CompoundBuilder
	Except(sql.Query) sql.CompoundBuilder
	ExceptAll(sql.Query) sql.CompoundBuilder
}

// cteMaker is satisfied by the query builders that can become a CTE body.
type cteMaker interface {
	CTE(string, ...sql.CTEOption) sql.CTE
}

// convertSelect converts a SELECT statement, dispatching to convertCompound for
// set operations (UNION/INTERSECT/EXCEPT). Construction goes through the fluent
// SelectBuilder rather than a struct literal so a converted query can also serve
// as a CTE body or a set-operation operand.
func convertSelect(s *rsql.SelectStatement) (sql.Query, error) {
	if s == nil {
		return nil, sqlparse.Unsupported("nil SELECT")
	}
	if s.Compound != nil {
		return convertCompound(s)
	}
	if len(s.ValueLists) > 0 {
		return nil, sqlparse.Unsupported("VALUES statement")
	}

	b, err := buildSelectCore(s)
	if err != nil {
		return nil, err
	}
	return applySelectTail(b, s)
}

// buildSelectCore builds everything in a SELECT except the trailing ORDER BY /
// LIMIT / OFFSET, which a set operation applies to the combined result rather
// than to its first operand.
func buildSelectCore(s *rsql.SelectStatement) (sql.SelectBuilder, error) {
	columns, err := convertColumns(s.Columns)
	if err != nil {
		return sql.SelectBuilder{}, err
	}
	b := sql.Select(columns...)

	if s.Distinct.IsValid() {
		b = b.Distinct()
	}

	if s.Source != nil {
		base, joins, err := convertSource(s.Source)
		if err != nil {
			return sql.SelectBuilder{}, err
		}
		if base != nil {
			b = b.From(base)
		}
		for _, j := range joins {
			b = applyJoin(b, j)
		}
	}

	if s.WhereExpr != nil {
		where, err := convertExpr(s.WhereExpr)
		if err != nil {
			return sql.SelectBuilder{}, err
		}
		b = b.Where(where)
	}

	if len(s.GroupByExprs) > 0 {
		group := make([]sql.Expression, 0, len(s.GroupByExprs))
		for _, g := range s.GroupByExprs {
			expr, err := convertPositional(g)
			if err != nil {
				return sql.SelectBuilder{}, err
			}
			group = append(group, expr)
		}
		b = b.GroupBy(group...)
	}

	if s.HavingExpr != nil {
		having, err := convertExpr(s.HavingExpr)
		if err != nil {
			return sql.SelectBuilder{}, err
		}
		b = b.Having(having)
	}

	if len(s.Windows) > 0 {
		windows, err := convertNamedWindows(s.Windows)
		if err != nil {
			return sql.SelectBuilder{}, err
		}
		b = b.Window(windows...)
	}

	if s.WithClause != nil {
		ctes, err := convertWith(s.WithClause)
		if err != nil {
			return sql.SelectBuilder{}, err
		}
		b = b.WithCTE(ctes...)
	}

	return b, nil
}

// applySelectTail applies ORDER BY / LIMIT / OFFSET to a single SELECT.
func applySelectTail(b sql.SelectBuilder, s *rsql.SelectStatement) (sql.Query, error) {
	if len(s.OrderingTerms) > 0 {
		terms, err := convertOrderingTerms(s.OrderingTerms)
		if err != nil {
			return nil, err
		}
		b = b.OrderBy(terms...)
	}
	limitExpr, offsetExpr := s.LimitExpr, s.OffsetExpr
	if s.OffsetComma.IsValid() {
		// "LIMIT a, b" is SQLite's (MySQL-style) OFFSET a LIMIT b; rqlite keeps
		// the two expressions in source order.
		limitExpr, offsetExpr = s.OffsetExpr, s.LimitExpr
	}
	if limitExpr != nil {
		n, err := intLiteral(limitExpr, "LIMIT")
		if err != nil {
			return nil, err
		}
		b = b.Limit(n)
	}
	if offsetExpr != nil {
		n, err := intLiteral(offsetExpr, "OFFSET")
		if err != nil {
			return nil, err
		}
		b = b.Offset(n)
	}
	return b, nil
}

// convertCompound converts a set operation. rqlite nests the chain on the first
// operand: the outer statement holds the first SELECT plus the operator and a
// Compound pointing at the rest, while a trailing ORDER BY / LIMIT on the outer
// applies to the whole result. The chain is folded left so A UNION B UNION C
// builds as ((A) UNION B) UNION C.
func convertCompound(s *rsql.SelectStatement) (sql.Query, error) {
	first, err := buildSelectCore(s)
	if err != nil {
		return nil, err
	}

	var result sql.Query = first
	node := s
	for node.Compound != nil {
		right, err := convertCompoundOperand(node.Compound)
		if err != nil {
			return nil, err
		}
		operand, ok := result.(setOperand)
		if !ok {
			return nil, sqlparse.Unsupportedf("set-operation operand %T", result)
		}
		result, err = applySetOp(operand, node, right)
		if err != nil {
			return nil, err
		}
		node = node.Compound
	}

	// The trailing ORDER BY / LIMIT / OFFSET sits on the outermost statement and
	// applies to the combined result.
	if cb, ok := result.(sql.CompoundBuilder); ok {
		cb, err = applyCompoundTail(cb, s)
		if err != nil {
			return nil, err
		}
		return cb, nil
	}
	return result, nil
}

// convertCompoundOperand builds one operand of a set operation: a plain SELECT
// without its own compound/ORDER BY/LIMIT, which belong to the chain.
func convertCompoundOperand(s *rsql.SelectStatement) (sql.Query, error) {
	b, err := buildSelectCore(s)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func applySetOp(left setOperand, node *rsql.SelectStatement, right sql.Query) (sql.Query, error) {
	switch {
	case node.Union.IsValid():
		if node.UnionAll.IsValid() {
			return left.UnionAll(right), nil
		}
		return left.Union(right), nil
	case node.Intersect.IsValid():
		return left.Intersect(right), nil
	case node.Except.IsValid():
		return left.Except(right), nil
	default:
		return nil, sqlparse.Unsupported("unknown set operation")
	}
}

func applyCompoundTail(b sql.CompoundBuilder, s *rsql.SelectStatement) (sql.CompoundBuilder, error) {
	if len(s.OrderingTerms) > 0 {
		terms, err := convertOrderingTerms(s.OrderingTerms)
		if err != nil {
			return b, err
		}
		b = b.OrderBy(terms...)
	}
	limitExpr, offsetExpr := s.LimitExpr, s.OffsetExpr
	if s.OffsetComma.IsValid() {
		// "LIMIT a, b" is SQLite's (MySQL-style) OFFSET a LIMIT b; rqlite keeps
		// the two expressions in source order.
		limitExpr, offsetExpr = s.OffsetExpr, s.LimitExpr
	}
	if limitExpr != nil {
		n, err := intLiteral(limitExpr, "LIMIT")
		if err != nil {
			return b, err
		}
		b = b.Limit(n)
	}
	if offsetExpr != nil {
		n, err := intLiteral(offsetExpr, "OFFSET")
		if err != nil {
			return b, err
		}
		b = b.Offset(n)
	}
	return b, nil
}

// convertWith converts a WITH clause into CTEs. A recursive WITH marks every CTE
// recursive, matching how the sql package emits WITH RECURSIVE for the block.
func convertWith(wc *rsql.WithClause) ([]sql.CTE, error) {
	ctes := make([]sql.CTE, 0, len(wc.CTEs))
	for _, cte := range wc.CTEs {
		body, err := convertSelect(cte.Select)
		if err != nil {
			return nil, err
		}
		maker, ok := body.(cteMaker)
		if !ok {
			return nil, sqlparse.Unsupportedf("CTE body %T", body)
		}
		var opts []sql.CTEOption
		if len(cte.Columns) > 0 {
			names := make([]string, 0, len(cte.Columns))
			for _, col := range cte.Columns {
				names = append(names, col.Name)
			}
			opts = append(opts, sql.Columns(names...))
		}
		if wc.Recursive.IsValid() {
			opts = append(opts, sql.Recursive())
		}
		ctes = append(ctes, maker.CTE(cte.TableName.Name, opts...))
	}
	return ctes, nil
}

// applyJoin adds a flattened join to a SELECT builder.
func applyJoin(b sql.SelectBuilder, j sql.JoinClause) sql.SelectBuilder {
	switch {
	case j.Natural && j.Kind == "LEFT JOIN":
		return b.NaturalLeftJoin(j.Source)
	case j.Natural:
		return b.NaturalJoin(j.Source)
	case j.Using != nil && j.Kind == "LEFT JOIN":
		return b.LeftJoinUsing(j.Source, sql.IdentNames(j.Using)...)
	case j.Using != nil:
		return b.JoinUsing(j.Source, sql.IdentNames(j.Using)...)
	}
	switch j.Kind {
	case "LEFT JOIN":
		return b.LeftJoin(j.Source, j.On)
	case "CROSS JOIN":
		return b.CrossJoin(j.Source)
	default:
		return b.Join(j.Source, j.On)
	}
}

// convertColumns converts the SELECT result-column list.
func convertColumns(columns []*rsql.ResultColumn) ([]sql.Selection, error) {
	out := make([]sql.Selection, 0, len(columns))
	for _, col := range columns {
		selection, err := convertResultColumn(col)
		if err != nil {
			return nil, err
		}
		out = append(out, selection)
	}
	return out, nil
}

func convertResultColumn(col *rsql.ResultColumn) (sql.Selection, error) {
	// "SELECT *": an unqualified star.
	if col.Star.IsValid() {
		return sql.Star(), nil
	}
	// "SELECT tbl.*": a qualified star carried as a QualifiedRef with Star set.
	if ref, ok := col.Expr.(*rsql.QualifiedRef); ok && ref.Star.IsValid() {
		if ref.Schema != nil {
			return nil, sqlparse.Unsupported("schema-qualified '*'")
		}
		return sql.StarOf(ref.Table.Name), nil
	}

	expr, err := convertSelectionExpr(col.Expr)
	if err != nil {
		return nil, err
	}
	if col.Alias != nil {
		return sql.As(expr, col.Alias.Name), nil
	}
	return expr, nil
}

// convertSelectionExpr converts a result-column expression to a Selection. Most
// expression nodes are also valid selections; a bare literal is not (as a bound
// value it is only an Expression), so it is rendered as a raw constant — faithful
// and common in EXISTS (SELECT 1 ...). An expression that is neither errors.
func convertSelectionExpr(node rsql.Expr) (sql.Selection, error) {
	if lit, ok := literalSelection(node); ok {
		return lit, nil
	}
	expr, err := convertExpr(node)
	if err != nil {
		return nil, err
	}
	selection, ok := expr.(sql.Selection)
	if !ok {
		return nil, sqlparse.Unsupportedf("select-list expression %T", node)
	}
	return selection, nil
}

// literalSelection renders a literal result column as a raw SQL constant, which
// is a Selection (a bound value is not). It reports false for non-literals.
func literalSelection(node rsql.Expr) (sql.Selection, bool) {
	switch lit := node.(type) {
	case *rsql.NumberLit:
		return sql.Raw(lit.Value), true
	case *rsql.NullLit:
		return sql.Raw("NULL"), true
	case *rsql.BoolLit:
		if lit.Value {
			return sql.Raw("TRUE"), true
		}
		return sql.Raw("FALSE"), true
	case *rsql.StringLit:
		return sql.Raw("'" + strings.ReplaceAll(lit.Value, "'", "''") + "'"), true
	default:
		return nil, false
	}
}

// convertPositional converts a GROUP BY / ORDER BY item. A bare integer there
// is a column position (ORDER BY 2 sorts by the second output column), not a
// value, so it is kept as literal text: binding it as a parameter would make
// it a constant and silently drop the ordering or grouping.
func convertPositional(e rsql.Expr) (sql.Expression, error) {
	if lit, ok := e.(*rsql.NumberLit); ok {
		if _, err := strconv.Atoi(lit.Value); err == nil {
			return sql.Raw(lit.Value), nil
		}
	}
	return convertExpr(e)
}

func convertOrderingTerms(terms []*rsql.OrderingTerm) ([]sql.OrderTerm, error) {
	out := make([]sql.OrderTerm, 0, len(terms))
	for _, t := range terms {
		expr, err := convertPositional(t.X)
		if err != nil {
			return nil, err
		}
		term := sql.Asc(expr)
		if t.Desc.IsValid() {
			term = sql.Desc(expr)
		}
		switch {
		case t.NullsFirst.IsValid():
			term = term.NullsFirst()
		case t.NullsLast.IsValid():
			term = term.NullsLast()
		}
		out = append(out, term)
	}
	return out, nil
}

// intLiteral reads a LIMIT/OFFSET operand, which sqlkit models as a plain int.
func intLiteral(node rsql.Expr, clause string) (int, error) {
	lit, ok := node.(*rsql.NumberLit)
	if !ok {
		return 0, sqlparse.Unsupportedf("non-integer %s", clause)
	}
	n, err := strconv.Atoi(lit.Value)
	if err != nil {
		return 0, sqlparse.Unsupportedf("non-integer %s", clause)
	}
	return n, nil
}
