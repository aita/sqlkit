package sqlkit

import querysql "github.com/sqlkit-dev/sqlkit/sqlkit/sql"

// Eq creates an equality predicate. Non-expression values become bind values.
func Eq(left, right any) querysql.BinaryExpression { return querysql.Eq(left, right) }

// Col creates a column reference expression.
func Col(name string) querysql.ColumnExpression { return querysql.Col(name) }

// Val creates a bind parameter expression.
func Val(value any) querysql.ValueExpression { return querysql.Val(value) }

// Int creates an integer bind parameter expression.
func Int(value int) querysql.ValueExpression { return querysql.Int(value) }

// Count creates a COUNT(selection) aggregate.
func Count(target querysql.Selection) querysql.CountExpression { return querysql.Count(target) }

// CountAll creates a COUNT(*) aggregate.
func CountAll() querysql.CountExpression { return querysql.CountAll() }
