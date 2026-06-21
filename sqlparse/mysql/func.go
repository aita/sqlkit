package mysql

import (
	"strings"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/format"
	"github.com/pingcap/tidb/pkg/parser/types"
)

// convertFuncCall converts a scalar function call. Names are rendered in upper
// case, MySQL's conventional spelling for built-ins.
func convertFuncCall(fc *ast.FuncCallExpr) (sql.Expression, error) {
	if fc.Schema.O != "" {
		return nil, sqlparse.Unsupportedf("schema-qualified function %s.%s", fc.Schema.O, fc.FnName.O)
	}
	// The name renders unquoted, so it must be a plain identifier. The lexer maps
	// an invalid byte to "?", which would render as the call "?()"; fall back to
	// raw for any non-identifier name rather than emit that.
	if !isPlainIdent(fc.FnName.O) {
		return nil, sqlparse.Unsupportedf("function name %q is not a plain identifier", fc.FnName.O)
	}
	// EXTRACT(unit FROM x) and POSITION(sub IN str) keep keyword syntax: rendered
	// as the call form EXTRACT(unit, x) / POSITION(sub, str), MySQL's grammar
	// rejects them, so rebuild the dedicated nodes that emit the keyword form.
	switch fc.FnName.L {
	case "count":
		// COUNT is modeled as an aggregate (AggregateFuncExpr). Reaching here means
		// the degenerate call form (e.g. the spaced "Count (0)"), which renders as a
		// plain COUNT(arg) that re-parses as the aggregate with different semantics
		// (COUNT(0) folds to COUNT(*)); fall back to raw rather than mis-render it.
		return nil, sqlparse.Unsupported("COUNT in non-aggregate call form")
	case "char_func":
		// TiDB names CHAR(...) "char_func" internally and even its own deparse emits
		// the non-standard CHAR_FUNC; rebuild the real CHAR(...) spelling.
		return convertCharFunc(fc)
	case "extract":
		if expr, err := convertExtract(fc); err != nil {
			return nil, err
		} else if expr != nil {
			return expr, nil
		}
	case "position":
		if expr, err := convertPosition(fc); err != nil {
			return nil, err
		} else if expr != nil {
			return expr, nil
		}
	case "convert":
		// CONVERT(x USING charset) renders as the call form CONVERT(x, charset),
		// which MySQL rejects. Fall back to a raw statement, whose deparse keeps the
		// USING keyword. (CONVERT(x, type) is a cast and never reaches here.)
		return nil, sqlparse.Unsupported("CONVERT(x USING charset)")
	case "trim":
		// TRIM(x) is an ordinary call, but TRIM([spec] [remstr] FROM x) renders as
		// TRIM(x, remstr), which MySQL rejects; let those forms fall back to raw.
		if len(fc.Args) != 1 {
			return nil, sqlparse.Unsupported("TRIM with a trim specification")
		}
	}
	args, err := convertExprAnys(fc.Args)
	if err != nil {
		return nil, err
	}
	return sql.Func(strings.ToUpper(fc.FnName.L), args...), nil
}

// convertCharFunc rebuilds CHAR(v1, v2, ... [USING charset]) from TiDB's internal
// char_func form, whose trailing argument carries the charset (NULL when none).
// The value and charset arguments are rendered via the parser's own restore so
// the spelling matches the source; a raw passthrough covers any restore failure.
func convertCharFunc(fc *ast.FuncCallExpr) (sql.Expression, error) {
	args := fc.Args
	if len(args) < 1 {
		return nil, sqlparse.Unsupported("CHAR() without arguments")
	}
	values, charset := args[:len(args)-1], args[len(args)-1]
	parts := make([]string, 0, len(values))
	for _, v := range values {
		text, err := restoreExpr(v)
		if err != nil {
			return nil, err
		}
		parts = append(parts, text)
	}
	inner := strings.Join(parts, ", ")
	// A non-NULL trailing argument is the result charset: CHAR(... USING name).
	if ce, ok := charset.(ast.ValueExpr); !ok || ce.GetValue() != nil {
		text, err := restoreExpr(charset)
		if err != nil {
			return nil, err
		}
		inner += " USING " + strings.Trim(text, "'")
	}
	return sql.Raw("CHAR(" + inner + ")"), nil
}

// restoreExpr renders a sub-expression to SQL text via the TiDB restore, so a
// node sqlkit rebuilds by hand keeps the source spelling of its operands.
func restoreExpr(node ast.ExprNode) (string, error) {
	var sb strings.Builder
	if err := node.Restore(format.NewRestoreCtx(format.DefaultRestoreFlags, &sb)); err != nil {
		return "", sqlparse.Unsupportedf("restore %T: %v", node, err)
	}
	return sb.String(), nil
}

// isPlainIdent reports whether s is a non-empty identifier of ASCII letters,
// digits, underscore, or dollar — the shape a bare (unquoted) function name must
// have to render as valid SQL. Every MySQL built-in qualifies.
func isPlainIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '$':
		default:
			return false
		}
	}
	return true
}

