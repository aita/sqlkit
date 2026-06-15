package postgres

import (
	"strconv"
	"strings"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/x/sqlparse"
	pg "github.com/pganalyze/pg_query_go/v6"
)

// convertExpr turns a pg_query expression node into a sqlkit/sql expression.
func convertExpr(node *pg.Node) (sql.Expression, error) {
	if node == nil {
		return nil, sqlparse.Unsupported("nil expression")
	}
	switch {
	case node.GetColumnRef() != nil:
		return convertColumnRef(node.GetColumnRef())
	case node.GetAConst() != nil:
		return convertAConst(node.GetAConst())
	case node.GetParamRef() != nil:
		// A positional placeholder ($N). sqlkit numbers its own placeholders from
		// bound values, so there is no native placeholder node; pass the PostgreSQL
		// spelling through verbatim, which round-trips under PostgreSQLDialect.
		return sql.Raw("$" + strconv.Itoa(int(node.GetParamRef().GetNumber()))), nil
	case node.GetAExpr() != nil:
		return convertAExpr(node.GetAExpr())
	case node.GetBoolExpr() != nil:
		return convertBoolExpr(node.GetBoolExpr())
	case node.GetNullTest() != nil:
		return convertNullTest(node.GetNullTest())
	case node.GetSubLink() != nil:
		return convertSubLink(node.GetSubLink())
	case node.GetFuncCall() != nil:
		return convertFuncCall(node.GetFuncCall())
	case node.GetCaseExpr() != nil:
		return convertCaseExpr(node.GetCaseExpr())
	case node.GetSqlvalueFunction() != nil:
		return convertSQLValueFunction(node.GetSqlvalueFunction())
	case node.GetBooleanTest() != nil:
		return convertBooleanTest(node.GetBooleanTest())
	case node.GetGroupingSet() != nil:
		return convertGroupingSet(node.GetGroupingSet())
	case node.GetAIndirection() != nil:
		return convertIndirection(node.GetAIndirection())
	case node.GetCollateClause() != nil:
		return convertCollate(node.GetCollateClause())
	case node.GetTypeCast() != nil:
		return convertTypeCast(node.GetTypeCast())
	case node.GetCoalesceExpr() != nil:
		args, err := convertExprArgs(node.GetCoalesceExpr().GetArgs())
		if err != nil {
			return nil, err
		}
		return sql.Func("coalesce", args...), nil
	case node.GetMinMaxExpr() != nil:
		return convertMinMax(node.GetMinMaxExpr())
	case node.GetRowExpr() != nil:
		args, err := convertExprArgs(node.GetRowExpr().GetArgs())
		if err != nil {
			return nil, err
		}
		return sql.Row(args...), nil
	case node.GetAArrayExpr() != nil:
		return convertArray(node.GetAArrayExpr())
	default:
		return nil, sqlparse.Unsupportedf("expression node %T", node.GetNode())
	}
}

// columnRefParts splits a ColumnRef into its qualified name, reporting whether
// it ends in a "*" star and the qualifier preceding that star.
func columnRefParts(cr *pg.ColumnRef) (name string, star bool, qualifier string, err error) {
	parts := make([]string, 0, len(cr.GetFields()))
	for _, field := range cr.GetFields() {
		switch {
		case field.GetString_() != nil:
			parts = append(parts, field.GetString_().GetSval())
		case field.GetAStar() != nil:
			return "", true, strings.Join(parts, "."), nil
		default:
			return "", false, "", sqlparse.Unsupportedf("column reference field %T", field.GetNode())
		}
	}
	return strings.Join(parts, "."), false, "", nil
}

func convertColumnRef(cr *pg.ColumnRef) (sql.Expression, error) {
	name, star, _, err := columnRefParts(cr)
	if err != nil {
		return nil, err
	}
	if star {
		return nil, sqlparse.Unsupported("'*' outside the select list")
	}
	return sql.Col(name), nil
}

