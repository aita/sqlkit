package sql

// NoPos is what Pos reports for a node with no recorded source position — one
// built with the query builders rather than parsed. It is -1 so that byte offset
// 0 (the very start of the source, where a top-level statement begins) is a real,
// distinct position rather than colliding with "no position".
const NoPos = -1

// Node is an AST node that can report where it was parsed from: the 0-based byte
// offset of its start in the source SQL, or NoPos when it was built with the query
// builders rather than parsed. The compiler ignores positions; they are metadata,
// so two queries that differ only in their nodes' offsets compile identically.
//
// Only the byte offset is kept. A line and column are derived from the original
// SQL text downstream (a linter, a diagnostic) by scanning to the offset, so a
// node need not carry them.
type Node interface{ Pos() int }

// posField is embedded in AST node structs to carry a source position and satisfy
// Node. NodePos stores the byte offset plus one, so the zero value (an unparsed,
// builder-built node) is NoPos while byte offset 0 is still representable. The
// parser records it through its own helpers; the field is exported so the parser,
// in another package, can set it on a node it has just built.
type posField struct{ NodePos int }

// Pos returns the node's 0-based byte offset in the source SQL, or NoPos when it
// was built rather than parsed.
func (p posField) Pos() int { return p.NodePos - 1 }

// SetPos records pos as the source position of q's top-level statement node and
// returns q. It is the seam a parser uses to attach a statement's offset:
// PostgreSQL records that offset on the RawStmt wrapper, not on the statement
// node, and the builders keep their statement in an unexported field, so it
// cannot be set from outside the package. A query kind that carries no position
// (a raw passthrough) is returned unchanged.
func SetPos(q Query, pos int) Query {
	if s, ok := q.(posSetter); ok {
		return s.setPos(pos)
	}
	return q
}

// posSetter is implemented by every statement node and its fluent builder: a
// value that can record a top-level source position and return itself. A
// builder sets the offset on the statement it wraps (its unexported field), a
// statement value on its own embedded posField. SetPos uses it to attach a
// parsed statement's offset uniformly, without enumerating every concrete kind
// at the call site or knowing which one it holds. Each statement and builder
// implements setPos beside its own definition, not here, so a type's methods stay
// grouped together.
type posSetter interface {
	Query
	setPos(pos int) Query
}
