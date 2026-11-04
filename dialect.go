package sqlkit

import (
	"sync"

	querysql "github.com/aita/sqlkit/sql"
)

// Dialect captures SQL syntax details that vary by database engine.
type Dialect = querysql.Dialect

// PostgreSQLDialect implements PostgreSQL identifier quoting and placeholders.
type PostgreSQLDialect = querysql.PostgreSQLDialect

// MySQLDialect implements MySQL identifier quoting and placeholders; see
// sql.MySQLDialect for its RETURNING and codegen caveats.
type MySQLDialect = querysql.MySQLDialect

// SQLiteDialect implements SQLite identifier quoting, placeholders, and DDL
// rendering; see sql.SQLiteDialect. It has no bundled driver and is reached via
// WithDialect (or DialectForDriver after RegisterDriverDialect).
type SQLiteDialect = querysql.SQLiteDialect

// LibSQLDialect renders SQL for libSQL (the Turso SQLite fork); see
// sql.LibSQLDialect. libSQL is SQL-compatible with SQLite, so this embeds
// SQLiteDialect and differs only in name.
type LibSQLDialect = querysql.LibSQLDialect

// StandardSQLDialect renders portable ANSI/SQL-standard text; see
// sql.StandardSQLDialect. It has no bundled driver and is reached via
// WithDialect (or DialectForDriver after RegisterDriverDialect).
type StandardSQLDialect = querysql.StandardSQLDialect

// driverDialectsMu guards driverDialects: RegisterDriverDialect and
// DialectForDriver are exported package-level functions with no other
// synchronization, and both are ordinary to call concurrently (e.g. opening
// several DB handles at startup).
var driverDialectsMu sync.RWMutex

// driverDialects maps database/sql driver names to dialects. It seeds the names
// of the drivers sqlkit knows: PostgreSQL (pgx) and MySQL. Extend it with
// RegisterDriverDialect for other drivers.
var driverDialects = map[string]Dialect{
	"pgx":      querysql.PostgreSQLDialect{},
	"postgres": querysql.PostgreSQLDialect{},
	"mysql":    querysql.MySQLDialect{},
}

// RegisterDriverDialect maps a database/sql driver name to a dialect so Open and
// DialectForDriver resolve it. Use it for a driver sqlkit does not seed by
// default, or to point a custom/aliased driver name at an existing dialect
// (e.g. RegisterDriverDialect("mysql-custom", MySQLDialect{})). It overwrites
// any existing mapping for the name.
func RegisterDriverDialect(driverName string, d Dialect) {
	driverDialectsMu.Lock()
	defer driverDialectsMu.Unlock()
	driverDialects[driverName] = d
}

// DialectForDriver maps a database/sql driver name to a dialect. Known drivers
// (registered by default or via RegisterDriverDialect) resolve to their dialect;
// any other name defaults to PostgreSQL. Open uses it to infer the dialect;
// override with WithDialect.
func DialectForDriver(driverName string) Dialect {
	driverDialectsMu.RLock()
	defer driverDialectsMu.RUnlock()
	if d, ok := driverDialects[driverName]; ok {
		return d
	}
	return querysql.PostgreSQLDialect{}
}
