// Package inspector reads database schema metadata into the schema model. Both
// PostgreSQL and MySQL are supported; the dialect switch is the extension point
// for other engines.
package inspector

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/aita/sqlkit/meta"
)

// Querier is the subset of *sql.DB used by the inspector.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Inspect reads all base tables of a schema for the given dialect ("postgres"
// or "mysql"; an empty dialect defaults to PostgreSQL).
func Inspect(ctx context.Context, db Querier, dialect, schemaName string) (meta.Schema, error) {
	switch dialect {
	case "", "postgres", "postgresql", "pgx":
		return inspectPostgres(ctx, db, schemaName)
	case "mysql":
		return inspectMySQL(ctx, db, schemaName)
	default:
		return meta.Schema{}, fmt.Errorf("inspector: unsupported dialect %q", dialect)
	}
}

// decimalTypes are the SQL types for which a precision/scale is meaningful.
// information_schema reports a numeric_precision for integer types too, which
// we drop so Precision/Scale stay limited to genuine decimal/numeric columns.
var decimalTypes = map[string]bool{
	"numeric": true,
	"decimal": true,
}

// normalizeNumeric clears precision/scale on non-decimal columns.
func normalizeNumeric(column *meta.Column) {
	if !decimalTypes[column.DataType] {
		column.Precision = 0
		column.Scale = 0
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
func attachForeignKeys(model *meta.Schema, rows []fkRow) {
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
			model.Tables[ti].ForeignKeys = append(model.Tables[ti].ForeignKeys, meta.ForeignKey{
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

// indexRow is one column of a secondary index.
type indexRow struct {
	table  string
	index  string
	unique bool
	column string
}

// attachIndexes groups index rows by (table, index) and attaches them to the
// matching tables, preserving column order.
func attachIndexes(model *meta.Schema, rows []indexRow) {
	tableIndex := map[string]int{}
	for i := range model.Tables {
		tableIndex[model.Tables[i].Name] = i
	}
	ixIndex := map[string]int{}
	for _, row := range rows {
		ti, ok := tableIndex[row.table]
		if !ok {
			continue
		}
		key := row.table + "\x00" + row.index
		ii, ok := ixIndex[key]
		if !ok {
			ii = len(model.Tables[ti].Indexes)
			ixIndex[key] = ii
			model.Tables[ti].Indexes = append(model.Tables[ti].Indexes, meta.Index{
				Name:   row.index,
				Unique: row.unique,
			})
		}
		ix := &model.Tables[ti].Indexes[ii]
		ix.Columns = append(ix.Columns, row.column)
	}
}

// attachCheck adds a check constraint to its table, normalizing the clause to
// the bare expression (no CHECK keyword or outer parentheses).
func attachCheck(model *meta.Schema, tableName, constraint, clause string) {
	for i := range model.Tables {
		if model.Tables[i].Name != tableName {
			continue
		}
		model.Tables[i].Checks = append(model.Tables[i].Checks, meta.Check{
			Name:       constraint,
			Expression: normalizeCheckClause(clause),
		})
		return
	}
}

// normalizeCheckClause strips the CHECK keyword and one level of enclosing
// parentheses, as engines report clauses like "CHECK ((price > 0))" or
// "(`price` > 0)".
func normalizeCheckClause(clause string) string {
	expr := strings.TrimSpace(clause)
	if rest, ok := strings.CutPrefix(expr, "CHECK"); ok {
		expr = strings.TrimSpace(rest)
	}
	for strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") && balancedParens(expr[1:len(expr)-1]) {
		expr = strings.TrimSpace(expr[1 : len(expr)-1])
	}
	return expr
}

// balancedParens reports whether the parentheses in s are balanced without
// the depth dipping below zero, i.e. whether stripping an outer pair is safe.
func balancedParens(s string) bool {
	depth := 0
	for _, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}
