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
//
// Two refinements (see Preload and RelPath): predicates passed to Preload narrow
// the follow-up SELECT to matching children (a conditional preload), and a RelPath
// target eager-loads a whole chain of related tables, each hop its own batched
// query (a nested preload). A nested preload runs against the just-loaded children
// before they are copied onto their parents, so value-type child fields still
// carry their grandchildren.

// preloadSpec is one registered preload: the relation to load, the table it loads
// (a RelationSource, so a RelPath can find this spec and resolve the next hop
// against it), conditions narrowing the follow-up SELECT, and nested preloads run
// against the loaded children.
type preloadSpec struct {
	rel    Relation
	target Table // table this preload loads; nil for PreloadRel
	wheres []Predicate
	nested []preloadSpec
}

// relPath is a preload path of related tables (base → path[0] → path[1] → …),
// built with RelPath and passed to Preload. It embeds the leaf table so it
// satisfies Table for Preload's parameter; Preload recovers the full path.
type relPath struct {
	Table         // the leaf (last) table on the path
	path  []Table // the full chain of related tables, from the base outward
}

// RelPath builds a nested preload target from a chain of related tables, each
// related to the one before it (the first to the From table). Pass it to Preload
// to eager-load along the whole path, creating any intermediate level not already
// registered; the Preload conditions then apply to the final table:
//
//	db.Select().From(Users).
//	    Preload(sqlkit.RelPath(Posts, Comments), Comments.Approved.Eq(true)).
//	    All(ctx).Scan(&users)
//	// Users → Posts → Comments, in three batched queries.
func RelPath(tables ...Table) Table {
	if len(tables) == 0 {
		return relPath{} // Preload defers an empty-path error
	}
	return relPath{Table: tables[len(tables)-1], path: tables}
}

// relationReaching returns src's relation whose target is t and how many match,
// so callers can report 0 (none) or >1 (ambiguous) distinctly.
func relationReaching(src RelationSource, t Table) (Relation, int) {
	want := t.Info()
	var found Relation
	matches := 0
	for _, rel := range src.Relations() {
		if got := rel.Target().Info(); got.Schema == want.Schema && got.Name == want.Name {
			found, matches = rel, matches+1
		}
	}
	return found, matches
}

// ambiguousRelation builds the error reported when several relations of src reach
// target, directing the caller to PreloadRel.
func ambiguousRelation(src, target Table) error {
	return fmt.Errorf("sqlkit: Preload: %s has multiple relations to %s; pick one with PreloadRel",
		src.Info().QualifiedName(), target.Info().QualifiedName())
}

// defaultPreloadChunkSize is the default cap on parent keys per follow-up IN query
// of a Preload (WithPreloadBatchSize overrides it), echoing SQLAlchemy
// selectinload's fixed-size batching for plan-cache friendliness.
const defaultPreloadChunkSize = 1000

// preloadChunkOf reports the executor's configured preload chunk size, or the
// default when the executor does not expose one.
func preloadChunkOf(ex executorBinding) int {
	if p, ok := ex.(interface{ preloadChunkSize() int }); ok {
		return p.preloadChunkSize()
	}
	return defaultPreloadChunkSize
}

// applyPreloads runs every registered preload against the just-scanned dest.
func applyPreloads(ctx context.Context, ex executorBinding, dest any, specs []preloadSpec) error {
	for _, spec := range specs {
		if err := preloadRelation(ctx, ex, dest, spec); err != nil {
			return err
		}
	}
	return nil
}

// preloadRelation eager-loads spec.rel onto dest, which is the Scan destination: a
// pointer to a slice of parents (every row) or to a single parent (First/One).
func preloadRelation(ctx context.Context, ex executorBinding, dest any, spec preloadSpec) error {
	rel := spec.rel
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

	// Collect distinct parent keys and the parents each maps to. keyOrder records
	// the keys in first-seen (parent) order so the IN value list — and the
	// statements AutoChunk splits it into — is deterministic rather than following
	// Go's randomized map iteration, keeping the split plan-cache friendly.
	groups := map[any][]int{}
	var keyOrder []any
	for i, p := range parents {
		ps := elemStruct(p, parentPtr)
		if !ps.IsValid() {
			continue
		}
		key, ok := scalarValue(ps.FieldByIndex(basePath))
		if !ok {
			continue
		}
		if _, seen := groups[key]; !seen {
			keyOrder = append(keyOrder, key)
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
	vals := make([]any, 0, len(keyOrder))
	for _, key := range keyOrder {
		vals = append(vals, sql.Val(key))
	}

	// Scan children into a fresh []childElem. Conditions are added before the IN so
	// the IN stays the lone deferred predicate (newInPredicate carries chunk
	// metadata): AutoChunk caps each follow-up at the preload batch size, splitting a
	// wide parent set across plan-cache-friendly statements like a user Column.In.
	childrenPtr := reflect.New(reflect.SliceOf(childElem))
	q := Select().From(target)
	if len(spec.wheres) > 0 {
		q = q.Where(spec.wheres...)
	}
	q = q.Where(newInPredicate(fkCol, vals)).AutoChunk(ChunkSize(preloadChunkOf(ex)))
	q.ex = ex
	rows := q.All(ctx)
	if err := rows.Scan(childrenPtr.Interface()); err != nil {
		return err
	}
	// Recurse before assignment: a to-many relation copies child structs into the
	// parent's slice, so the grandchildren must already be loaded onto the children
	// here for value-type fields to carry them.
	if len(spec.nested) > 0 {
		if err := applyPreloads(ctx, ex, childrenPtr.Interface(), spec.nested); err != nil {
			return err
		}
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
