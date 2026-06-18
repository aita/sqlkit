// Package querytrace is an application-level SQL tracing middleware for
// database/sql. It records the queries a unit of work issues
// together with their application context — the operation, the calling code, the
// transaction, and how the result rows were consumed — and surfaces problems
// that database-side statistics cannot see: N+1 patterns, queries in a loop,
// query-count blow-ups, writes where only reads were expected, and rows left
// unclosed.
//
// It is not an execution-plan analyzer or an APM agent. Plan/index/lock analysis
// belongs to the database and to OpenTelemetry/APM; querytrace answers a
// different question — "what did this request/usecase ask the database to do,
// and from where?"
//
// The center of the design is the Trace, a per-operation recorder placed in a
// context. A wrapped database/sql driver (subpackage driver) records an Event
// per statement against the Trace it finds in the context, and a Trace turns its
// Events into a Report and a set of Warnings. SQL is analyzed best-effort through
// an injected sqlparse.Parser; analysis never aborts execution.
package querytrace

import (
	"time"
)

// EventKind is the kind of thing an Event records. SQL reads and writes are the
// common cases; the kind is an enum so transaction and mark events can share the
// timeline.
type EventKind string

const (
	// EventQuery is a row-returning read (SELECT and other reads).
	EventQuery EventKind = "query"
	// EventExec is a write returning a result (INSERT/UPDATE/DELETE/DDL).
	EventExec EventKind = "exec"
	// EventTx is a transaction control point (begin/commit/rollback).
	EventTx EventKind = "tx"
	// EventMark is an application-supplied marker on the timeline.
	EventMark EventKind = "mark"
)

// Event is one recorded point on a Trace's timeline: a query, an exec, a
// transaction control point, or a mark. Events are ordered by StartedAt.
type Event struct {
	// ID identifies the event within its Trace.
	ID string
	// Kind is the kind of event.
	Kind EventKind
	// Name is an optional application label for the event, such as a query
	// name ("UserRepository.FindByID") set with WithQueryName or Named.
	Name string

	// StartedAt is when the operation began.
	StartedAt time.Time
	// EndedAt is when it finished; zero until then.
	EndedAt time.Time
	// Duration is EndedAt-StartedAt, the time the statement took to return
	// (for a query, the time until the rows cursor is returned — not the time
	// to iterate it; see Rows).
	Duration time.Duration

	// Query carries SQL details for EventQuery and EventExec.
	Query *QueryInfo
	// Tx carries transaction details for EventTx.
	Tx *TxInfo
	// Rows carries row-cursor lifecycle details for a read whose cursor was
	// wrapped; nil for execs and for reads that returned no wrapped cursor.
	Rows *RowsInfo

	// Caller is the application code that issued the statement, captured
	// subject to Config.CaptureCaller.
	Caller *Caller
	// Labels are context labels attached with WithLabel.
	Labels map[string]string

	// Err is the error the statement returned, if any.
	Err error
}

