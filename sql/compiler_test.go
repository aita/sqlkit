package sql

import (
	"strings"
	"testing"
)

// This file tests the SQL compiler (the CompileXxx functions in compiler.go and
// admin.go): given a built statement, it asserts the exact SQL the compiler
// emits, and the errors it raises for constructs a dialect cannot express.
//
// Portable statements are checked under BOTH PostgreSQL and MySQL so the
// compiler's per-dialect rendering (identifier quoting, $N vs ?, multi-table
// UPDATE/DELETE shapes, ...) is explicit; dialect-specific features assert the
// supported dialect's output and that the other dialect is rejected.

// --- CompileStatic ----------------------------------------------------------

func TestCompileStatic(t *testing.T) {
	// A bind-free expression compiles to text.
	got, err := CompileStatic(PostgreSQLDialect{}, Raw("1 + 1"))
	if err != nil || got != "1 + 1" {
		t.Fatalf("CompileStatic(raw) = %q, %v, want %q, nil", got, err, "1 + 1")
	}

	// A nil dialect defaults to PostgreSQL.
	got, err = CompileStatic(nil, Raw("now()"))
	if err != nil || got != "now()" {
		t.Fatalf("CompileStatic(nil dialect) = %q, %v", got, err)
	}

	// A nil expression errors.
	if _, err := CompileStatic(PostgreSQLDialect{}, nil); err == nil ||
		!strings.Contains(err.Error(), "nil expression") {
		t.Fatalf("CompileStatic(nil) error = %v, want nil-expression error", err)
	}

	// An expression that binds a value is rejected (static contexts forbid binds).
	if _, err := CompileStatic(PostgreSQLDialect{}, Val(5)); err == nil ||
		!strings.Contains(err.Error(), "must not bind values") {
		t.Fatalf("CompileStatic(value) error = %v, want bind rejection", err)
	}

	// An invalid inner expression propagates its error.
	if _, err := CompileStatic(PostgreSQLDialect{}, UnaryExpression{Operand: Col("x")}); err == nil {
		t.Fatalf("CompileStatic(invalid unary) error = nil, want operator error")
	}
}

// --- CompileSelection -------------------------------------------------------

func TestCompileSelectionForms(t *testing.T) {
	// Qualified star, both dialects.
	assertSQL(t, Select(StarOf("u")).From(SourceAlias(users, "u")),
		`SELECT "u".* FROM "public"."users" AS "u"`, nil)
	assertMySQL(t, Select(StarOf("u")).From(SourceAlias(users, "u")),
		"SELECT `u`.* FROM `public`.`users` AS `u`", nil)

	// Raw select item passes through verbatim, in both dialects.
	assertSQL(t, Select(Raw("COUNT(*) AS n")).From(orgs),
		`SELECT COUNT(*) AS n FROM "orgs"`, nil)
	assertMySQL(t, Select(Raw("COUNT(*) AS n")).From(orgs),
		"SELECT COUNT(*) AS n FROM `orgs`", nil)

	// Aliased expression selection, in both dialects.
	assertSQL(t, Select(As(Add(Col("a"), Col("b")), "sum")).From(orgs),
		`SELECT ("a" + "b") AS "sum" FROM "orgs"`, nil)
	assertMySQL(t, Select(As(Add(Col("a"), Col("b")), "sum")).From(orgs),
		"SELECT (`a` + `b`) AS `sum` FROM `orgs`", nil)
}

// --- writeOrderBy / order modifiers -----------------------------------------

func TestSelectOrderByForms(t *testing.T) {
	// PostgreSQL: ASC (implicit), DESC, NULLS FIRST/LAST across multiple keys.
	pg := Select(Col("id")).From(orgs).
		OrderBy(Asc(Col("a")), Desc(Col("b")).NullsLast(), Asc(Col("c")).NullsFirst())
	assertSQL(t, pg,
		`SELECT "id" FROM "orgs" ORDER BY "a", "b" DESC NULLS LAST, "c" NULLS FIRST`, nil)

	// MySQL: ASC/DESC only, multiple keys.
	my := Select(Col("id")).From(orgs).OrderBy(Asc(Col("a")), Desc(Col("b")))
	assertMySQL(t, my, "SELECT `id` FROM `orgs` ORDER BY `a`, `b` DESC", nil)

	// MySQL does not support NULLS ordering: the compiler rejects the modifier
	// rather than emit SQL MySQL cannot parse (it has no NULLS FIRST/LAST).
	nulls := Select(Col("id")).From(orgs).OrderBy(Desc(Col("b")).NullsLast())
	if _, _, err := nulls.ToSQL(MySQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "does not support NULLS ordering") {
		t.Fatalf("ToSQL(mysql NULLS) error = %v, want NULLS-ordering rejection", err)
	}
	// Standard SQL does support it.
	assertStandard(t, nulls, `SELECT "id" FROM "orgs" ORDER BY "b" DESC NULLS LAST`, nil)
}

