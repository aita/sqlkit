# sqlkit documentation

Guides for [sqlkit](../README.md). The API reference for every package lives on
[pkg.go.dev](https://pkg.go.dev/github.com/aita/sqlkit); these pages are the
narrative, how-to side.

## Start here

- **[Getting started](getting-started.md)** — declare a schema, generate
  bindings, run typed queries, end to end.

## Core

- **[Query builder](query-builder.md)** — the typed DSL: querying & scanning,
  predicates, joins, subqueries, aggregates & window functions, set operations &
  CTEs, writes & upserts, relation-aware queries & preload, transactions,
  auto-splitting, and the standalone `ToSQL` builder.
- **[Schema as code & code generation](schema-codegen.md)** — declare the schema
  with [`decl`](https://pkg.go.dev/github.com/aita/sqlkit/decl), generate bindings
  and DDL, customize the type mapping, bootstrap from a live database.
- **[Migrations](migrations.md)** — DAG-ordered up/down migrations, the CLI, and
  autogeneration.

## Operating

- **[Query routing](routing.md)** — read/write splitting, sharding, and pinned
  sessions.
- **[Observability](observability.md)** — hooks,
  [`querytrace`](https://pkg.go.dev/github.com/aita/sqlkit/querytrace) tracing, and the
  [`analysis`](https://pkg.go.dev/github.com/aita/sqlkit/analysis) AST framework.
- **[Redaction](redaction.md)** — mask PII literals before logging a statement.
- **[Testing](testing.md)** — the
  [`sqltest`](https://pkg.go.dev/github.com/aita/sqlkit/sqltest) seeding helpers,
  query spy, and mock database.

## Reference

- **[Limitations](limitations.md)** — dialect support, engine-specific gaps, and
  the rules behind auto-splitting, preload, migrations, codegen, and the parser.

## Packages and modules

The root module `github.com/aita/sqlkit` is **dependency-free** (standard library
only). Features whose dependency trees you should not have to import are split
into their own modules:

| Module | Why separate |
| --- | --- |
| `github.com/aita/sqlkit` | The core: query builder, [`sql`](https://pkg.go.dev/github.com/aita/sqlkit/sql), `decl`, [`meta`](https://pkg.go.dev/github.com/aita/sqlkit/meta), [`migrate`](https://pkg.go.dev/github.com/aita/sqlkit/migrate), [`codegen`](https://pkg.go.dev/github.com/aita/sqlkit/codegen), [`introspection`](https://pkg.go.dev/github.com/aita/sqlkit/introspection), [`sqlparse`](https://pkg.go.dev/github.com/aita/sqlkit/sqlparse) (surface), `querytrace`, `analysis`, [`redaction`](https://pkg.go.dev/github.com/aita/sqlkit/redaction), `sqltest`, [`uow`](https://pkg.go.dev/github.com/aita/sqlkit/uow). No third-party dependencies. |
| `cmd/sqlkit` | The CLI links PostgreSQL/MySQL drivers for `inspect`. |
| `sqlparse/postgres` | The `pg_query_go` (cgo) PostgreSQL grammar. |
| `sqlparse/mysql` | The TiDB MySQL parser. |
| `querytrace/otelquery` | The OpenTelemetry SDK. |
| `examples` | The pgx driver the runnable examples link. |

Importing the core never pulls in a database driver, a cgo parser, or the
OpenTelemetry SDK.
