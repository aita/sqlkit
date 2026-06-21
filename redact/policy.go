package redact

import (
	"path"
	"strings"

	"github.com/aita/sqlkit/meta"
)

// Policy is the hint that decides which columns are sensitive (PII) and so have
// their embedded string literals masked. It answers a single question — is this
// table's column sensitive? — so a Masker can ask it of every resolved column.
//
// Implementations come from the source of the knowledge: Columns from an explicit
// list (no schema needed), CommentMarker from a schema's column comments (which
// carries its own metadata). Combine merges several into one, so a Masker can be
// driven by any mix, and none of them forces metadata on a caller that does not
// have it.
type Policy interface {
	// IsSensitive reports whether the column of the table (schema may be "") is PII.
	IsSensitive(schema, table, column string) bool
}

type piiKey struct{ table, column string }

// columnSet is the common Policy implementation: a set of (table, column) keys,
// folded for case-insensitive matching. Both Columns and CommentMarker build one.
type columnSet map[piiKey]struct{}

func (s columnSet) IsSensitive(_, table, column string) bool {
	_, ok := s[piiKey{fold(table), fold(column)}]
	return ok
}

func (s columnSet) add(table, column string) {
	s[piiKey{fold(table), fold(column)}] = struct{}{}
}

// Columns is a Policy of named columns. Each spec is "table.column" or
// "schema.table.column" (the schema is accepted but ignored, since a column is
// matched by table and column name), and both the table and column halves accept
// glob wildcards (* ? [..]), matched case-insensitively:
//
//	Columns("users.email")    // exactly users.email
//	Columns("users.*")        // every column of users
//	Columns("*.email")        // an email column on any table
//	Columns("*.*_token")      // any column ending in _token, any table
//
// A spec without a table part is ignored. It needs no metadata.
func Columns(specs ...string) Policy {
	var ps patternPolicy
	for _, spec := range specs {
		if table, column, ok := splitTableColumn(spec); ok {
			ps = append(ps, piiPattern{table: table, column: column})
		}
	}
	return ps
}

// piiPattern is one Columns spec: a table glob and a column glob.
type piiPattern struct{ table, column string }

// patternPolicy is the Policy backing Columns: a column is sensitive when its
// table and name match any pattern's globs.
type patternPolicy []piiPattern

func (ps patternPolicy) IsSensitive(_, table, column string) bool {
	for _, p := range ps {
		if globMatch(p.table, table) && globMatch(p.column, column) {
			return true
		}
	}
	return false
}

// globMatch reports whether value matches the glob pattern, case-insensitively. A
// malformed pattern matches nothing. A pattern with no wildcards is an exact
// (folded) comparison, so a plain "table.column" spec behaves as before.
func globMatch(pattern, value string) bool {
	ok, err := path.Match(fold(pattern), fold(value))
	return err == nil && ok
}

// CommentPredicate reports whether a column is PII, given its table, column, and
// comment. CommentMarker calls it for every column of the schema, so the caller
// decides what marks a column sensitive — most often a substring of the comment,
// but the table and column names are available too.
type CommentPredicate func(table, column, comment string) bool

// CommentMarker is a Policy of every column of md for which sensitive reports
// true — the convention-driven source, so a schema declares its own PII through
// its column comments (e.g. sensitive returns whether the comment contains
// "pii"). The metadata is supplied here, to the hint that needs it; a nil
// predicate matches nothing.
func CommentMarker(md meta.Metadata, sensitive CommentPredicate) Policy {
	s := columnSet{}
	if sensitive != nil {
		for _, t := range md.Tables {
			for _, c := range t.Columns {
				if sensitive(t.Name, c.Name, c.Comment) {
					s.add(t.Name, c.Name)
				}
			}
		}
	}
	return s
}

// Combine merges policies into one that reports a column sensitive when any of
// them does. nil members are ignored; Combine() of nothing matches nothing.
func Combine(policies ...Policy) Policy { return combined(policies) }

type combined []Policy

func (c combined) IsSensitive(schema, table, column string) bool {
	for _, p := range c {
		if p != nil && p.IsSensitive(schema, table, column) {
			return true
		}
	}
	return false
}

// splitTableColumn splits a "table.column" or "schema.table.column" spec into its
// table and column parts (the last two dotted segments), reporting ok=false when
// it has no table part.
func splitTableColumn(spec string) (table, column string, ok bool) {
	parts := strings.Split(strings.TrimSpace(spec), ".")
	if len(parts) < 2 {
		return "", "", false
	}
	table = parts[len(parts)-2]
	column = parts[len(parts)-1]
	if table == "" || column == "" {
		return "", "", false
	}
	return table, column, true
}

// fold normalizes an identifier for case-insensitive matching.
func fold(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
