package redaction

import (
	"path"
	"strings"

	"github.com/aita/sqlkit/meta"
)

// Policy is the hint that decides which columns are sensitive (PII) and so have
// their embedded string literals masked. It answers a single question — is this
// table's column sensitive? — so a Redactor can ask it of every resolved column.
//
// Implementations come from the source of the knowledge: Columns from an explicit
// list (no schema needed), CommentMarker from a schema's column comments (which
// carries its own metadata). Combine merges several into one, so a Redactor can be
// driven by any mix, and none of them forces metadata on a caller that does not
// have it.
type Policy interface {
	// IsSensitive reports whether the column of the table (schema may be "") is PII.
	IsSensitive(schema, table, column string) bool
}

type columnKey struct{ table, column string }

// columnSet is the common Policy implementation: a set of (table, column) keys,
// folded for case-insensitive matching. Both Columns and CommentMarker build one.
type columnSet map[columnKey]struct{}

func (s columnSet) IsSensitive(_, table, column string) bool {
	_, ok := s[columnKey{fold(table), fold(column)}]
	return ok
}

func (s columnSet) add(table, column string) {
	s[columnKey{fold(table), fold(column)}] = struct{}{}
}

// Columns is a Policy of named columns. Each spec is "table.column" or
// "schema.table.column", and every part accepts glob wildcards (* ? [..]),
// matched case-insensitively:
//
//	Columns("users.email")         // an email column on a users table in any schema
//	Columns("public.users.email")  // exactly public.users.email — other schemas are left intact
//	Columns("users.*")             // every column of users
//	Columns("*.email")             // an email column on any table
//	Columns("public.*.email")      // an email column on any table in the public schema
//
// A spec without a schema part stays schema-insensitive — it matches the named
// table in any schema, so existing two-part specs behave as before. A spec that
// names a schema constrains matching to it, so a fully qualified spec with no
// wildcards is an exact match on all three parts: a precise, single-table target.
// A spec without a table part is ignored. It needs no metadata.
func Columns(specs ...string) Policy {
	var ps patternPolicy
	for _, spec := range specs {
		if schema, table, column, ok := splitSpec(spec); ok {
			ps = append(ps, columnPattern{schema: schema, table: table, column: column})
		}
	}
	return ps
}

// columnPattern is one Columns spec: an optional schema glob, a table glob, and a
// column glob. An empty schema is unconstrained — it matches any schema — so a
// two-part spec stays schema-insensitive.
type columnPattern struct{ schema, table, column string }

// patternPolicy is the Policy backing Columns: a column is sensitive when its
// schema (when the pattern names one), table, and name match a pattern's globs.
type patternPolicy []columnPattern

func (ps patternPolicy) IsSensitive(schema, table, column string) bool {
	for _, p := range ps {
		if p.schema != "" && !globMatch(p.schema, schema) {
			continue
		}
		if globMatch(p.table, table) && globMatch(p.column, column) {
			return true
		}
	}
	return false
}

// globMatch reports whether value matches the glob pattern, case-insensitively. A
// pattern with no wildcards is an exact (folded) comparison, so a plain
// "table.column" spec behaves as before.
//
// It falls back to an exact folded comparison whenever path.Match does not match
// (or rejects the pattern as malformed), so a spec that names an identifier which
// itself contains a glob metacharacter ([ ] \ * ?) — legal as a quoted identifier
// — still matches the column it names. Without this, path.Match reads the
// metacharacter as a wildcard and the spec fails to match its own column, leaving
// that column's literals unmasked (a fail-open leak). The fallback only ever
// broadens matching, so it cannot open a leak.
func globMatch(pattern, value string) bool {
	fp, fv := fold(pattern), fold(value)
	if ok, err := path.Match(fp, fv); err == nil && ok {
		return true
	}
	return fp == fv
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

// splitSpec splits a "table.column" or "schema.table.column" spec into its
// (optional) schema, table, and column parts — the last three dotted segments,
// with the schema empty when only two are given. It reports ok=false when there
// is no table part. A spec that names a schema constrains matching to it; a spec
// without one stays schema-insensitive.
func splitSpec(spec string) (schema, table, column string, ok bool) {
	parts := strings.Split(strings.TrimSpace(spec), ".")
	if len(parts) < 2 {
		return "", "", "", false
	}
	column = parts[len(parts)-1]
	table = parts[len(parts)-2]
	if len(parts) >= 3 {
		schema = parts[len(parts)-3]
	}
	if table == "" || column == "" {
		return "", "", "", false
	}
	return schema, table, column, true
}

// fold normalizes an identifier for case-insensitive matching.
func fold(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