func convertAConst(c *pg.A_Const) (sql.Expression, error) {
	switch {
	case c.GetIsnull():
		return sql.Val(nil), nil
	case c.GetIval() != nil:
		return sql.Val(int(c.GetIval().GetIval())), nil
	case c.GetFval() != nil:
		// A numeric literal (a "float" node in the grammar, but the spelling of any
		// non-integer constant: 3.14, 1e10, or an exact decimal too wide for any Go
		// numeric type). Parameterizing it would force a lossy float64, so emit the
		// literal verbatim instead — exact, and consistent with how a $N param ref
		// is passed through as Raw.
		return sql.Raw(c.GetFval().GetFval()), nil
	case c.GetSval() != nil:
		return sql.Val(c.GetSval().GetSval()), nil
	case c.GetBoolval() != nil:
		return sql.Val(c.GetBoolval().GetBoolval()), nil
	default:
		return nil, sqlparse.Unsupported("constant literal")
	}
}

// operatorName joins the String parts of an operator name list, e.g. ["="].
func operatorName(nodes []*pg.Node) (string, error) {
	parts := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n.GetString_() == nil {
			return "", sqlparse.Unsupportedf("operator name part %T", n.GetNode())
		}
		parts = append(parts, n.GetString_().GetSval())
	}
	return strings.Join(parts, "."), nil
}

func convertAExpr(e *pg.A_Expr) (sql.Expression, error) {
	op, err := operatorName(e.GetName())
	if err != nil {
		return nil, err
	}
	switch e.GetKind() {
	case pg.A_Expr_Kind_AEXPR_OP:
		if e.GetLexpr() == nil {
			return convertUnaryOp(op, e.GetRexpr())
		}
		left, err := convertExpr(e.GetLexpr())
		if err != nil {
			return nil, err
		}
		right, err := convertExpr(e.GetRexpr())
		if err != nil {
			return nil, err
		}
		return sql.BinaryExpression{Left: left, Operator: op, Right: right}, nil
	case pg.A_Expr_Kind_AEXPR_OP_ANY, pg.A_Expr_Kind_AEXPR_OP_ALL:
		return convertArrayQuantified(e, op)
	case pg.A_Expr_Kind_AEXPR_LIKE, pg.A_Expr_Kind_AEXPR_ILIKE:
		return convertLike(e, op)
	case pg.A_Expr_Kind_AEXPR_SIMILAR:
		return convertSimilar(e, op)
	case pg.A_Expr_Kind_AEXPR_IN:
		return convertInList(e, op)
	case pg.A_Expr_Kind_AEXPR_BETWEEN, pg.A_Expr_Kind_AEXPR_NOT_BETWEEN:
		return convertBetween(e)
	case pg.A_Expr_Kind_AEXPR_DISTINCT, pg.A_Expr_Kind_AEXPR_NOT_DISTINCT:
		left, err := convertExpr(e.GetLexpr())
		if err != nil {
			return nil, err
		}
		right, err := convertExpr(e.GetRexpr())
		if err != nil {
			return nil, err
		}
		if e.GetKind() == pg.A_Expr_Kind_AEXPR_NOT_DISTINCT {
			return sql.IsNotDistinctFrom(left, right), nil
		}
		return sql.IsDistinctFrom(left, right), nil
	case pg.A_Expr_Kind_AEXPR_NULLIF:
		left, err := convertExpr(e.GetLexpr())
		if err != nil {
			return nil, err
		}
		right, err := convertExpr(e.GetRexpr())
		if err != nil {
			return nil, err
		}
		return sql.Func("nullif", left, right), nil
	default:
		return nil, sqlparse.Unsupportedf("operator expression kind %s", e.GetKind())
	}
}

// convertUnaryOp folds a unary +/- applied to a numeric constant into a signed
// value; other unary operators are not yet representable.
func convertUnaryOp(op string, operand *pg.Node) (sql.Expression, error) {
	if op == "-" || op == "+" {
		if c := operand.GetAConst(); c != nil {
			val, err := convertAConst(c)
			if err != nil {
				return nil, err
			}
			if op == "+" {
				return val, nil
			}
			switch v := val.(type) {
			case sql.ValueExpression:
				if n, ok := v.Value.(int); ok {
					return sql.Val(-n), nil
				}
			case sql.RawExpression:
				// A numeric literal kept verbatim (see convertAConst); negate it by
				// prefixing the minus rather than evaluating it.
				return sql.Raw("-" + string(v)), nil
			}
		}
	}
	return nil, sqlparse.Unsupportedf("unary operator %q", op)
}

