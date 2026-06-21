package sql

import "testing"

func TestMaterializedViewCompile(t *testing.T) {
	cases := []struct {
		stmt Statement
		want string
	}{
		{CreateMaterializedView(Table{Name: NewIdent("m")}, Select(Col("id")).From(Table{Name: NewIdent("t")})),
			"CREATE MATERIALIZED VIEW \"m\" AS\nSELECT \"id\" FROM \"t\";"},
		{DropMaterializedView(Table{Name: NewIdent("m")}).IfExists(), `DROP MATERIALIZED VIEW IF EXISTS "m";`},
		{RefreshMaterializedView(Table{Name: NewIdent("m")}).WithNoData(), `REFRESH MATERIALIZED VIEW "m" WITH NO DATA;`},
	}
	for _, tc := range cases {
		got, _, err := tc.stmt.ToSQL(PostgreSQLDialect{})
		if err != nil {
			t.Fatalf("ToSQL() error = %v", err)
		}
		if got != tc.want {
			t.Errorf("ToSQL() = %q, want %q", got, tc.want)
		}
	}
}

// A materialized view body that would bind a value is rejected at compile time.
func TestMaterializedViewRejectsBoundValues(t *testing.T) {
	stmt := CreateMaterializedView(Table{Name: NewIdent("m")}, Select(Col("id")).From(Table{Name: NewIdent("t")}).Where(Eq(Col("a"), 1)))
	if _, _, err := stmt.ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("materialized view with a bound value compiled, want error")
	}
}

// TestMatviewCoverage exercises the materialized-view builder options and plumbing.
func TestMatviewCoverage(t *testing.T) {
	pg := PostgreSQLDialect{}
	mustCompile := func(q Query) {
		t.Helper()
		if _, _, err := q.ToSQL(pg); err != nil {
			t.Fatalf("%T: %v", q, err)
		}
	}
	mv := Table{Name: NewIdent("mv")}
	body := Select(Col("id")).From(orgs)
	mustCompile(CreateMaterializedView(mv, body).IfNotExists().Columns("id").WithNoData())
	mustCompile(DropMaterializedView(mv).IfExists().Cascade())
	mustCompile(RefreshMaterializedView(mv).Concurrently())
	mustCompile(RefreshMaterializedView(mv).WithNoData())

	for _, q := range []Query{
		CreateMaterializedView(mv, body), CreateMaterializedView(mv, body).Stmt(),
		DropMaterializedView(mv), DropMaterializedView(mv).Stmt(),
		RefreshMaterializedView(mv), RefreshMaterializedView(mv).Stmt(),
	} {
		if sp, ok := q.(interface{ setPos(int) Query }); ok {
			sp.setPos(2)
		}
	}
	_ = CreateMaterializedView(mv, body).buildStmt()
	_ = DropMaterializedView(mv).buildStmt()
	_ = RefreshMaterializedView(mv).buildStmt()
}
