package sql

import "testing"

// TestBuiltNodesHaveNoPos pins that builder-constructed nodes carry NoPos: only
// parsing records positions, so an AST assembled by the builders is position-free.
func TestBuiltNodesHaveNoPos(t *testing.T) {
	var _ Node = ColumnExpression{}
	var _ Node = ValueExpression{}

	if got := Col("id").Pos(); got != NoPos {
		t.Errorf("built Col Pos() = %d, want NoPos", got)
	}
	if got := Val(1).Pos(); got != NoPos {
		t.Errorf("built Val Pos() = %d, want NoPos", got)
	}
}

// TestPositionIsCarriedAndIgnoredByCompiler pins that NodePos is readable through
// Pos() and that positions are metadata: a positioned node and a built one compile
// to the same SQL and args.
func TestPositionIsCarriedAndIgnoredByCompiler(t *testing.T) {
	col := Col("id")
	col.NodePos = 8 // stored offset+1, so Pos() reports byte offset 7
	if col.Pos() != 7 {
		t.Errorf("Pos() = %d, want 7", col.Pos())
	}

	val := Val(1)
	val.NodePos = 26
	positioned := Select(col).From(Table{Name: "users"}).Where(Eq(col, val))
	built := Select(Col("id")).From(Table{Name: "users"}).Where(Eq(Col("id"), Val(1)))

	ps, pa, err := positioned.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("positioned ToSQL: %v", err)
	}
	bs, ba, err := built.ToSQL(PostgreSQLDialect{})
	if err != nil {
		t.Fatalf("built ToSQL: %v", err)
	}
	if ps != bs {
		t.Errorf("SQL differs:\n positioned %q\n built      %q", ps, bs)
	}
	if len(pa) != len(ba) {
		t.Errorf("args differ: %v vs %v", pa, ba)
	}
}
