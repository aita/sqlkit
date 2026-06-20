# Getting started

This walkthrough takes you end to end: declare a schema in Go, generate typed
bindings and DDL from it, apply it, and run typed queries. It mirrors the
runnable [`examples/blog`](../examples/blog) program — read that for the complete,
compiling version.

## 1. Install the toolkit

Add the library to your module:

```sh
go get github.com/aita/sqlkit
```

The `sqlkit` command (codegen, DDL, inspect, migrate) lives in a **separate
module** so its database drivers never enter your dependency graph. Run it on
demand with `go run …@latest`, or install the binary once:

```sh
go install github.com/aita/sqlkit/cmd/sqlkit@latest
```

## 2. Declare the schema

Author the schema as plain Go values with the `decl` package — no live database
required. Foreign keys reference their target by name and resolve at `Build`.

```go
// schema/schema.go
package schema

import (
    "github.com/aita/sqlkit/decl"
    "github.com/aita/sqlkit/sql"
)

var Catalog = decl.NewCatalog()
var Public = Catalog.Schema("public")

var Users = Public.Table("users",
    decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
    decl.Column("email", decl.VarChar(255), decl.Unique()),
    decl.Column("name", decl.Text, decl.Nullable()),
    decl.Column("created_at", decl.Timestamp, decl.Default(sql.Raw("now()"))),
)

var Posts = Public.Table("posts",
    decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
    decl.Column("user_id", decl.BigInt, decl.References("users", "id")),
    decl.Column("title", decl.Text),
    decl.Column("published", decl.Bool, decl.Default(sql.Raw("false"))),
    decl.Index("user_id"),
)
```

See [Schema as code & code generation](schema-codegen.md) for the full `decl`
vocabulary.

## 3. Generate bindings and DDL

Drop a pair of `go:generate` directives next to the catalog and run
`go generate ./...`:

```go
//go:generate go run github.com/aita/sqlkit/cmd/sqlkit@latest generate ./schema --name blogdb --out blogdb --relations
//go:generate go run github.com/aita/sqlkit/cmd/sqlkit@latest ddl ./schema --out schema.sql
```

- `generate` writes `blogdb/tables.go` — the typed `Users`/`Posts` bindings with
  `Column[T]` fields. `--relations` adds the FK metadata that powers `JoinRel`,
  `Preload`, and typed filter bundles.
- `ddl` writes `schema.sql` — the `CREATE TABLE`/index/constraint statements.

Apply the DDL (or use [migrations](migrations.md) once the schema starts
evolving):

```sh
createdb blog
psql blog -f schema.sql
```

## 4. Open a database and write typed queries

```go
db, err := sqlkit.Open("pgx", os.Getenv("DATABASE_URL"))
if err != nil {
    log.Fatal(err)
}
defer db.Close()

// INSERT ... RETURNING the generated id.
var created []blogdb.User
err = db.Insert(blogdb.Users).Values(blogdb.User{Email: "alice@example.com"}).
    Returning().
    All(ctx).Scan(&created)

// A typed SELECT with a join, scanned into your own struct.
type PostRow struct {
    ID    int64
    Title string
}
var posts []PostRow
err = db.Select(blogdb.Posts.ID, blogdb.Posts.Title).From(blogdb.Posts).
    Join(blogdb.Users, blogdb.Posts.UserID.EqExpr(blogdb.Users.ID)).
    Where(blogdb.Users.Email.Eq("alice@example.com")).
    OrderBy(blogdb.Posts.ID.Desc()).
    All(ctx).Scan(&posts)
```

`blogdb.Users.ID.Eq("foo")` would not compile — the column is `int64`. See the
[Query builder](query-builder.md) guide for predicates, subqueries, window
functions, upserts, preload, and transactions.

## 5. Eager-load relations without the N+1

With `--relations`, `Preload` fetches a relation in one batched follow-up query:

```go
type UserWithPosts struct {
    ID    int64 `sqlkit:"id"`
    Email string
    Posts []PostRow // filled by Preload — matched by field name
}

var users []UserWithPosts
err = db.Select().From(blogdb.Users).Preload(blogdb.Posts).All(ctx).Scan(&users)
```

## 6. Test it

The [`sqltest`](testing.md) package seeds fixtures, manages the schema, and spies
on the queries your code issues — no driver or container dependency of its own:

```go
func TestService(t *testing.T) {
    db := openTestDB(t)
    md, _ := schema.Catalog.Build()
    sqltest.SetupSchema(t, db, md)

    id := sqltest.InsertID(t, db, blogdb.Users, blogdb.User{Email: "a@example.com"})
    sqltest.InsertRows(t, db, blogdb.Posts, blogdb.Post{UserID: id, Title: "hello"})
    // ... exercise the code under test.
}
```

## Where to next

- [Query builder](query-builder.md) — the full DSL.
- [Schema as code & code generation](schema-codegen.md) — `decl`, codegen, and
  custom type mapping.
- [Migrations](migrations.md) — evolving the schema over time.
- [Limitations](limitations.md) — dialect gaps and the rules to know up front.

---

← Back to the [documentation index](README.md).
