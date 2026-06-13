package migrate

import (
	"context"

	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqldef"
)

// Operator performs the schema (and data) changes of one migration. A
// Migration's Up/Down callbacks receive it and call its operations —
// CreateTable, AddColumn, CreateForeignKey, and so on — which execute
// immediately against the migration's transaction. Columns are declared with
// the sqldef DSL, and tables are referenced by name; foreign keys reference
// their target table by name too. The first failing operation is recorded and
// the rest become no-ops, so callbacks need not check an error on every line;
// the Migrator inspects Err after running the callback.
//
//	Up: func(op *migrate.Operator) {
//		op.CreateTable("posts",
//			sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
//			sqldef.Column("user_id", sqldef.BigInt),
//			sqldef.Column("title", sqldef.Text),
//		)
//		op.CreateForeignKey("fk_posts_user_id", "posts",
//			[]string{"user_id"}, "users", []string{"id"})
//	}
type Operator struct {
	ctx    context.Context
	ex     Execer
	schema string
	err    error
}

func newOperator(ctx context.Context, ex Execer, schema string) *Operator {
	return &Operator{ctx: ctx, ex: ex, schema: schema}
}

// Err returns the first error any operation hit, or nil.
func (o *Operator) Err() error { return o.err }

func (o *Operator) fail(err error) {
	if o.err == nil {
		o.err = err
	}
}

// emit runs a DDL statement, unless an earlier operation already failed.
func (o *Operator) emit(stmt sql.Statement) {
	if o.err != nil {
		return
	}
	if _, err := o.ex.Exec(o.ctx, stmt); err != nil {
		o.fail(err)
	}
}

// table returns the schema-qualified table reference.
func (o *Operator) table(name string) sql.Table {
	return sql.Table{Schema: o.schema, Name: name}
}

// CreateTable creates a table declared with sqldef elements (Column, Index,
// UniqueIndex, Check, PrimaryKey, TableComment). It also emits the table's
// indexes and comments. Foreign keys belong in CreateForeignKey (referenced by
// name), not inline.
func (o *Operator) CreateTable(name string, elements ...sqldef.TableElement) {
	if o.err != nil {
		return
	}
	tbl, err := sqldef.BuildTable(o.schema, name, elements...)
	if err != nil {
		o.fail(err)
		return
	}
	o.emit(tbl.CreateStatement())
	for _, ix := range tbl.Indexes {
		o.emit(ix.CreateStatement(tbl))
	}
	for _, fk := range tbl.ForeignKeys {
		o.emit(fk.AddStatement(tbl))
	}
	for _, stmt := range tbl.CommentStatements() {
		o.emit(stmt)
	}
}

// DropTable drops a table if it exists.
func (o *Operator) DropTable(name string) {
	o.emit(sql.DropTable(o.table(name)).IfExists())
}

// AddColumn adds a column, declared with a single sqldef.Column element.
func (o *Operator) AddColumn(table string, column sqldef.TableElement) {
	if o.err != nil {
		return
	}
	col, err := sqldef.BuildColumn(column)
	if err != nil {
		o.fail(err)
		return
	}
	o.emit(sql.AlterTable(o.table(table)).AddColumn(col.ColumnDef()))
}

// DropColumn drops a column.
func (o *Operator) DropColumn(table, column string) {
	o.emit(sql.AlterTable(o.table(table)).DropColumn(column))
}

// RenameColumn renames a column.
func (o *Operator) RenameColumn(table, from, to string) {
	o.emit(sql.AlterTable(o.table(table)).RenameColumn(from, to))
}

// RenameTable renames a table.
func (o *Operator) RenameTable(from, to string) {
	o.emit(sql.AlterTable(o.table(from)).RenameTo(to))
}

