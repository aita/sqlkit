package redact

import (
	"github.com/aita/sqlkit/analyze"
	"github.com/aita/sqlkit/sql"
)

// rewriteExpr returns e with the string literals tied to a sensitive column
// masked. It recurses through every operand so a pattern nested in a composite
// predicate is reached, and into embedded subqueries (each with its own scope),
// and applies masking at the comparison and membership nodes where a column meets
// a literal. Unrecognized nodes are returned unchanged.
func (m *Masker) rewriteExpr(e sql.Expression, scope []analyze.SourceRef) sql.Expression {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case sql.BinaryExpression:
		x.Left = m.rewriteExpr(x.Left, scope)
		x.Right = m.rewriteExpr(x.Right, scope)
		if m.sensitiveColumn(x.Left, scope) {
			x.Right = m.maskValue(x.Right)
		} else if m.sensitiveColumn(x.Right, scope) {
			x.Left = m.maskValue(x.Left)
		}
		return x
	case sql.DistinctPredicate:
		x.Left = m.rewriteExpr(x.Left, scope)
		x.Right = m.rewriteExpr(x.Right, scope)
		if m.sensitiveColumn(x.Left, scope) {
			x.Right = m.maskValue(x.Right)
		} else if m.sensitiveColumn(x.Right, scope) {
			x.Left = m.maskValue(x.Left)
		}
		return x
	case sql.InExpression:
		x.Left = m.rewriteExpr(x.Left, scope)
		if len(x.Items) > 0 {
			items := clone(x.Items)
			sensitive := m.sensitiveColumn(x.Left, scope)
			for i := range items {
				if sensitive {
					items[i] = m.maskValue(items[i])
				} else {
					items[i] = m.rewriteExpr(items[i], scope)
				}
			}
			x.Items = items
		}
		if x.Subquery != nil {
			x.Subquery = m.rewriteQuery(x.Subquery)
		}
		return x
	case sql.BetweenExpression:
		x.Operand = m.rewriteExpr(x.Operand, scope)
		x.Low = m.rewriteExpr(x.Low, scope)
		x.High = m.rewriteExpr(x.High, scope)
		if m.sensitiveColumn(x.Operand, scope) {
			x.Low = m.maskValue(x.Low)
			x.High = m.maskValue(x.High)
		}
		return x
	case sql.ILikeExpression:
		x.Left = m.rewriteExpr(x.Left, scope)
		x.Pattern = m.rewriteExpr(x.Pattern, scope)
		if m.sensitiveColumn(x.Left, scope) {
			x.Pattern = m.maskValue(x.Pattern)
		}
		return x
	case sql.RegexpExpression:
		x.Left = m.rewriteExpr(x.Left, scope)
		x.Pattern = m.rewriteExpr(x.Pattern, scope)
		if m.sensitiveColumn(x.Left, scope) {
			x.Pattern = m.maskValue(x.Pattern)
		}
		return x
	case sql.SimilarToExpression:
		x.Left = m.rewriteExpr(x.Left, scope)
		x.Pattern = m.rewriteExpr(x.Pattern, scope)
		if m.sensitiveColumn(x.Left, scope) {
			x.Pattern = m.maskValue(x.Pattern)
		}
		return x
	case sql.QuantifiedArrayExpression:
		x.Left = m.rewriteExpr(x.Left, scope)
		x.Array = m.rewriteExpr(x.Array, scope)
		if m.sensitiveColumn(x.Left, scope) {
			x.Array = m.maskArray(x.Array)
		}
		return x
	case sql.LogicalExpression:
		x.Operands = m.rewriteExprs(x.Operands, scope)
		return x
	case sql.NotExpression:
		x.Operand = m.rewriteExpr(x.Operand, scope)
		return x
	case sql.UnaryExpression:
		x.Operand = m.rewriteExpr(x.Operand, scope)
		return x
	case sql.NullPredicate:
		x.Operand = m.rewriteExpr(x.Operand, scope)
		return x
	case sql.QuantifiedExpression:
		x.Left = m.rewriteExpr(x.Left, scope)
		if x.Query != nil {
			x.Query = m.rewriteQuery(x.Query)
		}
		return x
	case sql.ConcatExpression:
		x.Operands = m.rewriteExprs(x.Operands, scope)
		return x
	case sql.RowExpression:
		x.Elements = m.rewriteExprs(x.Elements, scope)
		return x
	case sql.ArrayExpression:
		x.Elements = m.rewriteExprs(x.Elements, scope)
		return x
	case sql.FuncExpression:
		x.Args = m.rewriteExprs(x.Args, scope)
		return x
	case sql.CastExpression:
		x.Operand = m.rewriteExpr(x.Operand, scope)
		return x
	case sql.CollateExpression:
		x.Operand = m.rewriteExpr(x.Operand, scope)
		return x
	case sql.FieldExpression:
		x.Operand = m.rewriteExpr(x.Operand, scope)
		return x
	case sql.CaseExpression:
		x.Operand = m.rewriteExpr(x.Operand, scope)
		if len(x.Whens) > 0 {
			whens := clone(x.Whens)
			for i := range whens {
				whens[i].Condition = m.rewriteExpr(whens[i].Condition, scope)
				whens[i].Result = m.rewriteExpr(whens[i].Result, scope)
			}
			x.Whens = whens
		}
		x.ElseValue = m.rewriteExpr(x.ElseValue, scope)
		return x
	case sql.ExistsExpression:
		if x.Query != nil {
			x.Query = m.rewriteQuery(x.Query)
		}
		return x
	case sql.ScalarSubqueryExpression:
		if x.Query != nil {
			x.Query = m.rewriteQuery(x.Query)
		}
		return x
	default:
		// Leaf nodes (ColumnExpression, ValueExpression, RawExpression, ...) carry
		// no operand to rewrite.
		return e
	}
}

