package sqlkit

import querysql "github.com/sqlkit-dev/sqlkit/sqlkit/sql"

// Dialect captures SQL syntax details that vary by database engine.
type Dialect = querysql.Dialect

// PostgreSQLDialect implements PostgreSQL identifier quoting and placeholders.
type PostgreSQLDialect = querysql.PostgreSQLDialect

// MySQLDialect implements MySQL identifier quoting and placeholders; see
// sql.MySQLDialect for its RETURNING and codegen caveats.
type MySQLDialect = querysql.MySQLDialect

// DialectForDriver maps a database/sql driver name to a dialect: "mysql"
// selects MySQL, everything else defaults to PostgreSQL. Open uses it to
// infer the dialect; override with WithDialect.
func DialectForDriver(driverName string) Dialect {
	switch driverName {
	case "mysql":
		return querysql.MySQLDialect{}
	default:
		return querysql.PostgreSQLDialect{}
	}
}
