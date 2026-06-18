# sqlparser-rs native AST: JSON shape notes

Findings from dumping `sqlparser` v0.62.0 output through the cgo bridge. These
inform the mapping layer (sqlparser-rs AST → sqlkit `sql.*`), which is built on
top of this package once the `sql` AST changes land.

## Bridge envelope

`Parse` returns one of:

- `{"statements": [ <Statement>, ... ]}` on success
- `{"error": "<message>"}` on failure (surfaced as `*ParseError`)

## Serialization conventions

- serde **externally tagged** enums: a variant is a single-key object whose key
  is the variant name. Examples:
  - `Statement::Query`  → `{"Query": { ... }}`
  - `SetExpr::Select`   → `{"Select": { ... }}`
  - `Expr::BinaryOp`    → `{"BinaryOp": {"left": …, "op": "Eq", "right": …}}`
  - `Expr::Identifier`  → `{"Identifier": <Ident>}`
  - `Expr::CompoundIdentifier` → `{"CompoundIdentifier": [<Ident>, …]}`
- Operators (`BinaryOperator`, etc.) serialize as bare strings: `"Eq"`, `"And"`,
  `"Gt"`, …
- `Ident` is `{"value": "users", "quote_style": null, "span": {…}}`.
- **Every** `Ident` and many nodes carry a `span` object
  (`{"start": {"line","column"}, "end": {…}}`). Spans roughly double the JSON
  size and are irrelevant to mapping — the Go mirror structs should simply omit
  the `span` field so it is skipped during unmarshal.

## Top-level SELECT skeleton

```
{"Query": {
  "body": {"Select": {
    "projection": [ <SelectItem>, … ],   // UnnamedExpr / ExprWithAlias / Wildcard / QualifiedWildcard
    "from": [ {"relation": <TableFactor>, "joins": [ <Join>, … ]} ],
    "selection": <Expr|null>,            // WHERE
    "group_by": …, "having": <Expr|null>,
    "distinct": <null | {...}>,
    "flavor": "Standard", ...            // plus many empty-by-default fields
  }},
  "order_by": …, "limit": …, "offset": …, "with": <null | CTEs>
}}
```

- `TableFactor::Table` → `{"Table": {"name": [<ObjectNamePart>], "alias": …}}`,
  where `ObjectNamePart` is `{"Identifier": <Ident>}` and `alias` is
  `null | {"name": <Ident>, "columns": [...]}`.
- `Join` has `relation`, `global`, and `join_operator`, the last being
  `{"Join": {"On": <Expr>}}` / `{"LeftOuter": …}` / `{"Inner": …}` etc.

## Mapping implications

- Decode into hand-written Go structs that mirror only the fields we map and
  drop `span`; do not try to round-trip the entire AST.
- The mapping is partial by design: unmapped variants should yield a clear
  "unsupported construct" error rather than a silent drop.
- Map onto sqlkit's public builder API (`sql.Select(...).From(...).Where(...)`,
  `sql.Col`, `sql.Eq`, …) since `sql.SelectQuery` fields are unexported.
