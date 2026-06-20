package postgres_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse/postgres"
)

// TestParsePositions checks the parser records each column reference and string
// literal's byte offset into the source, so a caller can map a node back to its
// place in the original SQL.
func TestParsePositions(t *testing.T) {
	src := "SELECT id FROM users WHERE name = 'taro'"
	q, err := postgres.ParseOne(src)
	if err != nil {
		t.Fatal(err)
	}
	sel, ok := sql.Unwrap(q).(sql.SelectStmt)
	if !ok {
		t.Fatalf("not a SELECT: %T", sql.Unwrap(q))
	}
	bin, ok := sel.Where.(sql.BinaryExpression)
	if !ok {
		t.Fatalf("WHERE is not a comparison: %T", sel.Where)
	}
	if bin.Pos() == sql.NoPos {
		t.Error("comparison node carries no position")
	}
	col, ok := bin.Left.(sql.ColumnExpression)
	if !ok {
		t.Fatalf("left operand is not a column: %T", bin.Left)
	}
	val, ok := bin.Right.(sql.ValueExpression)
	if !ok {
		t.Fatalf("right operand is not a value: %T", bin.Right)
	}

	if got, want := col.Pos(), strings.Index(src, "name"); got != want {
		t.Errorf("column offset = %d, want %d", got, want)
	} else if src[got:got+len("name")] != "name" {
		t.Errorf("column offset %d does not point at \"name\"", got)
	}
	if got, want := val.Pos(), strings.Index(src, "'taro'"); got != want {
		t.Errorf("literal offset = %d, want %d", got, want)
	} else if !strings.HasPrefix(src[got:], "'taro'") {
		t.Errorf("literal offset %d does not point at \"'taro'\"", got)
	}
}

// TestParseTableIdentPosition checks the parser records a relation's byte offset
// into the table Name identifier, so a caller can map the table name back to its
// place in the source. Positions now live inside sql.Ident, not only on the
// surrounding node.
func TestParseTableIdentPosition(t *testing.T) {
	src := "SELECT id FROM users WHERE name = 'taro'"
	q, err := postgres.ParseOne(src)
	if err != nil {
		t.Fatal(err)
	}
	sel := sql.Unwrap(q).(sql.SelectStmt)
	if sel.From == nil {
		t.Fatal("SELECT has no FROM source")
	}
	tbl, ok := sel.From.(sql.Table)
	if !ok {
		t.Fatalf("FROM source is not a Table: %T", sel.From)
	}
	if got, want := tbl.Name.Pos(), strings.Index(src, "users"); got != want {
		t.Errorf("table name Ident offset = %d, want %d", got, want)
	} else if !strings.HasPrefix(src[got:], "users") {
		t.Errorf("table name offset %d does not point at \"users\"", got)
	}
}

// TestParseQualifiedColumnIdentPosition checks a schema/table-qualified column
// reference still records its position on the column Ident.
func TestParseQualifiedColumnIdentPosition(t *testing.T) {
	src := "SELECT u.id FROM users AS u WHERE u.name = 'taro'"
	q, err := postgres.ParseOne(src)
	if err != nil {
		t.Fatal(err)
	}
	sel := sql.Unwrap(q).(sql.SelectStmt)
	bin, ok := sel.Where.(sql.BinaryExpression)
	if !ok {
		t.Fatalf("WHERE is not a comparison: %T", sel.Where)
	}
	col, ok := bin.Left.(sql.ColumnExpression)
	if !ok {
		t.Fatalf("left operand is not a column: %T", bin.Left)
	}
	if got, want := col.Name.Pos(), strings.Index(src, "u.name"); got != want {
		t.Errorf("qualified column Ident offset = %d, want %d", got, want)
	}
}

// TestParseCompositePositions checks the parser records positions on composite
// expression nodes too, not only the leaves: the WHERE's AND and each of its
// operands (an IN list and a LIKE comparison) report a position.
func TestParseCompositePositions(t *testing.T) {
	src := "SELECT id FROM users WHERE id IN (1, 2) AND name LIKE 'a%'"
	q, err := postgres.ParseOne(src)
	if err != nil {
		t.Fatal(err)
	}
	sel := sql.Unwrap(q).(sql.SelectStmt)
	and, ok := sel.Where.(sql.LogicalExpression)
	if !ok {
		t.Fatalf("WHERE is not an AND: %T", sel.Where)
	}
	if and.Pos() == sql.NoPos {
		t.Error("AND node carries no position")
	}
	if len(and.Operands) != 2 {
		t.Fatalf("AND has %d operands, want 2", len(and.Operands))
	}
	for _, op := range and.Operands {
		n, ok := op.(sql.Node)
		if !ok {
			t.Fatalf("operand %T does not satisfy Node", op)
		}
		if n.Pos() == sql.NoPos {
			t.Errorf("operand %T carries no position", op)
		}
	}
}

// TestParseStatementAtZero pins that a top-level statement starting at byte 0
// reports offset 0, not NoPos: positions store offset+1, so 0 is a real position.
func TestParseStatementAtZero(t *testing.T) {
	q, err := postgres.ParseOne("SELECT name FROM users")
	if err != nil {
		t.Fatal(err)
	}
	if got := sql.Unwrap(q).(sql.Node).Pos(); got != 0 {
		t.Errorf("statement at byte 0: Pos() = %d, want 0", got)
	}
}

// TestParseStatementPosition checks the parser records a statement's offset from
// the RawStmt wrapper, using the second statement of two.
func TestParseStatementPosition(t *testing.T) {
	src := "SELECT 1; SELECT name FROM users"
	qs, err := postgres.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 2 {
		t.Fatalf("parsed %d statements, want 2", len(qs))
	}
	second := sql.Unwrap(qs[1])
	n, ok := second.(sql.Node)
	if !ok {
		t.Fatalf("statement %T does not satisfy Node", second)
	}
	// pg_query's stmt_location is the offset just past the previous statement's
	// terminator, so it may include leading whitespace before the second SELECT.
	got := n.Pos()
	if got == sql.NoPos {
		t.Fatal("second statement carries no position")
	}
	if !strings.HasPrefix(strings.TrimSpace(src[got:]), "SELECT name") {
		t.Errorf("statement offset %d does not reach the second SELECT: %q", got, src[got:])
	}
}

// TestBuiltNodesHaveNoPos pins that builder-constructed nodes carry NoPos: only
// parsing records positions.
func TestBuiltNodesHaveNoPos(t *testing.T) {
	if sql.Col("id").Pos() != sql.NoPos {
		t.Error("built column has a position, want NoPos")
	}
	if sql.Val("x").Pos() != sql.NoPos {
		t.Error("built value has a position, want NoPos")
	}
}
