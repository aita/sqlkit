package analysis

import (
	"github.com/aita/sqlkit/sql"
)

// This file is analyze's resolution layer: it ties the schema-free detection
// above (SourcesOf, WalkColumns) to the table a column reference belongs to,
// structurally — from the query's own aliases and FROM scope alone. It needs no
// schema: a qualified reference binds to the source its qualifier names, and an
// unqualified reference binds to the sole source in scope. When the scope holds
// several sources an unqualified reference is reported Ambiguous rather than
// guessed, and the caller — which knows the schema, or a masking policy — decides
// which source owns it. Resolution never errors and never consults metadata; that
// lookup, keyed by the resolved table and column, is the caller's concern.

// Status is the outcome of resolving a column reference. Resolution never errors;
// the status reports how confidently a reference was placed and leaves the policy
// — treat Unknown/Ambiguous as fine, or as a failure — to the caller.
type Status int

const (
	// Unknown is a reference that could not be placed: an unqualified reference
	// with no base-table source in scope, or a qualifier that matches none. It is
	// the zero value.
	Unknown Status = iota
	// Resolved is a reference tied to exactly one table (Schema/Table are set):
	// any qualified reference whose qualifier matches a source, or an unqualified
	// reference in a single-table scope.
	Resolved
	// Ambiguous is an unqualified reference that several distinct in-scope tables
	// could own. Resolution does not guess; the caller disambiguates with schema
	// knowledge it has and analyze does not.
	Ambiguous
)

// Resolution is the outcome of resolving one column reference: where it appears
// and how it is used (Clause/Access, from the ColumnUse), and the table it was
// tied to (Schema/Table/Status). Resolve fills Clause/Access; ResolveColumn, which
// takes a bare reference and no context, leaves them zero.
type Resolution struct {
	// Column is the reference that was resolved, as written.
	Column ColumnRef
	// Clause and Access locate the reference in the statement and say whether it is
	// read or written. They are set by Resolve and zero from ResolveColumn.
	Clause Clause
	Access Access
	// Schema and Table name the owning table when Status is Resolved — taken from
	// the in-scope source the reference bound to — and are empty otherwise.
	Schema sql.Ident
	Table  sql.Ident
	// Status reports whether the reference was placed (Resolved), could not be
	// (Unknown), or was Ambiguous across in-scope tables.
	Status Status
}

// Resolve resolves every column reference in q and its subqueries, returning one
// Resolution per occurrence in document (pre-order) order. Each query node is
// resolved against its own scope — the sources SourcesOf reports for that node.
//
// Scopes are not nested: a correlated reference to an enclosing query's alias is
// resolved against the inner node's own sources only, so it comes out Unknown.
func Resolve(q sql.Query) []Resolution {
	var out []Resolution
	root, ok := sql.Unwrap(q).(sql.Node)
	if !ok {
		return nil
	}
	sql.Inspect(root, func(node sql.Node) bool {
		query, ok := node.(sql.Query)
		if !ok {
			return true
		}
		scope := SourcesOf(query)
		WalkColumns(query, func(u ColumnUse) {
			res := ResolveColumn(scope, u.Column)
			res.Clause, res.Access = u.Clause, u.Access
			out = append(out, res)
		})
		return true
	})
	return out
}

