# Query routing

`WrapExecutor` accepts any terminal `Executor`, and `NewRouter` is one that
spreads statements across several named backends — read/write splitting,
sharding, or any policy you write. You register the backends by alias (with a
required `"default"` fallback) and supply a `Router` that returns the alias each
statement should run on; `""` means `"default"`.

Read/write splitting is built in: `ReadWriteRouter` sends writes to `"default"`
(the primary) and balances reads across the replicas named with `WithReplicas`
(round-robin by default; pass `WithBalancer(sqlkit.Random())` or your own
`Balancer`). With no replicas it routes reads to the primary too, so the same
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
policy. It receives the fully built `Statement`, so it branches on
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
runs through the row-returning path with `Op == OpQuery` yet is a write, and a
locking read (`SELECT ... FOR UPDATE`) takes row locks — `stmt.IsWrite()` reports
both correctly so they never land on a read replica. For the three-way
read/write/**undetermined** distinction (an opaque raw string whose nature
cannot be inferred, which `IsWrite` folds to the safe write side), branch on
`stmt.Access()` and handle `AccessUnknown` yourself.

**Hints.** `WithRoute(alias)` is an advisory hint a caller attaches to a single
execution or to a whole session; the `Router` decides whether to honor it. The
built-in `ReadWriteRouter` honors it only for reads (pin a read to a specific
replica, or to the primary for read-after-write) and ignores it on a write, so a
stray hint can never send a write to a read replica. On a terminal it sets
`Statement.RouteHint`; it propagates to the statement's `Preload` follow-ups so a
sharded parent and its children share a backend, unless you pass `NoPropagate`:

```go
db.Select().From(Users).Preload(Posts).All(ctx, sqlkit.WithRoute("replica"))
db.Select().From(Users).Preload(Posts).All(ctx, sqlkit.WithRoute("replica", sqlkit.NoPropagate))
```

**Sessions** pin to one backend. A transaction cannot span backends, so the
`Router` is consulted once, at `Begin`, from the session's `WithRoute` hint and
read-only intent; every statement in the session then runs on that backend. A
read-only session routes to a read replica without a hint:

```go
db.NewSession(sqlkit.WithRoute("replica"))
db.NewSession(sqlkit.WithTxOptions(sqlkit.TxOptions{ReadOnly: true})) // -> replica via the router
```

A per-statement `WithRoute` inside a pinned session that names a *different*
backend is `ErrRouteConflict` (a distributed transaction the router will not
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