// writeOrderBy is reached from UPDATE/DELETE row limits on MySQL, with multiple
// keys exercising the i>0 separator branch.
func TestUpdateDeleteRowLimitOrderBy(t *testing.T) {
	upd := Update(Table{Name: NewIdent("t")}).Set(Set("a", 1)).
		OrderBy(Asc(Col("x")), Desc(Col("y"))).Limit(2)
	assertMySQL(t, upd, "UPDATE `t` SET `a` = ? ORDER BY `x`, `y` DESC LIMIT 2", []any{1})

	// DELETE with an ORDER BY but no LIMIT (hasLimit false branch).
	del := DeleteFrom(Table{Name: NewIdent("t")}).OrderBy(Asc(Col("x")), Asc(Col("y")))
	assertMySQL(t, del, "DELETE FROM `t` ORDER BY `x`, `y`", nil)

	// PostgreSQL rejects ORDER BY / LIMIT on a DELETE.
	if _, _, err := del.ToSQL(PostgreSQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "ORDER BY / LIMIT") {
		t.Fatalf("ToSQL(postgres) error = %v, want ORDER BY/LIMIT rejection", err)
	}
}

// --- compileWindowParts -----------------------------------------------------

func TestWindowParts(t *testing.T) {
	// A bare reference to a named window: "fn OVER w".
	bare := Select(Over(Func("rank")).Named("w")).From(orgs).
		Window(WindowDef("w").PartitionBy(Col("team")))
	assertSQL(t, bare,
		`SELECT rank() OVER "w" FROM "orgs" WINDOW "w" AS (PARTITION BY "team")`, nil)
	assertMySQL(t, bare,
		"SELECT rank() OVER `w` FROM `orgs` WINDOW `w` AS (PARTITION BY `team`)", nil)

	// Extending a named window with further ORDER BY clauses: "fn OVER (w ...)".
	ext := Select(Over(Func("rank")).Named("w").OrderBy(Asc(Col("score")))).From(orgs).
		Window(WindowDef("w").PartitionBy(Col("team")))
	assertMySQL(t, ext,
		"SELECT rank() OVER (`w` ORDER BY `score`) FROM `orgs` WINDOW `w` AS (PARTITION BY `team`)", nil)

	// PARTITION BY + ORDER BY + frame, all parts joined.
	full := Select(Over(Func("sum", Col("x"))).
		PartitionBy(Col("a"), Col("b")).
		OrderBy(Asc(Col("c")), Desc(Col("d"))).
		Frame("ROWS BETWEEN 1 PRECEDING AND CURRENT ROW")).From(orgs)
	assertSQL(t, full,
		`SELECT sum("x") OVER (PARTITION BY "a", "b" ORDER BY "c", "d" DESC ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) FROM "orgs"`, nil)
	assertMySQL(t, full,
		"SELECT sum(`x`) OVER (PARTITION BY `a`, `b` ORDER BY `c`, `d` DESC ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) FROM `orgs`", nil)
}

// --- compileSubscript -------------------------------------------------------

func TestSubscriptForms(t *testing.T) {
	// Chained subscripts a[1][2].
	chained := SubscriptExpression{Operand: Col("m"), Indices: []SubscriptIndex{
		{Upper: Val(1)}, {Upper: Val(2)},
	}}
	assertSQL(t, Select(chained).From(orgs), `SELECT "m"[$1][$2] FROM "orgs"`, []any{1, 2})

	// Slice with both bounds, lower-only, and upper-only.
	assertSQL(t, Select(Slice(Col("t"), Val(1), Val(3))).From(orgs),
		`SELECT "t"[$1:$2] FROM "orgs"`, []any{1, 3})
	assertSQL(t, Select(Slice(Col("t"), Val(2), nil)).From(orgs),
		`SELECT "t"[$1:] FROM "orgs"`, []any{2})

	// A non-slice index with no value errors.
	bad := SubscriptExpression{Operand: Col("t"), Indices: []SubscriptIndex{{}}}
	if _, _, err := NewCompiler(PostgreSQLDialect{}).CompileExpression(bad); err == nil ||
		!strings.Contains(err.Error(), "requires an index") {
		t.Fatalf("CompileExpression(empty subscript) error = %v, want index error", err)
	}
}

