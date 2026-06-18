package postgres_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestGrantTable(t *testing.T) {
	assertRoundTrip(t,
		"GRANT SELECT, INSERT ON employees TO analyst, PUBLIC WITH GRANT OPTION",
		sql.Grant("SELECT", "INSERT").On("employees").To("analyst", "PUBLIC").WithGrantOption(),
		`GRANT SELECT, INSERT ON TABLE "employees" TO "analyst", PUBLIC WITH GRANT OPTION;`)
}

func TestGrantAllOnSchema(t *testing.T) {
	assertRoundTrip(t,
		"GRANT ALL PRIVILEGES ON SCHEMA public TO dev",
		sql.Grant().OnType("SCHEMA", "public").To("dev"),
		`GRANT ALL PRIVILEGES ON SCHEMA "public" TO "dev";`)
}

func TestRevokeCascade(t *testing.T) {
	assertRoundTrip(t,
		"REVOKE GRANT OPTION FOR UPDATE ON hr.employees FROM analyst CASCADE",
		sql.Revoke("UPDATE").GrantOptionFor().On("hr.employees").From("analyst").Cascade(),
		`REVOKE GRANT OPTION FOR UPDATE ON TABLE "hr"."employees" FROM "analyst" CASCADE;`)
}

// GRANT role membership (GrantRoleStmt) is not modeled, so it round-trips raw.
func TestGrantRoleFallsBackToRaw(t *testing.T) {
	assertRaw(t, "GRANT admins TO alice")
}

// Function DDL is not modeled as typed nodes, so it round-trips via the raw
// passthrough (the builder offers CreateFunction/DropFunction for construction).
func TestFunctionDDLFallsBackToRaw(t *testing.T) {
	assertRaw(t, "CREATE OR REPLACE FUNCTION add(a integer, b integer) "+
		"RETURNS integer LANGUAGE sql AS $$ SELECT a + b $$")
	assertRaw(t, "DROP FUNCTION IF EXISTS add(integer, integer) CASCADE")
}
