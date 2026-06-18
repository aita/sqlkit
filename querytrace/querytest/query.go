package querytest

// QueryAssertion narrows an Assertion to the statements matching a set of
// matchers, ready to check how many of them ran. Build one with
// Assertion.Query; its terminal methods (Times, MinTimes, …) report the result
// and return the parent Assertion so checks keep chaining. The vocabulary
// mirrors the mock's Stub call-count methods.
type QueryAssertion struct {
	a  *Assertion
	ms []Match
}

// count returns how many recorded statements match.
func (q *QueryAssertion) count() int {
	n := 0
	for _, ev := range q.a.stmts {
		if matchAll(ev, q.ms) {
			n++
		}
	}
	return n
}

// Times asserts exactly n matching statements ran.
func (q *QueryAssertion) Times(n int) *Assertion {
	q.a.t.Helper()
	if got := q.count(); got != n {
		q.a.fail("expected %d statements matching %s, got %d", n, describe(q.ms), got)
	}
	return q.a
}

// MinTimes asserts at least n matching statements ran.
func (q *QueryAssertion) MinTimes(n int) *Assertion {
	q.a.t.Helper()
	if got := q.count(); got < n {
		q.a.fail("expected at least %d statements matching %s, got %d", n, describe(q.ms), got)
	}
	return q.a
}

// MaxTimes asserts at most n matching statements ran.
func (q *QueryAssertion) MaxTimes(n int) *Assertion {
	q.a.t.Helper()
	if got := q.count(); got > n {
		q.a.fail("expected at most %d statements matching %s, got %d", n, describe(q.ms), got)
	}
	return q.a
}

// Never asserts no matching statement ran. It is shorthand for Times(0).
func (q *QueryAssertion) Never() *Assertion {
	q.a.t.Helper()
	if got := q.count(); got > 0 {
		q.a.fail("expected no statement matching %s, got %d", describe(q.ms), got)
	}
	return q.a
}
