// Package oteltrace exports a querytrace.Trace to OpenTelemetry as a tree of
// spans: one operation-level span for the unit of work, and a child span per
// recorded statement carrying the OpenTelemetry database semantic conventions
// (db.system.name, db.query.text, db.operation.name, db.collection.name).
// Transaction control points and application marks become span events, and each
// querytrace Warning is attached as a span event — on the statement it concerns,
// or on the operation span for the whole-trace findings (N+1, query budget) that
// belong to no single statement.
//
// A querytrace.Trace already records each statement's start and end times, so
// ExportTrace replays them into spans with explicit timestamps after the unit of
// work has finished, rather than instrumenting live. The spans carry the
// application-level signals a plain SQL driver span cannot — the rows lifecycle,
// caller, fingerprint, and the derived warnings — so querytrace can be the only
// SQL instrumentation. (Run alongside a driver-level instrumentation such as
// otelsql and you get a second span per query; querytrace's is the richer one.)
//
//	exporter := oteltrace.New(tp.Tracer("querytrace"), oteltrace.WithDBSystem("postgresql"))
//	defer exporter.ExportTrace(ctx, trace)
package oteltrace

import (
	"context"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otel "go.opentelemetry.io/otel/trace"

	qt "github.com/aita/sqlkit/x/querytrace"
)

// Attribute keys. The db.* keys follow the OpenTelemetry database semantic
// conventions; the querytrace.* keys carry the application-level detail that has
// no standard equivalent (fingerprints, the rows lifecycle, warnings).
const (
	attrDBSystem     = "db.system.name"
	attrDBQueryText  = "db.query.text"
	attrDBQuerySumm  = "db.query.summary"
	attrDBOperation  = "db.operation.name"
	attrDBCollection = "db.collection.name"

	attrEventID     = "querytrace.event.id"
	attrFingerprint = "querytrace.query.fingerprint"
	attrParamCount  = "querytrace.query.param_count"
	attrParamTypes  = "querytrace.query.param_types"
	attrTxID        = "querytrace.tx.id"
	attrRowsCount   = "querytrace.rows.count"
	attrRowsClosed  = "querytrace.rows.closed"
	attrRowsIterMS  = "querytrace.rows.iteration_ms"
	attrRowsOpenMS  = "querytrace.rows.open_ms"

	attrQueryCount   = "querytrace.query_count"
	attrWriteCount   = "querytrace.write_count"
	attrUniqueCount  = "querytrace.unique_fingerprint_count"
	attrWarningCount = "querytrace.warning_count"
	attrDBTimeMS     = "querytrace.db_time_ms"

	attrWarnCode        = "querytrace.warning.code"
	attrWarnMessage     = "querytrace.warning.message"
	attrWarnSeverity    = "querytrace.warning.severity"
	attrWarnCaller      = "querytrace.warning.caller"
	attrWarnFingerprint = "querytrace.warning.fingerprint"
	attrWarnCount       = "querytrace.warning.count"
)

// Exporter replays a querytrace.Trace into OpenTelemetry spans on a Tracer.
type Exporter struct {
	tracer otel.Tracer

	dbSystem        string
	warningsAsError bool
}

// Option configures an Exporter.
type Option func(*Exporter)

// WithDBSystem sets the db.system.name attribute on every statement span, such
// as "postgresql", "mysql", or "sqlite". querytrace does not record the dialect
// on a Trace, so the system is supplied here. It is omitted when unset.
func WithDBSystem(name string) Option {
	return func(e *Exporter) { e.dbSystem = name }
}

// WithWarningsAsErrors sets the span status to Error on a statement span that
// carries a querytrace Warning of SeverityError (a forbidden query, a write in a
// read-only scope). By default warnings are recorded as span events only and do
// not change span status.
func WithWarningsAsErrors() Option {
	return func(e *Exporter) { e.warningsAsError = true }
}

