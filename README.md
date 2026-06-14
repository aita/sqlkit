# sqlkit

A typed SQL toolkit for Go, built on a single **SQL DSL** — you write SQL as
Go, and the compiler checks it. One notation and one compiler cover both
queries (DML) and schema changes (DDL), and the schema you declare in Go feeds
everything else: the typed query bindings, the migrations, and the generated
DDL.

`Users.ID.Eq("foo")` is a compile error, not a runtime SQL error. You build
statements explicitly and scan results into your own structs.

```go
var users []UserDTO
err := db.Select().From(appdb.Users).
    Where(appdb.Users.ID.Gt(10)).
    OrderBy(appdb.Users.CreatedAt.Desc()).
    All(ctx).Scan(&users)
```

## Features

- **[SQL Builder](#sql-builder)** — a typed, compiler-checked query builder
  covering the full SQL surface: joins, subqueries, CTEs, window functions, set
  operations, upserts, and `RETURNING`. Results scan into your own structs.
- **[Schema as code & code generation](#schema-as-code--code-generation)** —
  declare the schema once in Go (or bootstrap it from a live database), then
  generate the typed table bindings and the DDL from it.
- **[Migrations](#migrations)** — versioned, revision-linked (DAG) up/down
  migrations with full history recorded in the database.
- **[Autogeneration](#autogenerating-migrations)** — write a migration for you
  by diffing your schema against a live database or a past git revision
  (Alembic-style).
- **[Auto splitting](#auto-splitting)** — bulk inserts and large `IN` lists
  split themselves to stay within the database's bind-parameter limit.
- **[Test utility](#test-utility)** — the `dbtest` package cuts the boilerplate
  of seeding fixtures and managing schema in tests.
- **[Standalone builder](#standalone-builder)** — every chain also compiles to
  SQL text + args with `ToSQL(dialect)`, ready for pgx, sqlx, or `database/sql`.
- **[Multiple databases](#multiple-databases)** — the same query builder,
  schema, and migrations target both **PostgreSQL** and **MySQL**; portable
  schema types also resolve for **SQLite**.

Chains are bound to the executor they start from — a `Database` (auto-commit)
or a transaction-scoped `Session` — so the terminals (`All`, `One`, `Exec`, …)
take only a context.

---

## SQL Builder

### Querying and scanning

Build the query from typed columns; choose the destination at `Scan`. Columns
map to struct fields by `sqlkit` tag, exact field name, or snake_case name, with
embedded structs flattened. Fields implementing `sql.Scanner` receive the column
value directly, and `driver.Valuer` fields work as INSERT/UPDATE values.

```go
// All columns into a slice of your DTO.
var users []UserDTO
err := db.Select().From(appdb.Users).
    Where(appdb.Users.Name.IsNotNull(), sqlkit.Like(appdb.Users.Email, "%@x.com")).
    OrderBy(appdb.Users.CreatedAt.Desc()).
    Limit(20).
    All(ctx).Scan(&users)

// Explicit projections (alias with sql.As).
var pairs []struct {
    ID    int64
    Email string
}
err = db.Select(appdb.Users.ID, appdb.Users.Email).From(appdb.Users).
    All(ctx).Scan(&pairs)

// A scalar slice, a single row, or a count.
var ids []int64
err = db.Select(appdb.Users.ID).From(appdb.Users).All(ctx).Scan(&ids)

var one UserDTO
err = db.Select().From(appdb.Users).Where(appdb.Users.ID.Eq(1)).All(ctx).Scan(&one) // ErrNoRows if none

n, err := db.Select().From(appdb.Users).Where(...).Count(ctx)
```

`sqlkit.Star(table)` is the `table.*` select-list item, so you can fetch one
whole table plus a column from another. For a single row, chains offer `First`
(pushes `LIMIT 1`) and `One` (requires exactly one row, else `ErrTooManyRows`)
alongside `All`. `Page(ctx, limit, offset)` fetches a window in one call.

Row locking (PostgreSQL/MySQL 8.0+) is `ForUpdate` / `ForShare` (plus
PostgreSQL's `ForNoKeyUpdate` / `ForKeyShare`), refined with `SkipLocked` /
`NoWait` and `LockOf(tables...)` — the building block of a queue worker:

```go
err := db.Select().From(appdb.Jobs).Where(appdb.Jobs.Status.Eq("ready")).
    Limit(10).ForUpdate().SkipLocked().All(ctx).Scan(&jobs)
```

### Predicates and filters

Typed columns carry the predicate vocabulary directly: `Eq`/`Ne`/`Lt`/`Le`/
`Gt`/`Ge`, `In`/`NotIn`, `Between`/`NotBetween`, `IsNull`/`IsNotNull`,
`IsDistinctFrom`/`IsNotDistinctFrom` (null-safe, `<=>` on MySQL),
`InQuery`/`NotInQuery` for `IN (subquery)`, and `EqAny`/`NeAll` for PostgreSQL's
array-valued `= ANY($1)` / `<> ALL($1)` (the whole list bound as one array
parameter, so they replace a large `In`/`NotIn` without nearing the bind-param
limit). String columns add the free helpers
`sqlkit.Like` / `NotLike` / `ILike` (case-insensitive on both engines) and
`sqlkit.SimilarTo` / `NotSimilarTo` (PostgreSQL/standard only).
`sqlkit.Exists` / `NotExists` wrap a subquery, and `sqlkit.And` / `Or` / `Not`
combine predicates.

```go
err = db.Select().From(appdb.Users).
    Where(
        appdb.Users.Age.Between(18, 65),
        sqlkit.Exists(sqlkit.Select(appdb.Posts.ID).From(appdb.Posts).
            Where(appdb.Posts.UserID.EqExpr(appdb.Users.ID))),
    ).
    All(ctx).Scan(&users)
```

Reusable **filter bundles** map their set fields to columns by name, so a
request struct becomes a predicate set (unset fields are skipped):

```go
type UsersFilter struct {
    Age  sqlkit.FilterTerm // sqlkit.Eq/Ne/Lt/Le/Gt/Ge/In/FilterLike/FilterIsNull
    Name sqlkit.FilterTerm
}

err := db.Select().From(appdb.Users).
    Filter(UsersFilter{Age: sqlkit.Le(20)}).
    All(ctx).Scan(&users)
```

### Joins

`Join` / `LeftJoin` / `RightJoin` / `FullJoin` take a source and an ON
predicate; `CrossJoin` takes only a source. `JoinUsing` / `LeftJoinUsing` join
on equally named columns (`USING (...)`), and `NaturalJoin` / `NaturalLeftJoin`
match every shared column implicitly. Wrap a source with `sql.Lateral` for a
`LATERAL` join.

```go
var posts []PostDTO
err := db.Select(appdb.Posts.ID, appdb.Posts.Title).From(appdb.Posts).
    Join(appdb.Users, appdb.Posts.UserID.EqExpr(appdb.Users.ID)).
    Where(appdb.Users.Email.Eq("alice@example.com")).
    All(ctx).Scan(&posts)
```

### Subqueries

`Exists`/`NotExists` and `InQuery`/`NotInQuery` accept correlated subqueries.
Two more forms cover the rest:

- **Scalar subqueries** — `sqlkit.SubExpr[T](sub)` wraps a single-column,
  single-row subquery as a typed expression, usable as a projection or in a
  comparison (`EqExpr`/`LtExpr`/…). May be correlated.
- **Quantified comparisons** — `LtAny`/`GtAll`/… render `column <op> ANY/ALL
  (subquery)`.

```go
// SELECT id, (SELECT COUNT(*) FROM posts WHERE posts.user_id = users.id) AS post_count
postCount := sqlkit.Select(sql.CountAll()).From(appdb.Posts).
    Where(appdb.Posts.UserID.EqExpr(appdb.Users.ID))
err = db.Select(appdb.Users.ID, sql.As(sqlkit.SubExpr[int64](postCount), "post_count")).
    From(appdb.Users).All(ctx).Scan(&rows)
```

**Derived tables** — `As(alias)` turns a query into an aliased subquery used
inline in `From`/`JOIN`. The returned value doubles as a source and a handle
for its output columns (`Col`):

```go
sub := db.Select(appdb.Users.ID).From(appdb.Users).
    Where(appdb.Users.Active.Eq(true)).As("sub")
err := db.Select(sub.Col("id")).From(sub).All(ctx).Scan(&ids)
```

### Aggregates, CASE, and window functions

The `sql` package supplies the expression vocabulary; `sqlkit.Cond` lifts any
sql expression into a typed-chain predicate.

```go
// Aggregates with GROUP BY / HAVING.
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

// A reusable window shared by several functions via the SELECT's WINDOW clause.
db.Select(
    sql.As(sql.Over(sql.Sum(appdb.Games.Score)).Named("w"), "running_total"),
    sql.As(sql.Over(sql.Avg(appdb.Games.Score)).Named("w"), "running_avg"),
).From(appdb.Games).
    Window(sql.WindowDef("w").PartitionBy(appdb.Games.Team).OrderBy(appdb.Games.PlayedAt.Asc()))
```

The function DSL (in `sql`) covers the conditional/NULL helpers (`Coalesce`,
`NullIf`, `Greatest`, `Least`), aggregates (`Sum`, `Avg`, `Min`, `Max`,
`StringAgg`, …), ranking/window functions (`Rank`, `Lag`, `Lead`, …), portable
string and numeric functions, date/time keywords, and JSON access
(`sql.JSONGet` / `sql.JSONGetText` with portable path steps). `sql.Func(name,
args...)` is the escape hatch for anything else.

Distinct aggregates are spelled `sql.CountDistinct(col)` and
`sql.DistinctFunc("SUM", col)`. CASE comes in both forms — searched
(`sql.Case().When(cond, result).Else(...)`) and simple
(`sql.CaseOf(col).When(value, result)…`). Other expression nodes fill out the
vocabulary: unary operators (`sql.Neg` / `sql.Pos` / `sql.Unary`), row
constructors (`sql.Row(a, b)`) for row-wise comparisons and `IN`, and the
null-safe `sql.IsDistinctFrom` / `sql.IsNotDistinctFrom` (typed as
`col.IsDistinctFrom(v)` / `col.IsNotDistinctFrom(v)`), which render as MySQL's
`<=>` there. PostgreSQL arrays use `sql.Array(...)` for an `ARRAY[...]`
constructor and `sql.AnyArray` / `sql.AllArray` for `op ANY(array)` /
`op ALL(array)` comparisons.

### Set operations and CTEs

`Union` / `UnionAll` / `Intersect` / `IntersectAll` / `Except` / `ExceptAll`
combine a flat list of SELECTs; `db.Union(...)` binds the result so the operands
need no executor of their own. The `…All` variants keep duplicate rows (the
multiset forms).

```go
err = db.Union(
    sqlkit.Select(appdb.Users.Email).From(appdb.Users).Where(...),
    sqlkit.Select(appdb.Admins.Email).From(appdb.Admins),
).OrderBy(sql.Asc(sql.Col("email"))).All(ctx).Scan(&emails)
```

`CTE(name)` turns a query into a named common table expression; attach it with
`WithCTE` (works on SELECT/UPDATE/DELETE). Pass `sql.Recursive()` for a
`WITH RECURSIVE`.

`sql.Values().Row(...)` builds a standalone `VALUES (...), (...)` query — usable
on its own, as an aliased derived-table source, or as an `INSERT … SELECT`
source.

```go
active := db.Select(appdb.Users.ID).From(appdb.Users).
    Where(appdb.Users.Active.Eq(true)).CTE("active")
err := db.Select(active.Col("id")).From(active).WithCTE(active).All(ctx).Scan(&ids)
```

### Writing

The write builders start from the db handle (or a `Session`) and end with
`Exec(ctx)`. Add an explicit `Returning(...)` to read rows back — only then does
the chain expose `All(ctx).Scan(&dst)`. `Values` accepts structs, pointers, or
slices of either, mapped by the same rules as `Scan`. Columns with a
database-side default are omitted when their value is the Go zero value, so
serial keys and defaults fill in.

```go
var inserted []UserDTO
err := db.Insert(appdb.Users).Values([]UserDTO{u1, u2}).
    Returning(). // all columns; or Returning(appdb.Users.ID)
    All(ctx).Scan(&inserted)

_, err = db.Update(appdb.Users).
    Set(appdb.Users.Name.Set(&name)).
    Where(appdb.Users.ID.Eq(7)).
    Exec(ctx)

var deletedIDs []int64
err = db.Delete(appdb.Posts).Where(appdb.Posts.Published.Eq(false)).
    Returning(appdb.Posts.ID).
    All(ctx).Scan(&deletedIDs)
```

More write forms:

- **INSERT … SELECT** — `Insert(t).Columns(...).FromSelect(query)`.
- **Partial updates from a struct** — `Update(t).SetStruct(row, cols...)` maps a
  struct's fields to columns and emits one `SET` per column.
- **Upsert** — `OnConflictDoNothing` / `OnConflictDoUpdate` render PostgreSQL's
  `ON CONFLICT` and MySQL's `ON DUPLICATE KEY UPDATE`; `Column.Excluded()` refers
  to the proposed row. `OnConflictOnConstraintDoNothing` /
  `OnConflictOnConstraintDoUpdate` target a named constraint (PostgreSQL
  `ON CONFLICT ON CONSTRAINT`).
- **Cross-table writes** — `Update(t).From(src...)` and `Delete(t).Using(src...)`
  add `UPDATE … FROM` / `DELETE … USING` source lists.

```go
_, err = db.Insert(appdb.Users).Values(u).
    OnConflictDoUpdate([]sqlkit.AnyColumn{appdb.Users.Email},
        appdb.Users.Name.SetExpr(appdb.Users.Name.Excluded())).
    Exec(ctx)
```

### Relation-aware queries

Generate the bindings with `--relations` and each foreign key gains **relation
metadata** the same `SelectQuery` consumes (in the spirit of SQLAlchemy 2.0's
unified `select()`). `JoinRel` derives the `ON` clause from the FK, and `Preload`
eager-loads a relation in one batched follow-up query to avoid the N+1.

```go
// JOIN profiles ON profiles.user_id = users.id — ON clause comes from the FK.
err := appdb.Users.Q(db).
    Where(appdb.Users.Age.Le(20)).
    JoinRel(appdb.Users.Rels.Profile).
    Where(appdb.Users.Rels.Profile.Public.Eq(true)).
    All(ctx).Scan(&authors)
```

#### Preload: eager-loading without the N+1

`Preload` is **not** a join. The base query runs first; then, at `Scan`, each
preloaded relation is fetched in **one** follow-up `SELECT … WHERE fk IN (parent
keys)` and the rows are assigned back onto the destination by reflection. The
destination field is matched by the relation name (`Posts`, `User`) or a
`preload:"…"` tag. A to-many relation fills a slice; a to-one relation fills a
struct or pointer.

```go
// The destination's relation fields are populated by Preload; without a Preload
// call they are simply left zero — no follow-up query runs for them.
type UserWithPosts struct {
    ID    int64 `sqlkit:"id"`
    Name  string
    Posts []Post // filled by Preload(appdb.Posts) — matched by field name "Posts"
}
type Post struct {
    ID     int64 `sqlkit:"id"`
    UserID int64 `sqlkit:"user_id"`
    Title  string
}

var users []UserWithPosts
err = db.Select().From(appdb.Users).Preload(appdb.Posts).All(ctx).Scan(&users)
// 1) SELECT id, name, … FROM users
// 2) SELECT id, user_id, title FROM posts WHERE posts.user_id IN ($1, $2, …)
//    — one batched query for every user, then grouped onto each user's Posts.
```

Without the `Preload` call only step 1 runs and `Posts` stays `nil` — the related
`SELECT` is never issued. A wide parent set is automatically chunked
(selectinload-style, 1000 keys per statement by default) to stay within the
driver's bind-parameter limit.

**Conditional preload** — extra predicates passed to `Preload` are ANDed into the
follow-up `SELECT`, so only matching children load:

```go
err = db.Select().From(appdb.Users).
    Preload(appdb.Posts, appdb.Posts.Published.Eq(true)).
    All(ctx).Scan(&users)
// 2) SELECT … FROM posts WHERE posts.published = $1 AND posts.user_id IN ($2, …)
```

**Nested preload** — pass a `RelPath` of related tables as the target and `Preload`
eager-loads along the whole chain, creating any intermediate level not already
registered; each hop is its own batched query and the conditions apply to the last
table:

```go
type UserGraph struct {
    ID    int64 `sqlkit:"id"`
    Posts []PostWithComments // filled by the Posts hop
}
type PostWithComments struct {
    ID       int64 `sqlkit:"id"`
    UserID   int64 `sqlkit:"user_id"`
    Comments []Comment // filled by the Comments hop
}
type Comment struct {
    ID      int64 `sqlkit:"id"`
    PostID  int64 `sqlkit:"post_id"`
    Body    string
}

var graph []UserGraph
err = db.Select().From(appdb.Users).
    Preload(sqlkit.RelPath(appdb.Posts, appdb.Comments), appdb.Comments.Approved.Eq(true)).
    All(ctx).Scan(&graph)
// 1) SELECT … FROM users
// 2) SELECT … FROM posts    WHERE posts.user_id IN (user ids)
// 3) SELECT … FROM comments WHERE comments.approved = $1 AND comments.post_id IN (post ids)
```

When more than one relation reaches the same table (e.g. a self-reference's
`Manager` and `Reports`), pass the relation explicitly with `PreloadRel` instead
of the target table; it takes the same conditions.

Relations also drive multi-table writes (`Update(t).JoinRel(rel)`,
`Delete(t).UsingRel(rel)`) and primary-key lookups (`Get(ctx, pk...)`).

Each binding also exposes write entry points alongside `Query`/`Q`, so a
statement can start from the table the way a `SelectQuery` does — `Insert(ex)`,
`Update(ex)`, and `Delete(ex)` return the bound `Insert`/`Update`/`Delete`
builders, mirroring `ex.Insert(t)`/`ex.Update(t)`/`ex.Delete(t)`. And `Filter` —
the reusable filter bundle that narrows a `SelectQuery` — now applies to
`Update` and `Delete` too, so one bundle narrows a read or a write:

```go
// One filter bundle, reused across a read and a write.
active := appdb.Users.Age.Le(20)

err := appdb.Users.Q(db).Filter(active).All(ctx).Scan(&authors)

_, err = appdb.Users.Update(db).Filter(active).
    Set(appdb.Users.Name.Set(&name)).Exec(ctx)

_, err = appdb.Users.Delete(db).Filter(active).Exec(ctx)
```

Generating with `--relations` also emits a typed filter bundle per table — a
`XxxFilter` struct whose fields map to columns (holding a `FilterTerm` like
`sqlkit.Eq(…)`/`sqlkit.Le(…)` or a `Predicate`) and whose **relation fields** carry
a nested filter on the related table. A relation filter compiles to a correlated
`EXISTS`, so it narrows a `SELECT`, `UPDATE`, or `DELETE` without a join (and never
duplicates rows for a to-many relation):

```go
// Users who have a published post — EXISTS over the Posts relation.
err := appdb.Users.Q(db).Filter(appdb.UsersFilter{
    Age:   sqlkit.Le(20),
    Posts: &appdb.PostsFilter{Published: sqlkit.Eq(true)},
}).All(ctx).Scan(&authors)
// SELECT … FROM users WHERE "users"."age" <= $1
//   AND EXISTS (SELECT 1 FROM posts WHERE posts.user_id = users.id AND posts.published = $2)
```

### Raw SQL

When a query is easier by hand, `RawSQL` runs a raw string (or a built
`sql.Query`) and scans the same way.

```go
err := db.RawSQL(ctx, "SELECT * FROM users WHERE id = $1", 42).Scan(&user)
```

### Transactions

`WithSession` runs a callback in one transaction — commit on nil return,
rollback on error or panic:

```go
err := db.WithSession(func(session *sqlkit.Session) error {
    if _, err := session.Insert(appdb.Users).Values(u).Exec(ctx); err != nil {
        return err
    }
    return nil
})
```

For manual control, `NewSession` hands you the session directly. Either way the
transaction begins lazily — nothing is opened until the first statement runs, so
an untouched session's `Commit`/`Close` are free no-ops.

---

## Schema as code & code generation

The schema is the source of truth. You author it once — as a `sqldef` catalog
in Go, or bootstrapped from a live database — and generate the typed bindings
and the DDL from it.

### Declaring the schema (sqldef)

Declare the schema as plain Go values, no live database required. A `Catalog` is
the reusable root; `Catalog.Table(...)` returns a value you store and reference
from foreign keys.

```go
package schema

import (
    "github.com/aita/sqlkit/sql"
    "github.com/aita/sqlkit/sqldef"
)

var Catalog = sqldef.NewCatalog()
var Public = Catalog.Schema("public")

var Users = Public.Table("users",
    sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
    sqldef.Column("email", sqldef.VarChar(255), sqldef.Unique()),
    sqldef.Column("name", sqldef.Text, sqldef.Nullable()),
    sqldef.Column("created_at", sqldef.Timestamp, sqldef.Default(sql.Raw("now()"))),
)

var Posts = Public.Table("posts",
    sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
    sqldef.Column("user_id", sqldef.BigInt, sqldef.References(Users, "id")),
    sqldef.Column("title", sqldef.Text),
    sqldef.Column("published", sqldef.Bool, sqldef.Default(sql.Raw("false"))),
    sqldef.Index("user_id"),
    sqldef.Check(sql.Raw("title <> ''")),
)
```

Columns are NOT NULL unless marked `Nullable()` (nullable columns map to pointer
types). Types are **portable** (`BigInt`, `Timestamp`, `VarChar(n)`, …): one
canonical kind resolves to each engine's name, so a single catalog targets
PostgreSQL, MySQL, and SQLite. Foreign keys reference the table value, so the
compiler catches typos; add referential actions with `OnDelete`/`OnUpdate`.
Indexes (`Index`/`UniqueIndex`), constraints (`Unique`/`Check`), and comments
(`Comment`/`TableComment`) round out the catalog and carry through to the
generated code. Because declarations are values, they compose.

### Generating bindings and DDL

Point `sqlkit generate` at the package that declares the catalog, and
`sqlkit ddl` at the same package to render the `CREATE TABLE`/`INDEX`/constraint
statements.

```sh
go run github.com/aita/sqlkit/cmd/sqlkit generate ./schema --name appdb --out appdb
go run github.com/aita/sqlkit/cmd/sqlkit ddl ./schema --out schema.sql
```

The natural home for both is a pair of `go:generate` directives next to the
catalog, so `go generate ./...` rebuilds them (see `examples/blog`):

```go
//go:generate go run github.com/aita/sqlkit/cmd/sqlkit generate ./schema --name blogdb --out blogdb
//go:generate go run github.com/aita/sqlkit/cmd/sqlkit ddl ./schema --out schema.sql
```

The generated binding exposes `Column[T]` fields and the table identity; all
statements start from the db handle with the table as an argument
(`db.Select().From(Users)`, `db.Insert(Users)…`). Pass `--relations` for FK
relation metadata and `--emit-dto` to also generate a row struct per table. For
full control over the SQL→Go type mapping, call the `codegen` library directly.

### Bootstrapping from an existing database

`sqlkit inspect` reads a live **PostgreSQL or MySQL** schema and emits the
catalog source for it, so an existing database can be converted once and
maintained as Go code from then on. The output carries no `DO NOT EDIT` marker
— it is a starting point meant to be edited.

```sh
go run github.com/aita/sqlkit/cmd/sqlkit inspect \
  --dsn "$DATABASE_URL" --schema public --package schema --out schema/schema.go
```

---

## Migrations

The `migrate` package runs schema migrations: each migration's `Up` and `Down`
are callbacks that receive an `Operator` and perform the change with operations
like `op.CreateTable`, `op.AddColumn`, `op.CreateIndex`, and
`op.CreateForeignKey`. Session-scoped tables and views have their own operations
too — `op.CreateTemporaryTable`, and `op.CreateView` / `op.DropView` (the view
body is a `sql` package query, with `OrReplace()` / `ViewColumns(...)` options).
Columns are declared with the same `sqldef` DSL.
Migrations are self-contained — they don't import your schema catalog — and
target PostgreSQL or MySQL through the same code.

Migrations form a **DAG**: each carries a `Revision` id and the `DownRevision`
it builds on (`""` for the first), and the apply order is resolved from those
links — not declaration or filename order. `migrate.NewRevision()` mints
collision-resistant ids.

```go
var migrations = []migrate.Migration{
    {
        Revision:     "0001",
        DownRevision: "",
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
err := m.Up(ctx)   // apply all pending, each in its own transaction
```

`Upgrade` and `Downgrade` take a flexible target, but the all-target keyword
differs by direction:

```go
m.Upgrade(ctx, "head")   // or "" — every pending migration
m.Upgrade(ctx, "+1")     // the next pending migration
m.Upgrade(ctx, "8f3a")   // a revision (or unique prefix) and its ancestors
m.Downgrade(ctx, "-1")   // the most recently applied migration
m.Downgrade(ctx, "base") // or "" — every applied migration ("head" is not accepted here)
m.Downgrade(ctx, "8f3a") // down to that revision (its descendants are rolled back)
```

`Up` / `Down` / `UpTo` / `DownTo` are thin wrappers. Other operations include
`RenameTable`, `RenameColumn`, `AlterColumn`, `CreateUniqueIndex`, `DropIndex`,
`DropConstraint`, and `AddCheck`; drop to the `sql` package with `op.Execute` for
anything they don't cover, and use `op.Exec` for a data backfill.

Other capabilities:

- **History** — the `schema_migrations` table is **append-only**: applying
  inserts an `up` row, rolling back inserts a `down` row. `Status` reports what
  is applied; `History` returns the full timeline.
- **Branches and merges** — two feature branches can add migrations off the same
  parent, leaving two **heads** (`m.Heads()`); join them with a merge migration
  that lists both as `DownRevisions`.
- **Stamp** — `m.Stamp(ctx, rev)` records the database as being at a revision
  without running any migration (for baselining an existing schema).
- **Offline SQL** — `m.UpgradeSQL(from, to)` / `m.DowngradeSQL(from, to)` render
  the SQL a range would run, without touching the database.
- **Transactions** — each migration runs in a transaction; set
  `Migration.DisableTx` for statements a transaction rejects (e.g. PostgreSQL
  `CREATE INDEX CONCURRENTLY`).

**Splitting across files.** Register each migration with a shared
`migrate.Registry`, so adding a migration is dropping in a file rather than
editing a central slice (order is still resolved from the DAG):

```go
// migrations/migrations.go
var Registry = migrate.NewRegistry()

// migrations/0001_create_users.go
var _ = Registry.Add(migrate.Migration{Revision: "0001", DownRevision: "", Name: "create_users",
    Up: func(op *migrate.Operator) { op.CreateTable("users" /* … */) }})

// main.go
m := migrations.Registry.NewMigrator(db)
```

**CLI.** The `sqlkit` binary runs your migrations as subcommands of
`sqlkit migrate`. Because migrations are Go code, it compiles the package you
point it at (one that exposes a `Registry`) and runs that — no app code of your
own required.

```sh
export DATABASE_URL=postgres://localhost/app
sqlkit migrate up      --migrations ./migrations   # apply all pending (also: up +1, up <rev>)
sqlkit migrate down    --migrations ./migrations   # roll back the most recent
sqlkit migrate status  --migrations ./migrations   # each migration's applied state
sqlkit migrate history --migrations ./migrations   # the apply/rollback timeline
sqlkit migrate stamp head --migrations ./migrations
sqlkit migrate new "add tags" --migrations ./migrations  # scaffold a new migration file
```

If you would rather ship your own binary, the same command set is available as
`Migrator.RunCLI(ctx, args)`. See `examples/migrate` for a runnable example.

### Autogenerating migrations

`sqlkit migrate autogenerate` writes a migration for you by diffing a **source**
schema against the **target** catalog (the desired state) and rendering the
difference as `Operator` DSL in the new file's `Up` (and a best-effort reverse
`Down`). The source is selectable:

```sh
# Source = a live database (bring the DB up to the catalog — like Alembic's autogenerate)
sqlkit migrate autogenerate "sync schema" \
  --catalog ./schema --from-db --dsn "$DATABASE_URL" --migrations ./migrations

# Source = the catalog as it stood at a git revision (what changed since then)
sqlkit migrate autogenerate "add tags" \
  --catalog ./schema --from-git HEAD~1 --migrations ./migrations

# Preview without writing a file
sqlkit migrate autogenerate "add tags" --catalog ./schema --from-git HEAD --dry-run
```

The diff covers added/dropped tables and columns, type/nullability/default
changes, primary-key and index/foreign-key/check/comment changes, ordered for
safety. A drop+add that is really a rename keeps its data when you say so
(`--rename users.name:full_name`, `--rename-table old:new`); unhinted drop/add
pairs get a `// possible rename` suggestion. As with any autogenerator, **review
the result before applying** — data backfills and risky renames still need a
human. The same diff is available as a library via `migrate.Diff(old, new,
dialect)`.

---

## Auto splitting

Bulk inserts and large `IN` lists split themselves so a single statement never
exceeds the database's bind-parameter limit (the driver's `BindParamLimiter`
capability — 65535 for PostgreSQL and MySQL).

Automatic splitting is **on by default**: a bulk insert sizes itself to that
limit with no extra configuration. `WithAutoChunk(false)` turns it off, and you
can still split a single insert by hand with `BatchSize(n)`.

```go
db, err := sqlkit.Open("pgx", dsn)
// 100k rows × 5 columns is split into statements of ≤ 65535 parameters.
_, err = db.Insert(appdb.Users).Values(users).Exec(ctx)
```

The same limit chunks a large `IN` list. When a column `In` is the sole `Where`
predicate and its values would overflow the limit, `Select.All` runs one
statement per chunk and concatenates the rows, while `Update`/`Delete` `Exec` run
the chunks atomically and sum `RowsAffected`:

```go
// ids may hold far more than 65535 values; the query splits as needed.
var users []appdb.User
err = db.Select().From(appdb.Users).Where(appdb.Users.ID.In(ids...)).
    All(ctx).Scan(&users)
```

A `Select` is chunked only when its `IN` list is the whole query. Adding
`ORDER BY`, `GROUP BY`, `DISTINCT`, `HAVING`, or `LIMIT`/`OFFSET` marks it
unsafe to split (those clauses can't be combined across chunks), so it runs as a
single statement and can still hit the bind limit — batch such queries yourself
with `sqlkit.InBatches`.

A split `Select` runs its chunks inside one `REPEATABLE READ` transaction by
default so they observe a single consistent snapshot
(`WithBatchReadSnapshot(false)` turns that off); split writes are always wrapped
in their own atomic transaction. `ToSQL` is unaffected — it always compiles the
full value list into a single statement.

---

## Test utility

The `dbtest` package cuts the boilerplate of seeding fixtures and managing the
schema in tests, leaving the connection (and any test container) to the caller.
Every helper takes `*testing.T` and fails the test with `t.Fatalf` on error, so
setup code stays linear. It pulls in no database driver or test-container
dependencies.

Four groups of helpers:

- **Data insertion** — `Insert`, `InsertID`, and `InsertReturning`. `InsertID`
  smooths over the cross-dialect wrinkle of reading back a generated key
  (`RETURNING` on PostgreSQL, `Result.LastInsertId` on MySQL).
- **Fixtures** — `NewFixture` builds a named, ordered set of rows across tables;
  `Load` seeds them.
- **Schema lifecycle** — `SetupSchema` (apply now, drop on `t.Cleanup`), the
  lower-level `ApplySchema`, and `Truncate`.
- **Snapshots** — `Snapshot` (and `Fixture.LoadSnapshot`) capture the seeded
  state of a session as a `SAVEPOINT` and return a restore function, so many
  cases reuse one seed by rolling back between them instead of reloading it.

```go
func TestUsers(t *testing.T) {
    db := openTestDB(t)
    md, err := schema.Catalog.Build()
    if err != nil {
        t.Fatal(err)
    }
    dbtest.SetupSchema(t, db, md)

    id := dbtest.InsertID(t, db, appdb.Users, UserDTO{Email: "a@example.com"})
    dbtest.Insert(t, db, appdb.Posts, PostDTO{UserID: id, Title: "hello"})

    // ... exercise the code under test against the seeded data.
}
```

Foreign-key ordering is the caller's responsibility: insert parent rows before
the children that reference them.

---

## Standalone builder

sqlkit also works as a pure SQL builder, with execution and scanning handled by
another library. Every builder has an unbound form — the package-level
`sqlkit.Select` / `Insert` / `Update` / `Delete` (and everything in package
`sql`) — and compiles to SQL text + bind args with `ToSQL(dialect)`. Nothing in
that path touches a database, and it needs no database driver.

```go
query := sqlkit.Select(appdb.Users.ID, appdb.Users.Email).
    From(appdb.Users).
    Where(appdb.Users.ID.Gt(10)).
    OrderBy(appdb.Users.ID.Asc())

sqlText, args, err := query.ToSQL(sql.PostgreSQLDialect{}) // or sql.MySQLDialect{}

rows, err := pool.Query(ctx, sqlText, args...)                // pgx
err = sqlxDB.SelectContext(ctx, &users, sqlText, args...)     // sqlx
rows, err := stdDB.QueryContext(ctx, sqlText, args...)        // database/sql
```

When execution goes through `database/sql` (including sqlx and pgx's stdlib
adapter), `sqlkit.ScanRows` applies sqlkit's column-to-field mapping to the
result, so the scanning rules stay the same regardless of who ran the query.
Table bindings don't require codegen or a live database — a `TableInfo` and
typed columns are enough for SELECT, and the full `Table` interface can be
written by hand.

---

## Multiple databases

The same query builder, schema, and migrations target both PostgreSQL and
MySQL; PostgreSQL is the default and the most exercised dialect. `sql.Dialect`
is the single point where engine differences are resolved — identifier quoting,
placeholders, capability flags, and DDL rendering — so both the query compiler
and the schema model's `CreateSQL`/`DropSQL` run through it. The whole query
builder compiles for MySQL too (backtick quoting, `?` placeholders), and
`Open` infers the dialect from the driver name (`"mysql"` → MySQL, otherwise
PostgreSQL; override with `WithDialect`).

Engine-specific gaps surface at compile time: since MySQL has no `RETURNING`,
`Returning(...)` fails to compile on the MySQL dialect with a pointer to
`Result.LastInsertId`. The portable `sqldef` types additionally resolve for
SQLite, so a schema declared once can render DDL for all three.

---

## Interceptors

Interceptors wrap statement execution, gRPC-style: each receives the `Statement`
about to run and the `next` handler, and decides whether — and with what — to
call it. That covers observing, timing, rewriting SQL, short-circuiting, and
retrying. Register them with `WithInterceptor`; they apply to every read and
write run through the database and any session it opens.

The common case — rewriting SQL or args — needs no `next` plumbing; `Rewrite`
adapts a statement transform:

```go
db, _ := sqlkit.Open("pgx", dsn, sqlkit.WithInterceptor(
    sqlkit.Rewrite(func(ctx context.Context, stmt sqlkit.Statement) (sqlkit.Statement, error) {
        stmt.SQL = "/* tenant=" + tenantOf(ctx) + " */ " + stmt.SQL
        return stmt, nil
    }),
))
```

For the full middleware shape (observing results, timing, retries), implement
`Interceptor` directly and embed `NoopInterceptor` for the side you don't need.

---

## Compile-time guarantees

What the compiler enforces:

- comparison and `IN` values match the column's Go type (`Column[T]` methods take `T`),
- join conditions compare columns of the same type (`EqExpr(TypedExpr[T])`),
- `SET` assignments match the column type (`Column[T].Set(T)`),
- every generated binding satisfies `sqlkit.Table`.

What surfaces at runtime: column-to-field mapping (during `Scan` and
`Insert(...).Values`) and SQL-level errors the type system cannot see
(constraint violations, schema drift, raw SQL fragments).

---

## Development

Unit tests use a scriptable in-memory `database/sql` driver, so `go test ./...`
needs no database. Integration tests run the full stack — builder, scanner, DDL
generator, and inspector — against real PostgreSQL **and** MySQL servers:

```sh
cd internal/integration && go test ./...
```

With no DSN set, the integration suite boots a throwaway container per engine
with [testcontainers](https://golang.testcontainers.org/) (skipping when neither
a DSN nor Docker is available); set `SQLKIT_TEST_POSTGRES_DSN` /
`SQLKIT_TEST_MYSQL_DSN` to point at existing servers instead.
