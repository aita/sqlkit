package querytrace

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Report is the summary of a Trace: the counts and durations of its statements,
// the timeline of events and marks, and the warnings. It is a plain value, safe
// to marshal to JSON or render with TextReport.
type Report struct {
	// TraceID is the trace's identifier.
	TraceID string
	// Operation is the unit of work the trace covers.
	Operation string
	// StartedAt is when the Trace was created, the start of the unit of work.
	StartedAt time.Time

	// QueryCount is the number of read and write statements.
	QueryCount int
	// WriteCount is the number of write statements.
	WriteCount int
	// UniqueFingerprintCount is the number of distinct query shapes.
	UniqueFingerprintCount int

	// TotalQueryDuration is the summed time statements took to return.
	TotalQueryDuration time.Duration
	// TotalRowsIterationDuration is the summed time spent iterating row
	// cursors.
	TotalRowsIterationDuration time.Duration

	// Events is the recorded timeline, in record order.
	Events []Event
	// Marks is the recorded application marks.
	Marks []MarkInfo
	// Warnings is every warning for the trace.
	Warnings []Warning
}

// Report builds the Report for the Trace.
func (t *Trace) Report() Report {
	t.mu.Lock()
	defer t.mu.Unlock()

	r := Report{
		TraceID:   t.id,
		Operation: t.operation,
		StartedAt: t.startedAt,
		Events:    append([]Event(nil), t.events...),
		Marks:     append([]MarkInfo(nil), t.marks...),
	}

	fingerprints := make(map[string]struct{})
	for _, ev := range t.events {
		switch ev.Kind {
		case EventQuery, EventExec:
			r.QueryCount++
			r.TotalQueryDuration += ev.Duration
			if ev.Query != nil {
				if ev.Query.Kind.IsWrite() {
					r.WriteCount++
				}
				if ev.Query.Fingerprint != "" {
					fingerprints[ev.Query.Fingerprint] = struct{}{}
				}
			}
			if ev.Rows != nil {
				r.TotalRowsIterationDuration += ev.Rows.IterationDuration
			}
		}
	}
	r.UniqueFingerprintCount = len(fingerprints)

	r.Warnings = append([]Warning(nil), t.warnings...)
	r.Warnings = append(r.Warnings, t.deriveWarnings()...)
	return r
}

// TextReport renders a human-readable report for the Trace, the form most useful
// in development logs and test failures.
func TextReport(t *Trace) string {
	return t.Report().String()
}

// String renders the Report as text: a header line of counts, the warnings, and
// the numbered timeline of queries and marks.
func (r Report) String() string {
	var b strings.Builder

	fmt.Fprintf(&b, "SQL trace: %s\n", r.Operation)
	fmt.Fprintf(&b, "queries=%d unique=%d writes=%d total_db_time=%s\n",
		r.QueryCount, r.UniqueFingerprintCount, r.WriteCount,
		r.TotalQueryDuration.Round(time.Millisecond))

	if len(r.Warnings) > 0 {
		b.WriteString("\nwarnings:\n")
		for _, w := range r.Warnings {
			fmt.Fprintf(&b, "  %s [%s]: %s\n", w.Code, w.Severity, w.Message)
			if w.Caller != nil {
				fmt.Fprintf(&b, "    caller=%s:%d\n", w.Caller.File, w.Caller.Line)
			}
		}
	}

	timeline := r.timeline()
	if len(timeline) > 0 {
		b.WriteString("\ntimeline:\n")
		for i, item := range timeline {
			fmt.Fprintf(&b, "  %02d  %s\n", i+1, item)
		}
	}
	return b.String()
}

// timelineItem is one rendered line of the merged event/mark timeline, with the
// time it occurred for ordering.
type timelineItem struct {
	at   time.Time
	text string
}

// timeline merges events and marks into a single time-ordered list of rendered
// lines.
func (r Report) timeline() []string {
	items := make([]timelineItem, 0, len(r.Events)+len(r.Marks))
	for _, ev := range r.Events {
		items = append(items, timelineItem{at: ev.StartedAt, text: renderEvent(ev)})
	}
	for _, m := range r.Marks {
		items = append(items, timelineItem{at: m.At, text: "MARK " + m.Name})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].at.Before(items[j].at)
	})
	lines := make([]string, len(items))
	for i, it := range items {
		lines[i] = it.text
	}
	return lines
}

func renderEvent(ev Event) string {
	switch ev.Kind {
	case EventTx:
		action := ""
		if ev.Tx != nil {
			action = string(ev.Tx.Action)
		}
		return fmt.Sprintf("TX   %s", strings.ToUpper(action))
	case EventQuery, EventExec:
		label := ""
		if ev.Query != nil {
			label = ev.Query.Fingerprint
		}
		if ev.Name != "" {
			label = ev.Name + "  " + label
		}
		return fmt.Sprintf("%-7s %-6s %s", strings.ToUpper(string(ev.Kind)),
			ev.Duration.Round(time.Millisecond), label)
	default:
		return string(ev.Kind)
	}
}
