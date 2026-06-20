# sqlkit

[![Go Reference](https://pkg.go.dev/badge/github.com/aita/sqlkit.svg)](https://pkg.go.dev/github.com/aita/sqlkit)

A typed SQL toolkit for Go, built on a single **SQL DSL** — you write SQL as Go,
and the compiler checks it. One notation and one compiler cover both queries
(DML) and schema changes (DDL), and the schema you declare in Go feeds everything
else: the typed query bindings, the migrations, and the generated DDL.

```go
// Typed columns carry the predicate vocabulary; the ON clause and the
// projections are typed columns too, and rows scan into your own struct.
var rows []struct {
    Title  string
    Author string
}
err := db.Select(appdb.Posts.Title, sql.As(appdb.Users.Name, "author")).
    From(appdb.Posts).
    Join(appdb.Users, appdb.Posts.UserID.EqExpr(appdb.Users.ID)).
    Where(
        appdb.Posts.Published.Eq(true),
        sqlkit.Like(appdb.Users.Email, "%@example.com"),
        sqlkit.Exists(
            sqlkit.Select(appdb.Comments.ID).From(appdb.Comments).
                Where(appdb.Comments.PostID.EqExpr(appdb.Posts.ID)),
        ),
    ).
    OrderBy(appdb.Posts.CreatedAt.Desc()).
    Limit(20).
    All(ctx).Scan(&rows)
```

The typed query builder is the core. Everything else — schema-as-code, codegen,
migrations, routing, tracing, redaction, testing — builds on it and is opt-in.

## Overview

The narrative guides live in [`docs/`](docs/); the per-package API reference is on
[pkg.go.dev](https://pkg.go.dev/github.com/aita/sqlkit).

- **[Getting started](docs/getting-started.md)** — schema → bindings → typed
  queries, end to end.
- **[Query builder](docs/query-builder.md)** — the typed DSL in full: joins,
  subqueries, CTEs, window functions, upserts, `RETURNING`, preload, transactions,
  and the standalone `ToSQL` builder.
- **[Schema as code & code generation](docs/schema-codegen.md)** — declare the
  schema with `decl`, generate bindings and DDL, customize the type mapping,
  bootstrap from a live database.
- **[Migrations](docs/migrations.md)** — DAG-ordered up/down migrations, a CLI,
  and Alembic-style autogeneration.
- **[Query routing](docs/routing.md)** — read/write splitting, sharding, pinned
  sessions.
- **[Observability](docs/observability.md)** — hooks, `querytrace` tracing, and
  the `analyze` AST framework.
- **[Redaction](docs/redaction.md)** — mask PII literals before logging a
  statement.
- **[Testing](docs/testing.md)** — the `sqltest` seeding helpers, query spy, and
  mock database.
- **[Limitations](docs/limitations.md)** — dialect support and engine-specific
  gaps.

## Multiple databases

The same query builder, schema, and migrations target both **PostgreSQL** and
**MySQL**; PostgreSQL is the default and the most exercised dialect. `sql.Dialect`
is the single point where engine differences are resolved — identifier quoting,
placeholders, capability flags, and DDL rendering. Engine-specific gaps surface
at compile time (e.g. MySQL has no `RETURNING`); see
[Limitations](docs/limitations.md) for the full matrix.

## License

Released under the [MIT License](LICENSE).
