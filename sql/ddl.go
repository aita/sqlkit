package sql

// This file defines the DDL side of the SQL DSL: the data-definition
// statements (CREATE/ALTER/DROP TABLE, indexes, foreign keys, comments) as
// first-class AST nodes that compile through the same Compiler and Dialect as
// the query builders. DDL binds no values, so a Statement compiles to plain
// SQL text with no bind arguments. Each Statement renders exactly one
// ";"-terminated SQL statement; compose several with Statements.
//
// Like the query statements, each DDL statement is an immutable value struct
// with exported fields (its public, read-it-directly contract); the ones with a
// fluent surface pair it with a XxxBuilder, while statements with no options are
// constructed directly. The meta package builds these nodes to render a
// catalog's schema, and they are the building blocks migrations are written in.

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
//
// The remaining fields are inline column constraints: Unique adds a column-level
// UNIQUE, References a column-level FOREIGN KEY (REFERENCES other(col)), Generated
// a computed column (GENERATED ALWAYS AS (expr)), and Identity a standard
// identity column (GENERATED ... AS IDENTITY). Generated, Identity, Default, and
// AutoIncrement are mutually exclusive ways to populate a column.
type ColumnDef struct {
	Name          string
	Type          ColumnType
	NotNull       bool
	Default       Expression
	AutoIncrement bool
	Comment       string
	Unique        bool
	References    *ColumnReference
	Generated     *GeneratedColumn
	Identity      IdentityKind
	// Collate is an optional column collation rendered as COLLATE <name> after
	// the type (empty for none).
	Collate string
	// OnUpdate is an optional MySQL ON UPDATE clause (e.g. CURRENT_TIMESTAMP) for
	// timestamp/datetime columns; it must bind no values. MySQL only.
	OnUpdate Expression
}

// ColumnReference is a column-level foreign key: REFERENCES <schema.table>
// (column) with optional referential actions. Schema is optional.
type ColumnReference struct {
	Schema   string
	Table    string
	Column   string
	OnDelete string
	OnUpdate string
}

// GeneratedColumn is a computed column whose value is derived from an expression
// (which must bind no values), rendered as GENERATED ALWAYS AS (expr) STORED, or
// VIRTUAL when Stored is false. PostgreSQL supports only stored generated
// columns (before version 17); MySQL supports both.
type GeneratedColumn struct {
	Expression Expression
	Stored     bool
}

// IdentityKind selects a SQL-standard identity column: GENERATED ALWAYS AS
// IDENTITY (always system-generated) or GENERATED BY DEFAULT AS IDENTITY (a
// user-supplied value overrides). It is supported on PostgreSQL (10+) and the
// standard target; MySQL has no IDENTITY (use AutoIncrement).
type IdentityKind string

const (
	// IdentityAlways renders GENERATED ALWAYS AS IDENTITY.
	IdentityAlways IdentityKind = "ALWAYS"
	// IdentityByDefault renders GENERATED BY DEFAULT AS IDENTITY.
	IdentityByDefault IdentityKind = "BY DEFAULT"
)

