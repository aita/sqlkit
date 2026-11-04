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
- **Package-level tests use the package name**: a test that exercises the
  package as a whole (rather than one source file) is named
  `<package>_test.go` (e.g. `redaction/redaction_test.go`,
  `migrate/migrate_test.go`, `internal/tests/integration/integration_test.go`).
  Do **not** introduce purpose-suffixed names like `foo_e2e_test.go`.
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

- Root: `github.com/aita/sqlkit`. The core module consumers import.
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

## Code conventions

- **Builders are immutable values.** The typed query builders (`SelectQuery`,
  `InsertQuery`, `UpdateQuery`, `DeleteQuery`, `MergeQuery`) use **value
  receivers** and return the modified copy, so chains can safely branch
  (`a := q.Where(...); b := q.Limit(...)` don't interfere). New builder methods
  must keep this shape: `func (q SelectQuery) X(...) SelectQuery` — never mutate
  through a pointer receiver (see `select.go`, `insert.go`).
- **Dialect features are optional interfaces, discovered by type assertion.**
  The core `sql.Dialect` stays small; engine-specific capabilities (JSON,
  arrays, upsert, row locking, …) are separate interfaces the compiler probes
  with `if d, ok := c.Dialect.(ArrayDialect); ok { … }` (see `sql/compiler.go`).
  Add a new capability as its own interface rather than widening `Dialect` — and
  surface unsupported combinations as a descriptive **compile-time** (`ToSQL`)
  error naming the dialect and feature, not a runtime panic.
- **Sentinel errors are scope-prefixed package vars.** Declare them as
  `var ErrX = errors.New("<scope>: message")` where `<scope>` is the package
  (`sqlkit:`, `uow:`, `redaction:`, …); test identity with `errors.Is` and add
  context with `fmt.Errorf("...: %w", err)` (see `exec.go:113`,
  `uow/scope.go:50`).
- **Generated code is committed and never hand-edited.** Generated files begin
  with `// Code generated by sqlkit. DO NOT EDIT.` and are produced by
  `cmd/sqlkit` via `//go:generate` directives next to the catalog. After
  changing the generator or a catalog, run `go generate ./...` and commit the
  regenerated output; don't edit generated files by hand.
- **Don't break the public API; deprecate instead.** When superseding an
  exported API, keep the old symbol, add a `// Deprecated: use X instead.`
  comment, and have it delegate to the replacement (see `insert.go:95`,
  `sql/source.go:86`).

## Commit messages

- Use Conventional Commits with a scope:
  `feat(scope): ...`, `fix(scope): ...`, `build: ...`, `chore: ...`.
  The scope is the changed package (e.g. `fix(codegen)`,
  `feat(sqlparse/postgres)`, `redact: ...`).
- One concern per commit. Keep the summary imperative and concise.
- Write commit messages and pull request titles/descriptions in English.

## Pull requests

Bug fixes (especially from a bug-hunt round) follow a fixed shape. The full
methodology — how to find bugs, and the Severity / Mergeability rubrics — lives
in the `hunt-bug` skill (`.claude/skills/hunt-bug/`); the essentials for opening
a PR:

- **One fix per PR.** Small and self-contained, each with its own regression
  test, opened only after the full suite (`go test -race`, `vet`, `gofmt`,
  `golangci-lint`, and any affected leaf modules) is green. Prove the regression
  test fails without the production change.
- **Severity** — the PR body states the bug's impact if it persists:
  `critical` (silent wrong data or a security breach), `high` (crash or lost
  integrity guarantee), `medium` (a loud, contained failure — invalid SQL the
  engine rejects, non-determinism), or `low` (no production-visible wrong
  behavior). Weight it by reachability (common path vs. exotic construct).
- **Mergeability** — the first line of the PR body declares
  `Mergeability: auto-merge-safe` or `Mergeability: needs review — <reason>`.
  Auto-merge-safe means the change only corrects already-broken output (no
  behavior change for valid input, no new error/rejection), changes no public
  API / documented limitation / encoded form (checksum, fingerprint), is
  self-contained with no embedded-output regeneration, follows an established
  pattern, and is not in a security/integrity-critical pass (redaction, routing,
  transactions). Anything else is needs-review; default to needs-review when
  unsure. Only enable auto-merge for an auto-merge-safe PR.
- **Labels** — apply one kind (`bug` / `enhancement` / `documentation`), one
  severity label for bug fixes (`severity: critical|high|medium|low`), and one
  mergeability label (`auto-merge-safe` / `needs-review`); features and docs
  default to `needs-review`.

## Documentation

- When behavior changes, update the relevant topic under `docs/`
  (`query-builder.md`, `migrations.md`, `redaction.md`, `routing.md`,
  `observability.md`, `schema-codegen.md`, `testing.md`, etc.).
- For changes involving code generation (`go:generate` / `cmd/sqlkit`),
  regenerate and commit the generated output.
