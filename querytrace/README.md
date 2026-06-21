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

This package is part of the root `github.com/aita/sqlkit` module; its only
optional add-on is `querytrace/otelquery` (OpenTelemetry export), which is a
separate module so the OpenTelemetry SDK stays out of the core.

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
- **Too many parameters** — a statement carrying at or above the
  `WithMaxParams` budget of bind arguments, the signature of an oversized `IN`
  list or batch.
- **Large `IN` list** — a top-level `WHERE` carrying an `IN (...)` value list at
  or above the `WithMaxInList` budget, a shape better expressed as a join or a
  temporary table.
- **`SELECT *`** — a query that projects every column (`WithSelectStarWarning`),
  which over-fetches and couples the caller to the table's column set.
- **Missing tenant / soft-delete predicate** — a read or write that touches a
  base table without filtering on a required tenant column
  (`WithTenantColumns`), or a `SELECT` that does not filter on a soft-delete
  marker (`WithSoftDeleteColumns`) — the signature of a query that can cross
  tenant boundaries or return logically deleted rows. Each check can be scoped to
  the tables that actually have the column — by table-qualified specs or
  `ScopedToSchema` — so a reference table never false-positives (see below).

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
- **`sqltest`** (top-level `github.com/aita/sqlkit/sqltest`) — a test spy and
  mock built over a `Trace`: fluent assertions on what ran
  (`Expect(t, trace).MaxQueries(5).NoWrites().Query(Select(), Table("users")).Times(1)`)
  and a programmable `database/sql` double (`NewDB`).
- **`LogExporter`** — a summary exporter to `log/slog`, built into this package
  (`querytrace.NewLogExporter`); it needs only the standard library.
- **`Metrics`** — the numeric aggregate of a `Trace` (statement counts by kind,
  unique fingerprints, transactions, total durations, warnings by code), emitted
  through the two-method `MetricsRecorder` you implement over your backend. Built
  into this package; it needs only the standard library (see below).
