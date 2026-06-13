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
| `meta` | Database schema metadata model shared by the tooling (tables, columns, indexes, constraints, comments), plus SQL DDL generation (`CreateSQL`/`DropSQL`) |
| `inspector` | PostgreSQL/MySQL introspection into the `meta` model |
| `codegen` | `meta` → Go code generator (library: customizable types, layout, DTOs) |
| `cmd/sqlkitgen` | CLI wrapper around `inspector` + `codegen` |

## The flow: a catalog generates the bindings

You author the schema as a sqldef catalog (or bootstrap one from an existing
database with `sqlkitgen`), then generate the typed table bindings — and the
DDL — from it. The catalog is the source of truth; `sqlkitgen` only bootstraps
it. See [Declaring the schema in Go](#declaring-the-schema-in-go-sqldef-package)
below for authoring, and `examples/blog` for `go:generate` directives that
drive the bindings and DDL from the catalog.

To bootstrap a catalog from a live **PostgreSQL or MySQL** schema:

```sh
go run github.com/aita/sqlkit/cmd/sqlkitgen inspect \
  --dsn "$DATABASE_URL" --schema public --package schema --out schema/schema.go
```

| `sqlkitgen inspect` flag | Purpose |
|---|---|
| `--dsn` | database connection string (required) |
| `--driver` | `pgx` (default) or `mysql` |
| `--dialect` | override the schema dialect (`postgres`/`mysql`); inferred from `--driver` |
| `--schema` | schema/database to read (default `public` for postgres, current DB for mysql) |
| `--package` | package name of the generated catalog file (required) |
| `--tables` | comma-separated subset of tables to include |
| `--out` | output file or directory; stdout if empty |

The bootstrapped catalog is a starting point meant to be edited (it carries no
`DO NOT EDIT` marker); from then on the schema lives in code.

Once the catalog lives in a package, generate the typed bindings straight from
it — no hand-written generator program required. Point `sqlkitgen generate` at
the package that declares the `*sqldef.Catalog`:

```sh
go run github.com/aita/sqlkit/cmd/sqlkitgen generate ./schema -name appdb -out appdb
```

| `generate` flag | Purpose |
|---|---|
| `<package>` | import path or `./dir` of the package that declares the catalog (positional) |
| `-var` | name of the `*sqldef.Catalog` variable (default `Catalog`) |
| `-name` | package name of the generated bindings (required) |
| `-out` | output directory for the generated bindings (required) |
| `-tables` | comma-separated subset of tables to include |
| `-dialect` | type-mapping dialect (`postgres`/`mysql`, default `postgres`) |
| `-file-per-table` | one file per table instead of a single `tables.go` |
| `-emit-dto` | also generate a row struct (DTO) per table |

The DDL is a separate concern: `sqlkitgen ddl` renders the catalog's
`CREATE TABLE`/`INDEX`/constraint statements (to a file with `-out`, else
stdout) for a `-dialect`.

```sh
go run github.com/aita/sqlkit/cmd/sqlkitgen ddl ./schema -out schema.sql
```

The natural home for both is a pair of `go:generate` directives next to the
catalog, so `go generate ./...` rebuilds the bindings and the DDL — see
`examples/blog`:

```go
//go:generate go run github.com/aita/sqlkit/cmd/sqlkitgen generate ./schema --name blogdb --out blogdb
//go:generate go run github.com/aita/sqlkit/cmd/sqlkitgen ddl ./schema --out schema.sql
```

A catalog is a runtime Go value (foreign keys reference the `meta.Table` values
that `Table` returns), so it can't be read statically. Both commands write a
throwaway generator program that imports the catalog package and runs
`codegen.Generate`/`codegen.Write` (or `meta.Metadata.CreateSQL`) via `go run`, then
remove it. For full control over the type mapping or DTO naming, write that
program yourself against the `codegen` library below.

The generated binding is metadata, not an ORM: it exposes `Column[T]` fields
(`Users.ID` is a `sqlkit.Column[int64]`, ...) and the table identity, but no
`Get`, no relation navigation, no data-access methods. All statements start
from the db handle with the table as an argument: `db.Select().From(Users)`,
`db.Insert(Users).Values(...)`, `db.Update(Users)`, `db.Delete(Users)`.
Row types (DTOs) are not generated unless you ask (`EmitDTO`): define the
structs you scan into and insert from, and fields map by name.

### Using codegen as a library

The `codegen` package is the generator's public API. Build a `meta.Metadata`
with the `sqldef` package, by hand, or with `inspector.Inspect`, customize the
SQL→Go type mapping, and choose what to emit:

```go
catalog, _ := inspector.Inspect(ctx, db, "postgres", "public")

files, err := codegen.Generate(catalog, codegen.Options{
    PackageName:  "appdb",
    Dialect:      "postgres",
    FilePerTable: true,
    EmitDTO:      true,
    // Map numeric to a decimal type instead of string.
    TypeMapper: codegen.DefaultTypeMapper{Overrides: map[string]codegen.GoType{
        "numeric": {ImportPath: "github.com/shopspring/decimal", Name: "decimal.Decimal"},
    }},
})
// files[i].Name / files[i].Content
```

Schema metadata — the column's `meta.Type`, comments, and the
primary-key/nullable/default constraints — lives on `meta.Column`, the model
the generators consume. `meta.Type` is a portable type that resolves to an
engine type name only when a generator picks a dialect (`Type.Engine(dialect)`),
so the model stores no dialect itself; the sqldef DSL supplies types that
resolve on both engines, and the inspector supplies single-dialect ones
(`meta.Of`). The runtime binding stays minimal: each column is a plain
`sqlkit.NewColumn[T](info, "name")` carrying only the table identity, column
name, and static type `T`.

## Declaring the schema in Go (sqldef package)

The `sqldef` package is an alternative to introspection: declare the schema as
plain Go values in an ordinary package, and generate the table bindings — and
the DDL — from it, no live database required. The catalog is the source of
truth, lives in version control, and feeds the same `meta` model the rest of
the tooling consumes. By convention the package holding it is named `schema`;
it imports `sqldef` for the building blocks.

A `Catalog` is the reusable root, and `Catalog.Table(...)` returns a
`meta.Table` value: store it in a variable, reference it from foreign keys,
share it across files or packages.

```go
// schema/schema.go
package schema

import (
    "github.com/aita/sqlkit/sql"
    "github.com/aita/sqlkit/sqldef"
)

var Catalog = sqldef.NewCatalog()

var Public = Catalog.Schema("public")

var Users = Public.Table("users",
    sqldef.TableComment("Registered blog authors"),
    sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
    sqldef.Column("email", sqldef.VarChar(255), sqldef.Unique(), sqldef.Comment("Unique login address")),
    sqldef.Column("name", sqldef.Text, sqldef.Nullable()),
    sqldef.Column("created_at", sqldef.Timestamp, sqldef.Default(sql.Raw("now()"))),
)

var Posts = Public.Table("posts",
    sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
    sqldef.Column("user_id", sqldef.BigInt, sqldef.References(Users, "id")),
    sqldef.Column("title", sqldef.Text),
    sqldef.Column("body", sqldef.Text, sqldef.Nullable()),
    sqldef.Column("published", sqldef.Bool, sqldef.Default(sql.Raw("false"))),
    sqldef.Index("user_id"),
    sqldef.Check(sql.Raw("title <> ''")),
)
```

Single-schema catalogs can skip the schema handle — `Catalog.Table(...)`
declares tables in the catalog's default schema (unnamed, so unqualified
names). Pass `sqldef.NewCatalog(sqldef.DefaultSchema("public"))` to make that
default a named schema.

