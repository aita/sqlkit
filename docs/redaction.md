# Redaction

The [`redaction`](https://pkg.go.dev/github.com/aita/sqlkit/redaction) package masks the
sensitive string literals embedded in a built statement, so a query can be logged
or forwarded without leaking PII. A
[`Redactor`](https://pkg.go.dev/github.com/aita/sqlkit/redaction#Redactor)
rewrites the statement's AST — across `SELECT`, `INSERT`, `UPDATE`, `DELETE`,
and `MERGE` — replacing every string literal compared or assigned to a PII
column with a fixed mask, while leaving the statement's shape and every
non-sensitive value intact (numbers, bind placeholders, and column references
are never touched). It does not mutate the input. A column reference that
resolves to exactly one table is matched precisely; one that does not resolve
fails safe by default — an unqualified column that is ambiguous across in-scope
tables is masked when any candidate marks it PII —
[`WithMaskUnresolved(false)`](https://pkg.go.dev/github.com/aita/sqlkit/redaction#WithMaskUnresolved)
turns that off for resolve-only masking.

Two shapes whose values can't be traced to a target column are masked fail-safe:
the string literals of a `VALUES` derived table (such as the bulk
`UPDATE ... FROM (VALUES ...)` a unit of work emits, whose rows reach their
columns only through the table alias), and the projected literals of an
`INSERT ... SELECT` with no column list.

A [`Policy`](https://pkg.go.dev/github.com/aita/sqlkit/redaction#Policy) names the
sensitive columns. Build one from an explicit list (no
schema needed), or from a predicate over the schema's column comments — so a
schema declares its own PII — and
[`Combine`](https://pkg.go.dev/github.com/aita/sqlkit/redaction#Combine) merges several:

```go
// Explicit list: every part accepts glob wildcards (* ? [..]).
//   "users.name"          a name column on a users table in any schema
//   "public.users.name"   exactly public.users.name — other schemas are left intact
//   "users.*"             every column of users
//   "public.*.email"      an email column on any table in the public schema
m := redaction.New(redaction.Columns("users.name", "users.ssn"))

q := sql.Select(sql.Star()).From(users).
    Where(sql.Eq(sql.Col("name"), sql.Val("taro")))
text, args, _ := m.Apply(q).ToSQL(sql.PostgreSQLDialect{})
// SELECT * FROM "users" WHERE ("name" = $1)   args: [*****]

// Precise targeting: a fully qualified spec masks only its schema's table, and
// WithMaskUnresolved(false) limits masking to references that resolve to exactly
// one table (an ambiguous unqualified column is left intact).
m = redaction.New(
    redaction.Columns("public.users.name"),
    redaction.WithMaskUnresolved(false),
)

// Convention-driven: a predicate receives each column's table, column, and
// comment and reports whether it is PII. meta.Metadata can be hand-built or read
// from a live database with introspection.
md, _ := introspection.Schema(ctx, db, "postgres", "public")
m = redaction.New(redaction.CommentMarker(md, func(_, _, comment string) bool {
    return strings.Contains(strings.ToLower(comment), "pii")
}), redaction.WithMask("[redacted]"))
```

See [`examples/redaction`](../examples/redaction) for a runnable CLI that applies a
`schema.sql`, introspects it, and shows the literals of `SELECT` (and its
`WHERE`), `INSERT`, and `UPDATE` masked entirely from the schema's PII column
comments. It pairs naturally with [`sql.CompileInline`](https://pkg.go.dev/github.com/aita/sqlkit/sql#CompileInline) (see
[Standalone builder](query-builder.md#standalone-builder)) to render the masked
statement as readable text.

---

← Back to the [documentation index](README.md).