// AlterColumn changes a column's type, nullability, or default (PostgreSQL).
func (o *Operator) AlterColumn(table, column string, changes ...AlterColumnChange) {
	if o.err != nil || len(changes) == 0 {
		return
	}
	stmt := sql.AlterTable(o.table(table))
	for _, change := range changes {
		stmt = change(stmt, column)
	}
	o.emit(stmt)
}

// CreateIndex creates a (non-unique) index.
func (o *Operator) CreateIndex(name, table string, columns ...string) {
	o.emit(sql.CreateIndex(name, o.table(table), columns...))
}

// CreateUniqueIndex creates a unique index.
func (o *Operator) CreateUniqueIndex(name, table string, columns ...string) {
	o.emit(sql.CreateIndex(name, o.table(table), columns...).Unique())
}

// DropIndex drops an index.
func (o *Operator) DropIndex(name, table string) {
	o.emit(sql.DropIndex(name, o.table(table)))
}

// CreateForeignKey adds a foreign-key constraint referencing refTable by name
// (assumed to be in the same schema).
func (o *Operator) CreateForeignKey(name, table string, columns []string, refTable string, refColumns []string) {
	o.emit(sql.AlterTable(o.table(table)).AddForeignKey(sql.ForeignKeyConstraint{
		Name:       name,
		Columns:    columns,
		RefSchema:  o.schema,
		RefTable:   refTable,
		RefColumns: refColumns,
	}))
}

// DropForeignKey drops a foreign-key constraint (dialect-aware).
func (o *Operator) DropForeignKey(name, table string) {
	o.emit(sql.DropForeignKey(o.table(table), name))
}

// DropConstraint drops a CHECK / UNIQUE / primary-key constraint by name. Use
// DropForeignKey for foreign keys, whose syntax differs by dialect.
func (o *Operator) DropConstraint(name, table string) {
	o.emit(sql.AlterTable(o.table(table)).DropConstraint(name))
}

// AddCheck adds a named CHECK constraint with the given boolean expression.
func (o *Operator) AddCheck(name, table, expr string) {
	o.emit(sql.AlterTable(o.table(table)).AddCheck(name, sql.Raw(expr)))
}

// Execute runs a raw DDL statement built with the sql package — an escape hatch
// for steps the operations above do not cover, and for migrate.Diff output.
func (o *Operator) Execute(stmt sql.Statement) {
	o.emit(stmt)
}

// Exec runs a built query (INSERT/UPDATE/DELETE/SELECT) for data migrations.
func (o *Operator) Exec(query sql.Query) {
	if o.err != nil {
		return
	}
	if _, err := o.ex.Exec(o.ctx, query); err != nil {
		o.fail(err)
	}
}

// AlterColumnChange is one change applied by AlterColumn (TypeTo, SetNullable,
// SetDefault, DropDefault).
type AlterColumnChange func(stmt sql.AlterTableStmt, column string) sql.AlterTableStmt

// TypeTo changes a column's type to the given portable type.
func TypeTo(typ sqldef.DataType) AlterColumnChange {
	return func(stmt sql.AlterTableStmt, column string) sql.AlterTableStmt {
		return stmt.AlterColumnType(column, meta.Column{Name: column, Type: typ}.ColumnDef().Type)
	}
}

// SetNullable makes a column nullable (true) or NOT NULL (false).
func SetNullable(nullable bool) AlterColumnChange {
	return func(stmt sql.AlterTableStmt, column string) sql.AlterTableStmt {
		if nullable {
			return stmt.DropNotNull(column)
		}
		return stmt.SetNotNull(column)
	}
}

// SetDefault sets a column's default to the given boolean/expression text.
func SetDefault(expr string) AlterColumnChange {
	return func(stmt sql.AlterTableStmt, column string) sql.AlterTableStmt {
		return stmt.SetDefault(column, sql.Raw(expr))
	}
}

// DropDefault drops a column's default.
func DropDefault() AlterColumnChange {
	return func(stmt sql.AlterTableStmt, column string) sql.AlterTableStmt {
		return stmt.DropDefault(column)
	}
}
