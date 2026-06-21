package querytrace

import (
	"context"
	"time"
)

type ctxKey int

const (
	traceKey ctxKey = iota
	queryNameKey
	labelsKey
	expectKey
)

// expectations are the per-context constraints a Trace checks each statement
// against: a forbidden query, a forbidden write, or a read-only scope.
type expectations struct {
	forbidQueries bool
	forbidWrites  bool
	readOnly      bool
}

// WithTrace returns a context carrying trace, so a wrapped driver records the
// statements run under it against trace.
func WithTrace(ctx context.Context, trace *Trace) context.Context {
	return context.WithValue(ctx, traceKey, trace)
}

// FromContext returns the Trace carried by ctx, if any.
func FromContext(ctx context.Context) (*Trace, bool) {
	t, ok := ctx.Value(traceKey).(*Trace)
	return t, ok && t != nil
}

// SetOperation sets the operation name on the context's Trace, naming the unit
// of work after it has been created (for example once an HTTP route is known).
// Like Mark, it acts on the Trace already carried by ctx rather than returning a
// new context, and is a no-op when ctx carries no Trace.
func SetOperation(ctx context.Context, name string) {
	if t, ok := FromContext(ctx); ok {
		t.setOperation(name)
	}
}

// WithQueryName attaches a query name to ctx, applied to the statements run
// under it (for example "UserRepository.FindByID"). The name is sticky: it
// stays until overridden. Prefer Named when the name should apply to a single
// call.
func WithQueryName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, queryNameKey, name)
}

// Named runs fn with name attached to the statements it issues, scoping the
// query name to exactly one call site. It is the function-shaped form of
// WithQueryName:
//
//	err := querytrace.Named(ctx, "UserRepository.FindByID", func(ctx context.Context) error {
//		return repo.FindByID(ctx, id)
//	})
func Named(ctx context.Context, name string, fn func(ctx context.Context) error) error {
	return fn(WithQueryName(ctx, name))
}

// WithLabel attaches a key/value label to ctx, copied onto every Event recorded
// under it. Labels accumulate; later keys override earlier ones.
func WithLabel(ctx context.Context, key, value string) context.Context {
	labels := map[string]string{key: value}
	for k, v := range labelsFromContext(ctx) {
		if _, set := labels[k]; !set {
			labels[k] = v
		}
	}
	return context.WithValue(ctx, labelsKey, labels)
}

// ReadOnly marks ctx as read-only: a write run under it raises
// write_in_read_only. Reads are allowed. It is convenient in tests that assert a
// code path does not mutate the database.
func ReadOnly(ctx context.Context) context.Context {
	e := expectationsFromContext(ctx)
	e.readOnly = true
	return context.WithValue(ctx, expectKey, e)
}

// ForbidQueries marks ctx as forbidding all database access: any statement run
// under it raises query_forbidden. It asserts a path is served without touching
// the database, such as a cache hit.
func ForbidQueries(ctx context.Context) context.Context {
	e := expectationsFromContext(ctx)
	e.forbidQueries = true
	return context.WithValue(ctx, expectKey, e)
}

// ForbidWrites marks ctx as forbidding writes: a write run under it raises
// write_forbidden. Reads are allowed.
func ForbidWrites(ctx context.Context) context.Context {
	e := expectationsFromContext(ctx)
	e.forbidWrites = true
	return context.WithValue(ctx, expectKey, e)
}

// Mark places a named marker on the context's Trace timeline, interleaving an
// application step (a cache miss, an external call) with the queries around it.
// It is a no-op when ctx carries no Trace.
func Mark(ctx context.Context, name string, attrs ...Attr) {
	t, ok := FromContext(ctx)
	if !ok {
		return
	}
	m := MarkInfo{Name: name, At: time.Now()}
	if len(attrs) > 0 {
		m.Attrs = make(map[string]string, len(attrs))
		for _, a := range attrs {
			m.Attrs[a.Key] = a.Value
		}
	}
	t.recordMark(m)
}

// Attr is a key/value detail passed to Mark.
type Attr struct {
	Key   string
	Value string
}

// L builds an Attr, shorthand for Mark call sites.
func L(key, value string) Attr { return Attr{Key: key, Value: value} }

func queryNameFromContext(ctx context.Context) string {
	name, _ := ctx.Value(queryNameKey).(string)
	return name
}

func labelsFromContext(ctx context.Context) map[string]string {
	labels, _ := ctx.Value(labelsKey).(map[string]string)
	return labels
}

func expectationsFromContext(ctx context.Context) expectations {
	e, _ := ctx.Value(expectKey).(expectations)
	return e
}
