package sql

import (
	"fmt"
	"strings"
)

// FTSMode selects how a full-text search query string is interpreted:
// natural-language (the default) or boolean (the engine's operator syntax —
// PostgreSQL's websearch_to_tsquery, MySQL's IN BOOLEAN MODE).
type FTSMode int

const (
	// FTSNatural interprets the query as plain natural-language text.
	FTSNatural FTSMode = iota
	// FTSBoolean interprets the query with the engine's boolean operator syntax.
	FTSBoolean
)

// MatchExpression is a portable full-text search predicate over one or more
// columns. It renders through a FullTextSearcher dialect, so the same builder
// compiles to PostgreSQL's to_tsvector(...) @@ to_tsquery(...) and MySQL's
// MATCH(...) AGAINST(...); a dialect without that capability (the
// standard target) rejects it at compile time. Config is PostgreSQL's
// text-search configuration (a regconfig name such as "english") and is ignored
// by engines that have no equivalent.
type MatchExpression struct {
	posField
	Columns []Expression
	Query   Expression
	Mode    FTSMode
	Config  string
}

func (MatchExpression) SQLExpression() {}

// MatchBuilder collects the columns of a full-text search; complete it with
// Against to bind the query.
type MatchBuilder struct {
	columns []Expression
}

// Match begins a portable full-text search over one or more columns. Complete
// it with Against:
//
//	sql.Match(sql.Col("title"), sql.Col("body")).Against("quick fox")
//
// On PostgreSQL the columns form the searched document (concatenated with a
// space when there is more than one); on MySQL they are the MATCH column list
// and must be covered by a FULLTEXT index.
func Match(columns ...Expression) MatchBuilder {
	return MatchBuilder{columns: columns}
}

// Against binds the search query, finishing the match. A non-expression query
// becomes a bind value.
func (m MatchBuilder) Against(query any) MatchExpression {
	return MatchExpression{Columns: m.columns, Query: Expr(query)}
}

// Boolean interprets the query with the engine's boolean operator syntax
// (PostgreSQL websearch_to_tsquery, MySQL IN BOOLEAN MODE) instead of as plain
// natural-language text.
func (e MatchExpression) Boolean() MatchExpression {
	e.Mode = FTSBoolean
	return e
}

// WithConfig sets PostgreSQL's text-search configuration (a regconfig name such
// as "english"). It is ignored by engines that have no equivalent (MySQL).
func (e MatchExpression) WithConfig(name string) MatchExpression {
	e.Config = name
	return e
}

// FullTextSearcher is an optional capability a Dialect implements to render a
// portable full-text search (MatchExpression). Full-text search is genuinely
// non-portable — PostgreSQL spells it to_tsvector(...) @@ to_tsquery(...) and
// MySQL MATCH(...) AGAINST(...) — so it renders through this capability and a
// dialect without it (the standard target) rejects it at compile time
// (the compiler detects the capability with a type assertion, so adding it is
// not a breaking change to Dialect). columns and query are already-compiled SQL
// fragments; boolean selects the engine's boolean query syntax over plain
// natural-language text, and config is PostgreSQL's text-search configuration
// (a regconfig name), ignored by engines without an equivalent.
type FullTextSearcher interface {
	Match(columns []string, query string, boolean bool, config string) (string, error)
}

// Match renders full-text search as to_tsvector(...) @@ to_tsquery(...). The
// columns form the searched document (concatenated null-safely with a space
// when there is more than one); a boolean query uses websearch_to_tsquery and a
// natural-language query plainto_tsquery. config, when set, is passed as the
// regconfig to both to_tsvector and the query function.
func (PostgreSQLDialect) Match(columns []string, query string, boolean bool, config string) (string, error) {
	if len(columns) == 0 {
		return "", fmt.Errorf("sql: full-text search requires at least one column")
	}
	document := columns[0]
	if len(columns) > 1 {
		document = "concat_ws(' ', " + strings.Join(columns, ", ") + ")"
	}
	queryFunc := "plainto_tsquery"
	if boolean {
		queryFunc = "websearch_to_tsquery"
	}
	regconfig := ""
	if config != "" {
		regconfig = literal(config) + ", "
	}
	return "(to_tsvector(" + regconfig + document + ") @@ " + queryFunc + "(" + regconfig + query + "))", nil
}

// Match renders full-text search as MATCH(columns) AGAINST(query). A boolean
// query uses IN BOOLEAN MODE, a natural-language query IN NATURAL LANGUAGE
// MODE. The columns must be covered by a FULLTEXT index. config has no MySQL
// equivalent and is ignored.
func (MySQLDialect) Match(columns []string, query string, boolean bool, _ string) (string, error) {
	if len(columns) == 0 {
		return "", fmt.Errorf("sql: full-text search requires at least one column")
	}
	mode := "IN NATURAL LANGUAGE MODE"
	if boolean {
		mode = "IN BOOLEAN MODE"
	}
	return "(MATCH(" + strings.Join(columns, ", ") + ") AGAINST(" + query + " " + mode + "))", nil
}