// --- writeUpsert ------------------------------------------------------------

func TestUpsertMySQLOnDuplicateKey(t *testing.T) {
	q := InsertInto(Table{Name: NewIdent("users")}).Columns("email", "name").
		Values("a@x", "Al").
		OnConflict().DoUpdate(Set("name", Excluded("name")))
	assertMySQL(t, q,
		"INSERT INTO `users` (`email`, `name`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `name` = VALUES(`name`)",
		[]any{"a@x", "Al"})

	// MySQL has no DO NOTHING form.
	dn := InsertInto(Table{Name: NewIdent("u")}).Columns("a").Values(1).OnConflict().DoNothing()
	if _, _, err := dn.ToSQL(MySQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "DO NOTHING") {
		t.Fatalf("ToSQL(mysql DO NOTHING) error = %v, want rejection", err)
	}

	// MySQL ON DUPLICATE KEY UPDATE rejects a WHERE clause.
	dw := InsertInto(Table{Name: NewIdent("u")}).Columns("a").Values(1).
		OnConflict().DoUpdate(Set("a", Excluded("a"))).Where(Gt(Col("a"), 0))
	if _, _, err := dw.ToSQL(MySQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "WHERE") {
		t.Fatalf("ToSQL(mysql upsert WHERE) error = %v, want WHERE rejection", err)
	}
}

func TestUpsertExprTargetWithWhere(t *testing.T) {
	q := InsertInto(users).Columns("email").Values("a").
		OnConflictExpr(Func("lower", Col("email"))).
		DoUpdate(Set("n", Excluded("n"))).
		Where(Gt(Col("x"), 1))
	assertSQL(t, q,
		`INSERT INTO "public"."users" ("email") VALUES ($1) ON CONFLICT ((lower("email"))) DO UPDATE SET "n" = excluded."n" WHERE ("x" > $2)`,
		[]any{"a", 1})
}

// A conflict clause that names more than one target kind is rejected. The fluent
// builder cannot express it, so construct the AST directly.
func TestUpsertMultipleTargetsRejected(t *testing.T) {
	q := InsertStmt{
		Table:   Table{Name: NewIdent("u")},
		Columns: idents([]string{"a"}),
		Rows:    [][]Expression{{Val(1)}},
		Conflict: &ConflictClause{
			Target:           idents([]string{"a"}),
			TargetConstraint: NewIdent("c"),
			DoUpdate:         true,
			Sets:             []Assignment{Set("a", Val(2))},
		},
	}
	if _, _, err := q.ToSQL(PostgreSQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "can target only") {
		t.Fatalf("ToSQL(multi-target) error = %v, want multi-target rejection", err)
	}
}

// A dialect without upsert support rejects an ON CONFLICT clause.
func TestUpsertUnsupportedDialect(t *testing.T) {
	q := InsertInto(Table{Name: NewIdent("u")}).Columns("a").Values(1).
		OnConflict("a").DoUpdate(Set("a", Val(2)))
	if _, _, err := q.ToSQL(StandardSQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "does not support upsert") {
		t.Fatalf("ToSQL(standard upsert) error = %v, want unsupported rejection", err)
	}
}

// --- writeReturning ---------------------------------------------------------

func TestReturningMultipleAndExpr(t *testing.T) {
	// Multiple columns plus an aliased expression (i>0 separator branch).
	q := InsertInto(users).Columns("email").Values("a").
		Returning(Col("id"), As(Col("name"), "n"), Star())
	assertSQL(t, q,
		`INSERT INTO "public"."users" ("email") VALUES ($1) RETURNING "id", "name" AS "n", *`,
		[]any{"a"})

	// MySQL rejects RETURNING on INSERT/UPDATE/DELETE.
	for _, q := range []Query{
		InsertInto(Table{Name: NewIdent("t")}).Columns("a").Values(1).Returning(Col("id")),
		Update(Table{Name: NewIdent("t")}).Set(Set("a", 1)).Returning(Col("id")),
		DeleteFrom(Table{Name: NewIdent("t")}).Returning(Col("id")),
	} {
		if _, _, err := q.ToSQL(MySQLDialect{}); err == nil ||
			!strings.Contains(err.Error(), "does not support RETURNING") {
			t.Fatalf("ToSQL(mysql RETURNING) error = %v, want RETURNING rejection", err)
		}
	}
}

