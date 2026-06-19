package mysql

import (
	"strconv"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	"github.com/pingcap/tidb/pkg/parser/ast"
)

// setOperand is satisfied by the query types that can start a set operation
// (SelectQuery and CompoundQuery), letting the set-operation builder chain
// operators without caring which concrete type it holds.
type setOperand interface {
	Union(sql.Query) sql.CompoundBuilder
	UnionAll(sql.Query) sql.CompoundBuilder
	Intersect(sql.Query) sql.CompoundBuilder
	IntersectAll(sql.Query) sql.CompoundBuilder
	Except(sql.Query) sql.CompoundBuilder
	ExceptAll(sql.Query) sql.CompoundBuilder
}

// cteMaker is satisfied by the query types that can become a CTE body.
type cteMaker interface {
	CTE(string, ...sql.CTEOption) sql.CTE
}

func convertSelect(s *ast.SelectStmt) (sql.Query, error) {
	if s.Kind != ast.SelectStmtKindSelect {
		return nil, sqlparse.Unsupportedf("%s statement", s.Kind.String())
	}
	if s.Fields == nil {
		return nil, sqlparse.Unsupported("SELECT without a field list")
	}

	columns, err := convertFieldList(s.Fields)
	if err != nil {
		return nil, err
	}
	q := sql.Select(columns...)

	if s.Distinct {
		q = q.Distinct()
	}

	if s.From != nil && s.From.TableRefs != nil {
		base, joins, err := convertFrom(s.From.TableRefs)
		if err != nil {
			return nil, err
		}
		if base != nil {
			q = q.From(base)
		}
		for _, j := range joins {
			q = applyJoin(q, j)
		}
	}

	if s.Where != nil {
		expr, err := convertExpr(s.Where)
		if err != nil {
			return nil, err
		}
		q = q.Where(expr)
	}

	if s.GroupBy != nil {
		if s.GroupBy.Rollup {
			return nil, sqlparse.Unsupported("GROUP BY ... WITH ROLLUP")
		}
		exprs := make([]sql.Expression, 0, len(s.GroupBy.Items))
		for _, item := range s.GroupBy.Items {
			expr, err := convertExpr(item.Expr)
			if err != nil {
				return nil, err
			}
			exprs = append(exprs, expr)
		}
		q = q.GroupBy(exprs...)
	}

	if s.Having != nil {
		expr, err := convertExpr(s.Having.Expr)
		if err != nil {
			return nil, err
		}
		q = q.Having(expr)
	}

	if len(s.WindowSpecs) > 0 {
		defs := make([]sql.NamedWindow, 0, len(s.WindowSpecs))
		for i := range s.WindowSpecs {
			def, err := convertNamedWindow(&s.WindowSpecs[i])
			if err != nil {
				return nil, err
			}
			defs = append(defs, def)
		}
		q = q.Window(defs...)
	}

	if s.OrderBy != nil {
		terms, err := convertByItems(s.OrderBy.Items)
		if err != nil {
			return nil, err
		}
		q = q.OrderBy(terms...)
	}

	if s.Limit != nil {
		q, err = applyLimit(q, s.Limit)
		if err != nil {
			return nil, err
		}
	}

	if s.With != nil {
		ctes, err := convertWith(s.With)
		if err != nil {
			return nil, err
		}
		q = q.WithCTE(ctes...)
	}

	if s.LockInfo != nil {
		q, err = applyLocking(q, s.LockInfo)
		if err != nil {
			return nil, err
		}
	}

	return q, nil
}

func applyLimit(q sql.SelectBuilder, limit *ast.Limit) (sql.SelectBuilder, error) {
	if limit.Count != nil {
		n, err := intLiteral(limit.Count)
		if err != nil {
			return q, err
		}
		q = q.Limit(n)
	}
	if limit.Offset != nil {
		n, err := intLiteral(limit.Offset)
		if err != nil {
			return q, err
		}
		q = q.Offset(n)
	}
	return q, nil
}

func applyLocking(q sql.SelectBuilder, info *ast.SelectLockInfo) (sql.SelectBuilder, error) {
	var opts []sql.LockOption
	if len(info.Tables) > 0 {
		names := make([]string, 0, len(info.Tables))
		for _, t := range info.Tables {
			names = append(names, t.Name.O)
		}
		opts = append(opts, sql.LockOf(names...))
	}
	switch info.LockType {
	case ast.SelectLockForUpdate:
		return q.ForUpdate(opts...), nil
	case ast.SelectLockForShare:
		return q.ForShare(opts...), nil
	case ast.SelectLockForUpdateNoWait:
		return q.ForUpdate(append(opts, sql.NoWait())...), nil
	case ast.SelectLockForShareNoWait:
		return q.ForShare(append(opts, sql.NoWait())...), nil
	case ast.SelectLockForUpdateSkipLocked:
		return q.ForUpdate(append(opts, sql.SkipLocked())...), nil
	case ast.SelectLockForShareSkipLocked:
		return q.ForShare(append(opts, sql.SkipLocked())...), nil
	default:
		return q, sqlparse.Unsupportedf("lock type %s", info.LockType.String())
	}
}

