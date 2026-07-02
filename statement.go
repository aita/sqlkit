package sqlkit

import (
	"errors"
	"strings"

	"github.com/aita/sqlkit/sql"
)

// errNoStatementQuery is returned by Statement.Recompile when there is no AST
// to recompile (e.g. a raw-string statement).
var errNoStatementQuery = errors.New("sqlkit: statement has no Query to recompile")

// Op is the kind of statement a hook sees: a row-returning read or a
// result-returning write.
type Op int

const (
	// OpQuery is a read that yields rows (SELECT, a RETURNING read, RawSQL).
	OpQuery Op = iota
	// OpExec is a write that yields a sql.Result (INSERT, UPDATE, DELETE, and
	// the DDL statements run through Exec).
	OpExec
)

// String renders the Op for logs and errors.
func (o Op) String() string {
	switch o {
	case OpQuery:
		return "query"
	case OpExec:
		return "exec"
	default:
		return "unknown"
	}
}

// Statement is the query a hook inspects and may rewrite before it runs, and the
// value a Router routes on.
//
// SQL and Args are the source of truth for execution: the executor runs exactly
// what they hold, so a Before hook that rewrites them changes what reaches the
// database. Query carries the SQL DSL (the sql.Query AST) the statement was
// compiled from, for hooks and routers that want to branch on its structure —
// the target table, the kind of statement, and so on. It is nil when there is no
// AST, as with a RawSQL string. To rewrite at the AST level, build a new query,
// assign it to Query, and call Recompile to refresh SQL and Args from it.
type Statement struct {
	// Op is OpQuery for reads and OpExec for writes.
	Op Op
	// Query is the SQL DSL the statement was compiled from, or nil for a
	// raw-string statement (RawSQL with a string).
	Query sql.Query
	// SQL is the compiled SQL text that will run.
	SQL string
	// Args are the bind arguments that will run.
	Args []any
	// Dialect is the dialect SQL was compiled for; Recompile uses it.
	Dialect sql.Dialect
	// RouteHint is an advisory backend alias set by WithRoute. A Router reads it
	// from Route to honor (or override) a caller's routing preference; it is
	// empty when no hint was given. It is purely a hint — the Router decides what
	// to do with it.
	RouteHint string
}

// Recompile recompiles Query for the statement's Dialect and replaces SQL and
// Args with the result. It is the AST-level rewrite path: set Query to a new
// builder result, then call Recompile. It returns an error if Query is nil or
// compilation fails.
func (s *Statement) Recompile() error {
	if s.Query == nil {
		return errNoStatementQuery
	}
	sqlText, args, err := s.Query.ToSQL(s.Dialect)
	if err != nil {
		return err
	}
	s.SQL = sqlText
	s.Args = args
	return nil
}

// StatementKind is the normalized kind of a statement, derived from its AST
// (Statement.Query) rather than its Op. It exists because Op alone cannot tell a
// write from a read: an INSERT/UPDATE/DELETE ... RETURNING runs through the
// row-returning path with Op == OpQuery, yet is a write. A Router branches on
// Kind (via IsWrite) to route reads and writes correctly.
type StatementKind int

const (
	// KindSelect is a row-returning read: SELECT or a set operation (UNION, ...).
	// A SELECT ... FOR UPDATE/SHARE is still KindSelect, but IsWrite reports it as
	// a write because it takes row locks.
	KindSelect StatementKind = iota
	// KindInsert is an INSERT (with or without RETURNING).
	KindInsert
	// KindUpdate is an UPDATE (with or without RETURNING).
	KindUpdate
	// KindDelete is a DELETE (with or without RETURNING).
	KindDelete
	// KindMerge is a MERGE.
	KindMerge
	// KindDDL is a schema statement (CREATE/ALTER/DROP, ...) run through Exec.
	KindDDL
	// KindRawRead is a raw-string statement (RawSQL) that begins with SELECT.
	KindRawRead
	// KindRawUnknown is a raw-string statement whose read/write nature could not
	// be determined from its text; it is treated as a write (routed to the
	// write/default backend) to stay on the safe side.
	KindRawUnknown
)

// String renders the StatementKind for logs and errors.
func (k StatementKind) String() string {
	switch k {
	case KindSelect:
		return "select"
	case KindInsert:
		return "insert"
	case KindUpdate:
		return "update"
	case KindDelete:
		return "delete"
	case KindMerge:
		return "merge"
	case KindDDL:
		return "ddl"
	case KindRawRead:
		return "raw-read"
	case KindRawUnknown:
		return "raw-unknown"
	default:
		return "unknown"
	}
}