Columns are NOT NULL unless marked `Nullable()` (nullable columns map to
pointer types, as with introspection). `Default()` marks database-side
defaults; give it a `sql.Expression` (`Default(sql.Raw("now()"))`) and the DDL
generator compiles it for the target dialect, while a bare `Default()` on an
integer primary key renders as serial/auto-increment. `Check` likewise takes a
`sql.Expression` (`Check(sql.Raw("price > 0"))`); both reject bind values, so
wrap literals in `sql.Raw`. `PrimaryKey()` on several columns declares a composite
key. Types are portable: `BigInt`, `Timestamp`, ... are `meta.Type` values
that carry both engines' names (`int8`/`bigint`, `timestamptz`/`datetime`), so
one catalog targets PostgreSQL and MySQL and the generators resolve the engine
name only when you pick a dialect. `Type("citext")` passes an engine type name
through verbatim.

Foreign keys reference the `meta.Table` values, so the compiler catches typos:
`References(Users, "id")` for the single-column case,
`ForeignKey([]string{...}, Orders, []string{...})` for composite keys, and
`Catalog.AddForeignKey(Employees, ...)` for self-referencing keys (which can't
mention their own variable inside `Table(...)`). References may cross schemas
within the same catalog.

Indexes, constraints, and comments round out the catalog: `Unique()` puts a
single-column unique index on a column, `Index`/`UniqueIndex` declare
(composite) indexes, and `Check` adds a CHECK constraint. `Comment` on a
column and `TableComment` on a table carry through to the generated bindings
and DTOs as Go doc comments — and the inspector reads the same metadata back
from live PostgreSQL/MySQL schemas, so `sqlkitgen` bootstraps preserve it.

Because declarations are values, they compose: a shared
`[]sqldef.TableElement` of audit columns can be appended into any table.

