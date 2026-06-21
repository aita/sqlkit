package mysql_test

import (
	"strings"
	"testing"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse/mysql"
)

// TestParsePositions checks the parser records each column reference and string
// literal's byte offset into the source, so a caller can map a node back to its
// place in the original SQL.
func TestParsePositions(t *testing.T) {
	src := "SELECT id FROM users WHERE name = 'taro'"
	q, err := mysql.ParseOne(src)
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
	// The position also lives on the column's identifier, not only on the node.
	if got, want := col.Name.Pos(), strings.Index(src, "name"); got != want {
		t.Errorf("column name Ident offset = %d, want %d", got, want)
	}
	if got, want := val.Pos(), strings.Index(src, "'taro'"); got != want {
		t.Errorf("literal offset = %d, want %d", got, want)
	} else if !strings.HasPrefix(src[got:], "'taro'") {
		t.Errorf("literal offset %d does not point at \"'taro'\"", got)
	}
}

// TestParseQualifiedColumnIdentPosition checks a table-qualified column reference
// records its position on the column Ident, the qualifier included.
func TestParseQualifiedColumnIdentPosition(t *testing.T) {
	src := "SELECT u.id FROM users AS u WHERE u.name = 'taro'"
	q, err := mysql.ParseOne(src)
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

// TestParseTableIdentPosition checks the parser recovers a relation's byte offset
// onto the table Name identifier. The TiDB parser records no offset on table-name
// nodes, so the position is reconstructed by scanning the statement text; this
// pins that recovery end to end.
func TestParseTableIdentPosition(t *testing.T) {
	src := "SELECT id FROM users WHERE name = 'taro'"
	q, err := mysql.ParseOne(src)
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := sql.Unwrap(q).(sql.SelectStmt).From.(sql.Table)
	if !ok {
		t.Fatalf("FROM source is not a Table: %T", sql.Unwrap(q).(sql.SelectStmt).From)
	}
	if got, want := tbl.Name.Pos(), strings.Index(src, "users"); got != want {
		t.Errorf("table name Ident offset = %d, want %d", got, want)
	} else if !strings.HasPrefix(src[got:], "users") {
		t.Errorf("table name offset %d does not point at \"users\"", got)
	}
}

// TestParseAliasedAndQualifiedTablePositions checks table offsets survive an alias
// (which collapses the table into a single Name identifier) and a schema qualifier
// (db.users records the offset of the table name itself), across a JOIN.
func TestParseAliasedAndQualifiedTablePositions(t *testing.T) {
	src := "SELECT u.id FROM db.users AS u JOIN orders o ON u.id = o.uid"
	q, err := mysql.ParseOne(src)
	if err != nil {
		t.Fatal(err)
	}
	sel := sql.Unwrap(q).(sql.SelectStmt)

	base, ok := sel.From.(sql.SourceExpression)
	if !ok {
		t.Fatalf("FROM base is not a SourceExpression: %T", sel.From)
	}
	if got, want := base.Name.Pos(), strings.Index(src, "users"); got != want {
		t.Errorf("aliased/qualified table offset = %d, want %d", got, want)
	}

	if len(sel.Joins) != 1 {
		t.Fatalf("got %d joins, want 1", len(sel.Joins))
	}
	joined, ok := sel.Joins[0].Source.(sql.SourceExpression)
	if !ok {
		t.Fatalf("joined source is not a SourceExpression: %T", sel.Joins[0].Source)
	}
	if got, want := joined.Name.Pos(), strings.Index(src, "orders"); got != want {
		t.Errorf("joined table offset = %d, want %d", got, want)
	}
}

// TestParseSubqueryTableUnpositioned pins the documented boundary: a table inside a
// subquery in an expression (WHERE ... IN (SELECT ...)) is reached through the
// expression path, which scans no table offsets, so its Ident stays unpositioned
// rather than carrying a guess.
func TestParseSubqueryTableUnpositioned(t *testing.T) {
	q, err := mysql.ParseOne("SELECT id FROM users WHERE id IN (SELECT uid FROM bans)")
	if err != nil {
		t.Fatal(err)
	}
	in, ok := sql.Unwrap(q).(sql.SelectStmt).Where.(sql.InExpression)
	if !ok {
		t.Fatalf("WHERE is not an IN: %T", sql.Unwrap(q).(sql.SelectStmt).Where)
	}
	sub, ok := sql.Unwrap(in.Subquery).(sql.SelectStmt)
	if !ok {
		t.Fatalf("IN operand is not a subquery: %T", in.Subquery)
	}
	tbl, ok := sub.From.(sql.Table)
	if !ok {
		t.Fatalf("subquery FROM is not a Table: %T", sub.From)
	}
	if got := tbl.Name.Pos(); got != sql.NoPos {
		t.Errorf("subquery table offset = %d, want NoPos", got)
	}
}

// TestParseCompositePositions checks the parser records positions on composite
// expression nodes too, not only the leaves: the WHERE's AND and each of its
// operands (an IN list and a LIKE comparison) report a position.
func TestParseCompositePositions(t *testing.T) {
	src := "SELECT id FROM users WHERE id IN (1, 2) AND name LIKE 'a%'"
	q, err := mysql.ParseOne(src)
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

// TestParseSelectColumnPosition checks a select-list column reference records its
// byte offset, even though it takes the Selection fast path rather than the
// general expression converter.
func TestParseSelectColumnPosition(t *testing.T) {
	src := "SELECT name FROM users"
	q, err := mysql.ParseOne(src)
	if err != nil {
		t.Fatal(err)
	}
	sel := sql.Unwrap(q).(sql.SelectStmt)
	if len(sel.Columns) != 1 {
		t.Fatalf("got %d columns, want 1", len(sel.Columns))
	}
	col, ok := sel.Columns[0].(sql.ColumnExpression)
	if !ok {
		t.Fatalf("column is not a ColumnExpression: %T", sel.Columns[0])
	}
	if got, want := col.Pos(), strings.Index(src, "name"); got != want {
		t.Errorf("column offset = %d, want %d", got, want)
	}
}

// TestParseStatementAtZero pins that a top-level statement starting at byte 0
// reports offset 0, not NoPos: positions store offset+1, so 0 is a real position.
func TestParseStatementAtZero(t *testing.T) {
	q, err := mysql.ParseOne("SELECT name FROM users")
	if err != nil {
		t.Fatal(err)
	}
	if got := sql.Unwrap(q).(sql.Node).Pos(); got != 0 {
		t.Errorf("statement at byte 0: Pos() = %d, want 0", got)
	}
}

// TestParseStatementPosition checks the parser records a statement's offset,
// using the second statement of two.
func TestParseStatementPosition(t *testing.T) {
	src := "SELECT 1; SELECT name FROM users"
	qs, err := mysql.Parse(src)
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
	// The recorded offset is the end of the previous statement's text, so it may
	// include the whitespace before the second SELECT.
	got := n.Pos()
	if got == sql.NoPos {
		t.Fatal("second statement carries no position")
	}
	if !strings.HasPrefix(strings.TrimSpace(src[got:]), "SELECT name") {
		t.Errorf("statement offset %d does not reach the second SELECT: %q", got, src[got:])
	}
}

// TestParseTablePositionThroughComments checks the table-offset scanner skips
// line comments (-- and #) and block comments (/* */) and quoted strings, so an
// identifier inside them is never mistaken for a table and the real table name
// still records its offset.
func TestParseTablePositionThroughComments(t *testing.T) {
	src := "SELECT id /* from x */ FROM users -- from y\nWHERE name = '# from z'"
	q, err := mysql.ParseOne(src)
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := sql.Unwrap(q).(sql.SelectStmt).From.(sql.Table)
	if !ok {
		t.Fatalf("FROM source is not a Table: %T", sql.Unwrap(q).(sql.SelectStmt).From)
	}
	if got, want := tbl.Name.Pos(), strings.Index(src, "users"); got != want {
		t.Errorf("table name offset = %d, want %d", got, want)
	}
}

// TestParseTablePositionThroughHashComment exercises the # line-comment form
// specifically, which precedes the table-introducing FROM.
func TestParseTablePositionThroughHashComment(t *testing.T) {
	src := "SELECT id # pick a column\nFROM accounts WHERE id = 1"
	q, err := mysql.ParseOne(src)
	if err != nil {
		t.Fatal(err)
	}
	tbl := sql.Unwrap(q).(sql.SelectStmt).From.(sql.Table)
	if got, want := tbl.Name.Pos(), strings.Index(src, "accounts"); got != want {
		t.Errorf("table name offset = %d, want %d", got, want)
	}
}

// TestParseBacktickTablePosition checks the table-offset scanner reads a
// backtick-quoted table name, unquoting it and recording the offset of the opening
// backtick.
func TestParseBacktickTablePosition(t *testing.T) {
	src := "SELECT id FROM `users` WHERE id = 1"
	q, err := mysql.ParseOne(src)
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := sql.Unwrap(q).(sql.SelectStmt).From.(sql.Table)
	if !ok {
		t.Fatalf("FROM source is not a Table: %T", sql.Unwrap(q).(sql.SelectStmt).From)
	}
	if got := tbl.Name.String(); got != "users" {
		t.Errorf("table name = %q, want %q", got, "users")
	}
	if got, want := tbl.Name.Pos(), strings.Index(src, "`users`"); got != want {
		t.Errorf("backtick table offset = %d, want %d", got, want)
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
