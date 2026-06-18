package querytrace

import (
	"time"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
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
// is usable but minimal; NewConfig is the recommended starting point.
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
	// Wire it explicitly with a frontend's parser, for example postgres.New()
	// from github.com/aita/sqlkit/sqlparse/postgres. NewConfig sets it via
	// WithParser.
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
	// MaxParams is the bind-parameter count at or above which a statement raises
	// too_many_parameters. Zero disables the check.
	MaxParams int
}

// ConfigOption configures a Config built by NewConfig. Options compose, so a
// caller layers only the choices that differ from the defaults.
type ConfigOption func(*Config)

// NewConfig returns a Config built from opts. With no options it is enabled with
// production-minded defaults: no raw SQL, argument *types* only (never values),
// callers captured on error, fingerprinting, structural analysis, and N+1
// detection on, and the PostgreSQL dialect. Layer options to capture more for
// development (WithVerboseCapture) or to adjust any single field.
//
//	// Development: capture everything, wire a parser for structural analysis.
//	cfg := querytrace.NewConfig(
//		querytrace.WithParser(postgres.New()),
//		querytrace.WithVerboseCapture(),
//	)
//
//	// Production: defaults are already safe; just wire the parser.
//	cfg := querytrace.NewConfig(querytrace.WithParser(postgres.New()))
func NewConfig(opts ...ConfigOption) Config {
	cfg := Config{
		Enabled:            true,
		Dialect:            sql.PostgreSQLDialect{},
		CaptureArgs:        ArgsTypesOnly,
		CaptureCaller:      CallerOnError,
		Fingerprint:        true,
		AnalyzeSQL:         true,
		SlowQueryThreshold: 100 * time.Millisecond,
		NPlusOne:           NPlusOneConfig{Enabled: true, MinCount: 10},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// WithDialect sets the SQL dialect used to recompile parsed queries into
// fingerprints. A nil dialect is ignored, leaving the PostgreSQL default.
func WithDialect(d sql.Dialect) ConfigOption {
	return func(c *Config) {
		if d != nil {
			c.Dialect = d
		}
	}
}

// WithParser sets the parser that drives structural analysis and fingerprinting.
// Pass a frontend's parser, for example postgres.New() from
// github.com/aita/sqlkit/sqlparse/postgres. Without a parser, analysis falls
// back to lightweight string heuristics.
func WithParser(p sqlparse.Parser) ConfigOption {
	return func(c *Config) { c.Parser = p }
}

// WithRawSQL keeps the executed SQL text on each statement. Leave it off for
// production exporters; raw SQL can carry sensitive shapes.
func WithRawSQL() ConfigOption {
	return func(c *Config) { c.CaptureRawSQL = true }
}

// WithArgCapture controls how much of the bind arguments are kept.
func WithArgCapture(mode ArgCaptureMode) ConfigOption {
	return func(c *Config) { c.CaptureArgs = mode }
}

// WithCaller controls when the calling code is captured.
func WithCaller(mode CallerMode) ConfigOption {
	return func(c *Config) { c.CaptureCaller = mode }
}

// WithSlowQueryThreshold sets the duration above which a query counts as slow.
func WithSlowQueryThreshold(d time.Duration) ConfigOption {
	return func(c *Config) { c.SlowQueryThreshold = d }
}

// WithNPlusOne sets the repeated-query and loop detection configuration.
func WithNPlusOne(n NPlusOneConfig) ConfigOption {
	return func(c *Config) { c.NPlusOne = n }
}

// WithWarnings sets the threshold-based warning configuration.
func WithWarnings(w WarningConfig) ConfigOption {
	return func(c *Config) { c.Warnings = w }
}

// WithVerboseCapture turns on the development-grade capture the safe defaults
// leave off: raw SQL and a caller for every statement. It captures more than is
// appropriate for production.
func WithVerboseCapture() ConfigOption {
	return func(c *Config) {
		c.CaptureRawSQL = true
		c.CaptureCaller = CallerAlways
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
