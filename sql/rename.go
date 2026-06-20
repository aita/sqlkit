package sql

import "fmt"

// RenameTableStmt is the AST for an ALTER TABLE ... RENAME TO statement. It has
// no options, so it is constructed directly with RenameTable.
type RenameTableStmt struct {
	posField
	Table   Table
	NewName Ident
}

// RenameTable builds a statement that renames a table to newName.
func RenameTable(table Table, newName string) RenameTableStmt {
	return RenameTableStmt{Table: table, NewName: NewIdent(newName)}
}

// ToSQL compiles the rename-table statement.
func (s RenameTableStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s RenameTableStmt) compileDDL(c *Compiler) (string, error) {
	if s.Table.Name.IsZero() || s.NewName.IsZero() {
		return "", fmt.Errorf("sqlkit/sql: RENAME TABLE requires a table and a new name")
	}
	return "ALTER TABLE " + c.qualify(s.Table) + " RENAME TO " + c.Dialect.QuoteName(s.NewName.String()) + ";", nil
}
func (RenameTableStmt) ddlStatement() {}

// RenameColumnStmt is the AST for an ALTER TABLE ... RENAME COLUMN ... TO
// statement. It has no options, so it is constructed directly.
type RenameColumnStmt struct {
	posField
	Table   Table
	OldName Ident
	NewName Ident
}

// RenameColumn builds a statement that renames a column of a table.
func RenameColumn(table Table, oldName, newName string) RenameColumnStmt {
	return RenameColumnStmt{Table: table, OldName: NewIdent(oldName), NewName: NewIdent(newName)}
}

// ToSQL compiles the rename-column statement.
func (s RenameColumnStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s RenameColumnStmt) compileDDL(c *Compiler) (string, error) {
	if s.Table.Name.IsZero() || s.OldName.IsZero() || s.NewName.IsZero() {
		return "", fmt.Errorf("sqlkit/sql: RENAME COLUMN requires a table, an old name, and a new name")
	}
	return "ALTER TABLE " + c.qualify(s.Table) + " RENAME COLUMN " +
		c.Dialect.QuoteName(s.OldName.String()) + " TO " + c.Dialect.QuoteName(s.NewName.String()) + ";", nil
}
func (RenameColumnStmt) ddlStatement() {}