func convertLike(e *pg.A_Expr, op string) (sql.Expression, error) {
	left, err := convertExpr(e.GetLexpr())
	if err != nil {
		return nil, err
	}
	pattern, err := convertExpr(e.GetRexpr())
	if err != nil {
		return nil, err
	}
	switch op {
	case "~~":
		return sql.Like(left, pattern), nil
	case "!~~":
		return sql.NotLike(left, pattern), nil
	case "~~*":
		return sql.ILike(left, pattern), nil
	case "!~~*":
		notILike := sql.Not(sql.ILike(left, pattern))
		return notILike, nil
	default:
		return nil, sqlparse.Unsupportedf("LIKE operator %q", op)
	}
}

func convertInList(e *pg.A_Expr, op string) (sql.Expression, error) {
	left, err := convertExpr(e.GetLexpr())
	if err != nil {
		return nil, err
	}
	list := e.GetRexpr().GetList()
	if list == nil {
		return nil, sqlparse.Unsupported("IN without a value list")
	}
	items := make([]any, 0, len(list.GetItems()))
	for _, item := range list.GetItems() {
		expr, err := convertExpr(item)
		if err != nil {
			return nil, err
		}
		items = append(items, expr)
	}
	if op == "<>" {
		return sql.NotIn(left, items...), nil
	}
	return sql.In(left, items...), nil
}

func convertBetween(e *pg.A_Expr) (sql.Expression, error) {
	operand, err := convertExpr(e.GetLexpr())
	if err != nil {
		return nil, err
	}
	bounds := e.GetRexpr().GetList()
	if bounds == nil || len(bounds.GetItems()) != 2 {
		return nil, sqlparse.Unsupported("BETWEEN without two bounds")
	}
	low, err := convertExpr(bounds.GetItems()[0])
	if err != nil {
		return nil, err
	}
	high, err := convertExpr(bounds.GetItems()[1])
	if err != nil {
		return nil, err
	}
	if e.GetKind() == pg.A_Expr_Kind_AEXPR_NOT_BETWEEN {
		return sql.NotBetween(operand, low, high), nil
	}
	return sql.Between(operand, low, high), nil
}

func convertBoolExpr(e *pg.BoolExpr) (sql.Expression, error) {
	operands := make([]sql.Expression, 0, len(e.GetArgs()))
	for _, arg := range e.GetArgs() {
		expr, err := convertExpr(arg)
		if err != nil {
			return nil, err
		}
		operands = append(operands, expr)
	}
	switch e.GetBoolop() {
	case pg.BoolExprType_AND_EXPR:
		return sql.And(operands...), nil
	case pg.BoolExprType_OR_EXPR:
		return sql.Or(operands...), nil
	case pg.BoolExprType_NOT_EXPR:
		return sql.Not(operands[0]), nil
	default:
		return nil, sqlparse.Unsupportedf("boolean operator %s", e.GetBoolop())
	}
}

func convertNullTest(e *pg.NullTest) (sql.Expression, error) {
	operand, err := convertExpr(e.GetArg())
	if err != nil {
		return nil, err
	}
	if e.GetNulltesttype() == pg.NullTestType_IS_NOT_NULL {
		return sql.IsNotNull(operand), nil
	}
	return sql.IsNull(operand), nil
}