// --- CompileUpdate ----------------------------------------------------------

func TestUpdateFromMultipleJoinsFoldPostgres(t *testing.T) {
	// Two folded ON predicates plus an existing WHERE all AND together, and the
	// join sources flatten into a comma FROM list (writeCommaSources i>0 branch).
	q := Update(Table{Name: NewIdent("a")}).Set(Set("x", 1)).
		Join(Table{Name: NewIdent("b")}, Eq(Col("a.id"), Col("b.aid"))).
		Join(Table{Name: NewIdent("c")}, Eq(Col("a.id"), Col("c.aid"))).
		Where(Eq(Col("a.live"), Val(true)))
	assertSQL(t, q,
		`UPDATE "a" SET "x" = $1 FROM "b", "c" WHERE ((("a"."live" = $2) AND ("a"."id" = "b"."aid")) AND ("a"."id" = "c"."aid"))`,
		[]any{1, true})
}

func TestUpdateWithCTEAndReturning(t *testing.T) {
	cte := Select(Col("id")).From(Table{Name: NewIdent("orgs")}).CTE("recent")
	q := Update(users).WithCTE(cte).Set(Set("name", Val("x"))).
		Where(Eq(Col("id"), 1)).Returning(Star())
	assertSQL(t, q,
		`WITH "recent" AS (SELECT "id" FROM "orgs") UPDATE "public"."users" SET "name" = $1 WHERE ("id" = $2) RETURNING *`,
		[]any{"x", 1})
}

func TestUpdateRequiresTableAndAssignment(t *testing.T) {
	if _, _, err := Update(Table{}).Set(Set("a", 1)).ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("UPDATE without table compiled, want error")
	}
	if _, _, err := Update(Table{Name: NewIdent("t")}).ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("UPDATE without assignments compiled, want error")
	}
}

func TestUpdateMySQLMultiTableComma(t *testing.T) {
	// A From source on MySQL renders as an inline comma source after the target.
	q := Update(users).Set(Set("name", Col("o.name"))).
		From(SourceAlias(orgs, "o")).
		Where(Eq(Col("users.org_id"), Col("o.id")))
	assertMySQL(t, q,
		"UPDATE `public`.`users`, `orgs` AS `o` SET `name` = `o`.`name` WHERE (`users`.`org_id` = `o`.`id`)",
		nil)
}

// --- CompileDelete ----------------------------------------------------------

func TestDeleteWithCTEUsingReturningPostgres(t *testing.T) {
	cte := Select(Col("id")).From(Table{Name: NewIdent("orgs")}).CTE("recent")
	q := DeleteFrom(users).WithCTE(cte).Using(orgs).Where(joinOn()).
		Returning(Col("id"), As(Col("name"), "n"))
	assertSQL(t, q,
		`WITH "recent" AS (SELECT "id" FROM "orgs") DELETE FROM "public"."users" USING "orgs" WHERE ("public"."users"."org_id" = "orgs"."id") RETURNING "id", "name" AS "n"`,
		nil)
}

func TestDeleteMySQLInlineUsingAndJoin(t *testing.T) {
	// MySQL renders the comma USING source and the JOIN inline after DELETE t FROM t.
	q := DeleteFrom(users).
		Using(orgs).
		Join(Table{Name: NewIdent("teams")}, Eq(Col("a.x"), Col("teams.y"))).
		Where(Eq(Col("z"), 1))
	assertMySQL(t, q,
		"DELETE `public`.`users` FROM `public`.`users`, `orgs` JOIN `teams` ON (`a`.`x` = `teams`.`y`) WHERE (`z` = ?)",
		[]any{1})
}

func TestDeleteRequiresTable(t *testing.T) {
	if _, _, err := DeleteFrom(Table{}).ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("DELETE without table compiled, want error")
	}
}

// --- compileValuesSource ----------------------------------------------------

