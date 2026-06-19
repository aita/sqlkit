package querytrace

import (
	"reflect"

	"github.com/aita/sqlkit/analyze"
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
)

// Analyze produces the structural QueryInfo for a statement from its SQL text
// and bind arguments. It is best-effort and never returns an error: a parse
// failure is recorded in AnalyzeErr and the analysis falls back to a keyword
// guess, so observability never interferes with execution. It is part of the
// instrumentation contract used by wrapped drivers.
func (c Config) Analyze(sqlText string, args []any) *QueryInfo {
	qi := &QueryInfo{ParamCount: len(args)}
	if c.CaptureRawSQL {
		qi.RawSQL = sqlText
	}
	if c.CaptureArgs >= ArgsTypesOnly {
		qi.ParamTypes = argTypeNames(args)
	}

	a := analyze.New(analyze.WithDialect(c.dialect()))

	var parsed sql.Query
	var parseErr error
	if (c.AnalyzeSQL || c.Fingerprint) && c.Parser != nil {
		if q, err := sqlparse.ParseOne(c.Parser, sqlText); err != nil {
			parseErr = err
		} else {
			parsed = q
		}
	}

	if c.AnalyzeSQL {
		// The static analysis is produced and owned by the analyze framework; the
		// advanced facts stay empty on the keyword-fallback path, so the warnings
		// that read them do not fire there.
		qi.Analysis = a.Analyze(sqlText, parsed)
		qi.Err = parseErr
	}

	if c.Fingerprint {
		qi.Fingerprint = fingerprint(a, sqlText, parsed)
	}
	return qi
}

// fingerprint returns the normalized form of a statement used to group repeats.
// It prefers the parsed AST (recompiled through the analyzer's dialect, with every
// bind collapsed to "?") and falls back to normalizing the raw SQL text when no
// AST is available or it fails to recompile.
func fingerprint(a *analyze.Analyzer, sqlText string, parsed sql.Query) string {
	if fp, ok := a.Fingerprint(parsed); ok {
		return fp
	}
	return analyze.FingerprintRaw(sqlText)
}

// argTypeNames returns the Go type name of each argument, or "nil" for a nil.
func argTypeNames(args []any) []string {
	if len(args) == 0 {
		return nil
	}
	names := make([]string, len(args))
	for i, a := range args {
		if a == nil {
			names[i] = "nil"
			continue
		}
		names[i] = reflect.TypeOf(a).String()
	}
	return names
}
