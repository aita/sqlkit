package querytrace

import (
	"context"
	"errors"
	"fmt"
)

// deriveWarnings computes the whole-trace warnings from the recorded events:
// the query budget, repeated query shapes (N+1 and query-in-loop), and unclosed
// row cursors. The caller holds t.mu. It allocates fresh warnings each call so
// repeated Warnings/Report calls never accumulate duplicates.
func (t *Trace) deriveWarnings() []Warning {
	var out []Warning
	out = append(out, t.deriveTooManyQueries()...)
	out = append(out, t.deriveRepeated()...)
	out = append(out, t.deriveRowsNotClosed()...)
	out = append(out, t.derivePerStatement()...)
	return out
}

// derivePerStatement scans the recorded statements once and raises the
// per-statement warnings querytrace keeps when database-side statistics and
// OpenTelemetry tracing are assumed to cover the rest: a context cancellation
// or deadline that ended row iteration (a request abandoned after the database
// began streaming, invisible to a query span that has already ended), and a
// statement at or above the bind-parameter budget. Execution latency and
// errors are left to tracing. The caller holds t.mu.
func (t *Trace) derivePerStatement() []Warning {
	maxParams := t.cfg.Warnings.MaxParams

	var out []Warning
	for _, ev := range t.events {
		if ev.Kind != EventQuery && ev.Kind != EventExec {
			continue
		}
		// A context cancellation or deadline that ended row iteration — the
		// request abandoned after the database began streaming rows.
		if ev.Rows != nil && ev.Rows.IterErr != nil {
			if code, ok := classifyCtxErr(ev.Rows.IterErr); ok {
				w := ctxCancelWarning(ev, code, "iteration")
				w.Details["rows_read"] = ev.Rows.NextCount
				out = append(out, w)
			}
		}
		if maxParams > 0 && ev.Query != nil && ev.Query.ParamCount >= maxParams {
			d := fingerprintDetails(ev.Query)
			d["param_count"] = ev.Query.ParamCount
			d["budget"] = maxParams
			out = append(out, Warning{
				Code:     WarnTooManyParameters,
				Message:  fmt.Sprintf("statement carries %d bind parameters, at or above the budget of %d", ev.Query.ParamCount, maxParams),
				Severity: SeverityWarn,
				EventID:  ev.ID,
				Caller:   ev.Caller,
				Details:  d,
			})
		}
	}
	return out
}

func (t *Trace) deriveTooManyQueries() []Warning {
	budget := t.cfg.Warnings.MaxQueries
	if budget <= 0 {
		return nil
	}
	count := t.queryCount()
	if count <= budget {
		return nil
	}
	return []Warning{{
		Code:     WarnTooManyQueries,
		Message:  fmt.Sprintf("%d queries exceed the budget of %d", count, budget),
		Severity: SeverityWarn,
		Details:  map[string]any{"count": count, "budget": budget},
	}}
}

// repeatGroup accumulates the occurrences of one fingerprint.
type repeatGroup struct {
	fingerprint string
	operation   StmtKind
	name        string
	count       int
	callers     map[string]*Caller // distinct caller sites
	firstCaller *Caller
}