// Kind classifies the statement from its AST, independent of Op, so a write
// carried over the row-returning path (a RETURNING statement) is still reported
// as a write. A raw-string statement (no AST) is classified from its leading
// keyword: a SELECT is KindRawRead, anything else KindRawUnknown.
func (s Statement) Kind() StatementKind {
	switch s.Query.(type) {
	case SelectQuery, UnionQuery, sql.SelectBuilder, sql.CompoundBuilder:
		return KindSelect
	case InsertQuery, sql.InsertBuilder:
		return KindInsert
	case UpdateQuery, sql.UpdateBuilder:
		return KindUpdate
	case DeleteQuery, sql.DeleteBuilder:
		return KindDelete
	case MergeQuery, sql.MergeBuilder:
		return KindMerge
	case nil:
		// A raw-string statement, or a synthetic statement (e.g. the one built to
		// route a session at Begin). With text, classify by leading keyword;
		// without, fall back to Op.
		if s.SQL == "" {
			if s.Op == OpExec {
				return KindDDL
			}
			return KindSelect
		}
		return classifyRawSQL(s.SQL)
	default:
		// A DDL statement (CreateTable, AlterTable, ...) is recognized directly
		// so it classifies correctly regardless of Op: Database.Exec/Session.Exec
		// always set Op: OpExec for DDL, but RawSQL's Rows-based path always sets
		// Op: OpQuery even when the query passed in is a DDL builder/AST — falling
		// back to Op alone would then misreport a DDL statement run through
		// RawSQL(...).Scan(...) as KindSelect (a read).
		if isDDLQuery(s.Query) {
			return KindDDL
		}
		// An AST we do not recognize by shape at all. Fall back to Op: a writing
		// Exec is DDL, a reading Query is a select.
		if s.Op == OpExec {
			return KindDDL
		}
		return KindSelect
	}
}

// isDDLQuery reports whether q is one of the sql package's DDL statement
// types (its builder form or the built AST — sql.Unwrap resolves either),
// mirroring the DDL type list analysis.KindOf recognizes.
func isDDLQuery(q sql.Query) bool {
	switch sql.Unwrap(q).(type) {
	case sql.CreateTableStmt, sql.AlterTableStmt, sql.DropTableStmt,
		sql.CreateViewStmt, sql.DropViewStmt,
		sql.CreateIndexStmt, sql.DropIndexStmt,
		sql.DropForeignKeyStmt,
		sql.CommentOnTableStmt, sql.CommentOnColumnStmt,
		sql.TruncateStmt,
		sql.CreateSchemaStmt, sql.DropSchemaStmt:
		return true
	default:
		return false
	}
}

// Access is a statement's read/write nature for routing: a definite read, a
// definite write, or undetermined. It is the three-way form of IsWrite, exposing
// the case IsWrite folds away — a statement whose nature could not be inferred
// (an opaque raw string), which IsWrite reports as a write to stay on the safe
// side. A Router that wants to handle the undetermined case differently (its own
// default, or a warning) branches on Access instead.
type Access int

const (
	// AccessRead is a definite read: a non-locking SELECT, set operation, or a
	// raw string beginning with SELECT (without a locking clause).
	AccessRead Access = iota
	// AccessWrite is a definite write: INSERT/UPDATE/DELETE/MERGE/DDL, or a
	// locking read (SELECT ... FOR UPDATE/SHARE), which must run on the primary.
	AccessWrite
	// AccessUnknown is an undetermined statement: a raw string whose read/write
	// nature could not be inferred from its text. IsWrite treats it as a write.
	AccessUnknown
)

// String renders the Access for logs and errors.
func (a Access) String() string {
	switch a {
	case AccessRead:
		return "read"
	case AccessWrite:
		return "write"
	case AccessUnknown:
		return "unknown"
	default:
		return "invalid"
	}
}

// Access reports the statement's read/write nature, including the undetermined
// case. It derives from Kind, with a locking read (SELECT ... FOR UPDATE/SHARE)
// counted as a write because it takes row locks. A raw string that could not be
// classified is AccessUnknown — distinct from a definite read — so a Router can
// decide how to route it rather than having the choice made for it.
func (s Statement) Access() Access {
	switch s.Kind() {
	case KindInsert, KindUpdate, KindDelete, KindMerge, KindDDL:
		return AccessWrite
	case KindSelect, KindRawRead:
		if s.locksRows() || s.writesViaCTE() {
			return AccessWrite
		}
		return AccessRead
	case KindRawUnknown:
		return AccessUnknown
	default:
		return AccessRead
	}
}

