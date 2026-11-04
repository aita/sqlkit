# Migrations

The [`migrate`](https://pkg.go.dev/github.com/aita/sqlkit/migrate) package runs
schema migrations: each migration's `Up` and `Down`
are callbacks that receive an
[`Operator`](https://pkg.go.dev/github.com/aita/sqlkit/migrate#Operator) and perform the change with operations
like `op.CreateTable`, `op.AddColumn`, `op.CreateIndex`, and
`op.CreateForeignKey`. Session-scoped tables and views have their own operations
too — `op.CreateTemporaryTable`, and `op.CreateView` / `op.DropView` (the view
body is a [`sql`](https://pkg.go.dev/github.com/aita/sqlkit/sql) package query, with
[`OrReplace()`](https://pkg.go.dev/github.com/aita/sqlkit/migrate#OrReplace) /
[`ViewColumns(...)`](https://pkg.go.dev/github.com/aita/sqlkit/migrate#ViewColumns) options).
Columns are declared with the same [`decl`](https://pkg.go.dev/github.com/aita/sqlkit/decl) DSL. Migrations are self-contained —
they don't import your schema catalog — and target PostgreSQL or MySQL through
the same code.

Migrations form a **DAG**: each carries a `Revision` id and the `DownRevision`
it builds on (`""` for the first), and the apply order is resolved from those
links — not declaration or filename order. [`migrate.NewRevision()`](https://pkg.go.dev/github.com/aita/sqlkit/migrate#NewRevision) mints
collision-resistant ids.

```go
var migrations = []migrate.Migration{
    {
        Revision:     "0001",
        DownRevision: "",
        Name:         "create_tags",
        Up: func(op *migrate.Operator) {
            op.CreateTable("tags",
                decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
                decl.Column("name", decl.VarChar(64), decl.Unique()),
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
  `CREATE INDEX CONCURRENTLY`). MySQL commits DDL implicitly and cannot roll a
  partly-applied migration back — see [Limitations](limitations.md).

**Splitting across files.** Register each migration with a shared
[`migrate.Registry`](https://pkg.go.dev/github.com/aita/sqlkit/migrate#Registry), so adding a migration is dropping in a file rather than
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
own required. Install it with `go install github.com/aita/sqlkit/cmd/sqlkit@latest`
(it is a separate module, so its drivers never enter your project's dependencies).

```sh
export DATABASE_URL=postgres://localhost/app
sqlkit migrate up      --migrations ./migrations   # apply all pending (also: up +1, up <rev>)
sqlkit migrate down    --migrations ./migrations   # roll back the most recent
sqlkit migrate status  --migrations ./migrations   # each migration's applied state
sqlkit migrate history --migrations ./migrations   # the apply/rollback timeline
sqlkit migrate stamp head --migrations ./migrations
sqlkit migrate new "add tags" --migrations ./migrations  # scaffold a new migration file
```

If you would rather ship your own binary, the same command set is available
without `migrate` pulling in a CLI dependency: [`migrate.NewCLI()`](https://pkg.go.dev/github.com/aita/sqlkit/migrate#NewCLI) returns the
command grammar and `CLI.Dispatch(m.Runner(ctx), args)` parses and runs it
directly, so a minimal binary needs no third-party parser. If you want richer
`--help` and flag handling, hand `migrate.NewCLI()` to a parser like kong in
your own program and run the result against `m.Runner(ctx)`. See
[`examples/migrate`](../examples/migrate) for a runnable example.

## Autogenerating migrations

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
human. With `--from-db` on PostgreSQL the source also includes the schema's
enum types (`CREATE TYPE ... AS ENUM`), except those owned by an extension. An
enum the catalog doesn't declare is dropped only when no catalog column still
uses it by type name — `op.DropEnum` cascades, so dropping a type a column
depends on would drop the column too. The same diff is available as a library via [`migrate.Diff`](https://pkg.go.dev/github.com/aita/sqlkit/migrate#Diff)`(old, new,
dialect)`.

## Migrations across backends

Migrations are not routed: a [`migrate.Migrator`](https://pkg.go.dev/github.com/aita/sqlkit/migrate#Migrator) already targets one `*`[`Database`](https://pkg.go.dev/github.com/aita/sqlkit#Database)
and keeps its own `schema_migrations` history, so you apply them per writable
backend yourself (the Alembic `--name`/env-loop model, not a framework router).
Replicas are skipped — they receive DDL through replication.

```go
for _, db := range writableBackends { // exclude replicas
    if err := registry.NewMigrator(db).Up(ctx); err != nil {
        return err
    }
}
```

Homogeneous shards run the same migration set on each; for vertically
partitioned backends (different tables per database) keep a separate `Registry`
per group. See [Query routing](routing.md) for the read/write split on the query
side.

---

← Back to the [documentation index](README.md).
