package sql

import "testing"

func TestCreateSequenceStmt(t *testing.T) {
	seq := CreateSequence(Table{Name: NewIdent("s")}).Start(5).NoMaxValue().Cycle().Stmt()
	if seq.Start == nil || *seq.Start != 5 {
		t.Errorf("CreateSequenceStmt.Start = %v, want 5", seq.Start)
	}
	if !seq.NoMaxValue || seq.Cycle == nil || !*seq.Cycle {
		t.Errorf("CreateSequenceStmt = %+v, want NoMaxValue and Cycle=true", seq)
	}
}

func TestSequenceCompile(t *testing.T) {
	cases := []struct {
		stmt Statement
		want string
	}{
		{CreateSequence(Table{Name: NewIdent("s")}), `CREATE SEQUENCE "s";`},
		{CreateSequence(Table{Name: NewIdent("s")}).IfNotExists().Start(5).Increment(2),
			`CREATE SEQUENCE IF NOT EXISTS "s" INCREMENT BY 2 START WITH 5;`},
		{CreateSequence(Table{Name: NewIdent("s")}).NoMinValue().NoCycle().OwnedBy("NONE"),
			`CREATE SEQUENCE "s" NO MINVALUE NO CYCLE OWNED BY NONE;`},
		{DropSequence("s").IfExists(), `DROP SEQUENCE IF EXISTS "s";`},
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

// TestSequenceCoverage exercises the sequence builder options and plumbing.
func TestSequenceCoverage(t *testing.T) {
	pg := PostgreSQLDialect{}
	mustCompile := func(q Query) {
		t.Helper()
		if _, _, err := q.ToSQL(pg); err != nil {
			t.Fatalf("%T: %v", q, err)
		}
	}
	seq := Table{Name: NewIdent("s")}
	mustCompile(CreateSequence(seq).IfNotExists().Increment(2).Start(1).Cache(10).
		MinValue(1).MaxValue(100).Cycle().OwnedBy("t.c"))
	mustCompile(CreateSequence(seq).NoMinValue().NoMaxValue().NoCycle())
	mustCompile(DropSequence("s").IfExists().Cascade())
	mustCompile(DropSequence("s", "s2").IfExists()) // multiple names exercise the separator

	// Validation guards: a create with no name and a drop with no names.
	if _, _, err := CreateSequence(Table{}).ToSQL(pg); err == nil {
		t.Fatal("CREATE SEQUENCE without a name: want error")
	}
	if _, _, err := DropSequence().ToSQL(pg); err == nil {
		t.Fatal("DROP SEQUENCE without a name: want error")
	}

	_ = CreateSequence(seq).Stmt()
	_ = CreateSequence(seq).buildStmt()
	_ = DropSequence("s").Stmt()
	_ = DropSequence("s").buildStmt()
	for _, q := range []Query{
		CreateSequence(seq), CreateSequence(seq).Stmt(),
		DropSequence("s"), DropSequence("s").Stmt(),
	} {
		if sp, ok := q.(interface{ setPos(int) Query }); ok {
			sp.setPos(2)
		}
	}
}