func TestValuesSourceMySQLSingleColumn(t *testing.T) {
	// A single-column, single-row VALUES source renders as one SELECT on MySQL.
	q := Update(users).Set(Set("name", Col("v.c0"))).
		From(ValuesSource([][]Expression{{Val(1)}}, "v", "k")).
		Where(Eq(Col("public.users.id"), Col("v.k")))
	assertMySQL(t, q,
		"UPDATE `public`.`users`, (SELECT ? AS `k`) AS `v` SET `name` = `v`.`c0` WHERE (`public`.`users`.`id` = `v`.`k`)",
		[]any{1})
}

func TestValuesSourceEmptyRejected(t *testing.T) {
	q := Update(users).Set(Set("name", Col("v.c0"))).From(ValuesSource(nil, "v", "k"))
	if _, _, err := q.ToSQL(PostgreSQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "non-empty row") {
		t.Fatalf("ToSQL(empty VALUES source) error = %v, want non-empty rejection", err)
	}
}

func TestValuesSourceRaggedRowRejected(t *testing.T) {
	rows := [][]Expression{{Val(1), Val("a")}, {Val(2)}}
	q := Update(users).Set(Set("name", Col("v.c0"))).From(ValuesSource(rows, "v", "k", "c0"))
	if _, _, err := q.ToSQL(PostgreSQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "want 2") {
		t.Fatalf("ToSQL(ragged VALUES source) error = %v, want ragged rejection", err)
	}
}

// MySQL's UNION form needs a column name per value; a bare alias is rejected.
func TestValuesSourceMySQLNeedsColumnNames(t *testing.T) {
	rows := [][]Expression{{Val(1), Val("a")}}
	q := Update(users).Set(Set("name", Col("v.c0"))).From(ValuesSource(rows, "v"))
	if _, _, err := q.ToSQL(MySQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "column name per value") {
		t.Fatalf("ToSQL(mysql VALUES source without columns) error = %v, want column rejection", err)
	}
}

// --- DistinctOn -------------------------------------------------------------

func TestDistinctOnDialectGating(t *testing.T) {
	q := Select(Col("id")).From(orgs).DistinctOn(Col("a"), Col("b")).OrderBy(Asc(Col("a")))
	assertSQL(t, q,
		`SELECT DISTINCT ON ("a", "b") "id" FROM "orgs" ORDER BY "a"`, nil)

	if _, _, err := q.ToSQL(MySQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "DISTINCT ON") {
		t.Fatalf("ToSQL(mysql DISTINCT ON) error = %v, want rejection", err)
	}
}

// --- admin.go: setPos and compile error branches ----------------------------

func TestAdminSetPos(t *testing.T) {
	cases := []Query{
		Use("db"),
		LockTables(TableLock{Table: Table{Name: NewIdent("t")}, Mode: LockRead}),
		UnlockTables(),
		SetVariables(SetVar{Name: "x", Value: Raw("1")}),
	}
	for _, q := range cases {
		positioned := SetPos(q, 5)
		p, ok := positioned.(interface{ Pos() int })
		if !ok {
			t.Fatalf("%T does not expose Pos()", positioned)
		}
		if p.Pos() != 4 {
			t.Fatalf("%T Pos() = %d, want 4", positioned, p.Pos())
		}
	}
}

func TestAdminCompileErrors(t *testing.T) {
	mysql := MySQLDialect{}
	checks := []struct {
		name string
		q    Query
		want string
	}{
		{"use empty", Use(""), "USE requires a database name"},
		{"lock none", LockTables(), "at least one table"},
		{"lock bad mode", LockTables(TableLock{Table: Table{Name: NewIdent("t")}, Mode: "FOO"}), "READ or WRITE"},
		{"set none", SetVariables(), "at least one assignment"},
		{"set no name", SetVariables(SetVar{Value: Raw("1")}), "requires a variable name"},
		{"set no value", SetVariables(SetVar{Name: "x"}), "requires a value"},
		{"set non-static value", SetVariables(SetVar{Name: "x", Value: Val(5)}), "must not bind values"},
	}
	for _, c := range checks {
		if _, _, err := c.q.ToSQL(mysql); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: error = %v, want containing %q", c.name, err, c.want)
		}
	}
}

// --- CompileExpression error propagation ------------------------------------

