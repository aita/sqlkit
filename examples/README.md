# Examples

Runnable programs that each demonstrate one slice of sqlkit. They live in their
own Go module (`github.com/aita/sqlkit/examples`) so the PostgreSQL driver the
database-backed examples link does not reach the root module that library
consumers import.

Most examples expect a PostgreSQL database and read its connection string from
`DATABASE_URL`; `minimal` and `custom` need no database, and `querytrace` is
a separate module that also runs against PostgreSQL.

| Example | What it shows | Needs a database |
| --- | --- | --- |
| [`minimal`](minimal) | The smallest typed query, against a hand-written binding | no |
| [`generated`](generated) | The end-to-end flow: declare a schema, generate bindings with the CLI, run typed queries | yes (PostgreSQL) |
| [`custom`](custom) | Driving the `codegen` package as a library to customize the SQL→Go type mapping and DTO naming | no |
| [`migrate`](migrate) | Shipping migrations as your own standalone binary | yes (PostgreSQL) |
| [`redaction`](redact) | Masking sensitive literals in SQL, driven by column comments | yes (PostgreSQL) |
| [`querytrace`](querytrace) | Catching bad query patterns (N+1, missing WHERE, …) at runtime | yes (PostgreSQL) |

## minimal

The shortest path to a typed query. It declares one table binding **by hand**
(no code generation) and runs a `SELECT` through the query builder, so you can
read the whole thing in one screen. Start here, then see `generated` for how a
real project produces those bindings from a schema.

```sh
go run ./minimal
```

## generated

The complete, idiomatic workflow and the program the
[getting-started guide](../docs/getting-started.md) mirrors:

1. The schema is declared once as a `decl.Catalog` in [`generated/schema`](generated/schema) —
   the single source of truth.
2. `go:generate` directives regenerate the typed `blogdb` bindings **and**
   `schema.sql` from that catalog using the `sqlkit` CLI (`go generate ./...`).
3. `main.go` runs typed queries against the generated bindings: inserts with
   `RETURNING`, joins, aggregates, window functions, and relation-aware reads
   (`JoinRel`), scanning into caller-defined DTOs.

```sh
createdb blog && psql blog -f generated/schema.sql
DATABASE_URL=postgres://localhost/blog go run ./generated
```

This is the CLI-driven path. For generation that needs more control than the CLI
exposes, see `custom`.

## custom

The same goal as `generated` — produce typed bindings from a schema — but
driving the [`codegen`](../codegen) package **as a library** from a small Go
program instead of the `sqlkit generate` CLI. Calling the library directly buys
you the things the CLI can't express: full control over the SQL→Go type mapping
(`codegen.TypeMapper`) and the DTO naming scheme (`Options.DTOName`).

It runs the **same blog schema** as `generated` (declared as a `decl.Catalog` in
its own [`custom/schema`](custom/schema), with a few extra richly-typed columns),
so you can compare the output: where the CLI maps a `uuid`/`numeric`/`jsonb`
column to `string`/`string`/`[]byte` and a `status` to `string`, the mapper here
maps them to the domain types declared by hand in [`custom/blogdb`](custom/blogdb)
(`UUID`, `Decimal`, `json.RawMessage`, `UserStatus`) and gives every DTO a
`Record` suffix. Same schema in, different bindings out. The committed output
lives under `custom/blogdb`.

```sh
go generate ./custom   # regenerate blogdb from the in-code catalog
```

## migrate

How to ship migrations as **your own binary** rather than running them through
the `sqlkit` command. The migrations register themselves with a registry; the
program builds a `Migrator` from it and drives `migrate.NewCLI()` with kong,
exposing the same `up`/`down`/`status`/`new`/… commands the `sqlkit` binary has.
See [docs/migrations.md](../docs/migrations.md) for the surrounding guide.

```sh
createdb app
DATABASE_URL=postgres://localhost/app go run ./migrate up
DATABASE_URL=postgres://localhost/app go run ./migrate status
```

## redact

Masking sensitive string literals in SQL with the [`redaction`](../redact) package.
It applies a schema, introspects it, and treats **the database as the source of
truth** for what is sensitive: every column whose comment contains `pii` is
masked. See [docs/redaction.md](../docs/redaction.md).

```sh
createdb redact_demo
DATABASE_URL=postgres://localhost/redact_demo go run ./redact
```

## querytrace

A web server that shows what the [`querytrace`](../querytrace)
package catches. Each endpoint deliberately runs a different bad pattern — an
N+1, a missing `WHERE`, an unexpected write, a blown query budget, a leaked
cursor, an oversized `IN` list — and renders the request's trace (warnings +
timeline) as an HTML page. It runs against a real PostgreSQL database (wrapped
by the querytrace driver) and (re)creates its own small schema on startup. It is
its **own module** (it has its own `go.mod`), so run it from inside the
directory:

```sh
createdb querytrace_demo
cd querytrace && DATABASE_URL=postgres://localhost/querytrace_demo go run .
# open http://localhost:8080/
```
