package autogen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aita/sqlkit/codegen"
	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

// TableRename records that a table was renamed From -> To (in the orientation of
// the plan being rendered).
type TableRename struct{ From, To string }

// ColumnRename records that a column of Table was renamed From -> To. Table is
// the destination table's name (after any table rename).
type ColumnRename struct {
	Table string
	From  string
	To    string
}

// Renames are the rename hints applied to a render, turning what would be
// drop+add (data loss) into op.RenameTable / op.RenameColumn.
type Renames struct {
	Tables  []TableRename
	Columns []ColumnRename
}

// reverse flips the renames for the Down direction (To<->From), remapping each
// column rename onto its source table.
func (r Renames) reverse() Renames {
	toSource := map[string]string{}
	var tables []TableRename
	for _, tr := range r.Tables {
		tables = append(tables, TableRename{From: tr.To, To: tr.From})
		toSource[tr.To] = tr.From
	}
	var columns []ColumnRename
	for _, cr := range r.Columns {
		table := cr.Table
		if src, ok := toSource[cr.Table]; ok {
			table = src
		}
		columns = append(columns, ColumnRename{Table: table, From: cr.To, To: cr.From})
	}
	return Renames{Tables: tables, Columns: columns}
}

// Options configures Render.
type Options struct {
	// Dialect resolves column-type equality and default/check rendering.
	Dialect sql.Dialect
	// Renames turn detected drop+add into rename operations.
	Renames Renames
}

// Result is the rendered migration bodies and the imports they need.
type Result struct {
	Up       string
	Down     string
	UsesSQL  bool // references the sql package (sql.Raw, sql.CommentOn...)
	UsesDecl bool // references the decl package (almost always true)
}

// Empty reports whether the diff produced no operations in either direction.
func (r Result) Empty() bool {
	return strings.TrimSpace(r.Up) == "" && strings.TrimSpace(r.Down) == ""
}

// Render diffs source against target and returns the Operator-DSL bodies for a
// migration's Up (source -> target) and Down (target -> source, a best-effort
// reverse). Statements are ordered for safety, mirroring migrate.Diff.
func Render(source, target meta.Metadata, opts Options) (Result, error) {
	if opts.Dialect == nil {
		return Result{}, fmt.Errorf("autogen: Render needs a dialect")
	}
	up := &renderer{dialect: opts.Dialect, renames: opts.Renames}
	upBody, err := up.plan(source, target)
	if err != nil {
		return Result{}, err
	}
	down := &renderer{dialect: opts.Dialect, renames: opts.Renames.reverse()}
	downBody, err := down.plan(target, source)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Up:       upBody,
		Down:     downBody,
		UsesSQL:  up.usesSQL || down.usesSQL,
		UsesDecl: up.usesDecl || down.usesDecl,
	}, nil
}

// renderer accumulates the op.* statements for one direction of a plan.
type renderer struct {
	dialect  sql.Dialect
	renames  Renames
	usesSQL  bool
	usesDecl bool
}

