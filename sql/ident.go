package sql

import "strings"

// Ident is a SQL identifier — a table, column, schema, or alias name — as written
// in a statement. The SQL standard (and PostgreSQL) fold unquoted identifiers
// case-insensitively, so two idents that differ only in case or surrounding space
// name the same thing: compare with Equal, or canonicalize with Fold, rather than
// with ==.
//
// Ident is the atom the analysis layer builds its references from — a qualified
// column reference is an Ident qualifier plus an Ident name. It is deliberately a
// thin named string, cheap to pass and trivial to convert at a string boundary
// (an external metadata lookup, or a policy implemented over plain names).
type Ident string

// Fold returns id in its canonical case-folded form — trimmed and lower-cased —
// the form in which case-insensitive idents compare equal.
func (id Ident) Fold() Ident { return Ident(strings.ToLower(strings.TrimSpace(string(id)))) }

// Equal reports whether id and other name the same identifier, folding case.
func (id Ident) Equal(other Ident) bool { return id.Fold() == other.Fold() }

// String returns the identifier as written.
func (id Ident) String() string { return string(id) }
