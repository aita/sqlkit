# sqlkit

A typed SQL toolkit for Go:

- **Typed query builder** — table bindings expose `Column[T]` fields, so
  predicates, assignments, and join conditions are checked by the Go compiler.
  `Users.ID.Eq("foo")` is a compile error, not a runtime SQL error.
- **Results into your own structs** — build a typed query, then `Scan` the
  result into any destination you define (`sqlkit` tag / snake_case mapping,
  `sql.Scanner` support). The query shape and the DTO are decoupled.
- **Full SQL surface** — joins, GROUP BY / HAVING, ORDER BY, DISTINCT,
  UNION / INTERSECT / EXCEPT, aggregates, CASE / IF, and window functions
  (`OVER (PARTITION BY ...)`).
- **Code generation from PostgreSQL** — `sqlkitgen` introspects a live schema
  (`information_schema`) and generates the row structs and typed bindings.
- **Standalone builder** — every chain also compiles unbound with
  `ToSQL(dialect)` to SQL text + args, ready for pgx, sqlx, or `database/sql`;
  `ScanRows` applies the same struct mapping to results another library
  executed. See [Standalone query building](#standalone-query-building-pgx-sqlx-databasesql-).

Chains are bound to the executor they start from — a `Database` (auto-commit)
or a transaction-scoped `Session` — so terminals take only a context:

```go
var users []MyUserDTO
err := db.Select().From(appdb.Users).
    Where(appdb.Users.ID.Gt(10)).
    All(ctx).Scan(&users)
```

## Layout

| Package | Purpose |
|---|---|
| `sqlkit` | Typed query chains, `Database`, `Session`, result scanning, streaming `Records[T]` iteration (module root) |
| `sql` | SQL AST, dialects, compiler, and all statement generation |
| `sqldef` | In-code schema declarations: a reusable `Catalog` of tables that feeds the generators |
| `meta` | Database schema metadata model shared by the tooling (tables, columns, indexes, constraints, comments) |
| `inspector` | PostgreSQL/MySQL introspection into the `meta` model |
| `codegen` | `meta` → Go code generator (library: customizable types, layout, DTOs) |
| `ddl` | `meta` → SQL DDL generator (CREATE TABLE / INDEX, constraints, comments) |
| `cmd/sqlkitgen` | CLI wrapper around `inspector` + `codegen` + `ddl` |

## Generating table bindings

`sqlkitgen` introspects a live **PostgreSQL or MySQL** schema and generates
typed bindings:

```sh
# PostgreSQL
go run github.com/aita/sqlkit/cmd/sqlkitgen \
  -dsn "$DATABASE_URL" -schema public -package appdb -out appdb/tables.go

# MySQL, one file per table, with row DTOs in their own files
go run github.com/aita/sqlkit/cmd/sqlkitgen \
  -driver mysql -dsn 'user:pass@tcp(localhost:3306)/app' \
  -package appdb -out appdb -file-per-table -dto -dto-separate-file
```

By default the generator emits a typed binding per table — and nothing else.
Row types are not generated unless you ask for them (`-dto`): you can define
the structs you scan into and insert from, and fields map by name.

The binding is metadata, not an ORM: it exposes `Column[T]` fields (`Users.ID`
is a `sqlkit.Column[int64]`, ...) and the table identity, but carries no
data-access methods. All statements start from the db handle with the table as
an argument: `db.Select().From(Users)`, `db.Insert(Users).Values(...)`,
`db.Update(Users)`, `db.Delete(Users)`.

```go
var Users = ...
```

### CLI flags

| Flag | Purpose |
|---|---|
| `-driver` | `pgx` (default) or `mysql` |
| `-dialect` | override the schema dialect (`postgres`/`mysql`); inferred from `-driver` |
| `-schema` | schema/database to read (default `public` for postgres, current DB for mysql) |
| `-tables` | comma-separated subset of tables to generate |
| `-out` | output file (single-file mode) or directory (multi-file mode); stdout if empty |
| `-file-per-table` | write one file per table |
| `-file-name` | single-file output name when `-out` is a directory (default `tables.go`) |
| `-dto` | also generate a row struct (DTO) per table |
| `-dto-separate-file` | write DTOs into their own file(s) |
| `-dsl` | emit a `sqldef`-based design file from the schema instead of table bindings |
| `-ddl` | emit CREATE TABLE DDL from the schema instead of table bindings |

The generated binding is metadata, not an ORM: no `Get`, no relation
navigation, no per-column option soup. Foreign keys, indexes, constraints, and
comments are read into the metadata model (`meta.Table`) for the tools that
need them — the DDL generator consumes the same model — but they do not add
methods to the query bindings.

### Using codegen as a library

The `codegen` package is the generator's public API. Build a `meta.Schema`
with the `sqldef` package, by hand, or with `inspector.Inspect`, customize the
SQL→Go type mapping, and choose what to emit:

```go
model, _ := inspector.Inspect(ctx, db, "postgres", "public")

files, err := codegen.Generate(model, codegen.Options{
    PackageName:  "appdb",
    FilePerTable: true,
    EmitDTO:      true,
    // Map numeric to a decimal type instead of string.
    TypeMapper: codegen.DefaultTypeMapper{Overrides: map[string]codegen.GoType{
        "numeric": {ImportPath: "github.com/shopspring/decimal", Name: "decimal.Decimal"},
    }},
})
// files[i].Name / files[i].Content
```

Schema metadata — SQL type, length, precision/scale, comments, and the
primary-key/nullable/default constraints — lives on `meta.Column`, the model
the generators consume. The runtime binding stays minimal: each column is a
plain `sqlkit.NewColumn[T](info, "name")` carrying only the table identity,
column name, and static type `T`.

## Declaring the schema in Go (sqldef package)

The `sqldef` package is an alternative to introspection: declare the schema as
plain Go values in an ordinary package, and generate the table bindings — and
the DDL — from it, no live database required. The design is the source of
truth, lives in version control, and feeds the same `meta` model the rest of
the tooling consumes. By convention the design package itself is named
`schema`; it imports `sqldef` for the building blocks.

A `Catalog` is the reusable root. Tables are values: store them in variables,
reference them from foreign keys, share them across files or packages.

```go
// schema/schema.go
package schema

import "github.com/aita/sqlkit/sqldef"

var Catalog = sqldef.NewCatalog()

var Public = Catalog.Schema("public")

var Users = Public.Table("users",
    sqldef.TableComment("Registered blog authors"),
    sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
    sqldef.Column("email", sqldef.VarChar(255), sqldef.Unique(), sqldef.Comment("Unique login address")),
    sqldef.Column("name", sqldef.Text, sqldef.Nullable()),
    sqldef.Column("created_at", sqldef.Timestamp, sqldef.Default("now()")),
)

var Posts = Public.Table("posts",
    sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
    sqldef.Column("user_id", sqldef.BigInt, sqldef.References(Users, "id")),
    sqldef.Column("title", sqldef.Text),
    sqldef.Column("body", sqldef.Text, sqldef.Nullable()),
    sqldef.Column("published", sqldef.Bool, sqldef.Default("false")),
    sqldef.Index("user_id"),
    sqldef.Check("title <> ''"),
)
```

Single-schema designs can skip the schema handle — `Catalog.Table(...)`
declares tables with unqualified names.

Columns are NOT NULL unless marked `Nullable()` (nullable columns map to
pointer types, as with introspection). `Default()` marks database-side
defaults; give it the expression (`Default("now()")`) and the DDL generator
renders it, while a bare `Default()` on an integer primary key renders as
serial/auto-increment. `PrimaryKey()` on several columns declares a composite
key. Types are portable (`BigInt` is `int8` on PostgreSQL and `bigint` on
MySQL, `Timestamp` is `timestamptz`/`datetime`, ...); `Type("citext")` passes
an engine type name through verbatim.

Foreign keys reference the table values, so the compiler catches typos:
`References(Users, "id")` for the single-column case,
`ForeignKey([]string{...}, Orders, []string{...})` for composite keys, and the
`Table.ForeignKey` method for self-referencing keys
(`Employees.ForeignKey([]string{"manager_id"}, Employees, []string{"id"})`).
References may cross schemas within the same catalog.

Indexes, constraints, and comments round out the design: `Unique()` puts a
single-column unique index on a column, `Index`/`UniqueIndex` declare
(composite) indexes, and `Check` adds a CHECK constraint. `Comment` on a
column and `TableComment` on a table carry through to the generated bindings
and DTOs as Go doc comments — and the inspector reads the same metadata back
from live PostgreSQL/MySQL schemas, so `-dsl` bootstraps preserve it.

Because declarations are values, they compose: a shared
`[]sqldef.TableElement` of audit columns can be appended into any table.

A small generator program turns the catalog into code and DDL — see
`examples/blog/gen`, which regenerates both `blogdb` and `schema.sql`:

```go
files, err := schema.Catalog.Generate(codegen.Options{PackageName: "blogdb"})
if err != nil { ... }
err = sqldef.Write("blogdb", files)

ddl, err := schema.Catalog.DDL("postgres") // CREATE TABLE / INDEX / COMMENT ...
```

`Generate` accepts the full `codegen.Options` surface (DTOs, file layout, type
overrides); `Build("postgres")` returns the raw `meta.Schema` per declared
schema for other tooling. Mistakes — duplicate names, dangling foreign keys,
references into another catalog — are collected and reported by
`Build`/`Generate`/`DDL`.

### Bootstrapping a design from an existing database

`sqlkitgen -dsl` runs the introspection in reverse: it reads a live schema and
emits the catalog design file for it, so an existing database can be converted
once and maintained as Go code from then on:

```sh
go run github.com/aita/sqlkit/cmd/sqlkitgen \
  -dsn "$DATABASE_URL" -schema public -dsl -package schema -out schema/schema.go
```

The emitted file is a starting point meant to be edited (it carries no
`DO NOT EDIT` marker). The same conversion is available as a library via
`codegen.GenerateDesign(model, codegen.DesignOptions{...})`, and `-ddl` emits
the introspected schema as CREATE TABLE statements instead.

## Querying and scanning

Build the query from typed columns; choose the destination at `Scan`. Columns
map to struct fields by `sqlkit` tag, exact field name, or snake_case field
name, with embedded structs flattened. Fields implementing `sql.Scanner`
(e.g. `sql.NullString` or your own types) receive the column value directly,
and `driver.Valuer` fields work as INSERT/UPDATE values.

```go
// All columns of the source, into a slice of your DTO.
var users []MyUserDTO
err := db.Select().From(appdb.Users).
    Where(appdb.Users.Name.IsNotNull(), sqlkit.Like(appdb.Users.Email, "%@x.com")).
    OrderBy(appdb.Users.CreatedAt.Desc()).
    Limit(20).
    All(ctx).Scan(&users)

// Explicit columns / projections (alias with sql.As).
var pairs []struct {
    ID    int64
    Email string
}
err = db.Select(appdb.Users.ID, appdb.Users.Email).From(appdb.Users).
    All(ctx).Scan(&pairs)

// Scan into a single struct (first row) or a scalar slice.
var one MyUserDTO
err = db.Select().From(appdb.Users).Where(appdb.Users.ID.Eq(1)).
    All(ctx).Scan(&one)        // ErrNoRows if none

var ids []int64
err = db.Select(appdb.Users.ID).From(appdb.Users).All(ctx).Scan(&ids)

n, err := db.Select().From(appdb.Users).Where(...).Count(ctx)
```

Joins and the generated primary-key lookup:

```go
var posts []PostDTO
err := db.Select(appdb.Posts.ID, appdb.Posts.Title).From(appdb.Posts).
    Join(appdb.Users, appdb.Posts.UserID.EqExpr(appdb.Users.ID)).
    Where(appdb.Users.Email.Eq("alice@example.com")).
    All(ctx).Scan(&posts)

// Lookup by primary key is just a query; ErrNoRows when absent.
var user MyUserDTO
err := db.Select().From(appdb.Users).Where(appdb.Users.ID.Eq(42)).
    Limit(1).All(ctx).Scan(&user)
```

## Writing

Like `Select`, the write builders start from the db handle (or a `Session`)
with the table as an argument and end with `Exec(ctx)`. To read rows back,
add an explicit `Returning(...)` — only then does the chain expose
`All(ctx).Scan(&dst)`:

```go
// Values accepts structs, pointers, or slices of either. Fields map to
// columns by sqlkit tag / exact / snake_case name — the same rules as Scan —
// so generated rows and caller-defined DTOs both work.
var inserted []MyUserDTO
err := db.Insert(appdb.Users).Values([]MyUserDTO{u1, u2}).
    Returning(). // all columns; or Returning(appdb.Users.ID) for specific ones
    All(ctx).Scan(&inserted) // generated ids/defaults

// INSERT ... SELECT.
_, err = db.Insert(appdb.Archive).
    Columns(appdb.Archive.UserID, appdb.Archive.Title).
    FromSelect(sqlkit.Select(appdb.Posts.UserID, appdb.Posts.Title). // unbound: subquery only
        From(appdb.Posts).
        Where(appdb.Posts.Published.Eq(false))).
    Exec(ctx)

_, err = db.Update(appdb.Users).
    Set(appdb.Users.Name.Set(&name)).
    Where(appdb.Users.ID.Eq(7)).
    Exec(ctx)

var deletedIDs []int64
err = db.Delete(appdb.Posts).Where(appdb.Posts.Published.Eq(false)).
    Returning(appdb.Posts.ID).
    All(ctx).Scan(&deletedIDs)
```

Columns with a database-side default are omitted from INSERT when their value
is the Go zero value in every row, so serial keys and defaults are filled in.

## Aggregates, CASE, window functions, UNION

The `sql` package supplies the expression vocabulary; `sqlkit.Cond` lifts any
sql expression into a typed-chain predicate.

```go
// Aggregates with GROUP BY / HAVING; sql.If is a two-armed CASE.
var stats []StatDTO
err := db.Select(
    appdb.Users.ID,
    sql.As(sql.Count(appdb.Posts.ID), "post_count"),
).From(appdb.Users).
    LeftJoin(appdb.Posts, appdb.Posts.UserID.EqExpr(appdb.Users.ID)).
    GroupBy(appdb.Users.ID).
    Having(sqlkit.Cond(sql.Gt(sql.CountAll(), sql.Int(1)))).
    All(ctx).Scan(&stats)

// Window functions: ROW_NUMBER() OVER (PARTITION BY user_id ORDER BY id DESC).
rn := sql.RowNumber().Over().PartitionBy(appdb.Posts.UserID).OrderBy(appdb.Posts.ID.Desc())

// Set operations. A compound ORDER BY references output columns, not
// table-qualified ones.
var emails []string
err = db.Select(appdb.Users.Email).From(appdb.Users).Where(...).
    Union(sqlkit.Select(appdb.Users.Email).From(appdb.Users).Where(...)).
    OrderBy(sql.Asc(sql.Col("email"))).
    All(ctx).Scan(&emails)
```

`Coalesce`, `NullIf`, `Greatest`, `Least`, `Sum`, `Avg`, `Min`, `Max`,
`Rank`, `DenseRank`, `Lag`, `Lead`, and generic `sql.Func(name, args...)` are
available.

## Transactions

`WithSession` runs a callback in one transaction — commit on nil return,
rollback on error or panic:

```go
err := db.WithSession(func(session *sqlkit.Session) error {
    if _, err := session.Insert(appdb.Users).Values(u).Exec(ctx); err != nil {
        return err
    }
    n, err := session.Select().From(appdb.Users).Count(ctx)
    ...
    return err
})
```

For manual control, `NewSession` hands you the session directly. Either way
the transaction begins lazily — nothing is opened until the first statement
runs, so an untouched session's Commit/Close are free no-ops:

```go
session := db.NewSession()
defer session.Close() // rolls back unless committed; no-op if nothing ran

if _, err := session.Insert(appdb.Users).Values(u).Exec(ctx); err != nil { ... }
err = session.Commit()
```

## Raw SQL

When a query is easier to write by hand, `RawSQL` runs a raw string (or a
built `sql.Query`) and scans the same way:

```go
var stats []StatDTO
err := db.RawSQL(ctx, `
    SELECT u.email, COUNT(p.id) AS post_count
    FROM users u LEFT JOIN posts p ON p.user_id = u.id
    GROUP BY u.email`).Scan(&stats)

var user User
err = db.RawSQL(ctx, "SELECT * FROM users WHERE id = $1", 42).Scan(&user) // ErrNoRows if none
```

Source-less SELECTs work too (`SELECT now()`):

```go
var now time.Time
err = db.RawSQL(ctx, sql.Select(sql.Raw("now()"))).Scan(&now)
```

To stream a large result instead of materializing it as a slice,
`Records[T]` turns the same pending result into an iterator of
already-scanned values. Rows are fetched and scanned one at a time —
straight into `T`, with no intermediate map or slice per row — and breaking
out of the loop releases the rest of the result set. `T` may be a struct, a
pointer to struct, or a scalar, matched by the same rules as `Scan`:

```go
for user, err := range sqlkit.Records[User](db.RawSQL(ctx, query)) {
    if err != nil { return err }
    ...
}
```

For a single row, query chains offer `First` and `One` alongside `All`.
`First` pushes `LIMIT 1` into the query and scans the first row (or
`ErrNoRows`); `One` additionally requires the row to be the only one,
returning `ErrTooManyRows` otherwise (it pushes `LIMIT 2`, so the check
never reads further):

```go
var user User
err := db.Select().From(Users).Where(Users.ID.Eq(1)).One(ctx).Scan(&user)
```

`Page` fetches a LIMIT/OFFSET window in one call:

```go
var users []User
err := db.Select().From(Users).OrderBy(Users.ID.Asc()).Page(ctx, 20, 40).Scan(&users)
```

## Standalone query building (pgx, sqlx, database/sql, ...)

sqlkit also works as a pure SQL builder, with execution and scanning handled
by another library. Every builder has an unbound form — the package-level
constructors `sqlkit.Select`, `sqlkit.Insert`, `sqlkit.Update`,
`sqlkit.Delete` (and everything in package `sql`) — and compiles to SQL text
plus bind arguments with `ToSQL(dialect)`. Nothing in that path touches a
database, and the root module pulls in no driver dependencies:

```go
query := sqlkit.Select(appdb.Users.ID, appdb.Users.Email).
    From(appdb.Users).
    Where(appdb.Users.ID.Gt(10)).
    OrderBy(appdb.Users.ID.Asc())

sqlText, args, err := query.ToSQL(sql.PostgreSQLDialect{})

// pgx (native pool — $n placeholders match the PostgreSQL dialect):
rows, err := pool.Query(ctx, sqlText, args...)

// sqlx:
err = sqlxDB.SelectContext(ctx, &users, sqlText, args...)

// database/sql:
rows, err := stdDB.QueryContext(ctx, sqlText, args...)
```

DML compiles the same way — including struct-fed inserts — with
`sql.MySQLDialect{}` switching to backticks and `?` placeholders:

```go
sqlText, args, err := sqlkit.Insert(appdb.Users).Values(u).ToSQL(sql.PostgreSQLDialect{})
sqlText, args, err = sqlkit.Update(appdb.Users).
    Set(appdb.Users.Name.Set(&name)).
    Where(appdb.Users.ID.Eq(7)).
    ToSQL(sql.MySQLDialect{})
```

When execution goes through `database/sql` (including sqlx and pgx's stdlib
adapter), `sqlkit.ScanRows` applies sqlkit's column-to-field mapping to the
result, so the scanning rules stay the same regardless of who ran the query:

```go
rows, err := stdDB.QueryContext(ctx, sqlText, args...)
defer rows.Close()
var users []MyUserDTO
err = sqlkit.ScanRows(rows, &users)
```

Table bindings don't require codegen or a live database. For SELECT, a
`TableInfo` and typed columns are enough; the full `TableMeta` interface
(what `Insert`/`Update`/`Delete` take, ~30 lines per table — see
`examples/blog/blogdb` for the shape) can be written by hand too:

```go
var users = sqlkit.TableInfo{Name: "users"}
var userID = sqlkit.NewColumn[int64](users, "id")
var userEmail = sqlkit.NewColumn[string](users, "email")

sqlText, args, err := sqlkit.Select(userID, userEmail).
    From(users).
    Where(userEmail.Eq("a@example.com")).
    ToSQL(sql.PostgreSQLDialect{})
```

## Dialects

PostgreSQL is the default and the most exercised dialect. The compiler is
dialect-driven (`sql.Dialect`: identifier quoting, placeholders, capability
flags), and a `MySQLDialect` is included: the whole query builder compiles
for MySQL (backtick quoting, `?` placeholders), `Open` infers the dialect
from the driver name (`"mysql"` → MySQL, otherwise PostgreSQL; override with
`WithDialect`), and `Returning(...)` fails at compile time with a pointer to
`Result.LastInsertId`, since MySQL has no RETURNING.

`sqlkitgen` introspects both PostgreSQL and MySQL schemas (`inspector`'s
dialect switch is the extension point). MySQL integration tests are not yet
included; exercise the generated MySQL bindings before relying on them in
production.

## Compile-time guarantees

What the compiler enforces:

- comparison and `IN` values match the column's Go type (`Column[T]` methods take `T`),
- join conditions compare columns of the same type (`EqExpr(TypedExpr[T])`),
- `SET` assignments match the column type (`Column[T].Set(T)`),
- every generated binding satisfies `sqlkit.TableMeta` (a `var _` line in the
  generated file).

What surfaces at runtime: column-to-field mapping — during `Scan` for reads
and during `Insert(...).Values` for writes — and SQL-level errors the type
system cannot see (constraint violations, schema drift since generation, raw
SQL fragments).

## Testing

Unit tests use a scriptable in-memory `database/sql` driver
(`internal/sqltest`), so `go test ./...` needs no database.

Integration tests run the full stack against a real PostgreSQL started with
[testcontainers](https://golang.testcontainers.org/). They live in their own
module (`internal/integration`) so the testcontainers dependency tree stays
out of the root module that library consumers import — run them from that
directory:

```sh
cd internal/integration && go test ./...
```

They require a Docker daemon and registry access. To avoid Docker Hub's
anonymous pull rate limit, point at a mirror:

```sh
SQLKIT_TEST_POSTGRES_IMAGE=mirror.gcr.io/library/postgres:16-alpine \
  go test ./...
```
