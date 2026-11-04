package sqlite

import (
	"strconv"
	"strings"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	rsql "github.com/rqlite/sql"
)

// convertExpr turns an rqlite expression node into a sqlkit/sql expression.
func convertExpr(node rsql.Expr) (sql.Expression, error) {
	switch e := node.(type) {
	case *rsql.Ident:
		return colAt(e.Name, e.NamePos), nil
	case *rsql.QualifiedRef:
		return convertQualifiedRef(e)
	case *rsql.StringLit:
		return valAt(e.Value, e.ValuePos), nil
	case *rsql.NumberLit:
		return convertNumberLit(e)
	case *rsql.NullLit:
		return valAt(nil, e.Pos), nil
	case *rsql.BoolLit:
		return valAt(e.Value, e.ValuePos), nil
	case *rsql.BindExpr:
		// A bind placeholder (?, ?N, :name, @name, $name). sqlkit numbers its own
		// placeholders from bound values, so pass the SQLite spelling through
		// verbatim, the way the postgres frontend passes $N through as Raw.
		return sql.Raw(e.Name), nil
	case *rsql.ParenExpr:
		// sqlkit re-parenthesizes from expression structure, so the explicit
		// grouping node can be unwrapped.
		return convertExpr(e.X)
	case *rsql.UnaryExpr:
		expr, err := convertUnary(e)
		if err != nil {
			return nil, err
		}
		return positioned(expr, e.OpPos), nil
	case *rsql.BinaryExpr:
		// rqlite binds a prefix NOT tighter than comparison operators, parsing
		// "NOT a = 1" as "(NOT a) = 1"; SQLite binds NOT below them (above
		// AND/OR). Restore SQLite's grouping before converting.
		if fixed := hoistNot(e); fixed != rsql.Expr(e) {
			return convertExpr(fixed)
		}
		expr, err := convertBinary(e)
		if err != nil {
			return nil, err
		}
		return positioned(expr, e.OpPos), nil
	case *rsql.Null:
		// rqlite binds a prefix NOT tighter than the postfix NULL tests, parsing
		// "NOT a IS NULL" as "(NOT a) IS NULL"; SQLite means NOT (a IS NULL).
		if u, ok := e.X.(*rsql.UnaryExpr); ok && u.Op == rsql.NOT {
			return convertExpr(&rsql.UnaryExpr{OpPos: u.OpPos, Op: rsql.NOT, X: &rsql.Null{X: u.X, Op: e.Op, OpPos: e.OpPos}})
		}
		if leftSpineHasNot(e.X) {
			return nil, sqlparse.Unsupported("NOT under a postfix NULL test")
		}
		operand, err := convertExpr(e.X)
		if err != nil {
			return nil, err
		}
		if e.Op == rsql.ISNULL {
			return positioned(sql.IsNull(operand), e.OpPos), nil
		}
		return positioned(sql.IsNotNull(operand), e.OpPos), nil
	case *rsql.Exists:
		expr, err := convertExists(e)
		if err != nil {
			return nil, err
		}
		return positioned(expr, e.Exists), nil
	case rsql.SelectExpr:
		// A parenthesized SELECT used as a value: a scalar subquery.
		query, err := convertSelect(e.SelectStatement)
		if err != nil {
			return nil, err
		}
		return positioned(sql.ScalarSubquery(query), e.Select), nil
	case *rsql.Call:
		expr, err := convertCall(e)
		if err != nil {
			return nil, err
		}
		return positioned(expr, e.Name.NamePos), nil
	case *rsql.CaseExpr:
		expr, err := convertCase(e)
		if err != nil {
			return nil, err
		}
		return positioned(expr, e.Case), nil
	default:
		return nil, sqlparse.Unsupportedf("expression node %T", node)
	}
}

func convertQualifiedRef(ref *rsql.QualifiedRef) (sql.Expression, error) {
	if ref.Star.IsValid() {
		return nil, sqlparse.Unsupported("'*' outside the select list")
	}
	parts := make([]string, 0, 3)
	pos := ref.Table.NamePos
	if ref.Schema != nil {
		parts = append(parts, ref.Schema.Name)
		pos = ref.Schema.NamePos
	}
	parts = append(parts, ref.Table.Name, ref.Column.Name)
	return colAt(strings.Join(parts, "."), pos), nil
}

func convertNumberLit(lit *rsql.NumberLit) (sql.Expression, error) {
	// An integer binds as a value; a non-integer (3.14, 1e10, an exact decimal
	// too wide for a Go float) is emitted verbatim to stay exact — matching how
	// the postgres frontend handles numeric literals.
	if n, err := strconv.Atoi(lit.Value); err == nil {
		return valAt(n, lit.ValuePos), nil
	}
	return sql.Raw(lit.Value), nil
}

