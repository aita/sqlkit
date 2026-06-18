package meta

import (
	"fmt"
	"strings"

	"github.com/aita/sqlkit/sql"
)

// CreateSQL returns the SQL that creates the whole schema for dialect d (it
// does not touch a database): a CREATE SCHEMA IF NOT EXISTS for each distinct
// schema marked for creation (Schema.Create) first — so the tables that live in
// them never reference a missing schema — then CREATE TABLE statements with
// primary keys and CHECK constraints, then CREATE INDEX statements, then ALTER
// TABLE statements for foreign keys (emitted after all tables so declaration
// order never matters), then comments. Schemas not marked Create (including the
// default schema) are assumed to already exist and are left alone. Catalogs
// built with the decl package and schemas read by the introspection both feed it
// through the same model.
//
// The statements are built as sql-package DDL nodes (the DDL side of the SQL
// DSL) and compiled through the dialect, so meta, the migrate diff engine, and
// hand-written migrations share one rendering path.
//
// Default expressions render only when the model carries them (Column.DefaultExpr,
// declared as decl.Default(sql.Raw("now()"))), compiled for the dialect; an
// integer primary key with a default but no expression renders as a serial
// (PostgreSQL) or AUTO_INCREMENT (MySQL) column. It returns an error if a column
// has no type on the dialect or a default/check expression cannot be compiled.
func (md Metadata) CreateSQL(d sql.Dialect) (string, error) {
	var stmts []string
	for _, s := range md.Schemas() {
		if !s.Create || s.Name == "" {
			continue
		}
		out, _, err := s.CreateStatement().ToSQL(d)
		if err != nil {
			return "", err
		}
		stmts = append(stmts, out)
	}
	for _, table := range md.Tables {
		stmt, err := table.CreateSQL(d)
		if err != nil {
			return "", err
		}
		stmts = append(stmts, stmt)
	}
	for _, table := range md.Tables {
		for _, index := range table.Indexes {
			stmts = append(stmts, index.CreateSQL(table, d))
		}
	}
	for _, table := range md.Tables {
		for _, fk := range table.ForeignKeys {
			stmts = append(stmts, fk.addConstraintSQL(table, d))
		}
	}
	for _, table := range md.Tables {
		if block := table.commentStatementsSQL(d); block != "" {
			stmts = append(stmts, block)
		}
	}
	return strings.Join(stmts, "\n\n") + "\n", nil
}

// DropSQL returns the SQL that tears down everything CreateSQL creates,
// for dialect d (it does not touch a database): it drops the foreign keys first
// (so table drop order never matters), then drops the tables, which also drops
// their indexes, and finally a DROP SCHEMA IF EXISTS for each distinct schema
// marked for creation (Schema.Create), after its tables so the schema is empty
// by then. Schemas that CreateSQL leaves alone (the default schema and any not
// marked Create) are not dropped. It is the inverse of CreateSQL — run it before
// regenerating the schema. On MySQL, where a schema is a database, dropping it
// drops the database.
func (md Metadata) DropSQL(d sql.Dialect) string {
	var stmts []string
	for _, table := range md.Tables {
		for _, fk := range table.ForeignKeys {
			stmts = append(stmts, fk.dropConstraintSQL(table, d))
		}
	}
	for _, table := range md.Tables {
		stmts = append(stmts, table.DropSQL(d))
	}
	for _, s := range md.Schemas() {
		if !s.Create || s.Name == "" {
			continue
		}
		out, _, _ := s.DropStatement().ToSQL(d)
		stmts = append(stmts, out)
	}
	return strings.Join(stmts, "\n\n") + "\n"
}

// CreateStatement builds the CREATE SCHEMA IF NOT EXISTS DDL node for the
// schema. The unnamed default schema (empty Name) has no statement to build —
// CreateSQL skips it, leaving its tables unqualified.
func (s Schema) CreateStatement() sql.CreateSchemaStmt {
	return sql.CreateSchema(s.Name).IfNotExists()
}

// DropStatement builds the DROP SCHEMA IF EXISTS DDL node for the schema.
func (s Schema) DropStatement() sql.DropSchemaStmt {
	return sql.DropSchema(s.Name).IfExists()
}

// SQLTable returns the sql-package table reference for the table.
func (t Table) SQLTable() sql.Table {
	return sql.Table{Schema: t.Schema.Name, Name: t.Name}
}

// columnType adapts a portable meta.Type to an sql.ColumnType, resolving the
// engine type for the dialect at render time. That keeps the DDL nodes a table
// builds dialect-independent, so a schema's tables drop straight into a
// migration's Up/Down lists and resolve their types when the migrator compiles
// them for a dialect.
type columnType struct {
	name string // column name, for error messages
	typ  Type   // nil when the column declares no type
}

// RenderType resolves the engine type for the dialect.
func (ct columnType) RenderType(d sql.Dialect) (string, error) {
	if ct.typ == nil {
		return "", fmt.Errorf("column %q has no type", ct.name)
	}
	eng, ok := ct.typ.Engine(d.Name())
	if !ok {
		return "", fmt.Errorf("column %q has no type on dialect %s", ct.name, d.Name())
	}
	rendered := d.ColumnType(eng.Name, eng.Length, eng.Precision, eng.Scale)
	if eng.Array {
		rendered += "[]"
	}
	return rendered, nil
}

// RenderAutoIncrement resolves the auto-incrementing clause for the dialect.
func (ct columnType) RenderAutoIncrement(d sql.Dialect) (string, bool) {
	if ct.typ == nil {
		return "", false
	}
	eng, ok := ct.typ.Engine(d.Name())
	if !ok {
		return "", false
	}
	return d.AutoIncrementColumn(eng.Name, eng.Length, eng.Precision, eng.Scale)
}