// ResolveColumn resolves a single column reference against an explicit scope of
// table sources (as reported by SourcesOf), structurally: a qualified reference
// binds to the source its qualifier names; an unqualified reference binds to the
// sole base-table source, or is Ambiguous when several distinct tables are in
// scope. It is the per-reference core of Resolve, exposed for callers that drive
// their own traversal.
//
// When the matched source is a derived table or CTE (SourceRef.Subquery is set),
// resolution descends into the subquery's SELECT list to trace the column back to
// its underlying base table, mirroring how a real RDBMS resolves such references.
func ResolveColumn(scope []SourceRef, col ColumnRef) Resolution {
	res := Resolution{Column: col, Status: Unknown}
	if !col.Qualifier.IsZero() {
		if s, ok := matchQualifier(scope, col.Qualifier); ok {
			if !s.Table.IsZero() && s.Subquery == nil {
				res.Schema, res.Table, res.Status = s.Schema, s.Table, Resolved
			} else if s.Subquery != nil {
				return resolveInSubquery(s.Subquery, col)
			}
		}
		return res
	}
	// Unqualified: structural resolution — the column belongs to the sole base
	// table in scope; several distinct tables make it ambiguous.
	var match *SourceRef
	for i := range scope {
		s := &scope[i]
		if s.Table.IsZero() {
			continue // a plain derived (subquery/VALUES) source names no base table here
		}
		if match != nil && !sameTable(*match, *s) {
			res.Status = Ambiguous
			return res
		}
		match = s
	}
	if match != nil {
		if match.Subquery != nil {
			return resolveInSubquery(match.Subquery, col)
		}
		res.Schema, res.Table, res.Status = match.Schema, match.Table, Resolved
	}
	return res
}

// maxResolveDepth bounds how many derived-table, CTE, and set-operation hops a
// fan-out resolution follows. It both caps work on deeply nested queries and
// breaks the cycle a recursive CTE (whose body references itself) would otherwise
// spin in; past the limit a reference is reported as resolving to nothing.
const maxResolveDepth = 32

// ResolveColumnAll resolves col against scope like ResolveColumn, but returns
// every base table the reference may map to rather than a single placement. A
// qualified reference yields the source its qualifier names; an unqualified one
// yields every base table in scope it could belong to (one for a single-table
// scope, several when ambiguous). Tracing descends into derived sources and CTEs,
// and through both arms of a set operation, so a column projected from a UNION
// yields a candidate per arm; a column projected through an expression
// (an alias, a function, a concatenation) yields a candidate per base column the
// expression reads. Each candidate's Column names the underlying base column,
// which may differ from col when a projection renames it.
//
// The result holds only Resolved resolutions, deduplicated by owning table and
// base column, and is empty when the reference resolves to no base table in scope.
// It is the fan-out form of ResolveColumn, for a caller — a masking policy — that
// treats a column as sensitive when any candidate is, and so must see every
// provenance.
func ResolveColumnAll(scope []SourceRef, col ColumnRef) []Resolution {
	return resolveAll(scope, col, nil, 0)
}

// ResolveColumnAllVia is ResolveColumnAll with ambient CTEs — those visible from
// an enclosing WITH — applied to every scope it descends into, so a reference
// chained through several CTEs (a CTE whose body selects from an earlier CTE) is
// still traced to its base table. A query node alone cannot recover the CTEs in
// scope above it, so a caller that holds them passes them here.
func ResolveColumnAllVia(scope []SourceRef, col ColumnRef, ctes []sql.CTE) []Resolution {
	return resolveAll(scope, col, ctes, 0)
}

func resolveAll(scope []SourceRef, col ColumnRef, ctes []sql.CTE, depth int) []Resolution {
	if depth > maxResolveDepth {
		return nil
	}
	if !col.Qualifier.IsZero() {
		s, ok := matchQualifier(scope, col.Qualifier)
		if !ok {
			return nil
		}
		if s.Subquery != nil {
			return resolveInSubqueryAll(s.Subquery, col, ctes, depth+1)
		}
		if !s.Table.IsZero() {
			return []Resolution{{Column: col, Schema: s.Schema, Table: s.Table, Status: Resolved}}
		}
		return nil
	}
	// Unqualified: the column may belong to any base table in scope, and may pass
	// through any derived source's projection.
	var out []Resolution
	for i := range scope {
		s := &scope[i]
		if s.Subquery != nil {
			out = append(out, resolveInSubqueryAll(s.Subquery, col, ctes, depth+1)...)
			continue
		}
		if !s.Table.IsZero() {
			out = append(out, Resolution{Column: col, Schema: s.Schema, Table: s.Table, Status: Resolved})
		}
	}
	return dedupeResolutions(out)
}

