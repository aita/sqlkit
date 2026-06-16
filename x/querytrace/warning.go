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
)

// Warning is one problem querytrace found in a Trace. Warnings are advisory: a
// Trace records them, exporters surface them, and testkit asserts on them, but
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
