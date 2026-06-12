// Package loader reads database schema metadata into the codegen model. Both
// PostgreSQL and MySQL are supported; the dialect switch is the extension point
// for other engines.
package loader

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/sqlkit-dev/sqlkit/codegen"
)

// Querier is the subset of *sql.DB used by the loader.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Load reads all base tables of a schema for the given dialect ("postgres" or
// "mysql"; an empty dialect defaults to PostgreSQL).
func Load(ctx context.Context, db Querier, dialect, schema string) (codegen.Schema, error) {
	switch dialect {
	case "", "postgres", "postgresql", "pgx":
		return loadPostgres(ctx, db, schema)
	case "mysql":
		return loadMySQL(ctx, db, schema)
	default:
		return codegen.Schema{}, fmt.Errorf("loader: unsupported dialect %q", dialect)
	}
}

// fkRow is one referencing column of a foreign-key constraint.
type fkRow struct {
	constraint string
	table      string
	column     string
	refSchema  string
	refTable   string
	refColumn  string
}

// attachForeignKeys groups foreign-key rows by (table, constraint) and attaches
// them to the matching tables, preserving column order.
func attachForeignKeys(model *codegen.Schema, rows []fkRow) {
	tableIndex := map[string]int{}
	for i := range model.Tables {
		tableIndex[model.Tables[i].Name] = i
	}
	// key -> index of the ForeignKey within its table.
	fkIndex := map[string]int{}
	for _, row := range rows {
		ti, ok := tableIndex[row.table]
		if !ok {
			continue
		}
		key := row.table + "\x00" + row.constraint
		fi, ok := fkIndex[key]
		if !ok {
			fi = len(model.Tables[ti].ForeignKeys)
			fkIndex[key] = fi
			model.Tables[ti].ForeignKeys = append(model.Tables[ti].ForeignKeys, codegen.ForeignKey{
				Name:      row.constraint,
				RefSchema: row.refSchema,
				RefTable:  row.refTable,
			})
		}
		fk := &model.Tables[ti].ForeignKeys[fi]
		fk.Columns = append(fk.Columns, row.column)
		fk.RefColumns = append(fk.RefColumns, row.refColumn)
	}
}
