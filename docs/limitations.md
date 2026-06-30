# Limitations

sqlkit deliberately favors an explicit, portable core over covering every
engine-specific corner. This page collects the known limitations and the
workarounds, so you can tell ahead of time what the builder will and won't do.

Most engine gaps surface **at compile time of the SQL** (when `ToSQL`/`Exec`
runs against a dialect), as a descriptive error rather than malformed SQL.

Each limitation is tagged with its nature, so you can tell a permanent constraint
from a gap that may close:

- 🔒 **Inherent** — a database-engine constraint or a deliberate design choice.
  Treat it as permanent and design around it.
- 🚧 **Not yet** — a current implementation gap, not a fundamental one; a future
  release could lift it.

## Dialect support

| Engine | Status |
| --- | --- |
| **PostgreSQL** | Primary target; the default and the most exercised dialect. Full feature surface. |
| **MySQL** | Supported with a subset of features (see below). Backtick quoting, `?` placeholders. |
| **SQLite** | Supported with a subset of features (see below). Double-quoted identifiers, `?` placeholders, `INTEGER PRIMARY KEY AUTOINCREMENT`. No bundled driver — pair it with `modernc.org/sqlite` (pure Go) or `mattn/go-sqlite3`. |
| **libSQL** | The Turso SQLite fork. SQL-compatible with SQLite, so it shares SQLite's feature surface and limitations; engine-specific extensions (vector types, `ALTER COLUMN`) are not modeled yet. |
| Standard | A minimal reference dialect; many features are rejected on it. |

`Open` infers the dialect from the driver name (`"mysql"` → MySQL,
`"sqlite"`/`"sqlite3"` → SQLite, `"libsql"` → libSQL, otherwise PostgreSQL);
override with `WithDialect`.

## Feature support by dialect

- ✅ supported
- ⚠️ supported with a caveat (see **Notes**)
- ❌ not supported (rejected at compile time with a descriptive error, not
  malformed SQL)

libSQL mirrors the SQLite column throughout (it is SQLite-compatible).

