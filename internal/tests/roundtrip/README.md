# roundtrip — the parser round-trip oracle

This module proves a production-readiness invariant for sqlkit's SQL compiler:

> **The compiler never emits SQL its own grammar rejects, and its output is
> stable.**

It is the L1/L2 layer of the production-readiness plan. Concretely, for every
statement in the SQL corpus under [`testdata/`](testdata) it runs a two-pass
parse/render cycle against the real PostgreSQL and MySQL grammars:

```
text ──Parse──▶ q1 ──ToSQL──▶ text1 ──Parse──▶ q2 ──ToSQL──▶ text2
```

- **L1 — no invalid SQL.** `ToSQL` on a parseable statement never returns an
  error, and the SQL it produces (`text1`) parses again. A statement that parsed
  as a typed query must not degrade into a verbatim raw passthrough on re-parse.
- **L2 — fixpoint.** Rendering the re-parsed query yields `text2 == text1`, so the
  output is stable under a further round-trip.

It compares the two *generated* renderings (`text1` vs `text2`), never the
original source against a rendering, so it is free of formatting-noise false
negatives — it asserts the compiler is self-consistent, not that it preserves the
author's whitespace or literal style.

## Why a separate module

The oracle needs the builder/compiler (the root module) and **both** parser
frontends — `sqlparse/postgres` (pg_query_go, cgo) and `sqlparse/mysql` (the TiDB
parser) — together in one test. Those parser dependencies are deliberately kept
out of the root module that library consumers import, so the only place they can
meet the builder is a dedicated module like this one. It needs no database, so
its tests run in the unit-test CI job.

This is the **static / syntactic** half of sqlkit's cross-module testing; the
**dynamic / behavioral** half (`integration`) runs the same SQL against real
engines. See [`../README.md`](../README.md) for how the two relate.

## The corpus

`testdata/` holds plain-text `.sql` files, one statement per `;`-terminated
fragment (`--` line comments and blank lines are ignored):

- `testdata/common/` — portable statements, exercised by **both** frontends.
- `testdata/postgres/` — PostgreSQL-only syntax (e.g. `RETURNING`, `ILIKE`,
  `ON CONFLICT`, `FOR UPDATE`).
- `testdata/mysql/` — MySQL-only syntax (e.g. `ON DUPLICATE KEY UPDATE`).

### Growing it

The corpus is meant to grow — that is how coverage of the "no invalid SQL"
guarantee widens. To add cases, drop statements into the appropriate file (or a
new `.sql` file). The plain-text format is intentional so that, in a later step,
statements can be imported in bulk from external suites. Every fixed compiler bug
should leave a regression statement here.

## Running

```sh
cd internal/roundtrip
go test ./...
```
