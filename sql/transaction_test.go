package sql

import "testing"

func TestTransactionStmtCompile(t *testing.T) {
	cases := []struct {
		stmt TransactionStmt
		want string
	}{
		{Begin(), "BEGIN;"},
		{Commit(), "COMMIT;"},
		{Rollback(), "ROLLBACK;"},
		{Savepoint("sp"), `SAVEPOINT "sp";`},
		{ReleaseSavepoint("sp"), `RELEASE SAVEPOINT "sp";`},
		{RollbackTo("sp"), `ROLLBACK TO SAVEPOINT "sp";`},
	}
	for _, tc := range cases {
		got, args, err := tc.stmt.ToSQL(PostgreSQLDialect{})
		if err != nil {
			t.Fatalf("ToSQL() error = %v", err)
		}
		if got != tc.want {
			t.Errorf("ToSQL() = %q, want %q", got, tc.want)
		}
		if len(args) != 0 {
			t.Errorf("args = %#v, want none", args)
		}
	}
}

func TestSavepointRequiresName(t *testing.T) {
	if _, _, err := Savepoint("").ToSQL(PostgreSQLDialect{}); err == nil {
		t.Fatal("SAVEPOINT without a name succeeded, want error")
	}
}
