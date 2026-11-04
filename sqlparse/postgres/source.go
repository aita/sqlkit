package postgres

import (
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	pg "github.com/pganalyze/pg_query_go/v6"
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

// convertFrom turns a FROM list into a base source plus the joins applied to it.
// Comma-separated items become CROSS JOINs, and explicit JoinExprs are flattened
// left-to-right.
func convertFrom(from []*pg.Node) (sql.Source, []joinSpec, error) {
	base, joins, err := flattenFrom(from[0])
	if err != nil {
		return nil, nil, err
	}
	for _, item := range from[1:] {
		src, extra, err := flattenFrom(item)
		if err != nil {
			return nil, nil, err
		}
		joins = append(joins, joinSpec{kind: "CROSS JOIN", source: src})
		joins = append(joins, extra...)
	}
	return base, joins, nil
}

// flattenFrom walks a (possibly nested) FROM item, returning the leftmost source
// and the joins that build out from it.
func flattenFrom(node *pg.Node) (sql.Source, []joinSpec, error) {
	if join := node.GetJoinExpr(); join != nil {
		base, joins, err := flattenFrom(join.GetLarg())
		if err != nil {
			return nil, nil, err
		}
		right, err := convertSource(join.GetRarg())
		if err != nil {
			return nil, nil, err
		}
		spec, err := joinSpecFor(join, right)
		if err != nil {
			return nil, nil, err
		}
		return base, append(joins, spec), nil
	}
	src, err := convertSource(node)
	if err != nil {
		return nil, nil, err
	}
	return src, nil, nil
}

func joinSpecFor(join *pg.JoinExpr, right sql.Source) (joinSpec, error) {
	kind, err := joinKind(join.GetJointype())
	if err != nil {
		return joinSpec{}, err
	}

	// NATURAL and USING joins are sqlkit SELECT builders limited to INNER/LEFT.
	if join.GetIsNatural() {
		if kind != "JOIN" && kind != "LEFT JOIN" {
			return joinSpec{}, sqlparse.Unsupportedf("NATURAL %s", kind)
		}
		return joinSpec{kind: kind, source: right, natural: true}, nil
	}
	if cols := join.GetUsingClause(); len(cols) > 0 {
		if kind != "JOIN" && kind != "LEFT JOIN" {
			return joinSpec{}, sqlparse.Unsupportedf("%s ... USING", kind)
		}
		names := make([]string, 0, len(cols))
		for _, col := range cols {
			if col.GetString_() == nil {
				return joinSpec{}, sqlparse.Unsupportedf("USING column %T", col.GetNode())
			}
			names = append(names, col.GetString_().GetSval())
		}
		return joinSpec{kind: kind, source: right, using: names}, nil
	}

	var on sql.Expression
	if quals := join.GetQuals(); quals != nil {
		expr, err := convertExpr(quals)
		if err != nil {
			return joinSpec{}, err
		}
		on = expr
	}
	if kind == "JOIN" && on == nil {
		return joinSpec{kind: "CROSS JOIN", source: right}, nil
	}
	return joinSpec{kind: kind, source: right, on: on}, nil
}

func joinKind(t pg.JoinType) (string, error) {
	switch t {
	case pg.JoinType_JOIN_INNER:
		return "JOIN", nil
	case pg.JoinType_JOIN_LEFT:
		return "LEFT JOIN", nil
	case pg.JoinType_JOIN_RIGHT:
		return "RIGHT JOIN", nil
	case pg.JoinType_JOIN_FULL:
		return "FULL JOIN", nil
	default:
		return "", sqlparse.Unsupportedf("join type %s", t)
	}
}

// convertSource turns a single FROM/JOIN item (a table or subquery) into a
// sqlkit Source.
func convertSource(node *pg.Node) (sql.Source, error) {
	switch {
	case node.GetRangeVar() != nil:
		return convertRangeVar(node.GetRangeVar())
	case node.GetRangeSubselect() != nil:
		return convertRangeSubselect(node.GetRangeSubselect())
	default:
		return nil, sqlparse.Unsupportedf("FROM item %T", node.GetNode())
	}
}

func convertRangeVar(rv *pg.RangeVar) (sql.Source, error) {
	// FROM ONLY t (inh=false) excludes inheritance/partition children from the
	// scan; sqlkit cannot model it and dropping it would silently widen the query
	// to every descendant table. (A plain FROM relation has inh=true.)
	if !rv.GetInh() {
		return nil, sqlparse.Unsupported("FROM ONLY (inheritance exclusion)")
	}
	table, err := tableFromRangeVar(rv)
	if err != nil {
		return nil, err
	}
	if alias := rv.GetAlias(); alias != nil && alias.GetAliasname() != "" {
		// A column-alias list — t AS s(c1, c2) — renames the source's output
		// columns positionally; sqlkit models only the table alias, so dropping it
		// would leave the outer query referencing column names that no longer
		// exist. Fall back to raw.
		if len(alias.GetColnames()) > 0 {
			return nil, sqlparse.Unsupported("FROM alias with a column list")
		}
		return sql.SourceAlias(table, alias.GetAliasname()), nil
	}
	return table, nil
}

func convertRangeSubselect(rs *pg.RangeSubselect) (sql.Source, error) {
	query, err := convertSelect(rs.GetSubquery().GetSelectStmt())
	if err != nil {
		return nil, err
	}
	alias := rs.GetAlias()
	if alias == nil || alias.GetAliasname() == "" {
		return nil, sqlparse.Unsupported("subquery source without an alias")
	}
	// A derived-table column-alias list — (SELECT ...) AS s(c1, c2) — renames the
	// projected columns positionally; sqlkit models only the table alias, so
	// dropping it would leave the outer query binding columns that no longer
	// exist. Fall back to raw.
	if len(alias.GetColnames()) > 0 {
		return nil, sqlparse.Unsupported("derived table with a column alias list")
	}
	source := sql.Alias(query, alias.GetAliasname())
	if rs.GetLateral() {
		return sql.Lateral(source), nil
	}
	return source, nil
}
