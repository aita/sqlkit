package sql

// This file defines the DDL side of the SQL DSL: the data-definition
// statements (CREATE/ALTER/DROP TABLE, indexes, foreign keys, comments) as
// first-class AST nodes that compile through the same Compiler and Dialect as
// the query builders. DDL binds no values, so a Statement compiles to plain
// SQL text with no bind arguments. Each Statement renders exactly one
// ";"-terminated SQL statement; compose several with Statements.
//
// The meta package builds these nodes to render a catalog's schema, and they
// are the building blocks migrations are written in.

// Statement is a DDL statement that compiles to a single SQL statement. It is a
// Query (it compiles with ToSQL, with no bind arguments) so it runs through the
// same execution path as the query builders — sqlkit's Database.Exec /
// Session.Exec — rather than a bespoke one. The ddlStatement marker keeps DDL
// and DML separable at the type level (a migration is a list of Statement).
type Statement interface {
	Query
	ddlStatement()
}

// Statements is an ordered list of DDL statements.
type Statements []Statement

// Render compiles each statement and joins them with blank lines, the form a
// migration or schema script is written out in. Each statement already ends
// with ";"; statements that render empty (e.g. an inline-comment dialect's
// CommentOnTable) are skipped.
func (ss Statements) Render(dialect Dialect) (string, error) {
	parts := make([]string, 0, len(ss))
	for _, s := range ss {
		text, _, err := s.ToSQL(dialect)
		if err != nil {
			return "", err
		}
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "\n\n"
		}
		out += p
	}
	return out, nil
}

// ColumnType yields the rendered DDL type clause (e.g. "varchar(255)") for a
// dialect. It is the seam that lets meta hand a dialect-resolved engine type to
// the DDL compiler without the sql package depending on meta.
type ColumnType interface {
	RenderType(d Dialect) (string, error)
}

// AutoIncrementColumnType is an optional capability of a ColumnType: rendering
// an auto-incrementing integer clause (serial / AUTO_INCREMENT) for a dialect. A
// ColumnDef with AutoIncrement set uses it when its Type implements it,
// otherwise the column falls back to its plain type. EngineType implements it,
// as does the deferred type the meta package builds from a schema, so schema
// tables can render their serial primary keys.
type AutoIncrementColumnType interface {
	ColumnType
	RenderAutoIncrement(d Dialect) (string, bool)
}

// EngineType is a dialect-resolved column type: an engine type name plus its
// length/precision modifiers. The meta package builds these from meta.Engine.
// It renders through Dialect.ColumnType (and Dialect.AutoIncrementColumn when a
// column is auto-incrementing).
type EngineType struct {
	Name      string
	Length    int
	Precision int
	Scale     int
}

// RenderType renders the engine type for the dialect.
func (t EngineType) RenderType(d Dialect) (string, error) {
	return d.ColumnType(t.Name, t.Length, t.Precision, t.Scale), nil
}

// RenderAutoIncrement renders the auto-incrementing clause for the engine type,
// or reports false when the dialect does not auto-increment it.
func (t EngineType) RenderAutoIncrement(d Dialect) (string, bool) {
	return d.AutoIncrementColumn(t.Name, t.Length, t.Precision, t.Scale)
}

// RawType is a verbatim type clause for hand-written DDL, e.g.
// RawType("bigserial") or RawType("varchar(64)"). It ignores the dialect.
type RawType string

// RenderType returns the verbatim type text.
func (t RawType) RenderType(Dialect) (string, error) { return string(t), nil }

// ColumnDef is a column definition used by CREATE TABLE and ALTER TABLE ADD
// COLUMN. Default carries a static expression (no bind values); set
// AutoIncrement for an integer primary key that should render as a
// serial/AUTO_INCREMENT column.
type ColumnDef struct {
	Name          string
	Type          ColumnType
	NotNull       bool
	Default       Expression
	AutoIncrement bool
	Comment       string
}

// TableCheck is a named CHECK constraint within a CREATE TABLE statement.
type TableCheck struct {
	Name       string
	Expression Expression
}

