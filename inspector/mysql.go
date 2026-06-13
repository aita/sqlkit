package inspector

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/aita/sqlkit/meta"
)

const mysqlColumnsQuery = `
SELECT c.TABLE_NAME,
       c.COLUMN_NAME,
       c.DATA_TYPE,
       c.IS_NULLABLE = 'YES',
       c.COLUMN_DEFAULT,
       c.EXTRA LIKE '%auto_increment%',
       c.COLUMN_KEY = 'PRI',
       COALESCE(c.CHARACTER_MAXIMUM_LENGTH, 0),
       COALESCE(c.NUMERIC_PRECISION, 0),
       COALESCE(c.NUMERIC_SCALE, 0),
       c.COLUMN_COMMENT,
       COALESCE(t.TABLE_COMMENT, '')
FROM information_schema.COLUMNS c
JOIN information_schema.TABLES t
  ON t.TABLE_SCHEMA = c.TABLE_SCHEMA AND t.TABLE_NAME = c.TABLE_NAME
WHERE c.TABLE_SCHEMA = ? AND t.TABLE_TYPE = 'BASE TABLE'
ORDER BY c.TABLE_NAME, c.ORDINAL_POSITION`

// mysqlIndexesQuery reads secondary indexes (PRIMARY is implied by
// Column.PrimaryKey) with their columns in index order. Expression index
// parts have a NULL column name and are skipped.
const mysqlIndexesQuery = `
SELECT TABLE_NAME,
       INDEX_NAME,
       NON_UNIQUE = 0,
       COLUMN_NAME
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = ? AND INDEX_NAME <> 'PRIMARY' AND COLUMN_NAME IS NOT NULL
ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`

// mysqlChecksQuery reads CHECK constraints. information_schema.
// CHECK_CONSTRAINTS exists from MySQL 8.0.16 / MariaDB 10.2; on older
// servers the query fails and checks are skipped.
const mysqlChecksQuery = `
SELECT tc.TABLE_NAME, cc.CONSTRAINT_NAME, cc.CHECK_CLAUSE
FROM information_schema.TABLE_CONSTRAINTS tc
JOIN information_schema.CHECK_CONSTRAINTS cc
  ON cc.CONSTRAINT_SCHEMA = tc.CONSTRAINT_SCHEMA
 AND cc.CONSTRAINT_NAME = tc.CONSTRAINT_NAME
WHERE tc.TABLE_SCHEMA = ? AND tc.CONSTRAINT_TYPE = 'CHECK'
ORDER BY tc.TABLE_NAME, cc.CONSTRAINT_NAME`

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
func inspectMySQL(ctx context.Context, db Querier, schemaName string) ([]meta.Table, error) {
	if schemaName == "" {
		current, err := currentMySQLSchema(ctx, db)
		if err != nil {
			return nil, err
		}
		schemaName = current
	}
	if schemaName == "" {
		return nil, fmt.Errorf("inspector: no MySQL schema selected; pass a database name")
	}
	var tables []meta.Table

	rows, err := db.QueryContext(ctx, mysqlColumnsQuery, schemaName)
	if err != nil {
		return nil, fmt.Errorf("inspector: read columns: %w", err)
	}
	defer rows.Close()
	tableIndex := map[string]int{}
	for rows.Next() {
		var tableName, tableComment, dataType string
		var colDefault sql.NullString
		var autoIncrement bool
		var length, precision, scale int
		var column meta.Column
		if err := rows.Scan(&tableName, &column.Name, &dataType, &column.Nullable, &colDefault, &autoIncrement, &column.PrimaryKey, &length, &precision, &scale, &column.Comment, &tableComment); err != nil {
			return nil, fmt.Errorf("inspector: scan column: %w", err)
		}
		column.Type = columnType("mysql", dataType, length, precision, scale)
		column.HasDefault, column.DefaultExpr = columnDefault(colDefault, autoIncrement)
		index, ok := tableIndex[tableName]
		if !ok {
			index = len(tables)
			tableIndex[tableName] = index
			tables = append(tables, meta.Table{Schema: meta.Schema{Name: schemaName}, Name: tableName, Comment: tableComment})
		}
		tables[index].Columns = append(tables[index].Columns, column)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inspector: read columns: %w", err)
	}

	fkRows, err := db.QueryContext(ctx, mysqlForeignKeysQuery, schemaName)
	if err != nil {
		return nil, fmt.Errorf("inspector: read foreign keys: %w", err)
	}
	defer fkRows.Close()
	var fks []fkRow
	for fkRows.Next() {
		var row fkRow
		if err := fkRows.Scan(&row.constraint, &row.table, &row.column, &row.refSchema, &row.refTable, &row.refColumn); err != nil {
			return nil, fmt.Errorf("inspector: scan foreign key: %w", err)
		}
		fks = append(fks, row)
	}
	if err := fkRows.Err(); err != nil {
		return nil, fmt.Errorf("inspector: read foreign keys: %w", err)
	}
	attachForeignKeys(tables, fks)

	ixRows, err := db.QueryContext(ctx, mysqlIndexesQuery, schemaName)
	if err != nil {
		return nil, fmt.Errorf("inspector: read indexes: %w", err)
	}
	defer ixRows.Close()
	var indexes []indexRow
	for ixRows.Next() {
		var row indexRow
		if err := ixRows.Scan(&row.table, &row.index, &row.unique, &row.column); err != nil {
			return nil, fmt.Errorf("inspector: scan index: %w", err)
		}
		indexes = append(indexes, row)
	}
	if err := ixRows.Err(); err != nil {
		return nil, fmt.Errorf("inspector: read indexes: %w", err)
	}
	attachIndexes(tables, indexes)

	// Best-effort: CHECK_CONSTRAINTS is missing on servers older than
	// MySQL 8.0.16 / MariaDB 10.2, so a failed query skips checks rather
	// than failing the whole inspection.
	if ckRows, err := db.QueryContext(ctx, mysqlChecksQuery, schemaName); err == nil {
		defer ckRows.Close()
		for ckRows.Next() {
			var tableName, constraint, clause string
			if err := ckRows.Scan(&tableName, &constraint, &clause); err != nil {
				return nil, fmt.Errorf("inspector: scan check constraint: %w", err)
			}
			attachCheck(tables, tableName, constraint, clause)
		}
		if err := ckRows.Err(); err != nil {
			return nil, fmt.Errorf("inspector: read check constraints: %w", err)
		}
	}

	return tables, nil
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
