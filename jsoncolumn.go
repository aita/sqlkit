package sqlkit

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"reflect"
)

// A field tagged `sqlkit:",json"` (or `,jsonb`) maps to a JSON/JSONB column by
// its JSON encoding: a struct, map, or slice field marshals to JSON on write
// and unmarshals from the column's text or bytes on read. The wrappers below
// adapt such a field to database/sql's driver.Valuer / sql.Scanner contracts so
// the Scan, Insert, and Update builder paths handle it without a hand-written
// Valuer/Scanner on the field type.

// jsonValue wraps a field value so it is written as its JSON encoding. A nil
// pointer, map, or slice writes SQL NULL rather than the JSON literal null.
type jsonValue struct{ v any }

func (j jsonValue) Value() (driver.Value, error) {
	if j.v == nil {
		return nil, nil
	}
	rv := reflect.ValueOf(j.v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice:
		if rv.IsNil() {
			return nil, nil
		}
	}
	b, err := json.Marshal(j.v)
	if err != nil {
		return nil, fmt.Errorf("sqlkit: marshal JSON column value: %w", err)
	}
	return b, nil
}

// jsonScanner wraps the address of a field so a JSON/JSONB column value (text or
// bytes) decodes into it; a SQL NULL leaves the field at its zero value.
type jsonScanner struct{ dst any } // dst is a pointer to the destination field

func (j jsonScanner) Scan(src any) error {
	var data []byte
	switch s := src.(type) {
	case nil:
		reflect.ValueOf(j.dst).Elem().SetZero()
		return nil
	case []byte:
		data = s
	case string:
		data = []byte(s)
	default:
		return fmt.Errorf("sqlkit: cannot scan %T into a JSON column field", src)
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, j.dst); err != nil {
		return fmt.Errorf("sqlkit: unmarshal JSON column value: %w", err)
	}
	return nil
}
