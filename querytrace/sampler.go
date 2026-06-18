package querytrace

import "math/rand/v2"

// Sampler decides whether a Trace records the statements run under it. The
// decision is made once, when the Trace is created (see New and WithSampler), so
// a sampled-out Trace turns the wrapped driver into a thin pass-through for its
// whole lifetime — every statement under it is cheap, and the Trace records and
// reports nothing. Sampling keeps the per-statement overhead of analysis,
// fingerprinting, and caller capture off the hot path in production while still
// observing a representative fraction of traffic.
//
// Sampling is a whole-Trace decision on purpose: a fractional, per-statement
// sampler would fragment a trace, leaving partial statement sets that make N+1
// detection and query budgets meaningless. Either a Trace is observed in full or
// not at all.
type Sampler interface {
	// ShouldSample reports whether a new Trace should record. It is called once
	// per Trace and may be probabilistic.
	ShouldSample() bool
}

// SamplerFunc adapts a plain function to a Sampler.
type SamplerFunc func() bool

// ShouldSample calls f.
func (f SamplerFunc) ShouldSample() bool { return f() }

// AlwaysSample returns a Sampler that records every Trace. It is the default
// behavior when no sampler is set.
func AlwaysSample() Sampler { return SamplerFunc(func() bool { return true }) }

// NeverSample returns a Sampler that records no Trace, turning the wrapped driver
// into a pass-through. It is useful to disable observation behind a flag without
// changing call sites.
func NeverSample() Sampler { return SamplerFunc(func() bool { return false }) }

// SampleFraction returns a Sampler that records each Trace independently with
// probability p. A p at or below 0 never samples and a p at or above 1 always
// does, so SampleFraction(0) and SampleFraction(1) match NeverSample and
// AlwaysSample. The decision uses the default math/rand source; seed it (or
// supply a custom Sampler) when a deterministic stream is needed.
func SampleFraction(p float64) Sampler {
	switch {
	case p <= 0:
		return NeverSample()
	case p >= 1:
		return AlwaysSample()
	default:
		return SamplerFunc(func() bool { return rand.Float64() < p })
	}
}
