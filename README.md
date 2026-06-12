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
| `sqlkit` | Typed query chains, `Database`, `Session`, result scanning, dynamic `RecordSet` (module root) |
| `sql` | SQL AST, dialects, compiler, and all statement generation |
| `schema` | Database schema model shared by the tooling (tables, columns, foreign keys) |
| `inspector` | PostgreSQL/MySQL `information_schema` introspection into the `schema` model |
| `codegen` | `schema` → Go code generator (library: customizable types, layout, DTOs) |
| `cmd/sqlkitgen` | CLI wrapper around `inspector` + `codegen` |

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

The generated binding is metadata, not an ORM: no `Get`, no relation
navigation, no per-column option soup. Foreign keys are read into the schema
model (`schema.Table.ForeignKeys`) for tools that need them — the migration /
DDL generator that consumes the same model — but they do not add methods to the
query bindings.

### Using codegen as a library

The `codegen` package is the generator's public API. Build a `schema.Schema`
by hand or with `inspector.Inspect`, customize the SQL→Go type mapping, and
choose what to emit:

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

Schema metadata — SQL type, length, precision/scale, and the
primary-key/nullable/default constraints — lives on `schema.Column`, the model
the generator (and a future DDL/migration generator) consumes. The runtime
binding stays minimal: each column is a plain
`sqlkit.NewColumn[T](info, "name")` carrying only the table identity, column
name, and static type `T`.

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

`db.QueryRecords` returns a dynamic `RecordSet` when even the result shape
is unknown. It streams: records are fetched on demand during iteration or
Scan (single-pass over `sql.Rows`), and breaking out of the loop releases
the rest of the result set. Each `Record` is a lightweight view over the
current row of the underlying `sql.Rows` — values are scanned straight into
your destination, with no per-record map or intermediate slice — so it is
only valid within its loop step; copy values out with `Scan` instead of
retaining it:

```go
rs, err := db.QueryRecords(ctx, query)
for record, err := range rs.Records() {
    if err != nil { return err }
    var row struct{ ID int64 `sqlkit:"id"` }
    if err := record.Scan(&row); err != nil { return err }
    ...
}
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
