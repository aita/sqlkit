// Package sqlite parses SQLite statements into github.com/aita/sqlkit/sql
// queries. It is the SQLite frontend for github.com/aita/sqlkit/sqlparse,
// backed by github.com/rqlite/sql (a pure-Go parser built on the SQLite SQL
// grammar).
//
// It lives in its own module so the parser dependency stays isolated from
// callers that only need the sqlparse abstraction or the sql package. Unlike
// the postgres frontend the dependency is pure Go, so this module is cgo-free.
//
//	q, err := sqlite.ParseOne("SELECT id FROM users WHERE active")
//	text, args, err := q.ToSQL(sql.SQLiteDialect{})
//
// A statement the converters recognize but cannot yet model as typed nodes
// still round-trips: it is kept as a sql.RawSQL deparsed from the parse tree,
// so every statement the SQLite grammar accepts yields a usable Query.
package sqlite

import (
	"errors"
	"fmt"
	"strings"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	rsql "github.com/rqlite/sql"
)

// Parser parses SQLite SQL text into sqlkit/sql queries. The zero value is
// ready to use; it implements sqlparse.Parser.
type Parser struct{}

// New returns a SQLite Parser.
func New() Parser { return Parser{} }

var _ sqlparse.Parser = Parser{}

// Parse parses one or more SQLite statements in source order.
func (Parser) Parse(text string) ([]sql.Query, error) {
	stmts, err := rsql.NewParser(strings.NewReader(text)).ParseStatements()
	if err != nil {
		return nil, err
	}
	queries := make([]sql.Query, 0, len(stmts))
	for i, stmt := range stmts {
		query, err := convertStmt(stmt)
		if err != nil {
			// A construct the converters recognize but cannot model as typed nodes
			// still round-trips as a RawQuery. Genuine parse failures already
			// surfaced from ParseStatements above, so only UnsupportedError reaches
			// this fallback.
			var unsupported *sqlparse.UnsupportedError
			if errors.As(err, &unsupported) {
				raw, err := rawStatement(text, stmts, i)
				if err != nil {
					return nil, err
				}
				queries = append(queries, sql.RawSQL(raw))
				continue
			}
			return nil, err
		}
		// Record where the statement began in the source on its top-level node.
		queries = append(queries, sql.SetPos(query, offsetOf(stmtPos(stmt))))
	}
	return queries, nil
}

// rawStatement returns the source text of stmts[i] for a RawQuery. It slices
// the original text between statement start offsets rather than deparsing the
// parse tree: rqlite's deparser is lossy (it renders "- -b" as the comment
// "--b") and panics on some nodes (LIKE ... ESCAPE). When a statement's start
// isn't known, a lone statement is the whole text; otherwise it falls back to
// the deparser, turning a panic into an error.
func rawStatement(text string, stmts []rsql.Statement, i int) (raw string, err error) {
	clean := func(s string) string {
		return strings.TrimRight(strings.TrimSpace(s), "; \t\r\n")
	}
	if len(stmts) == 1 {
		return clean(text), nil
	}
	start := stmtPos(stmts[i])
	end := len(text)
	if i+1 < len(stmts) {
		next := stmtPos(stmts[i+1])
		if next.IsValid() {
			end = next.Offset
		} else {
			start = rsql.Pos{}
		}
	}
	if start.IsValid() && start.Offset <= end && end <= len(text) {
		return clean(text[start.Offset:end]), nil
	}
	defer func() {
		if r := recover(); r != nil {
			raw, err = "", fmt.Errorf("sqlite: cannot reproduce unsupported statement %d as raw SQL: %v", i+1, r)
		}
	}()
	return stmts[i].String(), nil
}

// Parse parses one or more SQLite statements using a default Parser.
func Parse(text string) ([]sql.Query, error) { return New().Parse(text) }

// ParseOne parses exactly one SQLite statement.
func ParseOne(text string) (sql.Query, error) { return sqlparse.ParseOne(New(), text) }

// convertStmt dispatches a top-level statement node to its converter. Statement
// kinds without a typed converter yet return an UnsupportedError, which Parse
// turns into a round-tripping RawSQL query.
func convertStmt(stmt rsql.Statement) (sql.Query, error) {
	switch s := stmt.(type) {
	case *rsql.SelectStatement:
		return convertSelect(s)
	case *rsql.InsertStatement:
		return convertInsert(s)
	case *rsql.UpdateStatement:
		return convertUpdate(s)
	case *rsql.DeleteStatement:
		return convertDelete(s)
	case *rsql.CreateTableStatement:
		return convertCreateTable(s)
	case *rsql.CreateIndexStatement:
		return convertCreateIndex(s)
	case *rsql.DropTableStatement:
		return convertDropTable(s)
	default:
		return nil, sqlparse.Unsupportedf("statement %T", stmt)
	}
}
