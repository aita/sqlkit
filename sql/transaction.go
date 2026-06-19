package sql

import "fmt"

// TransactionStmt is a transaction-control statement: BEGIN, COMMIT, ROLLBACK,
// or the savepoint forms (SAVEPOINT, RELEASE SAVEPOINT, ROLLBACK TO SAVEPOINT).
// It binds no values and compiles the same on every dialect. Build one with
// Begin, Commit, Rollback, Savepoint, ReleaseSavepoint, or RollbackTo.
type TransactionStmt struct {
	Kind      string
	Savepoint string
}

// Begin starts a transaction (BEGIN).
func Begin() TransactionStmt { return TransactionStmt{Kind: "BEGIN"} }

// Commit commits the current transaction (COMMIT).
func Commit() TransactionStmt { return TransactionStmt{Kind: "COMMIT"} }

// Rollback aborts the current transaction (ROLLBACK).
func Rollback() TransactionStmt { return TransactionStmt{Kind: "ROLLBACK"} }

// Savepoint establishes a named savepoint (SAVEPOINT name).
func Savepoint(name string) TransactionStmt {
	return TransactionStmt{Kind: "SAVEPOINT", Savepoint: name}
}

// ReleaseSavepoint destroys a named savepoint (RELEASE SAVEPOINT name).
func ReleaseSavepoint(name string) TransactionStmt {
	return TransactionStmt{Kind: "RELEASE SAVEPOINT", Savepoint: name}
}

// RollbackTo rolls back to a named savepoint (ROLLBACK TO SAVEPOINT name).
func RollbackTo(name string) TransactionStmt {
	return TransactionStmt{Kind: "ROLLBACK TO SAVEPOINT", Savepoint: name}
}

// ToSQL compiles the transaction-control statement.
func (s TransactionStmt) ToSQL(dialect Dialect) (string, []any, error) {
	return s.compile(NewCompiler(dialect))
}

func (s TransactionStmt) compile(c *Compiler) (string, []any, error) {
	switch s.Kind {
	case "BEGIN", "COMMIT", "ROLLBACK":
		return s.Kind + ";", nil, nil
	case "SAVEPOINT", "RELEASE SAVEPOINT", "ROLLBACK TO SAVEPOINT":
		if s.Savepoint == "" {
			return "", nil, fmt.Errorf("sqlkit/sql: %s requires a savepoint name", s.Kind)
		}
		return s.Kind + " " + c.Dialect.QuoteName(s.Savepoint) + ";", nil, nil
	default:
		return "", nil, fmt.Errorf("sqlkit/sql: unknown transaction statement %q", s.Kind)
	}
}
