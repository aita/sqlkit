# Schema as code & code generation

The schema is the source of truth. You author it once — as a
[`decl`](https://pkg.go.dev/github.com/aita/sqlkit/decl) catalog in
Go, or bootstrapped from a live database — and generate the typed bindings and
the DDL from it.

- [Table bindings](#table-bindings)
- [Declaring the schema (decl)](#declaring-the-schema-decl)
- [Generating bindings and DDL](#generating-bindings-and-ddl)
- [Customizing generation (codegen as a library)](#customizing-generation-codegen-as-a-library)
- [Bootstrapping from an existing database](#bootstrapping-from-an-existing-database)

## Table bindings

A binding is the typed handle the query builder starts from —
`db.Select().From(Users)`, `Users.ID.Eq(42)`. It is a plain Go value: a
[`sqlkit.TableInfo`](https://pkg.go.dev/github.com/aita/sqlkit#TableInfo) identity,
one [`sqlkit.Column[T]`](https://pkg.go.dev/github.com/aita/sqlkit#Column) field per column, and a small
set of methods the builder reads for metadata (`Info`, `SQLSource`, `Columns`,
`PrimaryKey`, `Defaulted`). There is no ORM and no query-time reflection — the
binding is just data describing the table, which is why `Users.ID.Eq("foo")` is a
compile error: `ID` is a `Column[int64]`.

You normally **generate** bindings from the schema rather than write them — see
[Generating bindings and DDL](#generating-bindings-and-ddl). Hand-writing one is
occasionally useful: to see exactly what the generator emits, or to bind a table
that isn't declared in a catalog. The shape is small:

```go
type UsersTable struct {
    info  sqlkit.TableInfo
    ID    sqlkit.Column[int64]
    Email sqlkit.Column[string]
}

var Users = func() UsersTable {
    info := sqlkit.TableInfo{Name: "users"}
    return UsersTable{
        info:  info,
        ID:    sqlkit.NewColumn[int64](info, "id"),
        Email: sqlkit.NewColumn[string](info, "email"),
    }
}()

func (t UsersTable) Info() sqlkit.TableInfo             { return t.info }
func (t UsersTable) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t UsersTable) Columns() []sqlkit.AnyColumn        { return []sqlkit.AnyColumn{t.ID, t.Email} }
func (t UsersTable) PrimaryKey() []sqlkit.AnyColumn     { return []sqlkit.AnyColumn{t.ID} }
func (t UsersTable) Defaulted() []sqlkit.AnyColumn      { return []sqlkit.AnyColumn{t.ID} }
```

A runnable version is [`examples/minimal`](../examples/minimal). For anything
real, declare the schema once and generate the bindings (and DDL) from it — the
rest of this page.

## Declaring the schema (decl)

Declare the schema as plain Go values, no live database required. A
[`Catalog`](https://pkg.go.dev/github.com/aita/sqlkit/decl#Catalog) is
the reusable root; `Catalog.Table(...)` returns a value you store and reference
from foreign keys.

```go
package schema

import (
    "github.com/aita/sqlkit/sql"
    "github.com/aita/sqlkit/decl"
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
    decl.Check(sql.Raw("title <> ''")),
)
```

Columns are NOT NULL unless marked
[`Nullable()`](https://pkg.go.dev/github.com/aita/sqlkit/decl#Nullable) (nullable columns map to pointer
types). A primary-key column cannot be nullable. Types are **portable**
([`BigInt`](https://pkg.go.dev/github.com/aita/sqlkit/decl#BigInt),
[`Timestamp`](https://pkg.go.dev/github.com/aita/sqlkit/decl#Timestamp),
[`VarChar(n)`](https://pkg.go.dev/github.com/aita/sqlkit/decl#VarChar), …): one canonical kind resolves to each
engine's name, so a single catalog targets PostgreSQL and MySQL. Foreign keys
name their target table (`References("users", "id")`, or `"schema.users"` to
cross schemas) and are resolved when the catalog `Build`s, so a table may
reference one declared after it or itself; add referential actions with
[`OnDelete`](https://pkg.go.dev/github.com/aita/sqlkit/decl#OnDelete)/[`OnUpdate`](https://pkg.go.dev/github.com/aita/sqlkit/decl#OnUpdate).
Indexes ([`Index`](https://pkg.go.dev/github.com/aita/sqlkit/decl#Index)/[`UniqueIndex`](https://pkg.go.dev/github.com/aita/sqlkit/decl#UniqueIndex)), constraints
([`Unique`](https://pkg.go.dev/github.com/aita/sqlkit/decl#Unique)/[`Check`](https://pkg.go.dev/github.com/aita/sqlkit/decl#Check)),
and comments ([`Comment`](https://pkg.go.dev/github.com/aita/sqlkit/decl#Comment)/[`TableComment`](https://pkg.go.dev/github.com/aita/sqlkit/decl#TableComment)) round out the catalog
and carry through to the generated code. Because declarations are values, they
compose.

[`Array(elem)`](https://pkg.go.dev/github.com/aita/sqlkit/decl#Array) declares a PostgreSQL **array** column, e.g.
`decl.Column("tags", decl.Array(decl.Text))` for `text[]`. The DDL renders
`elem[]`, the introspection reads array columns back, and the code generator maps
the column to a Go slice of the element's type (`[]string`, `[]int32`, …). A
slice is already nilable, so a nullable array stays a slice rather than a pointer.
Arrays are a PostgreSQL feature; the column is unavailable when a catalog targets
MySQL (see [Limitations](limitations.md)).

## Generating bindings and DDL

The `sqlkit` command lives in its own module (`github.com/aita/sqlkit/cmd/sqlkit`)
so the database drivers it links — needed by `inspect` to open a live database —
stay out of the root module that library consumers import. Run it with `@latest`
(or install it once with `go install github.com/aita/sqlkit/cmd/sqlkit@latest` and
call the `sqlkit` binary directly); your project never takes on the drivers as a
dependency.

Point `sqlkit generate` at the package that declares the catalog, and
`sqlkit ddl` at the same package to render the `CREATE TABLE`/`INDEX`/constraint
statements.

```sh
go run github.com/aita/sqlkit/cmd/sqlkit@latest generate ./schema --name appdb --out appdb
go run github.com/aita/sqlkit/cmd/sqlkit@latest ddl ./schema --out schema.sql
```

The natural home for both is a pair of `go:generate` directives next to the
catalog, so `go generate ./...` rebuilds them (see `examples/generated`):

```go
//go:generate go run github.com/aita/sqlkit/cmd/sqlkit@latest generate ./schema --name blogdb --out blogdb
//go:generate go run github.com/aita/sqlkit/cmd/sqlkit@latest ddl ./schema --out schema.sql
```

The generated binding exposes `Column[T]` fields and the table identity; all
statements start from the db handle with the table as an argument
(`db.Select().From(Users)`, `db.Insert(Users)…`). Pass `--relations` for FK
relation metadata (enables `JoinRel`, `Preload`, and the typed filter bundles —
see [Query builder](query-builder.md#relation-aware-queries)) and `--emit-dto`
to also generate a row struct per table. For full control over the SQL→Go type
mapping, call the [`codegen`](https://pkg.go.dev/github.com/aita/sqlkit/codegen) library directly.

## Customizing generation (codegen as a library)

The CLI calls [`codegen.Generate`](https://pkg.go.dev/github.com/aita/sqlkit/codegen#Generate)`(md, `[`codegen.Options`](https://pkg.go.dev/github.com/aita/sqlkit/codegen#Options)`{...})` for you with a fixed
set of options. Call it yourself when you need the parts the flags can't
express — chiefly the SQL→Go **type mapping** and the **DTO naming** scheme.
Build the schema model with the catalog's `Build` (or the inspector), then
generate:

```go
md, _ := schema.Catalog.Build()

files, _ := codegen.Generate(md, codegen.Options{
    PackageName:   "appdb",
    EmitRelations: true,
    EmitDTO:       true,
    // Map SQL types to your own Go types. Overrides apply by SQL type name,
    // ColumnOverrides by (schema.)table.column, OverrideFunc programmatically.
    TypeMapper: codegen.NewDefaultTypeMapper(codegen.TypeMapperConfig{
        Overrides: map[string]codegen.GoType{
            "numeric": {ImportPath: "github.com/shopspring/decimal", Name: "decimal.Decimal"},
        },
        ColumnOverrides: map[string]codegen.GoType{
            "users.id": {ImportPath: "github.com/google/uuid", Name: "uuid.UUID"},
        },
    }),
    // Name the row structs your way ("users" -> "UserRecord").
    DTOName: func(t meta.Table) string { return myName(t.Name) + "Record" },
})

_ = codegen.Write("appdb", files) // or inspect files[i].Content yourself
```

The mapper's hooks are consulted most-specific first (`OverrideFunc`, then
`ColumnOverrides`, then `Overrides`), falling through to the built-in type
tables; nullability is applied on top, so overrides pin the bare type. Exact
numeric (`decimal`/`numeric`) columns map to `shopspring/decimal` by default —
`float64` cannot scan them without losing precision — and the import lands in
your generated package, not the root sqlkit module; override the type to opt out.
A column with a SQL type that has no Go mapping is an error: add an `Overrides`,
`ColumnOverrides`, or pin a Go type in the `decl` column.

A runnable end-to-end version — custom type mapper, programmatic enum types, and
a custom DTO namer — lives in [`examples/custom`](../examples/custom), with its
committed generated output under
[`examples/custom/blogdb`](../examples/custom/blogdb). (To stay dependency-free
it maps to domain types declared in that package rather than `decimal`/`uuid`,
but the hooks take third-party types the same way.)

## Bootstrapping from an existing database

`sqlkit inspect` reads a live **PostgreSQL or MySQL** schema and emits the
catalog source for it, so an existing database can be converted once and
maintained as Go code from then on. The output carries no `DO NOT EDIT` marker
— it is a starting point meant to be edited.

```sh
go run github.com/aita/sqlkit/cmd/sqlkit@latest inspect \
  --dsn "$DATABASE_URL" --schema public --package schema --out schema/schema.go
```

The same reader is available as a library — [`introspect.Schema`](https://pkg.go.dev/github.com/aita/sqlkit/introspect#Schema)`(ctx, db, dialect,
schemaName)` returns a [`meta.Metadata`](https://pkg.go.dev/github.com/aita/sqlkit/meta#Metadata) you can feed straight into `codegen` or
[`redact`](redaction.md). Constructs the metadata model cannot represent are
skipped on import (see [Limitations](limitations.md)).

---

← Back to the [documentation index](README.md).