The `sqlkitgen generate` command above turns the catalog into code and DDL for
you; under the hood it is just the `codegen` and `ddl` libraries, which you can
also call directly when you need full control:

```go
md, err := schema.Catalog.Build() // dialect-agnostic meta.Metadata
if err != nil { ... }

files, err := codegen.Generate(md, codegen.Options{PackageName: "blogdb"})
if err != nil { ... }
err = codegen.Write("blogdb", files)

d, _ := sql.DialectByName("postgres")
sqlText, err := md.CreateSQL(d)  // CREATE TABLE / INDEX / COMMENT ...
dropText := md.DropSQL(d)        // ALTER ... DROP / DROP TABLE (the inverse)
```

These methods only build SQL text; they never touch a database. DDL also
renders per element — `Table.CreateSQL(d)` / `Table.DropSQL(d)` and
`Index.CreateSQL(t, d)` / `Index.DropSQL(t, d)` — and `sqlkitgen ddl --drop`
prepends the teardown statements so the generated `schema.sql` recreates
cleanly.

`sqldef` is purely the schema DSL: `Build()` returns the raw `meta.Metadata`
(dialect-agnostic) that the `codegen` generator and the DDL renderer
(`meta.Metadata.CreateSQL`) — and any other tooling — consume directly.
`codegen.Generate` accepts the full `codegen.Options` surface (DTOs, file
layout, type overrides). Mistakes — duplicate names, dangling foreign keys,
references into another catalog — are collected and reported by `Build`; a type
with no name on the chosen dialect is reported by `codegen.Generate` /
`meta.Metadata.CreateSQL`.

### Bootstrapping a catalog from an existing database

