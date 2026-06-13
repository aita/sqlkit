package codegen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aita/sqlkit/internal/names"
	"github.com/aita/sqlkit/meta"
)

// relationMethodNames are the methods/fields a relation name must not shadow on
// the generated columns/table structs.
var relationMethodNames = []string{
	"Info", "SQLSource", "Columns", "PrimaryKey", "Defaulted", "Queryer", "Q", "Rels",
}

// buildRelations derives the relation models for every generated table from the
// foreign keys among them, keyed by qualified table name. For each FK it adds a
// belongs-to relation on the child and the matching reverse (has-one when the
// FK is unique, else has-many) on the parent. Self-references emit both
// relations on the one table with the target aliased (so the self-join is
// unambiguous). Foreign keys to tables outside the generated set are skipped.
func buildRelations(tables []meta.Table) map[string][]relationModel {
	byName := make(map[string]meta.Table, len(tables))
	for _, t := range tables {
		byName[qualified(t.Schema.Name, t.Name)] = t
	}

	// Reserved identifier sets per table, seeded with column fields + methods,
	// so relation names never collide with columns, methods, or each other.
	reserved := make(map[string]map[string]bool, len(tables))
	for _, t := range tables {
		set := map[string]bool{}
		for _, name := range relationMethodNames {
			set[name] = true
		}
		for _, c := range t.Columns {
			set[fieldName(c.Name)] = true
		}
		reserved[qualified(t.Schema.Name, t.Name)] = set
	}

	out := map[string][]relationModel{}
	for _, child := range tables {
		childQual := qualified(child.Schema.Name, child.Name)
		for _, fk := range child.ForeignKeys {
			parentQual := qualified(fk.RefSchema.Name, fk.RefTable)
			parent, ok := byName[parentQual]
			if !ok {
				continue // FK targets a table we are not generating
			}
			if len(fk.Columns) == 0 || len(fk.Columns) != len(fk.RefColumns) {
				continue // malformed FK; skip rather than emit broken code
			}

			childVar := goName(child.Name)
			parentVar := goName(parent.Name)
			childCtor := "new" + childVar + "Columns"
			parentCtor := "new" + parentVar + "Columns"

			if parentQual == childQual {
				// Self-reference: target is an aliased view of the same table so
				// the self-join is unambiguous. Forward (belongs-to) and reverse
				// relations both live on this table.
				fwdName := uniqueName(reserved[childQual],
					fkDerivedName(fk.Columns), goName(singular(child.Name)))
				out[childQual] = append(out[childQual], relationModel{
					Field:         fwdName,
					RelType:       childVar + fwdName + "Rel",
					TargetColumns: childVar + "Columns",
					TargetCtor:    childCtor,
					Cardinality:   "sqlkit.ToOne",
					Alias:         selfAlias(fwdName, child.Name),
					JoinOn:        joinOn(fk.RefColumns, childCtor, fk.Columns),
					TargetKeys:    fk.RefColumns,
					BaseKeys:      fk.Columns,
				})

				var revBase, revCard string
				if uniqueFK(child, fk.Columns) {
					revBase, revCard = goName(singular(child.Name)), "sqlkit.ToOne"
				} else {
					revBase, revCard = goName(child.Name), "sqlkit.ToMany"
				}
				revName := uniqueName(reserved[childQual], revBase, fwdName+"Reverse")
				out[childQual] = append(out[childQual], relationModel{
					Field:         revName,
					RelType:       childVar + revName + "Rel",
					TargetColumns: childVar + "Columns",
					TargetCtor:    childCtor,
					Cardinality:   revCard,
					Alias:         selfAlias(revName, child.Name),
					JoinOn:        joinOn(fk.Columns, childCtor, fk.RefColumns),
					TargetKeys:    fk.Columns,
					BaseKeys:      fk.RefColumns,
				})
				continue
			}

			// Forward (belongs-to) on child, target = parent. The relation embeds
			// the parent columns, so JoinOn equates r.<parentRef> = child.<fk>.
			fwdName := uniqueName(reserved[childQual],
				goName(singular(parent.Name)), fkDerivedName(fk.Columns))
			out[childQual] = append(out[childQual], relationModel{
				Field:         fwdName,
				RelType:       childVar + fwdName + "Rel",
				TargetColumns: parentVar + "Columns",
				TargetCtor:    parentCtor,
				Cardinality:   "sqlkit.ToOne",
				JoinOn:        joinOn(fk.RefColumns, childCtor, fk.Columns),
				TargetKeys:    fk.RefColumns,
				BaseKeys:      fk.Columns,
			})

			// Reverse on parent, target = child. The relation embeds the child
			// columns, so JoinOn equates r.<fk> = parent.<ref>.
			var revBase, card string
			if uniqueFK(child, fk.Columns) {
				revBase, card = goName(singular(child.Name)), "sqlkit.ToOne"
			} else {
				revBase, card = goName(child.Name), "sqlkit.ToMany"
			}
			revName := uniqueName(reserved[parentQual], revBase, fkDerivedName(fk.Columns))
			out[parentQual] = append(out[parentQual], relationModel{
				Field:         revName,
				RelType:       parentVar + revName + "Rel",
				TargetColumns: childVar + "Columns",
				TargetCtor:    childCtor,
				Cardinality:   card,
				JoinOn:        joinOn(fk.Columns, parentCtor, fk.RefColumns),
				TargetKeys:    fk.Columns,
				BaseKeys:      fk.RefColumns,
			})
		}
	}
	return out
}

