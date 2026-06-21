package sqltest

import (
	"bytes"
	"context"
	"database/sql"
	stddriver "database/sql/driver"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	querytrace "github.com/aita/sqlkit/querytrace"
	qtdriver "github.com/aita/sqlkit/querytrace/driver"
	sqlpkg "github.com/aita/sqlkit/sql"
)

// Mock is a programmable database/sql test double. It backs the *sql.DB returned
// by NewDB, answering each statement with a response you registered — canned
// rows, a result, or an error — so code that talks to a database can run in a
// unit test without one. Because the same DB is traced by querytrace, the spy
// assertions (Expect, Count, Events) see every statement the code issued, so the
// mock provides the inputs and the spy verifies the calls.
//
// Register responses with ExpectQuery and ExpectExec. Each takes a Matcher
// that decides which statements the stub answers — Contains for a partial match,
// Regexp, or Equals — a plain string (matched with Equals), or a sql.Query built
// with the sqlkit builder. A sql.Query matches its compiled shape, and you may
// leave holes in it: AnyTable, AnyColumn, and AnyExpr match any table, column, or
// expression, while Arg, AnyArg, and ArgMatch constrain (or wildcard) a bind
// argument. Stubs are tried in registration order; the first applicable one
// answers. A statement matching no stub gets a lenient default — empty rows or a
// zero result — so a mock need only program the statements a test cares about.
// Verify reports any stub whose MinTimes (or Required) was not met.
type Mock struct {
	mu         sync.Mutex
	dialect    sqlpkg.Dialect
	strict     bool
	queryStubs []*Stub
	execStubs  []*Stub
	unmatched  []unmatchedStmt
	verified   bool
}

// unmatchedStmt records a statement no stub answered, for strict Verify.
type unmatchedStmt struct {
	kind string
	sql  string
}

// stubKind distinguishes a query stub from an exec stub.
type stubKind int

const (
	stubQuery stubKind = iota
	stubExec
)

func (k stubKind) String() string {
	if k == stubExec {
		return "exec"
	}
	return "query"
}

// Stub is one programmed response. Build it with ExpectQuery or ExpectExec and
// set its reply with Return, ReturnResult, or ReturnError. The setters chain.
type Stub struct {
	kind      stubKind
	matcher   Matcher
	argChecks []argCheck

	rows   *RowSet
	result mockResult
	err    error
	delay  time.Duration

	minTimes int // Verify fails if fewer statements matched
	maxTimes int // 0 means unlimited
	matched  int
}

// argCheck constrains the bind argument at a placeholder position.
type argCheck struct {
	index int
	m     ArgMatcher
}

// Option configures NewDB.
type Option func(*config)

type config struct {
	cfg    querytrace.Config
	strict bool
}

// WithConfig sets the querytrace.Config the mock DB records with. The default is
// NewConfig(WithVerboseCapture()): the PostgreSQL dialect, no parser, raw SQL and
// callers captured so every matcher works; supply your own (for example with a
// parser wired in) to enable the structural matchers Table, HasTopLevelWhere, and
// Fingerprint.
func WithConfig(cfg querytrace.Config) Option {
	return func(c *config) { c.cfg = cfg }
}

// Lenient turns off strict verification: a statement that matches no stub is
// allowed (it gets the empty-rows / zero-result default) instead of failing the
// test at cleanup. Use it for code paths that deliberately issue statements the
// mock does not program.
func Lenient() Option {
	return func(c *config) { c.strict = false }
}

// NewDB returns a *sql.DB backed by a programmable Mock and traced by
// querytrace, so statements run under a context carrying a Trace are recorded
// for the spy assertions. It registers a t.Cleanup that verifies the mock and
// closes the DB when the test ends, so a missed Required stub — or, by default,
// any statement no stub answered — fails the test without a manual Verify call.
// Pass Lenient to allow unmatched statements.
//
//	db, mock := sqltest.NewDB(t)
//	mock.ExpectQuery(sqltest.Contains("FROM users")).
//		Return(sqltest.NewRows("id", "name").AddRow(1, "alice"))
//
//	trace := querytrace.New("LoadUser")
//	ctx := querytrace.WithTrace(context.Background(), trace)
//	// ... use db under ctx ...
//	sqltest.Expect(t, trace).Query(sqltest.Select(), sqltest.Table("users")).Times(1)
func NewDB(t testing.TB, opts ...Option) (*sql.DB, *Mock) {
	t.Helper()
	c := config{cfg: querytrace.NewConfig(querytrace.WithVerboseCapture()), strict: true}
	for _, o := range opts {
		o(&c)
	}
	m := &Mock{dialect: c.cfg.Dialect(), strict: c.strict}
	db := sql.OpenDB(qtdriver.WrapConnector(&mockConnector{mock: m}, c.cfg))
	t.Cleanup(func() {
		m.Verify(t)
		db.Close() //nolint:errcheck // best-effort close
	})
	return db, m
}

