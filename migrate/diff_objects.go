package migrate

import (
	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

// objectChanges holds the DDL for added, dropped, and changed non-table objects
// (extensions, enum types, sequences). A changed object is rendered as a drop
// plus a create, like the table path treats incompatible changes.
type objectChanges struct {
	createExtensions []sql.Statement
	createEnums      []sql.Statement
	createSequences  []sql.Statement
	dropExtensions   []sql.Statement
	dropEnums        []sql.Statement
	dropSequences    []sql.Statement
}

// diffObjects compares the extensions, enums, and sequences of old and next and
// returns the create/drop statements that reconcile them. Objects are keyed by
// (qualified) name; one whose definition changed is dropped and recreated.
func diffObjects(old, next meta.Metadata) objectChanges {
	var c objectChanges

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

	// Enums, keyed by qualified name; a label change recreates the type.
	oldEnum := map[string]meta.Enum{}
	for _, e := range old.Enums {
		oldEnum[e.QualifiedName()] = e
	}
	newEnum := map[string]meta.Enum{}
	for _, e := range next.Enums {
		newEnum[e.QualifiedName()] = e
	}
	for _, e := range old.Enums {
		if n, ok := newEnum[e.QualifiedName()]; !ok || !e.Equal(n) {
			c.dropEnums = append(c.dropEnums, e.DropStatement())
		}
	}
	for _, e := range next.Enums {
		if o, ok := oldEnum[e.QualifiedName()]; !ok || !o.Equal(e) {
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
	return c
}
