// Package orm is a thin, relation-aware query layer over the sqlkit SQL DSL and
// the generated table bindings. It is the Query side of CQRS: a QuerySet builds
// SELECTs (including joins driven by generated relation metadata) and returns
// sqlkit.Rows, which the caller scans into a DTO of their choosing. DTO mapping
// is intentionally not the QuerySet's responsibility.
//
// The generated bindings expose relation metadata (appdb.Users.Rels.Profile)
// and QuerySet entry points (appdb.Users.Queryer(db) / appdb.Users.Q(db)); the
// runtime types they build on live here.
package orm

import "github.com/aita/sqlkit"

// Cardinality classifies a relation's target multiplicity. It is informational
// in this phase (carried for future eager-loading/preload work); joins generate
// the same SQL regardless.
type Cardinality int

const (
	// ToOne is a belongs-to or has-one relation (the target yields at most one
	// row per source row).
	ToOne Cardinality = iota
	// ToMany is a has-many relation.
	ToMany
)

// String renders the cardinality for diagnostics.
func (c Cardinality) String() string {
	switch c {
	case ToOne:
		return "ToOne"
	case ToMany:
		return "ToMany"
	default:
		return "Cardinality(?)"
	}
}

// Relation is generated join metadata: a typed path from a base table to a
// related table, derived from a foreign key. Generated relation structs embed
// the target columns binding — so callers reach the target's typed columns
// directly (appdb.Users.Rels.Profile.Public) — and implement this interface so
// QuerySet.Join can build the JOIN from the relation alone.
type Relation interface {
	// Target is the related table, used as the JOIN source.
	Target() sqlkit.Table
	// JoinOn is the ON predicate equating the foreign-key column pairs, e.g.
	// profiles.user_id = users.id.
	JoinOn() sqlkit.Predicate
	// Cardinality reports whether the relation is to-one or to-many.
	Cardinality() Cardinality
	// Name is the relation's generated field name (for diagnostics).
	Name() string
}