// selfAlias derives the table alias a self-referential relation's target is
// referenced by: the snake_case of the relation field name, suffixed when it
// would collide with the table's own name (which would make the self-join
// ambiguous).
func selfAlias(field, tableName string) string {
	alias := names.SnakeCase(field)
	if alias == tableName {
		alias += "_ref"
	}
	return alias
}

// joinOn renders the JoinOn predicate. targetCols are accessed on the embedded
// relation receiver (r.<Field>); baseCols are built from baseCtor()'s columns.
// Multiple column pairs are ANDed.
func joinOn(targetCols []string, baseCtor string, baseCols []string) string {
	conds := make([]string, len(targetCols))
	for i := range targetCols {
		conds[i] = fmt.Sprintf("sqlkit.Cond(sql.Eq(r.%s, %s().%s))",
			fieldName(targetCols[i]), baseCtor, fieldName(baseCols[i]))
	}
	if len(conds) == 1 {
		return conds[0]
	}
	return "sqlkit.And(" + strings.Join(conds, ", ") + ")"
}

// uniqueFK reports whether the child's FK column set is unique — equal to its
// primary key, or covered by a unique index — making the reverse relation
// has-one rather than has-many.
func uniqueFK(child meta.Table, cols []string) bool {
	var pk []string
	for _, c := range child.Columns {
		if c.PrimaryKey {
			pk = append(pk, c.Name)
		}
	}
	if sameSet(pk, cols) {
		return true
	}
	for _, idx := range child.Indexes {
		if idx.Unique && sameSet(idx.Columns, cols) {
			return true
		}
	}
	return false
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

// fkDerivedName derives a relation name from a single-column FK ending in "_id"
// (author_id -> Author); it returns "" when no such name applies, so callers
// fall back to numeric disambiguation.
func fkDerivedName(cols []string) string {
	if len(cols) != 1 {
		return ""
	}
	base := strings.TrimSuffix(cols[0], "_id")
	if base == "" || base == cols[0] {
		return ""
	}
	return goName(base)
}

// uniqueName returns the first non-empty candidate not already reserved, then
// falls back to base with a numeric suffix. The chosen name is reserved.
func uniqueName(reserved map[string]bool, base string, alts ...string) string {
	for _, name := range append([]string{base}, alts...) {
		if name != "" && !reserved[name] {
			reserved[name] = true
			return name
		}
	}
	for i := 2; ; i++ {
		name := fmt.Sprintf("%s%d", base, i)
		if !reserved[name] {
			reserved[name] = true
			return name
		}
	}
}
