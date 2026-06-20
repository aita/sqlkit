package migrate

import (
	"context"

	"github.com/aita/sqlkit/decl"
	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

// Operator performs the schema (and data) changes of one migration. A
// Migration's Up/Down callbacks receive it and call its operations —
// CreateTable, AddColumn, CreateForeignKey, and so on — which execute
// immediately against the migration's transaction. Columns are declared with
// the decl DSL, and tables are referenced by name; foreign keys reference
// their target table by name too. The first failing operation is recorded and
// the rest become no-ops, so callbacks need not check an error on every line;
// the Migrator inspects Err after running the callback.
//
//	Up: func(op *migrate.Operator) {
//		op.CreateTable("posts",
//			decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
//			decl.Column("user_id", decl.BigInt),
//			decl.Column("title", decl.Text),
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
	return sql.Table{Schema: sql.NewIdent(o.schema), Name: sql.NewIdent(name)}
}

// CreateTable creates a table declared with decl elements (Column, Index,
// UniqueIndex, Check, PrimaryKey, TableComment). It also emits the table's
// indexes and comments. Foreign keys belong in CreateForeignKey (referenced by
// name), not inline.
func (o *Operator) CreateTable(name string, elements ...decl.TableElement) {
	o.createTable(name, false, elements...)
}

// CreateTemporaryTable creates a session-scoped TEMPORARY table, otherwise
// identical to CreateTable. The engine drops it at the end of the session.
func (o *Operator) CreateTemporaryTable(name string, elements ...decl.TableElement) {
	o.createTable(name, true, elements...)
}

func (o *Operator) createTable(name string, temporary bool, elements ...decl.TableElement) {
	if o.err != nil {
		return
	}
	tbl, err := decl.BuildTable(o.schema, name, elements...)
	if err != nil {
		o.fail(err)
		return
	}
	create := tbl.CreateStatement()
	create.Temporary = temporary
	o.emit(create)
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

// CreateView creates a view named by name whose body is query (a SELECT built
// with the sql package). Pass OrReplace / ViewColumns options to redefine an
// existing view or name its columns explicitly. The body binds no values; inline
// any literals with sql.Raw.
func (o *Operator) CreateView(name string, query sql.Query, opts ...ViewOption) {
	stmt := sql.CreateView(o.table(name), query)
	for _, opt := range opts {
		stmt = opt(stmt)
	}
	o.emit(stmt)
}

// DropView drops a view if it exists.
func (o *Operator) DropView(name string) {
	o.emit(sql.DropView(o.table(name)).IfExists())
}

// ViewOption configures a view created with CreateView.
type ViewOption func(sql.CreateViewBuilder) sql.CreateViewBuilder

// OrReplace makes CreateView emit CREATE OR REPLACE VIEW (PostgreSQL/MySQL).
func OrReplace() ViewOption {
	return func(s sql.CreateViewBuilder) sql.CreateViewBuilder { return s.OrReplace() }
}

// ViewColumns sets explicit view column names.
func ViewColumns(columns ...string) ViewOption {
	return func(s sql.CreateViewBuilder) sql.CreateViewBuilder { return s.Columns(columns...) }
}

// AddColumn adds a column, declared with a single decl.Column element.
func (o *Operator) AddColumn(table string, column decl.TableElement) {
	if o.err != nil {
		return
	}
	col, err := decl.BuildColumn(column)
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

// ModifyColumn restates a column's full definition — type, nullability, and
// default — from a single decl.Column element, the dialect-agnostic way to
// change those properties at once. MySQL renders one MODIFY COLUMN; PostgreSQL
// renders the equivalent ALTER COLUMN TYPE / SET|DROP NOT NULL / SET|DROP
// DEFAULT actions. It is what the migration autogenerator emits for a column
// change.
func (o *Operator) ModifyColumn(table string, column decl.TableElement) {
	if o.err != nil {
		return
	}
	col, err := decl.BuildColumn(column)
	if err != nil {
		o.fail(err)
		return
	}
	o.emit(sql.AlterTable(o.table(table)).RestateColumn(col.ColumnDef()))
}

// AddPrimaryKey adds a PRIMARY KEY constraint over the columns.
func (o *Operator) AddPrimaryKey(table string, columns ...string) {
	o.emit(sql.AlterTable(o.table(table)).AddPrimaryKey(columns...))
}

// DropPrimaryKey drops a table's primary key (MySQL DROP PRIMARY KEY;
// PostgreSQL drops the conventional "<table>_pkey" constraint).
func (o *Operator) DropPrimaryKey(table string) {
	o.emit(sql.AlterTable(o.table(table)).DropPrimaryKey())
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
// (assumed to be in the same schema). Pass OnDelete/OnUpdate options to set its
// referential actions.
func (o *Operator) CreateForeignKey(name, table string, columns []string, refTable string, refColumns []string, opts ...ForeignKeyOption) {
	fk := sql.ForeignKeyConstraint{
		Name:       sql.NewIdent(name),
		Columns:    sql.Idents(columns...),
		RefSchema:  sql.NewIdent(o.schema),
		RefTable:   sql.NewIdent(refTable),
		RefColumns: sql.Idents(refColumns...),
	}
	for _, opt := range opts {
		opt(&fk)
	}
	o.emit(sql.AlterTable(o.table(table)).AddForeignKey(fk))
}

// ForeignKeyOption configures a foreign key added with CreateForeignKey.
type ForeignKeyOption func(*sql.ForeignKeyConstraint)

// OnDelete sets a foreign key's ON DELETE referential action.
func OnDelete(action meta.ReferentialAction) ForeignKeyOption {
	return func(fk *sql.ForeignKeyConstraint) { fk.OnDelete = string(action) }
}

// OnUpdate sets a foreign key's ON UPDATE referential action.
func OnUpdate(action meta.ReferentialAction) ForeignKeyOption {
	return func(fk *sql.ForeignKeyConstraint) { fk.OnUpdate = string(action) }
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
type AlterColumnChange func(stmt sql.AlterTableBuilder, column string) sql.AlterTableBuilder

// TypeTo changes a column's type to the given portable type.
func TypeTo(typ decl.DataType) AlterColumnChange {
	return func(stmt sql.AlterTableBuilder, column string) sql.AlterTableBuilder {
		return stmt.AlterColumnType(column, meta.Column{Name: column, Type: typ}.ColumnDef().Type)
	}
}

// SetNullable makes a column nullable (true) or NOT NULL (false).
func SetNullable(nullable bool) AlterColumnChange {
	return func(stmt sql.AlterTableBuilder, column string) sql.AlterTableBuilder {
		if nullable {
			return stmt.DropNotNull(column)
		}
		return stmt.SetNotNull(column)
	}
}

// SetDefault sets a column's default to the given boolean/expression text.
func SetDefault(expr string) AlterColumnChange {
	return func(stmt sql.AlterTableBuilder, column string) sql.AlterTableBuilder {
		return stmt.SetDefault(column, sql.Raw(expr))
	}
}

// DropDefault drops a column's default.
func DropDefault() AlterColumnChange {
	return func(stmt sql.AlterTableBuilder, column string) sql.AlterTableBuilder {
		return stmt.DropDefault(column)
	}
}
