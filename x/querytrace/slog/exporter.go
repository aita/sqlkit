// Package slogtrace exports a querytrace.Trace to a log/slog logger. It is the
// summary form of tracing output: at the end of a unit of work, log one record
// of the trace's counts and its warnings, rather than a line per query.
//
//	exporter := slogtrace.New(logger)
//	defer exporter.ExportTrace(ctx, trace)
package slogtrace

import (
	"context"
	"log/slog"
	"strconv"

	qt "github.com/aita/sqlkit/x/querytrace"
)

// Mode selects what ExportTrace logs.
type Mode int

const (
	// Summary logs one record per trace with its counts and warnings.
	Summary Mode = iota
	// WarningsOnly logs only traces that produced at least one warning.
	WarningsOnly
	// Off logs nothing.
	Off
)

// Exporter logs traces to an slog.Logger.
type Exporter struct {
	logger *slog.Logger
	mode   Mode
}

// Option configures an Exporter.
type Option func(*Exporter)

// WithMode sets the logging mode.
func WithMode(m Mode) Option { return func(e *Exporter) { e.mode = m } }

// New returns an Exporter that logs to logger.
func New(logger *slog.Logger, opts ...Option) *Exporter {
	e := &Exporter{logger: logger, mode: Summary}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// ExportTrace logs a summary of the trace: its operation and counts at info
// level, raised to warn when it produced warnings, followed by one record per
// warning. It honors the configured Mode.
func (e *Exporter) ExportTrace(ctx context.Context, trace *qt.Trace) {
	if e.mode == Off {
		return
	}
	rep := trace.Report()
	if e.mode == WarningsOnly && len(rep.Warnings) == 0 {
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
			attrs = append(attrs, slog.String("caller", caller(w.Caller)))
		}
		e.logger.LogAttrs(ctx, slog.LevelWarn, "sql trace warning", attrs...)
	}
}

func caller(c *qt.Caller) string {
	return c.File + ":" + strconv.Itoa(c.Line)
}
