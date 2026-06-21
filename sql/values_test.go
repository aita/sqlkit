package sql

import (
	"strings"
	"testing"
)

func TestValuesQuery(t *testing.T) {
	// Standalone VALUES, built both from rows and via Row.
	assertSQL(t,
		Values([]Expression{Int(1), Val("a")}).Row(2, "b"),
		`VALUES ($1, $2), ($3, $4)`, []any{1, "a", 2, "b"})

	// As a derived-table source feeding a SELECT.
	src := Alias(Values().Row(1, "a").Row(2, "b"), "t")
	assertSQL(t, Select(Col("t.column1")).From(src),
		`SELECT "t"."column1" FROM (VALUES ($1, $2), ($3, $4)) AS "t"`, []any{1, "a", 2, "b"})
}

func TestValuesQueryRejectsRaggedRows(t *testing.T) {
	q := Values().Row(1, "a").Row(2)
	if _, _, err := q.ToSQL(PostgreSQLDialect{}); err == nil || !strings.Contains(err.Error(), "want 2") {
		t.Fatalf("ToSQL() error = %v, want ragged-row rejection", err)
	}
}

// TestValuesSourceCoverage covers the VALUES query as a source and its plumbing.
func TestValuesSourceCoverage(t *testing.T) {
	pg := PostgreSQLDialect{}
	v := Values([]Expression{Val(1), Val(2)}, []Expression{Val(3), Val(4)})
	if _, _, err := v.ToSQL(pg); err != nil {
		t.Fatal(err)
	}
	_ = v.SQLSource()
	if sp, ok := Query(v).(interface{ setPos(int) Query }); ok {
		sp.setPos(2)
	}
	if _, _, err := Select(Col("column1")).From(Alias(v, "x")).ToSQL(pg); err != nil {
		t.Fatal(err)
	}
}
