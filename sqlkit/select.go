package sqlkit

import querysql "github.com/sqlkit-dev/sqlkit/sqlkit/sql"

// SelectQuery is the runtime representation for a SELECT statement.
type SelectQuery = querysql.SelectQuery

// Select creates a SELECT query builder.
func Select(columns ...querysql.Selection) SelectQuery { return querysql.Select(columns...) }
