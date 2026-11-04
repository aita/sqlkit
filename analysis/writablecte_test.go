package analysis_test

import (
	"testing"

	"github.com/aita/sqlkit/analysis"
	"github.com/aita/sqlkit/sql"
)

// TestWritableCTEIsWrite covers data-modifying CTEs: a SELECT (or set operation)
// whose WITH clause contains an INSERT/UPDATE/DELETE/MERGE writes even though its
// top-level kind is a read, so Analysis.IsWrite must report true — otherwise a
// read/write router sends the write to a read replica.
func TestWritableCTEIsWrite(t *testing.T) {
	users := sql.Table{Name: sql.NewIdent("users")}
	a := analysis.New()

	// A read-only CTE: WITH recent AS (SELECT ...) SELECT * FROM recent.
	readCTE := sql.Select(sql.Col("id")).From(users).CTE("recent")
	readQuery := sql.Select(sql.Star()).From(readCTE).WithCTE(readCTE)
	if got := a.Analyze("", readQuery).IsWrite(); got {
		t.Errorf("read-only CTE IsWrite() = true, want false")
	}

	// A data-modifying CTE: WITH ins AS (INSERT ... RETURNING ...) SELECT * FROM ins.
	insert := sql.InsertInto(users).Columns("email").Values("a@example.com").Returning(sql.Col("id"))
	writeCTE := readCTE.WithBody(insert)
	writeQuery := sql.Select(sql.Star()).From(writeCTE).WithCTE(writeCTE)
	if !a.Analyze("", writeQuery).IsWrite() {
		t.Errorf("data-modifying CTE IsWrite() = false, want true (would route the write to a read replica)")
	}
}
