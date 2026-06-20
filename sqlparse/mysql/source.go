package mysql

import (
	"strings"

	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	"github.com/pingcap/tidb/pkg/parser/ast"
)

// joinSpec is one flattened JOIN: its kind keyword, the right-hand source, and
// the join condition (an ON predicate, a USING column list, or NATURAL).
type joinSpec struct {
	kind    string
	source  sql.Source
	on      sql.Expression
	using   []string
	natural bool
}

// convertFrom flattens a FROM clause's join tree into a base source plus the
// joins applied to it, left-to-right.
func convertFrom(j *ast.Join, tp tablePos) (sql.Source, []joinSpec, error) {
	return flattenJoin(j, tp)
}

func flattenJoin(node ast.ResultSetNode, tp tablePos) (sql.Source, []joinSpec, error) {
	switch n := node.(type) {
	case *ast.Join:
		// A single-table FROM is a Join whose Right is nil wrapping the source.
		if n.Right == nil {
			return flattenJoin(n.Left, tp)
		}
		base, joins, err := flattenJoin(n.Left, tp)
		if err != nil {
			return nil, nil, err
		}
		right, err := convertTableSourceNode(n.Right, tp)
		if err != nil {
			return nil, nil, err
		}
		spec, err := joinSpecFor(n, right)
		if err != nil {
			return nil, nil, err
		}
		return base, append(joins, spec), nil
	case *ast.TableSource:
		src, err := convertTableSource(n, tp)
		if err != nil {
			return nil, nil, err
		}
		return src, nil, nil
	default:
		return nil, nil, sqlparse.Unsupportedf("FROM item %T", node)
	}
}

// joinSpecFor classifies one Join node. TiDB encodes an inner join as a
// CrossJoin carrying an ON/USING condition, a comma or CROSS JOIN as a bare
// CrossJoin, and tags NATURAL joins with NaturalJoin.
func joinSpecFor(j *ast.Join, right sql.Source) (joinSpec, error) {
	kind := "JOIN"
	switch j.Tp {
	case ast.LeftJoin:
		kind = "LEFT JOIN"
	case ast.RightJoin:
		kind = "RIGHT JOIN"
	}

	if j.NaturalJoin {
		if kind == "RIGHT JOIN" {
			return joinSpec{}, sqlparse.Unsupported("NATURAL RIGHT JOIN")
		}
		return joinSpec{kind: kind, source: right, natural: true}, nil
	}

	if len(j.Using) > 0 {
		if kind == "RIGHT JOIN" {
			return joinSpec{}, sqlparse.Unsupported("RIGHT JOIN ... USING")
		}
		names := make([]string, 0, len(j.Using))
		for _, col := range j.Using {
			names = append(names, col.Name.O)
		}
		return joinSpec{kind: kind, source: right, using: names}, nil
	}

	if j.On == nil {
		if kind != "JOIN" {
			return joinSpec{}, sqlparse.Unsupportedf("%s without an ON condition", kind)
		}
		return joinSpec{kind: "CROSS JOIN", source: right}, nil
	}
	on, err := convertExpr(j.On.Expr)
	if err != nil {
		return joinSpec{}, err
	}
	return joinSpec{kind: kind, source: right, on: on}, nil
}

func applyJoin(q sql.SelectBuilder, j joinSpec) sql.SelectBuilder {
	if j.natural {
		if j.kind == "LEFT JOIN" {
			return q.NaturalLeftJoin(j.source)
		}
		return q.NaturalJoin(j.source)
	}
	if j.using != nil {
		if j.kind == "LEFT JOIN" {
			return q.LeftJoinUsing(j.source, j.using...)
		}
		return q.JoinUsing(j.source, j.using...)
	}
	switch j.kind {
	case "LEFT JOIN":
		return q.LeftJoin(j.source, j.on)
	case "RIGHT JOIN":
		return q.RightJoin(j.source, j.on)
	case "CROSS JOIN":
		return q.CrossJoin(j.source)
	default:
		return q.Join(j.source, j.on)
	}
}

// convertTableSourceNode converts a join operand, which is normally a
// TableSource; a parenthesized join on the right side is not yet supported.
func convertTableSourceNode(node ast.ResultSetNode, tp tablePos) (sql.Source, error) {
	ts, ok := node.(*ast.TableSource)
	if !ok {
		return nil, sqlparse.Unsupportedf("join operand %T", node)
	}
	return convertTableSource(ts, tp)
}

func convertTableSource(ts *ast.TableSource, tp tablePos) (sql.Source, error) {
	switch src := ts.Source.(type) {
	case *ast.TableName:
		table := tableAt(src, tp)
		if alias := ts.AsName.O; alias != "" {
			// SourceAlias collapses the table into a single qualified Name Ident, so
			// carry the table name's recovered offset onto it explicitly.
			aliased := sql.SourceAlias(table, alias)
			aliased.Name.NodePos = table.Name.NodePos
			return aliased, nil
		}
		return table, nil
	case *ast.SelectStmt, *ast.SetOprStmt:
		if ts.AsName.O == "" {
			return nil, sqlparse.Unsupported("derived table without an alias")
		}
		query, err := convertResultSetQuery(src.(ast.ResultSetNode), tp)
		if err != nil {
			return nil, err
		}
		source := sql.Alias(query, ts.AsName.O)
		if ts.Lateral {
			return sql.Lateral(source), nil
		}
		return source, nil
	default:
		return nil, sqlparse.Unsupportedf("table source %T", ts.Source)
	}
}

// tableFromName builds a Table from a TableName, keeping any schema qualifier and
// recording the table name's source offset when the parser provides one.
func tableFromName(tn *ast.TableName) sql.Table {
	return sql.Table{Schema: sql.NewIdent(tn.Schema.O), Name: identAt(tn.Name.O, tn)}
}

// starQualifier renders the qualifier preceding a "*" wildcard, e.g. "t" or
// "db.t".
func starQualifier(w *ast.WildCardField) string {
	parts := make([]string, 0, 2)
	if w.Schema.O != "" {
		parts = append(parts, w.Schema.O)
	}
	if w.Table.O != "" {
		parts = append(parts, w.Table.O)
	}
	return strings.Join(parts, ".")
}