// A sub-expression compile error must propagate out of every composite node.
// badExpr is a UnaryExpression with an empty operator, which always errors.
func TestExpressionErrorPropagation(t *testing.T) {
	c := NewCompiler(PostgreSQLDialect{})
	bad := UnaryExpression{Operand: Col("x")}
	exprs := []Expression{
		BinaryExpression{Left: bad, Operator: "=", Right: Col("y")},
		BinaryExpression{Left: Col("y"), Operator: "=", Right: bad},
		UnaryExpression{Operator: "-", Operand: bad},
		RowExpression{Elements: []Expression{bad}},
		GroupingExpression{Kind: "ROLLUP", Elements: []Expression{bad}},
		GroupingSetsExpression{Sets: [][]Expression{{bad}}},
		LogicalExpression{Operator: "AND", Operands: []Expression{bad}},
		NotExpression{Operand: bad},
		InExpression{Left: bad, Items: []Expression{Col("y")}},
		InExpression{Left: Col("y"), Items: []Expression{bad}},
		BetweenExpression{Operand: bad, Low: Col("a"), High: Col("b")},
		BetweenExpression{Operand: Col("a"), Low: bad, High: Col("b")},
		BetweenExpression{Operand: Col("a"), Low: Col("b"), High: bad},
		NullPredicate{Operand: bad},
		CastExpression{Type: "int", Operand: bad},
		FuncExpression{Name: "f", Args: []Expression{bad}},
		CaseExpression{Operand: bad, Whens: []WhenClause{{Condition: Col("a"), Result: Col("b")}}},
		CaseExpression{Whens: []WhenClause{{Condition: bad, Result: Col("y")}}},
		CaseExpression{Whens: []WhenClause{{Condition: Col("a"), Result: bad}}},
		CaseExpression{Whens: []WhenClause{{Condition: Col("a"), Result: Col("b")}}, ElseValue: bad},
		ConcatExpression{Operands: []Expression{bad}},
		FieldExpression{Operand: bad, Field: NewIdent("f")},
		CollateExpression{Operand: bad, Collation: NewIdent("C")},
		DistinctPredicate{Left: bad, Right: Col("y")},
		DistinctPredicate{Left: Col("y"), Right: bad},
		ExtractExpression{Field: "YEAR", Operand: bad},
		PositionExpression{Substring: bad, String: Col("s")},
		PositionExpression{Substring: Col("a"), String: bad},
		StringAggExpression{Arg: bad},
		SubscriptExpression{Operand: bad, Indices: []SubscriptIndex{{Upper: Val(1)}}},
		SubscriptExpression{Operand: Col("a"), Indices: []SubscriptIndex{{Upper: bad}}},
		SubscriptExpression{Operand: Col("a"), Indices: []SubscriptIndex{{Slice: true, Lower: bad}}},
		SubscriptExpression{Operand: Col("a"), Indices: []SubscriptIndex{{Slice: true, Upper: bad}}},
	}
	for i, e := range exprs {
		if _, _, err := c.CompileExpression(e); err == nil {
			t.Errorf("case %d (%T) compiled without error, want propagation", i, e)
		}
	}
}

// Several expression nodes validate their own shape before compiling operands.
func TestExpressionValidationErrors(t *testing.T) {
	c := NewCompiler(PostgreSQLDialect{})
	cases := []struct {
		name string
		expr Expression
	}{
		{"empty unary operator", UnaryExpression{Operand: Col("x")}},
		{"empty row", RowExpression{}},
		{"empty logical", LogicalExpression{Operator: "AND"}},
		{"empty IN", InExpression{Left: Col("x")}},
		{"odd JSON_OBJECT", JSONObjectExpression{Pairs: []Expression{Val("k")}}},
		{"EXISTS without query", ExistsExpression{}},
		{"scalar subquery without query", ScalarSubqueryExpression{}},
		{"quantified without query", QuantifiedExpression{Left: Col("x"), Operator: "=", Quantifier: "ANY"}},
		{"empty CASE", CaseExpression{}},
		{"window without fn", WindowExpression{}},
		{"EXTRACT without field", ExtractExpression{Operand: Col("x")}},
		{"STRING_AGG without arg", StringAggExpression{}},
		{"CAST without type", CastExpression{Operand: Col("x")}},
		{"empty CONCAT", ConcatExpression{}},
		{"empty MATCH", MatchExpression{}},
	}
	for _, tc := range cases {
		if _, _, err := c.CompileExpression(tc.expr); err == nil {
			t.Errorf("%s: compiled without error, want validation error", tc.name)
		}
	}
}

