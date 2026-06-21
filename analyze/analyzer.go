package analyze

import "github.com/aita/sqlkit/sql"

// Analyzer is a configured entry point to the analysis functions, for a consumer
// that runs several over the same query and wants the configuration set once. The
// free functions remain the primitives; the Analyzer bundles the options they need
// — currently the SQL dialect used for fingerprinting — so a caller (querytrace,
// driving its per-statement observability) need not repeat them call to call.
//
// The zero value is usable: every method but Fingerprint is configuration-free, so
// a zero Analyzer behaves like the bare functions, and Fingerprint reports ok=false
// until a dialect is set. An Analyzer is read-only and safe for concurrent use.
type Analyzer struct {
	dialect sql.Dialect
}

// Option configures an Analyzer.
type Option func(*Analyzer)

// WithDialect sets the SQL dialect the Analyzer recompiles a parsed query through
// when fingerprinting. Without it, Fingerprint reports ok=false and a caller falls
// back to FingerprintRaw on the SQL text.
func WithDialect(d sql.Dialect) Option {
	return func(a *Analyzer) { a.dialect = d }
}

// New returns an Analyzer configured by opts.
func New(opts ...Option) *Analyzer {
	a := &Analyzer{}
	for _, o := range opts {
		o(a)
	}
	return a
}

// Analysis is the static analysis of one SQL statement: the facts derived from
// its text and AST alone, independent of execution. It is the analyze-side half of
// what a tracer records about a statement — the runtime half (timing, parameters,
// caller) belongs to the tracer — so the static facts are produced and owned here
// and embedded by the consumer.
//
// Parsed distinguishes the two ways it is produced: from a parsed AST (Parsed
// true, every field meaningful) or, when the SQL could not be parsed, from a
// keyword guess (Parsed false, only Kind populated). Checks that depend on clause
// shape consult Parsed to avoid firing on the guess. Fingerprint and Err are filled
// by the producer of the analysis (see Analyzer.Analyze): Fingerprint only when the
// caller wants it, Err with the parse failure that forced the fallback.
type Analysis struct {
	// Kind is the statement kind, from the AST (KindOf) or a keyword guess (DetectKind).
	Kind StmtKind
	// Tables are the base tables the statement references (Tables); nil unless Parsed.
	Tables []string
	// HasTopLevelWhere, HasTopLevelJoin, and SelectStar are the top-level clause
	// facts (HasTopLevelWhere, HasTopLevelJoin, SelectsStar); all false unless Parsed.
	HasTopLevelWhere bool
	HasTopLevelJoin  bool
	SelectStar       bool
	// FilterColumns are the bare column names in the top-level WHERE (FilterColumns),
	// and MaxInList the largest IN (...) value list there (MaxInList); both zero
	// unless Parsed. They back the tenant/soft-delete and large-IN checks.
	FilterColumns []string
	MaxInList     int
	// NullEquality reports whether the top-level WHERE compares against a NULL
	// literal with = or <> (NullEqualityPredicate); false unless Parsed. It backs
	// the eq_null check.
	NullEquality bool
	// Fingerprint is the normalized SQL used to group repeats; empty unless the
	// caller asked for it. See Analyzer.Fingerprint and FingerprintRaw.
	Fingerprint string
	// Parsed reports whether the facts came from a parsed AST rather than a keyword
	// guess.
	Parsed bool
	// Err records why analysis fell back to a keyword guess (a parse failure), for
	// diagnostics; nil when Parsed. It is never an execution error.
	Err error
}

// Analyze returns the static structural analysis of a statement, preferring the
// parsed AST q and falling back to a keyword guess on sqlText when q is nil or of
// an unrecognized kind (a parse failure). The structural fields (Tables, the clause
// facts, the WHERE predicates) are populated only on the parsed path; on the
// fallback only Kind is set, from DetectKind.
//
// It does not set Fingerprint or Err — the caller fills those: Fingerprint when it
// wants one (it carries a cost; see Analyzer.Fingerprint), Err with the parse
// failure. This is the single entry point a tracer uses to gather a statement's
// static facts in one call.
func (a *Analyzer) Analyze(sqlText string, q sql.Query) Analysis {
	kind, ok := KindOf(q)
	if !ok {
		return Analysis{Kind: DetectKind(sqlText)}
	}
	cols, maxIn := WherePredicates(q)
	return Analysis{
		Kind:             kind,
		Tables:           Tables(q),
		HasTopLevelWhere: HasTopLevelWhere(q),
		HasTopLevelJoin:  HasTopLevelJoin(q),
		SelectStar:       SelectsStar(q),
		FilterColumns:    cols,
		MaxInList:        maxIn,
		NullEquality:     NullEqualityPredicate(q),
		Parsed:           true,
	}
}

// Kind reports the statement kind from the AST; see KindOf.
func (a *Analyzer) Kind(q sql.Query) (StmtKind, bool) { return KindOf(q) }

// Tables returns the base tables the statement references; see Tables.
func (a *Analyzer) Tables(q sql.Query) []string { return Tables(q) }

// HasTopLevelWhere reports a top-level WHERE; see HasTopLevelWhere.
func (a *Analyzer) HasTopLevelWhere(q sql.Query) bool { return HasTopLevelWhere(q) }

// HasTopLevelJoin reports a top-level JOIN; see HasTopLevelJoin.
func (a *Analyzer) HasTopLevelJoin(q sql.Query) bool { return HasTopLevelJoin(q) }

// SelectsStar reports a top-level SELECT *; see SelectsStar.
func (a *Analyzer) SelectsStar(q sql.Query) bool { return SelectsStar(q) }

// WherePredicates returns the top-level WHERE filter columns and largest IN list
// in one walk; see WherePredicates.
func (a *Analyzer) WherePredicates(q sql.Query) (filterColumns []string, maxInList int) {
	return WherePredicates(q)
}

// Fingerprint returns the normalized form of a parsed query using the configured
// dialect; ok is false when no dialect is set or the query fails to compile, in
// which case a caller falls back to FingerprintRaw. See Fingerprint.
func (a *Analyzer) Fingerprint(q sql.Query) (string, bool) {
	if a.dialect == nil {
		return "", false
	}
	return Fingerprint(q, a.dialect)
}
