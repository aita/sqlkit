package sqlkit

import (
	stdsql "database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/aita/sqlkit/internal/reflectmap"
)

// Folding loads related rows in a single round trip: FoldRel adds a LEFT JOIN to
// the relation's target, projects the target's columns alongside the base ones,
// and at Scan groups the flat joined rows back into nested destination fields —
// the one-query counterpart to Preload's batched follow-up SELECT.
//
// A to-many relation fills a slice field, a to-one relation a struct or pointer
// field, found (as with Preload) by a `preload:"<RelationName>"` tag or a field
// named after the relation. Parents are de-duplicated by their primary key, so a
// parent that joins several children appears once carrying all of them; a parent
// with no match (the LEFT JOIN yields NULLs) keeps an empty slice or a nil/zero
// to-one field.
//
// Fold trades a second query for wider, partly redundant rows (each parent column
// repeats per child), and LIMIT counts joined rows rather than parents — prefer
// Preload when children are numerous or the result is paginated.

// foldSpec is one registered fold: the relation joined and folded, the table it
// loads, and where that table's columns sit in the projected row (colOff is the
// index of its first column, ncols how many).
type foldSpec struct {
	rel    Relation
	target Table
	colOff int
	ncols  int
}

// FoldRel joins rel's target with a LEFT JOIN, projects its columns next to the
// base table's, and folds the joined rows into the nested destination field at
// Scan — a single-query eager load:
//
//	db.Select().From(Users).FoldRel(Users.Rels.Posts).All(ctx).Scan(&users)
//
// Conditions are ANDed into the JOIN's ON clause, so only matching children are
// folded (non-matching parents still appear, with an empty relation field).
// FoldRel requires the chain to start from From with no explicit Select columns
// (it controls the projection); errors are deferred to the terminal.
func (q SelectQuery) FoldRel(rel Relation, conditions ...Predicate) SelectQuery {
	if q.base == nil {
		return q.fail(fmt.Errorf("sqlkit: FoldRel requires From to be a table"))
	}
	if q.hasCols {
		return q.fail(fmt.Errorf("sqlkit: FoldRel requires Select with no explicit columns"))
	}
	if rel == nil {
		return q.fail(fmt.Errorf("sqlkit: FoldRel requires a non-nil relation"))
	}
	target := rel.Target()
	// The single-level join references each table by its real name, so the target
	// must be distinct from the base and from any already-folded table; a repeated
	// table (e.g. a self-reference) would need an alias, which is not yet supported.
	if sameTable(target, q.base) {
		return q.fail(fmt.Errorf("sqlkit: FoldRel of %q: folding a table onto itself needs an alias, which is not supported yet", rel.Name()))
	}
	for _, f := range q.folds {
		if sameTable(f.target, target) {
			return q.fail(fmt.Errorf("sqlkit: FoldRel of %q: %s is already folded; folding the same table twice needs an alias, which is not supported yet",
				rel.Name(), target.Info().QualifiedName()))
		}
	}

	off := len(q.base.Columns())
	for _, f := range q.folds {
		off += f.ncols
	}
	on := rel.JoinOn()
	if len(conditions) > 0 {
		on = on.And(conditions...)
	}
	q.query = q.query.LeftJoin(target, on.Expression()).AddColumns(selectionsOf(target.Columns())...)
	q.folds = append(append([]foldSpec(nil), q.folds...), foldSpec{
		rel:    rel,
		target: target,
		colOff: off,
		ncols:  len(target.Columns()),
	})
	return q
}

// Fold resolves the From table's relation reaching target and folds it like
// FoldRel, so callers pass the target binding rather than the relation:
// Fold(Posts), not FoldRel(Users.Rels.Posts). When several relations reach the
// same table, the lookup is ambiguous — pick one with FoldRel.
func (q SelectQuery) Fold(target Table, conditions ...Predicate) SelectQuery {
	if q.base == nil {
		return q.fail(fmt.Errorf("sqlkit: Fold requires From to be a table"))
	}
	if _, ok := target.(relPath); ok {
		return q.fail(fmt.Errorf("sqlkit: Fold: nested fold paths are not supported yet"))
	}
	rs, ok := q.base.(RelationSource)
	if !ok {
		return q.fail(fmt.Errorf("sqlkit: Fold: %s exposes no relations", q.base.Info().QualifiedName()))
	}
	rel, n := relationReaching(rs, target)
	switch {
	case n == 0:
		return q.fail(fmt.Errorf("sqlkit: Fold: %s has no relation to %s",
			q.base.Info().QualifiedName(), target.Info().QualifiedName()))
	case n > 1:
		return q.fail(fmt.Errorf("sqlkit: Fold: %s has multiple relations to %s; pick one with FoldRel",
			q.base.Info().QualifiedName(), target.Info().QualifiedName()))
	}
	return q.FoldRel(rel, conditions...)
}

