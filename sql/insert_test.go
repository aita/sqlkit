package sql

import (
	"strings"
	"testing"
)

func TestOnConflictExpressionCompile(t *testing.T) {
	q := InsertInto(Table{Name: NewIdent("users")}).Columns("email").Values("a@x").
		OnConflictExpr(Func("lower", Col("email"))).
		DoUpdate(Set("seen", Col("now()")))
	got, args, err := q.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	want := `INSERT INTO "users" ("email") VALUES ($1) ON CONFLICT ((lower("email"))) DO UPDATE SET "seen" = "now()"`
	if got != want {
		t.Fatalf("ToSQL() =\n  %q\nwant\n  %q", got, want)
	}
	if len(args) != 1 || args[0] != "a@x" {
		t.Fatalf("args = %#v, want [a@x]", args)
	}
}

// Each conflict-target setter replaces any earlier target, so the last one wins
// (the fluent API cannot express a mixed column/expression target).
func TestOnConflictTargetLastWins(t *testing.T) {
	q := InsertInto(Table{Name: NewIdent("t")}).Columns("a").Values(1).
		OnConflict("a").OnConflictExpr(Col("b")).DoNothing()
	got, _, err := q.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("ToSQL() error = %v", err)
	}
	want := `INSERT INTO "t" ("a") VALUES ($1) ON CONFLICT (("b")) DO NOTHING`
	if got != want {
		t.Fatalf("ToSQL() = %q, want %q", got, want)
	}
}

func TestUpdateFromAndDeleteUsing(t *testing.T) {
	upd := Update(Table{Name: NewIdent("users")}).
		Set(Set("name", Col("o.name"))).
		From(SourceAlias(Table{Name: NewIdent("orgs")}, "o")).
		Where(Eq(Col("users.org_id"), Col("o.id")))
	assertSQL(t, upd,
		`UPDATE "users" SET "name" = "o"."name" FROM "orgs" AS "o" WHERE ("users"."org_id" = "o"."id")`, nil)

	del := DeleteFrom(Table{Name: NewIdent("users")}).
		Using(SourceAlias(Table{Name: NewIdent("orgs")}, "o")).
		Where(And(Eq(Col("users.org_id"), Col("o.id")), Eq(Col("o.active"), Val(false))))
	assertSQL(t, del,
		`DELETE FROM "users" USING "orgs" AS "o" WHERE (("users"."org_id" = "o"."id") AND ("o"."active" = $1))`,
		[]any{false})
}

func TestInsertFromSelectSQL(t *testing.T) {
	source := Select(Col("email"), Col("name")).
		From(Table{Name: NewIdent("old_users")}).
		Where(Eq(Col("active"), Val(true)))

	query := InsertInto(users).Columns("email", "name").FromSelect(source)

	assertSQL(t, query,
		`INSERT INTO "public"."users" ("email", "name") SELECT "email", "name" FROM "old_users" WHERE ("active" = $1)`,
		[]any{true})
}

func TestInsertFromSelectWithoutColumnsSQL(t *testing.T) {
	query := InsertInto(users).FromSelect(Select(Col("id")).From(Table{Name: NewIdent("old_users")}))

	assertSQL(t, query,
		`INSERT INTO "public"."users" SELECT "id" FROM "old_users"`,
		nil)
}

func TestInsertFromSelectReturning(t *testing.T) {
	query := InsertInto(users).
		Columns("email").
		FromSelect(Select(Col("email")).From(Table{Name: NewIdent("old_users")})).
		Returning(Col("id"))

	assertSQL(t, query,
		`INSERT INTO "public"."users" ("email") SELECT "email" FROM "old_users" RETURNING "id"`,
		nil)
}

func TestInsertFromSelectSharesPlaceholderNumbering(t *testing.T) {
	source := Select(Col("email")).
		From(Table{Name: NewIdent("old_users")}).
		Where(And(Eq(Col("a"), Int(1)), Eq(Col("b"), Int(2))))

	query := InsertInto(users).Columns("email").FromSelect(source)

	assertSQL(t, query,
		`INSERT INTO "public"."users" ("email") SELECT "email" FROM "old_users" WHERE (("a" = $1) AND ("b" = $2))`,
		[]any{1, 2})
}

func TestInsertFromCompoundQuery(t *testing.T) {
	a := Select(Col("email")).From(Table{Name: NewIdent("a")})
	b := Select(Col("email")).From(Table{Name: NewIdent("b")})

	query := InsertInto(users).Columns("email").FromSelect(a.Union(b))

	assertSQL(t, query,
		`INSERT INTO "public"."users" ("email") (SELECT "email" FROM "a") UNION (SELECT "email" FROM "b")`,
		nil)
}

