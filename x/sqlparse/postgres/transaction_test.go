package postgres_test

import (
	"testing"

	"github.com/aita/sqlkit/sql"
)

func TestTransactionControl(t *testing.T) {
	cases := []struct {
		input string
		want  sql.Query
		sql   string
	}{
		{"BEGIN", sql.Begin(), "BEGIN;"},
		{"START TRANSACTION", sql.Begin(), "BEGIN;"},
		{"COMMIT", sql.Commit(), "COMMIT;"},
		{"ROLLBACK", sql.Rollback(), "ROLLBACK;"},
		{"SAVEPOINT sp1", sql.Savepoint("sp1"), `SAVEPOINT "sp1";`},
		{"RELEASE SAVEPOINT sp1", sql.ReleaseSavepoint("sp1"), `RELEASE SAVEPOINT "sp1";`},
		{"ROLLBACK TO SAVEPOINT sp1", sql.RollbackTo("sp1"), `ROLLBACK TO SAVEPOINT "sp1";`},
	}
	for _, tc := range cases {
		assertRoundTrip(t, tc.input, tc.want, tc.sql)
	}
}

// BEGIN with an isolation level is not modeled, so it round-trips raw.
func TestBeginIsolationFallsBackToRaw(t *testing.T) {
	assertRaw(t, "BEGIN ISOLATION LEVEL SERIALIZABLE")
}
