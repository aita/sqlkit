// Package sqlparse turns raw SQL text into the query and expression nodes of
// package github.com/aita/sqlkit/sql — the inverse of sqlkit's build-then-
// compile flow.
//
// This package holds only the dialect-agnostic surface: the Parser interface
// every frontend implements, the shared error helpers, and small conveniences
// such as ParseOne. It deliberately depends on nothing but the sql package, so
// callers who only need the abstraction do not pull a parser's (often heavy)
// dependency tree.
//
// A concrete frontend lives in its own subpackage and module so its parser
// dependency stays isolated: github.com/aita/sqlkit/sqlparse/postgres, backed by
// pg_query_go; github.com/aita/sqlkit/sqlparse/mysql, backed by the TiDB parser;
// and github.com/aita/sqlkit/sqlparse/sqlite, a pure-Go frontend backed by
// github.com/rqlite/sql. Additional dialects can be added the same way.
//
// Frontends are used directly, not through a global registry: import the one
// you need and construct its parser.
//
//	p := postgres.New()
//	queries, err := p.Parse("SELECT id FROM users")
//
// # Limitations
//
// sqlparse is a high-coverage importer, not a total one. Treat its output as a
// best-effort reconstruction of the sql AST, subject to the following:
//
//   - Partial mapping, with a raw fallback. Each frontend recognizes its full
//     dialect grammar, but only the constructs the sql AST can model become typed
//     nodes. When a converter meets a construct it cannot represent it reports an
//     [UnsupportedError]; the frontend then deparses just that statement back to
//     SQL and yields it as a [github.com/aita/sqlkit/sql.RawSQL] passthrough, so
//     every statement the grammar accepts still produces a usable Query. Only a
//     genuine syntax error (not an UnsupportedError) fails the whole Parse call.
//     Inspect a result's concrete type to tell a typed node from a raw fallback.
//
//   - The raw fallback is reformatted and dialect-bound. A deparsed RawSQL is
//     normalized by the backend's printer: comments and the original whitespace,
//     quoting, and keyword casing are not preserved. It is rendered in its source
//     dialect and carries no structure, so re-compiling it against a different
//     dialect re-emits that text verbatim rather than translating it.
//
//   - Constructs that fall back rather than map to typed nodes include (non-
//     exhaustive): RIGHT/FULL joins combined with NATURAL or USING; a subquery
//     or derived-table source without an alias; window frame clauses and a window
//     that references another window; GROUP BY ... WITH ROLLUP; non-constant or
//     non-integer LIMIT/OFFSET; several multi-table UPDATE/DELETE shapes;
//     RETURNING on the MySQL grammar; and many engine-specific DDL options,
//     constraints, and ALTER actions.
//
//   - Coupled to the sql AST only. The package depends on nothing but the sql
//     package; the concrete grammars (and their heavy dependency trees) live in
//     the dialect subpackages, each its own module — see the package docs of
//     [github.com/aita/sqlkit/sqlparse/postgres],
//     [github.com/aita/sqlkit/sqlparse/mysql], and
//     [github.com/aita/sqlkit/sqlparse/sqlite] for backend-specific notes.
package sqlparse

import "github.com/aita/sqlkit/sql"

// Parser converts SQL text of one dialect into sqlkit/sql queries. A single
// input may hold several statements, so Parse returns one sql.Query per
// statement in source order.
type Parser interface {
	Parse(text string) ([]sql.Query, error)
}

// ParseOne parses text with p and requires exactly one statement, returning an
// error when the input holds none or more than one.
func ParseOne(p Parser, text string) (sql.Query, error) {
	queries, err := p.Parse(text)
	if err != nil {
		return nil, err
	}
	switch len(queries) {
	case 1:
		return queries[0], nil
	case 0:
		return nil, ErrNoStatement
	default:
		return nil, errStatementCount(len(queries))
	}
}
