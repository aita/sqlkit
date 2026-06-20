package mysql

import (
	"strings"

	"github.com/aita/sqlkit/sql"
	"github.com/pingcap/tidb/pkg/parser/ast"
)

// offsetOf normalizes a TiDB node's source offset into the value sqlkit stores in
// NodePos: the 0-based byte offset plus one, so the zero value is reserved for "no
// position". The TiDB parser records an offset on every expression node it
// reduces, so a converted expression always points back into the source SQL.
func offsetOf(node ast.Node) int {
	pos := node.OriginTextPosition()
	if pos < 0 {
		return 0
	}
	return pos + 1
}

// identAt builds an Ident for name carrying node's source offset, or an
// unpositioned Ident when the offset is not recorded. The TiDB parser leaves the
// offset at zero on identifier-only nodes it does not reduce as expressions
// (column names in DDL, for instance); since no identifier in a valid statement
// begins at byte 0, a zero offset means "unset" and the Ident stays position-free.
func identAt(name string, node ast.Node) sql.Ident {
	if pos := node.OriginTextPosition(); pos > 0 {
		return sql.NewIdentAt(name, pos)
	}
	return sql.NewIdent(name)
}

// positioned records node's source offset on e when e is a node that carries a
// position, returning it unchanged otherwise (a RawExpression, a passthrough). It
// lets a convert wrap a result built through a constructor that returns the
// Expression interface — sql.And, sql.In — without a type assertion at the call
// site. The set of cases mirrors the position-carrying expression types in the sql
// package.
func positioned(e sql.Expression, node ast.Node) sql.Expression {
	pos := offsetOf(node)
	switch x := e.(type) {
	case sql.BinaryExpression:
		x.NodePos = pos
		return x
	case sql.LogicalExpression:
		x.NodePos = pos
		return x
	case sql.NotExpression:
		x.NodePos = pos
		return x
	case sql.UnaryExpression:
		x.NodePos = pos
		return x
	case sql.NullPredicate:
		x.NodePos = pos
		return x
	case sql.DistinctPredicate:
		x.NodePos = pos
		return x
	case sql.BetweenExpression:
		x.NodePos = pos
		return x
	case sql.InExpression:
		x.NodePos = pos
		return x
	case sql.ILikeExpression:
		x.NodePos = pos
		return x
	case sql.RegexpExpression:
		x.NodePos = pos
		return x
	case sql.SimilarToExpression:
		x.NodePos = pos
		return x
	case sql.ConcatExpression:
		x.NodePos = pos
		return x
	case sql.RowExpression:
		x.NodePos = pos
		return x
	case sql.ArrayExpression:
		x.NodePos = pos
		return x
	case sql.QuantifiedExpression:
		x.NodePos = pos
		return x
	case sql.QuantifiedArrayExpression:
		x.NodePos = pos
		return x
	case sql.CaseExpression:
		x.NodePos = pos
		return x
	case sql.CastExpression:
		x.NodePos = pos
		return x
	case sql.CollateExpression:
		x.NodePos = pos
		return x
	case sql.FieldExpression:
		x.NodePos = pos
		return x
	case sql.SubscriptExpression:
		x.NodePos = pos
		return x
	case sql.FuncExpression:
		x.NodePos = pos
		return x
	case sql.CountExpression:
		x.NodePos = pos
		return x
	case sql.StringAggExpression:
		x.NodePos = pos
		return x
	case sql.ExtractExpression:
		x.NodePos = pos
		return x
	case sql.PositionExpression:
		x.NodePos = pos
		return x
	case sql.JSONObjectExpression:
		x.NodePos = pos
		return x
	case sql.JSONAccessExpression:
		x.NodePos = pos
		return x
	case sql.GroupingExpression:
		x.NodePos = pos
		return x
	case sql.GroupingSetsExpression:
		x.NodePos = pos
		return x
	case sql.WindowExpression:
		x.NodePos = pos
		return x
	case sql.ExistsExpression:
		x.NodePos = pos
		return x
	case sql.ScalarSubqueryExpression:
		x.NodePos = pos
		return x
	case sql.ColumnExpression:
		x.NodePos = pos
		return x
	case sql.ValueExpression:
		x.NodePos = pos
		return x
	default:
		return e
	}
}

