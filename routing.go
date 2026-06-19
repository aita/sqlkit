package sqlkit

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync/atomic"

	"github.com/aita/sqlkit/sql"
)

// ErrRouteConflict is returned when a statement run inside a session carries a
// WithRoute hint naming a backend other than the one the session is pinned to. A
// transaction lives on a single backend, so it cannot be relocated mid-flight;
// the conflicting hint is a programming error rather than something to silently
// ignore. Test for it with errors.Is.
var ErrRouteConflict = errors.New(
	"sqlkit: WithRoute conflicts with the session's pinned backend; a transaction cannot span backends")

// Backend is one routable execution target — typically a connection pool wrapped
// with DB. It is a TxExecutor so the router can both run auto-commit statements
// and begin transactions on it for pinned sessions.
type Backend interface {
	TxExecutor
}

// DB adapts a database/sql handle to a Backend, the routing counterpart to
// WrapDB. Register the handles behind a Router with NewRouter:
//
//	router := sqlkit.NewRouter(map[string]sqlkit.Backend{
//		"default": sqlkit.DB(primary),
//		"replica": sqlkit.DB(replica),
//	}, myRouter)
//	db := sqlkit.WrapExecutor(router)
func DB(db *stdsql.DB) Backend { return dbExecutor{db} }

// Router decides which backend alias a statement runs against. It is the single
// routing decision point: both auto-commit statements and sessions (at Begin)
// consult it. Return "" to defer to the "default" backend.
//
// Route receives the fully built Statement, so it can branch on stmt.IsWrite()
// (correct even for RETURNING writes and locking reads), stmt.Tables(),
// stmt.Kind(), or stmt.RouteHint (the advisory alias a caller set with
// WithRoute). For the three-way read/write/undetermined distinction — to route a
// statement whose nature could not be inferred (an opaque raw string) on its own
// path instead of the safe write default — branch on stmt.Access() and handle
// AccessUnknown explicitly. Honoring the hint is the Router's choice; the
// recommended idiom is to return it when set:
//
//	func (r MyRouter) Route(ctx context.Context, stmt sqlkit.Statement) string {
//		if stmt.RouteHint != "" {
//			return stmt.RouteHint
//		}
//		if stmt.IsWrite() {
//			return "" // -> default (primary)
//		}
//		return "replica"
//	}
type Router interface {
	Route(ctx context.Context, stmt Statement) string
}

// Routing is the terminal Executor that routes each statement to a named Backend
// chosen by a Router. Pass it to WrapExecutor. It implements TxExecutor, so a
// session opened over it runs in a real transaction pinned to the single backend
// the Router selects for that session.
//
// Routing itself emits no logs: a Router is your own code, so log routing
// decisions there if you want them, and observe execution through Hooks.
type Routing struct {
	backends map[string]Backend
	router   Router
}

// NewRouter builds a Routing over the named backends, choosing among them with
// router. The "default" backend is the fallback used whenever the Router returns
// "" (or for a statement the Router has no opinion on), so a typical setup
// registers the primary as "default".
func NewRouter(backends map[string]Backend, router Router) *Routing {
	return &Routing{backends: backends, router: router}
}

// ReadWriteRouter returns the canonical read/write-splitting Router: it sends
// writes — and undetermined statements (AccessUnknown), to stay on the safe side
// — to the "default" backend (the primary), and balances reads across the
// replica backends named with WithReplicas. Locking reads (SELECT ... FOR
// UPDATE) count as writes and go to the primary.
//
// A WithRoute hint is honored only for reads (pin a read to a specific replica,
// or to the primary for read-after-write); on a write it is ignored, so a stray
// hint can never route a write to a read replica.
//
//	db := sqlkit.WrapExecutor(sqlkit.NewRouter(map[string]sqlkit.Backend{
//		"default": sqlkit.DB(primary),
//		"replica": sqlkit.DB(replica),
//	}, sqlkit.ReadWriteRouter(sqlkit.WithReplicas("replica"))))
//
// With no replicas — a single-database setup, or one where replicas are
// configured only in some environments — reads also go to "default", so the same
// wiring works whether or not replicas are present.
func ReadWriteRouter(opts ...ReadWriteOption) Router {
	r := &readWriteRouter{}
	for _, opt := range opts {
		opt(r)
	}
	// Default to round-robin only when it is actually needed — not when the
	// caller supplied a Balancer, nor when there are no replicas to balance.
	if r.balancer == nil && len(r.replicas) > 0 {
		r.balancer = RoundRobin()
	}
	return r
}

