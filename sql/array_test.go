package sql_test

import (
	"strings"
	"testing"

	. "github.com/aita/sqlkit/sql"
)

func TestArrayConstructor(t *testing.T) {
	assertSQL(t,
		Select(As(Array(Int(1), Int(2), Int(3)), "xs")).From(Table{Name: "t"}),
		`SELECT ARRAY[$1, $2, $3] AS "xs" FROM "t"`, []any{1, 2, 3})
}

func TestAnyArrayAndAllArray(t *testing.T) {
	// col = ANY(ARRAY[...]) — the IN replacement spelled with an array.
	assertSQL(t,
		Select(Col("id")).From(Table{Name: "t"}).Where(AnyArray(Col("id"), "=", Array(Int(1), Int(2)))),
		`SELECT "id" FROM "t" WHERE ("id" = ANY(ARRAY[$1, $2]))`, []any{1, 2})

	// col <> ALL($1) — a single bound array parameter (the efficient NOT IN).
	assertSQL(t,
		Select(Col("id")).From(Table{Name: "t"}).Where(AllArray(Col("id"), "<>", Val([]int{1, 2, 3}))),
		`SELECT "id" FROM "t" WHERE ("id" <> ALL($1))`, []any{[]int{1, 2, 3}})
}

func TestArraysRejectedOnMySQL(t *testing.T) {
	q := Select(As(Array(Int(1)), "xs")).From(Table{Name: "t"})
	if _, _, err := q.ToSQL(MySQLDialect{}); err == nil || !strings.Contains(err.Error(), "arrays") {
		t.Fatalf("ToSQL(mysql) error = %v, want array rejection", err)
	}

	q2 := Select(Col("id")).From(Table{Name: "t"}).Where(AnyArray(Col("id"), "=", Val([]int{1})))
	if _, _, err := q2.ToSQL(MySQLDialect{}); err == nil || !strings.Contains(err.Error(), "arrays") {
		t.Fatalf("ToSQL(mysql) error = %v, want array rejection", err)
	}
}