// ExpectQuery registers a response for QueryContext statements (db.Query,
// db.QueryRow) whose SQL the matcher accepts. The matcher is a Matcher
// (Contains, Regexp, Equals, Anything), a plain string (matched with Equals), or
// a sql.Query (compiled with the mock's dialect, then Equals).
func (m *Mock) ExpectQuery(expect any) *Stub {
	return m.add(stubQuery, expect)
}

// ExpectExec registers a response for ExecContext statements (db.Exec). See
// ExpectQuery for how the expectation is matched.
func (m *Mock) ExpectExec(expect any) *Stub {
	return m.add(stubExec, expect)
}

// add builds a stub of the given kind, resolving expect to a SQL matcher and any
// argument checks.
func (m *Mock) add(kind stubKind, expect any) *Stub {
	matcher, checks := m.resolve(expect)
	s := &Stub{kind: kind, matcher: matcher, argChecks: checks}
	m.mu.Lock()
	if kind == stubExec {
		m.execStubs = append(m.execStubs, s)
	} else {
		m.queryStubs = append(m.queryStubs, s)
	}
	m.mu.Unlock()
	return s
}

// resolve turns an expectation into a Matcher and per-position argument
// checks: a Matcher passes through (no arg checks), a string becomes Equals,
// and a sql.Query is compiled with the mock's dialect — its structural wildcards
// (AnyTable, AnyColumn, AnyExpr) become SQL wildcards and its Arg/ArgMatch
// placeholders become argument checks. It panics on any other type, or when a
// query cannot be compiled — both test-setup errors.
func (m *Mock) resolve(expect any) (Matcher, []argCheck) {
	switch e := expect.(type) {
	case Matcher:
		return e, nil
	case string:
		return Equals(e), nil
	case sqlpkg.Query:
		text, args, err := e.ToSQL(m.dialect)
		if err != nil {
			panic(fmt.Sprintf("sqltest: cannot compile expected query: %v", err))
		}
		return newQueryMatcher(text), argChecksFrom(args)
	default:
		panic(fmt.Sprintf("sqltest: Expect takes a Matcher, string, or sql.Query, got %T", expect))
	}
}

// argChecksFrom collects the explicit ArgMatcher placeholders from a compiled
// query's arguments, by placeholder position. Concrete values impose no check.
func argChecksFrom(args []any) []argCheck {
	var checks []argCheck
	for i, a := range args {
		if am, ok := a.(ArgMatcher); ok {
			checks = append(checks, argCheck{index: i, m: am})
		}
	}
	return checks
}

// Matcher decides whether a mock stub answers a statement, by its SQL text.
// The built-in matchers are Contains (a partial match), Regexp, Equals, and
// Anything; ExpectQuery and ExpectExec also accept a plain string or a sql.Query
// for convenience. Implement Matcher for custom matching.
//
// It is distinct from the spy-side Match, which selects recorded statements by
// their analyzed shape; a Matcher sees only the raw SQL, at the point the mock
// must answer it.
type Matcher interface {
	// Matches reports whether a statement with the given SQL matches.
	Matches(sql string) bool
	// String describes the matcher for Verify failure messages.
	String() string
}

// Contains matches a statement whose SQL contains sub, case-insensitively.
func Contains(sub string) Matcher {
	return containsMatcher{sub: sub, low: strings.ToLower(sub)}
}

type containsMatcher struct{ sub, low string }

func (m containsMatcher) Matches(sql string) bool {
	return strings.Contains(strings.ToLower(sql), m.low)
}
func (m containsMatcher) String() string { return fmt.Sprintf("contains %q", m.sub) }

