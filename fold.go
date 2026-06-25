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

// foldSpec is one registered fold hop: the relation joined and folded, the table
// it loads, where that table's columns sit in the projected row (colOff is the
// index of its first column, ncols how many), and parent — the index of the hop
// it nests under in the same slice, or -1 when it folds directly off the base.
type foldSpec struct {
	rel    Relation
	target Table
	colOff int
	ncols  int
	parent int
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
	q, _ = q.registerFoldHop(-1, rel, conditions)
	return q
}

// Fold resolves the From table's relation reaching target and folds it like
// FoldRel, so callers pass the target binding rather than the relation:
// Fold(Posts), not FoldRel(Users.Rels.Posts). When several relations reach the
// same table, the lookup is ambiguous — pick one with FoldRel.
//
// Pass a RelPath as the target to fold a whole chain of related tables, each hop
// its own LEFT JOIN (base → path[0] → path[1] → …); the conditions then apply to
// the final hop:
//
//	db.Select().From(Users).
//	    Fold(sqlkit.RelPath(Posts, Comments), Comments.Approved.Eq(true)).
//	    All(ctx).Scan(&users)
func (q SelectQuery) Fold(target Table, conditions ...Predicate) SelectQuery {
	if q.base == nil {
		return q.fail(fmt.Errorf("sqlkit: Fold requires From to be a table"))
	}
	if q.hasCols {
		return q.fail(fmt.Errorf("sqlkit: Fold requires Select with no explicit columns"))
	}
	path := []Table{target}
	if rp, ok := target.(relPath); ok {
		if len(rp.path) == 0 {
			return q.fail(fmt.Errorf("sqlkit: Fold: RelPath requires at least one table"))
		}
		path = rp.path
	}
	return q.foldPath(path, conditions)
}

// foldPath registers a fold hop for each table on path, resolving each against
// the previous one's relations (path[0] against the base). Conditions apply to
// the final hop.
func (q SelectQuery) foldPath(path []Table, conditions []Predicate) SelectQuery {
	src := q.base
	parent := -1
	for i, tbl := range path {
		rs, ok := src.(RelationSource)
		if !ok {
			return q.fail(fmt.Errorf("sqlkit: Fold: %s exposes no relations", src.Info().QualifiedName()))
		}
		rel, n := relationReaching(rs, tbl)
		switch {
		case n == 0:
			return q.fail(fmt.Errorf("sqlkit: Fold: %s has no relation to %s",
				src.Info().QualifiedName(), tbl.Info().QualifiedName()))
		case n > 1:
			return q.fail(fmt.Errorf("sqlkit: Fold: %s has multiple relations to %s; pick one with FoldRel",
				src.Info().QualifiedName(), tbl.Info().QualifiedName()))
		}
		var conds []Predicate
		if i == len(path)-1 {
			conds = conditions
		}
		var idx int
		if q, idx = q.registerFoldHop(parent, rel, conds); q.err != nil {
			return q
		}
		src = tbl
		parent = idx
	}
	return q
}

// registerFoldHop adds a LEFT JOIN to rel.Target() nested under the hop at parent
// (-1 = the base), projects the target's columns, and records the hop, returning
// the resulting query and the hop's index. A path that revisits a table already
// folded under the same parent reuses that join (so sibling paths can share a
// prefix); revisiting it under a different parent, or folding the base's own
// table, needs an alias and errors — single-level joins reference each table by
// its real name.
func (q SelectQuery) registerFoldHop(parent int, rel Relation, conditions []Predicate) (SelectQuery, int) {
	if q.err != nil {
		return q, -1
	}
	target := rel.Target()
	for i, f := range q.folds {
		if !sameTable(f.target, target) {
			continue
		}
		if f.parent == parent {
			return q, i // shared-prefix hop already joined
		}
		return q.fail(fmt.Errorf("sqlkit: Fold of %q: %s is already folded via a different path; that needs an alias, which is not supported yet",
			rel.Name(), target.Info().QualifiedName())), -1
	}
	if sameTable(target, q.base) {
		return q.fail(fmt.Errorf("sqlkit: Fold of %q: folding a table onto itself needs an alias, which is not supported yet", rel.Name())), -1
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
		parent: parent,
	})
	return q, len(q.folds) - 1
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
	// Build each hop's node and edge, then attach it to its parent. A hop's parent
	// is always registered earlier (parent < i, or -1 for the base), so its node is
	// ready by the time this hop is processed.
	nodes := make([]*foldNode, len(specs))
	edges := make([]foldEdge, len(specs))
	for i, spec := range specs {
		parentType := rootBase
		if spec.parent >= 0 {
			parentType = nodes[spec.parent].elemType
		}
		edge, err := buildFoldEdge(parentType, spec, flat)
		if err != nil {
			return nil, err
		}
		nodes[i] = edge.node
		edges[i] = edge
	}
	for i, spec := range specs {
		if spec.parent < 0 {
			root.children = append(root.children, edges[i])
		} else {
			nodes[spec.parent].children = append(nodes[spec.parent].children, edges[i])
		}
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
