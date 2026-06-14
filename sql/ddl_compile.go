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
	if s.name == "" {
		return "", fmt.Errorf("sqlkit/sql: CREATE SCHEMA requires a name")
	}
	var sb strings.Builder
	sb.WriteString("CREATE SCHEMA ")
	if s.ifNotExists {
		sb.WriteString("IF NOT EXISTS ")
	}
	sb.WriteString(c.Dialect.QuoteName(s.name) + ";")
	return sb.String(), nil
}

// CompileDropSchema renders a DROP SCHEMA statement (shared syntax; CASCADE is
// PostgreSQL-only, as MySQL's DROP SCHEMA always drops the database's contents).
func (c *Compiler) CompileDropSchema(s DropSchemaStmt) (string, error) {
	if s.name == "" {
		return "", fmt.Errorf("sqlkit/sql: DROP SCHEMA requires a name")
	}
	var sb strings.Builder
	sb.WriteString("DROP SCHEMA ")
	if s.ifExists {
		sb.WriteString("IF EXISTS ")
	}
	sb.WriteString(c.Dialect.QuoteName(s.name))
	if s.cascade && c.Dialect.Name() != "mysql" {
		sb.WriteString(" CASCADE")
	}
	sb.WriteString(";")
	return sb.String(), nil
}