// Equals matches a statement whose SQL equals s, ignoring case and runs of
// whitespace.
func Equals(s string) Matcher {
	return equalsMatcher{want: s, norm: normalizeSQL(s)}
}

type equalsMatcher struct{ want, norm string }

func (m equalsMatcher) Matches(sql string) bool { return normalizeSQL(sql) == m.norm }
func (m equalsMatcher) String() string          { return fmt.Sprintf("equals %q", m.want) }

// Regexp matches a statement whose SQL matches the regular expression expr. It
// panics if expr does not compile.
func Regexp(expr string) Matcher {
	return regexpMatcher{re: regexp.MustCompile(expr)}
}

type regexpMatcher struct{ re *regexp.Regexp }

func (m regexpMatcher) Matches(sql string) bool { return m.re.MatchString(sql) }
func (m regexpMatcher) String() string          { return "regexp " + m.re.String() }

// Anything matches every statement.
func Anything() Matcher { return anyMatcher{} }

type anyMatcher struct{}

func (anyMatcher) Matches(string) bool { return true }
func (anyMatcher) String() string      { return "any statement" }

// normalizeSQL lowercases and collapses runs of whitespace, so two renderings of
// the same statement compare equal.
func normalizeSQL(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// wildcardRe matches a compiled structural-wildcard sentinel, with the quotes a
// dialect may wrap an identifier in. The sentinel is delimited by NUL bytes (see
// sentinel), which cannot occur in SQL a database executes, so the marker never
// collides with a real identifier or literal in the statement being matched.
var wildcardRe = regexp.MustCompile("\"?\x00qtwild\\d+\x00\"?")

// queryMatcher matches a statement against a compiled sql.Query: equal after
// normalization, except that each structural wildcard (AnyTable, AnyColumn,
// AnyExpr) matches any run of text. With no wildcards it is an exact match.
type queryMatcher struct {
	desc string
	re   *regexp.Regexp
}

// newQueryMatcher builds a queryMatcher from compiled SQL, turning each wildcard
// sentinel into a regular-expression gap and matching the rest literally.
func newQueryMatcher(text string) queryMatcher {
	norm := normalizeSQL(text)
	parts := wildcardRe.Split(norm, -1)
	for i := range parts {
		parts[i] = regexp.QuoteMeta(parts[i])
	}
	return queryMatcher{
		desc: wildcardRe.ReplaceAllString(text, "<any>"),
		re:   regexp.MustCompile("^" + strings.Join(parts, ".+?") + "$"),
	}
}

func (m queryMatcher) Matches(sql string) bool { return m.re.MatchString(normalizeSQL(sql)) }
func (m queryMatcher) String() string          { return "query " + m.desc }

// AnyTable is a From source that matches any table in a sql.Query expectation:
//
//	mock.ExpectQuery(sql.Select(sql.Col("id")).From(sqltest.AnyTable()))
func AnyTable() sqlpkg.Source { return sqlpkg.Tbl(sentinel()) }

// AnyColumn is a Select selection that matches any column or expression:
//
//	mock.ExpectQuery(sql.Select(sqltest.AnyColumn()).From(sql.Tbl("users")))
func AnyColumn() sqlpkg.Selection { return sqlpkg.Raw(sentinel()) }

// AnyExpr is an expression that matches any expression, for Where and other
// clauses:
//
//	...Where(sqltest.AnyExpr())
func AnyExpr() sqlpkg.Expression { return sqlpkg.Raw(sentinel()) }

var sentinelCounter atomic.Uint64

// sentinel returns a unique wildcard marker delimited by NUL bytes. A NUL cannot
// appear in SQL a database would execute (identifiers and the placeholders that
// stand in for bind values never contain one), so the marker survives
// compilation — quoted as an identifier or emitted as a raw fragment — yet can
// never collide with the real SQL it is matched against. The counter keeps
// repeated wildcards in one query distinct.
func sentinel() string { return fmt.Sprintf("\x00qtwild%d\x00", sentinelCounter.Add(1)) }

// ArgMatcher decides whether the bind argument at a placeholder position
// matches. Place one in a sql.Query expectation with Arg, AnyArg, or ArgMatch —
// for example Where(Eq(Col("id"), sqltest.Arg(5))) — and the mock checks the
// executed statement's argument there. Placeholders without an ArgMatcher (plain
// values) are left unconstrained, so a query matches any value unless you say
// otherwise.
type ArgMatcher interface {
	// MatchArg reports whether the executed bind value matches.
	MatchArg(v any) bool
	// String describes the matcher.
	String() string
}

// Arg matches a bind argument equal to v, comparing by database/sql driver value
// (so an int and the int64 the driver sees compare equal).
func Arg(v any) ArgMatcher { return exactArg{v} }

type exactArg struct{ v any }

func (a exactArg) MatchArg(got any) bool { return argEqual(a.v, got) }
func (a exactArg) String() string        { return fmt.Sprintf("%v", a.v) }

// AnyArg matches any bind argument. It is explicit documentation that a value is
// deliberately unconstrained.
func AnyArg() ArgMatcher { return anyArg{} }

type anyArg struct{}

func (anyArg) MatchArg(any) bool { return true }
func (anyArg) String() string    { return "any" }

// ArgMatch matches a bind argument for which fn returns true.
func ArgMatch(fn func(v any) bool) ArgMatcher { return funcArg{fn} }

type funcArg struct{ fn func(any) bool }

func (a funcArg) MatchArg(got any) bool { return a.fn(got) }
func (funcArg) String() string          { return "func" }

// argEqual compares an expected Go value to an executed driver value, coercing
// the expected side to the driver value types first.
func argEqual(want, got any) bool {
	w := coerce(want)
	if wb, ok := w.([]byte); ok {
		gb, ok := got.([]byte)
		return ok && bytes.Equal(wb, gb)
	}
	return w == got
}

// Verify reports any stub whose MinTimes (or Required) was not met, and — unless
// the mock is Lenient — any statement that matched no stub. NewDB registers it as
// a t.Cleanup, so calling it by hand is optional; it runs its checks only once.
func (m *Mock) Verify(t testing.TB) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.verified {
		return
	}
	m.verified = true
	for _, s := range append(append([]*Stub(nil), m.queryStubs...), m.execStubs...) {
		if s.matched < s.minTimes {
			t.Errorf("sqltest: stub [%s] matched %d statements, want at least %d", s.matcher, s.matched, s.minTimes)
		}
	}
	if m.strict {
		for _, u := range m.unmatched {
			t.Errorf("sqltest: unexpected %s with no matching stub: %s", u.kind, u.sql)
		}
	}
}

