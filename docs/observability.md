# Observability: hooks, tracing, analysis

sqlkit gives you three layers for seeing — and shaping — what runs against the
database:

- [Hooks](#hooks) — rewrite or observe every statement around execution.
- [querytrace](#querytrace) — application-level SQL tracing (N+1, query budgets,
  forbidden access).
- [analysis](#analysis) — read-only structural analysis over the SQL AST.

## Hooks

[`Hooks`](https://pkg.go.dev/github.com/aita/sqlkit#Hooks) observe — and may
rewrite — statements around execution. A `Before` hook runs just before a
statement reaches the database; an `AfterQuery`/`AfterExec` hook runs once its
result is produced. Register them with
[`WithHooks`](https://pkg.go.dev/github.com/aita/sqlkit#WithHooks); they apply
to every read and write run through the database and any session it opens, and
nil fields are skipped so a hook fills in only the points it needs.

`Before` is symmetric across reads and writes (`stmt.Op` distinguishes them) and
is where you rewrite SQL or args:

```go
db, _ := sqlkit.Open("pgx", dsn, sqlkit.WithHooks(sqlkit.Hooks{
    Before: func(ctx context.Context, stmt *sqlkit.Statement) (context.Context, error) {
        stmt.SQL = "/* tenant=" + tenantOf(ctx) + " */ " + stmt.SQL
        return ctx, nil
    },
}))
```

The `After` hooks split because the result type differs: `AfterQuery` receives a
read's result already materialized into the caller's destination — a real Go
value you can inspect or transform, unlike the opaque `*sql.Rows` cursor the
execution path works with — while `AfterExec` receives a write's `sql.Result`.
Both run on failure too (with a non-nil error), so a `Before`/`After` pair brackets
execution cleanly for tracing or metrics, threading a span through the context
`Before` returns.

Hooks deliberately offer no retry or short-circuit: statement-level retry is
unsafe across a transaction boundary — a failed statement may have aborted the
surrounding transaction, so the whole unit of work must replay — and belongs
outside the database, around the unit of work. For per-value transformation,
implement `database/sql.Scanner` on the column type instead.

## querytrace

[`querytrace`](../querytrace/) is an application-level SQL tracing library for
`database/sql`. It records the queries one operation — a request, a job, a
usecase — issues, together with the application context around them, and surfaces
problems that database-side statistics cannot see: N+1 / query-in-loop, query
budgets, a write where only reads were expected, a missing `WHERE`, an oversized
`IN` list, `SELECT *`, a cursor left open, and more.

It is a **standalone tool**: it wraps any `database/sql` driver and observes
whatever runs through it — hand-written SQL, an ORM, another builder, or
sqlkit's — so you do not need the rest of sqlkit to use it. It deliberately does
**not** do execution-plan analysis or latency percentiles; those belong to your
database and APM stack.

```go
trace := querytrace.New("RenderUserPage")
ctx := querytrace.WithTrace(ctx, trace)
// ... run the operation; statements flow through the wrapped driver ...
report := trace.Report()
fmt.Println(querytrace.TextReport(report))
```

- Drive assertions on a trace in tests with [`sqltest`](testing.md#query-spy).
- Export a trace to OpenTelemetry as a span tree with
  [`querytrace/otelquery`](../querytrace/otelquery/) (its own module, so the
  OpenTelemetry SDK stays out of the root module).

See the [querytrace README](../querytrace/README.md) for the full feature list,
configuration, and integration details.

## analysis

[`analysis`](https://pkg.go.dev/github.com/aita/sqlkit/analysis) is a small,
read-only analysis framework over the sqlkit SQL AST. It answers focused
structural questions about a built or parsed statement —
[`KindOf`](https://pkg.go.dev/github.com/aita/sqlkit/analysis#KindOf) (statement
kind), [`Tables`](https://pkg.go.dev/github.com/aita/sqlkit/analysis#Tables)
(tables touched),
[`HasTopLevelWhere`](https://pkg.go.dev/github.com/aita/sqlkit/analysis#HasTopLevelWhere) /
[`HasTopLevelJoin`](https://pkg.go.dev/github.com/aita/sqlkit/analysis#HasTopLevelJoin),
[`FilterColumns`](https://pkg.go.dev/github.com/aita/sqlkit/analysis#FilterColumns),
[`SelectsStar`](https://pkg.go.dev/github.com/aita/sqlkit/analysis#SelectsStar),
and [`Fingerprint`](https://pkg.go.dev/github.com/aita/sqlkit/analysis#Fingerprint)
(a normalized shape id). It is
the structural engine querytrace's warnings build on, exposed for your own
linters and guards.

`Fingerprint` needs a dialect to recompile and normalize the statement; without
one it reports `ok=false`.

---

← Back to the [documentation index](README.md).