// New returns an Exporter that records traces on tracer.
func New(tracer otel.Tracer, opts ...Option) *Exporter {
	e := &Exporter{tracer: tracer}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// ExportTrace replays trace as a span tree: an operation-level span covering the
// unit of work, with a child span per query and exec statement, transaction
// control points and marks as span events, and each Warning attached to the
// statement it concerns (or to the operation span when it concerns the trace as
// a whole). Spans are created with the statements' recorded timestamps, so the
// tree reflects when the work actually happened.
func (e *Exporter) ExportTrace(ctx context.Context, trace *qt.Trace) {
	rep := trace.Report()

	start := rep.StartedAt
	if start.IsZero() {
		start = time.Now()
	}
	end := start

	rootCtx, root := e.tracer.Start(ctx, operationName(rep.Operation),
		otel.WithSpanKind(otel.SpanKindInternal),
		otel.WithTimestamp(start),
	)
	root.SetAttributes(
		attribute.Int(attrQueryCount, rep.QueryCount),
		attribute.Int(attrWriteCount, rep.WriteCount),
		attribute.Int(attrUniqueCount, rep.UniqueFingerprintCount),
		attribute.Int(attrWarningCount, len(rep.Warnings)),
		attribute.Int64(attrDBTimeMS, rep.TotalQueryDuration.Milliseconds()),
	)

	// Index warnings by the event they concern; warnings with no event attach
	// to the operation span.
	byEvent := make(map[string][]qt.Warning)
	var rootWarnings []qt.Warning
	for _, w := range rep.Warnings {
		if w.EventID == "" {
			rootWarnings = append(rootWarnings, w)
			continue
		}
		byEvent[w.EventID] = append(byEvent[w.EventID], w)
	}

	for _, ev := range rep.Events {
		if t := evEnd(ev); t.After(end) {
			end = t
		}

		switch ev.Kind {
		case qt.EventQuery, qt.EventExec:
			e.recordStatement(rootCtx, ev, byEvent[ev.ID])
		case qt.EventTx:
			root.AddEvent(txEventName(ev), otel.WithTimestamp(tsOr(ev.StartedAt, start)),
				otel.WithAttributes(txAttrs(ev)...))
		}
	}

	for _, m := range rep.Marks {
		if m.At.After(end) {
			end = m.At
		}
		root.AddEvent("querytrace.mark", otel.WithTimestamp(tsOr(m.At, start)),
			otel.WithAttributes(markAttrs(m)...))
	}

	for _, w := range rootWarnings {
		addWarningEvent(root, w)
		if e.warningsAsError && w.Severity == qt.SeverityError {
			root.SetStatus(codes.Error, string(w.Code))
		}
	}

	root.End(otel.WithTimestamp(end))
}

// recordStatement creates the span for one query or exec event under ctx.
func (e *Exporter) recordStatement(ctx context.Context, ev qt.Event, warnings []qt.Warning) {
	_, span := e.tracer.Start(ctx, statementName(ev),
		otel.WithSpanKind(otel.SpanKindClient),
		otel.WithTimestamp(tsOr(ev.StartedAt, time.Now())),
	)

	span.SetAttributes(e.statementAttrs(ev)...)

	if ev.Caller != nil {
		span.SetAttributes(
			attribute.String("code.function", ev.Caller.Function),
			attribute.String("code.filepath", ev.Caller.File),
			attribute.Int("code.lineno", ev.Caller.Line),
		)
	}
	for k, v := range ev.Labels {
		span.SetAttributes(attribute.String("querytrace.label."+k, v))
	}
	if ev.Rows != nil {
		span.SetAttributes(rowsAttrs(ev.Rows)...)
	}

	if ev.Err != nil {
		span.RecordError(ev.Err)
		span.SetStatus(codes.Error, ev.Err.Error())
	}

	for _, w := range warnings {
		addWarningEvent(span, w)
		if e.warningsAsError && w.Severity == qt.SeverityError {
			span.SetStatus(codes.Error, string(w.Code))
		}
	}

	span.End(otel.WithTimestamp(evEnd(ev)))
}

// statementAttrs builds the db.* and querytrace.* attributes for a statement.
func (e *Exporter) statementAttrs(ev qt.Event) []attribute.KeyValue {
	attrs := []attribute.KeyValue{attribute.String(attrEventID, ev.ID)}
	if e.dbSystem != "" {
		attrs = append(attrs, attribute.String(attrDBSystem, e.dbSystem))
	}
	q := ev.Query
	if q == nil {
		return attrs
	}
	if op := operationString(q.Operation); op != "" {
		attrs = append(attrs, attribute.String(attrDBOperation, op))
	}
	if len(q.Tables) > 0 {
		attrs = append(attrs, attribute.String(attrDBCollection, q.Tables[0]))
	}
	if q.RawSQL != "" {
		attrs = append(attrs, attribute.String(attrDBQueryText, q.RawSQL))
	}
	if q.Fingerprint != "" {
		attrs = append(attrs,
			attribute.String(attrDBQuerySumm, q.Fingerprint),
			attribute.String(attrFingerprint, q.Fingerprint),
		)
	}
	attrs = append(attrs, attribute.Int(attrParamCount, q.ParamCount))
	if len(q.ParamTypes) > 0 {
		attrs = append(attrs, attribute.StringSlice(attrParamTypes, q.ParamTypes))
	}
	if q.TxID != "" {
		attrs = append(attrs, attribute.String(attrTxID, q.TxID))
	}
	return attrs
}

func rowsAttrs(r *qt.RowsInfo) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.Int(attrRowsCount, r.NextCount),
		attribute.Bool(attrRowsClosed, r.Closed),
		attribute.Int64(attrRowsIterMS, r.IterationDuration.Milliseconds()),
		attribute.Int64(attrRowsOpenMS, r.OpenDuration.Milliseconds()),
	}
}

