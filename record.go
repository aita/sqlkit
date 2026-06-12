package sqlkit

import (
	stdsql "database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/aita/sqlkit/internal/names"
)

// recordColumns holds the column layout of a result set, shared by every
// Record produced from it.
type recordColumns struct {
	names   []string
	indexes map[string]int
}

func newRecordColumns(columnNames []string) *recordColumns {
	indexes := make(map[string]int, len(columnNames))
	for i, name := range columnNames {
		if _, ok := indexes[name]; !ok {
			indexes[name] = i
		}
	}
	return &recordColumns{names: append([]string(nil), columnNames...), indexes: indexes}
}

// Record is a lightweight, read-only view over the current row of the
// underlying sql.Rows: it holds no row data itself, so iterating a RecordSet
// allocates nothing per record. Like sql.Rows, a Record is only valid until
// the iteration advances or ends; copy values out with Scan before moving on
// instead of retaining the Record. Scanning a record retained from an
// earlier iteration step returns an error instead of silently reading
// whatever row the cursor has advanced to.
type Record struct {
	rows    *stdsql.Rows
	columns *recordColumns
	// row is the row number this record was yielded for; current points at
	// the iteration's live row counter. They diverge once the iteration
	// advances, which lets Scan reject stale records: every yielded record
	// shares the same sql.Rows cursor, so without this check a retained
	// record would scan a different row than the one it was yielded for.
	row     int
	current *int
}

// Scan copies the current record into dest using `sqlkit` tags or exported
// field names. Dest must be a non-nil pointer to a struct. Values go through
// database/sql scanning straight into the fields, with no intermediate
// per-record allocation. Scanning the same record again simply re-reads the
// current row: sql.Rows.Scan re-converts the buffered row values, so this
// needs no guard (database/sql only rejects a repeated Scan when the
// previous one used *sql.RawBytes). Scanning a record from an earlier
// iteration step, however, returns an error: the shared cursor has moved
// on, and scanning would silently read a different row.
func (r Record) Scan(dest any) error {
	value := reflect.ValueOf(dest)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return errRecordNonNilPointer("scan")
	}
	if value.Elem().Kind() != reflect.Struct {
		return errRecordStructPointer("scan")
	}
	return scanRecordInto(r, value.Elem())
}

func scanRecordInto(r Record, dest reflect.Value) error {
	if r.rows == nil || r.columns == nil {
		return fmt.Errorf("sqlkit: scan on a zero Record")
	}
	if r.current != nil && r.row != *r.current {
		return fmt.Errorf("sqlkit: stale record: the iteration has advanced past this record's row")
	}
	plan := recordScanPlan(dest.Type(), r.columns)
	return r.rows.Scan(recordScanTargets(dest, plan, len(r.columns.names))...)
}

// fieldColumn pairs a struct field index with the column position it is
// filled from.
type fieldColumn struct {
	field  int
	column int
}

// recordScanPlan resolves struct fields to column positions once, so
// scanning a stream of records does no per-record name lookups. Columns are
// matched by `sqlkit` tag, exact field name, then snake_case field name.
func recordScanPlan(structType reflect.Type, columns *recordColumns) []fieldColumn {
	if columns == nil {
		return nil
	}
	plan := make([]fieldColumn, 0, structType.NumField())
	for i := 0; i < structType.NumField(); i++ {
		structField := structType.Field(i)
		if structField.PkgPath != "" {
			continue
		}
		columnName := recordFieldColumnName(structField)
		if columnName == "-" {
			continue
		}
		index, ok := columns.indexes[columnName]
		if !ok {
			index, ok = columns.indexes[structField.Name]
		}
		if !ok {
			index, ok = columns.indexes[names.SnakeCase(structField.Name)]
		}
		if !ok {
			continue
		}
		plan = append(plan, fieldColumn{field: i, column: index})
	}
	return plan
}

// recordScanTargets builds one sql.Rows.Scan target per column: planned
// columns point at their struct field, the rest at a shared discard slot.
func recordScanTargets(dest reflect.Value, plan []fieldColumn, columnCount int) []any {
	var discard any
	targets := make([]any, columnCount)
	for i := range targets {
		targets[i] = &discard
	}
	for _, entry := range plan {
		targets[entry.column] = dest.Field(entry.field).Addr().Interface()
	}
	return targets
}

func recordFieldColumnName(field reflect.StructField) string {
	if tag, ok := field.Tag.Lookup("sqlkit"); ok {
		name := strings.Split(tag, ",")[0]
		if name != "" {
			return name
		}
	}
	return field.Name
}

func errRecordNonNilPointer(operation string) error {
	return fmt.Errorf("sqlkit: %s destination must be a non-nil pointer", operation)
}

func errRecordStructPointer(operation string) error {
	return fmt.Errorf("sqlkit: %s destination must point to a struct", operation)
}
