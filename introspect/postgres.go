package introspect

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/aita/sqlkit/meta"
)

const pgColumnsQuery = `
SELECT c.table_name,
       c.column_name,
       c.udt_name,
       c.is_nullable = 'YES',
       c.column_default,
       c.is_identity = 'YES',
       COALESCE(c.character_maximum_length, 0),
       COALESCE(c.numeric_precision, 0),
       COALESCE(c.numeric_scale, 0)
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
// mispair and can be corrected with a custom introspection if needed.
const pgForeignKeysQuery = `
SELECT tc.constraint_name,
       tc.table_name,
       kcu.column_name,
       ccu.table_schema,
       ccu.table_name,
       ccu.column_name,
       rc.delete_rule,
       rc.update_rule
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu
  ON kcu.constraint_name = tc.constraint_name
 AND kcu.constraint_schema = tc.constraint_schema
JOIN information_schema.constraint_column_usage ccu
  ON ccu.constraint_name = tc.constraint_name
 AND ccu.constraint_schema = tc.constraint_schema
JOIN information_schema.referential_constraints rc
  ON rc.constraint_name = tc.constraint_name
 AND rc.constraint_schema = tc.constraint_schema
WHERE tc.table_schema = $1 AND tc.constraint_type = 'FOREIGN KEY'
ORDER BY tc.constraint_name, kcu.ordinal_position`

// pgIndexesQuery reads secondary indexes (the primary key index is implied by
// Column.PrimaryKey) with their key columns in index order. Index forms the
// schema model cannot represent are skipped so they are never imported as a
// plain index: expression indexes (indexprs) and partial indexes (indpred).
// INCLUDE columns are dropped (k.ord > indnkeyatts) so only the key columns
// remain.
const pgIndexesQuery = `
SELECT t.relname,
       i.relname,
       ix.indisunique,
       a.attname
FROM pg_index ix
JOIN pg_class t ON t.oid = ix.indrelid
JOIN pg_class i ON i.oid = ix.indexrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
JOIN LATERAL unnest(ix.indkey) WITH ORDINALITY AS k(attnum, ord) ON true
JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
WHERE n.nspname = $1 AND t.relkind = 'r' AND NOT ix.indisprimary
  AND ix.indexprs IS NULL AND ix.indpred IS NULL AND k.ord <= ix.indnkeyatts
