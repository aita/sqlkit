package sql_test

import (
	"reflect"
	"testing"

	"github.com/sqlkit-dev/sqlkit/sqlkit/meta"
	. "github.com/sqlkit-dev/sqlkit/sqlkit/sql"
)

type usersTable struct {
	table meta.Table
	ID    meta.Column
	Email meta.Column
	OrgID meta.Column
}

func newUsersTable(schema string) usersTable {
	table := meta.DefineTable("users", meta.WithSchema(schema))
	return usersTable{
		table: table,
		ID:    table.Column("id", meta.PrimaryKey(), meta.Type("integer"), meta.WithGoType(int(1))),
		Email: table.Column("email", meta.Varchar(255), meta.WithGoType("string")),
		OrgID: table.Column("org_id", meta.Type("integer")),
	}
}

func (t usersTable) SQLSource() SourceExpression { return t.table.SQLSource() }

type orgsTable struct {
	table meta.Table
	ID    meta.Column
	Name  meta.Column
}

func newOrgsTable() orgsTable {
	table := meta.DefineTable("orgs")
	return orgsTable{table: table, ID: table.Column("id"), Name: table.Column("name", meta.Varchar(100))}
}

func (t orgsTable) SQLSource() SourceExpression { return t.table.SQLSource() }

func TestSelectQueryToSQL(t *testing.T) {
	users := newUsersTable("public")
	query := Select(users.ID, users.Email).
		From(users).
		Where(Eq(Col("id"), 42))

	sql, args, err := query.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if want := `SELECT "public"."users"."id", "public"."users"."email" FROM "public"."users" WHERE ("id" = $1)`; sql != want {
		t.Fatalf("ToSQL() SQL = %q, want %q", sql, want)
	}
	if want := []any{42}; !reflect.DeepEqual(args, want) {
		t.Fatalf("ToSQL() args = %#v, want %#v", args, want)
	}
}

func TestSelectQueryAcceptsMetaColumn(t *testing.T) {
	users := newUsersTable("")

	query := Select().
		From(users).
		Where(Eq(users.ID, Int(123)))

	sql, args, err := query.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if want := `SELECT * FROM "users" WHERE ("users"."id" = $1)`; sql != want {
		t.Fatalf("ToSQL() SQL = %q, want %q", sql, want)
	}
	if want := []any{123}; !reflect.DeepEqual(args, want) {
		t.Fatalf("ToSQL() args = %#v, want %#v", args, want)
	}
}

func TestSelectQueryAcceptsAggregateSelection(t *testing.T) {
	users := newUsersTable("")
	query := Select(Count(users.ID)).From(users)

	sql, args, err := query.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if want := `SELECT COUNT("users"."id") FROM "users"`; sql != want {
		t.Fatalf("ToSQL() SQL = %q, want %q", sql, want)
	}
	if len(args) != 0 {
		t.Fatalf("ToSQL() args = %#v, want none", args)
	}
}

func TestSelectQueryAcceptsJoin(t *testing.T) {
	users := newUsersTable("")
	orgs := newOrgsTable()

	query := Select(users.Email, orgs.Name).
		From(users).
		Join(orgs, Eq(users.OrgID, orgs.ID))

	sql, args, err := query.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if want := `SELECT "users"."email", "orgs"."name" FROM "users" JOIN "orgs" ON ("users"."org_id" = "orgs"."id")`; sql != want {
		t.Fatalf("ToSQL() SQL = %q, want %q", sql, want)
	}
	if len(args) != 0 {
		t.Fatalf("ToSQL() args = %#v, want none", args)
	}
}

func TestSelectQueryAcceptsSubqueryFrom(t *testing.T) {
	users := newUsersTable("")
	subquery := Select(users.ID).From(users).Where(Eq(users.ID, Int(7)))

	query := Select(Col("active_users.id")).From(Alias(subquery, "active_users"))

	sql, args, err := query.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if want := `SELECT "active_users"."id" FROM (SELECT "users"."id" FROM "users" WHERE ("users"."id" = $1)) AS "active_users"`; sql != want {
		t.Fatalf("ToSQL() SQL = %q, want %q", sql, want)
	}
	if want := []any{7}; !reflect.DeepEqual(args, want) {
		t.Fatalf("ToSQL() args = %#v, want %#v", args, want)
	}
}

func TestSelectQueryAcceptsSubqueryDirectly(t *testing.T) {
	users := newUsersTable("")
	subquery := Select(users.ID).From(users)

	query := Select(CountAll()).From(subquery)

	sql, args, err := query.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	if want := `SELECT COUNT(*) FROM (SELECT "users"."id" FROM "users")`; sql != want {
		t.Fatalf("ToSQL() SQL = %q, want %q", sql, want)
	}
	if len(args) != 0 {
		t.Fatalf("ToSQL() args = %#v, want none", args)
	}
}

func TestColumnOptions(t *testing.T) {
	users := newUsersTable("")
	if users.Email.DatabaseType != "varchar" || users.Email.Length != 255 || users.Email.GoType != "string" {
		t.Fatalf("email column options = %#v, want varchar(255) string", users.Email)
	}
	if !users.ID.PrimaryKey || users.ID.DatabaseType != "integer" || users.ID.GoType != "int" {
		t.Fatalf("id column options = %#v, want primary integer int", users.ID)
	}
}
