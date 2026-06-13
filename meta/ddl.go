package meta

import (
	"fmt"
	"strings"

	"github.com/aita/sqlkit/sql"
)

// CreateSQL returns the SQL that creates the whole schema for dialect d (it
// does not touch a database): CREATE TABLE statements with primary keys and
// CHECK constraints, then CREATE INDEX statements, then ALTER TABLE statements
// for foreign keys (emitted after all tables so declaration order never
// matters), then comments. Catalogs built with the sqldef package and schemas
// read by the inspector both feed it through the same model.
//
// Default expressions render only when the model carries them (Column.DefaultExpr,
// declared as sqldef.Default(sql.Raw("now()"))), compiled for the dialect; an
// integer primary key with a default but no expression renders as a serial
// (PostgreSQL) or AUTO_INCREMENT (MySQL) column. It returns an error if a column
// has no type on the dialect or a default/check expression cannot be compiled.
func (md Metadata) CreateSQL(d sql.Dialect) (string, error) {
	var stmts []string
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
// their indexes. It is the inverse of CreateSQL — run it before regenerating
// the schema.
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
	return strings.Join(stmts, "\n\n") + "\n"
}

// CreateSQL returns the CREATE TABLE statement for the table on dialect d,
// including its primary key, CHECK constraints, and (where the dialect renders
// them inline) column and table comments. It returns an error if a column has
// no type on the dialect.
func (t Table) CreateSQL(d sql.Dialect) (string, error) {
	var defs []string
	var pk []string
	for _, column := range t.Columns {
		if column.Type == nil {
			return "", fmt.Errorf("meta: table %q column %q has no type", t.Name, column.Name)
		}
		eng, ok := column.Type.Engine(d.Name())
		if !ok {
			return "", fmt.Errorf("meta: table %q column %q has no type on dialect %s", t.Name, column.Name, d.Name())
		}
		def, err := columnDef(column, eng, d)
		if err != nil {
			return "", fmt.Errorf("meta: table %q column %q: %w", t.Name, column.Name, err)
		}
		defs = append(defs, def)
		if column.PrimaryKey {
			pk = append(pk, column.Name)
		}
	}
	if len(pk) > 0 {
		defs = append(defs, "PRIMARY KEY ("+quoteAll(d, pk)+")")
	}
	for _, check := range t.Checks {
		expr, err := sql.CompileStatic(d, check.Expression)
		if err != nil {
			return "", fmt.Errorf("meta: table %q check %q: %w", t.Name, check.Name, err)
		}
		defs = append(defs, "CONSTRAINT "+d.QuoteName(check.Name)+" CHECK ("+expr+")")
	}

	var sb strings.Builder
	sb.WriteString("CREATE TABLE " + qualify(d, t.Schema.Name, t.Name) + " (\n")
	for i, def := range defs {
		sb.WriteString("    " + def)
		if i < len(defs)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString(")" + d.InlineTableComment(t.Comment) + ";")
	return sb.String(), nil
}

// DropSQL returns the DROP TABLE statement for the table on dialect d.
func (t Table) DropSQL(d sql.Dialect) string {
	return "DROP TABLE IF EXISTS " + qualify(d, t.Schema.Name, t.Name) + ";"
}

// CreateSQL returns the CREATE INDEX statement for the index on its table.
func (ix Index) CreateSQL(table Table, d sql.Dialect) string {
	kind := "INDEX"
	if ix.Unique {
		kind = "UNIQUE INDEX"
	}
	return "CREATE " + kind + " " + d.QuoteName(ix.Name) + " ON " +
		qualify(d, table.Schema.Name, table.Name) + " (" + quoteAll(d, ix.Columns) + ");"
}

// DropSQL returns the DROP INDEX statement for the index on its table.
func (ix Index) DropSQL(table Table, d sql.Dialect) string {
	return d.DropIndex(
		qualify(d, table.Schema.Name, ix.Name),
		d.QuoteName(ix.Name),
		qualify(d, table.Schema.Name, table.Name),
	)
}

// addConstraintSQL renders the ALTER TABLE ... ADD CONSTRAINT statement that
// adds the foreign key to its table.
func (fk ForeignKey) addConstraintSQL(table Table, d sql.Dialect) string {
	return "ALTER TABLE " + qualify(d, table.Schema.Name, table.Name) +
		" ADD CONSTRAINT " + d.QuoteName(fk.Name) +
		" FOREIGN KEY (" + quoteAll(d, fk.Columns) + ")" +
		" REFERENCES " + qualify(d, fk.RefSchema.Name, fk.RefTable) +
		" (" + quoteAll(d, fk.RefColumns) + ");"
}

// dropConstraintSQL renders the ALTER TABLE ... statement that drops the
// foreign key from its table.
func (fk ForeignKey) dropConstraintSQL(table Table, d sql.Dialect) string {
	return d.DropForeignKey(qualify(d, table.Schema.Name, table.Name), d.QuoteName(fk.Name))
}

// commentStatementsSQL renders the table's standalone comment statements
// (PostgreSQL COMMENT ON for the table and its columns), or "" on dialects that
// render comments inline in the CREATE TABLE statement (MySQL).
func (t Table) commentStatementsSQL(d sql.Dialect) string {
	var stmts []string
	qualified := qualify(d, t.Schema.Name, t.Name)
	if stmt := d.TableCommentStatement(qualified, t.Comment); stmt != "" {
		stmts = append(stmts, stmt)
	}
	for _, column := range t.Columns {
		if stmt := d.ColumnCommentStatement(qualified, d.QuoteName(column.Name), column.Comment); stmt != "" {
			stmts = append(stmts, stmt)
		}
	}
	return strings.Join(stmts, "\n")
}

// columnDef renders a single column definition for the resolved engine type.
func columnDef(column Column, eng Engine, d sql.Dialect) (string, error) {
	if column.PrimaryKey && column.HasDefault && column.DefaultExpr == nil {
		if clause, ok := d.AutoIncrementColumn(eng.Name, eng.Length, eng.Precision, eng.Scale); ok {
			return d.QuoteName(column.Name) + " " + clause + d.InlineColumnComment(column.Comment), nil
		}
	}
	def := d.QuoteName(column.Name) + " " + d.ColumnType(eng.Name, eng.Length, eng.Precision, eng.Scale)
	if !column.Nullable {
		def += " NOT NULL"
	}
	if column.DefaultExpr != nil {
		expr, err := sql.CompileStatic(d, column.DefaultExpr)
		if err != nil {
			return "", err
		}
		def += " DEFAULT " + expr
	}
	return def + d.InlineColumnComment(column.Comment), nil
}

// qualify renders a schema-qualified identifier, quoting each part; an empty
// schema yields an unqualified name.
func qualify(d sql.Dialect, schemaName, name string) string {
	if schemaName == "" {
		return d.QuoteName(name)
	}
	return d.QuoteName(schemaName) + "." + d.QuoteName(name)
}

// quoteAll quotes each identifier and joins them with ", ".
func quoteAll(d sql.Dialect, names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = d.QuoteName(name)
	}
	return strings.Join(quoted, ", ")
}
