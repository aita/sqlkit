package sqlkit

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/aita/sqlkit/internal/reflectmap"
	"github.com/aita/sqlkit/sql"
)

// Preloading is declared on the SelectQuery chain with Preload and runs at Scan:
// after the base rows materialize into the destination, each registered relation
// is eager-loaded in one batched follow-up query (SELECT target WHERE
// target.<key> IN (parent keys)) and its rows are assigned onto the destination
// by reflection — avoiding the N+1 a per-parent fetch would cause.
//
// The destination field on each parent is found by a `preload:"<RelationName>"`
// tag, else by a field named after the relation (rel.Name()). A to-many relation
// fills a slice field; a to-one relation fills a struct or pointer field. The
// element type of that field determines what the children scan into, so the
// usual sqlkit field mapping applies to them.

// applyPreloads runs every registered preload against the just-scanned dest.
func applyPreloads(ctx context.Context, ex Executor, dest any, rels []Relation) error {
	for _, rel := range rels {
		if err := preloadRelation(ctx, ex, dest, rel); err != nil {
			return err
		}
	}
	return nil
}

// preloadRelation eager-loads rel onto dest, which is the Scan destination: a
// pointer to a slice of parents (every row) or to a single parent (First/One).
func preloadRelation(ctx context.Context, ex Executor, dest any, rel Relation) error {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("sqlkit: Preload requires a pointer destination, got %T", dest)
	}

	// Gather the parent elements as addressable values: slice elements, or the
	// single struct the destination points at.
	root := rv.Elem()
	var parents []reflect.Value
	if root.Kind() == reflect.Slice {
		for i := 0; i < root.Len(); i++ {
			parents = append(parents, root.Index(i))
		}
	} else {
		parents = []reflect.Value{root}
	}
	if len(parents) == 0 {
		return nil
	}

	targetKeys, baseKeys := rel.Keys()
	if len(targetKeys) != 1 || len(baseKeys) != 1 {
		return fmt.Errorf("sqlkit: Preload of %q: composite-key relations are not supported yet", rel.Name())
	}

	parentBase, parentPtr := derefType(parents[0].Type())
	if parentBase.Kind() != reflect.Struct {
		return fmt.Errorf("sqlkit: Preload parent type %s is not a struct", parentBase)
	}
	basePath, ok := reflectmap.FieldsByColumn(parentBase)[strings.ToLower(baseKeys[0])]
	if !ok {
		return fmt.Errorf("sqlkit: Preload of %q: parent %s has no field for key column %q",
			rel.Name(), parentBase, baseKeys[0])
	}
	destIndex, err := relationField(parentBase, rel)
	if err != nil {
		return err
	}

	// The relation field's element type is what children scan into.
	destType := parentBase.FieldByIndex(destIndex).Type
	toMany := rel.Cardinality() == ToMany
	childElem := destType
	if toMany {
		if destType.Kind() != reflect.Slice {
			return fmt.Errorf("sqlkit: Preload of %q: to-many destination field must be a slice, got %s", rel.Name(), destType)
		}
		childElem = destType.Elem()
	}
	childBase, _ := derefType(childElem)
	childPath, ok := reflectmap.FieldsByColumn(childBase)[strings.ToLower(targetKeys[0])]
	if !ok {
		return fmt.Errorf("sqlkit: Preload of %q: child %s has no field for key column %q",
			rel.Name(), childBase, targetKeys[0])
	}

	// Collect distinct parent keys and the parents each maps to.
	groups := map[any][]int{}
	for i, p := range parents {
		ps := elemStruct(p, parentPtr)
		if !ps.IsValid() {
			continue
		}
		key, ok := scalarValue(ps.FieldByIndex(basePath))
		if !ok {
			continue
		}
		groups[key] = append(groups[key], i)
	}
	if len(groups) == 0 {
		return nil
	}

	target := rel.Target()
	fkCol, ok := columnsByName(target)[targetKeys[0]]
	if !ok {
		return fmt.Errorf("sqlkit: Preload of %q: target %s has no column %q",
			rel.Name(), target.Info().QualifiedName(), targetKeys[0])
	}
	vals := make([]any, 0, len(groups))
	for key := range groups {
		vals = append(vals, sql.Val(key))
	}

	// Scan children into a fresh []childElem, then group them by target key.
	childrenPtr := reflect.New(reflect.SliceOf(childElem))
	rows := ex.Select().From(target).Where(Cond(sql.In(fkCol, vals...))).All(ctx)
	if err := rows.Scan(childrenPtr.Interface()); err != nil {
		return err
	}
	children := childrenPtr.Elem()
	childByKey := map[any][]reflect.Value{}
	for j := 0; j < children.Len(); j++ {
		ce := children.Index(j)
		cs := elemStruct(ce, childElem.Kind() == reflect.Pointer)
		if !cs.IsValid() {
			continue
		}
		key, ok := scalarValue(cs.FieldByIndex(childPath))
		if !ok {
			continue
		}
		childByKey[key] = append(childByKey[key], ce)
	}

	// Assign matched children into each parent's relation field.
	for key, indices := range groups {
		matches := childByKey[key]
		for _, i := range indices {
			field := elemStruct(parents[i], parentPtr).FieldByIndex(destIndex)
			if toMany {
				slice := reflect.MakeSlice(destType, 0, len(matches))
				for _, m := range matches {
					slice = reflect.Append(slice, m)
				}
				field.Set(slice)
			} else if len(matches) > 0 {
				field.Set(matches[0])
			}
		}
	}
	return nil
}

// relationField finds the parent field that holds rel's loaded rows: a field
// tagged `preload:"<RelationName>"` wins, else one named after the relation.
func relationField(parent reflect.Type, rel Relation) ([]int, error) {
	var named []int
	for i := 0; i < parent.NumField(); i++ {
		field := parent.Field(i)
		if field.PkgPath != "" {
			continue
		}
		if tag, ok := field.Tag.Lookup("preload"); ok {
			if strings.Split(tag, ",")[0] == rel.Name() {
				return field.Index, nil
			}
		}
		if field.Name == rel.Name() {
			named = field.Index
		}
	}
	if named != nil {
		return named, nil
	}
	return nil, fmt.Errorf("sqlkit: Preload of %q: %s has no field tagged `preload:%q` or named %q",
		rel.Name(), parent, rel.Name(), rel.Name())
}

// derefType returns the struct type behind t and whether t is a pointer.
func derefType(t reflect.Type) (reflect.Type, bool) {
	if t.Kind() == reflect.Pointer {
		return t.Elem(), true
	}
	return t, false
}

// elemStruct returns the struct value behind v (dereferencing when isPtr), or an
// invalid Value when v is a nil pointer.
func elemStruct(v reflect.Value, isPtr bool) reflect.Value {
	if isPtr {
		if v.IsNil() {
			return reflect.Value{}
		}
		return v.Elem()
	}
	return v
}

// scalarValue reads a key field as a comparable map key, dereferencing a
// pointer (nullable) field and reporting ok=false when it is nil.
func scalarValue(v reflect.Value) (any, bool) {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, false
		}
		v = v.Elem()
	}
	return v.Interface(), true
}
