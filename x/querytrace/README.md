# querytrace (experimental)

`querytrace` is an application-level SQL tracing middleware for `database/sql`.
It records the queries a unit of work — a request, a job, a usecase — issues,
together with the application context around them, and surfaces problems that
database-side statistics cannot see.

It deliberately does **not** do execution-plan analysis, index/buffer/lock
inspection, or per-query global latency percentiles. Those belong to the
database and to OpenTelemetry/APM. `querytrace` answers a different question:

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
- **testkit/** — assertions over a `Trace` (`AssertMaxQueries`, `AssertNoWrites`,
  `AssertNoNPlusOne`, …).
- **slog/** — a summary exporter to `log/slog`.

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

Create a trace per unit of work and put it in the context:

```go
trace := querytrace.New("GET /users/:id")
ctx := querytrace.WithTrace(r.Context(), trace)

// ... run the handler; every QueryContext/ExecContext under ctx is recorded ...

fmt.Println(querytrace.TextReport(trace))
```

### In tests

```go
func TestUserPageQueryCount(t *testing.T) {
    trace := querytrace.New("UserPage")
    ctx := querytrace.WithTrace(context.Background(), trace)

    if err := service.RenderUserPage(ctx, userID); err != nil {
        t.Fatal(err)
    }

    testkit.AssertMaxQueries(t, trace, 5)
    testkit.AssertNoNPlusOne(t, trace)
}

func TestCacheHitDoesNotAccessDB(t *testing.T) {
    trace := querytrace.New("CacheHit")
    ctx := querytrace.ForbidQueries(querytrace.WithTrace(context.Background(), trace))

    if _, err := service.GetUser(ctx, id); err != nil {
        t.Fatal(err)
    }
    testkit.AssertWarning(t, trace, querytrace.WarnQueryForbidden)
}
```

## Configuration

`Config` controls what is captured. `DebugConfig(dialect)` is the recommended
starting point for development and tests; it captures raw SQL, argument *types* (never
values), and callers, and enables analysis, fingerprinting, and N+1 detection.
Production setups should capture less — leave raw SQL off and capture callers
only on error or slow queries.

Defaults lean safe: argument **values** are never captured, and raw SQL is off
unless you turn it on.

## Status / not yet here

Out of scope for this first cut: OpenTelemetry export, an `net/http` middleware,
query-builder integration (`.Name()` / `.ExpectRows()`), strict transaction-to-
query linking, sampling, and the more advanced analyzer warnings (tenant/soft-
delete predicates, `SELECT *`, large `IN`). Only PostgreSQL has a parser today;
other dialects use the string-analysis fallback.
