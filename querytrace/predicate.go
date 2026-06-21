package querytrace

import (
	"path"
	"strings"
)

// columnPattern is one marker spec for a predicate check: a table glob and a
// column glob, both matched case-insensitively. A bare spec ("deleted_at") has a
// table of "*" so it governs every table; a qualified spec ("orders.deleted_at",
// "*.deleted_at") restricts the column requirement to the tables whose name
// matches. Both halves are glob patterns (path.Match syntax: * ? [..]), so
// "deactivated_*" or "*_at" match a family of columns.
type columnPattern struct {
	table  string // glob; "*" means any table
	column string // glob
}

// parsePattern splits a spec into its table and column globs. The column is the
// last dotted segment and the table the one before it (a leading schema segment
// is ignored, as in redact), defaulting the table to "*" for a bare column. It
// reports ok=false for an empty spec or an empty table/column half.
func parsePattern(spec string) (columnPattern, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return columnPattern{}, false
	}
	parts := strings.Split(spec, ".")
	column := strings.TrimSpace(parts[len(parts)-1])
	table := "*"
	if len(parts) >= 2 {
		table = strings.TrimSpace(parts[len(parts)-2])
	}
	if column == "" || table == "" {
		return columnPattern{}, false
	}
	return columnPattern{table: table, column: column}, true
}

// columnGlobsFor returns the column globs whose table glob matches the given base
// table — the marker columns required of that table. It is empty when no pattern
// governs the table, in which case the check does not apply to it.
func (pc predicateConfig) columnGlobsFor(table string) []string {
	bt := bareName(table)
	var globs []string
	for _, p := range pc.patterns {
		if globMatch(p.table, bt) {
			globs = append(globs, p.column)
		}
	}
	return globs
}

// globMatch reports whether value matches the glob pattern, case-insensitively.
// A malformed pattern matches nothing.
func globMatch(pattern, value string) bool {
	ok, err := path.Match(strings.ToLower(pattern), strings.ToLower(value))
	return err == nil && ok
}

// anyGlobMatch reports whether any value matches any glob.
func anyGlobMatch(values, globs []string) bool {
	for _, v := range values {
		for _, g := range globs {
			if globMatch(g, v) {
				return true
			}
		}
	}
	return false
}
