package autogen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/aita/sqlkit/decl"
)

func blogMeta(t *testing.T) (catalog *decl.Catalog) {
	c := decl.NewCatalog(decl.DefaultSchema("public"))
	c.Table("users",
		decl.TableComment("authors"),
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("email", decl.VarChar(255), decl.Unique(), decl.Comment("login")),
	)
	c.Table("posts",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("user_id", decl.BigInt, decl.References("users", "id", decl.OnDelete(decl.Cascade))),
		decl.Column("created_at", decl.Timestamp),
		decl.Index("user_id"),
	)
	return c
}

// TestSnapshotRoundTrip resolves a metadata to a snapshot, gob round-trips it,
// rebuilds it, and confirms a diff against the original is empty — so the
// snapshot preserves everything the diff compares (types, defaults, FK actions,
// indexes, comments).
func TestSnapshotRoundTrip(t *testing.T) {
	md, err := blogMeta(t).Build()
	if err != nil {
		t.Fatal(err)
	}
	d := mustDialect(t, "postgres")

	snap, err := Resolve(md, d)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	var buf bytes.Buffer
	if err := snap.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := DecodeSnapshot(&buf)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	// A render of the rebuilt metadata against itself must be empty.
	res, err := Render(decoded.Metadata(), decoded.Metadata(), Options{Dialect: d})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !res.Empty() {
		t.Errorf("round-tripped snapshot diffs against itself:\nUp=%s\nDown=%s", res.Up, res.Down)
	}

	// And the rebuilt metadata must equal the original under a diff.
	res, err = Render(md, decoded.Metadata(), Options{Dialect: d})
	if err != nil {
		t.Fatalf("Render vs original: %v", err)
	}
	if !res.Empty() {
		t.Errorf("rebuilt snapshot differs from original:\nUp=%s", res.Up)
	}
}

// TestSnapshotPreservesArrayColumn covers an array column (text[]) surviving the
// snapshot round-trip: SnapshotType dropped the Array flag, so the rebuilt
// metadata came back as a scalar and a diff spuriously reported (and would
// migrate) a text[] -> text change.
func TestSnapshotPreservesArrayColumn(t *testing.T) {
	c := decl.NewCatalog(decl.DefaultSchema("public"))
	c.Table("docs",
		decl.Column("id", decl.BigInt, decl.PrimaryKey()),
		decl.Column("tags", decl.Array(decl.Text)),
	)
	md, err := c.Build()
	if err != nil {
		t.Fatal(err)
	}
	d := mustDialect(t, "postgres")
	snap, err := Resolve(md, d)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	var buf bytes.Buffer
	if err := snap.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := DecodeSnapshot(&buf)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	res, err := Render(md, decoded.Metadata(), Options{Dialect: d})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !res.Empty() {
		t.Errorf("array column not preserved through snapshot:\nUp=%s\nDown=%s", res.Up, res.Down)
	}
}

func TestSnapshotPreservesFKActions(t *testing.T) {
	md, err := blogMeta(t).Build()
	if err != nil {
		t.Fatal(err)
	}
	snap, err := Resolve(md, mustDialect(t, "postgres"))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, tbl := range snap.Tables {
		for _, fk := range tbl.ForeignKeys {
			if fk.OnDelete == "CASCADE" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("snapshot lost the ON DELETE CASCADE action: %+v", snap.Tables)
	}
}

// TestSnapshotPreservesManagedSchema covers a schema put under migration
// management (decl.Create()): the flag must survive a Resolve/Metadata
// round-trip, or Render never sees the schema as managed and never emits its
// CreateSchema/DropSchema (the generated migration then fails to apply).
func TestSnapshotPreservesManagedSchema(t *testing.T) {
	c := decl.NewCatalog(decl.DefaultSchema("public"))
	c.Schema("audit", decl.Create()).Enum("kind", "a", "b")
	md, err := c.Build()
	if err != nil {
		t.Fatal(err)
	}
	snap, err := Resolve(md, mustDialect(t, "postgres"))
	if err != nil {
		t.Fatal(err)
	}
	rebuilt := snap.Metadata()
	if len(rebuilt.ManagedSchemas) != 1 || rebuilt.ManagedSchemas[0].Name != "audit" || !rebuilt.ManagedSchemas[0].Create {
		t.Errorf("managed schema flag lost through Resolve/Metadata round-trip: %+v", rebuilt.ManagedSchemas)
	}
}

// TestFKIntoManagedSchemaNoSpuriousChurn covers a foreign key referencing a table
// in a managed schema: fkEqual must not compare the whole RefSchema value (which
// includes the Create management flag) — a snapshot round-trip always resets
// Create to false (Snapshot only stores the schema name), so comparing the struct
// made an unchanged FK look different and emitted a spurious
// DropForeignKey+CreateForeignKey pair on every render.
func TestFKIntoManagedSchemaNoSpuriousChurn(t *testing.T) {
	c := decl.NewCatalog(decl.DefaultSchema("public"))
	audit := c.Schema("audit", decl.Create())
	audit.Table("owner", decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()))
	audit.Table("log",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
		decl.Column("owner_id", decl.BigInt, decl.References("audit.owner", "id")),
	)
	md, err := c.Build()
	if err != nil {
		t.Fatal(err)
	}
	snap, err := Resolve(md, mustDialect(t, "postgres"))
	if err != nil {
		t.Fatal(err)
	}
	roundtripped := snap.Metadata()

	res, err := Render(md, roundtripped, Options{Dialect: mustDialect(t, "postgres")})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Up, "DropForeignKey") || strings.Contains(res.Up, "CreateForeignKey") {
		t.Errorf("spurious FK churn for an unchanged FK into a managed schema:\n%s", res.Up)
	}
}
