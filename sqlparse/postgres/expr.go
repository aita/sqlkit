package postgres

import (
	"strconv"
	"strings"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
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
		// spelling through verbatim, which round-trips under PostgreSQLDialect. The
		// number is reproduced as written — including an out-of-range $0, which the
		// grammar accepts and the server rejects only at execution; faithfully
		// representing the input is the parser's job, not validating it. A number
		// the raw parser's int32 overflowed (a vast literal lands negative) has no
		// faithful spelling, so it falls back to raw.
		num := node.GetParamRef().GetNumber()
		if num < 0 {
			return nil, sqlparse.Unsupportedf("parameter reference out of range")
		}
		return sql.Raw("$" + strconv.Itoa(int(num))), nil
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
		return positioned(sql.Func("coalesce", args...), node.GetCoalesceExpr().GetLocation()), nil
	case node.GetMinMaxExpr() != nil:
		return convertMinMax(node.GetMinMaxExpr())
	case node.GetRowExpr() != nil:
		args, err := convertExprArgs(node.GetRowExpr().GetArgs())
		if err != nil {
			return nil, err
		}
		// An empty ROW() is parseable but the compiler requires at least one
		// element, so fall back to raw rather than build a row it must reject.
		if len(args) == 0 {
			return nil, sqlparse.Unsupported("empty ROW()")
		}
		return positioned(sql.Row(args...), node.GetRowExpr().GetLocation()), nil
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
			part := field.GetString_().GetSval()
			// sqlkit stores a column as one dotted string and re-splits it on '.' to
			// recover the qualifier, so a single identifier that itself contains a dot
			// (the quoted "a.b") cannot be told apart from the qualified a.b and would
			// render wrongly. Such a name has no faithful typed form; report it
			// unsupported so the statement round-trips as raw.
			if strings.Contains(part, ".") {
				return "", false, "", sqlparse.Unsupportedf("column identifier with a dot: %q", part)
			}
			parts = append(parts, part)
		case field.GetAStar() != nil:
			return "", true, strings.Join(parts, "."), nil
		default:
			return "", false, "", sqlparse.Unsupportedf("column reference field %T", field.GetNode())
		}
	}
	return strings.Join(parts, "."), false, "", nil
}

// offsetOf normalizes a pg_query node location (a 0-based byte offset, or -1 when
// unknown) into the value sqlkit stores in NodePos: the byte offset plus one, so
// the zero value is reserved for "no position". An unknown location stores 0.
func offsetOf(location int32) int {
	if location < 0 {
		return 0
	}
	return int(location) + 1
}

