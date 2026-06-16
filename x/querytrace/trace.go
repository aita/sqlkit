package querytrace

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Trace records the statements of one unit of work — a request, a job, a
// usecase — and the warnings querytrace derives from them. Place a Trace in a
// context with WithTrace; a wrapped driver then records an Event per statement
// against it. A Trace is safe for concurrent use, so parallel queries within one
// operation record correctly.
type Trace struct {
	mu        sync.Mutex
	id        string
	operation string
	startedAt time.Time
	cfg       Config
	cfgSet    bool

	events   []Event
	marks    []MarkInfo
	warnings []Warning // immediate warnings, recorded as statements run

	seq    int
	txSeq  int
	openTx map[string]time.Time
}

// Option configures a Trace at creation.
type Option func(*Trace)

// WithConfig sets the Trace's warning configuration up front. Normally a wrapped
// driver stamps its own Config onto the Trace on first use, so this is mainly
// for tests that build a Trace directly.
func WithConfig(cfg Config) Option {
	return func(t *Trace) {
		t.cfg = cfg
		t.cfgSet = true
	}
}

// New creates a Trace for the named operation, such as "GET /users/:id" or
// "CreateOrder".
func New(operation string, opts ...Option) *Trace {
	t := &Trace{
		operation: operation,
		startedAt: time.Now(),
		openTx:    make(map[string]time.Time),
	}
	t.id = fmt.Sprintf("trace_%d", t.startedAt.UnixNano())
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// ID returns the trace's identifier.
func (t *Trace) ID() string { return t.id }

// Operation returns the trace's operation name.
func (t *Trace) Operation() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.operation
}

func (t *Trace) setOperation(name string) {
	t.mu.Lock()
	t.operation = name
	t.mu.Unlock()
}

// ensureConfig stamps cfg onto the Trace the first time a statement is recorded,
// making the wrapping driver's Config the source of truth for warning
// thresholds. A Config set with WithConfig is left untouched.
func (t *Trace) ensureConfig(cfg Config) {
	if !t.cfgSet {
		t.cfg = cfg
		t.cfgSet = true
	}
}

func (t *Trace) nextEventID() string {
	t.seq++
	return fmt.Sprintf("ev_%d", t.seq)
}

// newTxID allocates a transaction identifier and records its start time.
func (t *Trace) newTxID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.txSeq++
	id := fmt.Sprintf("tx_%d", t.txSeq)
	t.openTx[id] = time.Now()
	return id
}

// EnsureConfig stamps cfg onto the Trace the first time it is recorded against,
// making the wrapping driver's Config the source of truth for warning
// thresholds. It is part of the instrumentation contract used by wrapped
// drivers; applications do not call it.
func (t *Trace) EnsureConfig(cfg Config) {
	t.mu.Lock()
	t.ensureConfig(cfg)
	t.mu.Unlock()
}

// RecordStatement records a completed query or exec event, filling its name and
// labels from ctx and running the immediate per-statement checks against the
// context's expectations. It returns the assigned event ID. It is part of the
// instrumentation contract used by wrapped drivers.
func (t *Trace) RecordStatement(ctx context.Context, ev Event) string {
	if ev.Name == "" {
		ev.Name = queryNameFromContext(ctx)
	}
	if ev.Labels == nil {
		if labels := labelsFromContext(ctx); len(labels) > 0 {
			ev.Labels = labels
		}
	}
	return t.record(ev, expectationsFromContext(ctx))
}

// RecordTxBegin allocates a transaction identifier, records a begin event, and
// returns the identifier for the wrapped transaction to carry. It is part of the
// instrumentation contract used by wrapped drivers.
func (t *Trace) RecordTxBegin() string {
	id := t.newTxID()
	t.recordTx(TxBegin, id)
	return id
}

// RecordTxEnd records a commit or rollback event for txID, carrying the
// transaction's start time so the timeline can show its span. It is part of the
// instrumentation contract used by wrapped drivers.
func (t *Trace) RecordTxEnd(action TxAction, txID string) {
	t.recordTx(action, txID)
}

