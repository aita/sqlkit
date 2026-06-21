package querytrace

import (
	"time"

	"github.com/aita/sqlkit/meta"
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
	// than the slow-query threshold (WithSlowQueryThreshold).
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

// Config configures a wrapped driver and the Traces it records. It is an opaque
// value built by NewConfig and the With* options; the zero value is a disabled,
// pass-through Config.
type Config struct {
	// enabled turns tracing on. A disabled Config makes the wrapped driver a thin
	// pass-through. The zero value is disabled; NewConfig enables it.
	enabled bool

	// dialect is the SQL dialect used to recompile a parsed query into its
	// fingerprint; it defaults to PostgreSQL when nil. See WithDialect.
	dialect sql.Dialect

	// parser analyzes SQL text into a sql.Query for structural analysis and
	// fingerprinting. It is best-effort: a parse failure or a nil parser falls
	// back to lightweight string analysis and never aborts execution. See
	// WithParser.
	parser sqlparse.Parser

	// captureRawSQL keeps the executed SQL text on each QueryInfo. See WithRawSQL.
	captureRawSQL bool
	// captureArgs controls how much of the bind arguments are kept. See
	// WithArgCapture.
	captureArgs ArgCaptureMode
	// captureCaller controls when the calling code is captured. See WithCaller.
	captureCaller CallerMode

	// fingerprint enables SQL fingerprinting, the basis of repeated-query and
	// N+1 detection. On by default; see WithoutFingerprint.
	fingerprint bool
	// analyzeSQL enables structural analysis (kind, tables, WHERE/JOIN/...), the
	// basis of the predicate and shape warnings. On by default; see
	// WithoutAnalyze.
	analyzeSQL bool

	// slowQueryThreshold is the duration above which a query counts as slow, for
	// CallerOnSlowQuery. See WithSlowQueryThreshold.
	slowQueryThreshold time.Duration

	// nplusOne configures repeated-query and loop detection. See
	// WithNPlusOneThreshold, WithNPlusOneIgnore, and WithoutNPlusOne.
	nplusOne nPlusOneConfig
	// warnings holds the threshold- and policy-based warning settings, each set
	// through its own ConfigOption (WithMaxQueries, WithTenantCheck, ...).
	warnings warningConfig
}

// nPlusOneConfig holds the repeated-query and loop detection settings, set
// through WithNPlusOneThreshold, WithNPlusOneIgnore, and WithoutNPlusOne.
type nPlusOneConfig struct {
	// enabled turns repeated-query and loop detection on. On by default.
	enabled bool
	// minCount is how many times one fingerprint must repeat before it is
	// reported. It defaults to 10 when zero.
	minCount int
	// ignoreOperations skips fingerprints of these kinds; repeated INSERTs into a
	// log table, for example, are usually expected.
	ignoreOperations []StmtKind
}

// warningConfig holds the threshold- and policy-based warning settings. Each
// field is set through its own ConfigOption rather than constructed directly, so
// the type is unexported; the zero value enables no warning.
type warningConfig struct {
	// maxQueries is the per-Trace query count above which too_many_queries fires.
	// Zero disables the check. See WithMaxQueries.
	maxQueries int
	// maxParams is the bind-parameter count at or above which a statement raises
	// too_many_parameters. Zero disables the check. See WithMaxParams.
	maxParams int
	// maxInListLen is the IN (...) value-list length at or above which a statement
	// raises large_in_list. Zero disables the check. It needs the parsed AST, so
	// it is silent on the keyword-fallback path. See WithMaxInList.
	maxInListLen int
	// selectStar enables the select_star warning for SELECT * queries. It needs
	// the parsed AST. See WithSelectStarWarning.
	selectStar bool
	// tenant configures missing_tenant_predicate: a read or write touching a base
	// table whose top-level WHERE references none of the marker columns. See
	// WithTenantCheck.
	tenant predicateConfig
	// softDelete configures missing_soft_delete_predicate: a SELECT touching a
	// base table whose top-level WHERE references none of the marker columns. See
	// WithSoftDeleteCheck.
	softDelete predicateConfig
}

// predicateConfig configures one of the predicate checks (tenant or soft-delete):
// the marker column patterns it requires in a statement's top-level WHERE and,
// when set, the schema that scopes it to the tables that actually have one. No
// patterns leaves the check off. It is built from PredicateOptions.
type predicateConfig struct {
	patterns []columnPattern
	schema   *schemaColumns
}

// ConfigOption configures a Config built by NewConfig. Options compose, so a
// caller layers only the choices that differ from the defaults.
type ConfigOption func(*Config)

// NewConfig returns a Config built from opts. With no options it is enabled with
// production-minded defaults: no raw SQL, argument *types* only (never values),
// callers captured on error, fingerprinting, structural analysis, and N+1
// detection on, and the PostgreSQL dialect. Layer options to capture more for
// development (WithVerboseCapture) or to adjust any single setting.
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
		enabled:            true,
		dialect:            sql.PostgreSQLDialect{},
		captureArgs:        ArgsTypesOnly,
		captureCaller:      CallerOnError,
		fingerprint:        true,
		analyzeSQL:         true,
		slowQueryThreshold: 100 * time.Millisecond,
		nplusOne:           nPlusOneConfig{enabled: true, minCount: 10},
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
			c.dialect = d
		}
	}
}