// positioned records location as the source offset of e when e is a node that
// carries a position, returning it unchanged otherwise (a RawExpression, a
// passthrough). It lets a convert wrap a result built through a constructor that
// returns the Expression interface — sql.And, sql.In — without a type assertion at
// the call site.
func positioned(e sql.Expression, location int32) sql.Expression {
	pos := offsetOf(location)
	switch x := e.(type) {
	case sql.BinaryExpression:
		x.NodePos = pos
		return x
	case sql.LogicalExpression:
		x.NodePos = pos
		return x
	case sql.NotExpression:
		x.NodePos = pos
		return x
	case sql.UnaryExpression:
		x.NodePos = pos
		return x
	case sql.NullPredicate:
		x.NodePos = pos
		return x
	case sql.DistinctPredicate:
		x.NodePos = pos
		return x
	case sql.BetweenExpression:
		x.NodePos = pos
		return x
	case sql.InExpression:
		x.NodePos = pos
		return x
	case sql.ILikeExpression:
		x.NodePos = pos
		return x
	case sql.RegexpExpression:
		x.NodePos = pos
		return x
	case sql.SimilarToExpression:
		x.NodePos = pos
		return x
	case sql.ConcatExpression:
		x.NodePos = pos
		return x
	case sql.RowExpression:
		x.NodePos = pos
		return x
	case sql.ArrayExpression:
		x.NodePos = pos
		return x
	case sql.QuantifiedExpression:
		x.NodePos = pos
		return x
	case sql.QuantifiedArrayExpression:
		x.NodePos = pos
		return x
	case sql.CaseExpression:
		x.NodePos = pos
		return x
	case sql.CastExpression:
		x.NodePos = pos
		return x
	case sql.CollateExpression:
		x.NodePos = pos
		return x
	case sql.FieldExpression:
		x.NodePos = pos
		return x
	case sql.SubscriptExpression:
		x.NodePos = pos
		return x
	case sql.FuncExpression:
		x.NodePos = pos
		return x
	case sql.CountExpression:
		x.NodePos = pos
		return x
	case sql.StringAggExpression:
		x.NodePos = pos
		return x
	case sql.ExtractExpression:
		x.NodePos = pos
		return x
	case sql.PositionExpression:
		x.NodePos = pos
		return x
	case sql.JSONObjectExpression:
		x.NodePos = pos
		return x
	case sql.JSONAccessExpression:
		x.NodePos = pos
		return x
	case sql.GroupingExpression:
		x.NodePos = pos
		return x
	case sql.GroupingSetsExpression:
		x.NodePos = pos
		return x
	case sql.WindowExpression:
		x.NodePos = pos
		return x
	case sql.ExistsExpression:
		x.NodePos = pos
		return x
	case sql.ScalarSubqueryExpression:
		x.NodePos = pos
		return x
	case sql.ColumnExpression:
		x.NodePos = pos
		return x
	case sql.ValueExpression:
		x.NodePos = pos
		return x
	default:
		return e
	}
}

// colAt and valAt are the internal positioned constructors the parser uses: they
// build a builder node and record its source offset, keeping position-setting out
// of the public sql builder API.
func colAt(name string, location int32) sql.ColumnExpression {
	e := sql.Col(name)
	e.NodePos = offsetOf(location)
	// The column's identifier shares the reference's position, so a qualified or
	// bare column name reports where it was written, not just the node.
	e.Name.NodePos = offsetOf(location)
	return e
}

func valAt(value any, location int32) sql.ValueExpression {
	e := sql.Val(value)
	e.NodePos = offsetOf(location)
	return e
}

func convertColumnRef(cr *pg.ColumnRef) (sql.Expression, error) {
	name, star, _, err := columnRefParts(cr)
	if err != nil {
		return nil, err
	}
	if star {
		return nil, sqlparse.Unsupported("'*' outside the select list")
	}
	return colAt(name, cr.GetLocation()), nil
}

func convertAConst(c *pg.A_Const) (sql.Expression, error) {
	loc := c.GetLocation()
	switch {
	case c.GetIsnull():
		return valAt(nil, loc), nil
	case c.GetIval() != nil:
		return valAt(int(c.GetIval().GetIval()), loc), nil
	case c.GetFval() != nil:
		// A numeric literal (a "float" node in the grammar, but the spelling of any
		// non-integer constant: 3.14, 1e10, or an exact decimal too wide for any Go
		// numeric type). Parameterizing it would force a lossy float64, so emit the
		// literal verbatim instead — exact, and consistent with how a $N param ref
		// is passed through as Raw. RawExpression carries no position.
		return sql.Raw(c.GetFval().GetFval()), nil
	case c.GetSval() != nil:
		return valAt(c.GetSval().GetSval(), loc), nil
	case c.GetBoolval() != nil:
		return valAt(c.GetBoolval().GetBoolval(), loc), nil
	default:
		return nil, sqlparse.Unsupported("constant literal")
	}
}

