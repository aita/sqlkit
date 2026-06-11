package rowset

import "reflect"

// Row behaves as a read-only container for one result row.
type Row struct {
	values map[string]any
}

func newRow(values map[string]any) Row { return Row{values: values} }

// Get returns a field value and whether it is present.
func (r Row) Get(name string) (any, bool) {
	value, ok := r.values[name]
	return value, ok
}

// Scan copies this row into dest using sqlkit/db/json tags or exported field names.
func (r Row) Scan(dest any) error {
	value := reflect.ValueOf(dest)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return errNonNilPointer("scan")
	}
	if value.Elem().Kind() != reflect.Struct {
		return errStructPointer("scan")
	}
	return scanRowIntoStruct(r, value.Elem())
}