// plan walks from -> to and returns the ordered op.* statements as Go source
// (one statement per element, gofmt-normalized by the caller).
func (r *renderer) plan(from, to meta.Metadata) (string, error) {
	// Tables are keyed by schema and name so same-named tables in different
	// schemas do not collapse, matching migrate.Diff.
	fromByKey := tablesByKey(from)
	toByKey := tablesByKey(to)
	// Rename hints are name-based (the Operator targets one schema), so they are
	// matched against name-indexed views.
	fromByName := tablesByName(from)
	toByName := tablesByName(to)

	// Identify table-rename pairs that match a real from-table and to-table.
	renamedFrom := map[string]bool{}
	renamedTo := map[string]bool{}
	type pair struct{ from, to meta.Table }
	var renamePairs []pair
	for _, tr := range r.renames.Tables {
		f, okF := fromByName[tr.From]
		t, okT := toByName[tr.To]
		if okF && okT {
			renamePairs = append(renamePairs, pair{f, t})
			renamedFrom[tr.From] = true
			renamedTo[tr.To] = true
		}
	}

	var dropFKs, dropIndexes, dropTables, renameTables, renameColumns []string
	var createTables, alters, createIndexes, addFKs, comments []string

	// Dropped tables (not renamed): drop foreign keys, then the table.
	var dropped, added []meta.Table
	for _, t := range from.Tables {
		if _, ok := toByKey[key(t)]; ok || renamedFrom[t.Name] {
			continue
		}
		dropped = append(dropped, t)
		for _, fk := range t.ForeignKeys {
			dropFKs = append(dropFKs, fmt.Sprintf("op.DropForeignKey(%q, %q)", fk.Name, t.Name))
		}
		dropTables = append(dropTables, fmt.Sprintf("op.DropTable(%q)", t.Name))
	}

	// Added tables (not a rename target): create the table and its foreign keys.
	for _, t := range to.Tables {
		if _, ok := fromByKey[key(t)]; ok || renamedTo[t.Name] {
			continue
		}
		added = append(added, t)
		create, err := r.createTable(t)
		if err != nil {
			return "", err
		}
		createTables = append(createTables, create)
		for _, fk := range t.ForeignKeys {
			addFKs = append(addFKs, r.fkCreate(t.Name, fk))
		}
	}

	// Heuristic: a lone dropped table and a lone added table with identical
	// column sets is likely a rename; hint the author without changing behavior.
	if len(renamePairs) == 0 && len(dropped) == 1 && len(added) == 1 {
		if sameColumnNames(dropped[0], added[0]) {
			dropTables = append([]string{fmt.Sprintf(
				"// possible rename: table %q -> %q? pass --rename-table %s:%s to keep data",
				dropped[0].Name, added[0].Name, dropped[0].Name, added[0].Name)}, dropTables...)
		}
	}

	// Renamed tables: emit the rename, then diff the pair like a retained table.
	for _, p := range renamePairs {
		renameTables = append(renameTables, fmt.Sprintf("op.RenameTable(%q, %q)", p.from.Name, p.to.Name))
		if err := r.diffTable(p.from, p.to, &renameColumns, &alters, &createIndexes, &dropIndexes, &addFKs, &dropFKs, &comments); err != nil {
			return "", err
		}
	}

	// Retained tables: diff columns, indexes, foreign keys, checks, comments.
	for _, t := range to.Tables {
		oldT, ok := fromByKey[key(t)]
		if !ok || renamedTo[t.Name] {
			continue
		}
		if err := r.diffTable(oldT, t, &renameColumns, &alters, &createIndexes, &dropIndexes, &addFKs, &dropFKs, &comments); err != nil {
			return "", err
		}
	}

	objs := r.objectOps(from, to)

	// Renames run first: a renamed table must exist under its new name before
	// the drops, alters, and re-adds that diffTable emitted against that name.
	var out []string
	out = append(out, renameTables...)
	out = append(out, renameColumns...)
	out = append(out, objs.dropGrants...)
	out = append(out, objs.dropMatviews...)
	out = append(out, dropFKs...)
	out = append(out, dropIndexes...)
	out = append(out, dropTables...)
	out = append(out, objs.dropSequences...)
	out = append(out, objs.dropEnums...)
	out = append(out, objs.dropExtensions...)
	out = append(out, objs.createExtensions...)
	out = append(out, objs.createEnums...)
	out = append(out, objs.createSequences...)
	out = append(out, createTables...)
	// Appended enum labels run in the alter phase, not the pre-table drop phase.
	out = append(out, objs.alterEnums...)
	out = append(out, alters...)
	out = append(out, createIndexes...)
	out = append(out, addFKs...)
	out = append(out, comments...)
	out = append(out, objs.createMatviews...)
	out = append(out, objs.createGrants...)
	return strings.Join(out, "\n"), nil
}