// ColumnDef builds the dialect-independent sql.ColumnDef for the column — the
// building block of CREATE TABLE and ALTER TABLE ADD COLUMN. The portable type
// resolves when the statement is compiled for a dialect.
func (c Column) ColumnDef() sql.ColumnDef {
	return sql.ColumnDef{
		Name:          c.Name,
		Type:          columnType{name: c.Name, typ: c.Type},
		NotNull:       !c.Nullable,
		Default:       c.DefaultExpr,
		AutoIncrement: c.PrimaryKey && c.HasDefault && c.DefaultExpr == nil,
		Comment:       c.Comment,
	}
}

// Column returns the named column, or false when the table has none.
func (t Table) Column(name string) (Column, bool) {
	for _, c := range t.Columns {
		if c.Name == name {
			return c, true
		}
	}
	return Column{}, false
}

// CreateStatement builds the CREATE TABLE DDL node for the table, including its
// primary key, CHECK constraints, and comment. It is dialect-independent — the
// column types resolve when it is compiled with ToSQL — so it can be used
// directly in a migration's Up list (schema.Users.CreateStatement()).
func (t Table) CreateStatement() sql.CreateTableStmt {
	stmt := sql.CreateTable(t.SQLTable())
	var pk []string
	for _, column := range t.Columns {
		stmt = stmt.Column(column.ColumnDef())
		if column.PrimaryKey {
			pk = append(pk, column.Name)
		}
	}
	if len(pk) > 0 {
		stmt = stmt.PrimaryKey(pk...)
	}
	for _, check := range t.Checks {
		stmt = stmt.Check(check.Name, check.Expression)
	}
	return stmt.Comment(t.Comment)
}

// CreateSQL returns the CREATE TABLE statement for the table on dialect d. It
// errors if a column has no type on the dialect.
func (t Table) CreateSQL(d sql.Dialect) (string, error) {
	out, _, err := t.CreateStatement().ToSQL(d)
	if err != nil {
		return "", fmt.Errorf("meta: table %q: %w", t.Name, err)
	}
	return out, nil
}

// DropStatement builds the DROP TABLE DDL node for the table.
func (t Table) DropStatement() sql.DropTableStmt {
	return sql.DropTable(t.SQLTable()).IfExists()
}

// DropSQL returns the DROP TABLE statement for the table on dialect d.
func (t Table) DropSQL(d sql.Dialect) string {
	out, _, _ := t.DropStatement().ToSQL(d)
	return out
}

// CreateStatement builds the CREATE INDEX DDL node for the index on its table.
func (ix Index) CreateStatement(table Table) sql.CreateIndexStmt {
	stmt := sql.CreateIndex(ix.Name, table.SQLTable(), ix.Columns...)
	if ix.Unique {
		stmt = stmt.Unique()
	}
	return stmt
}

// CreateSQL returns the CREATE INDEX statement for the index on its table.
func (ix Index) CreateSQL(table Table, d sql.Dialect) string {
	out, _, _ := ix.CreateStatement(table).ToSQL(d)
	return out
}

// DropStatement builds the DROP INDEX DDL node for the index on its table.
func (ix Index) DropStatement(table Table) sql.DropIndexStmt {
	return sql.DropIndex(ix.Name, table.SQLTable())
}

// DropSQL returns the DROP INDEX statement for the index on its table.
func (ix Index) DropSQL(table Table, d sql.Dialect) string {
	out, _, _ := ix.DropStatement(table).ToSQL(d)
	return out
}

// AddStatement builds the ALTER TABLE ... ADD CONSTRAINT DDL node that adds the
// foreign key to its table.
func (fk ForeignKey) AddStatement(table Table) sql.AlterTableStmt {
	return sql.AlterTable(table.SQLTable()).AddForeignKey(sql.ForeignKeyConstraint{
		Name:       fk.Name,
		Columns:    fk.Columns,
		RefSchema:  fk.RefSchema.Name,
		RefTable:   fk.RefTable,
		RefColumns: fk.RefColumns,
		OnDelete:   string(fk.OnDelete),
		OnUpdate:   string(fk.OnUpdate),
	})
}

// DropStatement builds the DDL node that drops the foreign key from its table.
func (fk ForeignKey) DropStatement(table Table) sql.DropForeignKeyStmt {
	return sql.DropForeignKey(table.SQLTable(), fk.Name)
}

func (fk ForeignKey) addConstraintSQL(table Table, d sql.Dialect) string {
	out, _, _ := fk.AddStatement(table).ToSQL(d)
	return out
}

func (fk ForeignKey) dropConstraintSQL(table Table, d sql.Dialect) string {
	out, _, _ := fk.DropStatement(table).ToSQL(d)
	return out
}

// CommentStatements builds the standalone comment DDL nodes for the table and
// its columns (PostgreSQL COMMENT ON ...). On dialects that comment inline they
// render empty.
func (t Table) CommentStatements() []sql.Statement {
	stmts := []sql.Statement{sql.CommentOnTable(t.SQLTable(), t.Comment)}
	for _, column := range t.Columns {
		stmts = append(stmts, sql.CommentOnColumn(t.SQLTable(), column.Name, column.Comment))
	}
	return stmts
}

// commentStatementsSQL renders the table's standalone comment statements
// (PostgreSQL COMMENT ON for the table and its columns), or "" on dialects that
// render comments inline in the CREATE TABLE statement (MySQL).
func (t Table) commentStatementsSQL(d sql.Dialect) string {
	var stmts []string
	for _, s := range t.CommentStatements() {
		if out, _, _ := s.ToSQL(d); out != "" {
			stmts = append(stmts, out)
		}
	}
	return strings.Join(stmts, "\n")
}
