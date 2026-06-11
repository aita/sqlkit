package sql

import (
	"strconv"
	"strings"
)

// Dialect captures SQL syntax details that vary by database engine.
type Dialect interface {
	QuoteIdent(identifier string) string
	Placeholder(position int) string
}

// PostgreSQLDialect implements PostgreSQL identifier quoting and placeholders.
type PostgreSQLDialect struct{}

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
