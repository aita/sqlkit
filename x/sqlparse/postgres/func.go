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
	if fc.GetAggDistinct() {
		return sql.CountDistinct(target), nil
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
	if fc.GetAggDistinct() {
		return sql.DistinctFunc(name, args...), nil
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

// PostgreSQL's FRAMEOPTION_* bits (parsenodes.h), used to reconstruct an
// explicit window frame clause from WindowDef.FrameOptions.
const (
	frameOptionNonDefault              = 0x00001
	frameOptionRange                   = 0x00002
	frameOptionRows                    = 0x00004
	frameOptionGroups                  = 0x00008
	frameOptionBetween                 = 0x00010
	frameOptionStartUnboundedPreceding = 0x00020
	frameOptionEndUnboundedPreceding   = 0x00040
	frameOptionStartUnboundedFollowing = 0x00080
	frameOptionEndUnboundedFollowing   = 0x00100
	frameOptionStartCurrentRow         = 0x00200
	frameOptionEndCurrentRow           = 0x00400
	frameOptionStartOffsetPreceding    = 0x00800
	frameOptionEndOffsetPreceding      = 0x01000
	frameOptionStartOffsetFollowing    = 0x02000
	frameOptionEndOffsetFollowing      = 0x04000
	frameOptionExcludeCurrentRow       = 0x08000
	frameOptionExcludeGroup            = 0x10000
	frameOptionExcludeTies             = 0x20000
)

// windowFrameClause reconstructs the textual frame clause (e.g. "ROWS BETWEEN 1
// PRECEDING AND CURRENT ROW") from a WindowDef, or "" when the window has no
// explicit frame.
func windowFrameClause(w *pg.WindowDef) (string, error) {
	opts := w.GetFrameOptions()
	if opts&frameOptionNonDefault == 0 {
		return "", nil
	}
	var mode string
	switch {
	case opts&frameOptionRows != 0:
		mode = "ROWS"
	case opts&frameOptionRange != 0:
		mode = "RANGE"
	case opts&frameOptionGroups != 0:
		mode = "GROUPS"
	default:
		return "", sqlparse.Unsupported("window frame mode")
	}
	start, err := frameBound(opts, true, w.GetStartOffset())
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(mode)
	b.WriteString(" ")
	if opts&frameOptionBetween != 0 {
		end, err := frameBound(opts, false, w.GetEndOffset())
		if err != nil {
			return "", err
		}
		b.WriteString("BETWEEN ")
		b.WriteString(start)
		b.WriteString(" AND ")
		b.WriteString(end)
	} else {
		b.WriteString(start)
	}
	switch {
	case opts&frameOptionExcludeCurrentRow != 0:
		b.WriteString(" EXCLUDE CURRENT ROW")
	case opts&frameOptionExcludeGroup != 0:
		b.WriteString(" EXCLUDE GROUP")
	case opts&frameOptionExcludeTies != 0:
		b.WriteString(" EXCLUDE TIES")
	}
	return b.String(), nil
}

func frameBound(opts int32, start bool, offset *pg.Node) (string, error) {
	if start {
		switch {
		case opts&frameOptionStartUnboundedPreceding != 0:
			return "UNBOUNDED PRECEDING", nil
		case opts&frameOptionStartCurrentRow != 0:
			return "CURRENT ROW", nil
		case opts&frameOptionStartUnboundedFollowing != 0:
			return "UNBOUNDED FOLLOWING", nil
		case opts&frameOptionStartOffsetPreceding != 0:
			return frameOffset(offset, "PRECEDING")
		case opts&frameOptionStartOffsetFollowing != 0:
			return frameOffset(offset, "FOLLOWING")
		}
		return "", sqlparse.Unsupported("window frame start bound")
	}
	switch {
	case opts&frameOptionEndUnboundedFollowing != 0:
		return "UNBOUNDED FOLLOWING", nil
	case opts&frameOptionEndCurrentRow != 0:
		return "CURRENT ROW", nil
	case opts&frameOptionEndUnboundedPreceding != 0:
		return "UNBOUNDED PRECEDING", nil
	case opts&frameOptionEndOffsetPreceding != 0:
		return frameOffset(offset, "PRECEDING")
	case opts&frameOptionEndOffsetFollowing != 0:
		return frameOffset(offset, "FOLLOWING")
	}
	return "", sqlparse.Unsupported("window frame end bound")
}

// frameOffset renders an "N PRECEDING/FOLLOWING" bound, inlining the offset as a
// literal (a frame offset is a static expression, not a bind value).
func frameOffset(offset *pg.Node, dir string) (string, error) {
	if offset == nil {
		return "", sqlparse.Unsupported("window frame offset")
	}
	expr, err := staticExpr(offset)
	if err != nil {
		return "", err
	}
	text, err := sql.CompileStatic(sql.PostgreSQLDialect{}, expr)
	if err != nil {
		return "", err
	}
	return text + " " + dir, nil
}

func convertWindow(fn sql.Expression, w *pg.WindowDef) (sql.Expression, error) {
	win := sql.Over(fn)
	// OVER w references a window named by the WINDOW clause; OVER (w ...) names a
	// base window in refname.
	if name := w.GetName(); name != "" {
		win = win.Named(name)
	} else if ref := w.GetRefname(); ref != "" {
		win = win.Named(ref)
	}
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
	frame, err := windowFrameClause(w)
	if err != nil {
		return nil, err
	}
	if frame != "" {
		win = win.Frame(frame)
	}
	return win, nil
}

// convertNamedWindow converts a SELECT-level WINDOW clause definition.
func convertNamedWindow(w *pg.WindowDef) (sql.NamedWindow, error) {
	def := sql.WindowDef(w.GetName())
	if parts := w.GetPartitionClause(); len(parts) > 0 {
		exprs := make([]sql.Expression, 0, len(parts))
		for _, p := range parts {
			expr, err := convertExpr(p)
			if err != nil {
				return sql.NamedWindow{}, err
			}
			exprs = append(exprs, expr)
		}
		def = def.PartitionBy(exprs...)
	}
	if order := w.GetOrderClause(); len(order) > 0 {
		terms, err := convertSortClause(order)
		if err != nil {
			return sql.NamedWindow{}, err
		}
		def = def.OrderBy(terms...)
	}
	frame, err := windowFrameClause(w)
	if err != nil {
		return sql.NamedWindow{}, err
	}
	if frame != "" {
		def = def.Frame(frame)
	}
	return def, nil
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