// operatorName joins the String parts of an operator name list, e.g. ["="]. A
// schema-qualified operator (e.g. ["pg_catalog", "+"]) is not a bare infix
// token — written as pg_catalog.+ it is invalid SQL — so it is wrapped in the
// OPERATOR(schema.op) form, the spelling that is valid in every position the
// compiler emits an operator (infix, prefix, ANY/ALL). An unqualified operator
// is returned bare; the parser already collapses OPERATOR(+) to a plain "+".
func operatorName(nodes []*pg.Node) (string, error) {
	parts := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n.GetString_() == nil {
			return "", sqlparse.Unsupportedf("operator name part %T", n.GetNode())
		}
		parts = append(parts, n.GetString_().GetSval())
	}
	if len(parts) > 1 {
		return "OPERATOR(" + strings.Join(parts, ".") + ")", nil
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
		be := sql.BinaryExpression{Left: left, Operator: op, Right: right}
		be.NodePos = offsetOf(e.GetLocation())
		return be, nil
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
			x := sql.IsNotDistinctFrom(left, right)
			x.NodePos = offsetOf(e.GetLocation())
			return x, nil
		}
		x := sql.IsDistinctFrom(left, right)
		x.NodePos = offsetOf(e.GetLocation())
		return x, nil
	case pg.A_Expr_Kind_AEXPR_NULLIF:
		left, err := convertExpr(e.GetLexpr())
		if err != nil {
			return nil, err
		}
		right, err := convertExpr(e.GetRexpr())
		if err != nil {
			return nil, err
		}
		return positioned(sql.Func("nullif", left, right), e.GetLocation()), nil
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
		// Unary +/- on a non-constant operand (column, parameter, expression):
		// model it as a UnaryExpression so the typed shape round-trips, rather
		// than falling back to a raw passthrough for the whole statement.
		operandExpr, err := convertExpr(operand)
		if err != nil {
			return nil, err
		}
		// Unary plus is identity. Drop it rather than emit (+x): PostgreSQL folds a
		// leading + (e.g. ++2 renders the inner as +2, which re-parses without the
		// plus), so keeping it would break the round-trip fixpoint.
		if op == "+" {
			return operandExpr, nil
		}
		return sql.Unary(op, operandExpr), nil
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
	pos := offsetOf(e.GetLocation())
	switch op {
	case "~~":
		x := sql.Like(left, pattern)
		x.NodePos = pos
		return x, nil
	case "!~~":
		x := sql.NotLike(left, pattern)
		x.NodePos = pos
		return x, nil
	case "~~*":
		x := sql.ILike(left, pattern)
		x.NodePos = pos
		return x, nil
	case "!~~*":
		x := sql.Not(sql.ILike(left, pattern))
		x.NodePos = pos
		return x, nil
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
	pos := offsetOf(e.GetLocation())
	if op == "<>" {
		x := sql.NotIn(left, items...)
		x.NodePos = pos
		return x, nil
	}
	x := sql.In(left, items...)
	x.NodePos = pos
	return x, nil
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
	pos := offsetOf(e.GetLocation())
	if e.GetKind() == pg.A_Expr_Kind_AEXPR_NOT_BETWEEN {
		x := sql.NotBetween(operand, low, high)
		x.NodePos = pos
		return x, nil
	}
	x := sql.Between(operand, low, high)
	x.NodePos = pos
	return x, nil
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
	loc := e.GetLocation()
	switch e.GetBoolop() {
	case pg.BoolExprType_AND_EXPR:
		return positioned(sql.And(operands...), loc), nil
	case pg.BoolExprType_OR_EXPR:
		return positioned(sql.Or(operands...), loc), nil
	case pg.BoolExprType_NOT_EXPR:
		return positioned(sql.Not(operands[0]), loc), nil
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
		return positioned(sql.IsNotNull(operand), e.GetLocation()), nil
	}
	return positioned(sql.IsNull(operand), e.GetLocation()), nil
}

