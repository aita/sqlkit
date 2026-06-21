# sqlkit

[![Go Reference](https://pkg.go.dev/badge/github.com/aita/sqlkit.svg)](https://pkg.go.dev/github.com/aita/sqlkit)

A SQL toolkit for Go — a Swiss-army knife for the whole life of a SQL statement.
Build it as typed Go, hold it as an AST, compile it to text, parse text back into
that same AST, then analyze, redact, route, migrate, trace, and test around it.
One representation runs through the middle, so the blades compose.

Two features sit at the center, and everything else builds on them:

- **SQL DSL** — a typed query builder. Typed columns carry the predicate
  vocabulary, the `ON` clause and the projections are typed columns too, and rows
  scan into your own struct, so the compiler checks the SQL as Go. One notation
  covers both queries (DML) and schema changes (DDL).
- **SQL AST** — the node tree the DSL builds and the compiler renders to text.
  It is untyped, and it is the representation the rest of the kit speaks: the
  parser produces it from raw SQL, and the analysis and redaction passes read and
  rewrite it. Modeled on `go/ast`, with a `Walk`/`Visitor` traversal.

Everything else is opt-in, and much of it works without the typed builder at all.
**querytrace** is standalone `database/sql` tracing — it observes hand-written
SQL, an ORM, or any other builder just as well as sqlkit's, and the **sqltest**
query spy is built on it — while **sqlparse**, **analysis**, and **redaction** operate
on any SQL once it is in the AST, whoever wrote it. Schema-as-code and codegen,
migrations, and routing are the parts that lean on the builder and the declared
schema.

## Architecture

The DSL and the parser are two front ends that produce the same AST; the
compiler and the AST passes are the back ends that consume it. One representation
in the middle is what lets the blades compose.

```
+----------------------------------------------------------------------+
| Your application -- typed queries, schema as Go                      |
+----------------------------------------------------------------------+
   |
   v
+----------------------------------------------------------------------+
| Front ends  ->  build the AST                                        |
|   - Typed DSL   query builder + generated bindings                   |
|                 (sqlkit, decl, codegen)                              |
|   - Parser      raw SQL text -> AST                                  |
|                 (sqlparse: Postgres / MySQL)                         |
+----------------------------------------------------------------------+
   |
   v
+----------------------------------------------------------------------+
| SQL AST + compiler   (sql)                                           |
|   builders -> AST -> SQL text + args   +   Walk / Visitor            |
+----------------------------------------------------------------------+
   |
   v
+----------------------------------------------------------------------+
| AST passes  ->  read / rewrite the AST                               |
|   analysis (structural facts)   +   redaction (mask PII)             |
+----------------------------------------------------------------------+
   |
   v
+----------------------------------------------------------------------+
| Runtime  ->  execute                                                 |
|   db, session, tx, hooks, routing, retry, struct scan                |
+----------------------------------------------------------------------+

Alongside (no builder needed): migrations (migrate), tracing (querytrace),
testing (sqltest). querytrace wraps any database/sql driver, so it -- and
the sqltest spy built on it -- trace SQL whether sqlkit emitted it or not.
```

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
- **[Parser](https://pkg.go.dev/github.com/aita/sqlkit/sqlparse)** — `sqlparse`
  turns raw SQL text back into the AST (the inverse of build-then-compile), with
  PostgreSQL and MySQL frontends, so the analysis and redaction passes work on
  hand-written SQL too.
- **[Observability](docs/observability.md)** — hooks, `querytrace` tracing, and
  the `analysis` AST framework.
- **[Redaction](docs/redaction.md)** — mask PII literals before logging a
  statement.
- **[Query routing](docs/routing.md)** — read/write splitting, sharding, pinned
  sessions.
- **[Testing](docs/testing.md)** — the `sqltest` seeding helpers, query spy, and
  mock database.
- **[Limitations](docs/limitations.md)** — dialect support and engine-specific
  gaps.

A taste of the DSL — a join with a correlated subquery, scanning into a local
struct:

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

## Multiple databases

The same query builder, schema, and migrations target both **PostgreSQL** and
**MySQL**; PostgreSQL is the default and the most exercised dialect. `sql.Dialect`
is the single point where engine differences are resolved — identifier quoting,
placeholders, capability flags, and DDL rendering. Engine-specific gaps surface
at compile time (e.g. MySQL has no `RETURNING`); see
[Limitations](docs/limitations.md) for the full matrix.

## License

Released under the [MIT License](LICENSE).
