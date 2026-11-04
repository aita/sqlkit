package migrate

import (
	"strings"

	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

// objectChanges holds the DDL for added, dropped, and changed non-table objects
// (extensions, enum types, sequences, materialized views, grants). A changed
// object is rendered as a drop plus a create, like the table path treats
// incompatible changes.
type objectChanges struct {
	createSchemas    []sql.Statement
	dropSchemas      []sql.Statement
	createExtensions []sql.Statement
	createEnums      []sql.Statement
	createSequences  []sql.Statement
	createMatviews   []sql.Statement
	createGrants     []sql.Statement
	dropExtensions   []sql.Statement
	dropEnums        []sql.Statement
	dropSequences    []sql.Statement
	dropMatviews     []sql.Statement
	dropGrants       []sql.Statement
	// alterEnums are ALTER TYPE ... ADD VALUE statements for enums whose labels
	// were only appended (old is a prefix of new). They run in the alter phase,
	// not the pre-table drop phase, so a column using the type is undisturbed.
	alterEnums []sql.Statement
}

// diffObjects compares the non-table objects of old and next and returns the
// create/drop statements that reconcile them. Objects are keyed by (qualified)
// name; one whose definition changed is dropped and recreated. The dialect
// renders materialized-view bodies for equality.
func diffObjects(old, next meta.Metadata, dialect sql.Dialect) (objectChanges, error) {
	var c objectChanges

	// Managed schemas (decl.Create()), keyed by name. Only a schema explicitly put
	// under migration management emits CREATE/DROP SCHEMA; a schema that merely
	// qualifies a table is assumed to already exist. Without this, a migration that
	// introduces an object-only managed schema emits CREATE TABLE "audit"."log"
	// with no preceding CREATE SCHEMA "audit", so the apply fails ("schema does not
	// exist"). Mirrors meta.CreateSQL/DropSQL, which filter Schemas() the same way.
	newSchema := map[string]bool{}
	for _, s := range next.Schemas() {
		if s.Create && s.Name != "" {
			newSchema[s.Name] = true
		}
	}
	oldSchema := map[string]bool{}
	for _, s := range old.Schemas() {
		if s.Create && s.Name != "" {
			oldSchema[s.Name] = true
		}
	}
	for _, s := range old.Schemas() {
		if s.Create && s.Name != "" && !newSchema[s.Name] {
			c.dropSchemas = append(c.dropSchemas, s.DropStatement())
		}
	}
	for _, s := range next.Schemas() {
		if s.Create && s.Name != "" && !oldSchema[s.Name] {
			c.createSchemas = append(c.createSchemas, s.CreateStatement())
		}
	}

	// Extensions, keyed by name (they are not schema-scoped objects).
	oldExt := map[string]meta.Extension{}
	for _, e := range old.Extensions {
		oldExt[e.Name] = e
	}
	newExt := map[string]meta.Extension{}
	for _, e := range next.Extensions {
		newExt[e.Name] = e
	}
	for _, e := range old.Extensions {
		if n, ok := newExt[e.Name]; !ok || n != e {
			c.dropExtensions = append(c.dropExtensions, e.DropStatement())
		}
	}
	for _, e := range next.Extensions {
		if o, ok := oldExt[e.Name]; !ok || o != e {
			c.createExtensions = append(c.createExtensions, e.CreateStatement())
		}
	}

	// Enums, keyed by qualified name. A pure append (the old labels are a prefix
	// of the new ones, same order) becomes one ALTER TYPE ... ADD VALUE per added
	// label, which keeps any column using the type usable. Any other change
	// (reordered, removed, or renamed labels) still drops and recreates the type.
	oldEnum := map[string]meta.Enum{}
	for _, e := range old.Enums {
		oldEnum[e.QualifiedName()] = e
	}
	newEnum := map[string]meta.Enum{}
	for _, e := range next.Enums {
		newEnum[e.QualifiedName()] = e
	}
	for _, e := range old.Enums {
		n, ok := newEnum[e.QualifiedName()]
		if !ok {
			c.dropEnums = append(c.dropEnums, e.DropStatement())
			continue
		}
		if e.Equal(n) {
			continue
		}
		if appended, ok := appendedEnumValues(e.Values, n.Values); ok {
			for _, v := range appended {
				c.alterEnums = append(c.alterEnums, sql.AlterTypeAddValue(e.QualifiedName(), v))
			}
			continue
		}
		c.dropEnums = append(c.dropEnums, e.DropStatement())
	}
	for _, e := range next.Enums {
		o, ok := oldEnum[e.QualifiedName()]
		if !ok {
			c.createEnums = append(c.createEnums, e.CreateStatement())
			continue
		}
		if o.Equal(e) {
			continue
		}
		// Recreate only when it is not a pure append (handled via ALTER above).
		if _, isAppend := appendedEnumValues(o.Values, e.Values); !isAppend {
			c.createEnums = append(c.createEnums, e.CreateStatement())
		}
	}

	// Sequences, keyed by qualified name.
	oldSeq := map[string]meta.Sequence{}
	for _, s := range old.Sequences {
		oldSeq[s.QualifiedName()] = s
	}
	newSeq := map[string]meta.Sequence{}
	for _, s := range next.Sequences {
		newSeq[s.QualifiedName()] = s
	}
	for _, s := range old.Sequences {
		if n, ok := newSeq[s.QualifiedName()]; !ok || !s.Equal(n) {
			c.dropSequences = append(c.dropSequences, s.DropStatement())
		}
	}
	for _, s := range next.Sequences {
		if o, ok := oldSeq[s.QualifiedName()]; !ok || !o.Equal(s) {
			c.createSequences = append(c.createSequences, s.CreateStatement())
		}
	}

	// Materialized views, keyed by qualified name; equality renders the body.
	oldMV := map[string]meta.MaterializedView{}
	for _, v := range old.MaterializedViews {
		oldMV[v.QualifiedName()] = v
	}
	newMV := map[string]meta.MaterializedView{}
	for _, v := range next.MaterializedViews {
		newMV[v.QualifiedName()] = v
	}
	for _, v := range old.MaterializedViews {
		eq, err := matviewEqual(v, newMV[v.QualifiedName()], dialect)
		if err != nil {
			return objectChanges{}, err
		}
		if _, ok := newMV[v.QualifiedName()]; !ok || !eq {
			c.dropMatviews = append(c.dropMatviews, v.DropStatement())
		}
	}
	for _, v := range next.MaterializedViews {
		o, ok := oldMV[v.QualifiedName()]
		eq, err := matviewEqual(o, v, dialect)
		if err != nil {
			return objectChanges{}, err
		}
		if !ok || !eq {
			c.createMatviews = append(c.createMatviews, v.CreateStatement())
		}
	}

	// Grants, keyed by their full definition: a grant is identified by the
	// (privileges, type, objects, grantees) tuple, so any change is an add+drop.
	oldGrant := map[string]meta.Grant{}
	for _, g := range old.Grants {
		oldGrant[grantKey(g)] = g
	}
	newGrant := map[string]meta.Grant{}
	for _, g := range next.Grants {
		newGrant[grantKey(g)] = g
	}
	for _, g := range old.Grants {
		if n, ok := newGrant[grantKey(g)]; !ok || !g.Equal(n) {
			c.dropGrants = append(c.dropGrants, g.DropStatement())
		}
	}
	for _, g := range next.Grants {
		if o, ok := oldGrant[grantKey(g)]; !ok || !o.Equal(g) {
			c.createGrants = append(c.createGrants, g.CreateStatement())
		}
	}
	return c, nil
}

// matviewEqual reports whether two materialized views are identical, rendering
// their bodies for the dialect to compare. A zero (missing) view never matches.
func matviewEqual(a, b meta.MaterializedView, dialect sql.Dialect) (bool, error) {
	if a.Query == nil || b.Query == nil {
		return false, nil
	}
	if a.Schema != b.Schema || a.Name != b.Name || a.WithNoData != b.WithNoData ||
		!stringsEqual(a.Columns, b.Columns) {
		return false, nil
	}
	at, _, err := a.Query.ToSQL(dialect)
	if err != nil {
		return false, err
	}
	bt, _, err := b.Query.ToSQL(dialect)
	if err != nil {
		return false, err
	}
	return at == bt, nil
}

// grantKey identifies a grant by its full (privileges, type, objects, grantees)
// tuple. Keying on the target alone (object type + objects) would collide two
// distinct grants on the same object for different grantees or privilege sets
// into one map slot, silently dropping one of them from the diff.
func grantKey(g meta.Grant) string {
	return strings.Join(g.Privileges, ",") + "\x00" + g.ObjectType + "\x00" +
		strings.Join(g.Objects, ",") + "\x00" + strings.Join(g.Grantees, ",")
}

// appendedEnumValues reports whether next is old with only labels appended (old
// is a prefix of next, in the same order), and if so returns the appended
// labels. A reordered, removed, or renamed label makes it report false, so the
// caller falls back to drop+create. Equal slices report (nil, true) but yield no
// appended labels.
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