// QueryInfo holds the SQL details of an EventQuery or EventExec. Raw SQL and
// argument values are captured only when configuration allows; the structural
// fields (kind, tables, shape, parameter count) are always safe to keep.
type QueryInfo struct {
	// RawSQL is the executed SQL text, kept only when Config.CaptureRawSQL is
	// set; otherwise empty.
	RawSQL string
	// Fingerprint is the normalized SQL with literals and placeholders
	// collapsed, used to group repeated queries. It is empty when
	// fingerprinting is disabled.
	Fingerprint string

	// Operation is the statement kind, from analysis or a best-effort guess.
	Operation StmtKind
	// Tables are the tables the statement references, when analysis succeeded.
	Tables []string

	// HasWhere, HasJoin, HasLimit, HasReturning mirror Shape; they are
	// false when analysis could not determine them.
	HasWhere     bool
	HasJoin      bool
	HasLimit     bool
	HasReturning bool
	// SelectStar mirrors Shape.SelectStar: the statement is a SELECT projecting
	// "*". It backs the select_star warning and is false on the fallback path.
	SelectStar bool

	// MaxInList is the length of the largest IN (...) value list in the
	// statement's top-level WHERE, or 0 when there is none. It backs the
	// large_in_list warning and is only set when the SQL parsed.
	MaxInList int

	// ParamCount is the number of bind arguments.
	ParamCount int
	// ParamTypes are the Go type names of the bind arguments, kept when
	// Config.CaptureArgs is ArgsTypesOnly or higher.
	ParamTypes []string

	// TxID links the statement to its transaction, empty outside one.
	TxID string

	// AnalyzeErr records why structural analysis fell back to a guess, for
	// diagnostics. It is never an execution error.
	AnalyzeErr error

	// analyzed reports whether the structural fields came from the AST (true)
	// rather than a keyword guess (false). Checks that depend on WHERE/JOIN
	// presence consult it to avoid false positives on the fallback path.
	analyzed bool

	// filterColumns are the bare (unqualified, lower-cased) column names
	// referenced in the statement's top-level WHERE clause. They drive the
	// tenant/soft-delete predicate checks and are populated only when the SQL
	// parsed. Unexported: an analysis detail, not part of the exported surface.
	filterColumns []string
}

// TxInfo holds the details of an EventTx.
type TxInfo struct {
	// TxID identifies the transaction within the Trace.
	TxID string
	// Action is the control point this event records.
	Action TxAction
	// StartedAt is when the transaction began; set on commit/rollback events
	// so the timeline can show the transaction's duration.
	StartedAt time.Time
}

// TxAction is a transaction control point.
type TxAction string

const (
	// TxBegin marks the start of a transaction.
	TxBegin TxAction = "begin"
	// TxCommit marks a commit.
	TxCommit TxAction = "commit"
	// TxRollback marks a rollback.
	TxRollback TxAction = "rollback"
)

// RowsInfo records the lifecycle of a query's row cursor — the cost that
// QueryContext alone hides. The difference between the query returning, the
// rows being iterated, and the cursor being closed is what distinguishes a
// fast query with a slow scan from a genuinely slow query.
type RowsInfo struct {
	// QueryReturnedAt is when QueryContext handed back the cursor.
	QueryReturnedAt time.Time
	// FirstNextAt is when the first row was read; zero if none was.
	FirstNextAt time.Time
	// LastNextAt is when the last row was read.
	LastNextAt time.Time
	// ClosedAt is when the cursor was closed; zero if it never was.
	ClosedAt time.Time

	// NextCount is the number of rows read from the cursor.
	NextCount int
	// Closed reports whether the cursor was closed.
	Closed bool

	// IterErr is the error that ended row iteration, if any: the first non-EOF
	// error returned by Next, or — when database/sql closed the cursor in
	// response to a context cancellation rather than surfacing an error through
	// Next — the context's error captured at Close. It is nil when the cursor was
	// drained to completion. A context.Canceled or context.DeadlineExceeded here
	// means the request was abandoned mid-stream, after the database had begun
	// returning rows.
	IterErr error

	// IterationDuration is LastNextAt-QueryReturnedAt, the time spent reading
	// rows.
	IterationDuration time.Duration
	// OpenDuration is ClosedAt-QueryReturnedAt, how long the cursor stayed
	// open.
	OpenDuration time.Duration
}

// Caller is the application code that issued a statement.
type Caller struct {
	// Function is the fully qualified function name.
	Function string
	// File is the source file path.
	File string
	// Line is the line number.
	Line int
}

// MarkInfo is an application event placed on the timeline with Mark, such as
// "calling payment API" or "cache miss". Marks make a transaction's timeline
// readable by interleaving non-SQL steps with the queries around them.
type MarkInfo struct {
	// Name is the marker text.
	Name string
	// At is when the mark was placed.
	At time.Time
	// Attrs are optional key/value details.
	Attrs map[string]string
}
