# Query builder

The typed SQL builder is the core of sqlkit: you build statements from typed
columns, the compiler checks them, and results scan into your own structs.
`Users.ID.Eq("foo")` is a compile error, not a runtime SQL error.

Chains are bound to the executor they start from — a
[`Database`](https://pkg.go.dev/github.com/aita/sqlkit#Database) (auto-commit) or
a transaction-scoped [`Session`](https://pkg.go.dev/github.com/aita/sqlkit#Session) — so the terminals (`All`, `One`, `Exec`, …) take
only a context. Every chain also has an unbound form (package-level
[`sqlkit.Select`](https://pkg.go.dev/github.com/aita/sqlkit#Select)/[`Insert`](https://pkg.go.dev/github.com/aita/sqlkit#Insert)/…
and everything in package [`sql`](https://pkg.go.dev/github.com/aita/sqlkit/sql)) that compiles to SQL
text + args with `ToSQL(dialect)` — see [Standalone builder](#standalone-builder).

- [Querying and scanning](#querying-and-scanning)
- [Struct field tags](#struct-field-tags)
- [Predicates and filters](#predicates-and-filters)
- [Joins](#joins)
- [Subqueries](#subqueries)
- [Aggregates, CASE, and window functions](#aggregates-case-and-window-functions)
- [Set operations and CTEs](#set-operations-and-ctes)
- [Writing](#writing)
- [Relation-aware queries](#relation-aware-queries)
- [Raw SQL](#raw-sql)
- [Transactions](#transactions)
- [Auto splitting](#auto-splitting)
- [Standalone builder](#standalone-builder)
- [Compile-time guarantees](#compile-time-guarantees)

> Relations, preload, and the typed filter bundles assume bindings generated
> with `--relations`. See [Schema as code & code generation](schema-codegen.md).

## Querying and scanning

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

[`sqlkit.Star`](https://pkg.go.dev/github.com/aita/sqlkit#Star)`(table)` is the `table.*` select-list item, so you can fetch one
whole table plus a column from another. For a single row, chains offer `First`
(pushes `LIMIT 1`) and `One` (requires exactly one row, else [`ErrTooManyRows`](https://pkg.go.dev/github.com/aita/sqlkit#ErrTooManyRows))
alongside `All`. `Page(ctx, limit, offset)` fetches a window in one call.

Row locking (PostgreSQL/MySQL 8.0+) is `ForUpdate` / `ForShare` (plus
PostgreSQL's `ForNoKeyUpdate` / `ForKeyShare`), refined with the `SkipLocked` /
`NoWait` / `LockOf(tables...)` options — the building block of a queue worker:

```go
err := db.Select().From(appdb.Jobs).Where(appdb.Jobs.Status.Eq("ready")).
    Limit(10).ForUpdate(sqlkit.SkipLocked()).All(ctx).Scan(&jobs)
```

For keyset (cursor) pagination — a page that costs the same at any depth and
does not shift when rows are inserted or deleted — configure a [`Paginator`](https://pkg.go.dev/github.com/aita/sqlkit#Paginator) with
the sort keys and a page size, then walk it with cursors. The first page uses
[`sqlkit.Start()`](https://pkg.go.dev/github.com/aita/sqlkit#Start); each returned [`Page`](https://pkg.go.dev/github.com/aita/sqlkit#Page) hands out the cursor for its neighbours,
which you pass straight back:

```go
pager := sqlkit.NewPaginator(
    sqlkit.KeyDesc(appdb.Users.CreatedAt),
    sqlkit.KeyAsc(appdb.Users.ID), // a unique tie-breaker, so the order is total
).Limit(20)

var users []MyUserDTO
page, err := pager.Paginate(ctx, db.Select().From(appdb.Users), &users, sqlkit.Start())
if page.HasNext {
    page, err = pager.Paginate(ctx, db.Select().From(appdb.Users), &users, page.Next())
}
```

The cursor is opaque and carries its own direction, so it cannot be applied the
wrong way. To cross an HTTP request, render it with `page.Next().String()` and
rebuild it with [`sqlkit.ParseCursor`](https://pkg.go.dev/github.com/aita/sqlkit#ParseCursor)`(token)` (an empty token is the first page) —
treat the string as a token, not a value to parse. The Paginator owns cursor
encoding; supply `WithCodec` to sign or encrypt the envelope. End the keys with a
unique column (typically the primary key) so the order is total.

## Struct field tags

The `sqlkit` tag names the column (`sqlkit:"created_at"`) and `sqlkit:"-"` drops
a field from mapping. Comma-separated options after the name follow
`encoding/json`'s shape and change how a field is *written* or *encoded* — they
apply to `Insert().Values`, `Update().SetStruct`, and `Scan` alike, on generated
DTOs and hand-written structs both:

```go
type Account struct {
    ID        int64          `sqlkit:"id"`
    Settings  map[string]any `sqlkit:"settings,json"`     // ↔ JSON/JSONB column
    Version   int64          `sqlkit:"version,version"`   // optimistic lock
    CreatedAt time.Time      `sqlkit:"created_at,readonly"` // DB-generated; never written
    UpdatedAt time.Time      `sqlkit:"updated_at,touch"`    // set to CURRENT_TIMESTAMP on write
}
```

- **`readonly`** — the field is scanned on read but never written by INSERT or
  UPDATE. Use it for database-generated columns (identity keys, computed
  columns, trigger-maintained timestamps), so one DTO serves both reads and
  writes without the generated columns leaking into your INSERT/UPDATE. Naming
  the column explicitly in `SetStruct(row, T.Col)` overrides the exclusion.
- **`json`** (or **`jsonb`**) — a struct, map, or slice field round-trips a
  JSON/JSONB column: it marshals to JSON on write and unmarshals on read,
  without a hand-written `driver.Valuer`/`sql.Scanner`. A nil pointer, map, or
  slice writes SQL NULL.
- **`version`** — turns `SetStruct` into an optimistic-locked update: the column
  is bumped (`version = version + 1`) and the row is guarded on the field's
  current value, so `Exec` returns [`ErrVersionConflict`](https://pkg.go.dev/github.com/aita/sqlkit#ErrVersionConflict) when another writer
  advanced it first — the tag-driven equivalent of `CheckVersion`.
- **`touch`** — an app-maintained timestamp: the column is set to
  `CURRENT_TIMESTAMP` (inlined, no bind value) on every `Insert().Values` and on
  an implicit `Update().SetStruct`, regardless of the field's value, so an
  `updated_at` stays current without a database trigger or a manual assignment.
  Naming the column explicitly in `SetStruct(row, T.Col)` writes the struct's
  value instead.

## Predicates and filters

Typed columns carry the predicate vocabulary directly: `Eq`/`Ne`/`Lt`/`Le`/
`Gt`/`Ge`, `In`/`NotIn`, `Between`/`NotBetween`, `IsNull`/`IsNotNull`,
`IsDistinctFrom`/`IsNotDistinctFrom` (null-safe, `<=>` on MySQL),
`InQuery`/`NotInQuery` for `IN (subquery)`, and `EqAny`/`NeAll` for PostgreSQL's
array-valued `= ANY($1)` / `<> ALL($1)` (the whole list bound as one array
parameter, so they replace a large `In`/`NotIn` without nearing the bind-param
limit). String columns add the free helpers
[`sqlkit.Like`](https://pkg.go.dev/github.com/aita/sqlkit#Like) / [`NotLike`](https://pkg.go.dev/github.com/aita/sqlkit#NotLike) / [`ILike`](https://pkg.go.dev/github.com/aita/sqlkit#ILike) (case-insensitive on both engines) and
[`sqlkit.SimilarTo`](https://pkg.go.dev/github.com/aita/sqlkit#SimilarTo) / [`NotSimilarTo`](https://pkg.go.dev/github.com/aita/sqlkit#NotSimilarTo) (PostgreSQL/standard only).
[`sqlkit.Exists`](https://pkg.go.dev/github.com/aita/sqlkit#Exists) / [`NotExists`](https://pkg.go.dev/github.com/aita/sqlkit#NotExists) wrap a subquery, and [`sqlkit.And`](https://pkg.go.dev/github.com/aita/sqlkit#And) / [`Or`](https://pkg.go.dev/github.com/aita/sqlkit#Or) / [`Not`](https://pkg.go.dev/github.com/aita/sqlkit#Not)
combine predicates.

[`sqlkit.Match`](https://pkg.go.dev/github.com/aita/sqlkit#Match)`(query, cols...)` is a **portable full-text search** predicate: it
renders as PostgreSQL's `to_tsvector(...) @@ plainto_tsquery(...)` and MySQL's
`MATCH(...) AGAINST(... IN NATURAL LANGUAGE MODE)` — on MySQL the columns must
be covered by a `FULLTEXT` index — and is rejected on the standard target. For a
boolean-mode query (PostgreSQL `websearch_to_tsquery`, MySQL
`IN BOOLEAN MODE`) or a PostgreSQL text-search configuration, drop to the `sql`
package: `sqlkit.Cond(sql.Match(cols...).Against(q).Boolean().WithConfig("english"))`.

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

## Joins

`Join` / `LeftJoin` / `RightJoin` / `FullJoin` take a source and an ON
predicate; `CrossJoin` takes only a source. `JoinUsing` / `LeftJoinUsing` join
on equally named columns (`USING (...)`), and `NaturalJoin` / `NaturalLeftJoin`
match every shared column implicitly. Wrap a source with [`sql.Lateral`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Lateral) for a
`LATERAL` join.

```go
var posts []PostDTO
err := db.Select(appdb.Posts.ID, appdb.Posts.Title).From(appdb.Posts).
    Join(appdb.Users, appdb.Posts.UserID.EqExpr(appdb.Users.ID)).
    Where(appdb.Users.Email.Eq("alice@example.com")).
    All(ctx).Scan(&posts)
```

## Subqueries

`Exists`/`NotExists` and `InQuery`/`NotInQuery` accept correlated subqueries.
Two more forms cover the rest:

- **Scalar subqueries** — [`sqlkit.SubExpr`](https://pkg.go.dev/github.com/aita/sqlkit#SubExpr)`[T](sub)` wraps a single-column,
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

## Aggregates, CASE, and window functions

The `sql` package supplies the expression vocabulary; [`sqlkit.Cond`](https://pkg.go.dev/github.com/aita/sqlkit#Cond) lifts any
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

The function DSL (in `sql`) covers the conditional/NULL helpers ([`Coalesce`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Coalesce),
[`NullIf`](https://pkg.go.dev/github.com/aita/sqlkit/sql#NullIf), [`Greatest`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Greatest), [`Least`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Least)), aggregates ([`Sum`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Sum), [`Avg`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Avg), [`Min`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Min), [`Max`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Max),
[`StringAgg`](https://pkg.go.dev/github.com/aita/sqlkit/sql#StringAgg), …), ranking/window functions ([`Rank`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Rank), [`Lag`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Lag), [`Lead`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Lead), …), portable
string and numeric functions, date/time keywords, and JSON access
([`sql.JSONGet`](https://pkg.go.dev/github.com/aita/sqlkit/sql#JSONGet) / [`sql.JSONGetText`](https://pkg.go.dev/github.com/aita/sqlkit/sql#JSONGetText) with portable path steps; each dialect renders it in its native syntax — PostgreSQL `->`/`->>`, MySQL `JSON_EXTRACT`/`JSON_UNQUOTE`).
[`sql.Func`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Func)`(name, args...)` is the escape hatch for anything else.

Distinct aggregates are spelled [`sql.CountDistinct`](https://pkg.go.dev/github.com/aita/sqlkit/sql#CountDistinct)`(col)` and
[`sql.DistinctFunc`](https://pkg.go.dev/github.com/aita/sqlkit/sql#DistinctFunc)`("SUM", col)`. CASE comes in both forms — searched
([`sql.Case`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Case)`().When(cond, result).Else(...)`) and simple
([`sql.CaseOf`](https://pkg.go.dev/github.com/aita/sqlkit/sql#CaseOf)`(col).When(value, result)…`). Other expression nodes fill out the
vocabulary: unary operators ([`sql.Neg`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Neg) / [`sql.Pos`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Pos) / [`sql.Unary`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Unary)), row
constructors ([`sql.Row`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Row)`(a, b)`) for row-wise comparisons and `IN`, and the
null-safe [`sql.IsDistinctFrom`](https://pkg.go.dev/github.com/aita/sqlkit/sql#IsDistinctFrom) / [`sql.IsNotDistinctFrom`](https://pkg.go.dev/github.com/aita/sqlkit/sql#IsNotDistinctFrom) (typed as
`col.IsDistinctFrom(v)` / `col.IsNotDistinctFrom(v)`), which render as MySQL's
`<=>` there. PostgreSQL arrays use [`sql.Array`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Array)`(...)` for an `ARRAY[...]`
constructor and [`sql.AnyArray`](https://pkg.go.dev/github.com/aita/sqlkit/sql#AnyArray) / [`sql.AllArray`](https://pkg.go.dev/github.com/aita/sqlkit/sql#AllArray) for `op ANY(array)` /
`op ALL(array)` comparisons.

## Set operations and CTEs

[`Union`](https://pkg.go.dev/github.com/aita/sqlkit#Union) / [`UnionAll`](https://pkg.go.dev/github.com/aita/sqlkit#UnionAll) / [`Intersect`](https://pkg.go.dev/github.com/aita/sqlkit#Intersect) / [`IntersectAll`](https://pkg.go.dev/github.com/aita/sqlkit#IntersectAll) / [`Except`](https://pkg.go.dev/github.com/aita/sqlkit#Except) / [`ExceptAll`](https://pkg.go.dev/github.com/aita/sqlkit#ExceptAll)
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
`WithCTE` (works on SELECT/UPDATE/DELETE). Pass [`sql.Recursive()`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Recursive) for a
`WITH RECURSIVE`.

[`sql.Values`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Values)`().Row(...)` builds a standalone `VALUES (...), (...)` query — usable
on its own, as an aliased derived-table source, or as an `INSERT … SELECT`
source.

```go
active := db.Select(appdb.Users.ID).From(appdb.Users).
    Where(appdb.Users.Active.Eq(true)).CTE("active")
err := db.Select(active.Col("id")).From(active).WithCTE(active).All(ctx).Scan(&ids)
```

## Writing

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

> `RETURNING` is PostgreSQL-only; on MySQL it fails to compile with a pointer to
> `Result.LastInsertId`. See [Limitations](limitations.md).

More write forms:

- **INSERT … SELECT** — `Insert(t).Columns(...).FromSelect(query)`.
- **Partial updates from a struct** — `Update(t).SetStruct(row, cols...)` maps a
  struct's fields to columns and emits one `SET` per column.
- **Upsert** — `OnConflict(...)` renders PostgreSQL's `ON CONFLICT` and MySQL's
  `ON DUPLICATE KEY UPDATE` through a fluent chain: `OnConflict(cols...)` /
  `OnConflictConstraint(name)` / `OnConflictExpr(...)` choose the conflict target,
  `DoUpdate(...)` / `DoNothing()` the action, and `Where(...)` adds a predicate;
  `Column.Excluded()` refers to the proposed row. (MySQL has no named-constraint
  target and no `WHERE` on the update — see [Limitations](limitations.md).)
- **MERGE** — `db.Merge(target).Using(source).On(...)` builds an
  `INSERT … ON …`/`MERGE` statement (PostgreSQL 15+; not on MySQL).
- **Cross-table writes** — `Update(t).From(src...)` and `Delete(t).Using(src...)`
  add `UPDATE … FROM` / `DELETE … USING` source lists.
- **Optimistic locking** — `Update(t).CheckVersion(t.Version, current)` guards the
  statement with `WHERE version = current` and bumps `SET version = version + 1`;
  `Exec` returns `ErrVersionConflict` when no row matches, so a concurrent writer
  that already advanced the version is detected rather than silently overwritten.

```go
_, err := db.Update(appdb.Users).
    SetStruct(u, appdb.Users.Name).
    Where(appdb.Users.ID.Eq(u.ID)).
    CheckVersion(appdb.Users.Version, u.Version).
    Exec(ctx)
if errors.Is(err, sqlkit.ErrVersionConflict) {
    // reload the row and retry
}
```

This pairs with the pessimistic row locks (`ForUpdate` / `ForShare`, see
[Querying and scanning](#querying-and-scanning)): take a row lock to serialize
writers in a transaction, or check a version to detect a lost update without
holding a lock.

```go
_, err = db.Insert(appdb.Users).Values(u).
    OnConflict(appdb.Users.Email).
    DoUpdate(appdb.Users.Name.SetExpr(appdb.Users.Name.Excluded())).
    Exec(ctx)
```

## Relation-aware queries

Generate the bindings with `--relations` and each foreign key gains **relation
metadata** the same [`SelectQuery`](https://pkg.go.dev/github.com/aita/sqlkit#SelectQuery) consumes (in the spirit of SQLAlchemy 2.0's
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

### Preload: eager-loading without the N+1

`Preload` is **not** a join. The base query runs first; then, at `Scan`, each
preloaded relation is fetched in **one** follow-up `SELECT … WHERE fk IN (parent
keys)` and the rows are assigned back onto the destination by reflection. The
destination field is matched by the relation name (`Posts`, `User`) or a
`preload:"…"` tag. A to-many relation fills a slice; a to-one relation fills a
struct or pointer.

```go
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

Without the `Preload` call only step 1 runs and `Posts` stays `nil`. A wide
parent set is automatically chunked (selectinload-style, 1000 keys per statement
by default) to stay within the driver's bind-parameter limit.

**Conditional preload** — extra predicates passed to `Preload` are ANDed into the
follow-up `SELECT`, so only matching children load:

```go
err = db.Select().From(appdb.Users).
    Preload(appdb.Posts, appdb.Posts.Published.Eq(true)).
    All(ctx).Scan(&users)
```

**Nested preload** — pass a [`RelPath`](https://pkg.go.dev/github.com/aita/sqlkit#RelPath) of related tables as the target and `Preload`
eager-loads along the whole chain, creating any intermediate level not already
registered; each hop is its own batched query and the conditions apply to the last
table:

```go
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
of the target table; it takes the same conditions. Preload currently supports
single-column foreign keys only — see [Limitations](limitations.md).

Relations also drive multi-table writes (`Update(t).JoinRel(rel)`,
`Delete(t).UsingRel(rel)`) and primary-key lookups (`Get(ctx, pk...)`).

Each binding also exposes write entry points alongside `Query`/`Q`, so a
statement can start from the table the way a `SelectQuery` does — `Insert(ex)`,
`Update(ex)`, and `Delete(ex)` return the bound builders. `Filter` applies to
`Update` and `Delete` too, so one bundle narrows a read or a write:

```go
// One filter bundle, reused across a read and a write.
active := appdb.Users.Age.Le(20)

err := appdb.Users.Q(db).Filter(active).All(ctx).Scan(&authors)
_, err = appdb.Users.Update(db).Filter(active).Set(appdb.Users.Name.Set(&name)).Exec(ctx)
_, err = appdb.Users.Delete(db).Filter(active).Exec(ctx)
```

Generating with `--relations` also emits a typed filter bundle per table — a
`XxxFilter` struct whose fields map to columns and whose **relation fields** carry
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

## Raw SQL

When a query is easier by hand, `RawSQL` runs a raw string (or a built
[`sql.Query`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Query)) and scans the same way.

```go
err := db.RawSQL(ctx, "SELECT * FROM users WHERE id = $1", 42).Scan(&user)
```

## Transactions

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

For a higher-level batch-and-flush pattern over a transaction-scoped session, see
the [`uow`](https://pkg.go.dev/github.com/aita/sqlkit/uow) (Unit of Work)
package.

## Auto splitting

Bulk inserts and large `IN` lists split themselves so a single statement never
exceeds the database's bind-parameter limit (65535 for PostgreSQL and MySQL).

Automatic splitting is **off by default**: pass [`WithAutoChunk`](https://pkg.go.dev/github.com/aita/sqlkit#WithAutoChunk)`(true)` to enable
it, after which a bulk insert sizes itself to that limit. With it off you can
still split a single insert by hand with `BatchSize(n)`.

```go
db, err := sqlkit.Open("pgx", dsn, sqlkit.WithAutoChunk(true))
// 100k rows × 5 columns is split into statements of ≤ 65535 parameters.
_, err = db.Insert(appdb.Users).Values(users).Exec(ctx)
```

The parameter count alone doesn't bound a bulk insert whose rows carry large
string or BLOB values — a few multi-megabyte rows can overflow MySQL's
`max_allowed_packet` with only a handful of parameters. [`WithMaxStatementBytes`](https://pkg.go.dev/github.com/aita/sqlkit#WithMaxStatementBytes)`(n)`
adds an estimated-size cap, in bytes, on each split statement, so a bulk insert is
chunked to stay under both the parameter limit and the byte budget (whichever
splits smaller wins), with a single oversized row still sent on its own. The figure
is an estimate, so set it below the server's configured packet limit; it defaults to
off and applies only when auto-splitting is on.

```go
db, _ := sqlkit.Open("mysql", dsn, sqlkit.WithAutoChunk(true), sqlkit.WithMaxStatementBytes(4<<20)) // ~4 MiB
// Rows with large text/BLOB columns split so no statement nears the packet limit.
_, err = db.Insert(appdb.Documents).Values(docs).Exec(ctx)
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
`ORDER BY`, `GROUP BY`, `DISTINCT` (including `DISTINCT ON`), `HAVING`,
`LIMIT`/`OFFSET`, or a window function (`Window`, or an inline `OVER(...)` in
the projection) marks it unsafe to split, so it runs as a single statement and
can still hit the bind limit — batch such queries yourself with
[`sqlkit.InBatches`](https://pkg.go.dev/github.com/aita/sqlkit#InBatches). `NOT IN` is never
split. See [Limitations](limitations.md) for the full rules.

A split `Select` runs its chunks inside one `REPEATABLE READ` transaction by
default so they observe a single consistent snapshot
([`WithBatchReadSnapshot`](https://pkg.go.dev/github.com/aita/sqlkit#WithBatchReadSnapshot)`(false)` turns that off); split writes are always wrapped
in their own atomic transaction. `ToSQL` is unaffected — it always compiles the
full value list into a single statement.

## Standalone builder

sqlkit also works as a pure SQL builder, with execution and scanning handled by
another library. Every builder has an unbound form — the package-level
`sqlkit.Select` / `Insert` / [`Update`](https://pkg.go.dev/github.com/aita/sqlkit#Update) / [`Delete`](https://pkg.go.dev/github.com/aita/sqlkit#Delete) (and everything in package
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
adapter), [`sqlkit.ScanRows`](https://pkg.go.dev/github.com/aita/sqlkit#ScanRows) applies sqlkit's column-to-field mapping to the
result, so the scanning rules stay the same regardless of who ran the query.
Table bindings don't require codegen or a live database — a [`TableInfo`](https://pkg.go.dev/github.com/aita/sqlkit#TableInfo) and
typed columns are enough for SELECT, and the full [`Table`](https://pkg.go.dev/github.com/aita/sqlkit#Table) interface can be
written by hand.

For display, logging, and snapshots, [`sql.CompileInline`](https://pkg.go.dev/github.com/aita/sqlkit/sql#CompileInline)`(dialect, query)` renders
a statement with its values inlined as literals instead of `$N` placeholders, so
it carries no args. It is not for execution — the values are not parameterized —
but it is handy for a readable, copy-pasteable statement. It takes a query built
with the `sql` package:

```go
users := sql.Table{Name: sql.NewIdent("users")}
q := sql.Select(sql.Star()).From(users).Where(sql.Gt(sql.Col("id"), 10))

text, err := sql.CompileInline(sql.PostgreSQLDialect{}, q)
// SELECT * FROM "users" WHERE ("id" > 10)
```

## Compile-time guarantees

What the compiler enforces:

- comparison and `IN` values match the column's Go type ([`Column[T]`](https://pkg.go.dev/github.com/aita/sqlkit#Column) methods take `T`),
- join conditions compare columns of the same type (`EqExpr(TypedExpr[T])`),
- `SET` assignments match the column type (`Column[T].Set(T)`),
- every generated binding satisfies `sqlkit.Table`.

What surfaces at runtime: column-to-field mapping (during `Scan` and
`Insert(...).Values`) and SQL-level errors the type system cannot see
(constraint violations, schema drift, raw SQL fragments).

---

← Back to the [documentation index](README.md).