func TestInsertRejectsValuesAndSelectSource(t *testing.T) {
	query := InsertInto(users).
		Columns("email").
		Values("a@example.com").
		FromSelect(Select(Col("email")).From(Table{Name: NewIdent("old_users")}))

	if _, _, err := query.ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("INSERT with both VALUES and SELECT compiled, want error")
	}
}

func TestInsertRequiresRowsOrSelect(t *testing.T) {
	if _, _, err := InsertInto(users).Columns("email").ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("INSERT without VALUES or SELECT compiled, want error")
	}
	if _, _, err := InsertInto(Table{}).Values(1).ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("INSERT without table compiled, want error")
	}
}

func TestInsertOnConflictDoNothing(t *testing.T) {
	query := InsertInto(users).Columns("email").Values("a@example.com").
		OnConflict("email").DoNothing()
	assertSQL(t, query,
		`INSERT INTO "public"."users" ("email") VALUES ($1) ON CONFLICT ("email") DO NOTHING`,
		[]any{"a@example.com"})
}

func TestInsertOnConflictDoUpdate(t *testing.T) {
	query := InsertInto(users).Columns("email", "name").Values("a@example.com", "Alice").
		OnConflict("email").DoUpdate(Set("name", Excluded("name"))).
		Returning(Col("id"))
	assertSQL(t, query,
		`INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2) ON CONFLICT ("email") DO UPDATE SET "name" = excluded."name" RETURNING "id"`,
		[]any{"a@example.com", "Alice"})
}

func TestInsertOnConflictOnConstraint(t *testing.T) {
	doNothing := InsertInto(users).Columns("email").Values("a@x.com").
		OnConflictConstraint("users_email_key").DoNothing()
	assertSQL(t, doNothing,
		`INSERT INTO "public"."users" ("email") VALUES ($1) ON CONFLICT ON CONSTRAINT "users_email_key" DO NOTHING`,
		[]any{"a@x.com"})

	doUpdate := InsertInto(users).Columns("email", "name").Values("a@x.com", "Alice").
		OnConflictConstraint("users_email_key").DoUpdate(Set("name", Excluded("name")))
	assertSQL(t, doUpdate,
		`INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2) ON CONFLICT ON CONSTRAINT "users_email_key" DO UPDATE SET "name" = excluded."name"`,
		[]any{"a@x.com", "Alice"})
}

func TestInsertOnConflictOnConstraintRejectedOnMySQL(t *testing.T) {
	q := InsertInto(users).Columns("email", "name").Values("a@x.com", "Alice").
		OnConflictConstraint("users_email_key").DoUpdate(Set("name", Excluded("name")))
	if _, _, err := q.ToSQL(MySQLDialect{}); err == nil || !strings.Contains(err.Error(), "constraint") {
		t.Fatalf("ToSQL(mysql) error = %v, want constraint rejection", err)
	}
}

func TestInsertOnConflictDoUpdateWherePlaceholderOrder(t *testing.T) {
	query := InsertInto(users).Columns("email", "name").Values("a@example.com", "Alice").
		OnConflict("email").
		DoUpdate(Set("name", Excluded("name"))).
		Where(Ne(Col("users.name"), Val("locked")))
	assertSQL(t, query,
		`INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2) ON CONFLICT ("email") DO UPDATE SET "name" = excluded."name" WHERE ("users"."name" <> $3)`,
		[]any{"a@example.com", "Alice", "locked"})
}

func TestInsertOnConflictDoUpdateRequiresAssignment(t *testing.T) {
	query := InsertInto(users).Columns("email").Values("a@example.com").
		OnConflict("email").DoUpdate()
	if _, _, err := query.ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("DO UPDATE without assignments compiled, want error")
	}
}

func TestInsertOnConflictDoUpdateRequiresTargetOnPostgres(t *testing.T) {
	query := InsertInto(users).Columns("email", "name").Values("a@example.com", "Alice").
		OnConflict().DoUpdate(Set("name", Excluded("name")))
	if _, _, err := query.ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("DO UPDATE without a conflict target compiled on PostgreSQL, want error")
	}
}

func TestInsertOnConflictWhereIgnoredOnDoNothing(t *testing.T) {
	// A WHERE alongside DO NOTHING is dropped, and must not leave a dangling
	// bind value behind (which would desync the placeholder count).
	query := InsertInto(users).Columns("email").Values("a@example.com").
		OnConflict("email").DoNothing().Where(Ne(Col("users.name"), Val("locked")))
	assertSQL(t, query,
		`INSERT INTO "public"."users" ("email") VALUES ($1) ON CONFLICT ("email") DO NOTHING`,
		[]any{"a@example.com"})
}

