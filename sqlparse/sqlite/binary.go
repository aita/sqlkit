package sqlite

import (
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	rsql "github.com/rqlite/sql"
)

// simpleBinaryOps maps the rqlite operator tokens that translate directly to a
// sql.BinaryExpression operator string. Operators with their own AST node
// (IN, BETWEEN, IS, AND/OR, ...) are handled explicitly in convertBinary.
var simpleBinaryOps = map[rsql.Token]string{
	rsql.PLUS:   "+",
	rsql.MINUS:  "-",
	rsql.STAR:   "*",
	rsql.SLASH:  "/",
	rsql.REM:    "%",
	rsql.EQ:     "=",
	rsql.NE:     "<>",
	rsql.LT:     "<",
	rsql.LE:     "<=",
	rsql.GT:     ">",
	rsql.GE:     ">=",
	rsql.BITAND: "&",
	rsql.BITOR:  "|",
	rsql.LSHIFT: "<<",
	rsql.RSHIFT: ">>",
	rsql.LIKE:   "LIKE",
}

func convertBinary(e *rsql.BinaryExpr) (sql.Expression, error) {
	// IN / NOT IN take a list (or, unsupported here, a subquery) on the right, so
	// the right operand is not a plain scalar expression.
	switch e.Op {
	case rsql.IN, rsql.NOTIN:
		return convertIn(e)
	}

	left, err := convertExpr(e.X)
	if err != nil {
		return nil, err
	}

	switch e.Op {
	case rsql.AND:
		right, err := convertExpr(e.Y)
		if err != nil {
			return nil, err
		}
		return sql.And(left, right), nil
	case rsql.OR:
		right, err := convertExpr(e.Y)
		if err != nil {
			return nil, err
		}
		return sql.Or(left, right), nil
	case rsql.CONCAT:
		right, err := convertExpr(e.Y)
		if err != nil {
			return nil, err
		}
		return sql.Concat(left, right), nil
	case rsql.NOTLIKE:
		right, err := convertExpr(e.Y)
		if err != nil {
			return nil, err
		}
		return sql.NotLike(left, right), nil
	case rsql.REGEXP:
		right, err := convertExpr(e.Y)
		if err != nil {
			return nil, err
		}
		return sql.Regexp(left, right), nil
	case rsql.NOTREGEXP:
		right, err := convertExpr(e.Y)
		if err != nil {
			return nil, err
		}
		return sql.NotRegexp(left, right), nil
	case rsql.IS:
		return convertIs(left, e.Y, false)
	case rsql.ISNOT:
		return convertIs(left, e.Y, true)
	case rsql.BETWEEN, rsql.NOTBETWEEN:
		return convertBetween(left, e.Y, e.Op == rsql.NOTBETWEEN)
	}

	if op, ok := simpleBinaryOps[e.Op]; ok {
		right, err := convertExpr(e.Y)
		if err != nil {
			return nil, err
		}
		return sql.BinaryExpression{Left: left, Operator: op, Right: right}, nil
	}
	return nil, sqlparse.Unsupportedf("binary operator %s", e.Op)
}

// convertIs maps SQLite's null-safe IS / IS NOT. Against NULL it is the idiomatic
// IS NULL / IS NOT NULL; otherwise it is IS [NOT] DISTINCT FROM.
func convertIs(left sql.Expression, rightNode rsql.Expr, negate bool) (sql.Expression, error) {
	if _, ok := rightNode.(*rsql.NullLit); ok {
		if negate {
			return sql.IsNotNull(left), nil
		}
		return sql.IsNull(left), nil
	}
	right, err := convertExpr(rightNode)
	if err != nil {
		return nil, err
	}
	if negate {
		return sql.IsDistinctFrom(left, right), nil
	}
	return sql.IsNotDistinctFrom(left, right), nil
}

func convertBetween(left sql.Expression, rightNode rsql.Expr, negate bool) (sql.Expression, error) {
	rng, ok := rightNode.(*rsql.Range)
	if !ok {
		return nil, sqlparse.Unsupportedf("BETWEEN range %T", rightNode)
	}
	low, err := convertExpr(rng.X)
	if err != nil {
		return nil, err
	}
	high, err := convertExpr(rng.Y)
	if err != nil {
		return nil, err
	}
	if negate {
		return sql.NotBetween(left, low, high), nil
	}
	return sql.Between(left, low, high), nil
}

func convertIn(e *rsql.BinaryExpr) (sql.Expression, error) {
	left, err := convertExpr(e.X)
	if err != nil {
		return nil, err
	}
	list, ok := e.Y.(*rsql.ExprList)
	if !ok {
		// IN (SELECT ...) and table/table-function forms are not modeled yet.
		return nil, sqlparse.Unsupportedf("IN operand %T", e.Y)
	}
	items := make([]any, 0, len(list.Exprs))
	for _, item := range list.Exprs {
		expr, err := convertExpr(item)
		if err != nil {
			return nil, err
		}
		items = append(items, expr)
	}
	if e.Op == rsql.NOTIN {
		return sql.NotIn(left, items...), nil
	}
	return sql.In(left, items...), nil
}
