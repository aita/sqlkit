package querytrace

import (
	"reflect"
	"strings"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/x/sqlparse"
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

	var parsed sql.Query
	if (c.AnalyzeSQL || c.Fingerprint) && c.Parser != nil {
		if q, err := sqlparse.ParseOne(c.Parser, sqlText); err != nil {
			qi.AnalyzeErr = err
		} else {
			parsed = q
		}
	}

	if c.AnalyzeSQL {
		if shape, ok := Describe(parsed); ok {
			qi.Operation = shape.Kind
			qi.Tables = shape.Tables
			qi.HasWhere = shape.HasWhere
			qi.HasJoin = shape.HasJoin
			qi.HasLimit = shape.HasLimit
			qi.HasReturning = shape.HasReturning
			qi.analyzed = true
		} else {
			qi.Operation = detectOperation(sqlText)
		}
	}

	if c.Fingerprint {
		qi.Fingerprint = c.fingerprint(sqlText, parsed)
	}
	return qi
}

// detectOperation guesses a statement's kind from its leading keyword, the
// fallback when no AST is available. It returns StmtUnknown when the leading
// token is unrecognized.
func detectOperation(sqlText string) StmtKind {
	switch leadingKeyword(sqlText) {
	case "SELECT", "WITH", "VALUES", "TABLE":
		return StmtSelect
	case "INSERT":
		return StmtInsert
	case "UPDATE":
		return StmtUpdate
	case "DELETE":
		return StmtDelete
	case "CREATE", "ALTER", "DROP", "TRUNCATE", "COMMENT":
		return StmtDDL
	default:
		return StmtUnknown
	}
}

// leadingKeyword returns the upper-cased first word of sqlText, skipping leading
// whitespace and a single leading line/block comment.
func leadingKeyword(sqlText string) string {
	s := strings.TrimSpace(sqlText)
	for {
		switch {
		case strings.HasPrefix(s, "--"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = strings.TrimSpace(s[i+1:])
				continue
			}
			return ""
		case strings.HasPrefix(s, "/*"):
			if i := strings.Index(s, "*/"); i >= 0 {
				s = strings.TrimSpace(s[i+2:])
				continue
			}
			return ""
		}
		break
	}
	i := strings.IndexFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '(' || r == ';'
	})
	if i < 0 {
		i = len(s)
	}
	return strings.ToUpper(s[:i])
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
