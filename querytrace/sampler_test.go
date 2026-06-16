package querytrace_test

import (
	"testing"

	querytrace "github.com/aita/sqlkit/querytrace"
)

func TestSamplers(t *testing.T) {
	if !querytrace.AlwaysSample().ShouldSample() {
		t.Error("AlwaysSample should sample")
	}
	if querytrace.NeverSample().ShouldSample() {
		t.Error("NeverSample should not sample")
	}
	if querytrace.SampleFraction(0).ShouldSample() {
		t.Error("SampleFraction(0) should never sample")
	}
	if !querytrace.SampleFraction(1).ShouldSample() {
		t.Error("SampleFraction(1) should always sample")
	}
	// A fraction in (0,1) eventually produces both outcomes; just check it runs.
	s := querytrace.SampleFraction(0.5)
	for i := 0; i < 10; i++ {
		_ = s.ShouldSample()
	}
}

func TestTraceSampledDefaultsTrue(t *testing.T) {
	if !querytrace.New("op").Sampled() {
		t.Error("a Trace should sample by default")
	}
}

func TestWithSampler(t *testing.T) {
	in := querytrace.New("op", querytrace.WithSampler(querytrace.AlwaysSample()))
	if !in.Sampled() {
		t.Error("WithSampler(AlwaysSample) should sample")
	}
	out := querytrace.New("op", querytrace.WithSampler(querytrace.NeverSample()))
	if out.Sampled() {
		t.Error("WithSampler(NeverSample) should not sample")
	}
}

func TestWithSampled(t *testing.T) {
	if querytrace.New("op", querytrace.WithSampled(false)).Sampled() {
		t.Error("WithSampled(false) should not sample")
	}
}

// TestSampledOutTraceStartsEmpty documents that a sampled-out Trace pairs with a
// driver that consults Sampled and skips recording (see the driver package's
// TestSamplingPassthrough); the Trace itself simply reports as not sampled.
func TestSampledOutTraceStartsEmpty(t *testing.T) {
	trace := querytrace.New("op", querytrace.WithSampled(false))
	if trace.Sampled() {
		t.Error("trace should be sampled out")
	}
	if len(trace.Events()) != 0 {
		t.Error("a new trace should have no events")
	}
}
