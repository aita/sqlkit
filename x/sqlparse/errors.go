package sqlparse

import (
	"errors"
	"fmt"
)

// ErrNoStatement is returned by ParseOne when the input holds no statement.
var ErrNoStatement = errors.New("sqlkit/sqlparse: no statement")

// UnsupportedError reports a SQL construct a frontend recognized but cannot yet
// represent as a sqlkit/sql node. Construct describes the offending node, e.g.
// "GROUPING SETS" or "expression node *pg_query.A_ArrayExpr".
type UnsupportedError struct {
	Construct string
}

func (e *UnsupportedError) Error() string {
	return "sqlkit/sqlparse: unsupported SQL construct: " + e.Construct
}

// Unsupported builds an UnsupportedError for the named construct.
func Unsupported(construct string) error {
	return &UnsupportedError{Construct: construct}
}

// Unsupportedf builds an UnsupportedError with a formatted construct name.
func Unsupportedf(format string, args ...any) error {
	return &UnsupportedError{Construct: fmt.Sprintf(format, args...)}
}

// errStatementCount reports that ParseOne found more than one statement.
func errStatementCount(n int) error {
	return fmt.Errorf("sqlkit/sqlparse: expected one statement, got %d", n)
}
