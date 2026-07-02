package analysis

import (
	"strings"

	"github.com/aita/sqlkit/sql"
)

// StmtKind classifies a SQL statement by the kind of statement it represents.
// It is a read-only label for tracing and analysis (query budgets, N+1
// detection, read-only enforcement) derived from a parsed query's shape.
type StmtKind int

const (
	// StmtUnknown is a statement of an unrecognized kind, or one that could not
	// be analyzed.
	StmtUnknown StmtKind = iota
	// StmtSelect is a row-returning read: SELECT, a set operation (UNION etc.),
	// or a standalone VALUES.
	StmtSelect
	// StmtInsert is an INSERT.
	StmtInsert
	// StmtUpdate is an UPDATE.
	StmtUpdate
	// StmtDelete is a DELETE.
	StmtDelete
	// StmtDDL is a schema statement (CREATE/ALTER/DROP/TRUNCATE/COMMENT).
	StmtDDL
	// StmtCall is a stored procedure call (CALL). Its effects are not
	// statically knowable — the procedure body may read, write, or do
	// nothing — so IsWrite reports true to stay on the safe side, the same
	// fail-safe stance StmtKind takes nowhere else but that Statement.Access's
	// AccessUnknown documents for an equivalent case.
	StmtCall
)

// String renders the kind for logs and reports.
func (k StmtKind) String() string {
	switch k {
	case StmtSelect:
		return "select"
	case StmtInsert:
		return "insert"
	case StmtUpdate:
		return "update"
	case StmtDelete:
		return "delete"
	case StmtDDL:
		return "ddl"
	case StmtCall:
		return "call"
	default:
		return "unknown"
	}
}

// IsRead reports whether the statement only reads rows.
func (k StmtKind) IsRead() bool { return k == StmtSelect }

// IsWrite reports whether the statement modifies data (INSERT/UPDATE/DELETE),
// or might (CALL, whose procedure body is not statically knowable).
func (k StmtKind) IsWrite() bool {
	return k == StmtInsert || k == StmtUpdate || k == StmtDelete || k == StmtCall
}

// FiltersRows reports whether the statement selects the rows it acts on through
// a WHERE clause — a SELECT, UPDATE, or DELETE. An INSERT does not (it names its
// rows directly).
func (k StmtKind) FiltersRows() bool {
	return k == StmtSelect || k == StmtUpdate || k == StmtDelete
}

// KindOf reports the kind of a parsed statement from its AST. ok is false when q
// is nil or of an unrecognized type, the same condition under which the other
// analysis functions yield nothing, so a caller can fall back to DetectKind on
// the raw SQL text. It reads only the statement's top-level type through the sql
// introspection seam, never compiling or mutating q.
func KindOf(q sql.Query) (StmtKind, bool) {
	switch v := sql.Unwrap(q).(type) {
	case nil:
		return StmtUnknown, false
	case sql.SelectStmt, sql.CompoundStmt, sql.ValuesQuery:
		return StmtSelect, true
	case sql.InsertStmt:
		return StmtInsert, true
	case sql.UpdateStmt:
		return StmtUpdate, true
	case sql.DeleteStmt:
		return StmtDelete, true
	default:
		if _, isDDL := ddlTables(v); isDDL {
			return StmtDDL, true
		}
		return StmtUnknown, false
	}
}

// DetectKind guesses a statement's kind from its leading keyword, the fallback
// when no AST is available (a parse failed, or no parser was configured). It
// returns StmtUnknown when the leading token is unrecognized.
func DetectKind(sqlText string) StmtKind {
	switch leadingKeyword(sqlText) {
	case "SELECT", "WITH", "VALUES", "TABLE":
		return StmtSelect
	case "INSERT":
		return StmtInsert
	case "UPDATE":
		return StmtUpdate
	case "DELETE":
		return StmtDelete
	case "CALL":
		// A stored procedure call: sqlparse frontends have no AST for it and
		// fall back to a raw passthrough, so KindOf never classifies one —
		// every CALL reaches DetectKind. Without this case it fell through to
		// StmtUnknown, whose IsWrite() is false: a procedure that writes
		// internally would silently bypass ForbidWrites/ReadOnly enforcement,
		// with no signal at all (unlike a rejected construct, which at least
		// fails loudly).
		return StmtCall
	case "CREATE", "ALTER", "DROP", "TRUNCATE", "COMMENT":
		return StmtDDL
	default:
		return StmtUnknown
	}
}

// leadingKeyword returns the upper-cased first word of sqlText, skipping leading
// whitespace and a single leading line/block comment.
func leadingKeyword(sqlText string) string {
	s := strings.TrimSpace(sqlText)
	for {
		switch {
		case strings.HasPrefix(s, "--"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = strings.TrimSpace(s[i+1:])
				continue
			}
			return ""
		case strings.HasPrefix(s, "/*"):
			if i := strings.Index(s, "*/"); i >= 0 {
				s = strings.TrimSpace(s[i+2:])
				continue
			}
			return ""
		}
		break
	}
	i := strings.IndexFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '(' || r == ';'
	})
	if i < 0 {
		i = len(s)
	}
	return strings.ToUpper(s[:i])
}
