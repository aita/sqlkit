package rowset

import (
	"fmt"
	"reflect"
	"strings"
)

// Scan copies rows into dest. Dest must be a pointer to a struct, a slice of
// structs, or a slice of pointers to structs. Columns are matched by
// sqlkit/db/json tags or exported field names.
func (rs RowSet) Scan(dest any) error {
	value := reflect.ValueOf(dest)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return errNonNilPointer("scan")
	}

	elem := value.Elem()
	switch elem.Kind() {
	case reflect.Struct:
		rows, err := rs.collectRows()
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		return scanRowIntoStruct(rows[0], elem)
	case reflect.Slice:
		return scanRowsIntoSlice(rs, elem)
	default:
		return fmt.Errorf("rowset: scan destination must point to a struct or slice")
	}
}

func scanRowsIntoSlice(rs RowSet, dest reflect.Value) error {
	elemType := dest.Type().Elem()
	if elemType.Kind() == reflect.Pointer {
		elemType = elemType.Elem()
	}
	if elemType.Kind() != reflect.Struct {
		return fmt.Errorf("rowset: scan slice element must be a struct or pointer to struct")
	}

	rows, err := rs.collectRows()
	if err != nil {
		return err
	}
	mapped := reflect.MakeSlice(dest.Type(), 0, len(rows))
	for i, row := range rows {
		item := reflect.New(elemType).Elem()
		if err := scanRowIntoStruct(row, item); err != nil {
			return fmt.Errorf("rowset: scan row %d: %w", i, err)
		}
		if dest.Type().Elem().Kind() == reflect.Pointer {
			mapped = reflect.Append(mapped, item.Addr())
		} else {
			mapped = reflect.Append(mapped, item)
		}
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
			if !ok {
				continue
			}
		}
		if err := assignValue(field, value); err != nil {
			return fmt.Errorf("field %s: %w", structField.Name, err)
		}
	}
	return nil
}

func fieldColumnName(field reflect.StructField) string {
	for _, tagName := range []string{"sqlkit", "db", "json"} {
		if tag, ok := field.Tag.Lookup(tagName); ok {
			name := strings.Split(tag, ",")[0]
			if name != "" {
				return name
			}
		}
	}
	return field.Name
}

func assignValue(dest reflect.Value, value any) error {
	if value == nil {
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
