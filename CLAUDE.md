# CLAUDE.md

Conventions for working in this repository. Collected here so the same
instructions don't have to be repeated every session.

## Test file layout

- **One source, one test file**: pair `foo.go` with `foo_test.go`. Keep this
  mapping as the default; don't pile unrelated tests into one file.
- **Split by feature/concern**: once a test file grows large, split it by
  feature (concern) into `foo_<topic>_test.go`. Don't cram everything into a
  single file.
  - Example: `querytrace/warning_positive_test.go`,
    `querytrace/warning_derive_test.go`,
    `querytrace/warning_transaction_test.go` split the `warning` feature by
    concern.
- **Fuzz tests in their own file**: keep fuzz tests separate from regular
  tests, in `foo_fuzz_test.go` (e.g. `paginator_fuzz_test.go`,
  `internal/reflectmap/snakecase_fuzz_test.go`).
- **Integration tests separate, in their own package**: integration and
  round-trip tests live under `internal/tests/` (e.g.
  `internal/tests/integration/`, `internal/tests/roundtrip/`). Don't mix them
  into the same directory as unit tests. See the section below for which suite
  a new test belongs in.

## Cross-module test suites (`internal/tests/`)

Most tests live next to the code they cover and run with `go test ./...` on the
root module. `internal/tests/` holds the suites that **can't** live there
because they need heavy dependencies the root module deliberately keeps out.
Each suite is its **own Go module** (same isolation rule as `sqlparse/*`,
`querytrace/otelquery`, etc.), so a change touching them must be built/tested in
that module's directory. Both suites have a `README.md`; read it before adding
cases.

- **`internal/tests/roundtrip`** — the **static / syntactic** oracle (no
  database). Asserts one invariant broadly across both dialects: the compiler
  never emits SQL its own grammar rejects, and rendering is a fixpoint. It needs
  the builder plus **both** parser frontends (`pg_query_go` cgo + TiDB), so it
  must be its own module; it runs in the unit-test CI job.
  - It is a **property + fuzz oracle, not a coverage corpus.** Do **not** grow
    `testdata/` to chase `ToSQL` coverage — add a builder-driven unit test in the
    root `sql`/`sqlkit` packages (or a fuzz seed) instead. Every fixed compiler
    bug should still leave a regression statement in `testdata/`.

- **`internal/tests/integration`** — the **dynamic / behavioral** oracle. Runs
  the full stack (builder, scanner, DDL, introspection, migrations) against real
  PostgreSQL + MySQL via testcontainers/drivers. It needs live engines, so it
  runs in its own CI job and is **skipped** (not failed) when a backend is
  unavailable — gate new tests through the existing `connect`/`setup` harness so
  they skip cleanly rather than hard-failing locally.

Rule of thumb for a new cross-module test: a *syntactic* check (is the generated
SQL well-formed / stable, parser-only) → `roundtrip`; a *semantic* check (does a
real engine execute it and return the right rows) → `integration`. Keep the two
modules separate — their heavy deps are disjoint and they run in different CI
jobs; don't merge them.

## Build, test & lint commands

This repo is several Go modules. In the root module, use:

```sh
go build ./...
go test -race ./...
go vet ./...
gofmt -l .            # must print nothing (detects unformatted files)
golangci-lint run ./...
```

- Before submitting, at minimum pass `gofmt`, `go vet`, and
  `go test -race ./...`.
- `examples/` is held to the stricter `examples/.golangci.yml`.

## Module layout & dependency policy

- Root: `github.com/aita/sqlkit` (go 1.25). The core module consumers import.
- **Keep the root module lightweight and cgo-free.** Isolate heavy or
  cgo-bearing dependencies in their own leaf modules so they never leak into
  the root.
  - `sqlparse/postgres`: separate module because it uses pg_query_go (cgo).
  - `sqlparse/mysql`: separate module because it uses the TiDB parser.
  - `querytrace/otelquery`: separate module to keep the OpenTelemetry
    dependency out of the root.
  - `internal/tests/roundtrip`: round-trip oracle that needs the builder plus
    both parser frontends; a separate module because it carries their
    cgo/parser deps.
- When adding a new heavyweight dependency, prefer a separate module over
  adding it to the root.
- For cross-package changes, also `go build/vet/test` the affected leaf
  module(s) individually (CI runs each module as its own job).

## Commit messages

- Use Conventional Commits with a scope:
  `feat(scope): ...`, `fix(scope): ...`, `build: ...`, `chore: ...`.
  The scope is the changed package (e.g. `fix(codegen)`,
  `feat(sqlparse/postgres)`, `redact: ...`).
- One concern per commit. Keep the summary imperative and concise.

## Documentation

- When behavior changes, update the relevant topic under `docs/`
  (`query-builder.md`, `migrations.md`, `redaction.md`, `routing.md`,
  `observability.md`, `schema-codegen.md`, `testing.md`, etc.).
- For changes involving code generation (`go:generate` / `cmd/sqlkit`),
  regenerate and commit the generated output.
