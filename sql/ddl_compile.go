package sql

import (
	"fmt"
	"strings"
)

// This file holds the Compiler methods that render DDL statement nodes. They
// reuse the Dialect's DDL helpers (ColumnType, AutoIncrementColumn, comment and
// drop helpers, QuoteName) so engine differences stay in one place, and they
// reproduce the exact formatting the meta package depends on.

// CompileCreateSchema renders a CREATE SCHEMA statement. PostgreSQL and MySQL
// share the syntax (on MySQL a schema is a database and CREATE SCHEMA is its
// synonym), so it renders directly through QuoteName.
func (c *Compiler) CompileCreateSchema(s CreateSchemaStmt) (string, error) {
	if s.Name.IsZero() {
		return "", fmt.Errorf("sqlkit/sql: CREATE SCHEMA requires a name")
	}
	var sb strings.Builder
	sb.WriteString("CREATE SCHEMA ")
	if s.IfNotExists {
		sb.WriteString("IF NOT EXISTS ")
	}
	sb.WriteString(c.Dialect.QuoteName(s.Name.String()) + ";")
	return sb.String(), nil
}

// CompileDropSchema renders a DROP SCHEMA statement (shared syntax; CASCADE is
// PostgreSQL-only, as MySQL's DROP SCHEMA always drops the database's contents).
func (c *Compiler) CompileDropSchema(s DropSchemaStmt) (string, error) {
	if s.Name.IsZero() {
		return "", fmt.Errorf("sqlkit/sql: DROP SCHEMA requires a name")
	}
	var sb strings.Builder
	sb.WriteString("DROP SCHEMA ")
	if s.IfExists {
		sb.WriteString("IF EXISTS ")
	}
	sb.WriteString(c.Dialect.QuoteName(s.Name.String()))
	if s.Cascade && c.Dialect.Name() != "mysql" {
		sb.WriteString(" CASCADE")
	}
	sb.WriteString(";")
	return sb.String(), nil
}

