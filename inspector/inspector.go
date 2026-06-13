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
// or "mysql"; an empty dialect defaults to PostgreSQL) into a meta.Metadata.
// Each table carries its schema in Table.Schema. Introspection is meant for
// the initial bootstrap; afterwards the schema is maintained as a sqldef
// catalog.
func Inspect(ctx context.Context, db Querier, dialect, schemaName string) (meta.Metadata, error) {
	var tables []meta.Table
	var err error
	switch dialect {
	case "", "postgres", "postgresql", "pgx":
		tables, err = inspectPostgres(ctx, db, schemaName)
	case "mysql":
		tables, err = inspectMySQL(ctx, db, schemaName)
	default:
		return meta.Metadata{}, fmt.Errorf("inspector: unsupported dialect %q", dialect)
	}
	if err != nil {
		return meta.Metadata{}, err
	}
	return meta.Metadata{Tables: tables}, nil
}

// decimalTypes are the SQL types for which a precision/scale is meaningful.
// information_schema reports a numeric_precision for integer types too, which
// we drop so Precision/Scale stay limited to genuine decimal/numeric columns.
var decimalTypes = map[string]bool{
	"numeric": true,
	"decimal": true,
}

// columnDefault derives a column's HasDefault flag and default expression from
// its raw default and whether it is a sequence/identity ("serial") column.
// Serial defaults — identity, auto_increment, and PostgreSQL nextval()
// sequence defaults — leave the expression empty so the DDL generator can
// render them as serial/AUTO_INCREMENT; other defaults keep their expression
// so it is preserved across a round-trip.
func columnDefault(def sql.NullString, serial bool) (bool, string) {
	if serial {
		return true, ""
	}
	if !def.Valid {
		return false, ""
	}
	if strings.HasPrefix(def.String, "nextval(") {
		return true, ""
	}
	return true, def.String
}

// columnType builds the column's portable type from the single dialect the
// inspector read it on, clearing precision/scale on non-decimal types.
func columnType(dialect, name string, length, precision, scale int) meta.Type {
	if !decimalTypes[name] {
		precision, scale = 0, 0
	}
	return meta.Of(dialect, name, length, precision, scale)
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
func attachForeignKeys(tables []meta.Table, rows []fkRow) {
	tableIndex := map[string]int{}
	for i := range tables {
		tableIndex[tables[i].Name] = i
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
			fi = len(tables[ti].ForeignKeys)
			fkIndex[key] = fi
			tables[ti].ForeignKeys = append(tables[ti].ForeignKeys, meta.ForeignKey{
				Name:      row.constraint,
				RefSchema: meta.Schema{Name: row.refSchema},
				RefTable:  row.refTable,
			})
		}
		fk := &tables[ti].ForeignKeys[fi]
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
func attachIndexes(tables []meta.Table, rows []indexRow) {
	tableIndex := map[string]int{}
	for i := range tables {
		tableIndex[tables[i].Name] = i
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
			ii = len(tables[ti].Indexes)
			ixIndex[key] = ii
			tables[ti].Indexes = append(tables[ti].Indexes, meta.Index{
				Name:   row.index,
				Unique: row.unique,
			})
		}
		ix := &tables[ti].Indexes[ii]
		ix.Columns = append(ix.Columns, row.column)
	}
}

// attachCheck adds a check constraint to its table, normalizing the clause to
// the bare expression (no CHECK keyword or outer parentheses).
func attachCheck(tables []meta.Table, tableName, constraint, clause string) {
	for i := range tables {
		if tables[i].Name != tableName {
			continue
		}
		tables[i].Checks = append(tables[i].Checks, meta.Check{
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
