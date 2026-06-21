package querytrace

// Severity ranks a Warning's importance.
type Severity int

const (
	// SeverityInfo is advisory.
	SeverityInfo Severity = iota
	// SeverityWarn is a likely problem.
	SeverityWarn
	// SeverityError is a strong expectation violation, such as a forbidden
	// query.
	SeverityError
)

// String renders the severity.
func (s Severity) String() string {
	switch s {
	case SeverityError:
		return "error"
	case SeverityWarn:
		return "warn"
	default:
		return "info"
	}
}

// WarningCode identifies a kind of warning. Codes are stable strings so they can
// be matched in tests and exporters.
type WarningCode string

const (
	// WarnPossibleNPlusOne marks a fingerprint repeated above the N+1
	// threshold within a Trace.
	WarnPossibleNPlusOne WarningCode = "possible_n_plus_one"
	// WarnQueryInLoop marks the same fingerprint repeated from the same caller,
	// the signature of a query inside a loop.
	WarnQueryInLoop WarningCode = "query_in_loop"
	// WarnTooManyQueries marks a Trace exceeding its query budget.
	WarnTooManyQueries WarningCode = "too_many_queries"
	// WarnWriteInReadOnly marks a write under a ReadOnly context.
	WarnWriteInReadOnly WarningCode = "write_in_read_only"
	// WarnQueryForbidden marks any statement under a ForbidQueries context.
	WarnQueryForbidden WarningCode = "query_forbidden"
	// WarnWriteForbidden marks a write under a ForbidWrites context.
	WarnWriteForbidden WarningCode = "write_forbidden"
	// WarnUpdateWithoutWhere marks an UPDATE with no WHERE clause.
	WarnUpdateWithoutWhere WarningCode = "update_without_where"
	// WarnDeleteWithoutWhere marks a DELETE with no WHERE clause.
	WarnDeleteWithoutWhere WarningCode = "delete_without_where"
	// WarnRowsNotClosed marks a row cursor that was never closed.
	WarnRowsNotClosed WarningCode = "rows_not_closed"
	// WarnQueryCanceled marks a row cursor abandoned through context
	// cancellation while it was still being iterated. Execution-level
	// cancellation, slow queries, and plain errors are left to OpenTelemetry
	// tracing, which records them as span duration and status; the mid-iteration
	// case is invisible to a query span that has already ended.
	WarnQueryCanceled WarningCode = "query_canceled"
	// WarnQueryDeadlineExceeded marks a row cursor that hit its context deadline
	// while it was still being iterated. See WarnQueryCanceled on scope.
	WarnQueryDeadlineExceeded WarningCode = "query_deadline_exceeded"
	// WarnTooManyParameters marks a statement carrying at or above the bind
	// parameter budget, the signature of a large IN list or oversized batch.
	WarnTooManyParameters WarningCode = "too_many_parameters"
	// WarnLargeInList marks a statement whose top-level WHERE carries an IN (...)
	// value list at or above the configured length, a shape that strains the
	// planner and the bind-parameter limit and is usually better expressed as a
	// join or a temporary table.
	WarnLargeInList WarningCode = "large_in_list"
	// WarnSelectStar marks a SELECT that projects every column with "*", which
	// over-fetches, couples the caller to the table's column set, and defeats
	// covering indexes.
	WarnSelectStar WarningCode = "select_star"
	// WarnMissingTenantPredicate marks a read or write that touches a base table
	// without referencing any of the required tenant-scoping columns in its
	// top-level WHERE, the signature of a query that can cross tenant boundaries.
	WarnMissingTenantPredicate WarningCode = "missing_tenant_predicate"
	// WarnMissingSoftDeletePredicate marks a SELECT that touches a base table
	// without referencing a soft-delete marker column in its top-level WHERE, so
	// it may return logically deleted rows.
	WarnMissingSoftDeletePredicate WarningCode = "missing_soft_delete_predicate"
	// WarnEqNull marks a comparison against a NULL literal with = or <>
	// (x = NULL / x <> NULL), which SQL evaluates to unknown rather than matching
	// NULL, so the predicate silently drops every row; IS NULL / IS NOT NULL is
	// meant.
	WarnEqNull WarningCode = "eq_null"
	// WarnWriteOutsideTransaction marks two or more writes issued outside any
	// transaction within one Trace — statements that cannot roll back together if
	// one fails, a consistency risk a single autocommit write does not carry.
	WarnWriteOutsideTransaction WarningCode = "write_outside_transaction"
	// WarnExternalCallInTransaction marks a Mark (an application step such as an
	// external API call) recorded while a transaction was open: work that holds the
	// transaction — and its locks and pinned connection — open across a
	// non-database operation.
	WarnExternalCallInTransaction WarningCode = "external_call_in_transaction"
)

// Warning is one problem querytrace found in a Trace. Warnings are advisory: a
// Trace records them, exporters surface them, and querytest asserts on them, but
// they never interrupt execution.
type Warning struct {
	// Code identifies the kind of warning.
	Code WarningCode
	// Message is a human-readable description.
	Message string
	// Severity ranks the warning.
	Severity Severity
	// EventID links the warning to the Event that triggered it, when one did.
	EventID string
	// Caller is the application code involved, when known.
	Caller *Caller
	// Details carries structured specifics (fingerprint, count, ...).
	Details map[string]any
}
