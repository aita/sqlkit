// Package codegen generates Go source from a database schema model
// (meta.Metadata): the typed table bindings that the sqlkit query builder is
// driven from, and optionally a row struct (DTO) per table.
//
// The generated binding for each table exposes typed Column[T] fields and the
// table identity, so statements start from the db handle with the table as an
// argument (db.Select().From(Users), db.Insert(Users)…) and the compiler checks
// every column and value. With EmitRelations the generator also emits
// foreign-key relation metadata (powering JoinRel, Preload, and typed filter
// bundles) and with EmitDTO a row struct per table.
//
// The usual entry point is the sqlkit command (go run
// github.com/aita/sqlkit/cmd/sqlkit@latest generate), which calls Generate for
// you. Call the library directly when you need what the CLI flags cannot
// express — chiefly the SQL→Go type mapping (TypeMapper / NewDefaultTypeMapper)
// and the DTO naming scheme:
//
//	md, _ := catalog.Build() // a meta.Metadata, from decl or introspect
//	files, _ := codegen.Generate(md, codegen.Options{
//		PackageName:   "appdb",
//		EmitRelations: true,
//	})
//	_ = codegen.Write("appdb", files)
//
// The type mapper's hooks are consulted most-specific first (OverrideFunc, then
// ColumnOverrides by table.column, then Overrides by SQL type name), falling
// through to the built-in tables; nullability is applied on top. A column whose
// SQL type has no Go mapping is an error — supply an override or pin a Go type
// on the decl column. See the package-level documentation guide at
// https://pkg.go.dev/github.com/aita/sqlkit and docs/schema-codegen.md for the
// full workflow.
package codegen
