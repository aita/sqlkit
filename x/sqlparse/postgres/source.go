package postgres

import (
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/x/sqlparse"
	pg "github.com/pganalyze/pg_query_go/v6"
)

// joinSpec is one flattened JOIN: its kind keyword, the right-hand source, and
// the ON predicate (nil for CROSS JOIN).
type joinSpec struct {
	kind   string
	source sql.Source
	on     sql.Expression
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
	if join.GetIsNatural() {
		return joinSpec{}, sqlparse.Unsupported("NATURAL JOIN")
	}
	if len(join.GetUsingClause()) > 0 {
		return joinSpec{}, sqlparse.Unsupported("JOIN ... USING")
	}
	var on sql.Expression
	if quals := join.GetQuals(); quals != nil {
		expr, err := convertExpr(quals)
		if err != nil {
			return joinSpec{}, err
		}
		on = expr
	}
	switch join.GetJointype() {
	case pg.JoinType_JOIN_INNER:
		if on == nil {
			return joinSpec{kind: "CROSS JOIN", source: right}, nil
		}
		return joinSpec{kind: "JOIN", source: right, on: on}, nil
	case pg.JoinType_JOIN_LEFT:
		return joinSpec{kind: "LEFT JOIN", source: right, on: on}, nil
	case pg.JoinType_JOIN_RIGHT:
		return joinSpec{kind: "RIGHT JOIN", source: right, on: on}, nil
	case pg.JoinType_JOIN_FULL:
		return joinSpec{kind: "FULL JOIN", source: right, on: on}, nil
	default:
		return joinSpec{}, sqlparse.Unsupportedf("join type %s", join.GetJointype())
	}
}

// convertSource turns a single FROM/JOIN item (a table or subquery) into a
// sqlkit Source.
func convertSource(node *pg.Node) (sql.Source, error) {
	switch {
	case node.GetRangeVar() != nil:
		return convertRangeVar(node.GetRangeVar()), nil
	case node.GetRangeSubselect() != nil:
		return convertRangeSubselect(node.GetRangeSubselect())
	default:
		return nil, sqlparse.Unsupportedf("FROM item %T", node.GetNode())
	}
}

func convertRangeVar(rv *pg.RangeVar) sql.Source {
	table := sql.Table{Schema: rv.GetSchemaname(), Name: rv.GetRelname()}
	if alias := rv.GetAlias(); alias != nil && alias.GetAliasname() != "" {
		return sql.SourceAlias(table, alias.GetAliasname())
	}
	return table
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
	source := sql.Alias(query, alias.GetAliasname())
	if rs.GetLateral() {
		return sql.Lateral(source), nil
	}
	return source, nil
}
