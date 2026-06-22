package meta_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/meta"
	"github.com/aita/sqlkit/sql"
)

func TestPrimaryKeyColumns(t *testing.T) {
	// Composite primary key returns the PK columns in column order, skipping
	// non-PK columns in between.
	tbl := meta.Table{
		Name: "memberships",
		Columns: []meta.Column{
			{Name: "org_id", PrimaryKey: true},
			{Name: "note"},
			{Name: "user_id", PrimaryKey: true},
		},
	}
	got := tbl.PrimaryKeyColumns()
	if want := []string{"org_id", "user_id"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("PrimaryKeyColumns() = %v, want %v", got, want)
	}

	// A table with no primary key returns nil.
	none := meta.Table{Name: "logs", Columns: []meta.Column{{Name: "msg"}}}
	if pk := none.PrimaryKeyColumns(); pk != nil {
		t.Errorf("PrimaryKeyColumns() = %v, want nil", pk)
	}
}

func TestGoTypeString(t *testing.T) {
	if got, want := (meta.GoType{Name: "int64"}).String(), "int64"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if got, want := (meta.GoType{Name: "time.Time", Pointer: true}).String(), "*time.Time"; got != want {
		t.Errorf("pointer String() = %q, want %q", got, want)
	}
}

// TestOfEngineDialect verifies a single-dialect Type resolves only for its
// dialect and reports the array flag from OfArray.
func TestOfEngineDialect(t *testing.T) {
	pgInt := meta.Of("postgres", "int8", 0, 0, 0)
	if eng, ok := pgInt.Engine("postgres"); !ok || eng.Name != "int8" {
		t.Errorf("Engine(postgres) = %+v, %v; want int8, true", eng, ok)
	}
	if _, ok := pgInt.Engine("mysql"); ok {
		t.Error("postgres-only type should not resolve on mysql")
	}

	arr := meta.OfArray("postgres", "text", 0, 0, 0)
	eng, ok := arr.Engine("postgres")
	if !ok || !eng.Array {
		t.Errorf("OfArray Engine(postgres) = %+v, %v; want Array=true", eng, ok)
	}
}

// TestObjectsRenderedAroundTables exercises the Metadata.CreateSQL/DropSQL
// object loops (extensions, enums, sequences, materialized views, grants) and
// pins their ordering relative to the tables.
func TestObjectsRenderedAroundTables(t *testing.T) {
	pg := mustDialect(t, "postgres")
	intType := meta.Of("postgres", "int8", 0, 0, 0)
	md := meta.Metadata{
		Tables: []meta.Table{
			{Name: "t", Columns: []meta.Column{{Name: "id", Type: intType, PrimaryKey: true}}},
		},
		Extensions: []meta.Extension{{Name: "uuid-ossp"}},
		Enums:      []meta.Enum{{Name: "mood", Values: []string{"happy", "sad"}}},
		Sequences:  []meta.Sequence{{Name: "seq"}},
		MaterializedViews: []meta.MaterializedView{
			{Name: "v", Query: sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")})},
		},
		Grants: []meta.Grant{{Privileges: []string{"SELECT"}, Objects: []string{"t"}, Grantees: []string{"r"}}},
	}

	create, err := md.CreateSQL(pg)
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	// Extensions, enums and sequences precede the table; views and grants follow.
	idxExt := strings.Index(create, "CREATE EXTENSION")
	idxEnum := strings.Index(create, "CREATE TYPE")
	idxSeq := strings.Index(create, "CREATE SEQUENCE")
	idxTable := strings.Index(create, "CREATE TABLE")
	idxView := strings.Index(create, "CREATE MATERIALIZED VIEW")
	idxGrant := strings.Index(create, "GRANT")
	for _, s := range []string{"CREATE EXTENSION", "CREATE TYPE", "CREATE SEQUENCE", "CREATE TABLE", "CREATE MATERIALIZED VIEW", "GRANT"} {
		if !strings.Contains(create, s) {
			t.Fatalf("CreateSQL missing %q:\n%s", s, create)
		}
	}
	if !(idxExt < idxTable && idxEnum < idxTable && idxSeq < idxTable) {
		t.Errorf("extensions/enums/sequences must precede tables:\n%s", create)
	}
	if !(idxTable < idxView && idxView < idxGrant) {
		t.Errorf("views and grants must follow tables:\n%s", create)
	}

	drop := md.DropSQL(pg)
	// Reverse: grants and views before the table; sequences, enums, extensions after.
	dGrant := strings.Index(drop, "REVOKE")
	dView := strings.Index(drop, "DROP MATERIALIZED VIEW")
	dTable := strings.Index(drop, "DROP TABLE")
	dSeq := strings.Index(drop, "DROP SEQUENCE")
	dEnum := strings.Index(drop, "DROP TYPE")
	dExt := strings.Index(drop, "DROP EXTENSION")
	for _, s := range []string{"REVOKE", "DROP MATERIALIZED VIEW", "DROP TABLE", "DROP SEQUENCE", "DROP TYPE", "DROP EXTENSION"} {
		if !strings.Contains(drop, s) {
			t.Fatalf("DropSQL missing %q:\n%s", s, drop)
		}
	}
	if !(dGrant < dTable && dView < dTable) {
		t.Errorf("grants and views must be dropped before the table:\n%s", drop)
	}
	if !(dTable < dSeq && dSeq < dEnum && dEnum < dExt) {
		t.Errorf("sequences/enums/extensions must be dropped after the table:\n%s", drop)
	}
}

// TestCreateSQLObjectError surfaces an object that fails to compile (a
// materialized view whose body binds a value) as a CreateSQL error rather than
// a panic.
func TestCreateSQLObjectError(t *testing.T) {
	pg := mustDialect(t, "postgres")
	md := meta.Metadata{
		MaterializedViews: []meta.MaterializedView{
			{Name: "v", Query: sql.Select(sql.Col("id")).From(sql.Table{Name: sql.NewIdent("t")}).Where(sql.Eq(sql.Col("a"), 1))},
		},
	}
	if _, err := md.CreateSQL(pg); err == nil {
		t.Error("CreateSQL with a value-binding view body should error")
	}
}