// objectOps renders the op.* calls that reconcile the non-table objects
// (extensions, enums, sequences) between from and to. Names are rendered bare,
// since the Operator qualifies them with the migration's schema.
func (r *renderer) objectOps(from, to meta.Metadata) renderedObjects {
	var o renderedObjects

	oldExt := map[string]meta.Extension{}
	for _, e := range from.Extensions {
		oldExt[e.Name] = e
	}
	newExt := map[string]meta.Extension{}
	for _, e := range to.Extensions {
		newExt[e.Name] = e
	}
	for _, e := range from.Extensions {
		if n, ok := newExt[e.Name]; !ok || n != e {
			o.dropExtensions = append(o.dropExtensions, fmt.Sprintf("op.DropExtension(%q)", e.Name))
		}
	}
	for _, e := range to.Extensions {
		if old, ok := oldExt[e.Name]; !ok || old != e {
			o.createExtensions = append(o.createExtensions, r.extensionCreate(e))
		}
	}

	oldEnum := map[string]meta.Enum{}
	for _, e := range from.Enums {
		oldEnum[e.QualifiedName()] = e
	}
	newEnum := map[string]meta.Enum{}
	for _, e := range to.Enums {
		newEnum[e.QualifiedName()] = e
	}
	// A pure append (old labels are a prefix of the new ones) becomes one
	// op.AddEnumValue per added label, which keeps any column using the type
	// usable. Any other change still drops and recreates the type.
	for _, e := range from.Enums {
		n, ok := newEnum[e.QualifiedName()]
		if !ok {
			o.dropEnums = append(o.dropEnums, fmt.Sprintf("op.DropEnum(%q)", e.Name))
			continue
		}
		if e.Equal(n) {
			continue
		}
		if appended, ok := appendedEnumValues(e.Values, n.Values); ok {
			for _, v := range appended {
				o.alterEnums = append(o.alterEnums, fmt.Sprintf("op.AddEnumValue(%q, %q)", e.Name, v))
			}
			continue
		}
		o.dropEnums = append(o.dropEnums, fmt.Sprintf("op.DropEnum(%q)", e.Name))
	}
	for _, e := range to.Enums {
		old, ok := oldEnum[e.QualifiedName()]
		if !ok {
			o.createEnums = append(o.createEnums, fmt.Sprintf("op.CreateEnum(%q, %s)", e.Name, quotedList(e.Values)))
			continue
		}
		if old.Equal(e) {
			continue
		}
		// Recreate only when it is not a pure append (handled via AddEnumValue).
		if _, isAppend := appendedEnumValues(old.Values, e.Values); !isAppend {
			o.createEnums = append(o.createEnums, fmt.Sprintf("op.CreateEnum(%q, %s)", e.Name, quotedList(e.Values)))
		}
	}

	oldSeq := map[string]meta.Sequence{}
	for _, s := range from.Sequences {
		oldSeq[s.QualifiedName()] = s
	}
	newSeq := map[string]meta.Sequence{}
	for _, s := range to.Sequences {
		newSeq[s.QualifiedName()] = s
	}
	for _, s := range from.Sequences {
		if n, ok := newSeq[s.QualifiedName()]; !ok || !s.Equal(n) {
			o.dropSequences = append(o.dropSequences, fmt.Sprintf("op.DropSequence(%q)", s.Name))
		}
	}
	for _, s := range to.Sequences {
		if old, ok := oldSeq[s.QualifiedName()]; !ok || !old.Equal(s) {
			o.createSequences = append(o.createSequences, r.sequenceCreate(s))
		}
	}

	oldMV := map[string]meta.MaterializedView{}
	for _, v := range from.MaterializedViews {
		oldMV[v.QualifiedName()] = v
	}
	newMV := map[string]meta.MaterializedView{}
	for _, v := range to.MaterializedViews {
		newMV[v.QualifiedName()] = v
	}
	for _, v := range from.MaterializedViews {
		if n, ok := newMV[v.QualifiedName()]; !ok || !r.matviewEqual(v, n) {
			o.dropMatviews = append(o.dropMatviews, fmt.Sprintf("op.DropMaterializedView(%q)", v.Name))
		}
	}
	for _, v := range to.MaterializedViews {
		if old, ok := oldMV[v.QualifiedName()]; !ok || !r.matviewEqual(old, v) {
			o.createMatviews = append(o.createMatviews, r.matviewCreate(v))
		}
	}

	oldGrant := map[string]meta.Grant{}
	for _, g := range from.Grants {
		oldGrant[grantKey(g)] = g
	}
	newGrant := map[string]meta.Grant{}
	for _, g := range to.Grants {
		newGrant[grantKey(g)] = g
	}
	for _, g := range from.Grants {
		if n, ok := newGrant[grantKey(g)]; !ok || !g.Equal(n) {
			o.dropGrants = append(o.dropGrants, r.grantRevoke(g))
		}
	}
	for _, g := range to.Grants {
		if old, ok := oldGrant[grantKey(g)]; !ok || !old.Equal(g) {
			o.createGrants = append(o.createGrants, r.grantCreate(g))
		}
	}
	return o
}

// renderedObjects holds the rendered op.* calls for each object kind.
type renderedObjects struct {
	createExtensions []string
	createEnums      []string
	createSequences  []string
	createMatviews   []string
	createGrants     []string
	dropExtensions   []string
	dropEnums        []string
	dropSequences    []string
	dropMatviews     []string
	dropGrants       []string
	// alterEnums are op.AddEnumValue calls for enums whose labels were only
	// appended; they run in the alter phase, not the pre-table drop phase.
	alterEnums []string
}