// WithParser sets the parser that drives structural analysis and fingerprinting.
// Pass a frontend's parser, for example postgres.New() from
// github.com/aita/sqlkit/sqlparse/postgres. Without a parser, analysis falls
// back to lightweight string heuristics.
func WithParser(p sqlparse.Parser) ConfigOption {
	return func(c *Config) { c.parser = p }
}

// WithRawSQL keeps the executed SQL text on each statement. Leave it off for
// production exporters; raw SQL can carry sensitive shapes.
func WithRawSQL() ConfigOption {
	return func(c *Config) { c.captureRawSQL = true }
}

// WithArgCapture controls how much of the bind arguments are kept.
func WithArgCapture(mode ArgCaptureMode) ConfigOption {
	return func(c *Config) { c.captureArgs = mode }
}

// WithCaller controls when the calling code is captured.
func WithCaller(mode CallerMode) ConfigOption {
	return func(c *Config) { c.captureCaller = mode }
}

// WithSlowQueryThreshold sets the duration above which a query counts as slow.
func WithSlowQueryThreshold(d time.Duration) ConfigOption {
	return func(c *Config) { c.slowQueryThreshold = d }
}

// WithoutFingerprint turns off SQL fingerprinting. Fingerprinting is on by
// default and underpins repeated-query and N+1 detection, so disabling it also
// disables those.
func WithoutFingerprint() ConfigOption {
	return func(c *Config) { c.fingerprint = false }
}

// WithoutAnalyze turns off structural analysis. Analysis is on by default and
// backs the predicate and shape warnings (large_in_list, select_star, the
// tenant/soft-delete predicates), so disabling it also disables those.
func WithoutAnalyze() ConfigOption {
	return func(c *Config) { c.analyzeSQL = false }
}

// WithNPlusOneThreshold sets how many times one query shape must repeat within a
// Trace before it is reported as a query-in-loop or possible N+1. Detection is on
// by default at a threshold of 10; a value <= 0 restores that default.
func WithNPlusOneThreshold(minCount int) ConfigOption {
	return func(c *Config) { c.nplusOne.minCount = minCount }
}

// WithNPlusOneIgnore excludes the given statement kinds from repeated-query
// detection; repeated INSERTs into a log table, for example, are usually
// expected.
func WithNPlusOneIgnore(ops ...StmtKind) ConfigOption {
	return func(c *Config) { c.nplusOne.ignoreOperations = ops }
}

// WithoutNPlusOne turns off repeated-query and loop detection, which is on by
// default.
func WithoutNPlusOne() ConfigOption {
	return func(c *Config) { c.nplusOne.enabled = false }
}

// WithMaxQueries sets the per-Trace query-count budget: a Trace with more read
// and write statements than n raises too_many_queries. Zero (the default)
// disables the check.
func WithMaxQueries(n int) ConfigOption {
	return func(c *Config) { c.warnings.maxQueries = n }
}

// WithMaxParams sets the bind-parameter budget: a statement carrying n or more
// bind parameters raises too_many_parameters. Zero (the default) disables the
// check.
func WithMaxParams(n int) ConfigOption {
	return func(c *Config) { c.warnings.maxParams = n }
}

// WithMaxInList sets the IN (...) value-list budget: a statement with an IN list
// of n or more items raises large_in_list. It reads the parsed AST, so it stays
// silent on the keyword-fallback path. Zero (the default) disables the check.
func WithMaxInList(n int) ConfigOption {
	return func(c *Config) { c.warnings.maxInListLen = n }
}

// WithSelectStarWarning enables select_star, which fires on a SELECT * query. It
// reads the parsed AST, so it stays silent on the keyword-fallback path.
func WithSelectStarWarning() ConfigOption {
	return func(c *Config) { c.warnings.selectStar = true }
}

