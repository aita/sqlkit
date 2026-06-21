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

## Scope: what `roundtrip` is — and is not — for

`roundtrip` overlaps, on the surface, with the parser frontends' own tests
(`sqlparse/postgres`, `sqlparse/mysql`), which already parse SQL and assert the
exact AST and the exact `ToSQL` output. To keep responsibilities clear:

- **Per-statement parser correctness** (this text parses to this AST and renders
  to this exact SQL) belongs to the `sqlparse/*` tests. They are the precise,
  authoritative round-trip checks for each construct.
- **Compiler (`ToSQL`) coverage** belongs to the **builder-driven unit tests** in
  the root `sql` / `sqlkit` packages. Building queries with the fluent API and
  rendering them is the production path, and it reaches branches no parsed corpus
  can — so it carries the bulk of the compiler's statement coverage.
- **`roundtrip` is a property + fuzz oracle, not a coverage corpus.** Its job is
  to assert one invariant broadly and across *both* dialects — the compiler never
  emits SQL its own grammar rejects, and rendering is a fixpoint — and to seed
  `FuzzRoundTrip`. It earns its keep by *catching specific bugs* (e.g. a bare
  `SELECT` that parses but fails `ToSQL`, and a MySQL `ORDER BY … NULLS …` that
  renders invalid SQL), not by maximizing a coverage number.

Do **not** grow the `testdata/` corpus to chase compiler coverage: it tops out
fast and has a structural ceiling. The corpus is text→parse→`ToSQL`, so it can
only reach what the parser can model — it is DML-only (so it covers none of the
DDL compiler), and constructs the parser can't model fall back to a raw
passthrough that skips the typed renderer entirely. Measured: expanding it by
~110 statements moved `sql`-package coverage only ~19.5% → ~24%, while the
builder-driven unit tests already sit above 80%. Add a builder unit test (or, for
arbitrary inputs, a fuzz seed) instead.

## Why separate modules (not one `internal/tests` module)

Their heavy dependencies are disjoint — `roundtrip` pulls the SQL parsers,
`integration` pulls testcontainers and the drivers — and they run in different CI
jobs (one needs no database, the other needs two). Merging them into a single
module would couple both dependency trees together and force the no-database
suite to drag in the database tooling. Keeping a module per suite preserves the
repo's dependency-isolation rule and lets each run independently.