// grantKey identifies a grant by its target (object type, objects, and
// grantees), so a change to the privileges of the same target/grantees is one
// modification. Grantees must be part of the key: a schema can declare
// several distinct GRANTs on the same object (e.g. SELECT to one role and
// INSERT to another on the same table) — a real, common ACL shape, not a
// variant of one grant — and omitting Grantees would collapse them onto the
// same map entry, silently losing one grant (an unpaired REVOKE with no
// matching re-GRANT) or manufacturing spurious modify-churn between two
// unrelated, unchanged grants that happen to share a target.
func grantKey(g meta.Grant) string {
	return g.ObjectType + "\x00" + strings.Join(g.Objects, ",") + "\x00" + strings.Join(g.Grantees, ",")
}

// appendedEnumValues reports whether next is old with only labels appended (old
// is a prefix of next, in the same order), and if so returns the appended
// labels. Any reorder, removal, or rename reports false, so the caller falls
// back to drop+create. Equal slices report (nil, true).
func appendedEnumValues(old, next []string) ([]string, bool) {
	if len(next) < len(old) {
		return nil, false
	}
	for i := range old {
		if old[i] != next[i] {
			return nil, false
		}
	}
	return next[len(old):], true
}

// matviewEqual compares two materialized views, rendering their bodies for the
// dialect. A missing view never matches.
func (r *renderer) matviewEqual(a, b meta.MaterializedView) bool {
	if a.Query == nil || b.Query == nil {
		return false
	}
	if a.Schema != b.Schema || a.Name != b.Name || a.WithNoData != b.WithNoData || !stringsEqual(a.Columns, b.Columns) {
		return false
	}
	at, _, err := a.Query.ToSQL(r.dialect)
	if err != nil {
		return false
	}
	bt, _, err := b.Query.ToSQL(r.dialect)
	if err != nil {
		return false
	}
	return at == bt
}

func (r *renderer) matviewCreate(v meta.MaterializedView) string {
	r.usesSQL = true
	body, _, _ := v.Query.ToSQL(r.dialect)
	var opts strings.Builder
	if len(v.Columns) > 0 {
		fmt.Fprintf(&opts, ", func(s sql.CreateMaterializedViewBuilder) sql.CreateMaterializedViewBuilder {\nreturn s.Columns(%s)\n}", quotedList(v.Columns))
	} else if v.WithNoData {
		opts.WriteString(", func(s sql.CreateMaterializedViewBuilder) sql.CreateMaterializedViewBuilder {\nreturn s.WithNoData()\n}")
	}
	return fmt.Sprintf("op.CreateMaterializedView(%q, sql.RawSQL(%q)%s)", v.Name, body, opts.String())
}

func (r *renderer) grantCreate(g meta.Grant) string {
	r.usesSQL = true
	return fmt.Sprintf("op.Grant(%s)", grantBuilderExpr("sql.Grant", g, true))
}

func (r *renderer) grantRevoke(g meta.Grant) string {
	r.usesSQL = true
	return fmt.Sprintf("op.Revoke(%s)", grantBuilderExpr("sql.Revoke", g, false))
}

// grantBuilderExpr renders a sql.Grant/sql.Revoke builder chain. grant chooses To
// vs From and whether WITH GRANT OPTION applies (Revoke has no such clause).
func grantBuilderExpr(ctor string, g meta.Grant, grant bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s(%s)", ctor, quotedList(g.Privileges))
	if g.ObjectType != "" {
		fmt.Fprintf(&b, ".OnType(%q, %s)", g.ObjectType, quotedList(g.Objects))
	} else {
		fmt.Fprintf(&b, ".On(%s)", quotedList(g.Objects))
	}
	if grant {
		fmt.Fprintf(&b, ".To(%s)", quotedList(g.Grantees))
		if g.WithGrantOption {
			b.WriteString(".WithGrantOption()")
		}
	} else {
		fmt.Fprintf(&b, ".From(%s)", quotedList(g.Grantees))
	}
	return b.String()
}

func (r *renderer) extensionCreate(e meta.Extension) string {
	if e.Schema == "" {
		return fmt.Sprintf("op.CreateExtension(%q)", e.Name)
	}
	r.usesSQL = true
	return fmt.Sprintf("op.CreateExtension(%q, func(s sql.CreateExtensionBuilder) sql.CreateExtensionBuilder {\nreturn s.Schema(%q)\n})", e.Name, e.Schema)
}

