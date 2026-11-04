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
	// StmtMerge is a MERGE, a data-modifying statement that conditionally
	// inserts, updates, or deletes target rows from a source.
	StmtMerge
	// StmtCall is a stored procedure call (CALL). Its effects are not
	// statically knowable — the procedure body may read, write, or do
	// nothing — so IsWrite reports true to stay on the safe side, mirroring
	// how Statement.Access treats an undetermined statement as AccessUnknown.
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
	case StmtMerge:
		return "merge"
	case StmtCall:
		return "call"
	default:
		return "unknown"
	}
}

// IsRead reports whether the statement only reads rows.
func (k StmtKind) IsRead() bool { return k == StmtSelect }

// IsWrite reports whether the statement modifies data
// (INSERT/UPDATE/DELETE/MERGE), or might (CALL, whose procedure body is not
// statically knowable).
func (k StmtKind) IsWrite() bool {
	return k == StmtInsert || k == StmtUpdate || k == StmtDelete || k == StmtMerge || k == StmtCall
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
	case sql.MergeStmt:
		return StmtMerge, true
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
	case "WITH":
		// The statement a WITH prefixes decides its kind: WITH ... INSERT /
		// UPDATE / DELETE / MERGE is a write, not a read.
		kind, _, _ := scanWith(sqlText)
		return kind
	case "SELECT", "VALUES", "TABLE":
		return StmtSelect
	case "INSERT", "REPLACE":
		// REPLACE INTO is MySQL's insert-or-replace; the parsed-AST path
		// (KindOf) already classifies it as StmtInsert via sql.InsertStmt's
		// Replace flag, so the raw-text fallback must agree — otherwise a
		// REPLACE INTO analyzed without a parser (or after a parse failure)
		// falls through to StmtUnknown, whose IsWrite() is false: a write that
		// silently bypasses ForbidWrites/ReadOnly enforcement.
		return StmtInsert
	case "UPDATE":
		return StmtUpdate
	case "DELETE":
		return StmtDelete
	case "MERGE":
		return StmtMerge
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

// scanWith reads a WITH-prefixed statement's text token by token (skipping
// string literals, quoted identifiers and comments) and reports the kind of the
// statement the CTE list prefixes — the first statement keyword at parenthesis
// depth 0 — plus whether a CTE body (depth > 0) modifies data (INSERT / UPDATE /
// DELETE / MERGE) or takes row locks (FOR UPDATE / FOR SHARE). It is the
// keyword fallback's view of a WITH statement when no AST is available, and
// errs toward reporting a write: an UPDATE inside a CTE body that is not part
// of a locking clause counts as a data-modifying CTE.
//
// Whether a backslash escapes a quote depends on the dialect (MySQL: yes;
// PostgreSQL standard strings: no), so the text is scanned both ways and the
// results merged toward the write-like side.
func scanWith(sqlText string) (kind StmtKind, writesViaCTE, locking bool) {
	k1, w1, l1 := scanWithMode(sqlText, false)
	k2, w2, l2 := scanWithMode(sqlText, true)
	kind = k1
	if k1 == StmtSelect {
		kind = k2
	}
	return kind, w1 || w2, l1 || l2
}

func scanWithMode(sqlText string, backslashEscapes bool) (kind StmtKind, writesViaCTE, locking bool) {
	kind = StmtSelect
	depth := 0
	prev := ""
	found := false
	s := sqlText
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			j := i + 1
			for j < len(s) {
				if s[j] == c {
					if j+1 < len(s) && s[j+1] == c { // doubled quote escape
						j += 2
						continue
					}
					break
				}
				if backslashEscapes && s[j] == '\\' && c != '"' {
					j++
				}
				j++
			}
			i = j + 1
			prev = ""
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			if j := strings.Index(s[i+2:], "*/"); j >= 0 {
				i += j + 4
			} else {
				i = len(s)
			}
		case c == '(':
			depth++
			i++
			prev = ""
		case c == ')':
			depth--
			i++
			prev = ""
		case isWordByte(c):
			j := i
			for j < len(s) && isWordByte(s[j]) {
				j++
			}
			word := strings.ToUpper(s[i:j])
			i = j
			if depth > 0 {
				switch word {
				case "INSERT", "DELETE", "MERGE":
					writesViaCTE = true
				case "UPDATE":
					if prev == "FOR" || prev == "KEY" {
						locking = true
					} else {
						writesViaCTE = true
					}
				case "SHARE":
					if prev == "FOR" || prev == "KEY" {
						locking = true
					}
				}
			} else if !found {
				switch word {
				case "SELECT", "VALUES", "TABLE":
					kind, found = StmtSelect, true
				case "INSERT", "REPLACE":
					kind, found = StmtInsert, true
				case "UPDATE":
					kind, found = StmtUpdate, true
				case "DELETE":
					kind, found = StmtDelete, true
				case "MERGE":
					kind, found = StmtMerge, true
				}
			} else if word == "UPDATE" || word == "SHARE" {
				if prev == "FOR" || prev == "KEY" {
					locking = true
				}
			}
			prev = word
		default:
			i++
			if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				prev = ""
			}
		}
	}
	return kind, writesViaCTE, locking
}

func isWordByte(c byte) bool {
	return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}