// withFolds attaches the registered folds to a Rows so Scan materializes the
// joined rows into nested destination fields instead of a flat scan.
func (q SelectQuery) withFolds(rows Rows) Rows {
	rows.folds = q.folds
	rows.foldBase = q.base
	return rows
}

// scannerType is the sql.Scanner interface, used to detect destination fields
// that handle their own NULL (so they are scanned natively rather than through
// the pointer-to-pointer NULL trick).
var scannerType = reflect.TypeOf((*stdsql.Scanner)(nil)).Elem()

// foldNode is one level of the fold tree: the base table is the root, each folded
// relation a child. It carries the element struct type, the columns it owns in
// the projected row, the columns forming its identity (primary key), and its
// child edges.
type foldNode struct {
	elemType reflect.Type
	cols     []foldColumn
	pkCols   []foldColumn
	children []foldEdge
}

// foldColumn binds a projected row position to the destination field it fills.
type foldColumn struct {
	rowIdx int
	field  reflectmap.Field
}

// foldEdge links a parent node to a child node through the parent's destination
// field, recording how to assign it.
type foldEdge struct {
	destPath  []int
	toMany    bool
	elemPtr   bool
	fieldType reflect.Type
	node      *foldNode
}

// foldPlan is the prepared fold tree plus the flat per-column scan descriptors,
// indexed by projected row position.
type foldPlan struct {
	root *foldNode
	flat []flatCol
}

// flatCol describes how one projected column is scanned and routed: skip drops a
// column with no destination field, json decodes it, nullable scans it natively
// (pointer or sql.Scanner field), otherwise it is scanned through a **T holder so
// a LEFT JOIN NULL is tolerated.
type flatCol struct {
	field    reflectmap.Field
	typ      reflect.Type
	skip     bool
	json     bool
	nullable bool
}

// foldInst is a built element during scanning: a pointer to the element struct
// plus, per child edge, the de-dup map and first-seen order of its children.
type foldInst struct {
	elem  reflect.Value
	kids  []map[any]*foldInst
	order [][]*foldInst
}

