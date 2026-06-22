// Package postgres parses PostgreSQL statements into github.com/aita/sqlkit/sql
// queries. It is the PostgreSQL frontend for github.com/aita/sqlkit/sqlparse,
// backed by the real PostgreSQL grammar (libpg_query): it parses and deparses
// through go-pgquery, which runs libpg_query as a WebAssembly module on the pure
// Go wazero runtime, and reuses the AST types from pg_query_go.
//
// It lives in its own module so the parser dependency tree stays isolated from
// callers that only need the sqlparse abstraction or the sql package.
//
//	q, err := postgres.ParseOne("SELECT id FROM users WHERE active")
//	text, args, err := q.ToSQL(sql.PostgreSQLDialect{})
//
// # Limitations
//
// See the [github.com/aita/sqlkit/sqlparse] package docs for the limitations
// common to every frontend (partial mapping with a deparsed RawSQL fallback,
// loss of comments and formatting on that fallback, and so on). Backend-specific
// notes:
//
//   - Pure Go. go-pgquery compiles libpg_query to WebAssembly and runs it on the
//     wazero runtime, so this module builds without cgo or a C toolchain (it
//     still depends on pg_query_go for the AST types, whose cgo entry points are
//     unused). WebAssembly execution is several times slower than calling
//     libpg_query through cgo, which is acceptable for parsing/import workloads
//     but not for parsing very high query volumes online.
//
//   - Statement positions come from the parse result's statement locations, so
//     each Query's position points at its start in the source text.
package postgres

import (
	"errors"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	pg "github.com/pganalyze/pg_query_go/v6"
	pgquery "github.com/wasilibs/go-pgquery"
)

// Parser parses PostgreSQL SQL text into sqlkit/sql queries. The zero value is
// ready to use; it implements sqlparse.Parser.
type Parser struct{}

// New returns a PostgreSQL Parser.
func New() Parser { return Parser{} }

var _ sqlparse.Parser = Parser{}

// Parse parses one or more PostgreSQL statements in source order.
func (Parser) Parse(text string) ([]sql.Query, error) {
	result, err := pgquery.Parse(text)
	if err != nil {
		return nil, err
	}
	stmts := result.GetStmts()
	queries := make([]sql.Query, 0, len(stmts))
	for _, raw := range stmts {
		query, err := convertStmt(raw.GetStmt())
		if err != nil {
			// A construct the converters recognize but cannot model as typed nodes
			// still round-trips: deparse just this statement back to SQL and keep it
			// as a RawQuery, so every statement the PostgreSQL grammar accepts yields
			// a usable Query. Genuine parse failures are not UnsupportedError and so
			// still surface.
			var unsupported *sqlparse.UnsupportedError
			if errors.As(err, &unsupported) {
				rawSQL, derr := deparseStmt(result, raw)
				if derr != nil {
					return nil, err
				}
				queries = append(queries, sql.RawSQL(rawSQL))
				continue
			}
			return nil, err
		}
		queries = append(queries, sql.SetPos(query, offsetOf(raw.GetStmtLocation())))
	}
	return queries, nil
}

// deparseStmt renders a single statement back to SQL text by deparsing a parse
// result that holds only that statement, preserving the source version.
func deparseStmt(result *pg.ParseResult, stmt *pg.RawStmt) (string, error) {
	return pgquery.Deparse(&pg.ParseResult{
		Version: result.GetVersion(),
		Stmts:   []*pg.RawStmt{stmt},
	})
}

// Parse parses one or more PostgreSQL statements using a default Parser.
func Parse(text string) ([]sql.Query, error) { return New().Parse(text) }

// ParseOne parses exactly one PostgreSQL statement.
func ParseOne(text string) (sql.Query, error) { return sqlparse.ParseOne(New(), text) }

// convertStmt dispatches a top-level statement node to its converter.
func convertStmt(node *pg.Node) (sql.Query, error) {
	if node == nil {
		return nil, sqlparse.Unsupported("empty statement")
	}
	switch {
	case node.GetSelectStmt() != nil:
		return convertSelect(node.GetSelectStmt())
	case node.GetInsertStmt() != nil:
		return convertInsert(node.GetInsertStmt())
	case node.GetUpdateStmt() != nil:
		return convertUpdate(node.GetUpdateStmt())
	case node.GetDeleteStmt() != nil:
		return convertDelete(node.GetDeleteStmt())
	case node.GetMergeStmt() != nil:
		return convertMerge(node.GetMergeStmt())
	case node.GetGrantStmt() != nil:
		return convertGrant(node.GetGrantStmt())
	case node.GetCreateStmt() != nil:
		return convertCreateTable(node.GetCreateStmt())
	case node.GetViewStmt() != nil:
		return convertCreateView(node.GetViewStmt())
	case node.GetIndexStmt() != nil:
		return convertCreateIndex(node.GetIndexStmt())
	case node.GetCommentStmt() != nil:
		return convertComment(node.GetCommentStmt())
	case node.GetCreateExtensionStmt() != nil:
		return convertCreateExtension(node.GetCreateExtensionStmt())
	case node.GetCreateEnumStmt() != nil:
		return convertCreateEnum(node.GetCreateEnumStmt())
	case node.GetRenameStmt() != nil:
		return convertRename(node.GetRenameStmt())
	case node.GetCreateSeqStmt() != nil:
		return convertCreateSequence(node.GetCreateSeqStmt())
	case node.GetCreateTableAsStmt() != nil:
		return convertCreateTableAs(node.GetCreateTableAsStmt())
	case node.GetRefreshMatViewStmt() != nil:
		return convertRefreshMatView(node.GetRefreshMatViewStmt())
	case node.GetAlterEnumStmt() != nil:
		return convertAlterEnum(node.GetAlterEnumStmt())
	case node.GetCreateSchemaStmt() != nil:
		return convertCreateSchema(node.GetCreateSchemaStmt())
	case node.GetAlterTableStmt() != nil:
		return convertAlterTable(node.GetAlterTableStmt())
	case node.GetDropStmt() != nil:
		return convertDrop(node.GetDropStmt())
	case node.GetTruncateStmt() != nil:
		return convertTruncate(node.GetTruncateStmt())
	case node.GetTransactionStmt() != nil:
		return convertTransaction(node.GetTransactionStmt())
	default:
		return nil, sqlparse.Unsupportedf("statement node %T", node.GetNode())
	}
}
