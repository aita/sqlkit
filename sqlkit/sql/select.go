package sql

import "strings"

// Query is implemented by SQL statements that can be compiled.
type Query interface {
	ToSQL(dialect Dialect) (string, []any, error)
}

// SelectQuery is the initial runtime representation for a SELECT statement.
type SelectQuery struct {
	from    Source
	columns []Selection
	joins   []JoinClause
	where   Expression
}

// JoinClause represents a JOIN clause.
type JoinClause struct {
	Kind   string
	Source Source
	On     Expression
}

// Select creates a SELECT query builder.
func Select(columns ...Selection) SelectQuery {
	return SelectQuery{columns: columns}
}

// From sets the table or subquery for the SELECT query.
func (q SelectQuery) From(source Source) SelectQuery {
	q.from = source
	return q
}

// Join adds an INNER JOIN clause.
func (q SelectQuery) Join(source Source, on Expression) SelectQuery {
	q.joins = append(q.joins, JoinClause{Kind: "JOIN", Source: source, On: on})
	return q
}

// LeftJoin adds a LEFT JOIN clause.
func (q SelectQuery) LeftJoin(source Source, on Expression) SelectQuery {
	q.joins = append(q.joins, JoinClause{Kind: "LEFT JOIN", Source: source, On: on})
	return q
}

// Where sets the WHERE expression for the SELECT query.
func (q SelectQuery) Where(expr Expression) SelectQuery {
	q.where = expr
	return q
}

// SQLSource marks SelectQuery as a subquery source.
func (q SelectQuery) SQLSource() SourceExpression { return SourceExpression{Query: q} }

// ToSQL compiles the SELECT query.
func (q SelectQuery) ToSQL(dialect Dialect) (string, []any, error) {
	compiler := NewCompiler(dialect)
	columns := "*"
	if len(q.columns) > 0 {
		compiled := make([]string, len(q.columns))
		for i, column := range q.columns {
			selectionSQL, err := compiler.CompileSelection(column)
			if err != nil {
				return "", nil, err
			}
			compiled[i] = selectionSQL
		}
		columns = strings.Join(compiled, ", ")
	}
	fromSQL, args, err := compiler.CompileSource(q.from)
	if err != nil {
		return "", nil, err
	}
	sql := "SELECT " + columns + " FROM " + fromSQL
	for _, join := range q.joins {
		joinSQL, joinArgs, err := compiler.CompileJoin(join)
		if err != nil {
			return "", nil, err
		}
		sql += " " + joinSQL
		args = append(args, joinArgs...)
	}
	if q.where != nil {
		whereSQL, whereArgs, err := compiler.CompileExpression(q.where)
		if err != nil {
			return "", nil, err
		}
		sql += " WHERE " + whereSQL
		args = append(args, whereArgs...)
		return sql, args, nil
	}
	return sql, args, nil
}