| Feature | PostgreSQL | MySQL | SQLite | Notes |
| --- | :---: | :---: | :---: | --- |
| `RETURNING` (read rows back from a write) | ✅ | ❌ | ✅ | MySQL: `Returning(...)` won't compile; use `Result.LastInsertId` ([`sqltest.InsertID`](https://pkg.go.dev/github.com/aita/sqlkit/sqltest#InsertID) smooths it over in tests). SQLite ≥ 3.35. |
| Upsert | ✅ | ⚠️ | ✅ | PG/SQLite `ON CONFLICT` (incl. `excluded.<col>` and a `WHERE` on the update); MySQL `ON DUPLICATE KEY UPDATE` — no named-constraint target (`OnConflictConstraint`), no `WHERE` on the update. SQLite has no `ON CONSTRAINT` target either. |
| `MERGE` | ✅ | ❌ | ❌ | PostgreSQL 15+. `WHEN NOT MATCHED` can only `INSERT` / do nothing — never `UPDATE`/`DELETE`. |
| Row locks `ForUpdate` / `ForShare` | ✅ | ✅ | ❌ | MySQL 8.0+. SQLite serializes writers and has no row-locking clause. Refine PG/MySQL with [`SkipLocked`](https://pkg.go.dev/github.com/aita/sqlkit#SkipLocked)/[`NoWait`](https://pkg.go.dev/github.com/aita/sqlkit#NoWait)/[`LockOf`](https://pkg.go.dev/github.com/aita/sqlkit#LockOf). |
| `ForKeyShare` / `ForNoKeyUpdate` | ✅ | ❌ | ❌ | Extra PostgreSQL lock strengths. |
| Arrays — [`sql.Array`](https://pkg.go.dev/github.com/aita/sqlkit/sql#Array), `EqAny`/`NeAll`, [`decl.Array`](https://pkg.go.dev/github.com/aita/sqlkit/decl#Array) | ✅ | ❌ | ❌ | Only PostgreSQL has a first-class array type. |
| JSON path access — [`sql.JSONGet`](https://pkg.go.dev/github.com/aita/sqlkit/sql#JSONGet) / [`JSONGetText`](https://pkg.go.dev/github.com/aita/sqlkit/sql#JSONGetText) | ✅ | ❌ | ✅ | PG/SQLite share the `->` / `->>` operators (SQLite ≥ 3.38). 🚧 MySQL has JSON path functions; sqlkit just doesn't map to them yet. The `json` struct-tag round-trip works on all. |
| Full-text search — [`sqlkit.Match`](https://pkg.go.dev/github.com/aita/sqlkit#Match) | ✅ | ⚠️ | ❌ | MySQL: the columns need a `FULLTEXT` index. SQLite's full-text is the separate FTS5 extension, not modeled here. |
| Regex match — `~` / `~*` | ✅ | ❌ | ⚠️ | SQLite renders `REGEXP` but needs a host-registered `regexp()` function (it ships none) and can't express a case-insensitive match inline. MySQL can't either (collation-dependent). |
| `SIMILAR TO` / `NOT SIMILAR TO` | ✅ | ❌ | ❌ | SQL-standard pattern matching. |
| `DISTINCT ON (col)` | ✅ | ❌ | ❌ | No MySQL/SQLite equivalent. |
| Multi-table `UPDATE … FROM` / `DELETE … USING` | ✅ | ⚠️ | ⚠️ | SQLite renders `UPDATE … FROM` (≥ 3.33) but has no `DELETE … USING`. Some MySQL shapes error; MySQL also rejects `ORDER BY` / `LIMIT` on a multi-table UPDATE/DELETE, so sqlkit errors at compile time rather than emit SQL the engine refuses. |
| Aliased `VALUES` derived-table source | ✅ | ⚠️ | ✅ | PG/SQLite use the standard `(VALUES …) AS alias(cols)` form; MySQL can't alias `VALUES` columns, so sqlkit renders a `UNION ALL` of `SELECT`s (equivalent result, different SQL text). |
| Schema introspection — [`introspection.Schema`](https://pkg.go.dev/github.com/aita/sqlkit/introspection#Schema) | ✅ | ✅ | ❌ | 🚧 No SQLite introspector yet. Constructs the [`meta`](https://pkg.go.dev/github.com/aita/sqlkit/meta) model can't represent are skipped on import. |
| Transactional (atomic) migrations | ✅ | ❌ | ✅ | MySQL commits DDL implicitly and can't roll a partly-applied migration back — see [Migrations](#migrations). PostgreSQL and SQLite have transactional DDL. |

The matrix records engine *capabilities*, so a ❌/⚠️ cell is 🔒 inherent (the
target engine can't express it) unless its Notes say 🚧.

The **standard** dialect is a minimal reference target and rejects most of the
engine-specific features above.

A few features have an escape hatch for what the portable helper can't reach:

- **Full-text** — for a boolean-mode query (PostgreSQL `websearch_to_tsquery`,
  MySQL `IN BOOLEAN MODE`) or a PostgreSQL text-search configuration, drop to the
  `sql` package: `sql.Match(cols...).Against(q).Boolean().WithConfig("english")`.
- **`RETURNING` on MySQL** — read a generated key back with `Result.LastInsertId`.

## Auto-splitting and batching

Splitting keeps a single statement under the driver's bind-parameter limit
(65535 for PostgreSQL and MySQL). These rules are all 🔒 **by design** — they
trade convenience for correctness:

- **Off by default.** Bulk-insert auto-splitting needs [`WithAutoChunk`](https://pkg.go.dev/github.com/aita/sqlkit#WithAutoChunk)`(true)` on
  the [`Database`](https://pkg.go.dev/github.com/aita/sqlkit#Database); otherwise split by hand with `BatchSize(n)`. `ToSQL` always
  compiles the full value list into one statement regardless.
- **`IN` splits only when it is the whole query.** A `Select` whose sole `Where`
  is a column `In(...)` splits and concatenates rows. Adding `ORDER BY`,
  `GROUP BY`, `DISTINCT`, `HAVING`, or `LIMIT`/`OFFSET` marks it **unsafe to
  split** (results can't be combined across chunks): it runs as a single
  statement and can still hit the bind limit. Batch those yourself with
  [`sqlkit.InBatches`](https://pkg.go.dev/github.com/aita/sqlkit#InBatches).
- **`NOT IN` is never split.** A row is excluded only if it appears in *no*
  chunk, which splitting cannot guarantee — a very large `NOT IN` must fit in one
  statement. Express the complement as an anti-join or `NOT EXISTS` instead, or
  use `NeAll` (PostgreSQL array form).
- **Preload chunking** is fixed-window — 1000 parent keys per follow-up `SELECT`
  by default ([`WithPreloadBatchSize`](https://pkg.go.dev/github.com/aita/sqlkit#WithPreloadBatchSize)`(n)`), for query-plan-cache friendliness.

## Preload

- 🚧 **Single-column foreign keys only.** Preloading a relation backed by a
  composite key errors ("composite-key relations are not supported yet").
- 🔒 A **to-many** destination field must be a **slice**; a to-one fills a struct
  or pointer. The field is matched by relation name or a `preload:"…"` tag and
  must map to the target key column.

## Migrations

- 🔒 **MySQL migrations are not atomic.** MySQL commits DDL implicitly and cannot
  roll a partly-applied migration back; treat each migration as non-atomic.
  PostgreSQL DDL is transactional and rolls back cleanly on failure.
- 🔒 **`DisableTx`** is required for statements a transaction rejects — e.g.
  PostgreSQL `CREATE INDEX CONCURRENTLY`, or a sequence with `OWNED BY` that
  cannot run in the same transaction block before it is used.
- 🔒 **Autogenerate is best-effort.** The diff orders operations for safety and
  suggests possible renames, but data backfills and risky renames still need a
  human — **review the result before applying.** A column with no resolved type
  on the target dialect, or a default expression that cannot be parsed, makes the
  diff fail rather than guess.

## Code generation

- 🔒 **Every SQL type needs a Go mapping.** A column whose database type has no
  built-in mapping is a generation error. Supply a [`TypeMapperConfig`](https://pkg.go.dev/github.com/aita/sqlkit/codegen#TypeMapperConfig)`.Overrides`
  (by type name) or `ColumnOverrides` (by `table.column`), or pin a Go type on
  the [`decl`](https://pkg.go.dev/github.com/aita/sqlkit/decl) column.
- 🔒 **Exact numerics default to `shopspring/decimal`.** `decimal`/`numeric`
  columns map to `github.com/shopspring/decimal` because `float64` cannot scan
  them without losing precision; the import lands in *your* generated package (the
  root sqlkit module stays dependency-free). Override the type to opt out.
- 🚧 **Foreign keys outside the catalog** are emitted only as a comment — declare
  cross-catalog references by hand for now.

## SQL parser (sqlparse)

[`sqlparse`](https://pkg.go.dev/github.com/aita/sqlkit/sqlparse) inverts the
build-then-compile flow, turning SQL text back into
`sql` AST nodes. Two facts shape its limits:

- 🔒 **Three backends, three modules.** [`sqlparse/postgres`](https://pkg.go.dev/github.com/aita/sqlkit/sqlparse/postgres) (real PostgreSQL
  grammar via `pg_query_go`), [`sqlparse/mysql`](https://pkg.go.dev/github.com/aita/sqlkit/sqlparse/mysql) (the TiDB parser, pure Go), and
  [`sqlparse/sqlite`](https://pkg.go.dev/github.com/aita/sqlkit/sqlparse/sqlite) (the `rqlite/sql` parser, pure Go; also parses libSQL)
  are **separate modules** so their heavy dependency trees never reach the root
  module that library consumers import. `pg_query_go` embeds `libpg_query`, so
  the PostgreSQL backend **requires cgo** and a C toolchain to build; the MySQL
  and SQLite backends are pure Go.
- 🔒 **The SQLite backend is a high-coverage grammar, not the canonical one.**
  `rqlite/sql` is a hand-written parser based on the SQLite SQL definition, not
  SQLite's own Lemon grammar; a few valid statements it does not accept (and
  libSQL-specific syntax) fail to parse rather than round-trip. For this reason
  the SQLite backend is **not** wired into the round-trip oracle, which validates
  each dialect's output against the engine's real grammar.
- 🚧 **Not every construct round-trips.** Each backend recognizes the full grammar
  but only maps the constructs the `sql` AST can model. When a converter meets a
  construct it cannot represent it reports an [`UnsupportedError`](https://pkg.go.dev/github.com/aita/sqlkit/sqlparse#UnsupportedError); the backend
  then **deparses just that statement back to SQL and keeps it as a
  [`sql.RawSQL`](https://pkg.go.dev/github.com/aita/sqlkit/sql#RawSQL) passthrough**, so every statement the grammar accepts still yields a
  usable `Query`. Only a genuine syntax error (not an `UnsupportedError`) fails
  the whole `Parse` call. Inspect a result's concrete type to tell a typed node
  from a raw fallback. Examples that do not model to typed nodes include
  partial/`INCLUDE`/concurrent indexes, materialized-view bodies,
  `INSERT … DEFAULT VALUES`, `WITH ROLLUP`, `RIGHT`/`FULL` joins combined with
  `NATURAL`/`USING`, an unaliased subquery source, non-constant `LIMIT`/`OFFSET`,
  some window-frame and multi-table-`UPDATE`/`DELETE` shapes, and `RETURNING` on
  the MySQL grammar. Treat the parser as a high-coverage importer, not a total one.
- 🔒 **The raw fallback is reformatted and dialect-bound.** A deparsed `RawSQL` is
  normalized by the backend's printer: **comments and the original whitespace,
  quoting, and keyword casing are not preserved.** It is rendered in its source
  dialect and carries no structure, so re-compiling it against a *different*
  dialect re-emits that text verbatim rather than translating it.
- 🚧 **MySQL positions are reconstructed.** `pg_query_go` reports real statement
  locations, but the TiDB parser records no usable node offset, so the MySQL
  backend computes each statement's position from the running length of the
  preceding statements' verbatim text and recovers table offsets heuristically.

## Introspection

🚧 `introspection.Schema` reads **PostgreSQL and MySQL** live schemas. Constructs
the `meta` metadata model has no type for are skipped on import — introspection
yields a faithful but not byte-exact model of the database. (Coverage grows as
the `meta` model gains types.)

## Other

- 🔒 **Keyset pagination needs a unique tail key.** End a [`Paginator`](https://pkg.go.dev/github.com/aita/sqlkit#Paginator)'s sort keys
  with a unique column (typically the primary key) so the order is total and a
  cursor lands on exactly one row.
- 🔒 **Filter terms** support `Eq`/`Ne`/`Lt`/`Le`/`Gt`/`Ge`/`In`/[`FilterLike`](https://pkg.go.dev/github.com/aita/sqlkit#FilterLike)/
  [`FilterIsNull`](https://pkg.go.dev/github.com/aita/sqlkit#FilterIsNull) or a raw [`Predicate`](https://pkg.go.dev/github.com/aita/sqlkit#Predicate); other [`FilterTerm`](https://pkg.go.dev/github.com/aita/sqlkit#FilterTerm) values error.
- 🚧 **`decl` targets PostgreSQL, MySQL, SQLite, and libSQL**; other dialects
  ("oracle", …) error.
- 🔒 **A primary-key column cannot be nullable** in a `decl` catalog.
- 🔒 **Unit of Work nesting requires a transaction.** Nested [`uow.Scope`](https://pkg.go.dev/github.com/aita/sqlkit/uow#Scope)s are
  savepoint-backed, so a nested flush over a non-transactional executor returns
  [`ErrNestingRequiresTx`](https://pkg.go.dev/github.com/aita/sqlkit/uow#ErrNestingRequiresTx) — open a `*`[`sqlkit.Session`](https://pkg.go.dev/github.com/aita/sqlkit#Session) first.

---

← Back to the [documentation index](README.md).
