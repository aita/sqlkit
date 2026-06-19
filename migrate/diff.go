package migrate

import (
	"fmt"
	"sort"

	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

// Diff compares an old schema to a desired new schema and returns the ordered
// DDL statements that migrate old into new — the forward (Up) plan for a
// migration. It takes a dialect because column-type equality and rendering are
// dialect-resolved.
//
// It detects added/dropped tables; added/dropped columns; column type,
// nullability, and default changes; primary-key membership changes;
// added/dropped/changed indexes, foreign keys (including ON DELETE/UPDATE
// actions), and CHECK constraints; and table/column comment changes. Column
// modifications render for both PostgreSQL (separate ALTER COLUMN actions) and
// MySQL (a single MODIFY COLUMN). Statements are ordered for safety: drop
// foreign keys, drop indexes, and drop tables first; then create tables, alter
// columns, create indexes, add foreign keys, and set comments last.
//
// Out of scope: column and table renames are reported as drop+add (which loses
// data — author a hand-written migration, or use the autogenerator's rename
// hints). Diff produces only the forward plan; author the Down statements by
// hand (the autogenerator derives a best-effort Down from the reverse diff).
func Diff(old, next meta.Metadata, dialect sql.Dialect) ([]sql.Statement, error) {
	oldByKey := tableMap(old)
	newByKey := tableMap(next)

	var dropFKs, dropIndexes, dropTables, createTables, alters, createIndexes, addFKs, comments []sql.Statement

	// Removed tables: drop their foreign keys first, then the table.
	for _, t := range old.Tables {
		if _, ok := newByKey[key(t)]; ok {
			continue
		}
		for _, fk := range t.ForeignKeys {
			dropFKs = append(dropFKs, fk.DropStatement(t))
		}
		dropTables = append(dropTables, t.DropStatement())
	}

	// Added tables: create the table, its indexes, foreign keys, and comments.
	for _, t := range next.Tables {
		if _, ok := oldByKey[key(t)]; ok {
			continue
		}
		createTables = append(createTables, t.CreateStatement())
		for _, ix := range t.Indexes {
			createIndexes = append(createIndexes, ix.CreateStatement(t))
		}
		for _, fk := range t.ForeignKeys {
			addFKs = append(addFKs, fk.AddStatement(t))
		}
		comments = append(comments, t.CommentStatements()...)
	}

	// Retained tables: diff their columns, indexes, foreign keys, checks, comments.
	for _, newT := range next.Tables {
		oldT, ok := oldByKey[key(newT)]
		if !ok {
			continue
		}
		tableDiff, err := diffTable(oldT, newT, dialect)
		if err != nil {
			return nil, err
		}
		dropFKs = append(dropFKs, tableDiff.dropFKs...)
		dropIndexes = append(dropIndexes, tableDiff.dropIndexes...)
		alters = append(alters, tableDiff.alters...)
		createIndexes = append(createIndexes, tableDiff.createIndexes...)
		addFKs = append(addFKs, tableDiff.addFKs...)
		comments = append(comments, tableDiff.comments...)
	}

	objs := diffObjects(old, next)

	var out []sql.Statement
	out = append(out, dropFKs...)
	out = append(out, dropIndexes...)
	out = append(out, dropTables...)
	// Sequences and enums drop after the tables that used them, extensions last —
	// the reverse of the create order below, mirroring meta.DropSQL.
	out = append(out, objs.dropSequences...)
	out = append(out, objs.dropEnums...)
	out = append(out, objs.dropExtensions...)
	// Extensions, enums, and sequences create before the tables that use them,
	// mirroring meta.CreateSQL.
	out = append(out, objs.createExtensions...)
	out = append(out, objs.createEnums...)
	out = append(out, objs.createSequences...)
	out = append(out, createTables...)
	out = append(out, alters...)
	out = append(out, createIndexes...)
	out = append(out, addFKs...)
	out = append(out, comments...)
	return out, nil
}

type tableChanges struct {
	dropFKs       []sql.Statement
	dropIndexes   []sql.Statement
	alters        []sql.Statement
	createIndexes []sql.Statement
	addFKs        []sql.Statement
	comments      []sql.Statement
}

// diffTable computes the changes between two versions of the same table.
func diffTable(oldT, newT meta.Table, d sql.Dialect) (tableChanges, error) {
	var tc tableChanges

	// Foreign keys: by name. Changed = drop + add.
	oldFKs := fkMap(oldT)
	newFKs := fkMap(newT)
	for name, fk := range oldFKs {
		nf, ok := newFKs[name]
		if !ok || !fkEqual(fk, nf) {
			tc.dropFKs = append(tc.dropFKs, fk.DropStatement(oldT))
		}
	}
	for name, fk := range newFKs {
		of, ok := oldFKs[name]
		if !ok || !fkEqual(of, fk) {
			tc.addFKs = append(tc.addFKs, fk.AddStatement(newT))
		}
	}

	// Indexes: by name. Changed = drop + create.
	oldIdx := indexMap(oldT)
	newIdx := indexMap(newT)
	for name, ix := range oldIdx {
		ni, ok := newIdx[name]
		if !ok || !indexEqual(ix, ni) {
			tc.dropIndexes = append(tc.dropIndexes, ix.DropStatement(oldT))
		}
	}
	for name, ix := range newIdx {
		oi, ok := oldIdx[name]
		if !ok || !indexEqual(oi, ix) {
			tc.createIndexes = append(tc.createIndexes, ix.CreateStatement(newT))
		}
	}

	// Columns and checks fold into a single ALTER TABLE statement.
	alter := sql.AlterTable(newT.SQLTable())
	hasAlter := false

	oldCols := columnMap(oldT)
	newCols := columnMap(newT)

	// Added columns (in new order).
	for _, c := range newT.Columns {
		if _, ok := oldCols[c.Name]; ok {
			continue
		}
		alter = alter.AddColumn(c.ColumnDef())
		hasAlter = true
		if c.Comment != "" {
			tc.comments = append(tc.comments, sql.CommentOnColumn(newT.SQLTable(), c.Name, c.Comment))
		}
	}

	// Changed columns (type, nullability, default).
	for _, nc := range newT.Columns {
		oc, ok := oldCols[nc.Name]
		if !ok {
			continue
		}
		changed, err := columnAlterations(&alter, oc, nc, d)
		if err != nil {
			return tc, fmt.Errorf("meta: table %q column %q: %w", newT.Name, nc.Name, err)
		}
		if changed {
			hasAlter = true
		}
		if oc.Comment != nc.Comment {
			tc.comments = append(tc.comments, sql.CommentOnColumn(newT.SQLTable(), nc.Name, nc.Comment))
		}
	}

	// Dropped columns.
	for _, c := range oldT.Columns {
		if _, ok := newCols[c.Name]; !ok {
			alter = alter.DropColumn(c.Name)
			hasAlter = true
		}
	}

	// Primary-key membership change: drop the old key, then add the new one.
	oldPK, newPK := oldT.PrimaryKeyColumns(), newT.PrimaryKeyColumns()
	if !stringsEqual(oldPK, newPK) {
		if len(oldPK) > 0 {
			alter = alter.DropPrimaryKey()
		}
		if len(newPK) > 0 {
			alter = alter.AddPrimaryKey(newPK...)
		}
		hasAlter = true
	}

	// Checks: by name. A changed check (same name, new expression) becomes a
	// drop + add; the drop must be emitted first so PostgreSQL does not reject
	// the duplicate constraint name within the same ALTER TABLE. Names are
	// sorted for deterministic output.
	oldChecks := checkMap(oldT)
	newChecks := checkMap(newT)
	for _, name := range sortedCheckNames(oldChecks) {
		ck := oldChecks[name]
		nc, ok := newChecks[name]
		eq, err := checkEqual(ck, nc, d)
		if err != nil {
			return tc, err
		}
		if !ok || !eq { // dropped or changed
			alter = alter.DropConstraint(ck.Name)
			hasAlter = true
		}
	}
	for _, name := range sortedCheckNames(newChecks) {
		ck := newChecks[name]
		oc, ok := oldChecks[name]
		eq, err := checkEqual(oc, ck, d)
		if err != nil {
			return tc, err
		}
		if !ok || !eq { // added or changed
			alter = alter.AddCheck(ck.Name, ck.Expression)
			hasAlter = true
		}
	}

	if hasAlter {
		tc.alters = append(tc.alters, alter)
	}

	if oldT.Comment != newT.Comment {
		tc.comments = append(tc.comments, sql.CommentOnTable(newT.SQLTable(), newT.Comment))
	}
	return tc, nil
}

// columnAlterations appends the type/nullability/default changes for a retained
// column to alter, reporting whether any were added. PostgreSQL emits separate
// ALTER COLUMN actions (TYPE / SET|DROP NOT NULL / SET|DROP DEFAULT); MySQL,
// which has no per-property ALTER COLUMN, restates the whole column with a
// single MODIFY COLUMN.
func columnAlterations(alter *sql.AlterTableStmt, oc, nc meta.Column, d sql.Dialect) (bool, error) {
	typeChanged, err := engineEqual(oc, nc, d)
	if err != nil {
		return false, err
	}
	typeChanged = !typeChanged
	nullChanged := oc.Nullable != nc.Nullable
	oldDefault, err := renderDefault(d, oc)
	if err != nil {
		return false, err
	}
	newDefault, err := renderDefault(d, nc)
	if err != nil {
		return false, err
	}
	defaultChanged := oldDefault != newDefault
	if !typeChanged && !nullChanged && !defaultChanged {
		return false, nil
	}

	if d.Name() == "mysql" {
		// MySQL restates the column in one MODIFY COLUMN; nc.ColumnDef carries the
		// new type, nullability, and default.
		*alter = alter.RestateColumn(nc.ColumnDef())
		return true, nil
	}

	if typeChanged {
		// Use the column's own ColumnDef type, which renders array columns as
		// "elem[]"; it is byte-identical to a plain EngineType for scalar columns.
		*alter = alter.AlterColumnType(nc.Name, nc.ColumnDef().Type)
	}
	if nullChanged {
		if nc.Nullable {
			*alter = alter.DropNotNull(nc.Name)
		} else {
			*alter = alter.SetNotNull(nc.Name)
		}
	}
	if defaultChanged {
		if nc.DefaultExpr != nil {
			*alter = alter.SetDefault(nc.Name, nc.DefaultExpr)
		} else {
			*alter = alter.DropDefault(nc.Name)
		}
	}
	return true, nil
}

// renderDefault renders a column's default expression for comparison; "" when
// the column has no renderable default expression.
func renderDefault(d sql.Dialect, c meta.Column) (string, error) {
	if c.DefaultExpr == nil {
		return "", nil
	}
	return sql.CompileStatic(d, c.DefaultExpr)
}

func key(t meta.Table) string { return t.Schema.Name + "\x00" + t.Name }
func tableMap(md meta.Metadata) map[string]meta.Table {
	m := make(map[string]meta.Table, len(md.Tables))
	for _, t := range md.Tables {
		m[key(t)] = t
	}
	return m
}

func columnMap(t meta.Table) map[string]meta.Column {
	m := make(map[string]meta.Column, len(t.Columns))
	for _, c := range t.Columns {
		m[c.Name] = c
	}
	return m
}

func indexMap(t meta.Table) map[string]meta.Index {
	m := make(map[string]meta.Index, len(t.Indexes))
	for _, ix := range t.Indexes {
		m[ix.Name] = ix
	}
	return m
}

func fkMap(t meta.Table) map[string]meta.ForeignKey {
	m := make(map[string]meta.ForeignKey, len(t.ForeignKeys))
	for _, fk := range t.ForeignKeys {
		m[fk.Name] = fk
	}
	return m
}

func checkMap(t meta.Table) map[string]meta.Check {
	m := make(map[string]meta.Check, len(t.Checks))
	for _, ck := range t.Checks {
		m[ck.Name] = ck
	}
	return m
}

func sortedCheckNames(m map[string]meta.Check) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func engineEqual(a, b meta.Column, d sql.Dialect) (bool, error) {
	if a.Type == nil || b.Type == nil {
		return false, fmt.Errorf("meta: column %q has no type", b.Name)
	}
	ea, oka := a.Type.Engine(d.Name())
	eb, okb := b.Type.Engine(d.Name())
	if !oka || !okb {
		return false, fmt.Errorf("meta: column %q has no type on dialect %s", b.Name, d.Name())
	}
	return ea == eb, nil
}

func indexEqual(a, b meta.Index) bool {
	return a.Unique == b.Unique && stringsEqual(a.Columns, b.Columns)
}

func fkEqual(a, b meta.ForeignKey) bool {
	return a.RefSchema == b.RefSchema && a.RefTable == b.RefTable &&
		stringsEqual(a.Columns, b.Columns) && stringsEqual(a.RefColumns, b.RefColumns) &&
		a.OnDelete == b.OnDelete && a.OnUpdate == b.OnUpdate
}

func checkEqual(a, b meta.Check, d sql.Dialect) (bool, error) {
	if a.Expression == nil || b.Expression == nil {
		return a.Expression == nil && b.Expression == nil, nil
	}
	ra, err := sql.CompileStatic(d, a.Expression)
	if err != nil {
		return false, err
	}
	rb, err := sql.CompileStatic(d, b.Expression)
	if err != nil {
		return false, err
	}
	return ra == rb, nil
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
