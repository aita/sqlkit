package slogtrace_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	querytrace "github.com/aita/sqlkit/x/querytrace"
	slogtrace "github.com/aita/sqlkit/x/querytrace/slog"
)

func newTrace(t *testing.T, op string, events ...querytrace.Event) *querytrace.Trace {
	t.Helper()
	trace := querytrace.New(op)
	for _, ev := range events {
		trace.RecordStatement(context.Background(), ev)
	}
	return trace
}

func queryEvent(fingerprint string, op querytrace.StmtKind) querytrace.Event {
	return querytrace.Event{
		Kind:  querytrace.EventQuery,
		Query: &querytrace.QueryInfo{Operation: op, Fingerprint: fingerprint},
	}
}

func TestExportTraceSummary(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	exporter := slogtrace.New(logger)

	trace := newTrace(t, "GET /users",
		queryEvent("SELECT * FROM users WHERE id = ?", querytrace.StmtSelect),
	)
	exporter.ExportTrace(context.Background(), trace)

	out := buf.String()
	for _, want := range []string{`"operation":"GET /users"`, `"queries":1`, `"sql trace"`} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q\n%s", want, out)
		}
	}
}

func TestExportTraceWarningsOnly(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	exporter := slogtrace.New(logger, slogtrace.WithMode(slogtrace.WarningsOnly))

	// A clean trace produces no output in WarningsOnly mode.
	clean := newTrace(t, "clean", queryEvent("SELECT 1", querytrace.StmtSelect))
	exporter.ExportTrace(context.Background(), clean)
	if buf.Len() != 0 {
		t.Errorf("WarningsOnly logged a clean trace: %s", buf.String())
	}
}

func TestExportTraceOff(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	exporter := slogtrace.New(logger, slogtrace.WithMode(slogtrace.Off))

	exporter.ExportTrace(context.Background(), newTrace(t, "op", queryEvent("SELECT 1", querytrace.StmtSelect)))
	if buf.Len() != 0 {
		t.Errorf("Off mode logged output: %s", buf.String())
	}
}