// resolveInSubqueryAll is the fan-out counterpart of resolveInSubquery: it traces
// col.Name through subq, descending into both arms of a set operation so a column
// drawn from a UNION yields a candidate per arm. The ambient CTEs are applied to
// each inner scope so a reference chained through CTEs keeps resolving.
func resolveInSubqueryAll(subq sql.Query, col ColumnRef, ctes []sql.CTE, depth int) []Resolution {
	if depth > maxResolveDepth {
		return nil
	}
	switch v := sql.Unwrap(subq).(type) {
	case sql.SelectStmt:
		return resolveInSelectAll(v, col, ctes, depth)
	case sql.CompoundStmt:
		// A set operation's output column draws from every arm; union their
		// candidates so a sensitive column on any arm is reported.
		var out []Resolution
		out = append(out, resolveInSubqueryAll(v.Left, col, ctes, depth+1)...)
		out = append(out, resolveInSubqueryAll(v.Right, col, ctes, depth+1)...)
		return dedupeResolutions(out)
	default:
		return nil
	}
}

// resolveInSelectAll traces col.Name through one SELECT's projection, returning the
// candidates of the underlying base column(s). It matches a named output column
// (a plain or aliased projection), then falls back to a wildcard; the matched
// projection is followed through whatever expression produces it, so a renamed,
// cast, or concatenated column still traces to its base. The inner scope is
// enriched with the ambient CTEs so a chained CTE reference keeps resolving.
func resolveInSelectAll(sel sql.SelectStmt, col ColumnRef, ctes []sql.CTE, depth int) []Resolution {
	innerScope := SourcesOf(sel)
	if len(ctes) > 0 {
		enrichWithCTEs(innerScope, ctes)
	}
	for _, s := range sel.Columns {
		outName, expr := selectionOutput(s)
		if outName.IsZero() || !outName.Equal(col.Name) {
			continue
		}
		return resolveExprColumns(innerScope, expr, ctes, depth)
	}
	for _, s := range sel.Columns {
		star, ok := s.(sql.StarSelection)
		if !ok {
			continue
		}
		if star.Qualifier.IsZero() {
			return resolveAll(innerScope, ColumnRef{Name: col.Name}, ctes, depth+1)
		}
		return resolveAll(innerScope, ColumnRef{Qualifier: star.Qualifier, Name: col.Name}, ctes, depth+1)
	}
	return nil
}

// resolveExprColumns resolves every base column an output expression reads: the
// expression itself when it is a plain column reference, or each column reference
// found within it (a function argument, a concatenation operand, a cast operand),
// so a value derived from a sensitive column is still traced to it.
func resolveExprColumns(scope []SourceRef, expr sql.Expression, ctes []sql.CTE, depth int) []Resolution {
	if c, ok := expr.(sql.ColumnExpression); ok {
		return resolveAll(scope, ColumnRefOf(c), ctes, depth+1)
	}
	var out []Resolution
	sql.WalkExpr(expr, func(e sql.Expression) {
		if c, ok := e.(sql.ColumnExpression); ok {
			out = append(out, resolveAll(scope, ColumnRefOf(c), ctes, depth+1)...)
		}
	})
	return dedupeResolutions(out)
}

// dedupeResolutions removes resolutions that name the same owning table and base
// column, preserving first-seen order, so a self-union or a self-join contributes
// one candidate while distinct base columns (a concatenation of two columns) are
// each kept.
func dedupeResolutions(in []Resolution) []Resolution {
	if len(in) <= 1 {
		return in
	}
	type key struct{ schema, table, column string }
	seen := make(map[key]struct{}, len(in))
	out := in[:0]
	for _, r := range in {
		k := key{r.Schema.Name, r.Table.Name, r.Column.Name.Name}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, r)
	}
	return out
}

