package migrate

import "github.com/aita/sqlkit/sql"

// This file adds first-class Operator methods for the schema objects the sql
// package gained beyond tables — schemas, sequences, enum types, extensions,
// materialized views, functions, and privileges. Each emits a sql DDL Statement
// immediately against the migration's transaction, exactly like CreateTable and
// CreateIndex. They are convenience over the Execute escape hatch (which already
// runs any sql.Statement); the option-rich builders take configure callbacks so
// the full fluent API of the underlying sql builder stays available without a
// separate option type per clause.

// qualified prefixes name with the migration's schema, for the object builders
// that take a string name (enum types, functions) rather than a sql.Table. An
// empty schema leaves the name unqualified.
func (o *Operator) qualified(name string) string {
	if o.schema == "" {
		return name
	}
	return o.schema + "." + name
}

// CreateSchema creates a schema (CREATE SCHEMA IF NOT EXISTS), so the step is
// idempotent and leaves an existing schema and its contents untouched.
func (o *Operator) CreateSchema(name string) {
	o.emit(sql.CreateSchema(name).IfNotExists())
}

// DropSchema drops a schema if it exists. On PostgreSQL a non-empty schema needs
// its objects dropped first; drop them in earlier steps or use Execute with
// sql.DropSchema(name).Cascade().
func (o *Operator) DropSchema(name string) {
	o.emit(sql.DropSchema(name).IfExists())
}

// CreateSequence creates a sequence in the migration's schema. Configure it with
// the sql builder's methods through configure callbacks:
//
//	op.CreateSequence("order_no", func(s sql.CreateSequenceStmt) sql.CreateSequenceStmt {
//		return s.Start(1000).Increment(1)
//	})
func (o *Operator) CreateSequence(name string, configure ...func(sql.CreateSequenceBuilder) sql.CreateSequenceBuilder) {
	s := sql.CreateSequence(o.table(name))
	for _, c := range configure {
		s = c(s)
	}
	o.emit(s)
}

// DropSequence drops a sequence if it exists.
func (o *Operator) DropSequence(name string) {
	o.emit(sql.DropSequence(o.qualified(name)).IfExists())
}

// CreateEnum creates an enum type (CREATE TYPE name AS ENUM (values...)) in the
// migration's schema.
func (o *Operator) CreateEnum(name string, values ...string) {
	o.emit(sql.CreateEnum(o.qualified(name), values...))
}

// DropEnum drops an enum type if it exists (DROP TYPE ... IF EXISTS CASCADE).
func (o *Operator) DropEnum(name string) {
	o.emit(sql.DropType(o.qualified(name)).IfExists().Cascade())
}

// AddEnumValue adds a label to an existing enum type (ALTER TYPE ... ADD VALUE).
// PostgreSQL cannot run this inside a transaction block before the value is used;
// keep it in its own migration.
func (o *Operator) AddEnumValue(name, value string) {
	o.emit(sql.AlterTypeAddValue(o.qualified(name), value))
}

// RenameEnumValue renames an enum label (ALTER TYPE ... RENAME VALUE).
func (o *Operator) RenameEnumValue(name, oldValue, newValue string) {
	o.emit(sql.AlterTypeRenameValue(o.qualified(name), oldValue, newValue))
}

// CreateExtension installs an extension (CREATE EXTENSION IF NOT EXISTS), so the
// step is idempotent. The extension name is not schema-qualified; target a schema
// with the WITH SCHEMA option via a configure callback:
//
//	op.CreateExtension("pg_trgm", func(s sql.CreateExtensionStmt) sql.CreateExtensionStmt {
//		return s.Schema("public")
//	})
func (o *Operator) CreateExtension(name string, configure ...func(sql.CreateExtensionBuilder) sql.CreateExtensionBuilder) {
	s := sql.CreateExtension(name).IfNotExists()
	for _, c := range configure {
		s = c(s)
	}
	o.emit(s)
}

// DropExtension drops an extension if it exists.
func (o *Operator) DropExtension(name string) {
	o.emit(sql.DropExtension(name).IfExists())
}

// CreateMaterializedView creates a materialized view named by name in the
// migration's schema, defined by query (a SELECT built with the sql package; it
// binds no values, so inline literals with sql.Raw). Pass configure callbacks for
// WithNoData, explicit Columns, or IfNotExists.
func (o *Operator) CreateMaterializedView(name string, query sql.Query, configure ...func(sql.CreateMaterializedViewBuilder) sql.CreateMaterializedViewBuilder) {
	s := sql.CreateMaterializedView(o.table(name), query)
	for _, c := range configure {
		s = c(s)
	}
	o.emit(s)
}

// DropMaterializedView drops a materialized view if it exists.
func (o *Operator) DropMaterializedView(name string) {
	o.emit(sql.DropMaterializedView(o.table(name)).IfExists())
}

// RefreshMaterializedView refreshes a materialized view's contents. Pass a
// configure callback for Concurrently or WithNoData.
func (o *Operator) RefreshMaterializedView(name string, configure ...func(sql.RefreshMaterializedViewBuilder) sql.RefreshMaterializedViewBuilder) {
	s := sql.RefreshMaterializedView(o.table(name))
	for _, c := range configure {
		s = c(s)
	}
	o.emit(s)
}

// CreateFunction creates a function in the migration's schema. The signature and
// body are set through configure callbacks on the sql builder:
//
//	op.CreateFunction("bump", func(s sql.CreateFunctionStmt) sql.CreateFunctionStmt {
//		return s.OrReplace().Returns("trigger").Language("plpgsql").Body("BEGIN ... END;")
//	})
func (o *Operator) CreateFunction(name string, configure ...func(sql.CreateFunctionBuilder) sql.CreateFunctionBuilder) {
	s := sql.CreateFunction(o.qualified(name))
	for _, c := range configure {
		s = c(s)
	}
	o.emit(s)
}

// DropFunction drops a function if it exists. For an overloaded function pass its
// argument types through a configure callback (s.ArgTypes("int", "text")).
func (o *Operator) DropFunction(name string, configure ...func(sql.DropFunctionBuilder) sql.DropFunctionBuilder) {
	s := sql.DropFunction(o.qualified(name)).IfExists()
	for _, c := range configure {
		s = c(s)
	}
	o.emit(s)
}

// Grant runs a GRANT statement built with sql.Grant. It is the typed counterpart
// of Execute for privileges:
//
//	op.Grant(sql.Grant("SELECT").On("public.users").To("readonly"))
func (o *Operator) Grant(grant sql.Statement) { o.emit(grant) }

// Revoke runs a REVOKE statement built with sql.Revoke.
func (o *Operator) Revoke(revoke sql.Statement) { o.emit(revoke) }