// tableAt builds a Table from a TableName, recording the table name's recovered
// source offset from tp when one is available. It is the positioned counterpart to
// tableFromName, used on the DML path where a statement's table offsets have been
// scanned; the DDL path, which scans none, uses tableFromName directly.
func tableAt(tn *ast.TableName, tp tablePos) sql.Table {
	t := tableFromName(tn)
	if off := tp.at(tn); off != sql.NoPos {
		t.Name.NodePos = off + 1
	}
	return t
}

// tablePos maps a parsed table-reference node to the byte offset of its name in
// the full source SQL. The TiDB parser records no offset on table-name nodes (it
// positions only expression nodes), so the offsets are recovered by scanning the
// statement text; see buildTablePos. A nil map — the value the expression path
// passes for subqueries it cannot place — reports NoPos for every lookup.
type tablePos map[*ast.TableName]int

// at returns the source offset recorded for tn, or NoPos when none was recovered.
func (tp tablePos) at(tn *ast.TableName) int {
	if tp == nil {
		return sql.NoPos
	}
	off, ok := tp[tn]
	if !ok {
		return sql.NoPos
	}
	return off
}

// buildTablePos recovers the source offset of each table name in a statement by
// pairing the table-name nodes (collected in source order) with the table-name
// identifiers found by scanning the statement text (also in source order), keyed
// by the node pointer. base is the statement's offset in the full source, so the
// recorded offsets are absolute, matching the offsets the parser puts on
// expression nodes. A table whose name cannot be matched is simply absent, so it
// stays unpositioned rather than carrying a guessed offset.
func buildTablePos(stmt ast.StmtNode, base int) tablePos {
	nodes := tableNameNodes(stmt)
	if len(nodes) == 0 {
		return nil
	}
	scanned := scanTableRefs(stmtText(stmt))
	tp := make(tablePos, len(nodes))
	j := 0
	for _, tn := range nodes {
		want := strings.ToLower(tn.Name.O)
		for j < len(scanned) && scanned[j].name != want {
			j++
		}
		if j >= len(scanned) {
			break
		}
		tp[tn] = base + scanned[j].offset
		j++
	}
	if len(tp) == 0 {
		return nil
	}
	return tp
}

// stmtText returns the verbatim source slice of a statement, the substring the
// offsets in buildTablePos are relative to.
func stmtText(stmt ast.StmtNode) string { return stmt.Text() }

// tableNameNodes collects every table-name node in a statement in source order.
func tableNameNodes(stmt ast.StmtNode) []*ast.TableName {
	var out []*ast.TableName
	stmt.Accept(tableNameCollector{&out})
	return out
}

type tableNameCollector struct{ out *[]*ast.TableName }

func (c tableNameCollector) Enter(n ast.Node) (ast.Node, bool) {
	if tn, ok := n.(*ast.TableName); ok {
		*c.out = append(*c.out, tn)
	}
	return n, false
}

func (tableNameCollector) Leave(n ast.Node) (ast.Node, bool) { return n, true }

// scannedRef is a table-name identifier located in the source text: its folded
// (lower-cased) name and the byte offset of the name within the scanned text.
type scannedRef struct {
	name   string
	offset int
}

// tableIntroducers are the keywords after which the next identifier names a table:
// FROM and the JOIN forms, INTO (INSERT/REPLACE), and UPDATE.
var tableIntroducers = map[string]bool{
	"FROM":          true,
	"JOIN":          true,
	"STRAIGHT_JOIN": true,
	"INTO":          true,
	"UPDATE":        true,
}

