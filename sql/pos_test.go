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

// TestSetPosDDL pins that SetPos reaches DDL statements and their builders, so a
// parser can attach a DDL statement's source offset the same way it does for DML.
// A builder records the offset on the statement it wraps; a statement value on
// itself. A query kind SetPos does not know (a raw passthrough) is returned
// unchanged with NoPos.
func TestSetPosDDL(t *testing.T) {
	// SetPos stores the offset+1 the parser passes (so byte offset 0 stays a real
	// position), and Pos() reports the byte offset back.

	// A fluent builder carries the position through to the statement it wraps
	// (the builder itself is not a Node; Unwrap yields its statement).
	b := SetPos(CreateTable(Table{Name: NewIdent("users")}), 6)
	if got := Unwrap(b).(Node).Pos(); got != 5 {
		t.Errorf("built CreateTableStmt Pos() = %d, want 5", got)
	}
	if got := b.(CreateTableBuilder).Stmt().Pos(); got != 5 {
		t.Errorf("CreateTableBuilder.Stmt() Pos() = %d, want 5", got)
	}

	// A statement value with no builder carries it too.
	s := SetPos(DropIndex("idx", Table{Name: NewIdent("users")}), 10)
	if got := s.(Node).Pos(); got != 9 {
		t.Errorf("DropIndexStmt Pos() = %d, want 9", got)
	}

	// A query kind SetPos does not handle (a raw passthrough carries no position)
	// is returned unchanged.
	got := SetPos(RawSQL("VACUUM"), 3)
	if rq, ok := got.(RawQuery); !ok || rq.SQL() != "VACUUM" {
		t.Errorf("raw passthrough = %#v, want the RawQuery returned unchanged", got)
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
	positioned := Select(col).From(Table{Name: NewIdent("users")}).Where(Eq(col, val))
	built := Select(Col("id")).From(Table{Name: NewIdent("users")}).Where(Eq(Col("id"), Val(1)))

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
