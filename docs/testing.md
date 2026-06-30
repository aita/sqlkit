# Testing (sqltest)

The [`sqltest`](https://pkg.go.dev/github.com/aita/sqlkit/sqltest) package is
sqlkit's toolkit for testing SQL code. It cuts the
boilerplate of seeding fixtures and managing the schema, leaving the connection
(and any test container) to the caller, and it spies on the queries your code
issues. Every helper takes `*testing.T` and fails the test with `t.Fatalf` on
error, so setup code stays linear. It pulls in no database driver or
test-container dependencies.

## Seeding and schema

[`sqltest.New`](https://pkg.go.dev/github.com/aita/sqlkit/sqltest#New) wraps a
`*`[`sqlkit.Database`](https://pkg.go.dev/github.com/aita/sqlkit#Database) you
opened against a disposable test database and returns a
`*`[`sqltest.DB`](https://pkg.go.dev/github.com/aita/sqlkit/sqltest#DB) whose methods reset, isolate, and seed it.
`New` issues no SQL — it only builds the handle — so the schema is applied by an
explicit `SetupSchema` (apply now, drop on `t.Cleanup`), never as a side effect
of construction:

```go
func TestUsers(t *testing.T) {
    md, err := schema.Catalog.Build()
    if err != nil {
        t.Fatal(err)
    }
    db := sqltest.New(openTestDB(t), md)
    db.SetupSchema(t) // drop + create now, dropped again on t.Cleanup

    id := db.InsertID(t, appdb.Users, UserDTO{Email: "a@example.com"})
    db.InsertRows(t, appdb.Posts, PostDTO{UserID: id, Title: "hello"})

    // ... exercise the code under test against the seeded data.
}
```

Constructing a `DB` is the **safety bar**: the destructive operations live only
as `DB` methods, reachable only through a database a test deliberately wrapped as
throwaway, so a `*sqlkit.Database` accidentally wired to a production DSN can
never be dropped or truncated by a test.

What `DB` and the surrounding helpers give you:

- **Schema lifecycle** — `db.SetupSchema` (apply now, drop on `t.Cleanup`), plus
  `db.ApplySchema` (apply, no teardown) and `db.Truncate` to reset within a test.
  Their drop and truncate steps are foreign-key-safe whatever order the tables
  were declared in — children before or after parents, across schemas, or in a
  cycle (`CASCADE` on PostgreSQL, foreign-key checks toggled on MySQL).
- **Isolation** — `db.Tx` gives each case its own transaction, rolled back on
  `t.Cleanup`, so cases that share one schema stay independent and can run with
  `t.Parallel()`. [`Snapshot`](https://pkg.go.dev/github.com/aita/sqlkit/sqltest#Snapshot)
  (and `Fixture.LoadSnapshot`) instead capture the
  seeded state of one session as a `SAVEPOINT` and return a restore function, so
  many *serial* cases reuse one seed by rolling back between them.
- **Data insertion** — `db.InsertRows` / `db.InsertID` / `db.Load` seed through
  auto-commit; the
  [`InsertRows`](https://pkg.go.dev/github.com/aita/sqlkit/sqltest#InsertRows),
  [`InsertID`](https://pkg.go.dev/github.com/aita/sqlkit/sqltest#InsertID),
  [`InsertReturning`](https://pkg.go.dev/github.com/aita/sqlkit/sqltest#InsertReturning),
  and `Fixture.Load`
  functions take any [`sqlkit.Querier`](https://pkg.go.dev/github.com/aita/sqlkit#Querier),
  so they seed into a `Tx` session (rolled
  back with the case) just as well. `InsertID` smooths over the cross-dialect
  wrinkle of reading back a generated key (`RETURNING` on PostgreSQL,
  `Result.LastInsertId` on MySQL).
- **Fixtures** — [`NewFixture`](https://pkg.go.dev/github.com/aita/sqlkit/sqltest#NewFixture) builds a named, ordered set of rows across tables.

For a suite of parallel cases, apply the schema once and give each case its own
rolled-back transaction:

```go
func TestBlog(t *testing.T) {
    db := sqltest.New(openTestDB(t), md)
    db.SetupSchema(t) // created once, dropped after all subtests

    t.Run("publish", func(t *testing.T) {
        t.Parallel()
        s := db.Tx(t) // isolated transaction, rolled back on return
        blog.Load(t, s)
        // ... mutate and assert against s; no other case sees these writes.
    })
}
```

Foreign-key ordering of inserted *rows* is still the caller's responsibility:
insert parent rows before the children that reference them.

### Seeding by column name

`InsertRows` and `Fixture` address rows through a generated `sqlkit.Table`
binding. Code that does not use the builder reaches for
[`InsertMap`](https://pkg.go.dev/github.com/aita/sqlkit/sqltest#InsertMap)
instead, which seeds a table named by string from `column → value` maps through a
plain `database/sql` handle:

```go
sqltest.InsertMap(t, db, sqlkit.PostgreSQLDialect{}, "users",
    map[string]any{"email": "a@example.com", "name": "alice"},
    map[string]any{"email": "b@example.com", "name": "bob"},
)
id := sqltest.InsertMapID(t, db, sqlkit.PostgreSQLDialect{}, "users", "id",
    map[string]any{"email": "c@example.com"})
```

The first argument after `t` is any `Execer` — a `*sql.DB` for auto-commit, or a
`*sql.Tx` a case rolls back for isolation. The dialect selects identifier quoting
and the placeholder style, so one call works on PostgreSQL and MySQL alike, and
[`InsertMapID`](https://pkg.go.dev/github.com/aita/sqlkit/sqltest#InsertMapID)
reads a generated key back the same cross-dialect way `InsertID` does
(`RETURNING` on PostgreSQL, `Result.LastInsertId` on MySQL). On a `NewRaw`
handle the `db.InsertMap` / `db.InsertMapID` methods supply the dialect and
connection for you.

### Fixtures from files

[`LoadFixtures`](https://pkg.go.dev/github.com/aita/sqlkit/sqltest#LoadFixtures)
seeds a directory of per-table `.json` / `.csv` files — the file-based,
language-agnostic counterpart of `Fixture` (which declares rows as Go structs),
in the style of Rails and go-testfixtures:

```go
sqltest.LoadFixtures(t, db, sqlkit.PostgreSQLDialect{}, "testdata/blog")
// testdata/blog/01_users.json, testdata/blog/02_posts.csv → tables users, posts
```

Each file seeds the table named after it. Files load in lexical filename order,
so a numeric prefix controls foreign-key order (parents before children) and is
stripped from the table name, so `01_users.json` loads into `users`. JSON files
are an array of `{column: value}` objects (numbers stay integers when whole,
`null` is `NULL`, a nested array/object seeds a JSON column as text); CSV files
have a header row of column names with the token `\N` meaning `NULL`. Each row
keeps its own column set, so a row that omits a column gets that column's
database default.
[`LoadFixtureFile`](https://pkg.go.dev/github.com/aita/sqlkit/sqltest#LoadFixtureFile)
loads a single file when the table name is decoupled from the filename.

## Query spy

The same package turns a [`querytrace.Trace`](observability.md) into a **query
spy** — [`sqltest.Expect`](https://pkg.go.dev/github.com/aita/sqlkit/sqltest#Expect)`(t, trace)` asserts on a query budget, no writes, no N+1,
or that a specific statement runs an exact number of times:

```go
trace := querytrace.New("GetUserPage")
ctx := querytrace.WithTrace(context.Background(), trace)
service.RenderUserPage(ctx, userID)

sqltest.Expect(t, trace).
    MaxQueries(5).
    NoWrites().
    NoNPlusOne().
    Query(sqltest.Select(), sqltest.Table("users")).Times(1)
```

## Mock database

[`sqltest.NewDB`](https://pkg.go.dev/github.com/aita/sqlkit/sqltest#NewDB) is a programmable mock `*sql.DB` for unit tests that should not
touch a real database — queue up rows and assert on the statements issued:

```go
db, mock := sqltest.NewDB(t)
mock.ExpectQuery(sqltest.Contains("from users")).
    Return(sqltest.NewRows("id", "name").AddRow(1, "alice").AddRow(2, "bob"))

// ... run the code under test against db ...
```

See [Observability](observability.md) for the tracing side that backs the spy.

---

← Back to the [documentation index](README.md).
