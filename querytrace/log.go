package querytrace

import (
	"context"
	"log/slog"
	"strconv"
)

// LogMode selects what a LogExporter logs.
type LogMode int

const (
	// LogSummary logs one record per trace with its counts and warnings.
	LogSummary LogMode = iota
	// LogWarningsOnly logs only traces that produced at least one warning.
	LogWarningsOnly
	// LogOff logs nothing.
	LogOff
)

// LogExporter logs traces to an slog.Logger. It is the summary form of tracing
// output: at the end of a unit of work, log one record of the trace's counts
// and its warnings, rather than a line per query. For richer, span-based output
// use the separate otelquery module.
//
//	exporter := querytrace.NewLogExporter(logger)
//	defer exporter.ExportTrace(ctx, trace)
type LogExporter struct {
	logger *slog.Logger
	mode   LogMode
}

// LogOption configures a LogExporter.
type LogOption func(*LogExporter)

// WithLogMode sets the logging mode.
func WithLogMode(m LogMode) LogOption { return func(e *LogExporter) { e.mode = m } }

// NewLogExporter returns a LogExporter that logs to logger.
func NewLogExporter(logger *slog.Logger, opts ...LogOption) *LogExporter {
	e := &LogExporter{logger: logger, mode: LogSummary}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// ExportTrace logs a summary of the trace: its operation and counts at info
// level, raised to warn when it produced warnings, followed by one record per
// warning. It honors the configured LogMode.
func (e *LogExporter) ExportTrace(ctx context.Context, trace *Trace) {
	if e.mode == LogOff {
		return
	}
	rep := trace.Report()
	if e.mode == LogWarningsOnly && len(rep.Warnings) == 0 {
		return
	}

	level := slog.LevelInfo
	if len(rep.Warnings) > 0 {
		level = slog.LevelWarn
	}
	e.logger.LogAttrs(ctx, level, "sql trace",
		slog.String("operation", rep.Operation),
		slog.Int("queries", rep.QueryCount),
		slog.Int("writes", rep.WriteCount),
		slog.Int("unique", rep.UniqueFingerprintCount),
		slog.Duration("db_time", rep.TotalQueryDuration),
		slog.Int("warnings", len(rep.Warnings)),
	)

	for _, w := range rep.Warnings {
		attrs := []slog.Attr{
			slog.String("code", string(w.Code)),
			slog.String("message", w.Message),
		}
		if w.Caller != nil {
			attrs = append(attrs, slog.String("caller", logCaller(w.Caller)))
		}
		e.logger.LogAttrs(ctx, slog.LevelWarn, "sql trace warning", attrs...)
	}
}

func logCaller(c *Caller) string {
	return c.File + ":" + strconv.Itoa(c.Line)
}