// An unsupported expression and selection type are rejected.
func TestUnsupportedExpressionAndSelection(t *testing.T) {
	c := NewCompiler(PostgreSQLDialect{})
	if _, _, err := c.CompileExpression(unknownExpr{}); err == nil ||
		!strings.Contains(err.Error(), "unsupported expression") {
		t.Fatalf("CompileExpression(unknown) error = %v", err)
	}
	if _, _, err := c.CompileSelection(unknownSelection{}); err == nil ||
		!strings.Contains(err.Error(), "unsupported selection") {
		t.Fatalf("CompileSelection(unknown) error = %v", err)
	}
}

type unknownExpr struct{}

func (unknownExpr) SQLExpression() {}

type unknownSelection struct{}

func (unknownSelection) SQLSelection() {}

// --- foldJoinOns (where == nil) ---------------------------------------------

func TestJoinFoldsAsWholeWhere(t *testing.T) {
	// With no explicit WHERE, the folded join ON becomes the entire WHERE clause.
	// PostgreSQL renders USING + WHERE; MySQL renders an inline JOIN instead.
	del := DeleteFrom(users).Join(orgs, joinOn())
	assertSQL(t, del,
		`DELETE FROM "public"."users" USING "orgs" WHERE ("public"."users"."org_id" = "orgs"."id")`, nil)
	assertMySQL(t, del,
		"DELETE `public`.`users` FROM `public`.`users` JOIN `orgs` ON (`public`.`users`.`org_id` = `orgs`.`id`)", nil)

	upd := Update(users).Set(Set("name", Val("x"))).Join(orgs, joinOn())
	assertSQL(t, upd,
		`UPDATE "public"."users" SET "name" = $1 FROM "orgs" WHERE ("public"."users"."org_id" = "orgs"."id")`,
		[]any{"x"})
	assertMySQL(t, upd,
		"UPDATE `public`.`users` JOIN `orgs` ON (`public`.`users`.`org_id` = `orgs`.`id`) SET `name` = ?",
		[]any{"x"})
}

// --- CompileInsert REPLACE / width / SELECT-source branches ------------------

func TestReplaceIntoRejections(t *testing.T) {
	// REPLACE on a non-MySQL dialect.
	if _, _, err := ReplaceInto(Table{Name: NewIdent("t")}).Columns("a").Values(1).
		ToSQL(PostgreSQLDialect{}); err == nil || !strings.Contains(err.Error(), "REPLACE INTO is supported only") {
		t.Fatalf("ToSQL(postgres REPLACE) error = %v, want dialect rejection", err)
	}

	// REPLACE with RETURNING.
	if _, _, err := ReplaceInto(Table{Name: NewIdent("t")}).Columns("a").Values(1).
		Returning(Col("id")).ToSQL(MySQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "REPLACE INTO does not support RETURNING") {
		t.Fatalf("ToSQL(REPLACE RETURNING) error = %v, want RETURNING rejection", err)
	}

	// REPLACE with an ON CONFLICT clause (constructed directly; the builder hides it).
	q := InsertStmt{
		Table:   Table{Name: NewIdent("t")},
		Replace: true,
		Columns: idents([]string{"a"}),
		Rows:    [][]Expression{{Val(1)}},
		Conflict: &ConflictClause{
			Target:   idents([]string{"a"}),
			DoUpdate: true,
			Sets:     []Assignment{Set("a", Val(2))},
		},
	}
	if _, _, err := q.ToSQL(MySQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "does not take an ON CONFLICT") {
		t.Fatalf("ToSQL(REPLACE ON CONFLICT) error = %v, want conflict rejection", err)
	}
}

func TestInsertRowWidthMismatch(t *testing.T) {
	q := InsertStmt{
		Table:   Table{Name: NewIdent("t")},
		Columns: idents([]string{"a", "b"}),
		Rows:    [][]Expression{{Val(1)}},
	}
	if _, _, err := q.ToSQL(PostgreSQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "1 values for 2 columns") {
		t.Fatalf("ToSQL(width mismatch) error = %v, want width rejection", err)
	}
}

func TestInsertFromSelectWithUpsertAndReturning(t *testing.T) {
	src := Select(Col("email")).From(Table{Name: NewIdent("old")})
	q := InsertInto(users).Columns("email").FromSelect(src).
		OnConflict("email").DoUpdate(Set("email", Excluded("email"))).
		Returning(Col("id"))
	assertSQL(t, q,
		`INSERT INTO "public"."users" ("email") SELECT "email" FROM "old" ON CONFLICT ("email") DO UPDATE SET "email" = excluded."email" RETURNING "id"`,
		nil)
}