`sqlkitgen` reads a live schema and emits the catalog source for it (see
[the flow](#the-flow-a-catalog-generates-the-bindings) above), so an
existing database can be converted once and maintained as Go code from then
on. The same conversion is available as a library via
`codegen.GenerateCatalog(catalog, codegen.CatalogOptions{...})`. Once you have a
catalog, `md.CreateSQL(d)` (with `md` from `Catalog.Build` and a dialect `d`
from `sql.DialectByName("postgres")`) renders the schema as CREATE TABLE
statements.

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

Typed columns carry the predicate vocabulary directly: `Eq`/`Ne`/`Lt`/`Le`/
`Gt`/`Ge`, `In`/`NotIn` (literal values), `Between`/`NotBetween`,
`IsNull`/`IsNotNull`, and `InQuery`/`NotInQuery` for an `IN (subquery)`. String
columns also have the free helpers `sqlkit.Like` / `NotLike` / `ILike`
(`ILike` is case-insensitive on both engines: native `ILIKE` on PostgreSQL,
`LOWER(x) LIKE LOWER(pattern)` on MySQL). `sqlkit.Exists` / `NotExists` wrap a subquery as a
predicate for semi-joins, and `sqlkit.And` / `Or` / `Not` combine predicates:

```go
err = db.Select().From(appdb.Users).
    Where(
        appdb.Users.Age.Between(18, 65),
        sqlkit.Exists(sqlkit.Select(appdb.Posts.ID).From(appdb.Posts).
            Where(appdb.Posts.UserID.EqExpr(appdb.Users.ID))),
    ).
    All(ctx).Scan(&users)
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

// Union / UnionAll / Intersect / Except combine a flat list of SELECTs instead
// of left-folding the chain by hand. db.Union (and session.Union) bind the
// result to that handle, so the operands need no executor of their own:
err = db.Union(
    sqlkit.Select(appdb.Users.Email).From(appdb.Users).Where(...),
    sqlkit.Select(appdb.Admins.Email).From(appdb.Admins),
    sqlkit.Select(appdb.Guests.Email).From(appdb.Guests),
).OrderBy(sql.Asc(sql.Col("email"))).All(ctx).Scan(&emails)

// The package-level sqlkit.Union is the same but binds to the first operand's
// executor — handy when the operands are already built from a db or session.
err = sqlkit.Union(
    db.Select(appdb.Users.Email).From(appdb.Users).Where(...),
    sqlkit.Select(appdb.Admins.Email).From(appdb.Admins),
).OrderBy(sql.Asc(sql.Col("email"))).All(ctx).Scan(&emails)
```

`Coalesce`, `NullIf`, `Greatest`, `Least`, `Sum`, `Avg`, `Min`, `Max`,
`Rank`, `DenseRank`, `Lag`, `Lead`, and generic `sql.Func(name, args...)` are
available. For JSON, `sql.JSONArray(...)`, `sql.JSONObject(key, val, ...)`,
`sql.JSONArrayAgg(expr)`, and `sql.JSONObjectAgg(key, val)` build the SQL/JSON
standard functions (PostgreSQL 16+, MySQL 5.7+); older PostgreSQL exposes the
classic `json_build_array` / `json_agg` names through `sql.Func`.

`sql.Concat(a, b, ...)` concatenates strings portably — the standard `||`
operator on PostgreSQL, `CONCAT(...)` on MySQL (whose `||` means logical OR).

JSON values are read with `sql.JSONGet(operand, path...)` (→ json) and
`sql.JSONGetText(operand, path...)` (→ text). Path steps are object keys
(`string`) or array indices (`int`), and the path is portable: each dialect
renders it natively — PostgreSQL chains `->` / `->>`, MySQL emits
`JSON_EXTRACT` / `JSON_UNQUOTE`.

```go
// PostgreSQL: ("data" -> 'address') ->> 'city'   MySQL: JSON_UNQUOTE(JSON_EXTRACT(`data`, '$.address.city'))
city := sql.JSONGetText(appdb.Events.Data, "address", "city")
err = db.Select(appdb.Events.ID).From(appdb.Events).
    Where(sqlkit.Cond(sql.Eq(city, sql.Val("Tokyo")))). // text form compares to strings
    All(ctx).Scan(&ids)

firstTag := sql.JSONGet(appdb.Events.Data, "tags", 0) // -> json
```

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
`TableInfo` and typed columns are enough; the full `Table` interface
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

PostgreSQL is the default and the most exercised dialect. `sql.Dialect` is the
single point where engine differences are resolved — identifier quoting,
placeholders, capability flags, and DDL rendering (column types, serial /
AUTO_INCREMENT keys, comments) — so both the query compiler and the schema
model's `CreateSQL` / `DropSQL` methods run through it. A `MySQLDialect`
is included: the whole query builder compiles
for MySQL (backtick quoting, `?` placeholders), `Open` infers the dialect
from the driver name (`"mysql"` → MySQL, otherwise PostgreSQL; override with
`WithDialect`), and `Returning(...)` fails at compile time with a pointer to
`Result.LastInsertId`, since MySQL has no RETURNING.

`sqlkitgen` introspects both PostgreSQL and MySQL schemas (`inspector`'s
dialect switch is the extension point). The integration suite runs the full
stack — builder, scanner, DDL generator, and inspector — against both engines
(see [Testing](#testing)).

## Compile-time guarantees

What the compiler enforces:

- comparison and `IN` values match the column's Go type (`Column[T]` methods take `T`),
- join conditions compare columns of the same type (`EqExpr(TypedExpr[T])`),
- `SET` assignments match the column type (`Column[T].Set(T)`),
- every generated binding satisfies `sqlkit.Table` (a `var _` line in the
  generated file).

What surfaces at runtime: column-to-field mapping — during `Scan` for reads
and during `Insert(...).Values` for writes — and SQL-level errors the type
system cannot see (constraint violations, schema drift since generation, raw
SQL fragments).

## Testing

Unit tests use a scriptable in-memory `database/sql` driver
(`internal/sqltest`), so `go test ./...` needs no database.

Integration tests run the full stack against real PostgreSQL **and** MySQL
servers — the same tests execute on both engines, so the query builder, the
scanner, the DDL generator, and the inspector are all verified against the
databases they target. They live in their own module (`internal/integration`)
so the testcontainers dependency tree stays out of the root module that
library consumers import — run them from that directory:

```sh
cd internal/integration && go test ./...
```

Each engine connects one of two ways:

- **Testcontainers (default).** With no DSN set, the suite boots a throwaway
  container per engine with [testcontainers](https://golang.testcontainers.org/),
  which needs a Docker daemon and registry access. When neither a DSN nor
  Docker is available, that engine's tests skip rather than fail. To avoid
  Docker Hub's anonymous pull rate limit, point at a mirror with
  `SQLKIT_TEST_POSTGRES_IMAGE` / `SQLKIT_TEST_MYSQL_IMAGE`.
- **An existing server.** Set `SQLKIT_TEST_POSTGRES_DSN` and/or
  `SQLKIT_TEST_MYSQL_DSN` to connect directly — this is how CI targets service
  containers without a Docker socket. The MySQL DSN must enable `parseTime`
  (so DATETIME scans into `time.Time`) and select a database named `public`,
  because the shared blog bindings qualify tables with the `public` schema (a
  PostgreSQL schema maps to a MySQL database):

  ```sh
  SQLKIT_TEST_MYSQL_DSN='sqlkit:sqlkit@tcp(127.0.0.1:3306)/public?parseTime=true' \
    go test ./...
  ```

## Continuous integration

[`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs on every branch
push (testing the branch's own commit). The `unit test` job builds, vets,
`gofmt`-checks, and runs `go test -race ./...` on the root module (no database
needed), and builds and vets the integration module. The `integration` job
starts PostgreSQL and MySQL service containers, points the suite at them via
the `SQLKIT_TEST_*_DSN` variables, and runs the integration tests against both.
