package inspector

import (
	"context"
	"fmt"

	"github.com/sqlkit-dev/sqlkit/schema"
)

const pgColumnsQuery = `
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

const pgPrimaryKeysQuery = `
SELECT tc.table_name, kcu.column_name
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu
  ON kcu.constraint_name = tc.constraint_name
 AND kcu.constraint_schema = tc.constraint_schema
WHERE tc.table_schema = $1 AND tc.constraint_type = 'PRIMARY KEY'`

// pgForeignKeysQuery reads foreign-key column pairs. constraint_column_usage is
// reliable for single-column foreign keys (the common case); composite keys may
// mispair and can be corrected with a custom inspector if needed.
const pgForeignKeysQuery = `
SELECT tc.constraint_name,
       tc.table_name,
       kcu.column_name,
       ccu.table_schema,
       ccu.table_name,
       ccu.column_name
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu
  ON kcu.constraint_name = tc.constraint_name
 AND kcu.constraint_schema = tc.constraint_schema
JOIN information_schema.constraint_column_usage ccu
  ON ccu.constraint_name = tc.constraint_name
 AND ccu.constraint_schema = tc.constraint_schema
WHERE tc.table_schema = $1 AND tc.constraint_type = 'FOREIGN KEY'
ORDER BY tc.constraint_name, kcu.ordinal_position`

// inspectPostgres reads the schema from PostgreSQL's information_schema.
func inspectPostgres(ctx context.Context, db Querier, schemaName string) (schema.Schema, error) {
	if schemaName == "" {
		schemaName = "public"
	}
	model := schema.Schema{Name: schemaName, Dialect: "postgres"}

	rows, err := db.QueryContext(ctx, pgColumnsQuery, schemaName)
	if err != nil {
		return schema.Schema{}, fmt.Errorf("inspector: read columns: %w", err)
	}
	defer rows.Close()
	tableIndex := map[string]int{}
	for rows.Next() {
		var tableName string
		var column schema.Column
		if err := rows.Scan(&tableName, &column.Name, &column.DataType, &column.Nullable, &column.HasDefault); err != nil {
			return schema.Schema{}, fmt.Errorf("inspector: scan column: %w", err)
		}
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

	pkRows, err := db.QueryContext(ctx, pgPrimaryKeysQuery, schemaName)
	if err != nil {
		return schema.Schema{}, fmt.Errorf("inspector: read primary keys: %w", err)
	}
	defer pkRows.Close()
	for pkRows.Next() {
		var tableName, columnName string
		if err := pkRows.Scan(&tableName, &columnName); err != nil {
			return schema.Schema{}, fmt.Errorf("inspector: scan primary key: %w", err)
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
		return schema.Schema{}, fmt.Errorf("inspector: read primary keys: %w", err)
	}

	fkRows, err := db.QueryContext(ctx, pgForeignKeysQuery, schemaName)
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
