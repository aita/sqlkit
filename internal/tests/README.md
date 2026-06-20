# internal/tests — sqlkit's cross-module test suites

Most of sqlkit's tests live next to the code they cover and run with
`go test ./...` on the root module. This directory holds the test suites that
**cannot** live there because they need heavy dependencies the root module
deliberately keeps out (so library consumers never inherit them).

Each suite is its **own Go module** — the same isolation pattern the rest of the
repo uses (`cmd/sqlkit`, `examples`, `sqlparse/*`, `querytrace/otelquery`). They
are grouped here so the repo's test suites are discoverable in one place.

## The suites

| Suite | Verifies | Needs a database? | Heavy deps | CI job |
|---|---|---|---|---|
| [`roundtrip`](roundtrip) | The compiler never emits SQL its own grammar rejects, and rendering is stable (a **static / syntactic** oracle). | No | the two SQL **parsers** (`pg_query_go` cgo, TiDB parser) | unit-test |
| [`integration`](integration) | The full stack — builder, scanner, DDL, introspection, migrations — behaves correctly when **executed** against real engines (a **dynamic / behavioral** oracle). | Yes (real PostgreSQL + MySQL) | `testcontainers`, `pgx`, the MySQL driver | integration |

## How they relate

They are **complementary halves** of the same goal — "sqlkit produces correct
SQL" — checked at two different points:

```
builder ──ToSQL──▶ SQL text ──┬──▶ roundtrip:    does a real parser accept it, and is it stable?   (no DB)
                              └──▶ integration:  does a real engine execute it and return the right rows?  (real DB)
```

- `roundtrip` is fast, deterministic, and needs no infrastructure, so it runs on
  every push in the unit-test job and catches *syntactic* faults (invalid SQL,
  unstable rendering) close to the change.
- `integration` is slower and needs live engines, so it runs in its own job and
  catches *semantic* faults a parser cannot see (constraint violations, wrong
  results, dialect-execution differences).

A fault that slips past one is usually caught by the other: `roundtrip` proves
the SQL is *well-formed*, `integration` proves it is *correct when run*.

## Why separate modules (not one `internal/tests` module)

Their heavy dependencies are disjoint — `roundtrip` pulls the SQL parsers,
`integration` pulls testcontainers and the drivers — and they run in different CI
jobs (one needs no database, the other needs two). Merging them into a single
module would couple both dependency trees together and force the no-database
suite to drag in the database tooling. Keeping a module per suite preserves the
repo's dependency-isolation rule and lets each run independently.