// Return sets the rows a query stub answers with.
func (s *Stub) Return(rows *RowSet) *Stub { s.rows = rows; return s }

// ReturnResult sets the result an exec stub answers with.
func (s *Stub) ReturnResult(lastInsertID, rowsAffected int64) *Stub {
	s.result = mockResult{lastInsertID: lastInsertID, rowsAffected: rowsAffected}
	return s
}

// ReturnError makes the stub answer with err.
func (s *Stub) ReturnError(err error) *Stub { s.err = err; return s }

// WillDelayFor makes the stub wait d before answering, returning the context's
// error if it is canceled or its deadline passes first. It exercises slow-query
// and cancellation handling — querytrace records a mid-iteration cancellation as
// a warning.
func (s *Stub) WillDelayFor(d time.Duration) *Stub { s.delay = d; return s }

// WithArgs constrains the stub to statements whose bind arguments match, by
// position: an ArgMatcher (Arg, AnyArg, ArgMatch) is used as given, any other
// value becomes an exact Arg. It is the string-matcher counterpart to embedding
// Arg in a sql.Query.
func (s *Stub) WithArgs(args ...any) *Stub {
	s.argChecks = s.argChecks[:0]
	for i, a := range args {
		m, ok := a.(ArgMatcher)
		if !ok {
			m = Arg(a)
		}
		s.argChecks = append(s.argChecks, argCheck{index: i, m: m})
	}
	return s
}

// Required marks the stub as one Verify must see matched at least once. It is
// shorthand for MinTimes(1).
func (s *Stub) Required() *Stub { return s.MinTimes(1) }

// Times sets the exact number of statements the stub answers and that Verify
// requires: shorthand for MinTimes(n) and MaxTimes(n).
func (s *Stub) Times(n int) *Stub { return s.MinTimes(n).MaxTimes(n) }

// MaxTimes caps how many statements the stub answers; further matching
// statements fall through to later stubs or the lenient default. Zero (the
// default) is unlimited.
func (s *Stub) MaxTimes(n int) *Stub { s.maxTimes = n; return s }

