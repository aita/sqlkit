package sqlkit

import (
	stdsql "database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/aita/sqlkit/internal/reflectmap"
	"github.com/aita/sqlkit/sql"
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
	if !reflectmap.MapsAsStruct(t) {
		return func(v reflect.Value) error {
			return rows.Scan(v.Addr().Interface())
		}, nil
	}
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	fields := reflectmap.FieldsByColumn(t)
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
