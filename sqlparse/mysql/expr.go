package mysql

import (
	"fmt"
	"strings"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/opcode"
)

// convertExpr turns a TiDB expression node into a sqlkit/sql expression, recording
// the node's byte offset in the source SQL so a caller can map the result back to
// its place in the original text.
func convertExpr(node ast.ExprNode) (sql.Expression, error) {
	if node == nil {
		return nil, sqlparse.Unsupported("nil expression")
	}
	// Parentheses carry no expression of their own: unwrap and keep the inner
	// node's position rather than the opening paren's.
	if p, ok := node.(*ast.ParenthesesExpr); ok {
		// sqlkit parenthesizes compound expressions itself; let it decide where
		// parentheses are needed.
		return convertExpr(p.Expr)
	}
	expr, err := convertExprNode(node)
	if err != nil {
		return nil, err
	}
	return positioned(expr, node), nil
}

// convertExprNode dispatches a non-nil, non-parenthesized expression node to its
// converter. convertExpr wraps it to attach the source position.
func convertExprNode(node ast.ExprNode) (sql.Expression, error) {
	switch e := node.(type) {
	case *ast.ColumnNameExpr:
		// The column's identifier shares the reference's position, so a qualified or
		// bare column name reports where it was written, not just the node.
		name, err := columnRefName(e.Name)
		if err != nil {
			return nil, err
		}
		c := sql.Col(name)
		c.Name = identAt(c.Name.Name, e)
		return c, nil
	case ast.ParamMarkerExpr:
		// A positional placeholder (?). sqlkit numbers its own placeholders from
		// bound values, so there is no native placeholder node; pass the MySQL
		// spelling through verbatim, which round-trips under MySQLDialect.
		return sql.Raw("?"), nil
	case ast.ValueExpr:
		return convertValue(e)
	case *ast.BinaryOperationExpr:
		return convertBinaryOp(e)
	case *ast.UnaryOperationExpr:
		return convertUnaryOp(e)
	case *ast.PatternLikeOrIlikeExpr:
		return convertLike(e)
	case *ast.PatternRegexpExpr:
		return convertRegexp(e)
	case *ast.PatternInExpr:
		return convertIn(e)
	case *ast.BetweenExpr:
		return convertBetween(e)
	case *ast.IsNullExpr:
		operand, err := convertExpr(e.Expr)
		if err != nil {
			return nil, err
		}
		if e.Not {
			return sql.IsNotNull(operand), nil
		}
		return sql.IsNull(operand), nil
	case *ast.CaseExpr:
		return convertCase(e)
	case *ast.FuncCallExpr:
		return convertFuncCall(e)
	case *ast.AggregateFuncExpr:
		return convertAggregate(e)
	case *ast.WindowFuncExpr:
		return convertWindowFunc(e)
	case *ast.FuncCastExpr:
		return convertCast(e)
	case *ast.SubqueryExpr:
		return convertScalarSubquery(e)
	case *ast.ExistsSubqueryExpr:
		return convertExists(e)
	case *ast.CompareSubqueryExpr:
		return convertCompareSubquery(e)
	case *ast.RowExpr:
		elems, err := convertExprAnys(e.Values)
		if err != nil {
			return nil, err
		}
		return sql.Row(elems...), nil
	case *ast.DefaultExpr:
		if e.Name != nil {
			return nil, sqlparse.Unsupported("DEFAULT(col)")
		}
		return sql.Raw("DEFAULT"), nil
	default:
		return nil, sqlparse.Unsupportedf("expression node %T", node)
	}
}