// EnrichSources annotates each base-table source in sources that names one of the
// CTEs with that CTE's body query, so resolution can trace columns through it.
// SourcesOf already applies a query's own WITH; this lets a caller also apply the
// CTEs visible from an enclosing WITH (ambient CTEs) to a nested query's sources,
// the piece a query node cannot recover from itself.
func EnrichSources(sources []SourceRef, ctes []sql.CTE) {
	enrichWithCTEs(sources, ctes)
}

// resolveInSubquery traces col.Name through the SELECT list of subq, returning
// the resolution of the underlying base column. It handles aliased and unaliased
// column projections and SELECT * wildcards. Non-SELECT queries (compound, VALUES)
// and complex (non-column) projected expressions return Unknown.
func resolveInSubquery(subq sql.Query, col ColumnRef) Resolution {
	sel, ok := sql.Unwrap(subq).(sql.SelectStmt)
	if !ok {
		return Resolution{Column: col, Status: Unknown}
	}
	innerScope := SourcesOf(subq)

	// Match col.Name against each named output column in the SELECT list.
	for _, s := range sel.Columns {
		outName, expr := selectionOutput(s)
		if outName.IsZero() || !outName.Equal(col.Name) {
			continue
		}
		c, ok := expr.(sql.ColumnExpression)
		if !ok {
			// Complex expression (function, cast, …) — cannot trace to a base column.
			return Resolution{Column: col, Status: Unknown}
		}
		return ResolveColumn(innerScope, ColumnRefOf(c))
	}

	// No named match — check for a wildcard that passes all inner columns through.
	for _, s := range sel.Columns {
		star, ok := s.(sql.StarSelection)
		if !ok {
			continue
		}
		if star.Qualifier.IsZero() {
			// SELECT * — resolve unqualified in the inner scope.
			return ResolveColumn(innerScope, ColumnRef{Name: col.Name})
		}
		// SELECT t.* — resolve as t.name in the inner scope.
		return ResolveColumn(innerScope, ColumnRef{Qualifier: star.Qualifier, Name: col.Name})
	}

	return Resolution{Column: col, Status: Unknown}
}

// selectionOutput returns the output alias and the underlying expression for a
// SELECT-list item. For a plain ColumnExpression, the output name is the column's
// bare name. For an AliasedSelection, the output name is the declared alias. For
// a wildcard or a non-expression item, both return values are zero/nil.
func selectionOutput(s sql.Selection) (name sql.Ident, expr sql.Expression) {
	switch x := s.(type) {
	case sql.AliasedSelection:
		if e, ok := x.Selection.(sql.Expression); ok {
			return x.Alias, e
		}
	case sql.Expression:
		if c, ok := x.(sql.ColumnExpression); ok {
			return ColumnRefOf(c).Name, x
		}
	}
	return sql.Ident{}, nil
}

// matchQualifier returns the in-scope source a column qualifier denotes: the
// source whose alias matches it, or, among unaliased sources, the one whose table
// name (bare or schema-qualified) matches.
func matchQualifier(scope []SourceRef, qualifier sql.Ident) (SourceRef, bool) {
	for _, s := range scope {
		if !s.Alias.IsZero() {
			if s.Alias.Equal(qualifier) {
				return s, true
			}
			continue
		}
		if s.Table.IsZero() {
			continue
		}
		if s.Table.Equal(qualifier) || sql.NewIdent(s.Schema.Name+"."+s.Table.Name).Equal(qualifier) {
			return s, true
		}
	}
	return SourceRef{}, false
}

// sameTable reports whether two sources name the same table (a self-join's two
// aliases), so a shared column resolves rather than reporting ambiguity.
func sameTable(a, b SourceRef) bool {
	return a.Schema.Equal(b.Schema) && a.Table.Equal(b.Table)
}
