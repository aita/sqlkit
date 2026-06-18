package sqlparser

import (
	"encoding/json"
	"errors"
)

// ErrUnavailable is returned by the parsing entry points when the binding was
// compiled without the `sqlparser_rs` build tag (the pure-Go stub). Build with
// `-tags sqlparser_rs` and a prebuilt Rust static library to enable parsing.
var ErrUnavailable = errors.New("sqlparser: built without the sqlparser_rs build tag; rebuild with -tags sqlparser_rs")

// Statement is a single parsed SQL statement in sqlparser-rs's own AST shape,
// kept as raw JSON. Higher layers (the sqlparse package) decode and map it onto
// sqlkit's sql.* types; callers that just want the native AST can unmarshal it
// directly.
type Statement = json.RawMessage

// Dialect selects the SQL grammar sqlparser-rs uses. Use one of the Dialect
// constants below. The zero value is the empty string, which Parse treats as
// DialectGeneric. An unrecognized dialect is rejected by Parse rather than
// silently falling back, so a typo surfaces instead of changing parse results.
type Dialect string

// Supported dialects, matching sqlparser-rs's dialect_from_str. Names are the
// canonical lowercase form sqlparser-rs expects.
const (
	DialectGeneric    Dialect = "generic"
	DialectMySQL      Dialect = "mysql"
	DialectPostgreSQL Dialect = "postgresql"
	DialectHive       Dialect = "hive"
	DialectSQLite     Dialect = "sqlite"
	DialectSnowflake  Dialect = "snowflake"
	DialectRedshift   Dialect = "redshift"
	DialectMsSQL      Dialect = "mssql"
	DialectClickHouse Dialect = "clickhouse"
	DialectBigQuery   Dialect = "bigquery"
	DialectANSI       Dialect = "ansi"
	DialectDuckDB     Dialect = "duckdb"
	DialectDatabricks Dialect = "databricks"
	DialectSpark      Dialect = "spark"
	DialectOracle     Dialect = "oracle"
	DialectTeradata   Dialect = "teradata"
)

// Dialects returns every dialect Parse accepts, in a stable order. Handy for
// building a CLI flag or validating user input before calling Parse.
func Dialects() []Dialect {
	return []Dialect{
		DialectGeneric, DialectMySQL, DialectPostgreSQL, DialectHive,
		DialectSQLite, DialectSnowflake, DialectRedshift, DialectMsSQL,
		DialectClickHouse, DialectBigQuery, DialectANSI, DialectDuckDB,
		DialectDatabricks, DialectSpark, DialectOracle, DialectTeradata,
	}
}

// String returns the dialect name.
func (d Dialect) String() string { return string(d) }

// parseEnvelope is the JSON document returned by the Rust bridge: exactly one of
// the two fields is populated.
type parseEnvelope struct {
	Statements []json.RawMessage `json:"statements"`
	Error      string            `json:"error"`
}

// decodeEnvelope turns the raw bridge document into statements or an error.
func decodeEnvelope(raw []byte) ([]Statement, error) {
	var env parseEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, errors.New("sqlparser: malformed bridge response: " + err.Error())
	}
	if env.Error != "" {
		return nil, &ParseError{Message: env.Error}
	}
	out := make([]Statement, len(env.Statements))
	for i, s := range env.Statements {
		out[i] = Statement(s)
	}
	return out, nil
}

// ParseError reports a parse failure surfaced by sqlparser-rs.
type ParseError struct {
	Message string
}

func (e *ParseError) Error() string { return "sqlparser: " + e.Message }
