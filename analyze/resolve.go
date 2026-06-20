package analyze

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
func ResolveColumn(scope []SourceRef, col ColumnRef) Resolution {
	res := Resolution{Column: col, Status: Unknown}
	if !col.Qualifier.IsZero() {
		if s, ok := matchQualifier(scope, col.Qualifier); ok && !s.Table.IsZero() {
			res.Schema, res.Table, res.Status = s.Schema, s.Table, Resolved
		}
		return res
	}
	// Unqualified: structural resolution — the column belongs to the sole base
	// table in scope; several distinct tables make it ambiguous.
	var match *SourceRef
	for i := range scope {
		s := &scope[i]
		if s.Table.IsZero() {
			continue // a derived (subquery/VALUES) source names no base table here
		}
		if match != nil && !sameTable(*match, *s) {
			res.Status = Ambiguous
			return res
		}
		match = s
	}
	if match != nil {
		res.Schema, res.Table, res.Status = match.Schema, match.Table, Resolved
	}
	return res
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
