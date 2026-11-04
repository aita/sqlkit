# Query routing

[`WrapExecutor`](https://pkg.go.dev/github.com/aita/sqlkit#WrapExecutor) accepts
any terminal [`Executor`](https://pkg.go.dev/github.com/aita/sqlkit#Executor),
and [`NewRouter`](https://pkg.go.dev/github.com/aita/sqlkit#NewRouter) is one that
spreads statements across several named backends — read/write splitting,
sharding, or any policy you write. You register the backends by alias (with a
required `"default"` fallback) and supply a
[`Router`](https://pkg.go.dev/github.com/aita/sqlkit#Router) that returns the alias each
statement should run on; `""` means `"default"`.

Read/write splitting is built in:
[`ReadWriteRouter`](https://pkg.go.dev/github.com/aita/sqlkit#ReadWriteRouter)
sends writes to `"default"` (the primary) and balances reads across the replicas
named with [`WithReplicas`](https://pkg.go.dev/github.com/aita/sqlkit#WithReplicas)
(round-robin by default; pass
[`WithBalancer`](https://pkg.go.dev/github.com/aita/sqlkit#WithBalancer)`(`[`sqlkit.Random()`](https://pkg.go.dev/github.com/aita/sqlkit#Random)`)`
or your own
[`Balancer`](https://pkg.go.dev/github.com/aita/sqlkit#Balancer)). With no replicas it routes reads to the primary too, so the same
wiring works whether or not replicas are configured (a single database, or
replicas only in some environments).

```go
router := sqlkit.NewRouter(map[string]sqlkit.Backend{
    "default": sqlkit.DB(primary),  // *sql.DB
    "replica": sqlkit.DB(replica),
}, sqlkit.ReadWriteRouter(sqlkit.WithReplicas("replica")))
db := sqlkit.WrapExecutor(router)
// callers are unchanged: db.Select()..., db.Insert()..., sessions, preloads.
```

`Router` is the single decision point — write your own for sharding or any
policy. It receives the fully built
[`Statement`](https://pkg.go.dev/github.com/aita/sqlkit#Statement), so it branches on
`stmt.IsWrite()`, `stmt.Tables()`, `stmt.Kind()`, or `stmt.RouteHint`:

```go
type ShardByTenant struct{}

func (ShardByTenant) Route(ctx context.Context, stmt sqlkit.Statement) string {
    if stmt.RouteHint != "" { // honor an explicit WithRoute (your choice)
        return stmt.RouteHint
    }
    return fmt.Sprintf("shard-%d", tenantOf(ctx)%shardCount)
}
```

Route on **`Kind`/`IsWrite`, not `Op`**: an `INSERT/UPDATE/DELETE ... RETURNING`
runs through the row-returning path with `Op == `[`OpQuery`](https://pkg.go.dev/github.com/aita/sqlkit#OpQuery) yet is a write, and a
locking read (`SELECT ... FOR UPDATE`, including one nested in a derived table,
CTE, subquery or set-operation arm) takes row locks — `stmt.IsWrite()` reports
both correctly so they never land on a read replica. A data-modifying CTE
(`WITH x AS (INSERT/UPDATE/DELETE ... RETURNING ...) SELECT ...`) is likewise a
write even though its top-level statement is a `SELECT`; `IsWrite()` detects it
**from the parsed AST**, so a router that runs builder-produced or parsed
statements keeps it off replicas. The keyword-guess fallback (a raw SQL string
analyzed without a parser) cannot see into the CTE body, so attach a
[`WithRoute`](https://pkg.go.dev/github.com/aita/sqlkit#WithRoute) hint to such
raw statements if you route them. For the three-way
read/write/**undetermined** distinction (an opaque raw string whose nature
cannot be inferred, which `IsWrite` folds to the safe write side), branch on
`stmt.Access()` and handle [`AccessUnknown`](https://pkg.go.dev/github.com/aita/sqlkit#AccessUnknown) yourself.

**Hints.** [`WithRoute`](https://pkg.go.dev/github.com/aita/sqlkit#WithRoute)`(alias)` is an advisory hint a caller attaches to a single
execution or to a whole session; the `Router` decides whether to honor it. The
built-in `ReadWriteRouter` honors it only for reads (pin a read to a specific
replica, or to the primary for read-after-write) and ignores it on a write, so a
stray hint can never send a write to a read replica. On a terminal it sets
`Statement.RouteHint`; it propagates to the statement's `Preload` follow-ups so a
sharded parent and its children share a backend, unless you pass [`NoPropagate`](https://pkg.go.dev/github.com/aita/sqlkit#NoPropagate):

```go
db.Select().From(Users).Preload(Posts).All(ctx, sqlkit.WithRoute("replica"))
db.Select().From(Users).Preload(Posts).All(ctx, sqlkit.WithRoute("replica", sqlkit.NoPropagate))
```

**Sessions** pin to one backend. A transaction cannot span backends, so the
`Router` is consulted once, at `Begin`, from the session's `WithRoute` hint and
read-only intent; every statement in the session then runs on that backend. A
read-only session routes to a read replica without a hint. `WithRoute` alone,
without `ReadOnly`, is a no-op under the built-in `ReadWriteRouter`: a session
that might later write must never pin to a replica, so the router only honors a
session-level route hint when it is also marked read-only — pass both together
to pin a read-only session to a specific replica:

```go
db.NewSession(sqlkit.WithRoute("replica")) // ReadWriteRouter ignores this; runs on the primary
db.NewSession(sqlkit.WithTxOptions(sqlkit.TxOptions{ReadOnly: true})) // -> replica via the router
db.NewSession(sqlkit.WithRoute("replica"), sqlkit.WithTxOptions(sqlkit.TxOptions{ReadOnly: true})) // -> replica, pinned
```

A hand-written `Router` can honor a plain `WithRoute` hint regardless of
read-only intent by checking `stmt.RouteHint` before `stmt.IsWrite()` — see
`rwSplit` in the package tests for the pattern — but the built-in
`ReadWriteRouter` deliberately does not, to stay safe by default.

A per-statement `WithRoute` inside a pinned session that names a *different*
backend is [`ErrRouteConflict`](https://pkg.go.dev/github.com/aita/sqlkit#ErrRouteConflict) (a distributed transaction the router will not
fake); one that matches the pin is redundant but harmless and runs. The router
itself emits no logs — a `Router` is your own code, so log routing decisions
there if you want them, and observe execution through [Hooks](observability.md).

Routing is in-process and decides per statement from the plain `Statement`
value, so it never depends on the query builder internals. A single statement
always runs on a single backend — there is no scatter-gather across shards and
no distributed transaction; those are the caller's responsibility.

For applying migrations across the same backends, see
[Migrations across backends](migrations.md#migrations-across-backends).

---

← Back to the [documentation index](README.md).
