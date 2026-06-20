# Limitations

sqlkit deliberately favors an explicit, portable core over covering every
engine-specific corner. This page collects the known limitations and the
workarounds, so you can tell ahead of time what the builder will and won't do.

Most engine gaps surface **at compile time of the SQL** (when `ToSQL`/`Exec`
runs against a dialect), as a descriptive error rather than malformed SQL.

## Dialect support

| Engine | Status |
| --- | --- |
| **PostgreSQL** | Primary target; the default and the most exercised dialect. Full feature surface. |
| **MySQL** | Supported with a subset of features (see below). Backtick quoting, `?` placeholders. |
| Standard | A minimal reference dialect; many features are rejected on it. |
| SQLite | **Not currently available.** The earlier SQLite dialect was withdrawn and its implementation is being reworked; target PostgreSQL or MySQL for now. |

`Open` infers the dialect from the driver name (`"mysql"` → MySQL, otherwise
PostgreSQL); override with `WithDialect`.

## Feature support by dialect

- ✅ supported
- ⚠️ supported with a caveat (see **Notes**)
- ❌ not supported (rejected at compile time with a descriptive error, not
  malformed SQL)

| Feature | PostgreSQL | MySQL | Notes |
| --- | :---: | :---: | --- |
| `RETURNING` (read rows back from a write) | ✅ | ❌ | MySQL: `Returning(...)` won't compile; use `Result.LastInsertId` (`sqltest.InsertID` smooths it over in tests). |
| Upsert | ✅ | ⚠️ | PG `ON CONFLICT`; MySQL `ON DUPLICATE KEY UPDATE` — no named-constraint target (`OnConflictConstraint`), no `WHERE` on the update. |
| `MERGE` | ✅ | ❌ | PostgreSQL 15+. `WHEN NOT MATCHED` can only `INSERT` / do nothing — never `UPDATE`/`DELETE` (both engines). |
| Row locks `ForUpdate` / `ForShare` | ✅ | ✅ | MySQL 8.0+. Refine with `SkipLocked`/`NoWait`/`LockOf`. |
| `ForKeyShare` / `ForNoKeyUpdate` | ✅ | ❌ | Extra PostgreSQL lock strengths. |
| Arrays — `sql.Array`, `EqAny`/`NeAll`, `decl.Array` | ✅ | ❌ | MySQL has no array type. |
| JSON path access — `sql.JSONGet` / `JSONGetText` | ✅ | ❌ | The `json`/`jsonb` struct-tag round-trip (whole-value marshal) works on both. |
| Full-text search — `sqlkit.Match` | ✅ | ⚠️ | MySQL: the columns need a `FULLTEXT` index. Boolean mode / a text-search config needs the `sql` package (below). |
| Regex match — `~` / `~*` | ✅ | ❌ | MySQL can't express a case-insensitive regex inline (collation-dependent). |
| `SIMILAR TO` / `NOT SIMILAR TO` | ✅ | ❌ | SQL-standard pattern matching. |
| `DISTINCT ON (col)` | ✅ | ❌ | No MySQL equivalent. |
| Multi-table `UPDATE … FROM` / `DELETE … USING` | ✅ | ⚠️ | Some shapes error ("multi-table UPDATE/DELETE is unsupported for this dialect"). |
| Aliased `VALUES` derived-table source | ✅ | ⚠️ | MySQL can't alias `VALUES` columns, so sqlkit renders it as a `UNION ALL` of `SELECT`s (equivalent result, different SQL text). |
| Schema introspection — `introspect.Schema` | ✅ | ✅ | Constructs the `meta` model can't represent are skipped on import. |
| Transactional (atomic) migrations | ✅ | ❌ | MySQL commits DDL implicitly and can't roll a partly-applied migration back — see [Migrations](#migrations). |