// convertExtract rebuilds EXTRACT(unit FROM operand) from its call form
// extract(TimeUnit, operand). It returns a nil expression (not an error) for any
// shape it does not recognize, so the caller falls back to a generic call.
func convertExtract(fc *ast.FuncCallExpr) (sql.Expression, error) {
	if len(fc.Args) != 2 {
		return nil, nil
	}
	unit, ok := fc.Args[0].(*ast.TimeUnitExpr)
	if !ok {
		return nil, nil
	}
	operand, err := convertExpr(fc.Args[1])
	if err != nil {
		return nil, err
	}
	return sql.Extract(unit.Unit.String(), operand), nil
}

// convertPosition rebuilds POSITION(substring IN str) from its call form
// position(substring, str). TiDB keeps the source operand order, unlike
// PostgreSQL.
func convertPosition(fc *ast.FuncCallExpr) (sql.Expression, error) {
	if len(fc.Args) != 2 {
		return nil, nil
	}
	substring, err := convertExpr(fc.Args[0])
	if err != nil {
		return nil, err
	}
	str, err := convertExpr(fc.Args[1])
	if err != nil {
		return nil, err
	}
	return sql.Position(substring, str), nil
}

// convertAggregate converts an aggregate function. COUNT maps to sqlkit's
// dedicated count nodes; the rest become generic (optionally DISTINCT) calls.
func convertAggregate(e *ast.AggregateFuncExpr) (sql.Expression, error) {
	name := strings.ToLower(e.F)
	if e.Order != nil {
		return nil, sqlparse.Unsupportedf("%s(... ORDER BY ...)", name)
	}
	if name == "count" {
		return convertCount(e)
	}
	args, err := convertExprAnys(e.Args)
	if err != nil {
		return nil, err
	}
	if e.Distinct {
		return sql.DistinctFunc(strings.ToUpper(name), args...), nil
	}
	return sql.Func(strings.ToUpper(name), args...), nil
}

func convertCount(e *ast.AggregateFuncExpr) (sql.Expression, error) {
	if len(e.Args) != 1 {
		return nil, sqlparse.Unsupported("COUNT with multiple arguments")
	}
	if e.Distinct {
		target, err := convertSelectionExpr(e.Args[0])
		if err != nil {
			return nil, err
		}
		return sql.CountDistinct(target), nil
	}
	// COUNT(*) reaches the parser as COUNT(1): a single constant argument. Any
	// constant counts every row, so render it as COUNT(*).
	if _, ok := e.Args[0].(ast.ValueExpr); ok {
		return sql.CountAll(), nil
	}
	target, err := convertSelectionExpr(e.Args[0])
	if err != nil {
		return nil, err
	}
	return sql.Count(target), nil
}

func convertWindowFunc(e *ast.WindowFuncExpr) (sql.Expression, error) {
	if e.Distinct {
		return nil, sqlparse.Unsupportedf("DISTINCT in window function %s", e.Name)
	}
	args, err := convertExprAnys(e.Args)
	if err != nil {
		return nil, err
	}
	win := sql.Over(sql.Func(strings.ToUpper(e.Name), args...))

	spec := e.Spec
	if spec.Frame != nil {
		return nil, sqlparse.Unsupported("window frame clause")
	}
	// OVER w references a window named by the WINDOW clause; OVER (w ...) names a
	// base window in Ref.
	if name := spec.Name.O; name != "" {
		win = win.Named(name)
	} else if ref := spec.Ref.O; ref != "" {
		win = win.Named(ref)
	}
	if spec.PartitionBy != nil {
		exprs := make([]sql.Expression, 0, len(spec.PartitionBy.Items))
		for _, item := range spec.PartitionBy.Items {
			expr, err := convertExpr(item.Expr)
			if err != nil {
				return nil, err
			}
			exprs = append(exprs, expr)
		}
		win = win.PartitionBy(exprs...)
	}
	if spec.OrderBy != nil {
		terms, err := convertByItems(spec.OrderBy.Items)
		if err != nil {
			return nil, err
		}
		win = win.OrderBy(terms...)
	}
	return win, nil
}

func convertCast(e *ast.FuncCastExpr) (sql.Expression, error) {
	operand, err := convertExpr(e.Expr)
	if err != nil {
		return nil, err
	}
	return sql.Cast(operand, castType(e.Tp, e.ExplicitCharSet)), nil
}

// castType renders a FieldType as it appears after AS in a CAST, e.g. "SIGNED",
// "CHAR(10)" or "DECIMAL(10, 2)". The internal column spelling (bigint(22)) is
// not a valid cast target, so the parser's dedicated cast-type restore is used.
func castType(ft *types.FieldType, explicitCharset bool) string {
	var sb strings.Builder
	ctx := format.NewRestoreCtx(format.DefaultRestoreFlags, &sb)
	ft.RestoreAsCastType(ctx, explicitCharset)
	return sb.String()
}
