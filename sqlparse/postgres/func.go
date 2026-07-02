package postgres

import (
	"strconv"
	"strings"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
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
	// sqlkit renders a function name unquoted, so each part must be a bare
	// identifier; a quoted name (a space, an embedded quote, or upper case the
	// grammar would fold) has no faithful unquoted form, so report it unsupported
	// and let the statement round-trip as raw.
	for _, p := range parts {
		if !isBareIdent(p) {
			return "", sqlparse.Unsupportedf("function name is not a bare identifier: %q", p)
		}
	}
	return strings.Join(parts, "."), nil
}

func convertFuncCall(fc *pg.FuncCall) (sql.Expression, error) {
	// WITHIN GROUP and VARIADIC are not modeled in the typed AST.
	if fc.GetAggWithinGroup() {
		return nil, sqlparse.Unsupported("WITHIN GROUP (ordered-set aggregate)")
	}
	if fc.GetFuncVariadic() {
		return nil, sqlparse.Unsupported("VARIADIC function call")
	}

	name, err := funcName(fc.GetFuncname())
	if err != nil {
		return nil, err
	}
	var base sql.Expression
	switch {
	case fc.GetFuncformat() == pg.CoercionForm_COERCE_SQL_SYNTAX && isSQLSyntaxFunc(name):
		// EXTRACT(field FROM x), POSITION(sub IN str), and similar keyword-syntax
		// functions are reported as ordinary calls (pg_catalog.extract, ...) tagged
		// COERCE_SQL_SYNTAX. They must be rebuilt as their dedicated nodes, or they
		// render as extract($1, x) — a call form PostgreSQL's grammar rejects.
		base, err = convertSQLSyntaxFunc(name, fc)
	case strings.EqualFold(name, "count"):
		base, err = convertCount(fc)
	default:
		base, err = convertGenericFunc(name, fc)
	}
	if err != nil {
		return nil, err
	}

	// Apply in-aggregate ORDER BY (e.g. array_agg(x ORDER BY y)).
	if aggOrder := fc.GetAggOrder(); len(aggOrder) > 0 {
		terms, err := convertSortClause(aggOrder)
		if err != nil {
			return nil, err
		}
		switch f := base.(type) {
		case sql.FuncExpression:
			base = f.AggOrderBy(terms...)
		default:
			return nil, sqlparse.Unsupported("in-aggregate ORDER BY on non-generic function")
		}
	}

	// Apply FILTER (WHERE ...) to the aggregate.
	if aggFilter := fc.GetAggFilter(); aggFilter != nil {
		filterExpr, err := convertExpr(aggFilter)
		if err != nil {
			return nil, err
		}
		switch f := base.(type) {
		case sql.FuncExpression:
			base = f.FilterWhere(filterExpr)
		case sql.CountExpression:
			base = f.FilterWhere(filterExpr)
		default:
			return nil, sqlparse.Unsupported("FILTER on non-aggregate expression")
		}
	}

	base = positioned(base, fc.GetLocation())

	if fc.GetOver() != nil {
		return convertWindow(base, fc.GetOver())
	}
	return base, nil
}

// isSQLSyntaxFunc reports whether name is a keyword-syntax function sqlkit models
// with a dedicated node. Only these are diverted from the generic call path;
// other COERCE_SQL_SYNTAX functions (substring, trim, overlay) render acceptably
// as ordinary calls, so they fall through.
func isSQLSyntaxFunc(name string) bool {
	return strings.EqualFold(name, "extract") || strings.EqualFold(name, "position")
}

