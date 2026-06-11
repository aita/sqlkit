package sqlkit

import querysql "github.com/sqlkit-dev/sqlkit/sqlkit/sql"

// Dialect captures SQL syntax details that vary by database engine.
type Dialect = querysql.Dialect

// PostgreSQLDialect implements PostgreSQL identifier quoting and placeholders.
type PostgreSQLDialect = querysql.PostgreSQLDialect