func (r *renderer) sequenceCreate(s meta.Sequence) string {
	var chain strings.Builder
	if s.Increment != nil {
		fmt.Fprintf(&chain, ".Increment(%d)", *s.Increment)
	}
	if s.MinValue != nil {
		fmt.Fprintf(&chain, ".MinValue(%d)", *s.MinValue)
	}
	if s.MaxValue != nil {
		fmt.Fprintf(&chain, ".MaxValue(%d)", *s.MaxValue)
	}
	if s.Start != nil {
		fmt.Fprintf(&chain, ".Start(%d)", *s.Start)
	}
	if s.Cache != nil {
		fmt.Fprintf(&chain, ".Cache(%d)", *s.Cache)
	}
	if s.Cycle != nil {
		if *s.Cycle {
			chain.WriteString(".Cycle()")
		} else {
			chain.WriteString(".NoCycle()")
		}
	}
	if s.OwnedBy != "" {
		fmt.Fprintf(&chain, ".OwnedBy(%q)", s.OwnedBy)
	}
	if chain.Len() == 0 {
		return fmt.Sprintf("op.CreateSequence(%q)", s.Name)
	}
	r.usesSQL = true
	return fmt.Sprintf("op.CreateSequence(%q, func(s sql.CreateSequenceBuilder) sql.CreateSequenceBuilder {\nreturn s%s\n})", s.Name, chain.String())
}