// ForeignKeyConstraint describes a foreign-key constraint added with
// AlterTable.AddForeignKey: the local Columns reference RefColumns of RefTable
// (in RefSchema). OnDelete/OnUpdate are the referential-action clauses (e.g.
// "CASCADE", "SET NULL"); an empty string renders nothing (the engine default).
type ForeignKeyConstraint struct {
	Name       string
	Columns    []string
	RefSchema  string
	RefTable   string
	RefColumns []string
	OnDelete   string
	OnUpdate   string
}

// CreateSchemaStmt builds a CREATE SCHEMA statement. On MySQL, where a schema
// is a database, CREATE SCHEMA is the engine's own synonym for CREATE DATABASE,
// so the same node renders for both dialects.
type CreateSchemaStmt struct {
	name        string
	ifNotExists bool
}

// CreateSchema starts a CREATE SCHEMA builder for a schema name.
func CreateSchema(name string) CreateSchemaStmt { return CreateSchemaStmt{name: name} }

// IfNotExists adds IF NOT EXISTS, making the statement idempotent (it leaves an
// already-existing schema, and whatever it contains, untouched).
func (s CreateSchemaStmt) IfNotExists() CreateSchemaStmt {
	s.ifNotExists = true
	return s
}

// ToSQL compiles the CREATE SCHEMA statement.
func (s CreateSchemaStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s CreateSchemaStmt) compileDDL(c *Compiler) (string, error) {
	return c.CompileCreateSchema(s)
}
func (CreateSchemaStmt) ddlStatement() {}

// DropSchemaStmt builds a DROP SCHEMA statement. On MySQL, where a schema is a
// database, this drops the database and everything left in it; on PostgreSQL a
// non-empty schema needs the tables dropped first (or Cascade).
type DropSchemaStmt struct {
	name     string
	ifExists bool
	cascade  bool
}

// DropSchema starts a DROP SCHEMA builder for a schema name.
func DropSchema(name string) DropSchemaStmt { return DropSchemaStmt{name: name} }

// IfExists adds IF EXISTS.
func (s DropSchemaStmt) IfExists() DropSchemaStmt {
	s.ifExists = true
	return s
}

// Cascade adds CASCADE, dropping objects the schema still contains. It renders
// only on PostgreSQL; MySQL has no CASCADE clause (DROP SCHEMA there always
// drops the database's contents), so the flag is ignored.
func (s DropSchemaStmt) Cascade() DropSchemaStmt {
	s.cascade = true
	return s
}

// ToSQL compiles the DROP SCHEMA statement.
func (s DropSchemaStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s DropSchemaStmt) compileDDL(c *Compiler) (string, error) {
	return c.CompileDropSchema(s)
}
func (DropSchemaStmt) ddlStatement() {}

// CreateTableStmt builds a CREATE TABLE statement.
type CreateTableStmt struct {
	table       Table
	columns     []ColumnDef
	primaryKey  []string
	checks      []TableCheck
	comment     string
	ifNotExists bool
}

// CreateTable starts a CREATE TABLE builder for a table.
func CreateTable(table Table) CreateTableStmt { return CreateTableStmt{table: table} }

// Column appends a column definition.
func (s CreateTableStmt) Column(c ColumnDef) CreateTableStmt {
	s.columns = append(append([]ColumnDef(nil), s.columns...), c)
	return s
}

// PrimaryKey sets the primary-key columns. Call once with all key columns.
func (s CreateTableStmt) PrimaryKey(columns ...string) CreateTableStmt {
	s.primaryKey = columns
	return s
}

// Check appends a named CHECK constraint. expr must bind no values.
func (s CreateTableStmt) Check(name string, expr Expression) CreateTableStmt {
	s.checks = append(append([]TableCheck(nil), s.checks...), TableCheck{Name: name, Expression: expr})
	return s
}

