// Package postgres parses PostgreSQL statements into github.com/aita/sqlkit/sql
// queries. It is the PostgreSQL frontend for github.com/aita/sqlkit/x/sqlparse,
// backed by pg_query_go (the real PostgreSQL grammar via libpg_query).
//
// It lives in its own module so the pg_query_go (cgo) dependency stays isolated
// from callers that only need the sqlparse abstraction or the sql package.
//
//	q, err := postgres.ParseOne("SELECT id FROM users WHERE active")
//	text, args, err := q.ToSQL(sql.PostgreSQLDialect{})
package postgres

import (
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/x/sqlparse"
	pg "github.com/pganalyze/pg_query_go/v6"
)

// Parser parses PostgreSQL SQL text into sqlkit/sql queries. The zero value is
// ready to use; it implements sqlparse.Parser.
type Parser struct{}

// New returns a PostgreSQL Parser.
func New() Parser { return Parser{} }

var _ sqlparse.Parser = Parser{}

// Parse parses one or more PostgreSQL statements in source order.
func (Parser) Parse(text string) ([]sql.Query, error) {
	result, err := pg.Parse(text)
	if err != nil {
		return nil, err
	}
	stmts := result.GetStmts()
	queries := make([]sql.Query, 0, len(stmts))
	for _, raw := range stmts {
		query, err := convertStmt(raw.GetStmt())
		if err != nil {
			return nil, err
		}
		queries = append(queries, query)
	}
	return queries, nil
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
	case node.GetCreateStmt() != nil:
		return convertCreateTable(node.GetCreateStmt())
	case node.GetCreateSchemaStmt() != nil:
		return convertCreateSchema(node.GetCreateSchemaStmt())
	case node.GetAlterTableStmt() != nil:
		return convertAlterTable(node.GetAlterTableStmt())
	case node.GetDropStmt() != nil:
		return convertDrop(node.GetDropStmt())
	default:
		return nil, sqlparse.Unsupportedf("statement node %T", node.GetNode())
	}
}