func TestInsertQueryToSQL(t *testing.T) {
	query := InsertInto(users).
		Columns("email", "name").
		Values("a@example.com", "Alice").
		Values("b@example.com", "Bob").
		Returning(Col("id"))

	assertSQL(t, query,
		`INSERT INTO "public"."users" ("email", "name") VALUES ($1, $2), ($3, $4) RETURNING "id"`,
		[]any{"a@example.com", "Alice", "b@example.com", "Bob"})
}

func TestUpdateQueryToSQL(t *testing.T) {
	query := Update(users).
		Set(Set("email", "new@example.com"), Set("name", "New")).
		Where(Eq(Col("id"), Int(7)))

	assertSQL(t, query,
		`UPDATE "public"."users" SET "email" = $1, "name" = $2 WHERE ("id" = $3)`,
		[]any{"new@example.com", "New", 7})
}

func TestDeleteQueryToSQL(t *testing.T) {
	query := DeleteFrom(users).Where(Eq(Col("id"), Int(7)))

	assertSQL(t, query, `DELETE FROM "public"."users" WHERE ("id" = $1)`, []any{7})
}

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

func TestInsertStmtConflict(t *testing.T) {
	users := Table{Name: NewIdent("users")}
	c := InsertInto(users).Columns("id").Values(1).
		OnConflict("id").DoUpdate(Set("id", Val(2))).
		Stmt()

	if c.Table.Name.String() != "users" {
		t.Errorf("Table = %q, want users", c.Table.Name)
	}
	if c.Conflict == nil || !c.Conflict.DoUpdate || len(c.Conflict.Sets) != 1 {
		t.Errorf("Conflict = %+v, want a DO UPDATE with one assignment", c.Conflict)
	}
}

// TestInsertUpdateDeletePlumbing covers the Insert/Update/Delete accessor
// plumbing: ColumnCount, SplitRows, the second-Where AND-combine, Stmt /
// buildStmt / setPos, and the compile entry point.
func TestInsertUpdateDeletePlumbing(t *testing.T) {
	pg := PostgreSQLDialect{}
	tbl := Table{Name: NewIdent("t")}

	// Calling Where twice AND-combines the predicates.
	mustOK := func(q Query) {
		t.Helper()
		if _, _, err := q.ToSQL(pg); err != nil {
			t.Fatalf("%T: %v", q, err)
		}
	}
	mustOK(InsertInto(tbl).Columns("a").Values(1).OnConflict("a").
		DoUpdate(Set("a", Excluded("a"))).Where(Gt(Col("a"), 0)).Where(Lt(Col("a"), 9)))
	mustOK(Update(tbl).Set(Set("a", 1)).Where(Gt(Col("a"), 0)).Where(Lt(Col("a"), 9)))
	mustOK(DeleteFrom(tbl).Where(Gt(Col("a"), 0)).Where(Lt(Col("a"), 9)))

	ib := InsertInto(tbl).Columns("a", "b").Values(1, 2)
	if ib.ColumnCount() != 2 || ib.Stmt().ColumnCount() != 2 {
		t.Fatalf("ColumnCount = %d", ib.ColumnCount())
	}
	if got := InsertInto(tbl).Columns("a").Values(1).Values(2).Values(3).SplitRows(2); len(got) == 0 {
		t.Fatal("SplitRows returned nothing")
	}

	c := NewCompiler(pg)
	// Compile via the builder methods, which delegate to the statement form.
	_, _, _ = InsertInto(tbl).Columns("a").Values(1).compile(c)
	_, _, _ = Update(tbl).Set(Set("a", 1)).compile(c)
	_, _, _ = DeleteFrom(tbl).Where(Gt(Col("a"), 0)).compile(c)

	// ColumnCount falls back to the first row width when no columns are named,
	// and reports zero when neither columns nor rows are present.
	if got := InsertInto(tbl).Values(1, 2).ColumnCount(); got != 2 {
		t.Fatalf("ColumnCount from rows = %d, want 2", got)
	}
	if got := InsertInto(tbl).Stmt().ColumnCount(); got != 0 {
		t.Fatalf("ColumnCount empty = %d, want 0", got)
	}

	// SplitRows returns the builder unchanged for n <= 0 and when the row count
	// does not exceed n.
	if got := InsertInto(tbl).Columns("a").Values(1).Values(2).SplitRows(0); len(got) != 1 {
		t.Fatalf("SplitRows(0) = %d batches, want 1", len(got))
	}
	if got := InsertInto(tbl).Columns("a").Values(1).SplitRows(5); len(got) != 1 {
		t.Fatalf("SplitRows(5) over 1 row = %d batches, want 1", len(got))
	}

	_ = InsertInto(tbl).Columns("a").Values(1).buildStmt()
	_ = Update(tbl).Set(Set("a", 1)).buildStmt()
	_ = DeleteFrom(tbl).Where(Gt(Col("a"), 0)).buildStmt()
	for _, q := range []Query{
		InsertInto(tbl).Columns("a").Values(1), InsertInto(tbl).Columns("a").Values(1).Stmt(),
		Update(tbl).Set(Set("a", 1)), Update(tbl).Set(Set("a", 1)).Stmt(),
		DeleteFrom(tbl).Where(Gt(Col("a"), 0)), DeleteFrom(tbl).Where(Gt(Col("a"), 0)).Stmt(),
	} {
		if sp, ok := q.(interface{ setPos(int) Query }); ok {
			sp.setPos(2)
		}
	}
}