// Comment sets the table comment (rendered inline on dialects that support it,
// e.g. MySQL; use CommentOnTable for the standalone PostgreSQL form).
func (s CreateTableStmt) Comment(text string) CreateTableStmt {
	s.comment = text
	return s
}

// IfNotExists adds IF NOT EXISTS.
func (s CreateTableStmt) IfNotExists() CreateTableStmt {
	s.ifNotExists = true
	return s
}

// ToSQL compiles the CREATE TABLE statement.
func (s CreateTableStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s CreateTableStmt) compileDDL(c *Compiler) (string, error) {
	return c.CompileCreateTable(s)
}
func (CreateTableStmt) ddlStatement() {}

// DropTableStmt builds a DROP TABLE statement.
type DropTableStmt struct {
	table    Table
	ifExists bool
}

// DropTable starts a DROP TABLE builder for a table.
func DropTable(table Table) DropTableStmt { return DropTableStmt{table: table} }

// IfExists adds IF EXISTS.
func (s DropTableStmt) IfExists() DropTableStmt {
	s.ifExists = true
	return s
}

// ToSQL compiles the DROP TABLE statement.
func (s DropTableStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s DropTableStmt) compileDDL(c *Compiler) (string, error) {
	return c.CompileDropTable(s)
}
func (DropTableStmt) ddlStatement() {}

// CreateIndexStmt builds a CREATE INDEX statement.
type CreateIndexStmt struct {
	name        string
	table       Table
	columns     []string
	unique      bool
	ifNotExists bool
}

// CreateIndex starts a CREATE INDEX builder for an index over columns of a table.
func CreateIndex(name string, table Table, columns ...string) CreateIndexStmt {
	return CreateIndexStmt{name: name, table: table, columns: columns}
}

// Unique makes it a CREATE UNIQUE INDEX.
func (s CreateIndexStmt) Unique() CreateIndexStmt {
	s.unique = true
	return s
}

// IfNotExists adds IF NOT EXISTS.
func (s CreateIndexStmt) IfNotExists() CreateIndexStmt {
	s.ifNotExists = true
	return s
}

// ToSQL compiles the CREATE INDEX statement.
func (s CreateIndexStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s CreateIndexStmt) compileDDL(c *Compiler) (string, error) {
	return c.CompileCreateIndex(s)
}
func (CreateIndexStmt) ddlStatement() {}

// DropIndexStmt builds a DROP INDEX statement.
type DropIndexStmt struct {
	name  string
	table Table
}

// DropIndex starts a DROP INDEX builder. The table is needed because MySQL
// drops an index from its table.
func DropIndex(name string, table Table) DropIndexStmt {
	return DropIndexStmt{name: name, table: table}
}

// ToSQL compiles the DROP INDEX statement.
func (s DropIndexStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s DropIndexStmt) compileDDL(c *Compiler) (string, error) {
	return c.CompileDropIndex(s)
}
func (DropIndexStmt) ddlStatement() {}

// DropForeignKeyStmt builds the statement that drops a foreign-key constraint,
// using the dialect's syntax (PostgreSQL DROP CONSTRAINT, MySQL DROP FOREIGN KEY).
type DropForeignKeyStmt struct {
	table Table
	name  string
}

// DropForeignKey starts a builder that drops the named foreign-key constraint
// from a table.
func DropForeignKey(table Table, name string) DropForeignKeyStmt {
	return DropForeignKeyStmt{table: table, name: name}
}

// ToSQL compiles the drop-foreign-key statement.
func (s DropForeignKeyStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s DropForeignKeyStmt) compileDDL(c *Compiler) (string, error) {
	return c.CompileDropForeignKey(s)
}
func (DropForeignKeyStmt) ddlStatement() {}

// CommentOnTableStmt sets a table comment in the standalone form (PostgreSQL
// COMMENT ON TABLE). It renders to "" on dialects that comment inline (MySQL).
type CommentOnTableStmt struct {
	table   Table
	comment string
}

// CommentOnTable starts a builder for a standalone table comment.
func CommentOnTable(table Table, comment string) CommentOnTableStmt {
	return CommentOnTableStmt{table: table, comment: comment}
}

