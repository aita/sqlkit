package sql

import (
	"fmt"
	"strings"
)

// This file holds the Compiler methods that render DDL statement nodes. They
// reuse the Dialect's DDL helpers (ColumnType, AutoIncrementColumn, comment and
// drop helpers, QuoteName) so engine differences stay in one place, and they
// reproduce the exact formatting the meta package depends on.

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
	if len(s.primaryKey) > 0 {
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

// CompileDropTable renders a DROP TABLE statement.
func (c *Compiler) CompileDropTable(s DropTableStmt) (string, error) {
	var sb strings.Builder
	sb.WriteString("DROP TABLE ")
	if s.ifExists {
		sb.WriteString("IF EXISTS ")
	}
	sb.WriteString(c.qualify(s.table) + ";")
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
		clause, err := c.compileAlteration(a)
		if err != nil {
			return "", err
		}
		clauses[i] = clause
	}
	return "ALTER TABLE " + c.qualify(s.table) + " " + strings.Join(clauses, ", ") + ";", nil
}

func (c *Compiler) compileAlteration(a alteration) (string, error) {
	switch v := a.(type) {
	case addColumnAlt:
		def, err := c.compileColumnDef(v.col)
		if err != nil {
			return "", err
		}
		return "ADD COLUMN " + def, nil
	case dropColumnAlt:
		return "DROP COLUMN " + c.Dialect.QuoteName(v.name), nil
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
	return "ADD CONSTRAINT " + c.Dialect.QuoteName(fk.Name) +
		" FOREIGN KEY (" + c.quoteAll(fk.Columns) + ")" +
		" REFERENCES " + c.qualifyName(fk.RefSchema, fk.RefTable) +
		" (" + c.quoteAll(fk.RefColumns) + ")"
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