// convertSetOpr converts a UNION/INTERSECT/EXCEPT statement, folding its operand
// list left-to-right and applying the statement-level ORDER BY / LIMIT.
func convertSetOpr(s *ast.SetOprStmt) (sql.Query, error) {
	if s.With != nil {
		return nil, sqlparse.Unsupported("WITH on a set operation")
	}
	q, err := convertSetOprList(s.SelectList)
	if err != nil {
		return nil, err
	}
	compound, ok := q.(sql.CompoundBuilder)
	if !ok {
		// A single-element list collapses to a plain query.
		return q, nil
	}
	if s.OrderBy != nil {
		terms, err := convertByItems(s.OrderBy.Items)
		if err != nil {
			return nil, err
		}
		compound = compound.OrderBy(terms...)
	}
	if s.Limit != nil {
		if s.Limit.Count != nil {
			n, err := intLiteral(s.Limit.Count)
			if err != nil {
				return nil, err
			}
			compound = compound.Limit(n)
		}
		if s.Limit.Offset != nil {
			n, err := intLiteral(s.Limit.Offset)
			if err != nil {
				return nil, err
			}
			compound = compound.Offset(n)
		}
	}
	return compound, nil
}

func convertSetOprList(list *ast.SetOprSelectList) (sql.Query, error) {
	if list == nil || len(list.Selects) == 0 {
		return nil, sqlparse.Unsupported("empty set operation")
	}
	if list.With != nil {
		return nil, sqlparse.Unsupported("WITH on a set operation")
	}

	result, _, err := setOprOperand(list.Selects[0])
	if err != nil {
		return nil, err
	}
	for _, node := range list.Selects[1:] {
		right, op, err := setOprOperand(node)
		if err != nil {
			return nil, err
		}
		if op == nil {
			return nil, sqlparse.Unsupported("set operation without an operator")
		}
		operand, ok := result.(setOperand)
		if !ok {
			return nil, sqlparse.Unsupportedf("set-operation operand %T", result)
		}
		switch *op {
		case ast.Union:
			result = operand.Union(right)
		case ast.UnionAll:
			result = operand.UnionAll(right)
		case ast.Intersect:
			result = operand.Intersect(right)
		case ast.IntersectAll:
			result = operand.IntersectAll(right)
		case ast.Except:
			result = operand.Except(right)
		case ast.ExceptAll:
			result = operand.ExceptAll(right)
		default:
			return nil, sqlparse.Unsupportedf("set operation %s", op.String())
		}
	}

	if list.OrderBy != nil || list.Limit != nil {
		compound, ok := result.(sql.CompoundBuilder)
		if !ok {
			return nil, sqlparse.Unsupported("ORDER BY / LIMIT on a single-select set list")
		}
		if list.OrderBy != nil {
			terms, err := convertByItems(list.OrderBy.Items)
			if err != nil {
				return nil, err
			}
			compound = compound.OrderBy(terms...)
		}
		if list.Limit != nil {
			if list.Limit.Count != nil {
				n, err := intLiteral(list.Limit.Count)
				if err != nil {
					return nil, err
				}
				compound = compound.Limit(n)
			}
			if list.Limit.Offset != nil {
				n, err := intLiteral(list.Limit.Offset)
				if err != nil {
					return nil, err
				}
				compound = compound.Offset(n)
			}
		}
		result = compound
	}
	return result, nil
}

// setOprOperand converts one element of a set-operation list, returning the
// operator that joins it to the preceding element (nil for the first).
func setOprOperand(node ast.Node) (sql.Query, *ast.SetOprType, error) {
	switch n := node.(type) {
	case *ast.SelectStmt:
		q, err := convertSelect(n)
		return q, n.AfterSetOperator, err
	case *ast.SetOprSelectList:
		q, err := convertSetOprList(n)
		return q, n.AfterSetOperator, err
	default:
		return nil, nil, sqlparse.Unsupportedf("set-operation element %T", node)
	}
}

// convertResultSetQuery converts a SELECT or set-operation subtree into a query.
func convertResultSetQuery(node ast.ResultSetNode) (sql.Query, error) {
	switch n := node.(type) {
	case *ast.SelectStmt:
		return convertSelect(n)
	case *ast.SetOprStmt:
		return convertSetOpr(n)
	default:
		return nil, sqlparse.Unsupportedf("subquery %T", node)
	}
}

func convertSubquery(e *ast.SubqueryExpr) (sql.Query, error) {
	return convertResultSetQuery(e.Query)
}

func convertFieldList(list *ast.FieldList) ([]sql.Selection, error) {
	columns := make([]sql.Selection, 0, len(list.Fields))
	for _, field := range list.Fields {
		if field.WildCard != nil {
			if q := starQualifier(field.WildCard); q != "" {
				columns = append(columns, sql.StarOf(q))
			} else {
				columns = append(columns, sql.Star())
			}
			continue
		}
		selection, err := convertSelectionExpr(field.Expr)
		if err != nil {
			return nil, err
		}
		if field.AsName.O != "" {
			selection = sql.As(selection, field.AsName.O)
		}
		columns = append(columns, selection)
	}
	return columns, nil
}

