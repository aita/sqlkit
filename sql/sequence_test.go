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