// rowsInserted sums the VALUES rows across the parts of a split insert, reading
// each part's bind-arg count divided by its column width.
func rowsInserted(parts []InsertBuilder) int {
	total := 0
	for _, part := range parts {
		w := part.ColumnCount()
		_, args, err := part.ToSQL(PostgreSQLDialect{})
		if err != nil {
			panic(err)
		}
		if w > 0 {
			total += len(args) / w
		}
	}
	return total
}

func bulkInsert(n int, value string) InsertBuilder {
	q := InsertInto(Table{Name: NewIdent("t")}).Columns("email")
	for i := 0; i < n; i++ {
		q = q.Values(value)
	}
	return q
}

func TestSplitRowsBySizeNoLimitsIsSingle(t *testing.T) {
	if parts := bulkInsert(5, "a@example.com").SplitRowsBySize(0, 0); len(parts) != 1 {
		t.Fatalf("parts = %d, want 1 (no limits)", len(parts))
	}
}

func TestSplitRowsBySizeRowCapMatchesSplitRows(t *testing.T) {
	q := bulkInsert(5, "x")
	bySize := q.SplitRowsBySize(2, 0)
	bySplit := q.SplitRows(2)
	if len(bySize) != len(bySplit) || len(bySize) != 3 {
		t.Fatalf("byte-aware = %d parts, SplitRows = %d, want 3 each", len(bySize), len(bySplit))
	}
	if got := rowsInserted(bySize); got != 5 {
		t.Fatalf("rows across parts = %d, want 5", got)
	}
}

func TestSplitRowsBySizeByteCapSplitsLargeRows(t *testing.T) {
	// Each row carries a ~100-byte string; a 256-byte budget keeps each part to a
	// single row here (base 64 + 2*(2+2+100)=272 > 256) despite no row cap.
	big := strings.Repeat("x", 100)
	parts := bulkInsert(5, big).SplitRowsBySize(0, 256)
	if len(parts) < 2 {
		t.Fatalf("parts = %d, want a multi-statement split for oversized rows", len(parts))
	}
	if got := rowsInserted(parts); got != 5 {
		t.Fatalf("rows across parts = %d, want 5 (disjoint chunks)", got)
	}
	for _, part := range parts {
		_, args, _ := part.ToSQL(PostgreSQLDialect{})
		if len(args) > 1 {
			t.Fatalf("part holds %d rows, want the byte budget to keep it small", len(args))
		}
	}
}

func TestSplitRowsBySizeOversizedRowGoesAlone(t *testing.T) {
	// A single row larger than the whole budget cannot be split further; it must
	// still appear, in a statement of its own.
	huge := strings.Repeat("x", 10_000)
	parts := bulkInsert(3, huge).SplitRowsBySize(0, 100)
	if len(parts) != 3 {
		t.Fatalf("parts = %d, want 3 (one oversized row each)", len(parts))
	}
	if got := rowsInserted(parts); got != 3 {
		t.Fatalf("rows across parts = %d, want 3", got)
	}
}

func TestSplitRowsBySizeSmallRowsFitOneStatement(t *testing.T) {
	if parts := bulkInsert(4, "a").SplitRowsBySize(0, 1<<20); len(parts) != 1 {
		t.Fatalf("parts = %d, want 1 (rows fit the budget)", len(parts))
	}
}
