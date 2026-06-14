# sqlkit

A typed SQL toolkit for Go. You write SQL as Go — the **SQL DSL** — and the same
notation and compiler cover both queries (DML) and schema changes (DDL):

- **Typed query builder** — table bindings expose `Column[T]` fields, so
  predicates, assignments, and join conditions are checked by the Go compiler.
  `Users.ID.Eq("foo")` is a compile error, not a runtime SQL error.
- **Versioned migrations** — the `migrate` package runs revision-linked
  (DAG) up/down migrations whose `Up`/`Down` callbacks call `op.CreateTable`,
  `op.AddColumn`, `op.CreateForeignKey`, … with columns declared in the `sqldef`
  DSL. Every apply and rollback is recorded in an append-only history table, and
  `migrate.Diff` generates the DDL for a schema change. See [Migrations](#migrations).
- **Results into your own structs** — build a typed query, then `Scan` the
  result into any destination you define (`sqlkit` tag / snake_case mapping,
  `sql.Scanner` support). The query shape and the DTO are decoupled.
- **Full SQL surface** — joins, GROUP BY / HAVING, ORDER BY, DISTINCT,
  UNION / INTERSECT / EXCEPT, CTEs (`WITH` / `WITH RECURSIVE`), aggregates,
  CASE / IF, and window functions (`OVER (PARTITION BY ...)`).
- **Code generation from PostgreSQL** — `sqlkit` introspects a live schema
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
| `sqlkit` | Typed query chains, `Database`, `Session`, result scanning, streaming `Records[T]` iteration, and the relation-aware surface (FK-driven `JoinRel`, `Filter` bundles, `Preload`) — module root |
| `sql` | SQL AST, dialects, compiler, and all statement generation — DML query builders and DDL statement builders (the SQL DSL) |
| `sqldef` | In-code schema declarations: a reusable `Catalog` of tables that feeds the generators |
| `meta` | Database schema metadata model shared by the tooling (tables, columns, indexes, constraints, comments), plus SQL DDL generation (`CreateSQL`/`DropSQL`) built on the `sql` DDL builders |
| `migrate` | Versioned up/down migrations (operations authored with `sqldef`), applied with full history recorded in a `schema_migrations` table; `migrate.Diff` generates DDL from two schema snapshots |
| `inspector` | PostgreSQL/MySQL introspection into the `meta` model |
| `codegen` | `meta` → Go code generator (library: customizable types, layout, DTOs) |
| `cmd/sqlkit` | CLI wrapper around `inspector` + `codegen` |

## The flow: a catalog generates the bindings

You author the schema as a sqldef catalog (or bootstrap one from an existing
database with `sqlkit`), then generate the typed table bindings — and the
DDL — from it. The catalog is the source of truth; `sqlkit` only bootstraps
it. See [Declaring the schema in Go](#declaring-the-schema-in-go-sqldef-package)
below for authoring, and `examples/blog` for `go:generate` directives that
drive the bindings and DDL from the catalog.

To bootstrap a catalog from a live **PostgreSQL or MySQL** schema:

```sh
go run github.com/aita/sqlkit/cmd/sqlkit inspect \
  --dsn "$DATABASE_URL" --schema public --package schema --out schema/schema.go
```

| `sqlkit inspect` flag | Purpose |
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
it — no hand-written generator program required. Point `sqlkit generate` at
the package that declares the `*sqldef.Catalog`:

```sh
go run github.com/aita/sqlkit/cmd/sqlkit generate ./schema -name appdb -out appdb
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

The DDL is a separate concern: `sqlkit ddl` renders the catalog's
`CREATE TABLE`/`INDEX`/constraint statements (to a file with `-out`, else
stdout) for a `-dialect`.

```sh
go run github.com/aita/sqlkit/cmd/sqlkit ddl ./schema -out schema.sql
```

The natural home for both is a pair of `go:generate` directives next to the
catalog, so `go generate ./...` rebuilds the bindings and the DDL — see
`examples/blog`:

```go
//go:generate go run github.com/aita/sqlkit/cmd/sqlkit generate ./schema --name blogdb --out blogdb
//go:generate go run github.com/aita/sqlkit/cmd/sqlkit ddl ./schema --out schema.sql
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
within the same catalog. Add referential actions with `OnDelete`/`OnUpdate`
options, e.g. `References(Users, "id", sqldef.OnDelete(sqldef.Cascade))`.

Indexes, constraints, and comments round out the catalog: `Unique()` puts a
single-column unique index on a column, `Index`/`UniqueIndex` declare
(composite) indexes, and `Check` adds a CHECK constraint. `Comment` on a
column and `TableComment` on a table carry through to the generated bindings
and DTOs as Go doc comments — and the inspector reads the same metadata back
from live PostgreSQL/MySQL schemas, so `sqlkit` bootstraps preserve it.

Because declarations are values, they compose: a shared
`[]sqldef.TableElement` of audit columns can be appended into any table.

The `sqlkit generate` command above turns the catalog into code and DDL for
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
`Index.CreateSQL(t, d)` / `Index.DropSQL(t, d)` — and `sqlkit ddl --drop`
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

`sqlkit` reads a live schema and emits the catalog source for it (see
[the flow](#the-flow-a-catalog-generates-the-bindings) above), so an
existing database can be converted once and maintained as Go code from then
on. The same conversion is available as a library via
`codegen.GenerateCatalog(catalog, codegen.CatalogOptions{...})`. Once you have a
catalog, `md.CreateSQL(d)` (with `md` from `Catalog.Build` and a dialect `d`
from `sql.DialectByName("postgres")`) renders the schema as CREATE TABLE
statements.

## Migrations

The `migrate` package runs schema migrations: each migration's
`Up` and `Down` are callbacks that receive an `Operator` and perform the change
with operations like `op.CreateTable`, `op.AddColumn`, `op.DropColumn`,
`op.CreateIndex`, and `op.CreateForeignKey`. Columns are declared with the
`sqldef` DSL (portable types `sqldef.BigInt` / `sqldef.VarChar(n)` …, options
`Nullable` / `Default` / `PrimaryKey` / `Unique`); tables and foreign-key targets
are referenced by name. Migrations are self-contained — they don't import your
schema catalog — and target PostgreSQL or MySQL through the same code.

Migrations form a **DAG**: each carries a `Revision` id and the `DownRevision`
it builds on (`""` for the first), and the apply order is resolved from those
links — not from declaration or filename order. Branches off a shared parent
coexist as separate heads, and `migrate.NewRevision()` mints collision-resistant
ids.

```go
import (
    "github.com/aita/sqlkit/migrate"
    "github.com/aita/sqlkit/sqldef"
)

var migrations = []migrate.Migration{
    {
        Revision:     "0001", // unique id; migrate.NewRevision() generates one
        DownRevision: "",     // builds on nothing — the first migration
        Name:         "create_tags",
        Up: func(op *migrate.Operator) {
            op.CreateTable("tags",
                sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
                sqldef.Column("name", sqldef.VarChar(64), sqldef.Unique()),
            )
        },
        Down: func(op *migrate.Operator) { op.DropTable("tags") },
    },
}

m := migrate.New(db, migrations)
err := m.Up(ctx)                    // apply all pending, each in its own transaction
// m.UpTo(ctx, "0001")              // apply a revision and its ancestors
// m.Down(ctx) / m.DownTo(ctx, rev) // roll back ("" rolls back everything)
// m.Status(ctx) / m.Pending(ctx)   // inspect state
// m.History(ctx)                   // the full apply/rollback timeline from the DB
```

Foreign keys reference their target table by name; create them after the table:

```go
Up: func(op *migrate.Operator) {
    op.CreateTable("posts",
        sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
        sqldef.Column("user_id", sqldef.BigInt),
        sqldef.Index("user_id"),
    )
    op.CreateForeignKey("fk_posts_user_id", "posts",
        []string{"user_id"}, "users", []string{"id"})
},
Down: func(op *migrate.Operator) {
    op.DropForeignKey("fk_posts_user_id", "posts")
    op.DropTable("posts")
},
```

The operator's other operations include `RenameTable`, `RenameColumn`,
`AlterColumn(table, col, migrate.TypeTo(...), migrate.SetNullable(...), ...)`,
`CreateUniqueIndex`, `DropIndex`, `DropConstraint`, and `AddCheck`. For a data
backfill use `op.Exec(query)`; for a step the operations don't cover, drop to the
`sql` package with `op.Execute(stmt)`. The first failing operation stops the
migration and is reported.

**Targets.** `Upgrade`/`Downgrade` take a flexible target:

```go
m.Upgrade(ctx, "head")   // or "" — every pending migration (all heads)
m.Upgrade(ctx, "+1")     // the next pending migration
m.Upgrade(ctx, "8f3a")   // a revision (or unique prefix) and its ancestors
m.Downgrade(ctx, "-1")   // the most recently applied migration
m.Downgrade(ctx, "base") // everything
m.Downgrade(ctx, "8f3a") // down to that revision (its descendants are rolled back)
```

`Up`/`Down`/`UpTo`/`DownTo` are thin wrappers (`Up` = `Upgrade(ctx, "head")`,
`Down` = `Downgrade(ctx, "-1")`).

**Branches and merges.** Because each migration names its parent(s), two feature
branches can add migrations off the same `DownRevision`; that leaves two **heads**
(`m.Heads()`). Join them with a merge migration that lists both as parents:

```go
var _ = Registry.Add(migrate.Migration{
    Revision:      "merge_ab",
    DownRevisions: []string{"feature_a", "feature_b"},
    Name:          "merge a and b",
    // Up/Down often empty for a pure merge
})
```

`m.Current(ctx)` reports the revision(s) the database is at; `m.Heads()` the tips
of the DAG.

**Stamp.** `m.Stamp(ctx, revision)` records the database as being at a revision
*without running any migration* — for baselining an existing schema. It writes
history rows so the applied set becomes exactly that revision's ancestors
(`"head"` stamps everything, `"base"` stamps nothing).

**Offline SQL.** `m.UpgradeSQL(from, to)` / `m.DowngradeSQL(from, to)` render the
SQL a range would run, without touching the database:

```go
script, _ := m.UpgradeSQL("base", "head") // CREATE TABLE ...; INSERT INTO schema_migrations ...
```

**History.** The `schema_migrations` table is **append-only**: applying a
migration inserts an `up` row, rolling it back inserts a `down` row (the apply is
not deleted), each with the revision, name, direction, and timestamp. A
revision's current state is its most recent event, so `Status` reports what is
applied while `History` returns the full timeline persisted in the database.

**Transactions.** Each migration runs in a transaction so its changes and its
history row commit together. PostgreSQL DDL is transactional; MySQL commits DDL
implicitly, so a partly-applied MySQL migration cannot be rolled back
automatically. Set `Migration.DisableTx` for statements a transaction rejects
(e.g. PostgreSQL `CREATE INDEX CONCURRENTLY`).

**Splitting migrations across files.** Keep each migration in its own file and
register it with a shared `migrate.Registry`, so adding a migration is dropping
in a file rather than editing a central slice (declaration order does not matter —
`New` resolves the order from the revision DAG):

```go
// migrations/migrations.go
var Registry = migrate.NewRegistry()

// migrations/0001_create_users.go — Add returns the registry, so a blank var registers it
var _ = Registry.Add(migrate.Migration{Revision: "0001", DownRevision: "", Name: "create_users",
    Up: func(op *migrate.Operator) { op.CreateTable("users" /* , sqldef.Column(...) */) }})

// main.go
m := migrations.Registry.NewMigrator(db)
err := m.Up(ctx)
```

See `examples/migrate` for a runnable example.

**CLI.** The `sqlkit` binary runs your migrations as subcommands of
`sqlkit migrate`. Because migrations are Go code, it cannot run them directly, so
it compiles the package you point it at (one that exposes a `Registry`) into a
throwaway program and runs that — no app code of your own required. Point it at
your migrations directory with `--migrations` and give it a connection with
`--dsn` or `DATABASE_URL`:

```sh
export DATABASE_URL=postgres://localhost/app
sqlkit migrate up    --migrations ./migrations   # apply all pending (also: up +1, up <revision>)
sqlkit migrate down  --migrations ./migrations   # roll back the most recent (also: down base, down <rev>)
sqlkit migrate status   --migrations ./migrations   # each migration's applied state
sqlkit migrate current  --migrations ./migrations   # the revision(s) the database is at
sqlkit migrate heads    --migrations ./migrations   # the head revision(s)
sqlkit migrate history  --migrations ./migrations   # the apply/rollback timeline
sqlkit migrate stamp head --migrations ./migrations # mark applied without running
sqlkit migrate sql up base head --migrations ./migrations  # render SQL for a range without running it
sqlkit migrate new "add tags"   --migrations ./migrations  # scaffold a new migration file
sqlkit migrate autogenerate "sync" --catalog ./schema --from-db --migrations ./migrations  # diff a source against the catalog
```

Every command compiles the migrations package, so it needs the Go toolchain and
your database driver in your module's `go.mod` (`--driver`/`--driver-pkg` default
to pgx; pass both for another engine). `sqlkit migrate new` is the exception that
needs no database: it reads the compiled migration set to scaffold a new file (a
fresh revision with the current head as its `DownRevision`; `--merge` joins all
heads).

If you would rather ship your own binary, the same command set is available as
`Migrator.RunCLI(ctx, args)` (a kong CLI under the hood) — build a `Migrator`
from your registry and hand it `os.Args[1:]`:

```go
func main() {
    db, err := sqlkit.Open("pgx", os.Getenv("DATABASE_URL"))
    if err != nil { log.Fatal(err) }
    defer db.SQL().Close()
    if err := migrations.Registry.NewMigrator(db).RunCLI(context.Background(), os.Args[1:]); err != nil {
        log.Fatal(err)
    }
}
```

`migrate.Diff(old, new, dialect)` generates the forward DDL (`[]sql.Statement`)
between two `meta.Metadata` snapshots — added/dropped tables and columns,
type/nullability/default changes (rendered for both PostgreSQL and MySQL),
primary-key membership changes, and index/foreign-key (including
`ON DELETE`/`ON UPDATE`)/check/comment changes, ordered for safety (drop foreign
keys and tables first, add foreign keys last). Renames are reported as drop+add.
Feed its output to a migration with `op.Execute`.

### Autogenerating a migration

`sqlkit migrate autogenerate` writes a migration for you by diffing a **source**
schema against the **target** catalog (the desired state) and rendering the
difference as `Operator` DSL in the new file's `Up` (and a best-effort reverse
`Down`). The source is selectable:

```sh
# Source = a live database (bring the DB up to the catalog — like Alembic's autogenerate)
sqlkit migrate autogenerate "sync schema" \
  --catalog ./schema --from-db --dsn "$DATABASE_URL" \
  --migrations ./migrations

# Source = the catalog as it stood at a git revision (what changed since then)
sqlkit migrate autogenerate "add tags" \
  --catalog ./schema --from-git HEAD~1 --migrations ./migrations

# Preview without writing a file
sqlkit migrate autogenerate "add tags" --catalog ./schema --from-git HEAD --dry-run
```

The generated file links onto the current head (or `--parent`/`--merge`) just
like `migrate new`. A drop+add that is really a rename keeps its data when you
say so: `--rename users.name:full_name` emits `op.RenameColumn`, and
`--rename-table old:new` emits `op.RenameTable`; unhinted single drop/add pairs
of the same type get a `// possible rename` suggestion. `--from-git` builds the
catalog at that revision in a temporary worktree, so the revision must contain a
buildable catalog. As with any autogenerator, **review the result before
applying** — data backfills and risky renames still need a human.

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

### Correlated subqueries

`Exists` / `NotExists` and `InQuery` / `NotInQuery` already accept correlated
subqueries — the inner query just references an outer column with `EqExpr`.
Two more forms cover the rest:

- **Scalar subqueries.** `sqlkit.SubExpr[T](sub)` wraps a single-column,
  single-row subquery as a typed expression of type `T`, usable as a projection
  (`sql.As(...)`) or in a comparison (`EqExpr`/`NeExpr`/`LtExpr`/`LeExpr`/
  `GtExpr`/`GeExpr`). It may be correlated.
- **Quantified comparisons.** `LtAny`/`LeAny`/`GtAny`/`GeAny` and the matching
  `…All` methods render `column <op> ANY/ALL (subquery)`, generalizing
  `InQuery` (`= ANY`) and `NotInQuery` (`<> ALL`) to the ordering operators.

```go
// SELECT id, (SELECT COUNT(*) FROM posts WHERE posts.user_id = users.id) AS post_count
postCount := sqlkit.Select(sql.CountAll()).From(appdb.Posts).
    Where(appdb.Posts.UserID.EqExpr(appdb.Users.ID))

err = db.Select(appdb.Users.ID, sql.As(sqlkit.SubExpr[int64](postCount), "post_count")).
    From(appdb.Users).All(ctx).Scan(&rows)

// WHERE age > (SELECT AVG(age) FROM users)
avg := sqlkit.Select(sql.Avg(appdb.Users.Age)).From(appdb.Users)
err = db.Select().From(appdb.Users).
    Where(appdb.Users.Age.GtExpr(sqlkit.SubExpr[int64](avg))).
    All(ctx).Scan(&users)
```

At the untyped `sql` level these are `sql.ScalarSubquery(query)` and
`sql.Any(left, op, query)` / `sql.All(left, op, query)`.

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

`Join` / `LeftJoin` / `RightJoin` / `FullJoin` all take a source and an ON
predicate; `CrossJoin` takes only a source. A `LATERAL` join lets the joined
subquery reference the rows to its left — wrap the source with `sql.Lateral`:

```go
recent := sqlkit.Select(appdb.Posts.ID).From(appdb.Posts).
    Where(appdb.Posts.UserID.EqExpr(appdb.Users.ID)).Limit(3)

err = db.Select(appdb.Users.ID).From(appdb.Users).
    Join(sql.Lateral(sql.SourceAlias(recent, "r")), sqlkit.Cond(sql.Raw("true"))).
    All(ctx).Scan(&rows)
```

Expressions: `sql.Add` / `Sub` / `Mul` / `Div` / `Mod` build arithmetic
(typed columns also have `Add`/`Sub`/`Mul`/`Div`, handy for `SET n = n + 1`),
`sqlkit.Cast[T](expr, "type")` and `sql.Cast` render `CAST(... AS ...)`, and
`OrderTerm.NullsFirst()` / `NullsLast()` append `NULLS FIRST|LAST` (PostgreSQL):

```go
db.Update(appdb.Counters).
    Set(appdb.Counters.N.SetExpr(appdb.Counters.N.Add(1))).
    Where(appdb.Counters.ID.Eq(id)).Exec(ctx)

db.Select().From(appdb.Users).OrderBy(appdb.Users.Name.Asc().NullsLast())
```

## Relation-aware queries

The query builder above stays at the SQL level: you spell out every join
condition. Generate with `--relations` and the bindings gain foreign-key
**relation metadata** (`sqlkit.Relation` values) that the same `SelectQuery`
consumes — in the spirit of SQLAlchemy 2.0's unified `select()`, there is no
separate query layer. Terminals return the same `sqlkit.Rows`, so DTO mapping
stays yours.

```sh
go run github.com/aita/sqlkit/cmd/sqlkit generate ./schema \
  --name appdb --out appdb --relations
```

Each foreign key generates a relation on both sides — `appdb.Posts.Rels.User`
(belongs-to), `appdb.Users.Rels.Posts` (has-many), and `appdb.Users.Rels.Profile`
(has-one, when the FK is unique). `JoinRel` takes a relation and derives the
`ON` clause from the FK, and the relation exposes the target's typed columns:

```go
// JOIN profiles ON profiles.user_id = users.id — the ON clause comes from the FK.
var authors []MyUserDTO
err := appdb.Users.Q(db).                  // or db.Select().From(appdb.Users)
    Where(appdb.Users.Age.Le(20)).
    JoinRel(appdb.Users.Rels.Profile).     // or LeftJoinRel
    Where(appdb.Users.Rels.Profile.Public.Eq(true)).
    OrderBy(appdb.Users.CreatedAt.Desc()).
    Limit(50).
    All(ctx).Scan(&authors)
```

`Queryer(db)`/`Q(db)` are sugar for `db.Select().From(table)` and take any
`sqlkit.Executor` (a `*Database` or a `*Session`); everything below lives on the
ordinary `SelectQuery`.

Reusable **filter bundles** map their set fields to columns by name (`sqlkit`
tag or snake_case), so a request struct becomes a predicate set:

```go
type UsersFilter struct {
    Age  sqlkit.FilterTerm // sqlkit.Eq/Ne/Lt/Le/Gt/Ge/In/FilterLike/FilterIsNull
    Name sqlkit.FilterTerm
}

err := db.Select().From(appdb.Users).
    Filter(UsersFilter{Age: sqlkit.Le(20)}).   // unset fields are skipped
    All(ctx).Scan(&authors)
```

`Get(ctx, pk...)` is a primary-key lookup on the same builder. **Eager loading**
is declared on the chain with `Preload` and runs at `Scan`: after the base rows
materialize, each relation is fetched in one batched follow-up query and
assigned onto a relation field (matched by name or a `preload:"<Relation>"` tag)
by reflection — avoiding the N+1. Pass the *target table* — the relation from the
`From` table is resolved automatically:

```go
var users []UserWithPosts  // has a `Posts []Post` field
err := db.Select().From(appdb.Users).
    Preload(appdb.Posts). // resolves Users→Posts; SELECT posts WHERE user_id IN (…)
    All(ctx).Scan(&users)
```

When several relations reach the same table (e.g. a self-reference's `Manager`
and `Reports`), pass the relation explicitly with `PreloadRel(appdb.Employees.Rels.Manager)`.

Relations also drive multi-table writes: `Update(t).JoinRel(rel)` and
`Delete(t).UsingRel(rel)`. See `examples/blog` for a runnable end-to-end use.

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

**Bulk inserts.** `BatchSize(n)` splits a `Values` insert into separate `Exec`
statements of at most `n` rows each, keeping large inserts within the
database's bind-parameter and statement-size limits. The returned result sums
`RowsAffected` across batches. Run it inside a `Session` if the batches must
share a transaction.

```go
_, err = db.Insert(appdb.Users).Values(users).BatchSize(1000).Exec(ctx)
```

**Upsert.** `OnConflictDoNothing` / `OnConflictDoUpdate` render PostgreSQL's
`ON CONFLICT` and MySQL's `ON DUPLICATE KEY UPDATE`. In a `DO UPDATE`
assignment, `Column.Excluded()` refers to the row the INSERT proposed
(PostgreSQL `excluded.col`, MySQL `VALUES(col)`). `OnConflictWhere` adds the
optional PostgreSQL predicate. MySQL has no `DO NOTHING` or conflict `WHERE`, so
those error on the MySQL dialect.

```go
_, err = db.Insert(appdb.Users).Values(u).
    OnConflictDoUpdate([]sqlkit.AnyColumn{appdb.Users.Email},
        appdb.Users.Name.SetExpr(appdb.Users.Name.Excluded())).
    Exec(ctx)
```

**Update/delete with other tables.** `Update(...).From(src...)` and
`Delete(...).Using(src...)` add the PostgreSQL `UPDATE ... FROM` /
`DELETE ... USING` source lists, so the SET expressions and WHERE may reference
another table (the SET value here is a typed column of the joined table):

```go
_, err = db.Update(appdb.Posts).
    Set(appdb.Posts.UserID.SetExpr(appdb.Users.ID)).
    From(appdb.Users).
    Where(appdb.Users.Email.Eq("alice@example.com")).
    Exec(ctx)

_, err = db.Delete(appdb.Posts).
    Using(appdb.Users).
    Where(appdb.Users.ID.EqExpr(appdb.Posts.UserID), appdb.Users.Banned.Eq(true)).
    Exec(ctx)
```

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

## Common table expressions (WITH)

`CTE(name)` turns a query into a named common table expression. The returned
value doubles as a FROM/JOIN source and a handle for its output columns (`Col`).
Attach it to a `SELECT`, `UPDATE`, or `DELETE` with `WithCTE`, which emits a
leading `WITH` clause; list CTEs in dependency order (a CTE before any CTE whose
body references it). Bind placeholders stay numbered consistently across the CTE
bodies and the main statement. Pass `sql.Recursive()` and `sql.Columns(...)`
options to configure a CTE.

```go
// WITH "active" AS (SELECT ...) SELECT ... FROM "active"
active := db.Select(appdb.Users.ID).From(appdb.Users).
    Where(appdb.Users.Active.Eq(true)).
    CTE("active")

var ids []int64
err := db.Select(active.Col("id")).
    From(active).
    WithCTE(active).
    All(ctx).Scan(&ids)

// sql.Columns declares an explicit column list: WITH "t" ("org_id", "n") AS (...)
totals := db.Select(appdb.Users.OrgID, sql.As(sql.CountAll(), "n")).
    From(appdb.Users).GroupBy(appdb.Users.OrgID).
    CTE("t", sql.Columns("org_id", "n"))
err = db.Select(totals.Col("org_id"), totals.Col("n")).
    From(totals).
    WithCTE(totals).
    All(ctx).Scan(&dst)

// Recursive CTE: build the body (base UNION ALL recursive term) first, then
// convert with sql.Recursive(). The recursive term references the CTE by name.
base := db.Select(appdb.Nodes.ID, appdb.Nodes.ParentID).From(appdb.Nodes).
    Where(appdb.Nodes.ParentID.IsNull())
step := db.Select(sql.Col("nodes.id"), sql.Col("nodes.parent_id")).From(appdb.Nodes).
    Join(sql.Tbl("tree"), sqlkit.Cond(sql.Eq(sql.Col("nodes.parent_id"), sql.Col("tree.id"))))
tree := base.UnionAll(step).CTE("tree", sql.Recursive(), sql.Columns("id", "parent_id"))
err = db.Select(tree.Col("id")).
    From(tree).
    WithCTE(tree).
    All(ctx).Scan(&dst)

// WithCTE works on UPDATE/DELETE too — reference the CTE from a WHERE subquery:
// WITH "doomed" AS (...) DELETE FROM "users" WHERE "org_id" IN (SELECT ... FROM "doomed")
```

## Subqueries in FROM/JOIN (derived tables)

`As(alias)` turns a query into an aliased subquery (derived table). Like a CTE
the returned value doubles as a FROM/JOIN source and a handle for its output
columns (`Col`), but it is inlined as a parenthesized query at its use site
rather than declared in a leading `WITH`. Reference its columns through the
handle, not the original table columns — the outer query sees the derived
table's alias, not the inner table.

```go
// SELECT "sub"."id" FROM (SELECT ... FROM "users" WHERE ...) AS "sub"
sub := db.Select(appdb.Users.ID).From(appdb.Users).
    Where(appdb.Users.Active.Eq(true)).
    As("sub")

var ids []int64
err := db.Select(sub.Col("id")).From(sub).All(ctx).Scan(&ids)

// A set operation works as a subquery source too.
ids2 := db.Select(appdb.Users.ID).From(appdb.Users).
    UnionAll(db.Select(appdb.Admins.ID).From(appdb.Admins)).
    As("ids")
err = db.Select(ids2.Col("id")).From(ids2).All(ctx).Scan(&dst)
```

Pass a query directly to `From` when you don't need to reference its columns by
alias, or use `sql.Alias(query, alias)` for an ad-hoc aliased source.

The function DSL lives in `sql/func.go`: the generic `sql.Func(name, args...)`
escape hatch plus named builders for the functions the SQL standard, PostgreSQL,
and MySQL share. For JSON, `sql.JSONArray(...)`, `sql.JSONObject(key, val, ...)`,
`sql.JSONArrayAgg(expr)`, and `sql.JSONObjectAgg(key, val)` build the SQL/JSON
standard functions (PostgreSQL 16+, MySQL 5.7+); older PostgreSQL exposes the
classic `json_build_array` / `json_agg` names through `sql.Func`.

Conditional and aggregate builders:

- **Conditional / NULL**: `Coalesce`, `NullIf`, `Greatest`, `Least`.
- **Aggregates**: `Sum`, `Avg`, `Min`, `Max`, `Count` / `CountAll`, `BitAnd`,
  `BitOr`, `StdDev`, `StdDevPop`, `StdDevSamp`, `Variance`, `VarPop`, `VarSamp`,
  and `StringAgg` (see below).
- **Window / ranking** (turn into a window with `.Over()`; MySQL needs 8.0+):
  `RowNumber`, `Rank`, `DenseRank`, `PercentRank`, `CumeDist`, `Ntile`, `Lag`,
  `Lead`, `FirstValue`, `LastValue`, `NthValue`.

A batch of common scalar functions ships as thin `sql.Func` wrappers whose
comma form is identical in the SQL standard, PostgreSQL, and MySQL:

- **String**: `Lower`, `Upper`, `Length` (bytes on MySQL — prefer `CharLength`
  for a portable character count), `CharLength`, `Trim`, `Ltrim`, `Rtrim`,
  `Replace`, `Substring`, `Left`, `Right`, `Reverse`, `Repeat`, `Lpad`, `Rpad`,
  `ConcatWs`, `Md5`, `Ascii`.
- **Numeric**: `Abs`, `Ceil`, `Floor`, `Round`, `Power`, `Sqrt`, `Exp`, `Ln`,
  `Sign`, `Pi`, and the trigonometric `Sin`, `Cos`, `Tan`, `Cot`, `Asin`,
  `Acos`, `Atan`, `Atan2`, `Degrees`, `Radians`.
- **Date/time**: `Now()` (renders `NOW()`), and the niladic keywords
  `CurrentDate()`, `CurrentTime()`, `CurrentTimestamp()` (rendered without
  parentheses, as the standard and PostgreSQL require).

```go
// SELECT UPPER("name"), ROUND("rate", $1) FROM "t"
db.Select(sql.Upper(appdb.Users.Name), sql.Round(appdb.Users.Rate, 2)).From(appdb.Users)
```

A few functions whose spelling diverges get their own portable builders:

- `sql.Extract(field, expr)` renders the standard `EXTRACT(field FROM expr)`,
  which both engines accept, e.g. `sql.Extract("year", appdb.Events.CreatedAt)`.
- `sql.Position(substr, str)` renders the standard `POSITION(substr IN str)` (the
  comma forms `STRPOS` / `LOCATE` diverge, so this is the portable spelling).
- `sql.StringAgg(expr, ", ")` aggregates strings into one delimited value —
  `STRING_AGG(expr, ', ')` on PostgreSQL, `GROUP_CONCAT(expr SEPARATOR ', ')` on
  MySQL. The delimiter is a constant string literal (MySQL's `SEPARATOR` rejects
  a bind placeholder).

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

`sqlkit` introspects both PostgreSQL and MySQL schemas (`inspector`'s
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