// IsWrite reports whether the statement must run on the primary/writer: a
// definite write, a locking read (SELECT ... FOR UPDATE/SHARE), or an
// undetermined statement (AccessUnknown), treated as a write to stay on the safe
// side. It is the boolean convenience over Access; call Access for the three-way
// read/write/unknown distinction.
func (s Statement) IsWrite() bool {
	return s.Access() != AccessRead
}

// locksRows reports whether a SELECT carries a row-locking clause (FOR UPDATE /
// FOR SHARE / FOR NO KEY UPDATE / FOR KEY SHARE), reading the AST when present
// and scanning the text for a raw string.
func (s Statement) locksRows() bool {
	switch q := s.Query.(type) {
	case SelectQuery:
		return q.query.Stmt().Lock != nil
	case sql.SelectBuilder:
		return q.Stmt().Lock != nil
	case nil:
		return hasLockingClause(s.SQL)
	}
	return false
}

// writesViaCTE reports whether a read-kinded statement (a SELECT or set
// operation) carries a data-modifying CTE — WITH x AS (INSERT/UPDATE/DELETE/MERGE
// ... RETURNING ...) SELECT .... Such a statement performs a write despite its
// read top-level kind, so it must route to the primary, not a read replica. It
// walks the AST itself (like locksRows) — mirroring analysis.writesViaCTE, which
// classifies the same shape for the tracing layer — so the routing decision does
// not silently disagree with the analyzer.
func (s Statement) writesViaCTE() bool {
	switch q := s.Query.(type) {
	case SelectQuery:
		return cteWritesInQuery(q.query)
	case UnionQuery:
		return cteWritesInQuery(q.query)
	case sql.SelectBuilder, sql.CompoundBuilder:
		return cteWritesInQuery(q)
	}
	return false
}

// cteWritesInQuery reports whether q is a SELECT or set operation whose WITH
// clause (in either arm, nested) holds a data-modifying CTE body.
func cteWritesInQuery(q sql.Query) bool {
	switch v := sql.Unwrap(q).(type) {
	case sql.SelectStmt:
		return cteListWrites(v.With)
	case sql.CompoundStmt:
		return cteWritesInQuery(v.Left) || cteWritesInQuery(v.Right)
	}
	return false
}

// cteListWrites reports whether any CTE in ctes has a data-modifying body, or
// itself carries a data-modifying CTE.
func cteListWrites(ctes []sql.CTE) bool {
	for _, c := range ctes {
		switch sql.Unwrap(c.Body()).(type) {
		case sql.InsertStmt, sql.UpdateStmt, sql.DeleteStmt, sql.MergeStmt:
			return true
		}
		if cteWritesInQuery(c.Body()) {
			return true
		}
	}
	return false
}

// hasLockingClause reports whether raw SQL text contains a SELECT row-locking
// clause. It is a conservative substring check: a false positive only routes a
// read to the primary, which is safe.
func hasLockingClause(text string) bool {
	upper := strings.ToUpper(text)
	return strings.Contains(upper, "FOR UPDATE") ||
		strings.Contains(upper, "FOR NO KEY UPDATE") ||
		strings.Contains(upper, "FOR SHARE") ||
		strings.Contains(upper, "FOR KEY SHARE")
}

// Tables returns the tables the statement targets, best effort, with the primary
// target first. It reads the AST: the target table of an INSERT/UPDATE/DELETE/
// MERGE, or the From table of a SELECT when its source is a table. It returns
// nil when no table can be determined (a subquery source, a raw string, or an
// AST shape that does not expose one), so a Router that shards by table should
// treat an empty result as "no opinion".
func (s Statement) Tables() []string {
	switch q := s.Query.(type) {
	case SelectQuery:
		if q.base != nil {
			return []string{q.base.Info().QualifiedName()}
		}
	case InsertQuery:
		return []string{q.def.Table.QualifiedName()}
	case UpdateQuery:
		return []string{q.def.Table.QualifiedName()}
	case DeleteQuery:
		return []string{q.def.Table.QualifiedName()}
	case MergeQuery:
		return []string{q.query.Stmt().Target.QualifiedName()}
	case sql.MergeBuilder:
		return []string{q.Stmt().Target.QualifiedName()}
	}
	return nil
}

// classifyRawSQL infers the kind of a raw-string statement from its leading
// keyword. Only a clear SELECT is treated as a read; anything else (including
// ambiguous WITH ... CTEs that may write) is KindRawUnknown, routed as a write
// to stay on the safe side.
func classifyRawSQL(text string) StatementKind {
	t := strings.TrimSpace(text)
	if len(t) >= 6 && strings.EqualFold(t[:6], "SELECT") {
		return KindRawRead
	}
	return KindRawUnknown
}
