// Package mysql parses MySQL statements into github.com/aita/sqlkit/sql
// queries. It is the MySQL frontend for github.com/aita/sqlkit/sqlparse,
// backed by the TiDB parser (github.com/pingcap/tidb/pkg/parser), a pure-Go
// implementation of the MySQL grammar.
//
// It lives in its own module so the parser's dependency tree stays isolated from
// callers that only need the sqlparse abstraction or the sql package.
//
//	q, err := mysql.ParseOne("SELECT id FROM users WHERE active = 1")
//	text, args, err := q.ToSQL(sql.MySQLDialect{})
//
// # Limitations
//
// See the [github.com/aita/sqlkit/sqlparse] package docs for the limitations
// common to every frontend (partial mapping with a deparsed RawSQL fallback,
// loss of comments and formatting on that fallback, and so on). Backend-specific
// notes:
//
//   - Positions are reconstructed, not native. The TiDB parser does not record a
//     usable byte offset on its nodes, so a statement's position is computed from
//     the running length of the preceding statements' verbatim text, and table
//     offsets within a statement are recovered heuristically from that text.
//
//   - MySQL-only construct gaps fall back to RawSQL, including RETURNING on
//     INSERT/UPDATE/DELETE, REPLACE ... ON DUPLICATE KEY UPDATE, LIMIT with
//     OFFSET on UPDATE/DELETE, CREATE TABLE ... LIKE / AS SELECT and table
//     options, functional index keys, and LIKE ... ESCAPE.
package mysql

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	"github.com/pingcap/tidb/pkg/parser"
	"github.com/pingcap/tidb/pkg/parser/ast"
	"github.com/pingcap/tidb/pkg/parser/format"

	// The TiDB parser represents literal values through a pluggable driver that
	// registers ast.NewValueExpr at init time. test_driver is the reference
	// implementation shipped with the parser and the documented choice for
	// standalone (non-TiDB) parsing; despite its name it carries no test-only
	// behavior, only the value/datum types literals decode into.
	_ "github.com/pingcap/tidb/pkg/parser/test_driver"
)

// Parser parses MySQL SQL text into sqlkit/sql queries. The zero value is ready
// to use; it implements sqlparse.Parser.
type Parser struct{}

// New returns a MySQL Parser.
func New() Parser { return Parser{} }

var _ sqlparse.Parser = Parser{}

// Parse parses one or more MySQL statements in source order.
func (Parser) Parse(text string) ([]sql.Query, error) {
	// SQL source is text, which is UTF-8. This is encoding validation, not a
	// rejection of any SQL construct: the TiDB lexer does not faithfully accept an
	// invalid byte — it silently maps it to U+FFFD/"?", so the byte is lost on the
	// first parse and a round-trip is never stable. Reject it rather than emit the
	// corrupted result.
	if !utf8.ValidString(text) {
		return nil, fmt.Errorf("sqlkit/sqlparse/mysql: input is not valid UTF-8")
	}
	stmts, err := parseStmts(text)
	if err != nil {
		return nil, err
	}
	queries := make([]sql.Query, 0, len(stmts))
	// The TiDB parser does not record a usable offset on statement nodes, but each
	// statement's Text() is the verbatim source slice and the statements partition
	// the source contiguously, so the running sum of preceding statement lengths is
	// the current statement's byte offset.
	offset := 0
	for _, stmt := range stmts {
		stmtOffset := offset
		offset += len(stmt.Text())
		query, err := convertStmt(stmt, buildTablePos(stmt, stmtOffset))
		if err != nil {
			// A construct the converters recognize but cannot model as typed nodes
			// still round-trips: deparse just this statement back to SQL and keep it
			// as a RawQuery, so every statement the MySQL grammar accepts yields a
			// usable Query. Genuine parse failures are not UnsupportedError and so
			// still surface.
			var unsupported *sqlparse.UnsupportedError
			if errors.As(err, &unsupported) {
				rawSQL, derr := deparseStmt(stmt)
				if derr != nil {
					return nil, err
				}
				queries = append(queries, sql.RawSQL(rawSQL))
				continue
			}
			return nil, err
		}
		queries = append(queries, sql.SetPos(query, stmtOffset+1))
	}
	return queries, nil
}

