# Testing (sqltest)

The `sqltest` package is sqlkit's toolkit for testing SQL code. It cuts the
boilerplate of seeding fixtures and managing the schema, leaving the connection
(and any test container) to the caller, and it spies on the queries your code
issues. Every helper takes `*testing.T` and fails the test with `t.Fatalf` on
error, so setup code stays linear. It pulls in no database driver or
test-container dependencies.

## Seeding and schema

Four groups of seeding helpers:

- **Data insertion** — `InsertRows`, `InsertID`, and `InsertReturning`.
  `InsertID` smooths over the cross-dialect wrinkle of reading back a generated
  key (`RETURNING` on PostgreSQL, `Result.LastInsertId` on MySQL).
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
    sqltest.SetupSchema(t, db, md)

    id := sqltest.InsertID(t, db, appdb.Users, UserDTO{Email: "a@example.com"})
    sqltest.InsertRows(t, db, appdb.Posts, PostDTO{UserID: id, Title: "hello"})

    // ... exercise the code under test against the seeded data.
}
```

Foreign-key ordering is the caller's responsibility: insert parent rows before
the children that reference them.

## Query spy

The same package turns a [`querytrace.Trace`](observability.md) into a **query
spy** — `sqltest.Expect(t, trace)` asserts on a query budget, no writes, no N+1,
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

`sqltest.NewDB` is a programmable mock `*sql.DB` for unit tests that should not
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
