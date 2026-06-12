// Package loader reads database schema metadata into the generator model.
// PostgreSQL is implemented; the dialect switch is the extension point for
// MySQL and others.
package loader

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/sqlkit-dev/sqlkit/internal/genmodel"
)

// Querier is the subset of *sql.DB used by the loader.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

const columnsQuery = `
SELECT c.table_name,
       c.column_name,
       c.udt_name,
       c.is_nullable = 'YES',
       c.column_default IS NOT NULL OR c.is_identity = 'YES'
FROM information_schema.columns c
JOIN information_schema.tables t
  ON t.table_schema = c.table_schema AND t.table_name = c.table_name
WHERE c.table_schema = $1 AND t.table_type = 'BASE TABLE'
ORDER BY c.table_name, c.ordinal_position`

const primaryKeysQuery = `
SELECT tc.table_name, kcu.column_name
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu
  ON kcu.constraint_name = tc.constraint_name
 AND kcu.constraint_schema = tc.constraint_schema
WHERE tc.table_schema = $1 AND tc.constraint_type = 'PRIMARY KEY'`

// Load reads all base tables of a schema for the given dialect ("postgres"
// or empty today; "mysql" is reserved and reports that it is not implemented
// yet).
func Load(ctx context.Context, db Querier, dialect, schema string) (genmodel.Schema, error) {
	switch dialect {
	case "", "postgres":
		return loadPostgres(ctx, db, schema)
	case "mysql":
		return genmodel.Schema{}, fmt.Errorf("loader: mysql schema loading is not implemented yet (the query builder supports MySQL via sql.MySQLDialect)")
	default:
		return genmodel.Schema{}, fmt.Errorf("loader: unsupported dialect %q", dialect)
	}
}

// loadPostgres reads the schema from PostgreSQL's information_schema.
func loadPostgres(ctx context.Context, db Querier, schema string) (genmodel.Schema, error) {
	if schema == "" {
		schema = "public"
	}
	model := genmodel.Schema{Name: schema}

	rows, err := db.QueryContext(ctx, columnsQuery, schema)
	if err != nil {
		return genmodel.Schema{}, fmt.Errorf("loader: read columns: %w", err)
	}
	defer rows.Close()
	tableIndex := map[string]int{}
	for rows.Next() {
		var tableName string
		var column genmodel.Column
		if err := rows.Scan(&tableName, &column.Name, &column.DataType, &column.Nullable, &column.HasDefault); err != nil {
			return genmodel.Schema{}, fmt.Errorf("loader: scan column: %w", err)
		}
		index, ok := tableIndex[tableName]
		if !ok {
			index = len(model.Tables)
			tableIndex[tableName] = index
			model.Tables = append(model.Tables, genmodel.Table{Schema: schema, Name: tableName})
		}
		model.Tables[index].Columns = append(model.Tables[index].Columns, column)
	}
	if err := rows.Err(); err != nil {
		return genmodel.Schema{}, fmt.Errorf("loader: read columns: %w", err)
	}

	pkRows, err := db.QueryContext(ctx, primaryKeysQuery, schema)
	if err != nil {
		return genmodel.Schema{}, fmt.Errorf("loader: read primary keys: %w", err)
	}
	defer pkRows.Close()
	for pkRows.Next() {
		var tableName, columnName string
		if err := pkRows.Scan(&tableName, &columnName); err != nil {
			return genmodel.Schema{}, fmt.Errorf("loader: scan primary key: %w", err)
		}
		index, ok := tableIndex[tableName]
		if !ok {
			continue
		}
		columns := model.Tables[index].Columns
		for i := range columns {
			if columns[i].Name == columnName {
				columns[i].PrimaryKey = true
			}
		}
	}
	if err := pkRows.Err(); err != nil {
		return genmodel.Schema{}, fmt.Errorf("loader: read primary keys: %w", err)
	}
	return model, nil
}