func convertSubLink(e *pg.SubLink) (sql.Expression, error) {
	query, err := convertSelect(e.GetSubselect().GetSelectStmt())
	if err != nil {
		return nil, err
	}
	switch e.GetSubLinkType() {
	case pg.SubLinkType_EXISTS_SUBLINK:
		return sql.Exists(query), nil
	case pg.SubLinkType_EXPR_SUBLINK:
		return sql.ScalarSubquery(query), nil
	case pg.SubLinkType_ANY_SUBLINK, pg.SubLinkType_ALL_SUBLINK:
		left, err := convertExpr(e.GetTestexpr())
		if err != nil {
			return nil, err
		}
		op := "="
		if len(e.GetOperName()) > 0 {
			if op, err = operatorName(e.GetOperName()); err != nil {
				return nil, err
			}
		}
		if e.GetSubLinkType() == pg.SubLinkType_ANY_SUBLINK {
			if op == "=" {
				return sql.InQuery(left, query), nil
			}
			return sql.Any(left, op, query), nil
		}
		if op == "<>" {
			return sql.NotInQuery(left, query), nil
		}
		return sql.All(left, op, query), nil
	default:
		return nil, sqlparse.Unsupportedf("subquery link type %s", e.GetSubLinkType())
	}
}

// convertGroupingSet converts a GROUP BY ROLLUP/CUBE/GROUPING SETS element.
// Grouping sets that nest ROLLUP/CUBE are not modeled and round-trip raw.
func convertGroupingSet(gs *pg.GroupingSet) (sql.Expression, error) {
	switch gs.GetKind() {
	case pg.GroupingSetKind_GROUPING_SET_ROLLUP:
		elems, err := convertExprList(gs.GetContent())
		if err != nil {
			return nil, err
		}
		return sql.Rollup(elems...), nil
	case pg.GroupingSetKind_GROUPING_SET_CUBE:
		elems, err := convertExprList(gs.GetContent())
		if err != nil {
			return nil, err
		}
		return sql.Cube(elems...), nil
	case pg.GroupingSetKind_GROUPING_SET_SETS:
		sets := make([][]sql.Expression, 0, len(gs.GetContent()))
		for _, item := range gs.GetContent() {
			if inner := item.GetGroupingSet(); inner != nil {
				switch inner.GetKind() {
				case pg.GroupingSetKind_GROUPING_SET_EMPTY:
					sets = append(sets, nil)
				case pg.GroupingSetKind_GROUPING_SET_SIMPLE:
					elems, err := convertExprList(inner.GetContent())
					if err != nil {
						return nil, err
					}
					sets = append(sets, elems)
				default:
					return nil, sqlparse.Unsupported("nested grouping set")
				}
				continue
			}
			// A multi-column set "(a, b)" parses as a row expression.
			if row := item.GetRowExpr(); row != nil {
				elems, err := convertExprList(row.GetArgs())
				if err != nil {
					return nil, err
				}
				sets = append(sets, elems)
				continue
			}
			expr, err := convertExpr(item)
			if err != nil {
				return nil, err
			}
			sets = append(sets, []sql.Expression{expr})
		}
		return sql.GroupingSets(sets...), nil
	default:
		return nil, sqlparse.Unsupportedf("grouping set kind %s", gs.GetKind())
	}
}

// convertExprList converts a list of expression nodes.
func convertExprList(nodes []*pg.Node) ([]sql.Expression, error) {
	exprs := make([]sql.Expression, 0, len(nodes))
	for _, node := range nodes {
		expr, err := convertExpr(node)
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, expr)
	}
	return exprs, nil
}

// convertIndirection converts an array subscript/slice (a[1], a[1:2], a[1][2])
// or a composite field access ((x).y). A chain that mixes subscripts and fields
// is not modeled and round-trips raw.
func convertIndirection(e *pg.A_Indirection) (sql.Expression, error) {
	operand, err := convertExpr(e.GetArg())
	if err != nil {
		return nil, err
	}
	items := e.GetIndirection()
	// A single String element is a composite field access.
	if len(items) == 1 && items[0].GetString_() != nil {
		return sql.Field(operand, items[0].GetString_().GetSval()), nil
	}
	// Otherwise every element must be a subscript/slice.
	indices := make([]sql.SubscriptIndex, 0, len(items))
	for _, item := range items {
		ai := item.GetAIndices()
		if ai == nil {
			return nil, sqlparse.Unsupportedf("indirection element %T", item.GetNode())
		}
		idx := sql.SubscriptIndex{Slice: ai.GetIsSlice()}
		if lo := ai.GetLidx(); lo != nil {
			if idx.Lower, err = convertExpr(lo); err != nil {
				return nil, err
			}
		}
		if up := ai.GetUidx(); up != nil {
			if idx.Upper, err = convertExpr(up); err != nil {
				return nil, err
			}
		}
		indices = append(indices, idx)
	}
	return sql.SubscriptExpression{Operand: operand, Indices: indices}, nil
}