// TableUnique is a table-level UNIQUE constraint within a CREATE TABLE statement.
// Name is optional (an unnamed UNIQUE (cols) constraint when empty).
type TableUnique struct {
	Name    string
	Columns []string
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

// CreateSchemaStmt is the AST for a CREATE SCHEMA statement. On MySQL, where a
// schema is a database, CREATE SCHEMA is the engine's own synonym for CREATE
// DATABASE, so the same node renders for both dialects.
type CreateSchemaStmt struct {
	Name        string
	IfNotExists bool
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

// CreateSchemaBuilder is the fluent constructor for a CreateSchemaStmt.
type CreateSchemaBuilder struct{ s CreateSchemaStmt }

// CreateSchema starts a CREATE SCHEMA builder for a schema name.
func CreateSchema(name string) CreateSchemaBuilder {
	return CreateSchemaBuilder{s: CreateSchemaStmt{Name: name}}
}

// Stmt returns the built CREATE SCHEMA statement.
func (b CreateSchemaBuilder) Stmt() CreateSchemaStmt { return b.s }

// IfNotExists adds IF NOT EXISTS, making the statement idempotent (it leaves an
// already-existing schema, and whatever it contains, untouched).
func (b CreateSchemaBuilder) IfNotExists() CreateSchemaBuilder {
	b.s.IfNotExists = true
	return b
}

// ToSQL compiles the building CREATE SCHEMA statement.
func (b CreateSchemaBuilder) ToSQL(d Dialect) (string, []any, error) { return b.s.ToSQL(d) }
func (b CreateSchemaBuilder) buildStmt() Query                       { return b.s }
func (CreateSchemaBuilder) ddlStatement()                            {}

// DropSchemaStmt is the AST for a DROP SCHEMA statement. On MySQL, where a schema
// is a database, this drops the database and everything left in it; on PostgreSQL
// a non-empty schema needs the tables dropped first (or Cascade).
type DropSchemaStmt struct {
	Name     string
	IfExists bool
	Cascade  bool
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

// DropSchemaBuilder is the fluent constructor for a DropSchemaStmt.
type DropSchemaBuilder struct{ s DropSchemaStmt }

// DropSchema starts a DROP SCHEMA builder for a schema name.
func DropSchema(name string) DropSchemaBuilder {
	return DropSchemaBuilder{s: DropSchemaStmt{Name: name}}
}

// Stmt returns the built DROP SCHEMA statement.
func (b DropSchemaBuilder) Stmt() DropSchemaStmt { return b.s }

// IfExists adds IF EXISTS.
func (b DropSchemaBuilder) IfExists() DropSchemaBuilder {
	b.s.IfExists = true
	return b
}

// Cascade adds CASCADE, dropping objects the schema still contains. It renders
// only on PostgreSQL; MySQL has no CASCADE clause (DROP SCHEMA there always
// drops the database's contents), so the flag is ignored.
func (b DropSchemaBuilder) Cascade() DropSchemaBuilder {
	b.s.Cascade = true
	return b
}

// ToSQL compiles the building DROP SCHEMA statement.
func (b DropSchemaBuilder) ToSQL(d Dialect) (string, []any, error) { return b.s.ToSQL(d) }
func (b DropSchemaBuilder) buildStmt() Query                       { return b.s }
func (DropSchemaBuilder) ddlStatement()                            {}

// CreateTableStmt is the AST for a CREATE TABLE statement.
type CreateTableStmt struct {
	Table       Table
	Columns     []ColumnDef
	PrimaryKey  []string
	Checks      []TableCheck
	Uniques     []TableUnique
	ForeignKeys []ForeignKeyConstraint
	Indexes     []TableIndex
	Comment     string
	IfNotExists bool
	Temporary   bool
}

// TableIndex is a non-unique secondary index declared inline in a CREATE TABLE
// statement (MySQL's KEY/INDEX). Name is optional. Inline indexes are a MySQL
// feature; other dialects declare indexes with a separate CREATE INDEX.
type TableIndex struct {
	Name    string
	Columns []string
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

// CreateTableBuilder is the fluent constructor for a CreateTableStmt.
type CreateTableBuilder struct{ s CreateTableStmt }

// CreateTable starts a CREATE TABLE builder for a table.
func CreateTable(table Table) CreateTableBuilder {
	return CreateTableBuilder{s: CreateTableStmt{Table: table}}
}

// Stmt returns the built CREATE TABLE statement.
func (b CreateTableBuilder) Stmt() CreateTableStmt { return b.s }

// Column appends a column definition.
func (b CreateTableBuilder) Column(c ColumnDef) CreateTableBuilder {
	b.s.Columns = append(append([]ColumnDef(nil), b.s.Columns...), c)
	return b
}

// PrimaryKey sets the primary-key columns. Call once with all key columns.
func (b CreateTableBuilder) PrimaryKey(columns ...string) CreateTableBuilder {
	b.s.PrimaryKey = columns
	return b
}

// Check appends a named CHECK constraint. expr must bind no values.
func (b CreateTableBuilder) Check(name string, expr Expression) CreateTableBuilder {
	b.s.Checks = append(append([]TableCheck(nil), b.s.Checks...), TableCheck{Name: name, Expression: expr})
	return b
}

// Unique appends a table-level UNIQUE constraint over the columns. name is
// optional (pass "" for an unnamed constraint).
func (b CreateTableBuilder) Unique(name string, columns ...string) CreateTableBuilder {
	b.s.Uniques = append(append([]TableUnique(nil), b.s.Uniques...), TableUnique{Name: name, Columns: columns})
	return b
}

// ForeignKey appends a table-level FOREIGN KEY constraint.
func (b CreateTableBuilder) ForeignKey(fk ForeignKeyConstraint) CreateTableBuilder {
	b.s.ForeignKeys = append(append([]ForeignKeyConstraint(nil), b.s.ForeignKeys...), fk)
	return b
}

// Index appends an inline non-unique secondary index (MySQL KEY/INDEX). name is
// optional. Inline indexes render only on MySQL; other dialects reject them in
// favor of a separate CreateIndex statement.
func (b CreateTableBuilder) Index(name string, columns ...string) CreateTableBuilder {
	b.s.Indexes = append(append([]TableIndex(nil), b.s.Indexes...), TableIndex{Name: name, Columns: columns})
	return b
}

// Comment sets the table comment (rendered inline on dialects that support it,
// e.g. MySQL; use CommentOnTable for the standalone PostgreSQL form).
func (b CreateTableBuilder) Comment(text string) CreateTableBuilder {
	b.s.Comment = text
	return b
}

// IfNotExists adds IF NOT EXISTS.
func (b CreateTableBuilder) IfNotExists() CreateTableBuilder {
	b.s.IfNotExists = true
	return b
}

// Temporary makes it a CREATE TEMPORARY TABLE: a session-scoped table that the
// engine drops at the end of the session (PostgreSQL/MySQL both spell it
// TEMPORARY). A temporary table usually lives in a session-private namespace, so
// the table is best referenced unqualified (an explicit schema is the engine's
// to reject).
func (b CreateTableBuilder) Temporary() CreateTableBuilder {
	b.s.Temporary = true
	return b
}

// ToSQL compiles the building CREATE TABLE statement.
func (b CreateTableBuilder) ToSQL(d Dialect) (string, []any, error) { return b.s.ToSQL(d) }
func (b CreateTableBuilder) buildStmt() Query                       { return b.s }
func (CreateTableBuilder) ddlStatement()                            {}

// DropTableStmt is the AST for a DROP TABLE statement over one or more tables.
type DropTableStmt struct {
	Tables   []Table
	IfExists bool
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

// DropTableBuilder is the fluent constructor for a DropTableStmt.
type DropTableBuilder struct{ s DropTableStmt }

// DropTable starts a DROP TABLE builder for one or more tables, dropped in a
// single "DROP TABLE a, b" statement.
func DropTable(tables ...Table) DropTableBuilder {
	return DropTableBuilder{s: DropTableStmt{Tables: tables}}
}

// Stmt returns the built DROP TABLE statement.
func (b DropTableBuilder) Stmt() DropTableStmt { return b.s }

// IfExists adds IF EXISTS.
func (b DropTableBuilder) IfExists() DropTableBuilder {
	b.s.IfExists = true
	return b
}

// ToSQL compiles the building DROP TABLE statement.
func (b DropTableBuilder) ToSQL(d Dialect) (string, []any, error) { return b.s.ToSQL(d) }
func (b DropTableBuilder) buildStmt() Query                       { return b.s }
func (DropTableBuilder) ddlStatement()                            {}

// CreateViewStmt is the AST for a CREATE VIEW statement whose body is a query (a
// SELECT builder, or any Query). Like the other DDL nodes a view binds no values,
// so the body must compile to plain SQL text — inline any literals with Raw
// rather than as bound parameters.
type CreateViewStmt struct {
	Table     Table
	Query     Query
	Columns   []string
	OrReplace bool
}

// ToSQL compiles the CREATE VIEW statement.
func (s CreateViewStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s CreateViewStmt) compileDDL(c *Compiler) (string, error) {
	return c.CompileCreateView(s)
}
func (CreateViewStmt) ddlStatement() {}

// CreateViewBuilder is the fluent constructor for a CreateViewStmt.
type CreateViewBuilder struct{ s CreateViewStmt }

// CreateView starts a CREATE VIEW builder for a view defined by query.
func CreateView(table Table, query Query) CreateViewBuilder {
	return CreateViewBuilder{s: CreateViewStmt{Table: table, Query: astOf(query)}}
}

// Stmt returns the built CREATE VIEW statement.
func (b CreateViewBuilder) Stmt() CreateViewStmt { return b.s }

// OrReplace makes it CREATE OR REPLACE VIEW, redefining an existing view in
// place (PostgreSQL/MySQL).
func (b CreateViewBuilder) OrReplace() CreateViewBuilder {
	b.s.OrReplace = true
	return b
}

// Columns sets explicit view column names (CREATE VIEW v (a, b) AS ...),
// overriding the names the body's select list would otherwise supply.
func (b CreateViewBuilder) Columns(columns ...string) CreateViewBuilder {
	b.s.Columns = columns
	return b
}

// ToSQL compiles the building CREATE VIEW statement.
func (b CreateViewBuilder) ToSQL(d Dialect) (string, []any, error) { return b.s.ToSQL(d) }
func (b CreateViewBuilder) buildStmt() Query                       { return b.s }
func (CreateViewBuilder) ddlStatement()                            {}

// DropViewStmt is the AST for a DROP VIEW statement.
type DropViewStmt struct {
	Table    Table
	IfExists bool
	Cascade  bool
}

// ToSQL compiles the DROP VIEW statement.
func (s DropViewStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s DropViewStmt) compileDDL(c *Compiler) (string, error) {
	return c.CompileDropView(s)
}
func (DropViewStmt) ddlStatement() {}

// DropViewBuilder is the fluent constructor for a DropViewStmt.
type DropViewBuilder struct{ s DropViewStmt }

// DropView starts a DROP VIEW builder for a view.
func DropView(table Table) DropViewBuilder {
	return DropViewBuilder{s: DropViewStmt{Table: table}}
}

// Stmt returns the built DROP VIEW statement.
func (b DropViewBuilder) Stmt() DropViewStmt { return b.s }

// IfExists adds IF EXISTS.
func (b DropViewBuilder) IfExists() DropViewBuilder {
	b.s.IfExists = true
	return b
}

// Cascade adds CASCADE, dropping objects that depend on the view (PostgreSQL /
// standard SQL). MySQL has no CASCADE clause for DROP VIEW, so the
// flag is ignored there.
func (b DropViewBuilder) Cascade() DropViewBuilder {
	b.s.Cascade = true
	return b
}

// ToSQL compiles the building DROP VIEW statement.
func (b DropViewBuilder) ToSQL(d Dialect) (string, []any, error) { return b.s.ToSQL(d) }
func (b DropViewBuilder) buildStmt() Query                       { return b.s }
func (DropViewBuilder) ddlStatement()                            {}

// CreateIndexStmt is the AST for a CREATE INDEX statement.
type CreateIndexStmt struct {
	Name        string
	Table       Table
	Columns     []string
	Unique      bool
	IfNotExists bool
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

// CreateIndexBuilder is the fluent constructor for a CreateIndexStmt.
type CreateIndexBuilder struct{ s CreateIndexStmt }

// CreateIndex starts a CREATE INDEX builder for an index over columns of a table.
func CreateIndex(name string, table Table, columns ...string) CreateIndexBuilder {
	return CreateIndexBuilder{s: CreateIndexStmt{Name: name, Table: table, Columns: columns}}
}

// Stmt returns the built CREATE INDEX statement.
func (b CreateIndexBuilder) Stmt() CreateIndexStmt { return b.s }

// Unique makes it a CREATE UNIQUE INDEX.
func (b CreateIndexBuilder) Unique() CreateIndexBuilder {
	b.s.Unique = true
	return b
}

// IfNotExists adds IF NOT EXISTS.
func (b CreateIndexBuilder) IfNotExists() CreateIndexBuilder {
	b.s.IfNotExists = true
	return b
}

// ToSQL compiles the building CREATE INDEX statement.
func (b CreateIndexBuilder) ToSQL(d Dialect) (string, []any, error) { return b.s.ToSQL(d) }
func (b CreateIndexBuilder) buildStmt() Query                       { return b.s }
func (CreateIndexBuilder) ddlStatement()                            {}

// DropIndexStmt is the AST for a DROP INDEX statement. It has no options, so it
// is constructed directly with DropIndex.
type DropIndexStmt struct {
	Name  string
	Table Table
}

// DropIndex builds a DROP INDEX statement. The table is needed because MySQL
// drops an index from its table.
func DropIndex(name string, table Table) DropIndexStmt {
	return DropIndexStmt{Name: name, Table: table}
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

// TruncateStmt is the AST for a TRUNCATE TABLE statement, which empties one or
// more tables far faster than a DELETE. It binds no values and runs through Exec
// like the other statements. The options diverge by engine: RESTART IDENTITY and
// CASCADE are PostgreSQL features (MySQL always resets AUTO_INCREMENT and has no
// CASCADE), and truncating several tables at once is PostgreSQL-only — these are
// reported as errors at compile time.
type TruncateStmt struct {
	Tables          []Table
	RestartIdentity bool
	Cascade         bool
}

// ToSQL compiles the TRUNCATE statement.
func (s TruncateStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s TruncateStmt) compileDDL(c *Compiler) (string, error) {
	return c.CompileTruncate(s)
}
func (TruncateStmt) ddlStatement() {}

// TruncateBuilder is the fluent constructor for a TruncateStmt.
type TruncateBuilder struct{ s TruncateStmt }

// Truncate starts a TRUNCATE TABLE builder for one or more tables (multiple
// tables are PostgreSQL-only).
func Truncate(tables ...Table) TruncateBuilder {
	return TruncateBuilder{s: TruncateStmt{Tables: tables}}
}

// Stmt returns the built TRUNCATE statement.
func (b TruncateBuilder) Stmt() TruncateStmt { return b.s }

// RestartIdentity adds RESTART IDENTITY, resetting owned identity/serial
// sequences (PostgreSQL and the standard target; implicit on MySQL).
func (b TruncateBuilder) RestartIdentity() TruncateBuilder {
	b.s.RestartIdentity = true
	return b
}

// Cascade adds CASCADE, also truncating tables with foreign keys referencing the
// truncated ones (PostgreSQL only).
func (b TruncateBuilder) Cascade() TruncateBuilder {
	b.s.Cascade = true
	return b
}

// ToSQL compiles the building TRUNCATE statement.
func (b TruncateBuilder) ToSQL(d Dialect) (string, []any, error) { return b.s.ToSQL(d) }
func (b TruncateBuilder) buildStmt() Query                       { return b.s }
func (TruncateBuilder) ddlStatement()                            {}

// DropForeignKeyStmt is the AST for the statement that drops a foreign-key
// constraint, using the dialect's syntax (PostgreSQL DROP CONSTRAINT, MySQL DROP
// FOREIGN KEY). It has no options, so it is constructed directly.
type DropForeignKeyStmt struct {
	Table Table
	Name  string
}

// DropForeignKey builds the statement that drops the named foreign-key
// constraint from a table.
func DropForeignKey(table Table, name string) DropForeignKeyStmt {
	return DropForeignKeyStmt{Table: table, Name: name}
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

// CommentOnTableStmt is the AST for a standalone table comment (PostgreSQL
// COMMENT ON TABLE). It renders to "" on dialects that comment inline (MySQL).
// It has no options, so it is constructed directly.
type CommentOnTableStmt struct {
	Table   Table
	Comment string
}

// CommentOnTable builds a standalone table comment.
func CommentOnTable(table Table, comment string) CommentOnTableStmt {
	return CommentOnTableStmt{Table: table, Comment: comment}
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

// CommentOnColumnStmt is the AST for a standalone column comment (PostgreSQL
// COMMENT ON COLUMN). It renders to "" on dialects that comment inline (MySQL).
// It has no options, so it is constructed directly.
type CommentOnColumnStmt struct {
	Table   Table
	Column  string
	Comment string
}

// CommentOnColumn builds a standalone column comment.
func CommentOnColumn(table Table, column, comment string) CommentOnColumnStmt {
	return CommentOnColumnStmt{Table: table, Column: column, Comment: comment}
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

// AlterationKind identifies which ALTER TABLE action an Alteration describes.
type AlterationKind int

const (
	// AlterAddColumn is ADD COLUMN; Column is set.
	AlterAddColumn AlterationKind = iota
	// AlterDropColumn is DROP COLUMN; Name is the column.
	AlterDropColumn
	// AlterAddForeignKey is ADD CONSTRAINT ... FOREIGN KEY; ForeignKey is set.
	AlterAddForeignKey
	// AlterAddCheck is ADD CONSTRAINT ... CHECK; Name and Expression are set.
	AlterAddCheck
	// AlterAddUnique is ADD CONSTRAINT ... UNIQUE; Name (optional) and Columns are set.
	AlterAddUnique
	// AlterDropConstraint is DROP CONSTRAINT; Name is the constraint.
	AlterDropConstraint
	// AlterColumnType changes a column's type; Name and Type are set.
	AlterColumnType
	// AlterSetNotNull adds NOT NULL; Name is the column and NotNull is true.
	AlterSetNotNull
	// AlterDropNotNull drops NOT NULL; Name is the column and NotNull is false.
	AlterDropNotNull
	// AlterSetDefault sets a column default; Name and Expression are set.
	AlterSetDefault
	// AlterDropDefault drops a column default; Name is the column.
	AlterDropDefault
	// AlterRenameColumn renames a column; Name is the old name and NewName the new.
	AlterRenameColumn
	// AlterRenameTable renames the table; NewName is the new table name.
	AlterRenameTable
	// AlterRestateColumn restates a column's full definition; Column is set.
	AlterRestateColumn
	// AlterAddPrimaryKey adds a primary key; Columns are the key columns.
	AlterAddPrimaryKey
	// AlterDropPrimaryKey drops the primary key.
	AlterDropPrimaryKey
)

// Alteration is one ALTER TABLE action. Kind selects which fields are meaningful
// (see each AlterationKind). It is both the AST value carried by AlterTableStmt
// and the unit the AlterTableBuilder methods append.
type Alteration struct {
	Kind       AlterationKind
	Name       string                // column or constraint name; rename's old column name
	NewName    string                // rename target (column or table)
	Columns    []string              // AddUnique / AddPrimaryKey columns
	Column     *ColumnDef            // AddColumn / RestateColumn
	Type       ColumnType            // AlterColumnType
	Expression Expression            // AddCheck / SetDefault
	ForeignKey *ForeignKeyConstraint // AddForeignKey
	NotNull    bool                  // SetNotNull (true) / DropNotNull (false)
}

// AlterTableStmt is the AST for an ALTER TABLE statement carrying one or more
// ordered alterations, rendered as a single comma-separated statement.
type AlterTableStmt struct {
	Table       Table
	Alterations []Alteration
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

// AlterTableBuilder is the fluent constructor for an AlterTableStmt.
type AlterTableBuilder struct{ s AlterTableStmt }

// AlterTable starts an ALTER TABLE builder for a table.
func AlterTable(table Table) AlterTableBuilder {
	return AlterTableBuilder{s: AlterTableStmt{Table: table}}
}

// Stmt returns the built ALTER TABLE statement.
func (b AlterTableBuilder) Stmt() AlterTableStmt { return b.s }

func (b AlterTableBuilder) with(a Alteration) AlterTableBuilder {
	b.s.Alterations = append(append([]Alteration(nil), b.s.Alterations...), a)
	return b
}

// AddColumn appends an ADD COLUMN action.
func (b AlterTableBuilder) AddColumn(c ColumnDef) AlterTableBuilder {
	return b.with(Alteration{Kind: AlterAddColumn, Column: &c})
}

// DropColumn appends a DROP COLUMN action.
func (b AlterTableBuilder) DropColumn(name string) AlterTableBuilder {
	return b.with(Alteration{Kind: AlterDropColumn, Name: name})
}

// AddForeignKey appends an ADD CONSTRAINT ... FOREIGN KEY action.
func (b AlterTableBuilder) AddForeignKey(fk ForeignKeyConstraint) AlterTableBuilder {
	return b.with(Alteration{Kind: AlterAddForeignKey, ForeignKey: &fk})
}

// AddCheck appends an ADD CONSTRAINT ... CHECK action. expr must bind no values.
func (b AlterTableBuilder) AddCheck(name string, expr Expression) AlterTableBuilder {
	return b.with(Alteration{Kind: AlterAddCheck, Name: name, Expression: expr})
}

// AddUnique appends an ADD CONSTRAINT ... UNIQUE action over the columns. name is
// optional (pass "" for an unnamed constraint).
func (b AlterTableBuilder) AddUnique(name string, columns ...string) AlterTableBuilder {
	return b.with(Alteration{Kind: AlterAddUnique, Name: name, Columns: columns})
}

// DropConstraint appends a DROP CONSTRAINT action (for CHECK / UNIQUE /
// primary-key constraints; use DropForeignKey for foreign keys, which differ by
// dialect).
func (b AlterTableBuilder) DropConstraint(name string) AlterTableBuilder {
	return b.with(Alteration{Kind: AlterDropConstraint, Name: name})
}

// AlterColumnType appends an action that changes a column's type (PostgreSQL).
func (b AlterTableBuilder) AlterColumnType(name string, t ColumnType) AlterTableBuilder {
	return b.with(Alteration{Kind: AlterColumnType, Name: name, Type: t})
}

// SetNotNull appends an action adding a NOT NULL constraint to a column (PostgreSQL).
func (b AlterTableBuilder) SetNotNull(name string) AlterTableBuilder {
	return b.with(Alteration{Kind: AlterSetNotNull, Name: name, NotNull: true})
}

// DropNotNull appends an action dropping a column's NOT NULL constraint (PostgreSQL).
func (b AlterTableBuilder) DropNotNull(name string) AlterTableBuilder {
	return b.with(Alteration{Kind: AlterDropNotNull, Name: name})
}

// SetDefault appends an action setting a column's default (PostgreSQL). expr
// must bind no values.
func (b AlterTableBuilder) SetDefault(name string, expr Expression) AlterTableBuilder {
	return b.with(Alteration{Kind: AlterSetDefault, Name: name, Expression: expr})
}

// DropDefault appends an action dropping a column's default (PostgreSQL).
func (b AlterTableBuilder) DropDefault(name string) AlterTableBuilder {
	return b.with(Alteration{Kind: AlterDropDefault, Name: name})
}

// RestateColumn appends an action that restates a column's full definition
// (type, NOT NULL, DEFAULT). It renders the dialect's spelling at compile time:
// MySQL's single MODIFY COLUMN, or PostgreSQL's separate ALTER COLUMN TYPE /
// SET|DROP NOT NULL / SET|DROP DEFAULT actions. It is the dialect-agnostic way a
// migration changes a column's properties at once.
func (b AlterTableBuilder) RestateColumn(c ColumnDef) AlterTableBuilder {
	return b.with(Alteration{Kind: AlterRestateColumn, Column: &c})
}

// AddPrimaryKey appends an ADD PRIMARY KEY action over the columns.
func (b AlterTableBuilder) AddPrimaryKey(columns ...string) AlterTableBuilder {
	return b.with(Alteration{Kind: AlterAddPrimaryKey, Columns: columns})
}

// DropPrimaryKey appends a DROP PRIMARY KEY action. MySQL spells it
// "DROP PRIMARY KEY"; PostgreSQL drops the constraint by its conventional
// "<table>_pkey" name.
func (b AlterTableBuilder) DropPrimaryKey() AlterTableBuilder {
	return b.with(Alteration{Kind: AlterDropPrimaryKey})
}

// RenameColumn appends a RENAME COLUMN action.
func (b AlterTableBuilder) RenameColumn(from, to string) AlterTableBuilder {
	return b.with(Alteration{Kind: AlterRenameColumn, Name: from, NewName: to})
}

// RenameTo appends a RENAME TO action (rename the table).
func (b AlterTableBuilder) RenameTo(name string) AlterTableBuilder {
	return b.with(Alteration{Kind: AlterRenameTable, NewName: name})
}

// ToSQL compiles the building ALTER TABLE statement.
func (b AlterTableBuilder) ToSQL(d Dialect) (string, []any, error) { return b.s.ToSQL(d) }
func (b AlterTableBuilder) buildStmt() Query                       { return b.s }
func (AlterTableBuilder) ddlStatement()                            {}

// Compile-time checks that every DDL node is a Statement (and therefore a
// Query, so it runs through Database.Exec / Session.Exec). Both the immutable
// statement values and the fluent builders satisfy Statement.
var (
	_ Statement = CreateSchemaStmt{}
	_ Statement = CreateSchemaBuilder{}
	_ Statement = DropSchemaStmt{}
	_ Statement = DropSchemaBuilder{}
	_ Statement = CreateTableStmt{}
	_ Statement = CreateTableBuilder{}
	_ Statement = DropTableStmt{}
	_ Statement = DropTableBuilder{}
	_ Statement = CreateViewStmt{}
	_ Statement = CreateViewBuilder{}
	_ Statement = DropViewStmt{}
	_ Statement = DropViewBuilder{}
	_ Statement = CreateIndexStmt{}
	_ Statement = CreateIndexBuilder{}
	_ Statement = DropIndexStmt{}
	_ Statement = TruncateStmt{}
	_ Statement = TruncateBuilder{}
	_ Statement = DropForeignKeyStmt{}
	_ Statement = CommentOnTableStmt{}
	_ Statement = CommentOnColumnStmt{}
	_ Statement = AlterTableStmt{}
	_ Statement = AlterTableBuilder{}
)
