package otelquery_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	qt "github.com/aita/sqlkit/querytrace"
	"github.com/aita/sqlkit/querytrace/otelquery"
)

// recordingHandler returns a handler that records one query against the Trace
// the middleware placed in the request context, as a wrapped driver would.
func recordingHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if trace, ok := qt.FromContext(r.Context()); ok {
			trace.RecordStatement(r.Context(), queryEvent())
		}
		w.WriteHeader(http.StatusOK)
	})
}

func TestMiddlewareNamesTraceByRoutePattern(t *testing.T) {
	exp, sr := recorder(t, otelquery.WithDBSystem("postgresql"))

	mux := http.NewServeMux()
	mux.Handle("GET /users/{id}", exp.Handler(recordingHandler()))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/42", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("handler status = %d, want 200", rec.Code)
	}

	spans := sr.Ended()
	root := findSpan(spans, "GET /users/{id}")
	if root == nil {
		t.Fatalf("operation span not named by route pattern; spans: %v", spanNames(spans))
	}
	stmt := findSpan(spans, "SELECT users")
	if stmt == nil {
		t.Fatalf("statement span not found; spans: %v", spanNames(spans))
	}
	if stmt.Parent().SpanID() != root.SpanContext().SpanID() {
		t.Error("statement span is not a child of the request's operation span")
	}
}

func TestMiddlewareFallsBackToMethod(t *testing.T) {
	exp, sr := recorder(t)

	// No mux: the request carries no routed pattern, so the operation falls
	// back to the method.
	handler := exp.Middleware()(recordingHandler())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/anything", nil))

	if findSpan(sr.Ended(), "GET") == nil {
		t.Fatalf("operation span not named by method; spans: %v", spanNames(sr.Ended()))
	}
}

func TestMiddlewareSamplerSkipsExport(t *testing.T) {
	exp, sr := recorder(t)

	// A never-sampling sampler makes each request's trace a pass-through; the
	// middleware records nothing and exports no span.
	handler := exp.Middleware(otelquery.WithSampler(qt.NeverSample()))(recordingHandler())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("handler status = %d, want 200", rec.Code)
	}
	if spans := sr.Ended(); len(spans) != 0 {
		t.Errorf("sampled-out request produced %d spans, want 0: %v", len(spans), spanNames(spans))
	}
}

func TestMiddlewareWithOperationNamer(t *testing.T) {
	exp, sr := recorder(t)

	handler := exp.Middleware(otelquery.WithOperationNamer(func(*http.Request) string {
		return "custom-op"
	}))(recordingHandler())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	if findSpan(sr.Ended(), "custom-op") == nil {
		t.Fatalf("operation span not named by custom namer; spans: %v", spanNames(sr.Ended()))
	}
}