// deriveRepeated reports fingerprints that repeat at or above the N+1 threshold.
// A group whose repeats all come from a single caller site is a query in a loop;
// a group spread across call sites is a possible N+1.
func (t *Trace) deriveRepeated() []Warning {
	if !t.cfg.NPlusOne.Enabled {
		return nil
	}
	min := t.cfg.nPlusOneMinCount()
	ignored := ignoredOps(t.cfg.NPlusOne.IgnoreOperations)

	groups := make(map[string]*repeatGroup)
	var order []string
	for _, ev := range t.events {
		q := ev.Query
		if q == nil || q.Fingerprint == "" {
			continue
		}
		if ignored[q.Operation] {
			continue
		}
		g := groups[q.Fingerprint]
		if g == nil {
			g = &repeatGroup{
				fingerprint: q.Fingerprint,
				operation:   q.Operation,
				name:        ev.Name,
				callers:     make(map[string]*Caller),
			}
			groups[q.Fingerprint] = g
			order = append(order, q.Fingerprint)
		}
		g.count++
		if ev.Caller != nil {
			key := callerKey(ev.Caller)
			if _, seen := g.callers[key]; !seen {
				g.callers[key] = ev.Caller
			}
			if g.firstCaller == nil {
				g.firstCaller = ev.Caller
			}
		}
	}

	var out []Warning
	for _, fp := range order {
		g := groups[fp]
		if g.count < min {
			continue
		}
		details := map[string]any{
			"fingerprint": g.fingerprint,
			"count":       g.count,
			"operation":   g.operation.String(),
		}
		if g.name != "" {
			details["query_name"] = g.name
		}
		// A single distinct caller site means the repeats came from one place:
		// a loop. Anything else is a cross-site N+1 fan-out.
		if len(g.callers) == 1 {
			out = append(out, Warning{
				Code:     WarnQueryInLoop,
				Message:  fmt.Sprintf("query repeated %d times from one call site", g.count),
				Severity: SeverityWarn,
				Caller:   g.firstCaller,
				Details:  details,
			})
		} else {
			out = append(out, Warning{
				Code:     WarnPossibleNPlusOne,
				Message:  fmt.Sprintf("query shape repeated %d times", g.count),
				Severity: SeverityWarn,
				Caller:   g.firstCaller,
				Details:  details,
			})
		}
	}
	return out
}

func (t *Trace) deriveRowsNotClosed() []Warning {
	var out []Warning
	for _, ev := range t.events {
		if ev.Rows == nil || ev.Rows.Closed {
			continue
		}
		details := map[string]any{"next_count": ev.Rows.NextCount}
		if ev.Query != nil && ev.Query.Fingerprint != "" {
			details["fingerprint"] = ev.Query.Fingerprint
		}
		out = append(out, Warning{
			Code:     WarnRowsNotClosed,
			Message:  "rows cursor was never closed",
			Severity: SeverityWarn,
			EventID:  ev.ID,
			Caller:   ev.Caller,
			Details:  details,
		})
	}
	return out
}

// queryCount counts the read and write statements (not transaction or mark
// events). The caller holds t.mu.
func (t *Trace) queryCount() int {
	n := 0
	for _, ev := range t.events {
		if ev.Kind == EventQuery || ev.Kind == EventExec {
			n++
		}
	}
	return n
}

func ignoredOps(ops []StmtKind) map[StmtKind]bool {
	if len(ops) == 0 {
		return nil
	}
	m := make(map[StmtKind]bool, len(ops))
	for _, op := range ops {
		m[op] = true
	}
	return m
}

func callerKey(c *Caller) string {
	if c == nil {
		return ""
	}
	return fmt.Sprintf("%s:%d", c.File, c.Line)
}

// fingerprintDetails builds the Details map shared by the per-statement
// warnings: the fingerprint and operation of the offending query.
func fingerprintDetails(q *QueryInfo) map[string]any {
	if q == nil {
		return map[string]any{}
	}
	d := map[string]any{"operation": q.Operation.String()}
	if q.Fingerprint != "" {
		d["fingerprint"] = q.Fingerprint
	}
	return d
}

// classifyCtxErr maps a context cancellation or deadline error to its warning
// code, returning ok=false for any other (or nil) error.
func classifyCtxErr(err error) (WarningCode, bool) {
	switch {
	case errors.Is(err, context.Canceled):
		return WarnQueryCanceled, true
	case errors.Is(err, context.DeadlineExceeded):
		return WarnQueryDeadlineExceeded, true
	default:
		return "", false
	}
}

// ctxCancelWarning builds a query_canceled or query_deadline_exceeded warning
// for the given phase ("iteration"), recorded in the Details.
func ctxCancelWarning(ev Event, code WarningCode, phase string) Warning {
	verb := "was canceled"
	if code == WarnQueryDeadlineExceeded {
		verb = "exceeded its deadline"
	}
	d := fingerprintDetails(ev.Query)
	d["phase"] = phase
	return Warning{
		Code:     code,
		Message:  fmt.Sprintf("query %s during %s", verb, phase),
		Severity: SeverityWarn,
		EventID:  ev.ID,
		Caller:   ev.Caller,
		Details:  d,
	}
}
