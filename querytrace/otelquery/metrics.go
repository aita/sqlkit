package otelquery

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	qt "github.com/aita/sqlkit/querytrace"
)

// MeterRecorder adapts querytrace metric emission to an OpenTelemetry meter. It
// implements qt.MetricsRecorder, so qt.Metrics.Emit (or Trace.RecordMetrics)
// pushes a trace's counters and durations to OpenTelemetry: counters become
// Int64Counter adds and durations Float64Histogram observations in seconds,
// each tagged with the querytrace labels as string attributes.
//
// Instruments are created lazily, once per metric name, and cached. A creation
// error is captured and returned by Err rather than panicking, so a deferred
// export never interferes with the request.
type MeterRecorder struct {
	ctx   context.Context
	meter metric.Meter

	mu         sync.Mutex
	counters   map[string]metric.Int64Counter
	histograms map[string]metric.Float64Histogram
	err        error
}

// NewMeterRecorder returns a recorder that records on meter. Emitted
// measurements use ctx (for exemplars, baggage, and cancellation); pass the
// request's context.
func NewMeterRecorder(ctx context.Context, meter metric.Meter) *MeterRecorder {
	if ctx == nil {
		ctx = context.Background()
	}
	return &MeterRecorder{
		ctx:        ctx,
		meter:      meter,
		counters:   map[string]metric.Int64Counter{},
		histograms: map[string]metric.Float64Histogram{},
	}
}

// Counter adds value to the named Int64Counter with the labels as attributes.
func (r *MeterRecorder) Counter(name string, value int64, labels ...qt.Label) {
	c := r.counter(name)
	if c == nil {
		return
	}
	c.Add(r.ctx, value, metric.WithAttributes(attrs(labels)...))
}

// Duration records d, in seconds, on the named Float64Histogram with the labels
// as attributes. Seconds is the OpenTelemetry convention for duration units.
func (r *MeterRecorder) Duration(name string, d time.Duration, labels ...qt.Label) {
	h := r.histogram(name)
	if h == nil {
		return
	}
	h.Record(r.ctx, d.Seconds(), metric.WithAttributes(attrs(labels)...))
}

// Err returns the first instrument-creation error, if any.
func (r *MeterRecorder) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *MeterRecorder) counter(name string) metric.Int64Counter {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.counters[name]; ok {
		return c
	}
	c, err := r.meter.Int64Counter(name)
	if err != nil {
		r.record(err)
		return nil
	}
	r.counters[name] = c
	return c
}

func (r *MeterRecorder) histogram(name string) metric.Float64Histogram {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok := r.histograms[name]; ok {
		return h
	}
	h, err := r.meter.Float64Histogram(name, metric.WithUnit("s"))
	if err != nil {
		r.record(err)
		return nil
	}
	r.histograms[name] = h
	return h
}

// record keeps the first error; the caller holds r.mu.
func (r *MeterRecorder) record(err error) {
	if r.err == nil {
		r.err = err
	}
}

func attrs(labels []qt.Label) []attribute.KeyValue {
	out := make([]attribute.KeyValue, len(labels))
	for i, l := range labels {
		out[i] = attribute.String(l.Key, l.Value)
	}
	return out
}

// ExportMetrics records trace's metrics through meter, bound to ctx. It is the
// metrics counterpart of Exporter.ExportTrace and the one-call form of
// constructing a MeterRecorder and calling trace.RecordMetrics; it returns any
// instrument-creation error.
//
//	defer otelquery.ExportMetrics(ctx, meter, trace)
func ExportMetrics(ctx context.Context, meter metric.Meter, trace *qt.Trace) error {
	r := NewMeterRecorder(ctx, meter)
	trace.RecordMetrics(r)
	return r.Err()
}