// convertCollate converts an operand COLLATE "name" expression.
func convertCollate(e *pg.CollateClause) (sql.Expression, error) {
	operand, err := convertExpr(e.GetArg())
	if err != nil {
		return nil, err
	}
	name, err := dottedName(e.GetCollname())
	if err != nil {
		return nil, err
	}
	return sql.Collate(operand, name), nil
}

// convertSQLValueFunction maps the niladic SQL value functions (CURRENT_DATE,
// CURRENT_TIMESTAMP, CURRENT_USER, ...) to their verbatim keyword, which renders
// inline and round-trips. The precision-qualified forms (CURRENT_TIME(n)) are
// not modeled and fall back to raw.
func convertSQLValueFunction(e *pg.SQLValueFunction) (sql.Expression, error) {
	var text string
	switch e.GetOp() {
	case pg.SQLValueFunctionOp_SVFOP_CURRENT_DATE:
		text = "CURRENT_DATE"
	case pg.SQLValueFunctionOp_SVFOP_CURRENT_TIME:
		text = "CURRENT_TIME"
	case pg.SQLValueFunctionOp_SVFOP_CURRENT_TIMESTAMP:
		text = "CURRENT_TIMESTAMP"
	case pg.SQLValueFunctionOp_SVFOP_LOCALTIME:
		text = "LOCALTIME"
	case pg.SQLValueFunctionOp_SVFOP_LOCALTIMESTAMP:
		text = "LOCALTIMESTAMP"
	case pg.SQLValueFunctionOp_SVFOP_CURRENT_ROLE:
		text = "CURRENT_ROLE"
	case pg.SQLValueFunctionOp_SVFOP_CURRENT_USER:
		text = "CURRENT_USER"
	case pg.SQLValueFunctionOp_SVFOP_USER:
		text = "USER"
	case pg.SQLValueFunctionOp_SVFOP_SESSION_USER:
		text = "SESSION_USER"
	case pg.SQLValueFunctionOp_SVFOP_CURRENT_CATALOG:
		text = "CURRENT_CATALOG"
	case pg.SQLValueFunctionOp_SVFOP_CURRENT_SCHEMA:
		text = "CURRENT_SCHEMA"
	default:
		return nil, sqlparse.Unsupportedf("SQL value function %s", e.GetOp())
	}
	return sql.Raw(text), nil
}

// convertBooleanTest maps IS [NOT] TRUE/FALSE/UNKNOWN to an IS comparison
// against the verbatim truth-value keyword.
func convertBooleanTest(e *pg.BooleanTest) (sql.Expression, error) {
	arg, err := convertExpr(e.GetArg())
	if err != nil {
		return nil, err
	}
	var op, value string
	switch e.GetBooltesttype() {
	case pg.BoolTestType_IS_TRUE:
		op, value = "IS", "TRUE"
	case pg.BoolTestType_IS_NOT_TRUE:
		op, value = "IS NOT", "TRUE"
	case pg.BoolTestType_IS_FALSE:
		op, value = "IS", "FALSE"
	case pg.BoolTestType_IS_NOT_FALSE:
		op, value = "IS NOT", "FALSE"
	case pg.BoolTestType_IS_UNKNOWN:
		op, value = "IS", "UNKNOWN"
	case pg.BoolTestType_IS_NOT_UNKNOWN:
		op, value = "IS NOT", "UNKNOWN"
	default:
		return nil, sqlparse.Unsupportedf("boolean test %s", e.GetBooltesttype())
	}
	return sql.BinaryExpression{Left: arg, Operator: op, Right: sql.Raw(value)}, nil
}