- **otelquery/** — an exporter to OpenTelemetry: one operation-level span per
  trace with a child DB span per statement. It is a separate module so the core
  stays dependency-free.

SQL analysis (operation, tables, `WHERE`/`JOIN` presence) and fingerprinting are
best-effort through a `sqlparse.Parser`. You wire one explicitly — pass a
frontend's parser (for example `postgres.New()` from
`github.com/aita/sqlkit/sqlparse/postgres`) via `WithParser`, or set
`Config.Parser` directly. A parse failure or a `nil` parser falls back to
lightweight string analysis and **never** aborts execution. The parser produces
a `sql.Query`, which the `analysis` package
(`github.com/aita/sqlkit/analysis`) introspects for the analysis and `ToSQL`
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

The top-level [`sqltest`](../sqltest) package turns a `Trace` into a **test
spy**: run the code under a `Trace`, then assert on what it actually issued.
`Expect` is a fluent assertion bound to the test; `Query(matchers...)` narrows to
a set of statements and checks how many ran.

```go
func TestUserPageQueryCount(t *testing.T) {
    trace := querytrace.New("UserPage")
    ctx := querytrace.WithTrace(context.Background(), trace)

    if err := service.RenderUserPage(ctx, userID); err != nil {
        t.Fatal(err)
    }

    sqltest.Expect(t, trace).
        MaxQueries(5).
        NoNPlusOne().
        Query(sqltest.Select(), sqltest.Table("users")).Times(1)
}

func TestCacheHitDoesNotAccessDB(t *testing.T) {
    trace := querytrace.New("CacheHit")
    ctx := querytrace.ForbidQueries(querytrace.WithTrace(context.Background(), trace))

    if _, err := service.GetUser(ctx, id); err != nil {
        t.Fatal(err)
    }
    sqltest.Expect(t, trace).Warns(querytrace.WarnQueryForbidden)
}
```

For a unit test without a real database, `sqltest.NewDB` returns a programmable
**mock** `*sql.DB` already traced, so the same spy assertions apply to its
statements. It registers a `t.Cleanup` that verifies the mock and closes the DB,
so a missed `Required` stub — or, by default, any statement no stub answered —
fails the test automatically (`sqltest.Lenient()` opts out):

```go
func TestLoadUser(t *testing.T) {
    db, mock := sqltest.NewDB(t)
    mock.ExpectQuery(sqltest.Contains("FROM users")).
        Return(sqltest.NewRows("id", "name").AddRow(1, "alice"))

    trace := querytrace.New("LoadUser")
    ctx := querytrace.WithTrace(context.Background(), trace)

    user, err := repo.Load(ctx, db, 1) // db.QueryContext(ctx, ...)
    if err != nil {
        t.Fatal(err)
    }

    sqltest.Expect(t, trace).
        NoWrites().
        Query(sqltest.Select(), sqltest.Table("users")).Times(1)
    _ = user
}
```

An expectation can also be a `sql.Query` built with the sqlkit builder, with
holes left for the parts a test does not pin down — `AnyTable`, `AnyColumn`, and
`AnyExpr` for structure, and `Arg`, `AnyArg`, or `ArgMatch` for a bind value:

```go
mock.ExpectQuery(
    sql.Select(sqltest.AnyColumn()).
        From(sql.Tbl("users")).
        Where(sql.Eq(sql.Col("id"), sqltest.Arg(42))),
).Return(sqltest.NewRows("id", "name").AddRow(42, "alice"))
```

This answers any `SELECT` from `users` filtered by `id = 42`, whatever columns it
selects. A plain bind value (`sql.Expr(...)`) is left unconstrained, so only the
parts you spell out with `Arg`/`ArgMatch` are checked.

### Emitting metrics

`Trace.Metrics()` reduces a finished trace to a `Metrics` value — statement
counts (total, writes, and per kind), unique fingerprints, transactions, the
total query and row-iteration durations, and warning counts by code and
severity. Inspect it directly, or push it to your metrics backend by
implementing the two-method `MetricsRecorder` and calling `Trace.RecordMetrics`
(the one-call form of `trace.Metrics().Emit(rec)`). The core package depends on
nothing but the standard library, so it pulls in no instrumentation library —
you adapt `Counter`/`Duration` to Prometheus, OpenTelemetry metrics, statsd, or
your own counters.

```go
// A MetricsRecorder over a Prometheus CounterVec/HistogramVec, for example.
type promRecorder struct{ /* counter *prometheus.CounterVec; hist *prometheus.HistogramVec */ }

func (r promRecorder) Counter(name string, v int64, labels ...querytrace.Label) {
    // counter.With(toLabels(labels)).Add(float64(v))
}
func (r promRecorder) Duration(name string, d time.Duration, labels ...querytrace.Label) {
    // hist.With(toLabels(labels)).Observe(d.Seconds())
}

trace := querytrace.New("GET /users/:id")
defer trace.RecordMetrics(promRecorder{ /* ... */ })
ctx := querytrace.WithTrace(r.Context(), trace)
// ... run the request's queries through the wrapped driver ...
```

Metrics are emitted under stable names (the `Metric*` constants:
`querytrace.statements` with a `kind` label, `querytrace.unique_fingerprints`,
`querytrace.transactions`, `querytrace.warnings` with a `code` label,
`querytrace.query_duration`, `querytrace.rows_iteration_duration`), each tagged
with the trace's `operation` when set.

For OpenTelemetry metrics specifically, the `otelquery` module ships a ready
adapter so you do not write the `MetricsRecorder` yourself: `otelquery.ExportMetrics`
records a trace's metrics on a `metric.Meter` (counters as `Int64Counter` adds,
durations as `Float64Histogram` observations in seconds), the metrics counterpart
of `Exporter.ExportTrace`. It lives in the separate module, so the core stays
dependency-free.

```go
import "github.com/aita/sqlkit/querytrace/otelquery"

