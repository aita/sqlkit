package querytrace

import (
	"time"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/x/sqlparse"
)

// CallerMode controls when the wrapped driver captures the calling code for an
// Event. Capturing a caller walks the stack, so the cost is paid only when the
// mode says it is worth it.
type CallerMode int

const (
	// CallerOff never captures a caller.
	CallerOff CallerMode = iota
	// CallerOnError captures a caller only for statements that errored.
	CallerOnError
	// CallerOnSlowQuery captures a caller for errors and for statements slower
	// than Config.SlowQueryThreshold.
	CallerOnSlowQuery
	// CallerAlways captures a caller for every statement. Best for development
	// and tests, where caller attribution drives N+1 and loop detection.
	CallerAlways
)

// ArgCaptureMode controls how much of a statement's bind arguments are kept.
// Values are sensitive, so the default keeps none.
type ArgCaptureMode int

const (
	// ArgsOff keeps no argument information beyond the count.
	ArgsOff ArgCaptureMode = iota
	// ArgsTypesOnly keeps the Go type name of each argument, not its value.
	ArgsTypesOnly
)

// Config configures a wrapped driver and the Traces it records. The zero value
// is usable but minimal; DebugConfig is the recommended starting point for
// development and tests.
type Config struct {
	// Enabled turns tracing on. A disabled Config makes the wrapped driver a
	// thin pass-through.
	Enabled bool

	// Dialect is the SQL dialect used to recompile a parsed query into its
	// fingerprint. It defaults to PostgreSQL when nil.
	Dialect sql.Dialect

	// Parser analyzes SQL text into a sql.Query for structural analysis and
	// fingerprinting. It is best-effort: a parse failure or a nil Parser falls
	// back to lightweight string analysis and never aborts execution.
	//
	// DebugConfig wires this automatically from its dialect argument, provided a
	// frontend such as github.com/aita/sqlkit/x/sqlparse/postgres is
	// blank-imported. Set it explicitly to override that, for example to use a
	// custom parser.
	Parser sqlparse.Parser

	// CaptureRawSQL keeps the executed SQL text on each QueryInfo. Leave it off
	// for production exporters; raw SQL can carry sensitive shapes.
	CaptureRawSQL bool
	// CaptureArgs controls how much of the bind arguments are kept.
	CaptureArgs ArgCaptureMode
	// CaptureCaller controls when the calling code is captured.
	CaptureCaller CallerMode

	// Fingerprint enables SQL fingerprinting, the basis of repeated-query and
	// N+1 detection.
	Fingerprint bool
	// AnalyzeSQL enables structural analysis (kind, tables, WHERE/JOIN/...),
	// the basis of write/where warnings.
	AnalyzeSQL bool

	// SlowQueryThreshold is the duration above which a query counts as slow,
	// for CallerOnSlowQuery.
	SlowQueryThreshold time.Duration

	// NPlusOne configures repeated-query and loop detection.
	NPlusOne NPlusOneConfig
	// Warnings configures the threshold-based warnings.
	Warnings WarningConfig
}

// NPlusOneConfig configures detection of repeated query shapes within one Trace.
type NPlusOneConfig struct {
	// Enabled turns repeated-query and loop detection on.
	Enabled bool
	// MinCount is how many times one fingerprint must repeat before it is
	// reported. It defaults to 10 when zero.
	MinCount int
	// IgnoreOperations skips fingerprints of these kinds; repeated INSERTs into
	// a log table, for example, are usually expected.
	IgnoreOperations []StmtKind
}

// WarningConfig configures the threshold-based warnings.
type WarningConfig struct {
	// MaxQueries is the per-Trace query count above which too_many_queries
	// fires. Zero disables the check.
	MaxQueries int
	// LongTransaction is the transaction duration above which long_transaction
	// fires. Zero disables the check.
	LongTransaction time.Duration
}

// DebugConfig returns a Config tuned for development and tests for the named
// dialect ("postgres", "mysql", or "sqlite"; "" and unknown names default to
// postgres): raw SQL and argument types captured, callers always captured,
// fingerprinting and analysis on, and N+1 detection enabled. It captures more
// than is appropriate for production.
//
// The matching sql.Dialect is stored in Dialect, and a parser registered for
// that dialect — via a blank-imported frontend such as
// github.com/aita/sqlkit/x/sqlparse/postgres — is wired into Parser
// automatically. Without such an import Parser stays nil and analysis falls
// back to lightweight string analysis.
func DebugConfig(dialect string) Config {
	cfg := Config{
		Enabled:            true,
		Dialect:            dialectByName(dialect),
		CaptureRawSQL:      true,
		CaptureArgs:        ArgsTypesOnly,
		CaptureCaller:      CallerAlways,
		Fingerprint:        true,
		AnalyzeSQL:         true,
		SlowQueryThreshold: 100 * time.Millisecond,
		NPlusOne:           NPlusOneConfig{Enabled: true, MinCount: 10},
	}
	cfg.Parser, _ = sqlparse.For(cfg.Dialect.Name())
	return cfg
}

// dialectByName maps a dialect name (matching sql.Dialect.Name()) to its
// sql.Dialect. "" and unknown names default to PostgreSQL.
func dialectByName(name string) sql.Dialect {
	switch name {
	case "mysql":
		return sql.MySQLDialect{}
	case "sqlite":
		return sql.SQLiteDialect{}
	default:
		return sql.PostgreSQLDialect{}
	}
}

// dialect returns the configured dialect, defaulting to PostgreSQL.
func (c Config) dialect() sql.Dialect {
	if c.Dialect == nil {
		return sql.PostgreSQLDialect{}
	}
	return c.Dialect
}

// nPlusOneMinCount returns the effective repeat threshold.
func (c Config) nPlusOneMinCount() int {
	if c.NPlusOne.MinCount <= 0 {
		return 10
	}
	return c.NPlusOne.MinCount
}