// ReadWriteOption configures ReadWriteRouter.
type ReadWriteOption func(*readWriteRouter)

// WithReplicas names the replica backends reads are balanced across. With none,
// reads go to the primary ("default").
func WithReplicas(aliases ...string) ReadWriteOption {
	return func(r *readWriteRouter) { r.replicas = aliases }
}

// WithBalancer sets how reads are spread across replicas. The default is
// RoundRobin; pass Random or your own Balancer.
func WithBalancer(b Balancer) ReadWriteOption {
	return func(r *readWriteRouter) {
		if b != nil {
			r.balancer = b
		}
	}
}

type readWriteRouter struct {
	replicas []string
	balancer Balancer
}

func (r *readWriteRouter) Route(_ context.Context, stmt Statement) string {
	// Writes and undetermined statements always go to the primary; the hint is
	// not honored here, so a stray WithRoute("replica") on a write can never send
	// it to a read replica. (There is only one writer in a read/write split.)
	if stmt.Access() != AccessRead {
		return ""
	}
	// A read may be pinned to a specific backend — a replica, or the primary for
	// read-after-write — via WithRoute.
	if stmt.RouteHint != "" {
		return stmt.RouteHint
	}
	if len(r.replicas) == 0 {
		return "" // no replicas configured -> read from the primary
	}
	return r.balancer.Pick(r.replicas)
}

// Balancer chooses one replica alias from the configured candidates for a read.
// RoundRobin and Random are provided; implement it for a custom policy (weights,
// least-connections, ...).
type Balancer interface {
	// Pick returns one of replicas, which is non-empty.
	Pick(replicas []string) string
}

// RoundRobin returns a Balancer that cycles through the replicas in order. Each
// call to ReadWriteRouter needs its own, so RoundRobin returns a fresh one.
func RoundRobin() Balancer { return &roundRobin{} }

type roundRobin struct{ next atomic.Uint64 }

func (b *roundRobin) Pick(replicas []string) string {
	i := b.next.Add(1) - 1
	return replicas[i%uint64(len(replicas))]
}

// Random returns a Balancer that picks a replica uniformly at random.
func Random() Balancer { return random{} }

type random struct{}

func (random) Pick(replicas []string) string {
	return replicas[rand.IntN(len(replicas))]
}

// pick resolves the backend for a statement: the Router's choice, or "default"
// when it abstains. It errors when the chosen alias is not registered.
func (r *Routing) pick(ctx context.Context, stmt Statement) (Backend, string, error) {
	alias := r.router.Route(ctx, stmt)
	if alias == "" {
		alias = "default"
	}
	b, ok := r.backends[alias]
	if !ok {
		return nil, "", fmt.Errorf("sqlkit: route to unknown backend %q", alias)
	}
	return b, alias, nil
}

// Query routes and runs a read statement.
func (r *Routing) Query(ctx context.Context, stmt Statement) (*stdsql.Rows, error) {
	b, _, err := r.pick(ctx, stmt)
	if err != nil {
		return nil, err
	}
	return b.Query(ctx, stmt)
}

// Exec routes and runs a write statement.
func (r *Routing) Exec(ctx context.Context, stmt Statement) (stdsql.Result, error) {
	b, _, err := r.pick(ctx, stmt)
	if err != nil {
		return nil, err
	}
	return b.Exec(ctx, stmt)
}

// Begin satisfies TxExecutor for the plain (unrouted) case, pinning the
// transaction to the backend the Router picks for a non-read-only, hint-free
// session. Sessions use beginRouted to convey their route hint and read-only
// intent.
func (r *Routing) Begin(ctx context.Context, opts *stdsql.TxOptions) (Tx, error) {
	tx, _, err := r.beginRouted(ctx, opts, "", opts != nil && opts.ReadOnly)
	return tx, err
}

// beginRouted picks the backend for a whole session and begins a transaction on
// it, returning the chosen alias so the session can pin to it. The session's
// route is decided once here, from its WithRoute hint and read-only intent; a
// transaction cannot span backends, so per-statement routing is suspended for
// its lifetime.
func (r *Routing) beginRouted(ctx context.Context, opts *stdsql.TxOptions, hint string, readOnly bool) (Tx, string, error) {
	stmt := Statement{RouteHint: hint}
	if readOnly {
		stmt.Op = OpQuery
	} else {
		stmt.Op = OpExec
	}
	b, alias, err := r.pick(ctx, stmt)
	if err != nil {
		return nil, "", err
	}
	tx, err := b.Begin(ctx, opts)
	if err != nil {
		return nil, "", err
	}
	return tx, alias, nil
}

