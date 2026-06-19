package querytrace

import (
	"expvar"
	"sort"
	"strings"
	"time"
)

// ExpvarRecorder is a MetricsRecorder that publishes a trace's metrics to an
// expvar.Map, exposing them on the standard /debug/vars endpoint with nothing
// but the standard library. Counters accumulate as expvar integers and
// durations as a running total in seconds (expvar has no histogram, so the
// distribution is not retained — only the sum, a monotonic counter of time).
//
// Labels are folded into the variable name, sorted for stability, e.g.
// "querytrace.statements{kind=select,operation=GET /users}", since expvar has no
// label dimensions.
type ExpvarRecorder struct {
	root *expvar.Map
}

// NewExpvarRecorder returns a recorder backed by root. Publish root once under a
// name of your choosing — typically expvar.NewMap("querytrace") — and reuse the
// recorder across traces; every trace's metrics accumulate into it.
//
//	var dbMetrics = querytrace.NewExpvarRecorder(expvar.NewMap("querytrace"))
//	// ... per request:
//	defer dbMetrics.Record(trace) // or trace.RecordMetrics(dbMetrics)
func NewExpvarRecorder(root *expvar.Map) *ExpvarRecorder {
	return &ExpvarRecorder{root: root}
}

// Record emits trace's metrics into the expvar map, the one-call form of
// trace.RecordMetrics(r).
func (r *ExpvarRecorder) Record(trace *Trace) {
	trace.RecordMetrics(r)
}

// Counter adds value to the integer variable named by name and labels.
func (r *ExpvarRecorder) Counter(name string, value int64, labels ...Label) {
	r.root.Add(expvarKey(name, labels), value)
}

// Duration adds d, in seconds, to the float variable named by name and labels —
// a running total, since expvar cannot record a distribution.
func (r *ExpvarRecorder) Duration(name string, d time.Duration, labels ...Label) {
	r.root.AddFloat(expvarKey(name, labels), d.Seconds())
}

// expvarKey folds a metric name and its labels into a single stable variable
// name. Labels are sorted by key so the same series always maps to one name.
func expvarKey(name string, labels []Label) string {
	if len(labels) == 0 {
		return name
	}
	sorted := append([]Label(nil), labels...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('{')
	for i, l := range sorted {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(l.Key)
		b.WriteByte('=')
		b.WriteString(l.Value)
	}
	b.WriteByte('}')
	return b.String()
}
