// Package driver wraps a database/sql/driver so that statements run through it
// are recorded on the querytrace.Trace carried by their context. It is the
// generic, driver-level integration: it works with any database/sql driver and,
// by wrapping the row cursor, captures the rows lifecycle (iteration and close
// timing) that the query call alone hides.
//
// Wrap a driver.Connector and open a database/sql handle over it:
//
//	db := sql.OpenDB(querytracedriver.WrapConnector(connector, querytrace.DebugConfig("postgres")))
//
// A statement is recorded only when its context carries a Trace (see
// querytrace.WithTrace) and the Config is enabled; otherwise the wrapper is a
// thin pass-through.
package driver

import (
	"context"
	stddriver "database/sql/driver"
	"time"

	qt "github.com/aita/sqlkit/querytrace"
)

// instrumentQuery records a read around run and, on success, wraps the returned
// cursor so its iteration and close are timed. It is a pass-through when the
// context carries no Trace, when tracing is disabled, or when run skips
// (driver.ErrSkip), so database/sql can fall back to the prepare path.
func instrumentQuery(ctx context.Context, cfg qt.Config, sqlText string, args []stddriver.NamedValue, run func() (stddriver.Rows, error)) (stddriver.Rows, error) {
	tr, ok := qt.FromContext(ctx)
	if !ok || !cfg.Enabled {
		return run()
	}
	tr.EnsureConfig(cfg)

	start := time.Now()
	rs, err := run()
	dur := time.Since(start)
	if err == stddriver.ErrSkip {
		return rs, err
	}

	ev := qt.Event{
		Kind:      qt.EventQuery,
		StartedAt: start,
		EndedAt:   start.Add(dur),
		Duration:  dur,
		Query:     cfg.Analyze(sqlText, values(args)),
		Err:       err,
	}
	if qt.WantCaller(cfg.CaptureCaller, err, dur, cfg.SlowQueryThreshold) {
		ev.Caller = qt.CaptureCaller()
	}

	if err == nil && rs != nil {
		info := &qt.RowsInfo{QueryReturnedAt: time.Now()}
		ev.Rows = info
		tr.RecordStatement(ctx, ev)
		return newRows(ctx, rs, info), nil
	}
	tr.RecordStatement(ctx, ev)
	return rs, err
}

// instrumentExec records a write around run. It is a pass-through under the same
// conditions as instrumentQuery.
func instrumentExec(ctx context.Context, cfg qt.Config, sqlText string, args []stddriver.NamedValue, run func() (stddriver.Result, error)) (stddriver.Result, error) {
	tr, ok := qt.FromContext(ctx)
	if !ok || !cfg.Enabled {
		return run()
	}
	tr.EnsureConfig(cfg)

	start := time.Now()
	res, err := run()
	dur := time.Since(start)
	if err == stddriver.ErrSkip {
		return res, err
	}

	ev := qt.Event{
		Kind:      qt.EventExec,
		StartedAt: start,
		EndedAt:   start.Add(dur),
		Duration:  dur,
		Query:     cfg.Analyze(sqlText, values(args)),
		Err:       err,
	}
	if qt.WantCaller(cfg.CaptureCaller, err, dur, cfg.SlowQueryThreshold) {
		ev.Caller = qt.CaptureCaller()
	}
	tr.RecordStatement(ctx, ev)
	return res, err
}

// values extracts the bind values from NamedValue arguments, for analysis.
func values(args []stddriver.NamedValue) []any {
	if len(args) == 0 {
		return nil
	}
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = a.Value
	}
	return out
}

// namedToValue drops the names from NamedValue arguments for the legacy
// Exec/Query fallback on drivers without the context statement interfaces.
func namedToValue(args []stddriver.NamedValue) []stddriver.Value {
	out := make([]stddriver.Value, len(args))
	for i, a := range args {
		out[i] = a.Value
	}
	return out
}