// diffTable appends the operations that turn oldT into newT to the buckets.
func (r *renderer) diffTable(oldT, newT meta.Table, renameColumns, alters, createIndexes, dropIndexes, addFKs, dropFKs, comments *[]string) error {
	name := newT.Name

	// Column renames for this table, kept in rename-declaration order so the
	// post-rename column-change statements below are emitted deterministically
	// (ranging a map would order them by Go's randomized iteration).
	type colRename struct{ from, to string }
	var colRenames []colRename
	renamedFromCol := map[string]bool{}
	renamedToCol := map[string]bool{}
	oldCols := columnsByName(oldT)
	newCols := columnsByName(newT)
	for _, cr := range r.renames.Columns {
		if cr.Table != name {
			continue
		}
		if _, ok := oldCols[cr.From]; !ok {
			continue
		}
		if _, ok := newCols[cr.To]; !ok {
			continue
		}
		*renameColumns = append(*renameColumns, fmt.Sprintf("op.RenameColumn(%q, %q, %q)", name, cr.From, cr.To))
		colRenames = append(colRenames, colRename{from: cr.From, to: cr.To})
		renamedFromCol[cr.From] = true
		renamedToCol[cr.To] = true
	}

	// Added columns (not a rename target), in new order.
	var droppedCols, addedCols []string
	for _, c := range newT.Columns {
		if _, ok := oldCols[c.Name]; ok || renamedToCol[c.Name] {
			continue
		}
		addedCols = append(addedCols, c.Name)
		el, err := r.columnElement(c)
		if err != nil {
			return err
		}
		*alters = append(*alters, fmt.Sprintf("op.AddColumn(%q, %s)", name, el))
		// A column comment is inline only where the dialect supports it (MySQL);
		// emit a standalone comment so it is not lost on PostgreSQL, matching the
		// diff engine. The statement renders empty (a no-op) where it is inline.
		if c.Comment != "" {
			*comments = append(*comments, r.commentOnColumn(name, c))
		}
	}

	// Changed columns: same name, or a renamed pair.
	for _, nc := range newT.Columns {
		var oc meta.Column
		switch {
		case renamedToCol[nc.Name]:
			// handled below via the rename mapping
			continue
		default:
			var ok bool
			oc, ok = oldCols[nc.Name]
			if !ok {
				continue
			}
		}
		if err := r.columnChange(name, oc, nc, alters, comments); err != nil {
			return err
		}
	}
	// Renamed columns may also have changed type/nullability/default/comment.
	for _, cr := range colRenames {
		if err := r.columnChange(name, oldCols[cr.from], newCols[cr.to], alters, comments); err != nil {
			return err
		}
	}

	// Dropped columns (not a rename source).
	for _, c := range oldT.Columns {
		if _, ok := newCols[c.Name]; ok || renamedFromCol[c.Name] {
			continue
		}
		droppedCols = append(droppedCols, c.Name)
		*alters = append(*alters, fmt.Sprintf("op.DropColumn(%q, %q)", name, c.Name))
	}

	// Heuristic column-rename hint.
	if len(droppedCols) == 1 && len(addedCols) == 1 {
		if oc, ok := oldCols[droppedCols[0]]; ok {
			if nc, ok := newCols[addedCols[0]]; ok && r.sameType(oc, nc) {
				*alters = append([]string{fmt.Sprintf(
					"// possible rename: column %s.%s -> %s? pass --rename %s.%s:%s to keep data",
					name, droppedCols[0], addedCols[0], name, droppedCols[0], addedCols[0])}, *alters...)
			}
		}
	}

	// Primary-key membership change.
	if !stringsEqual(oldT.PrimaryKeyColumns(), newT.PrimaryKeyColumns()) {
		if len(oldT.PrimaryKeyColumns()) > 0 {
			*alters = append(*alters, fmt.Sprintf("op.DropPrimaryKey(%q)", name))
		}
		if pk := newT.PrimaryKeyColumns(); len(pk) > 0 {
			*alters = append(*alters, fmt.Sprintf("op.AddPrimaryKey(%q, %s)", name, quotedList(pk)))
		}
	}

	// Indexes by name.
	oldIdx := indexesByName(oldT)
	newIdx := indexesByName(newT)
	for _, ix := range sortedIndexNames(oldIdx) {
		o := oldIdx[ix]
		n, ok := newIdx[ix]
		if !ok || !indexEqual(o, n) {
			*dropIndexes = append(*dropIndexes, fmt.Sprintf("op.DropIndex(%q, %q)", o.Name, name))
		}
	}
	for _, ix := range sortedIndexNames(newIdx) {
		n := newIdx[ix]
		o, ok := oldIdx[ix]
		if !ok || !indexEqual(o, n) {
			call := "op.CreateIndex"
			if n.Unique {
				call = "op.CreateUniqueIndex"
			}
			*createIndexes = append(*createIndexes, fmt.Sprintf("%s(%q, %q, %s)", call, n.Name, name, quotedList(n.Columns)))
		}
	}

	// Foreign keys by name.
	oldFK := fksByName(oldT)
	newFK := fksByName(newT)
	for _, fkName := range sortedFKNames(oldFK) {
		o := oldFK[fkName]
		n, ok := newFK[fkName]
		if !ok || !fkEqual(o, n) {
			*dropFKs = append(*dropFKs, fmt.Sprintf("op.DropForeignKey(%q, %q)", o.Name, name))
		}
	}
	for _, fkName := range sortedFKNames(newFK) {
		n := newFK[fkName]
		o, ok := oldFK[fkName]
		if !ok || !fkEqual(o, n) {
			*addFKs = append(*addFKs, r.fkCreate(name, n))
		}
	}

	// Checks by name.
	oldChecks := checksByName(oldT)
	newChecks := checksByName(newT)
	for _, ckName := range sortedCheckNames(oldChecks) {
		o := oldChecks[ckName]
		n, ok := newChecks[ckName]
		eq, err := r.checkEqual(o, n)
		if err != nil {
			return err
		}
		if !ok || !eq {
			*alters = append(*alters, fmt.Sprintf("op.DropConstraint(%q, %q)", o.Name, name))
		}
	}
	for _, ckName := range sortedCheckNames(newChecks) {
		n := newChecks[ckName]
		o, ok := oldChecks[ckName]
		eq, err := r.checkEqual(o, n)
		if err != nil {
			return err
		}
		if !ok || !eq {
			text, err := r.exprText(n.Expression)
			if err != nil {
				return err
			}
			*alters = append(*alters, fmt.Sprintf("op.AddCheck(%q, %q, %q)", n.Name, name, text))
		}
	}

	// Table comment change.
	if oldT.Comment != newT.Comment {
		*comments = append(*comments, r.commentOnTable(newT))
	}
	return nil
}

// columnChange appends a ModifyColumn for a type/nullability/default change and
// a comment statement for a comment change.
func (r *renderer) columnChange(table string, oc, nc meta.Column, alters, comments *[]string) error {
	changed, err := r.columnPropsChanged(oc, nc)
	if err != nil {
		return err
	}
	if changed {
		el, err := r.columnElement(nc)
		if err != nil {
			return err
		}
		*alters = append(*alters, fmt.Sprintf("op.ModifyColumn(%q, %s)", table, el))
	}
	if oc.Comment != nc.Comment {
		*comments = append(*comments, r.commentOnColumn(table, nc))
	}
	return nil
}

