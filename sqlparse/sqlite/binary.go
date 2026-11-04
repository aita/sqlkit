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

// negatedOps are the two-word operators rqlite tokenizes as one token with no
// precedence (NOT LIKE, IS NOT, ...): it parses their right operand at the
// lowest level, so "b NOT LIKE 'x%' AND a = 2" comes out as
// "b NOT LIKE ('x%' AND a = 2)". SQLite binds them like LIKE / IS.
var negatedOps = map[rsql.Token]bool{
	rsql.NOTLIKE: true, rsql.ISNOT: true, rsql.NOTREGEXP: true,
	rsql.NOTGLOB: true, rsql.NOTMATCH: true,
}

// absorbsOuter reports whether e, as the right operand of a comparison-level
// operator, must have swallowed operators that bind no tighter than the
// comparison itself — a grouping SQLite would never produce without
// parentheses (rqlite drops a ParenExpr's grouping into one, so an explicit
// "(...)" is a ParenExpr and not matched here).
func absorbsOuter(e rsql.Expr) bool {
	switch x := e.(type) {
	case *rsql.BinaryExpr:
		return x.Op.Precedence() <= rsql.GE.Precedence()
	case *rsql.Null:
		return true
	}
	return false
}

func convertBinary(e *rsql.BinaryExpr) (sql.Expression, error) {
	if negatedOps[e.Op] && absorbsOuter(e.Y) {
		return nil, sqlparse.Unsupported("negated comparison whose right operand rqlite mis-groups")
	}
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
	// x IS TRUE / IS FALSE test truthiness (any nonzero number is TRUE), which
	// IS [NOT] DISTINCT FROM a bound boolean does not express.
	if _, ok := rightNode.(*rsql.BoolLit); ok {
		return nil, sqlparse.Unsupported("IS [NOT] TRUE/FALSE")
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
	// rqlite parses the range's low bound at the lowest level, so in
	// "a BETWEEN 1 AND 2 AND b = 'x'" it takes "1 AND 2" as the low bound and
	// "b = 'x'" as the high one; likewise "BETWEEN 1 AND 2 = 1" puts the
	// comparison inside the high bound. Keep such statements raw.
	if lb, ok := rng.X.(*rsql.BinaryExpr); ok && lb.Op.Precedence() <= rsql.GE.Precedence() {
		return nil, sqlparse.Unsupported("BETWEEN bounds rqlite mis-groups")
	}
	if absorbsOuter(rng.Y) {
		return nil, sqlparse.Unsupported("BETWEEN bounds rqlite mis-groups")
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
		// The table / table-function forms of IN are not modeled.
		return nil, sqlparse.Unsupportedf("IN operand %T", e.Y)
	}
	// IN (SELECT ...): rqlite carries the subquery as a single SelectExpr element.
	if len(list.Exprs) == 1 {
		if sub, ok := list.Exprs[0].(rsql.SelectExpr); ok {
			query, err := convertSelect(sub.SelectStatement)
			if err != nil {
				return nil, err
			}
			if e.Op == rsql.NOTIN {
				return sql.NotInQuery(left, query), nil
			}
			return sql.InQuery(left, query), nil
		}
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
