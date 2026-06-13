package sqlkit

import (
	stdsql "database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/aita/sqlkit/internal/reflectmap"
	"github.com/aita/sqlkit/sql"
)

// ErrNoPrimaryKey is returned when a primary-key operation is attempted on a
// table whose binding declares no primary key.
var ErrNoPrimaryKey = errors.New("sqlkit: table has no primary key")

// ErrMissingPrimaryKey is returned when a row struct has no field mapping to a
// primary-key column, so its key cannot be read.
var ErrMissingPrimaryKey = errors.New("sqlkit: row struct has no field for a primary-key column")

// rowStruct dereferences row to its underlying struct value, the shape Scan and
// Insert map by field. It errors on a nil pointer or a non-struct.
func rowStruct(row any) (reflect.Value, error) {
	rv := reflect.ValueOf(row)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return reflect.Value{}, fmt.Errorf("sqlkit: row is a nil pointer")
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return reflect.Value{}, fmt.Errorf("sqlkit: row must be a struct, got %T", row)
	}
	return rv, nil
}

// PrimaryKeyValues extracts a row's primary-key column values, in the table's
// PrimaryKey declaration order, using the same column-to-field mapping as Scan
// and Insert (sqlkit tag, exact name, snake_case, embedded structs flattened).
// row may be a struct or a pointer to one. It is the bridge the uow package uses
// to identify a row from its binding. It returns ErrNoPrimaryKey when the table
// has no key and ErrMissingPrimaryKey when the struct omits a key column.
func PrimaryKeyValues(table Table, row any) ([]any, error) {
	pk := table.PrimaryKey()
	if len(pk) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoPrimaryKey, table.Info().QualifiedName())
	}
	rv, err := rowStruct(row)
	if err != nil {
		return nil, err
	}
	fields := reflectmap.FieldsByColumn(rv.Type())
	values := make([]any, len(pk))
	for i, col := range pk {
		path, ok := fields[strings.ToLower(col.ColumnName())]
		if !ok {
			return nil, fmt.Errorf("%w: %s.%s", ErrMissingPrimaryKey, table.Info().QualifiedName(), col.ColumnName())
		}
		values[i] = rv.FieldByIndex(path).Interface()
	}
	return values, nil
}

// ColumnValues maps a table's columns to the values found in row, in Columns
// declaration order, skipping any column the struct provides no field for. It
// uses the same column-to-field mapping as Scan and Insert. row may be a struct
// or a pointer to one. It backs the uow package's UPDATE assignment building.
func ColumnValues(table Table, row any) (names []string, values []any, err error) {
	rv, err := rowStruct(row)
	if err != nil {
		return nil, nil, err
	}
	fields := reflectmap.FieldsByColumn(rv.Type())
	for _, col := range table.Columns() {
		name := col.ColumnName()
		path, ok := fields[strings.ToLower(name)]
		if !ok {
			continue
		}
		names = append(names, name)
		values = append(values, rv.FieldByIndex(path).Interface())
	}
	return names, values, nil
}

// SetColumns assigns values to the fields of *rowPtr that map to the named
// columns, using the same column-to-field mapping as Scan and Insert. rowPtr
// must be a non-nil pointer to a struct so the fields are settable; columns the
// struct has no field for are skipped. It is the write-back counterpart of
// ColumnValues — the uow package uses it to store a database-generated primary
// key recovered from Result.LastInsertId back into the caller's row.
func SetColumns(rowPtr any, names []string, values []any) error {
	if len(names) != len(values) {
		return fmt.Errorf("sqlkit: SetColumns got %d names for %d values", len(names), len(values))
	}
	rv := reflect.ValueOf(rowPtr)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("sqlkit: SetColumns destination must be a non-nil pointer to a struct, got %T", rowPtr)
	}
	rv = rv.Elem()
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("sqlkit: SetColumns destination must point to a struct, got %T", rowPtr)
	}
	fields := reflectmap.FieldsByColumn(rv.Type())
	for i, name := range names {
		path, ok := fields[strings.ToLower(name)]
		if !ok {
			continue
		}
		field := rv.FieldByIndex(path)
		val := reflect.ValueOf(values[i])
		if !val.IsValid() {
			continue
		}
		if !val.Type().AssignableTo(field.Type()) {
			if !val.Type().ConvertibleTo(field.Type()) {
				return fmt.Errorf("sqlkit: SetColumns cannot assign %s to field for column %q (%s)", val.Type(), name, field.Type())
			}
			val = val.Convert(field.Type())
		}
		field.Set(val)
	}
	return nil
}

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