// columnRefName joins a ColumnName's qualified parts (schema.table.column) into
// the dotted name sqlkit's Col understands. sqlkit re-splits that name on '.' to
// recover the qualifier, so a single quoted identifier that itself contains a dot
// (`a.b`) cannot be told apart from the qualified a.b and would render wrongly; an
// individual part carrying a dot is reported unsupported so it round-trips as raw.
func columnRefName(name *ast.ColumnName) (string, error) {
	parts := make([]string, 0, 3)
	for _, p := range []string{name.Schema.O, name.Table.O, name.Name.O} {
		if p == "" {
			continue
		}
		if strings.Contains(p, ".") {
			return "", sqlparse.Unsupportedf("column identifier with a dot: %q", p)
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, "."), nil
}

// decimalDigits counts the digits in a numeric literal's text, ignoring a sign,
// decimal point, and exponent marker — the precision MySQL's DECIMAL bounds at 65.
func decimalDigits(text string) int {
	n := 0
	for _, r := range text {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
}

// convertValue maps a literal value. MySQL has no boolean literal type: TRUE and
// FALSE parse as the integers 1 and 0.
func convertValue(v ast.ValueExpr) (sql.Expression, error) {
	val := v.GetValue()
	switch x := val.(type) {
	case nil:
		return sql.Val(nil), nil
	case int64:
		return sql.Val(int(x)), nil
	case uint64:
		return sql.Val(int(x)), nil
	case float64:
		return sql.Val(x), nil
	case string:
		return sql.Val(x), nil
	case []byte:
		return sql.Val(string(x)), nil
	default:
		// Exact-decimal literals decode to the driver's decimal type, a
		// fmt.Stringer; keep its precise text (scale and all) in a Decimal rather
		// than rounding through a float.
		if s, ok := val.(fmt.Stringer); ok {
			text := s.String()
			// An empty hex/bit literal (x'' / b'') stringifies to "", which would
			// vanish from the output (and a Decimal never does). Render it as the
			// explicit empty hex literal so it stays valid SQL. A decimal value is
			// always non-empty, so this only catches the empty binary literal.
			if text == "" {
				return sql.Raw("x''"), nil
			}
			// A decimal beyond MySQL's 65-digit precision is out of range, and the
			// value driver can panic re-parsing such a literal; fall back to raw.
			if decimalDigits(text) > 65 {
				return nil, sqlparse.Unsupported("decimal literal out of range")
			}
			return sql.Val(Decimal(text)), nil
		}
		return nil, sqlparse.Unsupportedf("literal value %T", val)
	}
}

// binaryOperators maps the arithmetic, comparison and bitwise opcodes to the
// operator text sqlkit renders verbatim. Logical and null-safe operators are
// handled separately because they map to dedicated sql nodes.
var binaryOperators = map[opcode.Op]string{
	opcode.EQ:         "=",
	opcode.NE:         "<>",
	opcode.LT:         "<",
	opcode.LE:         "<=",
	opcode.GT:         ">",
	opcode.GE:         ">=",
	opcode.Plus:       "+",
	opcode.Minus:      "-",
	opcode.Mul:        "*",
	opcode.Div:        "/",
	opcode.Mod:        "%",
	opcode.IntDiv:     "DIV",
	opcode.And:        "&",
	opcode.Or:         "|",
	opcode.Xor:        "^",
	opcode.LeftShift:  "<<",
	opcode.RightShift: ">>",
}

func convertBinaryOp(e *ast.BinaryOperationExpr) (sql.Expression, error) {
	// AND / OR are associative; flatten a chain (parsed left-deep) into a single
	// multi-operand node so it matches a hand-built And/Or and renders flatter.
	switch e.Op {
	case opcode.LogicAnd:
		operands, err := flattenLogical(e, opcode.LogicAnd)
		if err != nil {
			return nil, err
		}
		return sql.And(operands...), nil
	case opcode.LogicOr:
		operands, err := flattenLogical(e, opcode.LogicOr)
		if err != nil {
			return nil, err
		}
		return sql.Or(operands...), nil
	}

	left, err := convertExpr(e.L)
	if err != nil {
		return nil, err
	}
	right, err := convertExpr(e.R)
	if err != nil {
		return nil, err
	}
	switch e.Op {
	case opcode.LogicXor:
		return sql.BinaryExpression{Left: left, Operator: "XOR", Right: right}, nil
	case opcode.NullEQ:
		// MySQL's null-safe equality <=> is IS NOT DISTINCT FROM.
		return sql.IsNotDistinctFrom(left, right), nil
	}
	if op, ok := binaryOperators[e.Op]; ok {
		return sql.BinaryExpression{Left: left, Operator: op, Right: right}, nil
	}
	return nil, sqlparse.Unsupportedf("binary operator %s", e.Op)
}

// flattenLogical collects the operands of a left-deep chain of the same logical
// operator (AND or OR) into a single, source-order slice.
func flattenLogical(e *ast.BinaryOperationExpr, op opcode.Op) ([]sql.Expression, error) {
	var operands []sql.Expression
	for _, side := range []ast.ExprNode{e.L, e.R} {
		if sub, ok := side.(*ast.BinaryOperationExpr); ok && sub.Op == op {
			nested, err := flattenLogical(sub, op)
			if err != nil {
				return nil, err
			}
			operands = append(operands, nested...)
			continue
		}
		expr, err := convertExpr(side)
		if err != nil {
			return nil, err
		}
		operands = append(operands, expr)
	}
	return operands, nil
}

func convertUnaryOp(e *ast.UnaryOperationExpr) (sql.Expression, error) {
	switch e.Op {
	case opcode.Minus:
		// Fold a negated numeric constant into a signed value.
		if v, ok := e.V.(ast.ValueExpr); ok {
			val, err := convertValue(v)
			if err != nil {
				return nil, err
			}
			switch n := val.(sql.ValueExpression).Value.(type) {
			case int:
				return sql.Val(-n), nil
			case float64:
				return sql.Val(-n), nil
			case Decimal:
				return sql.Val(Decimal("-" + string(n))), nil
			}
		}
		operand, err := convertExpr(e.V)
		if err != nil {
			return nil, err
		}
		return sql.Neg(operand), nil
	case opcode.Plus:
		return convertExpr(e.V)
	case opcode.Not, opcode.Not2:
		operand, err := convertExpr(e.V)
		if err != nil {
			return nil, err
		}
		return sql.Not(operand), nil
	case opcode.BitNeg:
		operand, err := convertExpr(e.V)
		if err != nil {
			return nil, err
		}
		return sql.Unary("~", operand), nil
	default:
		return nil, sqlparse.Unsupportedf("unary operator %s", e.Op)
	}
}

func convertLike(e *ast.PatternLikeOrIlikeExpr) (sql.Expression, error) {
	if e.EscapeExplicit {
		return nil, sqlparse.Unsupported("LIKE ... ESCAPE")
	}
	left, err := convertExpr(e.Expr)
	if err != nil {
		return nil, err
	}
	pattern, err := convertExpr(e.Pattern)
	if err != nil {
		return nil, err
	}
	// MySQL has no ILIKE; the parser only produces LIKE here.
	if e.Not {
		return sql.NotLike(left, pattern), nil
	}
	return sql.Like(left, pattern), nil
}

func convertRegexp(e *ast.PatternRegexpExpr) (sql.Expression, error) {
	left, err := convertExpr(e.Expr)
	if err != nil {
		return nil, err
	}
	pattern, err := convertExpr(e.Pattern)
	if err != nil {
		return nil, err
	}
	expr := sql.BinaryExpression{Left: left, Operator: "REGEXP", Right: pattern}
	if e.Not {
		return sql.Not(expr), nil
	}
	return expr, nil
}

func convertIn(e *ast.PatternInExpr) (sql.Expression, error) {
	left, err := convertExpr(e.Expr)
	if err != nil {
		return nil, err
	}
	if e.Sel != nil {
		sub, ok := e.Sel.(*ast.SubqueryExpr)
		if !ok {
			return nil, sqlparse.Unsupportedf("IN subquery %T", e.Sel)
		}
		query, err := convertSubquery(sub, nil)
		if err != nil {
			return nil, err
		}
		if e.Not {
			return sql.NotInQuery(left, query), nil
		}
		return sql.InQuery(left, query), nil
	}
	items, err := convertExprAnys(e.List)
	if err != nil {
		return nil, err
	}
	if e.Not {
		return sql.NotIn(left, items...), nil
	}
	return sql.In(left, items...), nil
}

func convertBetween(e *ast.BetweenExpr) (sql.Expression, error) {
	operand, err := convertExpr(e.Expr)
	if err != nil {
		return nil, err
	}
	low, err := convertExpr(e.Left)
	if err != nil {
		return nil, err
	}
	high, err := convertExpr(e.Right)
	if err != nil {
		return nil, err
	}
	if e.Not {
		return sql.NotBetween(operand, low, high), nil
	}
	return sql.Between(operand, low, high), nil
}

func convertCase(e *ast.CaseExpr) (sql.Expression, error) {
	c := sql.Case()
	if e.Value != nil {
		operand, err := convertExpr(e.Value)
		if err != nil {
			return nil, err
		}
		c = sql.CaseOf(operand)
	}
	for _, when := range e.WhenClauses {
		cond, err := convertExpr(when.Expr)
		if err != nil {
			return nil, err
		}
		result, err := convertExpr(when.Result)
		if err != nil {
			return nil, err
		}
		c = c.When(cond, result)
	}
	if e.ElseClause != nil {
		def, err := convertExpr(e.ElseClause)
		if err != nil {
			return nil, err
		}
		c = c.Else(def)
	}
	return c, nil
}

func convertScalarSubquery(e *ast.SubqueryExpr) (sql.Expression, error) {
	query, err := convertSubquery(e, nil)
	if err != nil {
		return nil, err
	}
	return sql.ScalarSubquery(query), nil
}

func convertExists(e *ast.ExistsSubqueryExpr) (sql.Expression, error) {
	sub, ok := e.Sel.(*ast.SubqueryExpr)
	if !ok {
		return nil, sqlparse.Unsupportedf("EXISTS operand %T", e.Sel)
	}
	query, err := convertSubquery(sub, nil)
	if err != nil {
		return nil, err
	}
	if e.Not {
		return sql.Not(sql.Exists(query)), nil
	}
	return sql.Exists(query), nil
}

func convertCompareSubquery(e *ast.CompareSubqueryExpr) (sql.Expression, error) {
	left, err := convertExpr(e.L)
	if err != nil {
		return nil, err
	}
	sub, ok := e.R.(*ast.SubqueryExpr)
	if !ok {
		return nil, sqlparse.Unsupportedf("quantified comparison operand %T", e.R)
	}
	query, err := convertSubquery(sub, nil)
	if err != nil {
		return nil, err
	}
	op, ok := binaryOperators[e.Op]
	if !ok {
		return nil, sqlparse.Unsupportedf("quantified comparison operator %s", e.Op)
	}
	if e.All {
		return sql.All(left, op, query), nil
	}
	return sql.Any(left, op, query), nil
}

// convertExprAnys converts expression nodes into a slice of any, the element
// type sqlkit's variadic builders (In, Row, Func, ...) accept.
func convertExprAnys(nodes []ast.ExprNode) ([]any, error) {
	out := make([]any, 0, len(nodes))
	for _, n := range nodes {
		expr, err := convertExpr(n)
		if err != nil {
			return nil, err
		}
		out = append(out, expr)
	}
	return out, nil
}