// convertSelectionExpr converts an expression that must also be usable as a
// SELECT-list / aggregate target (a Selection).
func convertSelectionExpr(node ast.ExprNode) (sql.Selection, error) {
	if col, ok := node.(*ast.ColumnNameExpr); ok {
		return sql.Col(columnRefName(col.Name)), nil
	}
	// A bare literal (SELECT 1, SELECT 'x') is not a sqlkit Selection as a bound
	// value, so render it as a raw constant — faithful and common in
	// EXISTS (SELECT 1 ...).
	if v, ok := node.(ast.ValueExpr); ok {
		return rawLiteral(v)
	}
	expr, err := convertExpr(node)
	if err != nil {
		return nil, err
	}
	selection, ok := expr.(sql.Selection)
	if !ok {
		return nil, sqlparse.Unsupportedf("%T is not valid in a select list", expr)
	}
	return selection, nil
}

// rawLiteral renders a literal value as raw SQL text for select-list contexts,
// where a bind parameter is not a Selection.
func rawLiteral(v ast.ValueExpr) (sql.Selection, error) {
	switch x := v.GetValue().(type) {
	case nil:
		return sql.Raw("NULL"), nil
	case int64:
		return sql.Raw(strconv.FormatInt(x, 10)), nil
	case uint64:
		return sql.Raw(strconv.FormatUint(x, 10)), nil
	case float64:
		return sql.Raw(strconv.FormatFloat(x, 'g', -1, 64)), nil
	case string:
		return sql.Raw("'" + escapeStringLiteral(x) + "'"), nil
	default:
		if s, ok := v.GetValue().(interface{ String() string }); ok {
			return sql.Raw(s.String()), nil
		}
		return nil, sqlparse.Unsupportedf("select-list literal %T", v.GetValue())
	}
}

func convertByItems(items []*ast.ByItem) ([]sql.OrderTerm, error) {
	terms := make([]sql.OrderTerm, 0, len(items))
	for _, item := range items {
		expr, err := convertExpr(item.Expr)
		if err != nil {
			return nil, err
		}
		if item.Desc {
			terms = append(terms, sql.Desc(expr))
		} else {
			terms = append(terms, sql.Asc(expr))
		}
	}
	return terms, nil
}

func convertNamedWindow(w *ast.WindowSpec) (sql.NamedWindow, error) {
	if w.Frame != nil {
		return sql.NamedWindow{}, sqlparse.Unsupported("window frame clause")
	}
	if w.Ref.O != "" {
		return sql.NamedWindow{}, sqlparse.Unsupported("window referencing another window")
	}
	def := sql.WindowDef(w.Name.O)
	if w.PartitionBy != nil {
		exprs := make([]sql.Expression, 0, len(w.PartitionBy.Items))
		for _, item := range w.PartitionBy.Items {
			expr, err := convertExpr(item.Expr)
			if err != nil {
				return sql.NamedWindow{}, err
			}
			exprs = append(exprs, expr)
		}
		def = def.PartitionBy(exprs...)
	}
	if w.OrderBy != nil {
		terms, err := convertByItems(w.OrderBy.Items)
		if err != nil {
			return sql.NamedWindow{}, err
		}
		def = def.OrderBy(terms...)
	}
	return def, nil
}

func convertWith(wc *ast.WithClause) ([]sql.CTE, error) {
	ctes := make([]sql.CTE, 0, len(wc.CTEs))
	for _, cte := range wc.CTEs {
		body, err := convertSubquery(cte.Query)
		if err != nil {
			return nil, err
		}
		maker, ok := body.(cteMaker)
		if !ok {
			return nil, sqlparse.Unsupportedf("CTE body %T", body)
		}
		var opts []sql.CTEOption
		if len(cte.ColNameList) > 0 {
			names := make([]string, 0, len(cte.ColNameList))
			for _, name := range cte.ColNameList {
				names = append(names, name.O)
			}
			opts = append(opts, sql.Columns(names...))
		}
		if wc.IsRecursive {
			opts = append(opts, sql.Recursive())
		}
		ctes = append(ctes, maker.CTE(cte.Name.O, opts...))
	}
	return ctes, nil
}

// intLiteral reads a LIMIT/OFFSET node, which must be an integer constant.
func intLiteral(node ast.ExprNode) (int, error) {
	v, ok := node.(ast.ValueExpr)
	if !ok {
		return 0, sqlparse.Unsupported("non-constant LIMIT/OFFSET")
	}
	switch n := v.GetValue().(type) {
	case int64:
		return int(n), nil
	case uint64:
		return int(n), nil
	default:
		return 0, sqlparse.Unsupported("non-integer LIMIT/OFFSET")
	}
}