// foldScanRows materializes the joined rows in rows into dest (a pointer to a
// slice) by folding them into the nested fields described by base and specs.
func foldScanRows(rows *stdsql.Rows, dest any, base Table, specs []foldSpec) error {
	dv := reflect.ValueOf(dest)
	if dv.Kind() != reflect.Pointer || dv.IsNil() {
		return fmt.Errorf("sqlkit: a folded query scans into a non-nil pointer to a slice, got %T", dest)
	}
	slice := dv.Elem()
	if slice.Kind() != reflect.Slice {
		return fmt.Errorf("sqlkit: a folded query scans into a pointer to a slice, got %T", dest)
	}
	rootElem := slice.Type().Elem()
	rootPtr := rootElem.Kind() == reflect.Pointer
	rootBase := rootElem
	if rootPtr {
		rootBase = rootElem.Elem()
	}
	if rootBase.Kind() != reflect.Struct {
		return fmt.Errorf("sqlkit: a folded query scans into a slice of structs, got %s", rootElem)
	}

	plan, err := buildFoldPlan(rootBase, base, specs)
	if err != nil {
		return err
	}

	noKey := len(plan.root.pkCols) == 0
	roots := map[any]*foldInst{}
	var order []*foldInst
	for rows.Next() {
		holders, err := plan.scanRow(rows)
		if err != nil {
			return err
		}
		var inst *foldInst
		if noKey {
			// Without a base key, each row is a distinct parent (only safe because a
			// to-many fold requires the key — see buildFoldPlan).
			inst = newFoldInst(plan.root, plan.flat, holders)
			order = append(order, inst)
		} else {
			key, _ := plan.root.identity(plan.flat, holders)
			if inst = roots[key]; inst == nil {
				inst = newFoldInst(plan.root, plan.flat, holders)
				roots[key] = inst
				order = append(order, inst)
			}
		}
		foldChildren(plan.root, inst, plan.flat, holders)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	result := reflect.MakeSlice(slice.Type(), len(order), len(order))
	for i, inst := range order {
		materializeFold(plan.root, inst)
		if rootPtr {
			result.Index(i).Set(inst.elem)
		} else {
			result.Index(i).Set(inst.elem.Elem())
		}
	}
	slice.Set(result)
	return nil
}

// buildFoldPlan prepares the fold tree and flat scan descriptors for a root
// element type, the base table, and the registered folds.
func buildFoldPlan(rootBase reflect.Type, base Table, specs []foldSpec) (*foldPlan, error) {
	total := len(base.Columns())
	for _, s := range specs {
		total += s.ncols
	}
	flat := make([]flatCol, total)

	root, err := buildFoldNode(rootBase, base, 0, flat)
	if err != nil {
		return nil, err
	}
	for _, spec := range specs {
		edge, err := buildFoldEdge(rootBase, spec, flat)
		if err != nil {
			return nil, err
		}
		root.children = append(root.children, edge)
	}
	return &foldPlan{root: root, flat: flat}, nil
}

// buildFoldNode builds a node for elemType backed by table, whose columns occupy
// the projected row starting at colOff, filling the flat descriptors for them.
func buildFoldNode(elemType reflect.Type, table Table, colOff int, flat []flatCol) (*foldNode, error) {
	fields := reflectmap.Fields(elemType)
	node := &foldNode{elemType: elemType}
	byName := map[string]foldColumn{}
	for i, col := range table.Columns() {
		idx := colOff + i
		f, ok := fields[strings.ToLower(col.ColumnName())]
		if !ok {
			flat[idx] = flatCol{skip: true}
			continue
		}
		ft := elemType.FieldByIndex(f.Path).Type
		flat[idx] = flatCol{field: f, typ: ft, json: f.JSON, nullable: isNullableField(ft)}
		fc := foldColumn{rowIdx: idx, field: f}
		node.cols = append(node.cols, fc)
		byName[col.ColumnName()] = fc
	}
	for _, pk := range table.PrimaryKey() {
		fc, ok := byName[pk.ColumnName()]
		if !ok {
			return nil, fmt.Errorf("sqlkit: Fold: %s has no field for key column %q (needed to group rows)",
				elemType, pk.ColumnName())
		}
		node.pkCols = append(node.pkCols, fc)
	}
	return node, nil
}

// buildFoldEdge resolves the parent field a fold loads into and builds the child
// node for the folded table.
func buildFoldEdge(parent reflect.Type, spec foldSpec, flat []flatCol) (foldEdge, error) {
	destPath, err := relationField(parent, spec.rel)
	if err != nil {
		return foldEdge{}, err
	}
	fieldType := parent.FieldByIndex(destPath).Type
	toMany := spec.rel.Cardinality() == ToMany

	childElem := fieldType
	if toMany {
		if fieldType.Kind() != reflect.Slice {
			return foldEdge{}, fmt.Errorf("sqlkit: FoldRel of %q: to-many destination field must be a slice, got %s", spec.rel.Name(), fieldType)
		}
		childElem = fieldType.Elem()
	}
	elemPtr := childElem.Kind() == reflect.Pointer
	childBase := childElem
	if elemPtr {
		childBase = childElem.Elem()
	}
	if childBase.Kind() != reflect.Struct {
		return foldEdge{}, fmt.Errorf("sqlkit: FoldRel of %q: destination element must be a struct, got %s", spec.rel.Name(), childElem)
	}

	node, err := buildFoldNode(childBase, spec.target, spec.colOff, flat)
	if err != nil {
		return foldEdge{}, err
	}
	if toMany && len(node.pkCols) == 0 {
		return foldEdge{}, fmt.Errorf("sqlkit: FoldRel of %q: to-many target %s needs a primary key to de-duplicate rows",
			spec.rel.Name(), spec.target.Info().QualifiedName())
	}
	return foldEdge{destPath: destPath, toMany: toMany, elemPtr: elemPtr, fieldType: fieldType, node: node}, nil
}

// scanRow scans the current row into a holder per column, returned indexed by
// projected row position.
func (p *foldPlan) scanRow(rows *stdsql.Rows) ([]reflect.Value, error) {
	holders := make([]reflect.Value, len(p.flat))
	dests := make([]any, len(p.flat))
	for i, fc := range p.flat {
		switch {
		case fc.skip:
			var sink any
			dests[i] = &sink
		case fc.json:
			h := reflect.New(fc.typ)
			holders[i] = h
			dests[i] = jsonScanner{dst: h.Interface()}
		case fc.nullable:
			// A pointer or sql.Scanner field tolerates NULL natively.
			h := reflect.New(fc.typ)
			holders[i] = h
			dests[i] = h.Interface()
		default:
			// A value field is scanned through a **T so a LEFT JOIN NULL leaves the
			// inner pointer nil rather than failing the scan.
			h := reflect.New(reflect.PointerTo(fc.typ))
			holders[i] = h
			dests[i] = h.Interface()
		}
	}
	if err := rows.Scan(dests...); err != nil {
		return nil, err
	}
	return holders, nil
}

// newFoldInst allocates an element for node and fills its own columns from the
// row holders.
func newFoldInst(node *foldNode, flat []flatCol, holders []reflect.Value) *foldInst {
	inst := &foldInst{
		elem:  reflect.New(node.elemType),
		kids:  make([]map[any]*foldInst, len(node.children)),
		order: make([][]*foldInst, len(node.children)),
	}
	for i := range inst.kids {
		inst.kids[i] = map[any]*foldInst{}
	}
	node.fill(inst.elem.Elem(), flat, holders)
	return inst
}

// foldChildren routes each child relation of node for the current row onto inst,
// creating a child element the first time its key is seen and recursing into it.
func foldChildren(node *foldNode, inst *foldInst, flat []flatCol, holders []reflect.Value) {
	for ei, edge := range node.children {
		key, present := edge.node.identity(flat, holders)
		if !present {
			continue
		}
		child := inst.kids[ei][key]
		if child == nil {
			child = newFoldInst(edge.node, flat, holders)
			inst.kids[ei][key] = child
			inst.order[ei] = append(inst.order[ei], child)
		}
		foldChildren(edge.node, child, flat, holders)
	}
}

// materializeFold assigns each built child onto its parent's destination field,
// recursing depth-first so a value-type child carries its own children before it
// is copied into a parent slice.
func materializeFold(node *foldNode, inst *foldInst) {
	elem := inst.elem.Elem()
	for ei, edge := range node.children {
		children := inst.order[ei]
		for _, c := range children {
			materializeFold(edge.node, c)
		}
		field := elem.FieldByIndex(edge.destPath)
		if edge.toMany {
			s := reflect.MakeSlice(edge.fieldType, len(children), len(children))
			for i, c := range children {
				if edge.elemPtr {
					s.Index(i).Set(c.elem)
				} else {
					s.Index(i).Set(c.elem.Elem())
				}
			}
			field.Set(s)
			continue
		}
		if len(children) > 0 {
			c := children[0]
			if edge.elemPtr {
				field.Set(c.elem)
			} else {
				field.Set(c.elem.Elem())
			}
		}
	}
}

// identity reads node's key columns from the row holders, returning a comparable
// key and whether the level is present (false when a key column is NULL, i.e. a
// LEFT JOIN produced no matching row).
func (node *foldNode) identity(flat []flatCol, holders []reflect.Value) (any, bool) {
	keys := make([]any, len(node.pkCols))
	for i, c := range node.pkCols {
		v, ok := holderValue(holders[c.rowIdx], flat[c.rowIdx])
		if !ok {
			return nil, false
		}
		keys[i] = v
	}
	return identityKey(keys), true
}

// fill copies node's own columns from the row holders into struct.
func (node *foldNode) fill(structVal reflect.Value, flat []flatCol, holders []reflect.Value) {
	for _, c := range node.cols {
		fc := flat[c.rowIdx]
		h := holders[c.rowIdx]
		field := structVal.FieldByIndex(c.field.Path)
		if fc.json || fc.nullable {
			field.Set(h.Elem())
			continue
		}
		inner := h.Elem()
		if inner.IsNil() {
			continue
		}
		field.Set(inner.Elem())
	}
}

// holderValue reads a scanned column as a comparable value, reporting ok=false
// when the column was NULL.
func holderValue(h reflect.Value, fc flatCol) (any, bool) {
	if fc.json {
		return h.Elem().Interface(), true
	}
	if fc.nullable {
		v := h.Elem()
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return nil, false
			}
			v = v.Elem()
		}
		return v.Interface(), true
	}
	inner := h.Elem()
	if inner.IsNil() {
		return nil, false
	}
	return inner.Elem().Interface(), true
}

// identityKey combines key column values into a single comparable map key.
func identityKey(vals []any) any {
	if len(vals) == 1 {
		return vals[0]
	}
	var b strings.Builder
	for i, v := range vals {
		if i > 0 {
			b.WriteByte(0)
		}
		fmt.Fprintf(&b, "%T:%v", v, v)
	}
	return b.String()
}

// isNullableField reports whether t handles a SQL NULL on its own — a pointer
// type or an sql.Scanner implementation — so it is scanned natively rather than
// through the **T NULL trick.
func isNullableField(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer {
		return true
	}
	return reflect.PointerTo(t).Implements(scannerType)
}