// MinTimes sets the fewest matching statements Verify requires. Zero (the
// default) requires none.
func (s *Stub) MinTimes(n int) *Stub { s.minTimes = n; return s }

// AnyTimes lets the stub answer any number of statements, zero included, and
// imposes no Verify floor. It clears an earlier Times/MinTimes/MaxTimes/Required.
func (s *Stub) AnyTimes() *Stub { s.minTimes, s.maxTimes = 0, 0; return s }

// applies reports whether the stub can answer a statement with the given SQL and
// bind arguments.
func (s *Stub) applies(sqlText string, args []stddriver.Value) bool {
	if s.maxTimes > 0 && s.matched >= s.maxTimes {
		return false
	}
	if !s.matcher.Matches(sqlText) {
		return false
	}
	for _, c := range s.argChecks {
		if c.index >= len(args) || !c.m.MatchArg(args[c.index]) {
			return false
		}
	}
	return true
}

// match finds the first applicable stub of the given kind and records the hit.
func (m *Mock) match(kind stubKind, sqlText string, args []stddriver.Value) (*Stub, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stubs := m.queryStubs
	if kind == stubExec {
		stubs = m.execStubs
	}
	for _, s := range stubs {
		if s.applies(sqlText, args) {
			s.matched++
			return s, true
		}
	}
	m.unmatched = append(m.unmatched, unmatchedStmt{kind: kind.String(), sql: sqlText})
	return nil, false
}

// RowSet is a set of canned rows a query stub returns. Build it with NewRows and
// add rows with AddRow.
type RowSet struct {
	cols    []string
	rows    [][]stddriver.Value
	rowErrs map[int]error
}

// NewRows starts a RowSet with the given column names.
func NewRows(columns ...string) *RowSet { return &RowSet{cols: columns} }

// AddRow appends a row. The values are coerced to the database/sql driver value
// types, so plain ints, strings, and the like are accepted.
func (r *RowSet) AddRow(values ...any) *RowSet {
	row := make([]stddriver.Value, len(values))
	for i, v := range values {
		row[i] = coerce(v)
	}
	r.rows = append(r.rows, row)
	return r
}

// RowError makes iteration fail with err when the cursor reaches row (0-based),
// for testing a read that breaks mid-stream — the rows lifecycle querytrace
// observes.
func (r *RowSet) RowError(row int, err error) *RowSet {
	if r.rowErrs == nil {
		r.rowErrs = make(map[int]error)
	}
	r.rowErrs[row] = err
	return r
}

// cursor returns a fresh driver.Rows over the set, so one RowSet can answer
// repeated queries.
func (r *RowSet) cursor() stddriver.Rows {
	if r == nil {
		return &mockRows{}
	}
	return &mockRows{cols: r.cols, rows: r.rows, rowErrs: r.rowErrs}
}

// coerce maps a Go value to a database/sql driver value type.
func coerce(v any) stddriver.Value {
	switch x := v.(type) {
	case nil:
		return nil
	case int:
		return int64(x)
	case int8:
		return int64(x)
	case int16:
		return int64(x)
	case int32:
		return int64(x)
	case int64:
		return x
	case uint:
		return int64(x)
	case uint8:
		return int64(x)
	case uint16:
		return int64(x)
	case uint32:
		return int64(x)
	case uint64:
		return int64(x)
	case float32:
		return float64(x)
	case float64:
		return x
	case bool:
		return x
	case []byte:
		return x
	case string:
		return x
	case time.Time:
		return x
	default:
		return x
	}
}

// --- database/sql/driver plumbing ---

type mockConnector struct{ mock *Mock }

func (c *mockConnector) Connect(context.Context) (stddriver.Conn, error) {
	return &mockConn{mock: c.mock}, nil
}
func (c *mockConnector) Driver() stddriver.Driver { return mockDriver{} }

type mockDriver struct{}

func (mockDriver) Open(string) (stddriver.Conn, error) {
	return nil, errors.New("sqltest: open the mock with NewDB, not a DSN")
}

type mockConn struct{ mock *Mock }

var (
	_ stddriver.QueryerContext     = (*mockConn)(nil)
	_ stddriver.ExecerContext      = (*mockConn)(nil)
	_ stddriver.ConnPrepareContext = (*mockConn)(nil)
	_ stddriver.ConnBeginTx        = (*mockConn)(nil)
)

