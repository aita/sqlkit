package oteltrace

import (
	"net/http"

	qt "github.com/aita/sqlkit/x/querytrace"
)

// OperationNamer derives the operation name for a request's Trace. It is called
// after the handler returns, so the routed pattern (http.Request.Pattern) is
// available for low-cardinality names.
type OperationNamer func(*http.Request) string

// handlerConfig holds the middleware options.
type handlerConfig struct {
	name OperationNamer
}

// HandlerOption configures the middleware.
type HandlerOption func(*handlerConfig)

// WithOperationNamer overrides how the per-request Trace is named. The default
// uses the routed pattern (such as "GET /users/{id}"), falling back to the
// request method before a route is matched.
func WithOperationNamer(namer OperationNamer) HandlerOption {
	return func(c *handlerConfig) {
		if namer != nil {
			c.name = namer
		}
	}
}

// defaultOperationName names a request after its routed pattern when one is
// known, keeping the operation low cardinality, and after the method otherwise.
func defaultOperationName(r *http.Request) string {
	if r.Pattern != "" {
		return r.Pattern
	}
	return r.Method
}

// Middleware returns net/http middleware that runs each request under a
// querytrace.Trace and exports it through the Exporter when the request
// finishes. Wrap a handler with it, and every QueryContext/ExecContext issued
// under the request's context — through a querytrace-wrapped driver — is recorded
// and turned into the operation's span tree.
//
//	exporter := oteltrace.New(tp.Tracer("querytrace"), oteltrace.WithDBSystem("postgresql"))
//	mux.Handle("/", exporter.Middleware()(appHandler))
//
// Place it inside any HTTP-level instrumentation (such as otelhttp): the
// operation span then nests under the HTTP server span, joining querytrace's
// application-level view to the request trace.
func (e *Exporter) Middleware(opts ...HandlerOption) func(http.Handler) http.Handler {
	cfg := handlerConfig{name: defaultOperationName}
	for _, opt := range opts {
		opt(&cfg)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			trace := qt.New(cfg.name(r))
			ctx := qt.WithTrace(r.Context(), trace)
			r = r.WithContext(ctx)

			defer func() {
				// The route is matched by the time the handler returns, so the
				// namer can now produce its low-cardinality name.
				if name := cfg.name(r); name != "" {
					qt.WithOperation(ctx, name)
				}
				e.ExportTrace(ctx, trace)
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// Handler is shorthand for wrapping a single handler with Middleware.
func (e *Exporter) Handler(next http.Handler, opts ...HandlerOption) http.Handler {
	return e.Middleware(opts...)(next)
}