// convertSQLSyntaxFunc rebuilds the dedicated node for a keyword-syntax function.
func convertSQLSyntaxFunc(name string, fc *pg.FuncCall) (sql.Expression, error) {
	args := fc.GetArgs()
	switch {
	case strings.EqualFold(name, "extract"):
		// extract(field_const, source): the field is a bare string constant.
		if len(args) != 2 {
			return nil, sqlparse.Unsupportedf("EXTRACT with %d arguments", len(args))
		}
		field := args[0].GetAConst().GetSval()
		if field == nil {
			return nil, sqlparse.Unsupported("EXTRACT field is not a constant")
		}
		operand, err := convertExpr(args[1])
		if err != nil {
			return nil, err
		}
		return sql.Extract(field.GetSval(), operand), nil
	case strings.EqualFold(name, "position"):
		// position(str, sub): PostgreSQL stores the operands swapped relative to the
		// POSITION(sub IN str) source order.
		if len(args) != 2 {
			return nil, sqlparse.Unsupportedf("POSITION with %d arguments", len(args))
		}
		str, err := convertExpr(args[0])
		if err != nil {
			return nil, err
		}
		substring, err := convertExpr(args[1])
		if err != nil {
			return nil, err
		}
		return sql.Position(substring, str), nil
	default:
		return nil, sqlparse.Unsupportedf("SQL-syntax function %q", name)
	}
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
	if ref := w.GetRefname(); ref != "" {
		def = def.Extends(ref)
	}
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

// quoteTypeIdent double-quotes a type-name part that would not render verbatim
// unquoted — one starting with a digit (a "0" type) or carrying an upper-case
// letter (which PostgreSQL folds to lower case on re-parse) — leaving a plain
// lower-case identifier unquoted so built-in and ordinary types render as is.
func quoteTypeIdent(s string) string {
	// "char" (the internal single-byte type) must stay quoted: unquoted char is
	// the reserved alias for character(1)/bpchar, a different type that re-parses
	// with an added (1). pg_query only leaves the bare name "char" for the quoted
	// internal type — the standard char already canonicalizes to bpchar — so a
	// "char" reaching here is always the one that needs its quotes back.
	if s == "char" {
		return `"char"`
	}
	if isBareIdent(s) {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// isBareIdent reports whether s renders unquoted without changing: a lower-case
// letter or underscore, then lower-case letters, digits, underscores, or dollars.
// Upper case is excluded because an unquoted identifier folds to lower case.
func isBareIdent(s string) bool {
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r == '_':
		case (r >= '0' && r <= '9' || r == '$') && i > 0:
		default:
			return false
		}
	}
	return s != ""
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
	hasTypmods := len(tn.GetTypmods()) > 0
	// A field-qualified interval (INTERVAL '1' HOUR, MINUTE TO SECOND, …) carries
	// its field set as a single bitmask typmod (HOUR=1024, …), not a
	// fractional-seconds precision. Rendering it as interval(1024) is both a
	// meaning change (the field restriction is lost) and non-round-trippable
	// (re-parsing reads 1024 as a precision). There is no typed spelling for the
	// field mask here, so fall back to raw for any modified interval.
	if base == "interval" && hasTypmods {
		return "", sqlparse.Unsupported("interval with a field qualifier or precision")
	}
	canonical, isCanonical := canonicalTypes[base]
	switch {
	// bpchar canonicalizes to char only with a length. Bare "char" is char(1), but
	// bare "bpchar" is the unbounded blank-padded type; rendering a length-less
	// bpchar as char would change its meaning and break the round-trip, since
	// re-parsing bare char re-adds the (1).
	case isCanonical && (base != "bpchar" || hasTypmods):
		base = canonical
	case !isCanonical:
		// A non-built-in type: quote any part that is not a valid bare identifier
		// (e.g. a quoted type name like "0"), or it renders as invalid SQL.
		quoted := make([]string, len(parts))
		for i, p := range parts {
			quoted[i] = quoteTypeIdent(p)
		}
		base = strings.Join(quoted, ".")
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
		typmod := "(" + strings.Join(modStrs, ", ") + ")"
		// timestamptz / timetz canonicalize to "timestamp with time zone" /
		// "time with time zone". The precision belongs on the leading type word
		// (timestamp(3) with time zone), not after the whole phrase
		// (timestamp with time zone(3)), which PostgreSQL's own grammar rejects.
		if suffix := " with time zone"; strings.HasSuffix(base, suffix) {
			base = strings.TrimSuffix(base, suffix) + typmod + suffix
		} else {
			base += typmod
		}
	}
	for range tn.GetArrayBounds() {
		base += "[]"
	}
	return base, nil
}
