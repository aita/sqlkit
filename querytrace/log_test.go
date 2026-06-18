package querytrace_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	querytrace "github.com/aita/sqlkit/querytrace"
)

func newLogTrace(t *testing.T, op string, events ...querytrace.Event) *querytrace.Trace {
	t.Helper()
	trace := querytrace.New(op)
	for _, ev := range events {
		trace.RecordStatement(context.Background(), ev)
	}
	return trace
}

func logQueryEvent(fingerprint string, op querytrace.StmtKind) querytrace.Event {
	return querytrace.Event{
		Kind:  querytrace.EventQuery,
		Query: &querytrace.QueryInfo{Operation: op, Fingerprint: fingerprint},
	}
}

func TestLogExporterSummary(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	exporter := querytrace.NewLogExporter(logger)

	trace := newLogTrace(t, "GET /users",
		logQueryEvent("SELECT * FROM users WHERE id = ?", querytrace.StmtSelect),
	)
	exporter.ExportTrace(context.Background(), trace)

	out := buf.String()
	for _, want := range []string{`"operation":"GET /users"`, `"queries":1`, `"sql trace"`} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q\n%s", want, out)
		}
	}
}

func TestLogExporterWarningsOnly(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	exporter := querytrace.NewLogExporter(logger, querytrace.WithLogMode(querytrace.LogWarningsOnly))

	// A clean trace produces no output in LogWarningsOnly mode.
	clean := newLogTrace(t, "clean", logQueryEvent("SELECT 1", querytrace.StmtSelect))
	exporter.ExportTrace(context.Background(), clean)
	if buf.Len() != 0 {
		t.Errorf("LogWarningsOnly logged a clean trace: %s", buf.String())
	}
}

func TestLogExporterOff(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	exporter := querytrace.NewLogExporter(logger, querytrace.WithLogMode(querytrace.LogOff))

	exporter.ExportTrace(context.Background(), newLogTrace(t, "op", logQueryEvent("SELECT 1", querytrace.StmtSelect)))
	if buf.Len() != 0 {
		t.Errorf("LogOff mode logged output: %s", buf.String())
	}
}