// ToSQL compiles the comment statement ("" when the dialect comments inline).
func (s CommentOnTableStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s CommentOnTableStmt) compileDDL(c *Compiler) (string, error) {
	return c.CompileCommentOnTable(s)
}
func (CommentOnTableStmt) ddlStatement() {}

// CommentOnColumnStmt sets a column comment in the standalone form (PostgreSQL
// COMMENT ON COLUMN). It renders to "" on dialects that comment inline (MySQL).
type CommentOnColumnStmt struct {
	table   Table
	column  string
	comment string
}

// CommentOnColumn starts a builder for a standalone column comment.
func CommentOnColumn(table Table, column, comment string) CommentOnColumnStmt {
	return CommentOnColumnStmt{table: table, column: column, comment: comment}
}

// ToSQL compiles the comment statement ("" when the dialect comments inline).
func (s CommentOnColumnStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s CommentOnColumnStmt) compileDDL(c *Compiler) (string, error) {
	return c.CompileCommentOnColumn(s)
}
func (CommentOnColumnStmt) ddlStatement() {}

// AlterTableStmt builds an ALTER TABLE statement carrying one or more ordered
// alterations, rendered as a single comma-separated statement.
type AlterTableStmt struct {
	table       Table
	alterations []alteration
}

// alteration is one ALTER TABLE action (a sealed union; build them through the
// AlterTableStmt methods).
type alteration interface{ alteration() }

// AlterTable starts an ALTER TABLE builder for a table.
func AlterTable(table Table) AlterTableStmt { return AlterTableStmt{table: table} }

func (s AlterTableStmt) with(a alteration) AlterTableStmt {
	s.alterations = append(append([]alteration(nil), s.alterations...), a)
	return s
}

// AddColumn appends an ADD COLUMN action.
func (s AlterTableStmt) AddColumn(c ColumnDef) AlterTableStmt { return s.with(addColumnAlt{c}) }

// DropColumn appends a DROP COLUMN action.
func (s AlterTableStmt) DropColumn(name string) AlterTableStmt { return s.with(dropColumnAlt{name}) }

// AddForeignKey appends an ADD CONSTRAINT ... FOREIGN KEY action.
func (s AlterTableStmt) AddForeignKey(fk ForeignKeyConstraint) AlterTableStmt {
	return s.with(addForeignKeyAlt{fk})
}

// AddCheck appends an ADD CONSTRAINT ... CHECK action. expr must bind no values.
func (s AlterTableStmt) AddCheck(name string, expr Expression) AlterTableStmt {
	return s.with(addCheckAlt{name: name, expr: expr})
}

// DropConstraint appends a DROP CONSTRAINT action (for CHECK / UNIQUE /
// primary-key constraints; use DropForeignKey for foreign keys, which differ by
// dialect).
func (s AlterTableStmt) DropConstraint(name string) AlterTableStmt {
	return s.with(dropConstraintAlt{name})
}

// AlterColumnType appends an action that changes a column's type (PostgreSQL).
func (s AlterTableStmt) AlterColumnType(name string, t ColumnType) AlterTableStmt {
	return s.with(alterColumnTypeAlt{name: name, typ: t})
}

// SetNotNull appends an action adding a NOT NULL constraint to a column (PostgreSQL).
func (s AlterTableStmt) SetNotNull(name string) AlterTableStmt {
	return s.with(notNullAlt{name: name, notNull: true})
}

// DropNotNull appends an action dropping a column's NOT NULL constraint (PostgreSQL).
func (s AlterTableStmt) DropNotNull(name string) AlterTableStmt {
	return s.with(notNullAlt{name: name, notNull: false})
}

// SetDefault appends an action setting a column's default (PostgreSQL). expr
// must bind no values.
func (s AlterTableStmt) SetDefault(name string, expr Expression) AlterTableStmt {
	return s.with(defaultAlt{name: name, expr: expr})
}