// parseStmts runs the TiDB parser, converting a panic into an error. The
// reference value driver (test_driver) panics on inputs that exercise an
// unimplemented MyDecimal branch — e.g. an over-long numeric literal — and a
// library must not crash the process on untrusted input.
func parseStmts(text string) (stmts []ast.StmtNode, err error) {
	defer func() {
		if r := recover(); r != nil {
			stmts, err = nil, fmt.Errorf("sqlkit/sqlparse/mysql: parser panic: %v", r)
		}
	}()
	stmts, _, err = parser.New().Parse(text, "", "")
	return stmts, err
}

// deparseStmt renders a single parsed statement back to MySQL SQL text via the
// TiDB parser's own restore, using backtick-quoted names and upper-case keywords.
//
// TiDB's restore is not always idempotent: for a SET system variable, the first
// restore of "SET A=0" keeps the source case ("SET @@SESSION.`A`=0") while a
// restore of *that* lower-cases the (case-insensitive) variable to `a`. A raw
// passthrough must be stable under re-parsing, so restore once more and keep the
// settled form. The extra pass is a no-op for the already-stable majority.
func deparseStmt(node ast.StmtNode) (string, error) {
	first, err := restoreStmt(node)
	if err != nil {
		return "", err
	}
	// Settle the restore to a re-parse fixpoint (see the SET note above). The
	// re-parse can also panic in the value driver on an over-long numeric literal
	// the first restore happened to emit; on a panic, error, or restore failure
	// keep the first form rather than crash.
	if second, ok := settledRestore(first); ok {
		return second, nil
	}
	return first, nil
}

// settledRestore re-parses and re-restores restored SQL, returning the settled
// form. It recovers a value-driver panic and reports ok=false on any failure so
// the caller keeps the first restore.
func settledRestore(restored string) (out string, ok bool) {
	defer func() {
		if recover() != nil {
			out, ok = "", false
		}
	}()
	stmt, err := parser.New().ParseOneStmt(restored, "", "")
	if err != nil {
		return "", false
	}
	second, err := restoreStmt(stmt)
	if err != nil {
		return "", false
	}
	return second, true
}

// restoreStmt renders one statement node to SQL via the TiDB restore.
func restoreStmt(node ast.StmtNode) (string, error) {
	var sb strings.Builder
	ctx := format.NewRestoreCtx(format.DefaultRestoreFlags, &sb)
	if err := node.Restore(ctx); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// Parse parses one or more MySQL statements using a default Parser.
func Parse(text string) ([]sql.Query, error) { return New().Parse(text) }

// ParseOne parses exactly one MySQL statement.
func ParseOne(text string) (sql.Query, error) { return sqlparse.ParseOne(New(), text) }

// convertStmt dispatches a top-level statement node to its converter. tp carries
// the statement's recovered table offsets to the DML converters; DDL converters
// take none.
func convertStmt(node ast.StmtNode, tp tablePos) (sql.Query, error) {
	switch s := node.(type) {
	case *ast.SelectStmt:
		return convertSelect(s, tp)
	case *ast.SetOprStmt:
		return convertSetOpr(s, tp)
	case *ast.InsertStmt:
		return convertInsert(s, tp)
	case *ast.UpdateStmt:
		return convertUpdate(s, tp)
	case *ast.DeleteStmt:
		return convertDelete(s, tp)
	case *ast.CreateTableStmt:
		return convertCreateTable(s)
	case *ast.AlterTableStmt:
		return convertAlterTable(s)
	case *ast.DropTableStmt:
		return convertDropTable(s)
	case *ast.CreateIndexStmt:
		return convertCreateIndex(s)
	case *ast.DropIndexStmt:
		return convertDropIndex(s)
	case *ast.RenameTableStmt:
		return convertRenameTable(s)
	case *ast.TruncateTableStmt:
		return convertTruncate(s)
	case *ast.CreateDatabaseStmt:
		return convertCreateDatabase(s)
	case *ast.DropDatabaseStmt:
		return convertDropDatabase(s)
	case *ast.UseStmt:
		return convertUse(s)
	case *ast.LockTablesStmt:
		return convertLockTables(s)
	case *ast.UnlockTablesStmt:
		return convertUnlockTables(s)
	default:
		return nil, sqlparse.Unsupportedf("statement node %T", node)
	}
}
