package sql

import "fmt"

// TransactionStmt is a transaction-control statement: BEGIN, COMMIT, ROLLBACK,
// or the savepoint forms (SAVEPOINT, RELEASE SAVEPOINT, ROLLBACK TO SAVEPOINT).
// It binds no values and compiles the same on every dialect. Build one with
// Begin, Commit, Rollback, Savepoint, ReleaseSavepoint, or RollbackTo.
type TransactionStmt struct {
	kind      string
	savepoint string
}

// Begin starts a transaction (BEGIN).
func Begin() TransactionStmt { return TransactionStmt{kind: "BEGIN"} }

// Commit commits the current transaction (COMMIT).
func Commit() TransactionStmt { return TransactionStmt{kind: "COMMIT"} }

// Rollback aborts the current transaction (ROLLBACK).
func Rollback() TransactionStmt { return TransactionStmt{kind: "ROLLBACK"} }

// Savepoint establishes a named savepoint (SAVEPOINT name).
func Savepoint(name string) TransactionStmt {
	return TransactionStmt{kind: "SAVEPOINT", savepoint: name}
}

// ReleaseSavepoint destroys a named savepoint (RELEASE SAVEPOINT name).
func ReleaseSavepoint(name string) TransactionStmt {
	return TransactionStmt{kind: "RELEASE SAVEPOINT", savepoint: name}
}

// RollbackTo rolls back to a named savepoint (ROLLBACK TO SAVEPOINT name).
func RollbackTo(name string) TransactionStmt {
	return TransactionStmt{kind: "ROLLBACK TO SAVEPOINT", savepoint: name}
}

// ToSQL compiles the transaction-control statement.
func (s TransactionStmt) ToSQL(dialect Dialect) (string, []any, error) {
	return s.compile(NewCompiler(dialect))
}

func (s TransactionStmt) compile(c *Compiler) (string, []any, error) {
	switch s.kind {
	case "BEGIN", "COMMIT", "ROLLBACK":
		return s.kind + ";", nil, nil
	case "SAVEPOINT", "RELEASE SAVEPOINT", "ROLLBACK TO SAVEPOINT":
		if s.savepoint == "" {
			return "", nil, fmt.Errorf("sqlkit/sql: %s requires a savepoint name", s.kind)
		}
		return s.kind + " " + c.Dialect.QuoteName(s.savepoint) + ";", nil, nil
	default:
		return "", nil, fmt.Errorf("sqlkit/sql: unknown transaction statement %q", s.kind)
	}
}
