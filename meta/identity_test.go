package meta_test

import (
	"errors"
	"testing"

	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

// sampleTable builds a representative table with columns, a primary key, an
// index, a foreign key, and a check, so the checksum exercises every section.
func sampleTable() meta.Table {
	public := meta.Schema{Name: "public"}
	return meta.Table{
		Schema: public,
		Name:   "users",
		Columns: []meta.Column{
			{Name: "id", Type: meta.Of("postgres", "bigint", 0, 0, 0), PrimaryKey: true, HasDefault: true},
			{Name: "email", Type: meta.Of("postgres", "varchar", 255, 0, 0)},
			{Name: "created_at", Type: meta.Of("postgres", "timestamp", 0, 0, 0), HasDefault: true, DefaultExpr: sql.Raw("now()")},
		},
		Indexes: []meta.Index{
			{Name: "uq_users_email", Columns: []string{"email"}, Unique: true},
		},
		ForeignKeys: []meta.ForeignKey{
			{Name: "fk_users_org", Columns: []string{"org_id"}, RefSchema: public, RefTable: "orgs", RefColumns: []string{"id"}},
		},
		Checks: []meta.Check{
			{Name: "ck_users_email", Expression: sql.Raw("email <> ''")},
		},
	}
}

func TestKeyDeterministic(t *testing.T) {
	tbl := sampleTable()
	first := tbl.Key()
	second := tbl.Key()
	if first != second {
		t.Fatalf("Key not deterministic: %v vs %v", first, second)
	}
	if first.Schema != "public" || first.Name != "users" {
		t.Fatalf("unexpected name fields: %+v", first)
	}
	if first.Checksum == "" {
		t.Fatal("checksum is empty")
	}
}

func TestKeyEqualAcrossIndependentTables(t *testing.T) {
	// Two tables built independently with the same structure must hash equal —
	// this is the cross-package reconciliation guarantee.
	if a, b := sampleTable().Key(), sampleTable().Key(); a != b {
		t.Fatalf("identical structures produced different keys: %v vs %v", a, b)
	}
}

func TestKeyDiffersOnStructuralChange(t *testing.T) {
	base := sampleTable().Key()

	cases := map[string]func(*meta.Table){
		"reordered columns": func(tbl *meta.Table) {
			tbl.Columns[0], tbl.Columns[1] = tbl.Columns[1], tbl.Columns[0]
		},
		"renamed column": func(tbl *meta.Table) {
			tbl.Columns[1].Name = "mail"
		},
		"different type": func(tbl *meta.Table) {
			tbl.Columns[1].Type = meta.Of("postgres", "text", 0, 0, 0)
		},
		"different type length": func(tbl *meta.Table) {
			tbl.Columns[1].Type = meta.Of("postgres", "varchar", 128, 0, 0)
		},
		"scalar vs array of same element": func(tbl *meta.Table) {
			// text and text[] are distinct columns; flipping only the array flag,
			// with the same element name and modifiers, must change the checksum.
			tbl.Columns[1].Type = meta.OfArray("postgres", "varchar", 255, 0, 0)
		},
		"nullability": func(tbl *meta.Table) {
			tbl.Columns[1].Nullable = true
		},
		"primary key set": func(tbl *meta.Table) {
			tbl.Columns[1].PrimaryKey = true
		},
		"default flag": func(tbl *meta.Table) {
			tbl.Columns[1].HasDefault = true
		},
		"default expression": func(tbl *meta.Table) {
			tbl.Columns[2].DefaultExpr = sql.Raw("CURRENT_TIMESTAMP")
		},
		"index uniqueness": func(tbl *meta.Table) {
			tbl.Indexes[0].Unique = false
		},
		"foreign key target": func(tbl *meta.Table) {
			tbl.ForeignKeys[0].RefTable = "tenants"
		},
		"foreign key on delete": func(tbl *meta.Table) {
			tbl.ForeignKeys[0].OnDelete = meta.Cascade
		},
		"foreign key on update": func(tbl *meta.Table) {
			tbl.ForeignKeys[0].OnUpdate = meta.Restrict
		},
		"check expression": func(tbl *meta.Table) {
			tbl.Checks[0].Expression = sql.Raw("email <> 'x'")
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			tbl := sampleTable()
			mutate(&tbl)
			if got := tbl.Key(); got.Checksum == base.Checksum {
				t.Fatalf("%s did not change the checksum (%s)", name, got.Checksum)
			}
		})
	}
}

func TestKeyStringFormat(t *testing.T) {
	k := meta.TableKey{Schema: "public", Name: "users", Checksum: "0123456789abcdef"}
	if got, want := k.String(), "public.users#01234567"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	k.Schema = ""
	if got, want := k.String(), "users#01234567"; got != want {
		t.Fatalf("unqualified String() = %q, want %q", got, want)
	}
}

func TestIdentities(t *testing.T) {
	md := meta.Metadata{Tables: []meta.Table{sampleTable()}}
	im, err := md.Identities()
	if err != nil {
		t.Fatalf("Identities() error = %v", err)
	}
	if im.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", im.Len())
	}
	key := sampleTable().Key()
	if _, ok := im.Lookup(key); !ok {
		t.Fatalf("Lookup(%v) missing", key)
	}
	if got, ok := im.KeyForName("public", "users"); !ok || got != key {
		t.Fatalf("KeyForName = %v, %v; want %v", got, ok, key)
	}
}

func TestIdentitiesFoldsSameIdentity(t *testing.T) {
	// The same table repeated (as independent packages declaring it would) folds
	// to a single entry rather than conflicting.
	md := meta.Metadata{Tables: []meta.Table{sampleTable(), sampleTable()}}
	im, err := md.Identities()
	if err != nil {
		t.Fatalf("Identities() error = %v", err)
	}
	if im.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", im.Len())
	}
}

func TestIdentitiesConflict(t *testing.T) {
	drifted := sampleTable()
	drifted.Columns[1].Type = meta.Of("postgres", "text", 0, 0, 0)
	md := meta.Metadata{Tables: []meta.Table{sampleTable(), drifted}}
	if _, err := md.Identities(); !errors.Is(err, meta.ErrTableConflict) {
		t.Fatalf("Identities() error = %v, want ErrTableConflict", err)
	}
}