func convertUnary(e *rsql.UnaryExpr) (sql.Expression, error) {
	operand, err := convertExpr(e.X)
	if err != nil {
		return nil, err
	}
	switch e.Op {
	case rsql.MINUS:
		return sql.Neg(operand), nil
	case rsql.PLUS:
		return sql.Pos(operand), nil
	case rsql.BITNOT:
		return sql.Unary("~", operand), nil
	case rsql.NOT:
		return sql.Not(operand), nil
	default:
		return nil, sqlparse.Unsupportedf("unary operator %s", e.Op)
	}
}

func convertExists(e *rsql.Exists) (sql.Expression, error) {
	query, err := convertSelect(e.Select)
	if err != nil {
		return nil, err
	}
	if e.Not.IsValid() {
		return sql.NotExists(query), nil
	}
	return sql.Exists(query), nil
}

// convertCase converts a CASE expression in either form: a searched CASE (no
// operand, boolean WHEN conditions) or a simple CASE (operand compared for
// equality against each WHEN value).
func convertCase(c *rsql.CaseExpr) (sql.Expression, error) {
	expr := sql.Case()
	if c.Operand != nil {
		operand, err := convertExpr(c.Operand)
		if err != nil {
			return nil, err
		}
		expr = sql.CaseOf(operand)
	}
	for _, block := range c.Blocks {
		cond, err := convertExpr(block.Condition)
		if err != nil {
			return nil, err
		}
		body, err := convertExpr(block.Body)
		if err != nil {
			return nil, err
		}
		expr = expr.When(cond, body)
	}
	if c.ElseExpr != nil {
		els, err := convertExpr(c.ElseExpr)
		if err != nil {
			return nil, err
		}
		expr = expr.Else(els)
	}
	return expr, nil
}

func convertCall(c *rsql.Call) (sql.Expression, error) {
	// FILTER (WHERE ...) on an aggregate is not modeled.
	if c.Filter != nil {
		return nil, sqlparse.Unsupported("FILTER clause")
	}

	fn, err := convertCallFunc(c)
	if err != nil {
		return nil, err
	}
	if c.Over != nil {
		return convertOver(fn, c.Over)
	}
	return fn, nil
}

// convertCallFunc converts the function/aggregate part of a call, without any
// OVER clause.
func convertCallFunc(c *rsql.Call) (sql.Expression, error) {
	name := c.Name.Name

	if strings.EqualFold(name, "count") {
		return convertCount(c)
	}
	if c.Star.IsValid() {
		return nil, sqlparse.Unsupportedf("%s(*)", name)
	}

	args := make([]any, 0, len(c.Args))
	for _, arg := range c.Args {
		expr, err := convertExpr(arg)
		if err != nil {
			return nil, err
		}
		args = append(args, expr)
	}
	if c.Distinct.IsValid() {
		return sql.DistinctFunc(name, args...), nil
	}
	return sql.Func(name, args...), nil
}

func convertCount(c *rsql.Call) (sql.Expression, error) {
	if c.Star.IsValid() {
		return sql.CountAll(), nil
	}
	if len(c.Args) != 1 {
		return nil, sqlparse.Unsupportedf("COUNT with %d arguments", len(c.Args))
	}
	expr, err := convertExpr(c.Args[0])
	if err != nil {
		return nil, err
	}
	target, ok := expr.(sql.Selection)
	if !ok {
		return nil, sqlparse.Unsupportedf("COUNT argument %T", c.Args[0])
	}
	if c.Distinct.IsValid() {
		return sql.CountDistinct(target), nil
	}
	return sql.Count(target), nil
}

// hoistNot rewrites a binary expression whose leftmost operand is a prefix
// NOT — "(NOT x) op y" as rqlite parses it — into SQLite's "NOT (x op y)", for
// every operator that binds tighter than NOT (everything but AND/OR). It
// returns e unchanged when no NOT needs moving.
func hoistNot(e *rsql.BinaryExpr) rsql.Expr {
	if e.Op == rsql.AND || e.Op == rsql.OR {
		return e
	}
	left := e.X
	if lb, ok := left.(*rsql.BinaryExpr); ok {
		left = hoistNot(lb)
	}
	u, ok := left.(*rsql.UnaryExpr)
	if !ok || u.Op != rsql.NOT {
		if left == e.X {
			return e
		}
		cp := *e
		cp.X = left
		return &cp
	}
	inner := *e
	inner.X = u.X
	var x rsql.Expr = &inner
	if fixed := hoistNot(&inner); fixed != rsql.Expr(&inner) {
		x = fixed
	}
	return &rsql.UnaryExpr{OpPos: u.OpPos, Op: rsql.NOT, X: x}
}

// leftSpineHasNot reports whether a prefix NOT sits on the left spine of a
// chain of binary operators — the shape rqlite produces for "NOT a = 1 ..."
// when an outer operator would need it regrouped.
func leftSpineHasNot(e rsql.Expr) bool {
	for {
		switch x := e.(type) {
		case *rsql.UnaryExpr:
			return x.Op == rsql.NOT
		case *rsql.BinaryExpr:
			e = x.X
		default:
			return false
		}
	}
}
