# Validation & fuzz reference

sqlkit is **several Go modules**. `go test ./...` on the root does NOT reach the
others — every fix must be validated across every module that could be affected,
exactly as CI (`.github/workflows/ci.yml`) does.

## Modules

| Directory | Notes |
|---|---|
| `.` (root) | builder, `sql` compiler, redaction, analysis, querytrace, sqlparse root |
| `cmd/sqlkit` | CLI; separate so drivers stay out of the root module |
| `sqlparse/postgres` | go-pgquery (pure-Go WASM) PostgreSQL frontend |
| `sqlparse/mysql` | TiDB-parser MySQL frontend |
| `querytrace/otelquery` | OpenTelemetry exporter |
| `internal/tests/roundtrip` | the round-trip + semantic oracles (needs both parsers) |
| `internal/tests/integration` | real PostgreSQL + MySQL; build/vet always, tests need DBs |
| `examples`, `examples/querytrace` | example programs (must still compile) |

## Per-module gate (run in each affected module's directory)

```sh
test -z "$(gofmt -l .)"   # formatting
go build ./...
go vet ./...
go test -race ./...
```

Run the root first, then each affected leaf module by `cd`-ing into it. A change
to `sql`/`sqlparse` almost always means re-running `internal/tests/roundtrip`.

## Lint (golangci-lint v2.5.0 — the version CI pins)

```sh
# install once
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/v2.5.0/install.sh \
  | sh -s -- -b "$(go env GOPATH)/bin" v2.5.0
# then, per module (nearest .golangci.yml applies)
golangci-lint run ./...
```

CI lints: `. cmd/sqlkit examples examples/querytrace internal/tests/integration
internal/tests/roundtrip querytrace/otelquery sqlparse/postgres sqlparse/mysql`.

## Fuzzing

Fuzz targets and the module to run them in:

| Target | Module / dir |
|---|---|
| `FuzzRoundTrip` | `internal/tests/roundtrip` |
| `FuzzSemanticEquivalence` | `internal/tests/roundtrip` |
| `FuzzRedactParsedNoLeak`, `FuzzRedactRobust`, `FuzzRedactRawParser` | `internal/tests/roundtrip` |
| `FuzzRedactNoLeak` | `.` (`redaction`) |
| `FuzzFingerprintRaw`, `FuzzFingerprintParsed` | `.` (`analysis`) |
| `FuzzParseCursor`, `FuzzPaginateCursor` | `.` (paginator) |
| `FuzzSnakeCase` | `.` (`internal/reflectmap`) |

Run a target for a real amount of time (CI-clean reference: ~240s):

```sh
cd internal/tests/roundtrip
go test -run='^$' -fuzz='^FuzzRoundTrip$' -fuzztime=240s ./...
```

A crash writes the input under `testdata/fuzz/<Target>/`. **Keep that file** —
it becomes a permanent regression seed. After a fix, re-run the target to
confirm it no longer crashes.

## Real-engine differential (optional but high value)

The integration suite connects via DSN env vars (no Docker socket needed):

```sh
cd internal/tests/integration
SQLKIT_TEST_POSTGRES_DSN='postgres://...?sslmode=disable' \
SQLKIT_TEST_MYSQL_DSN='user:pass@tcp(127.0.0.1:3306)/public?parseTime=true&tls=skip-verify' \
go test -v ./...
```

A local PostgreSQL alone is enough for a corpus differential check; render each
corpus statement and confirm the real engine accepts sqlkit's output.

## Benchmarks (don't regress the hot paths)

```sh
go test -run='^$' -bench=. -benchmem ./...        # root
# also: sqlparse/postgres, sqlparse/mysql
```

CI's bench job is a hard gate only on "must still compile and run"; the latency
comparison is informational. Don't ship an obvious latency/alloc regression.
