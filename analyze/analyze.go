// Package analyze is a small, read-only analysis framework over the sqlkit SQL
// AST. It is a toolbox of focused functions that turn a parsed sql.Query into
// individual structural facts — the statement kind (KindOf), the base tables it
// touches (Tables), whether the top-level node has a WHERE (HasTopLevelWhere) or
// projects "*" (SelectsStar), the columns its WHERE filters on (FilterColumns) and the
// largest IN list (MaxInList) — and
// normalizes a statement into a value-independent fingerprint (Fingerprint), all
// without recompiling or mutating the query.
//
// The package is dialect-agnostic and depends only on the sql package. It does
// not bundle the facts into an aggregate of its own: a consumer composes the
// functions it needs into its own type. querytrace, for instance, uses them to
// fill its QueryInfo, and they are equally the foundation other AST passes
// (column resolution, masking) build on.
package analyze
