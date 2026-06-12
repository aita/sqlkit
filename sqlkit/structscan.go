package sqlkit

import (
	stdsql "database/sql"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/sqlkit-dev/sqlkit/internal/names"
	"github.com/sqlkit-dev/sqlkit/sqlkit/sql"
)

// resolveQuery turns a raw SQL string (with args) or a built sql.Query into
// SQL text plus arguments. It backs the Database/Session Query escape hatch.
func resolveQuery(dialect sql.Dialect, query any, args []any) (string, []any, error) {
	switch q := query.(type) {
	case string:
		return q, args, nil
	case sql.Query:
		if len(args) > 0 {
			return "", nil, fmt.Errorf("sqlkit: extra args are not allowed with a built query; bind values in the builder")
		}
		return q.ToSQL(dialect)
	default:
		return "", nil, errUnsupportedQueryType(query)
	}
}

func errUnsupportedQueryType(query any) error {
	return fmt.Errorf("sqlkit: query must be a SQL string or sql.Query, got %T", query)
}

// structScanAll scans every row into dest, a pointer to a slice.
func structScanAll(rows *stdsql.Rows, dest any) error {
	dv := reflect.ValueOf(dest)
	if dv.Kind() != reflect.Pointer || dv.IsNil() {
		return fmt.Errorf("sqlkit: Select destination must be a non-nil pointer to a slice, got %T", dest)
	}
	slice := dv.Elem()
	if slice.Kind() != reflect.Slice {
		return fmt.Errorf("sqlkit: Select destination must point to a slice, got %T", dest)
	}
	elemType := slice.Type().Elem()
	pointerElems := elemType.Kind() == reflect.Pointer
	baseType := elemType
	if pointerElems {
		baseType = elemType.Elem()
	}

	scan, err := rowScanner(rows, baseType)
	if err != nil {
		return err
	}
	result := reflect.MakeSlice(slice.Type(), 0, 8)
	for rows.Next() {
		item := reflect.New(baseType)
		if err := scan(item.Elem()); err != nil {
			return err
		}
		if pointerElems {
			result = reflect.Append(result, item)
		} else {
			result = reflect.Append(result, item.Elem())
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	slice.Set(result)
	return nil
}

// structScanOne scans the first row into dest, a pointer to a struct or
// scalar, reporting whether a row was present.
func structScanOne(rows *stdsql.Rows, dest any) (bool, error) {
	dv := reflect.ValueOf(dest)
	if dv.Kind() != reflect.Pointer || dv.IsNil() {
		return false, fmt.Errorf("sqlkit: Get destination must be a non-nil pointer, got %T", dest)
	}
	scan, err := rowScanner(rows, dv.Type().Elem())
	if err != nil {
		return false, err
	}
	if !rows.Next() {
		return false, rows.Err()
	}
	if err := scan(dv.Elem()); err != nil {
		return false, err
	}
	return true, rows.Err()
}

// rowScanner returns a function that scans the current row into a value of
// the given type, mapping columns to struct fields when the type is a struct.
func rowScanner(rows *stdsql.Rows, t reflect.Type) (func(reflect.Value) error, error) {
	if !mapsAsStruct(t) {
		return func(v reflect.Value) error {
			return rows.Scan(v.Addr().Interface())
		}, nil
	}
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	fields := fieldsByColumn(t)
	paths := make([][]int, len(columns))
	for i, column := range columns {
		path, ok := fields[strings.ToLower(column)]
		if !ok {
			return nil, fmt.Errorf("sqlkit: no field in %s for column %q", t, column)
		}
		paths[i] = path
	}
	return func(v reflect.Value) error {
		dests := make([]any, len(paths))
		for i, path := range paths {
			dests[i] = v.FieldByIndex(path).Addr().Interface()
		}
		return rows.Scan(dests...)
	}, nil
}

var (
	scannerType = reflect.TypeOf((*stdsql.Scanner)(nil)).Elem()
	timeType    = reflect.TypeOf(time.Time{})
)

// mapsAsStruct reports whether values of t are filled field-by-field rather
// than scanned as a single column.
func mapsAsStruct(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	if t == timeType {
		return false
	}
	if t.Implements(scannerType) || reflect.PointerTo(t).Implements(scannerType) {
		return false
	}
	return true
}

// fieldsByColumn maps lowercased column names to field index paths. Fields
// register under their `sqlkit` tag when present, otherwise under both the
// lowercased and snake_case forms of the field name. Embedded structs are
// flattened (except types that scan as a single column, such as time.Time
// and sql.Scanner implementations); shallower fields win conflicts.
func fieldsByColumn(t reflect.Type) map[string][]int {
	out := map[string][]int{}
	collectFields(t, nil, out)
	return out
}

func collectFields(t reflect.Type, prefix []int, out map[string][]int) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		path := append(append([]int(nil), prefix...), i)
		tag := fieldTag(field)
		if tag == "-" {
			continue
		}
		// Embedded structs are flattened; exported fields promoted through
		// an unexported embedded type remain settable.
		if tag == "" && field.Anonymous && mapsAsStruct(field.Type) {
			collectFields(field.Type, path, out)
			continue
		}
		if field.PkgPath != "" {
			continue
		}
		if tag != "" {
			register(out, strings.ToLower(tag), path)
			continue
		}
		register(out, strings.ToLower(field.Name), path)
		register(out, names.SnakeCase(field.Name), path)
	}
}

func register(out map[string][]int, key string, path []int) {
	if existing, ok := out[key]; ok && len(existing) <= len(path) {
		return
	}
	out[key] = path
}

func fieldTag(field reflect.StructField) string {
	if tag, ok := field.Tag.Lookup("sqlkit"); ok {
		name := strings.Split(tag, ",")[0]
		if name != "" {
			return name
		}
	}
	return ""
}
