package sqlite

import (
	"github.com/aita/sqlkit/sql"
	"github.com/aita/sqlkit/sqlparse"
	rsql "github.com/rqlite/sql"
)

// convertSource turns a FROM clause into a base source plus the joins applied to
// it. rqlite models the FROM chain as a left-deep tree of JoinClause nodes, so
// flattening it yields the leftmost source and the joins in source order.
func convertSource(src rsql.Source) (sql.Source, []sql.JoinClause, error) {
	return flattenSource(src)
}

func flattenSource(src rsql.Source) (sql.Source, []sql.JoinClause, error) {
	switch s := src.(type) {
	case *rsql.JoinClause:
		base, joins, err := flattenSource(s.X)
		if err != nil {
			return nil, nil, err
		}
		right, err := convertLeafSource(s.Y)
		if err != nil {
			return nil, nil, err
		}
		join, err := joinClauseFor(s, right)
		if err != nil {
			return nil, nil, err
		}
		return base, append(joins, join), nil
	default:
		leaf, err := convertLeafSource(src)
		if err != nil {
			return nil, nil, err
		}
		return leaf, nil, nil
	}
}

// convertLeafSource converts a non-join FROM item: a table, a subquery, or a
// parenthesized source.
func convertLeafSource(src rsql.Source) (sql.Source, error) {
	switch s := src.(type) {
	case *rsql.QualifiedTableName:
		return convertTableName(s), nil
	case *rsql.ParenSource:
		return convertParenSource(s)
	default:
		return nil, sqlparse.Unsupportedf("FROM item %T", src)
	}
}

func convertTableName(t *rsql.QualifiedTableName) sql.Source {
	table := tableName(t.Schema, t.Name)
	if t.Alias != nil {
		return sql.SourceAlias(table, t.Alias.Name)
	}
	return table
}

func convertParenSource(p *rsql.ParenSource) (sql.Source, error) {
	switch x := p.X.(type) {
	case *rsql.SelectStatement:
		query, err := convertSelect(x)
		if err != nil {
			return nil, err
		}
		if p.Alias == nil {
			return nil, sqlparse.Unsupported("subquery source without an alias")
		}
		return sql.Alias(query, p.Alias.Name), nil
	default:
		// A parenthesized table or join, e.g. FROM (a JOIN b). sqlkit has no
		// nested-source node, so flatten it only when it carries no alias of its
		// own; otherwise it cannot be represented.
		if p.Alias != nil {
			return nil, sqlparse.Unsupported("aliased parenthesized source")
		}
		base, joins, err := flattenSource(p.X)
		if err != nil {
			return nil, err
		}
		if len(joins) > 0 {
			return nil, sqlparse.Unsupported("parenthesized join source")
		}
		return base, nil
	}
}

func joinClauseFor(j *rsql.JoinClause, right sql.Source) (sql.JoinClause, error) {
	op := j.Operator

	// A comma in the FROM list is a cross join with no constraint.
	if op != nil && op.Comma.IsValid() {
		return sql.JoinClause{Kind: "CROSS JOIN", Source: right}, nil
	}

	kind := "JOIN"
	switch {
	case op != nil && op.Left.IsValid():
		kind = "LEFT JOIN"
	case op != nil && op.Cross.IsValid():
		kind = "CROSS JOIN"
	}

	natural := op != nil && op.Natural.IsValid()

	switch c := j.Constraint.(type) {
	case *rsql.OnConstraint:
		on, err := convertExpr(c.X)
		if err != nil {
			return sql.JoinClause{}, err
		}
		return sql.JoinClause{Kind: kind, Source: right, On: on}, nil
	case *rsql.UsingConstraint:
		if kind != "JOIN" && kind != "LEFT JOIN" {
			return sql.JoinClause{}, sqlparse.Unsupportedf("%s ... USING", kind)
		}
		names := make([]string, 0, len(c.Columns))
		for _, col := range c.Columns {
			names = append(names, col.Name)
		}
		return sql.JoinClause{Kind: kind, Source: right, Using: sql.Idents(names...)}, nil
	case nil:
		if natural {
			if kind != "JOIN" && kind != "LEFT JOIN" {
				return sql.JoinClause{}, sqlparse.Unsupportedf("NATURAL %s", kind)
			}
			return sql.JoinClause{Kind: kind, Source: right, Natural: true}, nil
		}
		// No constraint and not natural: a cross join (explicit CROSS, or an
		// unconstrained INNER join which is equivalent to one). An unconstrained
		// LEFT JOIN is not one: it keeps every left row when the right side is
		// empty, so keep it raw.
		if kind == "LEFT JOIN" {
			return sql.JoinClause{}, sqlparse.Unsupported("LEFT JOIN without ON/USING")
		}
		return sql.JoinClause{Kind: "CROSS JOIN", Source: right}, nil
	default:
		return sql.JoinClause{}, sqlparse.Unsupportedf("join constraint %T", j.Constraint)
	}
}
