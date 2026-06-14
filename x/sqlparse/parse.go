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
// dependency stays isolated, for example
// github.com/aita/sqlkit/x/sqlparse/postgres, which is backed by pg_query_go.
// Additional dialects (MySQL, ...) can be added the same way.
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
