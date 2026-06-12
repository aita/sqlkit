package loader

import (
	"context"
	"fmt"

	"github.com/sqlkit-dev/sqlkit/codegen"
)

const mysqlColumnsQuery = `
SELECT c.TABLE_NAME,
       c.COLUMN_NAME,
       c.DATA_TYPE,
       c.IS_NULLABLE = 'YES',
       (c.COLUMN_DEFAULT IS NOT NULL OR c.EXTRA LIKE '%auto_increment%'),
       c.COLUMN_KEY = 'PRI'
FROM information_schema.COLUMNS c
JOIN information_schema.TABLES t
  ON t.TABLE_SCHEMA = c.TABLE_SCHEMA AND t.TABLE_NAME = c.TABLE_NAME
WHERE c.TABLE_SCHEMA = ? AND t.TABLE_TYPE = 'BASE TABLE'
ORDER BY c.TABLE_NAME, c.ORDINAL_POSITION`

const mysqlForeignKeysQuery = `
SELECT CONSTRAINT_NAME,
       TABLE_NAME,
       COLUMN_NAME,
       REFERENCED_TABLE_SCHEMA,
       REFERENCED_TABLE_NAME,
       REFERENCED_COLUMN_NAME
FROM information_schema.KEY_COLUMN_USAGE
WHERE TABLE_SCHEMA = ? AND REFERENCED_TABLE_NAME IS NOT NULL
ORDER BY CONSTRAINT_NAME, ORDINAL_POSITION`

// loadMySQL reads the schema from MySQL's information_schema. An empty schema
// resolves to the connection's current database via DATABASE().
func loadMySQL(ctx context.Context, db Querier, schema string) (codegen.Schema, error) {
	if schema == "" {
		current, err := currentMySQLSchema(ctx, db)
		if err != nil {
			return codegen.Schema{}, err
		}
		schema = current
	}
	if schema == "" {
		return codegen.Schema{}, fmt.Errorf("loader: no MySQL schema selected; pass a database name")
	}
	model := codegen.Schema{Name: schema, Dialect: "mysql"}

	rows, err := db.QueryContext(ctx, mysqlColumnsQuery, schema)
	if err != nil {
		return codegen.Schema{}, fmt.Errorf("loader: read columns: %w", err)
	}
	defer rows.Close()
	tableIndex := map[string]int{}
	for rows.Next() {
		var tableName string
		var column codegen.Column
		if err := rows.Scan(&tableName, &column.Name, &column.DataType, &column.Nullable, &column.HasDefault, &column.PrimaryKey); err != nil {
			return codegen.Schema{}, fmt.Errorf("loader: scan column: %w", err)
		}
		index, ok := tableIndex[tableName]
		if !ok {
			index = len(model.Tables)
			tableIndex[tableName] = index
			model.Tables = append(model.Tables, codegen.Table{Schema: schema, Name: tableName})
		}
		model.Tables[index].Columns = append(model.Tables[index].Columns, column)
	}
	if err := rows.Err(); err != nil {
		return codegen.Schema{}, fmt.Errorf("loader: read columns: %w", err)
	}

	fkRows, err := db.QueryContext(ctx, mysqlForeignKeysQuery, schema)
	if err != nil {
		return codegen.Schema{}, fmt.Errorf("loader: read foreign keys: %w", err)
	}
	defer fkRows.Close()
	var fks []fkRow
	for fkRows.Next() {
		var row fkRow
		if err := fkRows.Scan(&row.constraint, &row.table, &row.column, &row.refSchema, &row.refTable, &row.refColumn); err != nil {
			return codegen.Schema{}, fmt.Errorf("loader: scan foreign key: %w", err)
		}
		fks = append(fks, row)
	}
	if err := fkRows.Err(); err != nil {
		return codegen.Schema{}, fmt.Errorf("loader: read foreign keys: %w", err)
	}
	attachForeignKeys(&model, fks)

	return model, nil
}

func currentMySQLSchema(ctx context.Context, db Querier) (string, error) {
	rows, err := db.QueryContext(ctx, "SELECT DATABASE()")
	if err != nil {
		return "", fmt.Errorf("loader: resolve current database: %w", err)
	}
	defer rows.Close()
	var schema *string
	if rows.Next() {
		if err := rows.Scan(&schema); err != nil {
			return "", fmt.Errorf("loader: resolve current database: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("loader: resolve current database: %w", err)
	}
	if schema == nil {
		return "", nil
	}
	return *schema, nil
}
