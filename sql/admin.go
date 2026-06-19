package sql

import (
	"fmt"
	"strings"
)

// This file holds session/administrative statements that are MySQL-flavored
// utility commands rather than data queries: USE, LOCK/UNLOCK TABLES, and SET.
// They bind no values; SET renders its assignment values inline (static), as a
// SET statement carries no bind parameters. Each is a public-field AST value
// built by a plain constructor.

// UseStmt selects the session's default database (USE db). It is produced by the
// MySQL frontend; the same text is valid on MariaDB.
type UseStmt struct{ DB string }

// Use builds a USE statement selecting the default database.
func Use(db string) UseStmt { return UseStmt{DB: db} }

// ToSQL compiles the USE statement.
func (s UseStmt) ToSQL(d Dialect) (string, []any, error) { return s.compile(NewCompiler(d)) }

func (s UseStmt) compile(c *Compiler) (string, []any, error) {
	if s.DB == "" {
		return "", nil, fmt.Errorf("sqlkit/sql: USE requires a database name")
	}
	return "USE " + c.Dialect.QuoteName(s.DB) + ";", nil, nil
}

// TableLockMode is the lock a LOCK TABLES entry requests.
type TableLockMode string

const (
	// LockRead requests a READ lock (shared).
	LockRead TableLockMode = "READ"
	// LockWrite requests a WRITE lock (exclusive).
	LockWrite TableLockMode = "WRITE"
)

// TableLock pairs a table with the mode to lock it in within a LockTablesStmt.
type TableLock struct {
	Table Table
	Mode  TableLockMode
}

// LockTablesStmt acquires session table locks (MySQL LOCK TABLES t READ, u
// WRITE). It is a MySQL utility statement.
type LockTablesStmt struct{ Locks []TableLock }

// LockTables builds a LOCK TABLES statement over the given table locks.
func LockTables(locks ...TableLock) LockTablesStmt { return LockTablesStmt{Locks: locks} }

// ToSQL compiles the LOCK TABLES statement.
func (s LockTablesStmt) ToSQL(d Dialect) (string, []any, error) { return s.compile(NewCompiler(d)) }

func (s LockTablesStmt) compile(c *Compiler) (string, []any, error) {
	if len(s.Locks) == 0 {
		return "", nil, fmt.Errorf("sqlkit/sql: LOCK TABLES requires at least one table")
	}
	var sb strings.Builder
	sb.WriteString("LOCK TABLES ")
	for i, l := range s.Locks {
		if l.Mode != LockRead && l.Mode != LockWrite {
			return "", nil, fmt.Errorf("sqlkit/sql: LOCK TABLES mode must be READ or WRITE")
		}
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(c.qualify(l.Table))
		sb.WriteString(" ")
		sb.WriteString(string(l.Mode))
	}
	sb.WriteString(";")
	return sb.String(), nil, nil
}

// UnlockTablesStmt releases all of the session's table locks (UNLOCK TABLES).
type UnlockTablesStmt struct{}

// UnlockTables builds an UNLOCK TABLES statement.
func UnlockTables() UnlockTablesStmt { return UnlockTablesStmt{} }

// ToSQL compiles the UNLOCK TABLES statement.
func (s UnlockTablesStmt) ToSQL(d Dialect) (string, []any, error) { return s.compile(NewCompiler(d)) }

func (UnlockTablesStmt) compile(*Compiler) (string, []any, error) {
	return "UNLOCK TABLES;", nil, nil
}

// SetVar is one assignment in a SET statement. Value must bind no values (it is
// rendered inline). Scope is "", "GLOBAL", or "SESSION" for a system variable;
// User marks a user variable (@name).
type SetVar struct {
	Name  string
	Value Expression
	Scope string
	User  bool
}

// SetStmt assigns one or more variables (SET ...). It is produced by the MySQL
// frontend for the system/user-variable forms; SET NAMES and other special
// forms are not modeled here.
type SetStmt struct{ Vars []SetVar }

// SetVariables builds a SET statement over the given variable assignments.
func SetVariables(vars ...SetVar) SetStmt { return SetStmt{Vars: vars} }

// ToSQL compiles the SET statement.
func (s SetStmt) ToSQL(d Dialect) (string, []any, error) { return s.compile(NewCompiler(d)) }

func (s SetStmt) compile(c *Compiler) (string, []any, error) {
	if len(s.Vars) == 0 {
		return "", nil, fmt.Errorf("sqlkit/sql: SET requires at least one assignment")
	}
	var sb strings.Builder
	sb.WriteString("SET ")
	for i, v := range s.Vars {
		if v.Name == "" {
			return "", nil, fmt.Errorf("sqlkit/sql: SET assignment requires a variable name")
		}
		if v.Value == nil {
			return "", nil, fmt.Errorf("sqlkit/sql: SET %q requires a value", v.Name)
		}
		value, err := CompileStatic(c.Dialect, v.Value)
		if err != nil {
			return "", nil, err
		}
		if i > 0 {
			sb.WriteString(", ")
		}
		switch {
		case v.User:
			sb.WriteString("@" + v.Name)
		case v.Scope != "":
			sb.WriteString(v.Scope + " " + v.Name)
		default:
			sb.WriteString(v.Name)
		}
		sb.WriteString(" = ")
		sb.WriteString(value)
	}
	sb.WriteString(";")
	return sb.String(), nil, nil
}
