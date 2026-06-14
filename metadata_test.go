package sqlkit_test

import (
	"testing"

	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/internal/sqltest"
	"github.com/aita/sqlkit/meta"
)

// usersCatalog builds a representative metadata table that the WithMetadata
// tests compile into an identity map.
func usersCatalog() meta.Table {
	public := meta.Schema{Name: "public"}
	return meta.Table{
		Schema: public,
		Name:   "users",
		Columns: []meta.Column{
			{Name: "id", Type: meta.Of("postgres", "bigint", 0, 0, 0), PrimaryKey: true},
			{Name: "email", Type: meta.Of("postgres", "varchar", 255, 0, 0)},
		},
	}
}

func TestWithMetadataExposesIdentities(t *testing.T) {
	db := sqlkit.WrapDB((&sqltest.Fake{}).DB(),
		sqlkit.WithMetadata(meta.Metadata{Tables: []meta.Table{usersCatalog()}}))
	im := db.Identities()
	if im == nil {
		t.Fatal("Identities() = nil, want the compiled identity map")
	}
	if _, ok := im.KeyForName("public", "users"); !ok {
		t.Fatal("identity map does not contain public.users")
	}
	// A session derived from the database inherits the same identity map.
	if got := db.NewSession().Identities(); got != im {
		t.Fatalf("session Identities() = %p, want inherited %p", got, im)
	}
}

func TestWithoutMetadataIdentitiesNil(t *testing.T) {
	db := sqlkit.WrapDB((&sqltest.Fake{}).DB())
	if db.Identities() != nil {
		t.Fatal("Identities() should be nil when no metadata is bound")
	}
}

// WrapDB cannot return an error, so a metadata conflict leaves the database
// without identities; Open is the constructor that surfaces the failure.
func TestWithMetadataConflictLeavesIdentitiesNil(t *testing.T) {
	drifted := usersCatalog()
	drifted.Columns[1].Type = meta.Of("postgres", "text", 0, 0, 0)
	db := sqlkit.WrapDB((&sqltest.Fake{}).DB(),
		sqlkit.WithMetadata(meta.Metadata{Tables: []meta.Table{usersCatalog(), drifted}}))
	if db.Identities() != nil {
		t.Fatal("Identities() should be nil when the bound metadata conflicts")
	}
}
