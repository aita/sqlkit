package querytrace

import (
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
		return nil
	}
	d := map[string]any{"operation": q.Operation.String()}
	if q.Fingerprint != "" {
		d["fingerprint"] = q.Fingerprint
	}
	return d
}
