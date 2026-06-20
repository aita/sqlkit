# Redaction

The `redact` package masks the sensitive string literals embedded in a built
statement, so a query can be logged or forwarded without leaking PII. A `Masker`
rewrites the statement's AST — across `SELECT`, `INSERT`, `UPDATE`, `DELETE`,
and `MERGE` — replacing every string literal compared or assigned to a PII
column with a fixed mask, while leaving the statement's shape and every
non-sensitive value intact (numbers, bind placeholders, and column references
are never touched). It does not mutate the input, and resolution fails safe: an
unqualified column that is ambiguous across in-scope tables is masked when any
candidate marks it PII.

A `Policy` names the sensitive columns. Build one from an explicit list (no
schema needed), or from a predicate over the schema's column comments — so a
schema declares its own PII — and `Combine` merges several:

```go
// Explicit list: "table.column" (or "schema.table.column"), no schema needed.
m := redact.New(redact.Columns("users.name", "users.ssn"))

q := sql.Select(sql.Star()).From(users).
    Where(sql.Eq(sql.Col("name"), sql.Val("taro")))
text, args, _ := m.Apply(q).ToSQL(sql.PostgreSQLDialect{})
// SELECT * FROM "users" WHERE ("name" = $1)   args: [*****]

// Convention-driven: a predicate receives each column's table, column, and
// comment and reports whether it is PII. meta.Metadata can be hand-built or read
// from a live database with introspect.
md, _ := introspect.Schema(ctx, db, "postgres", "public")
m = redact.New(redact.CommentMarker(md, func(_, _, comment string) bool {
    return strings.Contains(strings.ToLower(comment), "pii")
}), redact.WithMask("[redacted]"))
```

See [`examples/redact`](../examples/redact) for a runnable CLI that applies a
`schema.sql`, introspects it, and shows the literals of `SELECT` (and its
`WHERE`), `INSERT`, and `UPDATE` masked entirely from the schema's PII column
comments. It pairs naturally with `sql.CompileInline` (see
[Standalone builder](query-builder.md#standalone-builder)) to render the masked
statement as readable text.

---

← Back to the [documentation index](README.md).
