package sqlkit

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/aita/sqlkit/internal/reflectmap"
	"github.com/aita/sqlkit/sql"
)

// FilterTerm is one deferred predicate in a filter bundle. The constructors
// below (Eq, Le, …) produce column-bound terms: SelectQuery.Filter resolves the
// column from the struct field's name and applies the term to it. A Predicate is
// also accepted as a filter field value — it is already complete and ignores the
// field's column.
type FilterTerm interface{ filterTerm() }

// columnTerm builds its predicate once Filter supplies the matching column.
type columnTerm struct {
	build func(col AnyColumn) Predicate
}

func (columnTerm) filterTerm() {}

// Eq builds a "column = value" filter term.
func Eq(value any) FilterTerm {
	return columnTerm{func(c AnyColumn) Predicate { return Cond(sql.Eq(c, sql.Val(value))) }}
}

// Ne builds a "column <> value" filter term.
func Ne(value any) FilterTerm {
	return columnTerm{func(c AnyColumn) Predicate { return Cond(sql.Ne(c, sql.Val(value))) }}
}

// Lt builds a "column < value" filter term.
func Lt(value any) FilterTerm {
	return columnTerm{func(c AnyColumn) Predicate { return Cond(sql.Lt(c, sql.Val(value))) }}
}

// Le builds a "column <= value" filter term.
func Le(value any) FilterTerm {
	return columnTerm{func(c AnyColumn) Predicate { return Cond(sql.Le(c, sql.Val(value))) }}
}

// Gt builds a "column > value" filter term.
func Gt(value any) FilterTerm {
	return columnTerm{func(c AnyColumn) Predicate { return Cond(sql.Gt(c, sql.Val(value))) }}
}

// Ge builds a "column >= value" filter term.
func Ge(value any) FilterTerm {
	return columnTerm{func(c AnyColumn) Predicate { return Cond(sql.Ge(c, sql.Val(value))) }}
}

// In builds a "column IN (values…)" filter term.
func In(values ...any) FilterTerm {
	items := make([]any, len(values))
	for i, v := range values {
		items[i] = sql.Val(v)
	}
	return columnTerm{func(c AnyColumn) Predicate { return Cond(sql.In(c, items...)) }}
}

// FilterLike builds a "column LIKE pattern" filter term.
func FilterLike(pattern string) FilterTerm {
	return columnTerm{func(c AnyColumn) Predicate { return Cond(sql.Like(c, sql.Val(pattern))) }}
}

// FilterIsNull builds a "column IS NULL" filter term.
func FilterIsNull() FilterTerm {
	return columnTerm{func(c AnyColumn) Predicate { return Cond(sql.IsNull(c)) }}
}

// buildFilter turns a filter argument into predicates against base's columns.
// It accepts a bare Predicate, or a struct whose exported, non-zero fields each
// either map to a column by `sqlkit` tag (else the snake_case field name) and
// hold a FilterTerm or a Predicate, or name one of base's relations and hold a
// nested filter on the related table. A relation field narrows base with a
// correlated EXISTS over the related table (see existsForRelation), so it works
// the same in a SELECT, UPDATE, or DELETE without a join.
func buildFilter(base Table, filter any) ([]Predicate, error) {
	if filter == nil {
		return nil, nil
	}
	if p, ok := filter.(Predicate); ok {
		return []Predicate{p}, nil
	}

	rv := reflect.ValueOf(filter)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, nil
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, fmt.Errorf("sqlkit: Filter expects a struct or Predicate, got %T", filter)
	}

	cols := columnsByName(base)
	rt := rv.Type()
	var out []Predicate
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if field.PkgPath != "" { // unexported
			continue
		}
		fv := rv.Field(i)
		if fv.IsZero() { // unset filter field
			continue
		}
		iface := fv.Interface()

		// A complete predicate ignores the field's column, so it need not map to
		// one — this lets reusable filters carry computed predicates (e.g. an OR
		// across several columns) under a non-column field name.
		if p, ok := iface.(Predicate); ok {
			out = append(out, p)
			continue
		}

		// A FilterTerm applies to the column the field maps to.
		if term, ok := iface.(FilterTerm); ok {
			col, err := filterColumn(base, cols, field)
			if err != nil {
				return nil, err
			}
			ct, ok := term.(columnTerm)
			if !ok {
				return nil, fmt.Errorf("sqlkit: Filter field %q has an unsupported FilterTerm", field.Name)
			}
			out = append(out, ct.build(col))
			continue
		}

		// A field naming a relation holds a nested filter on the related table,
		// applied as a correlated EXISTS.
		if rel, ok := relationByName(base, field.Name); ok {
			exists, err := existsForRelation(rel, iface)
			if err != nil {
				return nil, err
			}
			out = append(out, exists)
			continue
		}

		// Otherwise a plain value maps to a column as an equality.
		col, err := filterColumn(base, cols, field)
		if err != nil {
			return nil, err
		}
		out = append(out, Cond(sql.Eq(col, sql.Val(iface))))
	}
	return out, nil
}

// filterColumn resolves the column a filter field targets, erroring when the
// field maps to none of base's columns.
func filterColumn(base Table, cols map[string]AnyColumn, field reflect.StructField) (AnyColumn, error) {
	col, ok := cols[columnKey(field)]
	if !ok {
		return nil, fmt.Errorf("sqlkit: Filter field %q maps to no column of %s",
			field.Name, base.Info().QualifiedName())
	}
	return col, nil
}

// relationByName finds base's relation whose generated field name is name, when
// base exposes its relations (a binding emitted with relations).
func relationByName(base Table, name string) (Relation, bool) {
	src, ok := base.(RelationSource)
	if !ok {
		return nil, false
	}
	for _, rel := range src.Relations() {
		if rel.Name() == name {
			return rel, true
		}
	}
	return nil, false
}

// existsForRelation narrows base by sub, a nested filter on rel's target, with a
// correlated EXISTS: EXISTS (SELECT 1 FROM target WHERE <join condition> AND
// <sub predicates>). An empty sub leaves just the join, so the field then means
// "base has any related row" — and a to-many relation never duplicates rows.
func existsForRelation(rel Relation, sub any) (Predicate, error) {
	predicates, err := buildFilter(rel.Target(), sub)
	if err != nil {
		return Predicate{}, err
	}
	where := make([]Predicate, 0, len(predicates)+1)
	where = append(where, rel.JoinOn())
	where = append(where, predicates...)
	return Exists(Select(sql.Raw("1")).From(rel.Target()).Where(where...)), nil
}

// columnsByName maps a table's bare column names to their bindings.
func columnsByName(base Table) map[string]AnyColumn {
	columns := base.Columns()
	out := make(map[string]AnyColumn, len(columns))
	for _, c := range columns {
		out[c.ColumnName()] = c
	}
	return out
}

// columnKey resolves the column a filter field targets: the `sqlkit` tag when
// present, else the snake_case form of the field name (mirroring Scan).
func columnKey(field reflect.StructField) string {
	if tag, ok := field.Tag.Lookup("sqlkit"); ok {
		if name := strings.Split(tag, ",")[0]; name != "" {
			return name
		}
	}
	return reflectmap.SnakeCase(field.Name)
}