// DropDefault appends an action dropping a column's default (PostgreSQL).
func (s AlterTableStmt) DropDefault(name string) AlterTableStmt {
	return s.with(defaultAlt{name: name, expr: nil})
}

// RestateColumn appends an action that restates a column's full definition
// (type, NOT NULL, DEFAULT). It renders the dialect's spelling at compile time:
// MySQL's single MODIFY COLUMN, or PostgreSQL's separate ALTER COLUMN TYPE /
// SET|DROP NOT NULL / SET|DROP DEFAULT actions. It is the dialect-agnostic way a
// migration changes a column's properties at once.
func (s AlterTableStmt) RestateColumn(c ColumnDef) AlterTableStmt {
	return s.with(restateColumnAlt{col: c})
}

// AddPrimaryKey appends an ADD PRIMARY KEY action over the columns.
func (s AlterTableStmt) AddPrimaryKey(columns ...string) AlterTableStmt {
	return s.with(addPrimaryKeyAlt{columns: columns})
}

// DropPrimaryKey appends a DROP PRIMARY KEY action. MySQL spells it
// "DROP PRIMARY KEY"; PostgreSQL drops the constraint by its conventional
// "<table>_pkey" name.
func (s AlterTableStmt) DropPrimaryKey() AlterTableStmt {
	return s.with(dropPrimaryKeyAlt{})
}

// RenameColumn appends a RENAME COLUMN action.
func (s AlterTableStmt) RenameColumn(from, to string) AlterTableStmt {
	return s.with(renameColumnAlt{from: from, to: to})
}

// RenameTo appends a RENAME TO action (rename the table).
func (s AlterTableStmt) RenameTo(name string) AlterTableStmt {
	return s.with(renameTableAlt{name: name})
}

// ToSQL compiles the ALTER TABLE statement.
func (s AlterTableStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s AlterTableStmt) compileDDL(c *Compiler) (string, error) {
	return c.CompileAlterTable(s)
}
func (AlterTableStmt) ddlStatement() {}

type addColumnAlt struct{ col ColumnDef }
type dropColumnAlt struct{ name string }
type addForeignKeyAlt struct{ fk ForeignKeyConstraint }
type addCheckAlt struct {
	name string
	expr Expression
}
type dropConstraintAlt struct{ name string }
type alterColumnTypeAlt struct {
	name string
	typ  ColumnType
}
type notNullAlt struct {
	name    string
	notNull bool
}
type defaultAlt struct {
	name string
	expr Expression // nil means DROP DEFAULT
}
type renameColumnAlt struct{ from, to string }
type renameTableAlt struct{ name string }
type restateColumnAlt struct{ col ColumnDef }
type addPrimaryKeyAlt struct{ columns []string }
type dropPrimaryKeyAlt struct{}

func (addColumnAlt) alteration()       {}
func (dropColumnAlt) alteration()      {}
func (addForeignKeyAlt) alteration()   {}
func (addCheckAlt) alteration()        {}
func (dropConstraintAlt) alteration()  {}
func (alterColumnTypeAlt) alteration() {}
func (notNullAlt) alteration()         {}
func (defaultAlt) alteration()         {}
func (renameColumnAlt) alteration()    {}
func (renameTableAlt) alteration()     {}
func (restateColumnAlt) alteration()   {}
func (addPrimaryKeyAlt) alteration()   {}
func (dropPrimaryKeyAlt) alteration()  {}

// Compile-time checks that every DDL node is a Statement (and therefore a
// Query, so it runs through Database.Exec / Session.Exec).
var (
	_ Statement = CreateSchemaStmt{}
	_ Statement = DropSchemaStmt{}
	_ Statement = CreateTableStmt{}
	_ Statement = DropTableStmt{}
	_ Statement = CreateIndexStmt{}
	_ Statement = DropIndexStmt{}
	_ Statement = DropForeignKeyStmt{}
	_ Statement = CommentOnTableStmt{}
	_ Statement = CommentOnColumnStmt{}
	_ Statement = AlterTableStmt{}
)