func TestAdminSetScopesAndSchemaQualifiedLock(t *testing.T) {
	// SESSION scope and a schema-qualified locked table.
	assertMySQL(t, SetVariables(SetVar{Name: "x", Value: Raw("1"), Scope: "SESSION"}),
		"SET SESSION x = 1;", nil)
	assertMySQL(t,
		LockTables(TableLock{Table: Table{Schema: NewIdent("s"), Name: NewIdent("t")}, Mode: LockWrite}),
		"LOCK TABLES `s`.`t` WRITE;", nil)
}

// --- CompileExpression: subquery / element error propagation & capability gates

// TestCompileExpressionSubqueryAndCapabilityErrors covers the CompileExpression
// arms whose error branches the error-propagation table above does not reach:
// subquery-bearing nodes, array/JSON element loops, and the dialect-capability
// gates (arrays, JSON access).
func TestCompileExpressionSubqueryAndCapabilityErrors(t *testing.T) {
	pg := NewCompiler(PostgreSQLDialect{})
	// badExpr fails to compile (empty unary operator); badQuery is a query that
	// fails to compile because it projects badExpr.
	badExpr := UnaryExpression{Operand: Col("x")}
	badQuery := Select(badExpr).From(orgs)

	subqueryErr := []Expression{
		InExpression{Left: Col("a"), Subquery: badQuery},
		ExistsExpression{Query: badQuery},
		ScalarSubqueryExpression{Query: badQuery},
		QuantifiedExpression{Left: badExpr, Operator: "=", Quantifier: "ANY", Query: badQuery},
		QuantifiedExpression{Left: Col("a"), Operator: "=", Quantifier: "ANY", Query: badQuery},
		ArrayExpression{Elements: []Expression{badExpr}},
		JSONObjectExpression{Pairs: []Expression{badExpr, Col("b")}},
		JSONAccessExpression{Operand: badExpr, Path: []any{"k"}},
	}
	for i, e := range subqueryErr {
		if _, _, err := pg.CompileExpression(e); err == nil {
			t.Errorf("case %d (%T) compiled without error, want propagation", i, e)
		}
	}

	// Capability gates: arrays and JSON access are unavailable on the standard
	// dialect, so a well-formed node is rejected at compile time.
	std := NewCompiler(StandardSQLDialect{})
	if _, _, err := std.CompileExpression(Array(Val(1))); err == nil ||
		!strings.Contains(err.Error(), "does not support arrays") {
		t.Errorf("Array on standard: err = %v, want arrays rejection", err)
	}
}

// rawTestQuery is a minimal Query (ToSQL only, not compilable), so compiling it
// as a subquery exercises compileSubquery's ToSQL fallback.
type rawTestQuery struct{}

func (rawTestQuery) ToSQL(Dialect) (string, []any, error) { return "SELECT 1", nil, nil }

func TestCompileSubqueryToSQLFallback(t *testing.T) {
	got, _, err := NewCompiler(PostgreSQLDialect{}).
		CompileExpression(ScalarSubqueryExpression{Query: rawTestQuery{}})
	if err != nil || got != "(SELECT 1)" {
		t.Fatalf("scalar subquery over non-compilable query = %q, %v; want (SELECT 1)", got, err)
	}
}

// TestUpdateOrderByErrorsMySQL covers writeOrderBy's error branches, reached via
// the MySQL UPDATE row-limit path (writeRowLimit -> writeOrderBy).
func TestUpdateOrderByErrorsMySQL(t *testing.T) {
	bad := UnaryExpression{Operand: Col("x")}
	q := Update(Table{Name: NewIdent("t")}).Set(Set("a", 1)).OrderBy(Asc(bad)).Limit(1)
	if _, _, err := q.ToSQL(MySQLDialect{}); err == nil {
		t.Fatal("UPDATE ORDER BY bad expr compiled, want error")
	}
	q2 := Update(Table{Name: NewIdent("t")}).Set(Set("a", 1)).
		OrderBy(Desc(Col("x")).NullsLast()).Limit(1)
	if _, _, err := q2.ToSQL(MySQLDialect{}); err == nil ||
		!strings.Contains(err.Error(), "does not support NULLS ordering") {
		t.Fatalf("UPDATE ORDER BY NULLS on mysql = %v, want NULLS rejection", err)
	}
}