// scanTableRefs finds the table-name identifiers in a SQL statement and returns
// them in source order. It is a small purpose-built lexer: it skips string
// literals, quoted identifiers, and comments so an identifier inside them is never
// mistaken for a table, and it treats the identifier immediately after a
// table-introducing keyword (FROM, JOIN, INTO, UPDATE) as a table name, looking
// through a schema qualifier so db.users records the offset of users. It does not
// follow comma-separated FROM lists or DDL table names, which therefore stay
// unpositioned. It never returns a guessed offset: an identifier it does not place
// as a table is simply omitted.
func scanTableRefs(src string) []scannedRef {
	var out []scannedRef
	expectTable := false // the next identifier names a table
	i, n := 0, len(src)
	for i < n {
		switch c := src[i]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			i++
		case c == '-' && i+1 < n && src[i+1] == '-':
			i = skipLineComment(src, i+2)
		case c == '#':
			i = skipLineComment(src, i+1)
		case c == '/' && i+1 < n && src[i+1] == '*':
			i = skipBlockComment(src, i+2)
		case c == '\'' || c == '"':
			i = skipString(src, i)
			expectTable = false
		case c == '`':
			name, end := scanQuotedIdent(src, i)
			if expectTable && !qualifierFollows(src, end) {
				out = append(out, scannedRef{name: strings.ToLower(name), offset: i})
				expectTable = false
			} else if !qualifierFollows(src, end) {
				expectTable = false
			}
			i = end
		case isIdentStart(c):
			word, end := scanBareIdent(src, i)
			if expectTable {
				if !qualifierFollows(src, end) {
					out = append(out, scannedRef{name: strings.ToLower(word), offset: i})
					expectTable = false
				}
				// A qualifier (db.) keeps expectTable set for the name that follows.
			} else {
				expectTable = tableIntroducers[strings.ToUpper(word)]
			}
			i = end
		case c == '.':
			// A dot joins a qualified name (db.table); it neither starts nor ends a
			// table-introducing context, so leave expectTable untouched.
			i++
		default:
			// Any other character (an operator, a comma, an opening paren) ends a
			// table-introducing context: the identifier after FROM ( is inside a
			// subquery, not a table here.
			expectTable = false
			i++
		}
	}
	return out
}

// qualifierFollows reports whether the next non-space character at or after i is a
// '.', i.e. the identifier just scanned is a schema/database qualifier rather than
// the table name itself.
func qualifierFollows(src string, i int) bool {
	for i < len(src) {
		switch src[i] {
		case ' ', '\t', '\n', '\r', '\f', '\v':
			i++
		case '.':
			return true
		default:
			return false
		}
	}
	return false
}

func skipLineComment(src string, i int) int {
	for i < len(src) && src[i] != '\n' {
		i++
	}
	return i
}

func skipBlockComment(src string, i int) int {
	for i+1 < len(src) {
		if src[i] == '*' && src[i+1] == '/' {
			return i + 2
		}
		i++
	}
	return len(src)
}

// skipString skips a quoted string starting at the quote src[i], honoring doubled
// quotes ('') and backslash escapes, and returns the index just past it.
func skipString(src string, i int) int {
	quote := src[i]
	i++
	for i < len(src) {
		switch src[i] {
		case '\\':
			i += 2
		case quote:
			if i+1 < len(src) && src[i+1] == quote {
				i += 2
				continue
			}
			return i + 1
		default:
			i++
		}
	}
	return len(src)
}

// scanQuotedIdent reads a backtick-quoted identifier starting at src[i] (the
// backtick), returning the unquoted name and the index just past the closing
// backtick. A doubled backtick (``) is an escaped backtick within the name.
func scanQuotedIdent(src string, i int) (string, int) {
	i++ // opening backtick
	var b strings.Builder
	for i < len(src) {
		if src[i] == '`' {
			if i+1 < len(src) && src[i+1] == '`' {
				b.WriteByte('`')
				i += 2
				continue
			}
			return b.String(), i + 1
		}
		b.WriteByte(src[i])
		i++
	}
	return b.String(), len(src)
}

// scanBareIdent reads an unquoted identifier starting at src[i], returning it and
// the index just past it.
func scanBareIdent(src string, i int) (string, int) {
	start := i
	for i < len(src) && isIdentPart(src[i]) {
		i++
	}
	return src[start:i], i
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }
