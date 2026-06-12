package rowset

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/sqlkit-dev/sqlkit/internal/names"
)

// Scan copies rows into dest, streaming them from the source. Dest must be a
// pointer to a struct (first row only; further rows are not read), a slice of
// structs, or a slice of pointers to structs. Columns are matched by
// `sqlkit` tags or exported field names.
func (rs RowSet) Scan(dest any) error {
	value := reflect.ValueOf(dest)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return errNonNilPointer("scan")
	}

	elem := value.Elem()
	switch elem.Kind() {
	case reflect.Struct:
		return scanFirstRowIntoStruct(rs, elem)
	case reflect.Slice:
		return scanRowsIntoSlice(rs, elem)
	default:
		return fmt.Errorf("rowset: scan destination must point to a struct or slice")
	}
}

// scanFirstRowIntoStruct reads only the first row and stops the iteration,
// releasing the rest of the result set. With no rows, dest is left untouched.
func scanFirstRowIntoStruct(rs RowSet, dest reflect.Value) error {
	var scanErr error
	err := rs.iterate(func(row Row) bool {
		scanErr = scanRowIntoStruct(row, dest)
		return false
	})
	if err != nil {
		return err
	}
	return scanErr
}

func scanRowsIntoSlice(rs RowSet, dest reflect.Value) error {
	elemType := dest.Type().Elem()
	if elemType.Kind() == reflect.Pointer {
		elemType = elemType.Elem()
	}
	if elemType.Kind() != reflect.Struct {
		return fmt.Errorf("rowset: scan slice element must be a struct or pointer to struct")
	}

	mapped := reflect.MakeSlice(dest.Type(), 0, 8)
	index := 0
	var scanErr error
	err := rs.iterate(func(row Row) bool {
		item := reflect.New(elemType).Elem()
		if err := scanRowIntoStruct(row, item); err != nil {
			scanErr = fmt.Errorf("rowset: scan row %d: %w", index, err)
			return false
		}
		if dest.Type().Elem().Kind() == reflect.Pointer {
			mapped = reflect.Append(mapped, item.Addr())
		} else {
			mapped = reflect.Append(mapped, item)
		}
		index++
		return true
	})
	if err != nil {
		return err
	}
	if scanErr != nil {
		return scanErr
	}
	dest.Set(mapped)
	return nil
}

func errNonNilPointer(operation string) error {
	return fmt.Errorf("rowset: %s destination must be a non-nil pointer", operation)
}

func errStructPointer(operation string) error {
	return fmt.Errorf("rowset: %s destination must point to a struct", operation)
}

func scanRowIntoStruct(row Row, dest reflect.Value) error {
	typeOfDest := dest.Type()
	for i := 0; i < dest.NumField(); i++ {
		field := dest.Field(i)
		structField := typeOfDest.Field(i)
		if !field.CanSet() || structField.PkgPath != "" {
			continue
		}
		columnName := fieldColumnName(structField)
		if columnName == "-" {
			continue
		}
		value, ok := row.Get(columnName)
		if !ok {
			value, ok = row.Get(structField.Name)
		}
		if !ok {
			value, ok = row.Get(names.SnakeCase(structField.Name))
		}
		if !ok {
			continue
		}
		if err := assignValue(field, value); err != nil {
			return fmt.Errorf("field %s: %w", structField.Name, err)
		}
	}
	return nil
}

func fieldColumnName(field reflect.StructField) string {
	if tag, ok := field.Tag.Lookup("sqlkit"); ok {
		name := strings.Split(tag, ",")[0]
		if name != "" {
			return name
		}
	}
	return field.Name
}

func assignValue(dest reflect.Value, value any) error {
	// database/sql Scanner implementations decide for themselves how to
	// accept the value, including NULL (e.g. sql.NullString).
	if dest.CanAddr() {
		if scanner, ok := dest.Addr().Interface().(sql.Scanner); ok {
			return scanner.Scan(value)
		}
	}
	if value == nil {
		return nil
	}
	// Nullable destinations may be pointers: allocate and fill.
	if dest.Kind() == reflect.Pointer {
		elem := reflect.New(dest.Type().Elem())
		if err := assignValue(elem.Elem(), value); err != nil {
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