func convertSubLink(e *pg.SubLink) (sql.Expression, error) {
	query, err := convertSelect(e.GetSubselect().GetSelectStmt())
	if err != nil {
		return nil, err
	}
	loc := e.GetLocation()
	switch e.GetSubLinkType() {
	case pg.SubLinkType_EXISTS_SUBLINK:
		return positioned(sql.Exists(query), loc), nil
	case pg.SubLinkType_EXPR_SUBLINK:
		return positioned(sql.ScalarSubquery(query), loc), nil
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
				return positioned(sql.InQuery(left, query), loc), nil
			}
			return positioned(sql.Any(left, op, query), loc), nil
		}
		if op == "<>" {
			return positioned(sql.NotInQuery(left, query), loc), nil
		}
		return positioned(sql.All(left, op, query), loc), nil
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
		return positioned(sql.Rollup(elems...), gs.GetLocation()), nil
	case pg.GroupingSetKind_GROUPING_SET_CUBE:
		elems, err := convertExprList(gs.GetContent())
		if err != nil {
			return nil, err
		}
		return positioned(sql.Cube(elems...), gs.GetLocation()), nil
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
		return positioned(sql.GroupingSets(sets...), gs.GetLocation()), nil
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
		f := sql.Field(operand, items[0].GetString_().GetSval())
		f.NodePos = childPos(operand)
		return f, nil
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
	x := sql.SubscriptExpression{Operand: operand, Indices: indices}
	x.NodePos = childPos(operand)
	return x, nil
}

// childPos returns the stored NodePos value (offset+1) of e when it is a
// positioned node — how a node pg_query does not locate (an array subscript or
// field access) inherits its start from its operand, which the parser has already
// positioned. It re-encodes the operand's reported offset, mapping NoPos back to
// the zero stored value.
func childPos(e sql.Expression) int {
	if n, ok := e.(sql.Node); ok {
		return n.Pos() + 1
	}
	return 0
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
	return positioned(sql.Collate(operand, name), e.GetLocation()), nil
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
	x := sql.BinaryExpression{Left: arg, Operator: op, Right: sql.Raw(value)}
	x.NodePos = offsetOf(e.GetLocation())
	return x, nil
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
	return positioned(c, e.GetLocation()), nil
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
	return positioned(sql.Array(elements...), e.GetLocation()), nil
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
		return positioned(sql.AllArray(left, op, array), e.GetLocation()), nil
	}
	return positioned(sql.AnyArray(left, op, array), e.GetLocation()), nil
}

// convertSimilar handles "left SIMILAR TO pattern". PostgreSQL rewrites the
// pattern through pg_catalog.similar_to_escape(pattern), so the pattern is that
// call's first argument; the operator name is "~" (SIMILAR TO) or "!~" (NOT).
func convertSimilar(e *pg.A_Expr, op string) (sql.Expression, error) {
	left, err := convertExpr(e.GetLexpr())
	if err != nil {
		return nil, err
	}
	pattern, escape, err := similarPattern(e.GetRexpr())
	if err != nil {
		return nil, err
	}
	negate := op == "!~"
	expr := sql.SimilarToExpression{Left: left, Pattern: pattern, Negate: negate, Escape: escape}
	return positioned(expr, e.GetLocation()), nil
}

func similarPattern(node *pg.Node) (pattern sql.Expression, escape sql.Expression, err error) {
	if fc := node.GetFuncCall(); fc != nil && len(fc.GetArgs()) >= 1 {
		name, err := funcName(fc.GetFuncname())
		if err != nil {
			return nil, nil, err
		}
		if name == "similar_to_escape" {
			pat, err := convertExpr(fc.GetArgs()[0])
			if err != nil {
				return nil, nil, err
			}
			if len(fc.GetArgs()) >= 2 {
				esc, err := convertExpr(fc.GetArgs()[1])
				if err != nil {
					return nil, nil, err
				}
				return pat, esc, nil
			}
			return pat, nil, nil
		}
	}
	pat, err := convertExpr(node)
	return pat, nil, err
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
	return positioned(sql.Func(name, args...), e.GetLocation()), nil
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
				return positioned(sql.Val(s == "t"), e.GetLocation()), nil
			}
		}
	}
	return positioned(sql.Cast(operand, typeName), e.GetLocation()), nil
}