ORDER BY t.relname, i.relname, k.ord`

// pgChecksQuery reads CHECK constraints; pg_get_constraintdef renders the
// clause as "CHECK ((expr))".
const pgChecksQuery = `
SELECT t.relname, con.conname, pg_get_constraintdef(con.oid)
FROM pg_constraint con
JOIN pg_class t ON t.oid = con.conrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
WHERE n.nspname = $1 AND con.contype = 'c'
ORDER BY t.relname, con.conname`

// pgCommentsQuery reads table comments (objsubid = 0, empty column name) and
// column comments from pg_description.
const pgCommentsQuery = `
SELECT c.relname, COALESCE(a.attname, ''), d.description
FROM pg_description d
JOIN pg_class c ON c.oid = d.objoid
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = d.objsubid AND d.objsubid > 0
WHERE n.nspname = $1 AND c.relkind = 'r' AND d.classoid = 'pg_class'::regclass`

// inspectPostgres reads the schema from PostgreSQL's information_schema.
func inspectPostgres(ctx context.Context, db Querier, schemaName string) ([]meta.Table, error) {
	if schemaName == "" {
		schemaName = "public"
	}
	var tables []meta.Table

	rows, err := db.QueryContext(ctx, pgColumnsQuery, schemaName)
	if err != nil {
		return nil, fmt.Errorf("introspection: read columns: %w", err)
	}
	defer rows.Close()
	tableIndex := map[string]int{}
	for rows.Next() {
		var tableName, udt string
		var colDefault sql.NullString
		var isIdentity bool
		var length, precision, scale int
		var column meta.Column
		if err := rows.Scan(&tableName, &column.Name, &udt, &column.Nullable, &colDefault, &isIdentity, &length, &precision, &scale); err != nil {
			return nil, fmt.Errorf("introspection: scan column: %w", err)
		}
		column.Type = columnType("postgres", udt, length, precision, scale)
		column.HasDefault, column.DefaultExpr = columnDefault(colDefault, isIdentity)
		index, ok := tableIndex[tableName]
		if !ok {
			index = len(tables)
			tableIndex[tableName] = index
			tables = append(tables, meta.Table{Schema: meta.Schema{Name: schemaName}, Name: tableName})
		}
		tables[index].Columns = append(tables[index].Columns, column)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("introspection: read columns: %w", err)
	}

	pkRows, err := db.QueryContext(ctx, pgPrimaryKeysQuery, schemaName)
	if err != nil {
		return nil, fmt.Errorf("introspection: read primary keys: %w", err)
	}
	defer pkRows.Close()
	for pkRows.Next() {
		var tableName, columnName string
		if err := pkRows.Scan(&tableName, &columnName); err != nil {
			return nil, fmt.Errorf("introspection: scan primary key: %w", err)
		}
		index, ok := tableIndex[tableName]
		if !ok {
			continue
		}
		columns := tables[index].Columns
		for i := range columns {
			if columns[i].Name == columnName {
				columns[i].PrimaryKey = true
			}
		}
	}
	if err := pkRows.Err(); err != nil {
		return nil, fmt.Errorf("introspection: read primary keys: %w", err)
	}

	fkRows, err := db.QueryContext(ctx, pgForeignKeysQuery, schemaName)
	if err != nil {
		return nil, fmt.Errorf("introspection: read foreign keys: %w", err)
	}
	defer fkRows.Close()
	var fks []fkRow
	for fkRows.Next() {
		var row fkRow
		if err := fkRows.Scan(&row.constraint, &row.table, &row.column, &row.refSchema, &row.refTable, &row.refColumn, &row.onDelete, &row.onUpdate); err != nil {
			return nil, fmt.Errorf("introspection: scan foreign key: %w", err)
		}
		fks = append(fks, row)
	}
	if err := fkRows.Err(); err != nil {
		return nil, fmt.Errorf("introspection: read foreign keys: %w", err)
	}
	attachForeignKeys(tables, fks)

	ixRows, err := db.QueryContext(ctx, pgIndexesQuery, schemaName)
	if err != nil {
		return nil, fmt.Errorf("introspection: read indexes: %w", err)
	}
	defer ixRows.Close()
	var indexes []indexRow
	for ixRows.Next() {
		var row indexRow
		if err := ixRows.Scan(&row.table, &row.index, &row.unique, &row.column); err != nil {
			return nil, fmt.Errorf("introspection: scan index: %w", err)
		}
		indexes = append(indexes, row)
	}
	if err := ixRows.Err(); err != nil {
		return nil, fmt.Errorf("introspection: read indexes: %w", err)
	}
	attachIndexes(tables, indexes)

	ckRows, err := db.QueryContext(ctx, pgChecksQuery, schemaName)
	if err != nil {
		return nil, fmt.Errorf("introspection: read check constraints: %w", err)
	}
	defer ckRows.Close()
	for ckRows.Next() {
		var tableName, constraint, clause string
		if err := ckRows.Scan(&tableName, &constraint, &clause); err != nil {
			return nil, fmt.Errorf("introspection: scan check constraint: %w", err)
		}
		attachCheck(tables, tableName, constraint, clause)
	}
	if err := ckRows.Err(); err != nil {
		return nil, fmt.Errorf("introspection: read check constraints: %w", err)
	}

	cRows, err := db.QueryContext(ctx, pgCommentsQuery, schemaName)
	if err != nil {
		return nil, fmt.Errorf("introspection: read comments: %w", err)
	}
	defer cRows.Close()
	for cRows.Next() {
		var tableName, columnName, comment string
		if err := cRows.Scan(&tableName, &columnName, &comment); err != nil {
			return nil, fmt.Errorf("introspection: scan comment: %w", err)
		}
		index, ok := tableIndex[tableName]
		if !ok {
			continue
		}
		if columnName == "" {
			tables[index].Comment = comment
			continue
		}
		columns := tables[index].Columns
		for i := range columns {
			if columns[i].Name == columnName {
				columns[i].Comment = comment
			}
		}
	}
	if err := cRows.Err(); err != nil {
		return nil, fmt.Errorf("introspection: read comments: %w", err)
	}

	return tables, nil
}