func txAttrs(ev qt.Event) []attribute.KeyValue {
	if ev.Tx == nil {
		return nil
	}
	return []attribute.KeyValue{
		attribute.String(attrTxID, ev.Tx.TxID),
		attribute.String("querytrace.tx.action", string(ev.Tx.Action)),
	}
}

func markAttrs(m qt.MarkInfo) []attribute.KeyValue {
	attrs := []attribute.KeyValue{attribute.String("querytrace.mark.name", m.Name)}
	for k, v := range m.Attrs {
		attrs = append(attrs, attribute.String("querytrace.mark."+k, v))
	}
	return attrs
}

// addWarningEvent records one querytrace Warning as a span event. It carries the
// code, message, and severity, plus the caller and the actionable Details a
// warning may hold — most importantly the fingerprint and repeat count of an N+1
// or query-in-loop, which has no single statement span to attach to and so lands
// here on the operation span.
func addWarningEvent(span otel.Span, w qt.Warning) {
	attrs := []attribute.KeyValue{
		attribute.String(attrWarnCode, string(w.Code)),
		attribute.String(attrWarnMessage, w.Message),
		attribute.String(attrWarnSeverity, w.Severity.String()),
	}
	if w.Caller != nil {
		attrs = append(attrs, attribute.String(attrWarnCaller, w.Caller.File+":"+strconv.Itoa(w.Caller.Line)))
	}
	if fp, ok := w.Details["fingerprint"].(string); ok && fp != "" {
		attrs = append(attrs, attribute.String(attrWarnFingerprint, fp))
	}
	if c, ok := w.Details["count"].(int); ok {
		attrs = append(attrs, attribute.Int(attrWarnCount, c))
	}
	span.AddEvent("querytrace.warning", otel.WithAttributes(attrs...))
}

// statementName builds the span name for a statement, preferring the
// application-supplied query name, then the OpenTelemetry "<OPERATION> <target>"
// shape, then the fingerprint, then the operation alone. It stays low
// cardinality: it never includes raw SQL or bind values.
func statementName(ev qt.Event) string {
	if ev.Name != "" {
		return ev.Name
	}
	q := ev.Query
	if q == nil {
		return string(ev.Kind)
	}
	op := operationString(q.Operation)
	if op != "" && len(q.Tables) > 0 {
		return op + " " + q.Tables[0]
	}
	if q.Fingerprint != "" {
		return q.Fingerprint
	}
	if op != "" {
		return op
	}
	return string(ev.Kind)
}

func operationName(op string) string {
	if op == "" {
		return "sql trace"
	}
	return op
}

// operationString returns the uppercase OpenTelemetry db.operation.name for a
// statement kind, or "" when the kind is unknown.
func operationString(k qt.StmtKind) string {
	switch k {
	case qt.StmtSelect:
		return "SELECT"
	case qt.StmtInsert:
		return "INSERT"
	case qt.StmtUpdate:
		return "UPDATE"
	case qt.StmtDelete:
		return "DELETE"
	case qt.StmtDDL:
		return "DDL"
	default:
		return ""
	}
}

func txEventName(ev qt.Event) string {
	if ev.Tx == nil {
		return "querytrace.tx"
	}
	return "querytrace.tx." + string(ev.Tx.Action)
}

// evEnd returns the best end time for an event: its EndedAt, falling back to
// StartedAt, then to now.
func evEnd(ev qt.Event) time.Time {
	if !ev.EndedAt.IsZero() {
		return ev.EndedAt
	}
	return tsOr(ev.StartedAt, time.Now())
}

func tsOr(t, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback
	}
	return t
}
