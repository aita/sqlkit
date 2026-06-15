package autogen

import (
	"bytes"
	"testing"

	"github.com/aita/sqlkit/sqldef"
)

func blogMeta(t *testing.T) (catalog *sqldef.Catalog) {
	c := sqldef.NewCatalog(sqldef.DefaultSchema("public"))
	c.Table("users",
		sqldef.TableComment("authors"),
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("email", sqldef.VarChar(255), sqldef.Unique(), sqldef.Comment("login")),
	)
	c.Table("posts",
		sqldef.Column("id", sqldef.BigInt, sqldef.PrimaryKey(), sqldef.Default()),
		sqldef.Column("user_id", sqldef.BigInt, sqldef.References("users", "id", sqldef.OnDelete(sqldef.Cascade))),
		sqldef.Column("created_at", sqldef.Timestamp),
		sqldef.Index("user_id"),
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
