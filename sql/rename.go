package sql

import "fmt"

// RenameTableStmt builds an ALTER TABLE ... RENAME TO statement. It is a DDL
// Statement.
type RenameTableStmt struct {
	table   Table
	newName string
}

// RenameTable starts a builder that renames a table to newName.
func RenameTable(table Table, newName string) RenameTableStmt {
	return RenameTableStmt{table: table, newName: newName}
}

// ToSQL compiles the rename-table statement.
func (s RenameTableStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s RenameTableStmt) compileDDL(c *Compiler) (string, error) {
	if s.table.Name == "" || s.newName == "" {
		return "", fmt.Errorf("sqlkit/sql: RENAME TABLE requires a table and a new name")
	}
	return "ALTER TABLE " + c.qualify(s.table) + " RENAME TO " + c.Dialect.QuoteName(s.newName) + ";", nil
}
func (RenameTableStmt) ddlStatement() {}

// RenameColumnStmt builds an ALTER TABLE ... RENAME COLUMN ... TO statement.
type RenameColumnStmt struct {
	table   Table
	oldName string
	newName string
}

// RenameColumn starts a builder that renames a column of a table.
func RenameColumn(table Table, oldName, newName string) RenameColumnStmt {
	return RenameColumnStmt{table: table, oldName: oldName, newName: newName}
}

// ToSQL compiles the rename-column statement.
func (s RenameColumnStmt) ToSQL(d Dialect) (string, []any, error) {
	out, err := s.compileDDL(NewCompiler(d))
	return out, nil, err
}
func (s RenameColumnStmt) compileDDL(c *Compiler) (string, error) {
	if s.table.Name == "" || s.oldName == "" || s.newName == "" {
		return "", fmt.Errorf("sqlkit/sql: RENAME COLUMN requires a table, an old name, and a new name")
	}
	return "ALTER TABLE " + c.qualify(s.table) + " RENAME COLUMN " +
		c.Dialect.QuoteName(s.oldName) + " TO " + c.Dialect.QuoteName(s.newName) + ";", nil
}
func (RenameColumnStmt) ddlStatement() {}