// createTable renders an op.CreateTable call (columns, indexes, checks, table
// comment); foreign keys are emitted separately as op.CreateForeignKey.
func (r *renderer) createTable(t meta.Table) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "op.CreateTable(%q,\n", t.Name)
	r.usesDecl = true
	if t.Comment != "" {
		fmt.Fprintf(&b, "decl.TableComment(%q),\n", t.Comment)
	}
	for _, c := range t.Columns {
		el, err := r.columnElement(c)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s,\n", el)
	}
	for _, ix := range t.Indexes {
		call := "decl.Index"
		if ix.Unique {
			call = "decl.UniqueIndex"
		}
		fmt.Fprintf(&b, "%s(%s),\n", call, quotedList(ix.Columns))
	}
	for _, ck := range t.Checks {
		text, err := r.exprText(ck.Expression)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "decl.Check(sql.Raw(%q)),\n", text)
		r.usesSQL = true
	}
	b.WriteString(")")
	return b.String(), nil
}

// columnElement renders a decl.Column element with its intrinsic options.
func (r *renderer) columnElement(c meta.Column) (string, error) {
	if c.Type == nil {
		return "", fmt.Errorf("autogen: column %q has no type", c.Name)
	}
	r.usesDecl = true
	var b strings.Builder
	fmt.Fprintf(&b, "decl.Column(%q, %s", c.Name, codegen.DeclTypeExpr(c, r.dialect.Name()))
	if c.PrimaryKey {
		b.WriteString(", decl.PrimaryKey()")
	}
	if c.HasDefault {
		if c.DefaultExpr != nil {
			text, err := r.exprText(c.DefaultExpr)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&b, ", decl.Default(sql.Raw(%q))", text)
			r.usesSQL = true
		} else {
			b.WriteString(", decl.Default()")
		}
	}
	if c.Nullable {
		b.WriteString(", decl.Nullable()")
	}
	if c.Comment != "" {
		fmt.Fprintf(&b, ", decl.Comment(%q)", c.Comment)
	}
	b.WriteString(")")
	return b.String(), nil
}

// fkCreate renders an op.CreateForeignKey call with any referential actions. It
// flags the decl import only when an action constant (decl.*) is emitted —
// a plain foreign key references neither decl nor sql.
func (r *renderer) fkCreate(table string, fk meta.ForeignKey) string {
	s := fmt.Sprintf("op.CreateForeignKey(%q, %q, %s, %q, %s",
		fk.Name, table, quotedArgs(fk.Columns), fk.RefTable, quotedArgs(fk.RefColumns))
	if expr := codegen.ReferentialActionExpr(fk.OnDelete); expr != "" {
		s += ", migrate.OnDelete(" + expr + ")"
		r.usesDecl = true
	}
	if expr := codegen.ReferentialActionExpr(fk.OnUpdate); expr != "" {
		s += ", migrate.OnUpdate(" + expr + ")"
		r.usesDecl = true
	}
	return s + ")"
}

func (r *renderer) commentOnTable(t meta.Table) string {
	r.usesSQL = true
	return fmt.Sprintf("op.Execute(sql.CommentOnTable(%s, %q))", sqlTableExpr(t.Schema.Name, t.Name), t.Comment)
}

func (r *renderer) commentOnColumn(table string, c meta.Column) string {
	r.usesSQL = true
	return fmt.Sprintf("op.Execute(sql.CommentOnColumn(%s, %q, %q))", sqlTableExpr("", table), c.Name, c.Comment)
}

// columnPropsChanged reports whether a column's type, nullability, or default
// differs between oc and nc on the dialect.
func (r *renderer) columnPropsChanged(oc, nc meta.Column) (bool, error) {
	same, err := r.sameTypeErr(oc, nc)
	if err != nil {
		return false, err
	}
	if !same || oc.Nullable != nc.Nullable {
		return true, nil
	}
	od, err := r.defaultText(oc)
	if err != nil {
		return false, err
	}
	nd, err := r.defaultText(nc)
	if err != nil {
		return false, err
	}
	return od != nd, nil
}

func (r *renderer) sameType(a, b meta.Column) bool {
	same, _ := r.sameTypeErr(a, b)
	return same
}

