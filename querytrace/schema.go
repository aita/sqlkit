package querytrace

import (
	"github.com/aita/sqlkit/analysis"
	"github.com/aita/sqlkit/meta"
)

// schemaColumns records which columns each base table has, derived from a
// resolved schema (meta.Metadata) by ScopedToSchema. It scopes a predicate check
// to the tables that actually carry a marker column, so the check never
// false-positives on a reference table that has none. Table and column names are
// folded to bare, case-insensitive form, so a qualified reference ("public.users")
// lines up with the schema's declaration.
type schemaColumns struct {
	// tables maps a bare, lower-cased table name to the set of bare, lower-cased
	// column names it declares.
	tables map[string]map[string]struct{}
}

// schemaFromMeta builds the column index from a resolved schema: every table and
// the columns it declares.
func schemaFromMeta(md meta.Metadata) *schemaColumns {
	s := &schemaColumns{tables: make(map[string]map[string]struct{}, len(md.Tables))}
	for _, t := range md.Tables {
		set := make(map[string]struct{}, len(t.Columns))
		for _, c := range t.Columns {
			set[bareName(c.Name)] = struct{}{}
		}
		// Key by the bare table name; a schema-qualified statement still resolves
		// to it through bareName.
		s.tables[bareName(t.Name)] = set
	}
	return s
}

// hasColumnMatching reports whether the named table has, per the schema, at least
// one column matching one of the marker globs — that is, whether the table is
// subject to a check requiring those columns. A table the schema does not cover
// has no columns, so it reports false and the check skips it.
func (s *schemaColumns) hasColumnMatching(table string, globs []string) bool {
	set, ok := s.tables[bareName(table)]
	if !ok {
		return false
	}
	for col := range set {
		for _, g := range globs {
			if globMatch(g, col) {
				return true
			}
		}
	}
	return false
}

// bareName normalizes a possibly qualified table or column name to its bare,
// lower-cased form ("public.users" -> "users"), the same folding
// analysis.BareColumn applies, so a schema's keys line up with the analyzer's table
// and column names.
func bareName(name string) string { return analysis.BareColumn(name) }
