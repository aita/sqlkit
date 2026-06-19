package decl_test

import (
	"testing"

	"github.com/aita/sqlkit/decl"
	querysql "github.com/aita/sqlkit/sql"
)

func TestMaterializedViewAndGrantCreateSQL(t *testing.T) {
	catalog := decl.NewCatalog(decl.DefaultSchema("public"))
	catalog.Table("users",
		decl.Column("id", decl.BigInt, decl.PrimaryKey(), decl.Default()),
	)
	catalog.MaterializedView("active_users", querysql.Select(querysql.Raw("id")).From(querysql.Tbl("users")))
	catalog.Grant([]string{"SELECT"}, decl.GrantOn("active_users"), decl.GrantTo("readonly"))
	md := buildCatalog(t, catalog)

	got, err := md.CreateSQL(querysql.PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("CreateSQL() error = %v", err)
	}
	// View and grant come after the table.
	assertOrdered(t, got, []string{
		`CREATE TABLE "public"."users"`,
		`CREATE MATERIALIZED VIEW "public"."active_users" AS`,
		`GRANT SELECT ON TABLE "active_users" TO "readonly";`,
	})

	drop := md.DropSQL(querysql.PostgreSQLDialect{})
	// Grant and view drop before the table.
	assertOrdered(t, drop, []string{
		`REVOKE SELECT ON TABLE "active_users" FROM "readonly";`,
		`DROP MATERIALIZED VIEW IF EXISTS "public"."active_users";`,
		`DROP TABLE`,
	})
}

func TestGrantValidation(t *testing.T) {
	catalog := decl.NewCatalog(decl.DefaultSchema("public"))
	catalog.Grant([]string{"SELECT"}, decl.GrantOn("users")) // no grantee
	if _, err := catalog.Build(); err == nil {
		t.Fatal("expected Build to fail when a grant has no grantee")
	}
}
