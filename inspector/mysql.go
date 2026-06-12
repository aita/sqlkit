package inspector

import (
	"context"
	"fmt"

	"github.com/aita/sqlkit/schema"
)

const mysqlColumnsQuery = `
SELECT c.TABLE_NAME,
       c.COLUMN_NAME,
       c.DATA_TYPE,
       c.IS_NULLABLE = 'YES',
       (c.COLUMN_DEFAULT IS NOT NULL OR c.EXTRA LIKE '%auto_increment%'),
       c.COLUMN_KEY = 'PRI',
       COALESCE(c.CHARACTER_MAXIMUM_LENGTH, 0),
       COALESCE(c.NUMERIC_PRECISION, 0),
       COALESCE(c.NUMERIC_SCALE, 0)
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

// inspectMySQL reads the schema from MySQL's information_schema. An empty schema
// resolves to the connection's current database via DATABASE().
func inspectMySQL(ctx context.Context, db Querier, schemaName string) (schema.Schema, error) {
	if schemaName == "" {
		current, err := currentMySQLSchema(ctx, db)
		if err != nil {
			return schema.Schema{}, err
		}
		schemaName = current
	}
	if schemaName == "" {
		return schema.Schema{}, fmt.Errorf("inspector: no MySQL schema selected; pass a database name")
	}
	model := schema.Schema{Name: schemaName, Dialect: "mysql"}

	rows, err := db.QueryContext(ctx, mysqlColumnsQuery, schemaName)
	if err != nil {
		return schema.Schema{}, fmt.Errorf("inspector: read columns: %w", err)
	}
	defer rows.Close()
	tableIndex := map[string]int{}
	for rows.Next() {
		var tableName string
		var column schema.Column
		if err := rows.Scan(&tableName, &column.Name, &column.DataType, &column.Nullable, &column.HasDefault, &column.PrimaryKey, &column.Length, &column.Precision, &column.Scale); err != nil {
			return schema.Schema{}, fmt.Errorf("inspector: scan column: %w", err)
		}
		normalizeNumeric(&column)
		index, ok := tableIndex[tableName]
		if !ok {
			index = len(model.Tables)
			tableIndex[tableName] = index
			model.Tables = append(model.Tables, schema.Table{Schema: schemaName, Name: tableName})
		}
		model.Tables[index].Columns = append(model.Tables[index].Columns, column)
	}
	if err := rows.Err(); err != nil {
		return schema.Schema{}, fmt.Errorf("inspector: read columns: %w", err)
	}

	fkRows, err := db.QueryContext(ctx, mysqlForeignKeysQuery, schemaName)
	if err != nil {
		return schema.Schema{}, fmt.Errorf("inspector: read foreign keys: %w", err)
	}
	defer fkRows.Close()
	var fks []fkRow
	for fkRows.Next() {
		var row fkRow
		if err := fkRows.Scan(&row.constraint, &row.table, &row.column, &row.refSchema, &row.refTable, &row.refColumn); err != nil {
			return schema.Schema{}, fmt.Errorf("inspector: scan foreign key: %w", err)
		}
		fks = append(fks, row)
	}
	if err := fkRows.Err(); err != nil {
		return schema.Schema{}, fmt.Errorf("inspector: read foreign keys: %w", err)
	}
	attachForeignKeys(&model, fks)

	return model, nil
}

func currentMySQLSchema(ctx context.Context, db Querier) (string, error) {
	rows, err := db.QueryContext(ctx, "SELECT DATABASE()")
	if err != nil {
		return "", fmt.Errorf("inspector: resolve current database: %w", err)
	}
	defer rows.Close()
	var current *string
	if rows.Next() {
		if err := rows.Scan(&current); err != nil {
			return "", fmt.Errorf("inspector: resolve current database: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("inspector: resolve current database: %w", err)
	}
	if current == nil {
		return "", nil
	}
	return *current, nil
}
