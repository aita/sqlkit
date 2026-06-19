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
		return sql.Col(e.Name), nil
	case *rsql.QualifiedRef:
		return convertQualifiedRef(e)
	case *rsql.StringLit:
		return sql.Val(e.Value), nil
	case *rsql.NumberLit:
		return convertNumberLit(e)
	case *rsql.NullLit:
		return sql.Val(nil), nil
	case *rsql.BoolLit:
		return sql.Val(e.Value), nil
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
		return convertUnary(e)
	case *rsql.BinaryExpr:
		return convertBinary(e)
	case *rsql.Null:
		operand, err := convertExpr(e.X)
		if err != nil {
			return nil, err
		}
		if e.Op == rsql.ISNULL {
			return sql.IsNull(operand), nil
		}
		return sql.IsNotNull(operand), nil
	case *rsql.Exists:
		return convertExists(e)
	case *rsql.Call:
		return convertCall(e)
	default:
		return nil, sqlparse.Unsupportedf("expression node %T", node)
	}
}

func convertQualifiedRef(ref *rsql.QualifiedRef) (sql.Expression, error) {
	if ref.Star.IsValid() {
		return nil, sqlparse.Unsupported("'*' outside the select list")
	}
	parts := make([]string, 0, 3)
	if ref.Schema != nil {
		parts = append(parts, ref.Schema.Name)
	}
	parts = append(parts, ref.Table.Name, ref.Column.Name)
	return sql.Col(strings.Join(parts, ".")), nil
}

func convertNumberLit(lit *rsql.NumberLit) (sql.Expression, error) {
	// An integer binds as a value; a non-integer (3.14, 1e10, an exact decimal
	// too wide for a Go float) is emitted verbatim to stay exact — matching how
	// the postgres frontend handles numeric literals.
	if n, err := strconv.Atoi(lit.Value); err == nil {
		return sql.Val(n), nil
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

func convertCall(c *rsql.Call) (sql.Expression, error) {
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
