package sqlkit

import (
	stdsql "database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/aita/sqlkit/internal/names"
)

// recordColumns holds the column layout of a result set, shared by every
// Record produced from it, so each record carries only its value slice.
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

// Record is a read-only view of one result record. It stores values
// positionally against a column layout shared across the whole record set,
// so no per-record map is built.
type Record struct {
	columns *recordColumns
	values  []any
}

// Get returns a field value and whether the column is present.
func (r Record) Get(name string) (any, bool) {
	if r.columns == nil {
		return nil, false
	}
	index, ok := r.columns.indexes[name]
	if !ok {
		return nil, false
	}
	return r.values[index], true
}

// Scan copies this record into dest using `sqlkit` tags or exported field
// names. Dest must be a non-nil pointer to a struct.
func (r Record) Scan(dest any) error {
	value := reflect.ValueOf(dest)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return errRecordNonNilPointer("scan")
	}
	if value.Elem().Kind() != reflect.Struct {
		return errRecordStructPointer("scan")
	}
	plan := recordScanPlan(value.Elem().Type(), r.columns)
	return scanRecordWithPlan(r, value.Elem(), plan)
}

// fieldColumn pairs a struct field index with the record value index it is
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

func scanRecordWithPlan(record Record, dest reflect.Value, plan []fieldColumn) error {
	for _, entry := range plan {
		if err := assignRecordValue(dest.Field(entry.field), record.values[entry.column]); err != nil {
			return fmt.Errorf("field %s: %w", dest.Type().Field(entry.field).Name, err)
		}
	}
	return nil
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

func assignRecordValue(dest reflect.Value, value any) error {
	// database/sql Scanner implementations decide for themselves how to
	// accept the value, including NULL (e.g. sql.NullString).
	if dest.CanAddr() {
		if scanner, ok := dest.Addr().Interface().(stdsql.Scanner); ok {
			return scanner.Scan(value)
		}
	}
	if value == nil {
		return nil
	}
	// Nullable destinations may be pointers: allocate and fill.
	if dest.Kind() == reflect.Pointer {
		elem := reflect.New(dest.Type().Elem())
		if err := assignRecordValue(elem.Elem(), value); err != nil {
			return err
		}
		dest.Set(elem)
		return nil
	}
	source := reflect.ValueOf(value)
	if source.Type().AssignableTo(dest.Type()) {
		dest.Set(source)
		return nil
	}
	if source.Type().ConvertibleTo(dest.Type()) {
		dest.Set(source.Convert(dest.Type()))
		return nil
	}
	return fmt.Errorf("cannot assign %s to %s", source.Type(), dest.Type())
}

func errRecordNonNilPointer(operation string) error {
	return fmt.Errorf("sqlkit: %s destination must be a non-nil pointer", operation)
}

func errRecordStructPointer(operation string) error {
	return fmt.Errorf("sqlkit: %s destination must point to a struct", operation)
}
