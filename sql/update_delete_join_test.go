package sql_test

import (
	"testing"

	. "github.com/aita/sqlkit/sql"
)

// joinOn correlates users.org_id with orgs.id.
func joinOn() Expression {
	return Eq(Col("public.users.org_id"), Col("orgs.id"))
}

func TestUpdateFromPostgres(t *testing.T) {
	query := Update(users).
		Set(Set("name", Val("x"))).
		From(orgs).
		Where(joinOn())
	assertSQL(t, query,
		`UPDATE "public"."users" SET "name" = $1 FROM "orgs" WHERE ("public"."users"."org_id" = "orgs"."id")`,
		[]any{"x"})
}

func TestUpdateJoinPostgresFoldsOnIntoWhere(t *testing.T) {
	query := Update(users).
		Set(Set("name", Val("x"))).
		Join(orgs, joinOn()).
		Where(Eq(Col("orgs.active"), Val(true)))
	assertSQL(t, query,
		`UPDATE "public"."users" SET "name" = $1 FROM "orgs" WHERE (("orgs"."active" = $2) AND ("public"."users"."org_id" = "orgs"."id"))`,
		[]any{"x", true})
}

func TestUpdateJoinMySQLInline(t *testing.T) {
	query := Update(users).
		Set(Set("name", Val("x"))).
		Join(orgs, joinOn()).
		Where(Eq(Col("orgs.active"), Val(true)))
	assertMySQL(t, query,
		"UPDATE `public`.`users` JOIN `orgs` ON (`public`.`users`.`org_id` = `orgs`.`id`) SET `name` = ? WHERE (`orgs`.`active` = ?)",
		[]any{"x", true})
}

func TestDeleteUsingPostgres(t *testing.T) {
	query := DeleteFrom(users).
		Using(orgs).
		Where(joinOn())
	assertSQL(t, query,
		`DELETE FROM "public"."users" USING "orgs" WHERE ("public"."users"."org_id" = "orgs"."id")`,
		nil)
}

func TestDeleteJoinPostgresFoldsOnIntoWhere(t *testing.T) {
	query := DeleteFrom(users).
		Join(orgs, joinOn()).
		Where(Eq(Col("orgs.active"), Val(false)))
	assertSQL(t, query,
		`DELETE FROM "public"."users" USING "orgs" WHERE (("orgs"."active" = $1) AND ("public"."users"."org_id" = "orgs"."id"))`,
		[]any{false})
}

func TestDeleteJoinMySQLInline(t *testing.T) {
	query := DeleteFrom(users).
		Join(orgs, joinOn()).
		Where(Eq(Col("orgs.active"), Val(false)))
	assertMySQL(t, query,
		"DELETE `public`.`users` FROM `public`.`users` JOIN `orgs` ON (`public`.`users`.`org_id` = `orgs`.`id`) WHERE (`orgs`.`active` = ?)",
		[]any{false})
}

// A plain single-table UPDATE/DELETE is unchanged by the join support.
func TestUpdateDeleteWithoutJoinUnchanged(t *testing.T) {
	assertSQL(t, Update(users).Set(Set("name", Val("x"))).Where(Eq(Col("id"), Val(1))),
		`UPDATE "public"."users" SET "name" = $1 WHERE ("id" = $2)`,
		[]any{"x", 1})
	assertSQL(t, DeleteFrom(users).Where(Eq(Col("id"), Val(1))),
		`DELETE FROM "public"."users" WHERE ("id" = $1)`,
		[]any{1})
}

// PostgreSQL's FROM/USING form folds join ON into WHERE, which is only correct
// for inner joins; a LEFT JOIN must be rejected rather than silently collapsed
// to an inner join. MySQL's inline form renders it natively, so it is accepted.
func TestUpdateLeftJoinRejectedOnPostgres(t *testing.T) {
	query := Update(users).
		Set(Set("name", Val("x"))).
		LeftJoin(orgs, joinOn())
	if _, _, err := query.ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("ToSQL(postgres) error = nil, want LEFT JOIN rejection")
	}
	assertMySQL(t, query,
		"UPDATE `public`.`users` LEFT JOIN `orgs` ON (`public`.`users`.`org_id` = `orgs`.`id`) SET `name` = ?",
		[]any{"x"})
}

func TestDeleteLeftJoinRejectedOnPostgres(t *testing.T) {
	query := DeleteFrom(users).LeftJoin(orgs, joinOn())
	if _, _, err := query.ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("ToSQL(postgres) error = nil, want LEFT JOIN rejection")
	}
	assertMySQL(t, query,
		"DELETE `public`.`users` FROM `public`.`users` LEFT JOIN `orgs` ON (`public`.`users`.`org_id` = `orgs`.`id`)",
		nil)
}
