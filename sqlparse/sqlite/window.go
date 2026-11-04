package sqlite

import (
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	rsql "github.com/rqlite/sql"
)

// convertOver wraps a function/aggregate expression in an OVER clause, producing
// a window expression. It handles both "fn OVER name" (a reference to a window
// named in the WINDOW clause) and "fn OVER (...)" (an inline window definition).
func convertOver(fn sql.Expression, over *rsql.OverClause) (sql.Expression, error) {
	w := sql.Over(fn)

	// "fn OVER name": a bare reference to a named window.
	if over.Name != nil {
		return w.Named(over.Name.Name), nil
	}

	def := over.Definition
	if def == nil {
		return nil, sqlparse.Unsupported("OVER clause without a window definition")
	}
	if def.Base != nil {
		// "fn OVER (base ...)" extends a named base window with extra clauses.
		w = w.Named(def.Base.Name)
	}

	if len(def.Partitions) > 0 {
		parts, err := convertExprs(def.Partitions)
		if err != nil {
			return nil, err
		}
		w = w.PartitionBy(parts...)
	}
	if len(def.OrderingTerms) > 0 {
		terms, err := convertOrderingTerms(def.OrderingTerms)
		if err != nil {
			return nil, err
		}
		w = w.OrderBy(terms...)
	}
	if def.Frame != nil {
		w = w.Frame(def.Frame.String())
	}
	return w, nil
}

// convertNamedWindows converts a SELECT's WINDOW clause definitions.
func convertNamedWindows(windows []*rsql.Window) ([]sql.NamedWindow, error) {
	out := make([]sql.NamedWindow, 0, len(windows))
	for _, win := range windows {
		nw, err := convertNamedWindow(win)
		if err != nil {
			return nil, err
		}
		out = append(out, nw)
	}
	return out, nil
}

func convertNamedWindow(win *rsql.Window) (sql.NamedWindow, error) {
	def := win.Definition
	if def == nil {
		return sql.NamedWindow{}, sqlparse.Unsupported("WINDOW definition")
	}
	if def.Base != nil {
		// A WINDOW entry that extends another named window is not modeled.
		return sql.NamedWindow{}, sqlparse.Unsupported("WINDOW extending a base window")
	}
	nw := sql.WindowDef(win.Name.Name)
	if len(def.Partitions) > 0 {
		parts, err := convertExprs(def.Partitions)
		if err != nil {
			return sql.NamedWindow{}, err
		}
		nw = nw.PartitionBy(parts...)
	}
	if len(def.OrderingTerms) > 0 {
		terms, err := convertOrderingTerms(def.OrderingTerms)
		if err != nil {
			return sql.NamedWindow{}, err
		}
		nw = nw.OrderBy(terms...)
	}
	if def.Frame != nil {
		nw = nw.Frame(def.Frame.String())
	}
	return nw, nil
}

// convertExprs converts a list of expressions.
func convertExprs(nodes []rsql.Expr) ([]sql.Expression, error) {
	out := make([]sql.Expression, 0, len(nodes))
	for _, node := range nodes {
		expr, err := convertExpr(node)
		if err != nil {
			return nil, err
		}
		out = append(out, expr)
	}
	return out, nil
}
