package sqlkit

// Cardinality classifies a relation's target multiplicity: whether a source row
// relates to at most one target row (ToOne) or to many (ToMany). Preload uses it
// to decide whether a relation field is a single value or a slice.
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
// SelectQuery.JoinRel can build the JOIN, and Preload the batched fetch, from
// the relation alone.
type Relation interface {
	// Target is the related table, used as the JOIN/SELECT source.
	Target() Table
	// JoinOn is the ON predicate equating the foreign-key column pairs, e.g.
	// profiles.user_id = users.id.
	JoinOn() Predicate
	// Cardinality reports whether the relation is to-one or to-many.
	Cardinality() Cardinality
	// Name is the relation's generated field name (for diagnostics and as the
	// default Preload destination field).
	Name() string
	// Keys returns the join column pairs by bare SQL name, in order: for each i,
	// the relation matches target.target[i] = base.base[i]. Preload uses them to
	// batch-load the target with target.target[i] IN (base key values) and to
	// group the results back onto the base rows.
	Keys() (target, base []string)
}

// RelationSource is implemented by table bindings that expose their generated
// relations, so Preload(targetTable) can resolve which relation reaches the
// target. Generated bindings implement it when emitted with relations.
type RelationSource interface {
	// Relations lists the table's foreign-key relations.
	Relations() []Relation
}