// ── per-execution / per-session options ──────────────────────────────────────

// ExecOption configures a single execution — a terminal such as All, First, One,
// Page, Exec, or RawSQL. WithRoute is one.
type ExecOption interface {
	applyExec(*execOptions)
}

type execOptions struct {
	route       string
	noPropagate bool
}

// RouteFlag tunes a WithRoute hint. The only flag is NoPropagate.
type RouteFlag int

const (
	_ RouteFlag = iota
	// NoPropagate confines a per-statement WithRoute to the statement itself, so
	// its Preload follow-up queries are routed independently (by their own
	// table) rather than inheriting the hint. It is safe for read/write
	// splitting but dangerous for sharding, where a child must hit the same
	// shard as its parent; use it only when you mean it.
	NoPropagate
)

// routeOption is the value WithRoute returns; it is both an ExecOption (for
// terminals) and a SessionOption (for NewSession/WithSession).
type routeOption struct {
	alias       string
	noPropagate bool
}

func (o routeOption) applyExec(e *execOptions) {
	e.route = o.alias
	e.noPropagate = o.noPropagate
}

func (o routeOption) applySession(s *sessionOptions) {
	// NoPropagate is meaningless for a session: the whole session is pinned to
	// one backend, so there is nothing to propagate.
	s.route = o.alias
}

// RouteOption is the option returned by WithRoute, accepted by both terminals
// and session constructors.
type RouteOption interface {
	ExecOption
	SessionOption
}

// WithRoute supplies an advisory routing hint. Passed to a terminal it sets the
// statement's RouteHint, which the Router reads from Route; passed to NewSession
// or WithSession it routes the whole session to that backend at Begin. It is a
// hint, not a command: the Router decides whether to honor it (the conventional
// idiom honors it). Add NoPropagate to keep a per-statement hint off Preload
// follow-ups.
//
//	db.Select().From(Users).All(ctx, sqlkit.WithRoute("replica"))
//	db.NewSession(sqlkit.WithRoute("replica"))
func WithRoute(alias string, flags ...RouteFlag) RouteOption {
	o := routeOption{alias: alias}
	for _, f := range flags {
		if f == NoPropagate {
			o.noPropagate = true
		}
	}
	return o
}

// applyExecOptions resolves a terminal's ExecOptions into the binding the
// statement runs through and the binding its Preload follow-ups run through. With
// no route they are both base; with a route, the main binding carries the hint,
// and the preload binding carries it too unless NoPropagate was set.
func applyExecOptions(base executorBinding, opts []ExecOption) (ex, preloadEx executorBinding) {
	if len(opts) == 0 {
		return base, base
	}
	var eo execOptions
	for _, o := range opts {
		if o != nil {
			o.applyExec(&eo)
		}
	}
	if eo.route == "" {
		return base, base
	}
	routed := routedBinding{base: base, hint: eo.route}
	if eo.noPropagate {
		return routed, base
	}
	return routed, routed
}

// routedBinding wraps an executorBinding to stamp a RouteHint onto the statements
// built through it, delegating everything else to the base. It is how a
// per-statement WithRoute reaches the Statement the Router sees, and — because
// Preload follow-ups reuse the binding — how the hint propagates to them.
type routedBinding struct {
	base executorBinding
	hint string
}

func (r routedBinding) sqlDialect() sql.Dialect { return r.base.sqlDialect() }
func (r routedBinding) hooks() []Hooks          { return r.base.hooks() }
func (r routedBinding) maxBindParams() int      { return r.base.maxBindParams() }
func (r routedBinding) executor() Executor      { return r.base.executor() }
func (r routedBinding) routeHint() string       { return r.hint }

// routedBeginner is implemented by *Routing: it begins a session's transaction
// on the backend chosen from the session's route hint and read-only intent, and
// reports the chosen alias so the session can pin to it. A session whose base
// does not implement it (a plain WrapDB/Open database) is not routed.
type routedBeginner interface {
	beginRouted(ctx context.Context, opts *stdsql.TxOptions, hint string, readOnly bool) (Tx, string, error)
}
