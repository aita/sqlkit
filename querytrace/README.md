# querytrace

`querytrace` is an application-level SQL tracing library for `database/sql`. It
records the queries one operation — a request, a job, a usecase — issues,
together with the application context around them, and surfaces problems that
database-side statistics cannot see.

It is a **standalone tool**. It wraps any `database/sql` driver and observes
whatever runs through it — hand-written SQL, an ORM, another query builder, or
sqlkit's. You do **not** need to use the rest of sqlkit (its query builder or
models) to use it. Its only sqlkit touchpoint is the optional `sqlparse` parser
used for best-effort structural analysis and fingerprinting; without it,
querytrace falls back to lightweight string analysis.

It deliberately does **not** do execution-plan analysis, index/buffer/lock
inspection, or per-query latency percentiles — those belong to the database and
to your APM stack. `querytrace` answers a different question:

> For this request/usecase, what did the code ask the database to do, and from
> where?

This package lives under `x/` with its own module.

## What it catches

- **N+1 / query-in-loop** — the same query shape repeated within one trace.
- **Query budgets** — too many queries for one operation.
- **Read-only / forbidden access** — a write where only reads were expected, or
  any query where none was (a cache path that secretly hit the database).
- **Missing `WHERE`** — an `UPDATE` or `DELETE` with no filter.
- **Rows not closed** — a cursor opened and never closed.
- **Rows lifecycle cost** — the time spent *iterating* a cursor versus the time
  the query took to return, which a plain `QueryContext` span hides.
- **Cancellation mid-iteration** — a read abandoned through context
  cancellation or its deadline *while its rows were still being iterated*: the
  request gave up after the database had begun streaming rows, work it then
  threw away.
- **Too many parameters** — a statement carrying at or above
  `Warnings.MaxParams` bind arguments, the signature of an oversized `IN` list
  or batch.
- **Large `IN` list** — a top-level `WHERE` carrying an `IN (...)` value list at
  or above `Warnings.MaxInListLen`, a shape better expressed as a join or a
  temporary table.
- **`SELECT *`** — a query that projects every column (`Warnings.SelectStar`),
  which over-fetches and couples the caller to the table's column set.
- **Missing tenant / soft-delete predicate** — a read or write that touches a
  base table without filtering on a required tenant column
  (`Warnings.RequireTenantColumns`), or a `SELECT` that does not filter on a
  soft-delete marker (`Warnings.SoftDeleteColumns`) — the signature of a query
  that can cross tenant boundaries or return logically deleted rows.

These last three read predicate structure from the parsed AST, so they need a
parser (see below) and stay silent on the string-analysis fallback.

## How it fits together

```
context ── carries ──▶ Trace ── records ──▶ Event(s) ──▶ Report + Warnings
   ▲                                ▲
   │ WithTrace / ReadOnly / Mark    │ driver wrapper records each statement
```

- **core** (`querytrace`) — `Trace`, `Event`, `Report`, `Warning`, the context
  API, fingerprinting, and SQL analysis.