The **standard** dialect is a minimal reference target and rejects most of the
engine-specific features above. **SQLite** is not currently available (see the
table under [Dialect support](#dialect-support)).

A few features have an escape hatch for what the portable helper can't reach:

- **Full-text** — for a boolean-mode query (PostgreSQL `websearch_to_tsquery`,
  MySQL `IN BOOLEAN MODE`) or a PostgreSQL text-search configuration, drop to the
  `sql` package: `sql.Match(cols...).Against(q).Boolean().WithConfig("english")`.
- **`RETURNING` on MySQL** — read a generated key back with `Result.LastInsertId`.

## Auto-splitting and batching

Splitting keeps a single statement under the driver's bind-parameter limit
(65535 for PostgreSQL and MySQL). The rules:

- **Off by default.** Bulk-insert auto-splitting needs `WithAutoChunk(true)` on
  the `Database`; otherwise split by hand with `BatchSize(n)`. `ToSQL` always
  compiles the full value list into one statement regardless.
- **`IN` splits only when it is the whole query.** A `Select` whose sole `Where`
  is a column `In(...)` splits and concatenates rows. Adding `ORDER BY`,
  `GROUP BY`, `DISTINCT`, `HAVING`, or `LIMIT`/`OFFSET` marks it **unsafe to
  split** (results can't be combined across chunks): it runs as a single
  statement and can still hit the bind limit. Batch those yourself with
  `sqlkit.InBatches`.
- **`NOT IN` is never split.** A row is excluded only if it appears in *no*
  chunk, which splitting cannot guarantee — a very large `NOT IN` must fit in one
  statement. Express the complement as an anti-join or `NOT EXISTS` instead, or
  use `NeAll` (PostgreSQL array form).
- **Preload chunking** is fixed-window — 1000 parent keys per follow-up `SELECT`
  by default (`WithPreloadBatchSize(n)`), for query-plan-cache friendliness.

## Preload

- **Single-column foreign keys only.** Preloading a relation backed by a
  composite key errors ("composite-key relations are not supported yet").
- A **to-many** destination field must be a **slice**; a to-one fills a struct or
  pointer. The field is matched by relation name or a `preload:"…"` tag and must
  map to the target key column.

## Migrations

- **MySQL migrations are not atomic.** MySQL commits DDL implicitly and cannot
  roll a partly-applied migration back; treat each migration as non-atomic.
  PostgreSQL DDL is transactional and rolls back cleanly on failure.
- **`DisableTx`** is required for statements a transaction rejects — e.g.
  PostgreSQL `CREATE INDEX CONCURRENTLY`, or a sequence with `OWNED BY` that
  cannot run in the same transaction block before it is used.
- **Autogenerate is best-effort.** The diff orders operations for safety and
  suggests possible renames, but data backfills and risky renames still need a
  human — **review the result before applying.** A column with no resolved type
  on the target dialect, or a default expression that cannot be parsed, makes the
  diff fail rather than guess.

## Code generation

- **Every SQL type needs a Go mapping.** A column whose database type has no
  built-in mapping is a generation error. Supply a `TypeMapperConfig.Overrides`
  (by type name) or `ColumnOverrides` (by `table.column`), or pin a Go type on
  the `decl` column.
- **Exact numerics default to `shopspring/decimal`.** `decimal`/`numeric` columns
  map to `github.com/shopspring/decimal` because `float64` cannot scan them
  without losing precision; the import lands in *your* generated package (the
  root sqlkit module stays dependency-free). Override the type to opt out.
- **Foreign keys outside the catalog** are emitted only as a comment — declare
  cross-catalog references by hand.

## SQL parser (sqlparse)

`sqlparse` inverts the build-then-compile flow, turning SQL text back into
`sql` AST nodes. Two facts shape its limits:

- **Two backends, two modules.** `sqlparse/postgres` (real PostgreSQL grammar via
  `pg_query_go`, cgo) and `sqlparse/mysql` (the TiDB parser, pure Go) are
  **separate modules** so their heavy dependency trees never reach the root
  module that library consumers import.
- **Not every construct round-trips.** Each backend recognizes the full grammar
  but only maps the constructs the `sql` AST can model; anything else either
  falls back to a raw passthrough node or returns an `UnsupportedError` naming the
  construct. Examples that do not model to typed nodes include partial/`INCLUDE`/
  concurrent indexes, materialized-view bodies, `INSERT … DEFAULT VALUES`,
  `WITH ROLLUP`, some window-frame and multi-table-`UPDATE`/`DELETE` shapes, and
  `RETURNING` on the MySQL grammar. Treat the parser as a high-coverage importer,
  not a total one.

## Introspection

`introspect.Schema` reads **PostgreSQL and MySQL** live schemas. Constructs the
`meta` metadata model has no type for are skipped on import — introspection
yields a faithful but not byte-exact model of the database.

## Other

- **Keyset pagination needs a unique tail key.** End a `Paginator`'s sort keys
  with a unique column (typically the primary key) so the order is total and a
  cursor lands on exactly one row.
- **Filter terms** support `Eq`/`Ne`/`Lt`/`Le`/`Gt`/`Ge`/`In`/`FilterLike`/
  `FilterIsNull` or a raw `Predicate`; other `FilterTerm` values error.
- **`decl` targets PostgreSQL and MySQL** only; other dialects ("oracle", …)
  error.
- **A primary-key column cannot be nullable** in a `decl` catalog.
- **Unit of Work nesting requires a transaction.** Nested `uow.Scope`s are
  savepoint-backed, so a nested flush over a non-transactional executor returns
  `ErrNestingRequiresTx` — open a `*sqlkit.Session` first.

---

← Back to the [documentation index](README.md).