func convertCaseExpr(e *pg.CaseExpr) (sql.Expression, error) {
	c := sql.Case()
	if arg := e.GetArg(); arg != nil {
		operand, err := convertExpr(arg)
		if err != nil {
			return nil, err
		}
		c = sql.CaseOf(operand)
	}
	for _, arg := range e.GetArgs() {
		when := arg.GetCaseWhen()
		if when == nil {
			return nil, sqlparse.Unsupportedf("CASE arm %T", arg.GetNode())
		}
		cond, err := convertExpr(when.GetExpr())
		if err != nil {
			return nil, err
		}
		result, err := convertExpr(when.GetResult())
		if err != nil {
			return nil, err
		}
		c = c.When(cond, result)
	}
	if e.GetDefresult() != nil {
		def, err := convertExpr(e.GetDefresult())
		if err != nil {
			return nil, err
		}
		c = c.Else(def)
	}
	return c, nil
}

// convertExprArgs converts a list of expression nodes into Func arguments.
func convertExprArgs(nodes []*pg.Node) ([]any, error) {
	args := make([]any, 0, len(nodes))
	for _, n := range nodes {
		expr, err := convertExpr(n)
		if err != nil {
			return nil, err
		}
		args = append(args, expr)
	}
	return args, nil
}

func convertArray(e *pg.A_ArrayExpr) (sql.Expression, error) {
	elements, err := convertExprArgs(e.GetElements())
	if err != nil {
		return nil, err
	}
	return sql.Array(elements...), nil
}

// convertArrayQuantified handles "left op ANY/ALL (array)" where the right side
// is an array value (ARRAY[...] or an array literal), not a subquery.
func convertArrayQuantified(e *pg.A_Expr, op string) (sql.Expression, error) {
	left, err := convertExpr(e.GetLexpr())
	if err != nil {
		return nil, err
	}
	array, err := convertExpr(e.GetRexpr())
	if err != nil {
		return nil, err
	}
	if e.GetKind() == pg.A_Expr_Kind_AEXPR_OP_ALL {
		return sql.AllArray(left, op, array), nil
	}
	return sql.AnyArray(left, op, array), nil
}

// convertSimilar handles "left SIMILAR TO pattern". PostgreSQL rewrites the
// pattern through pg_catalog.similar_to_escape(pattern), so the pattern is that
// call's first argument; the operator name is "~" (SIMILAR TO) or "!~" (NOT).
func convertSimilar(e *pg.A_Expr, op string) (sql.Expression, error) {
	left, err := convertExpr(e.GetLexpr())
	if err != nil {
		return nil, err
	}
	pattern, err := similarPattern(e.GetRexpr())
	if err != nil {
		return nil, err
	}
	if op == "!~" {
		return sql.NotSimilarTo(left, pattern), nil
	}
	return sql.SimilarTo(left, pattern), nil
}

func similarPattern(node *pg.Node) (sql.Expression, error) {
	if fc := node.GetFuncCall(); fc != nil && len(fc.GetArgs()) >= 1 {
		name, err := funcName(fc.GetFuncname())
		if err != nil {
			return nil, err
		}
		if name == "similar_to_escape" {
			return convertExpr(fc.GetArgs()[0])
		}
	}
	return convertExpr(node)
}

func convertMinMax(e *pg.MinMaxExpr) (sql.Expression, error) {
	args, err := convertExprArgs(e.GetArgs())
	if err != nil {
		return nil, err
	}
	name := "greatest"
	if e.GetOp() == pg.MinMaxOp_IS_LEAST {
		name = "least"
	}
	return sql.Func(name, args...), nil
}

func convertTypeCast(e *pg.TypeCast) (sql.Expression, error) {
	operand, err := convertExpr(e.GetArg())
	if err != nil {
		return nil, err
	}
	typeName, err := typeNameString(e.GetTypeName())
	if err != nil {
		return nil, err
	}
	// PostgreSQL parses TRUE/FALSE as 'bool' typecasts of the strings 't'/'f'.
	if typeName == "boolean" {
		if v, ok := operand.(sql.ValueExpression); ok {
			if s, ok := v.Value.(string); ok && (s == "t" || s == "f") {
				return sql.Val(s == "t"), nil
			}
		}
	}
	return sql.Cast(operand, typeName), nil
}