- **driver/** — a `database/sql/driver` wrapper. This is the primary
  integration: it works with any driver and, by wrapping the row cursor,
  captures the rows lifecycle.
- **querytest/** — a test spy and mock over a `Trace`: fluent assertions on what
  ran (`Expect(t, trace).MaxQueries(5).NoWrites().Query(Select(), Table("users")).Times(1)`)
  and a programmable `database/sql` double (`NewDB`).
- **`LogExporter`** — a summary exporter to `log/slog`, built into this package
  (`querytrace.NewLogExporter`); it needs only the standard library.
- **otelquery/** — an exporter to OpenTelemetry: one operation-level span per
  trace with a child DB span per statement. It is a separate module so the core
  stays dependency-free.

SQL analysis (operation, tables, `WHERE`/`JOIN` presence) and fingerprinting are
best-effort through a `sqlparse.Parser`. You wire one explicitly — pass a
frontend's parser (for example `postgres.New()` from
`github.com/aita/sqlkit/sqlparse/postgres`) via `WithParser`, or set
`Config.Parser` directly. A parse failure or a `nil` parser falls back to
lightweight string analysis and **never** aborts execution. The parser produces
a `sql.Query`, which `sql.Describe` introspects for the analysis and `ToSQL`
recompiles into a normalized fingerprint.

## Usage

Wrap a connector and open a `database/sql` handle over it:

```go
import (
    "database/sql"

    querytrace "github.com/aita/sqlkit/querytrace"
    qtdriver "github.com/aita/sqlkit/querytrace/driver"
    "github.com/aita/sqlkit/sqlparse/postgres"
)

// NewConfig starts from safe defaults (PostgreSQL dialect, no raw SQL, callers
// on error); layer options to wire a parser or capture more.
cfg := querytrace.NewConfig(querytrace.WithParser(postgres.New()))

db := sql.OpenDB(qtdriver.WrapConnector(connector, cfg))
```

To capture everything for development, add `WithVerboseCapture`; to use a custom
parser, pass it to `WithParser` or set `cfg.Parser` after constructing the config.

Create a trace per operation and put it in the context:

```go
trace := querytrace.New("GET /users/:id")
ctx := querytrace.WithTrace(r.Context(), trace)

// ... run the handler; every QueryContext/ExecContext under ctx is recorded ...

fmt.Println(querytrace.TextReport(trace))
```

### In tests

The `querytest` subpackage turns a `Trace` into a **test spy**: run the code
under a `Trace`, then assert on what it actually issued. `Expect` is a fluent
assertion bound to the test; `Query(matchers...)` narrows to a set of statements
and checks how many ran.

```go
func TestUserPageQueryCount(t *testing.T) {
    trace := querytrace.New("UserPage")
    ctx := querytrace.WithTrace(context.Background(), trace)

    if err := service.RenderUserPage(ctx, userID); err != nil {
        t.Fatal(err)
    }

    querytest.Expect(t, trace).
        MaxQueries(5).
        NoNPlusOne().
        Query(querytest.Select(), querytest.Table("users")).Times(1)
}

func TestCacheHitDoesNotAccessDB(t *testing.T) {
    trace := querytrace.New("CacheHit")
    ctx := querytrace.ForbidQueries(querytrace.WithTrace(context.Background(), trace))

    if _, err := service.GetUser(ctx, id); err != nil {
        t.Fatal(err)
    }
    querytest.Expect(t, trace).Warns(querytrace.WarnQueryForbidden)
}
```

For a unit test without a real database, `querytest.NewDB` returns a programmable
**mock** `*sql.DB` already traced, so the same spy assertions apply to its
statements. It registers a `t.Cleanup` that verifies the mock and closes the DB,
so a missed `Required` stub — or, by default, any statement no stub answered —
fails the test automatically (`querytest.Lenient()` opts out):

```go
func TestLoadUser(t *testing.T) {
    db, mock := querytest.NewDB(t)
    mock.ExpectQuery(querytest.Contains("FROM users")).
        Return(querytest.NewRows("id", "name").AddRow(1, "alice"))

    trace := querytrace.New("LoadUser")
    ctx := querytrace.WithTrace(context.Background(), trace)

    user, err := repo.Load(ctx, db, 1) // db.QueryContext(ctx, ...)
    if err != nil {
        t.Fatal(err)
    }

    querytest.Expect(t, trace).
        NoWrites().
        Query(querytest.Select(), querytest.Table("users")).Times(1)
    _ = user
}
```

An expectation can also be a `sql.Query` built with the sqlkit builder, with
holes left for the parts a test does not pin down — `AnyTable`, `AnyColumn`, and
`AnyExpr` for structure, and `Arg`, `AnyArg`, or `ArgMatch` for a bind value:

```go
mock.ExpectQuery(
    sql.Select(querytest.AnyColumn()).
        From(sql.Tbl("users")).
        Where(sql.Eq(sql.Col("id"), querytest.Arg(42))),
).Return(querytest.NewRows("id", "name").AddRow(42, "alice"))
```

This answers any `SELECT` from `users` filtered by `id = 42`, whatever columns it
selects. A plain bind value (`sql.Expr(...)`) is left unconstrained, so only the
parts you spell out with `Arg`/`ArgMatch` are checked.

### Exporting to OpenTelemetry

The `otel/` subpackage exports a finished `Trace` to OpenTelemetry as a span
tree: one operation-level span with a child span per statement carrying the
database semantic conventions (`db.system.name`, `db.query.text`,
`db.operation.name`, `db.collection.name`), warnings as span events, and the rows
lifecycle as attributes. Because a `Trace` already records each statement's
timestamps, it *replays* the trace into spans rather than instrumenting live.

It is a separate Go module (`github.com/aita/sqlkit/querytrace/otelquery`) so the
core querytrace module carries no OpenTelemetry dependency.

```go
import "github.com/aita/sqlkit/querytrace/otelquery"

exporter := otelquery.New(tp.Tracer("querytrace"), otelquery.WithDBSystem("postgresql"))

trace := querytrace.New("GET /users/:id")
ctx := querytrace.WithTrace(r.Context(), trace)
defer exporter.ExportTrace(ctx, trace)
```

Each statement span carries the application-level signals a plain SQL driver span
cannot — the rows lifecycle, caller, fingerprint — and warnings ride the spans
they concern. A whole-trace warning such as an **N+1 / query-in-loop**, which
belongs to no single statement, lands on the operation span as a
`querytrace.warning` event carrying its fingerprint and repeat count.
`WithWarningsAsErrors` raises a span's status on a forbidden/read-only violation.

The operation span becomes a child of any span already in the context (for
example an `otelhttp` server span), or a new trace root when there is none — so
querytrace alone yields a DB-centric trace without needing a separate driver
instrumentation. Its spans carry more than a generic SQL driver span would, so
running it *alongside* one (such as [otelsql](https://github.com/XSAM/otelsql))
is possible but produces a second, richer span per query. Spans are created with
the recorded timestamps, so they render at the right place on the timeline even
though export happens once at the end.

#### net/http middleware

`Exporter.Middleware` (and the single-handler `Exporter.Handler`) wires this into
a web server: it creates a `Trace` per request, places it in the request context,
and exports it when the request finishes. Every statement issued under the
request through a querytrace-wrapped driver becomes part of the operation's span
tree, with no per-handler boilerplate.

```go
exporter := otelquery.New(tp.Tracer("querytrace"), otelquery.WithDBSystem("postgresql"))

mux := http.NewServeMux()
mux.Handle("GET /users/{id}", exporter.Handler(usersHandler))
```

The operation is named after the routed pattern (`GET /users/{id}`), so it stays
low cardinality; pass `WithOperationNamer` to override. Place the middleware
inside any HTTP-level instrumentation (such as `otelhttp`) and the operation span
nests under the HTTP server span, joining querytrace's view to the request trace.

## Configuration

`Config` controls what is captured. `NewConfig(opts...)` is the recommended
starting point: its defaults are production-minded — argument *types* only (never
values), no raw SQL, and callers only on error — with analysis, fingerprinting,
and N+1 detection enabled. Layer options such as `WithParser`, `WithRawSQL`, or
`WithCaller` to adjust; `WithVerboseCapture` turns on the development-grade
capture (raw SQL and a caller for every statement) that the defaults leave off.

Defaults lean safe: argument **values** are never captured, and raw SQL is off
unless you turn it on.

### Warnings

The `WithWarnings` option holds the threshold- and policy-based checks. All are
off when zero/empty, so you opt into each one:

```go
cfg := querytrace.NewConfig(
    querytrace.WithParser(postgres.New()),
    querytrace.WithWarnings(querytrace.WarningConfig{
        MaxQueries:           50,                     // too_many_queries
        MaxParams:            900,                    // too_many_parameters
        MaxInListLen:         100,                    // large_in_list
        SelectStar:           true,                   // select_star
        RequireTenantColumns: []string{"tenant_id"},  // missing_tenant_predicate
        SoftDeleteColumns:    []string{"deleted_at"}, // missing_soft_delete_predicate
    }),
)
```

The tenant and soft-delete checks match by **bare, case-insensitive column
name** in the statement's top-level `WHERE`: any read or write touching a base
table whose `WHERE` references none of `RequireTenantColumns` raises
`missing_tenant_predicate` (an `INSERT` is exempt — it carries its tenant value
in the row, not a filter), and any `SELECT` whose `WHERE` references none of
`SoftDeleteColumns` raises `missing_soft_delete_predicate`. They are deliberately
a blunt instrument applied across every analyzed statement, so scope them to the
columns your schema actually uses. Like `large_in_list` and `select_star`, they
read the parsed AST and stay silent when no parser is wired.

### Transaction linking

Each statement run inside a `database/sql` transaction records the id of that
transaction on its event (`QueryInfo.TxID`), so a report — and the OpenTelemetry
export — can attribute every query and exec to the `BEGIN`/`COMMIT` pair around
it. Statements run on a pooled connection after the transaction ends carry no tx
id again. Linking follows `database/sql`'s connection ownership, which holds one
driver connection for the life of a transaction.

### Sampling

In production you usually want to observe a *fraction* of traffic, not every
request, so analysis and caller capture stay off the hot path most of the time.
A `Sampler` decides **once per Trace, at creation,** whether that Trace records;
a sampled-out Trace turns the wrapped driver into a thin pass-through for its
whole lifetime and reports nothing. Sampling is a whole-Trace decision on
purpose — a per-statement sampler would fragment a trace and make N+1 detection
and query budgets meaningless.

```go
// Record ~5% of traces; the rest are pass-throughs.
trace := querytrace.New("GET /users/:id", querytrace.WithSampler(querytrace.SampleFraction(0.05)))
```

`AlwaysSample` (the default), `NeverSample`, and `SampleFraction(p)` cover the
common cases; implement `Sampler` for anything else (per-tenant, header-driven,
…). The OpenTelemetry middleware takes the same sampler with
`otelquery.WithSampler`, and skips exporting a sampled-out request entirely.

## Dialects

Only PostgreSQL has a parser today; other dialects use the string-analysis
fallback, which still recognizes the statement kind and fingerprints queries but
cannot drive the AST-based checks (missing `WHERE`, large `IN`, `SELECT *`, and
the tenant/soft-delete predicates).
