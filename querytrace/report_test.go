package querytrace_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aita/sqlkit/analyze"
	querytrace "github.com/aita/sqlkit/querytrace"
)

// queryInfo builds a minimal analyzed QueryInfo for report rendering tests.
func queryInfo(fp string, kind querytrace.StmtKind) *querytrace.QueryInfo {
	return &querytrace.QueryInfo{Analysis: analyze.Analysis{Fingerprint: fp, Kind: kind}}
}

// TestTextReportRendersTimelineAndWarnings exercises the report renderer end to
// end: the header counts, the warnings block, and the merged event/mark/tx
// timeline. It both covers the rendering paths and pins their output shape.
func TestTextReportRendersTimelineAndWarnings(t *testing.T) {
	cfg := querytrace.Config{
		Enabled:  true,
		NPlusOne: querytrace.NPlusOneConfig{Enabled: true, MinCount: 2},
	}
	trace := querytrace.New("checkout", querytrace.WithConfig(cfg))
	ctx := querytrace.WithTrace(context.Background(), trace)

	base := time.Now()
	loop := at("svc.go", 7)

	// A transaction, two looping reads (to trigger query-in-loop), an exec, an
	// unclosed cursor, and a mark — every renderEvent branch plus the warnings.
	txID := trace.RecordTxBegin()
	trace.RecordStatement(ctx, querytrace.Event{
		Kind:      querytrace.EventQuery,
		Name:      "Repo.Find",
		StartedAt: base.Add(1 * time.Millisecond),
		Duration:  2 * time.Millisecond,
		Query:     queryInfo("SELECT * FROM t WHERE id = ?", querytrace.StmtSelect),
		Caller:    loop,
		Rows:      &querytrace.RowsInfo{NextCount: 1, Closed: false},
	})
	trace.RecordStatement(ctx, querytrace.Event{
		Kind:      querytrace.EventQuery,
		StartedAt: base.Add(2 * time.Millisecond),
		Duration:  1 * time.Millisecond,
		Query:     queryInfo("SELECT * FROM t WHERE id = ?", querytrace.StmtSelect),
		Caller:    loop,
	})
	trace.RecordStatement(ctx, querytrace.Event{
		Kind:      querytrace.EventExec,
		StartedAt: base.Add(3 * time.Millisecond),
		Duration:  1 * time.Millisecond,
		Query:     queryInfo("UPDATE t SET x = ?", querytrace.StmtUpdate),
	})
	querytrace.Mark(ctx, "external call")
	trace.RecordTxEnd(querytrace.TxCommit, txID)

	out := querytrace.TextReport(trace)

	for _, want := range []string{
		"SQL trace: checkout",
		"queries=3",
		"writes=1",
		"warnings:",
		string(querytrace.WarnQueryInLoop),
		string(querytrace.WarnRowsNotClosed),
		"timeline:",
		"TX",
		"QUERY",
		"EXEC",
		"Repo.Find",
		"MARK external call",
		"caller=svc.go:7",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("TextReport missing %q\n--- report ---\n%s", want, out)
		}
	}
}

// TestReportCountsAndDurations checks the aggregate fields the renderer reads:
// query/write/unique counts and the summed durations.
func TestReportCountsAndDurations(t *testing.T) {
	trace := querytrace.New("op", querytrace.WithConfig(querytrace.Config{Enabled: true}))
	ctx := context.Background()

	trace.RecordStatement(ctx, querytrace.Event{
		Kind:     querytrace.EventQuery,
		Duration: 5 * time.Millisecond,
		Query:    queryInfo("SELECT a FROM t", querytrace.StmtSelect),
		Rows:     &querytrace.RowsInfo{IterationDuration: 3 * time.Millisecond, Closed: true},
	})
	trace.RecordStatement(ctx, querytrace.Event{
		Kind:     querytrace.EventExec,
		Duration: 2 * time.Millisecond,
		Query:    queryInfo("INSERT INTO t VALUES (?)", querytrace.StmtInsert),
	})
	// Same fingerprint as the first read: unique count must stay 2, not 3.
	trace.RecordStatement(ctx, querytrace.Event{
		Kind:     querytrace.EventQuery,
		Duration: 1 * time.Millisecond,
		Query:    queryInfo("SELECT a FROM t", querytrace.StmtSelect),
	})

	r := trace.Report()
	if r.QueryCount != 3 {
		t.Errorf("QueryCount = %d, want 3", r.QueryCount)
	}
	if r.WriteCount != 1 {
		t.Errorf("WriteCount = %d, want 1", r.WriteCount)
	}
	if r.UniqueFingerprintCount != 2 {
		t.Errorf("UniqueFingerprintCount = %d, want 2", r.UniqueFingerprintCount)
	}
	if r.TotalQueryDuration != 8*time.Millisecond {
		t.Errorf("TotalQueryDuration = %s, want 8ms", r.TotalQueryDuration)
	}
	if r.TotalRowsIterationDuration != 3*time.Millisecond {
		t.Errorf("TotalRowsIterationDuration = %s, want 3ms", r.TotalRowsIterationDuration)
	}
}

// TestEmptyReportRendersHeaderOnly covers the no-warnings, no-timeline branches.
func TestEmptyReportRendersHeaderOnly(t *testing.T) {
	trace := querytrace.New("empty", querytrace.WithConfig(querytrace.Config{Enabled: true}))
	out := querytrace.TextReport(trace)
	if !strings.Contains(out, "SQL trace: empty") {
		t.Errorf("missing header: %q", out)
	}
	if strings.Contains(out, "warnings:") {
		t.Errorf("unexpected warnings block: %q", out)
	}
	if strings.Contains(out, "timeline:") {
		t.Errorf("unexpected timeline block: %q", out)
	}
}

func TestSeverityString(t *testing.T) {
	cases := map[querytrace.Severity]string{
		querytrace.SeverityInfo:  "info",
		querytrace.SeverityWarn:  "warn",
		querytrace.SeverityError: "error",
		querytrace.Severity(99):  "info", // default branch
	}
	for sev, want := range cases {
		if got := sev.String(); got != want {
			t.Errorf("Severity(%d).String() = %q, want %q", sev, got, want)
		}
	}
}