trace := querytrace.New("GET /users/:id")
defer otelquery.ExportMetrics(ctx, meterProvider.Meter("querytrace"), trace)
// ... run the request's queries ...
```

(For finer control, `otelquery.NewMeterRecorder(ctx, meter)` returns a
`querytrace.MetricsRecorder` you can pass to `trace.RecordMetrics`.)

For a zero-dependency option, `querytrace.NewExpvarRecorder` publishes the same
metrics to an `expvar.Map`, exposed on the standard `/debug/vars` endpoint with
nothing but the standard library. Labels are folded into the variable name
(`querytrace.statements{kind=select,operation=GET /users}`); counters accumulate
as integers and durations as a running total in seconds (expvar has no
histogram). Publish one map and reuse the recorder across traces:

```go
var dbMetrics = querytrace.NewExpvarRecorder(expvar.NewMap("querytrace"))

// ... per request, after the work is done:
dbMetrics.Record(trace) // or trace.RecordMetrics(dbMetrics)
```

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

Each threshold- and policy-based check has its own option, all off by default, so
you opt into each one:

```go
cfg := querytrace.NewConfig(
    querytrace.WithParser(postgres.New()),
    querytrace.WithMaxQueries(50),      // too_many_queries
    querytrace.WithMaxParams(900),      // too_many_parameters
    querytrace.WithMaxInList(100),      // large_in_list
    querytrace.WithSelectStarWarning(), // select_star
    querytrace.WithTenantCheck(querytrace.Columns("tenant_id")),         // missing_tenant_predicate
    querytrace.WithSoftDeleteCheck(querytrace.Columns("deleted_at")),    // missing_soft_delete_predicate
)
```

The tenant and soft-delete checks are each configured by one option that takes
sub-options: `Columns` names the marker columns to look for, and `ScopedToSchema`
(below) limits the check to the tables that have them. They match
**case-insensitively** in the statement's top-level `WHERE`: any read or write
touching a base table whose `WHERE` references none of the tenant columns raises
`missing_tenant_predicate` (an `INSERT` is exempt — it carries its tenant value in
the row, not a filter), and any `SELECT` whose `WHERE` references none of the
soft-delete columns raises `missing_soft_delete_predicate`. Like `large_in_list`
and `select_star`, they read the parsed AST and stay silent when no parser is
wired.

Each `Columns` spec is a bare column or a table-qualified `table.column` (a
leading schema is ignored), and both halves accept glob wildcards (`*` `?`
`[..]`):

```go
querytrace.Columns("deleted_at")               // any table, column deleted_at
querytrace.Columns("deleted_at", "deactivated_at") // either marker
querytrace.Columns("*_at")                      // any column ending in _at
querytrace.Columns("orders.deleted_at")         // only the orders table
querytrace.Columns("tenant_*.tenant_id")        // tables matching tenant_*
```

A bare column governs every table; a table-qualified spec restricts the
requirement to the matching tables and leaves the rest unchecked — a second way to
scope the check alongside `ScopedToSchema`.

#### Scoping to a schema

A bare `Columns` spec governs **every** analyzed base table, which false-positives
on a table that has no tenant or soft-delete column at all — a lookup or reference
table (`countries`, `currencies`) that legitimately never filters on one. There
are two ways to scope a check to the right tables:

```go
// 1. By hand, with table-qualified specs: only the named tables are checked.
querytrace.WithSoftDeleteCheck(
    querytrace.Columns("users.deleted_at", "orders.deleted_at"),
)

// 2. From the resolved schema: a check applies to a table only if that table
// actually has a matching marker column, derived from the meta.Metadata your
// decl catalog builds (or the introspect produces). Reference tables are skipped
// automatically, and it stays correct as the schema evolves.
querytrace.WithSoftDeleteCheck(
    querytrace.Columns("deleted_at"),
    querytrace.ScopedToSchema(catalog), // meta.Metadata
)
```

Table and column names match by bare, case-insensitive name, so a schema-qualified
statement (`public.users`) lines up with a bare declaration. A statement that
touches several in-scope tables and misses the predicate on more than one raises a
warning per table, naming each in `Details["table"]`.

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
