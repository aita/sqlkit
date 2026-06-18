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

// Dialect names accepted by Parse. Any name supported by sqlparser-rs's
// dialect_from_str is also accepted; these are the common ones plus a couple of
// aliases normalized by the Rust side ("postgres", "sqlite3").
const (
	DialectGeneric    = "generic"
	DialectPostgreSQL = "postgresql"
	DialectMySQL      = "mysql"
	DialectSQLite     = "sqlite"
	DialectANSI       = "ansi"
	DialectSnowflake  = "snowflake"
	DialectBigQuery   = "bigquery"
	DialectClickHouse = "clickhouse"
	DialectMsSQL      = "mssql"
	DialectRedshift   = "redshift"
	DialectDuckDB     = "duckdb"
	DialectHive       = "hive"
)

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