// record appends a statement event and runs the immediate, per-statement checks
// (forbidden access, read-only violations, missing WHERE) against exp. It
// returns the assigned event ID.
func (t *Trace) record(ev Event, exp expectations) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	ev.ID = t.nextEventID()
	t.events = append(t.events, ev)
	t.checkStatement(ev, exp)
	return ev.ID
}

// recordMark appends an application mark to the timeline.
func (t *Trace) recordMark(m MarkInfo) {
	t.mu.Lock()
	t.marks = append(t.marks, m)
	t.mu.Unlock()
}

// recordTx appends a transaction control event. For a commit or rollback it
// carries the matching begin's time on the event so the timeline can show the
// transaction's span.
func (t *Trace) recordTx(action TxAction, txID string) string {
	t.mu.Lock()
	defer t.mu.Unlock()

	ev := Event{
		ID:        t.nextEventID(),
		Kind:      EventTx,
		StartedAt: time.Now(),
		EndedAt:   time.Now(),
		Tx:        &TxInfo{TxID: txID, Action: action},
	}
	if action != TxBegin {
		if began, ok := t.openTx[txID]; ok {
			ev.Tx.StartedAt = began
			delete(t.openTx, txID)
		}
	}
	t.events = append(t.events, ev)
	return ev.ID
}

// addWarning appends an immediate warning. The caller holds t.mu.
func (t *Trace) addWarning(w Warning) {
	t.warnings = append(t.warnings, w)
}

// checkStatement runs the per-statement warnings. The caller holds t.mu.
func (t *Trace) checkStatement(ev Event, exp expectations) {
	q := ev.Query
	if q == nil {
		return
	}
	isWrite := q.Operation.IsWrite()

	if exp.forbidQueries {
		t.addWarning(Warning{
			Code:     WarnQueryForbidden,
			Message:  "query executed under a ForbidQueries context",
			Severity: SeverityError,
			EventID:  ev.ID,
			Caller:   ev.Caller,
			Details:  fingerprintDetails(q),
		})
	}
	if isWrite && exp.forbidWrites {
		t.addWarning(Warning{
			Code:     WarnWriteForbidden,
			Message:  "write executed under a ForbidWrites context",
			Severity: SeverityError,
			EventID:  ev.ID,
			Caller:   ev.Caller,
			Details:  fingerprintDetails(q),
		})
	}
	if isWrite && exp.readOnly {
		t.addWarning(Warning{
			Code:     WarnWriteInReadOnly,
			Message:  "write executed under a ReadOnly context",
			Severity: SeverityError,
			EventID:  ev.ID,
			Caller:   ev.Caller,
			Details:  fingerprintDetails(q),
		})
	}

	// Missing-WHERE checks are only trustworthy when the WHERE presence came
	// from the AST, not the keyword fallback.
	if q.analyzed && !q.HasWhere {
		switch q.Operation {
		case StmtUpdate:
			t.addWarning(Warning{
				Code:     WarnUpdateWithoutWhere,
				Message:  "UPDATE without a WHERE clause",
				Severity: SeverityWarn,
				EventID:  ev.ID,
				Caller:   ev.Caller,
				Details:  fingerprintDetails(q),
			})
		case StmtDelete:
			t.addWarning(Warning{
				Code:     WarnDeleteWithoutWhere,
				Message:  "DELETE without a WHERE clause",
				Severity: SeverityWarn,
				EventID:  ev.ID,
				Caller:   ev.Caller,
				Details:  fingerprintDetails(q),
			})
		}
	}
}

// Events returns a copy of the recorded statement and transaction events, in
// record order.
func (t *Trace) Events() []Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Event(nil), t.events...)
}

// Marks returns a copy of the recorded marks.
func (t *Trace) Marks() []MarkInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]MarkInfo(nil), t.marks...)
}

// Warnings returns every warning for the Trace: the per-statement warnings
// raised as it ran, plus the whole-trace warnings (N+1, query-in-loop,
// too-many-queries, rows-not-closed) derived from its events. It is safe to call
// repeatedly; derived warnings are recomputed each time, not duplicated.
func (t *Trace) Warnings() []Warning {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := append([]Warning(nil), t.warnings...)
	out = append(out, t.deriveWarnings()...)
	return out
}