// PredicateOption configures a tenant or soft-delete predicate check: the marker
// columns it requires (Columns) and, optionally, the schema scope that limits it
// to the tables that actually have one (ScopedToSchema). It is the argument type
// of WithTenantCheck and WithSoftDeleteCheck.
type PredicateOption func(*predicateConfig)

// Columns sets the marker column specs a predicate check looks for in a
// statement's top-level WHERE, such as "tenant_id"/"org_id" for a tenant check or
// "deleted_at" for a soft-delete check. Each spec is a bare column or a
// table-qualified "table.column" (a leading schema is ignored), and both halves
// accept glob wildcards (* ? [..]), matched case-insensitively:
//
//	Columns("deleted_at")              // any table, column deleted_at
//	Columns("deleted_at", "deactivated_at") // either marker, any table
//	Columns("*_at")                    // any table, any column ending in _at
//	Columns("orders.deleted_at")       // only the orders table
//	Columns("*.deleted_at")            // explicit any-table form
//
// A bare column governs every table; a table-qualified spec restricts the
// requirement to the matching tables, so other tables are left unchecked. Columns
// is required: a check with no columns does nothing.
func Columns(specs ...string) PredicateOption {
	return func(p *predicateConfig) {
		for _, s := range specs {
			if pat, ok := parsePattern(s); ok {
				p.patterns = append(p.patterns, pat)
			}
		}
	}
}

// ScopedToSchema scopes a predicate check to the base tables that, per the
// resolved schema md, actually have a marker column — eliminating the false
// positive the check otherwise raises on a table that has no such column (a lookup
// or reference table). Without it the check applies to every analyzed base table.
// Pass the meta.Metadata a decl catalog builds or the introspect produces. To
// scope by hand instead, name the tables in the Columns specs ("orders.deleted_at").
func ScopedToSchema(md meta.Metadata) PredicateOption {
	return func(p *predicateConfig) { p.schema = schemaFromMeta(md) }
}

// WithTenantCheck enables missing_tenant_predicate: a read or write touching a
// base table whose top-level WHERE references none of the configured marker
// columns. Configure it with Columns (required) and, optionally, ScopedToSchema.
// The check reads the parsed AST, so it stays silent on the keyword-fallback path.
//
//	querytrace.WithTenantCheck(querytrace.Columns("tenant_id"), querytrace.ScopedToSchema(md))
func WithTenantCheck(opts ...PredicateOption) ConfigOption {
	return func(c *Config) { c.warnings.tenant = buildPredicate(opts) }
}

// WithSoftDeleteCheck enables missing_soft_delete_predicate: a SELECT touching a
// base table whose top-level WHERE references none of the configured marker
// columns. Configure it with Columns (required) and, optionally, ScopedToSchema.
// The check reads the parsed AST, so it stays silent on the keyword-fallback path.
//
//	querytrace.WithSoftDeleteCheck(querytrace.Columns("deleted_at"), querytrace.ScopedToSchema(md))
func WithSoftDeleteCheck(opts ...PredicateOption) ConfigOption {
	return func(c *Config) { c.warnings.softDelete = buildPredicate(opts) }
}

// buildPredicate applies the predicate options onto a fresh predicateConfig.
func buildPredicate(opts []PredicateOption) predicateConfig {
	var p predicateConfig
	for _, opt := range opts {
		opt(&p)
	}
	return p
}

// WithVerboseCapture turns on the development-grade capture the safe defaults
// leave off: raw SQL and a caller for every statement. It captures more than is
// appropriate for production.
func WithVerboseCapture() ConfigOption {
	return func(c *Config) {
		c.captureRawSQL = true
		c.captureCaller = CallerAlways
	}
}

// Enabled reports whether the Config records traces. A disabled Config (the zero
// value) makes the wrapped driver a thin pass-through.
func (c Config) Enabled() bool { return c.enabled }

// WantCaller reports whether a statement that finished with err after dur should
// have its calling code captured, under the configured CallerMode and slow-query
// threshold. A wrapped driver consults it before walking the stack.
func (c Config) WantCaller(err error, dur time.Duration) bool {
	return WantCaller(c.captureCaller, err, dur, c.slowQueryThreshold)
}

// Dialect returns the configured SQL dialect, defaulting to PostgreSQL when none
// was set. It never returns nil.
func (c Config) Dialect() sql.Dialect {
	if c.dialect == nil {
		return sql.PostgreSQLDialect{}
	}
	return c.dialect
}

// nPlusOneMinCount returns the effective repeat threshold.
func (c Config) nPlusOneMinCount() int {
	if c.nplusOne.minCount <= 0 {
		return 10
	}
	return c.nplusOne.minCount
}