// CompileCreateTable renders a CREATE TABLE statement.
func (c *Compiler) CompileCreateTable(s CreateTableStmt) (string, error) {
	var defs []string
	for _, col := range s.columns {
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
	if len(s.primaryKey) > 0 && !c.inlinesAutoIncrementPK(s) {
		defs = append(defs, "PRIMARY KEY ("+c.quoteAll(s.primaryKey)+")")
	}
	for _, check := range s.checks {
		expr, err := CompileStatic(c.Dialect, check.Expression)
		if err != nil {
			return "", err
		}
		defs = append(defs, "CONSTRAINT "+c.Dialect.QuoteName(check.Name)+" CHECK ("+expr+")")
	}

	var sb strings.Builder
	sb.WriteString("CREATE TABLE ")
	if s.ifNotExists {
		sb.WriteString("IF NOT EXISTS ")
	}
	sb.WriteString(c.qualify(s.table))
	sb.WriteString(" (\n")
	for i, def := range defs {
		sb.WriteString("    " + def)
		if i < len(defs)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString(")" + c.Dialect.InlineTableComment(s.comment) + ";")
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
	if !ok || !ip.InlinesAutoIncrementPrimaryKey() || len(s.primaryKey) != 1 {
		return false
	}
	for _, col := range s.columns {
		if col.Name != s.primaryKey[0] || !col.AutoIncrement {
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

// compileColumnDef renders a single column definition.
func (c *Compiler) compileColumnDef(col ColumnDef) (string, error) {
	if col.Type == nil {
		return "", fmt.Errorf("sqlkit/sql: column %q has no type", col.Name)
	}
	name := c.Dialect.QuoteName(col.Name)
	if col.AutoIncrement {
		if ai, ok := col.Type.(AutoIncrementColumnType); ok {
			if clause, ok := ai.RenderAutoIncrement(c.Dialect); ok {
				return name + " " + clause + c.Dialect.InlineColumnComment(col.Comment), nil
			}
		}
	}
	typeClause, err := col.Type.RenderType(c.Dialect)
	if err != nil {
		return "", err
	}
	def := name + " " + typeClause
	if col.NotNull {
		def += " NOT NULL"
	}
	if col.Default != nil {
		expr, err := CompileStatic(c.Dialect, col.Default)
		if err != nil {
			return "", err
		}
		def += " DEFAULT " + expr
	}
	return def + c.Dialect.InlineColumnComment(col.Comment), nil
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
	name := c.Dialect.QuoteName(col.Name)
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
	if len(s.tables) == 0 {
		return "", fmt.Errorf("sqlkit/sql: DROP TABLE requires at least one table")
	}
	tables := make([]string, len(s.tables))
	for i, t := range s.tables {
		tables[i] = c.qualify(t)
	}
	var sb strings.Builder
	sb.WriteString("DROP TABLE ")
	if s.ifExists {
		sb.WriteString("IF EXISTS ")
	}
	sb.WriteString(strings.Join(tables, ", ") + ";")
	return sb.String(), nil
}

// CompileCreateIndex renders a CREATE INDEX statement.
func (c *Compiler) CompileCreateIndex(s CreateIndexStmt) (string, error) {
	kind := "INDEX"
	if s.unique {
		kind = "UNIQUE INDEX"
	}
	var sb strings.Builder
	sb.WriteString("CREATE " + kind + " ")
	if s.ifNotExists {
		sb.WriteString("IF NOT EXISTS ")
	}
	sb.WriteString(c.Dialect.QuoteName(s.name) + " ON " + c.qualify(s.table) +
		" (" + c.quoteAll(s.columns) + ");")
	return sb.String(), nil
}

// CompileDropIndex renders a DROP INDEX statement via the dialect.
func (c *Compiler) CompileDropIndex(s DropIndexStmt) (string, error) {
	return c.Dialect.DropIndex(
		c.qualifyName(s.table.Schema, s.name),
		c.Dialect.QuoteName(s.name),
		c.qualify(s.table),
	), nil
}

// CompileTruncate renders a TRUNCATE TABLE statement. RESTART IDENTITY and
// CASCADE, and truncating more than one table, are PostgreSQL features; SQLite
// has no TRUNCATE. The unsupported combinations are rejected rather than
// silently mis-rendered.
func (c *Compiler) CompileTruncate(s TruncateStmt) (string, error) {
	if len(s.tables) == 0 {
		return "", fmt.Errorf("sqlkit/sql: TRUNCATE requires at least one table")
	}
	name := c.Dialect.Name()
	if name == "sqlite" {
		return "", fmt.Errorf("sqlkit/sql: SQLite has no TRUNCATE; use DELETE FROM instead")
	}
	if len(s.tables) > 1 && name != "postgres" {
		return "", fmt.Errorf("sqlkit/sql: the %s dialect cannot TRUNCATE multiple tables in one statement", name)
	}
	if s.cascade && name != "postgres" {
		return "", fmt.Errorf("sqlkit/sql: CASCADE on TRUNCATE is supported only on the postgres dialect")
	}
	tables := make([]string, len(s.tables))
	for i, t := range s.tables {
		tables[i] = c.qualify(t)
	}
	var sb strings.Builder
	sb.WriteString("TRUNCATE TABLE ")
	sb.WriteString(strings.Join(tables, ", "))
	// RESTART IDENTITY is explicit on PostgreSQL and the standard target; MySQL
	// always resets AUTO_INCREMENT, so the option renders nothing there.
	if s.restartIdentity && name != "mysql" {
		sb.WriteString(" RESTART IDENTITY")
	}
	if s.cascade {
		sb.WriteString(" CASCADE")
	}
	sb.WriteString(";")
	return sb.String(), nil
}

// CompileDropForeignKey renders the drop-foreign-key statement via the dialect.
func (c *Compiler) CompileDropForeignKey(s DropForeignKeyStmt) (string, error) {
	return c.Dialect.DropForeignKey(c.qualify(s.table), c.Dialect.QuoteName(s.name)), nil
}

// CompileCommentOnTable renders a standalone table comment ("" when the dialect
// comments inline).
func (c *Compiler) CompileCommentOnTable(s CommentOnTableStmt) (string, error) {
	return c.Dialect.TableCommentStatement(c.qualify(s.table), s.comment), nil
}

// CompileCommentOnColumn renders a standalone column comment ("" when the
// dialect comments inline).
func (c *Compiler) CompileCommentOnColumn(s CommentOnColumnStmt) (string, error) {
	return c.Dialect.ColumnCommentStatement(c.qualify(s.table), c.Dialect.QuoteName(s.column), s.comment), nil
}

// CompileAlterTable renders an ALTER TABLE statement as a single
// comma-separated list of actions.
func (c *Compiler) CompileAlterTable(s AlterTableStmt) (string, error) {
	if len(s.alterations) == 0 {
		return "", fmt.Errorf("sqlkit/sql: ALTER TABLE requires at least one alteration")
	}
	clauses := make([]string, len(s.alterations))
	for i, a := range s.alterations {
		clause, err := c.compileAlteration(s.table, a)
		if err != nil {
			return "", err
		}
		clauses[i] = clause
	}
	return "ALTER TABLE " + c.qualify(s.table) + " " + strings.Join(clauses, ", ") + ";", nil
}

func (c *Compiler) compileAlteration(table Table, a alteration) (string, error) {
	switch v := a.(type) {
	case addColumnAlt:
		def, err := c.compileColumnDef(v.col)
		if err != nil {
			return "", err
		}
		return "ADD COLUMN " + def, nil
	case dropColumnAlt:
		return "DROP COLUMN " + c.Dialect.QuoteName(v.name), nil
	case restateColumnAlt:
		return c.compileRestateColumn(v.col)
	case addPrimaryKeyAlt:
		return "ADD PRIMARY KEY (" + c.quoteAll(v.columns) + ")", nil
	case dropPrimaryKeyAlt:
		if c.Dialect.Name() == "mysql" {
			return "DROP PRIMARY KEY", nil
		}
		// PostgreSQL has no DROP PRIMARY KEY; drop the constraint by its
		// conventional "<table>_pkey" name.
		return "DROP CONSTRAINT " + c.Dialect.QuoteName(table.Name+"_pkey"), nil
	case addForeignKeyAlt:
		return c.foreignKeyClause(v.fk), nil
	case addCheckAlt:
		expr, err := CompileStatic(c.Dialect, v.expr)
		if err != nil {
			return "", err
		}
		return "ADD CONSTRAINT " + c.Dialect.QuoteName(v.name) + " CHECK (" + expr + ")", nil
	case dropConstraintAlt:
		return "DROP CONSTRAINT " + c.Dialect.QuoteName(v.name), nil
	case renameColumnAlt:
		return "RENAME COLUMN " + c.Dialect.QuoteName(v.from) + " TO " + c.Dialect.QuoteName(v.to), nil
	case renameTableAlt:
		return "RENAME TO " + c.Dialect.QuoteName(v.name), nil
	case alterColumnTypeAlt:
		if err := c.requireColumnModify("ALTER COLUMN TYPE"); err != nil {
			return "", err
		}
		t, err := v.typ.RenderType(c.Dialect)
		if err != nil {
			return "", err
		}
		return "ALTER COLUMN " + c.Dialect.QuoteName(v.name) + " TYPE " + t, nil
	case notNullAlt:
		if err := c.requireColumnModify("SET/DROP NOT NULL"); err != nil {
			return "", err
		}
		op := "SET NOT NULL"
		if !v.notNull {
			op = "DROP NOT NULL"
		}
		return "ALTER COLUMN " + c.Dialect.QuoteName(v.name) + " " + op, nil
	case defaultAlt:
		if err := c.requireColumnModify("SET/DROP DEFAULT"); err != nil {
			return "", err
		}
		col := c.Dialect.QuoteName(v.name)
		if v.expr == nil {
			return "ALTER COLUMN " + col + " DROP DEFAULT", nil
		}
		expr, err := CompileStatic(c.Dialect, v.expr)
		if err != nil {
			return "", err
		}
		return "ALTER COLUMN " + col + " SET DEFAULT " + expr, nil
	default:
		return "", fmt.Errorf("sqlkit/sql: unsupported alteration %T", a)
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

func (c *Compiler) foreignKeyClause(fk ForeignKeyConstraint) string {
	clause := "ADD CONSTRAINT " + c.Dialect.QuoteName(fk.Name) +
		" FOREIGN KEY (" + c.quoteAll(fk.Columns) + ")" +
		" REFERENCES " + c.qualifyName(fk.RefSchema, fk.RefTable) +
		" (" + c.quoteAll(fk.RefColumns) + ")"
	if fk.OnDelete != "" {
		clause += " ON DELETE " + fk.OnDelete
	}
	if fk.OnUpdate != "" {
		clause += " ON UPDATE " + fk.OnUpdate
	}
	return clause
}

// qualify renders a schema-qualified table identifier for DDL, quoting each
// part as one name; an empty schema yields an unqualified name.
func (c *Compiler) qualify(t Table) string {
	return c.qualifyName(t.Schema, t.Name)
}

// qualifyName quotes schema.name, or just name when schema is empty. DDL uses
// QuoteName (not QuoteIdent) so a name containing a literal dot stays one
// identifier.
func (c *Compiler) qualifyName(schemaName, name string) string {
	if schemaName == "" {
		return c.Dialect.QuoteName(name)
	}
	return c.Dialect.QuoteName(schemaName) + "." + c.Dialect.QuoteName(name)
}

// quoteAll quotes each identifier with QuoteName and joins them with ", ".
func (c *Compiler) quoteAll(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = c.Dialect.QuoteName(name)
	}
	return strings.Join(quoted, ", ")
}
