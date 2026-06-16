# querytrace (experimental)

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

This package is experimental and lives under `x/` with its own module.

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
- **slog/** — a summary exporter to `log/slog`.
- **otel/** — an exporter to OpenTelemetry: one operation-level span per trace
  with a child DB span per statement. It is a separate module so the core stays
  dependency-free.

SQL analysis (operation, tables, `WHERE`/`JOIN` presence) and fingerprinting are
best-effort through a `sqlparse.Parser`. `DebugConfig` wires one automatically
from its dialect name, provided the matching frontend is blank-imported (for
example `github.com/aita/sqlkit/x/sqlparse/postgres`); you can also set
`Config.Parser` explicitly to override it. A parse failure or a missing parser
falls back to lightweight string analysis and **never** aborts execution. The
parser produces a `sql.Query`, which `sql.Describe` introspects for the analysis
and `ToSQL` recompiles into a normalized fingerprint.

## Usage

Wrap a connector and open a `database/sql` handle over it:

```go
import (
    "database/sql"

    querytrace "github.com/aita/sqlkit/x/querytrace"
    qtdriver "github.com/aita/sqlkit/x/querytrace/driver"

    // Blank-imported so its PostgreSQL parser registers itself; DebugConfig
    // then wires it from the "postgres" dialect name.
    _ "github.com/aita/sqlkit/x/sqlparse/postgres"
)

// Pick the dialect by name; the matching parser is wired automatically.
cfg := querytrace.DebugConfig("postgres")

db := sql.OpenDB(qtdriver.WrapConnector(connector, cfg))
```

To override the parser (for example a custom one), set `cfg.Parser` after
constructing the config.

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

It is a separate Go module (`github.com/aita/sqlkit/x/querytrace/otel`) so the
core querytrace module carries no OpenTelemetry dependency.

```go
import oteltrace "github.com/aita/sqlkit/x/querytrace/otel"

exporter := oteltrace.New(tp.Tracer("querytrace"), oteltrace.WithDBSystem("postgresql"))

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
exporter := oteltrace.New(tp.Tracer("querytrace"), oteltrace.WithDBSystem("postgresql"))

mux := http.NewServeMux()
mux.Handle("GET /users/{id}", exporter.Handler(usersHandler))
```

The operation is named after the routed pattern (`GET /users/{id}`), so it stays
low cardinality; pass `WithOperationNamer` to override. Place the middleware
inside any HTTP-level instrumentation (such as `otelhttp`) and the operation span
nests under the HTTP server span, joining querytrace's view to the request trace.

## Configuration

`Config` controls what is captured. `DebugConfig(dialect)` is the recommended
starting point for development and tests; it captures raw SQL, argument *types* (never
values), and callers, and enables analysis, fingerprinting, and N+1 detection.
Production setups should capture less — leave raw SQL off and capture callers
only on error or slow queries.

Defaults lean safe: argument **values** are never captured, and raw SQL is off
unless you turn it on.

## Status / not yet here

Out of scope for this first cut: strict transaction-to-query linking, sampling,
and the more advanced analyzer warnings (tenant/soft-delete predicates,
`SELECT *`, large `IN`). Only PostgreSQL has a
parser today; other dialects use the string-analysis fallback.