// rewriteExprs rewrites each expression in es, returning a fresh slice so the
// input query's backing array is never mutated.
func (m *Masker) rewriteExprs(es []sql.Expression, scope []analyze.SourceRef) []sql.Expression {
	if len(es) == 0 {
		return es
	}
	out := clone(es)
	for i := range out {
		out[i] = m.rewriteExpr(out[i], scope)
	}
	return out
}

// sensitiveColumn reports whether e is a column reference that resolves, in scope,
// to a policy-sensitive column.
func (m *Masker) sensitiveColumn(e sql.Expression, scope []analyze.SourceRef) bool {
	col, ok := columnOf(e)
	return ok && m.isSensitive(scope, col)
}

// isSensitive resolves col against the scope and reports whether it is a policy
// column. It fails safe: an unqualified column that is ambiguous across in-scope
// tables is treated as sensitive when any candidate table marks it PII.
func (m *Masker) isSensitive(scope []analyze.SourceRef, col analyze.ColumnRef) bool {
	res := analyze.ResolveColumn(scope, col)
	if res.Status == analyze.Resolved {
		return m.policy.IsSensitive(res.Schema.String(), res.Table.String(), col.Name.String())
	}
	// Unknown or Ambiguous: fail safe — mask if any in-scope table marks the
	// column sensitive. Under executable SQL an unqualified column belongs to a
	// single in-scope table, so this masks the right one without over-masking.
	for _, s := range scope {
		if s.Table != "" && m.policy.IsSensitive(s.Schema.String(), s.Table.String(), col.Name.String()) {
			return true
		}
	}
	return false
}

// maskValue replaces a string literal with the mask value, leaving every other
// expression — a number, a bind placeholder, a column, an expression — untouched.
func (m *Masker) maskValue(e sql.Expression) sql.Expression {
	if v, ok := e.(sql.ValueExpression); ok {
		if _, isString := v.Value.(string); isString {
			return sql.Val(m.mask)
		}
	}
	return e
}

// maskArray masks the string elements of an ARRAY[...] constructor, for the
// "col = ANY(ARRAY['a','b'])" form; a non-array operand is unchanged.
func (m *Masker) maskArray(e sql.Expression) sql.Expression {
	arr, ok := e.(sql.ArrayExpression)
	if !ok {
		return e
	}
	elems := clone(arr.Elements)
	for i := range elems {
		elems[i] = m.maskValue(elems[i])
	}
	arr.Elements = elems
	return arr
}

// columnOf returns the column reference a ColumnExpression names, reporting
// ok=false for any other expression. The reference itself comes from analyze, so
// the masker resolves a column the same way the analysis layer does.
func columnOf(e sql.Expression) (analyze.ColumnRef, bool) {
	c, ok := e.(sql.ColumnExpression)
	if !ok {
		return analyze.ColumnRef{}, false
	}
	return analyze.ColumnRefOf(c), true
}
