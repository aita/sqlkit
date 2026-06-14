package postgres

import (
	"strconv"
	"strings"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/x/sqlparse"
	pg "github.com/pganalyze/pg_query_go/v6"
)

// funcName joins a function name list into a (possibly schema-qualified) name,
// dropping the implicit pg_catalog schema PostgreSQL adds to built-ins.
func funcName(nodes []*pg.Node) (string, error) {
	parts := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n.GetString_() == nil {
			return "", sqlparse.Unsupportedf("function name part %T", n.GetNode())
		}
		parts = append(parts, n.GetString_().GetSval())
	}
	if len(parts) == 2 && parts[0] == "pg_catalog" {
		parts = parts[1:]
	}
	return strings.Join(parts, "."), nil
}

func convertFuncCall(fc *pg.FuncCall) (sql.Expression, error) {
	name, err := funcName(fc.GetFuncname())
	if err != nil {
		return nil, err
	}
	if fc.GetAggDistinct() {
		return nil, sqlparse.Unsupportedf("DISTINCT aggregate %s", name)
	}

	var base sql.Expression
	switch {
	case strings.EqualFold(name, "count"):
		base, err = convertCount(fc)
	default:
		base, err = convertGenericFunc(name, fc)
	}
	if err != nil {
		return nil, err
	}

	if fc.GetOver() != nil {
		return convertWindow(base, fc.GetOver())
	}
	return base, nil
}

func convertCount(fc *pg.FuncCall) (sql.Expression, error) {
	if fc.GetAggStar() {
		return sql.CountAll(), nil
	}
	if len(fc.GetArgs()) != 1 {
		return nil, sqlparse.Unsupported("COUNT with multiple arguments")
	}
	target, err := convertSelectionExpr(fc.GetArgs()[0])
	if err != nil {
		return nil, err
	}
	return sql.Count(target), nil
}

func convertGenericFunc(name string, fc *pg.FuncCall) (sql.Expression, error) {
	if fc.GetAggStar() {
		return nil, sqlparse.Unsupportedf("%s(*)", name)
	}
	args := make([]any, 0, len(fc.GetArgs()))
	for _, a := range fc.GetArgs() {
		expr, err := convertExpr(a)
		if err != nil {
			return nil, err
		}
		args = append(args, expr)
	}
	return sql.Func(name, args...), nil
}

// convertSelectionExpr converts an expression that must also be usable as a
// SELECT-list / aggregate target (a Selection), such as a column or "*".
func convertSelectionExpr(node *pg.Node) (sql.Selection, error) {
	if cr := node.GetColumnRef(); cr != nil {
		name, star, qualifier, err := columnRefParts(cr)
		if err != nil {
			return nil, err
		}
		if star {
			if qualifier == "" {
				return sql.Star(), nil
			}
			return sql.StarOf(qualifier), nil
		}
		return sql.Col(name), nil
	}
	// A bare literal (SELECT 1, SELECT 'x') is not a sqlkit Selection as a bound
	// value, so render it as a raw constant — faithful and common in
	// EXISTS (SELECT 1 ...).
	if c := node.GetAConst(); c != nil {
		raw, err := staticConst(c)
		if err != nil {
			return nil, err
		}
		return raw.(sql.Selection), nil
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

func convertWindow(fn sql.Expression, w *pg.WindowDef) (sql.Expression, error) {
	if w.GetRefname() != "" {
		return nil, sqlparse.Unsupported("named window reference")
	}
	win := sql.Over(fn)
	if parts := w.GetPartitionClause(); len(parts) > 0 {
		exprs := make([]sql.Expression, 0, len(parts))
		for _, p := range parts {
			expr, err := convertExpr(p)
			if err != nil {
				return nil, err
			}
			exprs = append(exprs, expr)
		}
		win = win.PartitionBy(exprs...)
	}
	if order := w.GetOrderClause(); len(order) > 0 {
		terms, err := convertSortClause(order)
		if err != nil {
			return nil, err
		}
		win = win.OrderBy(terms...)
	}
	return win, nil
}

// canonicalTypes maps PostgreSQL's internal type names back to their canonical
// SQL spelling, so a parsed CAST renders readably rather than as int4 / bool.
var canonicalTypes = map[string]string{
	"int2":        "smallint",
	"int4":        "integer",
	"int8":        "bigint",
	"float4":      "real",
	"float8":      "double precision",
	"bool":        "boolean",
	"bpchar":      "char",
	"timestamptz": "timestamp with time zone",
	"timetz":      "time with time zone",
}

// typeNameString renders a TypeName as a CAST target type, e.g. "integer",
// "numeric(10, 2)", or "text[]".
func typeNameString(tn *pg.TypeName) (string, error) {
	parts := make([]string, 0, len(tn.GetNames()))
	for _, n := range tn.GetNames() {
		if n.GetString_() == nil {
			return "", sqlparse.Unsupportedf("type name part %T", n.GetNode())
		}
		parts = append(parts, n.GetString_().GetSval())
	}
	if len(parts) == 2 && parts[0] == "pg_catalog" {
		parts = parts[1:]
	}
	base := strings.Join(parts, ".")
	if canonical, ok := canonicalTypes[base]; ok {
		base = canonical
	}

	if mods := tn.GetTypmods(); len(mods) > 0 {
		modStrs := make([]string, 0, len(mods))
		for _, m := range mods {
			c := m.GetAConst()
			if c == nil || c.GetIval() == nil {
				return "", sqlparse.Unsupported("non-integer type modifier")
			}
			modStrs = append(modStrs, strconv.Itoa(int(c.GetIval().GetIval())))
		}
		base += "(" + strings.Join(modStrs, ", ") + ")"
	}
	for range tn.GetArrayBounds() {
		base += "[]"
	}
	return base, nil
}
