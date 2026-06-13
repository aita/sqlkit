package sqlkit_test

import (
	"testing"

	"github.com/aita/sqlkit"
)

func TestColumnReferenceQualifiesByRealTable(t *testing.T) {
	info := sqlkit.TableInfo{Schema: "public", Name: "users"}
	id := sqlkit.NewColumn[int64](info, "id")
	if got, want := id.Reference(), "public.users.id"; got != want {
		t.Fatalf("Reference() = %q, want %q", got, want)
	}
}

func TestColumnReferenceQualifiesByAlias(t *testing.T) {
	info := sqlkit.TableInfo{Schema: "public", Name: "employees", Alias: "manager"}
	id := sqlkit.NewColumn[int64](info, "id")
	if got, want := id.Reference(), "manager.id"; got != want {
		t.Fatalf("Reference() = %q, want %q", got, want)
	}
}

func TestTableInfoSQLSourceAlias(t *testing.T) {
	base := sqlkit.TableInfo{Schema: "public", Name: "employees"}
	if src := base.SQLSource(); src.Alias != "" || src.Name != "public.employees" {
		t.Fatalf("base SQLSource() = %#v, want Name=public.employees Alias empty", src)
	}

	aliased := base.WithAlias("manager")
	src := aliased.SQLSource()
	if src.Name != "public.employees" || src.Alias != "manager" {
		t.Fatalf("aliased SQLSource() = %#v, want Name=public.employees Alias=manager", src)
	}
	// QualifiedName stays the real name so statement targets are unaffected.
	if got := aliased.QualifiedName(); got != "public.employees" {
		t.Fatalf("aliased QualifiedName() = %q, want public.employees", got)
	}
}

func TestAliasedTableSelectQualifiesColumnsAndSource(t *testing.T) {
	manager := Users.As("manager")
	query := sqlkit.Select(manager.ID, manager.Email).From(manager)
	assertToSQL(t, query,
		`SELECT "manager"."id", "manager"."email" FROM "public"."users" AS "manager"`,
		nil)
}
