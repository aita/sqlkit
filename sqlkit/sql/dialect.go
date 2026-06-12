package sql

import (
	"strconv"
	"strings"
)

// Dialect captures SQL syntax details that vary by database engine. The
// builders compile through a Dialect, so adding an engine means implementing
// this interface; PostgreSQL is the default and the most exercised.
type Dialect interface {
	// Name identifies the dialect, e.g. "postgres" or "mysql".
	Name() string

	// QuoteIdent quotes a possibly dot-qualified identifier.
	QuoteIdent(identifier string) string

	// Placeholder renders the bind placeholder for the 1-based position.
	Placeholder(position int) string

	// SupportsReturning reports whether INSERT/UPDATE/DELETE may carry a
	// RETURNING clause. Dialects without it reject Returning at compile time.
	SupportsReturning() bool
}

// PostgreSQLDialect implements PostgreSQL identifier quoting and placeholders.
type PostgreSQLDialect struct{}

// Name identifies the dialect.
func (PostgreSQLDialect) Name() string { return "postgres" }

func (PostgreSQLDialect) QuoteIdent(identifier string) string {
	parts := strings.Split(identifier, ".")
	for i, part := range parts {
		parts[i] = `"` + strings.ReplaceAll(part, `"`, `""`) + `"`
	}
	return strings.Join(parts, ".")
}

func (PostgreSQLDialect) Placeholder(position int) string {
	return "$" + strconv.Itoa(position)
}

// SupportsReturning reports RETURNING support (PostgreSQL has it).
func (PostgreSQLDialect) SupportsReturning() bool { return true }

// MySQLDialect implements MySQL identifier quoting and placeholders. The
// query builders compile against it; RETURNING is rejected at compile time
// (MySQL has no RETURNING — read generated keys via Result.LastInsertId).
// sqlkitgen's schema loading is PostgreSQL-only for now.
type MySQLDialect struct{}

// Name identifies the dialect.
func (MySQLDialect) Name() string { return "mysql" }

func (MySQLDialect) QuoteIdent(identifier string) string {
	parts := strings.Split(identifier, ".")
	for i, part := range parts {
		parts[i] = "`" + strings.ReplaceAll(part, "`", "``") + "`"
	}
	return strings.Join(parts, ".")
}

func (MySQLDialect) Placeholder(int) string { return "?" }

// SupportsReturning reports RETURNING support (MySQL lacks it).
func (MySQLDialect) SupportsReturning() bool { return false }