func (r *renderer) sameTypeErr(a, b meta.Column) (bool, error) {
	if a.Type == nil || b.Type == nil {
		return a.Type == nil && b.Type == nil, nil
	}
	ea, oka := a.Type.Engine(r.dialect.Name())
	eb, okb := b.Type.Engine(r.dialect.Name())
	if !oka || !okb {
		return false, fmt.Errorf("autogen: column %q/%q has no type on dialect %s", a.Name, b.Name, r.dialect.Name())
	}
	return ea == eb, nil
}

func (r *renderer) defaultText(c meta.Column) (string, error) {
	if c.DefaultExpr == nil {
		return "", nil
	}
	return r.exprText(c.DefaultExpr)
}

func (r *renderer) exprText(e sql.Expression) (string, error) {
	if e == nil {
		return "", nil
	}
	return sql.CompileStatic(r.dialect, e)
}

func (r *renderer) checkEqual(a, b meta.Check) (bool, error) {
	if a.Expression == nil || b.Expression == nil {
		return a.Expression == nil && b.Expression == nil, nil
	}
	ta, err := r.exprText(a.Expression)
	if err != nil {
		return false, err
	}
	tb, err := r.exprText(b.Expression)
	if err != nil {
		return false, err
	}
	return ta == tb, nil
}

// --- small rendering and lookup helpers ---

func sqlTableExpr(schema, name string) string {
	if schema == "" {
		return fmt.Sprintf("sql.Table{Name: %q}", name)
	}
	return fmt.Sprintf("sql.Table{Schema: %q, Name: %q}", schema, name)
}

// quotedArgs renders a Go []string literal, for parameters typed []string
// (op.CreateForeignKey's columns/refColumns).
func quotedArgs(items []string) string {
	return "[]string{" + quotedList(items) + "}"
}

// quotedList renders a bare, comma-separated list of quoted strings, for
// variadic string parameters (op.CreateIndex, op.AddPrimaryKey, decl.Index).
func quotedList(items []string) string {
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = fmt.Sprintf("%q", item)
	}
	return strings.Join(quoted, ", ")
}

func sameColumnNames(a, b meta.Table) bool {
	if len(a.Columns) != len(b.Columns) {
		return false
	}
	set := map[string]bool{}
	for _, c := range a.Columns {
		set[c.Name] = true
	}
	for _, c := range b.Columns {
		if !set[c.Name] {
			return false
		}
	}
	return true
}

// key identifies a table by schema and name, so same-named tables in different
// schemas stay distinct (the key migrate.Diff uses).
func key(t meta.Table) string { return t.Schema.Name + "\x00" + t.Name }

func tablesByKey(md meta.Metadata) map[string]meta.Table {
	m := make(map[string]meta.Table, len(md.Tables))
	for _, t := range md.Tables {
		m[key(t)] = t
	}
	return m
}

func tablesByName(md meta.Metadata) map[string]meta.Table {
	m := make(map[string]meta.Table, len(md.Tables))
	for _, t := range md.Tables {
		m[t.Name] = t
	}
	return m
}

func columnsByName(t meta.Table) map[string]meta.Column {
	m := make(map[string]meta.Column, len(t.Columns))
	for _, c := range t.Columns {
		m[c.Name] = c
	}
	return m
}

func indexesByName(t meta.Table) map[string]meta.Index {
	m := make(map[string]meta.Index, len(t.Indexes))
	for _, ix := range t.Indexes {
		m[ix.Name] = ix
	}
	return m
}

func fksByName(t meta.Table) map[string]meta.ForeignKey {
	m := make(map[string]meta.ForeignKey, len(t.ForeignKeys))
	for _, fk := range t.ForeignKeys {
		m[fk.Name] = fk
	}
	return m
}

func checksByName(t meta.Table) map[string]meta.Check {
	m := make(map[string]meta.Check, len(t.Checks))
	for _, ck := range t.Checks {
		m[ck.Name] = ck
	}
	return m
}

func sortedIndexNames(m map[string]meta.Index) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedFKNames(m map[string]meta.ForeignKey) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedCheckNames(m map[string]meta.Check) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func indexEqual(a, b meta.Index) bool {
	return a.Unique == b.Unique && stringsEqual(a.Columns, b.Columns)
}

func fkEqual(a, b meta.ForeignKey) bool {
	return a.RefSchema == b.RefSchema && a.RefTable == b.RefTable &&
		stringsEqual(a.Columns, b.Columns) && stringsEqual(a.RefColumns, b.RefColumns) &&
		a.OnDelete == b.OnDelete && a.OnUpdate == b.OnUpdate
}

func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
