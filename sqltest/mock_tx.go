package sqltest

import (
	"encoding/csv"
	"fmt"
	"strings"
)

// CallStub is a programmed expectation for a non-statement database call — a
// transaction Begin, Commit, or Rollback, or a Ping. Build it with ExpectBegin,
// ExpectCommit, ExpectRollback, or ExpectPing and, optionally, make the call
// fail with ReturnError or require it with Required / Times. Unlike a statement
// Stub, a CallStub is not required by default: a transaction the code under test
// opens and commits without any expectation still succeeds, so a CallStub serves
// only to inject an error or to assert the call happened.
//
// Like a statement Stub, a CallStub answers every matching call by default (it
// has no call limit): ExpectCommit().ReturnError(err) makes every Commit fail.
// Limit it with Times or MaxTimes to have it answer only the next call(s) and
// let a later stub, or the default success, answer the rest.
type CallStub struct {
	action   string
	err      error
	minTimes int
	maxTimes int
	matched  int
}

// ExpectBegin programs transaction begins (every one, unless limited by
// Times/MaxTimes). Use ReturnError to make BeginTx fail, or Required to assert
// the code starts a transaction.
func (m *Mock) ExpectBegin() *CallStub { return m.addCall(&m.beginStubs, "begin") }

// ExpectCommit programs transaction commits (every one, unless limited by
// Times/MaxTimes): ReturnError simulates a commit failure, Required asserts the
// code commits.
func (m *Mock) ExpectCommit() *CallStub { return m.addCall(&m.commitStubs, "commit") }

// ExpectRollback programs transaction rollbacks (every one, unless limited by
// Times/MaxTimes), the natural way to assert that a failing unit of work rolled
// back rather than committed.
func (m *Mock) ExpectRollback() *CallStub { return m.addCall(&m.rollbackStubs, "rollback") }

// ExpectPing programs a database Ping (db.Ping / db.PingContext), so a health
// check can be made to fail with ReturnError or asserted with Required.
func (m *Mock) ExpectPing() *CallStub { return m.addCall(&m.pingStubs, "ping") }

func (m *Mock) addCall(into *[]*CallStub, action string) *CallStub {
	s := &CallStub{action: action}
	m.mu.Lock()
	*into = append(*into, s)
	m.mu.Unlock()
	return s
}

// onCall consumes the first applicable stub of a lifecycle action and returns its
// error, or nil when none is programmed (the call still succeeds).
func (m *Mock) onCall(stubs []*CallStub) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range stubs {
		if s.maxTimes > 0 && s.matched >= s.maxTimes {
			continue
		}
		s.matched++
		return s.err
	}
	return nil
}

// allCallStubs returns every lifecycle stub, for Verify's MinTimes check.
func (m *Mock) allCallStubs() []*CallStub {
	out := make([]*CallStub, 0, len(m.beginStubs)+len(m.commitStubs)+len(m.rollbackStubs)+len(m.pingStubs))
	out = append(out, m.beginStubs...)
	out = append(out, m.commitStubs...)
	out = append(out, m.rollbackStubs...)
	out = append(out, m.pingStubs...)
	return out
}

// ReturnError makes the call fail with err (BeginTx, Commit, Rollback, or Ping
// returns it).
func (s *CallStub) ReturnError(err error) *CallStub { s.err = err; return s }

// Required marks the call as one Verify must see happen at least once. It is
// shorthand for MinTimes(1).
func (s *CallStub) Required() *CallStub { return s.MinTimes(1) }

// Times sets the exact number of calls the stub answers and that Verify
// requires.
func (s *CallStub) Times(n int) *CallStub { return s.MinTimes(n).MaxTimes(n) }

// MinTimes sets the fewest calls Verify requires. Zero (the default) requires
// none.
func (s *CallStub) MinTimes(n int) *CallStub { s.minTimes = n; return s }

// MaxTimes caps how many calls the stub answers; further calls fall through to a
// later stub or the default success. Zero (the default) is unlimited.
func (s *CallStub) MaxTimes(n int) *CallStub { s.maxTimes = n; return s }

// FromCSVString appends rows parsed from CSV text, one record per row, matching
// the columns NewRows was given (the CSV has no header). Each field becomes a
// string value; it is a convenient way to spell out a wide canned result inline,
// mirroring go-sqlmock's RowSet.FromCSVString. It panics on malformed CSV or a
// record whose field count differs from the RowSet's columns — a test-setup
// error.
//
//	sqltest.NewRows("id", "name").FromCSVString("1,alice\n2,bob")
func (r *RowSet) FromCSVString(csvText string) *RowSet {
	reader := csv.NewReader(strings.NewReader(csvText))
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		panic(fmt.Sprintf("sqltest: parse CSV rows: %v", err))
	}
	for n, rec := range records {
		if len(rec) != len(r.cols) {
			panic(fmt.Sprintf("sqltest: CSV record %d has %d field(s), want %d (one per column)", n+1, len(rec), len(r.cols)))
		}
		values := make([]any, len(rec))
		for i, f := range rec {
			values[i] = f
		}
		r.AddRow(values...)
	}
	return r
}