func (c *mockConn) QueryContext(ctx context.Context, query string, args []stddriver.NamedValue) (stddriver.Rows, error) {
	return c.mock.runQuery(ctx, query, argValues(args))
}

func (c *mockConn) ExecContext(ctx context.Context, query string, args []stddriver.NamedValue) (stddriver.Result, error) {
	return c.mock.runExec(ctx, query, argValues(args))
}

// argValues drops the names from NamedValue arguments.
func argValues(named []stddriver.NamedValue) []stddriver.Value {
	out := make([]stddriver.Value, len(named))
	for i, n := range named {
		out[i] = n.Value
	}
	return out
}

func (c *mockConn) PrepareContext(_ context.Context, query string) (stddriver.Stmt, error) {
	return &mockStmt{mock: c.mock, query: query}, nil
}
func (c *mockConn) Prepare(query string) (stddriver.Stmt, error) {
	return &mockStmt{mock: c.mock, query: query}, nil
}
func (c *mockConn) Close() error                 { return nil }
func (c *mockConn) Begin() (stddriver.Tx, error) { return mockTx{}, nil }
func (c *mockConn) BeginTx(context.Context, stddriver.TxOptions) (stddriver.Tx, error) {
	return mockTx{}, nil
}

// runQuery answers a read from the matching stub, or with empty rows by default.
func (m *Mock) runQuery(ctx context.Context, query string, args []stddriver.Value) (stddriver.Rows, error) {
	s, ok := m.match(stubQuery, query, args)
	if !ok {
		return &mockRows{}, nil
	}
	if err := waitFor(ctx, s.delay); err != nil {
		return nil, err
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.rows.cursor(), nil
}

// runExec answers a write from the matching stub, or with a zero result by
// default.
func (m *Mock) runExec(ctx context.Context, query string, args []stddriver.Value) (stddriver.Result, error) {
	s, ok := m.match(stubExec, query, args)
	if !ok {
		return mockResult{}, nil
	}
	if err := waitFor(ctx, s.delay); err != nil {
		return nil, err
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.result, nil
}

// waitFor sleeps for d, returning the context's error if it is canceled or its
// deadline passes first. A zero or negative d returns immediately.
func waitFor(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type mockStmt struct {
	mock  *Mock
	query string
}

var (
	_ stddriver.Stmt             = (*mockStmt)(nil)
	_ stddriver.StmtQueryContext = (*mockStmt)(nil)
	_ stddriver.StmtExecContext  = (*mockStmt)(nil)
)

func (s *mockStmt) Close() error  { return nil }
func (s *mockStmt) NumInput() int { return -1 }
func (s *mockStmt) Exec(args []stddriver.Value) (stddriver.Result, error) {
	return s.mock.runExec(context.Background(), s.query, args)
}
func (s *mockStmt) Query(args []stddriver.Value) (stddriver.Rows, error) {
	return s.mock.runQuery(context.Background(), s.query, args)
}
func (s *mockStmt) ExecContext(ctx context.Context, args []stddriver.NamedValue) (stddriver.Result, error) {
	return s.mock.runExec(ctx, s.query, argValues(args))
}
func (s *mockStmt) QueryContext(ctx context.Context, args []stddriver.NamedValue) (stddriver.Rows, error) {
	return s.mock.runQuery(ctx, s.query, argValues(args))
}

type mockTx struct{}

func (mockTx) Commit() error   { return nil }
func (mockTx) Rollback() error { return nil }

type mockResult struct {
	lastInsertID int64
	rowsAffected int64
}

func (r mockResult) LastInsertId() (int64, error) { return r.lastInsertID, nil }
func (r mockResult) RowsAffected() (int64, error) { return r.rowsAffected, nil }

type mockRows struct {
	cols    []string
	rows    [][]stddriver.Value
	rowErrs map[int]error
	pos     int
}

func (r *mockRows) Columns() []string { return r.cols }
func (r *mockRows) Close() error      { return nil }
func (r *mockRows) Next(dest []stddriver.Value) error {
	if err, ok := r.rowErrs[r.pos]; ok {
		return err
	}
	if r.pos >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.pos])
	r.pos++
	return nil
}
