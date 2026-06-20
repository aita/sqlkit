package sql

import "strings"

// Ident is a SQL identifier — a table, column, schema, or alias name — as written
// in a statement, together with the source position it was parsed from. The SQL
// standard (and PostgreSQL) fold unquoted identifiers case-insensitively, so two
// idents that differ only in case or surrounding space name the same thing:
// compare with Equal, or canonicalize with Fold, rather than with ==. Equality
// with == additionally distinguishes positions, so it is never the right test for
// identifier identity.
//
// Ident is the atom the analysis layer builds its references from — a qualified
// column reference is an Ident qualifier plus an Ident name. It carries a position
// (embedded posField) so a parsed identifier reports where it came from in the SQL
// text; a builder-constructed ident has NoPos. The name itself is a plain string,
// reachable through Name or String for a lookup at a string boundary (an external
// metadata lookup, or a policy implemented over plain names).
type Ident struct {
	posField
	Name string
}

// NewIdent returns an unpositioned Ident for name — the form the builders produce.
// Its Pos is NoPos; a parser attaches a position with NewIdentAt or by setting
// NodePos.
func NewIdent(name string) Ident { return Ident{Name: name} }

// NewIdentAt returns an Ident for name positioned at the 0-based byte offset pos in
// the source SQL. It is the seam a parser uses to record where an identifier was
// written.
func NewIdentAt(name string, pos int) Ident {
	return Ident{posField: posField{NodePos: pos + 1}, Name: name}
}

// Fold returns id in its canonical case-folded form — trimmed and lower-cased —
// the form in which case-insensitive idents compare equal, keeping id's position.
func (id Ident) Fold() Ident {
	return Ident{posField: id.posField, Name: foldName(id.Name)}
}

// Equal reports whether id and other name the same identifier, folding case. It
// ignores position, so two occurrences of the same name compare equal.
func (id Ident) Equal(other Ident) bool { return foldName(id.Name) == foldName(other.Name) }

// IsZero reports whether id carries no name — the empty identifier the AST uses
// for an absent schema, alias, or qualifier.
func (id Ident) IsZero() bool { return id.Name == "" }

// String returns the identifier as written.
func (id Ident) String() string { return id.Name }

// foldName canonicalizes an identifier's name: trimmed and lower-cased.
func foldName(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// Idents converts names into unpositioned Idents — the helper a parser or
// builder uses to turn a column/name list taken as plain strings into the AST's
// Ident slices.
func Idents(names ...string) []Ident {
	if len(names) == 0 {
		return nil
	}
	out := make([]Ident, len(names))
	for i, n := range names {
		out[i] = NewIdent(n)
	}
	return out
}

// IdentNames returns the plain names of ids, the inverse of Idents — for a
// compiler or accessor that hands a name list back as strings.
func IdentNames(ids []Ident) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.Name
	}
	return out
}

// idents and identNames are the internal aliases the sql-package builders use.
func idents(names []string) []Ident   { return Idents(names...) }
func identNames(ids []Ident) []string { return IdentNames(ids) }