// CompileCreateTable renders a CREATE TABLE statement.
func (c *Compiler) CompileCreateTable(s CreateTableStmt) (string, error) {
	var defs []string
	for _, col := range s.Columns {
		def, err := c.compileColumnDef(col)
		if err != nil {
			return "", err
		}
		defs = append(defs, def)
	}
	// Some dialects declare the primary key inline on the auto-increment column
	// (SQLite's INTEGER PRIMARY KEY AUTOINCREMENT); for those, a separate
	// PRIMARY KEY constraint over that single column would be a duplicate, so
	// skip it. PostgreSQL/MySQL keep the standalone constraint as before.
	if len(s.PrimaryKey) > 0 && !c.inlinesAutoIncrementPK(s) {
		defs = append(defs, "PRIMARY KEY ("+c.quoteAll(s.PrimaryKey)+")")
	}
	for _, check := range s.Checks {
		expr, err := CompileStatic(c.Dialect, check.Expression)
		if err != nil {
			return "", err
		}
		defs = append(defs, "CONSTRAINT "+c.Dialect.QuoteName(check.Name.String())+" CHECK ("+expr+")")
	}
	for _, u := range s.Uniques {
		if len(u.Columns) == 0 {
			return "", fmt.Errorf("sqlkit/sql: UNIQUE constraint requires at least one column")
		}
		clause := "UNIQUE (" + c.quoteAll(u.Columns) + ")"
		if !u.Name.IsZero() {
			clause = "CONSTRAINT " + c.Dialect.QuoteName(u.Name.String()) + " " + clause
		}
		defs = append(defs, clause)
	}
	for _, fk := range s.ForeignKeys {
		def, err := c.foreignKeyDef(fk)
		if err != nil {
			return "", err
		}
		defs = append(defs, def)
	}
	if len(s.Indexes) > 0 {
		if c.Dialect.Name() != "mysql" {
			return "", fmt.Errorf("sqlkit/sql: inline table indexes are supported only on the mysql dialect; use CreateIndex")
		}
		for _, idx := range s.Indexes {
			if len(idx.Columns) == 0 {
				return "", fmt.Errorf("sqlkit/sql: table index requires at least one column")
			}
			clause := "KEY "
			if !idx.Name.IsZero() {
				clause += c.Dialect.QuoteName(idx.Name.String()) + " "
			}
			defs = append(defs, clause+"("+c.quoteAll(idx.Columns)+")")
		}
	}

	if len(defs) == 0 {
		return "", fmt.Errorf("sqlkit/sql: CREATE TABLE requires at least one column or constraint")
	}

	var sb strings.Builder
	sb.WriteString("CREATE ")
	if s.Temporary {
		sb.WriteString("TEMPORARY ")
	}
	sb.WriteString("TABLE ")
	if s.IfNotExists {
		sb.WriteString("IF NOT EXISTS ")
	}
	sb.WriteString(c.qualify(s.Table))
	sb.WriteString(" (\n")
	for i, def := range defs {
		sb.WriteString("    " + def)
		if i < len(defs)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString(")" + c.Dialect.InlineTableComment(s.Comment) + ";")
	return sb.String(), nil
}

// inlinesAutoIncrementPK reports whether the table's primary key is a single
// auto-increment column whose clause already declares the primary key inline for
// this dialect (so CompileCreateTable must not also emit a standalone PRIMARY
// KEY constraint). It is false for dialects that do not implement
// InlineAutoIncrementPrimaryKey (PostgreSQL, MySQL), keeping their output
// unchanged.
func (c *Compiler) inlinesAutoIncrementPK(s CreateTableStmt) bool {
	ip, ok := c.Dialect.(InlineAutoIncrementPrimaryKey)
	if !ok || !ip.InlinesAutoIncrementPrimaryKey() || len(s.PrimaryKey) != 1 {
		return false
	}
	for _, col := range s.Columns {
		if !col.Name.Equal(s.PrimaryKey[0]) || !col.AutoIncrement {
			continue
		}
		ai, ok := col.Type.(AutoIncrementColumnType)
		if !ok {
			return false
		}
		_, rendered := ai.RenderAutoIncrement(c.Dialect)
		return rendered
	}
	return false
}

// compileColumnDef renders a single column definition, including any inline
// column constraints (GENERATED / IDENTITY, NOT NULL, DEFAULT, UNIQUE, REFERENCES).
func (c *Compiler) compileColumnDef(col ColumnDef) (string, error) {
	if col.Type == nil {
		return "", fmt.Errorf("sqlkit/sql: column %q has no type", col.Name)
	}
	name := c.Dialect.QuoteName(col.Name.String())

	// The column's type-and-value core: an auto-increment clause when the type
	// renders one, otherwise the plain type plus a GENERATED/IDENTITY/DEFAULT.
	var core string
	autoInc := false
	if col.AutoIncrement {
		if ai, ok := col.Type.(AutoIncrementColumnType); ok {
			if clause, ok := ai.RenderAutoIncrement(c.Dialect); ok {
				core = clause
				autoInc = true
			}
		}
	}
	if !autoInc {
		typeClause, err := col.Type.RenderType(c.Dialect)
		if err != nil {
			return "", err
		}
		core = typeClause
		if !col.Collate.IsZero() {
			core += " COLLATE " + col.Collate.String()
		}
		switch {
		case col.Generated != nil:
			expr, err := CompileStatic(c.Dialect, col.Generated.Expression)
			if err != nil {
				return "", err
			}
			storage := "STORED"
			if !col.Generated.Stored {
				// PostgreSQL (and the standard target) has no VIRTUAL generated
				// columns — only STORED — so emitting VIRTUAL produces DDL the engine
				// rejects. Reject it at compile time rather than render invalid SQL.
				if c.Dialect.Name() != "mysql" {
					return "", fmt.Errorf("sqlkit/sql: the %s dialect has no VIRTUAL generated columns; use a STORED generated column", c.Dialect.Name())
				}
				storage = "VIRTUAL"
			}
			core += " GENERATED ALWAYS AS (" + expr + ") " + storage
		case col.Identity != "":
			if err := c.requireIdentity(); err != nil {
				return "", err
			}
			core += " GENERATED " + string(col.Identity) + " AS IDENTITY"
		}
		if col.NotNull {
			core += " NOT NULL"
		}
		if col.Default != nil {
			if col.Generated != nil || col.Identity != "" {
				return "", fmt.Errorf("sqlkit/sql: column %q cannot have both a DEFAULT and a GENERATED/IDENTITY clause", col.Name)
			}
			expr, err := CompileStatic(c.Dialect, col.Default)
			if err != nil {
				return "", err
			}
			core += " DEFAULT " + expr
		}
		if col.OnUpdate != nil {
			if c.Dialect.Name() != "mysql" {
				return "", fmt.Errorf("sqlkit/sql: column ON UPDATE is supported only on the mysql dialect")
			}
			expr, err := CompileStatic(c.Dialect, col.OnUpdate)
			if err != nil {
				return "", err
			}
			core += " ON UPDATE " + expr
		}
	}

	def := name + " " + core
	if col.Unique {
		def += " UNIQUE"
	}
	if col.References != nil {
		ref, err := c.columnReferenceClause(*col.References)
		if err != nil {
			return "", err
		}
		def += " " + ref
	}
	return def + c.Dialect.InlineColumnComment(col.Comment), nil
}

// requireIdentity guards IDENTITY columns, which PostgreSQL and the standard
// target support but MySQL and the SQLite family do not (they use AutoIncrement).
func (c *Compiler) requireIdentity() error {
	if name := c.Dialect.Name(); name == "mysql" || sqliteFamily(name) {
		return fmt.Errorf("sqlkit/sql: the %s dialect has no IDENTITY columns; use an auto-increment column instead", name)
	}
	return nil
}

// columnReferenceClause renders a column-level REFERENCES clause.
func (c *Compiler) columnReferenceClause(r ColumnReference) (string, error) {
	if r.Table.IsZero() || r.Column.IsZero() {
		return "", fmt.Errorf("sqlkit/sql: a column REFERENCES needs a table and column")
	}
	clause := "REFERENCES " + c.qualifyName(r.Schema, r.Table) + " (" + c.Dialect.QuoteName(r.Column.String()) + ")"
	if r.OnDelete != "" {
		clause += " ON DELETE " + r.OnDelete
	}
	if r.OnUpdate != "" {
		clause += " ON UPDATE " + r.OnUpdate
	}
	return clause, nil
}

// compileRestateColumn renders an action that restates a column's full
// definition: MySQL's single MODIFY COLUMN, or PostgreSQL's comma-separated
// ALTER COLUMN TYPE / SET|DROP NOT NULL / SET|DROP DEFAULT actions.
func (c *Compiler) compileRestateColumn(col ColumnDef) (string, error) {
	if col.Type == nil {
		return "", fmt.Errorf("sqlkit/sql: column %q has no type", col.Name)
	}
	if c.Dialect.Name() == "mysql" {
		def, err := c.compileColumnDef(col)
		if err != nil {
			return "", err
		}
		return "MODIFY COLUMN " + def, nil
	}
	name := c.Dialect.QuoteName(col.Name.String())
	typeClause, err := col.Type.RenderType(c.Dialect)
	if err != nil {
		return "", err
	}
	clauses := []string{"ALTER COLUMN " + name + " TYPE " + typeClause}
	if col.NotNull {
		clauses = append(clauses, "ALTER COLUMN "+name+" SET NOT NULL")
	} else {
		clauses = append(clauses, "ALTER COLUMN "+name+" DROP NOT NULL")
	}
	if col.Default != nil {
		expr, err := CompileStatic(c.Dialect, col.Default)
		if err != nil {
			return "", err
		}
		clauses = append(clauses, "ALTER COLUMN "+name+" SET DEFAULT "+expr)
	} else {
		clauses = append(clauses, "ALTER COLUMN "+name+" DROP DEFAULT")
	}
	return strings.Join(clauses, ", "), nil
}

// CompileDropTable renders a DROP TABLE statement over one or more tables.
func (c *Compiler) CompileDropTable(s DropTableStmt) (string, error) {
	if len(s.Tables) == 0 {
		return "", fmt.Errorf("sqlkit/sql: DROP TABLE requires at least one table")
	}
	tables := make([]string, len(s.Tables))
	for i, t := range s.Tables {
		tables[i] = c.qualify(t)
	}
	var sb strings.Builder
	sb.WriteString("DROP TABLE ")
	if s.IfExists {
		sb.WriteString("IF EXISTS ")
	}
	sb.WriteString(strings.Join(tables, ", ") + ";")
	return sb.String(), nil
}

// CompileCreateView renders a CREATE VIEW statement. The view body compiles to
// plain SQL text; a body that binds values is rejected, since a DDL statement
// carries no bind arguments (inline literals with Raw).
func (c *Compiler) CompileCreateView(s CreateViewStmt) (string, error) {
	if s.Query == nil {
		return "", fmt.Errorf("sqlkit/sql: CREATE VIEW requires a query")
	}
	if s.OrReplace && sqliteFamily(c.Dialect.Name()) {
		return "", fmt.Errorf("sqlkit/sql: %s does not support CREATE OR REPLACE VIEW; drop and recreate instead", c.Dialect.Name())
	}
	body, args, err := s.Query.ToSQL(c.Dialect)
	if err != nil {
		return "", err
	}
	if len(args) > 0 {
		return "", fmt.Errorf("sqlkit/sql: CREATE VIEW body must not bind values; use Raw to inline literals")
	}
	var sb strings.Builder
	sb.WriteString("CREATE ")
	if s.OrReplace {
		sb.WriteString("OR REPLACE ")
	}
	sb.WriteString("VIEW ")
	sb.WriteString(c.qualify(s.Table))
	if len(s.Columns) > 0 {
		sb.WriteString(" (" + c.quoteAll(s.Columns) + ")")
	}
	sb.WriteString(" AS\n")
	sb.WriteString(body)
	sb.WriteString(";")
	return sb.String(), nil
}

// CompileDropView renders a DROP VIEW statement (CASCADE is PostgreSQL/standard
// only; MySQL has no CASCADE clause for DROP VIEW).
func (c *Compiler) CompileDropView(s DropViewStmt) (string, error) {
	var sb strings.Builder
	sb.WriteString("DROP VIEW ")
	if s.IfExists {
		sb.WriteString("IF EXISTS ")
	}
	sb.WriteString(c.qualify(s.Table))
	if s.Cascade && c.Dialect.Name() != "mysql" && !sqliteFamily(c.Dialect.Name()) {
		sb.WriteString(" CASCADE")
	}
	sb.WriteString(";")
	return sb.String(), nil
}

// CompileCreateIndex renders a CREATE INDEX statement.
func (c *Compiler) CompileCreateIndex(s CreateIndexStmt) (string, error) {
	kind := "INDEX"
	if s.Unique {
		kind = "UNIQUE INDEX"
	}
	var sb strings.Builder
	sb.WriteString("CREATE " + kind + " ")
	if s.IfNotExists {
		sb.WriteString("IF NOT EXISTS ")
	}
	sb.WriteString(c.Dialect.QuoteName(s.Name.String()) + " ON " + c.qualify(s.Table) +
		" (" + c.quoteAll(s.Columns) + ");")
	return sb.String(), nil
}

// CompileDropIndex renders a DROP INDEX statement via the dialect.
func (c *Compiler) CompileDropIndex(s DropIndexStmt) (string, error) {
	return c.Dialect.DropIndex(
		c.qualifyName(s.Table.Schema, s.Name),
		c.Dialect.QuoteName(s.Name.String()),
		c.qualify(s.Table),
	), nil
}

// CompileTruncate renders a TRUNCATE TABLE statement. RESTART IDENTITY and
// CASCADE, and truncating more than one table, are PostgreSQL features. The
// unsupported combinations are rejected rather than silently mis-rendered.
func (c *Compiler) CompileTruncate(s TruncateStmt) (string, error) {
	if len(s.Tables) == 0 {
		return "", fmt.Errorf("sqlkit/sql: TRUNCATE requires at least one table")
	}
	name := c.Dialect.Name()
	if sqliteFamily(name) {
		return "", fmt.Errorf("sqlkit/sql: %s has no TRUNCATE; use DELETE FROM instead", name)
	}
	if len(s.Tables) > 1 && name != "postgres" {
		return "", fmt.Errorf("sqlkit/sql: the %s dialect cannot TRUNCATE multiple tables in one statement", name)
	}
	if s.Cascade && name != "postgres" {
		return "", fmt.Errorf("sqlkit/sql: CASCADE on TRUNCATE is supported only on the postgres dialect")
	}
	tables := make([]string, len(s.Tables))
	for i, t := range s.Tables {
		tables[i] = c.qualify(t)
	}
	var sb strings.Builder
	sb.WriteString("TRUNCATE TABLE ")
	sb.WriteString(strings.Join(tables, ", "))
	// RESTART IDENTITY is explicit on PostgreSQL and the standard target; MySQL
	// always resets AUTO_INCREMENT, so the option renders nothing there.
	if s.RestartIdentity && name != "mysql" {
		sb.WriteString(" RESTART IDENTITY")
	}
	if s.Cascade {
		sb.WriteString(" CASCADE")
	}
	sb.WriteString(";")
	return sb.String(), nil
}

// CompileDropForeignKey renders the drop-foreign-key statement via the dialect.
func (c *Compiler) CompileDropForeignKey(s DropForeignKeyStmt) (string, error) {
	return c.Dialect.DropForeignKey(c.qualify(s.Table), c.Dialect.QuoteName(s.Name.String())), nil
}

// CompileCommentOnTable renders a standalone table comment ("" when the dialect
// comments inline).
func (c *Compiler) CompileCommentOnTable(s CommentOnTableStmt) (string, error) {
	return c.Dialect.TableCommentStatement(c.qualify(s.Table), s.Comment), nil
}

// CompileCommentOnColumn renders a standalone column comment ("" when the
// dialect comments inline).
func (c *Compiler) CompileCommentOnColumn(s CommentOnColumnStmt) (string, error) {
	return c.Dialect.ColumnCommentStatement(c.qualify(s.Table), c.Dialect.QuoteName(s.Column.String()), s.Comment), nil
}

// CompileAlterTable renders an ALTER TABLE statement as a single
// comma-separated list of actions.
func (c *Compiler) CompileAlterTable(s AlterTableStmt) (string, error) {
	if len(s.Alterations) == 0 {
		return "", fmt.Errorf("sqlkit/sql: ALTER TABLE requires at least one alteration")
	}
	clauses := make([]string, len(s.Alterations))
	for i, a := range s.Alterations {
		clause, err := c.compileAlteration(s.Table, a)
		if err != nil {
			return "", err
		}
		clauses[i] = clause
	}
	return "ALTER TABLE " + c.qualify(s.Table) + " " + strings.Join(clauses, ", ") + ";", nil
}

func (c *Compiler) compileAlteration(table Table, a Alteration) (string, error) {
	switch a.Kind {
	case AlterAddColumn:
		if a.Column == nil {
			return "", fmt.Errorf("sqlkit/sql: ADD COLUMN has no column definition")
		}
		def, err := c.compileColumnDef(*a.Column)
		if err != nil {
			return "", err
		}
		return "ADD COLUMN " + def, nil
	case AlterDropColumn:
		return "DROP COLUMN " + c.Dialect.QuoteName(a.Name.String()), nil
	case AlterRestateColumn:
		if a.Column == nil {
			return "", fmt.Errorf("sqlkit/sql: restate column has no column definition")
		}
		return c.compileRestateColumn(*a.Column)
	case AlterAddPrimaryKey:
		return "ADD PRIMARY KEY (" + c.quoteAll(a.Columns) + ")", nil
	case AlterDropPrimaryKey:
		if c.Dialect.Name() == "mysql" {
			return "DROP PRIMARY KEY", nil
		}
		// PostgreSQL has no DROP PRIMARY KEY; drop the constraint by its
		// conventional "<table>_pkey" name.
		return "DROP CONSTRAINT " + c.Dialect.QuoteName(table.Name.String()+"_pkey"), nil
	case AlterAddForeignKey:
		if a.ForeignKey == nil {
			return "", fmt.Errorf("sqlkit/sql: ADD FOREIGN KEY has no constraint")
		}
		return c.foreignKeyClause(*a.ForeignKey)
	case AlterAddCheck:
		expr, err := CompileStatic(c.Dialect, a.Expression)
		if err != nil {
			return "", err
		}
		return "ADD CONSTRAINT " + c.Dialect.QuoteName(a.Name.String()) + " CHECK (" + expr + ")", nil
	case AlterAddUnique:
		if len(a.Columns) == 0 {
			return "", fmt.Errorf("sqlkit/sql: ADD UNIQUE requires at least one column")
		}
		clause := "ADD "
		if !a.Name.IsZero() {
			clause += "CONSTRAINT " + c.Dialect.QuoteName(a.Name.String()) + " "
		}
		return clause + "UNIQUE (" + c.quoteAll(a.Columns) + ")", nil
	case AlterDropConstraint:
		return "DROP CONSTRAINT " + c.Dialect.QuoteName(a.Name.String()), nil
	case AlterRenameColumn:
		return "RENAME COLUMN " + c.Dialect.QuoteName(a.Name.String()) + " TO " + c.Dialect.QuoteName(a.NewName.String()), nil
	case AlterRenameTable:
		return "RENAME TO " + c.Dialect.QuoteName(a.NewName.String()), nil
	case AlterColumnType:
		if err := c.requireColumnModify("ALTER COLUMN TYPE"); err != nil {
			return "", err
		}
		t, err := a.Type.RenderType(c.Dialect)
		if err != nil {
			return "", err
		}
		return "ALTER COLUMN " + c.Dialect.QuoteName(a.Name.String()) + " TYPE " + t, nil
	case AlterSetNotNull, AlterDropNotNull:
		if err := c.requireColumnModify("SET/DROP NOT NULL"); err != nil {
			return "", err
		}
		op := "SET NOT NULL"
		if a.Kind == AlterDropNotNull {
			op = "DROP NOT NULL"
		}
		return "ALTER COLUMN " + c.Dialect.QuoteName(a.Name.String()) + " " + op, nil
	case AlterSetDefault, AlterDropDefault:
		if err := c.requireColumnModify("SET/DROP DEFAULT"); err != nil {
			return "", err
		}
		col := c.Dialect.QuoteName(a.Name.String())
		if a.Kind == AlterDropDefault {
			return "ALTER COLUMN " + col + " DROP DEFAULT", nil
		}
		expr, err := CompileStatic(c.Dialect, a.Expression)
		if err != nil {
			return "", err
		}
		return "ALTER COLUMN " + col + " SET DEFAULT " + expr, nil
	default:
		return "", fmt.Errorf("sqlkit/sql: unsupported alteration kind %d", a.Kind)
	}
}

// requireColumnModify guards the column-modification actions whose syntax this
// package only renders for PostgreSQL; MySQL spells them with MODIFY COLUMN and
// is out of scope for now.
func (c *Compiler) requireColumnModify(op string) error {
	if c.Dialect.Name() == "mysql" {
		return fmt.Errorf("sqlkit/sql: %s is not supported on the mysql dialect", op)
	}
	return nil
}

// foreignKeyClause renders an ALTER TABLE "ADD CONSTRAINT ... FOREIGN KEY ..."
// action; foreignKeyDef renders the same constraint as a CREATE TABLE definition.
func (c *Compiler) foreignKeyClause(fk ForeignKeyConstraint) (string, error) {
	def, err := c.foreignKeyDef(fk)
	if err != nil {
		return "", err
	}
	return "ADD " + def, nil
}

// foreignKeyDef renders a "CONSTRAINT name FOREIGN KEY (cols) REFERENCES ..."
// table constraint, shared by CREATE TABLE and ALTER TABLE ADD. Both column
// lists must be non-empty, mirroring the UNIQUE and KEY constraint guards, so a
// degenerate constraint errors rather than emitting "FOREIGN KEY () REFERENCES".
func (c *Compiler) foreignKeyDef(fk ForeignKeyConstraint) (string, error) {
	if len(fk.Columns) == 0 || len(fk.RefColumns) == 0 {
		return "", fmt.Errorf("sqlkit/sql: FOREIGN KEY requires at least one referencing and one referenced column")
	}
	clause := "CONSTRAINT " + c.Dialect.QuoteName(fk.Name.String()) +
		" FOREIGN KEY (" + c.quoteAll(fk.Columns) + ")" +
		" REFERENCES " + c.qualifyName(fk.RefSchema, fk.RefTable) +
		" (" + c.quoteAll(fk.RefColumns) + ")"
	if fk.OnDelete != "" {
		clause += " ON DELETE " + fk.OnDelete
	}
	if fk.OnUpdate != "" {
		clause += " ON UPDATE " + fk.OnUpdate
	}
	return clause, nil
}

// qualify renders a schema-qualified table identifier for DDL, quoting each
// part as one name; an empty schema yields an unqualified name.
func (c *Compiler) qualify(t Table) string {
	return c.qualifyName(t.Schema, t.Name)
}

// qualifyName quotes schema.name, or just name when schema is empty. DDL uses
// QuoteName (not QuoteIdent) so a name containing a literal dot stays one
// identifier.
func (c *Compiler) qualifyName(schemaName, name Ident) string {
	if schemaName.IsZero() {
		return c.Dialect.QuoteName(name.String())
	}
	return c.Dialect.QuoteName(schemaName.String()) + "." + c.Dialect.QuoteName(name.String())
}

// quoteAll quotes each identifier with QuoteName and joins them with ", ".
func (c *Compiler) quoteAll(names []Ident) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = c.Dialect.QuoteName(name.String())
	}
	return strings.Join(quoted, ", ")
}
